package kingbase

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jinzhu/gorm"
)

const DialectKingbaseMySQL = "kingbase-mysql"

// KingbaseDialect proxies a real GORM dialect and can override behavior by mode.
type KingbaseDialect struct {
	mode            KbMode
	db              gorm.SQLCommon
	raw             gorm.Dialect
	identifierLimit *mysqlIdentifierLimit
}

func (k *KingbaseDialect) ensureRaw() {
	if k.raw != nil {
		return
	}
	rawName := "mysql"
	prototype, ok := gorm.GetDialect(rawName)
	if !ok {
		panic(fmt.Sprintf("raw dialect not registered: %s", rawName))
	}

	// Registered GORM dialects are prototypes. Clone the prototype so SetDB on
	// one Kingbase connection cannot overwrite another connection's DB handle.
	typ := reflect.TypeOf(prototype)
	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	raw, ok := reflect.New(typ).Interface().(gorm.Dialect)
	if !ok {
		panic(fmt.Sprintf("raw dialect does not implement gorm.Dialect: %s", rawName))
	}
	k.raw = raw
}

func (*KingbaseDialect) GetName() string { return DialectKingbaseMySQL }

func (k *KingbaseDialect) SetDB(db gorm.SQLCommon) {
	k.db = db
	k.identifierLimit = &mysqlIdentifierLimit{}
	k.ensureRaw()
	k.raw.SetDB(db)
}

func (k *KingbaseDialect) BindVar(i int) string { k.ensureRaw(); return k.raw.BindVar(i) }

func (k *KingbaseDialect) Quote(key string) string {
	return quoteMySQLIdentifier(key)
}

func (k *KingbaseDialect) DataTypeOf(field *gorm.StructField) string {
	return k.rewriteMysqlType(field)
}

func (k *KingbaseDialect) HasIndex(tableName string, indexName string) bool {
	return k.hasIndexMySQLCompat(tableName, indexName)
}

func (k *KingbaseDialect) HasForeignKey(tableName string, foreignKeyName string) bool {
	return k.hasForeignKeyMySQLCompat(tableName, foreignKeyName)
}

func (k *KingbaseDialect) RemoveIndex(tableName string, indexName string) error {
	if k.db == nil {
		return sql.ErrConnDone
	}
	return k.removeIndexMySQLCompat(tableName, indexName)
}

func (k *KingbaseDialect) HasTable(tableName string) bool {
	return k.hasTableMySQLCompat(tableName)
}

func (k *KingbaseDialect) HasColumn(tableName string, columnName string) bool {
	return k.hasColumnMySQLCompat(tableName, columnName)
}

func (k *KingbaseDialect) ModifyColumn(tableName string, columnName string, typ string) error {
	if k.db == nil {
		return sql.ErrConnDone
	}
	query := "ALTER TABLE " + tableName + " MODIFY COLUMN " + columnName + " " + strings.TrimSpace(typ)
	_, err := k.db.Exec(query)
	return err
}

func (k *KingbaseDialect) LimitAndOffsetSQL(limit, offset interface{}) (string, error) {
	return limitAndOffsetMySQLCompat(limit, offset)
}

func (k *KingbaseDialect) SelectFromDummyTable() string {
	k.ensureRaw()
	return k.raw.SelectFromDummyTable()
}

func (k *KingbaseDialect) LastInsertIDOutputInterstitial(tableName, columnName string, columns []string) string {
	k.ensureRaw()
	return k.raw.LastInsertIDOutputInterstitial(tableName, columnName, columns)
}

func (k *KingbaseDialect) LastInsertIDReturningSuffix(tableName, columnName string) string {
	k.ensureRaw()
	return k.raw.LastInsertIDReturningSuffix(tableName, columnName)
}

func (k *KingbaseDialect) DefaultValueStr() string {
	k.ensureRaw()
	return k.raw.DefaultValueStr()
}

func (k *KingbaseDialect) NormalizeIndexAndColumn(indexName, columnName string) (string, string) {
	k.ensureRaw()
	index, column := k.raw.NormalizeIndexAndColumn(indexName, columnName)
	schema, name := splitSchemaTable(index)
	index = quoteMySQLIdentifier(name)
	if schema != "" {
		index = quoteMySQLIdentifier(schema) + "." + index
	}
	return index, column
}

func (k *KingbaseDialect) CurrentDatabase() string {
	if k.db == nil {
		return ""
	}
	if name := k.currentSchema(); name != "" {
		return name
	}
	k.ensureRaw()
	return k.raw.CurrentDatabase()
}

type kingbaseMySQLDialect struct{ KingbaseDialect }

func (d *kingbaseMySQLDialect) SetDB(db gorm.SQLCommon) {
	d.mode = KbModeMySQL
	d.KingbaseDialect.SetDB(db)
}

func limitAndOffsetMySQLCompat(limit, offset interface{}) (string, error) {
	var query string
	for _, item := range []struct {
		keyword string
		value   interface{}
	}{{"LIMIT", limit}, {"OFFSET", offset}} {
		if item.value == nil {
			continue
		}
		value, err := strconv.ParseInt(fmt.Sprint(item.value), 0, 0)
		if err != nil {
			return "", err
		}
		if value >= 0 {
			query += fmt.Sprintf(" %s %d", item.keyword, value)
		}
	}
	return query, nil
}

// Kingbase drops an index by its (optionally schema-qualified) name. MySQL's
// ON table suffix is not part of this syntax. Inherit the table's schema when
// the caller supplies an unqualified index name.
func (k *KingbaseDialect) removeIndexMySQLCompat(tableName, indexName string) error {
	schema, index := splitSchemaTable(indexName)
	if schema == "" {
		schema, _ = splitSchemaTable(tableName)
	}
	quoted := quoteMySQLIdentifier(index)
	if schema != "" {
		quoted = quoteMySQLIdentifier(schema) + "." + quoted
	}
	_, err := k.db.Exec("DROP INDEX " + quoted)
	return err
}

func (k *KingbaseDialect) BuildKeyName(kind, tableName string, fields ...string) string {
	k.ensureRaw()
	name := k.raw.BuildKeyName(kind, tableName, fields...)
	if len(name) <= 63 {
		return name
	}
	return shortenMySQLKeyName(name, k.mysqlIdentifierBytes())
}

func quoteMySQLIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

type mysqlIdentifierLimit struct {
	once  sync.Once
	bytes int
}

// Read the actual instance limit only when a generated name needs it. The
// Dialect interface cannot return an error here; observers still receive any
// configuration-query failure. Disconnected SQL generation uses 63 bytes.
func (k *KingbaseDialect) mysqlIdentifierBytes() int {
	if k.db == nil || k.identifierLimit == nil {
		return 63
	}
	state := k.identifierLimit
	state.once.Do(func() {
		state.bytes = 63
		var limit int
		if err := k.db.QueryRow("SHOW max_identifier_length").Scan(&limit); err != nil {
			k.reportError(err)
		} else if limit <= 0 {
			k.reportError(fmt.Errorf("invalid max_identifier_length: %d", limit))
		} else {
			state.bytes = limit
		}
	})
	return state.bytes
}

func shortenMySQLKeyName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	// Preserve a hash of the whole name, including the part beyond the limit.
	// Cutting only the prefix would make similarly named objects collide.
	digest := sha1.Sum([]byte(name))
	suffix := hex.EncodeToString(digest[:])
	if limit <= len(suffix) {
		return suffix[:limit]
	}
	prefixBytes := limit - len(suffix) - 1
	for prefixBytes > 0 && !utf8.RuneStart(name[prefixBytes]) {
		prefixBytes--
	}
	return name[:prefixBytes] + "_" + suffix
}
