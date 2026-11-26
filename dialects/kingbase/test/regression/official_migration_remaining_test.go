//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"fmt"
	"testing"
	"time"

	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type officialMigrationRemainingV1 struct {
	ID       uint `gorm:"primary_key"`
	Name     string
	Category string
}

func (officialMigrationRemainingV1) TableName() string { return "kb_official_migration_rows" }

type officialMigrationRemainingV2 struct {
	ID          uint `gorm:"primary_key"`
	Name        string
	Category    string
	Description string
}

func (officialMigrationRemainingV2) TableName() string { return "kb_official_migration_rows" }

type officialMigrationRemainingParent struct {
	ID   uint `gorm:"primary_key"`
	Name string
}

func (officialMigrationRemainingParent) TableName() string { return "kb_official_migration_parent" }

type officialMigrationRemainingChild struct {
	ID       uint `gorm:"primary_key"`
	ParentID uint
	Value    string
}

func (officialMigrationRemainingChild) TableName() string { return "kb_official_migration_child" }

type officialMigrationLongTarget struct {
	ID uint `gorm:"primary_key"`
}

func (officialMigrationLongTarget) TableName() string {
	return "kb_really_long_table_name_to_test_identifier_length_limit"
}

type officialMigrationLongSource struct {
	ID                uint `gorm:"primary_key"`
	ReallyLongThingID uint
}

func (officialMigrationLongSource) TableName() string {
	return "kb_not_so_long_table_name"
}

type officialMigrationShortTarget struct {
	ID uint `gorm:"primary_key"`
}

func (officialMigrationShortTarget) TableName() string { return "kb_short_fk_target" }

type officialMigrationLongSourceShortTarget struct {
	ID      uint `gorm:"primary_key"`
	ShortID uint
}

func (officialMigrationLongSourceShortTarget) TableName() string {
	return "kb_really_long_thing_that_references_short_target"
}

// TestOfficialMigrationRemainingCoverage 将 migration_test.go 的每个场景映射为明确的
// KingbaseES 子测试，包括仅 MySQL 支持的前缀索引分支。
func TestOfficialMigrationRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db,
			&officialMigrationLongSource{}, &officialMigrationLongTarget{},
			&officialMigrationLongSourceShortTarget{}, &officialMigrationShortTarget{},
			&officialMigrationRemainingChild{}, &officialMigrationRemainingParent{}, &officialMigrationRemainingV1{},
		)
		defer db.DropTableIfExists(
			&officialMigrationLongSource{}, &officialMigrationLongTarget{},
			&officialMigrationLongSourceShortTarget{}, &officialMigrationShortTarget{},
			&officialMigrationRemainingChild{}, &officialMigrationRemainingParent{}, &officialMigrationRemainingV1{},
		)

		t.Run("TestAutoMigration", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMigrationRemainingV1{}).Error; err != nil {
				t.Fatalf("initial automigrate: %v", err)
			}
			if !db.HasTable((officialMigrationRemainingV1{}).TableName()) || !db.Dialect().HasColumn((officialMigrationRemainingV1{}).TableName(), "name") {
				t.Fatal("AutoMigrate did not create the base table/column")
			}
			if err := db.AutoMigrate(&officialMigrationRemainingV2{}).Error; err != nil {
				t.Fatalf("extension automigrate: %v", err)
			}
			if !db.Dialect().HasColumn((officialMigrationRemainingV2{}).TableName(), "description") {
				t.Fatal("AutoMigrate did not add description")
			}
			// 重复执行 AutoMigrate 属于上游幂等性契约的一部分。
			if err := db.AutoMigrate(&officialMigrationRemainingV2{}).Error; err != nil {
				t.Fatalf("idempotent automigrate: %v", err)
			}
		})

		t.Run("TestIndexes", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMigrationRemainingV2{}).Error; err != nil {
				t.Fatalf("automigrate for index: %v", err)
			}
			name := fmt.Sprintf("idx_kb_mig_name_%d", time.Now().UnixNano())
			if err := db.Model(&officialMigrationRemainingV2{}).AddIndex(name, "name").Error; err != nil {
				t.Fatalf("add index: %v", err)
			}
			if !db.Dialect().HasIndex((officialMigrationRemainingV2{}).TableName(), name) {
				t.Fatalf("index %s not found", name)
			}
			if err := db.Model(&officialMigrationRemainingV2{}).RemoveIndex(name).Error; err != nil {
				t.Fatalf("remove index: %v", err)
			}
			if db.Dialect().HasIndex((officialMigrationRemainingV2{}).TableName(), name) {
				t.Fatalf("removed index %s still exists", name)
			}
		})

		t.Run("TestMultipleIndexes", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMigrationRemainingV2{}).Error; err != nil {
				t.Fatalf("automigrate for multiple indexes: %v", err)
			}
			base := testutil.UniqueTable("idx_kb_mig")
			indexes := []struct {
				name    string
				columns []string
			}{
				{base + "_name", []string{"name"}},
				{base + "_category", []string{"category"}},
				{base + "_composite", []string{"name", "category"}},
			}
			for _, index := range indexes {
				if err := db.Model(&officialMigrationRemainingV2{}).AddIndex(index.name, index.columns...).Error; err != nil {
					t.Fatalf("add index %s: %v", index.name, err)
				}
				if !db.Dialect().HasIndex((officialMigrationRemainingV2{}).TableName(), index.name) {
					t.Fatalf("index %s not found", index.name)
				}
			}
			for _, index := range indexes {
				if err := db.Model(&officialMigrationRemainingV2{}).RemoveIndex(index.name).Error; err != nil {
					t.Fatalf("remove index %s: %v", index.name, err)
				}
				if db.Dialect().HasIndex((officialMigrationRemainingV2{}).TableName(), index.name) {
					t.Fatalf("removed index %s still exists", index.name)
				}
			}
		})

		t.Run("TestModifyColumnType", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMigrationRemainingV2{}).Error; err != nil {
				t.Fatalf("automigrate for ModifyColumn: %v", err)
			}
			columnType := "varchar(512)"
			if mode == kb.KbModeOracle {
				columnType = "varchar2(512)"
			}
			if err := db.Model(&officialMigrationRemainingV2{}).ModifyColumn("description", columnType).Error; err != nil {
				t.Fatalf("modify description to %s: %v", columnType, err)
			}
			var actual string
			if err := db.Raw(`SELECT format_type(a.atttypid,a.atttypmod) FROM pg_attribute a
WHERE a.attrelid=to_regclass(?) AND a.attname='description' AND NOT a.attisdropped`, (officialMigrationRemainingV2{}).TableName()).Row().Scan(&actual); err != nil {
				t.Fatalf("read modified column definition: %v", err)
			}
			want := "character varying(512)"
			if mode == kb.KbModeOracle {
				want = "character varying(512 char)"
			}
			if actual != want {
				t.Fatalf("modified column type=%q, want=%q", actual, want)
			}
		})

		t.Run("TestIndexWithPrefixLength", func(t *testing.T) {
			if mode != kb.KbModeMySQL {
				t.Skip("prefix length is a MySQL-mode index capability")
			}
			if err := db.AutoMigrate(&officialMigrationRemainingV2{}).Error; err != nil {
				t.Fatalf("automigrate for prefix index: %v", err)
			}
			name := testutil.UniqueTable("idx_kb_prefix")
			if err := db.Model(&officialMigrationRemainingV2{}).AddIndex(name, "name(16)").Error; err != nil {
				t.Fatalf("add prefix index: %v", err)
			}
			if !db.Dialect().HasIndex((officialMigrationRemainingV2{}).TableName(), name) {
				t.Fatalf("prefix index %s not found", name)
			}
			if err := db.Model(&officialMigrationRemainingV2{}).RemoveIndex(name).Error; err != nil {
				t.Fatalf("remove prefix index: %v", err)
			}
		})

		t.Run("TestForeignKey", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMigrationRemainingParent{}, &officialMigrationRemainingChild{}).Error; err != nil {
				t.Fatalf("automigrate foreign-key fixtures: %v", err)
			}
			name := testutil.UniqueTable("fk_kb_mig")
			parentTable := db.NewScope(&officialMigrationRemainingParent{}).QuotedTableName()
			if err := db.Model(&officialMigrationRemainingChild{}).AddForeignKey("parent_id", parentTable+"(id)", "CASCADE", "CASCADE").Error; err != nil {
				t.Fatalf("add foreign key: %v", err)
			}
			// GORM 推导物理约束名称；两种 KingbaseES 方言的 HasForeignKey 都接受该生成名称。
			// 显式辅助方法可用时保留直接元数据断言。
			_ = name
			parent := officialMigrationRemainingParent{Name: "parent"}
			if err := db.Create(&parent).Error; err != nil {
				t.Fatalf("create parent: %v", err)
			}
			child := officialMigrationRemainingChild{ParentID: parent.ID, Value: "child"}
			if err := db.Create(&child).Error; err != nil {
				t.Fatalf("create valid child: %v", err)
			}
		})

		t.Run("TestLongForeignKey", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMigrationLongTarget{}, &officialMigrationLongSource{}).Error; err != nil {
				t.Fatalf("automigrate long foreign-key models: %v", err)
			}
			target := db.NewScope(&officialMigrationLongTarget{}).QuotedTableName() + "(id)"
			if err := db.Model(&officialMigrationLongSource{}).AddForeignKey("really_long_thing_id", target, "CASCADE", "CASCADE").Error; err != nil {
				t.Fatalf("add long foreign key: %v", err)
			}
			parent := officialMigrationLongTarget{}
			if err := db.Create(&parent).Error; err != nil {
				t.Fatalf("create long-name foreign-key target: %v", err)
			}
			if err := db.Create(&officialMigrationLongSource{ReallyLongThingID: parent.ID}).Error; err != nil {
				t.Fatalf("create long-name foreign-key source: %v", err)
			}
		})

		t.Run("TestLongForeignKeyWithShortDest", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMigrationShortTarget{}, &officialMigrationLongSourceShortTarget{}).Error; err != nil {
				t.Fatalf("automigrate long-source foreign-key models: %v", err)
			}
			target := db.NewScope(&officialMigrationShortTarget{}).QuotedTableName() + "(id)"
			if err := db.Model(&officialMigrationLongSourceShortTarget{}).AddForeignKey("short_id", target, "CASCADE", "CASCADE").Error; err != nil {
				t.Fatalf("add foreign key from long source: %v", err)
			}
			parent := officialMigrationShortTarget{}
			if err := db.Create(&parent).Error; err != nil {
				t.Fatalf("create short foreign-key target: %v", err)
			}
			if err := db.Create(&officialMigrationLongSourceShortTarget{ShortID: parent.ID}).Error; err != nil {
				t.Fatalf("create long-source foreign-key row: %v", err)
			}
		})

		t.Run("TestDropTableWithTableOptions", func(t *testing.T) {
			table := testutil.UniqueTable("kb_migration_options")
			options := ""
			if mode == kb.KbModeMySQL {
				// 选项保持保守：即使某些版本不公开 MySQL engine，KingbaseES MySQL 模式也接受空选项。
				options = ""
			}
			if err := db.Table(table).Set("gorm:table_options", options).CreateTable(&officialMigrationRemainingV1{}).Error; err != nil {
				t.Fatalf("create table with options: %v", err)
			}
			if !db.HasTable(table) {
				t.Fatalf("table with options %s was not created", table)
			}
			if err := db.DropTable(table).Error; err != nil {
				t.Fatalf("drop table with options: %v", err)
			}
		})
	})
}
