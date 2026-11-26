package kingbase

import "database/sql"

// GORM V1 元数据方法只能返回 bool。观察器保存原始错误，任何查询失败都不能被解释成
// 对象不存在，也不能通过重试静默消除。
func (d *kingbaseOracleDialect) reportMetadataError(err error) {
	if err != nil {
		if observer, ok := d.db.(interface{ ReportMetadataError(error) }); ok {
			observer.ReportMetadataError(err)
		}
	}
}

func (d *kingbaseOracleDialect) queryCount(query string, args ...interface{}) (int, error) {
	// 所有 bool 元数据判断统一经过该入口，确保查询错误总能上报给连接观察器。
	if d.db == nil {
		return 0, sql.ErrConnDone
	}
	var count int
	err := d.db.QueryRow(query, args...).Scan(&count)
	d.reportMetadataError(err)
	return count, err
}

func (d *kingbaseOracleDialect) currentSchema() (name string) {
	// 使用会话 current_schema()，使 search_path 变化能够反映到未限定对象查询。
	if d.db != nil {
		d.reportMetadataError(d.db.QueryRow("SELECT current_schema()").Scan(&name))
	}
	return
}

// KingbaseES Oracle 模式提供这些原生系统目录。所有查询都限定到对象实际 schema，
// 不使用仅描述登录用户所有者的 USER_* 视图；名称按 GORM 引用后的原值比较，
// 避免大小写折叠合并本应不同的对象。
func (d *kingbaseOracleDialect) hasTableOracleCompat(tableName string) bool {
	// 已限定对象按显式 schema 查询；未限定对象只在当前 schema 内查找。
	schema, name := splitOracleIdentifierPath(tableName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = :1 AND table_name = :2",
			schema, name,
		)
	} else {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = :1",
			name,
		)
	}
	return err == nil && count > 0
}

func (d *kingbaseOracleDialect) hasColumnOracleCompat(tableName, columnName string) bool {
	// 列名是单个标识符，只清除自身引号，不参与 schema 路径拆分。
	schema, name := splitOracleIdentifierPath(tableName)
	columnName = cleanIdentifier(columnName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = :1 AND table_name = :2 AND column_name = :3",
			schema, name, columnName,
		)
	} else {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = :1 AND column_name = :2",
			name, columnName,
		)
	}
	return err == nil && count > 0
}

func (d *kingbaseOracleDialect) hasIndexOracleCompat(tableName, indexName string) bool {
	schema, name := splitOracleIdentifierPath(tableName)
	indexSchema, indexName := splitOracleIdentifierPath(indexName)
	if schema != "" && indexSchema != "" && schema != indexSchema {
		// 表和索引都显式限定且 schema 不一致时，不应跨 schema 误报同名索引。
		return false
	}
	if schema == "" {
		schema = indexSchema
	}
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = :1 AND tablename = :2 AND indexname = :3",
			schema, name, indexName,
		)
	} else {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = :1 AND indexname = :2",
			name, indexName,
		)
	}
	return err == nil && count > 0
}

func (d *kingbaseOracleDialect) hasForeignKeyOracleCompat(tableName, foreignKeyName string) bool {
	// 约束目录可能包含其他同名类型，因此额外限定 constraint_type='FOREIGN KEY'。
	schema, name := splitOracleIdentifierPath(tableName)
	foreignKeyName = cleanIdentifier(foreignKeyName)
	var (
		count int
		err   error
	)
	if schema != "" {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = :1 AND table_name = :2 AND constraint_name = :3 AND constraint_type = 'FOREIGN KEY'",
			schema, name, foreignKeyName,
		)
	} else {
		count, err = d.queryCount(
			"SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = current_schema() AND table_name = :1 AND constraint_name = :2 AND constraint_type = 'FOREIGN KEY'",
			name, foreignKeyName,
		)
	}
	return err == nil && count > 0
}
