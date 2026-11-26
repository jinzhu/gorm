package kingbase

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jinzhu/gorm"
)

const dialectKingbaseOracle = "kingbase-oracle"

func registerKingbaseModeDialects() {
	// 模式方言只作为统一方言的内部委托注册，应用不应直接选择这些名称。
	gorm.RegisterDialect(dialectKingbaseMySQL, &kingbaseMySQLDialect{})
	gorm.RegisterDialect(dialectKingbaseOracle, &kingbaseOracleDialect{})
}

// kingbaseDialectMode 只识别包内注册的两个模式方言，用于限制专用回调的作用范围。
func kingbaseDialectMode(dialect gorm.Dialect) (KbMode, bool) {
	if dialect == nil {
		return "", false
	}
	switch dialect.GetName() {
	case dialectKingbaseMySQL:
		return KbModeMySQL, true
	case dialectKingbaseOracle:
		return KbModeOracle, true
	default:
		return "", false
	}
}

// kingbaseOracleDialect 是独立实现，Oracle 模式 SQL 不继承其他 GORM 方言的隐含行为。
type kingbaseOracleDialect struct {
	db gorm.SQLCommon
}

var _ gorm.Dialect = (*kingbaseOracleDialect)(nil)

// 下列方法完整实现 GORM V1 Dialect 接口，Oracle 模式不借用其他数据库方言的 SQL。
func (*kingbaseOracleDialect) GetName() string { return dialectKingbaseOracle }

func (d *kingbaseOracleDialect) SetDB(db gorm.SQLCommon) { d.db = db }

// Oracle 兼容 SQL 使用带编号的冒号绑定变量；gokb 会将其归一化为 KingbaseES 协议层占位符。
func (*kingbaseOracleDialect) BindVar(i int) string { return fmt.Sprintf(":%d", i) }

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
	// 索引未显式限定 schema 时继承表 schema；若索引已限定则以索引自身为准。
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
	// 名称未超过默认 63 字节时不访问数据库；只有长名称才查询实例实际限制。
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
	// 由服务器直接截断会丢失区分后缀，导致后续 HasForeignKey/RemoveForeignKey
	// 无法复用同一名称，因此在客户端用完整名称哈希生成稳定后缀。
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

// quoteOracleIdentifier 为单个 Oracle 标识符增加双引号，并通过双写转义内部引号。
func quoteOracleIdentifier(identifier string) string {
	identifier = cleanIdentifier(identifier)
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// quoteOracleIdentifierPath 分别引用 schema 和对象名，避免把限定路径当作单一标识符。
func quoteOracleIdentifierPath(identifier string) string {
	schema, name := splitOracleIdentifierPath(identifier)
	if schema == "" {
		return quoteOracleIdentifier(name)
	}
	return quoteOracleIdentifier(schema) + "." + quoteOracleIdentifier(name)
}

// 引号内的点属于标识符名称，不是 schema 路径分隔符。
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

// modifyColumnOracleCompat 生成 Oracle 兼容的 ALTER TABLE ... MODIFY 语法。
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
	// 先统一解析 GORM 传入的整数或字符串值，再按 Oracle 兼容语法组合分页片段。
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

// rewriteOracleType 将常见 GORM 字段定义映射为 KingbaseES Oracle 模式类型，
// 同时保留 not null、unique、default 等附加属性。
func (d *kingbaseOracleDialect) rewriteOracleType(f *gorm.StructField) string {
	dataValue, sqlType, size, additionalType := gorm.ParseFieldStructForDialect(f, d)

	if sqlType != "" {
		// 用户显式指定 type 时只做兼容表中声明的确定性改写。
		sqlType = rewriteOracleExplicitType(sqlType)
	} else {
		// 未显式指定时依据 Go 类型、字段角色和 GORM 标签推导物理类型。
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
				// KingbaseES 无法在 NUMBER 与 BIGINT/INTEGER identity 之间创建外键。
				// 即使关系稍后通过 AddForeignKey 添加，也要让外键字段保持相同原生基类型。
				sqlType = base
			}
		case reflect.Float32:
			sqlType = "binary_float"
		case reflect.Float64:
			sqlType = "binary_double"
		case reflect.String:
			// 未声明 SIZE 时使用 text；有界且小于 KingbaseES varchar2 边界时才生成 varchar2(n)。
			if _, ok := f.TagSettingsGet("SIZE"); !ok {
				size = 0
			}
			if size > 0 && size < 65532 {
				sqlType = fmt.Sprintf("varchar2(%d)", size)
			} else {
				sqlType = "text"
			}
		case reflect.Struct:
			if _, ok := dataValue.Interface().(time.Time); ok {
				sqlType = "timestamp with time zone"
			}
		default:
			if gorm.IsByteArrayOrSlice(dataValue) {
				// 普通二进制值使用 bytea，只有可明确识别的 UUID/GUID 和 RawMessage 才特化。
				sqlType = "bytea"
				if isOracleUUID(dataValue) {
					sqlType = "uuid"
				}
				if isOracleJSON(dataValue) {
					sqlType = "jsonb"
				}
			}
		}
	}

	if sqlType == "" {
		panic(fmt.Sprintf("invalid sql type %s (%s) for kingbase oracle", dataValue.Type().Name(), dataValue.Kind().String()))
	}

	// ParseFieldStructForDialect 会把 COMMENT 标签追加在末尾。这里只删除精确的末尾片段，
	// 不在 DEFAULT 表达式中模糊查找 COMMENT 字样。
	if comment, ok := f.TagSettingsGet("COMMENT"); ok {
		additionalType = strings.TrimSpace(strings.TrimSuffix(additionalType, "COMMENT "+comment))
	}
	if strings.TrimSpace(additionalType) == "" {
		return sqlType
	}
	return fmt.Sprintf("%v %v", sqlType, additionalType)
}

// isOracleUUID 仅把名称明确为 UUID/GUID 的 16 字节数组识别为 uuid，避免误判普通数组。
func isOracleUUID(value reflect.Value) bool {
	if value.Kind() != reflect.Array || value.Type().Len() != 16 {
		return false
	}
	typeName := strings.ToLower(value.Type().Name())
	return typeName == "uuid" || typeName == "guid"
}

// isOracleJSON 通过 json.RawMessage 的具体类型识别 JSON 二进制载体。
func isOracleJSON(value reflect.Value) bool {
	_, ok := value.Interface().(json.RawMessage)
	return ok
}

func rewriteOracleExplicitType(sqlType string) string {
	// 显式 type 标签先拆出基础类型和修饰部分，仅对确认兼容的类型别名进行改写，
	// 未列出的定义原样保留，避免适配层擅自改变用户 SQL。
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
		{"text", "text"},
		{"longblob", "blob"},
		{"mediumblob", "blob"},
		{"tinyblob", "blob"},
		{"bytea", "bytea"},
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
			// 只有参数语法一致的别名才能保留长度、精度等修饰参数。
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

// 下列模式只处理需要重新排列参数或关键字的显式类型，普通别名由映射表处理。
var oracleTimestampTZ = regexp.MustCompile(`(?i)^timestamptz\s*\(\s*\d+\s*\)`)
var oracleFloatScale = regexp.MustCompile(`(?i)^float\s*\(\s*(\d+)\s*,\s*(\d+)\s*\)(\s.*)?$`)
var oracleYearType = regexp.MustCompile(`(?i)^year(?:\s*\(\s*4\s*\))?(\s.*)?$`)

func kingbaseFieldCanAutoIncrement(field *gorm.StructField) bool {
	// 显式关闭 AUTO_INCREMENT 时优先尊重用户设置；其余情况沿用 GORM 对主键整数的判断。
	if value, ok := field.TagSettingsGet("AUTO_INCREMENT"); ok {
		return strings.ToLower(value) != "false"
	}
	return field.IsPrimaryKey
}
