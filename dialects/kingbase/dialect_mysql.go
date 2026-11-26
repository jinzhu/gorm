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

const dialectKingbaseMySQL = "kingbase-mysql"

// KingbaseDialect 包装一个真实的 GORM 方言，并在 KingbaseES MySQL 模式下覆盖差异行为。
type KingbaseDialect struct {
	// mode 记录已选择的兼容模式；db 是当前连接；raw 是被复用的原生 MySQL 方言克隆。
	mode KbMode
	db   gorm.SQLCommon
	raw  gorm.Dialect
	// identifierLimit 按连接缓存 max_identifier_length，避免每次生成名称都访问数据库。
	identifierLimit *mysqlIdentifierLimit
}

// ensureRaw 延迟创建原生 MySQL 方言克隆，只复用 GORM 已有的通用 MySQL 行为。
func (k *KingbaseDialect) ensureRaw() {
	if k.raw != nil {
		return
	}
	rawName := "mysql"
	prototype, ok := gorm.GetDialect(rawName)
	if !ok {
		panic(fmt.Sprintf("raw dialect not registered: %s", rawName))
	}

	// GORM 注册的是方言原型。这里克隆原型，避免一个连接执行 SetDB 时覆盖其他连接的句柄。
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

// 下列方法构成 GORM V1 Dialect 接口：KingbaseES 存在差异的部分在本类型内实现，
// 语义一致的部分委托给当前连接独占的原生 MySQL 方言克隆。
func (*KingbaseDialect) GetName() string { return dialectKingbaseMySQL }

func (k *KingbaseDialect) SetDB(db gorm.SQLCommon) {
	// 每个新连接使用独立的标识符长度缓存，并把同一连接传给原生方言克隆。
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
	// GORM 传入的表名和列名已经按当前方言引用，此处只拼接 R3 支持的 MODIFY COLUMN 语法。
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
	// 先复用 GORM MySQL 对前缀长度等列定义的归一化，再单独处理 schema 限定索引名。
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
	// KingbaseES 优先使用 current_schema()；未取得名称时保留上游返回路径，
	// 查询错误仍已由元数据观察器独立记录，不会在此静默丢失。
	if name := k.currentSchema(); name != "" {
		return name
	}
	k.ensureRaw()
	return k.raw.CurrentDatabase()
}

type kingbaseMySQLDialect struct{ KingbaseDialect }

// SetDB 固定内部模式后再绑定连接，供统一方言安全识别当前委托类型。
func (d *kingbaseMySQLDialect) SetDB(db gorm.SQLCommon) {
	d.mode = KbModeMySQL
	d.KingbaseDialect.SetDB(db)
}

func limitAndOffsetMySQLCompat(limit, offset interface{}) (string, error) {
	// 沿用 MySQL 分页语法；只有 OFFSET 时使用最大无符号值作为 LIMIT，
	// 这是 MySQL 兼容语法表达“跳过后返回其余全部记录”的标准形式。
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

// KingbaseES 通过索引名称（可带 schema）删除索引，不接受 MySQL 的 ON 表名后缀。
// 调用方未指定索引 schema 时继承表的 schema，确保限定名称落在同一命名空间。
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
	// 先使用 GORM 默认命名规则，只有超出实例边界时才缩短，保证短名称与上游一致。
	k.ensureRaw()
	name := k.raw.BuildKeyName(kind, tableName, fields...)
	if len(name) <= 63 {
		return name
	}
	return shortenMySQLKeyName(name, k.mysqlIdentifierBytes())
}

func quoteMySQLIdentifier(name string) string {
	// MySQL 标识符中的反引号通过双写转义。
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// mysqlIdentifierLimit 保存单个连接的标识符字节上限查询结果。
type mysqlIdentifierLimit struct {
	once  sync.Once
	bytes int
}

// 仅当自动名称确实超过默认边界时查询实例的实际长度限制。Dialect 接口无法在此返回错误，
// 因此查询失败仍通过元数据观察器上报；脱离连接生成 SQL 时按 63 字节处理。
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
	// 长度按数据库标识符字节数计算，并在 UTF-8 字符边界处截断前缀。
	if len(name) <= limit {
		return name
	}
	// 哈希覆盖完整原始名称（包括截断位置之后的内容），避免相似长名称仅截前缀后发生冲突。
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
