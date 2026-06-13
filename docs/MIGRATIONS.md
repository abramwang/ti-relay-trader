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

回滚：

```bash
psql "$RELAY_DATABASE_URL" -f migrations/postgres/000001_init_ledger.down.sql
```

## 后续工作

1. 增加数据库连接和 migration runner。
2. 将 `config.database.dsn` 接入启动检查。
3. 增加 `GET /v1/status` 的数据库状态。
4. 增加订单、成交、事件写入 repository。
5. 增加基于临时 PostgreSQL 的集成测试。
