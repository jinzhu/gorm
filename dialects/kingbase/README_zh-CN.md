# GORM V1 KingbaseES 适配说明

[English](README.md) | 简体中文

## 1. 适配边界

本项目为 GORM V1 提供 KingbaseES 方言：

- 应用统一使用 `kingbase` 方言。方言在连接初始化时执行 `SHOW database_mode`，并根据结果加载 MySQL 或 Oracle 模式实现。
- 模式查询失败或返回未知值时不加载任一模式方言，不根据连接失败情况猜测或切换模式。
- `gokb` 应从电科金仓官方网站下载，并与目标数据库版本及兼容模式保持一致。
- 模式识别由统一方言在连接初始化阶段完成，应用不需要再次查询或手工选择模式。

当前验证基线：

| 模式 | 数据库版本 |
| --- | --- |
| MySQL | KingbaseES `V009R003C018` |
| Oracle | KingbaseES `V009R002C016` |

GORM 仓库只使用根目录的 `go.mod/go.sum`，不引入 Kingbase 私有驱动或专用测试依赖。Kingbase 测试源码由 `kingbase_test` 构建标签控制，默认构建不解析 `gokb`。MySQL 和 Oracle 模式的验证分别使用对应版本的本地驱动，具体路径通过不提交的临时 modfile 配置。

## 2. gokb 驱动配置

`gokb` 是 KingbaseES 提供的 Go 数据库驱动，实现了标准的 `database/sql` 接口。本项目仅提供 GORM V1 KingbaseES 方言，不包含或重新发布 `gokb` 源码。

### 2.1 获取驱动

应用应访问[电科金仓官网下载中心](https://www.kingbase.com.cn/download.html)，下载与目标 KingbaseES 版本、客户端操作系统及兼容模式相匹配的 `gokb` 驱动。MySQL 模式和 Oracle 模式应分别使用对应版本的驱动，不应混用。

### 2.2 配置本地依赖

将驱动放置在客户端的固定目录，并在应用工程的 `go.mod` 中配置本地路径。Linux 示例：

```go
require kingbase.com/gokb v0.0.0

replace kingbase.com/gokb => /opt/kingbase/gokb
```

Windows 示例：

```go
require kingbase.com/gokb v0.0.0

replace kingbase.com/gokb => D:/KingbaseES/gokb
```

`replace` 后的路径应指向 `gokb` 模块所在目录。本地 `replace` 配置属于应用工程的部署配置，不应提交到 GORM 官方仓库。

### 2.3 使用边界

- 应用需要匿名导入 `kingbase.com/gokb`，由驱动完成 `database/sql` 注册。
- GORM 方言统一使用 `kingbase` 名称，并在连接初始化阶段识别数据库兼容模式。
- `kingbase` 是应用可使用的方言名称；MySQL、Oracle 模式名称只用于包内实现，不属于公开接入接口。
- 方言不会自动下载、安装或切换 `gokb`，应用必须使用与目标实例配套的驱动。
- 未导入 `gokb` 时，连接会返回 `sql: unknown driver "kingbase"`。
- 未使用 Kingbase 方言的应用不需要引入 `gokb`，现有 MySQL、PostgreSQL 等方言的依赖和连接方式保持不变。
- `gokb` 的授权、分发和技术支持由 KingbaseES 提供。

## 3. 使用方式

```go
import (
	"github.com/jinzhu/gorm/dialects/kingbase"
	_ "kingbase.com/gokb"
)

db, err := kingbase.Open(
	"host=127.0.0.1 port=54331 user=system password=*** dbname=test sslmode=disable get_last_insert_id=yes",
)
```

推荐使用 `kingbase.Open`。该入口内部仍调用 GORM V1 的公开 `gorm.Open`，但会在返回连接前检查统一方言的 `DialectInitializationError()`：如果 `SHOW database_mode` 查询失败或返回不支持的模式，入口会关闭本次连接并直接返回可诊断错误。

`kingbase.Open` 还会自动安装元数据错误跟踪连接。GORM V1 的 `HasTable/HasColumn/HasIndex/HasForeignKey` 只能返回 `bool`；如果底层元数据查询失败，跟踪连接会保存第一个原始错误，并阻止 `AutoMigrate` 根据错误的 `false` 结果继续执行 DDL。迁移或元数据操作后可通过 `kingbase.MetadataError(db)` 获取原始错误：

```go
result := db.AutoMigrate(&Account{})
if err := kingbase.MetadataError(result); err != nil {
	return err
}
if result.Error != nil {
	return result.Error
}
```

元数据跟踪通过 `gorm.SQLCommon` 包装器实现。需要设置连接池参数时，使用 `kingbase.SQLDB(db)` 取得原始 `*sql.DB`，不要直接调用 GORM V1 的 `db.DB()`：

```go
sqlDB, err := kingbase.SQLDB(db)
if err != nil {
	return err
}
sqlDB.SetMaxOpenConns(20)
```

为兼容已有应用，直接调用 `gorm.Open("kingbase", ...)` 仍受支持，但 GORM V1 原始 `Open` 不会读取方言初始化结果，也不会安装元数据错误跟踪连接。继续使用该方式的应用必须在执行 CRUD 前检查 `db.Dialect()` 是否实现以下接口，并处理返回错误；此时 `kingbase.MetadataError(db)` 会返回 `kingbase.ErrMetadataTrackingUnavailable`：

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

`kingbase.Open` 的参数与 `gorm.Open` 中方言名之后的参数一致：通常传入 DSN；测试或已有连接场景也可以传入实现 `gorm.SQLCommon` 的连接。若传入已有连接且模式初始化失败，该连接同样会被关闭。

MySQL 模式必须设置 `get_last_insert_id=yes`。普通 `Create` 沿用 GORM V1 原始回调；如果配套驱动因自增主键不是表的第一列而未返回 ID，Kingbase 回调会在同一事务中执行 `SELECT last_insert_id()` 完成回填。

`Set("gorm:insert_modifier", "IGNORE").Create(...)` 使用单独的 Kingbase MySQL Create 路径，因为当前配套 `gokb` 的 `INSERT IGNORE` 不能通过 `LastInsertId` 获取生成值。该路径保留 GORM 的事务、Hook、关联、默认值、`RowsAffected` 及 SQL trace 日志语义。

## 4. MySQL R3 支持范围

- 实现 GORM V1 `Dialect` 接口的全部方法。
- 反引号和内部反引号转义、`?` 参数绑定、schema 限定名称。
- 直接使用 R3 原生 `TINYINT/SMALLINT/MEDIUMINT/INT/BIGINT UNSIGNED` 与 `AUTO_INCREMENT`。
- 无符号类型保持 R3 原生定义，不生成适配层 CHECK，也不使用 `serial/bigserial` 代替 R3 原生类型。
- 通过 `information_schema` 和 `pg_indexes` 检查表、列、索引和外键。
- `RemoveIndex` 生成 R3 支持的 `DROP INDEX 索引名`，不生成 MySQL 的 `ON 表名` 后缀。
- `ModifyColumn` 生成原生 `ALTER TABLE ... MODIFY COLUMN ...`。
- 支持 `LIMIT`、`OFFSET` 及单独 `OFFSET`。
- 根据实例 `max_identifier_length` 缩短自动生成的索引和约束名。
- 支持索引前缀、特殊索引名和 schema 限定索引。
- 支持 `Group/Having/Count` 与软删除组合计数。
- 支持普通 `Create` 下非第一列自增主键回填。
- `INSERT IGNORE` 使用独立 Create 路径，并与配套 `gokb` 的 `RETURNING` 行为兼容。

## 5. 已知限制

以下内容不属于当前 GORM V1 方言适配范围：

- `db.Exec` 提交的原始 SQL 不会经过方言改写。原始 `DROP INDEX ... ON 表`、`DROP CHECK`、`DROP FOREIGN KEY` 应按 R3 实际语法修改。
- GORM V1 没有 CHECK 约束迁移接口，因此不增加私有 `DropCheck` 扩展。
- `LastInsertId` 的 Go 返回类型是 `int64`；超过 `MaxInt64` 的完整 `uint64` 自增回填不在当前适配范围内。非自增 `BIGINT UNSIGNED` 可按 R3 原生范围读写。
- Oracle R2 的原生 Identity 使用有符号序列，`uint64` 自增主键只保证到 `MaxInt64`；不增加偏移序列、范围约束等私有完整范围扩展。非自增 `NUMBER(20)` 可手动读写完整 `uint64` 范围。
- `AutoMigrate` 按 GORM V1 原生行为逐条执行 DDL，不保证整组迁移原子提交；失败时此前成功的 DDL 可能保留，生产迁移应使用独立迁移脚本或专用迁移工具。
- Oracle R2 索引迁移沿用 GORM V1 原生语义：`HasIndex` 判断后执行创建或删除，不增加创建后的索引定义反查和严格校验。
- R3 的 ENUM 成员大小写规则可能不接受仅大小写不同的成员，应在需求文档中记录。
- GORM V1 原生 MySQL 对“非主键 AUTO_INCREMENT 并依赖建表后再创建索引”的模型存在建表顺序边界，不在 Kingbase 方言中增加私有迁移扩展。
- GORM V1 原生复杂回调排序等框架行为不通过修改 GORM 主干处理。

## 6. Oracle R2 适配

Oracle 模式使用独立方言，主要覆盖：

- 双引号标识符和 schema 限定名称。
- 未超出边界的定长字符串使用 `varchar2`，未指定长度或超长字符串使用 `text`。
- 普通字节数据使用 `bytea`，UUID/GUID 和 `json.RawMessage` 分别使用 `uuid`、`jsonb`。
- 仅在字段显式指定时使用 `clob/blob` 大对象类型。
- `number/timestamp/identity` 等 Oracle 兼容类型。
- `FETCH FIRST`、`OFFSET ... FETCH NEXT` 分页。
- `ALTER TABLE ... MODIFY ...`。
- `information_schema/pg_indexes` 元数据查询。
- `DUAL` 和 `RETURNING` 主键回填。

Oracle R2 的 `uint64` 边界与 GORM V1 原生方言保持一致：非自增字段映射为 `NUMBER(20)`，可手动写入完整范围；自增主键使用原生 `BIGINT IDENTITY`，只保证到 `MaxInt64`。不增加偏移序列、范围约束等完整 `uint64` 自动分配扩展。

字段注释迁移使用 `NewOracleMigrator`；该扩展只属于 Oracle 模式：

```go
kb.NewOracleMigrator(db).AutoMigrate(&Account{}).Error
```

## 7. 测试

仓库内不保存 Kingbase 专用 modfile 或本机驱动路径。在 GORM 根目录从公共模块生成仅供本机使用的 modfile，并指向与目标模式配套的 `gokb`：

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

`KINGBASE_GOKB_DIR` 必须是本机 `gokb` 模块的绝对路径。切换 MySQL、Oracle 模式时，应重新生成 `go.kingbase.local.mod` 并将该变量指向相应模式的驱动目录。`go.kingbase.local.mod/go.kingbase.local.sum` 只用于本机验证，不进入提交内容。

离线单元测试：

```bash
go vet ./dialects/kingbase
go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly \
  ./dialects/kingbase/test/unit ./dialects/kingbase/test/testutil
go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly -race \
  ./dialects/kingbase/test/unit
go vet -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly \
  ./dialects/kingbase/...
```

MySQL R3 真实库测试：

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

Oracle R2 真实库测试：

```bash
export KINGBASE_ORACLE_DSN='host=127.0.0.1 port=54332 user=system password=*** dbname=test sslmode=disable'
export REQUIRE_KINGBASE_DB=1

go test -tags=kingbase_test -modfile=go.kingbase.local.mod -mod=readonly -v -count=1 \
  ./dialects/kingbase/test/integration \
  -run '^TestKingbaseOracle'
```

`testutil.Open` 在未设置 `REQUIRE_KINGBASE_DB=1` 时允许连接失败后跳过，因此正式验收必须设置该变量。MySQL DSN 未显式设置 `get_last_insert_id` 时，测试入口会自动补上 `yes`。

## 8. 目录职责

```text
dialects/
└── kingbase/              # KingbaseES 方言实现
    ├── README.md           # 英文使用、验证与目录说明
    ├── README_zh-CN.md     # 中文使用、验证与目录说明
    └── test/
        ├── unit/            # SQL Mock 与公开接口测试
        ├── integration/     # 真实数据库功能与适配验收
        ├── regression/      # GORM V1 行为回归
        ├── negative/        # 错误传播与异常场景
        ├── nonfunctional/   # 并发、性能和稳定性
        └── testutil/        # 测试连接、日志和公共辅助
```

生产适配文件：

| 文件 | 职责 |
| --- | --- |
| `dialect.go` | 模式常量、方言注册、构造入口和名称解析 |
| `dialect_mysql.go` | MySQL `Dialect` 方法、分页、DDL 和自动名称 |
| `dialect_mysql_types.go` | 复用 GORM MySQL 类型并保留 R3 原生定义 |
| `dialect_mysql_metadata.go` | MySQL 模式元数据查询 |
| `dialect_mysql_callbacks.go` | 普通 Create 主键兜底及 `INSERT IGNORE` |
| `dialect_callbacks.go` | 两个模式共用的分组计数修补 |
| `dialect_oracle.go` | Oracle 方言接口、类型、分页和 DDL |
| `dialect_oracle_metadata.go` | Oracle 模式元数据查询 |
| `metadata.go` | 元数据错误跟踪和底层 `*sql.DB` 获取 |
| `oracle_bind.go` | Oracle `:name` 命名绑定变量构造与名称校验 |
| `oracle_migration.go` | Oracle 专项迁移入口 |
