package kingbase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/jinzhu/gorm"
)

// ErrMetadataTrackingUnavailable 表示连接未通过 Open 创建，因而无法取得仅返回 bool 的
// 元数据查询接口所丢失的原始错误。
var ErrMetadataTrackingUnavailable = errors.New("kingbase: metadata error tracking is unavailable; open the connection with kingbase.Open")

// ErrSQLDBUnavailable 表示当前 GORM 句柄不包含 *sql.DB，例如该句柄正处于事务中。
var ErrSQLDBUnavailable = errors.New("kingbase: underlying *sql.DB is unavailable")

// metadataErrorState 在 GORM 派生句柄和方言克隆之间共享，并保证并发读取安全。
type metadataErrorState struct {
	mu  sync.RWMutex
	err error
}

func (s *metadataErrorState) report(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	// 只保存第一个错误，避免后续派生错误覆盖真正的元数据查询根因。
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *metadataErrorState) current() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

// metadataTrackingDB 保存首个元数据错误，并阻止 GORM V1 的 Has* 方法把查询错误转换为
// false 后继续执行 DDL。
type metadataTrackingDB struct {
	gorm.SQLCommon
	state *metadataErrorState
}

// newMetadataTrackingDB 为现有 SQLCommon 创建一份新的首错状态。
func newMetadataTrackingDB(db gorm.SQLCommon) *metadataTrackingDB {
	return &metadataTrackingDB{SQLCommon: db, state: &metadataErrorState{}}
}

func (db *metadataTrackingDB) ReportMetadataError(err error) {
	db.state.report(err)
}

func (db *metadataTrackingDB) MetadataError() error {
	return db.state.current()
}

func (db *metadataTrackingDB) Exec(query string, args ...interface{}) (sql.Result, error) {
	// 元数据状态不可靠时禁止继续写入，避免 AutoMigrate 根据错误的“不存在”结论建表。
	if err := db.MetadataError(); err != nil {
		return nil, err
	}
	return db.SQLCommon.Exec(query, args...)
}

func (db *metadataTrackingDB) Prepare(query string) (*sql.Stmt, error) {
	// Prepare 也可能成为 AutoMigrate 写入入口，因此与 Exec 使用相同阻断规则。
	if err := db.MetadataError(); err != nil {
		return nil, err
	}
	return db.SQLCommon.Prepare(query)
}

func (db *metadataTrackingDB) Begin() (*sql.Tx, error) {
	// 写回调通常先开启事务，必须在事务创建前检查元数据错误，避免后续绕过包装器。
	if err := db.MetadataError(); err != nil {
		return nil, err
	}
	if beginner, ok := db.SQLCommon.(interface{ Begin() (*sql.Tx, error) }); ok {
		return beginner.Begin()
	}
	return nil, gorm.ErrCantStartTransaction
}

func (db *metadataTrackingDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	// 保留支持上下文的 database/sql 事务入口，并复用相同的首错阻断状态。
	if err := db.MetadataError(); err != nil {
		return nil, err
	}
	if beginner, ok := db.SQLCommon.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	}); ok {
		return beginner.BeginTx(ctx, opts)
	}
	return nil, gorm.ErrCantStartTransaction
}

func (db *metadataTrackingDB) Close() error {
	// 只有底层连接本身支持关闭时才转发，外部 SQLCommon 不强制假设具体实现。
	if closer, ok := db.SQLCommon.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return errors.New("can't close current db")
}

// MetadataError 返回连接记录的第一个元数据查询错误；nil 表示尚未发生元数据查询失败。
// 直接通过 gorm.Open 创建的连接返回 ErrMetadataTrackingUnavailable。
func MetadataError(db *gorm.DB) error {
	if db == nil {
		return ErrMetadataTrackingUnavailable
	}
	tracker, ok := db.CommonDB().(interface{ MetadataError() error })
	if !ok {
		return ErrMetadataTrackingUnavailable
	}
	return tracker.MetadataError()
}

// SQLDB 返回通过 Open 创建的连接所使用的底层 *sql.DB。由于 Open 会安装用于跟踪
// 元数据错误的 SQLCommon 包装器，配置连接池时应使用本函数而不是 gorm.DB.DB。
func SQLDB(db *gorm.DB) (*sql.DB, error) {
	if db == nil {
		return nil, ErrSQLDBUnavailable
	}
	connection := db.CommonDB()
	if tracker, ok := connection.(*metadataTrackingDB); ok {
		connection = tracker.SQLCommon
	}
	if sqlDB, ok := connection.(*sql.DB); ok {
		return sqlDB, nil
	}
	return nil, ErrSQLDBUnavailable
}

// openWithMetadataTracking 对齐 gorm.Open 的常用参数形式，并在连接下方安装共享观察器。
// 函数只关闭自己通过 sql.Open 创建的连接；调用方传入连接的正常生命周期仍由调用方管理。
func openWithMetadataTracking(args ...interface{}) (*gorm.DB, error) {
	if len(args) == 0 {
		return nil, errors.New("invalid database source")
	}

	var (
		connection gorm.SQLCommon
		own        bool
	)
	switch value := args[0].(type) {
	case string:
		// 单字符串按 DSN 处理；双字符串保持与 gorm.Open(driver, source) 一致。
		driver := DialectKingbase
		source := value
		if len(args) >= 2 {
			driver = value
			var ok bool
			source, ok = args[1].(string)
			if !ok {
				return nil, fmt.Errorf("invalid database source: %v is not a valid type", args[1])
			}
		}
		sqlDB, err := sql.Open(driver, source)
		if err != nil {
			return nil, err
		}
		connection = sqlDB
		own = true
	case gorm.SQLCommon:
		connection = value
	default:
		return nil, fmt.Errorf("invalid database source: %v is not a valid type", value)
	}

	tracker, ok := connection.(*metadataTrackingDB)
	if !ok {
		// 已包装的连接直接复用同一状态，保证 GORM clone 后仍能读取首个错误。
		tracker = newMetadataTrackingDB(connection)
	}
	db, err := gorm.Open(DialectKingbase, tracker)
	if err != nil && own {
		_ = tracker.Close()
	}
	return db, err
}
