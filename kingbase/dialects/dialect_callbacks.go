package kingbase

import (
	"fmt"
	"sync"

	"github.com/jinzhu/gorm"
)

var queryCallbacks sync.Once

// The same GORM v1 count-construction defect affects both Kingbase modes.
// Register once and leave every other dialect on its original callback.
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

// GORM v1 has already built the grouped query with soft-delete and primary-key
// filters. Rebuilding its count wrapper as a model query adds those filters
// against the entire derived-table expression. Match only the exact wrapper
// GORM constructs from this scope's previously built SQL; keep its parameters.
func queryKingbaseDerivedCount(scope *gorm.Scope) bool {
	result, ok := scope.InstanceGet("row_query_result")
	row, isRow := result.(*gorm.RowQueryResult)
	if !ok || !isRow || scope.SQL == "" || scope.TableName() != "( "+scope.SQL+" ) AS count_table" {
		return false
	}
	query := "SELECT count(*) FROM " + scope.TableName()
	if option, ok := scope.Get("gorm:query_option"); ok {
		query += " " + fmt.Sprint(option)
	}
	if hint, ok := scope.Get("gorm:query_hint"); ok {
		query = fmt.Sprint(hint) + query
	}
	scope.Raw(query)
	row.Row = scope.SQLDB().QueryRow(scope.SQL, scope.SQLVars...)
	return true
}
