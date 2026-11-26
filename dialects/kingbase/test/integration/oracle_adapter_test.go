//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
	"kingbase.com/gokb"
)

// 这些测试从 GORM 入口执行，并检查实际保存的定义和值。下方原始 SQL 只用于读取状态
// 或准备独立夹具，绝不会用另一条语句替换失败的 ORM 语句。
type oracleAdapterValue struct {
	ID      int64 `gorm:"primary_key"`
	Native  int
	U32     uint32
	U64     uint64
	F24     float64   `gorm:"type:float(24)"`
	F53     float64   `gorm:"type:float(53)"`
	Exact   string    `gorm:"type:decimal(65,30)"`
	Literal string    `gorm:"type:varchar(96);default:'Alpha  COMMENT Bravo';comment:'column description'"`
	JSON    string    `gorm:"type:json"`
	JSONB   string    `gorm:"type:jsonb"`
	Moment  time.Time `gorm:"type:timestamptz(6)"`
}

type oracleAdapterIndexRow struct {
	ID   int64  `gorm:"primary_key"`
	Name string `gorm:"type:varchar2(32);not null"`
}

func oracleStoredType(t *testing.T, db *gorm.DB, table, column string) string {
	t.Helper()
	var typ string
	if err := db.Raw(`SELECT format_type(a.atttypid,a.atttypmod)
FROM pg_attribute a WHERE a.attrelid=to_regclass(?) AND a.attname=? AND NOT a.attisdropped`, table, column).Row().Scan(&typ); err != nil {
		t.Fatalf("读取实际字段定义 %s.%s: %v", table, column, err)
	}
	return typ
}

func oracleRequireCount(t *testing.T, db *gorm.DB, query string, want int, args ...interface{}) {
	t.Helper()
	var count int
	if err := db.Raw(query, args...).Row().Scan(&count); err != nil {
		t.Fatalf("状态核验失败: %v; SQL=%s", err, query)
	}
	if count != want {
		t.Fatalf("状态不匹配: count=%d want=%d; SQL=%s", count, want, query)
	}
}

func oracleExpectedServerError(t *testing.T, db *gorm.DB, code, constraint string, run func() error) {
	t.Helper()
	match := func(err error) bool {
		e, ok := err.(*gokb.Error)
		return ok && e != nil && string(e.Code) == code && (constraint == "" || e.Constraint == constraint)
	}
	log := testutil.DriverLogFor(db)
	start := log.Snapshot()
	err := log.WithExpectedError(match, run)
	if !match(err) || log.ExpectErrors(start, match) != 1 {
		t.Fatalf("负向断言不匹配: want SQLSTATE=%s constraint=%q; actual=%#v", code, constraint, err)
	}
	t.Logf("[预期拒绝已核验] SQLSTATE=%s；这是负向检查，不代表该写法受支持", code)
}

func TestKingbaseOracleMode_AdapterBoundaries(t *testing.T) {
	db := openKBOracle(t)
	defer finishLoggedTest(t, db)
	var version string
	if err := db.Raw("SELECT version()").Row().Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("[测试基准] 金仓 V9 官方 SQL 参考；模式=oracle；schema=%s；实例=%s", db.Dialect().CurrentDatabase(), version)

	runLoggedTest(t, db, "01_Types_Defaults_And_RoundTrip", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_types")
		defer oracleCleanupTable(t, db, table)
		if err := db.Table(table).AutoMigrate(&oracleAdapterValue{}).Error; err != nil {
			t.Fatal(err)
		}
		// KingbaseES V9R2 Oracle 模式同时接受 JSON 和 JSONB 声明，但会把物理目录类型统一为 jsonb。
		for column, want := range map[string]string{"id": "bigint", "native": "bigint", "u32": "bigint", "u64": "numeric(20,0)", "f24": "real", "f53": "double precision", "exact": "numeric(65,30)", "json": "jsonb", "json_b": "jsonb", "moment": "timestamp(6) with time zone"} {
			if got := oracleStoredType(t, db, table, column); got != want {
				t.Fatalf("字段 %s 实际类型=%q，预期=%q", column, got, want)
			}
		}
		row := oracleAdapterValue{Native: math.MaxInt64, U32: math.MaxUint32, U64: math.MaxUint64,
			F24: 1.5, F53: 12345.6789012345, Exact: "1234567890.123456789012345678901234567890", JSON: `{"Case":"Alpha"}`, JSONB: `{"Case":"Alpha"}`,
			Moment: time.Date(2024, 2, 29, 15, 45, 30, 123456000, time.FixedZone("UTC+08", 8*3600))}
		if err := db.Table(table).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		var got oracleAdapterValue
		if err := db.Table(table).First(&got, row.ID).Error; err != nil {
			t.Fatal(err)
		}
		if row.ID <= 0 || got.Native != row.Native || got.U32 != row.U32 || got.U64 != row.U64 || got.F24 != row.F24 || got.F53 != row.F53 || got.Exact != row.Exact || got.Literal != "Alpha  COMMENT Bravo" || got.JSON != row.JSON || !got.Moment.Equal(row.Moment) {
			t.Fatalf("写入回读不一致: got=%+v want=%+v", got, row)
		}
		var jsonValue string
		if err := db.Raw(fmt.Sprintf(`SELECT "json_b"->>'Case' FROM "%s" WHERE "id"=?`, table), row.ID).Row().Scan(&jsonValue); err != nil || jsonValue != "Alpha" {
			t.Fatalf("原生 JSONB 运算结果=%q error=%v", jsonValue, err)
		}
		// 如果错误替换为 CLOB，这里的非法 JSON 会被错误接受。
		oracleExpectedServerError(t, db, "22P02", "", func() error {
			return db.Table(table).Where("id=?", row.ID).UpdateColumn("json", "{broken").Error
		})
		var unchanged string
		if err := db.Raw(fmt.Sprintf(`SELECT "json"::text FROM "%s" WHERE "id"=?`, table), row.ID).Row().Scan(&unchanged); err != nil || unchanged != row.JSON {
			t.Fatalf("JSON 拒绝写入后数据改变: %q error=%v", unchanged, err)
		}
	})

	runLoggedTest(t, db, "02_Quoted_Index_Removal", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_idx")
		defer oracleCleanupTable(t, db, table)
		if err := db.Table(table).CreateTable(&oracleAdapterIndexRow{}).Error; err != nil {
			t.Fatal(err)
		}
		index := testutil.UniqueTable("Idx-Mixed")
		// 独立准备混合大小写索引；本场景验证方言 RemoveIndex，而不是 GORM V1 原样传递的 AddIndex 参数。
		if err := db.Exec(fmt.Sprintf(`CREATE INDEX "%s" ON "%s" ("name")`, index, table)).Error; err != nil {
			t.Fatal(err)
		}
		stateSQL := "SELECT COUNT(*) FROM pg_indexes WHERE schemaname=current_schema() AND tablename=? AND indexname=?"
		oracleRequireCount(t, db, stateSQL, 1, table, index)
		log := testutil.DriverLogFor(db)
		start := log.Snapshot()
		if err := db.Table(table).RemoveIndex(index).Error; err != nil {
			t.Fatal(err)
		}
		var drops int
		for _, result := range log.SQLResults(start) {
			if strings.HasPrefix(result.SQL, "DROP INDEX ") {
				drops++
				t.Logf("[实际 SQL] %s", result.SQL)
				if result.Err != nil {
					t.Fatalf("GORM 删除索引丢失驱动错误: %v", result.Err)
				}
			}
		}
		if drops != 1 {
			t.Fatalf("期望一次 DROP INDEX，实际=%d", drops)
		}
		oracleRequireCount(t, db, stateSQL, 0, table, index)
	})

	runLoggedTest(t, db, "03_ModifyColumn_State_And_Data", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_modify")
		defer oracleCleanupTable(t, db, table)
		if err := db.Table(table).CreateTable(&oracleAdapterIndexRow{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Table(table).ModifyColumn("name", "varchar2(128)").Error; err != nil {
			t.Fatal(err)
		}
		if got := oracleStoredType(t, db, table, "name"); got != "character varying(128 char)" {
			t.Fatalf("修改未生效: %s", got)
		}
		row := oracleAdapterIndexRow{Name: strings.Repeat("金", 80)}
		if err := db.Table(table).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		var got oracleAdapterIndexRow
		if err := db.Table(table).First(&got, row.ID).Error; err != nil || got.Name != row.Name {
			t.Fatalf("修改后数据核验失败: %+v error=%v", got, err)
		}
	})

	runLoggedTest(t, db, "04_Metadata_CurrentSchema_And_MissingObjects", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_meta")
		defer oracleCleanupTable(t, db, table)
		if err := db.Table(table).CreateTable(&oracleAdapterIndexRow{}).Error; err != nil {
			t.Fatal(err)
		}
		qualified := db.Dialect().CurrentDatabase() + "." + table
		if !db.HasTable(table) || !db.HasTable(qualified) || !db.Dialect().HasColumn(qualified, "name") {
			t.Fatal("当前 schema 的表或列未能找到")
		}
		missing := table + "_missing"
		if db.HasTable(missing) || db.Dialect().HasColumn(table, "missing") || db.Dialect().HasIndex(table, missing) || db.Dialect().HasForeignKey(table, missing) {
			t.Fatal("不存在的对象被误判为存在")
		}
		// 使用第二个自有 schema 暴露 USER_TABLES 中 owner 与 search_path 不一致的问题，
		// 同时不触碰任何已有应用对象。
		schema := testutil.UniqueTable("kb_ora_other")
		if err := db.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`).Error; err != nil {
				t.Errorf("清理独立 schema: %v", err)
			}
		}()
		if err := db.Exec(fmt.Sprintf(`CREATE TABLE "%s"."%s" ("name" varchar2(16))`, schema, missing)).Error; err != nil {
			t.Fatal(err)
		}
		if db.HasTable(missing) || !db.HasTable(schema+"."+missing) {
			t.Fatal("schema 查询范围错误：其他 schema 的对象不能冒充当前 schema 对象")
		}
	})

	runLoggedTest(t, db, "05_Identity_DefaultValues_And_Pagination", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_identity")
		defer oracleCleanupTable(t, db, table)
		type identityOnly struct {
			ID int64 `gorm:"primary_key"`
		}
		if err := db.Table(table).CreateTable(&identityOnly{}).Error; err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for i := 0; i < 3; i++ {
			row := identityOnly{}
			if err := db.Table(table).Create(&row).Error; err != nil || row.ID <= 0 {
				t.Fatalf("DEFAULT VALUES / RETURNING: id=%d error=%v", row.ID, err)
			}
			ids = append(ids, row.ID)
		}
		for _, check := range []struct {
			name  string
			query *gorm.DB
			want  []int64
		}{
			{"OffsetOnly", db.Table(table).Order("id").Offset(1), ids[1:]},
			{"LimitOffset", db.Table(table).Order("id").Limit(1).Offset(1), ids[1:2]},
			{"ZeroLimit", db.Table(table).Order("id").Limit(0), nil},
			{"RemovedLimit", db.Table(table).Order("id").Limit(-1).Offset(2), ids[2:]},
		} {
			var rows []identityOnly
			if err := check.query.Find(&rows).Error; err != nil {
				t.Fatalf("%s: %v", check.name, err)
			}
			var got []int64
			for _, row := range rows {
				got = append(got, row.ID)
			}
			if !reflect.DeepEqual(got, check.want) {
				t.Fatalf("%s: 实际=%v 预期=%v", check.name, got, check.want)
			}
		}
	})

	runLoggedTest(t, db, "06_ORM_ForeignKey_Add_Reject_And_Remove", func(t *testing.T) {
		parentTable, childTable := testutil.UniqueTable("kb_ora_fk_parent"), testutil.UniqueTable("kb_ora_fk_child")
		defer oracleCleanupTable(t, db, parentTable)
		defer oracleCleanupTable(t, db, childTable)
		type childRow struct {
			ID       int64 `gorm:"primary_key"`
			ParentID int64 `gorm:"not null"`
		}
		if err := db.Table(parentTable).CreateTable(&oracleAdapterIndexRow{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Table(childTable).CreateTable(&childRow{}).Error; err != nil {
			t.Fatal(err)
		}
		destination := fmt.Sprintf(`"%s"("id")`, parentTable)
		constraint := db.Dialect().BuildKeyName(childTable, "parent_id", destination, "foreign")
		if err := db.Table(childTable).AddForeignKey("parent_id", destination, "CASCADE", "NO ACTION").Error; err != nil {
			t.Fatal(err)
		}
		stateSQL := "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema=current_schema() AND table_name=? AND constraint_name=? AND constraint_type='FOREIGN KEY'"
		oracleRequireCount(t, db, stateSQL, 1, childTable, constraint)
		parent := oracleAdapterIndexRow{Name: "parent"}
		if err := db.Table(parentTable).Create(&parent).Error; err != nil {
			t.Fatal(err)
		}
		child := childRow{ParentID: parent.ID}
		if err := db.Table(childTable).Create(&child).Error; err != nil {
			t.Fatal(err)
		}
		oracleExpectedServerError(t, db, "23503", constraint, func() error { return db.Table(childTable).Create(&childRow{ParentID: parent.ID + 10000}).Error })
		oracleRequireCount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, childTable), 1)
		if err := db.Table(parentTable).Delete(&parent).Error; err != nil {
			t.Fatal(err)
		}
		oracleRequireCount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, childTable), 0)
		if err := db.Table(childTable).RemoveForeignKey("parent_id", destination).Error; err != nil {
			t.Fatal(err)
		}
		oracleRequireCount(t, db, stateSQL, 0, childTable, constraint)
		// 删除必须真正改变数据库行为，而不只是让 HasForeignKey 返回 false。
		if err := db.Table(childTable).Create(&childRow{ParentID: parent.ID + 10000}).Error; err != nil {
			t.Fatal(err)
		}
		oracleRequireCount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, childTable), 1)
	})

	runLoggedTest(t, db, "07_Explicit_Native_Identity", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_explicit_id")
		defer oracleCleanupTable(t, db, table)
		type explicitIdentity struct {
			ID int64 `gorm:"primary_key;type:bigint generated by default as identity"`
		}
		if err := db.Table(table).CreateTable(&explicitIdentity{}).Error; err != nil {
			t.Fatal(err)
		}
		if got := oracleStoredType(t, db, table, "id"); got != "bigint" {
			t.Fatalf("native identity base type changed: %q", got)
		}
		var identity string
		if err := db.Raw(`SELECT attidentity::text FROM pg_attribute WHERE attrelid=to_regclass(?) AND attname='id'`, table).Row().Scan(&identity); err != nil || identity != "d" {
			t.Fatalf("BY DEFAULT identity not created: %q error=%v", identity, err)
		}
		row := explicitIdentity{}
		if err := db.Table(table).Create(&row).Error; err != nil || row.ID <= 0 {
			t.Fatalf("native identity/RETURNING: id=%d error=%v", row.ID, err)
		}
		oracleRequireCount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE "id"=?`, table), 1, row.ID)
	})

	runLoggedTest(t, db, "08_ORM_AddUniqueIndex", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_unique_idx")
		index := testutil.UniqueTable("uix_kb_ora_code")
		defer oracleCleanupTable(t, db, table)
		type uniqueIndexRow struct {
			ID   int64  `gorm:"primary_key"`
			Code string `gorm:"size:32;not null"`
		}
		if err := db.Table(table).CreateTable(&uniqueIndexRow{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Table(table).AddUniqueIndex(index, "code").Error; err != nil {
			t.Fatal(err)
		}
		if !db.Dialect().HasIndex(table, index) {
			t.Fatalf("唯一索引未创建：%s", index)
		}
		// GORM V1 的原生语义是 HasIndex 命中后直接跳过。
		if err := db.Table(table).AddUniqueIndex(index, "code").Error; err != nil {
			t.Fatal("重复 AddUniqueIndex:", err)
		}
		if err := db.Table(table).Create(&uniqueIndexRow{Code: "same"}).Error; err != nil {
			t.Fatal(err)
		}
		oracleExpectedServerError(t, db, "23505", index, func() error {
			return db.Table(table).Create(&uniqueIndexRow{Code: "same"}).Error
		})
	})

	runLoggedTest(t, db, "09_ORM_DropColumn", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_drop_col")
		defer oracleCleanupTable(t, db, table)
		type beforeDrop struct {
			ID       int64  `gorm:"primary_key"`
			Name     string `gorm:"size:32;not null"`
			Obsolete string `gorm:"size:32"`
		}
		type afterDrop struct {
			ID   int64
			Name string
		}
		row := beforeDrop{Name: "kept", Obsolete: "remove-me"}
		if err := db.Table(table).CreateTable(&beforeDrop{}).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if !db.Dialect().HasColumn(table, "obsolete") {
			t.Fatal("待删除字段不存在")
		}
		if err := db.Table(table).DropColumn("obsolete").Error; err != nil {
			t.Fatal(err)
		}
		if db.Dialect().HasColumn(table, "obsolete") {
			t.Fatal("DropColumn 后字段仍存在")
		}
		var got afterDrop
		if err := db.Table(table).Where("id = ?", row.ID).First(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got.ID != row.ID || got.Name != row.Name {
			t.Fatalf("删除字段后原数据异常：got=%+v wantID=%d wantName=%q", got, row.ID, row.Name)
		}
	})

	runLoggedTest(t, db, "10_SchemaQualified_CRUD_And_Migration", func(t *testing.T) {
		schema := testutil.UniqueTable("kb_ora_schema")
		table := testutil.UniqueTable("items")
		qualified := schema + "." + table
		if err := db.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`).Error; err != nil {
				t.Errorf("清理 schema %s: %v", schema, err)
			}
		}()
		type schemaRowV1 struct {
			ID   int64  `gorm:"primary_key"`
			Name string `gorm:"size:32;not null"`
		}
		type schemaRowV2 struct {
			ID   int64  `gorm:"primary_key"`
			Name string `gorm:"size:32;not null"`
			Note string `gorm:"size:64"`
		}
		if err := db.Table(qualified).AutoMigrate(&schemaRowV1{}).Error; err != nil {
			t.Fatal(err)
		}
		if !db.Dialect().HasTable(qualified) || !db.Dialect().HasColumn(qualified, "name") {
			t.Fatal("schema 限定表或字段未创建")
		}
		row := schemaRowV1{Name: "qualified"}
		if err := db.Table(qualified).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.ID <= 0 {
			t.Fatal("schema 限定表插入后未回填主键")
		}
		if err := db.Table(qualified).AutoMigrate(&schemaRowV2{}).Error; err != nil {
			t.Fatal(err)
		}
		if !db.Dialect().HasColumn(qualified, "note") {
			t.Fatal("schema 限定表 AutoMigrate 未新增字段")
		}
		if err := db.Table(qualified).Where("id = ?", row.ID).Update("note", "updated").Error; err != nil {
			t.Fatal(err)
		}
		var got schemaRowV2
		if err := db.Table(qualified).Where("id = ?", row.ID).First(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got.Name != row.Name || got.Note != "updated" {
			t.Fatalf("schema 限定 CRUD 结果异常：%+v", got)
		}
		if err := db.Table(qualified).Delete(&got).Error; err != nil {
			t.Fatal(err)
		}
		oracleRequireCount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"."%s" WHERE "id"=?`, schema, table), 0, row.ID)
	})
}
