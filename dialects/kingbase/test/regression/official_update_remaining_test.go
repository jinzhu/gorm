//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
)

type officialUpdateRemainingProduct struct {
	ID          uint `gorm:"primary_key"`
	Code        string
	Price       int
	UpdatedAt   time.Time
	Ignored     int    `sql:"-"`
	DisplayName string `gorm:"column:display_name"`
}

func (officialUpdateRemainingProduct) TableName() string { return "kb_official_update_products" }

type officialUpdateRemainingAddress struct {
	ID      uint `gorm:"primary_key"`
	OwnerID uint
	Street  string
}

func (officialUpdateRemainingAddress) TableName() string { return "kb_official_update_addresses" }

type officialUpdateRemainingOwner struct {
	ID      uint `gorm:"primary_key"`
	Name    string
	Age     int
	Address officialUpdateRemainingAddress `gorm:"foreignkey:OwnerID"`
}

func (officialUpdateRemainingOwner) TableName() string { return "kb_official_update_owners" }

type officialUpdateRemainingDefault struct {
	Counter   uint64 `gorm:"primary_key"`
	Name      string `gorm:"default:'galeone'"`
	From      string
	UpdatedAt time.Time
}

func (officialUpdateRemainingDefault) TableName() string { return "kb_official_update_defaults" }

// TestOfficialUpdateRemainingCoverage 覆盖 update_test.go 中 Select、Omit、Map、
// UpdateColumn 的组合及虚拟字段行为。
func TestOfficialUpdateRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db, &officialUpdateRemainingProduct{}, &officialUpdateRemainingAddress{}, &officialUpdateRemainingOwner{}, &officialUpdateRemainingDefault{})
		defer db.DropTableIfExists(&officialUpdateRemainingProduct{}, &officialUpdateRemainingAddress{}, &officialUpdateRemainingOwner{}, &officialUpdateRemainingDefault{})
		if err := db.AutoMigrate(&officialUpdateRemainingProduct{}, &officialUpdateRemainingAddress{}, &officialUpdateRemainingOwner{}, &officialUpdateRemainingDefault{}).Error; err != nil {
			t.Fatalf("automigrate update fixtures: %v", err)
		}

		first := officialUpdateRemainingProduct{Code: "product-one-" + string(mode), Price: 10, DisplayName: "one"}
		second := officialUpdateRemainingProduct{Code: "product-two-" + string(mode), Price: 20, DisplayName: "two"}
		if err := db.Create(&first).Error; err != nil {
			t.Fatalf("create first product: %v", err)
		}
		if err := db.Create(&second).Error; err != nil {
			t.Fatalf("create second product: %v", err)
		}

		t.Run("TestUpdate", func(t *testing.T) {
			// 使用确定的时间间隔比较持久化时间戳；原生 MySQL DATETIME 可能只保留秒，
			// 而内存值包含纳秒。
			old := time.Now().Add(-time.Hour).Truncate(time.Second)
			if err := db.Model(&first).UpdateColumn("updated_at", old).Error; err != nil {
				t.Fatalf("seed persisted update timestamp: %v", err)
			}
			var persisted officialUpdateRemainingProduct
			if err := db.First(&persisted, first.ID).Error; err != nil || !persisted.UpdatedAt.Equal(old) {
				t.Fatalf("read seeded update timestamp: got=%s want=%s err=%v", persisted.UpdatedAt, old, err)
			}
			before := persisted.UpdatedAt
			if err := db.Model(&first).Update("code", "product-one-updated-"+string(mode)).Error; err != nil {
				t.Fatalf("single update: %v", err)
			}
			if err := db.Model(&officialUpdateRemainingProduct{}).Where("id = ?", second.ID).Update("price", gorm.Expr("price + ?", 5)).Error; err != nil {
				t.Fatalf("expression update: %v", err)
			}
			var loaded officialUpdateRemainingProduct
			if err := db.First(&loaded, first.ID).Error; err != nil {
				t.Fatalf("reload updated product: %v", err)
			}
			if loaded.Code != "product-one-updated-"+string(mode) {
				t.Fatalf("code was not updated: %#v", loaded)
			}
			if !loaded.UpdatedAt.After(before) {
				t.Fatalf("updated_at did not advance: before=%s after=%s", before, loaded.UpdatedAt)
			}
		})

		t.Run("TestUpdates", func(t *testing.T) {
			if err := db.Model(&first).Updates(map[string]interface{}{"price": 100, "display_name": "map-updated"}).Error; err != nil {
				t.Fatalf("map Updates: %v", err)
			}
			if err := db.Model(&second).Updates(officialUpdateRemainingProduct{Code: "product-two-updated-" + string(mode)}).Error; err != nil {
				t.Fatalf("struct Updates: %v", err)
			}
			var loaded officialUpdateRemainingProduct
			if err := db.First(&loaded, first.ID).Error; err != nil {
				t.Fatalf("reload map update: %v", err)
			}
			if loaded.Price != 100 || loaded.DisplayName != "map-updated" {
				t.Fatalf("map update result: %#v", loaded)
			}
			loaded = officialUpdateRemainingProduct{}
			if err := db.First(&loaded, second.ID).Error; err != nil || loaded.Code != "product-two-updated-"+string(mode) {
				t.Fatalf("struct update result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestUpdateColumn", func(t *testing.T) {
			var loaded officialUpdateRemainingProduct
			if err := db.First(&loaded, first.ID).Error; err != nil {
				t.Fatalf("reload before UpdateColumn: %v", err)
			}
			before := loaded.UpdatedAt
			if err := db.Model(&loaded).UpdateColumn(map[string]interface{}{"price": 150, "display_name": "column-updated"}).Error; err != nil {
				t.Fatalf("map UpdateColumn: %v", err)
			}
			if err := db.Model(&loaded).UpdateColumn("price", gorm.Expr("price + ?", 5)).Error; err != nil {
				t.Fatalf("expression UpdateColumn: %v", err)
			}
			if err := db.First(&loaded, first.ID).Error; err != nil {
				t.Fatalf("reload UpdateColumn: %v", err)
			}
			if loaded.Price != 155 || loaded.DisplayName != "column-updated" {
				t.Fatalf("UpdateColumn result: %#v", loaded)
			}
			if !before.IsZero() && !loaded.UpdatedAt.Equal(before) {
				t.Fatalf("UpdateColumn changed updated_at: before=%s after=%s", before, loaded.UpdatedAt)
			}
		})

		t.Run("TestSelectWithUpdateWithMap", func(t *testing.T) {
			if err := db.Model(&first).Select("code").Updates(map[string]interface{}{"code": "select-code", "price": 999}).Error; err != nil {
				t.Fatalf("Select map update: %v", err)
			}
			var loaded officialUpdateRemainingProduct
			if err := db.First(&loaded, first.ID).Error; err != nil {
				t.Fatalf("reload Select update: %v", err)
			}
			if loaded.Code != "select-code" || loaded.Price == 999 {
				t.Fatalf("Select update wrote wrong fields: %#v", loaded)
			}
		})

		t.Run("TestOmitWithUpdateWithMap", func(t *testing.T) {
			if err := db.Model(&first).Omit("code").Updates(map[string]interface{}{"code": "omit-code", "price": 321}).Error; err != nil {
				t.Fatalf("Omit map update: %v", err)
			}
			var loaded officialUpdateRemainingProduct
			if err := db.First(&loaded, first.ID).Error; err != nil {
				t.Fatalf("reload Omit update: %v", err)
			}
			if loaded.Code == "omit-code" || loaded.Price != 321 {
				t.Fatalf("Omit update wrote wrong fields: %#v", loaded)
			}
		})

		t.Run("TestSelectWithUpdateColumn", func(t *testing.T) {
			row := officialUpdateRemainingProduct{Code: "select-column-original-" + string(mode), Price: 40}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create Select UpdateColumn row: %v", err)
			}
			if err := db.Model(&row).Select("code").UpdateColumn(map[string]interface{}{"code": "select-column-code", "price": 400}).Error; err != nil {
				t.Fatalf("Select UpdateColumn: %v", err)
			}
			var loaded officialUpdateRemainingProduct
			if err := db.First(&loaded, row.ID).Error; err != nil {
				t.Fatalf("reload Select UpdateColumn: %v", err)
			}
			if loaded.Code != "select-column-code" || loaded.Price != 40 {
				t.Fatalf("Select UpdateColumn result: %#v", loaded)
			}
		})

		t.Run("TestOmitWithUpdateColumn", func(t *testing.T) {
			row := officialUpdateRemainingProduct{Code: "omit-column-original-" + string(mode), Price: 41}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create Omit UpdateColumn row: %v", err)
			}
			if err := db.Model(&row).Omit("code").UpdateColumn(map[string]interface{}{"code": "omit-column-code", "price": 401}).Error; err != nil {
				t.Fatalf("Omit UpdateColumn: %v", err)
			}
			var loaded officialUpdateRemainingProduct
			if err := db.First(&loaded, row.ID).Error; err != nil {
				t.Fatalf("reload Omit UpdateColumn: %v", err)
			}
			if loaded.Code != row.Code || loaded.Price != 401 {
				t.Fatalf("Omit UpdateColumn result: %#v", loaded)
			}
		})

		t.Run("TestUpdateColumnsSkipsAssociations", func(t *testing.T) {
			owner := officialUpdateRemainingOwner{Name: "owner-" + string(mode), Age: 10}
			if err := db.Create(&owner).Error; err != nil {
				t.Fatalf("create owner: %v", err)
			}
			address := officialUpdateRemainingAddress{OwnerID: owner.ID, Street: "first street"}
			if err := db.Create(&address).Error; err != nil {
				t.Fatalf("create address: %v", err)
			}
			owner.Address = officialUpdateRemainingAddress{ID: address.ID, OwnerID: owner.ID, Street: "second street"}
			if result := db.Model(&owner).UpdateColumns(officialUpdateRemainingOwner{Age: 100, Address: owner.Address}); result.Error != nil || result.RowsAffected != 1 {
				t.Fatalf("UpdateColumns result: rows=%d err=%v", result.RowsAffected, result.Error)
			}
			var loadedAddress officialUpdateRemainingAddress
			if err := db.First(&loadedAddress, address.ID).Error; err != nil {
				t.Fatalf("reload address: %v", err)
			}
			if loadedAddress.Street != "first street" {
				t.Fatalf("UpdateColumns unexpectedly saved association: %#v", loadedAddress)
			}
		})

		t.Run("TestUpdatesTableWithIgnoredValues", func(t *testing.T) {
			row := officialUpdateRemainingProduct{Code: "ignored-fields-" + string(mode), Price: 1, Ignored: 10}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create ignored-field row: %v", err)
			}
			if err := db.Table(row.TableName()).Where("id = ?", row.ID).Updates(&officialUpdateRemainingProduct{Price: 2, Ignored: 100}).Error; err != nil {
				t.Fatalf("Updates ignored field: %v", err)
			}
			var loaded officialUpdateRemainingProduct
			if err := db.First(&loaded, row.ID).Error; err != nil {
				t.Fatalf("reload ignored-field row: %v", err)
			}
			if loaded.Price != 2 || loaded.Ignored != 0 {
				t.Fatalf("ignored field was persisted: %#v", loaded)
			}
		})

		t.Run("TestUpdateDecodeVirtualAttributes", func(t *testing.T) {
			row := officialUpdateRemainingProduct{Code: "virtual-fields-" + string(mode), Price: 1, Ignored: 10}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create virtual-field row: %v", err)
			}
			if result := db.Model(&row).Updates(officialUpdateRemainingProduct{Code: "virtual-updated", Ignored: 100}); result.Error != nil {
				t.Fatalf("virtual attribute update: %v", result.Error)
			}
			if row.Ignored != 100 {
				t.Fatalf("virtual attribute was not decoded into the struct: %d", row.Ignored)
			}
		})

		t.Run("TestUpdateWithNoStdPrimaryKeyAndDefaultValues", func(t *testing.T) {
			row := officialUpdateRemainingDefault{From: "first-" + string(mode)}
			if err := db.Save(&row).Error; err != nil || row.Counter == 0 {
				t.Fatalf("create default row: counter=%d err=%v", row.Counter, err)
			}
			if row.Name != "galeone" {
				// 部分 KingbaseES 版本只在重新加载后返回数据库默认值；官方测试也会在断言前显式重载。
				var reloaded officialUpdateRemainingDefault
				if err := db.First(&reloaded, row.Counter).Error; err != nil {
					t.Fatalf("reload default row: %v", err)
				}
				row = reloaded
			}
			if err := db.Model(&row).Update("from", "updated-from").Error; err != nil {
				t.Fatalf("update default row: %v", err)
			}
			var check officialUpdateRemainingDefault
			if err := db.First(&check, row.Counter).Error; err != nil {
				t.Fatalf("reload updated default row: %v", err)
			}
			if check.Name != "galeone" || check.From != "updated-from" {
				t.Fatalf("default row changed incorrectly: %#v", check)
			}
		})
	})
}
