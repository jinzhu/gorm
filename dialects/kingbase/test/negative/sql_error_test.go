//go:build kingbase_test
// +build kingbase_test

package negative_test

import (
	"testing"

	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type sqlErrorRow struct {
	ID   uint `gorm:"primary_key"`
	Name string
}

func TestKingbaseInvalidTableAndColumnErrors(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db := testutil.Open(t, mode)
			defer db.Close()

			missingTable := testutil.UniqueTable("kb_missing_table")
			var rows []sqlErrorRow
			if err := db.Table(missingTable).Find(&rows).Error; err == nil {
				t.Fatal("querying a missing table should return an error")
			}

			table := testutil.UniqueTable("kb_negative_sql")
			defer db.DropTable(table)
			if err := db.Table(table).AutoMigrate(&sqlErrorRow{}).Error; err != nil {
				t.Fatalf("automigrate: %v", err)
			}
			if err := db.Table(table).Where("missing_column = ?", 1).Find(&rows).Error; err == nil {
				t.Fatal("querying a missing column should return an error")
			}
		})
	}
}
