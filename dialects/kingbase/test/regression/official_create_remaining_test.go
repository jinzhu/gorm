//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"reflect"
	"testing"
	"time"

	kb "github.com/jinzhu/gorm/dialects/kingbase"
)

// 这些夹具让其余 Create 场景独立于较大的 official_coverage 夹具。上游测试使用共享 User
// 图；KingbaseES 测试使用简短隔离表名，使 MySQL 和 Oracle 可重复运行且类别间不泄漏状态。
type officialCreateRemainingRow struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	Age       int
	Payload   []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (officialCreateRemainingRow) TableName() string { return "kb_official_create_remaining" }

type officialCreateRemainingCompany struct {
	ID   uint `gorm:"primary_key"`
	Name string
}

func (officialCreateRemainingCompany) TableName() string { return "kb_official_create_companies" }

type officialCreateRemainingUser struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	CompanyID uint
	Company   officialCreateRemainingCompany
}

func (officialCreateRemainingUser) TableName() string { return "kb_official_create_users" }

type officialCreateRemainingAddress struct {
	ID      uint `gorm:"primary_key"`
	OwnerID uint
	Address string
}

func (officialCreateRemainingAddress) TableName() string { return "kb_official_create_addresses" }

type officialCreateRemainingOwner struct {
	ID      uint `gorm:"primary_key"`
	Name    string
	Address officialCreateRemainingAddress `gorm:"foreignkey:OwnerID"`
}

func (officialCreateRemainingOwner) TableName() string { return "kb_official_create_owners" }

// TestOfficialCreateRemainingCoverage 覆盖 official_coverage_test.go 的共享标量夹具
// 无法表达的 Create 场景。
func TestOfficialCreateRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db,
			&officialCreateRemainingRow{},
			&officialCreateRemainingCompany{}, &officialCreateRemainingUser{},
			&officialCreateRemainingAddress{}, &officialCreateRemainingOwner{},
		)
		defer db.DropTableIfExists(
			&officialCreateRemainingRow{},
			&officialCreateRemainingCompany{}, &officialCreateRemainingUser{},
			&officialCreateRemainingAddress{}, &officialCreateRemainingOwner{},
		)

		t.Run("TestCreate", func(t *testing.T) {
			if err := db.AutoMigrate(&officialCreateRemainingRow{}).Error; err != nil {
				t.Fatalf("automigrate create row: %v", err)
			}
			row := officialCreateRemainingRow{
				Name:    "create-" + string(mode),
				Age:     18,
				Payload: []byte("gorm-v1-create"),
			}
			if !db.NewRecord(row) || !db.NewRecord(&row) {
				t.Fatal("record should be new before create")
			}
			result := db.Save(&row)
			if result.Error != nil || result.RowsAffected != 1 {
				t.Fatalf("create result: rows=%d err=%v", result.RowsAffected, result.Error)
			}
			if db.NewRecord(row) || row.ID == 0 || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
				t.Fatalf("create did not populate identity/timestamps: %#v", row)
			}
			var loaded officialCreateRemainingRow
			if err := db.First(&loaded, row.ID).Error; err != nil {
				t.Fatalf("reload created row: %v", err)
			}
			if !reflect.DeepEqual(loaded.Payload, row.Payload) || loaded.Age != row.Age {
				t.Fatalf("created values mismatch: %#v", loaded)
			}
			createdAt := loaded.CreatedAt
			if err := db.Model(&row).Update("name", "updated-"+string(mode)).Error; err != nil {
				t.Fatalf("update after create: %v", err)
			}
			if err := db.First(&loaded, row.ID).Error; err != nil {
				t.Fatalf("reload updated row: %v", err)
			}
			if !loaded.CreatedAt.Equal(createdAt) {
				t.Fatalf("created_at changed during update: before=%s after=%s", createdAt, loaded.CreatedAt)
			}
		})

		t.Run("TestAnonymousField", func(t *testing.T) {
			if err := db.AutoMigrate(&officialCreateRemainingCompany{}, &officialCreateRemainingUser{}).Error; err != nil {
				t.Fatalf("automigrate anonymous relation: %v", err)
			}
			user := officialCreateRemainingUser{
				Name:    "anonymous-field-" + string(mode),
				Company: officialCreateRemainingCompany{Name: "company-" + string(mode)},
			}
			if err := db.Save(&user).Error; err != nil {
				t.Fatalf("save user with relation: %v", err)
			}
			if user.Company.ID == 0 || user.CompanyID == 0 {
				t.Fatalf("company relation was not saved: %#v", user)
			}
			var loaded officialCreateRemainingUser
			if err := db.First(&loaded, user.ID).Error; err != nil {
				t.Fatalf("reload user: %v", err)
			}
			if err := db.Model(&loaded).Related(&loaded.Company, "Company").Error; err != nil {
				t.Fatalf("load related company: %v", err)
			}
			if loaded.Company.Name != user.Company.Name {
				t.Fatalf("related company mismatch: %#v", loaded.Company)
			}
		})

		t.Run("TestSelectWithCreateAndOmitWithCreateAssociations", func(t *testing.T) {
			if err := db.AutoMigrate(&officialCreateRemainingCompany{}, &officialCreateRemainingUser{}).Error; err != nil {
				t.Fatalf("automigrate select/omit relations: %v", err)
			}
			selected := officialCreateRemainingUser{
				Name:    "selected-" + string(mode),
				Company: officialCreateRemainingCompany{Name: "selected-company"},
			}
			if err := db.Select("Name").Create(&selected).Error; err != nil {
				t.Fatalf("select create: %v", err)
			}
			var selectedReload officialCreateRemainingUser
			if err := db.First(&selectedReload, selected.ID).Error; err != nil {
				t.Fatalf("reload select create: %v", err)
			}
			if selectedReload.Name != selected.Name || selectedReload.CompanyID != 0 {
				t.Fatalf("select create persisted an unselected relation: %#v", selectedReload)
			}

			omitted := officialCreateRemainingUser{
				Name:    "omitted-" + string(mode),
				Company: officialCreateRemainingCompany{Name: "omitted-company"},
			}
			if err := db.Omit("Name", "Company").Create(&omitted).Error; err != nil {
				t.Fatalf("omit create: %v", err)
			}
			var omittedReload officialCreateRemainingUser
			if err := db.First(&omittedReload, omitted.ID).Error; err != nil {
				t.Fatalf("reload omit create: %v", err)
			}
			if omittedReload.Name != "" || omittedReload.CompanyID != 0 {
				t.Fatalf("omit create persisted an omitted field: %#v", omittedReload)
			}
		})
	})
}
