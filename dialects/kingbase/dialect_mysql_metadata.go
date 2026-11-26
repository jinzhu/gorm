package kingbase

import (
	"database/sql"
)

// 连接观察器用于保存 GORM V1 仅返回 bool 的元数据接口无法携带的原始错误，
// 整个过程无需修改 GORM 主框架。
func (k *KingbaseDialect) reportError(err error) {
	if err != nil {
		if observer, ok := k.db.(interface{ ReportMetadataError(error) }); ok {
			observer.ReportMetadataError(err)
		}
	}
}

func (k *KingbaseDialect) queryCount(query string, args ...interface{}) (int, error) {
	// 所有 Has* 查询统一经过该入口，保证连接不可用和 Scan 错误都能旁路上报。
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
	// current_schema() 同时遵循会话 search_path，适合隔离测试和多 schema 应用。
	if k.db == nil {
		return ""
	}
	k.reportError(k.db.QueryRow("SELECT current_schema()").Scan(&name))
	return
}

// 以下方法使用 KingbaseES MySQL 模式可用的系统目录查询元数据。
func (k *KingbaseDialect) hasTableMySQLCompat(tableName string) bool {
	// 显式 schema 直接按限定名称查询；未限定时严格限制到 current_schema()。
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
		// 表和索引都显式限定但 schema 不一致时，不跨 schema 查找同名索引。
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
		count, err = k.queryCount(
			"SELECT COUNT(*) FROM pg_indexes WHERE schemaname = ? AND tablename = ? AND indexname = ?",
			schema, name, indexName,
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
	// 只接受 FOREIGN KEY 类型，避免与同名唯一约束或检查约束混淆。
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
