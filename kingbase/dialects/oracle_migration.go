package kingbase

import (
	"fmt"
	"strings"

	"github.com/jinzhu/gorm"
)

// OracleMigration supplies the DDL hooks that GORM v1's Dialect interface lacks.
// Pass db.Table(...) or db.Model(...) before calling a migration method. The
// returned *gorm.DB retains the original database error in Error.
type OracleMigration struct{ db *gorm.DB }

// NewOracleMigrator enables Oracle DDL extensions without changing GORM v1.
func NewOracleMigrator(db *gorm.DB) *OracleMigration { return &OracleMigration{db: db} }

func (m *OracleMigration) result() *gorm.DB {
	if m.db == nil {
		return &gorm.DB{Error: fmt.Errorf("kingbase-oracle: nil database")}
	}
	r := m.db.NewScope(m.db.Value).DB()
	mode, kingbase := kingbaseDialectMode(r.Dialect())
	if r.Error == nil && (!kingbase || mode != KbModeOracle) {
		r.AddError(fmt.Errorf("kingbase-oracle: Oracle migrator requires Oracle dialect"))
	}
	return r
}

func (m *OracleMigration) AutoMigrate(models ...interface{}) *gorm.DB {
	return m.migrate(false, models)
}

func (m *OracleMigration) CreateTable(models ...interface{}) *gorm.DB {
	return m.migrate(true, models)
}

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
		for _, field := range scope.GetModelStruct().StructFields {
			if !field.IsNormal || field.IsIgnored {
				continue
			}
			if comment, ok := field.TagSettingsGet("COMMENT"); ok {
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

func oracleQualified(schema, name string) string {
	return quoteOracleIdentifier(schema) + "." + quoteOracleIdentifier(name)
}

func oracleSQLString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func oracleMigrationTable(db *gorm.DB, table string) (schema, name string, err error) {
	err = db.Raw(`SELECT n.nspname,c.relname FROM pg_class c
JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=to_regclass(?)`, quoteOracleIdentifierPath(table)).Row().Scan(&schema, &name)
	return
}
