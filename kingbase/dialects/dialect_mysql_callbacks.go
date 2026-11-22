package kingbase

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/jinzhu/gorm"
)

var mysqlCallbacks sync.Once

// Keep GORM's ordinary MySQL Create callback for normal inserts, then add only
// the Kingbase-specific generated-ID fallback. INSERT IGNORE keeps its separate
// path because gokb cannot expose its generated ID through LastInsertId.
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

// gokb's LastInsertId path requires the AUTO_INCREMENT column to be the
// table's first physical column. GORM does not impose that struct-field order,
// so a successful INSERT can otherwise leave a non-first primary key at zero.
// The normal Create callback runs inside GORM's transaction; reading the
// session value here keeps the lookup on the same connection and happens
// before GORM reloads database-default columns.
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

// INSERT IGNORE cannot be combined with the RETURNING appended by gokb.
// A leading comment suppresses that driver rewrite without changing the
// server's native INSERT IGNORE semantics. Keep GORM's surrounding callbacks
// (hooks, associations, defaults and transaction handling) in place.
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
	result, err := scope.SQLDB().Exec(scope.SQL, scope.SQLVars...)
	if scope.Err(err) != nil {
		return
	}
	affected, err := result.RowsAffected()
	if scope.Err(err) != nil {
		return
	}
	scope.DB().RowsAffected = affected
	if affected == 0 {
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
