//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jinzhu/gorm"
)

func TestKingbaseMySQLMode_AutoIncrementAdapter(t *testing.T) {
	runMySQLDDLCases(t, "Kingbase 适配层自增验收：原始 ORM 输入，核验落库与 GORM V1 标准回填", []mysqlDDLTestCase{
		{"01_SignedNativeSemantics", "有符号原生自增的 NULL、零值和显式值", mysqlAutoSigned},
		{"02_SignedPrimaryKeyBackfill", "有符号主键自增回填", mysqlAutoSignedPrimary},
		{"03_UnsignedNoAutoValueOnZero", "无符号自增遵循事务内 NO_AUTO_VALUE_ON_ZERO（落库值）", mysqlAutoUnsignedMode},
		{"04_UnsignedConcurrentNull", "无符号 NULL 自增并发分配不重复（落库值）", mysqlAutoUnsignedConcurrent},
		{"05_UnsignedRollbackAndOrdinaryNull", "无符号自增事务回滚与普通 NULL 保留", mysqlAutoUnsignedRollback},
		{"06_UnsignedExplicitValueAdvances", "无符号显式值推进序列且较小值不回退", mysqlAutoUnsignedExplicit},
		{"07_UnsignedPrimaryAndOmittedColumn", "无符号主键和省略列遵循显式值后的自增", mysqlAutoUnsignedOmitted},
		{"08_UnsignedExplicitRollbackAndFailure", "显式值回滚不重用，拒绝越界不污染序列", mysqlAutoUnsignedExplicitRollback},
		{"09_UnsignedConcurrentExplicitAndGenerated", "并发显式值与自动生成不回退、不重复（落库值）", mysqlAutoUnsignedMixed},
	})
}

func mysqlAutoUnsignedMixed(c *mysqlDDLCase) {
	table := mysqlPropertyTable(c, &mysqlAutoCheckedRow{})
	const workers = 6
	if !c.operation("并发 ORM 显式值及 NULL 生成", func() error {
		results := make(chan error, workers)
		for i := 1; i <= workers; i++ {
			go func(id int) {
				value := uint32(id * 100)
				err := c.db.Table(table).Create(&mysqlAutoNullRow{ID: id * 2, Ticket: &value}).Error
				if err == nil {
					row := mysqlAutoNullRow{ID: id*2 + 1}
					err = c.db.Table(table).Create(&row).Error
					if err == nil {
						var generated uint32
						err = c.db.CommonDB().QueryRow(
							"SELECT ticket FROM "+c.quote(table)+" WHERE id=?",
							row.ID,
						).Scan(&generated)
						if err == nil && generated <= value {
							err = fmt.Errorf("显式 %d 后落库生成值未推进: %d", value, generated)
						}
					}
				}
				results <- err
			}(i)
		}
		var failure error
		for i := 0; i < workers; i++ {
			if err := <-results; err != nil {
				failure = err
			}
		}
		return failure
	}) {
		return
	}
	c.count("并发记录及自增值均唯一", "SELECT COUNT(DISTINCT ticket) FROM "+c.quote(table), workers*2)
	var maximum uint32
	if err := c.db.CommonDB().QueryRow("SELECT MAX(ticket) FROM " + c.quote(table)).Scan(&maximum); err != nil {
		c.t.Fatal(err)
	}
	last := mysqlAutoNullRow{ID: 99}
	if !c.operation("并发结束后继续生成", func() error { return c.db.Table(table).Create(&last).Error }) {
		return
	}
	c.count("序列从实际最大值继续", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=99 AND ticket=?", 1, maximum+1)
}

func mysqlAutoUnsignedExplicit(c *mysqlDDLCase) {
	table := mysqlPropertyTable(c, &mysqlAutoCheckedRow{})
	for i, item := range []struct{ input, want uint32 }{{12, 12}, {0, 13}, {5, 5}, {0, 14}} {
		row := mysqlAutoCheckedRow{ID: i + 1, Ticket: item.input}
		if !c.operation("ORM 显式值与默认生成", func() error { return c.db.Table(table).Create(&row).Error }) {
			return
		}
		if row.Ticket != item.want {
			c.t.Errorf("[未通过] 回填=%d，预期=%d", row.Ticket, item.want)
		}
		c.count("落库值正确", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=? AND ticket=?", 1, i+1, item.want)
	}
	c.prepare("DELETE FROM " + c.quote(table) + " WHERE ticket=14")
	row := mysqlAutoNullRow{ID: 5}
	if !c.operation("删除最大值后 NULL 继续递增", func() error { return c.db.Table(table).Create(&row).Error }) {
		return
	}
	c.count("删除不回退序列", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=5 AND ticket=15", 1)
}

func mysqlAutoUnsignedOmitted(c *mysqlDDLCase) {
	type row struct {
		ID   uint32 `gorm:"primary_key"`
		Name string
	}
	table := mysqlPropertyTable(c, &row{})
	first := row{ID: 12, Name: "explicit"}
	if !c.operation("ORM 显式无符号主键", func() error { return c.db.Table(table).Create(&first).Error }) {
		return
	}
	next := row{Name: "generated"}
	if !c.operation("ORM 省略空主键并回填", func() error { return c.db.Table(table).Create(&next).Error }) {
		return
	}
	if next.ID != 13 {
		c.t.Errorf("[未通过] 主键回填=%d，预期=13", next.ID)
	}
	// 调用方的数据载体可以完全省略自增列。
	type nameOnly struct{ Name string }
	if !c.operation("ORM 数据模型未包含主键", func() error { return c.db.Table(table).Create(&nameOnly{Name: "name_only"}).Error }) {
		return
	}
	c.count("未包含主键也继续递增", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=14 AND name='name_only'", 1)
	c.prepare("INSERT INTO " + c.quote(table) + " (id,name) VALUES (50,'existing')")
	if !c.operation("已有显式大值后的 ORM 插入", func() error { return c.db.Table(table).Create(&nameOnly{Name: "after_existing"}).Error }) {
		return
	}
	c.count("已有数据纳入序列同步", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=51 AND name='after_existing'", 1)
}

func mysqlAutoUnsignedExplicitRollback(c *mysqlDDLCase) {
	table := mysqlPropertyTable(c, &mysqlAutoCheckedRow{})
	mysqlAutoInTransaction(c, func(tx *gorm.DB) {
		row := mysqlAutoCheckedRow{ID: 1, Ticket: 100}
		c.operation("事务内显式大值", func() error { return tx.Table(table).Create(&row).Error })
	})
	c.count("显式记录已回滚", "SELECT COUNT(*) FROM "+c.quote(table), 0)
	type wideRow struct {
		ID     int `gorm:"primary_key;AUTO_INCREMENT:false"`
		Ticket uint64
	}
	mysqlAdapterReject(c, "22003", "", "", func() error { return c.db.Table(table).Create(&wideRow{ID: 2, Ticket: 4294967296}).Error })
	row := mysqlAutoNullRow{ID: 3}
	if !c.operation("回滚及失败后继续生成", func() error { return c.db.Table(table).Create(&row).Error }) {
		return
	}
	c.count("回滚不重用且越界不污染序列", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=3 AND ticket=101", 1)
}

type mysqlAutoCheckedRow struct {
	ID     int    `gorm:"primary_key;AUTO_INCREMENT:false"`
	Ticket uint32 `gorm:"type:int unsigned AUTO_INCREMENT;AUTO_INCREMENT;index;not null;unique;comment:'CheckedTicket'"`
	Note   *int
}

// 此数据载体有意不声明 AUTO_INCREMENT 标签；INSERT 省略该列时数据库默认值仍应生效。
type mysqlAutoNullRow struct {
	ID     int `gorm:"primary_key;AUTO_INCREMENT:false"`
	Ticket *uint32
	Note   *int
}

func mysqlAutoSigned(c *mysqlDDLCase) {
	type row struct {
		ID     int   `gorm:"primary_key;AUTO_INCREMENT:false"`
		Ticket int64 `gorm:"type:bigint AUTO_INCREMENT;AUTO_INCREMENT;index;not null;unique;comment:'NativeTicket'"`
	}
	type inputRow struct {
		ID     int `gorm:"primary_key;AUTO_INCREMENT:false"`
		Ticket *int64
	}
	table := mysqlPropertyTable(c, &row{})
	mysqlPropertyComment(c, table, "ticket", "NativeTicket")
	zero, explicit := int64(0), int64(12)
	for i, item := range []struct {
		value *int64
		want  int64
	}{{nil, 1}, {&zero, 2}, {&explicit, 12}} {
		input := inputRow{ID: i + 1, Ticket: item.value}
		if !c.operation("ORM 传入自增值", func() error { return c.db.Table(table).Create(&input).Error }) {
			return
		}
		c.count("原生自增值正确", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=? AND ticket=?", 1, i+1, item.want)
	}
	last := row{ID: 4}
	if !c.operation("显式值后的 ORM 自增回填", func() error { return c.db.Table(table).Create(&last).Error }) {
		return
	}
	if last.Ticket != 13 {
		c.t.Errorf("[未通过] 显式 12 后应生成 13，实际=%d", last.Ticket)
	}
	c.count("显式值后序列继续", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=4 AND ticket=13", 1)
}

func mysqlAutoSignedPrimary(c *mysqlDDLCase) {
	type row struct {
		ID   int64 `gorm:"primary_key"`
		Name string
	}
	table := mysqlPropertyTable(c, &row{})
	for i := int64(1); i <= 2; i++ {
		input := row{Name: fmt.Sprintf("row%d", i)}
		if !c.operation("ORM 原生主键自增回填", func() error { return c.db.Table(table).Create(&input).Error }) {
			return
		}
		if input.ID != i {
			c.t.Errorf("[未通过] 主键回填=%d，预期=%d", input.ID, i)
		}
		c.count("主键及数据正确", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=? AND name=?", 1, i, input.Name)
	}
}

func mysqlAutoInTransaction(c *mysqlDDLCase, run func(*gorm.DB)) {
	c.t.Helper()
	parent := c.db
	tx := parent.Begin()
	if tx.Error != nil {
		c.t.Fatalf("[未通过] 开启事务: %v", tx.Error)
	}
	c.db = tx
	defer func() {
		c.db = parent
		if err := tx.Rollback().Error; err != nil {
			c.t.Errorf("[未通过] 回滚测试事务: %v", err)
		}
	}()
	run(tx)
}

func mysqlAutoUnsignedMode(c *mysqlDDLCase) {
	table := mysqlPropertyTable(c, &mysqlAutoCheckedRow{})
	mysqlAutoInTransaction(c, func(tx *gorm.DB) {
		var flags string
		if err := tx.CommonDB().QueryRow("SHOW sql_mode").Scan(&flags); err != nil {
			c.t.Fatalf("[未通过] 读取 sql_mode: %v", err)
		}
		flags += ",NO_AUTO_VALUE_ON_ZERO"
		// true 使该设置只在固定事务内生效。这里使用参数接口，因为 SET 对带引号逗号列表
		// 的解析规则不同。
		var activeFlags string
		if err := tx.CommonDB().QueryRow("SELECT set_config('sql_mode', ?, true)", flags).Scan(&activeFlags); err != nil {
			c.t.Fatalf("[准备失败] 设置事务内 sql_mode: %v", err)
		}
		if !strings.Contains(activeFlags, "NO_AUTO_VALUE_ON_ZERO") {
			c.t.Fatalf("[准备失败] 自增模式未生效: %s", activeFlags)
		}
		zero := uint32(0)
		input := mysqlAutoNullRow{ID: 1, Ticket: &zero}
		if !c.operation("ORM 显式零值按模式保留", func() error { return tx.Table(table).Create(&input).Error }) {
			return
		}
		c.count("NO_AUTO_VALUE_ON_ZERO 保留零值", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=1 AND ticket=0", 1)
		null := mysqlAutoNullRow{ID: 2}
		if !c.operation("该模式下 NULL 仍触发自增", func() error { return tx.Table(table).Create(&null).Error }) {
			return
		}
		c.t.Log("[限制] GORM V1 不回填未声明默认/自增元数据的非主键字段；以落库值核验")
		c.count("NULL 生成 1", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=2 AND ticket=1", 1)
	})
	c.count("测试事务已回滚", "SELECT COUNT(*) FROM "+c.quote(table), 0)
}

func mysqlAutoUnsignedConcurrent(c *mysqlDDLCase) {
	table := mysqlPropertyTable(c, &mysqlAutoCheckedRow{})
	const workers = 6
	if !c.operation("并发 ORM NULL 自增", func() error {
		results := make(chan error, workers)
		for i := 1; i <= workers; i++ {
			go func(id int) {
				row := mysqlAutoNullRow{ID: id}
				err := c.db.Table(table).Create(&row).Error
				results <- err
			}(i)
		}
		var failure error
		for i := 0; i < workers; i++ {
			if err := <-results; err != nil {
				failure = err
			}
		}
		return failure
	}) {
		return
	}
	c.t.Log("[限制] 无标签数据载体的非主键自增值不要求 GORM V1 回填；查询数据库核验")
	c.count("并发写入行数", "SELECT COUNT(*) FROM "+c.quote(table), workers)
	c.count("并发自增值唯一且为正", "SELECT COUNT(DISTINCT ticket) FROM "+c.quote(table)+" WHERE ticket BETWEEN 1 AND 6", workers)
}

func mysqlAutoUnsignedRollback(c *mysqlDDLCase) {
	table := mysqlPropertyTable(c, &mysqlAutoCheckedRow{})
	mysqlAutoInTransaction(c, func(tx *gorm.DB) {
		input := mysqlAutoNullRow{ID: 1}
		if !c.operation("事务内 NULL 自增", func() error { return tx.Table(table).Create(&input).Error }) {
			return
		}
		c.count("事务内自增值正确且普通 NULL 未改写", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=1 AND ticket=1 AND note IS NULL", 1)
	})
	c.count("回滚后无记录", "SELECT COUNT(*) FROM "+c.quote(table), 0)
	input := mysqlAutoNullRow{ID: 2}
	if !c.operation("回滚后继续自增", func() error { return c.db.Table(table).Create(&input).Error }) {
		return
	}
	c.count("不重用已分配序列值且普通 NULL 保留", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=2 AND ticket=2 AND note IS NULL", 1)
}
