# Chronos 测试资产基线修复与复测说明

日期：`2026-09-14`

对应需求：`/home/ti-chronos-strategy/docs/RELAY-TEST-ASSET-BASELINE-REQUIREMENT-20260914.md`

## 结论

Chronos 在 `19:11 Asia/Shanghai` 观测到的资产 HTTP 500 已修复。故障不在 OC：测试 OC 当时能够回复查询，但测试 PostgreSQL 只应用到 schema 24，Relay 新版资产 SQL 已读取 migration 28 增加的 `reverse_repo_receivable`，因此底层查询报缺列。

测试库现已应用 migration 25-28，当前 schema 与程序一致。环境切换脚本也改为先对目标数据库执行幂等迁移，迁移成功后才停止当前服务和切换环境，避免再次进入“API 已切换但目标账本 schema 落后”的半完成状态。

## 当前验收证据

当前服务仍为 `test`，账户 `00030484` 的查询和交易路由启用，生产配置未修改。

| 检查项 | 结果 | request/message ID |
| --- | --- | --- |
| OC 资金刷新 | `completed`，唯一终态，成功落库 | `msg-asset-query-1789385138464428374-1` |
| OC 持仓刷新 | 10 条持仓、11 个 reply 分片、唯一 completed 终态 | `msg-positions-query-1789385142902454774-2` |
| 原始资产读取 | HTTP 200，`account_id=00030484`，更新时间 `19:25:38+08:00` | `relay-1789385328487562538-10` |
| 默认资产读取 | HTTP 200，现金、证券市值和净资产完整 | `relay-1789385334143708693-12` |
| 当前持仓读取 | HTTP 200，10 条且账户 ID 一致 | `relay-1789385194208139555-25` |
| 当日订单读取 | HTTP 200，10 条，活动订单 0 | `relay-1789385201056086034-27` |
| 当日成交读取 | HTTP 200，0 条 | `relay-1789385207919539480-29` |

默认资产的本轮关键数值为：

```text
cash_available = 45,275,336.221000
cash_total     = 45,275,336.221000
market_value   =  4,249,320.500000
net_asset      = 49,524,656.721000
```

所有金额均可按既有契约用 `Decimal(str(value)) + ROUND_HALF_UP` 无损归一为六位微元。`enrich=false` 明确返回 OC 原始可见资金，不加入展示层持仓估值；Chronos 基线使用的默认 `/asset` 返回现金加当前持仓市值，两者均只读取测试 PostgreSQL，不会回退生产事实。

## 错误契约

资产读取现可区分三类失败，并通过 `/v1/schema` 的 `assets.readiness_errors.v1` 能力显式声明：

| HTTP / code | 含义 | 调用方行为 |
| --- | --- | --- |
| `404 NOT_FOUND` | 账户路由不存在 | 修正账户或环境配置，不重试 |
| `503 ASSET_NOT_READY` | 账户已配置，但当前环境尚无资产快照 | 确认 OC ready，执行资产刷新后有限重试 |
| `500 INTERNAL` | PostgreSQL 查询、schema 或服务内部故障 | 停止创建风险，交由 Relay 运维修复 |

底层数据库错误只写服务日志，不在公共响应中泄露。已新增以上三种分支的 API 单测、migration 28 字段契约测试及 SDK 金额 JSON 六位微元往返测试。

## Chronos 复测入口

Chronos 可以按原需求中的顺序重新执行 GET-only 准入和 Direct 两子单闭环。测试 OC 当前 `redis_ready=true`、`broker_ready=true`、`order_snapshot_ready=true`、`accepting_trade_commands=true`；盘后显示的 `off_hours` 只是 Relay 监控阶段，不代表 OC 未 ready。
