//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jinzhu/gorm"
	"kingbase.com/gokb"
)

const (
	testDialectKingbaseMySQL  = "kingbase-mysql"
	testDialectKingbaseOracle = "kingbase-oracle"
)

func TestMySQLUniqueViolationRejectsUnrelatedErrors(t *testing.T) {
	const constraint = "uix_test_name"
	uniqueErr := &gokb.Error{Code: "23505", Constraint: constraint}
	var nilDriverErr *gokb.Error
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"expected_constraint", uniqueErr, true},
		{"no_error", nil, false},
		{"typed_nil", nilDriverErr, false},
		{"connection_failure", errors.New("connection lost"), false},
		{"syntax_error", &gokb.Error{Code: "42601", Constraint: constraint}, false},
		{"different_constraint", &gokb.Error{Code: "23505", Constraint: "other_constraint"}, false},
		{"missing_constraint", &gokb.Error{Code: "23505"}, false},
		{"gorm_unique_error", gorm.Errors{uniqueErr}, true},
		{"gorm_empty_errors", gorm.Errors{}, false},
		{"gorm_mixed_errors", gorm.Errors{uniqueErr, errors.New("rollback failed")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUniqueViolation(tt.err, constraint); got != tt.want {
				t.Fatalf("isUniqueViolation(%v, %q) = %v, want %v", tt.err, constraint, got, tt.want)
			}
		})
	}
}

func TestMySQLRemoveIndexObserverRetainsDriverError(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	observer := &mysqlDDLObserver{SQLCommon: sqlDB, t: t}
	db, err := gorm.Open(testDialectKingbaseMySQL, observer)
	if err != nil {
		t.Fatal(err)
	}
	db.LogMode(false)
	failure := errors.New("driver refused DROP INDEX")
	mock.ExpectExec("^" + regexp.QuoteMeta("DROP INDEX `idx_users_name`") + "$").WillReturnError(failure)
	gormErr := db.Table("users").RemoveIndex("idx_users_name").Error
	if observer.execCount != 1 || observer.lastError != failure {
		t.Fatalf("driver observation lost: executions=%d; error=%v", observer.execCount, observer.lastError)
	}
	if gormErr != nil && gormErr != failure {
		t.Fatalf("unexpected GORM error: %v", gormErr)
	}
	mock.ExpectClose()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
