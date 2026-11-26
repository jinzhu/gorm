//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/erikstmartin/go-testdb"
	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
)

type officialMetadataMapped struct {
	ID      int64      `gorm:"column:mapped_id;primary_key"`
	Name    string     `gorm:"column:mapped_name"`
	Date    *time.Time `gorm:"column:mapped_time"`
	Ignored string     `sql:"-"`
}

func (officialMetadataMapped) TableName() string { return "kb_official_metadata_mapped" }

type officialMetadataIgnoredClash struct {
	ID      uint   `gorm:"primary_key"`
	Body    string `sql:"-"`
	RawBody string `gorm:"column:body"`
}

func (officialMetadataIgnoredClash) TableName() string { return "kb_official_metadata_clash" }

type officialMetadataScanner struct{ Data string }

func (v officialMetadataScanner) Value() (driver.Value, error) { return v.Data, nil }

func (v *officialMetadataScanner) Scan(src interface{}) error {
	switch value := src.(type) {
	case string:
		v.Data = value
	case []byte:
		v.Data = string(value)
	case nil:
		v.Data = ""
	default:
		return fmt.Errorf("metadata scanner cannot scan %T", src)
	}
	return nil
}

type officialMetadataScannerRow struct {
	ID       uint                     `gorm:"primary_key"`
	Explicit *officialMetadataScanner `sql:"type:varchar(64)"`
	Implicit *officialMetadataScanner `sql:"type:varchar(24)"`
}

func (officialMetadataScannerRow) TableName() string { return "kb_official_metadata_scanner" }

type officialMetadataChild struct {
	ID       uint `gorm:"primary_key"`
	ParentID uint
	Name     string
}

type OfficialMetadataEmbedded struct {
	Name     string
	Children []officialMetadataChild `gorm:"foreignkey:ParentID"`
}

type officialMetadataResponse struct {
	ID uint `gorm:"primary_key"`
	OfficialMetadataEmbedded
}

type officialMetadataTableName struct{ ID uint }

func (officialMetadataTableName) TableName() string { return "kb_official_named_table" }

// TestOfficialMetadataRemainingCoverage 在不依赖后端 SQL 的情况下验证模型解析、命名、
// Scanner/Valuer、Scope 和查询克隆。
func TestOfficialMetadataRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db, &officialMetadataMapped{}, &officialMetadataIgnoredClash{}, &officialMetadataScannerRow{}, &officialCoreRemainingRow{}, &officialEmbeddedRow{})
		defer db.DropTableIfExists(&officialMetadataMapped{}, &officialMetadataIgnoredClash{}, &officialMetadataScannerRow{}, &officialCoreRemainingRow{}, &officialEmbeddedRow{})

		// 查询克隆断言需要标准核心记录，不应使用后续 Scopes 子测试创建的无关低年龄记录。
		defer db.DropTableIfExists(&officialCoreRemainingChild{})
		if err := db.AutoMigrate(&officialCoreRemainingRow{}, &officialCoreRemainingChild{}).Error; err != nil {
			t.Fatalf("prepare metadata search fixtures: %v", err)
		}
		seedOfficialCoreRows(t, db, mode)

		t.Run("TestCustomizeColumn", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMetadataMapped{}).Error; err != nil {
				t.Fatalf("automigrate mapped columns: %v", err)
			}
			scope := db.NewScope(&officialMetadataMapped{})
			if scope.PrimaryKey() != "mapped_id" || !scope.Dialect().HasColumn(scope.TableName(), "mapped_name") {
				t.Fatalf("custom column metadata: table=%s pk=%s", scope.TableName(), scope.PrimaryKey())
			}
			now := time.Now().Truncate(time.Microsecond)
			row := officialMetadataMapped{ID: 666, Name: "foo", Date: &now, Ignored: "ignored"}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create mapped row: %v", err)
			}
			row.Name = "bar"
			if err := db.Save(&row).Error; err != nil {
				t.Fatalf("update mapped row: %v", err)
			}
			var loaded officialMetadataMapped
			if err := db.First(&loaded, "mapped_id = ?", row.ID).Error; err != nil || loaded.Name != "bar" || loaded.Ignored != "" {
				t.Fatalf("mapped row result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestCustomColumnAndIgnoredFieldClash", func(t *testing.T) {
			if err := db.AutoMigrate(&officialMetadataIgnoredClash{}).Error; err != nil {
				t.Fatalf("automigrate ignored/custom column clash: %v", err)
			}
			row := officialMetadataIgnoredClash{Body: "ignored", RawBody: "persisted"}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create ignored/custom column clash: %v", err)
			}
			var loaded officialMetadataIgnoredClash
			if err := db.First(&loaded, row.ID).Error; err != nil || loaded.Body != "" || loaded.RawBody != "persisted" {
				t.Fatalf("ignored/custom column result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestCalculateField", func(t *testing.T) {
			type Embedded struct {
				EmbeddedName string `sql:"NOT NULL;DEFAULT:'hello'"`
			}
			type model struct {
				ID       uint
				Children []officialMetadataChild `gorm:"foreignkey:ParentID"`
				Embedded
			}
			scope := db.NewScope(&model{})
			if field, ok := scope.FieldByName("Children"); !ok || field.Relationship == nil {
				t.Fatal("has-many field relationship was not calculated")
			}
			if field, ok := scope.FieldByName("embedded_name"); !ok {
				t.Fatal("embedded field was not calculated")
			} else if _, ok := field.TagSettingsGet("NOT NULL"); !ok {
				t.Fatal("embedded field tag settings were not retained")
			}
		})

		t.Run("TestFieldSet", func(t *testing.T) {
			model := &officialExtendedFieldModel{}
			field, ok := db.NewScope(model).FieldByName("Value")
			if !ok {
				t.Fatal("scanner field not found")
			}
			if err := field.Set("field-set-" + string(mode)); err != nil {
				t.Fatalf("Field.Set: %v", err)
			}
			if string(model.Value) != "field-set-"+string(mode) {
				t.Fatalf("Field.Set value=%q", model.Value)
			}
		})

		t.Run("TestScannerDataType", func(t *testing.T) {
			scope := db.NewScope(&officialMetadataScannerRow{})
			for _, name := range []string{"Explicit", "Implicit"} {
				field, ok := scope.FieldByName(name)
				if !ok {
					t.Fatalf("scanner field %s not found", name)
				}
				got := db.Dialect().DataTypeOf(field.StructField)
				if got == "" || !strings.Contains(strings.ToLower(got), "varchar") {
					t.Fatalf("scanner field %s type=%q", name, got)
				}
			}
		})

		t.Run("TestScannableSlices", func(t *testing.T) {
			if err := db.AutoMigrate(&officialScannerRow{}).Error; err != nil {
				t.Fatalf("automigrate scannable slice: %v", err)
			}
			row := officialScannerRow{Labels: officialJSONSlice{"a", "b", "c"}}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("save scannable slice: %v", err)
			}
			var loaded officialScannerRow
			if err := db.First(&loaded, row.ID).Error; err != nil || !reflect.DeepEqual(loaded.Labels, row.Labels) {
				t.Fatalf("scannable slice result=%#v err=%v", loaded.Labels, err)
			}
		})

		t.Run("TestValuer", func(t *testing.T) {
			if err := db.AutoMigrate(&officialExtendedScannerUser{}).Error; err != nil {
				t.Fatalf("automigrate Valuer row: %v", err)
			}
			defer db.DropTableIfExists(&officialExtendedScannerUser{})
			row := officialExtendedScannerUser{Name: "valuer-" + string(mode), Role: officialExtendedRole{Name: "admin"}}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("save Valuer row: %v", err)
			}
			var loaded officialExtendedScannerUser
			if err := db.Where("name = ? AND role = ?", row.Name, officialExtendedRole{Name: "admin"}).First(&loaded).Error; err != nil {
				t.Fatalf("query with Valuer: %v", err)
			}
		})

		t.Run("TestScopes", func(t *testing.T) {
			if err := db.AutoMigrate(&officialCoreRemainingRow{}).Error; err != nil {
				t.Fatalf("automigrate scope rows: %v", err)
			}
			prefix := "metadata-scope-" + string(mode)
			for i := 1; i <= 3; i++ {
				if err := db.Create(&officialCoreRemainingRow{Name: fmt.Sprintf("%s-%d", prefix, i), Age: i}).Error; err != nil {
					t.Fatalf("seed scope row: %v", err)
				}
			}
			scopeNames := func(names ...string) func(*gorm.DB) *gorm.DB {
				return func(query *gorm.DB) *gorm.DB { return query.Where("name IN (?)", names) }
			}
			var loaded []officialCoreRemainingRow
			if err := db.Scopes(scopeNames(prefix+"-1", prefix+"-3")).Find(&loaded).Error; err != nil || len(loaded) != 2 {
				t.Fatalf("Scopes rows=%d err=%v", len(loaded), err)
			}
		})

		t.Run("TestCloneSearch", func(t *testing.T) {
			base := db.Model(&officialCoreRemainingRow{}).Where("age >= ?", 10)
			var first, second []officialCoreRemainingRow
			if err := base.Where("category = ?", "a").Find(&first).Error; err != nil {
				t.Fatalf("first cloned search: %v", err)
			}
			if err := base.Where("category = ?", "b").Find(&second).Error; err != nil {
				t.Fatalf("second cloned search: %v", err)
			}
			if len(first) == 0 || len(second) == 0 {
				t.Fatalf("cloned search results: first=%d second=%d", len(first), len(second))
			}
		})

		t.Run("TestWhereCloneCorruption", func(t *testing.T) {
			base := db.Model(&officialCoreRemainingRow{}).Where("name LIKE ?", "core-%")
			var alpha, gamma []officialCoreRemainingRow
			if err := base.Where("age = ?", 10).Find(&alpha).Error; err != nil {
				t.Fatalf("first clone query: %v", err)
			}
			if err := base.Where("age = ?", 30).Find(&gamma).Error; err != nil {
				t.Fatalf("second clone query: %v", err)
			}
			if len(alpha) != 1 || len(gamma) != 1 || alpha[0].ID == gamma[0].ID {
				t.Fatalf("where clone corruption: alpha=%#v gamma=%#v", alpha, gamma)
			}
		})

		t.Run("TestModelStructRaceSameModel", func(t *testing.T) {
			officialRunModelStructRace(t, db, false)
		})
		t.Run("TestModelStructRaceDifferentModel", func(t *testing.T) {
			officialRunModelStructRace(t, db, true)
		})

		t.Run("TestModelStructEmbeddedHasMany", func(t *testing.T) {
			field, ok := db.NewScope(&officialMetadataResponse{}).FieldByName("Children")
			if !ok || field.Relationship == nil || field.Relationship.Kind != "has_many" {
				t.Fatalf("embedded has-many relationship: ok=%v field=%+v", ok, field)
			}
		})

		t.Run("TestSaveAndQueryEmbeddedStruct", func(t *testing.T) {
			if err := db.AutoMigrate(&officialEmbeddedRow{}).Error; err != nil {
				t.Fatalf("automigrate embedded struct: %v", err)
			}
			row := officialEmbeddedRow{
				OfficialEmbeddedBase:   OfficialEmbeddedBase{Title: "metadata-embedded-" + string(mode), URL: "https://example.test"},
				OfficialEmbeddedAuthor: OfficialEmbeddedAuthor{Name: "author", Email: "author@example.test"},
				Upvotes:                10,
			}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("save embedded struct: %v", err)
			}
			var loaded officialEmbeddedRow
			if err := db.First(&loaded, row.ID).Error; err != nil || loaded.Title != row.Title || loaded.Name != row.Name {
				t.Fatalf("embedded struct result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestTableName", func(t *testing.T) {
			if got := db.NewScope(&officialMetadataTableName{}).TableName(); got != "kb_official_named_table" {
				t.Fatalf("custom TableName=%q", got)
			}
			db.SingularTable(true)
			defer db.SingularTable(false)
			type cart struct{ ID uint }
			if got := db.NewScope(&cart{}).TableName(); got != "cart" {
				t.Fatalf("singular table name=%q", got)
			}
		})

		t.Run("TestTableNameConcurrently", func(t *testing.T) {
			const workers = 32
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(workers)
			errors := make(chan string, workers)
			for i := 0; i < workers; i++ {
				go func() {
					defer wg.Done()
					<-start
					if got := db.NewScope(&officialMetadataTableName{}).TableName(); got != "kb_official_named_table" {
						errors <- got
					}
				}()
			}
			close(start)
			wg.Wait()
			close(errors)
			for got := range errors {
				t.Fatalf("concurrent table name=%q", got)
			}
		})
	})
}

func officialRunModelStructRace(t *testing.T, db *gorm.DB, different bool) {
	t.Helper()
	type modelA struct {
		ID       uint
		Children []officialMetadataChild `gorm:"foreignkey:ParentID"`
	}
	type modelB struct {
		ID       uint
		Children []*officialMetadataChild `gorm:"foreignkey:ParentID"`
	}
	const workers = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			if different && i%2 == 1 {
				db.NewScope(&modelB{}).GetStructFields()
			} else {
				db.NewScope(&modelA{}).GetStructFields()
			}
		}()
	}
	close(start)
	wg.Wait()
}

// TestOfficialRuntimeIndependentCoverage 保留不依赖真实数据库连接的官方行为验证。
func TestOfficialRuntimeIndependentCoverage(t *testing.T) {
	t.Run("TestOpen_ReturnsError_WithBadArgs", func(t *testing.T) {
		for _, source := range []interface{}{42, time.Now(), new(string)} {
			if _, err := gorm.Open(testDialectKingbaseMySQL, source); err == nil || !strings.HasPrefix(err.Error(), "invalid database source:") {
				t.Fatalf("invalid database source %#v returned %v", source, err)
			}
		}
	})

	t.Run("TestOpenWithOneParameter", func(t *testing.T) {
		db, err := gorm.Open(testDialectKingbaseMySQL)
		if db != nil || err == nil {
			t.Fatalf("one-parameter Open: db=%v err=%v", db, err)
		}
	})

	t.Run("TestDdlErrors", func(t *testing.T) {
		sqlDB, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("create mock database: %v", err)
		}
		db, err := gorm.Open(testDialectKingbaseMySQL, sqlDB)
		if err != nil {
			t.Fatalf("open mock Kingbase dialect: %v", err)
		}
		_ = sqlDB.Close()
		if err := db.Find(&officialMetadataMapped{}).Error; err == nil {
			t.Fatal("operation on a closed database should fail")
		}
	})

	t.Run("TestCompatibilityMode", func(t *testing.T) {
		db, err := gorm.Open("testdb", "")
		if err != nil {
			t.Fatalf("open compatibility driver: %v", err)
		}
		defer db.Close()
		testdb.SetQueryFunc(func(string) (driver.Rows, error) {
			return testdb.RowsFromCSVString([]string{"id", "name", "age"}, "1,Tim,20\n2,Joe,25\n3,Bob,30"), nil
		})
		var rows []officialCoreRemainingRow
		if err := db.Find(&rows).Error; err != nil || len(rows) != 3 || rows[0].Name != "Tim" {
			t.Fatalf("compatibility result=%#v err=%v", rows, err)
		}
	})

	t.Run("TestTheNamingStrategy", func(t *testing.T) {
		cases := map[string]string{
			gorm.TheNamingStrategy.DB("auth"):                "auth",
			gorm.TheNamingStrategy.Table("userRestrictions"): "user_restrictions",
			gorm.TheNamingStrategy.Column("clientID"):        "client_id",
		}
		for got, want := range cases {
			if got != want {
				t.Fatalf("naming strategy=%q, want=%q", got, want)
			}
		}
	})

	t.Run("TestHstore", func(t *testing.T) {
		t.Skip("PostgreSQL hstore extension is not an applicable Kingbase MySQL/Oracle compatibility-mode contract")
	})
}
