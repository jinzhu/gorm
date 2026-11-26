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

func TestKingbaseOracleMode_CountHavingSoftDelete(t *testing.T) {
	db := testutil.OpenLogged(t, kb.KbModeOracle)
	defer testutil.Close(t, db)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	type row struct {
		ID        int64 `gorm:"primary_key"`
		Category  string
		DeletedAt *time.Time
	}
	table := testutil.UniqueTable("kb_ora_count")
	if err := tx.Table(table).AutoMigrate(&row{}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, item := range []row{{Category: "a"}, {Category: "a"}, {Category: "b"}, {Category: "b", DeletedAt: &now}} {
		if err := tx.Table(table).Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name  string
		query *gorm.DB
		want  int
	}{
		{"scoped", tx.Table(table).Model(&row{}).Where("category IN (?)", []string{"a", "b"}).Select("category").Group("category").Having("COUNT(*) > ?", 1), 1},
		{"unscoped", tx.Table(table).Model(&row{}).Unscoped().Select("category").Group("category").Having("COUNT(*) > ?", 1), 2},
		{"primary_key", tx.Table(table).Model(&row{ID: 1}).Select("category").Group("category").Having("COUNT(*) > ?", 0), 1},
		{"empty", tx.Table(table).Model(&row{}).Where("category=?", "missing").Select("category").Group("category").Having("COUNT(*) > ?", 1), 0},
	} {
		var count int
		if err := test.query.Count(&count).Error; err != nil || count != test.want {
			t.Fatalf("[未通过] %s count=%d want=%d error=%v", test.name, count, test.want, err)
		}
		t.Logf("[通过] %s count=%d", test.name, count)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if db.HasTable(table) {
		t.Fatal("回滚后测试表仍存在")
	}
	for _, result := range testutil.DriverLogFor(db).SQLResults(0) {
		if result.Err != nil {
			t.Fatalf("[未通过] 驱动错误: %v; SQL=%s", result.Err, result.SQL)
		}
	}
}
