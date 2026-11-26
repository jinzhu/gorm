//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jinzhu/gorm"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
	"kingbase.com/gokb"
)

// 每个场景都从调用方 ORM API 和类型标签进入，不使用测试侧替换 SQL 或重试来掩盖
// 不受支持的调用方定义。
func TestKingbaseMySQLMode_AdapterBoundaries(t *testing.T) {
	cases := []mysqlDDLTestCase{
		{"01_OffsetWithoutLimit", "ORM 独立 Offset", mysqlAdapterOffset},
		{"02_QuotedIdentifiers", "ORM 标识符内部反引号", mysqlAdapterQuotedIdentifiers},
	}
	for _, item := range []struct{ typ, maximum, overflow, code, constraint string }{
		{"tinyint unsigned", "255", "256", "23514", "unsignedtinyint_check"},
		{"smallint unsigned", "65535", "65536", "23514", "unsignedsmallint_check"},
		{"mediumint unsigned", "16777215", "16777216", "23514", "unsignedmediumint_check"},
		{"int unsigned", "4294967295", "4294967296", "22003", ""},
		{"integer(11) unsigned", "4294967295", "4294967296", "22003", ""},
		{"bigint unsigned", "18446744073709551615", "18446744073709551616", "22003", ""},
	} {
		item := item
		cases = append(cases, mysqlDDLTestCase{"03_" + strings.ReplaceAll(item.typ, " ", "_"), "ORM " + item.typ + " 范围", func(c *mysqlDDLCase) {
			mysqlAdapterUnsigned(c, item.typ, item.maximum, item.overflow, item.code, item.constraint)
		}})
	}
	for _, typ := range []string{"tinytext", "mediumtext", "longtext", "tinyblob", "mediumblob", "longblob"} {
		typ := typ
		cases = append(cases, mysqlDDLTestCase{"04_" + typ, "ORM " + typ + " 容量", func(c *mysqlDDLCase) { mysqlAdapterCapacity(c, typ) }})
	}
	cases = append(cases,
		mysqlDDLTestCase{"05_Uint32AutoIncrement", "ORM UINT32 自增范围", mysqlAdapterUint32Auto},
		mysqlDDLTestCase{"06_ModifyUnsigned", "ORM 修改小整数 UNSIGNED", mysqlAdapterModifyUnsigned},
		mysqlDDLTestCase{"07_ModifyUnsignedAtomicFailure", "ORM 无符号修改失败保持原状态", mysqlAdapterModifyUnsignedAtomicFailure},
		mysqlDDLTestCase{"08_Uint64ManualPrimaryKey", "ORM UINT64 手动主键完整范围", mysqlAdapterUint64Manual},
	)
	runMySQLDDLCases(t, "ORM 方言边界验收：原始接口和类型标签，严格检查实际结果", cases)
}

func mysqlAdapterOffset(c *mysqlDDLCase) {
	table := c.table("id INTEGER PRIMARY KEY")
	c.prepare("INSERT INTO " + c.quote(table) + " VALUES (1), (2), (3), (4), (5)")
	for _, removeLimit := range []bool{false, true} {
		query := c.db.Table(table).Order("id ASC").Offset(2)
		if removeLimit {
			query = query.Limit(-1)
		}
		var rows []struct{ ID int }
		if !c.observeOperation("ORM 独立 Offset", "Query", func() error { return query.Find(&rows).Error }) {
			return
		}
		got := make([]int, len(rows))
		for i, row := range rows {
			got[i] = row.ID
		}
		if !reflect.DeepEqual(got, []int{3, 4, 5}) {
			c.t.Errorf("[未通过] Offset(2)，Limit(-1)=%t：实际=%v；预期=[3 4 5]", removeLimit, got)
		} else {
			c.t.Logf("[状态通过] Offset(2)，Limit(-1)=%t：结果=%v", removeLimit, got)
		}
	}
}

func mysqlAdapterQuotedIdentifiers(c *mysqlDDLCase) {
	type row struct {
		ID    int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Value string "gorm:\"column:val`ue;type:varchar(32)\""
	}
	table := testutil.UniqueTable("kb_quote") + "`test"
	c.cleanup = append(c.cleanup, table)
	if !c.operation("ORM 建立特殊名称表", func() error { return c.db.Table(table).AutoMigrate(&row{}).Error }) {
		return
	}
	if !c.db.Dialect().HasTable(table) || !c.db.Dialect().HasColumn(table, "val`ue") {
		c.t.Fatal("[未通过] 元数据未找到原始表名或列名")
	}
	value := row{ID: 1, Value: "KeepCase"}
	if !c.operation("ORM 写入特殊名称列", func() error { return c.db.Table(table).Create(&value).Error }) {
		return
	}
	var got row
	if err := c.db.Table(table).Where("id = ?", 1).First(&got).Error; err != nil || got.Value != value.Value {
		c.t.Fatalf("[未通过] 特殊名称回读：value=%q；error=%v", got.Value, err)
	}
	if !c.operation("ORM 删除特殊名称列", func() error { return c.db.Table(table).DropColumn("val`ue").Error }) {
		return
	}
	c.columnCount("特殊名称列已删除", table, "val`ue", 0)
	c.count("原记录仍在", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id = 1", 1)
}

// 运行时结构体保持传入的 GORM 类型标签不变，并在各受支持类型族之间复用相同的
// schema 和边界断言。
func mysqlAdapterValue(typ string, value interface{}) interface{} {
	kind := reflect.TypeOf(value)
	model := reflect.New(reflect.StructOf([]reflect.StructField{{Name: "Value", Type: kind, Tag: reflect.StructTag(`gorm:"column:value;type:` + typ + `"`)}}))
	model.Elem().Field(0).Set(reflect.ValueOf(value))
	return model.Interface()
}

func mysqlAdapterDeclaredType(c *mysqlDDLCase, table string) string {
	var declared string
	query := "SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a JOIN pg_class t ON t.oid = a.attrelid JOIN pg_namespace n ON n.oid = t.relnamespace WHERE n.nspname = ? AND t.relname = ? AND a.attname = 'value' AND a.attnum > 0 AND NOT a.attisdropped"
	if err := c.db.CommonDB().QueryRow(query, c.schema, table).Scan(&declared); err != nil {
		c.t.Fatalf("[未通过] 读取真实字段类型: %v", err)
	}
	c.t.Logf("[字段定义] value=%s", declared)
	return declared
}

func mysqlAdapterReject(c *mysqlDDLCase, code, table, constraint string, run func() error) {
	c.t.Helper()
	log := testutil.DriverLogFor(c.db)
	since := log.Snapshot()
	match := func(err error) bool {
		return mysqlAdapterMatchesError(err, code, table, constraint)
	}
	err := log.WithExpectedError(match, run)
	if !match(err) {
		c.t.Fatalf("[未通过] 要求 SQLSTATE=%s、目标对象匹配；实际=%v", code, err)
	}
	if n := log.ExpectErrors(since, match); n != 1 {
		c.t.Fatalf("[未通过] 要求一次预期驱动错误；实际=%d", n)
	}
	c.t.Logf("[状态通过] 非法值被拒绝（%s）", code)
}

func mysqlAdapterMatchesError(err error, code, table, constraint string) bool {
	if all, ok := err.(gorm.Errors); ok {
		if len(all) == 0 {
			return false
		}
		for _, item := range all {
			if !mysqlAdapterMatchesError(item, code, table, constraint) {
				return false
			}
		}
		return true
	}
	e, ok := err.(*gokb.Error)
	if !ok || e == nil || string(e.Code) != code {
		return false
	}
	if table != "" && e.Table != table {
		return false
	}
	if constraint != "" && e.Constraint != constraint {
		return false
	}
	return true
}

func mysqlAdapterUnsigned(c *mysqlDDLCase, typ, maximum, overflow, code, constraint string) {
	table := testutil.UniqueTable("kb_uint")
	c.cleanup = append(c.cleanup, table)
	if !c.operation("ORM 创建无符号列", func() error { return c.db.Table(table).AutoMigrate(mysqlAdapterValue(typ, "0")).Error }) {
		return
	}
	mysqlAdapterDeclaredType(c, table)
	for _, value := range []string{"0", maximum} {
		if !c.operation("ORM 写入合法边界", func() error { return c.db.Table(table).Create(mysqlAdapterValue(typ, value)).Error }) {
			return
		}
		c.count("合法边界精确保留", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE value = ?", 1, value)
	}
	for _, value := range []string{"-1", overflow} {
		mysqlAdapterReject(c, code, "", constraint, func() error { return c.db.Table(table).Create(mysqlAdapterValue(typ, value)).Error })
		c.count("拒绝后合法记录保持两条", "SELECT COUNT(*) FROM "+c.quote(table), 2)
	}
}

func mysqlAdapterCapacity(c *mysqlDDLCase, typ string) {
	table := testutil.UniqueTable("kb_capacity")
	c.cleanup = append(c.cleanup, table)
	binary := strings.HasSuffix(typ, "blob")
	makeValue := func(text string) interface{} {
		if binary {
			return mysqlAdapterValue(typ, []byte(text))
		}
		return mysqlAdapterValue(typ, text)
	}
	if !c.operation("ORM 创建容量类型", func() error { return c.db.Table(table).AutoMigrate(makeValue("")).Error }) {
		return
	}
	if got := mysqlAdapterDeclaredType(c, table); got != typ {
		c.t.Errorf("[未通过] 类型被替换：实际=%s；预期=%s", got, typ)
	}
	if !c.operation("ORM 写入 255 字节", func() error { return c.db.Table(table).Create(makeValue(strings.Repeat("x", 255))).Error }) {
		return
	}
	c.count("255 字节完整存储", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE octet_length(value) = 255", 1)
	if strings.HasPrefix(typ, "tiny") {
		mysqlAdapterReject(c, "23514", "", typ+"_check", func() error { return c.db.Table(table).Create(makeValue(strings.Repeat("x", 256))).Error })
		c.count("超限值没有写入", "SELECT COUNT(*) FROM "+c.quote(table), 1)
	} else {
		if !c.operation("ORM 写入 256 字节", func() error { return c.db.Table(table).Create(makeValue(strings.Repeat("x", 256))).Error }) {
			return
		}
		c.count("256 字节完整存储", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE octet_length(value) = 256", 1)
		if strings.HasPrefix(typ, "medium") {
			var definition string
			query := "SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_attribute a ON a.atttypid = c.contypid JOIN pg_class t ON t.oid = a.attrelid JOIN pg_namespace n ON n.oid = t.relnamespace WHERE n.nspname = ? AND t.relname = ? AND a.attname = 'value'"
			if err := c.db.CommonDB().QueryRow(query, c.schema, table).Scan(&definition); err != nil {
				c.t.Fatalf("[未通过] 读取容量域约束: %v", err)
			}
			if !regexp.MustCompile(`<=\s*16777215\s*\)`).MatchString(definition) || !strings.Contains(definition, "length(") {
				c.t.Errorf("[未通过] MEDIUM 容量约束不匹配：%s", definition)
			}
			c.t.Logf("[容量定义] %s", definition)
			payload := func(size int) interface{} {
				if binary {
					return bytes.Repeat([]byte{'x'}, size)
				}
				return gorm.Expr("repeat(?, ?)", "x", size)
			}
			if !c.operation("ORM 写入 MEDIUM 的 ASCII 上限", func() error {
				return c.db.Table(table).UpdateColumn("value", payload(16777215)).Error
			}) {
				return
			}
			c.count("MEDIUM 上限完整存储", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE octet_length(value) = 16777215", 2)
			mysqlAdapterReject(c, "23514", "", typ+"_check", func() error {
				return c.db.Table(table).UpdateColumn("value", payload(16777216)).Error
			})
			c.count("MEDIUM 拒绝后原值保持", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE octet_length(value) = 16777215", 2)
		} else {
			var kind string
			var base string
			var length int
			query := "SELECT y.typtype, y.typlen, format_type(y.typbasetype, y.typtypmod) FROM pg_type y JOIN pg_attribute a ON a.atttypid = y.oid JOIN pg_class t ON t.oid = a.attrelid JOIN pg_namespace n ON n.oid = t.relnamespace WHERE n.nspname = ? AND t.relname = ? AND a.attname = 'value'"
			if err := c.db.CommonDB().QueryRow(query, c.schema, table).Scan(&kind, &length, &base); err != nil {
				c.t.Fatalf("[未通过] 读取 LONG 原生类型: %v", err)
			}
			if kind != "d" || length != -1 || base == "-" {
				c.t.Errorf("[未通过] LONG 原生存储定义不匹配：kind=%s,length=%d", kind, length)
			}
			c.t.Logf("[类型定义] LONG 为原生域类型，基础类型=%s；没有额外域 CHECK", base)
			c.t.Log("[范围未验证] LONG 原生变长类型及基本读写已验证；未分配 1GB 数据验证容量上界")
		}
	}
}

type mysqlAdapterAuto32 struct {
	ID   uint32 `gorm:"primary_key"`
	Name string
}

type mysqlAdapterManual64 struct {
	ID   uint64 `gorm:"primary_key;AUTO_INCREMENT:false"`
	Name string
}

func mysqlAdapterUint64Manual(c *mysqlDDLCase) {
	table := testutil.UniqueTable("kb_manual64")
	c.cleanup = append(c.cleanup, table)
	if !c.operation("ORM 建立 UINT64 手动主键表", func() error { return c.db.Table(table).AutoMigrate(&mysqlAdapterManual64{}).Error }) {
		return
	}
	row := mysqlAdapterManual64{ID: 18446744073709551615, Name: "maximum"}
	if !c.operation("ORM 写入 UINT64 最大主键", func() error { return c.db.Table(table).Create(&row).Error }) {
		return
	}
	var got mysqlAdapterManual64
	if err := c.db.Table(table).Where("id = ?", row.ID).First(&got).Error; err != nil || got.ID != row.ID || got.Name != row.Name {
		c.t.Fatalf("[未通过] UINT64 主键回读：ID=%d;error=%v", got.ID, err)
	}
	c.t.Logf("[状态通过] Go uint64 主键完整回读：%d", got.ID)
}

func mysqlAdapterUint32Auto(c *mysqlDDLCase) {
	table := testutil.UniqueTable("kb_auto32")
	c.cleanup = append(c.cleanup, table)
	if !c.operation("ORM 建立 UINT32 自增表", func() error { return c.db.Table(table).AutoMigrate(&mysqlAdapterAuto32{}).Error }) {
		return
	}
	row := mysqlAdapterAuto32{Name: "first"}
	if !c.operation("ORM 自增回填", func() error { return c.db.Table(table).Create(&row).Error }) {
		return
	}
	if row.ID != 1 {
		c.t.Errorf("[未通过] 自增 ID=%d；预期=1", row.ID)
	}
	max := mysqlAdapterAuto32{ID: 4294967295, Name: "maximum"}
	if !c.operation("ORM 写入 UINT32 最大值", func() error { return c.db.Table(table).Create(&max).Error }) {
		return
	}
	c.count("UINT32 最大值保留", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id = 4294967295", 1)
}

func mysqlAdapterModifyUnsigned(c *mysqlDDLCase) {
	table := c.table("value INTEGER")
	c.prepare("INSERT INTO " + c.quote(table) + " VALUES (7)")
	if !c.operation("ORM 修改为 TINYINT UNSIGNED", func() error { return c.db.Table(table).ModifyColumn("value", "TINYINT UNSIGNED").Error }) {
		return
	}
	c.count("原数据保留", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE value=7", 1)
	for _, value := range []string{"-1", "256"} {
		mysqlAdapterReject(c, "23514", "", "unsignedtinyint_check", func() error {
			return c.db.Table(table).Create(mysqlAdapterValue("tinyint unsigned", value)).Error
		})
		c.count("非法值未写入", "SELECT COUNT(*) FROM "+c.quote(table), 1)
	}
	if !c.operation("ORM 重复修改无符号列", func() error { return c.db.Table(table).ModifyColumn("value", "TINYINT UNSIGNED").Error }) {
		return
	}
	if !c.operation("ORM 恢复有符号整数列", func() error { return c.db.Table(table).ModifyColumn("value", "INTEGER").Error }) {
		return
	}
	if !c.operation("ORM 写入原范围以外的值", func() error {
		return c.db.Table(table).Create(mysqlAdapterValue("integer", "-1")).Error
	}) {
		return
	}
	c.count("原范围约束已移除", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE value=-1", 1)
}

func mysqlAdapterModifyUnsignedAtomicFailure(c *mysqlDDLCase) {
	table := c.table("value INTEGER")
	c.prepare("INSERT INTO " + c.quote(table) + " VALUES (256)")
	mysqlAdapterReject(c, "23514", "", "unsignedtinyint_check", func() error {
		return c.db.Table(table).ModifyColumn("value", "TINYINT UNSIGNED").Error
	})
	c.targetSent = true
	if got := mysqlAdapterDeclaredType(c, table); got != "integer" {
		c.t.Errorf("[未通过] 失败后字段类型改变：%s", got)
	}
	c.count("失败后原数据保留", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE value=256", 1)
}
