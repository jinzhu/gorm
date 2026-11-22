package kingbase

import (
	"database/sql"
)

// A connection observer can capture errors that the bool-only GORM V1
// metadata interface cannot return. GORM itself remains unchanged.
func (k *KingbaseDialect) reportError(err error) {
	if err != nil {
		if observer, ok := k.db.(interface{ ReportMetadataError(error) }); ok {
			observer.ReportMetadataError(err)
		}
	}
}

func (k *KingbaseDialect) queryCount(query string, args ...interface{}) (int, error) {
	if k.db == nil {
		k.reportError(sql.ErrConnDone)
		return 0, sql.ErrConnDone
	}
	var count int
	err := k.db.QueryRow(query, args...).Scan(&count)
	k.reportError(err)
	return count, err
}

func (k *KingbaseDialect) currentSchema() (name string) {
	if k.db == nil {
		return ""
	}
	k.reportError(k.db.QueryRow("SELECT current_schema()").Scan(&name))
	return
}

// MySQL-compatible metadata queries for Kingbase MySQL mode.
func (k *KingbaseDialect) hasTableMySQLCompat(tableName string) bool {
	schema, name := splitSchemaTable(tableName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			schema, name,
		)
	} else {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?",
			name,
		)
	}
	return err == nil && count > 0
}

func (k *KingbaseDialect) hasColumnMySQLCompat(tableName, columnName string) bool {
	schema, name := splitSchemaTable(tableName)
	columnName = cleanIdentifier(columnName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?",
			schema, name, columnName,
		)
	} else {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?",
			name, columnName,
		)
	}
	return err == nil && count > 0
}

func (k *KingbaseDialect) hasIndexMySQLCompat(tableName, indexName string) bool {
	schema, name := splitSchemaTable(tableName)
	indexSchema, indexName := splitSchemaTable(indexName)
	if indexSchema != "" && schema != "" && indexSchema != schema {
		return false
	}
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = ? AND tablename = ? AND indexname = ?",
			schema, name, indexName,
		)
	} else if indexSchema != "" {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND schemaname = ? AND tablename = ? AND indexname = ?",
			indexSchema, name, indexName,
		)
	} else {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = ? AND indexname = ?",
			name, indexName,
		)
	}
	return err == nil && count > 0
}

func (k *KingbaseDialect) hasForeignKeyMySQLCompat(tableName, foreignKeyName string) bool {
	schema, name := splitSchemaTable(tableName)
	foreignKeyName = cleanIdentifier(foreignKeyName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = ? AND table_name = ? AND constraint_name = ? AND constraint_type = 'FOREIGN KEY'",
			schema, name, foreignKeyName,
		)
	} else {
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = current_schema() AND table_name = ? AND constraint_name = ? AND constraint_type = 'FOREIGN KEY'",
			name, foreignKeyName,
		)
	}
	return err == nil && count > 0
}
