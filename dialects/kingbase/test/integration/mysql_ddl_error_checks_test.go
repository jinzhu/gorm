//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"errors"
	"testing"

	"github.com/jinzhu/gorm"
	"kingbase.com/gokb"
)

func TestMySQLDDLExpectedErrorMatcher(t *testing.T) {
	good := &gokb.Error{Code: "23514", Table: "target", Constraint: "ck_target"}
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"exact_target", good, true},
		{"no_error", nil, false},
		{"connection_error", errors.New("connection lost"), false},
		{"wrong_code", &gokb.Error{Code: "42601", Table: "target", Constraint: "ck_target"}, false},
		{"wrong_table", &gokb.Error{Code: "23514", Table: "other", Constraint: "ck_target"}, false},
		{"wrong_constraint", &gokb.Error{Code: "23514", Table: "target", Constraint: "other"}, false},
		{"missing_table", &gokb.Error{Code: "23514", Constraint: "ck_target"}, false},
		{"missing_constraint", &gokb.Error{Code: "23514", Table: "target"}, false},
		{"gorm_error", gorm.Errors{good}, true},
		{"gorm_mixed_errors", gorm.Errors{good, errors.New("rollback failed")}, false},
		{"gorm_empty_errors", gorm.Errors{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := mysqlDDLMatchesError(test.err, "23514", "target", "ck_target", ""); got != test.want {
				t.Fatalf("error match=%t, want %t; error=%v", got, test.want, test.err)
			}
		})
	}
	goodNull := &gokb.Error{Code: "23502", Table: "target", Column: "qty"}
	if !mysqlDDLMatchesError(goodNull, "23502", "target", "", "qty") || mysqlDDLMatchesError(goodNull, "23502", "target", "", "other") {
		t.Fatal("NOT NULL errors must match their exact column")
	}
	var nilError *gokb.Error
	if mysqlDDLMatchesError(nilError, "23502", "target", "", "qty") {
		t.Fatal("a typed nil error must not satisfy an expected rejection")
	}
}
