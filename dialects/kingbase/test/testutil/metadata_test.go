//go:build kingbase_test
// +build kingbase_test

package testutil

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jinzhu/gorm"
)

type metadataTestRow struct {
	ID uint `gorm:"primary_key"`
}

func TestObserveMetadataPreservesTransactions(t *testing.T) {
	for _, operation := range []string{"commit", "rollback", "begin_failure", "begin_tx", "create_commit", "create_rollback"} {
		t.Run(operation, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			db, err := gorm.Open(testDialectKingbaseMySQL, sqlDB)
			if err != nil {
				t.Fatal(err)
			}
			db = ObserveMetadata(t, db)
			db.LogMode(false)
			defer func() {
				mock.ExpectClose()
				Close(t, db)
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Errorf("SQL expectations: %v", err)
				}
			}()
			failure := errors.New("original transaction driver error")
			begin := mock.ExpectBegin()
			if operation == "begin_failure" {
				begin.WillReturnError(failure)
			}
			switch operation {
			case "commit", "rollback", "begin_tx":
				var tx *gorm.DB
				if operation == "begin_tx" {
					tx = db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
				} else {
					tx = db.Begin()
				}
				if tx.Error != nil {
					t.Fatalf("observer prevented starting a transaction: %v", tx.Error)
				}
				mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
				if err := tx.Exec("INSERT INTO users (id) VALUES (1)").Error; err != nil {
					t.Fatalf("transaction write: %v", err)
				}
				if operation == "rollback" {
					mock.ExpectRollback()
					if err := tx.Rollback().Error; err != nil {
						t.Fatalf("rollback: %v", err)
					}
				} else {
					mock.ExpectCommit()
					if err := tx.Commit().Error; err != nil {
						t.Fatalf("commit: %v", err)
					}
				}
			case "begin_failure":
				if err := db.Begin().Error; err != failure {
					t.Fatalf("begin error = %v, want original driver error %v", err, failure)
				}
			case "create_commit":
				mock.ExpectExec("INSERT INTO `users`").WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
				if err := db.Table("users").Create(&metadataTestRow{ID: 1}).Error; err != nil {
					t.Fatalf("automatic create transaction: %v", err)
				}
			case "create_rollback":
				mock.ExpectExec("INSERT INTO `users`").WillReturnError(failure)
				mock.ExpectRollback()
				if err := db.Table("users").Create(&metadataTestRow{ID: 1}).Error; err != failure {
					t.Fatalf("create error = %v, want original driver error %v", err, failure)
				}
			}
		})
	}
}

func TestMetadataFailuresAreObservedWithoutChangingGorm(t *testing.T) {
	for _, dialect := range []string{testDialectKingbaseMySQL, testDialectKingbaseOracle} {
		t.Run(dialect, func(t *testing.T) {
			for _, operation := range []string{"has_table", "has_column", "has_index", "has_foreign_key", "drop_if_exists", "migrate_table", "migrate_column", "add_index"} {
				t.Run(operation, func(t *testing.T) {
					sqlDB, mock, err := sqlmock.New()
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						mock.ExpectClose()
						if err := sqlDB.Close(); err != nil {
							t.Errorf("close mock: %v", err)
						}
						if err := mock.ExpectationsWereMet(); err != nil {
							t.Errorf("SQL expectations: %v", err)
						}
					}()
					failure := errors.New("metadata permission denied")
					var reported []error
					observer := &metadataObserver{SQLCommon: sqlDB, report: func(err error) { reported = append(reported, err) }}
					db, err := gorm.Open(dialect, observer)
					if err != nil {
						t.Fatal(err)
					}
					db.LogMode(false)
					var operationErr error
					switch operation {
					case "has_table":
						mock.ExpectQuery("SELECT COUNT.*FROM information_schema.tables").WillReturnError(failure)
						if db.HasTable("users") {
							t.Fatal("failed query reported an existing table")
						}
					case "has_column":
						mock.ExpectQuery("SELECT COUNT.*FROM information_schema.columns").WillReturnError(failure)
						if db.Dialect().HasColumn("users", "name") {
							t.Fatal("failed query reported an existing column")
						}
					case "has_index":
						mock.ExpectQuery("SELECT COUNT.*FROM pg_indexes").WillReturnError(failure)
						if db.Dialect().HasIndex("users", "idx_users_name") {
							t.Fatal("failed query reported an existing index")
						}
					case "has_foreign_key":
						mock.ExpectQuery("SELECT COUNT.*FROM information_schema.table_constraints").WillReturnError(failure)
						if db.Dialect().HasForeignKey("users", "fk_users") {
							t.Fatal("failed query reported an existing foreign key")
						}
					case "drop_if_exists":
						mock.ExpectQuery("SELECT COUNT.*FROM information_schema.tables").WillReturnError(failure)
						operationErr = db.DropTableIfExists("users").Error
					case "migrate_table":
						mock.ExpectQuery("SELECT COUNT.*FROM information_schema.tables").WillReturnError(failure)
						operationErr = db.Table("users").AutoMigrate(&metadataTestRow{}).Error
					case "migrate_column":
						mock.ExpectQuery("SELECT COUNT.*FROM information_schema.tables").
							WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
						mock.ExpectQuery("SELECT COUNT.*FROM information_schema.columns").WillReturnError(failure)
						operationErr = db.Table("users").AutoMigrate(&metadataTestRow{}).Error
					case "add_index":
						mock.ExpectQuery("SELECT COUNT.*FROM pg_indexes").WillReturnError(failure)
						operationErr = db.Table("users").AddIndex("idx_users_name", "name").Error
					}
					if len(reported) != 1 || reported[0] != failure {
						t.Fatalf("reported metadata errors = %v, want the original driver error", reported)
					}
					if operation == "migrate_table" || operation == "migrate_column" || operation == "add_index" {
						if operationErr != failure {
							t.Fatalf("DDL error = %v, want metadata error %v", operationErr, failure)
						}
					}
					// 底层驱动不应收到 Exec：观察器必须在元数据失败后阻止写入，
					// 包括来自 GORM 克隆句柄的写入。
					if _, err := observer.Exec("DELETE FROM users"); err != failure {
						t.Fatalf("Exec was not blocked after a metadata failure: %v", err)
					}
				})
			}
		})
	}
}

func TestMissingTableIsNotMetadataFailure(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	observer := &metadataObserver{SQLCommon: sqlDB, report: func(err error) { t.Errorf("unexpected metadata failure: %v", err) }}
	db, err := gorm.Open(testDialectKingbaseMySQL, observer)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT COUNT.*FROM information_schema.tables").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	if err := db.DropTableIfExists("users").Error; err != nil {
		t.Fatalf("missing table must be a successful no-op: %v", err)
	}
	mock.ExpectClose()
	Close(t, db)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
