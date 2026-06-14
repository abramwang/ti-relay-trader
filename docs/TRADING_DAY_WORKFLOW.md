# relay 交易日流程

更新时间：`2026-06-14`

## 时间口径

relay 的业务时间统一使用 `Asia/Shanghai`，即东八区 UTC+8。A 股没有夏令时，后续文档、配置、页面、API 业务字段、报表和 cron 调度都应使用这个时区，不使用容易歧义的三字母时区缩写。

数据库建议继续使用 PostgreSQL `timestamptz` 保存绝对时刻，API 和报表展示时再转换为 `Asia/Shanghai`。Redis 原始消息、前置原始时间戳和 Meridian 原始响应仍保留在 raw 字段，便于审计和回放。

`trade_date` 按 `Asia/Shanghai` 下的 A 股交易日确定。交易日判断和最近交易日回退以 Meridian 的交易日接口为准；如果当天不是交易日，行情查询使用最近一个交易日，交易任务默认跳过写入型流程，只允许显式指定 `target_trade_date` 的只读检查或补数任务。

## 每日主流程

relay 每个交易日需要两个稳定流程：

| 流程 | 建议任务名 | 建议窗口 | 目标 |
| --- | --- | --- | --- |
| 盘前初始化 | `pre_open_init` | 08:25-09:20 `Asia/Shanghai` | 确认交易日、依赖、账户、昨夜位点、初始资金持仓和风险基线 |
| 收盘后结算 | `post_close_settlement` | 15:45-16:30 `Asia/Shanghai` | 追平回报、刷新终态账本、生成日终快照、对账和盈亏输入 |

具体时间可按托管机房、前置程序和券商柜台可用窗口调整，但配置和日志都必须明确是 `Asia/Shanghai`。

## 盘前初始化

`pre_open_init` 的建议步骤：

1. 从 Meridian 交易日接口解析 `target_trade_date`；非交易日默认跳过写入型初始化。
2. 检查 `/v1/status`，确认 PostgreSQL、Redis、事件流、行情代理、订单服务和自动刷新处于可用状态。
3. 检查 `stream_checkpoints`，追赶前一晚遗留的 `reply/event/hb/dlq`，避免开盘后先处理历史积压。
4. 加载账户路由，确认 `enabled`、`trading_enabled`、`broker_id`、`gateway_id`、`stream_prefix` 与当日运行计划一致。
5. 对每个启用账户执行资金、持仓、订单、成交查询刷新，将柜台当前状态合并到 PostgreSQL 账本。
6. 校验前一交易日仍未终态的订单；如仍有 working 状态，标记为盘前异常，交由人工确认或前置补充查询。
7. 建立当日风险基线：可用资金、可卖持仓、昨仓、冻结资金、冻结持仓、标的价格精度和涨跌停参考数据。
8. 写入任务运行记录，记录交易日、账户数、依赖状态、刷新命令回执、异常摘要和完成时间。

盘前初始化不应主动发送交易委托。它只做依赖检查、账本追平、账户基线和风险输入准备。

## 收盘后结算

`post_close_settlement` 的建议步骤：

1. 进入收盘后窗口后停止策略侧新增交易，或将账户切换为只读/人工确认状态。
2. 等待 Redis `reply/event` 流短时间稳定，并持续消费到最新 checkpoint。
3. 对每个启用账户重新查询资金、持仓、订单和成交，确保本地账本与柜台终态对齐。
4. 将订单状态更新到终态；仍未终态的订单写入异常列表，供人工复核。
5. 写入 `asset_snapshots`、`position_snapshots` 和必要的 `cash_ledger` 日终流水。
6. 生成对账输入：柜台查询快照、Redis 原始消息摘要、relay 标准账本摘要。
7. 运行盘后对账，记录 `reconciliation_runs`、`reconciliation_inputs` 和 `reconciliation_breaks`。
8. 为盈亏统计准备输入：日终权益、持仓市值、成交金额、费用、已实现盈亏和浮动盈亏。
9. 输出结算报告，并把任务状态暴露给 `/v1/status` 或后续运维页面。

收盘后结算可以拆分为多个 Python job，但外部状态上应能看到一个完整的 `post_close_settlement` 批次。

## 配置建议

示例配置：

```yaml
service:
  timezone: "Asia/Shanghai"

jobs:
  pre_open_init:
    enabled: true
    schedule: "25 8 * * 1-5"
  post_close_settlement:
    enabled: true
    schedule: "45 15 * * 1-5"
```

cron 部署时建议设置：

```cron
CRON_TZ=Asia/Shanghai
TZ=Asia/Shanghai
```

后续如果迁移到 systemd timer、Airflow 或其他调度器，也必须显式设置 `Asia/Shanghai`，不要依赖主机默认时区。

## 后续落地项

1. 检查订单/成交/资金/持仓账本 API 的历史时间字段展示是否全部转换为 `Asia/Shanghai`。
2. 将 `/v1/status` 扩展出 `trade_date`、`trading_phase`、`pre_open_init` 和 `post_close_settlement` 状态。
3. 实现 `python -m relay.jobs.pre_open_init`。
4. 实现 `python -m relay.jobs.post_close_settlement`，并把盘后对账和盈亏输入纳入该批次。
5. 增加任务运行表或复用对账批次表，保存任务开始时间、结束时间、结果、异常摘要和触发方式。
