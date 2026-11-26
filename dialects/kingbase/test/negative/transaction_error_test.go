//go:build kingbase_test
// +build kingbase_test

package negative_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jinzhu/gorm"
)

const (
	testDialectKingbaseMySQL  = "kingbase-mysql"
	testDialectKingbaseOracle = "kingbase-oracle"
)

func TestKingbaseTransactionCommitAndRollbackDriverErrors(t *testing.T) {
	for _, dialect := range []string{testDialectKingbaseMySQL, testDialectKingbaseOracle} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("create sqlmock: %v", err)
			}
			defer sqlDB.Close()

			db, err := gorm.Open(dialect, sqlDB)
			if err != nil {
				t.Fatalf("open %s mock database: %v", dialect, err)
			}

			mock.ExpectBegin()
			commitTx := db.Begin()
			if commitTx.Error != nil {
				t.Fatalf("begin commit-error transaction: %v", commitTx.Error)
			}
			commitFailure := errors.New("driver connection lost during commit")
			mock.ExpectCommit().WillReturnError(commitFailure)
			if err := commitTx.Commit().Error; err == nil || !strings.Contains(err.Error(), commitFailure.Error()) {
				t.Fatalf("commit error = %v, want %q", err, commitFailure)
			}

			mock.ExpectBegin()
			rollbackTx := db.Begin()
			if rollbackTx.Error != nil {
				t.Fatalf("begin rollback-error transaction: %v", rollbackTx.Error)
			}
			rollbackFailure := errors.New("driver connection lost during rollback")
			mock.ExpectRollback().WillReturnError(rollbackFailure)
			if err := rollbackTx.Rollback().Error; err == nil || !strings.Contains(err.Error(), rollbackFailure.Error()) {
				t.Fatalf("rollback error = %v, want %q", err, rollbackFailure)
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("sql expectations: %v", err)
			}
		})
	}
}
