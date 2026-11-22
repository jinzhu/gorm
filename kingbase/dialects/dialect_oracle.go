package kingbase

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jinzhu/gorm"
)

const DialectKingbaseOracle = "kingbase-oracle"

func registerKingbaseModeDialects() {
	gorm.RegisterDialect(DialectKingbaseMySQL, &kingbaseMySQLDialect{})
	gorm.RegisterDialect(DialectKingbaseOracle, &kingbaseOracleDialect{})
}

func kingbaseDialectMode(dialect gorm.Dialect) (KbMode, bool) {
	if dialect == nil {
		return "", false
	}
	switch dialect.GetName() {
	case DialectKingbaseMySQL:
		return KbModeMySQL, true
	case DialectKingbaseOracle:
		return KbModeOracle, true
	default:
		return "", false
	}
}

// kingbaseOracleDialect is intentionally self-contained. Oracle-mode SQL must
// not inherit behavior from another GORM dialect.
type kingbaseOracleDialect struct {
	db gorm.SQLCommon
}

var _ gorm.Dialect = (*kingbaseOracleDialect)(nil)

func (*kingbaseOracleDialect) GetName() string { return DialectKingbaseOracle }

func (d *kingbaseOracleDialect) SetDB(db gorm.SQLCommon) { d.db = db }

// gokb normalizes question-mark placeholders to the wire-level placeholders
// expected by KingbaseES in every database mode.
func (*kingbaseOracleDialect) BindVar(int) string { return "?" }

func (*kingbaseOracleDialect) Quote(key string) string {
	return quoteOracleIdentifier(key)
}

func (d *kingbaseOracleDialect) DataTypeOf(field *gorm.StructField) string {
	return d.rewriteOracleType(field)
}

func (d *kingbaseOracleDialect) HasIndex(tableName, indexName string) bool {
	return d.hasIndexOracleCompat(tableName, indexName)
}

func (d *kingbaseOracleDialect) HasForeignKey(tableName, foreignKeyName string) bool {
	return d.hasForeignKeyOracleCompat(tableName, foreignKeyName)
}

func (d *kingbaseOracleDialect) RemoveIndex(tableName, indexName string) error {
	if d.db == nil {
		return sql.ErrConnDone
	}

	schema, _ := splitOracleIdentifierPath(tableName)
	indexSchema, index := splitOracleIdentifierPath(indexName)
	if indexSchema == "" {
		indexSchema = schema
	}
	if indexSchema != "" {
		index = quoteOracleIdentifier(indexSchema) + "." + quoteOracleIdentifier(index)
	} else {
		index = quoteOracleIdentifier(index)
	}
	_, err := d.db.Exec("DROP INDEX " + index)
	return err
}

func (d *kingbaseOracleDialect) HasTable(tableName string) bool {
	return d.hasTableOracleCompat(tableName)
}

func (d *kingbaseOracleDialect) HasColumn(tableName, columnName string) bool {
	return d.hasColumnOracleCompat(tableName, columnName)
}

func (d *kingbaseOracleDialect) ModifyColumn(tableName, columnName, typ string) error {
	return d.modifyColumnOracleCompat(tableName, columnName, typ)
}

func (*kingbaseOracleDialect) LimitAndOffsetSQL(limit, offset interface{}) (string, error) {
	return limitAndOffsetOracleCompat(limit, offset)
}

func (*kingbaseOracleDialect) SelectFromDummyTable() string { return "FROM DUAL" }

func (*kingbaseOracleDialect) LastInsertIDOutputInterstitial(string, string, []string) string {
	return ""
}

func (*kingbaseOracleDialect) LastInsertIDReturningSuffix(tableName, columnName string) string {
	return fmt.Sprintf("RETURNING %s.%s", tableName, columnName)
}

func (*kingbaseOracleDialect) DefaultValueStr() string { return "DEFAULT VALUES" }

func (d *kingbaseOracleDialect) BuildKeyName(kind, tableName string, fields ...string) string {
	name := (gorm.DefaultForeignKeyNamer{}).BuildKeyName(kind, tableName, fields...)
	if len(name) <= 63 {
		return name
	}
	limit := 63
	if d.db != nil {
		if err := d.db.QueryRow("SHOW max_identifier_length").Scan(&limit); err != nil {
			d.reportMetadataError(err)
			limit = 63
		} else if limit <= 0 {
			d.reportMetadataError(fmt.Errorf("invalid max_identifier_length: %d", limit))
			limit = 63
		}
	}
	if len(name) <= limit {
		return name
	}
	// Server truncation would lose the distinguishing suffix and prevent
	// subsequent HasForeignKey/RemoveForeignKey from using the same name.
	digest := sha1.Sum([]byte(name))
	suffix := hex.EncodeToString(digest[:])
	if limit <= len(suffix) {
		return suffix[:limit]
	}
	prefix := limit - len(suffix) - 1
	for prefix > 0 && !utf8.RuneStart(name[prefix]) {
		prefix--
	}
	return name[:prefix] + "_" + suffix
}

func (*kingbaseOracleDialect) NormalizeIndexAndColumn(indexName, columnName string) (string, string) {
	return indexName, columnName
}

func (d *kingbaseOracleDialect) CurrentDatabase() string { return d.currentSchema() }

func quoteOracleIdentifier(identifier string) string {
	identifier = cleanIdentifier(identifier)
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quoteOracleIdentifierPath(identifier string) string {
	schema, name := splitOracleIdentifierPath(identifier)
	if schema == "" {
		return quoteOracleIdentifier(name)
	}
	return quoteOracleIdentifier(schema) + "." + quoteOracleIdentifier(name)
}

// A dot inside a quoted identifier belongs to the name, not the schema path.
func splitOracleIdentifierPath(path string) (schema, name string) {
	path = strings.TrimSpace(path)
	quoted := false
	for i := 0; i < len(path); i++ {
		if path[i] == '"' {
			if quoted && i+1 < len(path) && path[i+1] == '"' {
				i++
			} else {
				quoted = !quoted
			}
		} else if path[i] == '.' && !quoted {
			return cleanIdentifier(path[:i]), cleanIdentifier(path[i+1:])
		}
	}
	return "", cleanIdentifier(path)
}

func (d *kingbaseOracleDialect) modifyColumnOracleCompat(tableName, columnName, typ string) error {
	if d.db == nil {
		return sql.ErrConnDone
	}
	_, err := d.db.Exec(fmt.Sprintf(
		"ALTER TABLE %s MODIFY %s %s",
		quoteOracleIdentifierPath(tableName),
		quoteOracleIdentifier(columnName),
		typ,
	))
	return err
}

func limitAndOffsetOracleCompat(limit, offset interface{}) (string, error) {
	var (
		parsedLimit  int64 = -1
		parsedOffset int64 = -1
	)
	if limit != nil {
		value, err := strconv.ParseInt(fmt.Sprint(limit), 0, 0)
		if err != nil {
			return "", err
		}
		parsedLimit = value
	}
	if offset != nil {
		value, err := strconv.ParseInt(fmt.Sprint(offset), 0, 0)
		if err != nil {
			return "", err
		}
		parsedOffset = value
	}

	if parsedOffset >= 0 {
		if parsedLimit >= 0 {
			return fmt.Sprintf(" OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", parsedOffset, parsedLimit), nil
		}
		return fmt.Sprintf(" OFFSET %d ROWS", parsedOffset), nil
	}
	if parsedLimit >= 0 {
		return fmt.Sprintf(" FETCH FIRST %d ROWS ONLY", parsedLimit), nil
	}
	return "", nil
}

// rewriteOracleType maps common GORM field definitions to Kingbase Oracle-mode
// types while preserving tags such as not null, unique and default.
func (d *kingbaseOracleDialect) rewriteOracleType(f *gorm.StructField) string {
	dataValue, sqlType, size, additionalType := gorm.ParseFieldStructForDialect(f, d)

	if sqlType != "" {
		sqlType = rewriteOracleExplicitType(sqlType)
	} else {
		switch dataValue.Kind() {
		case reflect.Bool:
			sqlType = "boolean"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			bits := dataValue.Type().Bits()
			unsigned := dataValue.Kind() >= reflect.Uint && dataValue.Kind() <= reflect.Uintptr
			wide := bits > 32 || (unsigned && bits == 32)
			base := "integer"
			if wide {
				base = "bigint"
			}
			if kingbaseFieldCanAutoIncrement(f) {
				f.TagSettingsSet("AUTO_INCREMENT", "AUTO_INCREMENT")
				sqlType = base + " generated by default as identity"
			} else if dataValue.Kind() == reflect.Uint64 && !f.IsForeignKey {
				sqlType = "number(20)"
			} else {
				// Kingbase cannot create a foreign key between NUMBER and a
				// BIGINT/INTEGER identity. Keep the same native base even when
				// a relation is added later with AddForeignKey rather than a tag.
				sqlType = base
			}
		case reflect.Float32:
			sqlType = "binary_float"
		case reflect.Float64:
			sqlType = "binary_double"
		case reflect.String:
			if _, ok := f.TagSettingsGet("SIZE"); !ok {
				size = 0
			}
			// Preserve an explicit length and let Kingbase report an
			// out-of-range VARCHAR2 value instead of silently using CLOB.
			if size > 0 {
				sqlType = fmt.Sprintf("varchar2(%d)", size)
			} else {
				sqlType = "clob"
			}
		case reflect.Struct:
			if _, ok := dataValue.Interface().(time.Time); ok {
				sqlType = "timestamp with time zone"
			}
		default:
			if gorm.IsByteArrayOrSlice(dataValue) {
				sqlType = "blob"
			}
		}
	}

	if sqlType == "" {
		panic(fmt.Sprintf("invalid sql type %s (%s) for kingbase oracle", dataValue.Type().Name(), dataValue.Kind().String()))
	}

	// ParseFieldStructForDialect appends the COMMENT tag last. Remove that
	// exact suffix rather than scanning DEFAULT expressions for the word.
	if comment, ok := f.TagSettingsGet("COMMENT"); ok {
		additionalType = strings.TrimSpace(strings.TrimSuffix(additionalType, "COMMENT "+comment))
	}
	if strings.TrimSpace(additionalType) == "" {
		return sqlType
	}
	return fmt.Sprintf("%v %v", sqlType, additionalType)
}

func rewriteOracleExplicitType(sqlType string) string {
	typ := strings.TrimSpace(sqlType)
	if at := oracleTimestampTZ.FindStringIndex(typ); at != nil {
		precision := typ[len("timestamptz"):at[1]]
		return "timestamp" + precision + " with time zone" + typ[at[1]:]
	}
	if parts := oracleFloatScale.FindStringSubmatch(typ); parts != nil {
		return fmt.Sprintf("number(%s,%s)%s", parts[1], parts[2], parts[3])
	}
	if parts := oracleYearType.FindStringSubmatch(typ); parts != nil {
		return "number(4)" + parts[1]
	}

	for _, repl := range []struct {
		from string
		to   string
	}{
		{"bigserial", "bigint generated by default as identity"},
		{"serial", "integer generated by default as identity"},
		{"varchar2", "varchar2"},
		{"nvarchar2", "nvarchar2"},
		{"nvarchar", "nvarchar2"},
		{"varchar", "varchar2"},
		{"character varying", "varchar2"},
		{"longtext", "clob"},
		{"mediumtext", "clob"},
		{"tinytext", "clob"},
		{"text", "clob"},
		{"longblob", "blob"},
		{"mediumblob", "blob"},
		{"tinyblob", "blob"},
		{"bytea", "blob"},
		{"varbinary", "raw"},
		{"binary", "raw"},
		{"numeric", "number"},
		{"decimal", "number"},
		{"datetime", "timestamp"},
		{"timestamptz", "timestamp with time zone"},
		{"timestamp with time zone", "timestamp with time zone"},
		{"double precision", "binary_double"},
		{"double", "binary_double"},
		{"real", "binary_float"},
		{"bigint unsigned", "number(20)"},
		{"integer unsigned", "number(10)"},
		{"int unsigned", "number(10)"},
		{"mediumint unsigned", "number(8)"},
		{"smallint unsigned", "number(5)"},
		{"tinyint unsigned", "number(3)"},
		{"tinyint", "number(3)"},
		{"bool", "boolean"},
	} {
		lower := strings.ToLower(typ)
		if lower == repl.from {
			return repl.to
		}
		if strings.HasPrefix(lower, repl.from+"(") {
			// Only aliases with the same parameter grammar may keep modifiers.
			switch repl.from {
			case "varchar2", "nvarchar2", "nvarchar", "varchar", "character varying", "varbinary", "binary", "numeric", "decimal", "datetime", "timestamptz":
			default:
				return typ
			}
			return repl.to + typ[len(repl.from):]
		}
		if strings.HasPrefix(lower, repl.from+" ") {
			return repl.to + typ[len(repl.from):]
		}
	}

	return typ
}

var oracleTimestampTZ = regexp.MustCompile(`(?i)^timestamptz\s*\(\s*\d+\s*\)`)
var oracleFloatScale = regexp.MustCompile(`(?i)^float\s*\(\s*(\d+)\s*,\s*(\d+)\s*\)(\s.*)?$`)
var oracleYearType = regexp.MustCompile(`(?i)^year(?:\s*\(\s*4\s*\))?(\s.*)?$`)

func kingbaseFieldCanAutoIncrement(field *gorm.StructField) bool {
	if value, ok := field.TagSettingsGet("AUTO_INCREMENT"); ok {
		return strings.ToLower(value) != "false"
	}
	return field.IsPrimaryKey
}
