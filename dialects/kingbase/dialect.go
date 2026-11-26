package kingbase

import (
	"fmt"
	"strings"

	"github.com/jinzhu/gorm"
)

// KbMode 表示 KingbaseES 实例的兼容模式。
type KbMode string

const (
	KbModeMySQL  KbMode = "mysql"
	KbModeOracle KbMode = "oracle"
)

// DialectKingbase 是应用接入 KingbaseES 时使用的统一 GORM 方言名称。
const DialectKingbase = "kingbase"

// dialectInitializer 用于在不修改 GORM V1 Open 的前提下读取方言初始化错误。
type dialectInitializer interface {
	DialectInitializationError() error
}

// Open 通过统一的 KingbaseES 方言初始化 GORM，并直接返回连接阶段的模式探测错误。
// 参数与 gorm.Open 中方言名称之后的参数保持一致。
func Open(args ...interface{}) (*gorm.DB, error) {
	db, openErr := openWithMetadataTracking(args...)
	if db == nil {
		return nil, openErr
	}

	initializer, ok := db.Dialect().(dialectInitializer)
	if !ok {
		return db, openErr
	}
	if initErr := initializer.DialectInitializationError(); initErr != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("%v; close Kingbase connection: %v", initErr, closeErr)
		}
		return nil, initErr
	}

	return db, openErr
}

// NewKingbaseDialect 按已确认的兼容模式创建内部方言实现。
// 应用连接应优先使用 Open，由统一方言自动探测模式。
func NewKingbaseDialect(mode KbMode) gorm.Dialect {
	base := KingbaseDialect{mode: mode}
	switch mode {
	case KbModeMySQL:
		return &kingbaseMySQLDialect{KingbaseDialect: base}
	case KbModeOracle:
		return &kingbaseOracleDialect{}
	default:
		panic(fmt.Sprintf("unsupported Kingbase mode: %s", mode))
	}
}

// kingbaseAutoDialect 是 KingbaseES 对外统一方言的内部实现。
// 它在 gorm.Open 调用 SetDB 时只探测一次数据库模式，后续所有方言操作
// 均转发给对应的 MySQL 或 Oracle 模式实现，避免运行期间发生隐式模式切换。
type kingbaseAutoDialect struct {
	// mode 和 delegate 只在 SetDB 成功探测后同时赋值；initErr 保存探测阶段的首个失败。
	mode     KbMode
	delegate gorm.Dialect
	initErr  error
}

var _ gorm.Dialect = (*kingbaseAutoDialect)(nil)

func (d *kingbaseAutoDialect) GetName() string {
	// 初始化成功后返回模式方言名，供内部回调选择分支；初始化前仍保持公开名称 kingbase。
	if d.delegate != nil {
		return d.delegate.GetName()
	}
	return DialectKingbase
}

func (d *kingbaseAutoDialect) SetDB(db gorm.SQLCommon) {
	// 方言原型可能被重复注册或复用，每次绑定连接前必须清理上一次探测状态。
	d.mode = ""
	d.delegate = nil
	d.initErr = nil
	if db == nil {
		d.initErr = fmt.Errorf("kingbase: database connection is nil")
		return
	}

	var databaseMode string
	// database_mode 是选择 SQL 语法和元数据实现的唯一依据；查询失败时不猜测模式。
	if err := db.QueryRow("SHOW database_mode").Scan(&databaseMode); err != nil {
		d.initErr = fmt.Errorf("kingbase: read database_mode: %v", err)
		return
	}

	switch strings.ToLower(strings.TrimSpace(databaseMode)) {
	case string(KbModeMySQL):
		d.mode = KbModeMySQL
	case string(KbModeOracle):
		d.mode = KbModeOracle
	default:
		d.initErr = fmt.Errorf("kingbase: unsupported database_mode %q", databaseMode)
		return
	}

	d.delegate = NewKingbaseDialect(d.mode)
	d.delegate.SetDB(db)
}

// DialectInitializationError 暴露模式探测错误，无需为原始 GORM V1 Open 增加初始化钩子。
func (d *kingbaseAutoDialect) DialectInitializationError() error { return d.initErr }

func (d *kingbaseAutoDialect) selected() gorm.Dialect {
	// 方言方法不具备 error 返回值。若调用方绕过 Open 的初始化检查，
	// 在首次使用时立即 panic 比生成错误模式的 SQL 更容易定位根因。
	if d.delegate != nil {
		return d.delegate
	}
	if d.initErr != nil {
		panic(d.initErr)
	}
	panic("kingbase: database mode has not been initialized")
}

// 以下 Dialect 方法均透明转发给已探测的模式实现，统一入口本身不重复维护 SQL 规则。
func (d *kingbaseAutoDialect) BindVar(i int) string { return d.selected().BindVar(i) }

func (d *kingbaseAutoDialect) Quote(key string) string { return d.selected().Quote(key) }

func (d *kingbaseAutoDialect) DataTypeOf(field *gorm.StructField) string {
	return d.selected().DataTypeOf(field)
}

func (d *kingbaseAutoDialect) HasIndex(tableName, indexName string) bool {
	return d.selected().HasIndex(tableName, indexName)
}

func (d *kingbaseAutoDialect) HasForeignKey(tableName, foreignKeyName string) bool {
	return d.selected().HasForeignKey(tableName, foreignKeyName)
}

func (d *kingbaseAutoDialect) RemoveIndex(tableName, indexName string) error {
	return d.selected().RemoveIndex(tableName, indexName)
}

func (d *kingbaseAutoDialect) HasTable(tableName string) bool {
	return d.selected().HasTable(tableName)
}

func (d *kingbaseAutoDialect) HasColumn(tableName, columnName string) bool {
	return d.selected().HasColumn(tableName, columnName)
}

func (d *kingbaseAutoDialect) ModifyColumn(tableName, columnName, typ string) error {
	return d.selected().ModifyColumn(tableName, columnName, typ)
}

func (d *kingbaseAutoDialect) LimitAndOffsetSQL(limit, offset interface{}) (string, error) {
	return d.selected().LimitAndOffsetSQL(limit, offset)
}

func (d *kingbaseAutoDialect) SelectFromDummyTable() string {
	return d.selected().SelectFromDummyTable()
}

func (d *kingbaseAutoDialect) LastInsertIDOutputInterstitial(tableName, columnName string, columns []string) string {
	return d.selected().LastInsertIDOutputInterstitial(tableName, columnName, columns)
}

func (d *kingbaseAutoDialect) LastInsertIDReturningSuffix(tableName, columnName string) string {
	return d.selected().LastInsertIDReturningSuffix(tableName, columnName)
}

func (d *kingbaseAutoDialect) DefaultValueStr() string {
	return d.selected().DefaultValueStr()
}

func (d *kingbaseAutoDialect) BuildKeyName(kind, tableName string, fields ...string) string {
	return d.selected().BuildKeyName(kind, tableName, fields...)
}

func (d *kingbaseAutoDialect) NormalizeIndexAndColumn(indexName, columnName string) (string, string) {
	return d.selected().NormalizeIndexAndColumn(indexName, columnName)
}

func (d *kingbaseAutoDialect) CurrentDatabase() string {
	return d.selected().CurrentDatabase()
}

// Register 注册统一方言、内部模式方言及 KingbaseES 专用回调。
func Register() {
	// 对外只使用 kingbase；两个模式名称仅供内部委托和兼容性测试使用。
	gorm.RegisterDialect(DialectKingbase, &kingbaseAutoDialect{})
	registerKingbaseModeDialects()
	registerMySQLCallbacks()
	registerQueryCallbacks()
}

func init() {
	Register()
}

// cleanIdentifier 去除单个标识符外层的 SQL 引号并还原转义。
func cleanIdentifier(name string) string {
	// 仅移除包裹整个标识符的一层引号，并还原对应的转义字符。
	// schema 路径必须先拆分再调用本函数，避免把 "APP"."IDX" 当成单个名称。
	name = strings.TrimSpace(name)
	if len(name) >= 2 {
		switch {
		case name[0] == '"' && name[len(name)-1] == '"':
			return strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
		case name[0] == '`' && name[len(name)-1] == '`':
			return strings.ReplaceAll(name[1:len(name)-1], "``", "`")
		case name[0] == '[' && name[len(name)-1] == ']':
			return strings.ReplaceAll(name[1:len(name)-1], "]]", "]")
		}
	}
	return name
}

// splitSchemaTable 拆分 MySQL 风格或通用引用形式的 schema 与对象名。
func splitSchemaTable(table string) (schema, name string) {
	// 只把引号之外的第一个点解释为 schema 分隔符，引号内的点属于标识符本身。
	table = strings.TrimSpace(table)
	var quote byte
	componentStart := true
	for i := 0; i < len(table); i++ {
		c := table[i]
		if quote != 0 {
			if c == quote {
				if i+1 < len(table) && table[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if componentStart && (c == '`' || c == '"' || c == '[') {
			quote = c
			if c == '[' {
				quote = ']'
			}
			componentStart = false
			continue
		}
		if c == '.' {
			return cleanIdentifier(table[:i]), cleanIdentifier(table[i+1:])
		}
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			componentStart = false
		}
	}
	return "", cleanIdentifier(table)
}
