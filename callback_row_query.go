package gorm

import (
	"database/sql"
	"fmt"
)

// Define callbacks for row query
func init() {
	DefaultCallback.RowQuery().Register("gorm:row_query", rowQueryCallback)
}

type RowQueryResult struct {
	Row *sql.Row
}

type RowsQueryResult struct {
	Rows  *sql.Rows
	Error error
}

// queryCallback used to query data from database
func rowQueryCallback(scope *Scope) {
	if result, ok := scope.InstanceGet("row_query_result"); ok {
		scope.prepareQuerySQL()

		if scope.DB() != nil {
			scope.DB().SQL = FormatSQL(scope.SQL, scope.SQLVars...)
		}

		if str, ok := scope.Get("gorm:query_hint"); ok {
			scope.SQL = fmt.Sprint(str) + scope.SQL
		}

		if rowResult, ok := result.(*RowQueryResult); ok {
			rowResult.Row = scope.SQLDB().QueryRow(scope.SQL, scope.SQLVars...)
		} else if rowsResult, ok := result.(*RowsQueryResult); ok {
			rows, err := scope.SQLDB().Query(scope.SQL, scope.SQLVars...)
			rowsResult.Rows = rows
			if rowsResult.Error != nil {
				rowsResult.Error = NewGormError(err, scope.SQL)
			} else {
				rowsResult.Error = err
			}
		}
	}
}
