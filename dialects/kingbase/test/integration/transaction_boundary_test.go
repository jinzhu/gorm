//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	kb "github.com/jinzhu/gorm/dialects/kingbase"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
)

// 事务边界测试有意放在 integration/ 下，用于验证 database/sql 事务和 KingbaseES 服务器行为，
// 而不只是方言生成的 SQL 字符串。
type transactionBoundaryRow struct {
	ID    uint   `gorm:"primary_key"`
	Name  string `gorm:"size:128;unique_index;not null"`
	Value int    `gorm:"not null"`
}

// 仅供 DDL 回滚测试使用：在已开启事务中向现有表增加一列。
type transactionBoundaryMigrationRow struct {
	ID     uint `gorm:"primary_key"`
	Marker int
}

func forEachTransactionMode(t *testing.T, fn func(*testing.T, kb.KbMode, *gorm.DB)) {
	t.Helper()
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db := testutil.Open(t, mode)
			if db == nil {
				return
			}
			defer db.Close()
			fn(t, mode, db)
		})
	}
}

func newTransactionBoundaryTable(t *testing.T, db *gorm.DB) string {
	t.Helper()
	table := testutil.UniqueTable("kb_tx_boundary")
	if err := db.Table(table).AutoMigrate(&transactionBoundaryRow{}).Error; err != nil {
		t.Fatalf("automigrate %s: %v", table, err)
	}
	return table
}

func transactionBoundaryCount(t *testing.T, db *gorm.DB, table, name string) int {
	t.Helper()
	var count int
	if err := db.Table(table).Where("name = ?", name).Count(&count).Error; err != nil {
		t.Fatalf("count %s/%s: %v", table, name, err)
	}
	return count
}

func transactionSQL(t *testing.T, tx *gorm.DB) *sql.Tx {
	t.Helper()
	sqlTx, ok := tx.CommonDB().(*sql.Tx)
	if !ok || sqlTx == nil {
		t.Fatalf("transaction CommonDB is %T, want *sql.Tx", tx.CommonDB())
	}
	return sqlTx
}

func TestKingbaseTransactionIsolationLevels(t *testing.T) {
	forEachTransactionMode(t, func(t *testing.T, mode kb.KbMode, db *gorm.DB) {
		for _, tt := range []struct {
			name  string
			level sql.IsolationLevel
			want  string
		}{
			{name: "read-committed", level: sql.LevelReadCommitted, want: "read committed"},
			{name: "serializable", level: sql.LevelSerializable, want: "serializable"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				tx := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: tt.level})
				if tx.Error != nil {
					t.Fatalf("%s begin %s transaction: %v", mode, tt.name, tx.Error)
				}
				defer tx.Rollback()

				var actual string
				if err := transactionSQL(t, tx).QueryRow("SHOW TRANSACTION ISOLATION LEVEL").Scan(&actual); err != nil {
					t.Fatalf("show isolation level: %v", err)
				}
				actual = strings.ToLower(strings.Join(strings.Fields(actual), " "))
				if !strings.Contains(actual, tt.want) {
					t.Fatalf("isolation level = %q, want %q", actual, tt.want)
				}
				if err := tx.Commit().Error; err != nil {
					t.Fatalf("commit %s transaction: %v", tt.name, err)
				}
			})
		}
	})
}

func TestKingbaseDirtyReadIsNotVisible(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			dbWriter := testutil.Open(t, mode)
			if dbWriter == nil {
				return
			}
			defer dbWriter.Close()
			dbReader := testutil.Open(t, mode)
			if dbReader == nil {
				return
			}
			defer dbReader.Close()

			table := newTransactionBoundaryTable(t, dbWriter)
			defer dbWriter.DropTable(table)
			row := transactionBoundaryRow{Name: testutil.UniqueTable("dirty-read"), Value: 10}
			if err := dbWriter.Table(table).Create(&row).Error; err != nil {
				t.Fatalf("seed row: %v", err)
			}

			writer := dbWriter.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if writer.Error != nil {
				t.Fatalf("begin writer: %v", writer.Error)
			}
			if err := writer.Table(table).Where("id = ?", row.ID).UpdateColumn("value", 20).Error; err != nil {
				_ = writer.Rollback()
				t.Fatalf("uncommitted update: %v", err)
			}

			reader := dbReader.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if reader.Error != nil {
				_ = writer.Rollback()
				t.Fatalf("begin reader: %v", reader.Error)
			}
			resultCh := make(chan struct {
				value int
				err   error
			}, 1)
			go func() {
				var value int
				sqlReader, ok := reader.CommonDB().(*sql.Tx)
				if !ok || sqlReader == nil {
					resultCh <- struct {
						value int
						err   error
					}{err: fmt.Errorf("reader CommonDB is %T, want *sql.Tx", reader.CommonDB())}
					return
				}
				err := sqlReader.QueryRow("SELECT value FROM "+table+" WHERE id = ?", row.ID).Scan(&value)
				resultCh <- struct {
					value int
					err   error
				}{value: value, err: err}
			}()

			var result struct {
				value int
				err   error
			}
			select {
			case result = <-resultCh:
			case <-time.After(300 * time.Millisecond):
				// 等待锁同样可以接受；写事务回滚后，读事务必须看到已提交值 10。
				if err := writer.Rollback().Error; err != nil {
					t.Fatalf("rollback writer after lock wait: %v", err)
				}
				select {
				case result = <-resultCh:
				case <-time.After(2 * time.Second):
					t.Fatal("reader remained blocked after writer rollback")
				}
			}
			if result.err != nil {
				_ = reader.Rollback()
				_ = writer.Rollback()
				t.Fatalf("read while writer was uncommitted: %v", result.err)
			}
			if result.value != 10 {
				_ = reader.Rollback()
				_ = writer.Rollback()
				t.Fatalf("dirty value became visible: got %d, want 10", result.value)
			}
			_ = reader.Rollback()
			_ = writer.Rollback()
		})
	}
}

func TestKingbaseRepeatableReadPreventsNonRepeatableReads(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			dbReader := testutil.Open(t, mode)
			if dbReader == nil {
				return
			}
			defer dbReader.Close()
			dbWriter := testutil.Open(t, mode)
			if dbWriter == nil {
				return
			}
			defer dbWriter.Close()

			table := newTransactionBoundaryTable(t, dbReader)
			defer dbReader.DropTable(table)
			row := transactionBoundaryRow{Name: testutil.UniqueTable("repeatable-read"), Value: 10}
			if err := dbReader.Table(table).Create(&row).Error; err != nil {
				t.Fatalf("seed row: %v", err)
			}

			reader := dbReader.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if reader.Error != nil {
				t.Fatalf("begin repeatable-read transaction: %v", reader.Error)
			}
			readValue := func() int {
				var value int
				if err := transactionSQL(t, reader).QueryRow("SELECT value FROM "+table+" WHERE id = ?", row.ID).Scan(&value); err != nil {
					t.Fatalf("read value in repeatable-read transaction: %v", err)
				}
				return value
			}
			first := readValue()

			writer := dbWriter.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if writer.Error != nil {
				_ = reader.Rollback()
				t.Fatalf("begin writer transaction: %v", writer.Error)
			}
			if err := writer.Table(table).Where("id = ?", row.ID).UpdateColumn("value", 20).Error; err != nil {
				_ = writer.Rollback()
				_ = reader.Rollback()
				t.Fatalf("update concurrent row: %v", err)
			}
			if err := writer.Commit().Error; err != nil {
				_ = reader.Rollback()
				t.Fatalf("commit concurrent update: %v", err)
			}

			second := readValue()
			if first != 10 || second != 10 {
				_ = reader.Rollback()
				t.Fatalf("repeatable-read values = (%d,%d), want (10,10)", first, second)
			}
			if err := reader.Commit().Error; err != nil {
				t.Fatalf("commit repeatable-read transaction: %v", err)
			}
		})
	}
}

func TestKingbaseReadCommittedSeesCommittedChanges(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			dbReader := testutil.Open(t, mode)
			if dbReader == nil {
				return
			}
			defer dbReader.Close()
			dbWriter := testutil.Open(t, mode)
			if dbWriter == nil {
				return
			}
			defer dbWriter.Close()

			table := newTransactionBoundaryTable(t, dbReader)
			defer dbReader.DropTable(table)
			row := transactionBoundaryRow{Name: testutil.UniqueTable("read-committed"), Value: 10}
			if err := dbReader.Table(table).Create(&row).Error; err != nil {
				t.Fatalf("seed read-committed row: %v", err)
			}

			reader := dbReader.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if reader.Error != nil {
				t.Fatalf("begin read-committed reader: %v", reader.Error)
			}
			readValue := func(tx *gorm.DB) int {
				var value int
				if err := transactionSQL(t, tx).QueryRow("SELECT value FROM "+table+" WHERE id = ?", row.ID).Scan(&value); err != nil {
					t.Fatalf("read read-committed value: %v", err)
				}
				return value
			}
			if first := readValue(reader); first != 10 {
				_ = reader.Rollback()
				t.Fatalf("initial read-committed value=%d, want 10", first)
			}

			writer := dbWriter.Begin()
			if writer.Error != nil {
				_ = reader.Rollback()
				t.Fatalf("begin read-committed writer: %v", writer.Error)
			}
			if err := writer.Table(table).Where("id = ?", row.ID).UpdateColumn("value", 20).Error; err != nil {
				_ = writer.Rollback()
				_ = reader.Rollback()
				t.Fatalf("update read-committed row: %v", err)
			}
			if err := writer.Commit().Error; err != nil {
				_ = reader.Rollback()
				t.Fatalf("commit read-committed update: %v", err)
			}
			if second := readValue(reader); second != 20 {
				_ = reader.Rollback()
				t.Fatalf("read-committed second value=%d, want 20", second)
			}
			if err := reader.Commit().Error; err != nil {
				t.Fatalf("commit read-committed reader: %v", err)
			}
			t.Logf("%s read-committed transaction observed the committed update", mode)
		})
	}
}

func TestKingbaseRepeatableReadPreventsPhantoms(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			dbReader := testutil.Open(t, mode)
			if dbReader == nil {
				return
			}
			defer dbReader.Close()
			dbWriter := testutil.Open(t, mode)
			if dbWriter == nil {
				return
			}
			defer dbWriter.Close()

			table := newTransactionBoundaryTable(t, dbReader)
			defer dbReader.DropTable(table)
			seed := transactionBoundaryRow{Name: testutil.UniqueTable("phantom-seed"), Value: 10}
			if err := dbReader.Table(table).Create(&seed).Error; err != nil {
				t.Fatalf("seed row: %v", err)
			}

			reader := dbReader.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if reader.Error != nil {
				t.Fatalf("begin repeatable-read transaction: %v", reader.Error)
			}
			countRows := func() int {
				var count int
				if err := transactionSQL(t, reader).QueryRow("SELECT COUNT(*) FROM "+table+" WHERE value >= ?", 0).Scan(&count); err != nil {
					t.Fatalf("count rows in repeatable-read transaction: %v", err)
				}
				return count
			}
			first := countRows()

			writer := dbWriter.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
			if writer.Error != nil {
				_ = reader.Rollback()
				t.Fatalf("begin phantom writer: %v", writer.Error)
			}
			if err := writer.Table(table).Create(&transactionBoundaryRow{Name: testutil.UniqueTable("phantom-new"), Value: 20}).Error; err != nil {
				_ = writer.Rollback()
				_ = reader.Rollback()
				t.Fatalf("insert phantom row: %v", err)
			}
			if err := writer.Commit().Error; err != nil {
				_ = reader.Rollback()
				t.Fatalf("commit phantom row: %v", err)
			}

			second := countRows()
			if first != 1 || second != 1 {
				_ = reader.Rollback()
				t.Fatalf("repeatable-read counts = (%d,%d), want (1,1)", first, second)
			}
			if err := reader.Commit().Error; err != nil {
				t.Fatalf("commit phantom reader: %v", err)
			}
		})
	}
}

func TestKingbaseLockWaitHonorsContextTimeout(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			dbHolder := testutil.Open(t, mode)
			if dbHolder == nil {
				return
			}
			defer dbHolder.Close()
			dbWaiter := testutil.Open(t, mode)
			if dbWaiter == nil {
				return
			}
			defer dbWaiter.Close()

			table := newTransactionBoundaryTable(t, dbHolder)
			defer dbHolder.DropTable(table)
			row := transactionBoundaryRow{Name: testutil.UniqueTable("lock-wait"), Value: 1}
			if err := dbHolder.Table(table).Create(&row).Error; err != nil {
				t.Fatalf("seed lock row: %v", err)
			}

			holder := dbHolder.Begin()
			if holder.Error != nil {
				t.Fatalf("begin lock holder: %v", holder.Error)
			}
			if err := holder.Table(table).Where("id = ?", row.ID).UpdateColumn("value", 2).Error; err != nil {
				_ = holder.Rollback()
				t.Fatalf("hold row lock: %v", err)
			}

			waiter := dbWaiter.Begin()
			if waiter.Error != nil {
				_ = holder.Rollback()
				t.Fatalf("begin lock waiter: %v", waiter.Error)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			waitResult := make(chan error, 1)
			go func() {
				sqlWaiter, ok := waiter.CommonDB().(*sql.Tx)
				if !ok || sqlWaiter == nil {
					waitResult <- fmt.Errorf("waiter CommonDB is %T, want *sql.Tx", waiter.CommonDB())
					return
				}
				_, err := sqlWaiter.ExecContext(ctx, "UPDATE "+table+" SET value = value + 1 WHERE id = ?", row.ID)
				waitResult <- err
			}()
			var err error
			select {
			case err = <-waitResult:
			case <-time.After(3 * time.Second):
				cancel()
				_ = holder.Rollback()
				_ = waiter.Rollback()
				t.Fatal("lock waiter did not return after context timeout")
			}
			cancel()
			_ = waiter.Rollback()
			_ = holder.Rollback()
			if err == nil {
				t.Fatal("blocked update unexpectedly succeeded before context timeout")
			}
			t.Logf("%s lock wait returned: %v", mode, err)
		})
	}
}

func TestKingbaseDeadlockIsDetectedOrCanceled(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db1 := testutil.Open(t, mode)
			if db1 == nil {
				return
			}
			defer db1.Close()
			db2 := testutil.Open(t, mode)
			if db2 == nil {
				return
			}
			defer db2.Close()

			table := newTransactionBoundaryTable(t, db1)
			defer db1.DropTable(table)
			first := transactionBoundaryRow{Name: testutil.UniqueTable("deadlock-a"), Value: 1}
			second := transactionBoundaryRow{Name: testutil.UniqueTable("deadlock-b"), Value: 1}
			if err := db1.Table(table).Create(&first).Error; err != nil {
				t.Fatalf("seed deadlock row A: %v", err)
			}
			if err := db1.Table(table).Create(&second).Error; err != nil {
				t.Fatalf("seed deadlock row B: %v", err)
			}

			tx1 := db1.Begin()
			tx2 := db2.Begin()
			if tx1.Error != nil || tx2.Error != nil {
				_ = tx1.Rollback()
				_ = tx2.Rollback()
				t.Fatalf("begin deadlock transactions: tx1=%v tx2=%v", tx1.Error, tx2.Error)
			}
			if err := tx1.Table(table).Where("id = ?", first.ID).UpdateColumn("value", 2).Error; err != nil {
				_ = tx1.Rollback()
				_ = tx2.Rollback()
				t.Fatalf("lock row A: %v", err)
			}
			if err := tx2.Table(table).Where("id = ?", second.ID).UpdateColumn("value", 2).Error; err != nil {
				_ = tx1.Rollback()
				_ = tx2.Rollback()
				t.Fatalf("lock row B: %v", err)
			}

			start := make(chan struct{})
			results := make(chan error, 2)
			update := func(tx *gorm.DB, id uint) {
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				sqlTx, ok := tx.CommonDB().(*sql.Tx)
				if !ok || sqlTx == nil {
					results <- fmt.Errorf("deadlock CommonDB is %T, want *sql.Tx", tx.CommonDB())
					return
				}
				_, err := sqlTx.ExecContext(ctx, "UPDATE "+table+" SET value = value + 1 WHERE id = ?", id)
				results <- err
			}
			go update(tx1, second.ID)
			go update(tx2, first.ID)
			close(start)

			var firstErr, secondErr error
			select {
			case firstErr = <-results:
			case <-time.After(5 * time.Second):
				_ = tx1.Rollback()
				_ = tx2.Rollback()
				t.Fatal("deadlock test did not return within 5 seconds")
			}
			select {
			case secondErr = <-results:
			case <-time.After(5 * time.Second):
				_ = tx1.Rollback()
				_ = tx2.Rollback()
				t.Fatal("second deadlock participant did not return within 5 seconds")
			}
			_ = tx1.Rollback()
			_ = tx2.Rollback()
			if firstErr == nil && secondErr == nil {
				t.Fatal("cyclic row updates both succeeded; deadlock was not detected or canceled")
			}
			t.Logf("%s deadlock results: %v / %v", mode, firstErr, secondErr)
		})
	}
}

func TestKingbaseSavepointRollbackToAndNestedTransactionSemantics(t *testing.T) {
	forEachTransactionMode(t, func(t *testing.T, mode kb.KbMode, db *gorm.DB) {
		table := newTransactionBoundaryTable(t, db)
		defer db.DropTable(table)

		tx := db.Begin()
		if tx.Error != nil {
			t.Fatalf("begin savepoint transaction: %v", tx.Error)
		}
		if err := tx.Table(table).Create(&transactionBoundaryRow{Name: "savepoint-before", Value: 1}).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert before savepoint: %v", err)
		}
		if _, err := transactionSQL(t, tx).Exec("SAVEPOINT gorm_boundary_sp"); err != nil {
			_ = tx.Rollback()
			t.Fatalf("create savepoint: %v", err)
		}
		if err := tx.Table(table).Create(&transactionBoundaryRow{Name: "savepoint-rolled-back", Value: 2}).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert after savepoint: %v", err)
		}
		if _, err := transactionSQL(t, tx).Exec("ROLLBACK TO SAVEPOINT gorm_boundary_sp"); err != nil {
			_ = tx.Rollback()
			t.Fatalf("rollback to savepoint: %v", err)
		}
		if err := tx.Table(table).Create(&transactionBoundaryRow{Name: "savepoint-after", Value: 3}).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert after rollback to savepoint: %v", err)
		}
		if err := tx.Commit().Error; err != nil {
			t.Fatalf("commit savepoint transaction: %v", err)
		}
		if got := transactionBoundaryCount(t, db, table, "savepoint-before"); got != 1 {
			t.Fatalf("before-savepoint count=%d, want 1", got)
		}
		if got := transactionBoundaryCount(t, db, table, "savepoint-rolled-back"); got != 0 {
			t.Fatalf("rolled-back savepoint count=%d, want 0", got)
		}
		if got := transactionBoundaryCount(t, db, table, "savepoint-after"); got != 1 {
			t.Fatalf("after-savepoint count=%d, want 1", got)
		}

		outer := db.Begin()
		if outer.Error != nil {
			t.Fatalf("begin outer transaction: %v", outer.Error)
		}
		if err := outer.Table(table).Create(&transactionBoundaryRow{Name: "nested-outer", Value: 4}).Error; err != nil {
			_ = outer.Rollback()
			t.Fatalf("insert outer row: %v", err)
		}
		nestedErr := errors.New("nested transaction callback failure")
		if err := outer.Transaction(func(inner *gorm.DB) error {
			if createErr := inner.Table(table).Create(&transactionBoundaryRow{Name: "nested-inner", Value: 5}).Error; createErr != nil {
				return createErr
			}
			return nestedErr
		}); err != nestedErr {
			_ = outer.Rollback()
			t.Fatalf("nested transaction error = %v, want %v", err, nestedErr)
		}
		// 当接收者已经包装 *sql.Tx 时，GORM V1 不会为嵌套 Transaction 调用创建保存点。
		// 外层事务仍可使用，因此本记录与内层记录会在下方一起提交。
		if err := outer.Table(table).Create(&transactionBoundaryRow{Name: "nested-after", Value: 6}).Error; err != nil {
			_ = outer.Rollback()
			t.Fatalf("continue outer transaction after nested callback: %v", err)
		}
		if err := outer.Commit().Error; err != nil {
			t.Fatalf("commit outer transaction: %v", err)
		}
		for _, name := range []string{"nested-outer", "nested-inner", "nested-after"} {
			if got := transactionBoundaryCount(t, db, table, name); got != 1 {
				t.Fatalf("nested transaction row %q count=%d, want 1", name, got)
			}
		}
		t.Logf("%s explicit savepoint and GORM V1 nested-transaction behavior verified", mode)
	})
}

func TestKingbaseTransactionReadYourOwnWrite(t *testing.T) {
	forEachTransactionMode(t, func(t *testing.T, mode kb.KbMode, db *gorm.DB) {
		table := newTransactionBoundaryTable(t, db)
		defer db.DropTable(table)

		name := testutil.UniqueTable("read-your-own-write")
		tx := db.Begin()
		if tx.Error != nil {
			t.Fatalf("begin read-your-own-write transaction: %v", tx.Error)
		}
		row := transactionBoundaryRow{Name: name, Value: 41}
		if err := tx.Table(table).Create(&row).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert inside transaction: %v", err)
		}
		var loaded transactionBoundaryRow
		if err := tx.Table(table).Where("id = ?", row.ID).First(&loaded).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("read own uncommitted write: %v", err)
		}
		if loaded.Name != name || loaded.Value != 41 {
			_ = tx.Rollback()
			t.Fatalf("read-your-own-write = %#v, want name=%q value=41", loaded, name)
		}
		if err := tx.Rollback().Error; err != nil {
			t.Fatalf("rollback read-your-own-write transaction: %v", err)
		}
		if got := transactionBoundaryCount(t, db, table, name); got != 0 {
			t.Fatalf("rolled-back read-your-own-write row count=%d, want 0", got)
		}
		t.Logf("%s transaction sees its own write before commit", mode)
	})
}

func TestKingbaseTransactionContextCancellationAndTimeout(t *testing.T) {
	forEachTransactionMode(t, func(t *testing.T, mode kb.KbMode, db *gorm.DB) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		tx := db.BeginTx(ctx, &sql.TxOptions{})
		if tx.Error == nil {
			_ = tx.Rollback()
			t.Fatal("BeginTx with a canceled context unexpectedly succeeded")
		}

		activeCtx, activeCancel := context.WithCancel(context.Background())
		active := db.BeginTx(activeCtx, &sql.TxOptions{})
		if active.Error != nil {
			activeCancel()
			t.Fatalf("begin active context transaction: %v", active.Error)
		}
		activeCancel()
		if _, err := transactionSQL(t, active).ExecContext(activeCtx, "SELECT 1"); err == nil {
			_ = active.Rollback()
			t.Fatal("query with a canceled transaction context unexpectedly succeeded")
		}
		if err := active.Commit().Error; err == nil {
			_ = active.Rollback()
			t.Fatal("commit after transaction context cancellation unexpectedly succeeded")
		}
		// 取消后再次 Rollback 应保持安全；database/sql 可能已经回滚，且 GORM V1 会忽略 sql.ErrTxDone。
		_ = active.Rollback()
		t.Logf("%s transaction context cancellation returned a commit error", mode)
	})
}

func TestKingbaseRollbackUnlessCommittedBranches(t *testing.T) {
	forEachTransactionMode(t, func(t *testing.T, mode kb.KbMode, db *gorm.DB) {
		table := newTransactionBoundaryTable(t, db)
		defer db.DropTable(table)

		tx := db.Begin()
		if tx.Error != nil {
			t.Fatalf("begin rollback-unless transaction: %v", tx.Error)
		}
		if err := tx.Table(table).Create(&transactionBoundaryRow{Name: "rollback-unless", Value: 1}).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert rollback-unless row: %v", err)
		}
		if err := tx.RollbackUnlessCommitted().Error; err != nil {
			t.Fatalf("RollbackUnlessCommitted on open tx: %v", err)
		}
		if got := transactionBoundaryCount(t, db, table, "rollback-unless"); got != 0 {
			t.Fatalf("rollback-unless row count=%d, want 0", got)
		}

		tx = db.Begin()
		if tx.Error != nil {
			t.Fatalf("begin committed rollback-unless transaction: %v", tx.Error)
		}
		if err := tx.Table(table).Create(&transactionBoundaryRow{Name: "committed-unless", Value: 2}).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert committed-unless row: %v", err)
		}
		if err := tx.Commit().Error; err != nil {
			t.Fatalf("commit committed-unless row: %v", err)
		}
		if err := tx.RollbackUnlessCommitted().Error; err != nil {
			t.Fatalf("RollbackUnlessCommitted after commit: %v", err)
		}
		if got := transactionBoundaryCount(t, db, table, "committed-unless"); got != 1 {
			t.Fatalf("committed-unless row count=%d, want 1", got)
		}
		t.Logf("%s RollbackUnlessCommitted rollback and committed branches verified", mode)
	})
}

func TestKingbaseReadOnlyTransactionRejectsWrite(t *testing.T) {
	forEachTransactionMode(t, func(t *testing.T, mode kb.KbMode, db *gorm.DB) {
		table := newTransactionBoundaryTable(t, db)
		defer db.DropTable(table)
		row := transactionBoundaryRow{Name: "readonly-seed", Value: 1}
		if err := db.Table(table).Create(&row).Error; err != nil {
			t.Fatalf("seed readonly row: %v", err)
		}

		tx := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if tx.Error != nil {
			t.Fatalf("begin read-only transaction in %s mode: %v", mode, tx.Error)
		}
		defer tx.Rollback()
		var loaded transactionBoundaryRow
		if err := tx.Table(table).First(&loaded, row.ID).Error; err != nil {
			t.Fatalf("read in read-only transaction: %v", err)
		}
		if err := tx.Table(table).Create(&transactionBoundaryRow{Name: "readonly-write", Value: 2}).Error; err == nil {
			t.Fatal("write in read-only transaction unexpectedly succeeded")
		}
		t.Logf("%s read-only transaction rejected write as expected", mode)
	})
}

func TestKingbaseConstraintErrorContinueOrCommitBehavior(t *testing.T) {
	forEachTransactionMode(t, func(t *testing.T, mode kb.KbMode, db *gorm.DB) {
		table := newTransactionBoundaryTable(t, db)
		defer db.DropTable(table)
		tx := db.Begin()
		if tx.Error != nil {
			t.Fatalf("begin constraint transaction: %v", tx.Error)
		}
		sqlTx := transactionSQL(t, tx)
		insertSQL := fmt.Sprintf("INSERT INTO %s (name, value) VALUES (?, ?)", table)
		if _, err := sqlTx.Exec(insertSQL, "constraint-first", 1); err != nil {
			_ = tx.Rollback()
			t.Fatalf("first insert: %v", err)
		}
		if _, err := sqlTx.Exec(insertSQL, "constraint-first", 2); err == nil {
			_ = tx.Rollback()
			t.Fatal("duplicate insert unexpectedly succeeded")
		}

		_, continueErr := sqlTx.Exec(insertSQL, "constraint-recovery", 3)
		if continueErr == nil {
			if err := tx.Commit().Error; err != nil {
				t.Fatalf("commit after recoverable constraint error: %v", err)
			}
			if got := transactionBoundaryCount(t, db, table, "constraint-first"); got != 1 {
				t.Fatalf("recoverable branch first row count=%d, want 1", got)
			}
			if got := transactionBoundaryCount(t, db, table, "constraint-recovery"); got != 1 {
				t.Fatalf("recoverable branch recovery row count=%d, want 1", got)
			}
			t.Logf("%s constraint error allowed continuation and commit", mode)
			return
		}

		if err := tx.Rollback().Error; err != nil {
			t.Fatalf("rollback aborted constraint transaction: %v", err)
		}
		if got := transactionBoundaryCount(t, db, table, "constraint-first"); got != 0 {
			t.Fatalf("aborted branch retained first row count=%d, want 0", got)
		}
		if got := transactionBoundaryCount(t, db, table, "constraint-recovery"); got != 0 {
			t.Fatalf("aborted branch retained recovery row count=%d, want 0", got)
		}
		t.Logf("%s constraint error aborted transaction; continuation returned %v", mode, continueErr)
	})
}

func TestKingbaseDDLTransactionIsAtomic(t *testing.T) {
	forEachTransactionMode(t, func(t *testing.T, mode kb.KbMode, db *gorm.DB) {
		table := newTransactionBoundaryTable(t, db)
		defer db.DropTable(table)

		tx := db.Begin()
		if tx.Error != nil {
			t.Fatalf("begin DDL transaction: %v", tx.Error)
		}
		rowName := testutil.UniqueTable("ddl-rollback")
		if err := tx.Table(table).Create(&transactionBoundaryRow{Name: rowName, Value: 1}).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert before DDL: %v", err)
		}
		if err := tx.Table(table).AutoMigrate(&transactionBoundaryMigrationRow{}).Error; err != nil {
			_ = tx.Rollback()
			t.Fatalf("ALTER TABLE inside transaction: %v", err)
		}
		if err := tx.Rollback().Error; err != nil {
			t.Fatalf("rollback DDL transaction: %v", err)
		}

		// 表在事务开始前已存在，因此元数据或计数查询失败必须判为测试失败，
		// 绝不能当成可接受的“表不存在”。
		if got := transactionBoundaryCount(t, db, table, rowName); got != 0 {
			t.Fatalf("%s DDL rollback retained %d DML row(s)", mode, got)
		}
		if db.Dialect().HasColumn(table, "marker") {
			t.Fatalf("%s DDL rollback retained the marker column", mode)
		}
		t.Logf("%s DDL and DML rolled back atomically", mode)
	})
}

func TestKingbaseClosedConnectionTransactionErrors(t *testing.T) {
	for _, mode := range []kb.KbMode{kb.KbModeMySQL, kb.KbModeOracle} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			db := testutil.Open(t, mode)
			if db == nil {
				return
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close %s connection: %v", mode, err)
			}
			tx := db.Begin()
			if tx.Error == nil {
				t.Fatalf("begin on closed %s connection unexpectedly succeeded", mode)
			}
			if err := tx.Commit().Error; err == nil {
				t.Fatal("commit invalid transaction unexpectedly succeeded")
			}
			if err := tx.Rollback().Error; err == nil {
				t.Fatal("rollback invalid transaction unexpectedly succeeded")
			}
		})
	}
}
