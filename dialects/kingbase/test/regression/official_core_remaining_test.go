//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type officialCoreRemainingRow struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	Age       int
	Category  string
	Optional  sql.NullString
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

func (officialCoreRemainingRow) TableName() string { return "kb_official_core_rows" }

type officialCoreRemainingChild struct {
	ID    uint `gorm:"primary_key"`
	RowID uint
	Label string
}

func (officialCoreRemainingChild) TableName() string { return "kb_official_core_children" }

type officialCoreStringPK struct {
	ID   string `gorm:"primary_key;size:32"`
	Name string
}

func (officialCoreStringPK) TableName() string { return "kb_official_core_string_pk" }

type officialCoreUintPK struct {
	Counter uint64 `gorm:"primary_key"`
	Name    string
}

func (officialCoreUintPK) TableName() string { return "kb_official_core_uint_pk" }

type officialCoreCustomID uint

type officialCoreCustomPK struct {
	ID   officialCoreCustomID `gorm:"primary_key"`
	Name string
}

func (officialCoreCustomPK) TableName() string { return "kb_official_core_custom_pk" }

func seedOfficialCoreRows(t *testing.T, db *gorm.DB, mode kb.KbMode) []officialCoreRemainingRow {
	t.Helper()
	rows := []officialCoreRemainingRow{
		{Name: "core-alpha-" + string(mode), Age: 10, Category: "a", Optional: sql.NullString{String: "alpha", Valid: true}},
		{Name: "core-beta-" + string(mode), Age: 20, Category: "a", Optional: sql.NullString{Valid: false}},
		{Name: "core-gamma-" + string(mode), Age: 30, Category: "b", Optional: sql.NullString{String: "gamma", Valid: true}},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed core row %d: %v", i, err)
		}
	}
	children := []officialCoreRemainingChild{{RowID: rows[0].ID, Label: "child-1"}, {RowID: rows[0].ID, Label: "child-2"}, {RowID: rows[1].ID, Label: "child-3"}}
	for i := range children {
		if err := db.Create(&children[i]).Error; err != nil {
			t.Fatalf("seed core child %d: %v", i, err)
		}
	}
	return rows
}

// TestOfficialCoreRemainingCoverage 收纳 main_test.go、query_test.go 和 delete_test.go 中
// 不适合归入关联、预加载或迁移类别的数据库场景。
func TestOfficialCoreRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db, &officialCoreRemainingChild{}, &officialCoreRemainingRow{}, &officialCoreStringPK{}, &officialCoreUintPK{}, &officialCoreCustomPK{})
		defer db.DropTableIfExists(&officialCoreRemainingChild{}, &officialCoreRemainingRow{}, &officialCoreStringPK{}, &officialCoreUintPK{}, &officialCoreCustomPK{})
		if err := db.AutoMigrate(&officialCoreRemainingRow{}, &officialCoreRemainingChild{}, &officialCoreStringPK{}, &officialCoreUintPK{}, &officialCoreCustomPK{}).Error; err != nil {
			t.Fatalf("automigrate core fixtures: %v", err)
		}
		rows := seedOfficialCoreRows(t, db, mode)

		t.Run("TestStringPrimaryKey", func(t *testing.T) {
			row := officialCoreStringPK{ID: "uuid-" + string(mode), Name: "string-pk"}
			if err := db.Save(&row).Error; err != nil {
				t.Fatalf("save string primary key: %v", err)
			}
			row.Name = "string-pk-updated"
			if err := db.Save(&row).Error; err != nil {
				t.Fatalf("update string primary key: %v", err)
			}
			var loaded officialCoreStringPK
			if err := db.First(&loaded, "id = ?", row.ID).Error; err != nil || loaded.ID != row.ID || loaded.Name != row.Name {
				t.Fatalf("string primary key result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestStringPrimaryKeyForNumericValueStartingWithZero", func(t *testing.T) {
			row := officialCoreStringPK{ID: "00501", Name: "leading-zero"}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create leading-zero primary key: %v", err)
			}
			var loaded officialCoreStringPK
			if err := db.First(&loaded, "id = ?", "00501").Error; err != nil || loaded.ID != "00501" {
				t.Fatalf("leading-zero primary key result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestUIntPrimaryKey", func(t *testing.T) {
			if err := db.Create(&officialCoreUintPK{Counter: 101, Name: "uint-pk"}).Error; err != nil {
				t.Fatalf("create uint primary key: %v", err)
			}
			var loaded officialCoreUintPK
			if err := db.First(&loaded, "counter = ?", uint64(101)).Error; err != nil || loaded.Counter != 101 {
				t.Fatalf("uint primary key result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestCustomizedTypePrimaryKey", func(t *testing.T) {
			row := officialCoreCustomPK{Name: "custom-pk"}
			if err := db.Create(&row).Error; err != nil || row.ID == 0 {
				t.Fatalf("create custom primary key: id=%d err=%v", row.ID, err)
			}
			var loaded officialCoreCustomPK
			if err := db.First(&loaded, "id = ?", row.ID).Error; err != nil || loaded.Name != row.Name {
				t.Fatalf("custom primary key result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestExceptionsWithInvalidSql", func(t *testing.T) {
			var values []string
			if err := db.Table((officialCoreRemainingRow{}).TableName()).Where("missing_alias.missing_column = ?", 1).Pluck("name", &values).Error; err == nil {
				t.Fatal("invalid SQL should return an error")
			}
			var count int
			if err := db.Model(&officialCoreRemainingRow{}).Count(&count).Error; err != nil || count != 3 {
				t.Fatalf("invalid SQL affected valid data: count=%d err=%v", count, err)
			}
		})

		t.Run("TestSetTable", func(t *testing.T) {
			table := testutil.UniqueTable("kb_official_set_table")
			defer db.DropTableIfExists(table)
			if err := db.Table(table).CreateTable(&officialCoreRemainingRow{}).Error; err != nil {
				t.Fatalf("CreateTable with Table: %v", err)
			}
			row := officialCoreRemainingRow{Name: "set-table"}
			if err := db.Table(table).Create(&row).Error; err != nil {
				t.Fatalf("create through Table: %v", err)
			}
			var loaded officialCoreRemainingRow
			if err := db.Table(table).First(&loaded, row.ID).Error; err != nil || loaded.Name != row.Name {
				t.Fatalf("query through Table: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestHasTable", func(t *testing.T) {
			if !db.HasTable((officialCoreRemainingRow{}).TableName()) {
				t.Fatal("HasTable did not find an existing table")
			}
			if db.HasTable(testutil.UniqueTable("kb_official_missing")) {
				t.Fatal("HasTable found a missing table")
			}
		})

		t.Run("TestNullValues", func(t *testing.T) {
			var valid, invalid officialCoreRemainingRow
			if err := db.Where("id = ?", rows[0].ID).First(&valid).Error; err != nil {
				t.Fatalf("read valid nullable value: %v", err)
			}
			if err := db.Where("id = ?", rows[1].ID).First(&invalid).Error; err != nil {
				t.Fatalf("read NULL value: %v", err)
			}
			if !valid.Optional.Valid || valid.Optional.String != "alpha" || invalid.Optional.Valid {
				t.Fatalf("nullable values mismatch: valid=%#v invalid=%#v", valid.Optional, invalid.Optional)
			}
		})

		t.Run("TestNullValuesWithFirstOrCreate", func(t *testing.T) {
			where := officialCoreRemainingRow{Name: "nullable-first-or-create-" + string(mode), Optional: sql.NullString{String: "value", Valid: true}}
			var loaded officialCoreRemainingRow
			defer deleteOfficialCoreFixture(t, db, where.Name)
			result := db.Where(where).FirstOrCreate(&loaded)
			if result.Error != nil || result.RowsAffected != 1 || !loaded.Optional.Valid {
				t.Fatalf("nullable FirstOrCreate: rows=%d value=%#v err=%v", result.RowsAffected, loaded, result.Error)
			}
		})

		t.Run("TestTransaction", func(t *testing.T) {
			rollbackName := "core-tx-rollback-" + string(mode)
			tx := db.Begin()
			if tx.Error != nil {
				t.Fatalf("begin rollback transaction: %v", tx.Error)
			}
			if _, ok := tx.CommonDB().(*sql.Tx); !ok {
				t.Fatalf("CommonDB is not *sql.Tx: %T", tx.CommonDB())
			}
			if err := tx.Create(&officialCoreRemainingRow{Name: rollbackName}).Error; err != nil {
				_ = tx.Rollback()
				t.Fatalf("create rollback row: %v", err)
			}
			if err := tx.Rollback().Error; err != nil {
				t.Fatalf("rollback: %v", err)
			}
			var count int
			if err := db.Model(&officialCoreRemainingRow{}).Where("name = ?", rollbackName).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("rollback row count=%d err=%v", count, err)
			}

			commitName := "core-tx-commit-" + string(mode)
			defer deleteOfficialCoreFixture(t, db, commitName)
			tx = db.Begin()
			if err := tx.Create(&officialCoreRemainingRow{Name: commitName}).Error; err != nil {
				_ = tx.Rollback()
				t.Fatalf("create commit row: %v", err)
			}
			if err := tx.Commit().Error; err != nil {
				t.Fatalf("commit: %v", err)
			}
			if err := db.Where("name = ?", commitName).First(&officialCoreRemainingRow{}).Error; err != nil {
				t.Fatalf("committed row not found: %v", err)
			}
		})

		t.Run("TestTransaction_NoErrorOnRollbackAfterCommit", func(t *testing.T) {
			defer deleteOfficialCoreFixture(t, db, "commit-rollback-"+string(mode))
			tx := db.Begin()
			if err := tx.Create(&officialCoreRemainingRow{Name: "commit-rollback-" + string(mode)}).Error; err != nil {
				_ = tx.Rollback()
				t.Fatalf("create before commit: %v", err)
			}
			if err := tx.Commit().Error; err != nil {
				t.Fatalf("commit: %v", err)
			}
			if err := tx.Rollback().Error; err != nil {
				t.Fatalf("rollback after commit: %v", err)
			}
		})

		t.Run("TestTransactionReadonly", func(t *testing.T) {
			if !strings.EqualFold(strings.TrimSpace(os.Getenv("KINGBASE_SUPPORTS_READONLY_TX")), "true") && os.Getenv("KINGBASE_SUPPORTS_READONLY_TX") != "1" {
				t.Skip("set KINGBASE_SUPPORTS_READONLY_TX=1 when the target Kingbase release enforces read-only transactions")
			}
			tx := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
			if tx.Error != nil {
				t.Fatalf("begin read-only transaction: %v", tx.Error)
			}
			defer tx.Rollback()
			if err := tx.First(&officialCoreRemainingRow{}, rows[0].ID).Error; err != nil {
				t.Fatalf("read in read-only transaction: %v", err)
			}
			if err := tx.Create(&officialCoreRemainingRow{Name: "readonly-write"}).Error; err == nil {
				t.Fatal("write in read-only transaction should fail")
			}
		})

		t.Run("TestRow", func(t *testing.T) {
			var age int
			if err := db.Model(&officialCoreRemainingRow{}).Where("id = ?", rows[1].ID).Select("age").Row().Scan(&age); err != nil || age != 20 {
				t.Fatalf("Row.Scan age=%d err=%v", age, err)
			}
		})

		t.Run("TestRows", func(t *testing.T) {
			sqlRows, err := db.Model(&officialCoreRemainingRow{}).Where("category = ?", "a").Select("name, age").Order("age").Rows()
			if err != nil {
				t.Fatalf("Rows: %v", err)
			}
			defer sqlRows.Close()
			count := 0
			for sqlRows.Next() {
				var name string
				var age int
				if err := sqlRows.Scan(&name, &age); err != nil {
					t.Fatalf("Rows.Scan: %v", err)
				}
				count++
			}
			if count != 2 {
				t.Fatalf("Rows count=%d, want=2", count)
			}
		})

		t.Run("TestScanRows", func(t *testing.T) {
			sqlRows, err := db.Model(&officialCoreRemainingRow{}).Where("category = ?", "a").Select("name, age").Order("age").Rows()
			if err != nil {
				t.Fatalf("Rows for ScanRows: %v", err)
			}
			defer sqlRows.Close()
			count := 0
			for sqlRows.Next() {
				var result struct {
					Name string
					Age  int
				}
				if err := db.ScanRows(sqlRows, &result); err != nil {
					t.Fatalf("ScanRows: %v", err)
				}
				count++
			}
			if count != 2 {
				t.Fatalf("ScanRows count=%d, want=2", count)
			}
		})

		t.Run("TestScan", func(t *testing.T) {
			var result struct {
				Name string
				Age  int
			}
			if err := db.Model(&officialCoreRemainingRow{}).Select("name, age").Where("id = ?", rows[2].ID).Scan(&result).Error; err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if result.Name != rows[2].Name || result.Age != rows[2].Age {
				t.Fatalf("Scan result: %#v", result)
			}
		})

		t.Run("TestRaw", func(t *testing.T) {
			table := db.NewScope(&officialCoreRemainingRow{}).QuotedTableName()
			var result []officialCoreRemainingRow
			if err := db.Raw("SELECT * FROM "+table+" WHERE category = ? ORDER BY age", "a").Scan(&result).Error; err != nil {
				t.Fatalf("Raw Scan: %v", err)
			}
			if len(result) != 2 {
				t.Fatalf("Raw rows=%d, want=2", len(result))
			}
		})

		t.Run("TestGroup", func(t *testing.T) {
			groupRows, err := db.Model(&officialCoreRemainingRow{}).Select("category").Group("category").Rows()
			if err != nil {
				t.Fatalf("Group.Rows: %v", err)
			}
			defer groupRows.Close()
			count := 0
			for groupRows.Next() {
				count++
			}
			if count != 2 {
				t.Fatalf("group count=%d, want=2", count)
			}
		})

		t.Run("TestJoins", func(t *testing.T) {
			rowTable := db.NewScope(&officialCoreRemainingRow{}).QuotedTableName()
			childTable := db.NewScope(&officialCoreRemainingChild{}).QuotedTableName()
			join := "LEFT JOIN " + childTable + " ON " + childTable + ".row_id = " + rowTable + ".id"
			var joined []officialCoreRemainingRow
			if err := db.Table((officialCoreRemainingRow{}).TableName()).Joins(join).Where(rowTable+".id = ?", rows[0].ID).Find(&joined).Error; err != nil {
				t.Fatalf("join query: %v", err)
			}
			if len(joined) != 2 {
				t.Fatalf("joined rows=%d, want=2", len(joined))
			}
		})

		t.Run("TestScanIdenticalColumnNames", func(t *testing.T) {
			rowTable := db.NewScope(&officialCoreRemainingRow{}).QuotedTableName()
			childTable := db.NewScope(&officialCoreRemainingChild{}).QuotedTableName()
			var result []struct {
				RowID   uint `gorm:"column:row_id_value"`
				ChildID uint `gorm:"column:child_id_value"`
			}
			if err := db.Table((officialCoreRemainingRow{}).TableName()).
				Select(rowTable+".id AS row_id_value, "+childTable+".id AS child_id_value").
				Joins("JOIN "+childTable+" ON "+childTable+".row_id = "+rowTable+".id").
				Where(rowTable+".id = ?", rows[0].ID).Scan(&result).Error; err != nil {
				t.Fatalf("scan joined identifiers: %v", err)
			}
			if len(result) != 2 || result[0].RowID != rows[0].ID || result[0].ChildID == result[1].ChildID {
				t.Fatalf("joined identifier scan: %#v", result)
			}
		})

		t.Run("TestJoinsWithSelect", func(t *testing.T) {
			rowTable := db.NewScope(&officialCoreRemainingRow{}).QuotedTableName()
			childTable := db.NewScope(&officialCoreRemainingChild{}).QuotedTableName()
			var result []struct {
				Name  string
				Label string
			}
			if err := db.Table((officialCoreRemainingRow{}).TableName()).
				Select(rowTable+".name, "+childTable+".label").
				Joins("LEFT JOIN "+childTable+" ON "+childTable+".row_id = "+rowTable+".id").
				Where(rowTable+".id = ?", rows[0].ID).Scan(&result).Error; err != nil {
				t.Fatalf("join with select: %v", err)
			}
			sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
			if len(result) != 2 || result[0].Label != "child-1" || result[1].Label != "child-2" {
				t.Fatalf("join with select result: %#v", result)
			}
		})

		t.Run("TestHaving", func(t *testing.T) {
			var result []struct {
				Category string
				Total    int
			}
			if err := db.Model(&officialCoreRemainingRow{}).Select("category, COUNT(*) AS total").Group("category").Having("COUNT(*) > ?", 1).Scan(&result).Error; err != nil {
				t.Fatalf("Having: %v", err)
			}
			if len(result) != 1 || result[0].Category != "a" || result[0].Total != 2 {
				t.Fatalf("Having result: %#v", result)
			}
		})

		t.Run("TestCount", func(t *testing.T) {
			var count int
			if err := db.Model(&officialCoreRemainingRow{}).Where("category = ?", "a").Count(&count).Error; err != nil || count != 2 {
				t.Fatalf("Count=%d err=%v", count, err)
			}
		})

		t.Run("TestCountWithHaving", func(t *testing.T) {
			var count int
			if err := db.Model(&officialCoreRemainingRow{}).Select("category").Group("category").Having("COUNT(*) > ?", 1).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("Count with Having=%d err=%v", count, err)
			}
		})

		t.Run("TestPluck", func(t *testing.T) {
			var names []string
			query := db.Model(&officialCoreRemainingRow{}).Where("name LIKE ?", "core-%").Order("age")
			if err := query.Pluck("name", &names).Error; err != nil || len(names) != 3 {
				t.Fatalf("first Pluck=%v err=%v", names, err)
			}
			names = nil
			if err := query.Pluck("name", &names).Error; err != nil || len(names) != 3 {
				t.Fatalf("second Pluck=%v err=%v", names, err)
			}
		})

		t.Run("TestCountWithQueryOption", func(t *testing.T) {
			var count int
			option := "/* kingbase count query option */"
			if err := db.Model(&officialCoreRemainingRow{}).Where("name = ?", rows[1].Name).Select("id").Set("gorm:query_option", option).Count(&count).Error; err != nil {
				t.Fatalf("Count with query option: %v", err)
			}
			if count != 1 {
				t.Fatalf("Count with query option=%d, want=1", count)
			}
		})

		t.Run("TestQueryBuilderRawQueryWithSubquery", func(t *testing.T) {
			sub := db.Model(&officialCoreRemainingRow{}).Select("id").Where("age >= ?", 20).QueryExpr()
			var selected []officialCoreRemainingRow
			if err := db.Where("id IN (?)", sub).Find(&selected).Error; err != nil || len(selected) != 2 {
				t.Fatalf("raw query with subquery rows=%d err=%v", len(selected), err)
			}
		})

		t.Run("TestQueryBuilderSubselectInHaving", func(t *testing.T) {
			var groups []struct {
				Category string
				Total    int
			}
			sub := db.Model(&officialCoreRemainingRow{}).Select("COUNT(*)").Where("age > ?", 100).QueryExpr()
			if err := db.Model(&officialCoreRemainingRow{}).Select("category, COUNT(*) AS total").Group("category").Having("COUNT(*) > (?)", sub).Scan(&groups).Error; err != nil {
				t.Fatalf("subselect in Having: %v", err)
			}
			if len(groups) != 2 {
				t.Fatalf("subselect in Having groups=%d, want=2", len(groups))
			}
		})

		t.Run("TestQueryHint1", func(t *testing.T) {
			query := "SELECT 1 AS value"
			if mode == kb.KbModeOracle {
				query += " FROM DUAL"
			}
			var result struct{ Value int }
			if err := db.Raw(query).Scan(&result).Error; err != nil || result.Value != 1 {
				t.Fatalf("raw query hint smoke: value=%d err=%v", result.Value, err)
			}
		})

		t.Run("TestQueryHint2", func(t *testing.T) {
			row := officialCoreStringPK{ID: "hint-" + string(mode), Name: "query-hint"}
			if err := db.Set("gorm:query_hint", "/* kingbase-master */").Save(&row).Error; err != nil {
				t.Fatalf("save with query hint: %v", err)
			}
		})

		t.Run("TestFloatColumnPrecision", func(t *testing.T) {
			if mode != kb.KbModeMySQL {
				t.Skip("official precision case targets MySQL-compatible float declarations")
			}
			type floatRow struct {
				ID    string  `gorm:"primary_key;size:32"`
				Value float64 `sql:"type:float(30,5);"`
			}
			table := testutil.UniqueTable("kb_official_float")
			defer db.DropTableIfExists(table)
			if err := db.Table(table).AutoMigrate(&floatRow{}).Error; err != nil {
				t.Fatalf("automigrate float precision row: %v", err)
			}
			row := floatRow{ID: "float", Value: 112.57315}
			if err := db.Table(table).Save(&row).Error; err != nil {
				t.Fatalf("save float precision row: %v", err)
			}
			var loaded floatRow
			if err := db.Table(table).First(&loaded, "id = ?", row.ID).Error; err != nil || loaded.Value != row.Value {
				t.Fatalf("float precision value=%v want=%v err=%v", loaded.Value, row.Value, err)
			}
		})

		t.Run("TestDelete", func(t *testing.T) {
			row := officialCoreRemainingRow{Name: "delete-" + string(mode)}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create delete row: %v", err)
			}
			if err := db.Delete(&row).Error; err != nil {
				t.Fatalf("delete row: %v", err)
			}
			if err := db.First(&officialCoreRemainingRow{}, row.ID).Error; !gorm.IsRecordNotFoundError(err) {
				t.Fatalf("deleted row remains visible: %v", err)
			}
		})

		t.Run("TestSoftDelete", func(t *testing.T) {
			row := officialCoreRemainingRow{Name: "soft-delete-" + string(mode)}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create soft-delete row: %v", err)
			}
			if err := db.Delete(&row).Error; err != nil {
				t.Fatalf("soft delete: %v", err)
			}
			var deleted officialCoreRemainingRow
			if err := db.Unscoped().First(&deleted, row.ID).Error; err != nil || deleted.DeletedAt == nil {
				t.Fatalf("unscoped soft-deleted row: %#v err=%v", deleted, err)
			}
			if err := db.Unscoped().Delete(&deleted).Error; err != nil {
				t.Fatalf("hard delete: %v", err)
			}
		})

		t.Run("TestOpenExistingDB", func(t *testing.T) {
			// 本场景专门验证真实 *sql.DB 的包装。元数据观察器类型为 SQLCommon，
			// 因此使用指向同一隔离 schema 的独立连接，而不在其上调用 DB()。
			existing := testutil.Open(t, mode)
			defer testutil.Close(t, existing)
			wrapped, err := gorm.Open(db.Dialect().GetName(), existing.DB())
			if err != nil {
				t.Fatalf("wrap existing database connection: %v", err)
			}
			var loaded officialCoreRemainingRow
			if err := wrapped.First(&loaded, rows[0].ID).Error; err != nil || loaded.ID != rows[0].ID {
				t.Fatalf("query wrapped connection: %#v err=%v", loaded, err)
			}
		})
	})
}

// 防止修改型子测试改变共享的读取/查询夹具。
func deleteOfficialCoreFixture(t *testing.T, db *gorm.DB, name string) {
	t.Helper()
	if err := db.Unscoped().Where("name = ?", name).Delete(&officialCoreRemainingRow{}).Error; err != nil {
		t.Errorf("cleanup core fixture %s: %v", name, err)
	}
}
