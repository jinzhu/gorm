# GORM V1 KingbaseES Dialect

English | [简体中文](README_zh-CN.md)

## 1. Scope

This package provides a KingbaseES dialect for GORM V1:

- Applications use the single `kingbase` dialect. During connection initialization, the dialect runs `SHOW database_mode` and selects the MySQL-mode or Oracle-mode implementation.
- If mode detection fails or returns an unknown value, no mode implementation is loaded. The dialect never guesses or switches modes based on later connection errors.
- Obtain `gokb` from the official KingbaseES distribution and use the driver version that matches both the target database release and compatibility mode.
- Applications do not need to query the mode again or select a mode-specific dialect manually.

Validated baselines:

| Mode | Database version |
| --- | --- |
| MySQL | KingbaseES `V009R003C018` |
| Oracle | KingbaseES `V009R002C016` |

The GORM repository keeps only its root `go.mod/go.sum`. It does not add a private KingbaseES driver or test-only dependency to the committed module files. KingbaseES tests use the `kingbase_test` build tag, so the default build does not resolve `gokb`. MySQL-mode and Oracle-mode verification use their corresponding local driver packages through an uncommitted temporary modfile.

## 2. Configuring the gokb driver

`gokb` is the KingbaseES Go driver and implements the standard `database/sql` interfaces. This package provides only the GORM V1 dialect; it does not include or redistribute `gokb` source code.

### 2.1 Obtaining the driver

Download the `gokb` package that matches the target KingbaseES version, operating system, and compatibility mode from the [official KingbaseES download center](https://www.kingbase.com.cn/download.html). MySQL mode and Oracle mode require their corresponding driver packages and must not share an incompatible build.

### 2.2 Configuring a local dependency

Place the driver in a stable local directory and configure that location in the application's `go.mod`.

Linux example:

```go
require kingbase.com/gokb v0.0.0

replace kingbase.com/gokb => /opt/kingbase/gokb
```

Windows example:

```go
require kingbase.com/gokb v0.0.0

replace kingbase.com/gokb => D:/KingbaseES/gokb
```

The replacement target must be the `gokb` module directory. A local `replace` directive is application deployment configuration and should not be committed to the upstream GORM repository.

### 2.3 Driver boundaries

- Applications must import `kingbase.com/gokb` anonymously so the driver can register itself with `database/sql`.
- Use `kingbase` as the GORM dialect name. The package detects the compatibility mode during connection initialization.
- Mode-specific names are internal implementation details and are not public application entry points.
- The dialect does not download, install, or switch `gokb`. The application must provide the driver that matches its target instance.
- If `gokb` is not imported, opening a connection returns `sql: unknown driver "kingbase"`.
- Applications that do not use the KingbaseES dialect do not need `gokb`; existing MySQL, PostgreSQL, and other dialect dependencies remain unchanged.
- KingbaseES provides the driver license, distribution, and technical support.

## 3. Usage

```go
import (
	"github.com/jinzhu/gorm/dialects/kingbase"
	_ "kingbase.com/gokb"
)

db, err := kingbase.Open(
	"host=127.0.0.1 port=54331 user=system password=*** dbname=test sslmode=disable get_last_insert_id=yes",
)
```

`kingbase.Open` is the recommended entry point. It still uses the public GORM V1 `gorm.Open` API internally, but checks `DialectInitializationError()` before returning the connection. If `SHOW database_mode` fails or returns an unsupported mode, it closes the new connection and returns a diagnostic error.

`kingbase.Open` also installs metadata error tracking. GORM V1's `HasTable`, `HasColumn`, `HasIndex`, and `HasForeignKey` methods return only `bool`. When an underlying metadata query fails, the tracking connection stores the first original error and prevents `AutoMigrate` from issuing DDL based on an incorrect `false` result. Retrieve the original error after migration or metadata operations with `kingbase.MetadataError`:

```go
result := db.AutoMigrate(&Account{})
if err := kingbase.MetadataError(result); err != nil {
	return err
}
if result.Error != nil {
	return result.Error
}
```

Metadata tracking uses a `gorm.SQLCommon` wrapper. Configure connection pool settings through `kingbase.SQLDB` instead of calling GORM V1's `db.DB()` directly:

```go
sqlDB, err := kingbase.SQLDB(db)
if err != nil {
	return err
}
sqlDB.SetMaxOpenConns(20)
```

Direct `gorm.Open("kingbase", ...)` calls remain supported for existing applications, but the original GORM V1 `Open` implementation does not read dialect initialization errors or install metadata tracking. Applications that retain this form must check the following interface before issuing CRUD operations. In this case, `kingbase.MetadataError(db)` returns `kingbase.ErrMetadataTrackingUnavailable`.

```go
type dialectInitializer interface {
	DialectInitializationError() error
}

if initializer, ok := db.Dialect().(dialectInitializer); ok {
	if err := initializer.DialectInitializationError(); err != nil {
		_ = db.Close()
		return err
	}
}
```

Arguments to `kingbase.Open` match the arguments after the dialect name in `gorm.Open`: normally a DSN, or an existing `gorm.SQLCommon` connection in tests and connection-wrapping scenarios. If initialization fails for an existing connection, that connection is also closed.

MySQL mode requires `get_last_insert_id=yes`. Ordinary `Create` operations keep the original GORM V1 callback. If the matching driver does not return an ID when the auto-increment primary key is not the first physical column, the KingbaseES callback runs `SELECT last_insert_id()` in the same transaction and backfills the model.

`Set("gorm:insert_modifier", "IGNORE").Create(...)` uses a dedicated KingbaseES MySQL Create path because the current matching `gokb` cannot expose generated values for `INSERT IGNORE` through `LastInsertId`. This path retains GORM transaction, Hook, association, default-value, `RowsAffected`, and SQL trace behavior.

## 4. MySQL R3 support

- Implements every method in the GORM V1 `Dialect` interface.
- Supports backtick quoting and escaping, `?` binds, and schema-qualified names.
- Uses native R3 `TINYINT/SMALLINT/MEDIUMINT/INT/BIGINT UNSIGNED` and `AUTO_INCREMENT` definitions.
- Preserves native unsigned types without adapter CHECK constraints or `serial/bigserial` substitutions.
- Reads tables, columns, indexes, and foreign keys from `information_schema` and `pg_indexes`.
- Generates the R3-supported `DROP INDEX index_name` form without the MySQL `ON table_name` suffix.
- Generates native `ALTER TABLE ... MODIFY COLUMN ...` statements.
- Supports `LIMIT`, `OFFSET`, and offset-only pagination.
- Shortens generated index and constraint names using the instance `max_identifier_length`.
- Supports prefix indexes, special index names, and schema-qualified indexes.
- Supports combined `Group`, `Having`, `Count`, and soft-delete queries.
- Backfills a non-first auto-increment primary key after an ordinary `Create`.
- Uses a dedicated Create path for `INSERT IGNORE` that is compatible with the matching `gokb` `RETURNING` behavior.

## 5. Known limitations

The following items are outside the current GORM V1 dialect scope:

- Raw SQL submitted through `db.Exec` is not rewritten by the dialect. Adjust raw `DROP INDEX ... ON table`, `DROP CHECK`, and `DROP FOREIGN KEY` statements to the actual R3 syntax.
- GORM V1 has no CHECK-constraint migration API, so this package does not add a private `DropCheck` extension.
- Go's `LastInsertId` return type is `int64`; complete auto-increment backfill beyond `MaxInt64` for `uint64` is not supported. A non-auto-increment `BIGINT UNSIGNED` field can still use the native R3 range.
- Oracle R2 native identity sequences are signed. Auto-increment `uint64` primary keys are guaranteed only through `MaxInt64`; the dialect does not add an offset sequence or private full-range allocation scheme. A non-auto-increment `NUMBER(20)` field can store the full `uint64` range manually.
- `AutoMigrate` executes DDL statements one at a time according to native GORM V1 behavior and does not guarantee an atomic migration group. Previously successful DDL may remain after a failure; use a dedicated migration script or tool for production migrations.
- Oracle R2 index migration keeps GORM V1 semantics: it checks `HasIndex` and then creates or drops the index. It does not add post-create definition introspection or strict validation.
- R3 ENUM member rules may reject members that differ only by letter case; document this constraint in the application requirements.
- Native GORM V1 MySQL has a table-creation ordering boundary for a non-primary `AUTO_INCREMENT` field whose index is created later. The KingbaseES dialect does not add a private migration extension for this framework behavior.
- Native GORM V1 framework behavior, including complex callback ordering, is not changed in the GORM core.

## 6. Oracle R2 support

Oracle mode uses an independent dialect implementation and includes:

- Double-quoted identifiers and schema-qualified names.
- `varchar2` for bounded strings within the supported length; `text` for unspecified or oversized strings.
- `bytea` for ordinary byte data, `uuid` for UUID/GUID values, and `jsonb` for `json.RawMessage`.
- `clob/blob` only when explicitly requested by the field definition.
- Oracle-compatible `number`, `timestamp`, and `identity` types.
- `FETCH FIRST` and `OFFSET ... FETCH NEXT` pagination.
- `ALTER TABLE ... MODIFY ...`.
- Metadata queries through `information_schema` and `pg_indexes`.
- `DUAL` and `RETURNING` primary-key backfill.

For `uint64`, Oracle R2 follows the GORM V1 dialect boundary: a non-auto-increment field maps to `NUMBER(20)` and can store the full range manually; an auto-increment primary key uses native `BIGINT IDENTITY` and is guaranteed only through `MaxInt64`. No offset sequence, range constraint, or private full-range allocation extension is added.

Use `NewOracleMigrator` when field-comment migration is required. This extension is available only in Oracle mode:

```go
kb.NewOracleMigrator(db).AutoMigrate(&Account{}).Error
```

## 7. Testing

The repository does not store a KingbaseES-specific modfile or a local driver path. From the GORM root, generate a local-only modfile that points to the `gokb` package matching the target mode:

```bash
cd /path/to/gorm-master
cp go.mod go.kingbase.local.mod
cp go.sum go.kingbase.local.sum
export KINGBASE_GOKB_DIR=/opt/kingbase/gokb
go mod edit -modfile=go.kingbase.local.mod \
  -require=github.com/DATA-DOG/go-sqlmock@v1.5.0 \
  -require=github.com/golang-sql/civil@v0.0.0-20190719163853-cb61b32ac6fe \
  -require=github.com/shopspring/decimal@v1.4.0 \
  -require=kingbase.com/gokb@v0.0.0
go mod edit -modfile=go.kingbase.local.mod \
  -replace=kingbase.com/gokb="$KINGBASE_GOKB_DIR"
go mod download -modfile=go.kingbase.local.mod all
```

`KINGBASE_GOKB_DIR` must be the absolute path of the local `gokb` module. Regenerate `go.kingbase.local.mod` and point the variable to the matching driver directory when switching between MySQL and Oracle modes. `go.kingbase.local.mod/go.kingbase.local.sum` are local verification files and must not be committed.

Offline unit tests:

```bash
go vet ./dialects/kingbase
go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly \
  ./dialects/kingbase/test/unit ./dialects/kingbase/test/testutil
go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly -race \
  ./dialects/kingbase/test/unit
go vet -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly \
  ./dialects/kingbase/...
```

MySQL R3 real-database tests:

```bash
export KINGBASE_MYSQL_DSN='host=127.0.0.1 port=54331 user=system password=*** dbname=test sslmode=disable get_last_insert_id=yes'
export REQUIRE_KINGBASE_DB=1

go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly -v -count=1 \
  ./dialects/kingbase/test/integration \
  -run '^TestKingbaseMySQLMode_FullIntegration$'

go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly -v -count=1 \
  ./dialects/kingbase/test/integration \
  -run '^TestKingbaseMySQLMode_NormalCreateNonFirstPrimaryKey$'

go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly -v -count=1 \
  ./dialects/kingbase/test/integration \
  -run '^TestKingbaseMySQLMode_InsertIgnoreAdapter$'
```

Oracle R2 real-database tests:

```bash
export KINGBASE_ORACLE_DSN='host=127.0.0.1 port=54332 user=system password=*** dbname=test sslmode=disable'
export REQUIRE_KINGBASE_DB=1

go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly -v -count=1 \
  ./dialects/kingbase/test/integration \
  -run '^TestKingbaseOracle'
```

Without `REQUIRE_KINGBASE_DB=1`, `testutil.Open` may skip a test after a connection failure. Formal acceptance runs must set this variable. If a MySQL DSN omits `get_last_insert_id`, the test helper adds `yes` automatically.

## 8. Directory responsibilities

```text
dialects/
└── kingbase/                 # KingbaseES dialect implementation
    ├── README.md              # English usage and verification guide
    ├── README_zh-CN.md        # Chinese usage and verification guide
    └── test/
        ├── unit/              # SQL mock and public API tests
        ├── integration/       # Real-database functional acceptance tests
        ├── regression/        # GORM V1 behavior regression tests
        ├── negative/          # Error propagation and failure scenarios
        ├── nonfunctional/     # Concurrency, performance, and resilience
        └── testutil/          # Shared connections, logging, and helpers
```

Production files:

| File | Responsibility |
| --- | --- |
| `dialect.go` | Mode constants, dialect registration, unified connection entry point, and identifier parsing |
| `dialect_mysql.go` | MySQL-mode `Dialect` methods, pagination, DDL, and generated names |
| `dialect_mysql_types.go` | GORM MySQL type reuse while retaining native R3 definitions |
| `dialect_mysql_metadata.go` | MySQL-mode metadata queries |
| `dialect_mysql_callbacks.go` | Ordinary Create ID fallback and `INSERT IGNORE` handling |
| `dialect_callbacks.go` | Grouped-count handling shared by both modes |
| `dialect_oracle.go` | Oracle-mode dialect interface, types, pagination, and DDL |
| `dialect_oracle_metadata.go` | Oracle-mode metadata queries |
| `metadata.go` | Metadata error tracking and underlying `*sql.DB` access |
| `oracle_bind.go` | Oracle `:name` bind creation and name validation |
| `oracle_migration.go` | Oracle-specific migration entry point |
