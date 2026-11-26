package kingbase

import (
	"fmt"
	"strings"

	"github.com/jinzhu/gorm"
)

// OracleMigration 补充 GORM V1 Dialect 接口缺少的 Oracle DDL 能力。
// 调用迁移方法前可通过 db.Table(...) 或 db.Model(...) 指定对象。每个方法返回克隆后的
// *gorm.DB，并在 Error 中保留原始数据库错误；传给 NewOracleMigrator 的实例不会被修改。
type OracleMigration struct{ db *gorm.DB }

// NewOracleMigrator 在不修改 GORM V1 主框架的前提下启用 Oracle DDL 扩展。
func NewOracleMigrator(db *gorm.DB) *OracleMigration { return &OracleMigration{db: db} }

// result 创建用于本次迁移的独立 GORM 句柄，并在执行 DDL 前验证 Oracle 模式。
func (m *OracleMigration) result() *gorm.DB {
	if m.db == nil {
		return &gorm.DB{Error: fmt.Errorf("kingbase-oracle: nil database")}
	}
	// NewScope(...).DB() 返回独立克隆，避免 AddError 污染调用方后续复用的 *gorm.DB。
	r := m.db.NewScope(m.db.Value).DB()
	mode, kingbase := kingbaseDialectMode(r.Dialect())
	if r.Error == nil && (!kingbase || mode != KbModeOracle) {
		r.AddError(fmt.Errorf("kingbase-oracle: Oracle migrator requires Oracle dialect"))
	}
	return r
}

// AutoMigrate 执行 GORM V1 自动迁移，并在 Oracle 模式下补充字段注释。
func (m *OracleMigration) AutoMigrate(models ...interface{}) *gorm.DB {
	return m.migrate(false, models)
}

// CreateTable 创建模型表，并在 Oracle 模式下补充字段注释。
func (m *OracleMigration) CreateTable(models ...interface{}) *gorm.DB {
	return m.migrate(true, models)
}

// migrate 先调用 GORM 原生建表或自动迁移，再补充 Dialect 接口无法表达的列注释。
func (m *OracleMigration) migrate(create bool, models []interface{}) *gorm.DB {
	r := m.result()
	if r.Error != nil {
		return r
	}
	for _, model := range models {
		var migrated *gorm.DB
		if create {
			migrated = r.CreateTable(model)
		} else {
			migrated = r.AutoMigrate(model)
		}
		if migrated.Error != nil {
			r.AddError(migrated.Error)
			return r
		}
		scope := r.NewScope(model)
		schema, table, err := oracleMigrationTable(r, scope.TableName())
		if err != nil {
			r.AddError(err)
			return r
		}
		// GORM V1 不会从 COMMENT 标签生成 Oracle COMMENT ON COLUMN，
		// 因此在表迁移成功后逐列补充，任一失败立即保留原始错误并停止。
		for _, field := range scope.GetModelStruct().StructFields {
			if !field.IsNormal || field.IsIgnored {
				continue
			}
			if comment, ok := field.TagSettingsGet("COMMENT"); ok {
				// GORM 标签可能以 SQL 字符串字面量保存注释，先还原一次再统一安全转义。
				if len(comment) >= 2 && comment[0] == '\'' && comment[len(comment)-1] == '\'' {
					comment = strings.ReplaceAll(comment[1:len(comment)-1], "''", "'")
				}
				query := "COMMENT ON COLUMN " + oracleQualified(schema, table) + "." + quoteOracleIdentifier(field.DBName) + " IS " + oracleSQLString(comment)
				if err := r.Exec(query).Error; err != nil {
					r.AddError(err)
					return r
				}
			}
		}
	}
	return r
}

// oracleQualified 分别引用 schema 和对象名，生成安全的限定名称。
func oracleQualified(schema, name string) string {
	return quoteOracleIdentifier(schema) + "." + quoteOracleIdentifier(name)
}

// oracleSQLString 通过双写单引号生成 SQL 字符串字面量。
func oracleSQLString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

// oracleMigrationTable 通过 to_regclass 解析实际对象，避免仅依赖当前 search_path
// 拼接 COMMENT 目标，并取得数据库最终采用的 schema 与表名。
func oracleMigrationTable(db *gorm.DB, table string) (schema, name string, err error) {
	err = db.Raw(`SELECT n.nspname,c.relname FROM pg_class c
JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=to_regclass(?)`, quoteOracleIdentifierPath(table)).Row().Scan(&schema, &name)
	return
}
