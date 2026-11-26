//go:build kingbase_test
// +build kingbase_test

package testutil

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"kingbase.com/gokb"
)

const driverLogKey = "kingbase:test:driver_log"

type driverEvent struct {
	id        uint64
	operation string
	query     string
	err       error
	expected  bool
	reported  bool
}

type driverCall struct {
	query string
	args  []interface{}
}

// DriverLog 在不修改 GORM 或 gokb 的前提下观察 database/sql 驱动边界。
// 成功调用保持静默，错误保留给测试判定。
type DriverLog struct {
	mu            sync.Mutex
	tb            interface{ Logf(string, ...interface{}) }
	next          uint64
	calls         map[uint64]driverCall
	events        []driverEvent
	expectedError func(error) bool
}

type driverTestReporter interface {
	Helper()
	Logf(string, ...interface{})
	Errorf(string, ...interface{})
	Fail()
	Failed() bool
	Skipped() bool
	Name() string
}

func DriverLogFor(db interface {
	Get(string) (interface{}, bool)
}) *DriverLog {
	value, _ := db.Get(driverLogKey)
	log, _ := value.(*DriverLog)
	return log
}

func (l *DriverLog) Bind(tb testing.TB) {
	l.mu.Lock()
	l.tb = tb
	l.mu.Unlock()
}

func (l *DriverLog) Snapshot() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

// DriverSQLResult 表示一次已完成 SQL 调用的真实驱动返回结果。
type DriverSQLResult struct {
	Operation string
	SQL       string
	Err       error
}

// SQLResults 返回自 Snapshot 以来 SQL 调用的副本，使定向诊断测试无需开启全局详细日志
// 也能展示目标 SQL。
func (l *DriverLog) SQLResults(since int) []DriverSQLResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	var results []DriverSQLResult
	for _, event := range l.events[since:] {
		if event.operation == "Exec" || event.operation == "Query" {
			results = append(results, DriverSQLResult{Operation: event.operation, SQL: event.query, Err: event.err})
		}
	}
	return results
}

// WithExpectedError 将运行期间匹配的错误标记为预期错误；测试仍须验证返回错误，
// 并通过 ExpectErrors 明确认领。
func (l *DriverLog) WithExpectedError(match func(error) bool, run func() error) error {
	l.mu.Lock()
	previous := l.expectedError
	l.expectedError = match
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.expectedError = previous
		l.mu.Unlock()
	}()
	return run()
}

func (l *DriverLog) print(format string, args ...interface{}) {
	l.mu.Lock()
	tb := l.tb
	l.mu.Unlock()
	tb.Logf(format, args...)
}

func (l *DriverLog) start(operation, query string, args interface{}) uint64 {
	var arguments []driver.Value
	switch args := args.(type) {
	case []driver.Value:
		arguments = args
	case []driver.NamedValue:
		for _, arg := range args {
			arguments = append(arguments, arg.Value)
		}
	}
	l.mu.Lock()
	l.next++
	id := l.next
	if l.calls == nil {
		l.calls = make(map[uint64]driverCall)
	}
	l.calls[id] = driverCall{query: query, args: readableValues(arguments)}
	l.mu.Unlock()
	return id
}

func readableValues(values []driver.Value) []interface{} {
	result := make([]interface{}, len(values))
	for i, value := range values {
		switch value := value.(type) {
		case []byte:
			if len(value) > 4096 {
				result[i] = fmt.Sprintf("0x%x… [共%d字节]", value[:64], len(value))
				continue
			}
			printable := utf8.Valid(value)
			for _, char := range string(value) {
				printable = printable && unicode.IsPrint(char)
			}
			if printable {
				result[i] = strconv.Quote(string(value))
			} else {
				result[i] = fmt.Sprintf("0x%x", value)
			}
		case string:
			if len(value) > 4096 {
				prefix := 128
				for prefix > 0 && !utf8.RuneStart(value[prefix]) {
					prefix--
				}
				result[i] = fmt.Sprintf("%s… [共%d字节]", strconv.Quote(value[:prefix]), len(value))
				continue
			}
			result[i] = strconv.Quote(value)
		case time.Time:
			result[i] = value.Format(time.RFC3339Nano)
		default:
			result[i] = value
		}
	}
	return result
}

func (l *DriverLog) finish(id uint64, operation, query string, err error) {
	if err == driver.ErrSkip && (operation == "Exec" || operation == "Query") {
		return
	}
	if err == io.EOF && operation == "NextResultSet" {
		return
	}
	l.mu.Lock()
	call := l.calls[id]
	if query == "" {
		query = call.query
	}
	l.events = append(l.events, driverEvent{id: id, operation: operation, query: query, err: err})
	match := l.expectedError
	l.mu.Unlock()
	if err != nil {
		label := "金仓错误"
		if match != nil && match(err) {
			label = "预期错误"
		}
		l.print("[%s #%d] %s: %s", label, id, operation, driverErrorMessage(err))
		if query != "" {
			l.print("SQL: %s", query)
		}
		if len(call.args) > 0 {
			l.print("参数: %v", call.args)
		}
		if strings.EqualFold(strings.TrimSpace(os.Getenv("DEBUG")), "true") {
			l.print("完整驱动错误: %#v", err)
		}
	}
}

func driverErrorMessage(err error) string {
	if serverErr, ok := err.(*gokb.Error); ok && serverErr != nil {
		parts := []string{fmt.Sprintf("SQLSTATE=%s; %v", serverErr.Code, err)}
		if serverErr.Detail != "" {
			parts = append(parts, "详情="+serverErr.Detail)
		}
		if serverErr.Hint != "" {
			parts = append(parts, "提示="+serverErr.Hint)
		}
		if serverErr.Constraint != "" {
			parts = append(parts, "约束="+serverErr.Constraint)
		}
		return strings.Join(parts, "; ")
	}
	return fmt.Sprintf("%v (%T)", err, err)
}

// ExpectErrors 只认领已由测试验证为预期的错误；即使 GORM 丢弃了返回值，
// 无关驱动错误仍必须判为失败。
func (l *DriverLog) ExpectErrors(since int, match func(error) bool) int {
	l.mu.Lock()
	var ids []uint64
	for i := since; i < len(l.events); i++ {
		event := &l.events[i]
		if event.err != nil && !event.expected && match(event.err) {
			event.expected = true
			ids = append(ids, event.id)
		}
	}
	l.mu.Unlock()
	return len(ids)
}

func (l *DriverLog) Check(tb driverTestReporter, since int) {
	tb.Helper()
	l.mu.Lock()
	var failures []driverEvent
	for i := since; i < len(l.events); i++ {
		event := &l.events[i]
		if event.err == nil || event.expected {
			continue
		}
		if !event.reported {
			failures = append(failures, *event)
			event.reported = true
		}
	}
	l.mu.Unlock()
	if len(failures) > 0 {
		tb.Fail()
	}
	status := "通过"
	if tb.Failed() {
		status = "未通过"
	} else if tb.Skipped() {
		status = "跳过（未验证）"
	}
	name := tb.Name()
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	tb.Logf("[%s] %s", status, name)
}

type loggedConnector struct {
	base driver.Driver
	dsn  string
	log  *DriverLog
}

func (c *loggedConnector) Driver() driver.Driver { return c.base }

func (c *loggedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	id := c.log.start("Connect", "", nil)
	var conn driver.Conn
	var err error
	if base, ok := c.base.(driver.DriverContext); ok {
		var connector driver.Connector
		if connector, err = base.OpenConnector(c.dsn); err == nil {
			conn, err = connector.Connect(ctx)
		}
	} else if err = ctx.Err(); err == nil {
		conn, err = c.base.Open(c.dsn)
	}
	c.log.finish(id, "Connect", "", err)
	if err != nil {
		return nil, err
	}
	return &loggedConn{Conn: conn, log: c.log}, nil
}

type loggedConn struct {
	driver.Conn
	log *DriverLog
}

func (c *loggedConn) Close() error {
	id := c.log.start("Close", "", nil)
	err := c.Conn.Close()
	c.log.finish(id, "Close", "", err)
	return err
}

func (c *loggedConn) Prepare(query string) (driver.Stmt, error) {
	id := c.log.start("Prepare", query, nil)
	stmt, err := c.Conn.Prepare(query)
	c.log.finish(id, "Prepare", query, err)
	if err != nil {
		return nil, err
	}
	return &loggedStmt{Stmt: stmt, query: query, log: c.log}, nil
}

func (c *loggedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *loggedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	id := c.log.start(fmt.Sprintf("BeginTx(isolation=%d, readOnly=%t)", opts.Isolation, opts.ReadOnly), "", nil)
	var tx driver.Tx
	var err error
	if base, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = base.BeginTx(ctx, opts)
	} else if opts.Isolation != 0 || opts.ReadOnly {
		err = fmt.Errorf("driver does not support requested transaction options")
	} else if err = ctx.Err(); err == nil {
		tx, err = c.Conn.Begin()
		if err == nil && ctx.Err() != nil {
			rollbackErr := tx.Rollback()
			c.log.finish(id, "RollbackAfterCanceledBegin", "", rollbackErr)
			err = ctx.Err()
		}
	}
	c.log.finish(id, "BeginTx", "", err)
	if err != nil {
		return nil, err
	}
	return &loggedTx{Tx: tx, log: c.log, id: id}, nil
}

func (c *loggedConn) Ping(ctx context.Context) error {
	id := c.log.start("Ping", "", nil)
	err := ctx.Err()
	if base, ok := c.Conn.(driver.Pinger); ok {
		err = base.Ping(ctx)
	}
	c.log.finish(id, "Ping", "", err)
	return err
}

func (c *loggedConn) CheckNamedValue(value *driver.NamedValue) error {
	if base, ok := c.Conn.(driver.NamedValueChecker); ok {
		err := base.CheckNamedValue(value)
		if err != nil && err != driver.ErrSkip && err != driver.ErrRemoveArgument {
			id := c.log.start("CheckNamedValue", "", nil)
			c.log.finish(id, "CheckNamedValue", "", err)
		}
		return err
	}
	return driver.ErrSkip
}

func (c *loggedConn) ResetSession(ctx context.Context) error {
	if base, ok := c.Conn.(driver.SessionResetter); ok {
		err := base.ResetSession(ctx)
		if err != nil {
			id := c.log.start("ResetSession", "", nil)
			c.log.finish(id, "ResetSession", "", err)
		}
		return err
	}
	return nil
}

func (c *loggedConn) IsValid() bool {
	if base, ok := c.Conn.(interface{ IsValid() bool }); ok {
		return base.IsValid()
	}
	return true
}

func values(args []driver.NamedValue) ([]driver.Value, error) {
	result := make([]driver.Value, len(args))
	for i, arg := range args {
		if arg.Name != "" {
			return nil, fmt.Errorf("driver does not support named parameters")
		}
		result[i] = arg.Value
	}
	return result, nil
}

func (c *loggedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	id := c.log.start("Exec", query, args)
	var result driver.Result
	var err error
	if base, ok := c.Conn.(driver.ExecerContext); ok {
		result, err = base.ExecContext(ctx, query, args)
	} else if base, ok := c.Conn.(driver.Execer); ok {
		var positional []driver.Value
		if positional, err = values(args); err == nil {
			if err = ctx.Err(); err == nil {
				result, err = base.Exec(query, positional)
			}
		}
	} else {
		c.log.finish(id, "Exec", query, driver.ErrSkip)
		return nil, driver.ErrSkip
	}
	c.log.finish(id, "Exec", query, err)
	if err != nil || result == nil {
		return result, err
	}
	return &loggedResult{Result: result, log: c.log, id: id, query: query}, nil
}

func (c *loggedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	id := c.log.start("Query", query, args)
	var rows driver.Rows
	var err error
	if base, ok := c.Conn.(driver.QueryerContext); ok {
		rows, err = base.QueryContext(ctx, query, args)
	} else if base, ok := c.Conn.(driver.Queryer); ok {
		var positional []driver.Value
		if positional, err = values(args); err == nil {
			if err = ctx.Err(); err == nil {
				rows, err = base.Query(query, positional)
			}
		}
	} else {
		c.log.finish(id, "Query", query, driver.ErrSkip)
		return nil, driver.ErrSkip
	}
	c.log.finish(id, "Query", query, err)
	if err != nil || rows == nil {
		return rows, err
	}
	return &loggedRows{Rows: rows, log: c.log, id: id, query: query}, nil
}

type loggedTx struct {
	driver.Tx
	log *DriverLog
	id  uint64
}

func (t *loggedTx) Commit() error {
	err := t.Tx.Commit()
	t.log.finish(t.id, "Commit", "", err)
	return err
}

func (t *loggedTx) Rollback() error {
	err := t.Tx.Rollback()
	t.log.finish(t.id, "Rollback", "", err)
	return err
}

type loggedResult struct {
	driver.Result
	log   *DriverLog
	id    uint64
	query string
}

func (r *loggedResult) RowsAffected() (int64, error) {
	count, err := r.Result.RowsAffected()
	r.log.finish(r.id, "RowsAffected", r.query, err)
	return count, err
}

func (r *loggedResult) LastInsertId() (int64, error) {
	id, err := r.Result.LastInsertId()
	r.log.finish(r.id, "LastInsertId", r.query, err)
	return id, err
}

type loggedRows struct {
	driver.Rows
	log   *DriverLog
	id    uint64
	query string
}

func (r *loggedRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err != nil && err != io.EOF {
		r.log.finish(r.id, "Rows.Next", r.query, err)
	}
	return err
}

func (r *loggedRows) Close() error {
	err := r.Rows.Close()
	r.log.finish(r.id, "Rows.Close", r.query, err)
	return err
}

func (r *loggedRows) HasNextResultSet() bool {
	base, ok := r.Rows.(driver.RowsNextResultSet)
	return ok && base.HasNextResultSet()
}

func (r *loggedRows) NextResultSet() error {
	if base, ok := r.Rows.(driver.RowsNextResultSet); ok {
		err := base.NextResultSet()
		r.log.finish(r.id, "NextResultSet", r.query, err)
		return err
	}
	return io.EOF
}

func (r *loggedRows) ColumnTypeDatabaseTypeName(index int) string {
	if base, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return base.ColumnTypeDatabaseTypeName(index)
	}
	return ""
}

func (r *loggedRows) ColumnTypeLength(index int) (int64, bool) {
	if base, ok := r.Rows.(driver.RowsColumnTypeLength); ok {
		return base.ColumnTypeLength(index)
	}
	return 0, false
}

func (r *loggedRows) ColumnTypeNullable(index int) (bool, bool) {
	if base, ok := r.Rows.(driver.RowsColumnTypeNullable); ok {
		return base.ColumnTypeNullable(index)
	}
	return false, false
}

func (r *loggedRows) ColumnTypePrecisionScale(index int) (int64, int64, bool) {
	if base, ok := r.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return base.ColumnTypePrecisionScale(index)
	}
	return 0, 0, false
}

func (r *loggedRows) ColumnTypeScanType(index int) reflect.Type {
	if base, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return base.ColumnTypeScanType(index)
	}
	return reflect.TypeOf((*interface{})(nil)).Elem()
}

type loggedStmt struct {
	driver.Stmt
	log   *DriverLog
	query string
}

func (s *loggedStmt) Close() error {
	id := s.log.start("Stmt.Close", s.query, nil)
	err := s.Stmt.Close()
	s.log.finish(id, "Stmt.Close", s.query, err)
	return err
}

func (s *loggedStmt) ColumnConverter(index int) driver.ValueConverter {
	if base, ok := s.Stmt.(driver.ColumnConverter); ok {
		return base.ColumnConverter(index)
	}
	return driver.DefaultParameterConverter
}

func (s *loggedStmt) Exec(args []driver.Value) (driver.Result, error) {
	id := s.log.start("Stmt.Exec", s.query, args)
	result, err := s.Stmt.Exec(args)
	s.log.finish(id, "Stmt.Exec", s.query, err)
	if err != nil || result == nil {
		return result, err
	}
	return &loggedResult{Result: result, log: s.log, id: id, query: s.query}, nil
}

func (s *loggedStmt) Query(args []driver.Value) (driver.Rows, error) {
	id := s.log.start("Stmt.Query", s.query, args)
	rows, err := s.Stmt.Query(args)
	s.log.finish(id, "Stmt.Query", s.query, err)
	if err != nil || rows == nil {
		return rows, err
	}
	return &loggedRows{Rows: rows, log: s.log, id: id, query: s.query}, nil
}

func (s *loggedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if base, ok := s.Stmt.(driver.StmtExecContext); ok {
		id := s.log.start("Stmt.Exec", s.query, args)
		result, err := base.ExecContext(ctx, args)
		s.log.finish(id, "Stmt.Exec", s.query, err)
		if err != nil || result == nil {
			return result, err
		}
		return &loggedResult{Result: result, log: s.log, id: id, query: s.query}, nil
	}
	argsWithoutNames, err := values(args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Exec(argsWithoutNames)
}

func (s *loggedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if base, ok := s.Stmt.(driver.StmtQueryContext); ok {
		id := s.log.start("Stmt.Query", s.query, args)
		rows, err := base.QueryContext(ctx, args)
		s.log.finish(id, "Stmt.Query", s.query, err)
		if err != nil || rows == nil {
			return rows, err
		}
		return &loggedRows{Rows: rows, log: s.log, id: id, query: s.query}, nil
	}
	argsWithoutNames, err := values(args)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Query(argsWithoutNames)
}
