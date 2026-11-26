//go:build kingbase_test
// +build kingbase_test

package kingbase_test

import (
	"testing"

	"github.com/jinzhu/gorm"
	"github.com/jinzhu/gorm/dialects/kingbase/test/testutil"
	"kingbase.com/gokb"
)

func runLoggedTest(t *testing.T, db *gorm.DB, name string, test func(*testing.T)) {
	t.Helper()
	t.Run(name, func(child *testing.T) {
		log := testutil.DriverLogFor(db)
		log.Bind(child)
		start := log.Snapshot()
		defer log.Bind(t)
		defer log.Check(child, start)
		test(child)
	})
}

func finishLoggedTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	testutil.Close(t, db)
	testutil.DriverLogFor(db).Check(t, 0)
}

// 检查每一个返回错误：无关失败不能让“故意触发特定唯一约束”的测试误判为通过。
func isUniqueViolation(err error, constraint string) bool {
	if errs, ok := err.(gorm.Errors); ok {
		if len(errs) == 0 {
			return false
		}
		for _, item := range errs {
			if !isUniqueViolation(item, constraint) {
				return false
			}
		}
		return true
	}
	driverErr, ok := err.(*gokb.Error)
	return ok && driverErr != nil && driverErr.Code == "23505" && driverErr.Constraint == constraint
}

func assertUniqueViolation(t *testing.T, db *gorm.DB, constraint string, insert func() error) {
	t.Helper()
	log := testutil.DriverLogFor(db)
	since := log.Snapshot()
	match := func(err error) bool { return isUniqueViolation(err, constraint) }
	err := log.WithExpectedError(match, insert)
	if !isUniqueViolation(err, constraint) {
		t.Fatalf("期望唯一约束 %s 的冲突错误（23505），实际 error=%v; 错误类型=%T; 完整错误结构=%#v", constraint, err, err, err)
	}
	count := log.ExpectErrors(since, match)
	if count != 1 {
		t.Fatalf("预期观测到一次约束 %s 的金仓错误，实际次数=%d", constraint, count)
	}
	t.Logf("[通过] 唯一约束 %s 按预期返回冲突（23505）", constraint)
}
