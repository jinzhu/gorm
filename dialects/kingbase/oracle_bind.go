package kingbase

import (
	"database/sql"
	"fmt"
	"regexp"

	"github.com/jinzhu/gorm"
)

// 命名变量限制为稳定的 ASCII 标识符，避免把冒号、空白或 SQL 片段注入表达式。
var oracleNamedBindPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// OracleNamedBind 创建 :name 形式的 Oracle 命名绑定表达式，同时将值保留为
// database/sql 命名参数交给 gokb。
func OracleNamedBind(name string, value interface{}) (*gorm.SqlExpr, error) {
	if !oracleNamedBindPattern.MatchString(name) {
		return nil, fmt.Errorf("kingbase-oracle: invalid bind variable name %q", name)
	}
	return gorm.Expr(":"+name, sql.Named(name, value)), nil
}
