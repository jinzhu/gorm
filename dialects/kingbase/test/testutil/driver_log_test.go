//go:build kingbase_test
// +build kingbase_test

package testutil

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jinzhu/gorm"
	"kingbase.com/gokb"
)

// 此报告器允许测试验证失败结论，而无需故意令外层 Go 测试失败。
type recordedConclusion struct {
	failed bool
	lines  []string
}

func (*recordedConclusion) Helper()        {}
func (*recordedConclusion) Skipped() bool  { return false }
func (*recordedConclusion) Name() string   { return "driver_log_test" }
func (r *recordedConclusion) Failed() bool { return r.failed }
func (r *recordedConclusion) Fail()        { r.failed = true }
func (r *recordedConclusion) Logf(format string, args ...interface{}) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}
func (r *recordedConclusion) Errorf(format string, args ...interface{}) {
	r.failed = true
	r.Logf(format, args...)
}

func openDriverLogMock(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, *DriverLog, func()) {
	t.Helper()
	return openDriverLogMockDialect(t, testDialectKingbaseMySQL)
}

func openDriverLogMockDialect(t *testing.T, dialect string) (*gorm.DB, sqlmock.Sqlmock, *DriverLog, func()) {
	t.Helper()
	dsn := UniqueTable("driver_log_mock")
	base, mock, err := sqlmock.NewWithDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	log := &DriverLog{tb: t}
	sqlDB := sql.OpenDB(&loggedConnector{base: base.Driver(), dsn: dsn, log: log})
	db, err := gorm.Open(dialect, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	db = db.Set(driverLogKey, log)
	db.LogMode(false)
	return db, mock, log, func() {
		mock.ExpectClose()
		Close(t, db)
		mock.ExpectClose()
		if err := base.Close(); err != nil {
			t.Errorf("close base mock: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("SQL expectations: %v", err)
		}
	}
}

func TestDriverLogOracleUniqueConflict(t *testing.T) {
	expected := &gokb.Error{Code: "23505", Constraint: "uix_users_username"}
	for _, test := range []struct {
		name        string
		queryErr    error
		rollbackErr error
		wantFailure bool
	}{
		{"expected_conflict", expected, nil, false},
		{"unexpected_query_error", &gokb.Error{Code: "42601"}, nil, true},
		{"rollback_error", expected, errors.New("rollback connection lost"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, log, close := openDriverLogMockDialect(t, testDialectKingbaseOracle)
			defer close()
			db = ObserveMetadata(t, db)
			db.LogMode(false)
			output := &recordedConclusion{}
			log.tb = output
			mock.ExpectBegin()
			mock.ExpectQuery("INSERT INTO.*RETURNING").WillReturnError(test.queryErr)
			rollback := mock.ExpectRollback()
			if test.rollbackErr != nil {
				rollback.WillReturnError(test.rollbackErr)
			}
			match := func(err error) bool { return err == expected }
			start := log.Snapshot()
			if err := log.WithExpectedError(match, func() error {
				return db.Table("users").Create(&metadataTestRow{}).Error
			}); err == nil {
				t.Fatal("Oracle INSERT lost the driver error")
			}
			log.ExpectErrors(start, match)
			conclusion := &recordedConclusion{}
			log.Check(conclusion, start)
			text := strings.Join(output.lines, "\n")
			if conclusion.failed != test.wantFailure || strings.Contains(text, "[金仓错误 #") != test.wantFailure {
				t.Fatalf("failed=%t; output=%s", conclusion.failed, text)
			}
			if !test.wantFailure && !strings.Contains(text, "[预期错误 #") {
				t.Fatalf("expected Oracle conflict has a misleading label: %s", text)
			}
		})
	}
}

func TestDriverLogRetainsErrorDroppedByGorm(t *testing.T) {
	for _, dialect := range []string{testDialectKingbaseMySQL, testDialectKingbaseOracle} {
		t.Run(dialect, func(t *testing.T) {
			db, mock, log, close := openDriverLogMockDialect(t, dialect)
			defer close()
			failure := &gokb.Error{Code: "42601", Message: "syntax error near ON"}
			output := &recordedConclusion{}
			log.tb = output
			start := log.Snapshot()
			mock.ExpectExec("DROP INDEX").WillReturnError(failure)
			gormErr := db.Table("users").RemoveIndex("idx_users_name").Error
			if gormErr != nil && gormErr != failure {
				t.Fatalf("unexpected GORM error: %v", gormErr)
			}
			results := log.SQLResults(start)
			if len(results) != 1 || results[0].Err != failure || results[0].Operation != "Exec" || !strings.HasPrefix(results[0].SQL, "DROP INDEX") {
				t.Fatalf("original driver SQL and error were not retained: %+v", results)
			}
			results[0].SQL = "changed copy"
			if log.SQLResults(start)[0].SQL == "changed copy" || len(log.SQLResults(log.Snapshot())) != 0 {
				t.Fatal("SQL results are not an isolated snapshot")
			}
			conclusion := &recordedConclusion{}
			log.Check(conclusion, 0)
			if !conclusion.failed || !strings.Contains(strings.Join(output.lines, "\n"), failure.Error()) {
				t.Fatalf("driver error did not fail the test conclusion: %v", conclusion.lines)
			}
			text := strings.Join(append(output.lines, conclusion.lines...), "\n")
			if strings.Count(text, failure.Error()) != 1 || !strings.Contains(text, "SQLSTATE=42601") || !strings.Contains(text, "DROP INDEX") {
				t.Fatalf("error must appear once with its SQL and code: %s", text)
			}
		})
	}
}

func TestDriverLogSuccessfulSQLIsQuiet(t *testing.T) {
	db, mock, log, close := openDriverLogMock(t)
	defer close()
	output := &recordedConclusion{}
	log.tb = output
	mock.ExpectBegin()
	mock.ExpectExec("INSERT").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := db.Table("users").Create(&metadataTestRow{ID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"cnt"}).AddRow(3))
	var count int
	if err := db.CommonDB().QueryRow("SELECT COUNT(*) AS cnt FROM users").Scan(&count); err != nil || count != 3 {
		t.Fatalf("count=%d; error=%v", count, err)
	}
	if len(output.lines) != 0 {
		t.Fatalf("successful driver calls produced unnecessary logs: %v", output.lines)
	}
	log.Check(output, 0)
	if len(output.lines) != 1 || !strings.Contains(output.lines[0], "[通过]") {
		t.Fatalf("expected a single passing conclusion: %v", output.lines)
	}
}

func TestDriverLogOnlyIgnoresValidControlSignals(t *testing.T) {
	for _, test := range []struct {
		operation string
		err       error
		failure   bool
	}{
		{"Exec", driver.ErrSkip, false},
		{"NextResultSet", io.EOF, false},
		{"Exec", io.EOF, true},
		{"Rollback", io.EOF, true},
		{"RowsAffected", driver.ErrSkip, true},
	} {
		t.Run(test.operation+"_"+test.err.Error(), func(t *testing.T) {
			log := &DriverLog{tb: t}
			log.finish(1, test.operation, "", test.err)
			conclusion := &recordedConclusion{}
			log.Check(conclusion, 0)
			if conclusion.failed != test.failure {
				t.Fatalf("unexpected conclusion for %s: %v", test.operation, conclusion.lines)
			}
		})
	}
}

func TestDriverLogOnlyAcknowledgesExpectedErrors(t *testing.T) {
	db, mock, log, close := openDriverLogMock(t)
	defer close()
	expected := errors.New("expected unique constraint conflict")
	unrelated := errors.New("rollback connection lost")
	start := log.Snapshot()
	mock.ExpectExec("INSERT").WillReturnError(expected)
	if err := db.Exec("INSERT INTO users VALUES (1)").Error; err != expected {
		t.Fatal(err)
	}
	if count := log.ExpectErrors(start, func(err error) bool { return err == expected }); count != 1 {
		t.Fatalf("expected error matches = %d, want 1", count)
	}
	conclusion := &recordedConclusion{}
	log.Check(conclusion, start)
	if conclusion.failed {
		t.Fatalf("verified expected error failed the test: %v", conclusion.lines)
	}
	log.finish(99, "Rollback", "", unrelated)
	log.Check(conclusion, start)
	if !conclusion.failed {
		t.Fatal("an unrelated driver error was treated as expected")
	}
}

func TestDriverLogExpectedErrorLabelsAreScoped(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		wantLabel string
		wantCount int
	}{
		{"unique_conflict", &gokb.Error{Code: "23505", Constraint: "uix_name"}, "预期错误", 1},
		{"wrong_constraint", &gokb.Error{Code: "23505", Constraint: "other_index"}, "金仓错误", 0},
		{"syntax_error", &gokb.Error{Code: "42601"}, "金仓错误", 0},
		{"connection_error", errors.New("connection lost"), "金仓错误", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, log, close := openDriverLogMock(t)
			defer close()
			output := &recordedConclusion{}
			log.tb = output
			match := func(err error) bool {
				serverErr, ok := err.(*gokb.Error)
				return ok && serverErr.Code == "23505" && serverErr.Constraint == "uix_name"
			}
			start := log.Snapshot()
			mock.ExpectExec("INSERT").WillReturnError(test.err)
			err := log.WithExpectedError(match, func() error {
				return db.Exec("INSERT INTO users VALUES (1)").Error
			})
			if err != test.err || len(output.lines) == 0 || !strings.HasPrefix(output.lines[0], "["+test.wantLabel+" #") {
				t.Fatalf("error=%v; output=%v", err, output.lines)
			}
			// 仅有标签绝不能认领错误或让测试通过。
			unverified := &recordedConclusion{}
			log.Check(unverified, start)
			if !unverified.failed {
				t.Fatal("an unverified error did not fail the test")
			}
			if count := log.ExpectErrors(start, match); count != test.wantCount {
				t.Fatalf("acknowledged %d errors, want %d", count, test.wantCount)
			}
			// 相同错误若发生在重复插入之外，必须失去该标签。
			before := len(output.lines)
			log.finish(99, "Exec", "INSERT", test.err)
			if !strings.HasPrefix(output.lines[before], "[金仓错误 #") {
				t.Fatalf("expected-error label leaked outside its scope: %v", output.lines[before:])
			}
		})
	}
}

func TestDriverLogPreservesTransactionsAndRecordsFailures(t *testing.T) {
	for _, action := range []string{"commit", "rollback", "commit_error", "rollback_error", "create"} {
		t.Run(action, func(t *testing.T) {
			db, mock, log, close := openDriverLogMock(t)
			defer close()
			db = ObserveMetadata(t, db)
			failure := errors.New("transaction driver error")
			mock.ExpectBegin()
			if action == "create" {
				mock.ExpectExec("INSERT INTO `users`").WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
				if err := db.Table("users").Create(&metadataTestRow{ID: 1}).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				tx := db.Begin()
				if tx.Error != nil {
					t.Fatal(tx.Error)
				}
				mock.ExpectExec("INSERT").WillReturnResult(sqlmock.NewResult(1, 1))
				if err := tx.Exec("INSERT INTO users VALUES (1)").Error; err != nil {
					t.Fatal(err)
				}
				var got error
				if strings.HasPrefix(action, "commit") {
					expect := mock.ExpectCommit()
					if action == "commit_error" {
						expect.WillReturnError(failure)
					}
					got = tx.Commit().Error
				} else {
					expect := mock.ExpectRollback()
					if action == "rollback_error" {
						expect.WillReturnError(failure)
					}
					got = tx.Rollback().Error
				}
				if strings.HasSuffix(action, "_error") && got != failure {
					t.Fatalf("transaction error = %v, want %v", got, failure)
				}
				if !strings.HasSuffix(action, "_error") && got != nil {
					t.Fatal(got)
				}
			}
			conclusion := &recordedConclusion{}
			log.Check(conclusion, 0)
			if conclusion.failed != strings.HasSuffix(action, "_error") {
				t.Fatalf("unexpected conclusion: %v", conclusion.lines)
			}
			var sawBegin, sawWrite, sawEnd bool
			for _, event := range log.events {
				sawBegin = sawBegin || event.operation == "BeginTx"
				sawWrite = sawWrite || event.operation == "Exec" && strings.HasPrefix(event.query, "INSERT")
				sawEnd = sawEnd || event.operation == "Commit" || event.operation == "Rollback"
			}
			if !sawBegin || !sawWrite || !sawEnd {
				t.Fatalf("transaction logging is incomplete: begin=%t, write=%t, end=%t", sawBegin, sawWrite, sawEnd)
			}
		})
	}
}

func TestDriverLogRecordsReadAndResultErrors(t *testing.T) {
	for _, action := range []string{"rows", "rows_affected"} {
		t.Run(action, func(t *testing.T) {
			db, mock, log, close := openDriverLogMock(t)
			defer close()
			failure := errors.New("driver result error")
			if action == "rows" {
				mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).RowError(0, failure))
				rows, err := db.CommonDB().Query("SELECT id FROM users")
				if err != nil {
					t.Fatal(err)
				}
				if rows.Next() || rows.Err() != failure {
					t.Fatalf("row read error was lost: %v", rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				mock.ExpectBegin()
				mock.ExpectExec("INSERT").WillReturnResult(sqlmock.NewErrorResult(failure))
				mock.ExpectCommit()
				// GORM V1 在此 Create 路径上会忽略 RowsAffected 返回的错误。
				if err := db.Table("users").Create(&metadataTestRow{ID: 1}).Error; err != nil && err != failure {
					t.Fatalf("unexpected GORM error: %v", err)
				}
			}
			conclusion := &recordedConclusion{}
			log.Check(conclusion, 0)
			if !conclusion.failed {
				t.Fatal("driver result error did not fail the conclusion")
			}
		})
	}
}

func TestDriverLogPreparedStatementsAndValues(t *testing.T) {
	db, mock, log, close := openDriverLogMock(t)
	defer close()
	mock.ExpectPrepare("SELECT").ExpectQuery().WithArgs(3).
		WillReturnRows(sqlmock.NewRows([]string{"cnt"}).AddRow(3))
	stmt, err := db.CommonDB().Prepare("SELECT ? AS cnt")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := stmt.QueryRow(3).Scan(&count); err != nil || count != 3 {
		t.Fatalf("prepared query: count=%d; error=%v", count, err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	conclusion := &recordedConclusion{}
	log.Check(conclusion, 0)
	if conclusion.failed {
		t.Fatalf("successful prepared query was marked failed: %v", conclusion.lines)
	}
}
