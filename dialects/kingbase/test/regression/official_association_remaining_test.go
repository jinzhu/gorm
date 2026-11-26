//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
)

type officialAssocRemainingCategory struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	DeletedAt *time.Time
}

func (officialAssocRemainingCategory) TableName() string { return "kb_official_assoc_categories" }

type officialAssocRemainingPost struct {
	ID          uint `gorm:"primary_key"`
	Title       string
	CategoryRef uint
	Category    officialAssocRemainingCategory `gorm:"foreignkey:CategoryRef"`
}

func (officialAssocRemainingPost) TableName() string { return "kb_official_assoc_posts" }

type officialAssocRemainingCard struct {
	ID        uint `gorm:"primary_key"`
	OwnerID   uint
	Number    string
	DeletedAt *time.Time
}

func (officialAssocRemainingCard) TableName() string { return "kb_official_assoc_cards" }

type officialAssocRemainingChild struct {
	ID        uint `gorm:"primary_key"`
	ParentID  uint
	Name      string
	DeletedAt *time.Time
}

func (officialAssocRemainingChild) TableName() string { return "kb_official_assoc_children" }

type officialAssocRemainingLanguage struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	DeletedAt *time.Time
}

func (officialAssocRemainingLanguage) TableName() string { return "kb_official_assoc_languages" }

type officialAssocRemainingOwner struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	Card      officialAssocRemainingCard       `gorm:"foreignkey:OwnerID"`
	Children  []officialAssocRemainingChild    `gorm:"foreignkey:ParentID"`
	Languages []officialAssocRemainingLanguage `gorm:"many2many:kb_official_assoc_owner_languages;"`
}

func (officialAssocRemainingOwner) TableName() string { return "kb_official_assoc_owners" }

type OfficialAssocEmbeddedFields struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	Languages []officialAssocRemainingLanguage `gorm:"many2many:kb_official_assoc_embedded_languages;"`
}

type officialAssocEmbeddedOwner struct {
	OfficialAssocEmbeddedFields
	ExternalID string
}

func (officialAssocEmbeddedOwner) TableName() string { return "kb_official_assoc_embedded_owners" }

type officialAssocNamedToy struct {
	ID        uint `gorm:"primary_key"`
	Name      string
	OwnerID   uint
	OwnerType string
}

func (officialAssocNamedToy) TableName() string { return "kb_official_assoc_named_toys" }

type officialAssocNamedOwner struct {
	ID           uint `gorm:"primary_key"`
	Name         string
	PreferredToy officialAssocNamedToy `gorm:"polymorphic:Owner;polymorphic_value:preferred"`
	OtherToy     officialAssocNamedToy `gorm:"polymorphic:Owner;polymorphic_value:other"`
}

func (officialAssocNamedOwner) TableName() string { return "kb_official_assoc_named_owners" }

type officialAssocCompositeTag struct {
	ID     uint   `gorm:"primary_key"`
	Locale string `gorm:"primary_key;size:16"`
	Value  string
}

func (officialAssocCompositeTag) TableName() string { return "kb_official_assoc_composite_tags" }

type officialAssocCompositeBlog struct {
	ID         uint   `gorm:"primary_key"`
	Locale     string `gorm:"primary_key;size:16"`
	Subject    string
	SharedTags []officialAssocCompositeTag `gorm:"many2many:kb_official_assoc_shared_tags;foreignkey:ID;associationforeignkey:ID"`
	LocaleTags []officialAssocCompositeTag `gorm:"many2many:kb_official_assoc_locale_tags;foreignkey:ID,Locale;associationforeignkey:ID"`
}

func (officialAssocCompositeBlog) TableName() string { return "kb_official_assoc_composite_blogs" }

func officialAssertAssociationCount(t *testing.T, got, want int, label string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s association count=%d, want=%d", label, got, want)
	}
}

// TestOfficialAssociationRemainingCoverage 将 association_test.go、join_table_test.go、
// polymorphic_test.go 和 multi_primary_keys_test.go 中尚未覆盖的场景映射到 KingbaseES 执行层。
func TestOfficialAssociationRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		values := []interface{}{
			"kb_official_assoc_owner_languages", "kb_official_assoc_embedded_languages",
			"kb_official_assoc_shared_tags", "kb_official_assoc_locale_tags",
			&officialAssocNamedToy{}, &officialAssocNamedOwner{},
			&officialAssocRemainingCard{}, &officialAssocRemainingChild{},
			&officialAssocRemainingLanguage{}, &officialAssocRemainingOwner{},
			&officialAssocRemainingPost{}, &officialAssocRemainingCategory{},
			&officialAssocEmbeddedOwner{},
			&officialAssocCompositeBlog{}, &officialAssocCompositeTag{},
		}
		dropOfficialTables(t, db, values...)
		defer db.DropTableIfExists(values...)
		if err := db.AutoMigrate(
			&officialAssocRemainingCategory{}, &officialAssocRemainingPost{},
			&officialAssocRemainingOwner{}, &officialAssocRemainingCard{}, &officialAssocRemainingChild{}, &officialAssocRemainingLanguage{},
			&officialAssocEmbeddedOwner{},
			&officialAssocNamedOwner{}, &officialAssocNamedToy{},
			&officialAssocCompositeBlog{}, &officialAssocCompositeTag{},
		).Error; err != nil {
			t.Fatalf("automigrate association fixtures: %v", err)
		}

		t.Run("TestBelongsTo", func(t *testing.T) {
			post := officialAssocRemainingPost{Title: "belongs-to-" + string(mode), Category: officialAssocRemainingCategory{Name: "category-1"}}
			if err := db.Save(&post).Error; err != nil {
				t.Fatalf("save belongs-to graph: %v", err)
			}
			if post.Category.ID == 0 || post.CategoryRef == 0 {
				t.Fatalf("belongs-to keys not populated: %#v", post)
			}
			var category officialAssocRemainingCategory
			if err := db.Model(&post).Association("Category").Find(&category).Error; err != nil || category.Name != "category-1" {
				t.Fatalf("find belongs-to: %#v err=%v", category, err)
			}
			var related officialAssocRemainingCategory
			if err := db.Model(&post).Related(&related, "Category").Error; err != nil || related.ID != category.ID {
				t.Fatalf("Related belongs-to: %#v err=%v", related, err)
			}
			second := officialAssocRemainingCategory{Name: "category-2"}
			if err := db.Model(&post).Association("Category").Append(&second).Error; err != nil {
				t.Fatalf("append belongs-to: %v", err)
			}
			third := officialAssocRemainingCategory{Name: "category-3"}
			if err := db.Model(&post).Association("Category").Replace(&third).Error; err != nil {
				t.Fatalf("replace belongs-to: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&post).Association("Category").Count(), 1, "belongs-to replace")
			if err := db.Model(&post).Association("Category").Delete(&third).Error; err != nil {
				t.Fatalf("delete belongs-to: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&post).Association("Category").Count(), 0, "belongs-to delete")
			if err := db.Model(&post).Association("Category").Append(&second).Error; err != nil {
				t.Fatalf("append before clear belongs-to: %v", err)
			}
			if err := db.Model(&post).Association("Category").Clear().Error; err != nil {
				t.Fatalf("clear belongs-to: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&post).Association("Category").Count(), 0, "belongs-to clear")
		})

		t.Run("TestHasOne", func(t *testing.T) {
			owner := officialAssocRemainingOwner{Name: "has-one-" + string(mode), Card: officialAssocRemainingCard{Number: "card-1"}}
			if err := db.Save(&owner).Error; err != nil {
				t.Fatalf("save has-one graph: %v", err)
			}
			if owner.Card.ID == 0 || owner.Card.OwnerID != owner.ID {
				t.Fatalf("has-one key not populated: %#v", owner.Card)
			}
			second := officialAssocRemainingCard{Number: "card-2"}
			if err := db.Model(&owner).Association("Card").Append(&second).Error; err != nil {
				t.Fatalf("append has-one: %v", err)
			}
			var found officialAssocRemainingCard
			if err := db.Model(&owner).Related(&found, "Card").Error; err != nil || found.Number != "card-2" {
				t.Fatalf("Related has-one: %#v err=%v", found, err)
			}
			third := officialAssocRemainingCard{Number: "card-3"}
			if err := db.Model(&owner).Association("Card").Replace(&third).Error; err != nil {
				t.Fatalf("replace has-one: %v", err)
			}
			if err := db.Model(&owner).Association("Card").Delete(&third).Error; err != nil {
				t.Fatalf("delete has-one: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Card").Count(), 0, "has-one delete")
			if err := db.Model(&owner).Association("Card").Append(&officialAssocRemainingCard{Number: "card-4"}).Error; err != nil {
				t.Fatalf("append has-one before clear: %v", err)
			}
			if err := db.Model(&owner).Association("Card").Clear().Error; err != nil {
				t.Fatalf("clear has-one: %v", err)
			}
		})

		t.Run("TestHasMany", func(t *testing.T) {
			owner := officialAssocRemainingOwner{Name: "has-many-" + string(mode), Children: []officialAssocRemainingChild{{Name: "child-1"}, {Name: "child-2"}}}
			if err := db.Save(&owner).Error; err != nil {
				t.Fatalf("save has-many graph: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Children").Count(), 2, "has-many save")
			third := officialAssocRemainingChild{Name: "child-3"}
			if err := db.Model(&owner).Association("Children").Append(&third).Error; err != nil {
				t.Fatalf("append has-many: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Children").Count(), 3, "has-many append")
			if err := db.Model(&owner).Association("Children").Delete(owner.Children[0]).Error; err != nil {
				t.Fatalf("delete has-many: %v", err)
			}
			fourth := officialAssocRemainingChild{Name: "child-4"}
			if err := db.Model(&owner).Association("Children").Replace(&fourth).Error; err != nil {
				t.Fatalf("replace has-many: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Children").Count(), 1, "has-many replace")
			if err := db.Model(&owner).Association("Children").Clear().Error; err != nil {
				t.Fatalf("clear has-many: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Children").Count(), 0, "has-many clear")
		})

		t.Run("TestManyToMany", func(t *testing.T) {
			owner := officialAssocRemainingOwner{Name: "many-to-many-" + string(mode), Languages: []officialAssocRemainingLanguage{{Name: "zh"}, {Name: "en"}}}
			if err := db.Save(&owner).Error; err != nil {
				t.Fatalf("save many-to-many graph: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Languages").Count(), 2, "many-to-many save")
			third := officialAssocRemainingLanguage{Name: "de"}
			if err := db.Model(&owner).Association("Languages").Append(&third).Error; err != nil {
				t.Fatalf("append many-to-many: %v", err)
			}
			if err := db.Model(&owner).Association("Languages").Delete(&owner.Languages[0]).Error; err != nil {
				t.Fatalf("delete many-to-many: %v", err)
			}
			if err := db.Model(&owner).Association("Languages").Replace(&third).Error; err != nil {
				t.Fatalf("replace many-to-many: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Languages").Count(), 1, "many-to-many replace")
			if err := db.Model(&owner).Association("Languages").Clear().Error; err != nil {
				t.Fatalf("clear many-to-many: %v", err)
			}
		})

		t.Run("TestAssociationSoftDeleteAndUnscoped", func(t *testing.T) {
			owner := officialAssocRemainingOwner{Name: "soft-delete-" + string(mode)}
			if err := db.Create(&owner).Error; err != nil {
				t.Fatalf("create soft-delete owner: %v", err)
			}
			language := officialAssocRemainingLanguage{Name: "soft-language"}
			if err := db.Model(&owner).Association("Languages").Append(&language).Error; err != nil {
				t.Fatalf("append soft-delete language: %v", err)
			}
			if err := db.Delete(&language).Error; err != nil {
				t.Fatalf("soft delete language: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Languages").Count(), 0, "scoped soft-deleted many-to-many")
			officialAssertAssociationCount(t, db.Unscoped().Model(&owner).Association("Languages").Count(), 1, "unscoped soft-deleted many-to-many")
		})

		t.Run("TestRelated", func(t *testing.T) {
			owner := officialAssocRemainingOwner{Name: "related-" + string(mode), Children: []officialAssocRemainingChild{{Name: "related-child-1"}, {Name: "related-child-2"}}}
			if err := db.Save(&owner).Error; err != nil {
				t.Fatalf("save Related graph: %v", err)
			}
			var children []officialAssocRemainingChild
			if err := db.Model(&owner).Related(&children, "Children").Error; err != nil || len(children) != 2 {
				t.Fatalf("Related slice: len=%d err=%v", len(children), err)
			}
			children = nil
			if err := db.Model(&owner).Where("name = ?", "related-child-1").Related(&children, "Children").Error; err != nil || len(children) != 1 {
				t.Fatalf("Related with condition: len=%d err=%v", len(children), err)
			}
		})

		t.Run("TestAutoSaveBelongsToAssociationAndHasOneAndMany2Many", func(t *testing.T) {
			post := officialAssocRemainingPost{Title: "autosave-belongs-to", Category: officialAssocRemainingCategory{Name: "autosaved-category"}}
			if err := db.Create(&post).Error; err != nil || post.Category.ID == 0 || post.CategoryRef == 0 {
				t.Fatalf("autosave belongs-to: %#v err=%v", post, err)
			}
			owner := officialAssocRemainingOwner{
				Name:      "autosave-owner-" + string(mode),
				Card:      officialAssocRemainingCard{Number: "autosaved-card"},
				Languages: []officialAssocRemainingLanguage{{Name: "autosaved-language"}},
			}
			if err := db.Create(&owner).Error; err != nil {
				t.Fatalf("autosave has-one/many-to-many: %v", err)
			}
			if owner.Card.ID == 0 || len(owner.Languages) != 1 || owner.Languages[0].ID == 0 {
				t.Fatalf("autosaved associations have no IDs: %#v", owner)
			}
		})

		t.Run("TestEmbeddedMany2ManyRelationship", func(t *testing.T) {
			owner := officialAssocEmbeddedOwner{
				OfficialAssocEmbeddedFields: OfficialAssocEmbeddedFields{Name: "embedded-" + string(mode), Languages: []officialAssocRemainingLanguage{{Name: "embedded-1"}, {Name: "embedded-2"}}},
				ExternalID:                  "external",
			}
			if err := db.Save(&owner).Error; err != nil {
				t.Fatalf("save embedded many-to-many: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Languages").Count(), 2, "embedded many-to-many")
			if err := db.Model(&owner).Association("Languages").Delete(&owner.Languages[0]).Error; err != nil {
				t.Fatalf("delete embedded many-to-many: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("Languages").Count(), 1, "embedded delete")
			if err := db.Model(&owner).Association("Languages").Clear().Error; err != nil {
				t.Fatalf("clear embedded many-to-many: %v", err)
			}
		})

		t.Run("TestNamedPolymorphic", func(t *testing.T) {
			owner := officialAssocNamedOwner{Name: "named-poly-" + string(mode), PreferredToy: officialAssocNamedToy{Name: "bike"}, OtherToy: officialAssocNamedToy{Name: "treadmill"}}
			if err := db.Save(&owner).Error; err != nil {
				t.Fatalf("save named polymorphic graph: %v", err)
			}
			var loaded officialAssocNamedOwner
			if err := db.Preload("PreferredToy").Preload("OtherToy").First(&loaded, owner.ID).Error; err != nil {
				t.Fatalf("preload named polymorphic graph: %v", err)
			}
			if loaded.PreferredToy.Name != "bike" || loaded.OtherToy.Name != "treadmill" || loaded.PreferredToy.OwnerType == loaded.OtherToy.OwnerType {
				t.Fatalf("named polymorphic values mismatch: %#v", loaded)
			}
			if err := db.Model(&owner).Association("PreferredToy").Replace(&officialAssocNamedToy{Name: "bike-2"}).Error; err != nil {
				t.Fatalf("replace preferred toy: %v", err)
			}
			if err := db.Model(&owner).Association("PreferredToy").Clear().Error; err != nil {
				t.Fatalf("clear preferred toy: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&owner).Association("PreferredToy").Count(), 0, "named preferred clear")
			officialAssertAssociationCount(t, db.Model(&owner).Association("OtherToy").Count(), 1, "named other remains")
		})

		t.Run("TestManyToManyWithCustomizedForeignKeys", func(t *testing.T) {
			blog := officialAssocCompositeBlog{Locale: "zh", Subject: "shared", SharedTags: []officialAssocCompositeTag{{Locale: "zh", Value: "shared-1"}, {Locale: "zh", Value: "shared-2"}}}
			if err := db.Save(&blog).Error; err != nil {
				t.Fatalf("save shared-key blog: %v", err)
			}
			otherLocale := officialAssocCompositeBlog{ID: blog.ID, Locale: "en", Subject: "shared-en"}
			if err := db.Create(&otherLocale).Error; err != nil {
				t.Fatalf("save second-locale blog: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&blog).Association("SharedTags").Count(), 2, "shared custom key")
			officialAssertAssociationCount(t, db.Model(&otherLocale).Association("SharedTags").Count(), 2, "shared custom key second locale")
		})

		t.Run("TestManyToManyWithCustomizedForeignKeys2", func(t *testing.T) {
			blog := officialAssocCompositeBlog{Locale: "fr", Subject: "locale", LocaleTags: []officialAssocCompositeTag{{Locale: "fr", Value: "locale-1"}, {Locale: "fr", Value: "locale-2"}}}
			if err := db.Save(&blog).Error; err != nil {
				t.Fatalf("save locale-key blog: %v", err)
			}
			otherLocale := officialAssocCompositeBlog{ID: blog.ID, Locale: "de", Subject: "locale-de"}
			if err := db.Create(&otherLocale).Error; err != nil {
				t.Fatalf("save second locale-key blog: %v", err)
			}
			officialAssertAssociationCount(t, db.Model(&blog).Association("LocaleTags").Count(), 2, "locale custom key")
			officialAssertAssociationCount(t, db.Model(&otherLocale).Association("LocaleTags").Count(), 0, "locale custom key isolation")
		})
	})
}

// TestOfficialAssociationMetadataCoverage 保留 association_test.go 中六项精确关系元数据断言。
// 它们不要求真实数据库，但会遍历每个 KingbaseES 方言以发现模型解析偏差。
func TestOfficialAssociationMetadataCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()

		t.Run("TestBelongsToOverrideForeignKey1", func(t *testing.T) {
			type profile struct{ ID uint }
			type user struct {
				ID           uint
				Profile      profile `gorm:"ForeignKey:ProfileRefer"`
				ProfileRefer uint
			}
			officialAssertRelationship(t, db, &user{}, "Profile", "belongs_to", []string{"ProfileRefer"}, []string{"ID"})
		})
		t.Run("TestBelongsToOverrideForeignKey2", func(t *testing.T) {
			type profile struct{ Refer string }
			type user struct {
				ID        uint
				Profile   profile `gorm:"ForeignKey:ProfileID;AssociationForeignKey:Refer"`
				ProfileID string
			}
			officialAssertRelationship(t, db, &user{}, "Profile", "belongs_to", []string{"ProfileID"}, []string{"Refer"})
		})
		t.Run("TestHasOneOverrideForeignKey1", func(t *testing.T) {
			type profile struct{ UserRefer uint }
			type user struct {
				ID      uint
				Profile profile `gorm:"ForeignKey:UserRefer"`
			}
			officialAssertRelationship(t, db, &user{}, "Profile", "has_one", []string{"UserRefer"}, []string{"ID"})
		})
		t.Run("TestHasOneOverrideForeignKey2", func(t *testing.T) {
			type profile struct{ UserRef string }
			type user struct {
				ID      uint
				Refer   string
				Profile profile `gorm:"ForeignKey:UserRef;AssociationForeignKey:Refer"`
			}
			officialAssertRelationship(t, db, &user{}, "Profile", "has_one", []string{"UserRef"}, []string{"Refer"})
		})
		t.Run("TestHasManyOverrideForeignKey1", func(t *testing.T) {
			type profile struct{ UserRefer uint }
			type user struct {
				ID       uint
				Profiles []profile `gorm:"ForeignKey:UserRefer"`
			}
			officialAssertRelationship(t, db, &user{}, "Profiles", "has_many", []string{"UserRefer"}, []string{"ID"})
		})
		t.Run("TestHasManyOverrideForeignKey2", func(t *testing.T) {
			type profile struct{ UserRef string }
			type user struct {
				ID       uint
				Refer    string
				Profiles []profile `gorm:"ForeignKey:UserRef;AssociationForeignKey:Refer"`
			}
			officialAssertRelationship(t, db, &user{}, "Profiles", "has_many", []string{"UserRef"}, []string{"Refer"})
		})
	})
}

func officialAssertRelationship(t *testing.T, db interface {
	NewScope(interface{}) *gorm.Scope
}, model interface{}, fieldName, kind string, foreignFields, associationFields []string) {
	t.Helper()
	field, ok := db.NewScope(model).FieldByName(fieldName)
	if !ok || field.Relationship == nil {
		t.Fatalf("relationship %s was not calculated", fieldName)
	}
	if field.Relationship.Kind != kind ||
		!reflect.DeepEqual(field.Relationship.ForeignFieldNames, foreignFields) ||
		!reflect.DeepEqual(field.Relationship.AssociationForeignFieldNames, associationFields) {
		t.Fatalf("relationship %s mismatch: %+v", fieldName, field.Relationship)
	}
}
