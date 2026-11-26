//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

// 这些集成场景用于补充只验证方言 SQL 的 sqlmock 测试。未配置实例时由
// openKBMySQL/openKBOracle 跳过；提供 DSN 后在真实 KingbaseES 模式上执行。
type kbMySQLDialectBoundaryRow struct {
	ID        uint    `gorm:"primary_key"`
	MinInt    int     `gorm:"type:int"`
	MaxInt    int     `gorm:"type:int"`
	Unicode   string  `gorm:"type:varchar(128)"`
	EmptyText string  `gorm:"type:varchar(32)"`
	Optional  *string `gorm:"type:varchar(32)"`
	Payload   []byte  `gorm:"type:blob"`
}

type kbOracleDialectBoundaryRow struct {
	ID         uint    `gorm:"primary_key"`
	At4000     string  `gorm:"size:4000"`
	Over4000   string  `gorm:"size:4001"`
	Unicode    string  `gorm:"size:64"`
	EmptyValue string  `gorm:"size:32"`
	Nullable   *string `gorm:"size:32"`
	MinInt     int64
	MaxInt     int64
}

func TestKingbaseMySQLDialectDataBoundaries(t *testing.T) {
	db := openKBMySQL(t)
	if db == nil {
		return
	}
	defer db.Close()

	table := testutil.UniqueTable("kb_mysql_dialect_boundary")
	defer db.DropTable(table)
	if err := db.Table(table).AutoMigrate(&kbMySQLDialectBoundaryRow{}).Error; err != nil {
		t.Fatalf("create MySQL boundary table: %v", err)
	}

	want := kbMySQLDialectBoundaryRow{
		MinInt:    -2147483648,
		MaxInt:    2147483647,
		Unicode:   "女王🚀",
		EmptyText: "",
		Optional:  nil,
		Payload:   []byte{0x00, 0x01, 0x7f, 0xff},
	}
	if err := db.Table(table).Create(&want).Error; err != nil {
		t.Fatalf("insert MySQL boundary values: %v", err)
	}
	if want.ID == 0 {
		t.Fatal("MySQL boundary row did not receive an auto-increment ID")
	}

	var got kbMySQLDialectBoundaryRow
	if err := db.Table(table).First(&got, want.ID).Error; err != nil {
		t.Fatalf("read MySQL boundary values: %v", err)
	}
	if got.MinInt != want.MinInt || got.MaxInt != want.MaxInt {
		t.Fatalf("integer boundary mismatch: got (%d,%d), want (%d,%d)", got.MinInt, got.MaxInt, want.MinInt, want.MaxInt)
	}
	if got.Unicode != want.Unicode || got.EmptyText != "" {
		t.Fatalf("string boundary mismatch: unicode=%q empty=%q", got.Unicode, got.EmptyText)
	}
	if got.Optional != nil {
		t.Fatalf("NULL pointer column became non-nil: %q", *got.Optional)
	}
	if !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("BLOB boundary mismatch: got %v, want %v", got.Payload, want.Payload)
	}

	// 这里验证 MySQL 兼容方言 limit=0 的真实分支，而不只是确认能够构造 Limit 调用。
	var noRows []kbMySQLDialectBoundaryRow
	if err := db.Table(table).Limit(0).Find(&noRows).Error; err != nil {
		t.Fatalf("zero-limit query failed: %v", err)
	}
	if len(noRows) != 0 {
		t.Fatalf("zero-limit query returned %d rows", len(noRows))
	}
}

func TestKingbaseOracleDialectDataBoundaries(t *testing.T) {
	db := openKBOracle(t)
	if db == nil {
		return
	}
	defer db.Close()

	table := testutil.UniqueTable("kb_oracle_dialect_boundary")
	defer db.DropTableIfExists(table)
	if err := db.Table(table).AutoMigrate(&kbOracleDialectBoundaryRow{}).Error; err != nil {
		t.Fatalf("create Oracle boundary table: %v", err)
	}
	var longStringType string
	if err := db.Raw(`SELECT format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relname = ?
		AND a.attname = ? AND a.attnum > 0 AND NOT a.attisdropped`,
		table, "over4000").Row().Scan(&longStringType); err != nil {
		t.Fatalf("read Oracle 4001-character column type: %v", err)
	}
	if !strings.HasPrefix(strings.ToLower(longStringType), "character varying(4001 ") {
		t.Fatalf("Oracle SIZE:4001 changed to %q, want VARCHAR2(4001)", longStringType)
	}

	want := kbOracleDialectBoundaryRow{
		At4000:     strings.Repeat("a", 4000),
		Over4000:   strings.Repeat("b", 4001),
		Unicode:    "女王🚀",
		EmptyValue: "",
		Nullable:   nil,
		MinInt:     -9223372036854775807 - 1,
		MaxInt:     9223372036854775807,
	}
	if err := db.Table(table).Create(&want).Error; err != nil {
		t.Fatalf("insert Oracle boundary values: %v", err)
	}
	if want.ID == 0 {
		t.Fatal("Oracle boundary row did not receive an identity ID")
	}

	var got kbOracleDialectBoundaryRow
	if err := db.Table(table).First(&got, want.ID).Error; err != nil {
		t.Fatalf("read Oracle boundary values: %v", err)
	}
	if len(got.At4000) != 4000 || len(got.Over4000) != 4001 {
		t.Fatalf("Oracle string threshold mismatch: got lengths (%d,%d)", len(got.At4000), len(got.Over4000))
	}
	if got.Unicode != want.Unicode {
		t.Fatalf("Oracle Unicode value mismatch: got %q, want %q", got.Unicode, want.Unicode)
	}
	if got.MinInt != want.MinInt || got.MaxInt != want.MaxInt {
		t.Fatalf("Oracle numeric boundary mismatch: got (%d,%d), want (%d,%d)", got.MinInt, got.MaxInt, want.MinInt, want.MaxInt)
	}
	if got.Nullable != nil {
		t.Fatalf("Oracle NULL pointer column became non-nil: %q", *got.Nullable)
	}

	// Oracle 兼容语义会把空字符值视为 NULL。
	var emptyIsNull int
	if err := db.Table(table).Where("empty_value IS NULL").Count(&emptyIsNull).Error; err != nil {
		t.Fatalf("check Oracle empty-string semantics: %v", err)
	}
	if emptyIsNull != 1 {
		t.Fatalf("empty string was not normalized to NULL, matching rows=%d", emptyIsNull)
	}

	var noRows []kbOracleDialectBoundaryRow
	if err := db.Table(table).Limit(0).Find(&noRows).Error; err != nil {
		t.Fatalf("Oracle zero-limit query failed: %v", err)
	}
	if len(noRows) != 0 {
		t.Fatalf("Oracle zero-limit query returned %d rows", len(noRows))
	}
}
