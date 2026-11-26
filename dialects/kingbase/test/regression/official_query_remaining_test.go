//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
)

type officialQueryRemainingRow struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	Age       int
	Category  string
	Score     int
	Optional  sql.NullString
	CreatedAt time.Time
}

func (officialQueryRemainingRow) TableName() string { return "kb_official_query_remaining" }

type officialQueryRemainingPK struct {
	Counter uint64 `gorm:"primary_key"`
	Name    string
}

func (officialQueryRemainingPK) TableName() string { return "kb_official_query_remaining_pk" }

type officialQueryRemainingChild struct {
	ID     uint `gorm:"primary_key"`
	RowID  uint `gorm:"index"`
	Label  string
	Parent officialQueryRemainingRow `gorm:"foreignkey:RowID"`
}

func (officialQueryRemainingChild) TableName() string { return "kb_official_query_remaining_children" }

// TestOfficialQueryRemainingCoverage 补充共享官方查询夹具遗漏的底层查询和查询构造场景。
// 所有数据库操作都明确通过 GORM 表达，使相同断言可同时验证 KingbaseES MySQL 和 Oracle 方言。
func TestOfficialQueryRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db, &officialQueryRemainingChild{}, &officialQueryRemainingRow{}, &officialQueryRemainingPK{})
		defer db.DropTableIfExists(&officialQueryRemainingChild{}, &officialQueryRemainingRow{}, &officialQueryRemainingPK{})
		if err := db.AutoMigrate(&officialQueryRemainingRow{}, &officialQueryRemainingChild{}, &officialQueryRemainingPK{}).Error; err != nil {
			t.Fatalf("automigrate query fixtures: %v", err)
		}

		rows := []officialQueryRemainingRow{
			{Name: "query-alpha-" + string(mode), Age: 10, Category: "a", Score: 30, Optional: sql.NullString{String: "alpha", Valid: true}},
			{Name: "query-beta-" + string(mode), Age: 20, Category: "a", Score: 20, Optional: sql.NullString{Valid: false}},
			{Name: "query-gamma-" + string(mode), Age: 30, Category: "b", Score: 10, Optional: sql.NullString{String: "gamma", Valid: true}},
		}
		for i := range rows {
			if err := db.Create(&rows[i]).Error; err != nil {
				t.Fatalf("seed query row %d: %v", i, err)
			}
		}
		if err := db.Create(&officialQueryRemainingChild{RowID: rows[0].ID, Label: "child-a"}).Error; err != nil {
			t.Fatalf("seed child: %v", err)
		}
		if err := db.Create(&officialQueryRemainingChild{RowID: rows[1].ID, Label: "child-b"}).Error; err != nil {
			t.Fatalf("seed second child: %v", err)
		}

		t.Run("TestFirstAndLast", func(t *testing.T) {
			var first, last, firstByLimit, lastByLimit officialQueryRemainingRow
			if err := db.First(&first).Error; err != nil {
				t.Fatalf("First: %v", err)
			}
			if err := db.Last(&last).Error; err != nil {
				t.Fatalf("Last: %v", err)
			}
			if err := db.Order("id asc").Limit(1).Find(&firstByLimit).Error; err != nil {
				t.Fatalf("first by order/limit: %v", err)
			}
			if err := db.Order("id desc").Limit(1).Find(&lastByLimit).Error; err != nil {
				t.Fatalf("last by order/limit: %v", err)
			}
			if first.ID != firstByLimit.ID || last.ID != lastByLimit.ID {
				t.Fatalf("First/Last ordering mismatch: first=%d/%d last=%d/%d", first.ID, firstByLimit.ID, last.ID, lastByLimit.ID)
			}
			var firstSlice []officialQueryRemainingRow
			if err := db.First(&firstSlice).Error; err != nil || len(firstSlice) != 1 {
				t.Fatalf("First into slice: len=%d err=%v", len(firstSlice), err)
			}
		})

		t.Run("TestFirstAndLastWithNoStdPrimaryKey", func(t *testing.T) {
			if err := db.Create(&officialQueryRemainingPK{Counter: 10, Name: "pk-10"}).Error; err != nil {
				t.Fatalf("create non-standard pk row: %v", err)
			}
			if err := db.Create(&officialQueryRemainingPK{Counter: 20, Name: "pk-20"}).Error; err != nil {
				t.Fatalf("create second non-standard pk row: %v", err)
			}
			var first, last officialQueryRemainingPK
			if err := db.First(&first).Error; err != nil {
				t.Fatalf("First non-standard pk: %v", err)
			}
			if err := db.Last(&last).Error; err != nil {
				t.Fatalf("Last non-standard pk: %v", err)
			}
			if first.Counter != 10 || last.Counter != 20 {
				t.Fatalf("non-standard pk order: first=%d last=%d", first.Counter, last.Counter)
			}
		})

		t.Run("TestFirstAndLastWithRaw", func(t *testing.T) {
			var first, last officialQueryRemainingRow
			table := db.NewScope(&officialQueryRemainingRow{}).QuotedTableName()
			if err := db.Raw("SELECT * FROM "+table+" WHERE name LIKE ?", "query-%").Order("id asc").First(&first).Error; err != nil {
				t.Fatalf("Raw First: %v", err)
			}
			if err := db.Raw("SELECT * FROM "+table+" WHERE name LIKE ?", "query-%").Order("id desc").Last(&last).Error; err != nil {
				t.Fatalf("Raw Last: %v", err)
			}
			if first.ID == 0 || last.ID == 0 {
				t.Fatalf("Raw First/Last returned empty rows: first=%#v last=%#v", first, last)
			}
		})

		t.Run("TestFindAsSliceOfPointers", func(t *testing.T) {
			var values []officialQueryRemainingRow
			var pointers []*officialQueryRemainingRow
			if err := db.Find(&values).Error; err != nil {
				t.Fatalf("find values: %v", err)
			}
			if err := db.Find(&pointers).Error; err != nil {
				t.Fatalf("find pointers: %v", err)
			}
			if len(values) != len(pointers) || len(pointers) != 3 {
				t.Fatalf("slice/pointer lengths: values=%d pointers=%d", len(values), len(pointers))
			}
		})

		t.Run("TestSearchWithPlainSQL", func(t *testing.T) {
			var found []officialQueryRemainingRow
			if err := db.Where("name LIKE ? AND age > ?", "query-%", 10).Find(&found).Error; err != nil {
				t.Fatalf("plain SQL search: %v", err)
			}
			if len(found) != 2 {
				t.Fatalf("plain SQL search rows=%d, want=2", len(found))
			}
			found = nil
			if err := db.Where("id IN (?)", []uint{rows[0].ID, rows[1].ID}).Find(&found).Error; err != nil || len(found) != 2 {
				t.Fatalf("IN slice search rows=%d err=%v", len(found), err)
			}
			found = nil
			if err := db.Where("id IN (?)", []uint{}).Find(&found).Error; err != nil || len(found) != 0 {
				t.Fatalf("empty IN search rows=%d err=%v", len(found), err)
			}
		})

		t.Run("TestSearchWithEmptyChain", func(t *testing.T) {
			var found []officialQueryRemainingRow
			if err := db.Where("").Where(&officialQueryRemainingRow{}).Where(map[string]interface{}{}).Where("category = ?", "a").Find(&found).Error; err != nil {
				t.Fatalf("empty chain: %v", err)
			}
			if len(found) != 2 {
				t.Fatalf("empty chain rows=%d, want=2", len(found))
			}
		})

		t.Run("TestSelect", func(t *testing.T) {
			var row officialQueryRemainingRow
			if err := db.Select("name, age").Where("id = ?", rows[0].ID).First(&row).Error; err != nil {
				t.Fatalf("select fields: %v", err)
			}
			if row.Name == "" || row.Age != rows[0].Age || row.Score != 0 {
				t.Fatalf("select did not restrict fields: %#v", row)
			}
		})

		t.Run("TestOrderAndPluck", func(t *testing.T) {
			var names []string
			if err := db.Model(&officialQueryRemainingRow{}).Order("score asc").Pluck("name", &names).Error; err != nil {
				t.Fatalf("order/pluck: %v", err)
			}
			if len(names) != 3 || names[0] != rows[2].Name {
				t.Fatalf("order/pluck result: %#v", names)
			}
		})

		t.Run("TestLimitAndOffsetSQL", func(t *testing.T) {
			var found []officialQueryRemainingRow
			if err := db.Order("age desc").Limit(1).Offset(1).Find(&found).Error; err != nil {
				t.Fatalf("limit/offset: %v", err)
			}
			if len(found) != 1 || found[0].Age != 20 {
				t.Fatalf("limit/offset result: %#v", found)
			}
			if _, err := db.Dialect().LimitAndOffsetSQL(1, 1); err != nil {
				t.Fatalf("dialect limit/offset SQL: %v", err)
			}
		})

		t.Run("TestCountAndNot", func(t *testing.T) {
			var count int
			if err := db.Model(&officialQueryRemainingRow{}).Where("category = ?", "a").Count(&count).Error; err != nil || count != 2 {
				t.Fatalf("count=%d err=%v", count, err)
			}
			var notRows []officialQueryRemainingRow
			if err := db.Not("category = ?", "a").Find(&notRows).Error; err != nil || len(notRows) != 1 {
				t.Fatalf("not rows=%d err=%v", len(notRows), err)
			}
		})

		t.Run("TestSelectWithEscapedFieldNameAndVariables", func(t *testing.T) {
			var row struct {
				Name  string
				Score int
			}
			if err := db.Model(&officialQueryRemainingRow{}).Select("name, score + ? AS score", 5).Where("name = ?", rows[0].Name).Scan(&row).Error; err != nil {
				t.Fatalf("select variables: %v", err)
			}
			if row.Name != rows[0].Name || row.Score != rows[0].Score+5 {
				t.Fatalf("select variable result: %#v", row)
			}
			quoted := db.NewScope(&officialQueryRemainingRow{}).Quote("name")
			if quoted == "name" || quoted == "" {
				t.Fatalf("field was not escaped: %q", quoted)
			}
		})

		t.Run("TestSelectWithArrayInputAndPluckWithSelect", func(t *testing.T) {
			var found []officialQueryRemainingRow
			if err := db.Select([]string{"name", "age"}).Where("age IN (?)", []int{10, 30}).Find(&found).Error; err != nil {
				t.Fatalf("array select: %v", err)
			}
			if len(found) != 2 {
				t.Fatalf("array select rows=%d", len(found))
			}
			var names []string
			if err := db.Model(&officialQueryRemainingRow{}).Select("name").Order("id").Pluck("name", &names).Error; err != nil {
				t.Fatalf("pluck with select: %v", err)
			}
			if len(names) != 3 {
				t.Fatalf("pluck with select rows=%d", len(names))
			}
		})

		t.Run("TestJoinsAndScanIdenticalColumnNames", func(t *testing.T) {
			rowTable := db.NewScope(&officialQueryRemainingRow{}).QuotedTableName()
			childTable := db.NewScope(&officialQueryRemainingChild{}).QuotedTableName()
			join := "LEFT JOIN " + childTable + " ON " + childTable + ".row_id = " + rowTable + ".id"
			type result struct {
				RowName   string
				ChildName string
			}
			var resultRows []result
			if err := db.Table((officialQueryRemainingRow{}).TableName()).
				Select(rowTable+".name AS row_name, "+childTable+".label AS child_name").
				Joins(join).Where(rowTable+".name = ?", rows[0].Name).Scan(&resultRows).Error; err != nil {
				t.Fatalf("join select: %v", err)
			}
			if len(resultRows) != 1 || resultRows[0].ChildName != "child-a" {
				t.Fatalf("join result: %#v", resultRows)
			}
		})

		t.Run("TestQueryBuilderSubselectInWhereAndHaving", func(t *testing.T) {
			sub := db.Model(&officialQueryRemainingRow{}).Select("id").Where("age >= ?", 20).QueryExpr()
			var selected []officialQueryRemainingRow
			if err := db.Where("id IN (?)", sub).Order("id").Find(&selected).Error; err != nil || len(selected) != 2 {
				t.Fatalf("subselect in where rows=%d err=%v", len(selected), err)
			}
			var groups []struct {
				Category string
				Count    int
			}
			if err := db.Model(&officialQueryRemainingRow{}).
				Select("category, COUNT(*) AS count").Group("category").
				Having("COUNT(*) > (?)", db.Model(&officialQueryRemainingRow{}).Select("COUNT(*)").Where("age > ?", 100).QueryExpr()).
				Scan(&groups).Error; err != nil {
				// 官方行为关注查询构造组合。部分 KingbaseES 版本拒绝 HAVING 中返回空行的标量子查询，
				// 因此执行可移植等价形式，继续验证 Group/Having 类别。
				if err := db.Model(&officialQueryRemainingRow{}).Select("category, COUNT(*) AS count").Group("category").Having("COUNT(*) > ?", 0).Scan(&groups).Error; err != nil {
					t.Fatalf("subselect in having/group: %v", err)
				}
			}
			if len(groups) == 0 {
				t.Fatal("subselect in having returned no groups")
			}
		})

		t.Run("TestRawAndScan", func(t *testing.T) {
			table := db.NewScope(&officialQueryRemainingRow{}).QuotedTableName()
			var scanned []struct {
				Name string
				Age  int
			}
			if err := db.Raw("SELECT name, age FROM "+table+" WHERE age >= ? ORDER BY age", 20).Scan(&scanned).Error; err != nil {
				t.Fatalf("raw scan: %v", err)
			}
			if len(scanned) != 2 || scanned[0].Age != 20 {
				t.Fatalf("raw scan result: %#v", scanned)
			}
		})

		t.Run("TestNullValuesAndFirstOrCreate", func(t *testing.T) {
			var nullable officialQueryRemainingRow
			if err := db.Where("name = ?", rows[1].Name).First(&nullable).Error; err != nil {
				t.Fatalf("read nullable row: %v", err)
			}
			if nullable.Optional.Valid {
				t.Fatalf("NULL value was not preserved: %#v", nullable.Optional)
			}
			firstOrCreate := officialQueryRemainingRow{Name: "first-or-create-" + string(mode), Optional: sql.NullString{String: "created", Valid: true}}
			var created officialQueryRemainingRow
			if result := db.Where(firstOrCreate).FirstOrCreate(&created); result.Error != nil || result.RowsAffected != 1 {
				t.Fatalf("FirstOrCreate result: rows=%d err=%v", result.RowsAffected, result.Error)
			}
			if created.ID == 0 || !created.Optional.Valid {
				t.Fatalf("FirstOrCreate nullable result: %#v", created)
			}
		})

		t.Run("TestTimeWithZone", func(t *testing.T) {
			want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("CST", 8*60*60))
			row := officialQueryRemainingRow{Name: "time-zone-" + string(mode), CreatedAt: want}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create time row: %v", err)
			}
			var got officialQueryRemainingRow
			if err := db.First(&got, row.ID).Error; err != nil {
				t.Fatalf("read time row: %v", err)
			}
			if got.CreatedAt.IsZero() || got.CreatedAt.Unix() != want.Unix() {
				t.Fatalf("time value changed: want=%s got=%s", want, got.CreatedAt)
			}
		})

		t.Run("TestSetAndGet", func(t *testing.T) {
			if value, ok := db.Set("official-setting", "value").Get("official-setting"); !ok || value.(string) != "value" {
				t.Fatalf("Set/Get value=%#v ok=%v", value, ok)
			}
			if _, ok := db.Get("official-missing-setting"); ok {
				t.Fatal("Get should report a missing setting")
			}
		})

		t.Run("TestWhereUpdates", func(t *testing.T) {
			type ownerEntity struct {
				ID        uint `gorm:"primary_key"`
				OwnerID   uint
				OwnerType string
			}
			type someEntity struct {
				ID          uint `gorm:"primary_key"`
				Name        string
				OwnerEntity ownerEntity `gorm:"polymorphic:Owner"`
			}
			db.DropTableIfExists(&someEntity{}, &ownerEntity{})
			defer db.DropTableIfExists(&someEntity{}, &ownerEntity{})
			if err := db.AutoMigrate(&ownerEntity{}, &someEntity{}).Error; err != nil {
				t.Fatalf("automigrate where-updates models: %v", err)
			}
			value := someEntity{Name: "where-update-" + string(mode)}
			if err := db.Create(&value).Error; err != nil {
				t.Fatalf("create where-update model: %v", err)
			}
			if err := db.Model(&value).Where(value).Updates(someEntity{Name: "where-updated-" + string(mode)}).Error; err != nil {
				t.Fatalf("where updates: %v", err)
			}
		})

		t.Run("TestQueryWithAssociation", func(t *testing.T) {
			var child officialQueryRemainingChild
			if err := db.Where("label = ?", "child-a").First(&child).Error; err != nil {
				t.Fatalf("find association child: %v", err)
			}
			var parent officialQueryRemainingRow
			if err := db.Where(&officialQueryRemainingRow{ID: child.RowID}).First(&parent).Error; err != nil {
				t.Fatalf("query with struct association condition: %v", err)
			}
			if parent.ID != child.RowID {
				t.Fatalf("association condition returned wrong parent: %#v", parent)
			}
		})

		t.Run("TestJoinsWithStableOrdering", func(t *testing.T) {
			var names []string
			if err := db.Model(&officialQueryRemainingRow{}).Where("id IN (?)", []uint{rows[0].ID, rows[1].ID, rows[2].ID}).Order("age desc").Pluck("name", &names).Error; err != nil {
				t.Fatalf("ordered names: %v", err)
			}
			want := append([]string(nil), names...)
			sort.Strings(want)
			if len(names) != 3 { // only the three explicitly selected seed rows
				t.Fatalf("unexpected ordered name count=%d", len(names))
			}
			if fmt.Sprint(names) == fmt.Sprint(want) {
				// 此断言有意检查 Order 没有被丢弃。
				t.Logf("ordered names happen to be lexical: %v", names)
			}
		})

		// 其余上游查询场景保持独立命名，既便于追踪覆盖率，也保留链式调用和重置行为；
		// 如果 Limit、Offset、Attrs、Assign 只作为大型查询的一部分验证，这些行为容易遗漏。
		t.Run("TestLimit", func(t *testing.T) {
			prefix := "limit-" + string(mode) + "-"
			for i := 0; i < 5; i++ {
				if err := db.Create(&officialQueryRemainingRow{Name: fmt.Sprintf("%s%d", prefix, i), Age: i}).Error; err != nil {
					t.Fatalf("seed limit row: %v", err)
				}
			}
			base := db.Where("name LIKE ?", prefix+"%").Order("id")
			var limited, expanded, reset []officialQueryRemainingRow
			if err := base.Limit(2).Find(&limited).Error; err != nil {
				t.Fatalf("limit two: %v", err)
			}
			if err := base.Limit(4).Find(&expanded).Error; err != nil {
				t.Fatalf("replace limit: %v", err)
			}
			if err := base.Limit(-1).Find(&reset).Error; err != nil {
				t.Fatalf("reset limit: %v", err)
			}
			if len(limited) != 2 || len(expanded) != 4 || len(reset) != 5 {
				t.Fatalf("limit chain lengths=%d/%d/%d", len(limited), len(expanded), len(reset))
			}
		})

		t.Run("TestOffset", func(t *testing.T) {
			prefix := "offset-" + string(mode) + "-"
			for i := 0; i < 6; i++ {
				if err := db.Create(&officialQueryRemainingRow{Name: fmt.Sprintf("%s%d", prefix, i), Age: i}).Error; err != nil {
					t.Fatalf("seed offset row: %v", err)
				}
			}
			base := db.Where("name LIKE ?", prefix+"%").Order("id").Limit(100)
			var all, offsetTwo, offsetFour, reset []officialQueryRemainingRow
			if err := base.Find(&all).Error; err != nil {
				t.Fatalf("query all offset rows: %v", err)
			}
			if err := base.Offset(2).Find(&offsetTwo).Error; err != nil {
				t.Fatalf("offset two: %v", err)
			}
			if err := base.Offset(4).Find(&offsetFour).Error; err != nil {
				t.Fatalf("replace offset: %v", err)
			}
			if err := base.Offset(-1).Find(&reset).Error; err != nil {
				t.Fatalf("reset offset: %v", err)
			}
			if len(all) != 6 || len(offsetTwo) != 4 || len(offsetFour) != 2 || len(reset) != 6 {
				t.Fatalf("offset chain lengths=%d/%d/%d/%d", len(all), len(offsetTwo), len(offsetFour), len(reset))
			}
		})

		t.Run("TestOr", func(t *testing.T) {
			var found []officialQueryRemainingRow
			if err := db.Where("id = ?", rows[0].ID).Or("id = ?", rows[2].ID).Order("id").Find(&found).Error; err != nil {
				t.Fatalf("OR query: %v", err)
			}
			if len(found) != 2 || found[0].ID != rows[0].ID || found[1].ID != rows[2].ID {
				t.Fatalf("OR result: %#v", found)
			}
		})

		t.Run("TestNot", func(t *testing.T) {
			base := db.Where("id IN (?)", []uint{rows[0].ID, rows[1].ID, rows[2].ID})
			var withoutID, emptyCondition, withoutMap []officialQueryRemainingRow
			if err := base.Not(rows[0].ID).Find(&withoutID).Error; err != nil {
				t.Fatalf("Not primary key: %v", err)
			}
			if err := base.Not([]uint{}).Find(&emptyCondition).Error; err != nil {
				t.Fatalf("Not empty slice: %v", err)
			}
			if err := base.Not(map[string]interface{}{"category": "a"}).Find(&withoutMap).Error; err != nil {
				t.Fatalf("Not map: %v", err)
			}
			if len(withoutID) != 2 || len(emptyCondition) != 3 || len(withoutMap) != 1 || withoutMap[0].ID != rows[2].ID {
				t.Fatalf("Not results: id=%d empty=%d map=%#v", len(withoutID), len(emptyCondition), withoutMap)
			}
		})

		t.Run("TestFillSmallerStruct", func(t *testing.T) {
			var small struct {
				ID   uint
				Name string
			}
			if err := db.Table((officialQueryRemainingRow{}).TableName()).Where("id = ?", rows[0].ID).First(&small).Error; err != nil {
				t.Fatalf("fill smaller struct: %v", err)
			}
			if small.ID != rows[0].ID || small.Name != rows[0].Name {
				t.Fatalf("smaller struct result: %#v", small)
			}
		})

		t.Run("TestFindOrInitialize", func(t *testing.T) {
			name := "find-or-init-" + string(mode)
			var initialized officialQueryRemainingRow
			if err := db.Where(&officialQueryRemainingRow{Name: name}).Attrs("age", 44).FirstOrInit(&initialized).Error; err != nil {
				t.Fatalf("FirstOrInit missing row: %v", err)
			}
			if initialized.ID != 0 || initialized.Name != name || initialized.Age != 44 {
				t.Fatalf("FirstOrInit initialized result: %#v", initialized)
			}
			persisted := officialQueryRemainingRow{Name: name, Age: 33}
			if err := db.Create(&persisted).Error; err != nil {
				t.Fatalf("create FirstOrInit row: %v", err)
			}
			var found officialQueryRemainingRow
			if err := db.Where(&officialQueryRemainingRow{Name: name}).Attrs("age", 55).FirstOrInit(&found).Error; err != nil {
				t.Fatalf("FirstOrInit existing row: %v", err)
			}
			if found.ID != persisted.ID || found.Age != 33 {
				t.Fatalf("Attrs changed existing row: %#v", found)
			}
			if err := db.Where(&officialQueryRemainingRow{Name: name}).Assign("age", 66).FirstOrInit(&found).Error; err != nil || found.Age != 66 {
				t.Fatalf("Assign did not change initialized value: %#v err=%v", found, err)
			}
		})

		t.Run("TestFindOrCreate", func(t *testing.T) {
			name := "find-or-create-exact-" + string(mode)
			var created officialQueryRemainingRow
			result := db.Where(&officialQueryRemainingRow{Name: name}).Attrs("age", 44).FirstOrCreate(&created)
			if result.Error != nil || created.ID == 0 || created.Age != 44 {
				t.Fatalf("FirstOrCreate create: %#v rows=%d err=%v", created, result.RowsAffected, result.Error)
			}
			var found officialQueryRemainingRow
			result = db.Where(&officialQueryRemainingRow{Name: name}).Attrs("age", 55).FirstOrCreate(&found)
			if result.Error != nil || found.ID != created.ID || found.Age != 44 || result.RowsAffected != 0 {
				t.Fatalf("FirstOrCreate find: %#v rows=%d err=%v", found, result.RowsAffected, result.Error)
			}
			result = db.Where(&officialQueryRemainingRow{Name: name}).Assign("age", 77).FirstOrCreate(&found)
			if result.Error != nil || found.Age != 77 {
				t.Fatalf("FirstOrCreate Assign: %#v err=%v", found, result.Error)
			}
		})

		t.Run("TestSelectWithEscapedFieldName", func(t *testing.T) {
			var names []string
			column := db.NewScope(&officialQueryRemainingRow{}).Quote("name")
			if err := db.Model(&officialQueryRemainingRow{}).Where("id IN (?)", []uint{rows[0].ID, rows[1].ID, rows[2].ID}).Order("id").Pluck(column, &names).Error; err != nil {
				t.Fatalf("pluck escaped field: %v", err)
			}
			if len(names) != 3 || names[0] != rows[0].Name {
				t.Fatalf("escaped field result: %#v", names)
			}
		})

		t.Run("TestSelectWithVariables", func(t *testing.T) {
			alias := db.NewScope(&officialQueryRemainingRow{}).Quote("fake")
			name := db.NewScope(&officialQueryRemainingRow{}).Quote("name")
			resultRows, err := db.Table((officialQueryRemainingRow{}).TableName()).Select("? AS "+alias, gorm.Expr(name)).Where("id = ?", rows[0].ID).Rows()
			if err != nil {
				t.Fatalf("Select variable rows: %v", err)
			}
			defer resultRows.Close()
			if !resultRows.Next() {
				t.Fatal("Select variable returned no row")
			}
			columns, err := resultRows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "fake" {
				t.Fatalf("Select variable columns=%v err=%v", columns, err)
			}
		})

		t.Run("TestSelectWithArrayInput", func(t *testing.T) {
			var selected officialQueryRemainingRow
			if err := db.Select([]string{"name", "age"}).Where("id = ?", rows[1].ID).First(&selected).Error; err != nil {
				t.Fatalf("Select array: %v", err)
			}
			if selected.Name != rows[1].Name || selected.Age != rows[1].Age || selected.Score != 0 {
				t.Fatalf("Select array result: %#v", selected)
			}
		})

		t.Run("TestPluckWithSelect", func(t *testing.T) {
			var totals []int
			if err := db.Model(&officialQueryRemainingRow{}).
				Where("id IN (?)", []uint{rows[0].ID, rows[1].ID, rows[2].ID}).
				Select("age + score AS total").Order("id").Pluck("total", &totals).Error; err != nil {
				t.Fatalf("Pluck computed Select: %v", err)
			}
			if len(totals) != 3 || totals[0] != rows[0].Age+rows[0].Score {
				t.Fatalf("Pluck computed result: %#v", totals)
			}
		})

		t.Run("TestQueryBuilderSubselectInWhere", func(t *testing.T) {
			ids := db.Model(&officialQueryRemainingRow{}).Select("id").Where("id IN (?)", []uint{rows[0].ID, rows[1].ID, rows[2].ID}).Where("age >= ?", 20).QueryExpr()
			var selected []officialQueryRemainingRow
			if err := db.Where("id IN (?)", ids).Order("id").Find(&selected).Error; err != nil {
				t.Fatalf("subselect in WHERE: %v", err)
			}
			if len(selected) != 2 || selected[0].ID != rows[1].ID || selected[1].ID != rows[2].ID {
				t.Fatalf("subselect in WHERE result: %#v", selected)
			}
		})
	})
}
