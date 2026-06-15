# relay 绩效分析页面设计

更新时间：`2026-06-15`

## 定位

`/trade#performance` 后续定位为“日终复盘 + 交易归因”工作区，而不是实时交易辅助图或单纯盘后对账页。

它回答三类问题：

1. 当日账户到底赚亏多少，和基准相比如何。
2. 赚亏来自哪些持仓、成交、费用和异常订单。
3. 当前结果是否可信，结算、对账、终态订单和缺失字段有没有风险。

盘后对账不再作为单独页面入口存在。结算快照、对账输入和差异记录仍保留在后台任务、API Console 和数据质量面板里，作为绩效分析可信度的一部分展示。

## 设计原则

1. 实盘边界清晰：绩效分析只读取 relay 本地账本、日终快照、后台任务结果和 Meridian 行情，不主动查询柜台。
2. 数据口径可追溯：每个核心指标都能追到 `asset_snapshots`、`position_snapshots`、`fills`、`orders`、`reconciliation_*` 或 Meridian `bars`。
3. 估算必须显式标记：如果前置/柜台未提供最终口径，relay 可以展示估算值，但页面必须标明 `estimated` 或 `missing`，不能伪装成券商最终账单。
4. 行情字段以 Meridian 为准：基准、收盘价、证券类型、证券名称等字段不在 relay 内另造标准。
5. 先支持账户级日终复盘，再扩展到多日归因、策略分组和更精细成本引擎。

## 数据来源

| 数据 | 来源 | 用途 |
| --- | --- | --- |
| 上日收盘资产 | `asset_snapshots(snapshot_type=close)` 的上一交易日记录 | 隔夜调整参考基准 |
| 日初资产 | `asset_snapshots(snapshot_type=open)`；由 `pre_open_init` 在盘前刷新后写入 | 当日交易收益率和贡献 bps 的优先分母 |
| 日终资产 | `asset_snapshots(snapshot_type=close)` | close 净资产、现金、证券市值、日内盈亏、收益率主线 |
| 日终持仓 | `position_snapshots` | 持仓市值、权重、浮动盈亏、收盘持仓贡献 |
| 当前持仓 | `positions` | 当天尚未结算时的临时查看口径，不作为历史绩效最终口径 |
| 成交账本 | `fills` | 买卖金额、费用、成交数量、成交时间分布、按证券贡献估算 |
| 委托账本 | `orders` | 下单数、成交率、撤单率、拒单率、未终态订单和异常订单 |
| 任务运行 | `job_runs` | 盘前初始化、收盘结算是否完成，运行耗时和错误摘要 |
| 对账批次 | `reconciliation_runs` | 目标交易日是否完成对账/结算 |
| 对账差异 | `reconciliation_breaks` | 未终态订单、订单成交数量不一致、快照缺失、刷新失败等质量问题 |
| Meridian bars | `/v1/meridian/market/bars` | 基准收益、收盘价参考、后续持仓估值补充 |
| Meridian instruments | `/v1/meridian/metadata/instruments` | 证券名称、证券类型、ETF/股票分类和价格精度 |

## 核心指标

### 账户总览

| 指标 | 口径 |
| --- | --- |
| 期末净资产 | 当日 close `net_asset` |
| 上日净资产 | 上一交易日 close `net_asset` |
| 日初净资产 | 当日 open `net_asset`；缺失时显示兜底来源和 `missing_open_asset` 标记 |
| 隔夜调整 | `open_net_asset(today) - close_net_asset(previous_trading_day)`，用于识别逆回购回款、清算入账、占款释放、资金划转等非日内交易因素 |
| 资产变动 | `close_net_asset(today) - close_net_asset(previous_trading_day)`，只作为资产桥展示，不直接等同日内交易收益 |
| 日内盈亏 | `close_net_asset(today) - open_net_asset(today)`；缺少 open 快照时才兜底为 `close - previous_close` 并标明估算 |
| 日内收益率 | `intraday_pnl / open_net_asset(today)` |
| 累计收益率 | 区间内净值序列累计收益 |
| 最大回撤 | 区间净值高点到低点的最大回撤 |
| 基准收益 | Meridian `bars` 生成的基准 close 收益序列 |
| 超额收益 | 账户累计收益率减基准累计收益率 |
| 成交额 | `sum(fill.price * fill.qty)` |
| 买入金额 | `sum(fill.price * fill.qty where trade_side=B)` |
| 卖出金额 | `sum(fill.price * fill.qty where trade_side=S)` |
| 手续费 | `sum(fill.fee)`，缺字段时尝试读取 `adapter_context.fee/nFee` |
| 数据质量 | 结算任务状态、对账差异数量、未终态订单数量、估算字段数量 |

### PnL 分解

第一版沿用 P8 已落地的研究侧口径：

```text
realized_pnl = settled_profit
gross_pnl = realized_pnl + unrealized_pnl
net_pnl = gross_pnl - fee_total
```

说明：

1. `settled_profit` 来自前置/柜台或资产快照字段，优先级高于 relay 自行估算。
2. 如果 `settled_profit` 缺失，页面可以展示 `estimated_realized_pnl`，但必须标明估算。
3. `unrealized_pnl` 优先读取日终持仓快照；缺字段时可以用 Meridian close 与成本价估算，并标明估算。
4. 后续精确版本需要引入成本引擎，覆盖 FIFO/移动加权、分红派息、除权除息、逆回购、ETF 申赎和特殊费用。

### 持仓贡献

持仓贡献表第一版按证券聚合，建议字段：

| 字段 | 说明 |
| --- | --- |
| `security_id` | Meridian 标准证券代码，例如 `600000.SH` |
| `name` | Meridian 证券名称 |
| `instrument_type` | Meridian 证券类型 |
| `prev_qty` | 上一交易日 close 持仓 |
| `close_qty` | 当日 close 持仓 |
| `buy_qty` | 当日买入成交数量 |
| `sell_qty` | 当日卖出成交数量 |
| `avg_cost` | 日终持仓成本价；缺失时标明 |
| `close_price` | 当日 Meridian close 或日终快照现价 |
| `market_value` | 日终市值 |
| `weight` | `market_value / account_net_asset` |
| `realized_pnl` | 已实现贡献，第一版可为空或估算 |
| `unrealized_pnl` | 浮动贡献 |
| `fee` | 该证券成交费用 |
| `net_contribution` | `realized_pnl + unrealized_pnl - fee` |
| `contribution_bps` | 优先使用 `net_contribution / open_net_asset * 10000`；缺少日初资产时才兜底上一 close 净资产并标记 |
| `quality_flags` | `estimated_cost`、`missing_close_price`、`missing_settled_profit` 等 |

### 交易质量

| 指标 | 说明 |
| --- | --- |
| 委托数 | 当日订单总数 |
| 成交数 | 当日成交总笔数 |
| 成交订单数 | `cum_filled_qty > 0` 的订单数 |
| 撤单数 | `status=cancelled` 的订单数 |
| 拒单数 | `status=rejected` 或前置 `failed/rejected` 回包 |
| 成交率 | 成交订单数 / 委托数 |
| 撤单率 | 撤单数 / 委托数 |
| 拒单率 | 拒单数 / 委托数 |
| 未终态订单 | 非 `filled/cancelled/rejected` 的订单 |
| 异常订单 | 有 `reject_message`、柜台错误、数量不一致或状态冲突的订单 |
| 分钟分布 | 按成交时间聚合成交额、成交笔数和买卖方向 |

## 页面结构

### 顶部过滤区

1. 账户选择。
2. 日期或日期范围。
3. 基准证券选择，默认使用上证指数 `000001.SH`，用户可临时切换其他指数或标的。
4. 刷新按钮。
5. CSV 导出。
6. 数据状态标签：`settled`、`estimated`、`breaks`、`missing`。

### KPI 条

第一屏展示高密度账户指标：

1. 期末净资产。
2. 日初净资产。
3. 隔夜调整。
4. 日内盈亏 / 日内收益率。
5. 区间累计收益。
6. 最大回撤。
7. 基准收益 / 超额收益。
8. 成交额。
9. 手续费。
10. 对账差异数量。

### 主图

主图不再使用分钟 K 线。分钟 K 线保留在“交易测试”页面，负责手工下单点位理解。

绩效分析主图采用 tab：

1. 净值曲线：账户净值与基准净值。
2. 超额收益：账户累计收益减基准累计收益。
3. 回撤曲线：账户回撤和基准回撤。
4. PnL 分解：已实现、浮动、费用、净收益的 waterfall 或堆叠柱。

### 贡献表

持仓/交易贡献表放在主图下方，默认按 `net_contribution` 绝对值排序。

表格需要支持：

1. 按证券代码/名称搜索。
2. 按股票/ETF/其他类型过滤。
3. 按贡献、权重、成交额、费用排序。
4. 展开行查看该证券当日成交明细和订单异常。

### 交易质量区

展示：

1. 委托状态分布。
2. 成交时间分布。
3. 买入/卖出金额分布。
4. 拒单和异常订单列表。
5. 需要人工复核的订单/成交差异。

### 数据质量面板

展示：

1. `post_close_settlement` 是否在目标交易日完成。
2. `pre_open_init` 是否在目标交易日 08:55 之后完成，并是否写入日初资产。
3. `reconciliation_runs` 状态。
4. `reconciliation_breaks` 未处理数量。
5. 日初资产、日终资产和持仓快照时间。
6. 未终态订单数量。
7. 缺失字段和估算字段清单，尤其是 `missing_open_asset`、`open_asset_fallback` 和 `overnight_adjustment_unclassified`。
8. Meridian bars 是否命中目标交易日。

## 日初资产与隔夜调整

绩效页需要把“隔夜清算/资金变化”和“日内交易收益”分开。原因包括：

1. 逆回购到期回款会在开盘前增加可用资金或净资产。
2. 隔夜清算、费用、利息、红利、占款释放、资金划转等会让当日日初资产不同于上一日日终资产。
3. 如果直接用上一日日终资产计算当日收益，会把这些非日内交易因素混入策略绩效。

建议字段和口径：

| 字段 | 口径 |
| --- | --- |
| `previous_close_net_asset` | 上一交易日 close 资产快照的 `net_asset` |
| `open_net_asset` | 当日 `pre_open_init` 后写入的 open 资产快照 `net_asset` |
| `close_net_asset` | 当日 close 资产快照 `net_asset` |
| `overnight_adjustment` | `open_net_asset - previous_close_net_asset` |
| `asset_change` | `close_net_asset - previous_close_net_asset` |
| `intraday_pnl` | `close_net_asset - open_net_asset` |
| `intraday_return` | `intraday_pnl / open_net_asset` |
| `open_snapshot_source` | `open`、`first_intraday_after_pre_open`、`previous_close_fallback` 等 |
| `quality_flags` | `missing_open_asset`、`estimated_open_asset`、`overnight_adjustment_unclassified` 等 |

展示建议：

1. KPI 区同时展示“上日收盘”“日初资产”“隔夜调整”“日终资产”“日内盈亏”。
2. 净值曲线仍可使用 close-to-close 保持长期连续性，但单日收益解释优先使用 open-to-close。
3. 收益贡献、贡献 bps 和当日交易绩效优先以 `open_net_asset` 为分母。
4. 隔夜调整大于阈值时，在数据质量面板中提示人工复核，并在 tooltip 展示可能原因：逆回购回款、现金划转、清算入账、利息/红利等。
5. 后续若能从柜台或资金流水拿到明确 cash flow 分类，再把 `overnight_adjustment` 分解为 `reverse_repo_repayment`、`cash_transfer`、`settlement_adjustment`、`interest_dividend`、`fee_tax_adjustment` 等。

## 接口规划

第一版页面尽量复用现有接口：

| 能力 | 接口 |
| --- | --- |
| 日绩效 | `GET /v1/accounts/{account_id}/performance/daily` |
| 区间绩效 | `GET /v1/accounts/{account_id}/performance/series` |
| CSV 导出 | `GET /v1/accounts/{account_id}/performance/series.csv` |
| 历史订单 | `GET /v1/history/orders` |
| 历史成交 | `GET /v1/history/fills` |
| 历史持仓 | `GET /v1/accounts/{account_id}/positions/history` |
| 对账差异 | `GET /v1/reconciliations/breaks` |
| 任务状态 | `GET /v1/status`、`GET /v1/jobs/runs` |
| 基准行情 | `GET /v1/meridian/market/bars` |
| 证券主数据 | `GET /v1/meridian/metadata/instruments` |

如果前端聚合过重，再新增只读聚合接口：

```text
GET /v1/accounts/{account_id}/performance/contributions
```

建议返回：

1. `summary`：账户 KPI 和数据质量摘要。
2. `positions`：按证券聚合的贡献表。
3. `trading_quality`：订单/成交质量统计。
4. `quality_flags`：缺失、估算和对账差异。

该接口只读，只使用本地账本和 Meridian，不查询柜台。

## 分阶段推进

### Phase 1 文档设计

状态：`done`

产出当前文档，明确页面定位、指标口径、数据来源和第一版边界。

### Phase 2 UI 重构

使用现有 performance、history、reconciliation 和 Meridian 接口重排 `/trade#performance`：

1. 将当前 close/open 快照和表格整理成 KPI + 净值主图 + 数据质量面板，明确展示日初资产、隔夜调整和日内盈亏。
2. 将分钟 K 线从绩效页完全移除，只保留在交易测试页。
3. 增加估算/缺失字段提示。
4. 保留 CSV 下载。

### Phase 3 贡献聚合

视前端复杂度新增 `performance/contributions` 只读接口：

1. 后端聚合成交额、费用、订单状态和持仓贡献。
2. 前端贡献表和交易质量区直接读取聚合结果。
3. 增加 Go 单元测试覆盖聚合口径。

### Phase 4 精确成本引擎

在前置/柜台字段和账本数据足够后推进：

1. 明确已实现盈亏字段来源。
2. 补现金流水和成本调整。
3. 支持 FIFO 或移动加权成本。
4. 处理逆回购、ETF 申赎、分红派息、除权除息。
5. 给研究侧导出 view 增加 v2 版本，避免破坏 v1。

## 第一版边界

1. 不接入实时 level2、trades、orders 或 order-queues。
2. 不主动查询柜台，避免绩效页影响实盘柜台压力。
3. 不把估算盈亏当成券商最终账单。
4. 不在 relay 中新增行情字段标准，所有行情字段继续以 Meridian 为准。
5. 不在第一版实现完整 FIFO/移动加权成本引擎。
6. 不把回测模拟撮合逻辑放进 relay。

## 验收口径

页面第一版完成时，需要能明确回答：

1. 指定账户在指定交易日或区间的净资产、盈亏、收益率和回撤是多少。
2. 相对基准的超额收益是多少。
3. 哪些证券贡献了主要收益或亏损。
4. 当天成交额、手续费、撤单、拒单和异常订单情况如何。
5. 盘后结算是否完成，对账差异是否为 0，哪些字段是估算或缺失。
6. 所有结果都可以通过本地账本、任务记录、对账记录和 Meridian 数据追溯。
