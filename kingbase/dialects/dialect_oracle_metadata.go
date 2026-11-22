package kingbase

import "database/sql"

// GORM V1 metadata methods return bool. Observers retain the original error;
// no query failure may be reinterpreted as an absent object or retried away.
func (d *kingbaseOracleDialect) reportMetadataError(err error) {
	if err != nil {
		if observer, ok := d.db.(interface{ ReportMetadataError(error) }); ok {
			observer.ReportMetadataError(err)
		}
	}
}

func (d *kingbaseOracleDialect) queryCount(query string, args ...interface{}) (int, error) {
	if d.db == nil {
		return 0, sql.ErrConnDone
	}
	var count int
	err := d.db.QueryRow(query, args...).Scan(&count)
	d.reportMetadataError(err)
	return count, err
}

func (d *kingbaseOracleDialect) currentSchema() (name string) {
	if d.db != nil {
		d.reportMetadataError(d.db.QueryRow("SELECT current_schema()").Scan(&name))
	}
	return
}

// Kingbase documents these native catalogs in Oracle mode. Scope all lookups
// to the actual schema, not USER_* views (which describe the logged-in owner).
// Compare the names GORM quotes verbatim instead of folding distinct names.
func (d *kingbaseOracleDialect) hasTableOracleCompat(tableName string) bool {
	schema, name := splitOracleIdentifierPath(tableName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			schema, name,
		)
	} else {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?",
			name,
		)
	}
	return err == nil && count > 0
}

func (d *kingbaseOracleDialect) hasColumnOracleCompat(tableName, columnName string) bool {
	schema, name := splitOracleIdentifierPath(tableName)
	columnName = cleanIdentifier(columnName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?",
			schema, name, columnName,
		)
	} else {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?",
			name, columnName,
		)
	}
	return err == nil && count > 0
}

func (d *kingbaseOracleDialect) hasIndexOracleCompat(tableName, indexName string) bool {
	schema, name := splitOracleIdentifierPath(tableName)
	indexName = cleanIdentifier(indexName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = ? AND tablename = ? AND indexname = ?",
			schema, name, indexName,
		)
	} else {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = ? AND indexname = ?",
			name, indexName,
		)
	}
	return err == nil && count > 0
}

func (d *kingbaseOracleDialect) hasForeignKeyOracleCompat(tableName, foreignKeyName string) bool {
	schema, name := splitOracleIdentifierPath(tableName)
	foreignKeyName = cleanIdentifier(foreignKeyName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = ? AND table_name = ? AND constraint_name = ? AND constraint_type = 'FOREIGN KEY'",
			schema, name, foreignKeyName,
		)
	} else {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = current_schema() AND table_name = ? AND constraint_name = ? AND constraint_type = 'FOREIGN KEY'",
			name, foreignKeyName,
		)
	}
	return err == nil && count > 0
}
