# relay PostgreSQL Migration

更新时间：`2026-06-13`

## 当前状态

已新增 PostgreSQL 首批账本 migration：

```text
migrations/postgres/000001_init_ledger.up.sql
migrations/postgres/000001_init_ledger.down.sql
```

文件命名采用 `golang-migrate` / `goose` 常见的 `version_name.up.sql`、`version_name.down.sql` 形式，但 SQL 本身保持工具无关。部署阶段可以用 `psql`、`golang-migrate`、`goose` 或内部发布脚本执行。

当前仓库不保存真实 PostgreSQL DSN。连接方式仍从部署机本地配置或 `http://doc.quantstage.com` 获取。

已在内网 PostgreSQL 上创建专用数据库 `relay_trader`，并通过 `relayctl migrate status/up/status` 验证首版 migration 已应用。验证结果：

1. `000001_init_ledger` 已应用。
2. `relay_schema_migrations` 已记录版本 `1:init_ledger`。
3. 当前 public schema 下共有 15 张基础表。

当前环境已安装 PostgreSQL client：

```bash
psql --version
```

也已新增 Go 版 migration runner：

```bash
go run ./cmd/relayctl migrate status
go run ./cmd/relayctl migrate up
go run ./cmd/relayctl migrate down -steps 1
```

runner 会创建 `relay_schema_migrations` 表记录已应用版本。真实 DSN 可通过 `-database-url`、`RELAY_DATABASE_URL` 或 `config.database.dsn` 提供。

当前也已新增 Go 账本写入 repository：

```text
internal/ledger
```

首批覆盖：

1. `UpsertAccount`
2. `UpsertOrder`
3. `AppendOrderEvent`
4. `InsertFill`
5. `ArchiveRawStreamMessage`

这些入口会把标准交易结构体、stream key、stream id、source/correlation 信息和原始 payload 写入 PostgreSQL。重复消费场景使用唯一约束和 `ON CONFLICT` 做幂等处理。

可选集成测试：

```bash
RELAY_LEDGER_TEST_DATABASE_URL="$RELAY_DATABASE_URL" go test ./internal/ledger -run TestRepositoryWritesToPostgres -count=1 -v
```

该测试默认跳过；设置测试库 DSN 后会写入一组临时账户、订单、事件、成交和原始 stream 消息，并在测试清理阶段删除。

## 覆盖表

配置与路由：

1. `gateways`
2. `accounts`
3. `account_gateway_routes`

交易账本：

1. `orders`
2. `order_events`
3. `fills`
4. `raw_stream_messages`

账户账表：

1. `positions`
2. `position_snapshots`
3. `asset_snapshots`
4. `cash_ledger`

盘后对账：

1. `reconciliation_runs`
2. `reconciliation_inputs`
3. `reconciliation_breaks`

## 关键约束

1. `orders(account_id, gateway_order_id)` 唯一，用作订单跨系统主键。
2. `fills(account_id, fill_id)` 在 `fill_id` 存在时唯一。
3. 如果 `fill_id` 缺失，`fills` 使用 `account_id + order_stream_id + match_timestamp + qty + price` 作为 fallback 去重。
4. `order_events` 和 `fills` 对 `stream_key + stream_id` 做唯一约束，避免重复消费写入。
5. `raw_stream_messages` 归档每条 Redis Stream 原始消息，保留 `body`、`body_text` 和 `parse_error`。
6. 金额和价格字段使用 `numeric(20, 6)`，避免浮点误差进入最终账本。
7. 时间字段统一使用 `timestamptz`，原始柜台时间戳保留在 raw 或 adapter 字段。

## 手动执行示例

以下命令仅用于本地或部署机执行，不要把真实 DSN 写入仓库：

```bash
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000001_init_ledger.up.sql
```

使用 relayctl：

```bash
RELAY_DATABASE_URL="$RELAY_DATABASE_URL" go run ./cmd/relayctl migrate up
```

回滚：

```bash
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000001_init_ledger.down.sql
```

使用 relayctl 回滚最近一步：

```bash
RELAY_DATABASE_URL="$RELAY_DATABASE_URL" go run ./cmd/relayctl migrate down -steps 1
```

## 后续工作

1. 将 `config.database.dsn` 接入 API 模式启动检查。
2. 增加 `GET /v1/status` 的数据库状态。
3. 将 Redis Stream `reply/event` 消费接入 `internal/ledger` repository。
4. 增加基于临时 PostgreSQL 的 CI 集成测试。
