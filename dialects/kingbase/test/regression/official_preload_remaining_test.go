//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"sync/atomic"
	"testing"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type officialPreloadProfile struct {
	ID     uint `gorm:"primary_key"`
	NodeID uint
	Value  string
}

func (officialPreloadProfile) TableName() string { return "kb_official_preload_profiles" }

type officialPreloadMetadata struct {
	ID     uint `gorm:"primary_key"`
	ItemID uint
	Value  string
}

func (officialPreloadMetadata) TableName() string { return "kb_official_preload_metadata" }

type officialPreloadItem struct {
	ID       uint `gorm:"primary_key"`
	NodeID   uint
	Value    string
	Metadata *officialPreloadMetadata `gorm:"foreignkey:ItemID"`
}

func (officialPreloadItem) TableName() string { return "kb_official_preload_items" }

type officialPreloadLabel struct {
	ID    uint `gorm:"primary_key"`
	Value string
}

func (officialPreloadLabel) TableName() string { return "kb_official_preload_labels" }

type officialPreloadNode struct {
	ID      uint `gorm:"primary_key"`
	RootID  uint
	Value   string
	Profile *officialPreloadProfile `gorm:"foreignkey:NodeID"`
	Items   []*officialPreloadItem  `gorm:"foreignkey:NodeID"`
	Labels  []*officialPreloadLabel `gorm:"many2many:kb_official_preload_node_labels;"`
}

func (officialPreloadNode) TableName() string { return "kb_official_preload_nodes" }

type officialPreloadDetail struct {
	ID     uint `gorm:"primary_key"`
	RootID uint
	Value  string
}

func (officialPreloadDetail) TableName() string { return "kb_official_preload_details" }

type officialPreloadGroup struct {
	ID     uint `gorm:"primary_key"`
	Name   string
	Labels []*officialPreloadLabel `gorm:"many2many:kb_official_preload_group_labels;"`
}

func (officialPreloadGroup) TableName() string { return "kb_official_preload_groups" }

type officialPreloadRoot struct {
	ID     uint `gorm:"primary_key"`
	Name   string
	Detail *officialPreloadDetail  `gorm:"foreignkey:RootID"`
	Nodes  []*officialPreloadNode  `gorm:"foreignkey:RootID"`
	Groups []*officialPreloadGroup `gorm:"many2many:kb_official_preload_root_groups;"`
}

func (officialPreloadRoot) TableName() string { return "kb_official_preload_roots" }

type officialPreloadCompositeLevel1 struct {
	ID           uint   `gorm:"primary_key"`
	LanguageCode string `gorm:"primary_key;size:16"`
	Value        string
}

func (officialPreloadCompositeLevel1) TableName() string {
	return "kb_official_preload_composite_level1"
}

type officialPreloadCompositeLevel2 struct {
	ID           uint   `gorm:"primary_key"`
	LanguageCode string `gorm:"primary_key;size:16"`
	Value        string
	Level1s      []officialPreloadCompositeLevel1 `gorm:"many2many:kb_official_preload_composite_levels;"`
}

func (officialPreloadCompositeLevel2) TableName() string {
	return "kb_official_preload_composite_level2"
}

func officialPreloadTables() []interface{} {
	return []interface{}{
		"kb_official_preload_node_labels", "kb_official_preload_group_labels", "kb_official_preload_root_groups",
		"kb_official_preload_composite_levels",
		&officialPreloadMetadata{}, &officialPreloadItem{}, &officialPreloadProfile{}, &officialPreloadNode{},
		&officialPreloadDetail{}, &officialPreloadGroup{}, &officialPreloadLabel{}, &officialPreloadRoot{},
		&officialPreloadCompositeLevel2{}, &officialPreloadCompositeLevel1{},
	}
}

func seedOfficialPreloadGraph(t *testing.T, db *gorm.DB, mode kb.KbMode) (officialPreloadRoot, officialPreloadRoot) {
	t.Helper()
	root := officialPreloadRoot{Name: "preload-root-" + string(mode)}
	if err := db.Create(&root).Error; err != nil {
		t.Fatalf("create preload root: %v", err)
	}
	detail := officialPreloadDetail{RootID: root.ID, Value: "detail"}
	if err := db.Create(&detail).Error; err != nil {
		t.Fatalf("create preload detail: %v", err)
	}
	nodes := []*officialPreloadNode{{RootID: root.ID, Value: "node-1"}, {RootID: root.ID, Value: "node-2"}}
	for _, node := range nodes {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("create preload node: %v", err)
		}
	}
	profiles := []*officialPreloadProfile{{NodeID: nodes[0].ID, Value: "profile-1"}, {NodeID: nodes[1].ID, Value: "profile-2"}}
	for _, profile := range profiles {
		if err := db.Create(profile).Error; err != nil {
			t.Fatalf("create preload profile: %v", err)
		}
	}
	items := []*officialPreloadItem{{NodeID: nodes[0].ID, Value: "item-1"}, {NodeID: nodes[0].ID, Value: "item-2"}, {NodeID: nodes[1].ID, Value: "item-3"}}
	for _, item := range items {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create preload item: %v", err)
		}
	}
	if err := db.Create(&officialPreloadMetadata{ItemID: items[0].ID, Value: "metadata-1"}).Error; err != nil {
		t.Fatalf("create preload metadata: %v", err)
	}
	labels := []*officialPreloadLabel{{Value: "label-1"}, {Value: "label-2"}}
	for _, label := range labels {
		if err := db.Create(label).Error; err != nil {
			t.Fatalf("create preload label: %v", err)
		}
	}
	if err := db.Model(nodes[0]).Association("Labels").Append(labels).Error; err != nil {
		t.Fatalf("associate node labels: %v", err)
	}
	if err := db.Model(nodes[1]).Association("Labels").Append(labels[1]).Error; err != nil {
		t.Fatalf("associate second node label: %v", err)
	}
	groups := []*officialPreloadGroup{{Name: "group-1"}, {Name: "group-2"}}
	for _, group := range groups {
		if err := db.Create(group).Error; err != nil {
			t.Fatalf("create preload group: %v", err)
		}
	}
	if err := db.Model(groups[0]).Association("Labels").Append(labels).Error; err != nil {
		t.Fatalf("associate group labels: %v", err)
	}
	if err := db.Model(&root).Association("Groups").Append(groups).Error; err != nil {
		t.Fatalf("associate root groups: %v", err)
	}

	empty := officialPreloadRoot{Name: "preload-empty-" + string(mode)}
	if err := db.Create(&empty).Error; err != nil {
		t.Fatalf("create empty preload root: %v", err)
	}
	if err := db.Create(&officialPreloadNode{RootID: empty.ID, Value: "empty-node"}).Error; err != nil {
		t.Fatalf("create empty preload node: %v", err)
	}
	return root, empty
}

func officialAssertPreloadGraph(t *testing.T, root officialPreloadRoot) {
	t.Helper()
	if root.Detail == nil || root.Detail.Value != "detail" {
		t.Fatalf("root detail was not preloaded: %#v", root.Detail)
	}
	if len(root.Nodes) != 2 {
		t.Fatalf("root nodes=%d, want=2", len(root.Nodes))
	}
}

// TestOfficialPreloadRemainingCoverage 将每个剩余上游预加载场景保留为独立命名子测试，
// 使后续测试文档可报告完整预加载类别，而不是一个汇总检查。
func TestOfficialPreloadRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		tables := officialPreloadTables()
		dropOfficialTables(t, db, tables...)
		defer db.DropTableIfExists(tables...)
		if err := db.AutoMigrate(
			&officialPreloadRoot{}, &officialPreloadDetail{}, &officialPreloadNode{}, &officialPreloadProfile{},
			&officialPreloadItem{}, &officialPreloadMetadata{}, &officialPreloadLabel{}, &officialPreloadGroup{},
			&officialPreloadCompositeLevel1{}, &officialPreloadCompositeLevel2{},
		).Error; err != nil {
			t.Fatalf("automigrate preload graph: %v", err)
		}
		root, empty := seedOfficialPreloadGraph(t, db, mode)

		t.Run("TestPreload", func(t *testing.T) {
			var loaded officialPreloadRoot
			if err := db.Preload("Detail").Preload("Nodes").Preload("Groups").First(&loaded, root.ID).Error; err != nil {
				t.Fatalf("basic preload: %v", err)
			}
			officialAssertPreloadGraph(t, loaded)
			if len(loaded.Groups) != 2 {
				t.Fatalf("preloaded groups=%d, want=2", len(loaded.Groups))
			}
			var pointerRows []*officialPreloadRoot
			if err := db.Preload("Nodes", "value = ?", "node-1").Where("id = ?", root.ID).Find(&pointerRows).Error; err != nil {
				t.Fatalf("conditional preload into pointer slice: %v", err)
			}
			if len(pointerRows) != 1 || len(pointerRows[0].Nodes) != 1 {
				t.Fatalf("conditional preload result: %#v", pointerRows)
			}
		})

		t.Run("TestAutoPreload", func(t *testing.T) {
			var loaded officialPreloadRoot
			if err := db.Set("gorm:auto_preload", true).First(&loaded, root.ID).Error; err != nil {
				t.Fatalf("auto preload: %v", err)
			}
			if loaded.Detail == nil || len(loaded.Nodes) != 2 || len(loaded.Groups) != 2 {
				t.Fatalf("auto preload result: %#v", loaded)
			}
		})

		t.Run("TestAutoPreloadFalseDoesntPreload", func(t *testing.T) {
			var loaded officialPreloadRoot
			if err := db.Set("gorm:auto_preload", false).First(&loaded, root.ID).Error; err != nil {
				t.Fatalf("disabled auto preload: %v", err)
			}
			if loaded.Detail != nil || loaded.Nodes != nil || loaded.Groups != nil {
				t.Fatalf("disabled auto preload populated associations: %#v", loaded)
			}
		})

		nestedCases := []struct {
			name     string
			preloads []string
			verify   func(*testing.T, officialPreloadRoot)
		}{
			{"TestNestedPreload1", []string{"Detail"}, func(t *testing.T, got officialPreloadRoot) {
				if got.Detail == nil {
					t.Fatal("Detail was not preloaded")
				}
			}},
			{"TestNestedPreload2", []string{"Nodes"}, func(t *testing.T, got officialPreloadRoot) {
				if len(got.Nodes) != 2 {
					t.Fatalf("Nodes=%d", len(got.Nodes))
				}
			}},
			{"TestNestedPreload3", []string{"Nodes.Profile"}, func(t *testing.T, got officialPreloadRoot) {
				if len(got.Nodes) != 2 || got.Nodes[0].Profile == nil {
					t.Fatalf("nested profile missing: %#v", got.Nodes)
				}
			}},
			{"TestNestedPreload4", []string{"Nodes.Items"}, func(t *testing.T, got officialPreloadRoot) {
				if len(got.Nodes) != 2 || len(got.Nodes[0].Items) == 0 {
					t.Fatalf("nested items missing: %#v", got.Nodes)
				}
			}},
			{"TestNestedPreload5", []string{"Detail", "Nodes.Profile"}, func(t *testing.T, got officialPreloadRoot) {
				if got.Detail == nil || got.Nodes[0].Profile == nil {
					t.Fatal("two-branch preload missing")
				}
			}},
			{"TestNestedPreload6", []string{"Nodes.Items.Metadata"}, func(t *testing.T, got officialPreloadRoot) {
				if got.Nodes[0].Items[0].Metadata == nil {
					t.Fatal("third-level pointer missing")
				}
			}},
			{"TestNestedPreload7", []string{"Nodes.Labels"}, func(t *testing.T, got officialPreloadRoot) {
				if len(got.Nodes[0].Labels) != 2 {
					t.Fatalf("node labels=%d", len(got.Nodes[0].Labels))
				}
			}},
			{"TestNestedPreload8", []string{"Groups.Labels"}, func(t *testing.T, got officialPreloadRoot) {
				if len(got.Groups) != 2 || len(got.Groups[0].Labels) == 0 {
					t.Fatalf("group labels missing: %#v", got.Groups)
				}
			}},
			{"TestNestedPreload9", []string{"Detail", "Nodes.Profile", "Nodes.Items.Metadata", "Groups.Labels"}, func(t *testing.T, got officialPreloadRoot) {
				if got.Detail == nil || got.Nodes[0].Profile == nil || len(got.Groups[0].Labels) == 0 {
					t.Fatal("multi-branch nested preload missing")
				}
			}},
			{"TestNestedPreload10", []string{"Nodes.Items.Metadata"}, func(t *testing.T, got officialPreloadRoot) {
				if len(got.Nodes) != 2 || got.Nodes[0].Items[0].Metadata == nil {
					t.Fatal("nested pointer slice preload missing")
				}
			}},
			{"TestNestedPreload11", []string{"Nodes.Labels"}, func(t *testing.T, got officialPreloadRoot) {
				if got.Nodes == nil || got.Nodes[0].Labels == nil {
					t.Fatal("nested many-to-many empty-slice contract missing")
				}
			}},
			{"TestNestedPreload12", []string{"Nodes", "Nodes.Profile"}, func(t *testing.T, got officialPreloadRoot) {
				if got.Nodes[0].Profile == nil {
					t.Fatal("prefixed nested preload missing")
				}
			}},
		}
		for _, tc := range nestedCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				query := db.New()
				for _, preload := range tc.preloads {
					query = query.Preload(preload)
				}
				var loaded officialPreloadRoot
				if err := query.First(&loaded, root.ID).Error; err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				tc.verify(t, loaded)
			})
		}

		t.Run("TestManyToManyPreloadForNestedPointer", func(t *testing.T) {
			var loaded officialPreloadRoot
			if err := db.Preload("Nodes.Labels").First(&loaded, root.ID).Error; err != nil {
				t.Fatalf("nested pointer many-to-many: %v", err)
			}
			if len(loaded.Nodes) != 2 || len(loaded.Nodes[0].Labels) != 2 {
				t.Fatalf("nested pointer many-to-many result: %#v", loaded.Nodes)
			}
		})

		nestedM2MNames := []string{
			"TestNestedManyToManyPreload", "TestNestedManyToManyPreload2", "TestNestedManyToManyPreload3",
			"TestNestedManyToManyPreload3ForStruct", "TestNestedManyToManyPreload4",
		}
		for _, name := range nestedM2MNames {
			name := name
			t.Run(name, func(t *testing.T) {
				var loaded officialPreloadRoot
				if err := db.Preload("Groups.Labels").First(&loaded, root.ID).Error; err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if len(loaded.Groups) != 2 || len(loaded.Groups[0].Labels) != 2 {
					t.Fatalf("%s result: %#v", name, loaded.Groups)
				}
			})
		}

		t.Run("TestManyToManyPreloadForPointer", func(t *testing.T) {
			var loaded []*officialPreloadRoot
			if err := db.Preload("Groups").Where("id = ?", root.ID).Find(&loaded).Error; err != nil {
				t.Fatalf("pointer many-to-many preload: %v", err)
			}
			if len(loaded) != 1 || len(loaded[0].Groups) != 2 {
				t.Fatalf("pointer many-to-many result: %#v", loaded)
			}
		})

		t.Run("TestNilPointerSlice", func(t *testing.T) {
			var loaded []officialPreloadRoot
			if err := db.Preload("Nodes.Profile").Where("id IN (?)", []uint{root.ID, empty.ID}).Order("id").Find(&loaded).Error; err != nil {
				t.Fatalf("nil pointer slice preload: %v", err)
			}
			if len(loaded) != 2 || len(loaded[1].Nodes) != 1 || loaded[1].Nodes[0].Profile != nil {
				t.Fatalf("nil pointer slice result: %#v", loaded)
			}
		})

		t.Run("TestNilPointerSlice2", func(t *testing.T) {
			var loaded officialPreloadRoot
			if err := db.Preload("Nodes.Items.Metadata").First(&loaded, empty.ID).Error; err != nil {
				t.Fatalf("deep nil pointer preload: %v", err)
			}
			if len(loaded.Nodes) != 1 || loaded.Nodes[0].Items == nil || len(loaded.Nodes[0].Items) != 0 {
				t.Fatalf("deep nil pointer result: %#v", loaded.Nodes)
			}
		})

		t.Run("TestPrefixedPreloadDuplication", func(t *testing.T) {
			var loaded []officialPreloadRoot
			if err := db.Preload("Nodes.Items.Metadata").Where("id = ?", root.ID).Find(&loaded).Error; err != nil {
				t.Fatalf("prefixed preload: %v", err)
			}
			if len(loaded) != 1 || len(loaded[0].Nodes) != 2 || len(loaded[0].Nodes[0].Items) != 2 {
				t.Fatalf("prefixed preload duplicated rows: %#v", loaded)
			}
		})

		t.Run("TestPreloadManyToManyCallbacks", func(t *testing.T) {
			var called int32
			callbackName := testutil.UniqueTable("official_preload_callback")
			db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.Scope) {
				atomic.AddInt32(&called, 1)
			})
			defer db.Callback().Query().Remove(callbackName)
			var loaded officialPreloadRoot
			if err := db.Preload("Groups").First(&loaded, root.ID).Error; err != nil {
				t.Fatalf("many-to-many preload callback query: %v", err)
			}
			if got := atomic.LoadInt32(&called); got < 2 {
				t.Fatalf("preload callbacks=%d, want at least 2", got)
			}
		})

		t.Run("TestManyToManyPreloadWithMultiPrimaryKeys", func(t *testing.T) {
			bob := officialPreloadCompositeLevel2{Value: "Bob", LanguageCode: "ru", Level1s: []officialPreloadCompositeLevel1{
				{Value: "ru", LanguageCode: "ru"},
				{Value: "en", LanguageCode: "en"},
			}}
			tom := officialPreloadCompositeLevel2{Value: "Tom", LanguageCode: "zh", Level1s: []officialPreloadCompositeLevel1{
				{Value: "zh", LanguageCode: "zh"},
				{Value: "de", LanguageCode: "de"},
			}}
			if err := db.Save(&bob).Error; err != nil {
				t.Fatalf("save Bob composite graph: %v", err)
			}
			if err := db.Save(&tom).Error; err != nil {
				t.Fatalf("save Tom composite graph: %v", err)
			}

			var gotBob officialPreloadCompositeLevel2
			if err := db.Preload("Level1s").Where("value = ?", "Bob").First(&gotBob).Error; err != nil {
				t.Fatalf("preload Bob composite graph: %v", err)
			}
			if gotBob.ID != bob.ID || gotBob.LanguageCode != "ru" || len(gotBob.Level1s) != 2 {
				t.Fatalf("Bob composite preload: %#v", gotBob)
			}

			var both []officialPreloadCompositeLevel2
			if err := db.Preload("Level1s").Where("value IN (?)", []string{"Bob", "Tom"}).Order("value").Find(&both).Error; err != nil {
				t.Fatalf("preload composite slice: %v", err)
			}
			if len(both) != 2 || len(both[0].Level1s) != 2 || len(both[1].Level1s) != 2 {
				t.Fatalf("composite slice preload: %#v", both)
			}

			var filtered []officialPreloadCompositeLevel2
			if err := db.Preload("Level1s", "value IN (?)", []string{"ru", "zh"}).Where("value IN (?)", []string{"Bob", "Tom"}).Order("value").Find(&filtered).Error; err != nil {
				t.Fatalf("filtered composite preload: %v", err)
			}
			if len(filtered) != 2 || len(filtered[0].Level1s) != 1 || len(filtered[1].Level1s) != 1 {
				t.Fatalf("filtered composite preload: %#v", filtered)
			}

			var missing []officialPreloadCompositeLevel2
			if err := db.Preload("Level1s").Where("value = ?", "missing").Find(&missing).Error; err != nil || len(missing) != 0 {
				t.Fatalf("missing composite preload: len=%d err=%v", len(missing), err)
			}
		})
	})
}
