//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"fmt"
	"testing"
	"time"

	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type mysqlNonFirstPrimaryRow struct {
	Name        string
	Note        string `gorm:"default:'READY'"`
	ID          uint   `gorm:"primary_key"`
	BeforeCalls int    `gorm:"-"`
	AfterCalls  int    `gorm:"-"`
}

func (r *mysqlNonFirstPrimaryRow) BeforeCreate() error { r.BeforeCalls++; return nil }
func (r *mysqlNonFirstPrimaryRow) AfterCreate() error  { r.AfterCalls++; return nil }

func TestKingbaseMySQLMode_NormalCreateNonFirstPrimaryKey(t *testing.T) {
	db := testutil.OpenLogged(t, kb.KbModeMySQL)
	defer testutil.Close(t, db)

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()

	schema := fmt.Sprintf("kb_create_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	const table = "kb_nonfirst_primary"
	if err := tx.Table(table).CreateTable(&mysqlNonFirstPrimaryRow{}).Error; err != nil {
		t.Fatal(err)
	}

	var ordinal int
	if err := tx.Raw("SELECT ordinal_position FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = 'id'", schema, table).Row().Scan(&ordinal); err != nil {
		t.Fatal(err)
	}
	if ordinal <= 1 {
		t.Fatalf("test fixture did not place the generated primary key after another column: ordinal=%d", ordinal)
	}

	created := mysqlNonFirstPrimaryRow{Name: "non-first"}
	result := tx.Table(table).Create(&created)
	if result.Error != nil || result.RowsAffected != 1 || created.ID == 0 {
		t.Fatalf("normal Create did not backfill the non-first primary key: id=%d rows=%d error=%v", created.ID, result.RowsAffected, result.Error)
	}
	if created.Note != "READY" || created.BeforeCalls != 1 || created.AfterCalls != 1 {
		t.Fatalf("normal Create semantics changed: note=%q before=%d after=%d", created.Note, created.BeforeCalls, created.AfterCalls)
	}

	var stored mysqlNonFirstPrimaryRow
	if err := tx.Table(table).Where("id = ?", created.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ID != created.ID || stored.Name != created.Name || stored.Note != "READY" {
		t.Fatalf("stored row mismatch: model=%+v stored=%+v", created, stored)
	}

	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.Raw("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?", schema).Row().Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("isolated schema still exists: count=%d error=%v", remaining, err)
	}
}
