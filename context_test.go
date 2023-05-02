package gorm

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

type testDB struct {
	t      *testing.T
	gotCtx context.Context
	SQLCommon
}

type result int64

func (res result) LastInsertId() (int64, error) {
	return 0, nil
}

func (res result) RowsAffected() (int64, error) {
	return int64(res), nil
}

func (tdb *testDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	tdb.gotCtx = ctx
	return result(42), nil
}

func TestContext(t *testing.T) {
	tdb := &testDB{t: t}
	db, err := Open("postgres", tdb)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	dl := time.Now().Add(60 * time.Minute)

	dlctx, cancel := context.WithDeadline(context.Background(), dl)
	defer cancel()

	vctx := context.WithValue(dlctx, "some-key", 23)
	db = db.WithContext(vctx)

	conn := db.Exec("WHATEVER")
	if errs := conn.GetErrors(); len(errs) > 0 {
		t.Fatalf("got errors: %v", errs)
	}
	if conn.RowsAffected != 42 {
		t.Fatalf("got %d rows affected, want 42", conn.RowsAffected)
	}
	got := tdb.gotCtx
	if deadline, _ := got.Deadline(); !deadline.Equal(dl) {
		t.Fatalf("got deadline %v, want %v", deadline, dl)
	}
	if val := got.Value("some-key").(int); val != 23 {
		t.Fatalf("got value %v, want 23", val)
	}

}
