//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/jinzhu/gorm"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
	"kingbase.com/gokb"
)

const mysqlDDLReference = "V9R3C11 MySQL / 4.1 DDL"

type mysqlDDLCase struct {
	t          *testing.T
	db         *gorm.DB
	schema     string
	cleanup    []string
	targetSent bool
}

func openMySQLDDLCase(t *testing.T) *mysqlDDLCase {
	t.Helper()
	c := &mysqlDDLCase{t: t, db: openKBMySQL(t)}
	return c
}

func (c *mysqlDDLCase) identify() {
	c.t.Helper()
	var version string
	if err := c.db.CommonDB().QueryRow("SELECT version(), current_schema()").Scan(&version, &c.schema); err != nil {
		c.t.Fatalf("[未通过] 读取实例版本和 schema: %v", err)
	}
	c.t.Logf("[测试基准] 文档=%s；模式=mysql；schema=%s；实例=%s", mysqlDDLReference, c.schema, version)
}

func (c *mysqlDDLCase) finish() {
	c.t.Helper()
	for i := len(c.cleanup) - 1; i >= 0; i-- {
		if err := c.db.Exec("DROP TABLE IF EXISTS " + c.quote(c.cleanup[i])).Error; err != nil {
			c.t.Errorf("[未通过] 清理测试表 %s: %v", c.cleanup[i], err)
		}
	}
	finishLoggedTest(c.t, c.db)
}

func (c *mysqlDDLCase) quote(name string) string { return c.db.Dialect().Quote(name) }

func (c *mysqlDDLCase) table(columns string) string {
	c.t.Helper()
	name := testutil.UniqueTable("kb_ddl")
	c.cleanup = append(c.cleanup, name)
	c.prepare("CREATE TABLE " + c.quote(name) + " (" + columns + ")")
	return name
}

func (c *mysqlDDLCase) prepare(query string, args ...interface{}) {
	c.t.Helper()
	if err := c.db.Exec(query, args...).Error; err != nil {
		c.t.Fatalf("[准备失败] 测试前置 SQL 执行失败，详见金仓日志: %v", err)
	}
}

// 只输出目标操作的 SQL；准备、元数据和成功查询保持静默，驱动失败的 SQL 已由 DriverLog 记录。
func (c *mysqlDDLCase) operation(name string, run func() error) bool {
	c.t.Helper()
	return c.observeOperation(name, "Exec", run)
}

func (c *mysqlDDLCase) observeOperation(name, operation string, run func() error) bool {
	c.t.Helper()
	log := testutil.DriverLogFor(c.db)
	start := log.Snapshot()
	err := run()
	var executions, failures int
	for _, result := range log.SQLResults(start) {
		if result.Err != nil {
			failures++
		}
		// 成功的序列维护属于 Create 内部步骤。保持目标 DDL/INSERT 输出清晰，
		// 且不把维护语句计入目标操作。
		maintenance := strings.HasPrefix(result.SQL, "SELECT pg_advisory_xact_lock(") || strings.HasPrefix(result.SQL, "SELECT setval(")
		if result.Operation == operation && !maintenance {
			executions++
			if result.Err == nil {
				c.t.Logf("[实际SQL] %s", strings.TrimSpace(result.SQL))
			}
		}
	}
	if executions == 0 {
		c.t.Errorf("[未通过] %s：没有观测到目标 %s，不能判为已验证", name, operation)
	}
	c.targetSent = c.targetSent || executions > 0
	if failures != 0 {
		c.t.Errorf("[未通过] %s：驱动返回错误；GORM.Error=%v，详见金仓日志", name, err)
	} else if err != nil {
		c.t.Errorf("[未通过] %s：GORM 返回错误: %v", name, err)
	}
	return executions > 0 && failures == 0 && err == nil
}

func (c *mysqlDDLCase) statement(name, query string, args ...interface{}) bool {
	c.t.Helper()
	return c.operation(name, func() error { return c.db.Exec(query, args...).Error })
}

func (c *mysqlDDLCase) count(label, query string, want int, args ...interface{}) {
	c.t.Helper()
	var got int
	if err := c.db.CommonDB().QueryRow(query, args...).Scan(&got); err != nil {
		c.t.Fatalf("[未通过] %s：状态查询失败: %v", label, err)
	}
	if got != want {
		c.t.Errorf("[未通过] %s：实际=%d；预期=%d", label, got, want)
	} else {
		c.t.Logf("[状态通过] %s（%d）", label, got)
	}
}

func (c *mysqlDDLCase) text(label, query, want string, args ...interface{}) {
	c.t.Helper()
	var got sql.NullString
	if err := c.db.CommonDB().QueryRow(query, args...).Scan(&got); err != nil {
		c.t.Fatalf("[未通过] %s：回读失败: %v", label, err)
	}
	if !got.Valid || got.String != want {
		c.t.Errorf("[未通过] %s：实际=%v；预期=%q（严格区分大小写）", label, got, want)
	} else {
		c.t.Logf("[状态通过] %s", label)
	}
}

func (c *mysqlDDLCase) indexCount(label, table, index string, want int) {
	c.t.Helper()
	c.count(label, "SELECT COUNT(*) FROM pg_indexes WHERE schemaname = ? AND tablename = ? AND indexname = ?", want, c.schema, table, index)
}

func (c *mysqlDDLCase) columnCount(label, table, column string, want int) {
	c.t.Helper()
	c.count(label, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?", want, c.schema, table, column)
}

func (c *mysqlDDLCase) constraintCount(label, table, constraint string, want int) {
	c.t.Helper()
	c.count(label, "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = ? AND table_name = ? AND constraint_name = ?", want, c.schema, table, constraint)
}

func mysqlDDLMatchesError(err error, code, table, constraint, column string) bool {
	if errs, ok := err.(gorm.Errors); ok {
		if len(errs) == 0 {
			return false
		}
		for _, item := range errs {
			if !mysqlDDLMatchesError(item, code, table, constraint, column) {
				return false
			}
		}
		return true
	}
	e, ok := err.(*gokb.Error)
	return ok && e != nil && string(e.Code) == code && e.Table == table &&
		(constraint == "" || e.Constraint == constraint) && (column == "" || e.Column == column)
}

// 反向探针精确匹配错误码和目标对象；执行成功时交给调用方做明确的语义断言。
func (c *mysqlDDLCase) reject(name, code, table, constraint, column, query string, args ...interface{}) bool {
	c.t.Helper()
	log := testutil.DriverLogFor(c.db)
	start := log.Snapshot()
	match := func(err error) bool { return mysqlDDLMatchesError(err, code, table, constraint, column) }
	err := log.WithExpectedError(match, func() error { return c.db.Exec(query, args...).Error })
	if err == nil {
		c.t.Logf("[实际SQL] %s", query)
		return false
	}
	if !match(err) {
		c.t.Fatalf("[未通过] %s：返回了非预期错误，要求 SQLSTATE=%s 且目标对象匹配；实际=%v", name, code, err)
	}
	if n := log.ExpectErrors(start, match); n != 1 {
		c.t.Fatalf("[未通过] %s：预期一次驱动错误，实际=%d", name, n)
	}
	c.t.Logf("[通过] %s：按预期拒绝（%s）", name, code)
	return true
}

type mysqlDDLTestCase struct {
	name, title string
	run         func(*mysqlDDLCase)
}

// 适配验收始终走真实 ORM 调用；原始 MySQL 探针和 KingbaseES 官方参考查询使用独立测试入口。
func TestKingbaseMySQLMode_DDLStep1(t *testing.T) {
	runMySQLDDLCases(t, "ORM 适配验收：通过真实 ORM 调用生成 SQL", []mysqlDDLTestCase{
		{"02_ORMRemoveIndex", "ORM 删除索引", mysqlDDLORMRemoveIndex},
		{"03_ORMDropColumn", "ORM 删除列并核验保留数据", mysqlDDLDropColumn},
		{"04_DefaultSetAndDrop", "ORM 修改默认值", mysqlDDLDefault},
		{"05_NotNullSetAndDrop", "ORM 修改非空属性", mysqlDDLNotNull},
		{"09_UniqueConstraint", "ORM 唯一索引创建、拒绝与删除", mysqlDDLUniqueConstraint},
		{"10_ForeignKeyConstraint", "ORM 外键增加、拒绝与删除", mysqlDDLForeignKeyConstraint},
		{"13_FieldDefinitionRoundTrip", "ORM 字段定义与数据回读", mysqlDDLFieldDefinition},
	})
}

func runMySQLDDLCases(t *testing.T, entry string, cases []mysqlDDLTestCase) {
	t.Helper()
	ran := 0
	for _, item := range cases {
		item := item
		t.Run(item.name, func(t *testing.T) {
			ran++
			mysqlTestTitle(t, item.title)
			t.Logf("[测试入口] %s", entry)
			var c *mysqlDDLCase
			defer func() {
				if c == nil || !c.targetSent {
					t.Log("[未验证] 目标 DDL 尚未发送，请先排查连接、准备阶段或未执行目标 SQL 的原因")
				}
			}()
			c = openMySQLDDLCase(t)
			defer c.finish()
			c.identify()
			item.run(c)
		})
	}
	if ran == 0 {
		t.Skip("[未验证] 没有匹配的子测试；部分用例已迁移，请使用当前运行说明中的单项命令")
	}
}

func mysqlDDLORMRemoveIndex(c *mysqlDDLCase) {
	table := c.table("id INTEGER PRIMARY KEY, name VARCHAR(32)")
	index := "idx_" + table
	c.prepare("CREATE INDEX " + c.quote(index) + " ON " + c.quote(table) + " (name)")
	c.indexCount("删除前索引存在", table, index, 1)
	c.operation("ORM 删除索引", func() error { return c.db.Table(table).RemoveIndex(index).Error })
	c.indexCount("删除后索引不存在", table, index, 0)
}

func mysqlDDLDropColumn(c *mysqlDDLCase) {
	table := c.table("id INTEGER PRIMARY KEY, keep_value VARCHAR(32), obsolete INTEGER")
	c.prepare("INSERT INTO " + c.quote(table) + " (id, keep_value, obsolete) VALUES (1, 'KeepCase', 7)")
	c.columnCount("删除前列存在", table, "obsolete", 1)
	c.operation("ORM 删除列", func() error { return c.db.Table(table).DropColumn("obsolete").Error })
	c.columnCount("目标列已删除", table, "obsolete", 0)
	c.columnCount("保留列仍存在", table, "keep_value", 1)
	c.text("保留数据未变化", "SELECT keep_value FROM "+c.quote(table)+" WHERE id = 1", "KeepCase")
}

func mysqlDDLDefault(c *mysqlDDLCase) {
	table := c.table("id INTEGER PRIMARY KEY, status VARCHAR(16)")
	c.prepare("INSERT INTO " + c.quote(table) + " (id, status) VALUES (1, 'OldCase')")
	if !c.operation("ORM 设置默认值", func() error { return c.db.Table(table).ModifyColumn("status", "VARCHAR(16) DEFAULT 'READY'").Error }) {
		return
	}
	var def sql.NullString
	if err := c.db.CommonDB().QueryRow("SELECT column_default FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = 'status'", c.schema, table).Scan(&def); err != nil {
		c.t.Fatalf("[未通过] 读取默认值定义: %v", err)
	}
	if !def.Valid {
		c.t.Error("[未通过] 默认值定义仍为空")
	}
	c.prepare("INSERT INTO " + c.quote(table) + " (id) VALUES (2)")
	c.text("新记录采用 READY 默认值", "SELECT status FROM "+c.quote(table)+" WHERE id = 2", "READY")
	c.text("设置默认值不修改旧记录", "SELECT status FROM "+c.quote(table)+" WHERE id = 1", "OldCase")
	if !c.operation("ORM 删除默认值", func() error { return c.db.Table(table).ModifyColumn("status", "VARCHAR(16)").Error }) {
		return
	}
	c.count("默认值定义已删除", "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = 'status' AND column_default IS NULL", 1, c.schema, table)
	c.prepare("INSERT INTO " + c.quote(table) + " (id) VALUES (3)")
	c.count("删除默认值后新记录为 NULL", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id = 3 AND status IS NULL", 1)
	c.text("删除默认值不修改已有 READY 记录", "SELECT status FROM "+c.quote(table)+" WHERE id = 2", "READY")
}

func mysqlDDLNotNull(c *mysqlDDLCase) {
	table := c.table("id INTEGER PRIMARY KEY, qty INTEGER")
	c.prepare("INSERT INTO " + c.quote(table) + " (id, qty) VALUES (1, 7)")
	if !c.operation("ORM 设置非空属性", func() error { return c.db.Table(table).ModifyColumn("qty", "INTEGER NOT NULL").Error }) {
		return
	}
	c.count("字段定义为非空", "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = 'qty' AND is_nullable = 'NO'", 1, c.schema, table)
	// 文档描述的 MySQL 模式可能把显式 NULL 转换为非空默认值；记录该分支，
	// 不假设原生 MySQL 严格模式行为。
	rejected := c.reject("非空列 NULL 探测", "23502", table, "", "qty", "INSERT INTO "+c.quote(table)+" (id, qty) VALUES (2, NULL)")
	if rejected {
		c.count("拒绝后没有写入记录", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id = 2", 0)
	} else {
		var qty sql.NullInt64
		if err := c.db.CommonDB().QueryRow("SELECT qty FROM " + c.quote(table) + " WHERE id = 2").Scan(&qty); err != nil {
			c.t.Fatalf("[未通过] 读取 NULL 探测结果: %v", err)
		}
		if !qty.Valid || qty.Int64 != 0 {
			c.t.Errorf("[未通过] 非空列 NULL 转换：实际=%v；预期为非空整数 0", qty)
		} else {
			c.t.Log("[状态通过] 实例将显式 NULL 转为 0，非空约束未存入 NULL")
		}
	}
	if !c.operation("ORM 删除非空属性", func() error { return c.db.Table(table).ModifyColumn("qty", "INTEGER NULL").Error }) {
		return
	}
	c.count("字段恢复可空", "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = 'qty' AND is_nullable = 'YES'", 1, c.schema, table)
	c.prepare("INSERT INTO " + c.quote(table) + " (id, qty) VALUES (3, NULL)")
	c.count("删除非空属性后可存入 NULL", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id = 3 AND qty IS NULL", 1)
	c.count("已有数值未变化", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id = 1 AND qty = 7", 1)
}

func mysqlDDLUniqueConstraint(c *mysqlDDLCase) {
	table := c.table("id INTEGER PRIMARY KEY, code VARCHAR(16)")
	constraint := "uq_" + table
	if !c.operation("ORM 创建唯一索引", func() error { return c.db.Table(table).AddUniqueIndex(constraint, "code").Error }) {
		return
	}
	c.indexCount("唯一索引存在", table, constraint, 1)
	c.prepare("INSERT INTO " + c.quote(table) + " VALUES (1, 'SameCase')")
	rejected := c.reject("UNIQUE 拒绝重复值", "23505", table, constraint, "", "INSERT INTO "+c.quote(table)+" VALUES (2, 'SameCase')")
	c.count("拒绝后原记录保持一条", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE code = 'SameCase'", 1)
	if !rejected {
		c.t.Error("[未通过] UNIQUE 探测没有返回要求的约束冲突错误")
		return
	}
	if !c.operation("ORM 删除唯一索引", func() error { return c.db.Table(table).RemoveIndex(constraint).Error }) {
		return
	}
	c.indexCount("唯一索引已删除", table, constraint, 0)
	c.prepare("INSERT INTO " + c.quote(table) + " VALUES (2, 'SameCase')")
	c.count("删除约束后重复值可写入", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE code = 'SameCase'", 2)
}

func mysqlDDLForeignKeyConstraint(c *mysqlDDLCase) {
	parent := c.table("id INTEGER PRIMARY KEY")
	child := c.table("id INTEGER PRIMARY KEY, parent_id INTEGER")
	destination := c.quote(parent) + "(id)"
	constraint := c.db.Dialect().BuildKeyName(child, "parent_id", destination, "foreign")
	if !c.operation("ORM 增加外键", func() error {
		return c.db.Table(child).AddForeignKey("parent_id", destination, "RESTRICT", "RESTRICT").Error
	}) {
		return
	}
	c.constraintCount("外键存在", child, constraint, 1)
	c.prepare("INSERT INTO " + c.quote(parent) + " VALUES (1)")
	c.prepare("INSERT INTO " + c.quote(child) + " VALUES (1, 1)")
	rejected := c.reject("外键拒绝不存在的父记录", "23503", child, constraint, "", "INSERT INTO "+c.quote(child)+" VALUES (2, 999)")
	c.count("拒绝后没有非法子记录", "SELECT COUNT(*) FROM "+c.quote(child)+" WHERE id = 2", 0)
	if !rejected {
		c.t.Error("[未通过] 外键探测没有返回要求的约束冲突错误")
		return
	}
	if !c.operation("ORM 删除外键", func() error { return c.db.Table(child).RemoveForeignKey("parent_id", destination).Error }) {
		return
	}
	c.constraintCount("外键已删除", child, constraint, 0)
	c.prepare("INSERT INTO " + c.quote(child) + " VALUES (2, 999)")
	c.count("删除外键后可写入原非法引用", "SELECT COUNT(*) FROM "+c.quote(child)+" WHERE id = 2 AND parent_id = 999", 1)
}

type mysqlDDLDefinitionRow struct {
	ID       uint    `gorm:"primary_key"`
	Status   string  `gorm:"type:varchar(16);not null;default:'READY'"`
	Amount   string  `gorm:"type:decimal(10,2);not null"`
	Optional *string `gorm:"type:varchar(32)"`
}

func mysqlDDLFieldDefinition(c *mysqlDDLCase) {
	table := testutil.UniqueTable("kb_ddl")
	c.cleanup = append(c.cleanup, table)
	if !c.operation("ORM 建表", func() error { return c.db.Table(table).AutoMigrate(&mysqlDDLDefinitionRow{}).Error }) {
		return
	}
	type columnDefinition struct {
		name, nullable string
		declaredType   string
		length         sql.NullInt64
		precision      sql.NullInt64
		scale          sql.NullInt64
		defaultValue   sql.NullString
	}
	for _, column := range []string{"status", "amount", "optional"} {
		var d columnDefinition
		d.name = column
		query := "SELECT c.is_nullable, c.character_maximum_length, c.numeric_precision, c.numeric_scale, c.column_default, format_type(a.atttypid, a.atttypmod) FROM information_schema.columns c JOIN pg_namespace n ON n.nspname = c.table_schema JOIN pg_class t ON t.relnamespace = n.oid AND t.relname = c.table_name JOIN pg_attribute a ON a.attrelid = t.oid AND a.attname = c.column_name WHERE c.table_schema = ? AND c.table_name = ? AND c.column_name = ?"
		if err := c.db.CommonDB().QueryRow(query, c.schema, table, column).Scan(&d.nullable, &d.length, &d.precision, &d.scale, &d.defaultValue, &d.declaredType); err != nil {
			c.t.Fatalf("[未通过] 读取字段 %s 定义: %v", column, err)
		}
		switch column {
		case "status":
			if d.nullable != "NO" || d.declaredType != "character varying(16)" || !d.defaultValue.Valid {
				c.t.Errorf("[未通过] status 字段定义: %+v", d)
			} else {
				c.t.Logf("[字段定义] status：长度=16，非空，默认表达式=%s；默认值大小写由后续数据回读核验", d.defaultValue.String)
			}
		case "amount":
			if d.nullable != "NO" || !d.precision.Valid || d.precision.Int64 != 10 || !d.scale.Valid || d.scale.Int64 != 2 {
				c.t.Errorf("[未通过] amount 字段定义: %+v", d)
			} else {
				c.t.Log("[状态通过] amount：精度=10，小数位=2，非空")
			}
		case "optional":
			if d.nullable != "YES" || d.declaredType != "character varying(32)" {
				c.t.Errorf("[未通过] optional 字段定义: %+v", d)
			} else {
				c.t.Log("[状态通过] optional：长度=32，可空")
			}
		}
		if column == "status" || column == "optional" {
			c.t.Logf("[目录核验] %s：information_schema 长度=%v；底层字段定义=%s", column, d.length, d.declaredType)
		}
	}
	row := mysqlDDLDefinitionRow{Amount: "123.45"}
	if err := c.db.Table(table).Create(&row).Error; err != nil {
		c.t.Fatalf("[未通过] ORM 插入字段核验数据: %v", err)
	}
	var got mysqlDDLDefinitionRow
	if err := c.db.Table(table).Where("id = ?", row.ID).First(&got).Error; err != nil {
		c.t.Fatalf("[未通过] ORM 回读字段核验数据: %v", err)
	}
	if got.ID == 0 || got.ID != row.ID || got.Status != "READY" || got.Amount != "123.45" || got.Optional != nil {
		c.t.Errorf("[未通过] 字段数据回读：ID=%d/回填=%d；status=%q（预期 READY）；amount=%q（预期 123.45）；optional=%v（预期 NULL）", got.ID, row.ID, got.Status, got.Amount, got.Optional)
	} else {
		c.t.Log("[状态通过] 自增回填、READY 大小写、decimal 数值与 NULL 回读一致")
	}
}
