package kingbase

import (
	"fmt"
	"sync"

	"github.com/jinzhu/gorm"
)

var queryCallbacks sync.Once

// GORM V1 的分组计数构造问题同时影响 KingbaseES 两种模式。
// 此回调只注册一次，其他数据库方言继续使用原始查询回调。
func registerQueryCallbacks() {
	queryCallbacks.Do(func() {
		rowQuery := gorm.DefaultCallback.RowQuery().Get("gorm:row_query")
		gorm.DefaultCallback.RowQuery().Replace("gorm:row_query", func(scope *gorm.Scope) {
			if _, kingbase := kingbaseDialectMode(scope.Dialect()); kingbase {
				if queryKingbaseDerivedCount(scope) {
					return
				}
			}
			rowQuery(scope)
		})
	})
}

// GORM V1 此时已经生成包含软删除和主键条件的分组查询。如果把计数包装重新按模型查询
// 构造，会错误地把这些条件追加到整个派生表表达式之后。这里只匹配当前 Scope 已生成 SQL
// 对应的标准包装形式，并完整保留原参数，避免影响普通 Count 查询。
func queryKingbaseDerivedCount(scope *gorm.Scope) bool {
	result, ok := scope.InstanceGet("row_query_result")
	row, isRow := result.(*gorm.RowQueryResult)
	// 仅接管 GORM 自己生成的精确派生表形式，用户自定义 Raw/Row 查询继续走原回调。
	if !ok || !isRow || scope.SQL == "" || scope.TableName() != "( "+scope.SQL+" ) AS count_table" {
		return false
	}
	query := "SELECT count(*) FROM " + scope.TableName()
	if option, ok := scope.Get("gorm:query_option"); ok {
		query += " " + fmt.Sprint(option)
	}
	if hint, ok := scope.Get("gorm:query_hint"); ok {
		// hint 位于 SELECT 之前，query_option 位于派生表之后，顺序与 GORM 原语义一致。
		query = fmt.Sprint(hint) + query
	}
	scope.Raw(query)
	row.Row = scope.SQLDB().QueryRow(scope.SQL, scope.SQLVars...)
	return true
}
