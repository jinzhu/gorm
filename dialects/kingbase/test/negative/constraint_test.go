//go:build kingbase_test
// +build kingbase_test

package negative_test

import (
	"testing"

	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type uniqueConstraintRow struct {
	ID   uint   `gorm:"primary_key"`
	Code string `gorm:"size:64;unique_index;not null"`
}

func TestKingbaseUniqueConstraintError(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db := testutil.Open(t, mode)
			defer db.Close()

			table := testutil.UniqueTable("kb_negative_unique")
			defer db.DropTable(table)
			if err := db.Table(table).AutoMigrate(&uniqueConstraintRow{}).Error; err != nil {
				t.Fatalf("automigrate: %v", err)
			}
			if err := db.Table(table).Create(&uniqueConstraintRow{Code: "same"}).Error; err != nil {
				t.Fatalf("first create: %v", err)
			}
			if err := db.Table(table).Create(&uniqueConstraintRow{Code: "same"}).Error; err == nil {
				t.Fatal("duplicate unique value should return an error")
			}
		})
	}
}
