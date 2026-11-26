//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"testing"

	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type oracleMigrationIndex struct {
	ID   int64  `gorm:"primary_key"`
	Name string `gorm:"column:Display-Name;size:64;comment:'金仓 O''Reilly'"`
}

type oracleMigrationExtension struct {
	ID   int64  `gorm:"primary_key"`
	Name string `gorm:"column:Display-Name;size:64;comment:'金仓 O''Reilly'"`
	Note string `gorm:"size:64;comment:新增字段"`
}

func TestKingbaseOracleMode_MigrationExtensions(t *testing.T) {
	db := openKBOracle(t)
	defer finishLoggedTest(t, db)

	runLoggedTest(t, db, "01_Comments_New_And_Added_Columns", func(t *testing.T) {
		table := testutil.UniqueTable("kb_ora_mig_comment")
		defer oracleCleanupTable(t, db, table)
		if err := kb.NewOracleMigrator(db.Table(table)).AutoMigrate(&oracleMigrationIndex{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := kb.NewOracleMigrator(db.Table(table)).AutoMigrate(&oracleMigrationExtension{}).Error; err != nil {
			t.Fatal(err)
		}
		for column, want := range map[string]string{"Display-Name": "金仓 O'Reilly", "note": "新增字段"} {
			var got string
			if err := db.Raw("SELECT col_description(a.attrelid,a.attnum) FROM pg_attribute a WHERE a.attrelid=to_regclass(?) AND a.attname=?", table, column).Row().Scan(&got); err != nil || got != want {
				t.Fatalf("column %s comment=%q want=%q error=%v", column, got, want, err)
			}
		}
		if err := kb.NewOracleMigrator(db.Table(table)).AutoMigrate(&oracleMigrationExtension{}).Error; err != nil {
			t.Fatal("repeat migration:", err)
		}
	})

}
