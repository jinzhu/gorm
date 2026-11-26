//go:build kingbase_test
// +build kingbase_test

package regression_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

const (
	testDialectKingbaseMySQL  = "kingbase-mysql"
	testDialectKingbaseOracle = "kingbase-oracle"
)

type regressionUser struct {
	gorm.Model
	Name  string `gorm:"size:64;not null"`
	Email string `gorm:"size:128;unique_index;not null"`
	Age   int
}

type regressionParent struct {
	gorm.Model
	Name     string
	Children []regressionChild `gorm:"foreignkey:ParentID"`
}

type regressionChild struct {
	gorm.Model
	ParentID uint
	Name     string
}

func (regressionParent) TableName() string { return "kb_regression_parents" }
func (regressionChild) TableName() string  { return "kb_regression_children" }

func forEachMode(t *testing.T, fn func(*testing.T, kb.KbMode)) {
	t.Helper()
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			fn(t, mode)
		})
	}
}

func TestGORMV1CRUDQueryAndTransaction(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := testutil.Open(t, mode)
		defer db.Close()

		table := testutil.UniqueTable("kb_gorm_v1_users")
		defer db.DropTable(table)
		if err := db.Table(table).AutoMigrate(&regressionUser{}).Error; err != nil {
			t.Fatalf("automigrate: %v", err)
		}

		user := regressionUser{Name: "alice", Email: testutil.UniqueTable("alice"), Age: 21}
		if err := db.Table(table).Create(&user).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
		if user.ID == 0 {
			t.Fatal("create did not populate the primary key")
		}

		var loaded regressionUser
		if err := db.Table(table).Where("name = ?", "alice").First(&loaded).Error; err != nil {
			t.Fatalf("first: %v", err)
		}
		if loaded.Email != user.Email {
			t.Fatalf("loaded email = %q, want %q", loaded.Email, user.Email)
		}

		if err := db.Table(table).Where("id = ?", user.ID).Update("age", 22).Error; err != nil {
			t.Fatalf("update: %v", err)
		}
		var count int
		if err := db.Table(table).Where("age >= ?", 20).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("count = %d, err = %v", count, err)
		}

		tx := db.Begin()
		if tx.Error != nil {
			t.Fatalf("begin: %v", tx.Error)
		}
		rollbackUser := regressionUser{Name: "rollback", Email: testutil.UniqueTable("rollback"), Age: 30}
		if err := tx.Table(table).Create(&rollbackUser).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("transaction create: %v", err)
		}
		if err := tx.Rollback().Error; err != nil {
			t.Fatalf("rollback: %v", err)
		}
		if err := db.Table(table).Where("name = ?", "rollback").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("rolled back row count = %d, err = %v", count, err)
		}
	})
}

func TestGORMV1AssociationAndPreload(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := testutil.Open(t, mode)
		defer db.Close()
		if err := db.DropTableIfExists(&regressionChild{}, &regressionParent{}).Error; err != nil {
			t.Fatalf("clean associations before test: %v", err)
		}
		defer db.DropTableIfExists(&regressionChild{}, &regressionParent{})

		if err := db.AutoMigrate(&regressionParent{}, &regressionChild{}).Error; err != nil {
			t.Fatalf("automigrate associations: %v", err)
		}
		parent := regressionParent{Name: fmt.Sprintf("parent_%s", mode)}
		if err := db.Create(&parent).Error; err != nil {
			t.Fatalf("create parent: %v", err)
		}
		child := regressionChild{ParentID: parent.ID, Name: "child"}
		if err := db.Create(&child).Error; err != nil {
			t.Fatalf("create child: %v", err)
		}

		var loaded regressionParent
		if err := db.Preload("Children").First(&loaded, parent.ID).Error; err != nil {
			t.Fatalf("preload: %v", err)
		}
		if len(loaded.Children) != 1 || loaded.Children[0].Name != "child" {
			t.Fatalf("preloaded children = %#v", loaded.Children)
		}
		if got := db.Model(&loaded).Association("Children").Count(); got != 1 {
			t.Fatalf("association count = %d, want 1", got)
		}
	})
}

func TestGORMV1CreateCallback(t *testing.T) {
	forEachMode(t, func(t *testing.T, mode kb.KbMode) {
		db := testutil.Open(t, mode)
		defer db.Close()

		table := testutil.UniqueTable("kb_gorm_v1_callbacks")
		defer db.DropTable(table)
		if err := db.Table(table).AutoMigrate(&regressionUser{}).Error; err != nil {
			t.Fatalf("automigrate: %v", err)
		}

		var calls int32
		callbackName := testutil.UniqueTable("kingbase_regression_callback")
		db.Callback().Create().Before("gorm:create").Register(callbackName, func(*gorm.Scope) {
			atomic.AddInt32(&calls, 1)
		})
		defer db.Callback().Create().Remove(callbackName)

		if err := db.Table(table).Create(&regressionUser{Name: "callback", Email: testutil.UniqueTable("callback")}).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
		if atomic.LoadInt32(&calls) != 1 {
			t.Fatalf("callback calls = %d, want 1", calls)
		}
	})
}
