//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type mysqlRepairCountRow struct {
	ID        int64 `gorm:"primary_key"`
	Category  string
	DeletedAt *time.Time
}

func TestKingbaseMySQLMode_RepairBoundaries(t *testing.T) {
	db := testutil.OpenLogged(t, kb.KbModeMySQL)
	defer testutil.Close(t, db)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	schema := testutil.UniqueTable("kb_repair")
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		run  func(*testing.T, *gorm.DB)
	}{
		{"01_CountHavingSoftDelete", mysqlRepairCountHaving},
		{"02_IgnoreUnsignedPrimaryKey", mysqlRepairIgnoreUnsigned},
		{"03_QuotedIndexName", mysqlRepairQuotedIndex},
		{"04_TaggedSpecialIndex", mysqlRepairTaggedIndex},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := tx.Exec("SAVEPOINT kb_repair_case").Error; err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := tx.Exec("ROLLBACK TO SAVEPOINT kb_repair_case").Error; err != nil {
					t.Error(err)
				}
				if err := tx.Exec("RELEASE SAVEPOINT kb_repair_case").Error; err != nil {
					t.Error(err)
				}
			}()
			start := testutil.DriverLogFor(db).Snapshot()
			test.run(t, tx)
			for _, result := range testutil.DriverLogFor(db).SQLResults(start) {
				if result.Err != nil {
					t.Errorf("[未通过] 驱动错误: %v; SQL=%s", result.Err, result.SQL)
				}
			}
		})
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.Raw("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name=?", schema).Row().Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("清理 schema: remaining=%d error=%v", remaining, err)
	}
}

func mysqlRepairCountHaving(t *testing.T, tx *gorm.DB) {
	if err := tx.Table("count_items").AutoMigrate(&mysqlRepairCountRow{}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, row := range []mysqlRepairCountRow{{Category: "a"}, {Category: "a"}, {Category: "b"}, {Category: "b", DeletedAt: &now}} {
		if err := tx.Table("count_items").Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		query *gorm.DB
		want  int
	}{
		{"scoped", tx.Table("count_items").Model(&mysqlRepairCountRow{}).Where("category IN (?)", []string{"a", "b"}).Select("category").Group("category").Having("COUNT(*) > ?", 1), 1},
		{"unscoped", tx.Table("count_items").Model(&mysqlRepairCountRow{}).Unscoped().Select("category").Group("category").Having("COUNT(*) > ?", 1), 2},
		{"primary_key", tx.Table("count_items").Model(&mysqlRepairCountRow{ID: 1}).Select("category").Group("category").Having("COUNT(*) > ?", 0), 1},
		{"empty", tx.Table("count_items").Model(&mysqlRepairCountRow{}).Where("category=?", "missing").Select("category").Group("category").Having("COUNT(*) > ?", 1), 0},
	} {
		var count int
		if err := test.query.Count(&count).Error; err != nil || count != test.want {
			t.Fatalf("[未通过] %s count=%d want=%d error=%v", test.name, count, test.want, err)
		}
		t.Logf("[通过] %s count=%d", test.name, count)
	}
}

func mysqlRepairIgnoreUnsigned(t *testing.T, tx *gorm.DB) {
	type row struct {
		ID    uint32 `gorm:"primary_key"`
		Email string `gorm:"unique_index:uq_ignore_email"`
	}
	if err := tx.Table("ignore_items").AutoMigrate(&row{}).Error; err != nil {
		t.Fatal(err)
	}
	first := row{Email: "first"}
	if err := tx.Table("ignore_items").Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := row{Email: first.Email}
	result := tx.Table("ignore_items").Set("gorm:insert_modifier", "IGNORE").Create(&duplicate)
	if result.Error != nil || result.RowsAffected != 0 {
		t.Fatalf("ignore duplicate rows=%d error=%v", result.RowsAffected, result.Error)
	}
	created := row{Email: "second"}
	result = tx.Table("ignore_items").Set("gorm:insert_modifier", "IGNORE").Create(&created)
	var stored row
	err := tx.Table("ignore_items").Where("email=?", created.Email).First(&stored).Error
	if result.Error != nil || result.RowsAffected != 1 || err != nil || created.ID <= first.ID || created.ID != stored.ID {
		t.Fatalf("[未通过] unsigned IGNORE model=%d stored=%d first=%d rows=%d insert=%v read=%v", created.ID, stored.ID, first.ID, result.RowsAffected, result.Error, err)
	}
	t.Logf("[通过] unsigned IGNORE model=%d stored=%d", created.ID, stored.ID)
}

func mysqlRepairQuotedIndex(t *testing.T, tx *gorm.DB) {
	if err := tx.Exec("CREATE TABLE quote_items (id integer)").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("CREATE INDEX `idx.part` ON quote_items (id)").Error; err != nil {
		t.Fatal(err)
	}
	if !tx.Dialect().HasIndex("quote_items", "`idx.part`") {
		t.Fatal("删除前索引不存在")
	}
	start := testutil.DriverLogFor(tx).Snapshot()
	if err := tx.Table("quote_items").RemoveIndex("`idx.part`").Error; err != nil {
		t.Fatal(err)
	}
	for _, result := range testutil.DriverLogFor(tx).SQLResults(start) {
		if result.Err != nil {
			t.Fatalf("[未通过] ORM 丢失驱动错误: %v; SQL=%s", result.Err, result.SQL)
		}
	}
	if tx.Dialect().HasIndex("quote_items", "`idx.part`") {
		t.Fatal("删除后索引仍存在")
	}
	t.Log("[通过] 带点的引用索引名按原名删除")
}

func mysqlRepairTaggedIndex(t *testing.T, tx *gorm.DB) {
	type row struct {
		ID   int64  `gorm:"primary_key"`
		Name string `gorm:"index:idx-repair-name;size:32"`
	}
	if err := tx.Table("tag_items").AutoMigrate(&row{}).Error; err != nil {
		t.Fatal(err)
	}
	if !tx.Dialect().HasIndex("tag_items", "idx-repair-name") {
		t.Fatal("标签索引未落库")
	}
	if err := tx.Table("tag_items").AutoMigrate(&row{}).Error; err != nil {
		t.Fatal(err)
	}
	t.Log("[通过] 特殊 INDEX 标签及重复迁移")
}
