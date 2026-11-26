//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jinzhu/gorm"
)

type oracleFunctionOutput struct {
	logs, failures []string
}

func (*oracleFunctionOutput) Helper() {}
func (o *oracleFunctionOutput) Logf(format string, args ...interface{}) {
	o.logs = append(o.logs, fmt.Sprintf(format, args...))
}
func (o *oracleFunctionOutput) Errorf(format string, args ...interface{}) {
	o.failures = append(o.failures, fmt.Sprintf(format, args...))
}

// 在不故意令真实 testing.T 失败的情况下验证判定结果。每个场景还会继续执行后续查询，
// 确保单个函数失败不会遮蔽其余检查。
func TestOracleFunctionChecksValidateResultsAndContinue(t *testing.T) {
	for _, test := range []struct {
		name        string
		value       driver.Value
		want        string
		numeric     bool
		wantNull    bool
		queryErr    error
		noRows      bool
		wantFailure bool
	}{
		{name: "string", value: "abc", want: "abc"},
		{name: "numeric_format", value: []byte("12.3500"), want: "12.35", numeric: true},
		{name: "wrong_value", value: "wrong", want: "abc", wantFailure: true},
		{name: "unexpected_null", want: "abc", wantFailure: true},
		{name: "expected_null", wantNull: true},
		{name: "unexpected_non_null", value: "abc", wantNull: true, wantFailure: true},
		{name: "invalid_number", value: "invalid", want: "1", numeric: true, wantFailure: true},
		{name: "nan", value: "NaN", want: "1", numeric: true, wantFailure: true},
		{name: "driver_error", queryErr: errors.New("function is unavailable"), wantFailure: true},
		{name: "no_result", noRows: true, wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			db, err := gorm.Open(testDialectKingbaseOracle, sqlDB)
			if err != nil {
				t.Fatal(err)
			}
			db.LogMode(false)
			defer func() {
				mock.ExpectClose()
				if err := db.Close(); err != nil {
					t.Error(err)
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Error(err)
				}
			}()

			const query = "SELECT NVL(NULL, ?) AS actual FROM DUAL"
			expect := mock.ExpectQuery(regexp.QuoteMeta("SELECT NVL(NULL, :1) AS actual FROM DUAL")).WithArgs("abc")
			if test.queryErr != nil {
				expect.WillReturnError(test.queryErr)
			} else {
				rows := sqlmock.NewRows([]string{"actual"})
				if !test.noRows {
					rows.AddRow(test.value)
				}
				expect.WillReturnRows(rows)
			}
			const nextQuery = "SELECT LENGTH('abc') AS actual FROM DUAL"
			mock.ExpectQuery(regexp.QuoteMeta(nextQuery)).WillReturnRows(sqlmock.NewRows([]string{"actual"}).AddRow(3))
			output := &oracleFunctionOutput{}
			oracleCheckFunctions(output, db, []oracleFunctionCheck{
				{name: "first", query: query, args: []interface{}{"abc"}, want: test.want, numeric: test.numeric, wantNull: test.wantNull},
				{name: "next", query: nextQuery, want: "3", numeric: true},
			})
			if (len(output.failures) != 0) != test.wantFailure || len(output.failures) > 1 {
				t.Fatalf("unexpected failures: %v", output.failures)
			}
			if len(output.logs) == 0 || output.logs[len(output.logs)-1] != "[通过] next" {
				t.Fatalf("later function was not verified: %v", output.logs)
			}
			for _, line := range output.logs {
				if !strings.HasPrefix(line, "[通过] ") || strings.Contains(line, "SQL:") {
					t.Fatalf("successful function produced unnecessary details: %s", line)
				}
			}
		})
	}
}
