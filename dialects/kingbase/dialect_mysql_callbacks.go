package kingbase

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/jinzhu/gorm"
)

var mysqlCallbacks sync.Once

// 普通插入继续使用 GORM 原生 MySQL Create 回调，只追加 KingbaseES 的生成主键兜底逻辑。
// INSERT IGNORE 使用独立路径，因为 gokb 无法通过 LastInsertId 返回该语句的生成值。
func registerMySQLCallbacks() {
	mysqlCallbacks.Do(func() {
		create := gorm.DefaultCallback.Create().Get("gorm:create")
		gorm.DefaultCallback.Create().Replace("gorm:create", func(scope *gorm.Scope) {
			modifier, _ := scope.Get("gorm:insert_modifier")
			mode, kingbase := kingbaseDialectMode(scope.Dialect())
			mysql := kingbase && mode == KbModeMySQL
			if mysql && strings.EqualFold(strings.TrimSpace(fmt.Sprint(modifier)), "IGNORE") {
				createMySQLIgnore(scope)
				return
			}
			create(scope)
			if mysql {
				backfillMySQLPrimaryKey(scope)
			}
		})
	})
}

// gokb 的 LastInsertId 路径要求 AUTO_INCREMENT 列是表的第一个物理列，而 GORM 不限制
// 结构体字段顺序，因此成功 INSERT 后非首列主键仍可能保持为零。原生 Create 回调位于
// GORM 事务内，此处读取会话级 last_insert_id() 可保证使用同一连接，并在 GORM 回读
// 数据库默认值字段之前完成主键回填。
func backfillMySQLPrimaryKey(scope *gorm.Scope) {
	if scope.HasError() || scope.DB().RowsAffected <= 0 {
		return
	}
	primary := scope.PrimaryField()
	if primary == nil || !primary.IsBlank || !mysqlAutoIncrementPrimary(primary) {
		return
	}
	if _, ok := scope.SQLDB().(interface {
		Commit() error
		Rollback() error
	}); !ok {
		scope.Err(fmt.Errorf("kingbase-mysql: generated primary-key fallback requires a Create transaction"))
		return
	}
	var id int64
	if scope.Err(scope.SQLDB().QueryRow("SELECT last_insert_id()").Scan(&id)) != nil {
		return
	}
	if id <= 0 {
		scope.Err(fmt.Errorf("kingbase-mysql: last_insert_id() returned %d after a successful insert", id))
		return
	}
	scope.Err(primary.Set(id))
}

func mysqlAutoIncrementPrimary(primary *gorm.Field) bool {
	// 显式 AUTO_INCREMENT:false 优先；否则只允许整数族主键进入 last_insert_id() 兜底。
	if value, ok := primary.TagSettingsGet("AUTO_INCREMENT"); ok && strings.EqualFold(strings.TrimSpace(value), "false") {
		return false
	}
	typ := primary.Struct.Type
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

// 开启 get_last_insert_id=yes 后，gokb 会识别首个词元为 INSERT 的 SQL 并追加 RETURNING。
// INSERT IGNORE 不能使用该改写，因此有意增加前置注释，使语句避开驱动的 INSERT 识别，
// 同时保留服务器原生 INSERT IGNORE 语义。升级 gokb 时必须执行真实驱动的
// InsertIgnoreAdapter 测试，以便及时发现驱动前序识别规则变化。
func createMySQLIgnore(scope *gorm.Scope) {
	if scope.HasError() {
		return
	}
	if _, ok := scope.SQLDB().(interface {
		Commit() error
		Rollback() error
	}); !ok {
		scope.Err(fmt.Errorf("kingbase-mysql: INSERT IGNORE requires a Create transaction"))
		return
	}
	var columns, values, defaults []string
	// 按 GORM 原生 Create 规则收集普通字段、数据库默认值字段及 belongs_to 外键，
	// 同时尊重 Select/Omit，避免独立路径改变字段选择语义。
	for _, field := range scope.Fields() {
		if !mysqlCreateFieldSelected(scope, field) {
			continue
		}
		if field.IsNormal && !field.IsIgnored {
			if field.IsBlank && field.HasDefaultValue {
				defaults = append(defaults, scope.Quote(field.DBName))
			} else if !field.IsPrimaryKey || !field.IsBlank {
				columns = append(columns, scope.Quote(field.DBName))
				values = append(values, scope.AddToVars(field.Field.Interface()))
			}
		} else if field.Relationship != nil && field.Relationship.Kind == "belongs_to" {
			for _, key := range field.Relationship.ForeignDBNames {
				if foreign, ok := scope.FieldByName(key); ok && !mysqlCreateFieldSelected(scope, foreign) {
					columns = append(columns, scope.Quote(foreign.DBName))
					values = append(values, scope.AddToVars(foreign.Field.Interface()))
				}
			}
		}
	}
	if len(defaults) != 0 {
		// 沿用 GORM Create 回调保存默认值列的约定，供后续默认值回读逻辑使用。
		scope.InstanceSet("gorm:blank_columns_with_default_value", defaults)
	}
	query := "/* kingbase mysql insert ignore */ INSERT IGNORE INTO " + scope.QuotedTableName()
	if len(columns) == 0 {
		query += " " + scope.Dialect().DefaultValueStr()
	} else {
		query += " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(values, ",") + ")"
	}
	if option, ok := scope.Get("gorm:insert_option"); ok {
		query += " " + fmt.Sprint(option)
	}
	scope.Raw(query)
	// Scope.Exec 内部负责 SQL trace、RowsAffected 和执行错误传播，保持 GORM 原生可观测性。
	scope.Exec()
	if scope.HasError() {
		return
	}
	affected := scope.DB().RowsAffected
	if affected == 0 {
		// 重复记录被 IGNORE 时 RowsAffected 为 0，不应回填上一条语句留下的会话 ID。
		return
	}
	if primary := scope.PrimaryField(); primary != nil && primary.IsBlank {
		var id int64
		if scope.Err(scope.SQLDB().QueryRow("SELECT last_insert_id()").Scan(&id)) == nil {
			scope.Err(primary.Set(id))
		}
	}
}

func mysqlCreateFieldSelected(scope *gorm.Scope, field *gorm.Field) bool {
	// Select 白名单优先于 Omit；字段既可使用 Go 名称，也可使用数据库列名指定。
	if selected := scope.SelectAttrs(); len(selected) > 0 {
		for _, name := range selected {
			if name == field.Name || name == field.DBName {
				return true
			}
		}
		return false
	}
	for _, name := range scope.OmitAttrs() {
		if name == field.Name || name == field.DBName {
			return false
		}
	}
	return true
}
