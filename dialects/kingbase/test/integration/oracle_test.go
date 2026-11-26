//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type KBOracleUser struct {
	gorm.Model
	Username string `gorm:"type:varchar2(64);unique_index;not null"`
	Email    string `gorm:"type:varchar2(128)"`
	Age      int    `gorm:"not null"`
	Score    float64
	Status   string `gorm:"type:varchar2(16);not null"`
	Note     string `gorm:"type:varchar2(512)"`
	Active   bool   `gorm:"not null;default:true"`
}

type KBOracleMigrationExtension struct {
	ID       uint   `gorm:"primary_key"`
	Category string `gorm:"type:varchar2(32)"`
}

type KBOracleParent struct {
	ID   uint   `gorm:"primary_key"`
	Name string `gorm:"type:varchar2(64);not null"`
}

type KBOracleChild struct {
	ID       uint `gorm:"primary_key"`
	ParentID uint `gorm:"not null"`
	Parent   KBOracleParent
}

type KBOracleLargeValue struct {
	ID         uint      `gorm:"primary_key"`
	Content    string    `gorm:"type:clob;not null"`
	Payload    []byte    `gorm:"type:blob;not null"`
	Optional   *string   `gorm:"type:varchar2(64)"`
	RecordedAt time.Time `gorm:"type:timestamp with time zone;not null"`
}

type KBOracleColonBindRow struct {
	ID    uint   `gorm:"primary_key"`
	Name  string `gorm:"type:varchar2(64);not null"`
	Score int    `gorm:"not null"`
}

func startOracleTestPhase(t *testing.T, phase string) {
	t.Helper()
	t.Logf("========== %s ==========", phase)
}

func failOracleTestPhase(t *testing.T, phase, format string, args ...interface{}) {
	t.Helper()
	t.Fatalf("[未通过] %s：%s", phase, fmt.Sprintf(format, args...))
}

func openKBOracle(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.OpenLogged(t, kb.KbModeOracle)
	db = testutil.ObserveMetadata(t, db)
	t.Log("[通过] 连接数据库并校验 Oracle 模式")
	db.LogMode(false)
	return db
}

func oracleCleanupTable(t *testing.T, db *gorm.DB, table string) {
	t.Helper()
	if err := db.DropTableIfExists(table).Error; err != nil {
		t.Errorf("[未通过] 清理测试表 %s: %v", table, err)
	}
}

func TestKingbaseOracleMode_ConnectOnly(t *testing.T) {
	startOracleTestPhase(t, "仅连接验证")
	db := openKBOracle(t)
	if db == nil {
		return
	}
	defer finishLoggedTest(t, db)
}

func TestKingbaseOracleMode_ColonBindVariables(t *testing.T) {
	db := openKBOracle(t)
	if db == nil {
		return
	}
	defer finishLoggedTest(t, db)

	table := testutil.UniqueTable("kb_ora_colon_bind")
	defer oracleCleanupTable(t, db, table)
	if err := db.Table(table).CreateTable(&KBOracleColonBindRow{}).Error; err != nil {
		t.Fatal(err)
	}

	log := testutil.DriverLogFor(db)
	start := log.Snapshot()
	row := KBOracleColonBindRow{Name: "alice", Score: 98}
	if err := db.Table(table).Create(&row).Error; err != nil {
		t.Fatalf("冒号占位符 INSERT 执行失败: %v", err)
	}
	var got KBOracleColonBindRow
	if err := db.Table(table).Where("name = ? AND score = ?", row.Name, row.Score).First(&got).Error; err != nil {
		t.Fatalf("冒号占位符 SELECT 执行失败: %v", err)
	}
	if got.ID != row.ID || got.Name != row.Name || got.Score != row.Score {
		t.Fatalf("冒号占位符写入回读不一致: got=%+v want=%+v", got, row)
	}
	nameBind, err := kb.OracleNamedBind("name", row.Name)
	if err != nil {
		t.Fatal(err)
	}
	scoreBind, err := kb.OracleNamedBind("score", row.Score)
	if err != nil {
		t.Fatal(err)
	}
	var namedGot KBOracleColonBindRow
	if err := db.Table(table).Where("name = ? AND score = ?", nameBind, scoreBind).First(&namedGot).Error; err != nil {
		t.Fatalf("命名冒号占位符 SELECT 执行失败: %v", err)
	}
	if namedGot.ID != row.ID {
		t.Fatalf("命名冒号占位符查询结果不一致: got=%+v want=%+v", namedGot, row)
	}

	var insertChecked, numericSelectChecked, namedSelectChecked bool
	for _, result := range log.SQLResults(start) {
		sql := result.SQL
		switch {
		case strings.HasPrefix(sql, "INSERT INTO "):
			insertChecked = true
			if strings.Contains(sql, "?") || !strings.Contains(sql, ":1") || !strings.Contains(sql, ":2") {
				t.Fatalf("INSERT 未使用连续冒号占位符: %s", sql)
			}
		case strings.HasPrefix(sql, "SELECT ") && strings.Contains(sql, `FROM "`+table+`"`):
			if strings.Contains(sql, ":name") || strings.Contains(sql, ":score") {
				namedSelectChecked = true
				if strings.Contains(sql, "?") || !strings.Contains(sql, ":name") || !strings.Contains(sql, ":score") {
					t.Fatalf("SELECT 未使用完整命名冒号占位符: %s", sql)
				}
			} else {
				numericSelectChecked = true
				if strings.Contains(sql, "?") || !strings.Contains(sql, ":1") || !strings.Contains(sql, ":2") {
					t.Fatalf("SELECT 未使用连续数字冒号占位符: %s", sql)
				}
			}
		}
	}
	if !insertChecked || !numericSelectChecked || !namedSelectChecked {
		t.Fatalf("未观测到完整的冒号占位符 SQL: insert=%t numeric-select=%t named-select=%t", insertChecked, numericSelectChecked, namedSelectChecked)
	}
}

type oracleFunctionCheck struct {
	name, query, want string
	args              []interface{}
	numeric, wantNull bool
}

type oracleFunctionReporter interface {
	Helper()
	Logf(string, ...interface{})
	Errorf(string, ...interface{})
}

func oracleCheckFunctions(t oracleFunctionReporter, db *gorm.DB, checks []oracleFunctionCheck) {
	t.Helper()
	for _, check := range checks {
		var actual sql.NullString
		if err := db.Raw(check.query, check.args...).Row().Scan(&actual); err != nil {
			t.Errorf("[未通过] %s：执行或读取失败: %v", check.name, err)
			continue
		}
		matches := !actual.Valid && check.wantNull
		if actual.Valid && !check.wantNull {
			matches = actual.String == check.want
			if check.numeric {
				got, gotErr := strconv.ParseFloat(actual.String, 64)
				want, wantErr := strconv.ParseFloat(check.want, 64)
				matches = gotErr == nil && wantErr == nil && math.Abs(got-want) < 1e-9
			}
		}
		if !matches {
			got, want := strconv.Quote(actual.String), strconv.Quote(check.want)
			if !actual.Valid {
				got = "NULL"
			}
			if check.wantNull {
				want = "NULL"
			}
			t.Errorf("[未通过] %s：实际=%s；预期=%s", check.name, got, want)
			continue
		}
		t.Logf("[通过] %s", check.name)
	}
}

func TestKingbaseOracleMode_QualifiedIndexMetadata(t *testing.T) {
	db := openKBOracle(t)
	if db == nil {
		return
	}
	defer finishLoggedTest(t, db)

	table := testutil.UniqueTable("kb_ora_qualified_index")
	index := testutil.UniqueTable("idx_ora_qualified")
	defer oracleCleanupTable(t, db, table)
	type row struct {
		ID   int64  `gorm:"primary_key"`
		Name string `gorm:"type:varchar2(32);not null"`
	}
	if err := db.Table(table).CreateTable(&row{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table(table).AddIndex(index, "name").Error; err != nil {
		t.Fatal(err)
	}

	schema := db.Dialect().CurrentDatabase()
	if schema == "" {
		t.Fatal("未能读取当前 schema")
	}
	quote := func(identifier string) string {
		return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
	}
	qualifiedTable := quote(schema) + "." + quote(table)
	qualifiedIndex := quote(schema) + "." + quote(index)
	if !db.Dialect().HasIndex(qualifiedTable, qualifiedIndex) {
		t.Fatalf("schema 限定索引查询失败：table=%s index=%s", qualifiedTable, qualifiedIndex)
	}
	t.Logf("[通过] schema 限定索引查询：table=%s index=%s", qualifiedTable, qualifiedIndex)
}

// 每条查询使用固定输入检查一个函数；日期结果与带类型字面量比较，避免 TO_CHAR 失败
// 遮蔽其他日期函数。
func TestKingbaseOracleMode_FunctionCompatibility(t *testing.T) {
	startOracleTestPhase(t, "Oracle 函数兼容性专项")
	db := openKBOracle(t)
	if db == nil {
		return
	}
	defer finishLoggedTest(t, db)

	groups := []struct {
		name, title string
		checks      []oracleFunctionCheck
	}{
		{
			name: "Null_And_Conditional", title: "空值与条件函数",
			checks: []oracleFunctionCheck{
				{name: "NVL/NULL替换", query: "SELECT NVL(NULL, 'fallback') AS actual FROM DUAL", want: "fallback"},
				{name: "NVL/保留非空值", query: "SELECT NVL('value', 'fallback') AS actual FROM DUAL", want: "value"},
				{name: "NVL/空字符串按NULL处理", query: "SELECT NVL('', 'fallback') AS actual FROM DUAL", want: "fallback"},
				{name: "NVL2/NULL分支", query: "SELECT NVL2(NULL, 'present', 'missing') AS actual FROM DUAL", want: "missing"},
				{name: "NVL2/非空分支", query: "SELECT NVL2('value', 'present', 'missing') AS actual FROM DUAL", want: "present"},
				{name: "DECODE/命中分支", query: "SELECT DECODE('A', 'A', 'matched', 'other') AS actual FROM DUAL", want: "matched"},
				{name: "DECODE/默认分支", query: "SELECT DECODE('B', 'A', 'matched', 'other') AS actual FROM DUAL", want: "other"},
				{name: "DECODE/NULL匹配", query: "SELECT DECODE(NULL, NULL, 'matched', 'other') AS actual FROM DUAL", want: "matched"},
				{name: "COALESCE", query: "SELECT COALESCE(NULL, 'first', 'second') AS actual FROM DUAL", want: "first"},
				{name: "NULLIF/相等返回NULL", query: "SELECT NULLIF('same', 'same') AS actual FROM DUAL", wantNull: true},
				{name: "NULLIF/不等保留原值", query: "SELECT NULLIF('first', 'second') AS actual FROM DUAL", want: "first"},
			},
		},
		{
			name: "String_Functions", title: "字符串函数",
			checks: []oracleFunctionCheck{
				{name: "CONCAT/参数绑定", query: "SELECT CONCAT(?, ?) AS actual FROM DUAL", args: []interface{}{"ab", "_x"}, want: "ab_x"},
				{name: "SUBSTR/正向截取", query: "SELECT SUBSTR('abcdef', 2, 3) AS actual FROM DUAL", want: "bcd"},
				{name: "SUBSTR/负数起点", query: "SELECT SUBSTR('abcdef', -3, 2) AS actual FROM DUAL", want: "de"},
				{name: "LENGTH/中文字符", query: "SELECT LENGTH('金仓abc') AS actual FROM DUAL", want: "5", numeric: true},
				{name: "INSTR/第二次匹配", query: "SELECT INSTR('abcabc', 'bc', 1, 2) AS actual FROM DUAL", want: "5", numeric: true},
				{name: "UPPER", query: "SELECT UPPER('Abc') AS actual FROM DUAL", want: "ABC"},
				{name: "LOWER", query: "SELECT LOWER('AbC') AS actual FROM DUAL", want: "abc"},
				{name: "TRIM", query: "SELECT TRIM('  abc  ') AS actual FROM DUAL", want: "abc"},
				{name: "REPLACE", query: "SELECT REPLACE('ab-ab', 'ab', 'xy') AS actual FROM DUAL", want: "xy-xy"},
			},
		},
		{
			name: "Numeric_Functions", title: "数值函数",
			checks: []oracleFunctionCheck{
				{name: "ROUND/正数", query: "SELECT ROUND(12.3456, 2) AS actual FROM DUAL", want: "12.35", numeric: true},
				{name: "ROUND/负数", query: "SELECT ROUND(-12.3456, 2) AS actual FROM DUAL", want: "-12.35", numeric: true},
				{name: "TRUNC/数值截断", query: "SELECT TRUNC(12.3456, 2) AS actual FROM DUAL", want: "12.34", numeric: true},
				{name: "TRUNC/负数向零截断", query: "SELECT TRUNC(-12.3456, 2) AS actual FROM DUAL", want: "-12.34", numeric: true},
				{name: "MOD", query: "SELECT MOD(11, 4) AS actual FROM DUAL", want: "3", numeric: true},
				{name: "ABS", query: "SELECT ABS(-7) AS actual FROM DUAL", want: "7", numeric: true},
				{name: "CEIL", query: "SELECT CEIL(1.2) AS actual FROM DUAL", want: "2", numeric: true},
				{name: "FLOOR", query: "SELECT FLOOR(1.8) AS actual FROM DUAL", want: "1", numeric: true},
			},
		},
		{
			name: "Date_And_Conversion", title: "日期与类型转换函数",
			checks: []oracleFunctionCheck{
				{name: "TO_CHAR/显式日期格式", query: "SELECT TO_CHAR(DATE '2024-02-29', 'YYYY-MM-DD') AS actual FROM DUAL", want: "2024-02-29"},
				{name: "TO_DATE/参数绑定与闰日", query: "SELECT CASE WHEN TO_DATE(?, 'YYYY-MM-DD') = DATE '2024-02-29' THEN 1 ELSE 0 END AS actual FROM DUAL", args: []interface{}{"2024-02-29"}, want: "1", numeric: true},
				{name: "TO_TIMESTAMP", query: "SELECT CASE WHEN TO_TIMESTAMP(?, 'YYYY-MM-DD HH24:MI:SS') = TIMESTAMP '2024-02-29 15:45:30' THEN 1 ELSE 0 END AS actual FROM DUAL", args: []interface{}{"2024-02-29 15:45:30"}, want: "1", numeric: true},
				{name: "TO_NUMBER", query: "SELECT TO_NUMBER(?, '999.99') AS actual FROM DUAL", args: []interface{}{"123.45"}, want: "123.45", numeric: true},
				{name: "TRUNC/日期截断到日", query: "SELECT CASE WHEN TRUNC(TIMESTAMP '2024-02-29 15:45:30') = DATE '2024-02-29' THEN 1 ELSE 0 END AS actual FROM DUAL", want: "1", numeric: true},
				{name: "TRUNC/日期截断到月", query: "SELECT CASE WHEN TRUNC(DATE '2024-02-29', 'MM') = DATE '2024-02-01' THEN 1 ELSE 0 END AS actual FROM DUAL", want: "1", numeric: true},
				{name: "ADD_MONTHS/闰年月末", query: "SELECT CASE WHEN ADD_MONTHS(DATE '2024-01-31', 1) = DATE '2024-02-29' THEN 1 ELSE 0 END AS actual FROM DUAL", want: "1", numeric: true},
				{name: "LAST_DAY/闰年二月", query: "SELECT CASE WHEN LAST_DAY(DATE '2024-02-10') = DATE '2024-02-29' THEN 1 ELSE 0 END AS actual FROM DUAL", want: "1", numeric: true},
				{name: "MONTHS_BETWEEN/月末", query: "SELECT MONTHS_BETWEEN(DATE '2024-03-31', DATE '2024-02-29') AS actual FROM DUAL", want: "1", numeric: true},
			},
		},
	}
	for _, group := range groups {
		group := group
		runLoggedTest(t, db, group.name, func(t *testing.T) {
			startOracleTestPhase(t, group.title)
			oracleCheckFunctions(t, db, group.checks)
		})
	}
}

func TestKingbaseOracleMode_FullIntegration(t *testing.T) {
	startOracleTestPhase(t, "完整集成测试")
	db := openKBOracle(t)
	if db == nil {
		return
	}
	defer finishLoggedTest(t, db)

	runID := time.Now().Unix()
	table := fmt.Sprintf("kb_oracle_users_%d", runID)
	statusIndexName := fmt.Sprintf("idx_oracle_st_%d", runID)
	if err := db.DropTableIfExists(table).Error; err != nil {
		failOracleTestPhase(t, "准备测试表", "清理旧表失败：%v", err)
	}
	if testutil.KeepTestTables() {
		t.Logf("保留测试表 %s，供 KStudio 查看；下次执行会先清理该表", table)
	} else {
		defer oracleCleanupTable(t, db, table)
	}

	runLoggedTest(t, db, "DDL_And_Metadata", func(t *testing.T) {
		startOracleTestPhase(t, "DDL 与元数据")
		if err := db.Table(table).AutoMigrate(&KBOracleUser{}).Error; err != nil {
			failOracleTestPhase(t, "DDL 与元数据", "自动迁移失败：%v", err)
		}
		if !db.HasTable(table) {
			failOracleTestPhase(t, "DDL 与元数据", "表未创建成功：%s", table)
		}
		if !db.Dialect().HasColumn(table, "username") {
			failOracleTestPhase(t, "DDL 与元数据", "字段 username 未创建成功")
		}

		if err := db.Table(table).AddIndex(statusIndexName, "status").Error; err != nil {
			failOracleTestPhase(t, "DDL 与元数据", "新增索引失败：%v", err)
		}
		if !db.Dialect().HasIndex(table, statusIndexName) {
			failOracleTestPhase(t, "DDL 与元数据", "索引 %s 未创建成功", statusIndexName)
		}
		if err := db.Table(table).ModifyColumn("note", "varchar2(1024)").Error; err != nil {
			failOracleTestPhase(t, "DDL 与元数据", "字段类型变更失败：%v", err)
		}
		if err := db.Table(table).AutoMigrate(&KBOracleMigrationExtension{}).Error; err != nil {
			failOracleTestPhase(t, "DDL 与元数据", "已有表自动增列失败：%v", err)
		}
		if !db.Dialect().HasColumn(table, "category") {
			failOracleTestPhase(t, "DDL 与元数据", "已有表未新增 category 字段")
		}

		schema := db.Dialect().CurrentDatabase()
		if schema == "" {
			failOracleTestPhase(t, "DDL 与元数据", "未能读取当前 schema")
		}
		qualifiedTable := schema + "." + table
		if !db.Dialect().HasTable(qualifiedTable) {
			failOracleTestPhase(t, "DDL 与元数据", "schema 限定表查询失败：%s", qualifiedTable)
		}
		if !db.Dialect().HasColumn(qualifiedTable, "username") {
			failOracleTestPhase(t, "DDL 与元数据", "schema 限定字段查询失败：%s.username", qualifiedTable)
		}
		if !db.Dialect().HasIndex(qualifiedTable, statusIndexName) {
			failOracleTestPhase(t, "DDL 与元数据", "schema 限定索引查询失败：%s.%s", qualifiedTable, statusIndexName)
		}
	})

	seed := []KBOracleUser{
		{Username: "alice", Email: "a@test.com", Age: 18, Score: 90, Status: "active", Note: strings.Repeat("n", 700), Active: true},
		{Username: "bob", Email: "b@test.com", Age: 22, Score: 75, Status: "active", Note: "second", Active: true},
		{Username: "cindy", Email: "c@test.com", Age: 31, Score: 60, Status: "inactive", Note: "third", Active: false},
	}

	runLoggedTest(t, db, "CRUD_And_Pagination", func(t *testing.T) {
		startOracleTestPhase(t, "CRUD 与分页")
		for i := range seed {
			if err := db.Table(table).Create(&seed[i]).Error; err != nil {
				failOracleTestPhase(t, "CRUD 与分页", "插入数据失败：%v", err)
			}
			if seed[i].ID == 0 {
				failOracleTestPhase(t, "CRUD 与分页", "插入后未返回自增主键")
			}
		}

		var users []KBOracleUser
		if err := db.Table(table).
			Where("age >= ?", 18).
			Order("score desc").
			Limit(2).
			Offset(1).
			Find(&users).Error; err != nil {
			failOracleTestPhase(t, "CRUD 与分页", "分页查询失败：%v", err)
		}
		if len(users) != 2 {
			failOracleTestPhase(t, "CRUD 与分页", "分页结果不符合预期：期望=2，实际=%d", len(users))
		}

		if err := db.Table(table).Where("username = ?", "bob").
			Update("score", gorm.Expr("score + ?", 5)).Error; err != nil {
			failOracleTestPhase(t, "CRUD 与分页", "表达式更新失败：%v", err)
		}
		var bob KBOracleUser
		if err := db.Table(table).Where("username = ?", "bob").First(&bob).Error; err != nil {
			failOracleTestPhase(t, "CRUD 与分页", "查询 bob 失败：%v", err)
		}
		if bob.Score != 80 {
			failOracleTestPhase(t, "CRUD 与分页", "更新结果不符合预期：期望分数=80，实际=%v", bob.Score)
		}
	})

	runLoggedTest(t, db, "Constraints_And_Index_Removal", func(t *testing.T) {
		startOracleTestPhase(t, "约束与索引删除")

		indexName := fmt.Sprintf("idx_oracle_rm_%d", runID)
		if err := db.Table(table).AddIndex(indexName, "email").Error; err != nil {
			failOracleTestPhase(t, "约束与索引删除", "创建待删除索引失败：%v", err)
		}
		if !db.Dialect().HasIndex(table, indexName) {
			failOracleTestPhase(t, "约束与索引删除", "新建索引未在系统目录中查到：%s", indexName)
		}
		if err := db.Table(table).RemoveIndex(indexName).Error; err != nil {
			failOracleTestPhase(t, "约束与索引删除", "删除索引失败：%v", err)
		}
		if db.Dialect().HasIndex(table, indexName) {
			failOracleTestPhase(t, "约束与索引删除", "索引删除后仍能查到：%s", indexName)
		}

		duplicate := KBOracleUser{
			Username: "alice",
			Email:    "duplicate@test.com",
			Age:      20,
			Status:   "active",
			Active:   true,
		}
		constraint := db.Dialect().BuildKeyName("uix", table, "username")
		assertUniqueViolation(t, db, constraint, func() error {
			return db.Table(table).Create(&duplicate).Error
		})
	})

	runLoggedTest(t, db, "Transaction_Commit_And_Rollback", func(t *testing.T) {
		startOracleTestPhase(t, "事务提交与回滚")
		tx := db.Begin()
		if tx.Error != nil {
			failOracleTestPhase(t, "事务提交与回滚", "开启事务失败：%v", tx.Error)
		}
		committed := KBOracleUser{Username: "tx_commit", Age: 20, Score: 66, Status: "active", Active: true}
		if err := tx.Table(table).Create(&committed).Error; err != nil {
			if rollbackErr := tx.Rollback().Error; rollbackErr != nil {
				t.Errorf("[未通过] 清理失败事务: %v", rollbackErr)
			}
			failOracleTestPhase(t, "事务提交与回滚", "事务内插入失败：%v", err)
		}
		if err := tx.Commit().Error; err != nil {
			failOracleTestPhase(t, "事务提交与回滚", "事务提交失败：%v", err)
		}

		tx = db.Begin()
		if tx.Error != nil {
			failOracleTestPhase(t, "事务提交与回滚", "开启回滚事务失败：%v", tx.Error)
		}
		rolledBack := KBOracleUser{Username: "tx_rollback", Age: 20, Score: 50, Status: "active", Active: true}
		if err := tx.Table(table).Create(&rolledBack).Error; err != nil {
			if rollbackErr := tx.Rollback().Error; rollbackErr != nil {
				t.Errorf("[未通过] 清理失败事务: %v", rollbackErr)
			}
			failOracleTestPhase(t, "事务提交与回滚", "回滚事务内插入失败：%v", err)
		}
		if err := tx.Rollback().Error; err != nil {
			failOracleTestPhase(t, "事务提交与回滚", "事务回滚失败：%v", err)
		}

		var committedCount, rolledBackCount int
		if err := db.Table(table).Where("username = ?", "tx_commit").Count(&committedCount).Error; err != nil {
			failOracleTestPhase(t, "事务提交与回滚", "查询提交记录失败：%v", err)
		}
		if err := db.Table(table).Where("username = ?", "tx_rollback").Count(&rolledBackCount).Error; err != nil {
			failOracleTestPhase(t, "事务提交与回滚", "查询回滚记录失败：%v", err)
		}
		if committedCount != 1 || rolledBackCount != 0 {
			failOracleTestPhase(t, "事务提交与回滚", "事务结果不符合预期：提交记录=%d，回滚记录=%d", committedCount, rolledBackCount)
		}
	})

	runLoggedTest(t, db, "Soft_Delete_Restore_And_Hard_Delete", func(t *testing.T) {
		startOracleTestPhase(t, "软删除、恢复与硬删除")

		victim := KBOracleUser{
			Username: "delete_target",
			Email:    "delete@test.com",
			Age:      25,
			Status:   "active",
			Active:   true,
		}
		if err := db.Table(table).Create(&victim).Error; err != nil {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "创建待删除记录失败：%v", err)
		}
		if err := db.Table(table).Delete(&victim).Error; err != nil {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "软删除失败：%v", err)
		}

		var visible KBOracleUser
		err := db.Table(table).Where("id = ?", victim.ID).First(&visible).Error
		if !gorm.IsRecordNotFoundError(err) {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "软删除记录仍可被普通查询命中：%v", err)
		}

		var deleted KBOracleUser
		if err := db.Table(table).Unscoped().Where("id = ?", victim.ID).First(&deleted).Error; err != nil {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "Unscoped 查询软删除记录失败：%v", err)
		}
		if deleted.DeletedAt == nil {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "软删除后 deleted_at 仍为空")
		}

		if err := db.Table(table).Unscoped().Model(&KBOracleUser{}).
			Where("id = ?", victim.ID).
			UpdateColumn("deleted_at", nil).Error; err != nil {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "恢复软删除记录失败：%v", err)
		}
		var restored KBOracleUser
		if err := db.Table(table).Where("id = ?", victim.ID).First(&restored).Error; err != nil {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "恢复后普通查询未找到记录：%v", err)
		}

		if err := db.Table(table).Unscoped().Delete(&restored).Error; err != nil {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "永久删除失败：%v", err)
		}
		var remaining int
		if err := db.Table(table).Unscoped().Where("id = ?", victim.ID).Count(&remaining).Error; err != nil {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "查询永久删除结果失败：%v", err)
		}
		if remaining != 0 {
			failOracleTestPhase(t, "软删除、恢复与硬删除", "永久删除后仍存在记录：%d", remaining)
		}
	})

	runLoggedTest(t, db, "Foreign_Key_And_Cascade", func(t *testing.T) {
		startOracleTestPhase(t, "外键与级联删除")

		parentTable := fmt.Sprintf("kb_oracle_parent_%d", runID)
		childTable := fmt.Sprintf("kb_oracle_child_%d", runID)
		constraintName := fmt.Sprintf("fk_oracle_%d", runID)
		defer oracleCleanupTable(t, db, parentTable)
		defer oracleCleanupTable(t, db, childTable)
		if err := db.DropTableIfExists(childTable).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "清理旧子表失败：%v", err)
		}
		if err := db.DropTableIfExists(parentTable).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "清理旧父表失败：%v", err)
		}

		if err := db.Table(parentTable).AutoMigrate(&KBOracleParent{}).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "创建父表失败：%v", err)
		}
		if err := db.Table(childTable).AutoMigrate(&KBOracleChild{}).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "创建子表失败：%v", err)
		}

		addForeignKeySQL := fmt.Sprintf(
			`ALTER TABLE "%s" ADD CONSTRAINT "%s" FOREIGN KEY ("parent_id") REFERENCES "%s" ("id") ON DELETE CASCADE`,
			childTable,
			constraintName,
			parentTable,
		)
		if err := db.Exec(addForeignKeySQL).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "创建外键失败：%v", err)
		}
		if !db.Dialect().HasForeignKey(childTable, constraintName) {
			failOracleTestPhase(t, "外键与级联删除", "外键未在 USER_CONSTRAINTS 中查到：%s", constraintName)
		}

		parent := KBOracleParent{Name: "parent"}
		if err := db.Table(parentTable).Create(&parent).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "插入父记录失败：%v", err)
		}
		child := KBOracleChild{ParentID: parent.ID}
		if err := db.Table(childTable).Create(&child).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "插入子记录失败：%v", err)
		}
		if err := db.Table(parentTable).Delete(&parent).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "删除父记录失败：%v", err)
		}
		var childCount int
		if err := db.Table(childTable).Where("id = ?", child.ID).Count(&childCount).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "查询级联删除结果失败：%v", err)
		}
		if childCount != 0 {
			failOracleTestPhase(t, "外键与级联删除", "父记录删除后子记录未被级联删除：%d", childCount)
		}

		dropForeignKeySQL := fmt.Sprintf(`ALTER TABLE "%s" DROP CONSTRAINT "%s"`, childTable, constraintName)
		if err := db.Exec(dropForeignKeySQL).Error; err != nil {
			failOracleTestPhase(t, "外键与级联删除", "删除外键失败：%v", err)
		}
		if db.Dialect().HasForeignKey(childTable, constraintName) {
			failOracleTestPhase(t, "外键与级联删除", "外键删除后仍能在系统目录中查到：%s", constraintName)
		}
	})

	runLoggedTest(t, db, "CLOB_BLOB_NULL_And_Timestamp", func(t *testing.T) {
		startOracleTestPhase(t, "大字段、NULL 与时间")

		lobDB := db.New().LogMode(false)
		lobTable := fmt.Sprintf("kb_oracle_lob_%d", runID)
		defer oracleCleanupTable(t, lobDB, lobTable)
		if err := lobDB.DropTableIfExists(lobTable).Error; err != nil {
			failOracleTestPhase(t, "大字段、NULL 与时间", "清理旧表失败：%v", err)
		}
		if err := lobDB.Table(lobTable).AutoMigrate(&KBOracleLargeValue{}).Error; err != nil {
			failOracleTestPhase(t, "大字段、NULL 与时间", "创建大字段测试表失败：%v", err)
		}

		content := strings.Repeat("Kingbase Oracle CLOB 测试内容。", 300)
		payload := bytes.Repeat([]byte{0x00, 0x01, 0x7f, 0xff}, 2048)
		recordedAt := time.Now().Truncate(time.Microsecond)
		value := KBOracleLargeValue{
			Content:    content,
			Payload:    payload,
			Optional:   nil,
			RecordedAt: recordedAt,
		}
		if err := lobDB.Table(lobTable).Create(&value).Error; err != nil {
			failOracleTestPhase(t, "大字段、NULL 与时间", "写入大字段记录失败：%v", err)
		}
		var loaded KBOracleLargeValue
		if err := lobDB.Table(lobTable).Where("id = ?", value.ID).First(&loaded).Error; err != nil {
			failOracleTestPhase(t, "大字段、NULL 与时间", "读取大字段记录失败：%v", err)
		}
		if loaded.Content != content {
			failOracleTestPhase(t, "大字段、NULL 与时间", "CLOB 内容不一致：期望长度=%d，实际长度=%d", len(content), len(loaded.Content))
		}
		if !bytes.Equal(loaded.Payload, payload) {
			failOracleTestPhase(t, "大字段、NULL 与时间", "BLOB 内容不一致：期望长度=%d，实际长度=%d", len(payload), len(loaded.Payload))
		}
		if loaded.Optional != nil {
			failOracleTestPhase(t, "大字段、NULL 与时间", "NULL 字段读取后不是 nil：%q", *loaded.Optional)
		}
		delta := loaded.RecordedAt.Sub(recordedAt)
		if delta < 0 {
			delta = -delta
		}
		if delta > time.Second {
			failOracleTestPhase(t, "大字段、NULL 与时间", "时间值偏差过大：期望=%s，实际=%s", recordedAt, loaded.RecordedAt)
		}
	})

	runLoggedTest(t, db, "Oracle_Dual", func(t *testing.T) {
		startOracleTestPhase(t, "DUAL 常量查询")
		var result struct {
			Value int
		}
		if err := db.Raw("SELECT 1 AS value FROM DUAL").Scan(&result).Error; err != nil {
			failOracleTestPhase(t, "DUAL 常量查询", "DUAL 查询失败：%v", err)
		}
		if result.Value != 1 {
			failOracleTestPhase(t, "DUAL 常量查询", "DUAL 查询结果不符合预期：期望=1，实际=%d", result.Value)
		}
	})
}
