//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
)

type officialCustomPerson struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	Addresses []*officialCustomAddress `gorm:"many2many:kb_official_custom_person_addresses;jointable_foreignkey:person_id;association_jointable_foreignkey:address_id"`
}

func (officialCustomPerson) TableName() string { return "kb_official_custom_people" }

type officialCustomAddress struct {
	ID      uint `gorm:"primary_key"`
	Address string
}

func (officialCustomAddress) TableName() string { return "kb_official_custom_addresses" }

type officialCustomPersonAddress struct {
	gorm.JoinTableHandler
	PersonID  uint
	AddressID uint
	DeletedAt *time.Time
}

func (officialCustomPersonAddress) TableName() string { return "kb_official_custom_person_addresses" }

func (*officialCustomPersonAddress) Add(_ gorm.JoinTableHandlerInterface, db *gorm.DB, source interface{}, destination interface{}) error {
	personID, _ := strconv.ParseUint(fmt.Sprint(db.NewScope(source).PrimaryKeyValue()), 10, 64)
	addressID, _ := strconv.ParseUint(fmt.Sprint(db.NewScope(destination).PrimaryKeyValue()), 10, 64)
	values := map[string]interface{}{"person_id": personID, "address_id": addressID}
	if result := db.Unscoped().Model(&officialCustomPersonAddress{}).Where(values).Updates(map[string]interface{}{"deleted_at": gorm.Expr("NULL")}); result.Error != nil {
		return result.Error
	} else if result.RowsAffected == 0 {
		return db.Create(&officialCustomPersonAddress{PersonID: uint(personID), AddressID: uint(addressID)}).Error
	}
	return nil
}

func (*officialCustomPersonAddress) Delete(_ gorm.JoinTableHandlerInterface, db *gorm.DB, _ ...interface{}) error {
	return db.Delete(&officialCustomPersonAddress{}).Error
}

func (h *officialCustomPersonAddress) JoinWith(_ gorm.JoinTableHandlerInterface, db *gorm.DB, _ interface{}) *gorm.DB {
	table := h.Table(db)
	addressTable := db.NewScope(&officialCustomAddress{}).QuotedTableName()
	return db.Joins("INNER JOIN " + table + " ON " + table + ".address_id = " + addressTable + ".id").Where(table + ".deleted_at IS NULL")
}

type officialCustomUser struct {
	ID    uint   `gorm:"primary_key"`
	Email string `gorm:"column:email_address;unique_index"`
}

func (officialCustomUser) TableName() string { return "kb_official_custom_users" }

type officialCustomInvitation struct {
	ID      uint                `gorm:"primary_key"`
	Address string              `gorm:"column:invitation"`
	Person  *officialCustomUser `gorm:"foreignkey:Email;associationforeignkey:Address"`
}

func (officialCustomInvitation) TableName() string { return "kb_official_custom_invitations" }

type officialCustomDiscount struct {
	ID       uint `gorm:"primary_key"`
	Name     string
	Coupons  []*officialCustomCoupon `gorm:"foreignkey:DiscountID"`
	Rule     *officialCustomRule     `gorm:"foreignkey:DiscountID"`
	Benefits []officialCustomBenefit `gorm:"foreignkey:PromotionID"`
}

func (officialCustomDiscount) TableName() string { return "kb_official_custom_discounts" }

type officialCustomCoupon struct {
	ID         uint `gorm:"primary_key"`
	Code       string
	DiscountID uint
	Discount   officialCustomDiscount
}

func (officialCustomCoupon) TableName() string { return "kb_official_custom_coupons" }

type officialCustomRule struct {
	ID         uint `gorm:"primary_key"`
	Name       string
	DiscountID uint
	Discount   *officialCustomDiscount
}

func (officialCustomRule) TableName() string { return "kb_official_custom_rules" }

type officialCustomBenefit struct {
	ID          uint `gorm:"primary_key"`
	Name        string
	PromotionID uint
	Discount    officialCustomDiscount `gorm:"foreignkey:PromotionID"`
}

func (officialCustomBenefit) TableName() string { return "kb_official_custom_benefits" }

type officialCustomSelfUser struct {
	ID      uint `gorm:"primary_key"`
	Name    string
	Friends []*officialCustomSelfUser `gorm:"many2many:kb_official_custom_friends;association_jointable_foreignkey:friend_id"`
}

func (officialCustomSelfUser) TableName() string { return "kb_official_custom_self_users" }

type officialCustomCategory struct {
	ID         uint `gorm:"primary_key"`
	ParentID   uint
	Name       string
	Categories []officialCustomCategory `gorm:"foreignkey:ParentID"`
}

func (officialCustomCategory) TableName() string { return "kb_official_custom_categories" }

type officialAutoSaveCompany struct {
	ID     uint `gorm:"primary_key"`
	UserID uint
	Name   string
}

func (officialAutoSaveCompany) TableName() string { return "kb_official_autosave_companies" }

type officialAutoSaveBelongsUser struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	CompanyID uint
	Company   officialAutoSaveCompany `gorm:"association_autoupdate:false;association_autocreate:false;foreignkey:CompanyID"`
}

func (officialAutoSaveBelongsUser) TableName() string { return "kb_official_autosave_belongs_users" }

type officialAutoSaveHasOneUser struct {
	ID      uint `gorm:"primary_key"`
	Name    string
	Company officialAutoSaveCompany `gorm:"association_autoupdate:false;association_autocreate:false;foreignkey:UserID"`
}

func (officialAutoSaveHasOneUser) TableName() string { return "kb_official_autosave_has_one_users" }

type officialAutoSaveManyUser struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	Companies []officialAutoSaveCompany `gorm:"many2many:kb_official_autosave_user_companies;association_autoupdate:false;association_autocreate:false;"`
}

func (officialAutoSaveManyUser) TableName() string { return "kb_official_autosave_many_users" }

type officialSaveAssociationAddress struct {
	ID      uint `gorm:"primary_key"`
	Address string
}

func (officialSaveAssociationAddress) TableName() string {
	return "kb_official_save_association_addresses"
}

type officialSaveAssociationPlace struct {
	ID             uint `gorm:"primary_key"`
	PlaceAddressID uint
	PlaceAddress   *officialSaveAssociationAddress `gorm:"save_associations:false"`
	OwnerAddressID uint
	OwnerAddress   *officialSaveAssociationAddress `gorm:"save_associations:true"`
}

func (officialSaveAssociationPlace) TableName() string {
	return "kb_official_save_association_places"
}

// TestOfficialCustomRelationRemainingCoverage 覆盖自定义列、自定义连接表和关联自动保存场景。
func TestOfficialCustomRelationRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		values := []interface{}{
			"kb_official_custom_person_addresses", "kb_official_custom_friends", "kb_official_autosave_user_companies",
			&officialCustomInvitation{}, &officialCustomUser{},
			&officialCustomCoupon{}, &officialCustomRule{}, &officialCustomBenefit{}, &officialCustomDiscount{},
			&officialCustomPerson{}, &officialCustomAddress{}, &officialCustomSelfUser{}, &officialCustomCategory{},
			&officialAutoSaveBelongsUser{}, &officialAutoSaveHasOneUser{}, &officialAutoSaveManyUser{}, &officialAutoSaveCompany{},
			&officialSaveAssociationPlace{}, &officialSaveAssociationAddress{},
		}
		dropOfficialTables(t, db, values...)
		defer db.DropTableIfExists(values...)
		if err := db.AutoMigrate(
			&officialCustomPerson{}, &officialCustomAddress{}, &officialCustomPersonAddress{},
			&officialCustomUser{}, &officialCustomInvitation{},
			&officialCustomDiscount{}, &officialCustomCoupon{}, &officialCustomRule{}, &officialCustomBenefit{},
			&officialCustomSelfUser{}, &officialCustomCategory{},
			&officialAutoSaveCompany{}, &officialAutoSaveBelongsUser{}, &officialAutoSaveHasOneUser{}, &officialAutoSaveManyUser{},
			&officialSaveAssociationAddress{}, &officialSaveAssociationPlace{},
		).Error; err != nil {
			t.Fatalf("automigrate custom relation fixtures: %v", err)
		}

		t.Run("TestJoinTable", func(t *testing.T) {
			db.SetJoinTableHandler(&officialCustomPerson{}, "Addresses", &officialCustomPersonAddress{})
			first := &officialCustomAddress{Address: "address-1"}
			second := &officialCustomAddress{Address: "address-2"}
			person := officialCustomPerson{Name: "person-" + string(mode), Addresses: []*officialCustomAddress{first, second}}
			if err := db.Save(&person).Error; err != nil {
				t.Fatalf("save custom join graph: %v", err)
			}
			if err := db.Model(&person).Association("Addresses").Delete(first).Error; err != nil {
				t.Fatalf("soft delete custom join row: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&person).Association("Addresses").Count(), 1, "custom join scoped")
			var joins []officialCustomPersonAddress
			if err := db.Unscoped().Where("person_id = ?", person.ID).Find(&joins).Error; err != nil || len(joins) != 2 {
				t.Fatalf("custom join unscoped rows=%d err=%v", len(joins), err)
			}
			if err := db.Model(&person).Association("Addresses").Clear().Error; err != nil {
				t.Fatalf("clear custom join table: %v", err)
			}
		})

		t.Run("TestOneToOneWithCustomizedColumn", func(t *testing.T) {
			user := officialCustomUser{Email: "one-to-one-" + string(mode) + "@example.test"}
			invitation := officialCustomInvitation{Address: user.Email}
			if err := db.Create(&user).Error; err != nil {
				t.Fatalf("create custom-column user: %v", err)
			}
			if err := db.Create(&invitation).Error; err != nil {
				t.Fatalf("create custom-column invitation: %v", err)
			}
			var loaded officialCustomInvitation
			if err := db.Preload("Person").First(&loaded, invitation.ID).Error; err != nil || loaded.Person == nil || loaded.Person.Email != user.Email {
				t.Fatalf("custom one-to-one result: %#v err=%v", loaded, err)
			}
		})

		t.Run("TestOneToManyWithCustomizedColumn", func(t *testing.T) {
			discount := officialCustomDiscount{Name: "one-to-many-" + string(mode), Coupons: []*officialCustomCoupon{{Code: "coupon-1"}, {Code: "coupon-2"}}}
			if err := db.Create(&discount).Error; err != nil {
				t.Fatalf("create custom has-many: %v", err)
			}
			var loaded officialCustomDiscount
			if err := db.Preload("Coupons").First(&loaded, discount.ID).Error; err != nil || len(loaded.Coupons) != 2 {
				t.Fatalf("custom has-many result: %#v err=%v", loaded.Coupons, err)
			}
			var coupon officialCustomCoupon
			if err := db.Preload("Discount").Where("code = ?", "coupon-1").First(&coupon).Error; err != nil || coupon.Discount.Name != discount.Name {
				t.Fatalf("custom belongs-to result: %#v err=%v", coupon.Discount, err)
			}
		})

		t.Run("TestHasOneWithPartialCustomizedColumn", func(t *testing.T) {
			discount := officialCustomDiscount{Name: "has-one-" + string(mode), Rule: &officialCustomRule{Name: "rule"}}
			if err := db.Create(&discount).Error; err != nil {
				t.Fatalf("create partial custom has-one: %v", err)
			}
			var loaded officialCustomDiscount
			if err := db.Preload("Rule").First(&loaded, discount.ID).Error; err != nil || loaded.Rule == nil || loaded.Rule.Name != "rule" {
				t.Fatalf("partial custom has-one result: %#v err=%v", loaded.Rule, err)
			}
		})

		t.Run("TestBelongsToWithPartialCustomizedColumn", func(t *testing.T) {
			discount := officialCustomDiscount{Name: "benefit-" + string(mode), Benefits: []officialCustomBenefit{{Name: "free-delivery"}, {Name: "gift"}}}
			if err := db.Create(&discount).Error; err != nil {
				t.Fatalf("create partial custom belongs-to graph: %v", err)
			}
			var benefit officialCustomBenefit
			if err := db.Preload("Discount").Where("name = ?", "free-delivery").First(&benefit).Error; err != nil || benefit.Discount.Name != discount.Name {
				t.Fatalf("partial custom belongs-to result: %#v err=%v", benefit.Discount, err)
			}
		})

		t.Run("TestSelfReferencingMany2ManyColumn", func(t *testing.T) {
			friends := []*officialCustomSelfUser{{Name: "friend-1"}, {Name: "friend-2"}}
			for _, friend := range friends {
				if err := db.Create(friend).Error; err != nil {
					t.Fatalf("create self friend: %v", err)
				}
			}
			user := officialCustomSelfUser{Name: "self-" + string(mode), Friends: friends}
			if err := db.Create(&user).Error; err != nil {
				t.Fatalf("create self-referencing graph: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&user).Association("Friends").Count(), 2, "self-referencing many-to-many")
			var loaded officialCustomSelfUser
			if err := db.Preload("Friends").First(&loaded, user.ID).Error; err != nil || len(loaded.Friends) != 2 {
				t.Fatalf("self-referencing preload: %#v err=%v", loaded.Friends, err)
			}
			if err := db.Model(&loaded).Association("Friends").Replace(&officialCustomSelfUser{Name: "friend-3"}).Error; err != nil {
				t.Fatalf("replace self-referencing friends: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&user).Association("Friends").Count(), 1, "self-referencing replace")
		})

		t.Run("TestHasManyChildrenWithOneStruct", func(t *testing.T) {
			category := officialCustomCategory{Name: "main-" + string(mode), Categories: []officialCustomCategory{{Name: "sub-1"}, {Name: "sub-2"}}}
			if err := db.Save(&category).Error; err != nil {
				t.Fatalf("save self has-many graph: %v", err)
			}
			if len(category.Categories) != 2 || category.Categories[0].ParentID != category.ID || category.Categories[1].ParentID != category.ID {
				t.Fatalf("self has-many keys: %#v", category.Categories)
			}
		})

		t.Run("TestAutoSaveBelongsToAssociation", func(t *testing.T) {
			blockedName := "autosave-belongs-blocked-" + string(mode)
			blocked := officialAutoSaveBelongsUser{Name: "blocked", Company: officialAutoSaveCompany{Name: blockedName}}
			if err := db.Save(&blocked).Error; err != nil {
				t.Fatalf("save blocked belongs-to: %v", err)
			}
			if err := db.Where("name = ?", blockedName).First(&officialAutoSaveCompany{}).Error; !gorm.IsRecordNotFoundError(err) {
				t.Fatalf("association_autocreate:false created company: %v", err)
			}
			enabledName := "autosave-belongs-enabled-" + string(mode)
			enabled := officialAutoSaveBelongsUser{Name: "enabled", Company: officialAutoSaveCompany{Name: enabledName}}
			if err := db.Set("gorm:association_autocreate", true).Save(&enabled).Error; err != nil {
				t.Fatalf("force belongs-to autocreate: %v", err)
			}
			if enabled.Company.ID == 0 || enabled.CompanyID == 0 {
				t.Fatalf("belongs-to autocreate did not set keys: %#v", enabled)
			}
		})

		t.Run("TestAutoSaveHasOneAssociation", func(t *testing.T) {
			blockedName := "autosave-has-one-blocked-" + string(mode)
			blocked := officialAutoSaveHasOneUser{Name: "blocked", Company: officialAutoSaveCompany{Name: blockedName}}
			if err := db.Save(&blocked).Error; err != nil {
				t.Fatalf("save blocked has-one: %v", err)
			}
			if err := db.Where("name = ?", blockedName).First(&officialAutoSaveCompany{}).Error; !gorm.IsRecordNotFoundError(err) {
				t.Fatalf("association_autocreate:false created has-one company: %v", err)
			}
			enabled := officialAutoSaveHasOneUser{Name: "enabled", Company: officialAutoSaveCompany{Name: "autosave-has-one-enabled-" + string(mode)}}
			if err := db.Set("gorm:association_autocreate", true).Save(&enabled).Error; err != nil {
				t.Fatalf("force has-one autocreate: %v", err)
			}
			if enabled.Company.ID == 0 || enabled.Company.UserID != enabled.ID {
				t.Fatalf("has-one autocreate did not set keys: %#v", enabled)
			}
		})

		t.Run("TestAutoSaveMany2ManyAssociation", func(t *testing.T) {
			blockedName := "autosave-many-blocked-" + string(mode)
			blocked := officialAutoSaveManyUser{Name: "blocked", Companies: []officialAutoSaveCompany{{Name: blockedName}}}
			if err := db.Save(&blocked).Error; err != nil {
				t.Fatalf("save blocked many-to-many: %v", err)
			}
			if err := db.Where("name = ?", blockedName).First(&officialAutoSaveCompany{}).Error; !gorm.IsRecordNotFoundError(err) {
				t.Fatalf("association_autocreate:false created many-to-many company: %v", err)
			}
			enabled := officialAutoSaveManyUser{Name: "enabled", Companies: []officialAutoSaveCompany{{Name: "autosave-many-enabled-" + string(mode)}}}
			if err := db.Set("gorm:association_autocreate", true).Save(&enabled).Error; err != nil {
				t.Fatalf("force many-to-many autocreate: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&enabled).Association("Companies").Count(), 1, "forced many-to-many autosave")
		})

		t.Run("TestSaveAssociations", func(t *testing.T) {
			shared := officialSaveAssociationAddress{Address: "shared-" + string(mode)}
			if err := db.Create(&shared).Error; err != nil {
				t.Fatalf("create shared address: %v", err)
			}
			first := officialSaveAssociationPlace{
				PlaceAddressID: shared.ID,
				PlaceAddress:   &shared,
				OwnerAddress:   &officialSaveAssociationAddress{Address: "owner-one-" + string(mode)},
			}
			if err := db.Create(&first).Error; err != nil {
				t.Fatalf("create first place: %v", err)
			}
			if first.OwnerAddress == nil || first.OwnerAddress.ID == 0 || first.OwnerAddressID != first.OwnerAddress.ID {
				t.Fatalf("save_associations:true did not save owner address: %#v", first)
			}
			second := officialSaveAssociationPlace{
				PlaceAddressID: shared.ID,
				// 关联已显式禁用保存，因此不得插入该临时值；外键仍复用 shared。
				PlaceAddress: &officialSaveAssociationAddress{Address: "must-not-save-" + string(mode)},
				OwnerAddress: &officialSaveAssociationAddress{Address: "owner-two-" + string(mode)},
			}
			if err := db.Create(&second).Error; err != nil {
				t.Fatalf("create second place: %v", err)
			}
			var addressCount, disabledCount, placeCount int
			if err := db.Model(&officialSaveAssociationAddress{}).Count(&addressCount).Error; err != nil {
				t.Fatalf("count addresses: %v", err)
			}
			if err := db.Model(&officialSaveAssociationAddress{}).Where("address = ?", second.PlaceAddress.Address).Count(&disabledCount).Error; err != nil {
				t.Fatalf("count disabled association: %v", err)
			}
			if err := db.Model(&officialSaveAssociationPlace{}).Where("place_address_id = ?", shared.ID).Count(&placeCount).Error; err != nil {
				t.Fatalf("count places sharing address: %v", err)
			}
			if addressCount != 3 || disabledCount != 0 || placeCount != 2 || second.OwnerAddressID == 0 {
				t.Fatalf("save association counts: addresses=%d disabled=%d places=%d second=%#v", addressCount, disabledCount, placeCount, second)
			}
		})
	})
}
