//go:build kingbase_test
// +build kingbase_test

package nonfunctional_test

import (
	"fmt"
	"sync"
	"testing"

	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

type concurrentRow struct {
	ID    uint `gorm:"primary_key"`
	Value string
}

func TestKingbaseConcurrentCRUD(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db := testutil.Open(t, mode)
			defer db.Close()

			table := testutil.UniqueTable("kb_concurrent_rows")
			defer db.DropTable(table)
			if err := db.Table(table).AutoMigrate(&concurrentRow{}).Error; err != nil {
				t.Fatalf("automigrate: %v", err)
			}

			const workers = 4
			const rowsPerWorker = 10
			errors := make(chan error, workers)
			var wg sync.WaitGroup
			for worker := 0; worker < workers; worker++ {
				worker := worker
				wg.Add(1)
				go func() {
					defer wg.Done()
					for row := 0; row < rowsPerWorker; row++ {
						value := fmt.Sprintf("worker-%d-row-%d", worker, row)
						if err := db.New().Table(table).Create(&concurrentRow{Value: value}).Error; err != nil {
							errors <- err
							return
						}
					}
				}()
			}
			wg.Wait()
			close(errors)
			for err := range errors {
				t.Fatal(err)
			}

			var count int
			if err := db.Table(table).Count(&count).Error; err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != workers*rowsPerWorker {
				t.Fatalf("row count = %d, want %d", count, workers*rowsPerWorker)
			}
		})
	}
}
