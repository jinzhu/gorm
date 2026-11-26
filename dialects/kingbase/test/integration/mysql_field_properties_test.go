//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

func TestKingbaseMySQLMode_FieldProperties(t *testing.T) {
	runMySQLDDLCases(t, "ORM 字段属性专项：原始标签、实际目录和数据回读", []mysqlDDLTestCase{
		{"01_DefaultLiterals", "默认值字面量原样保留", mysqlPropertyDefaults},
		{"02_ColumnComments", "列注释大小写、中文及引号", mysqlPropertyComments},
		{"03_EnumMembersAndDefault", "ENUM 成员大小写与默认值", mysqlPropertyEnum},
		{"04_AutoIncrementAttributes", "自增非主键列的非空、唯一和注释", mysqlPropertyAuto},
		{"05_ModifyLiteralsAndComment", "修改列时保留默认值与注释", mysqlPropertyModify},
		{"06_UnsignedDefaultsAndComment", "小整数无符号转换保留默认值和注释", mysqlPropertyUnsigned},
		{"07_EnumDistinctMembers", "ENUM 不同名称成员与默认值", mysqlPropertyEnumDistinct},
		{"08_AutoIncrementExplicitNullAndZero", "自增显式 NULL 与零值语义", mysqlPropertyAutoNull},
	})
}

func mysqlPropertyTable(c *mysqlDDLCase, model interface{}) string {
	c.t.Helper()
	table := testutil.UniqueTable("kb_props")
	c.cleanup = append(c.cleanup, table)
	if !c.operation("ORM 建表", func() error { return c.db.Table(table).AutoMigrate(model).Error }) {
		c.t.FailNow()
	}
	return table
}

func mysqlPropertyComment(c *mysqlDDLCase, table, column, want string) {
	c.t.Helper()
	query := "SELECT col_description(t.oid, a.attnum) FROM pg_attribute a JOIN pg_class t ON t.oid = a.attrelid JOIN pg_namespace n ON n.oid = t.relnamespace WHERE n.nspname = ? AND t.relname = ? AND a.attname = ? AND a.attnum > 0 AND NOT a.attisdropped"
	c.text("列注释 "+column, query, want, c.schema, table, column)
}

func mysqlPropertyDefault(c *mysqlDDLCase, table, column string) {
	c.t.Helper()
	var definition sql.NullString
	if err := c.db.CommonDB().QueryRow("SELECT column_default FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?", c.schema, table, column).Scan(&definition); err != nil {
		c.t.Fatalf("[未通过] 读取默认表达式: %v", err)
	}
	if !definition.Valid || definition.String == "" {
		c.t.Errorf("[未通过] %s 的默认表达式为空", column)
	} else {
		c.t.Logf("[默认定义] %s=%s；实际默认值由数据回读核验", column, definition.String)
	}
}

func mysqlPropertyDefaults(c *mysqlDDLCase) {
	type row struct {
		ID    int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Ready string `gorm:"type:varchar(32);default:'READY'"`
		Text  string `gorm:"type:varchar(32);default:'LONGTEXT'"`
		Auto  string `gorm:"type:varchar(32);default:'AUTO_INCREMENT'"`
	}
	table := mysqlPropertyTable(c, &row{})
	for _, column := range []string{"ready", "text", "auto"} {
		mysqlPropertyDefault(c, table, column)
	}
	first := row{ID: 1}
	if !c.operation("ORM 省略默认字段并回填", func() error { return c.db.Table(table).Create(&first).Error }) {
		return
	}
	if first.Ready != "READY" || first.Text != "LONGTEXT" || first.Auto != "AUTO_INCREMENT" {
		c.t.Errorf("[未通过] ORM 默认值回填：%+v", first)
	}
	// 显式省略字段可直接验证数据库默认值，不依赖 ORM 回读。
	if !c.statement("省略列触发数据库默认值", "INSERT INTO "+c.quote(table)+" (id) VALUES (2)") {
		return
	}
	for _, id := range []int{1, 2} {
		var got row
		if err := c.db.Table(table).Where("id = ?", id).First(&got).Error; err != nil {
			c.t.Fatalf("[未通过] 默认值回读: %v", err)
		}
		if got.Ready != "READY" || got.Text != "LONGTEXT" || got.Auto != "AUTO_INCREMENT" {
			c.t.Errorf("[未通过] ID=%d 默认值被改写：%+v", id, got)
		} else {
			c.t.Logf("[状态通过] ID=%d：READY、LONGTEXT、AUTO_INCREMENT 原样保留", id)
		}
	}
}

func mysqlPropertyComments(c *mysqlDDLCase) {
	type row struct {
		ID      int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Label   string `gorm:"type:varchar(32);comment:'StatusLabel'"`
		Chinese string `gorm:"type:varchar(32);comment:'状态Label'"`
		Quote   string `gorm:"type:varchar(32);comment:'Owner''sLabel'"`
	}
	table := mysqlPropertyTable(c, &row{})
	mysqlPropertyComment(c, table, "label", "StatusLabel")
	mysqlPropertyComment(c, table, "chinese", "状态Label")
	mysqlPropertyComment(c, table, "quote", "Owner'sLabel")
}

func mysqlPropertyEnum(c *mysqlDDLCase) {
	type row struct {
		ID     int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Choice string `gorm:"type:enum('Alpha','alpha','READY');not null;default:'READY'"`
	}
	mysqlPropertyEnumCase(c, &row{}, []string{"Alpha", "alpha", "READY"})
}

func mysqlPropertyEnumDistinct(c *mysqlDDLCase) {
	type row struct {
		ID     int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Choice string `gorm:"type:enum('Alpha','Beta','READY');not null;default:'READY'"`
	}
	mysqlPropertyEnumCase(c, &row{}, []string{"Alpha", "Beta", "READY"})
}

func mysqlPropertyEnumCase(c *mysqlDDLCase, model interface{}, members []string) {
	// 建表使用传入的 enum 标签；这些记录只承载 INSERT 和读取数据。
	type row struct {
		ID     int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Choice string `gorm:"default:'READY'"`
	}
	table := mysqlPropertyTable(c, model)
	var namespace, name, kind string
	query := "SELECT en.nspname, e.typname, e.typtype FROM pg_attribute a JOIN pg_class t ON t.oid = a.attrelid JOIN pg_namespace n ON n.oid = t.relnamespace JOIN pg_type e ON e.oid = a.atttypid JOIN pg_namespace en ON en.oid = e.typnamespace WHERE n.nspname = ? AND t.relname = ? AND a.attname = 'choice'"
	if err := c.db.CommonDB().QueryRow(query, c.schema, table).Scan(&namespace, &name, &kind); err != nil {
		c.t.Fatalf("[未通过] 读取 ENUM 定义: %v", err)
	}
	defer func() {
		if err := c.db.Exec("DROP TABLE IF EXISTS " + c.quote(table)).Error; err != nil {
			c.t.Errorf("[未通过] 清理 ENUM 表: %v", err)
			return
		}
		var remaining int
		if err := c.db.CommonDB().QueryRow("SELECT COUNT(*) FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=? AND t.typname=?", namespace, name).Scan(&remaining); err != nil {
			c.t.Errorf("[未通过] 核验 ENUM 清理: %v", err)
			return
		}
		if remaining == 0 {
			c.t.Log("[状态通过] ENUM 类型已随测试表清理，无残留")
			return
		}
		if namespace != c.schema || !strings.HasPrefix(name, table) {
			c.t.Errorf("[未通过] 剩余 ENUM 类型未能确认归属：%s.%s", namespace, name)
			return
		}
		if err := c.db.Exec("DROP TYPE " + c.quote(namespace) + "." + c.quote(name)).Error; err != nil {
			c.t.Errorf("[未通过] 清理本用例 ENUM 类型: %v", err)
		}
	}()
	// KingbaseES 使用 'l' 表示内联 MySQL enum，使用 'e' 表示标准 enum；
	// 下方对成员、顺序和往返读写的精确断言仍是必需项。
	if kind != "e" && kind != "l" {
		c.t.Errorf("[未通过] 实际类型不是 ENUM：kind=%s；type=%s.%s", kind, namespace, name)
	}
	c.t.Logf("[枚举定义] type=%s.%s；kind=%s", namespace, name, kind)
	rows, err := c.db.CommonDB().Query("SELECT e.enumlabel FROM pg_enum e JOIN pg_type t ON t.oid=e.enumtypid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=? AND t.typname=? ORDER BY e.enumsortorder", namespace, name)
	if err != nil {
		c.t.Fatalf("[未通过] 读取 ENUM 成员: %v", err)
	}
	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			rows.Close()
			c.t.Fatalf("[未通过] 读取 ENUM 成员: %v", err)
		}
		labels = append(labels, label)
	}
	readError := rows.Err()
	closeError := rows.Close()
	if readError != nil || closeError != nil {
		c.t.Fatalf("[未通过] ENUM 目录读取：%v；关闭：%v", readError, closeError)
	}
	if !reflect.DeepEqual(labels, members) {
		c.t.Errorf("[未通过] ENUM 成员或顺序变化：%q", labels)
	} else {
		c.t.Logf("[状态通过] ENUM 成员及顺序=%q", labels)
	}
	for i, value := range append(append([]string{}, members...), "") {
		input := row{ID: i + 1, Choice: value}
		if !c.operation("ORM 写入 ENUM", func() error { return c.db.Table(table).Create(&input).Error }) {
			return
		}
		want := value
		if want == "" {
			want = "READY"
		}
		var got row
		if err := c.db.Table(table).Where("id = ?", i+1).First(&got).Error; err != nil {
			c.t.Fatalf("[未通过] ENUM 回读: %v", err)
		}
		if got.Choice != want {
			c.t.Errorf("[未通过] ENUM 回读：实际=%q；预期=%q", got.Choice, want)
		} else {
			c.t.Logf("[状态通过] ENUM ID=%d，值=%q", i+1, got.Choice)
		}
	}
}

func mysqlPropertyAuto(c *mysqlDDLCase) {
	type row struct {
		ID     int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Ticket uint32 `gorm:"type:int unsigned AUTO_INCREMENT;AUTO_INCREMENT;index;not null;unique;comment:'TicketID'"`
	}
	table := mysqlPropertyTable(c, &row{})
	mysqlPropertyComment(c, table, "ticket", "TicketID")
	c.count("自增列非空", "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=? AND table_name=? AND column_name='ticket' AND is_nullable='NO'", 1, c.schema, table)
	var constraint string
	query := "SELECT tc.constraint_name FROM information_schema.table_constraints tc JOIN information_schema.key_column_usage k ON k.constraint_schema=tc.constraint_schema AND k.constraint_name=tc.constraint_name AND k.table_name=tc.table_name WHERE tc.table_schema=? AND tc.table_name=? AND tc.constraint_type='UNIQUE' AND k.column_name='ticket'"
	if err := c.db.CommonDB().QueryRow(query, c.schema, table).Scan(&constraint); err != nil {
		c.t.Fatalf("[未通过] 独立 UNIQUE 约束未保留: %v", err)
	}
	for _, id := range []int{1, 2} {
		input := row{ID: id}
		if !c.operation("ORM 自增并回填非主键列", func() error { return c.db.Table(table).Create(&input).Error }) {
			return
		}
		if input.Ticket != uint32(id) {
			c.t.Errorf("[未通过] 非主键自增回填：实际=%d；预期=%d", input.Ticket, id)
		}
		c.count("数据库自增值正确", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=? AND ticket=?", 1, id, id)
	}
	mysqlAdapterReject(c, "23505", table, constraint, func() error { return c.db.Table(table).Create(&row{ID: 3, Ticket: 1}).Error })
	c.count("拒绝重复自增值后仍两条记录", "SELECT COUNT(*) FROM "+c.quote(table), 2)

	c.count("未存储 NULL 自增值", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE ticket IS NULL", 0)
}

func mysqlPropertyAutoNull(c *mysqlDDLCase) {
	type row struct {
		ID     int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Ticket uint32 `gorm:"type:int unsigned AUTO_INCREMENT;AUTO_INCREMENT;index;not null;unique;comment:'TicketID'"`
	}
	table := mysqlPropertyTable(c, &row{})
	for _, id := range []int{1, 2} {
		if !c.operation("ORM 省略列正常自增", func() error { return c.db.Table(table).Create(&row{ID: id}).Error }) {
			return
		}
	}
	// 真正的 AUTO_INCREMENT 列在显式传入 NULL 时也必须生成值。
	type nullRow struct {
		ID     int `gorm:"primary_key;AUTO_INCREMENT:false"`
		Ticket *uint32
	}
	if !c.operation("ORM 显式 NULL 触发自增", func() error { return c.db.Table(table).Create(&nullRow{ID: 4}).Error }) {
		return
	}
	var ticket sql.NullInt64
	if err := c.db.CommonDB().QueryRow("SELECT ticket FROM " + c.quote(table) + " WHERE id=4").Scan(&ticket); err != nil {
		c.t.Fatalf("[未通过] 读取 NULL 探测结果: %v", err)
	}
	if !ticket.Valid || ticket.Int64 <= 2 {
		c.t.Errorf("[未通过] 显式 NULL 应生成后续自增值，实际=%v", ticket)
	} else {
		c.t.Logf("[状态通过] 显式 NULL 生成后续自增值=%d", ticket.Int64)
	}
	c.prepare("DELETE FROM " + c.quote(table) + " WHERE id=4")
	var flags string
	if err := c.db.CommonDB().QueryRow("SHOW sql_mode").Scan(&flags); err != nil {
		c.t.Fatalf("[未通过] 读取 sql_mode: %v", err)
	}
	zero := uint32(0)
	if !c.operation("ORM 显式零值的自增语义", func() error { return c.db.Table(table).Create(&nullRow{ID: 5, Ticket: &zero}).Error }) {
		return
	}
	var zeroStored uint32
	if err := c.db.CommonDB().QueryRow("SELECT ticket FROM " + c.quote(table) + " WHERE id=5").Scan(&zeroStored); err != nil {
		c.t.Fatalf("[未通过] 读取零值探测结果: %v", err)
	}
	if strings.Contains(strings.ToUpper(flags), "NO_AUTO_VALUE_ON_ZERO") {
		if zeroStored != 0 {
			c.t.Errorf("[未通过] NO_AUTO_VALUE_ON_ZERO 下零值未保留：%d", zeroStored)
		}
	} else if int64(zeroStored) <= ticket.Int64 {
		c.t.Errorf("[未通过] 显式零值未生成后续自增值：%d", zeroStored)
	}
	c.t.Logf("[自增行为] sql_mode=%s；显式零值落库=%d", flags, zeroStored)

}

func mysqlPropertyModify(c *mysqlDDLCase) {
	table := c.table("id INTEGER PRIMARY KEY, value VARCHAR(32)")
	c.prepare("INSERT INTO " + c.quote(table) + " VALUES (1,'OldCase')")
	definition := "VARCHAR(32) NOT NULL DEFAULT 'AUTO_INCREMENT' COMMENT 'UpdatedLabel'"
	if !c.operation("ORM 修改字段属性", func() error { return c.db.Table(table).ModifyColumn("value", definition).Error }) {
		return
	}
	mysqlPropertyComment(c, table, "value", "UpdatedLabel")
	mysqlPropertyDefault(c, table, "value")
	c.count("修改后保持非空", "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=? AND table_name=? AND column_name='value' AND is_nullable='NO'", 1, c.schema, table)
	c.text("原数据未变化", "SELECT value FROM "+c.quote(table)+" WHERE id=1", "OldCase")
	if !c.statement("修改后触发默认值", "INSERT INTO "+c.quote(table)+" (id) VALUES (2)") {
		return
	}
	c.text("默认值未误识别为自增", "SELECT value FROM "+c.quote(table)+" WHERE id=2", "AUTO_INCREMENT")
}

func mysqlPropertyUnsigned(c *mysqlDDLCase) {
	type row struct {
		ID    int    `gorm:"primary_key;AUTO_INCREMENT:false"`
		Value uint16 `gorm:"type:tinyint unsigned;not null;default:7;comment:'TinyLabel'"`
	}
	table := mysqlPropertyTable(c, &row{})
	mysqlPropertyComment(c, table, "value", "TinyLabel")
	mysqlPropertyDefault(c, table, "value")
	c.count("无符号替代列非空", "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=? AND table_name=? AND column_name='value' AND is_nullable='NO'", 1, c.schema, table)
	input := row{ID: 1}
	if !c.operation("ORM 省略无符号列使用默认值", func() error { return c.db.Table(table).Create(&input).Error }) {
		return
	}
	if input.Value != 7 {
		c.t.Errorf("[未通过] 无符号列默认回填=%d；预期=7", input.Value)
	}
	c.count("默认值落库为 7", "SELECT COUNT(*) FROM "+c.quote(table)+" WHERE id=1 AND value=7", 1)
	mysqlAdapterReject(c, "23514", "", "unsignedtinyint_check", func() error { return c.db.Table(table).Create(&row{ID: 2, Value: 256}).Error })
	c.count("越界值未写入", "SELECT COUNT(*) FROM "+c.quote(table), 1)
}
