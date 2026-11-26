//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type KBMySQLUser struct {
	gorm.Model
	Username string `gorm:"type:varchar(64);unique_index;not null"`
	Email    string `gorm:"type:varchar(128);index"`
	Age      int    `gorm:"index"`
	Score    int
	Status   string `gorm:"type:varchar(16);index"`
}

type KBMySQLSpecialType struct {
	ID        uint           `gorm:"primary_key"`
	Name      string         `gorm:"type:varchar(64);not null;unique_index"`
	Price     float64        `gorm:"type:decimal(10,2);not null"`
	Flag      string         `gorm:"type:char(1);not null"`
	Meta      string         `gorm:"type:json"`
	RawData   []byte         `gorm:"type:blob"`
	Note      sql.NullString `gorm:"type:text"`
	CreatedAt time.Time
}

type KBMySQLConstraintRow struct {
	ID         uint      `gorm:"primary_key"`
	TenantID   int       `gorm:"not null;unique_index:uix_tenant_code"`
	Code       string    `gorm:"type:varchar(32);not null;unique_index:uix_tenant_code;index"`
	Name       string    `gorm:"type:varchar(128);not null"`
	Amount     float64   `gorm:"type:decimal(12,4);not null"`
	Active     bool      `gorm:"not null;default:true"`
	OccurredAt time.Time `gorm:"type:datetime"`
}

func openKBMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.OpenLogged(t, kb.KbModeMySQL)
	db = testutil.ObserveMetadata(t, db)
	t.Log("[通过] 连接数据库并校验 MySQL 模式")
	// 独立观察驱动错误，成功 SQL 保持静默。
	db.LogMode(false)
	return db
}

type mysqlFunctionCheck struct {
	name, query, want string
	args              []interface{}
	numeric           bool
	boolean           bool
}

func mysqlCheckFunctions(t *testing.T, db *gorm.DB, checks []mysqlFunctionCheck) {
	t.Helper()
	for _, check := range checks {
		var row struct{ Actual sql.NullString }
		if err := db.Raw(check.query, check.args...).Scan(&row).Error; err != nil {
			t.Errorf("[函数未通过] %s: %v", check.name, err)
			continue
		}
		matches := row.Actual.Valid && row.Actual.String == check.want
		if check.numeric && row.Actual.Valid {
			got, gotErr := strconv.ParseFloat(row.Actual.String, 64)
			want, wantErr := strconv.ParseFloat(check.want, 64)
			matches = gotErr == nil && wantErr == nil && math.Abs(got-want) < 1e-9
		}
		if check.boolean && row.Actual.Valid {
			got, gotErr := strconv.ParseBool(row.Actual.String)
			want, wantErr := strconv.ParseBool(check.want)
			matches = gotErr == nil && wantErr == nil && got == want
		}
		if !matches {
			t.Errorf("[函数未通过] %s: 实际=%#v; 预期=%s", check.name, row.Actual, check.want)
			continue
		}
		t.Logf("[通过] %s", check.name)
	}
}

func mysqlTestTitle(t *testing.T, title string) {
	t.Helper()
	t.Logf("========== %s ==========", title)
}

func mysqlTestCleanup(t *testing.T, db *gorm.DB, table string) {
	t.Helper()
	mysqlTestTitle(t, "测试结束：清理测试表")
	if err := db.DropTableIfExists(table).Error; err != nil {
		t.Errorf("清理测试表 %s 失败: %v", table, err)
	}
}

// 在不重复执行 SQL 的前提下观察真实驱动的 Exec 结果；嵌入 SQLCommon 可让元数据查询
// 继续使用同一个底层连接。
type mysqlDDLObserver struct {
	gorm.SQLCommon
	t         *testing.T
	execCount int
	lastError error
}

func (d *mysqlDDLObserver) ReportMetadataError(err error) {
	if observer, ok := d.SQLCommon.(interface{ ReportMetadataError(error) }); ok {
		observer.ReportMetadataError(err)
	}
}

func (d *mysqlDDLObserver) Exec(query string, args ...interface{}) (sql.Result, error) {
	result, err := d.SQLCommon.Exec(query, args...)
	d.execCount++
	d.lastError = err
	return result, err
}

func TestKingbaseMySQLMode_FullIntegration(t *testing.T) {
	mysqlTestTitle(t, "MySQL 模式完整集成测试：DDL、CRUD、事务、软删除、原生语法")
	db := openKBMySQL(t)
	if db == nil {
		return
	}
	defer finishLoggedTest(t, db)

	table := "kb_mysql_users_full"
	if err := db.DropTableIfExists(table).Error; err != nil {
		t.Fatalf("清理测试表失败: %v", err)
	}
	if testutil.KeepTestTables() {
		t.Logf("保留测试表 %s，供 KStudio 查看；下次执行会先清理该表", table)
	} else {
		defer mysqlTestCleanup(t, db, table)
	}

	runLoggedTest(t, db, "DDL_AutoMigrate_Indexes", func(t *testing.T) {
		mysqlTestTitle(t, "DDL 自动迁移与索引创建")
		if err := db.Table(table).AutoMigrate(&KBMySQLUser{}).Error; err != nil {
			t.Fatalf("自动迁移失败: %v", err)
		}
		if !db.HasTable(table) {
			t.Fatalf("表未创建成功: %s", table)
		}
		if !db.Dialect().HasColumn(table, "username") {
			t.Fatal("字段 username 未创建成功")
		}
		for _, index := range []string{
			"idx_" + table + "_deleted_at", "idx_" + table + "_email",
			"idx_" + table + "_age", "idx_" + table + "_status", "uix_" + table + "_username",
		} {
			if !db.Dialect().HasIndex(table, index) {
				t.Errorf("索引未创建成功: %s", index)
			}
		}
	})

	seed := []KBMySQLUser{
		{Username: "alice", Email: "a@test.com", Age: 18, Score: 90, Status: "active"},
		{Username: "bob", Email: "b@test.com", Age: 22, Score: 75, Status: "active"},
		{Username: "cindy", Email: "c@test.com", Age: 31, Score: 60, Status: "inactive"},
		{Username: "david", Email: "d@test.com", Age: 27, Score: 88, Status: "active"},
	}

	runLoggedTest(t, db, "CRUD_Create_Batch_Query", func(t *testing.T) {
		mysqlTestTitle(t, "基础 CRUD：插入、统计、首条查询")
		for i := range seed {
			if err := db.Table(table).Create(&seed[i]).Error; err != nil {
				t.Fatalf("插入数据失败: %v", err)
			}
		}

		var cnt int
		if err := db.Table(table).Where("status = ?", "active").Count(&cnt).Error; err != nil {
			t.Fatalf("统计数据失败: %v", err)
		}
		if cnt != 3 {
			t.Fatalf("期望 active 用户为 3，实际为 %d", cnt)
		}

		var one KBMySQLUser
		if err := db.Table(table).Where("username = ?", "alice").First(&one).Error; err != nil {
			t.Fatalf("查询首条数据失败: %v", err)
		}
		if one.Email != "a@test.com" {
			t.Fatalf("邮箱字段不符合预期: %s", one.Email)
		}
	})

	runLoggedTest(t, db, "Pagination_Order_Group_Having", func(t *testing.T) {
		mysqlTestTitle(t, "分页、排序、分组与聚合查询")
		var list []KBMySQLUser
		if err := db.Table(table).
			Where("age >= ?", 18).
			Order("score desc").
			Limit(2).
			Offset(1).
			Find(&list).Error; err != nil {
			t.Fatalf("分页查询失败: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("期望分页结果 2 条，实际为 %d 条", len(list))
		}
		if list[0].Username != "david" || list[1].Username != "bob" {
			t.Errorf("分页排序结果不符合预期: 实际=%s,%s; 预期=david,bob", list[0].Username, list[1].Username)
		}

		type Stat struct {
			Status string
			Cnt    int
			MaxAge int
		}
		var stats []Stat
		if err := db.Table(table).
			Select("status, COUNT(*) AS cnt, MAX(age) AS max_age").
			Group("status").
			Having("COUNT(*) >= ?", 1).
			Scan(&stats).Error; err != nil {
			t.Fatalf("分组聚合查询失败: %v", err)
		}
		if len(stats) != 2 {
			t.Fatalf("分组数量不符合预期: 实际=%d; 预期=2", len(stats))
		}
		for _, stat := range stats {
			switch stat.Status {
			case "active":
				if stat.Cnt != 3 || stat.MaxAge != 27 {
					t.Errorf("active 分组结果不符合预期: %#v", stat)
				}
			case "inactive":
				if stat.Cnt != 1 || stat.MaxAge != 31 {
					t.Errorf("inactive 分组结果不符合预期: %#v", stat)
				}
			default:
				t.Errorf("出现非预期分组: %#v", stat)
			}
		}
	})

	runLoggedTest(t, db, "Update_Expr_And_Save", func(t *testing.T) {
		mysqlTestTitle(t, "表达式更新与保存更新")
		if err := db.Table(table).Where("username = ?", "bob").
			Update("score", gorm.Expr("score + ?", 5)).Error; err != nil {
			t.Fatalf("表达式更新失败: %v", err)
		}

		var bob KBMySQLUser
		if err := db.Table(table).Where("username = ?", "bob").First(&bob).Error; err != nil {
			t.Fatalf("查询 bob 失败: %v", err)
		}
		if bob.Score != 80 {
			t.Fatalf("期望 bob 分数为 80，实际为 %d", bob.Score)
		}

		bob.Status = "inactive"
		if err := db.Table(table).Save(&bob).Error; err != nil {
			t.Fatalf("保存数据失败: %v", err)
		}
		var saved KBMySQLUser
		if err := db.Table(table).Where("username = ?", "bob").First(&saved).Error; err != nil {
			t.Fatalf("回读 Save 结果失败: %v", err)
		}
		if saved.Status != "inactive" || saved.Score != 80 {
			t.Fatalf("Save 结果不符合预期: status=%s; score=%d", saved.Status, saved.Score)
		}
	})

	runLoggedTest(t, db, "Transaction_Commit_And_Rollback", func(t *testing.T) {
		mysqlTestTitle(t, "事务提交与回滚")
		tx := db.Begin()
		if tx.Error != nil {
			t.Fatalf("开启事务失败: %v", tx.Error)
		}
		if err := tx.Table(table).Create(&KBMySQLUser{
			Username: "tx_commit", Email: "tx_commit@test.com", Age: 20, Score: 66, Status: "active",
		}).Error; err != nil {
			if rollbackErr := tx.Rollback().Error; rollbackErr != nil {
				t.Errorf("插入失败后回滚事务失败: %v", rollbackErr)
			}
			t.Fatalf("事务内插入数据失败: %v", err)
		}
		if err := tx.Commit().Error; err != nil {
			t.Fatalf("事务提交失败: %v", err)
		}

		var c1 int
		if err := db.Table(table).Where("username = ?", "tx_commit").Count(&c1).Error; err != nil {
			t.Fatalf("提交后查询记录失败: %v", err)
		}
		if c1 != 1 {
			t.Fatalf("提交后数据不存在，count=%d", c1)
		}

		tx2 := db.Begin()
		if tx2.Error != nil {
			t.Fatalf("开启回滚事务失败: %v", tx2.Error)
		}
		if err := tx2.Table(table).Create(&KBMySQLUser{
			Username: "tx_rollback", Email: "tx_rollback@test.com", Age: 19, Score: 50, Status: "active",
		}).Error; err != nil {
			if rollbackErr := tx2.Rollback().Error; rollbackErr != nil {
				t.Errorf("插入失败后回滚事务失败: %v", rollbackErr)
			}
			t.Fatalf("回滚事务内插入数据失败: %v", err)
		}
		if err := tx2.Rollback().Error; err != nil {
			t.Fatalf("事务回滚失败: %v", err)
		}

		var c2 int
		if err := db.Table(table).Where("username = ?", "tx_rollback").Count(&c2).Error; err != nil {
			t.Fatalf("回滚后查询记录失败: %v", err)
		}
		if c2 != 0 {
			t.Fatalf("回滚后数据不应存在，count=%d", c2)
		}
	})

	runLoggedTest(t, db, "SoftDelete_And_Unscoped", func(t *testing.T) {
		mysqlTestTitle(t, "软删除与 Unscoped 查询")
		var user KBMySQLUser
		if err := db.Table(table).Where("username = ?", "cindy").First(&user).Error; err != nil {
			t.Fatalf("查询 cindy 失败: %v", err)
		}
		if err := db.Table(table).Delete(&user).Error; err != nil {
			t.Fatalf("软删除失败: %v", err)
		}

		var normalCnt int
		if err := db.Table(table).Where("username = ? AND deleted_at IS NULL", "cindy").Count(&normalCnt).Error; err != nil {
			t.Fatalf("软删除后查询可见记录失败: %v", err)
		}
		if normalCnt != 0 {
			t.Fatalf("软删除后记录应被隐藏，count=%d", normalCnt)
		}

		var unscopedCnt int
		if err := db.Table(table).Unscoped().Where("username = ?", "cindy").Count(&unscopedCnt).Error; err != nil {
			t.Fatalf("Unscoped 查询软删除记录失败: %v", err)
		}
		if unscopedCnt != 1 {
			t.Fatalf("Unscoped 查询应包含 1 条软删除记录，实际=%d", unscopedCnt)
		}
	})

	runLoggedTest(t, db, "RawSQL_MySQLStyle_Normal_And_SpecialSyntax", func(t *testing.T) {
		mysqlTestTitle(t, "原生 MySQL 常规与特殊语法")
		var rows []KBMySQLUser
		sql1 := fmt.Sprintf("SELECT `id`,`username`,`email`,`age`,`score`,`status`,`created_at`,`updated_at`,`deleted_at` FROM `%s` WHERE `age` >= ? ORDER BY `id` ASC LIMIT 2", table)
		if err := db.Raw(sql1, 18).Scan(&rows).Error; err != nil {
			t.Fatalf("原生 MySQL 常规语法 SQL 执行失败: %v", err)
		}
		if len(rows) != 2 || rows[0].Username != "alice" || rows[1].Username != "bob" {
			t.Fatalf("原生 MySQL 查询结果不符合预期: %#v", rows)
		}

		type FuncRow struct {
			Username string
			Nick     string
			DayStr   string
		}
		var out []FuncRow
		sql2 := fmt.Sprintf(
			"SELECT `username`, IFNULL(`email`, 'none') AS `nick`, DATE_FORMAT(`created_at`, '%%Y-%%m-%%d') AS `day_str` FROM `%s` WHERE `id` > 0 ORDER BY `id` ASC LIMIT 3",
			table,
		)
		if err := db.Raw(sql2).Scan(&out).Error; err != nil {
			t.Fatalf("原生 MySQL 特殊语法 SQL 执行失败: %v", err)
		}
		if len(out) != 3 {
			t.Fatalf("原生 MySQL 特殊语法 SQL 行数不符合预期: 实际=%d; 预期=3", len(out))
		}
		for i, row := range out {
			if row.Username != seed[i].Username || row.Nick != seed[i].Email {
				t.Errorf("原生函数查询结果不符合预期: %#v", row)
			}
			if _, err := time.Parse("2006-01-02", row.DayStr); err != nil {
				t.Errorf("DATE_FORMAT 格式不符合预期: value=%s; error=%v", row.DayStr, err)
			}
		}
	})

}

func TestKingbaseMySQLMode_SpecialTypesAndFunctions(t *testing.T) {
	mysqlTestTitle(t, "MySQL 特性专项测试：特有类型、主键约束、函数兼容")
	db := openKBMySQL(t)
	if db == nil {
		return
	}
	defer finishLoggedTest(t, db)

	table := "kb_mysql_special_types"
	if err := db.DropTableIfExists(table).Error; err != nil {
		t.Fatalf("清理测试表失败: %v", err)
	}
	defer mysqlTestCleanup(t, db, table)

	runLoggedTest(t, db, "MySQL特有数据类型测试", func(t *testing.T) {
		mysqlTestTitle(t, "MySQL 特有数据类型映射与读写")
		if err := db.Table(table).AutoMigrate(&KBMySQLSpecialType{}).Error; err != nil {
			t.Fatalf("特殊类型表自动迁移失败: %v", err)
		}

		row := KBMySQLSpecialType{
			Name:    fmt.Sprintf("row_%d", time.Now().UnixNano()),
			Price:   123.45,
			Flag:    "Y",
			Meta:    `{"k":"v","n":1}`,
			RawData: []byte{0x01, 0x02, 0x03},
			Note:    sql.NullString{String: "hello", Valid: true},
		}
		if err := db.Table(table).Create(&row).Error; err != nil {
			t.Fatalf("插入特殊类型数据失败: %v", err)
		}

		var got KBMySQLSpecialType
		if err := db.Table(table).Where("name = ?", row.Name).First(&got).Error; err != nil {
			t.Fatalf("查询特殊类型数据失败: %v", err)
		}
		if got.ID == 0 || got.Name != row.Name || got.Price != row.Price || got.Flag != row.Flag ||
			!bytes.Equal(got.RawData, row.RawData) || got.Note != row.Note || got.CreatedAt.IsZero() {
			t.Fatalf("特殊类型回读不符合预期: 实际=%#v; 写入=%#v", got, row)
		}
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(got.Meta), &meta); err != nil || meta["k"] != "v" || meta["n"] != float64(1) {
			t.Fatalf("JSON 回读不符合预期: meta=%s; error=%v", got.Meta, err)
		}
	})

	runLoggedTest(t, db, "自增主键与特殊约束测试", func(t *testing.T) {
		mysqlTestTitle(t, "自增主键与唯一约束")
		name := fmt.Sprintf("unique_%d", time.Now().UnixNano())
		first := KBMySQLSpecialType{Name: name, Price: 1.00, Flag: "Y", Meta: "{}"}
		if err := db.Table(table).Create(&first).Error; err != nil {
			t.Fatalf("首条插入失败: %v", err)
		}

		var inserted KBMySQLSpecialType
		if err := db.Table(table).Where("name = ?", name).First(&inserted).Error; err != nil {
			t.Fatalf("查询首条记录失败: %v", err)
		}
		if inserted.ID == 0 {
			t.Fatal("自增主键未生效，数据库中的 ID 仍为 0")
		}
		if first.ID != inserted.ID {
			t.Fatalf("自增 ID 回填不符合预期: 模型 ID=%d; 数据库 ID=%d", first.ID, inserted.ID)
		}

		dup := KBMySQLSpecialType{Name: name, Price: 2.00, Flag: "N", Meta: "{}"}
		assertUniqueViolation(t, db, "uix_kb_mysql_special_types_name", func() error {
			return db.Table(table).Create(&dup).Error
		})
	})

	runLoggedTest(t, db, "MySQL函数兼容性测试", func(t *testing.T) {
		mysqlTestTitle(t, "MySQL 函数兼容性")
		var source KBMySQLSpecialType
		if err := db.Table(table).Order("id asc").First(&source).Error; err != nil {
			t.Fatalf("读取函数测试基准记录失败: %v", err)
		}
		mysqlCheckFunctions(t, db, []mysqlFunctionCheck{
			{name: "CONCAT", query: "SELECT CONCAT('ab', '_x') AS actual", want: "ab_x"},
			{name: "LENGTH", query: "SELECT LENGTH('abc') AS actual", want: "3", numeric: true},
			{name: "SUBSTRING", query: "SELECT SUBSTRING('abcdef', 2, 3) AS actual", want: "bcd"},
			{name: "IFNULL", query: "SELECT IFNULL(NULL, 'fallback') AS actual", want: "fallback"},
			{name: "DATE_FORMAT", query: fmt.Sprintf("SELECT DATE_FORMAT(created_at, '%%Y-%%m-%%d') AS actual FROM `%s` WHERE id = ?", table), args: []interface{}{source.ID}, want: source.CreatedAt.Format("2006-01-02")},
		})
	})
}

func TestKingbaseMySQLMode_AdvancedCompatibility(t *testing.T) {
	mysqlTestTitle(t, "MySQL 模式高级兼容测试：DDL 变更、索引生命周期、约束冲突、函数语法")
	db := openKBMySQL(t)
	if db == nil {
		return
	}
	defer finishLoggedTest(t, db)

	table := fmt.Sprintf("kb_mysql_adv_%d", time.Now().Unix())
	if err := db.DropTableIfExists(table).Error; err != nil {
		t.Fatalf("清理测试表失败: %v", err)
	}
	defer mysqlTestCleanup(t, db, table)

	runLoggedTest(t, db, "DDL变更与索引生命周期", func(t *testing.T) {
		mysqlTestTitle(t, "DDL 变更与索引生命周期")
		observer := &mysqlDDLObserver{SQLCommon: db.CommonDB(), t: t}
		ddlDB, err := gorm.Open(db.Dialect().GetName(), observer)
		if err != nil {
			t.Fatalf("初始化 DDL 执行观测失败: %v; 完整错误结构=%#v", err, err)
		}
		// ddlDB 借用 db 的连接，其生命周期由外层测试负责；驱动错误由测试驱动包装器独立记录。
		ddlDB.LogMode(false)
		if err := ddlDB.Table(table).AutoMigrate(&KBMySQLConstraintRow{}).Error; err != nil {
			t.Fatalf("高级表自动迁移失败: %v", err)
		}
		if !ddlDB.HasTable(table) {
			t.Fatalf("高级表未创建成功: %s", table)
		}
		t.Log("[通过] 自动建表")

		if err := ddlDB.Table(table).AddIndex("idx_adv_name", "name").Error; err != nil {
			t.Fatalf("新增索引失败: %v", err)
		}
		indexCount := func() (int, error) {
			const query = "SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = ? AND indexname = ?"
			var count int
			queryErr := db.CommonDB().QueryRow(query, table, "idx_adv_name").Scan(&count)
			return count, queryErr
		}
		if count, err := indexCount(); err != nil || count != 1 {
			t.Fatalf("删除前索引核验失败: count=%d; error=%v", count, err)
		}
		t.Log("[通过] 新增索引")

		beforeExecutions := observer.execCount
		gormErr := ddlDB.Table(table).RemoveIndex("idx_adv_name").Error
		if observer.execCount != beforeExecutions+1 {
			t.Fatalf("删除索引未观测到预期的单次 Exec: 实际次数=%d", observer.execCount-beforeExecutions)
		}
		// 保留驱动观察结果，独立检查 GORM 错误传播及执行后的数据库状态。
		removeErr := observer.lastError
		// 即使执行失败也检查数据库，使日志同时包含原始驱动错误和执行后的真实索引状态。
		remaining, checkErr := indexCount()
		switch {
		case checkErr != nil:
			t.Errorf("[未通过] 删除索引：无法核验删除结果: %v", checkErr)
		case remaining != 0:
			t.Errorf("[未通过] 删除索引：索引仍存在（数量=%d）", remaining)
		case removeErr != nil:
			t.Error("[未通过] 删除索引：金仓驱动返回错误")
		case gormErr != nil:
			t.Errorf("[未通过] 删除索引: %v", gormErr)
		default:
			t.Log("[通过] 删除索引")
		}
		modifyErr := ddlDB.Table(table).ModifyColumn("name", "varchar(256)").Error
		if modifyErr != nil {
			t.Errorf("字段变更失败: %v", modifyErr)
		}
		var column struct {
			MaxLength    int
			DeclaredType string
		}
		// 该实例在 information_schema 中把有界 VARCHAR 长度报告为 -1；因此验证精确物理定义，
		// 不能接受任意长度为 -1 的结果。
		query := "SELECT c.character_maximum_length AS max_length, format_type(a.atttypid,a.atttypmod) AS declared_type FROM information_schema.columns c JOIN pg_namespace n ON n.nspname=c.table_schema JOIN pg_class t ON t.relnamespace=n.oid AND t.relname=c.table_name JOIN pg_attribute a ON a.attrelid=t.oid AND a.attname=c.column_name WHERE c.table_schema=current_schema() AND c.table_name=? AND c.column_name=?"
		if err := db.Raw(query, table, "name").Scan(&column).Error; err != nil {
			t.Errorf("字段变更后核验失败: %v", err)
		} else if column.DeclaredType != "character varying(256)" {
			t.Errorf("字段变更未生效: 实际定义=%s; 目录长度=%d; 预期=character varying(256)", column.DeclaredType, column.MaxLength)
		} else if modifyErr == nil {
			t.Logf("[通过] 字段长度修改为 256；实际定义=%s；information_schema 长度=%d", column.DeclaredType, column.MaxLength)
		}
	})

	runLoggedTest(t, db, "批量数据与统计校验", func(t *testing.T) {
		mysqlTestTitle(t, "批量数据与统计校验")
		rows := []KBMySQLConstraintRow{
			{TenantID: 1, Code: "A001", Name: "订单A", Amount: 12.3456, Active: true, OccurredAt: time.Now()},
			{TenantID: 1, Code: "A002", Name: "订单B", Amount: 88.8800, Active: true, OccurredAt: time.Now()},
			{TenantID: 2, Code: "A001", Name: "订单C", Amount: 30.5000, Active: false, OccurredAt: time.Now()},
		}
		for i := range rows {
			if err := db.Table(table).Create(&rows[i]).Error; err != nil {
				t.Fatalf("批量写入失败: %v", err)
			}
		}

		var total int
		if err := db.Table(table).Count(&total).Error; err != nil {
			t.Fatalf("总数统计失败: %v", err)
		}
		if total != 3 {
			t.Fatalf("期望 3 条记录，实际 %d", total)
		}

		type Agg struct {
			TenantID int
			Cnt      int
			SumAmt   float64
		}
		var aggs []Agg
		if err := db.Table(table).
			Select("tenant_id, COUNT(*) AS cnt, SUM(amount) AS sum_amt").
			Group("tenant_id").
			Order("tenant_id asc").
			Scan(&aggs).Error; err != nil {
			t.Fatalf("聚合统计失败: %v", err)
		}
		if len(aggs) != 2 {
			t.Fatalf("租户分组数量不符合预期: 实际=%d; 预期=2", len(aggs))
		}
		for _, agg := range aggs {
			switch agg.TenantID {
			case 1:
				if agg.Cnt != 2 || math.Abs(agg.SumAmt-101.2256) > 1e-6 {
					t.Errorf("租户 1 聚合结果不符合预期: %#v", agg)
				}
			case 2:
				if agg.Cnt != 1 || math.Abs(agg.SumAmt-30.5) > 1e-6 {
					t.Errorf("租户 2 聚合结果不符合预期: %#v", agg)
				}
			default:
				t.Errorf("出现非预期租户分组: %#v", agg)
			}
		}
	})

	runLoggedTest(t, db, "复合唯一约束冲突测试", func(t *testing.T) {
		mysqlTestTitle(t, "复合唯一约束冲突：tenant_id + code")
		okRow := KBMySQLConstraintRow{
			TenantID: 9, Code: "DUP1", Name: "基准记录", Amount: 1.23, Active: true, OccurredAt: time.Now(),
		}
		if err := db.Table(table).Create(&okRow).Error; err != nil {
			t.Fatalf("插入基准记录失败: %v", err)
		}

		dup := KBMySQLConstraintRow{
			TenantID: 9, Code: "DUP1", Name: "重复记录", Amount: 9.99, Active: true, OccurredAt: time.Now(),
		}
		assertUniqueViolation(t, db, "uix_tenant_code", func() error {
			return db.Table(table).Create(&dup).Error
		})
	})

	runLoggedTest(t, db, "更多MySQL函数兼容性", func(t *testing.T) {
		mysqlTestTitle(t, "更多 MySQL 函数兼容性")
		var source KBMySQLConstraintRow
		if err := db.Table(table).Order("id asc").First(&source).Error; err != nil {
			t.Fatalf("读取函数测试基准记录失败: %v", err)
		}
		mysqlCheckFunctions(t, db, []mysqlFunctionCheck{
			{name: "CONCAT_WS", query: "SELECT CONCAT_WS('-', 1, 'A001') AS actual", want: "1-A001"},
			{name: "ROUND", query: "SELECT ROUND(12.3456, 2) AS actual", want: "12.35", numeric: true},
			{name: "CAST", query: "SELECT CAST(true AS int) AS actual", want: "1", numeric: true},
			{name: "IFNULL", query: "SELECT IFNULL(NULL, false) AS actual", want: "false", boolean: true},
			{name: "DATE_FORMAT", query: fmt.Sprintf("SELECT DATE_FORMAT(occurred_at, '%%Y-%%m-%%d') AS actual FROM `%s` WHERE id = ?", table), args: []interface{}{source.ID}, want: source.OccurredAt.Format("2006-01-02")},
		})
	})

}
