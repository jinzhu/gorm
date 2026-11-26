//go:build kingbase_test
// +build kingbase_test

package testutil

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"kingbase.com/gokb"
)

var tableSequence uint64

const (
	testDialectKingbaseMySQL  = "kingbase-mysql"
	testDialectKingbaseOracle = "kingbase-oracle"
)

// Open 连接 KingbaseES 实例并验证所需数据库模式。配置的实例不可用时跳过测试，
// 使仅运行单元测试的 CI 仍可使用。
func Open(tb testing.TB, mode kb.KbMode) *gorm.DB {
	tb.Helper()
	return open(tb, mode, nil)
}

// OpenLogged 观察真实驱动错误（包括事务失败），成功调用保持静默；DSN 和模式验证与 Open 一致。
func OpenLogged(tb testing.TB, mode kb.KbMode) *gorm.DB {
	tb.Helper()
	return open(tb, mode, &DriverLog{tb: tb})
}

func open(tb testing.TB, mode kb.KbMode, log *DriverLog) *gorm.DB {
	tb.Helper()

	dialect, dsn := connectionConfig(mode)
	var db *gorm.DB
	var err error
	if log == nil {
		db, err = gorm.Open(dialect, "kingbase", dsn)
	} else {
		sqlDB := sql.OpenDB(&loggedConnector{base: &gokb.Driver{}, dsn: dsn, log: log})
		db, err = gorm.Open(dialect, sqlDB)
		if err != nil {
			if closeErr := sqlDB.Close(); closeErr != nil {
				tb.Errorf("close Kingbase database after failed connection: %v", closeErr)
			}
		}
	}
	if err != nil {
		if requireKingbaseDatabase() {
			tb.Fatalf("Kingbase %s integration test: connect failed: %v", mode, err)
		}
		tb.Skipf("skip Kingbase %s integration test: connect failed: %v", mode, err)
		return nil
	}

	if err := db.DB().Ping(); err != nil {
		Close(tb, db)
		if requireKingbaseDatabase() {
			tb.Fatalf("Kingbase %s integration test: ping failed: %v", mode, err)
		}
		tb.Skipf("skip Kingbase %s integration test: ping failed: %v", mode, err)
		return nil
	}

	var actualMode string
	if err := db.DB().QueryRow("SHOW database_mode").Scan(&actualMode); err != nil {
		Close(tb, db)
		tb.Fatalf("read database_mode for %s: %v", mode, err)
	}
	if !strings.EqualFold(actualMode, string(mode)) {
		Close(tb, db)
		tb.Fatalf("database mode mismatch: got %q, want %q", actualMode, mode)
	}
	// 验收运行独占一个隔离 schema。在任何测试创建或删除对象前，拒绝未遵循 search_path 的连接。
	if expected := os.Getenv("KINGBASE_TEST_SCHEMA"); expected != "" {
		var actual string
		if err := db.DB().QueryRow("SELECT current_schema()").Scan(&actual); err != nil || actual != expected {
			Close(tb, db)
			tb.Fatalf("Kingbase test schema mismatch: got %q, want %q, err=%v", actual, expected, err)
		}
	}

	db.LogMode(strings.EqualFold(strings.TrimSpace(os.Getenv("DEBUG")), "true"))
	if log != nil {
		db = db.Set(driverLogKey, log)
	}
	return db
}

// Close 报告连接清理错误，不静默丢弃。
func Close(tb testing.TB, db *gorm.DB) {
	tb.Helper()
	if err := db.Close(); err != nil {
		tb.Errorf("close Kingbase database: %v", err)
	}
}

// requireKingbaseDatabase 在发布或验收运行中把“无数据库则跳过”改为强制失败；
// 本地仅单元测试场景可以不设置，并在实例不可用时继续跳过集成测试。
func requireKingbaseDatabase() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REQUIRE_KINGBASE_DB"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// KeepTestTables 允许操作人员在指定测试结束后通过 KStudio 检查集成测试表。
// 该能力必须显式开启；普通 CI 和全量测试仍保持自动清理。
func KeepTestTables() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KEEP_KINGBASE_TEST_TABLES"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// UniqueTable 返回适合集成测试的简短隔离表名。
func UniqueTable(prefix string) string {
	sequence := atomic.AddUint64(&tableSequence, 1)
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), sequence)
}

func connectionConfig(mode kb.KbMode) (dialect, dsn string) {
	dialect = "kingbase"
	switch mode {
	case kb.KbModeMySQL:
		dsn = firstNonEmpty(os.Getenv("KINGBASE_MYSQL_DSN"), os.Getenv("KINGBASE_DSN"))
		if dsn == "" {
			dsn = "host=127.0.0.1 port=54331 user=system password=123456 dbname=test sslmode=disable get_last_insert_id=yes"
		} else if !hasDSNOption(dsn, "get_last_insert_id") {
			// 只有启用该驱动选项，gokb 才会通过 LastInsertId 返回 MySQL 模式生成主键。
			dsn += " get_last_insert_id=yes"
		}
	case kb.KbModeOracle:
		dsn = firstNonEmpty(os.Getenv("KINGBASE_ORACLE_DSN"), os.Getenv("KINGBASE_DSN"))
		if dsn == "" {
			dsn = "host=127.0.0.1 port=54332 user=system password=123456 dbname=test sslmode=disable"
		}
	default:
		panic(fmt.Sprintf("unsupported Kingbase test mode: %s", mode))
	}
	return dialect, dsn
}

func hasDSNOption(dsn, key string) bool {
	prefix := key + "="
	options := strings.Fields(dsn)
	for _, option := range options {
		if option == key || strings.HasPrefix(option, prefix) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
