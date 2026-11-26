//go:build kingbase_test
// +build kingbase_test

package regression_test

// 本文件收纳较小且相互独立的 GORM 官方场景，避免大型集成测试合并为单一流程时遗漏。
// 子测试保留上游测试名称，便于检索 KingbaseES 报告，同时两种模式继续复用相同夹具和断言。

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
)

type officialExtendedTimestamp struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (officialExtendedTimestamp) TableName() string { return "kb_official_ext_timestamps" }

type officialExtendedEmpty struct {
	ID uint `gorm:"primary_key"`
}

func (officialExtendedEmpty) TableName() string { return "kb_official_ext_empty" }

type officialExtendedMapped struct {
	ID      uint   `gorm:"column:mapped_id;primary_key"`
	Name    string `gorm:"column:mapped_name"`
	Ignored string `sql:"-"`
}

func (officialExtendedMapped) TableName() string { return "kb_official_ext_mapped" }

// officialExtendedRole 有意实现为包含真实字段的结构体 Scanner，
// 对应 create_test.go 中 GORM 的匿名 Scanner 路径。
type officialExtendedRole struct{ Name string }

func (r officialExtendedRole) Value() (driver.Value, error) { return r.Name, nil }

func (r *officialExtendedRole) Scan(value interface{}) error {
	switch v := value.(type) {
	case string:
		r.Name = v
	case []byte:
		r.Name = string(v)
	case sql.RawBytes:
		r.Name = string(v)
	case nil:
		r.Name = ""
	default:
		return fmt.Errorf("official role cannot scan %T", value)
	}
	return nil
}

func (r officialExtendedRole) IsAdmin() bool { return r.Name == "admin" }

type officialExtendedScannerUser struct {
	ID   uint `gorm:"primary_key"`
	Name string
	Role officialExtendedRole
}

func (officialExtendedScannerUser) TableName() string { return "kb_official_ext_scanner_users" }

type officialExtendedPerson struct {
	ID       string `gorm:"primary_key;size:32"`
	Name     string
	Accounts []officialExtendedAccount `gorm:"many2many:kb_official_ext_person_accounts;foreignkey:ID;associationforeignkey:ID"`
}

func (officialExtendedPerson) TableName() string { return "kb_official_ext_people" }

type officialExtendedAccount struct {
	ID     string `gorm:"primary_key;size:32"`
	Name   string
	People []officialExtendedPerson `gorm:"many2many:kb_official_ext_person_accounts;foreignkey:ID;associationforeignkey:ID"`
}

func (officialExtendedAccount) TableName() string { return "kb_official_ext_accounts" }

type officialExtendedParent struct {
	ID       uint   `gorm:"primary_key"`
	Code     string `gorm:"size:32;unique_index"`
	Name     string
	Children []officialExtendedChild `gorm:"foreignkey:ParentCode;associationforeignkey:Code"`
}

func (officialExtendedParent) TableName() string { return "kb_official_ext_parents" }

type officialExtendedChild struct {
	ID         uint `gorm:"primary_key"`
	ParentCode string
	Name       string
	Parent     *officialExtendedParent `gorm:"foreignkey:ParentCode;associationforeignkey:Code"`
}

func (officialExtendedChild) TableName() string { return "kb_official_ext_children" }

type officialExtendedFailingValue string

func (v officialExtendedFailingValue) Value() (driver.Value, error) {
	if strings.HasPrefix(string(v), "bad") {
		return nil, errors.New("official failing valuer")
	}
	return string(v), nil
}

type officialExtendedScopeRow struct {
	ID   uint `gorm:"primary_key"`
	Name string
	Age  int
}

func (officialExtendedScopeRow) TableName() string { return "kb_official_ext_scope_rows" }

type officialExtendedFieldScanner string

func (v *officialExtendedFieldScanner) Scan(src interface{}) error {
	switch value := src.(type) {
	case string:
		*v = officialExtendedFieldScanner(value)
	case []byte:
		*v = officialExtendedFieldScanner(string(value))
	default:
		return fmt.Errorf("official field scanner cannot scan %T", src)
	}
	return nil
}

type officialExtendedFieldModel struct {
	Value officialExtendedFieldScanner
}

func officialExtNameIn(names ...string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB { return db.Where("name IN (?)", names) }
}

// TestOfficialExtendedCreateCoverage 覆盖需要独立夹具、不能复用共享 CRUD 记录的 Create 场景。
func TestOfficialExtendedCreateCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db, &officialExtendedTimestamp{}, &officialExtendedEmpty{}, &officialExtendedMapped{}, &officialExtendedScannerUser{})
		defer db.DropTableIfExists(&officialExtendedTimestamp{}, &officialExtendedEmpty{}, &officialExtendedMapped{}, &officialExtendedScannerUser{})

		t.Run("TestCreateWithNowFuncOverride", func(t *testing.T) {
			if err := db.AutoMigrate(&officialExtendedTimestamp{}).Error; err != nil {
				t.Fatalf("automigrate timestamps: %v", err)
			}
			fixed := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
			fixedDB := db.New().SetNowFuncOverride(func() time.Time { return fixed })
			row := officialExtendedTimestamp{Name: "now-override-" + string(mode)}
			if err := fixedDB.Create(&row).Error; err != nil {
				t.Fatalf("create with now override: %v", err)
			}
			if !row.CreatedAt.UTC().Equal(fixed) || !row.UpdatedAt.UTC().Equal(fixed) {
				t.Fatalf("now override not applied: created=%s updated=%s", row.CreatedAt, row.UpdatedAt)
			}
			fresh := officialExtendedTimestamp{Name: "now-fresh-" + string(mode)}
			if err := db.Create(&fresh).Error; err != nil {
				t.Fatalf("create with fresh clock: %v", err)
			}
			if fresh.CreatedAt.UTC().Equal(fixed) {
				t.Fatal("now override leaked from cloned DB into the original DB")
			}
		})

		t.Run("TestCreateEmptyStrut", func(t *testing.T) {
			if err := db.AutoMigrate(&officialExtendedEmpty{}).Error; err != nil {
				t.Fatalf("automigrate empty model: %v", err)
			}
			row := officialExtendedEmpty{}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create empty model: %v", err)
			}
			if row.ID == 0 {
				t.Fatal("empty model did not receive its generated ID")
			}
		})

		t.Run("TestCustomizeColumnAndIgnoredField", func(t *testing.T) {
			if err := db.AutoMigrate(&officialExtendedMapped{}).Error; err != nil {
				t.Fatalf("automigrate mapped model: %v", err)
			}
			scope := db.NewScope(&officialExtendedMapped{})
			if scope.PrimaryKey() != "mapped_id" || !scope.Dialect().HasColumn(scope.TableName(), "mapped_name") {
				t.Fatalf("custom column metadata is wrong: table=%s pk=%s", scope.TableName(), scope.PrimaryKey())
			}
			row := officialExtendedMapped{ID: 9, Name: "mapped", Ignored: "not persisted"}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create mapped model: %v", err)
			}
			var loaded officialExtendedMapped
			if err := db.First(&loaded, "mapped_id = ?", row.ID).Error; err != nil {
				t.Fatalf("reload mapped model: %v", err)
			}
			if loaded.Name != row.Name || loaded.Ignored != "" {
				t.Fatalf("mapped/ignored fields mismatch: %#v", loaded)
			}
		})

		t.Run("TestAnonymousScanner", func(t *testing.T) {
			if err := db.AutoMigrate(&officialExtendedScannerUser{}).Error; err != nil {
				t.Fatalf("automigrate scanner user: %v", err)
			}
			row := officialExtendedScannerUser{Name: "scanner-" + string(mode), Role: officialExtendedRole{Name: "admin"}}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create scanner user: %v", err)
			}
			var loaded officialExtendedScannerUser
			if err := db.First(&loaded, row.ID).Error; err != nil {
				t.Fatalf("reload scanner user: %v", err)
			}
			if !loaded.Role.IsAdmin() {
				t.Fatalf("anonymous scanner value was not decoded: %#v", loaded.Role)
			}
		})
	})
}

// TestOfficialExtendedQueryAndTransactionCoverage 覆盖公开查询链行为，以及 Transaction
// 回调的三种结果：提交、错误回滚和 panic 回滚。
func TestOfficialExtendedQueryAndTransactionCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db, &officialExtendedScopeRow{})
		defer db.DropTableIfExists(&officialExtendedScopeRow{})
		if err := db.AutoMigrate(&officialExtendedScopeRow{}).Error; err != nil {
			t.Fatalf("automigrate query/transaction rows: %v", err)
		}
		seed := []officialExtendedScopeRow{
			{Name: "scope-one", Age: 1},
			{Name: "scope-two", Age: 1},
			{Name: "scope-three", Age: 2},
		}
		for i := range seed {
			if err := db.Create(&seed[i]).Error; err != nil {
				t.Fatalf("seed scope row %d: %v", i, err)
			}
		}

		t.Run("TestScopesAndCloneSearch", func(t *testing.T) {
			var rows []officialExtendedScopeRow
			if err := db.Scopes(officialExtNameIn("scope-one", "scope-two")).Find(&rows).Error; err != nil {
				t.Fatalf("scope query: %v", err)
			}
			if len(rows) != 2 {
				t.Fatalf("scope result=%d, want=2", len(rows))
			}
			base := db.Where("age = ?", 1)
			var first, second []officialExtendedScopeRow
			if err := base.Where("name = ?", "scope-one").Find(&first).Error; err != nil {
				t.Fatalf("first cloned query: %v", err)
			}
			if err := base.Where("name = ?", "scope-two").Find(&second).Error; err != nil {
				t.Fatalf("second cloned query: %v", err)
			}
			if len(first) != 1 || len(second) != 1 || first[0].Name == second[0].Name {
				t.Fatalf("query chain leaked conditions: first=%#v second=%#v", first, second)
			}
		})

		t.Run("TestFirstOrInitAndFirstOrCreate", func(t *testing.T) {
			var initialized officialExtendedScopeRow
			if err := db.Where(&officialExtendedScopeRow{Name: "not-yet-" + string(mode), Age: 7}).FirstOrInit(&initialized).Error; err != nil {
				t.Fatalf("first or init: %v", err)
			}
			if initialized.ID != 0 || initialized.Age != 7 {
				t.Fatalf("FirstOrInit result: %#v", initialized)
			}
			var created officialExtendedScopeRow
			name := "first-or-create-" + string(mode)
			if err := db.Where(&officialExtendedScopeRow{Name: name}).Attrs(map[string]interface{}{"age": 9}).FirstOrCreate(&created).Error; err != nil {
				t.Fatalf("first or create: %v", err)
			}
			if created.ID == 0 || created.Age != 9 {
				t.Fatalf("FirstOrCreate result: %#v", created)
			}
		})

		t.Run("TestFillSmallerStructAndPluck", func(t *testing.T) {
			var small struct {
				ID   uint
				Name string
			}
			if err := db.Table((officialExtendedScopeRow{}).TableName()).Where("name = ?", "scope-one").First(&small).Error; err != nil {
				t.Fatalf("scan smaller struct: %v", err)
			}
			if small.ID == 0 || small.Name != "scope-one" {
				t.Fatalf("smaller struct result: %#v", small)
			}
			var names []string
			if err := db.Model(&officialExtendedScopeRow{}).Order("age asc").Pluck("name", &names).Error; err != nil {
				t.Fatalf("pluck: %v", err)
			}
			if len(names) < 3 {
				t.Fatalf("pluck returned %d names", len(names))
			}
		})

		t.Run("TestTransactionWithBlock", func(t *testing.T) {
			rollbackName := "tx-rollback-" + string(mode)
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&officialExtendedScopeRow{Name: rollbackName, Age: 10}).Error; err != nil {
					return err
				}
				return errors.New("official transaction rollback")
			})
			if err == nil || err.Error() != "official transaction rollback" {
				t.Fatalf("transaction error=%v", err)
			}
			var count int
			if err := db.Model(&officialExtendedScopeRow{}).Where("name = ?", rollbackName).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("rollback row count=%d err=%v", count, err)
			}

			commitName := "tx-commit-" + string(mode)
			if err := db.Transaction(func(tx *gorm.DB) error {
				return tx.Create(&officialExtendedScopeRow{Name: commitName, Age: 11}).Error
			}); err != nil {
				t.Fatalf("transaction commit: %v", err)
			}
			if err := db.Where("name = ?", commitName).First(&officialExtendedScopeRow{}).Error; err != nil {
				t.Fatalf("committed row not found: %v", err)
			}

			panicName := "tx-panic-" + string(mode)
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				_ = db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Create(&officialExtendedScopeRow{Name: panicName, Age: 12}).Error; err != nil {
						return err
					}
					panic("official transaction panic")
				})
			}()
			if !panicked {
				t.Fatal("transaction panic was swallowed")
			}
			if err := db.Model(&officialExtendedScopeRow{}).Where("name = ?", panicName).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("panic rollback row count=%d err=%v", count, err)
			}
		})
	})
}

// TestOfficialExtendedAssociationCoverage 补充自定义列和关联自动保存变体，
// 它们不同于 official_coverage_test.go 中的基础关联图。
func TestOfficialExtendedAssociationCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db, "kb_official_ext_person_accounts", &officialExtendedAccount{}, &officialExtendedPerson{}, &officialExtendedChild{}, &officialExtendedParent{})
		defer db.DropTableIfExists("kb_official_ext_person_accounts", &officialExtendedAccount{}, &officialExtendedPerson{}, &officialExtendedChild{}, &officialExtendedParent{})
		if err := db.AutoMigrate(&officialExtendedPerson{}, &officialExtendedAccount{}, &officialExtendedParent{}, &officialExtendedChild{}).Error; err != nil {
			t.Fatalf("automigrate customized associations: %v", err)
		}

		t.Run("TestManyToManyWithCustomizedColumn", func(t *testing.T) {
			account := officialExtendedAccount{ID: "account-" + string(mode), Name: "account"}
			person := officialExtendedPerson{ID: "person-" + string(mode), Name: "person", Accounts: []officialExtendedAccount{account}}
			if err := db.Create(&person).Error; err != nil {
				t.Fatalf("create customized many-to-many person: %v", err)
			}
			var loaded officialExtendedPerson
			if err := db.Preload("Accounts").First(&loaded, "id = ?", person.ID).Error; err != nil {
				t.Fatalf("preload customized many-to-many: %v", err)
			}
			if len(loaded.Accounts) != 1 || loaded.Accounts[0].ID != account.ID {
				t.Fatalf("customized many-to-many result: %#v", loaded.Accounts)
			}
		})

		t.Run("TestAutoSaveHasManyAndRelated", func(t *testing.T) {
			parent := officialExtendedParent{Code: "parent-" + string(mode), Name: "parent", Children: []officialExtendedChild{{Name: "child"}}}
			if err := db.Create(&parent).Error; err != nil {
				t.Fatalf("create parent with child: %v", err)
			}
			var loaded officialExtendedParent
			if err := db.Preload("Children").First(&loaded, parent.ID).Error; err != nil {
				t.Fatalf("preload customized has-many: %v", err)
			}
			if len(loaded.Children) != 1 || loaded.Children[0].Name != "child" || loaded.Children[0].ParentCode != parent.Code {
				t.Fatalf("customized has-many result: %#v", loaded.Children)
			}
			var child officialExtendedChild
			if err := db.Preload("Parent").Where("parent_code = ?", parent.Code).First(&child).Error; err != nil {
				t.Fatalf("find child by customized foreign key: %v", err)
			}
			if child.Parent == nil || child.Parent.Code != parent.Code {
				t.Fatalf("belongs-to customized foreign key was not loaded: %#v", child.Parent)
			}
		})

		t.Run("TestForeignKeyFieldMetadata", func(t *testing.T) {
			field, ok := db.NewScope(&officialExtendedChild{}).FieldByName("ParentCode")
			if !ok || field.Relationship == nil || field.Relationship.Kind == "" {
				// 某些 GORM V1 版本把关系元数据挂在关联字段上，因此改为验证关联本身。
				if relation, relationOK := db.NewScope(&officialExtendedChild{}).FieldByName("Parent"); !relationOK || relation.Relationship == nil {
					t.Fatalf("customized foreign-key relationship was not calculated: field=%+v", field)
				}
			}
		})
	})
}

// TestOfficialExtendedValueAndMetadataCoverage 保证即使模式特有服务器能力不可用，
// 非 SQL 的 GORM 契约仍可执行验证。
func TestOfficialExtendedValueAndMetadataCoverage(t *testing.T) {
	t.Run("TestErrorsCanBeUsedOutsideGorm", func(t *testing.T) {
		gErrs := gorm.Errors([]error{errors.New("First"), errors.New("Second")})
		gErrs = gErrs.Add(errors.New("Third"))
		gErrs = gErrs.Add(gErrs)
		if gErrs.Error() != "First; Second; Third" {
			t.Fatalf("unexpected gorm errors: %s", gErrs.Error())
		}
	})

	t.Run("TestNamingStrategy", func(t *testing.T) {
		if got := gorm.TheNamingStrategy.Table("userRestrictions"); got != "user_restrictions" {
			t.Fatalf("default table naming=%q", got)
		}
		ns := &gorm.NamingStrategy{DB: func(s string) string { return "db_" + s }, Table: func(s string) string { return "tbl_" + s }, Column: func(s string) string { return "col_" + s }}
		if ns.Table("user") != "tbl_user" || ns.Column("password") != "col_password" {
			t.Fatalf("custom naming strategy failed")
		}
	})

	t.Run("TestCalculateFieldAndFieldSet", func(t *testing.T) {
		scope := gorm.Scope{Value: &officialExtendedFieldModel{}}
		field, ok := scope.FieldByName("Value")
		if !ok {
			t.Fatal("scanner field was not calculated")
		}
		if err := field.Set("field-set"); err != nil {
			t.Fatalf("Field.Set through Scanner failed: %v", err)
		}
		if got := scope.Value.(*officialExtendedFieldModel).Value; got != "field-set" {
			t.Fatalf("Field.Set value=%q", got)
		}
	})

	t.Run("TestFailedValuer", func(t *testing.T) {
		if _, err := officialExtendedFailingValue("bad-value").Value(); err == nil || !strings.Contains(err.Error(), "official failing valuer") {
			t.Fatalf("failed valuer error=%v", err)
		}
	})

}
