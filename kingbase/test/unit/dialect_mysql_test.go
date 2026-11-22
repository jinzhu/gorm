package kingbase_test

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/kingbase/dialects"
)

type mysqlBoundaryTypeModel struct {
	BigUnsigned string `gorm:"type:bigint unsigned"`
	IntUnsigned string `gorm:"type:int unsigned"`
	Integer     string `gorm:"type:integer unsigned"`
	MediumInt   string `gorm:"type:mediumint unsigned"`
	SmallInt    string `gorm:"type:smallint unsigned"`
	TinyInt     string `gorm:"type:tinyint unsigned"`
	LongText    string `gorm:"type:longtext"`
	MediumText  string `gorm:"type:mediumtext"`
	TinyText    string `gorm:"type:tinytext"`
	LongBlob    []byte `gorm:"type:longblob"`
	MediumBlob  []byte `gorm:"type:mediumblob"`
	TinyBlob    []byte `gorm:"type:tinyblob"`
	AutoBig     int64  `gorm:"primary_key;type:bigint auto_increment"`
}

type mysqlTypeModel struct {
	ID      uint   `gorm:"primary_key"`
	Name    string `gorm:"size:64;not null"`
	Note    string
	Payload []byte
	Amount  uint64
}

func TestKingbaseMySQLTypeRewriteBoundaryValues(t *testing.T) {
	db, _, cleanup := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer cleanup()

	tests := []struct {
		field string
		want  string
	}{
		{field: "BigUnsigned", want: "bigint unsigned"},
		{field: "IntUnsigned", want: "int unsigned"},
		{field: "Integer", want: "integer unsigned"},
		{field: "MediumInt", want: "mediumint unsigned"},
		{field: "SmallInt", want: "smallint unsigned"},
		{field: "TinyInt", want: "tinyint unsigned"},
		{field: "LongText", want: "longtext"},
		{field: "MediumText", want: "mediumtext"},
		{field: "TinyText", want: "tinytext"},
		{field: "LongBlob", want: "longblob"},
		{field: "MediumBlob", want: "mediumblob"},
		{field: "TinyBlob", want: "tinyblob"},
		{field: "AutoBig", want: "bigint AUTO_INCREMENT"},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			if got := fieldType(t, db, &mysqlBoundaryTypeModel{}, tt.field); got != strings.ToLower(tt.want) {
				t.Fatalf("type mismatch: got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKingbaseMySQLUnsignedDefinitionUsesR3NativeTypes(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"tinyint unsigned", "tinyint unsigned"},
		{"SMALLINT(5) UNSIGNED NOT NULL DEFAULT 7", "SMALLINT(5) UNSIGNED NOT NULL DEFAULT 7"},
		{"mediumint unsigned", "mediumint unsigned"},
		{"INT(11) UNSIGNED DEFAULT 7", "INT(11) UNSIGNED DEFAULT 7"},
		{"bigint unsigned", "bigint unsigned"},
		{"int unsigned AUTO_INCREMENT", "int unsigned AUTO_INCREMENT"},
	} {
		if got := definitionType(kb.NewKingbaseDialect(kb.KbModeMySQL), test.input, "v`x"); got != test.want {
			t.Errorf("definition(%q)=%q, want %q", test.input, got, test.want)
		}
	}
}

func TestKingbaseMySQLModifyColumnPreservesCallerDefinition(t *testing.T) {
	for _, definition := range []string{
		"VARCHAR(16) DEFAULT 'READY'",
		"VARCHAR(16)",
		"INTEGER NOT NULL",
		"INTEGER NULL",
	} {
		t.Run(definition, func(t *testing.T) {
			db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
			defer close()
			db.LogMode(false)
			query := "ALTER TABLE `users` MODIFY COLUMN `status` " + definition
			mock.ExpectExec("^" + regexp.QuoteMeta(query) + "$").WillReturnResult(sqlmock.NewResult(0, 0))
			if err := db.Table("users").ModifyColumn("status", definition).Error; err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKingbaseMySQLTypeDefinitionPreservesLiteralsAndAttributes(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"VARCHAR(16) NOT NULL DEFAULT 'READY' COMMENT 'StatusLabel'", "VARCHAR(16) NOT NULL DEFAULT 'READY' COMMENT 'StatusLabel'"},
		{"ENUM('A','B')", "ENUM('A','B')"},
		{"varchar(32) DEFAULT 'AUTO_INCREMENT'", "varchar(32) DEFAULT 'AUTO_INCREMENT'"},
		{"int DEFAULT 'AUTO_INCREMENT'", "int DEFAULT 'AUTO_INCREMENT'"},
		{"int COMMENT 'AUTO_INCREMENT with ''quotes'''", "int COMMENT 'AUTO_INCREMENT with ''quotes'''"},
		{"int COMMENT \"AUTO_INCREMENT\"", "int COMMENT \"AUTO_INCREMENT\""},
		{"int /* AUTO_INCREMENT */ DEFAULT 1", "int /* AUTO_INCREMENT */ DEFAULT 1"},
		{"int DEFAULT $$AUTO_INCREMENT$$", "int DEFAULT $$AUTO_INCREMENT$$"},
		{"int DEFAULT $tag$AUTO_INCREMENT$tag$", "int DEFAULT $tag$AUTO_INCREMENT$tag$"},
		{"int -- AUTO_INCREMENT", "int -- AUTO_INCREMENT"},
		{"int # AUTO_INCREMENT", "int # AUTO_INCREMENT"},
		{"tinytext DEFAULT 'LONGTEXT'", "tinytext DEFAULT 'LONGTEXT'"},
		{"bigint unsigned DEFAULT 1 COMMENT 'BIGINT UNSIGNED'", "bigint unsigned DEFAULT 1 COMMENT 'BIGINT UNSIGNED'"},
		{"int unsigned AUTO_INCREMENT NOT NULL COMMENT 'OrderID'", "int unsigned AUTO_INCREMENT NOT NULL COMMENT 'OrderID'"},
		{"bigint AUTO_INCREMENT NOT NULL UNIQUE", "bigint AUTO_INCREMENT NOT NULL UNIQUE"},
		{"tinyint AUTO_INCREMENT NOT NULL COMMENT 'TinyID'", "tinyint AUTO_INCREMENT NOT NULL COMMENT 'TinyID'"},
		{"int(11) AUTO_INCREMENT", "int(11) AUTO_INCREMENT"},
		{"int(11) unsigned AUTO_INCREMENT COMMENT 'LONGTEXT'", "int(11) unsigned AUTO_INCREMENT COMMENT 'LONGTEXT'"},
	} {
		t.Run(test.input, func(t *testing.T) {
			want := test.want
			if got := definitionType(kb.NewKingbaseDialect(kb.KbModeMySQL), test.input, "value"); got != want {
				t.Fatalf("definition=%q, want %q", got, want)
			}
		})
	}
}

func TestKingbaseMySQLGormFieldRetainsDefaultCase(t *testing.T) {
	type row struct {
		Status string `gorm:"type:varchar(16);not null;default:'READY';comment:'StatusLabel'"`
		Phrase string `gorm:"type:varchar(32);default:'AUTO_INCREMENT'"`
	}
	db, _, close := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close()
	for _, field := range db.NewScope(&row{}).GetModelStruct().StructFields {
		got := db.Dialect().DataTypeOf(field)
		if field.Name == "Status" && (!strings.Contains(got, "DEFAULT 'READY'") || !strings.Contains(got, "COMMENT 'StatusLabel'")) {
			t.Fatalf("GORM field definition changed literal case: %s", got)
		}
		if field.Name == "Phrase" && (!strings.HasPrefix(got, "varchar(32)") || !strings.Contains(got, "DEFAULT 'AUTO_INCREMENT'")) {
			t.Fatalf("GORM default mistaken for auto-increment attribute: %s", got)
		}
	}
}

func TestKingbaseMySQLTypeMapping(t *testing.T) {
	db, _, cleanup := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer cleanup()

	assertType := func(field, want string) {
		t.Helper()
		if got := fieldType(t, db, &mysqlTypeModel{}, field); got != strings.ToLower(want) {
			t.Fatalf("%s type mismatch: got %q, want %q", field, got, want)
		}
	}

	assertType("ID", "int unsigned AUTO_INCREMENT")
	assertType("Name", "varchar(64) NOT NULL")
	assertType("Note", "varchar(255)")
	assertType("Payload", "varbinary(255)")
	assertType("Amount", "bigint unsigned")
}

func TestKingbaseMySQLMetadataCleansSchemaQualifiedIdentifiers(t *testing.T) {
	db, mock, cleanup := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer cleanup()

	query := "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?"
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("APP", "Order", "select").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	if !db.Dialect().HasColumn("`APP`.`Order`", "`select`") {
		t.Fatal("expected schema-qualified column metadata lookup to succeed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestKingbaseMySQLIndexMetadataRespectsQuotedPaths(t *testing.T) {
	db, mock, cleanup := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer cleanup()
	query := "SELECT COUNT(*) FROM pg_indexes WHERE schemaname = ? AND tablename = ? AND indexname = ?"
	mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs("app.part", "items", "idx.part").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	if !db.Dialect().HasIndex("`app.part`.`items`", "`app.part`.`idx.part`") {
		t.Fatal("quoted schema/index path was split incorrectly")
	}
	if db.Dialect().HasIndex("app.items", "other.idx") {
		t.Fatal("index in another schema matched target table")
	}
	query = "SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND schemaname = ? AND tablename = ? AND indexname = ?"
	mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs("app", "items", "idx").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	if db.Dialect().HasIndex("items", "app.idx") {
		t.Fatal("explicit index schema bypassed current_schema")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKingbaseMySQLQuoteEmbeddedBacktick(t *testing.T) {
	dialect := kb.NewKingbaseDialect(kb.KbModeMySQL)
	for input, want := range map[string]string{"select": "`select`", "a`b": "`a``b`", "`": "````", "a``b": "`a````b`"} {
		if got := dialect.Quote(input); got != want {
			t.Errorf("Quote(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestKingbaseMySQLKeyNameUsesInstanceLimit(t *testing.T) {
	for _, limit := range []int{63, 64, 128} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
			defer close()
			mock.ExpectQuery("^SHOW max_identifier_length$").WillReturnRows(sqlmock.NewRows([]string{"limit"}).AddRow(limit))
			dialect := db.Dialect()
			field := strings.Repeat("name", 30)
			rawDialect, ok := gorm.GetDialect("mysql")
			if !ok {
				t.Fatal("native MySQL dialect is not registered")
			}
			raw := rawDialect.BuildKeyName("idx", "users", field)
			var wait sync.WaitGroup
			for i := 0; i < 8; i++ {
				wait.Add(1)
				go func() {
					defer wait.Done()
					got := dialect.BuildKeyName("idx", "users", field)
					if len(got) > limit || got != dialect.BuildKeyName("idx", "users", field) {
						t.Errorf("unstable or oversized key: %q", got)
					}
					if limit >= len(raw) && got != raw {
						t.Errorf("supported name changed: got %q, want %q", got, raw)
					}
				}()
			}
			wait.Wait()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKingbaseMySQLKeyNameRetainsUnicodeAndDistinctSuffixes(t *testing.T) {
	dialect := kb.NewKingbaseDialect(kb.KbModeMySQL)
	first := dialect.BuildKeyName("idx", strings.Repeat("表", 25), strings.Repeat("列", 30)+"A")
	second := dialect.BuildKeyName("idx", strings.Repeat("表", 25), strings.Repeat("列", 30)+"B")
	if !utf8.ValidString(first) || !utf8.ValidString(second) || len(first) > 63 || len(second) > 63 || first == second {
		t.Fatalf("invalid, oversized or colliding names: %q / %q", first, second)
	}
}

func TestKingbaseMySQLCallerOffsetWithoutLimit(t *testing.T) {
	db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close()
	db.LogMode(false)
	for _, removeLimit := range []bool{false, true} {
		query := db.Table("users").Order("id ASC").Offset(2)
		if removeLimit {
			query = query.Limit(-1)
		}
		mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `users` ORDER BY id ASC OFFSET 2")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
		var rows []struct{ ID int }
		if err := query.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].ID != 3 {
			t.Fatalf("offset query: rows=%v, error=%v", rows, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Raw SQL is a capability probe, while RemoveIndex is the adaptation entry.
// Keep the ON clause and original error so the probe cannot silently pass by
// using the adapter's supported DROP INDEX form.
func TestKingbaseMySQLRawDDLRetainsOriginalSQLAndError(t *testing.T) {
	db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close()
	db.LogMode(false)
	query := "DROP INDEX `idx_users_name` ON `users`"
	failure := errors.New("syntax error near ON")
	mock.ExpectExec("^" + regexp.QuoteMeta(query) + "$").WillReturnError(failure)
	if err := db.Exec(query).Error; err != failure {
		t.Fatalf("raw DDL error=%v, want original error %v", err, failure)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKingbaseMySQLRemoveIndexUsesKingbaseSQL(t *testing.T) {
	for _, test := range []struct {
		name, table, index, query string
	}{
		{"ordinary", "users", "idx_users_name", "DROP INDEX `idx_users_name`"},
		{"table_schema", "APP.users", "idx_users_name", "DROP INDEX `APP`.`idx_users_name`"},
		{"quoted_names", "`APP`.`Order`", "`idx_Order_name`", "DROP INDEX `APP`.`idx_Order_name`"},
		{"double_quoted_names", `"APP"."Order"`, `"idx_Order_name"`, "DROP INDEX `APP`.`idx_Order_name`"},
		{"explicit_index_schema", "APP.users", "OTHER.idx_users_name", "DROP INDEX `OTHER`.`idx_users_name`"},
		{"reserved_word", "users", "select", "DROP INDEX `select`"},
		{"embedded_quote", "users", "idx`name", "DROP INDEX `idx``name`"},
		{"quoted_embedded_quote", "users", "`idx``name`", "DROP INDEX `idx``name`"},
		{"quoted_dot", "users", "`idx.part`", "DROP INDEX `idx.part`"},
		{"quoted_schema_dot", "`app.part`.`users`", "`idx.part`", "DROP INDEX `app.part`.`idx.part`"},
		{"double_quoted_dot", "users", `"idx.part"`, "DROP INDEX `idx.part`"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
			defer close()
			db.LogMode(false)
			// Exercise the real GORM entry point, not a test-side raw SQL path.
			mock.ExpectExec("^" + regexp.QuoteMeta(test.query) + "$").WillReturnResult(sqlmock.NewResult(0, 0))
			if err := db.Table(test.table).RemoveIndex(test.index).Error; err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKingbaseMySQLRemoveIndexPreservesDriverError(t *testing.T) {
	db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close()
	failure := errors.New("permission denied for index")
	mock.ExpectExec("^" + regexp.QuoteMeta("DROP INDEX `idx_users_name`") + "$").WillReturnError(failure)
	if err := db.Dialect().RemoveIndex("users", "idx_users_name"); err != failure {
		t.Fatalf("RemoveIndex error=%v, want original driver error %v", err, failure)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKingbaseMySQLDialectBoundaryHelpers(t *testing.T) {
	dialect := kb.NewKingbaseDialect(kb.KbModeMySQL)
	if got, want := dialect.Quote("select"), "`select`"; got != want {
		t.Fatalf("reserved identifier quote mismatch: got %q, want %q", got, want)
	}
	if got, want := dialect.SelectFromDummyTable(), "FROM DUAL"; got != want {
		t.Fatalf("dummy table mismatch: got %q, want %q", got, want)
	}
	if got := dialect.LastInsertIDOutputInterstitial("users", "id", []string{"name"}); got != "" {
		t.Fatalf("unexpected last-insert-id interstitial: %q", got)
	}
	if got := dialect.LastInsertIDReturningSuffix("users", "id"); got != "" {
		t.Fatalf("unexpected MySQL RETURNING suffix: %q", got)
	}
	if got, want := dialect.DefaultValueStr(), "VALUES()"; got != want {
		t.Fatalf("default value SQL mismatch: got %q, want %q", got, want)
	}

	for _, tt := range []struct {
		index, column string
		wantIndex     string
		wantColumn    string
	}{
		{index: "idx_name(16)", column: "name", wantIndex: "`idx_name`", wantColumn: "name(16)"},
		{index: "idx_name(0)", column: "name", wantIndex: "`idx_name`", wantColumn: "name(0)"},
		{index: "idx_name", column: "name", wantIndex: "`idx_name`", wantColumn: "name"},
		{index: "idx_name(x)", column: "name", wantIndex: "`idx_name(x)`", wantColumn: "name"},
		{index: "idx-special", column: "name", wantIndex: "`idx-special`", wantColumn: "name"},
		{index: "`idx.part`", column: "name", wantIndex: "`idx.part`", wantColumn: "name"},
	} {
		gotIndex, gotColumn := dialect.NormalizeIndexAndColumn(tt.index, tt.column)
		if gotIndex != tt.wantIndex || gotColumn != tt.wantColumn {
			t.Errorf("NormalizeIndexAndColumn(%q, %q) = (%q, %q), want (%q, %q)", tt.index, tt.column, gotIndex, gotColumn, tt.wantIndex, tt.wantColumn)
		}
	}

	longField := strings.Repeat("field", 20)
	first := dialect.BuildKeyName("idx", "users", longField)
	second := dialect.BuildKeyName("idx", "users", longField)
	if len(first) != 63 || first != second {
		t.Fatalf("disconnected long key name should be deterministic and 63 bytes: first=%q len=%d second=%q", first, len(first), second)
	}
	if got, want := kb.NewKingbaseDialect(kb.KbModeOracle).BuildKeyName("idx", "users", "name"), "idx_users_name"; got != want {
		t.Fatalf("oracle key name mismatch: got %q, want %q", got, want)
	}
}

func TestKingbaseMySQLForeignKeyLifecycleUsesSameGeneratedName(t *testing.T) {
	db, mock, close := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close()
	db.LogMode(false)
	child := "kb_ddl_1792850711196928042_2"
	destination := "`kb_ddl_1792850711196926398_1`(id)"
	mock.ExpectQuery("^SHOW max_identifier_length$").WillReturnRows(sqlmock.NewRows([]string{"limit"}).AddRow(63))
	name := db.Dialect().BuildKeyName(child, "parent_id", destination, "foreign")
	metadata := regexp.QuoteMeta("SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = current_schema() AND table_name = ? AND constraint_name = ? AND constraint_type = 'FOREIGN KEY'")
	mock.ExpectQuery(metadata).WithArgs(child, name).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	add := "ALTER TABLE `" + child + "` ADD CONSTRAINT " + name + " FOREIGN KEY (parent_id) REFERENCES " + destination + " ON DELETE RESTRICT ON UPDATE RESTRICT;"
	mock.ExpectExec("^" + regexp.QuoteMeta(add) + "$").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := db.Table(child).AddForeignKey("parent_id", destination, "RESTRICT", "RESTRICT").Error; err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(metadata).WithArgs(child, name).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec("^" + regexp.QuoteMeta("ALTER TABLE `"+child+"` DROP CONSTRAINT "+name+";") + "$").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := db.Table(child).RemoveForeignKey("parent_id", destination).Error; err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKingbaseRawDialectIsConnectionScoped(t *testing.T) {
	db1, mock1, close1 := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close1()
	db2, mock2, close2 := openMockGorm(t, kb.DialectKingbaseMySQL)
	defer close2()
	// Force the public CurrentDatabase path to delegate to native MySQL.
	// A shared delegate would query the other connection and fail expectations.
	for i, item := range []struct {
		db   *gorm.DB
		mock sqlmock.Sqlmock
		want string
	}{
		{db1, mock1, "first_schema"}, {db2, mock2, "second_schema"},
	} {
		item.mock.ExpectQuery("^" + regexp.QuoteMeta("SELECT current_schema()") + "$").WillReturnRows(sqlmock.NewRows([]string{"schema"}).AddRow(""))
		item.mock.ExpectQuery("^" + regexp.QuoteMeta("SELECT DATABASE()") + "$").WillReturnRows(sqlmock.NewRows([]string{"schema"}).AddRow(item.want))
		if got := item.db.Dialect().CurrentDatabase(); got != item.want {
			t.Errorf("connection %d: schema=%q, want %q", i, got, item.want)
		}
	}
	for _, mock := range []sqlmock.Sqlmock{mock1, mock2} {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}
