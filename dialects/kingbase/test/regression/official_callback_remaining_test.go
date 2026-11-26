//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type officialCallbackRemainingRow struct {
	ID                uint `gorm:"primary_key"`
	Code              string
	Price             int
	BeforeSaveCalls   int `gorm:"-"`
	BeforeCreateCalls int `gorm:"-"`
	AfterCreateCalls  int `gorm:"-"`
	AfterSaveCalls    int `gorm:"-"`
	BeforeUpdateCalls int `gorm:"-"`
	AfterUpdateCalls  int `gorm:"-"`
	BeforeDeleteCalls int `gorm:"-"`
	AfterDeleteCalls  int `gorm:"-"`
	AfterFindCalls    int `gorm:"-"`
}

func (officialCallbackRemainingRow) TableName() string { return "kb_official_callback_rows" }

func (r *officialCallbackRemainingRow) BeforeSave() error {
	r.BeforeSaveCalls++
	if r.Code == "before-save-error" {
		return errors.New("before save error")
	}
	return nil
}

func (r *officialCallbackRemainingRow) BeforeCreate() error {
	r.BeforeCreateCalls++
	if r.Code == "before-create-error" {
		return errors.New("before create error")
	}
	return nil
}

func (r *officialCallbackRemainingRow) AfterCreate() { r.AfterCreateCalls++ }

func (r *officialCallbackRemainingRow) AfterSave() error {
	r.AfterSaveCalls++
	if r.Code == "after-save-error" {
		return errors.New("after save error")
	}
	return nil
}

func (r *officialCallbackRemainingRow) BeforeUpdate() error {
	r.BeforeUpdateCalls++
	if r.Code == "before-update-error" {
		return errors.New("before update error")
	}
	return nil
}

func (r *officialCallbackRemainingRow) AfterUpdate() { r.AfterUpdateCalls++ }

func (r *officialCallbackRemainingRow) BeforeDelete() error {
	r.BeforeDeleteCalls++
	if r.Code == "before-delete-error" {
		return errors.New("before delete error")
	}
	return nil
}

func (r *officialCallbackRemainingRow) AfterDelete() error {
	r.AfterDeleteCalls++
	if r.Code == "after-delete-error" {
		return errors.New("after delete error")
	}
	return nil
}

func (r *officialCallbackRemainingRow) AfterFind() { r.AfterFindCalls++ }

// TestOfficialCallbackRemainingCoverage 将回调注册机制和模型 Hook 执行归入同一可追踪类别。
func TestOfficialCallbackRemainingCoverage(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := openOfficialCoverageDB(t, mode)
		defer db.Close()
		dropOfficialTables(t, db, &officialCallbackRemainingRow{})
		defer db.DropTableIfExists(&officialCallbackRemainingRow{})
		if err := db.AutoMigrate(&officialCallbackRemainingRow{}).Error; err != nil {
			t.Fatalf("automigrate callback fixture: %v", err)
		}

		t.Run("TestRegisterCallback", func(t *testing.T) {
			name := testutil.UniqueTable("official_register_create")
			called := 0
			db.Callback().Create().Register(name, func(*gorm.Scope) { called++ })
			defer db.Callback().Create().Remove(name)
			if db.Callback().Create().Get(name) == nil {
				t.Fatal("registered callback cannot be retrieved")
			}
			if err := db.Create(&officialCallbackRemainingRow{Code: "register-" + string(mode)}).Error; err != nil {
				t.Fatalf("execute registered callback: %v", err)
			}
			if called != 1 {
				t.Fatalf("registered callback calls=%d, want=1", called)
			}
		})

		t.Run("TestRegisterCallbackWithComplexOrder", func(t *testing.T) {
			var events []string
			first := testutil.UniqueTable("official_complex_first")
			middle := testutil.UniqueTable("official_complex_middle")
			last := testutil.UniqueTable("official_complex_last")
			// 与 GORM V1 自身的复杂排序覆盖保持一致：先注册同时携带两个约束的回调，
			// 再注册它的两个锚点。
			db.Callback().Query().After(first).Before(last).Register(middle, func(*gorm.Scope) { events = append(events, "middle") })
			db.Callback().Query().Register(first, func(*gorm.Scope) { events = append(events, "first") })
			db.Callback().Query().Register(last, func(*gorm.Scope) { events = append(events, "last") })
			defer db.Callback().Query().Remove(first)
			defer db.Callback().Query().Remove(middle)
			defer db.Callback().Query().Remove(last)
			if err := db.Find(&[]officialCallbackRemainingRow{}).Error; err != nil {
				t.Fatalf("execute complex callback order: %v", err)
			}
			if !reflect.DeepEqual(events, []string{"first", "middle", "last"}) {
				t.Fatalf("complex callback order=%v", events)
			}
		})

		t.Run("TestReplaceCallback", func(t *testing.T) {
			name := testutil.UniqueTable("official_replace")
			value := "initial"
			db.Callback().Create().Before("gorm:create").Register(name, func(*gorm.Scope) { value = "original" })
			db.Callback().Create().Before("gorm:create").Replace(name, func(*gorm.Scope) { value = "replacement" })
			defer db.Callback().Create().Remove(name)
			if err := db.Create(&officialCallbackRemainingRow{Code: "replace-" + string(mode)}).Error; err != nil {
				t.Fatalf("execute replacement callback: %v", err)
			}
			if value != "replacement" {
				t.Fatalf("replacement callback value=%q", value)
			}
		})

		t.Run("TestRemoveCallback", func(t *testing.T) {
			name := testutil.UniqueTable("official_remove")
			called := false
			db.Callback().Create().Register(name, func(*gorm.Scope) { called = true })
			db.Callback().Create().Remove(name)
			if db.Callback().Create().Get(name) != nil {
				t.Fatal("removed callback can still be retrieved")
			}
			if err := db.Create(&officialCallbackRemainingRow{Code: "remove-" + string(mode)}).Error; err != nil {
				t.Fatalf("create after callback removal: %v", err)
			}
			if called {
				t.Fatal("removed callback was executed")
			}
		})

		t.Run("TestGetCallback", func(t *testing.T) {
			name := testutil.UniqueTable("official_get")
			scope := db.NewScope(nil)
			if db.Callback().Create().Get(name) != nil {
				t.Fatal("unknown callback should be nil")
			}
			db.Callback().Create().Register(name, func(scope *gorm.Scope) { scope.Set("official_callback_value", 1) })
			callback := db.Callback().Create().Get(name)
			if callback == nil {
				t.Fatal("registered callback is nil")
			}
			callback(scope)
			if value, ok := scope.Get("official_callback_value"); !ok || value != 1 {
				t.Fatalf("retrieved callback result=%v ok=%v", value, ok)
			}
			db.Callback().Create().Replace(name, func(scope *gorm.Scope) { scope.Set("official_callback_value", 2) })
			db.Callback().Create().Get(name)(scope)
			if value, _ := scope.Get("official_callback_value"); value != 2 {
				t.Fatalf("retrieved replacement callback result=%v", value)
			}
			db.Callback().Create().Remove(name)
		})

		t.Run("TestUseDefaultCallback", func(t *testing.T) {
			name := testutil.UniqueTable("official_default_callback")
			gorm.DefaultCallback.Update().Register(name, func(scope *gorm.Scope) { scope.Set("official_default_callback_value", 1) })
			defer gorm.DefaultCallback.Update().Remove(name)
			gorm.DefaultCallback.Update().Replace(name, func(scope *gorm.Scope) { scope.Set("official_default_callback_value", 2) })
			scope := db.NewScope(nil)
			callback := gorm.DefaultCallback.Update().Get(name)
			if callback == nil {
				t.Fatal("default callback is nil")
			}
			callback(scope)
			if value, ok := scope.Get("official_default_callback_value"); !ok || value != 2 {
				t.Fatalf("default replacement callback value=%v ok=%v", value, ok)
			}
		})

		t.Run("TestRunCallbacks", func(t *testing.T) {
			row := officialCallbackRemainingRow{Code: "callback-lifecycle-" + string(mode), Price: 10}
			if err := db.Save(&row).Error; err != nil {
				t.Fatalf("create lifecycle row: %v", err)
			}
			if row.BeforeSaveCalls != 1 || row.BeforeCreateCalls != 1 || row.AfterCreateCalls != 1 || row.AfterSaveCalls != 1 {
				t.Fatalf("create hook counts: %#v", row)
			}
			row.Price = 20
			if err := db.Save(&row).Error; err != nil {
				t.Fatalf("update lifecycle row: %v", err)
			}
			if row.BeforeUpdateCalls != 1 || row.AfterUpdateCalls != 1 || row.BeforeSaveCalls != 2 || row.AfterSaveCalls != 2 {
				t.Fatalf("update hook counts: %#v", row)
			}
			var loaded officialCallbackRemainingRow
			if err := db.First(&loaded, row.ID).Error; err != nil {
				t.Fatalf("query lifecycle row: %v", err)
			}
			if loaded.AfterFindCalls != 1 {
				t.Fatalf("AfterFind calls=%d, want=1", loaded.AfterFindCalls)
			}
			if err := db.Delete(&row).Error; err != nil {
				t.Fatalf("delete lifecycle row: %v", err)
			}
			if row.BeforeDeleteCalls != 1 || row.AfterDeleteCalls != 1 {
				t.Fatalf("delete hook counts: %#v", row)
			}
		})

		t.Run("TestCallbacksWithErrors", func(t *testing.T) {
			cases := []struct {
				code string
				op   func(*officialCallbackRemainingRow) error
			}{
				{"before-create-error", func(row *officialCallbackRemainingRow) error { return db.Create(row).Error }},
				{"before-save-error", func(row *officialCallbackRemainingRow) error { return db.Create(row).Error }},
				{"after-save-error", func(row *officialCallbackRemainingRow) error { return db.Create(row).Error }},
			}
			for _, tc := range cases {
				row := officialCallbackRemainingRow{Code: tc.code, Price: 1}
				if err := tc.op(&row); err == nil {
					t.Fatalf("%s callback should fail", tc.code)
				}
				var count int
				if err := db.Model(&officialCallbackRemainingRow{}).Where("code = ?", tc.code).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("%s rollback count=%d err=%v", tc.code, count, err)
				}
			}

			update := officialCallbackRemainingRow{Code: "update-baseline", Price: 1}
			if err := db.Create(&update).Error; err != nil {
				t.Fatalf("create update callback baseline: %v", err)
			}
			update.Code = "before-update-error"
			if err := db.Save(&update).Error; err == nil {
				t.Fatal("before-update callback should fail")
			}
			var persisted officialCallbackRemainingRow
			if err := db.First(&persisted, update.ID).Error; err != nil || persisted.Code != "update-baseline" {
				t.Fatalf("failed update was persisted: %#v err=%v", persisted, err)
			}

			deletion := officialCallbackRemainingRow{Code: "before-delete-error", Price: 1}
			if err := db.Create(&deletion).Error; err != nil {
				t.Fatalf("create delete callback baseline: %v", err)
			}
			if err := db.Delete(&deletion).Error; err == nil {
				t.Fatal("before-delete callback should fail")
			}
			if err := db.First(&officialCallbackRemainingRow{}, deletion.ID).Error; err != nil {
				t.Fatalf("failed delete removed the row: %v", err)
			}
		})
	})
}
