# relay 绩效分析页面设计

更新时间：`2026-08-01`

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

## 2026-07-26 实盘数据审计与计算暂停点

N8 已完成数据审计、第一版计算口径和绩效工作区落地。历史快照仍保持原始输入不被覆盖，经济净值、估算贡献和质量标记继续使用独立版本化结果；现场推断不会反写为 OC 或 Meridian 协议。

### 数据覆盖

| 账户 | close 快照 | open 快照 | 成交 | transfer.event |
| --- | ---: | ---: | ---: | ---: |
| `501000114077` | 28，`2026-06-15` 至 `2026-07-24` | 27 | 7,793 | 4,399 |
| `314000046830` | 28，`2026-06-15` 至 `2026-07-24` | 27 | 11,299 | 0 |
| `314000045768` | 24，`2026-06-18` 至 `2026-07-24` | 24 | 28,901 | 9,367 |

三账户合计已有 47,993 条成交和 4,598 条日终持仓快照。数据量足以建设页面和质量监控，但尚不足以直接把现有字段解释为正式绩效。

### 已确认的数据事实

1. 三账户共检查到 376 个非空 `asset_page.account` 回包，`market_value` 全为 0，`net_asset` 全部等于 `cash_total`，且均未提供 `commission/day_profit/stock_value/fund_value`。
2. `post_close_settlement` 写 open/close 快照时调用内部 `GetAsset()`，没有复用 `GET /asset` 的持仓市值补全。因此现有 `asset_snapshots.net_asset` 主要是资金余额，不是可直接使用的账户经济净资产。
3. 现有成交中有 126 笔逆回购。回报 `price` 是年化利率，不能用 `price * qty` 作为成交额；上交所现行规则和生产资金桥均确认 `204001.SH` 首次结算价为 100 元，本金按 `qty * 100` 计算。
4. 现有成交中有 188 笔 `P/R` 申购赎回记录。ETF 赎回主记录常见 `price=1`，该价格不代表赎回资产价值，不能计入普通卖出金额。
5. 2026-07-29 按 OC v1.1 完成历史重放后，共识别 14,795 条 `transfer.event/fill_page.component_transfers[]`。这些记录独立写入 `etf_component_transfers`，不再混入普通成交；`component_value=null` 按原义保留，不擅自换算为 0。
6. 三账户 47,993 条成交的 `fee` 全为 0；`cash_ledger` 当前为 0 行；日终持仓 `settled_profit` 当前也全部为 0。现有 `realized_pnl/net_pnl/fee_total` 不能作为正式口径。
7. open 快照当前只保存资产，不保存日初持仓。当 `asset_page` 不包含证券市值时，无法仅用现有 open 快照准确重建日初经济净资产。
8. 当前基准序列从查询区间首日 close 开始，而账户累计收益优先从区间首日前一 close 净资产开始，首日收益窗口并未完全对齐。

### 当前实现为何不能直接上线为绩效

当前页面同时展示三套尚未统一的数值：

```text
close-to-close = close_net_asset - previous_close_net_asset
open-to-close  = close_net_asset - open_net_asset
derived_pnl    = settled_profit + day_unrealized_pnl - fee_total
```

其中 open/close `net_asset` 当前常常只是资金余额；逆回购本金会在成交日离开资金余额，ETF 申赎会产生证券划转和待结算现金，费用和外部现金流又没有账表。因此这些公式可能把正常占款、资产转换或回款误判为巨额盈亏。

### 需要共同确认的目标口径

绩效计算建议拆成三层，不用一个公式同时承担所有解释：

1. **正式账户收益率**：以完整经济净资产为主，使用经过外部现金流修正的时间加权收益率。
2. **日内资产桥**：解释上一 close、当日 open、外部现金流、逆回购/申赎待结算和当日 close 之间的变化。
3. **证券贡献与交易归因**：按普通股票/ETF 二级市场、逆回购、ETF 申赎、费用和其它调整分别计算。

正式计算前需要继续确认或落实：

1. OC 后续若能提供完整总资产/净资产、证券市值、冻结资金、逆回购在途资产、ETF 待结算款、手续费和当日盈亏，可作为更高优先级的对账来源，但不再作为第一版绩效的阻塞条件。
2. OC 暂不提供完整资金流水时，Relay 使用人工维护的资金流水账区分入金、出金、柜台间划转、红利、利息、费用、逆回购回款和清算调整；后续 OC 增加接口时仍落入同一标准账表。
3. `204001.SH` 逆回购由 Relay 按交易所规则、OC 成交和 Meridian 交易日历派生；后续 OC 提供实际应计利息或费用时，以实际值对账并替换估算来源。
4. ETF 申赎 T0 使用已经确认的 IOPV + 15bp 估算口径；OC 后续补充现金差额或清算价值时只提高对账精度。
5. 完整字段到位前允许展示 `provisional` 估算净值；T+1 校正且差异通过阈值后转为 `finalized`，只有 `finalized` 进入正式累计收益、回撤和基准比较。

以上口径已于 `2026-07-26` 确认，N8 可以按“输入层先行、归因层分步落地”的方式推进。当前仍不能把现有 `asset_snapshots.net_asset` 直接标记为正式绩效；正式经济净值应写入独立的版本化结果表，原始柜台快照继续保留为对账输入。

### 第一批实现状态

已落地第一批绩效输入和逆回购归因底座：

1. `000009_performance_accounting` 新增 `performance_fee_rules`、`performance_nav_baselines`、`performance_nav_versions`、`performance_nav_reconciliations` 和 `reverse_repo_accruals`，并扩展 `cash_ledger` 的流水分类、资金仓位、发生时间、确认/作废、成对划转、幂等键和审计字段。
2. `/v1/performance/settings` 返回经济净值公式版本、自动/人工对账阈值和 `performance.settings_write_enabled`。生产默认保持只读，人工设置写入接口在开关关闭时统一返回 `403`。
3. `/v1/performance/fee-rules`、`/v1/accounts/{account_id}/cash-ledger` 和 `/v1/accounts/{account_id}/performance/baselines` 提供费率、手工资金流水和日初经济净值基线的查询/新增接口。
4. `/v1/accounts/{account_id}/performance/reverse-repo` 从成交账本预览 `204001.SH` 应计利息；`/reverse-repo/rebuild` 可在写入开关开启时重建并落库；`/reverse-repo/accruals` 查询已落库结果。
5. `/trade#performance-settings` 新增绩效设置工作区，展示配置状态、费率规则、手工资金流水、日初经济净值和逆回购估算结果。该页面是运营/研究侧人工输入入口，不主动查询柜台。
6. `/v1/accounts/{account_id}/performance/economic-nav/preview` 已提供只读 economic NAV 试算；`/economic-nav/rebuild` 在写入开关开启时写入新的 current NAV 版本和 same-day NAV 对账记录。

下一步是在上述输入层和 NAV 容器基础上实现 ETF 申赎 T0、股票截面、ETF 截面的策略归因分量；T+1 observed open assets 更新、人工确认流程和页面告警展示已经形成第一版闭环。

### 第二批实现状态

已落地策略归因和交易日业务键底座：

1. `000010_strategy_attribution_keys` 为 `orders/order_events/fills` 增加 `trade_date`、`strategy_type`、`strategy_id`、`basket_id`、`parent_order_id` 和 `t0_order_group_id`；`fills` 额外保存 `business_type`。
2. `000012_trade_date_order_scope` 已将 `orders/fills/order_events` 的唯一键、外键和 upsert 冲突目标统一为 `account_id + trade_date + gateway_order_id`；生产原始流已完成重放和外键验证。
3. `performance_attribution_links` 作为可追溯链接表，后续把订单、成交、ETF 成分划转、持仓、现金流水和 NAV 分量连接到策略归因结果。
4. `SubmitOrderRequest`、Redis order/fill 解析、HTTP 查询过滤和 Python SDK `0.1.12` 已支持策略归因字段。未来新策略单应显式携带这些字段；历史订单仍可由归因任务推断后写入链接表。

## 2026-07-22 至 2026-07-24 三类策略交易事实样本

本节只根据三个连续交易日的订单事件、成交、`transfer.event`、日终持仓和 Meridian PCF 记录交易事实，不定义盈利公式。

### 2026-07-24 账户概览

| 账户 | 委托 | 成交 | 当日可识别策略 | 日终持仓 |
| --- | ---: | ---: | --- | --- |
| `501000114077` | 256 | 342 | ETF 日内买入赎回并卖出成分股 | 0 |
| `314000046830` | 0 | 0 | 当日无交易 | 0 |
| `314000045768` | 854 | 1,144 | ETF 日内申赎 T0、ETF 篮子截面调仓、逆回购 | 32 个 ETF |

最近交易日没有观察到独立的股票篮子截面交易：两个活跃账户的普通股票卖出都能逐证券匹配到当天 ETF 赎回产生的成分股划转，且没有普通股票买入。该结论只描述 `2026-07-24`，不能推导其它交易日没有股票截面策略。

### 2026-07-22 至 2026-07-23 股票截面与策略交叉

`314000046830` 提供了一组完整的隔夜股票篮子样本：

1. `2026-07-22` 从空仓买入 155 只股票，共 155 笔成交终态委托、169 条成交、75,800 股，买入金额 1,582,781.88；日终持仓正好是同一组 155 只股票、75,800 股，账本市值 1,568,765。
2. `2026-07-23` 将这 155 只股票全部卖出，共 187 条股票成交、75,800 股，卖出金额 1,567,206.34；逐证券卖出数量与前一日买入数量 155/155 完全一致，日终回到空仓。
3. 两日普通股票成交金额差为 -15,575.54，但这只是未扣费用、未处理外部现金流的成交事实，不能直接作为正式策略盈利。
4. 两天各有一笔 `204001.SH` 逆回购，必须从股票篮子交易中排除。

`501000114077` 在 `2026-07-22` 买入并赎回 `159915.SZ` 2,000,000 份，196 条成分划转对应 98 只股票、102,400 股，股票卖出数量与划转完全一致；`2026-07-23` 只有一笔逆回购。这两天没有股票或 ETF 隔夜持仓。

`314000045768` 同时存在 ETF 申赎 T0 和 ETF 截面调仓：

1. `2026-07-22` 买入并赎回 `159915.SZ` 5,000,000 份，即 5 个最小申赎单位；买入并赎回 `588200.SH` 18,000,000 份，即 4 个最小申赎单位。
2. 当日 539 条划转事件扣除 `588200.SH` 旧协议中的 ETF 本体标记后，正股成分合计 146 只、384,144 股，普通股票卖出数量逐证券完全一致；不存在独立股票截面交易。
3. ETF 截面持仓从 30 个变为 32 个：新增 2 个、增持 4 个、减持 26 个。
4. `159915.SZ` 日初持仓 64,400 份，普通卖出 4,100 份，同时完成 5,000,000 份买入赎回，日终为 60,300 份。
5. `588200.SH` 日初持仓 4,600,000 份，普通卖出 4,500,000 份；当日共买入 18,066,000 份、赎回 18,000,000 份，日终为 166,000 份。多出的 66,000 份属于截面持仓变化，不能把当日全部 `588200.SH` 买入都归为 T0。
6. `2026-07-23` 再买入 `588200.SH` 4,505,700 份并赎回 4,500,000 份，即一个最小申赎单位；48 只正股成分共 32,046 股，卖出数量与 PCF/划转完全一致。
7. 当日 ETF 截面持仓从 32 个变为 31 个：增持 18 个、减持 10 个、移除 1 个、保持 3 个。`588200.SH` 的 5,700 份申赎外净买入使日终持仓从 166,000 增至 171,700 份。

三个账户在 22 日和 23 日都各有一笔 `204001.SH` 逆回购，应作为现金管理交易单独归因，不进入股票或 ETF 篮子收益。

以上事实证明，策略归因不能只按账户或证券代码完成。同一账户中的 `588200.SH` 在同一交易日同时承担申赎 T0 和 ETF 截面持仓调整；历史数据至少需要按“最小申赎单位对应数量 + ETF 成分划转 parent”拆分，未来订单应显式携带 `strategy_id/strategy_type/basket_id/parent_order_id`。

### ETF 日内申赎 T0

Meridian `etf-pcf-status` 显示 `2026-07-24` 同步成功。相关 PCF：

| ETF | 最小申赎单位 | PCF 成分 | 实物成分 | 必须现金替代 | 每单位 cash_component | 每单位 nav_per_basket |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `159915.SZ` | 1,000,000 | 100 | 98 | 2 | 11,762.12 | 3,599,041.12 |
| `159381.SZ` | 2,000,000 | 50 | 49 | 1 | 15,331.31 | 2,340,681.31 |

`501000114077`：

1. 二级市场买入 `159915.SZ` 2,000,000 份，成交金额 7,113,000。
2. 分两笔各赎回 1,000,000 份，均为最小申赎单位整数倍。
3. OC 推送 196 条成分划转，单笔赎回各 98 条，数量与 Meridian PCF 的 98 个正股成分逐项一致。
4. 98 个成分证券共卖出 102,400 股，成交金额 7,147,725；逐证券卖出数量与两笔赎回获得数量完全一致。

`314000045768`：

1. 二级市场买入 `159915.SZ` 6,000,000 份，成交金额 21,247,000；分六笔各赎回 1,000,000 份。
2. 二级市场买入 `159381.SZ` 2,000,000 份，成交金额 2,280,000；一次赎回一个最小申赎单位。
3. OC 共推送 637 条成分划转。每笔 `159915.SZ` 对应 98 个正股成分，`159381.SZ` 对应 49 个正股成分，均与当日 PCF 正数量成分逐项一致。
4. 122 个去重后的成分证券共卖出 349,200 股，成交金额 23,517,668.50；逐证券卖出数量与七笔赎回获得数量完全一致。

目前可以确认申赎交易链路闭合，但不能直接把“成分股卖出金额 + PCF cash_component - ETF 买入金额”定义为正式盈利。仍需确认最终现金差额、必须现金替代结算、交易费用、印花税和其它清算调整。

### ETF 申赎 T0 估算盈亏口径

由于跨市场成分不会全部划入证券账户，部分成分由基金管理人代买代卖；基金网站披露的实际成交价、现金替代、现金差额和回款周期又没有统一标准接口，因此 Relay 暂不追求从成分成交还原精确清算盈亏。Meridian PCF 只用于校验最小申赎单位、篮子结构和实物成分数量，不作为基金管理人最终清算单。

已确认采用 `estimated_iopv_15bp` 近似口径。对每笔赎回：

```text
estimated_redemption_value = redemption_qty * iopv_at_redemption
estimated_friction_cost = estimated_redemption_value * 0.0015
estimated_etf_t0_pnl =
    estimated_redemption_value
    - attributed_etf_buy_cost
    - estimated_friction_cost
```

其中：

1. `redemption_qty` 取赎回终态成交数量，不使用柜台赎回记录中的名义价格 `1.0/0`。
2. `iopv_at_redemption` 优先取 Meridian 历史 Level1 中时间不晚于赎回成交时刻的最近一条有效 `iopv`。必须同时保存 `iopv_timestamp` 和 `iopv_lag_ms`，禁止使用赎回时刻之后的快照。
3. Level1 缺失时，可以降级使用赎回前最后一个完整 1 分钟 bar 的 `iopv`，并标记 `minute_iopv_fallback`；仍无有效值时不计算该笔盈亏，标记 `missing_iopv`。
4. `attributed_etf_buy_cost` 从订单组识别，不使用当日全部 ETF 买入的加权均价。按账户、交易日、ETF 和买入方向聚合买入委托；一个订单组的委托总量是 Meridian PCF `unit_subscribe_redeem` 的整数倍时，该组关联的实际成交金额归入 T0 成本。交易所拆单后单张子单可以小于最小申赎单位，例如 `588200.SH` 的一个 4,500,000 份订单组实际由同一批次 5 张 900,000 份子单组成。未来订单应显式携带 `t0_order_group_id/basket_id`；历史数据只能结合提交时间簇、连续订单流 ID 和后续赎回数量推断，存在歧义时标记 `ambiguous_t0_order_group`。
5. 合格订单组发生部分成交、撤单和补单时，以原订单组目标委托量为边界，将补足该目标的后续替代订单及实际成交继续归入同一 T0 批次；无法闭合到整数申赎单位时标记 `incomplete_t0_order_group`，不静默挪用底仓成交补足。
6. 不属于合格 T0 订单组的 ETF 买入、卖出和剩余数量全部保留在 ETF 底仓调仓成本中。即使同一账户、同一交易日、同一 ETF 同时用于 T0 和底仓，也禁止在两个成本池之间做日均价摊分。
7. 订单委托量优先采用盘后 `order.list.query` 的最终值。若实时 `order.event.order_qty` 小于 `cum_filled_qty` 或与盘后查询不一致，应使用盘后修正值并标记 `realtime_order_qty_corrected`；盘后仍无法修正时该订单组不可归因。
8. 固定成本率 `0.0015` 以 IOPV 估算的一篮子价值为基数，统一覆盖 ETF 买入、赎回后成分卖出手续费以及成交价差异的冲击成本。该参数必须配置化、版本化并随结果输出，不能作为无来源常量隐藏在公式中。
9. 实物成分股成交、现金替代和后续回款只用于链路对账与数据质量检查，不再重复计入本估算公式，避免双重计算。
10. 输出必须使用 `estimated_etf_t0_pnl` 等估算字段，不得写入或展示为券商 `settled_profit`。

`2026-07-22` 至 `2026-07-24` 的 19 笔赎回均能命中不晚于成交时刻的 Meridian 历史 Level1 IOPV，最大快照滞后约 2.23 秒。按订单组规则回算：

| 交易日 | 账户 | 赎回笔数 | 赎回数量 | IOPV 估算赎回价值 | T0 买入成本 | 15bp 成本 | T0 估算盈亏 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `2026-07-22` | `314000045768` | 7 | 23,000,000 | 42,230,600.00 | 42,150,927.30 | 63,345.90 | 16,326.80 |
| `2026-07-22` | `501000114077` | 2 | 2,000,000 | 7,292,100.00 | 7,311,000.00 | 10,938.15 | -29,838.15 |
| `2026-07-23` | `314000045768` | 1 | 4,500,000 | 5,735,250.00 | 5,710,500.00 | 8,602.88 | 16,147.13 |
| `2026-07-24` | `314000045768` | 7 | 8,000,000 | 23,584,800.00 | 23,527,000.00 | 35,377.20 | 22,422.80 |
| `2026-07-24` | `501000114077` | 2 | 2,000,000 | 7,149,400.00 | 7,113,000.00 | 10,724.10 | 25,675.90 |

订单组拆分样本：

1. `588200.SH` 在 `2026-07-22` 的最小申赎单位为 4,500,000。四组各 5 张 900,000 份买单归入 T0，单独的 66,000 份买单归入底仓；T0 买入成本为 23,827,500，底仓买入成本为 85,932。
2. `2026-07-23` 的一组 5 张 900,000 份买单归入 T0，单独的 5,700 份买单归入底仓；T0 买入成本为 5,710,500，底仓买入成本为 7,444.20。
3. `159381.SZ` 在 `2026-07-24` 的最小申赎单位为 2,000,000，同一批次两张各 1,000,000 份买单应聚合为一个 T0 订单组。
4. `159915.SZ` 在 `2026-07-22` 有一张 1,000,000 份合格委托部分成交 796,100 后撤单，再以 203,900 份补足；两张订单的实际成交共同归入原 T0 批次。

### 股票篮子截面策略

`2026-07-24` 没有识别到独立股票篮子调仓：

1. 两个活跃账户均无普通股票买入。
2. 普通股票卖出全部属于 ETF 赎回成分股，数量与 `transfer.event` 完全匹配。
3. 后续识别该策略时，必须先从普通股票成交中扣除有 ETF redemption parent 的成分划转卖出，再把剩余股票交易与上一 close/当日 close 持仓差关联。

### ETF 篮子截面策略

`314000045768` 当日除申赎 T0 使用的 `159915.SZ/159381.SZ` 买入外，还发生：

1. 21 个 ETF 的普通二级市场调仓，共 36 条成交。
2. 买入金额 93,350，卖出金额 29,665.40。
3. 日终持仓从上一交易日 31 个 ETF 变为 32 个 ETF：新增 1 个、增持 8 个、减持 12 个、保持 11 个。
4. 日终 32 个持仓经 Meridian instruments 验证全部为 ETF，账本市值合计 5,110,976.20。

该部分具备“前一日持仓 + 当日普通 ETF 成交 + 当日日终持仓”的基本归因条件，但手续费和外部现金流仍缺失。

### 股票与 ETF 截面策略盈亏口径

股票截面和 ETF 截面均按普通二级市场持仓、买入、卖出和费用计算。ETF 申赎 T0 订单组及其 `P/R`、成分划转和成分卖出先按前述规则从 ETF 截面成本池中剥离；逆回购也单独归因。

单证券单日毛盈亏使用资产现金流恒等式：

```text
adjusted_open_value = adjusted_open_qty * meridian_pre_close
close_position_value = close_qty * meridian_close
effective_fee = actual_fee if fee_complete else estimated_fee

cross_section_gross_pnl =
    close_position_value
    + ordinary_sell_amount
    - ordinary_buy_amount
    - adjusted_open_value

cross_section_net_pnl =
    cross_section_gross_pnl
    - effective_fee
```

其中：

1. `ordinary_buy/sell_amount` 只包含该截面策略成本池中的普通二级市场成交；同一 ETF 中已经归入 T0 订单组的买入成本不得再次进入。
2. `meridian_pre_close` 使用目标交易日 Meridian `1d, adjustment=none` bar 的 `pre_close`。交易所在除权日提供的该字段已经换算到当日价格单位，可用于承接昨日持仓，不直接拿昨日原始 close 与今日除权后价格比较。
3. `adjusted_open_qty` 优先取盘前初始化完成后的券商持仓数量，对应 `position_snapshots(snapshot_type=open)`。若目标日缺少 open 持仓快照，只能降级到上一交易日 close 持仓并标记质量问题。
4. `close_qty/meridian_close` 取当日日终持仓和 Meridian 原始收盘价。缺少日终快照时只能展示临时估算，不能固化为正式日绩效。
5. `actual_fee` 优先使用柜台实际费用；缺失时按该账户、交易日和业务类型的有效费用规则生成 `estimated_fee`。两者都不可用时标记 `missing_cross_section_fee`，且普通截面交易不沿用 ETF T0 的 15bp 参数。
6. 账户日总盈亏以以上现金流恒等式为准。页面需要拆分已实现/浮动时，可在经过公司行为调整后的成本池内使用移动加权成本，但两部分之和必须回到同一总盈亏。

股票和 ETF 都可能发生除权除息、分红、送转、拆并份或 ETF 份额折算。公司行为处理规则：

1. 统一查询 Meridian `/v1/metadata/adjust-factors`，读取 `ex_date/ex_factor/ex_cum_factor`；Relay 通过同源薄代理转发，不重新定义因子。
2. 物理持仓数量始终以券商盘前持仓为准，不能对所有 `ex_factor` 机械乘数量。现金分红通常不改变股份，送转、拆分和 ETF 份额折算才改变数量。
3. 数量变化用持仓桥校验：优先直接比较上一 close 与当日 open；缺少 open 快照时，使用 `close_qty - ordinary_buy_qty + ordinary_sell_qty` 反推公司行为后的日初数量。ETF 还需先剥离已闭合的 T0 买入/赎回数量。
4. 若反推数量比与 Meridian 因子相符，按数量变化修正单位成本，持仓总成本保持不变：`adjusted_unit_cost = previous_total_cost / adjusted_open_qty`。
5. 若因子发生变化但券商数量不变，按现金分红/价格除权处理：物理数量不变，日收益通过当日 `pre_close` 基准体现；后续若拿到明确红利资金流水，不得再重复计入。
6. 因子、`pre_close`、盘前数量或持仓桥无法一致时标记 `corporate_action_mismatch`，不得用异常价格跳变直接生成策略巨额盈亏。
7. 研究侧可以保留 `performance_adjusted_cost`，但不能覆盖券商原始持仓数量和原始成本字段。

现有样本：

1. `588200.SH` 在 `2026-07-21` 有 Meridian `ex_factor=3`。前一 close 持仓 93,700 份，折算后为 281,100 份；当日买入 9,000,000、赎回 4,500,000、普通卖出 181,100 后，日终正好为 4,600,000 份，数量桥完全闭合。
2. `600000.SH` 在 `2026-07-16` 的前一日原始 close 为 9.31，当日 bar `pre_close=8.89`，与 `ex_factor=1.0472451375925815` 对应；该类现金除权不能把实际股份数乘以因子。
3. `314000046830` 股票篮子在 `2026-07-22` 从空仓建仓，按日终市值计算毛盈亏 -14,016.88；`2026-07-23` 以 155 只股票的 Meridian `pre_close` 汇总日初基准 1,568,765，全部卖出后的毛盈亏 -1,558.66。两日合计 -15,575.54，与未扣费买卖金额差完全一致。

### 其它交易与数据质量

1. `314000045768` 在 14:56:46 有一笔 `204001.SH` 逆回购，回报 `qty=358,280`、`price=1.005`。Meridian instruments 当前未返回该标的元数据，逆回购本金与利息规则需要单独建模。
2. `314000045768` 日终仍有 2 笔 ETF 买单显示 `working`，另有 4 笔赎回成分股卖单曾被拒绝；成分股最终成交数量仍与划转数量完全一致，说明拒单后存在重试成交。
3. Relay 旧账户级订单唯一键曾导致跨日订单覆盖。2026-07-29 已切换交易日复合键并重放生产原始流：订单由 19,762 恢复到 34,601，成交缺同日订单和订单事件缺同日订单均归零。
4. 历史仍有少量 OC 原始 `order.event/fill.event` 共用 ID 但证券和数量不一致，不能由 Relay 猜测改配；策略事实和盈利归因必须优先使用通过质量校验的真实成交、成分划转和持仓快照。样本与 OC 整改要求见 `docs/OC_LEDGER_QUALITY_REPORT_20260729.md`。
5. `2026-07-22/23` 的 `588200.SH` 赎回订单事件已有 `business_type=E`，但对应成交记录缺少该字段，并额外把 ETF 本体作为一条 `transfer.event`；历史归因需通过 `gateway_order_id` 关联订单事件，并从 PCF 成分中排除 ETF 本体标记。
6. `2026-07-24` 两笔 `159915.SZ` 实时订单事件曾分别出现 `order_qty=200/600`、`cum_filled_qty=1,000,000` 的矛盾；盘后订单查询已将两笔 `order_qty` 修正为 1,000,000。T0 归因必须使用最终订单查询值并保留实时修正质量标记。

## 数据来源

| 数据 | 来源 | 用途 |
| --- | --- | --- |
| 上日收盘资产 | `asset_snapshots(snapshot_type=close)` 的上一交易日记录 | 隔夜调整参考基准 |
| 日初资产 | `asset_snapshots(snapshot_type=open)`；由 `pre_open_init` 在盘前刷新后写入 | 当日交易收益率和贡献 bps 的优先分母 |
| 日终资产 | `asset_snapshots(snapshot_type=close)` | close 净资产、现金、证券市值、日内盈亏、收益率主线 |
| 日初持仓 | 规划中的 open 持仓快照；由 `pre_open_init` 在盘前刷新后写入 | 公司行为后的权威日初数量、昨日持仓成本承接 |
| 日初持仓 | `position_snapshots(snapshot_type=open)` | 公司行为后的券商实际数量、开盘持仓贡献和截面策略昨仓基准 |
| 日终持仓 | `position_snapshots(snapshot_type=close)` | 持仓市值、权重、浮动盈亏、收盘持仓贡献 |
| 当前持仓 | `positions` | 当天尚未结算时的临时查看口径，不作为历史绩效最终口径 |
| 成交账本 | `fills` | 买卖金额、费用、成交数量、成交时间分布、按证券贡献估算 |
| 费用规则 | 规划中的账户级费用规则版本 | 柜台未返回实际费用时的估算手续费；保留规则版本和估算标记 |
| 人工资金流水 | 扩展后的 `cash_ledger` | 外部入出金、柜台间内部划转、收益性现金流和结算调整 |
| 委托账本 | `orders` | 下单数、成交率、撤单率、拒单率、未终态订单和异常订单 |
| 任务运行 | `job_runs` | 盘前初始化、收盘结算是否完成，运行耗时和错误摘要 |
| 对账批次 | `reconciliation_runs` | 目标交易日是否完成对账/结算 |
| 对账差异 | `reconciliation_breaks` | 未终态订单、订单成交数量不一致、快照缺失、刷新失败等质量问题 |
| Meridian bars | `/v1/meridian/market/bars` | 基准收益、收盘价参考、后续持仓估值补充 |
| Meridian Level1 | `/v1/meridian/market/snapshots` | ETF 赎回成交时点 IOPV；使用不晚于赎回时刻的最近快照 |
| Meridian ETF PCF | `/v1/meridian/market/etf-cash-components` | 读取 `unit_subscribe_redeem`，校验 ETF T0 赎回量和买入订单组是否符合最小申赎单位 |
| Meridian adjust factors | `/v1/meridian/metadata/adjust-factors` 薄代理 | 股票/ETF 公司行为日期、复权因子和持仓成本调整 |
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
| 手续费 | 实际柜台费用优先；缺失时按账户有效费用规则估算，并分别展示 `actual_fee/estimated_fee` |
| 数据质量 | 结算任务状态、对账差异数量、未终态订单数量、估算字段数量 |

### PnL 分解

第一版沿用 P8 已落地的研究侧口径：

```text
realized_pnl = settled_profit
gross_pnl = realized_pnl + day_unrealized_pnl
net_pnl = gross_pnl - fee_total
```

说明：

1. `settled_profit` 来自前置/柜台或资产快照字段，优先级高于 relay 自行估算。
2. 如果 `settled_profit` 缺失，页面可以展示 `estimated_realized_pnl`，但必须标明估算。
3. `unrealized_pnl` 表示按买入成本计算的总持仓浮盈，用于持仓页和持仓贡献解释。
4. `day_unrealized_pnl` 表示当日持仓浮动贡献：前一交易日已有持仓按今日开盘价计日内基准，当日买入持仓按当日买入成交成本计日内基准；缺字段时可以用 Meridian open/close 与成交账本估算，并标明估算。
5. 后续精确版本需要引入成本引擎，覆盖 FIFO/移动加权、分红派息、除权除息、逆回购、ETF 申赎和特殊费用。

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
| `unrealized_pnl` | 按买入成本计算的总持仓浮盈 |
| `day_unrealized_pnl` | 当日持仓浮动贡献 |
| `fee` | 该证券成交费用 |
| `net_contribution` | `realized_pnl + day_unrealized_pnl - fee` |
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
2. `pre_open_init` 是否在目标交易日 09:01 之后完成，并是否写入日初资产。
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
5. 使用人工资金流水先把 `overnight_adjustment` 分解为 `external_flow`、`internal_transfer`、`settlement_adjustment`、`interest_dividend`、`fee_tax_adjustment` 等；后续柜台提供同类接口时复用相同分类并对账。

## 账户级费用规则

不同账户、券商和业务类型的费用可能不同，绩效设置中预留“费用规则”页面。费用规则只用于柜台没有返回实际费用时的研究侧估算，不覆盖 OC 或券商返回的原始费用。

规则按账户和生效区间版本化，至少支持：

1. 适用范围：账户、市场、证券类型、业务类型和买卖方向；账户默认规则可以被更具体的规则覆盖。
2. 费率项：佣金率、最低佣金、印花税、过户费、经手费、其它固定或比例费用。
3. ETF 申赎 T0 的 `estimated_friction_rate=0.0015` 单独配置，不与普通股票/ETF 二级市场手续费混用。
4. `effective_from/effective_to` 使用 `Asia/Shanghai` 交易日。已用于结算的规则不可原地修改，只能创建新版本。
5. 每次计算保存 `fee_rule_id/version`、估算基数、各费用分项和总额，保证历史结果可复现。

费用取值优先级：

```text
OC/柜台实际费用
> 人工确认的单笔费用调整
> 账户级有效费用规则估算
> missing_fee
```

实际费用和估算费用必须分字段保存。成交费用为 0 不能单独证明“真实免佣”，还需要 `fee_source/fee_complete` 标记区分真实零费用和字段未返回。后续收到实际费用时生成对账差异并替换研究结果来源，但不删除旧估算及其规则版本。页面需要展示账户、规则状态、生效区间、费率明细、最近修改人和试算结果；生产环境修改需要确认并写审计记录。

## 人工资金流水与柜台间划转

OC 当前没有完整资金流水接口，外部入出金和柜台资金变化先由用户在绩效设置中人工维护。现有 `cash_ledger` 作为统一账本，后续通过 migration 扩展分类和审计字段，不另建一套平行口径。

资金事件分为：

| 分类 | 示例 | 绩效处理 |
| --- | --- | --- |
| `external_flow` | 银证入金、银证出金、账户外部调拨 | 改变资产规模，不计入策略盈亏；用于修正时间加权收益率 |
| `internal_transfer` | 极速柜台与普通柜台之间划转 | 账户级净流入为 0，只解释不同资金仓位的可见余额变化 |
| `income_expense` | 利息、红利、税费、逆回购收益 | 属于账户收益或费用，进入相应归因，不作为外部资金 |
| `settlement_adjustment` | ETF 申赎现金差额、在途款、清算修正 | 进入资产桥并等待明确归因，不自动视为盈利 |

人工记录至少包含：

1. `account_id`、发生时间 `effective_at`、交易日、币种和带符号金额。
2. `flow_class/ledger_type`、极速或普通柜台资金仓位、说明和可选附件/外部流水号。
3. `source=manual`、录入人、录入时间、确认人、确认时间和幂等键。
4. `draft/confirmed/voided` 状态；只有 `confirmed` 进入绩效计算，修改已确认记录使用冲正或作废记录，禁止物理删除。
5. 柜台间划转使用同一 `transfer_group_id` 记录转出和转入两条腿。两条腿金额不一致、缺失或币种不同均标记 `internal_transfer_unbalanced`。

资金计时规则：

1. 盘前 open 快照之前生效的外部资金已经包含在日初资产中，只解释隔夜调整，不在日内收益中再次扣减。
2. open 与 close 之间生效的外部资金用于修正当日收益；close 之后的记录归入下一交易日。
3. 内部柜台划转无论何时发生都不改变账户经济净资产。若 Relay 暂时只能看到一侧余额，使用人工记录补齐资产桥并标记 `partial_counter_visibility`，不得把可见资金下降当作亏损。
4. 红利若已通过 Meridian `pre_close` 进入证券日收益，不得在证券贡献中重复增加；实际红利现金只用于账户资产桥和分类核对。
5. 只确认交易日、无法取得精确时刻的盘中外部资金，不伪造事件级精度：`raw_payload.effective_time_precision=date`，`effective_at` 使用可审计的盘中基准时刻，Modified Dietz 分母采用 `0.5` 权重，并输出 `external_flow_time_estimated_mid_session`。后续取得银行或柜台精确时间后，以冲正/修订记录重算。

第一版页面建议在 `/trade#performance-settings` 提供“费用规则”和“资金流水”两个 tab。资金流水支持账户、日期、分类和状态过滤，提供新增、确认、冲正、CSV 导入导出及未配平内部划转提示。该页面是生产敏感写入口，不复用交易权限开关，需要独立的绩效配置写权限和完整审计。

## 经济净资产与净值滚动方案

当前柜台 `asset_page.net_asset` 主要是可见资金，无法直接覆盖证券市值、逆回购本金、ETF 申赎待结算款和柜台间不可见资金。Relay 采用“绩效滚动主线 + 资产对账辅线”，不把不完整的柜台资金差直接当作收益。

### 绩效滚动主线

每个账户先人工确认一个起始日的 `initial_economic_nav`，后续按已经确认的策略盈亏和资金事件滚动：

```text
account_day_pnl =
    etf_t0_estimated_pnl
    + stock_cross_section_net_pnl
    + etf_cross_section_net_pnl
    + reverse_repo_net_interest
    + other_income_expense
    + settlement_adjustment

provisional_close_economic_nav =
    open_economic_nav
    + account_day_pnl
    + intraday_external_net_flow
```

规则：

1. `internal_transfer` 不进入公式；极速柜台与普通柜台之间搬账不改变账户经济净资产。
2. 盘前发生的外部入出金直接进入当日 `open_economic_nav`；盘中外部资金单独进入 `intraday_external_net_flow`，不计入 `account_day_pnl`。
3. 单日收益优先使用 Modified Dietz 近似：`daily_return = account_day_pnl / (open_economic_nav + sum(weight_i * external_flow_i))`，其中 `weight_i` 按资金发生后剩余的交易时段计算。没有盘中外部资金时退化为 `account_day_pnl / open_economic_nav`。
4. 区间净值使用 `product(1 + daily_return)` 连乘，不让入出金改变累计收益曲线。
5. 输出字段使用 `performance_economic_nav`，并保留 `provisional/finalized` 和估算来源；不得覆盖原始 `asset_snapshots.net_asset`，也不得宣称为券商会计总资产。

当前实现第一版已经落地为：

1. `GET /v1/accounts/{account_id}/performance/economic-nav/preview?trade_date=YYYYMMDD`：只读试算，不写库，生产环境可安全使用。
2. `POST /v1/accounts/{account_id}/performance/economic-nav/rebuild`：受 `performance.settings_write_enabled` 保护，写入新的 current `performance_nav_versions`，并把同账户同交易日旧 current 版本退役。
3. 公式暂用 `close_economic_nav = close_visible_net_asset + reverse_repo_receivable`，`account_day_pnl = close_economic_nav - open_economic_nav - external_net_flow - settlement_adjustment`。
4. `daily_return` 使用 Modified Dietz 外部资金权重；09:30 前资金权重为 1，15:00 后权重为 0，盘中按剩余交易时间线性衰减。只有日期精度的盘中资金流固定使用 `0.5` 权重并携带估算质量标记。
5. `pnl_components.cash_management` 承接逆回购净息和已知 `income_expense`；策略尚未拆分的部分进入 `pnl_components.unattributed`，并标记 `strategy_attribution_pending`。
6. `performance_nav_reconciliations` 写入 same-day provisional 对账记录，并已提供 T+1 observed open assets 预览/落库接口；人工确认/阻断 API 可将 current `performance_nav_versions` 就地推进为 `finalized/blocked`，`/trade#performance` 已提供正式状态告警和页面复核操作流。

### 资产对账辅线

T 日 15:01 先发布 `provisional`，T+1 交易日 09:01 盘前初始化后校正：

```text
observed_next_open_assets =
    next_open_visible_cash
    + next_open_position_value
    + confirmed_invisible_counter_cash
    + outstanding_settlement_assets

reconciliation_residual =
    observed_next_open_assets
    - provisional_close_economic_nav
    - overnight_external_net_flow
    - known_overnight_income_expense
```

1. `next_open_position_value` 使用盘前持仓数量和当日 Meridian `pre_close`；因此盘前任务必须保存 open 持仓快照。
2. 只能看到一个柜台资金仓位时，由已确认的内部划转和人工仓位余额生成 `confirmed_invisible_counter_cash`。
3. 逆回购本金及预计利息、ETF 清算在途款只在尚未进入可见资金时列入 `outstanding_settlement_assets`，转为现金后立即冲销，避免重复计入。
4. `reconciliation_residual` 回写原交易日的独立 `settlement_adjustment`，不静默摊给股票截面、ETF 截面或 ETF T0。
5. 建议默认阈值配置化：绝对差异不超过 `max(50 CNY, open_nav * 0.1 bp)` 时自动完成；不超过 `max(500 CNY, open_nav * 1 bp)` 时保留警告并等待人工确认；更大差异保持 `provisional`，不得进入正式累计净值。
6. 周五或节假日前的记录在下一个交易日盘前再最终确认，不按自然日强行结算。

当前接口实现：

1. `GET /v1/accounts/{account_id}/performance/economic-nav/reconcile?trade_date=YYYYMMDD&observed_trade_date=YYYYMMDD` 只读预览。`observed_trade_date` 为空时通过 Meridian 交易日历取 T 日后的下一个交易日；若没有已落库 current NAV，preview 会临时试算 `economic-nav/preview` 作为对账基准并打 `economic_nav_preview_source` 标记。
2. `POST /v1/accounts/{account_id}/performance/economic-nav/reconcile` 受 `performance.settings_write_enabled` 保护，要求已存在 current `performance_nav_versions`，然后按 `performance_nav_pk` 幂等 upsert 对账记录。
3. 第一版 `observed_open_assets = open.cash_total + sum(open_positions.market_value)`；盘前 `external_flow` 和 `income_expense` 只纳入 09:30 及以前的已确认手工流水，09:30 后发生的流水会被排除并打质量标记。
4. `POST /v1/accounts/{account_id}/performance/nav-reconciliations/confirm` 受写开关保护，要求传入 `trade_date/operator`，将同一 current NAV 对应的对账记录改为 `confirmed` 并写入 `reviewed_by/reviewed_at`，同时把 current `performance_nav_versions.status` 就地推进为 `finalized`。如果 residual 超过 warning threshold 或记录已 blocked，需要 `force=true`。
5. `POST /v1/accounts/{account_id}/performance/nav-reconciliations/block` 受写开关保护，写入阻断复核信息，并把 current NAV 标记为 `blocked`，避免进入正式累计净值。
6. `/trade#performance` 读取当前交易日 NAV 对账记录，按 `confirmed/auto_completed/review_required/blocked` 展示状态告警、账面/观测 NAV、残差、自动/警告阈值、资金/持仓观测值和复核信息。确认/阻断按钮读取 `/v1/performance/settings` 的服务端写开关；超警告阈值或已阻断记录需要显式勾选强制确认，阻断要求填写说明，两类动作均二次确认。生产写开关关闭时完整保留只读监控，但所有复核输入和按钮禁用。

该模式让小额清算误差在 T+1 被吸收且不跨日累积，同时保留可追溯的估算值、校正值和误差来源。

## 逆回购近似口径

第一版只处理已经在生产样本中验证的上交所一天期通用质押式回购 `204001.SH`。OC 回报方向为 `S`，`price` 是年化利率百分数，`qty` 是百元资金单位。

规则依据为上交所现行有效的[债券通用质押式回购交易指引](https://www.sse.com.cn/lawandrules/sselawsrules2025/bond/trading/currency/c/c_20250606_10781048.shtml)：`204***` 为通用回购代码，逆回购方向为卖出，首次结算价为 100 元，购回价按实际占款天数和 365 天年基准计算。

同一账户、交易日和 `gateway_order_id` 的去重成交先聚合，再逐成交计算：

```text
repo_principal_i = fill_qty_i * 100
repo_gross_interest_i =
    repo_principal_i
    * (fill_price_i / 100)
    * actual_occupation_days
    / 365

reverse_repo_net_interest =
    sum(repo_gross_interest_i)
    - effective_repo_fee

repo_receivable =
    sum(repo_principal_i)
    + reverse_repo_net_interest
```

其中：

1. `actual_occupation_days` 按交易所实际占款天数计算，使用 Meridian 交易日历推导首次交收日和到期交收日之间的自然日天数；不能固定写成名义期限 1 天。
2. `effective_repo_fee` 优先使用柜台实际费用，缺失时读取账户级逆回购费用规则，不复用股票、ETF 或 ETF T0 费率。
3. 逆回购本金不进入股票卖出额、普通成交额或策略利润；只有净利息进入 `reverse_repo_net_interest`。
4. T 日 close 将本金和预计净利息记为应收资产，T+1 资金可用后冲销本金应收；实际到账差异进入前述 `settlement_adjustment`。
5. 缺少交易日历、数量乘数不通过现金桥校验或出现非 `204001.SH` 品种时标记 `unsupported_repo_contract`，不猜测计算。

现有生产样本验证：

| 交易日 | 账户 | 本金 | 加权年化利率 | 实际占款天数 | 预计毛利息 | 次日资金桥增加额 | 未扣费残差 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `2026-07-22` | `314000046830` | 3,809,000.00 | 1.355% | 1 | 141.40 | 156.97 | 15.57 |
| `2026-07-23` | `314000046830` | 5,375,000.00 | 1.400% | 3 | 618.49 | 634.86 | 16.36 |

该账户两日没有 ETF 申赎清算干扰，公式与次日盘前资金桥只差十几元，适合作为第一版逆回购回归样本。其它两个账户同日存在 ETF 申赎待结算，不用单一现金差反推逆回购收入。

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
| 复权因子 | `GET /v1/meridian/metadata/adjust-factors` |
| 经济净值 | `GET /v1/accounts/{account_id}/performance/economic-nav/preview`、`POST /v1/accounts/{account_id}/performance/economic-nav/rebuild` |
| T+1 净值对账 | `GET/POST /v1/accounts/{account_id}/performance/economic-nav/reconcile` |
| 证券/策略贡献 | `GET /v1/accounts/{account_id}/performance/contributions` |

规划中的绩效配置写接口：

```text
GET/POST /v1/performance/fee-rules
GET/POST /v1/accounts/{account_id}/cash-ledger
POST /v1/accounts/{account_id}/cash-ledger/{entry_id}/confirm
POST /v1/accounts/{account_id}/cash-ledger/{entry_id}/void
GET /v1/accounts/{account_id}/performance/nav-reconciliations
```

所有写接口使用独立权限、幂等键和审计日志。生产环境默认只读，不能因为开放交易下单权限而自动开放绩效配置写权限。

只读贡献聚合接口已实现：

```text
GET /v1/accounts/{account_id}/performance/contributions
```

当前返回：

1. `summary`：账户 KPI 和数据质量摘要。
2. `contributions`：按证券和策略聚合的 open/close 数量、买卖额、费用、净贡献、贡献 bp、估值/IOPV 来源和质量状态。
3. `strategies`：股票截面、ETF 截面、ETF 申赎 T0、现金管理、成分划转和待归因汇总。
4. `quality_flags`：缺失、估算、T0 历史订单组推断、成分划转链接缺口，以及交易质量中的成交证券/方向错配、终态时间缺失或跨日。

该接口只读，只使用本地账本和 Meridian，不查询柜台。OC v1.1 不再要求 ETF 申赎主记录出现在普通成交中：Relay 从独立 transfer 账本中选择“证券代码与关联 ETF 订单一致”的 ETF 本体记录作为赎回时点信号；同一订单下的成分证券 transfer 只做数量链路核对，不转换为成交，也不参与收益重复计算。如果普通赎回成交已经存在，则优先使用普通成交并跳过 transfer 信号，避免双计。

open 持仓缺失时只允许使用前一交易日 close 快照兜底，并且必须满足 `open + buy - sell = close` 数量桥；否则该证券保持 `missing`，不能把缺失持仓当作 0 制造虚假收益。多个 ETF 赎回组的 IOPV 请求最多 8 路并发，返回顺序仍按归因组稳定。

2026-07-22 至 2026-07-24 生产只读样本已复核：

1. `501000114077` 7 月 24 日两组 `159915.SZ` T0 净贡献合计 25,675.90 元，98 条成分划转不重复计利。
2. `314000045768` 7 月 24 日七组 ETF T0 净贡献合计 22,422.80 元，逆回购净息 986.496986 元，122 条成分划转不重复计利。
3. `314000046830` 7 月 22/23 日股票截面净贡献分别为 -14,016.88/-1,558.66 元；逆回购净息分别为 141.402603/618.493151 元。23 日使用前一日 close 作为 open 兜底后，155 只证券数量桥全部闭合。

## 分阶段推进

### Phase 1 文档设计

状态：`done`

产出当前文档，明确页面定位、指标口径、数据来源和第一版边界。

### Phase 2 UI 重构

状态：`done`

使用现有 performance、history、reconciliation 和 Meridian 接口完成 `/trade#performance` 重排：

1. KPI 同时展示期初/期末账面净资产、日初资产、隔夜调整、日内/区间盈亏、经济净值、资金桥、基准和超额收益。
2. ECharts 主图使用 close 账面收益序列生成归一化账户净值，并同步展示上证指数基准、超额收益、账户回撤和基准回撤；tooltip 保留实际净资产和基准收盘值。
3. 正式数据质量区固定检查七类输入：资产快照与资金桥、Meridian 基准 bars、证券贡献、订单成交账本、持仓成本连续性、经济净值与 T+1 NAV 对账、盘前初始化与盘后结算。
4. 质量状态按 `passed/warning/blocked` 汇总，`missing` 输入进入阻断，`estimated/provisional` 和历史任务不匹配进入提示；所选历史交易日不会误用最新任务状态。
5. 绩效辅助接口并行读取且带请求代次保护，切换账户或连续查询时旧响应不会覆盖新结果。
6. 分钟 K 线从绩效页完全移除，只保留在交易测试页；CSV 下载继续保留。
7. `tests/integration/performance_visual_smoke.py` 使用生产只读数据验证区间查询、canvas 有效像素、七项质量检查和无浏览器/HTTP 错误；`1600x1280` 与 `1280x800` 均无横向溢出。

### Phase 3 贡献聚合

状态：`done`

当前完成：

1. 后端聚合成交额、费用、open/close 持仓、证券贡献和策略汇总。
2. ETF T0 合并同一赎回订单的多条普通成交；OC v1.1 未提供普通赎回成交时，以独立 transfer 账本中的 ETF 本体记录作为赎回时点信号。两种路径都使用不晚于赎回时刻的 Meridian IOPV，并隔离可精确闭合赎回量的历史买入订单组。
3. 从 Meridian `etf_cash_component.v1.unit_subscribe_redeem` 读取目标交易日最小申赎单位。赎回量不是整数倍时标记 `redemption_quantity_not_pcf_unit_multiple` 并停止该组收益计算；PCF 缺失时保留既有历史估算路径，但明确标记 `missing_meridian_etf_redemption_unit`，不得把未校验结果解释为精确清算。
4. ETF 成分股卖出从 IOPV 估值收益中排除，逆回购只把净息计入现金管理。
5. `/trade#performance` 默认展示证券贡献表，可切换到原净值序列；估算、缺失和排除状态可见。
6. Go 单元测试覆盖普通持仓现金流恒等式、ETF T0 多成交聚合和 PCF 最小申赎单位不匹配。
7. 2026-07-22 至 2026-07-24 生产只读样本完成复核，多组 IOPV 查询并发后最慢样本约 4.55 秒。
8. Playwright 已验证证券贡献/净值序列切换、未结算日自动回退和 1680px 布局。

### Phase 3.1 交易质量统计

状态：`done`

当前完成：

1. 新增只读 `GET /v1/accounts/{account_id}/performance/trade-quality`，支持单日或日期区间。
2. 成交率拆为有实际成交订单率、完全成交率和按委托数量计算的成交率，同时输出撤单率与拒单率。
3. 异常明细覆盖拒单、未终态、订单与成交数量不一致、终态冲突、柜台错误字段残留和成交缺委托。
4. 订单和成交严格按 `trade_date + gateway_order_id` 关联，避免柜台订单 ID 跨日复用导致错误归组；真实成交存在时排除同订单 `relay-summary:*` 汇总成交。
5. `/trade#performance` 增加“交易质量”视图，API Console 与 Python SDK helper 同步。
6. 2026-07-22 至 2026-07-24 三账户生产只读样本完成核对，正常撤单不作为异常；历史成交缺委托、成交终态残留柜台拒绝字段及数量冲突可明确追溯。

Phase 2 主图和正式数据质量区已完成；后续精度提升进入 Phase 4，不再阻塞 N8 页面验收。

### Phase 4 可信成本账与经济净值 v2

状态：`doing`

`2026-08-01` 已落地的计算底座：

1. `performance_account_inceptions` 保存账户起算日、日初资金、初始持仓/成本来源、策略范围和确认审计；账户范围不在程序中写死。
2. `performance_position_cost_states` 以 `account_id + trade_date + symbol + exchange + cost_bucket` 保存移动加权成本、已实现/浮动盈亏、行情估值和数量残差。
3. 成本账按 `日初数量 + 买入 - 卖出 = 日终数量` 逐证券校验。逆回购从证券成本账中排除，ETF `P/R` 预留独立分账；数量不平时直接阻断，不用柜台成本强行抹平。
4. `performance_economic_nav.v2` 以 `可见资金 + Meridian 持仓重估 + 逆回购应收 + 确认调整` 计算日初/日终 NAV。日初持仓使用 Meridian `pre_close`，日终使用 `close`；柜台 `avg_cost/market_value/unrealized_pnl` 仅用于输入对账。
5. 日收益按 v2 NAV 和外部资金流计算，区间收益按日收益复利链接；被阻断日期不计入正式曲线。无 v2 NAV 的历史现金快照只返回 `legacy_cash_snapshot_diagnostic`，不再计算虚假收益。
6. 贡献聚合显式区分缺失盈亏与真实零值，并输出 `NAV 日盈亏 - 证券贡献 - 资金管理贡献 - 已知收支` 残差。费用规则缺失时只能 provisional。
7. 新增起算配置、成本试算/重建 API 和 `relayctl performance-rebuild`；绩效页质量区增加“持仓成本连续性”。
8. `307000051387` 的 `2026-07-29` 百万元盘中出金已按 confirmed `external_flow` 纳入 v2：精确时刻未知时使用日期精度和 `0.5` 权重，重算日盈亏 `+71,089.76` 元、收益率 `+0.141192%`、归因残差 `-458.48` 元，当日由 blocked 降为 provisional；`2026-07-30` 的 18 个数量差异继续独立阻断。

首批可信范围为 `307000051387`、`307000051388`、`307000051389` 和债享5号 `314000046830`。前三户从新账户首个可信快照起算；债享5号仅运行股票截面策略，以已确认柜台日初持仓成本为锚点。该账户的历史持仓不是“空仓起算”，但因无 ETF 申赎，不存在申赎对柜台成本的污染。

待完成项：

1. 配置四账户的实际费率和最低收费，将 provisional 升级为 finalized。
2. 使用 Meridian 除权因子处理股票和 ETF 的公司行为，保持总成本连续并调整单位成本/数量桥。
3. 在出现 ETF 申赎前实现 `CORE` 与 `ETF_T0:{group_id}` 独立成本池，引入 PCF、现金替代和跨市场回款质量标记。
4. 对四账户逐日做人工金标验收，完成后再向其他历史账户扩展。
5. 给研究侧导出 view 增加 v2 版本，保留 v1 作原始柜台参考。

## 第一版边界

1. 不接入实时 level2、trades、orders 或 order-queues。
2. 不主动查询柜台，避免绩效页影响实盘柜台压力。
3. 不把估算盈亏当成券商最终账单。
4. 不在 relay 中新增行情字段标准，所有行情字段继续以 Meridian 为准。
5. 移动加权成本 v1 已用于可信账户；公司行为和 ETF T0 分账完成前，其他账户仍不输出精确成本。
6. 不把回测模拟撮合逻辑放进 relay。

## 验收口径

页面第一版完成时，需要能明确回答：

1. 指定账户在指定交易日或区间的净资产、盈亏、收益率和回撤是多少。
2. 相对基准的超额收益是多少。
3. 哪些证券贡献了主要收益或亏损。
4. 当天成交额、手续费、撤单、拒单和异常订单情况如何。
5. 盘后结算是否完成，对账差异是否为 0，哪些字段是估算或缺失。
6. 所有结果都可以通过本地账本、任务记录、对账记录和 Meridian 数据追溯。
