//go:build kingbase_test
// +build kingbase_test

package nonfunctional_test

import (
	"context"
	"testing"

	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type resilienceRow struct {
	ID   uint   `gorm:"primary_key"`
	Code string `gorm:"size:64;unique_index;not null"`
}

func TestKingbaseCanceledContextReturnsError(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db := testutil.Open(t, mode)
			defer db.Close()

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := db.DB().PingContext(ctx); err == nil {
				t.Fatal("ping with a canceled context should return an error")
			}
		})
	}
}

func TestKingbaseClosedConnectionReturnsError(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db := testutil.Open(t, mode)
			if err := db.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			if err := db.DB().Ping(); err == nil {
				t.Fatal("pinging a closed connection pool should return an error")
			}
		})
	}
}

func TestKingbaseTransactionRollbackAfterConstraintError(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db := testutil.Open(t, mode)
			defer db.Close()

			table := testutil.UniqueTable("kb_resilience_rows")
			defer db.DropTable(table)
			if err := db.Table(table).AutoMigrate(&resilienceRow{}).Error; err != nil {
				t.Fatalf("automigrate: %v", err)
			}

			tx := db.Begin()
			if tx.Error != nil {
				t.Fatalf("begin: %v", tx.Error)
			}
			row := resilienceRow{Code: "duplicate"}
			if err := tx.Table(table).Create(&row).Error; err != nil {
				_ = tx.Rollback()
				t.Fatalf("first create: %v", err)
			}
			if err := tx.Table(table).Create(&resilienceRow{Code: "duplicate"}).Error; err == nil {
				_ = tx.Rollback()
				t.Fatal("duplicate create should fail")
			}
			if err := tx.Rollback().Error; err != nil {
				t.Fatalf("rollback: %v", err)
			}

			var count int
			if err := db.Table(table).Count(&count).Error; err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != 0 {
				t.Fatalf("rollback left %d rows, want 0", count)
			}
		})
	}
}
