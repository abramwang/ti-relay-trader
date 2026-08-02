# relay 开发路线图

更新时间：`2026-08-01`

## 状态口径

- `done`: 已完成并提交。
- `doing`: 当前优先推进。
- `todo`: 已规划，尚未开始。
- `blocked`: 受外部信息、权限或环境阻塞。
- `deferred`: 已明确暂缓，不纳入近期 relay 边界。

## 总体阶段

| 阶段 | 状态 | 目标 | 主要产出 |
| --- | --- | --- | --- |
| P0 文档门户与恢复机制 | done | 9092 可访问项目框架、文档、测试目录和开发路线图 | Go 文档门户、README 恢复卡片、ROADMAP |
| P1 工程化底座 | done | 建立正式服务骨架和配置体系 | 服务模式拆分、配置文件、日志、错误模型、基础测试 |
| P2 标准交易接口设计 | done | 定义统一 A 股交易 API 和 schema | 账户、资金、持仓、下单、撤单、订单、成交、事件 schema |
| P3 多账户路由 | done | 管理 account/broker/gateway/stream prefix 关系 | 多账户配置、账户启停状态、路由校验和路由诊断接口 |
| P4 Redis Stream 前置对接 | done | 对接托管机房前置服务协议 | 命令写入、reply/event/hb/dlq 消费、幂等、位点和运行可观测 |
| P5 交易账表持久化 | doing | 建立标准交易账表和审计流水 | PostgreSQL migration、订单表、成交表、资金持仓表、事件表 |
| P6 9092 正式交易 API 与 SDK | doing | 给交易软件和策略提供统一接口 | HTTP API、Python SDK、事件订阅、状态查询、错误码 |
| P7 交易日流程与盘后对账 | doing | 管理盘前初始化、收盘后结算和盘后对账 | Python jobs、任务状态、对账批次、差异表、修复入口 |
| P8 历史数据与盈亏统计 | doing | 接入 Meridian 并计算账户绩效 | 历史行情拉取、资产快照、PnL、收益率、回撤、绩效归因 |
| P9 模拟柜台 | deferred | 暂缓，不纳入 relay 近期边界 | 实盘调试使用券商测试环境；历史数据模拟撮合放在回测引擎 |
| P10 运维发布 | doing | 形成可部署、可观测、可回滚的服务 | 容器自启动、进程管理、监控、告警、备份、发布手册 |

## 当前优先级

1. N13 优先修复绩效计算底座：正式净值改为资金加 Meridian 行情重估持仓，隔离柜台 `avg_cost/market_value/unrealized_pnl` 对 ETF 申赎后的污染。
2. 以 `307000051387`、`307000051388`、`307000051389` 三个新账户和只运行股票截面策略的 `314000046830` 为首批可信成本账户，建立起算锚点、移动加权成本账和逐日数量对账。
3. 从 OC 新版本后的第一个完整交易日开始建立每日绩效流水线：盘后确认订单、成交、订单实际费用、资金、持仓和 Meridian 日线齐备，逐账户计算成本账和经济净值；账户异常独立标记，不拖累其他账户。
4. 历史订单、成交、费用和缺失 close 快照不再作为当前主线推断修复；保留原始缺口，后续取得券商交割单后通过独立导入和重建流程补齐。
5. 股票和 ETF 公司行为校正及 ETF T0/底仓分账已接入 `performance_position_cost.v3`；下一步等待首个新协议完整交易日做真实订单组、因子与券商持仓联合验收。
6. 完成 OC v1.2 联合验收：下一次启动使用现存 PEL 验证 `QUERY_INTERRUPTED`、pending 清零且不再产生伪 `BAD_RECOVERED_COMMAND`。
7. N12 暂列次优先级：后续补 API Console 断言集合、券商测试环境批量下单视图，以及 API/worker 独立常驻进程。

### N13 可信成本账与绩效重建

状态：`doing`

目标：把绩效主线从柜台参考成本和现金余额中解耦，建立可连续滚动、可回算、可对账、可阻断的 Relay 研究账表。

首批账户：

- `307000051387`、`307000051388`、`307000051389`：新账户，尚无 ETF 申购赎回和公募占款；从首次有效日初资金/持仓状态起算。
- `314000046830`：债享5号，仅运行股票截面策略，无 ETF 申购赎回成本污染；以确认过的日初持仓成本作为锚点。
- 其他账户在成本污染识别、ETF T0 分账和历史归类完成前，只展示诊断结果，不发布正式成本绩效。

实施步骤：

- [x] 完成现有公式和生产样本审计，确认主净值漏算持仓市值、柜台 ETF 成本污染、日初 `market_value` 可能等于成本金额、归因残差缺失语义错误等问题。
- [x] 确认首批四账户的策略边界和可信成本条件；前三个账户当前不处理申赎占款，债享5号按普通股票截面处理。
- [x] 新增账户级绩效起算配置：记录起算日、初始资金、初始持仓来源、成本可信状态、策略范围和人工确认信息，不在代码中写死账户白名单。
- [x] 建立行情重估口径：日初持仓使用 Meridian `pre_close`，日终持仓使用 Meridian `close`，重估结果保存到 v2 NAV 估值分量；原始柜台数量保留，柜台 `avg_cost/market_value` 仅作对账参考。
- [x] 建立 v2 经济净值底座：使用可见资金、行情重估持仓、逆回购应收和已确认结算调整，并用资金流加权分母计算日收益。
- [x] 发布 `performance_economic_nav.v2.1` 修正逆回购口径：用含/不含本金两条账表恒等式识别可见资金重叠，无法明确判断则阻断；本金只在未进入可见资金时加入，预估利息保留诊断但不进入正式 NAV/PnL，实际回款/费用后通过 `income_expense` 确认。`314000046830/2026-07-02`、`2026-07-10` 正确识别为 embedded，`2026-07-14` 正确识别为 separate。
- [x] 将用户确认的债享5号 2026 年 7 月 17 条资产/盈利记录保存为人工金标，新增 `scripts/compare-performance-gold.py` 可重复比较，并完成本金单计、预估利息和隔夜调整的逐日对账；详见 `docs/DEBT5_MANUAL_NAV_RECONCILIATION_20260801.md`。
- [x] 新增 `000021_performance_nav_gold` 和 `relayctl performance-gold-import/compare`：人工金标按版本、current、确认审计和内容哈希独立落库，17 条生产记录事务导入且重复导入仍为 version 1；金标只验收公式，不覆盖 OC 表或参与 NAV 计算。
- [x] 建立移动加权成本账 v3：从可信锚点继承上一日结余，按普通二级市场成交和费用滚动，保存日初/买入/卖出/日终数量、总成本、单位成本和已实现盈亏，并接入公司行为数量桥及 ETF T0 分账。
- [x] 建立数量连续性检查：`日初数量 + 买入 - 卖出 +/- 公司行为/转移 = 日终数量`；不平时按账户、证券和交易日阻断成本结果，不使用柜台日终成本覆盖。
- [x] 修复贡献汇总：显式区分 `account_day_pnl` 缺失和真实零值，主净值、证券贡献与资金管理贡献输出可解释残差。
- [x] 接入 OC `fee.list.query/fee_page` 订单级实际费用：新增 `000022_order_fee_records`，按 `account_id + fee_record_id` 幂等更新；只有 `fee_complete && association_complete` 才进入绩效。普通贡献、移动成本账和逆回购按订单只扣一次，ETF T0 继续使用独立 15bp 摩擦模型；盘后任务在订单/成交后刷新费用，SDK `0.1.23` 提供查询和刷新方法。
- [x] 拆分交易可用性与费用完备性门禁：核心交易字段及订单成交关联完整时，缺费用的订单仍进入数量桥、移动成本、成交额和毛收益；按当日有成交唯一订单统计费用覆盖，缺口只使净绩效保持 provisional 并等待券商交割单。
- [x] 建立 OC 新版本后的每日绩效计算任务。交易日 17:45 在 Meridian 日线同步后，对可信账户先试算移动成本账，再试算经济净值；任务保存逐账户 `ready/attention/blocked`、订单费用覆盖、质量标记和摘要，任一账户失败不把整批其他账户标记失败。任务只读，不绕过绩效写保护。
- [ ] 历史数据修复延期。OC 不具备历史查询能力，当前不使用最新费率或衍生 close 替代历史柜台事实；7 月历史费用、旧成交缺口和 7 月 31 日 close 缺失保持只读诊断，后续取得券商交割单后再设计版本化导入、对账和重建。
- [x] 公司行为接入 Meridian `/v1/metadata/adjust-factors`：新增 `000023_position_cost_corporate_actions`；物理数量以券商 open 为权威，数量比匹配因子时保持总成本并调整单位成本，数量不变时按价格除权，无法闭合时阻断。原始 Meridian 上下文可审计且不覆盖 OC 持仓。
- [x] 实现 `CORE` 与 `ETF_T0:{group_id}` 虚拟成本分账：复用贡献模块的显式/历史订单组和 Meridian PCF 单位门禁，T0 买入成本不再污染 ETF 底仓；显式完整组日内归零，历史歧义或未闭合组阻断。现金替代、跨市场代买代卖和公募回款仍等待权威结算输入。
- [x] 绩效页面增加成本连续性、估值来源、公式版本和正式/阻断状态；被阻断日期不计入正式收益曲线，无 v2 NAV 的旧现金快照只作诊断。
- [ ] 验收首个 OC 实费版本后的完整交易日：四个可信账户逐项检查订单成交关联、订单费用覆盖、open/close 资金持仓、Meridian 估值、数量桥、成本账和 economic NAV；`ready` 才允许进入正式发布，`attention/blocked` 必须保留账户级原因且不影响其他账户。
- [ ] 四账户连续完整交易日通过后，按可信锚点和数据质量分批开放正式绩效发布；其他账户及历史区间继续只读诊断，原始柜台数据与旧公式版本不原地覆盖。

`2026-08-01` 首轮回算现状：

- `307000051388`：`2026-07-28` 至 `2026-07-30` 的 v2 累计收益为 `-0.883054%`，数量桥闭合；因缺费用规则仍为 provisional。`2026-07-28` 柜台 close 快照漏三个非零持仓，使用次日 open 同数量快照补足并显式标记。
- `314000046830`：用户提供的 `2026-07-01..31` 共 17 条人工资产/盈利金标已作为 `manual_user_confirmed` 独立版本化落库。16 条有 OC close 的记录中，7 月 22、23、28、29、30 日的日末资产和日盈亏误差均小于 0.01 元，7 月 14 日日末资产完全一致、盈利只差 0.72 元；7 月 2 日和 10 日资金快照正确识别为本金 embedded。严格逐日门禁通过 7 月 22、28、29、30 日，29/30 日 current NAV 已受控重建；22/28 日因中间 23 日仍 blocked 暂不跨日发布，避免累计净值漏计。早期记录继续等待历史费用和 OC 成交身份修复，7 月 31 日缺 OC close 快照保持 unavailable。OC 当前日费用协议见 `docs/OC_ACTUAL_FEE_INTEGRATION_GUIDE_20260801.md`。
- `307000051389`：起算后资金和持仓均为零，系统保留起算配置但不生成无意义的正式收益率。
- `307000051387`：用户确认 `2026-07-29` 盘中转出 `1,000,000` 元，已按 confirmed `external_flow` 落账；精确时刻未知，原始记录标记日期精度，Modified Dietz 分母按盘中 `0.5` 权重估算。重算后当日状态由 blocked 变为 provisional，日盈亏 `+71,089.76` 元、收益率 `+0.141192%`，归因残差降至 `-458.48` 元；仍缺账户真实费率。`2026-07-30` 有 18 个证券数量差异，例如 `510810.SH` 委托成交 `76,400`，成交表仅有 `300`，属历史 OC 成交身份错配，必须使用权威查询/重放修复，Relay 不生成合成成交。
- `2026-07-31`：三个有资产账户都缺 OC close 资产/持仓快照，当前正式绩效继续阻断；该历史缺口暂不使用衍生快照替代，等待券商交割单或其他权威历史数据源。

`314000046830` 历史边界补充：当前仅两天是因为起算锚点保守设置为 `2026-07-29`，不是数据库只保存了两天。资金/订单/成交从 `2026-06-15` 开始，Meridian 可估值日线从 `2026-06-22` 开始；人工金标现可为 7 月账户级资产和盈利提供连续验收基准，但早期 OC 成交身份错配仍会阻断证券级成本贡献。人工表中的承接日初资产与当日实际 open 之间存在隔夜调整，例如 7 月 28 日为 -2,465.14 元、7 月 30 日为 +175.53 元；此前观察到的隔夜资金变化不应直接判作外部流。`512760.SH` 二级市场交易表明策略范围应包含 ETF 截面，而不应只标为股票截面。

验收口径：

- 每个账户必须有明确起算日和起算依据，首日资金流不计为投资收益。
- 收盘净值必须能由资金、Meridian 重估持仓和确认调整逐项复算；不得依赖柜台 ETF 平均成本。
- 每个证券的成本数量方程和账户净值方程均闭合；超过阈值的残差必须显示证券级原因并阻断 finalized。
- 四账户从 OC 新协议后的连续完整交易日通过数据门禁和人工抽查后，才替换绩效首页主曲线口径；历史金标仅用于公式回归，不要求 OC 在线补查历史。

## 阶段任务

### N8 绩效分析页面 Phase 2/3

状态：`done`

目标：把 `/trade#performance` 从数据验证页整理为可用于日终复盘的绩效工作区。

完成项：

- [x] 完成现有绩效 API、页面代码和三账户生产账本的只读数据审计。
- [x] 确认现有 `asset_page.net_asset` 主要是资金余额，逆回购、ETF 申赎、费用和外部现金流不能直接套用现有收益公式。
- [x] 以 `2026-07-22` 至 `2026-07-24` 的订单、成交、ETF 成分划转和 Meridian PCF 梳理策略事实：确认 ETF 申赎 T0、股票截面和 ETF 截面三类策略，并识别同一 ETF 在 T0 与截面持仓之间的数量交叉。
- [x] 确认 ETF 申赎 T0 使用赎回时点 Meridian IOPV 作为估算卖出价值，并按篮子价值计提 15bp 综合摩擦成本；T0 买入成本由委托总量构成最小申赎单位整数倍的订单组及其实际成交独立归集，不与 ETF 底仓按日均价摊分。
- [x] 确认股票/ETF 截面按普通持仓现金流恒等式计算；股票和 ETF 的除权除息统一使用 Meridian `pre_close/adjust-factors` 修正绩效基准，以券商盘前持仓作为实际数量，不机械按因子修改物理股份。
- [x] 确认费用使用账户级、生效区间版本化规则；柜台实际费用优先，规则估算与实际费用分别保存，ETF T0 的 15bp 参数独立维护。
- [x] 确认 OC 暂无完整资金流水时由用户人工维护；外部入出金修正收益率，极速/普通柜台内部划转成对记录且不计入收益。
- [x] 确认“绩效滚动主线 + T+1 资产对账辅线”的正式经济净资产方案，以及 `204001.SH` 逆回购近似口径。
- [x] 设计跨账户、跨策略归因标识，并修正订单“账户内当日唯一”业务键没有包含交易日的问题：新增 `trade_date`、策略归因字段、交易日唯一索引和 `performance_attribution_links`；旧二元唯一约束暂作为外键兼容锚点保留。
- [x] 增加 Meridian `metadata/adjust-factors` 同源薄代理，并让盘前初始化保存公司行为后的 open 持仓快照。
- [x] 扩展 `cash_ledger` 的分类、发生时间、柜台资金仓位、成对划转、确认/冲正和审计字段。
- [x] 增加 `/trade#performance-settings`，提供账户级费用规则、人工资金流水、日初经济净值和逆回购估算维护。
- [x] 增加版本化 economic NAV 预览/重建 API、`performance_nav_versions` current 版本写入、`provisional/finalized` 状态、same-day NAV 对账记录和阈值判断；策略未拆部分先写入 `pnl_components.unattributed`。
- [x] 补 T+1 盘前 observed open assets 预览/落库接口：读取 next trading day `asset_snapshots(open)` 与 `position_snapshots(open)` 聚合，计算 overnight 外部资金/已知损益、残差和阈值状态，并回写 `performance_nav_reconciliations`。
- [x] 补 `performance_nav_reconciliations` 人工确认/finalized 推进和阻断 API；`/trade#performance` 轻量展示当前交易日 NAV 对账状态。
- [x] 补 NAV 对账正式告警展示和页面人工确认/阻断操作流：展示账面/观测 NAV、残差、自动/警告阈值、复核信息和写权限状态；页面确认/阻断受服务端写开关、强制确认和二次确认保护。
- [x] 按 `qty*100`、年化利率、实际占款天数和账户费用规则实现 `204001.SH` 逆回购归因。
- [x] 新增只读 `GET /v1/accounts/{account_id}/performance/contributions`：按证券和策略聚合 open/close 持仓、买卖额、费用、净贡献、贡献 bp 与质量标记。
- [x] 实现 ETF 申赎 T0 第一版估算：同一赎回订单多条成交先合并，以不晚于赎回时刻的 Meridian Level1 IOPV 估值，按配置摩擦率扣费；历史买单仅在目标委托量精确闭合赎回量时归入 T0。
- [x] 将 ETF 成分股卖出从 T0 估算收益中排除，保留成交额和 `missing_transfer_link` 质量标记，避免与 IOPV 估值重复计利。
- [x] `/trade#performance` 增加“证券贡献 / 净值序列”切换、策略汇总和贡献明细表；API Console、schema 和 Python SDK helper 同步。
- [x] 新增只读 `performance/trade-quality`：输出有成交订单率、完全成交率、数量成交率、撤单率、拒单率、未终态和异常明细；订单成交按交易日关联，`/trade#performance`、API Console 和 `relay-sdk 0.1.18` 已同步。
- [x] 对齐 OC v1.1 数据质量协议：稳定外部订单 ID 作为不透明值使用，普通成交与 ETF 划转分表，`adapter.data_quality` DLQ 纳入消费统计，新增划转 API、终端页签和归档重放。
- [x] 对齐 OC v1.2 增量协议：撤单动作结果独立审计且不污染订单状态，batch `failed_orders[]` 逐单回写，`COMMAND_OUTCOME_UNKNOWN` 保持结果未知，运维页展示 broker/snapshot/交易/撤单真实就绪字段，未知事件 raw 归档后继续推进消费。
- [ ] 联合验证 OC v1.2：不可撤订单产生 `order.cancel.rejected`、超时进入需对账审计、长 ID 跨 OC 重启保持不变、重复查询完整重放且 PEL 清零。
- [x] 审计 Meridian SDK 至 `0.1.17` 并同步 Relay 所需增量：ETF PCF/现金清单/状态薄代理、实时分钟 Bar SSE、交易日显式字段；ETF T0 使用 `unit_subscribe_redeem` 做最小申赎单位质量校验，分钟线分块、replay/task/cursor 和行业分类保留在 Prism/回测边界。Relay Go HTTP 链路不依赖 Python SDK。
- [x] `/trade#performance` 新增 ECharts 主图：账户 close 净值归一化序列、上证指数基准、超额收益，以及账户/基准回撤双层联动展示。
- [x] 建立正式数据质量区：按资产快照与资金桥、Meridian 基准行情、收益归因输入、订单成交账本、经济净值与 T+1 对账、盘前初始化与盘后结算六项检查展示通过/提示/阻断。
- [x] KPI 同步覆盖上日收盘、日初资产、隔夜调整、日终资产、日内/区间盈亏、费用、经济净值与质量标记；分钟 K 线仅保留在交易测试页。
- [x] 绩效辅助查询并行执行并加入请求代次保护，切换账户或连续查询时旧响应不会覆盖新结果。
- [x] 新增 Playwright 绩效页交互测试；生产只读 `20260701-20260729` 的 21 个样本在 `1600x1280`、`1280x800` 下通过图表像素、六项质量检查、无横向溢出、无控制台错误和无 HTTP 错误验收。

范围：

- 主图展示账户净值、上证指数基准、超额收益和账户/基准回撤。
- KPI 同时展示上日收盘、日初资产、隔夜调整、日终资产、日内盈亏、费用和数据质量状态。
- 费用同时展示实际值、估算值和规则版本；资金桥区分外部净流入、柜台间内部划转和待分类结算调整。
- 新增只读 `performance/contributions` 聚合接口，按证券输出持仓贡献、成交额、费用、贡献 bps 和质量标记。
- 增加交易质量区：成交率、撤单率、拒单率、未终态订单和异常订单。
- 分钟 K 线继续只保留在交易测试页，绩效页不再把 bars 图当作绩效主图。

验收口径：

- 指定账户和区间能回答净值、收益、回撤、超额收益和主要证券贡献。
- 缺少 open/close 快照、Meridian bars 或柜台字段时明确标记 `missing/estimated`。
- 聚合逻辑有 Go 单元测试，页面有 Playwright 交互测试。
- 页面只读取本地账本和 Meridian，不主动查询柜台。

### N9 Redis Stream 与 gateway 可观测

状态：`done`

目标：把已经归档的 `hb/dlq` 和 `stream_checkpoints` 变成可判断、可告警、可处置的运行状态。

完成项：

- [x] 按 broker/gateway/account 汇总最后心跳时间、在线/重连状态、pending trade/query 和最近 `BROKER_NOT_READY`。
- [x] 展示每条 `reply/event/hb/dlq` 的 Redis 最新 ID、PostgreSQL checkpoint、最近消费时间、最近错误和真实 lag。
- [x] lag 使用有上限的 Redis 服务端计数；支持 stream trim，阈值默认 warning `500`、critical `5000`。
- [x] 新增 `stream_dlq_reviews` 不可变审核记录和 DLQ/`BROKER_NOT_READY` 部分索引；DLQ 支持待处理、已确认、已忽略、已重放状态，生产审核写默认关闭。
- [x] 新增 `/v1/operations/status`、`/v1/operations/dlq*`，并把摘要接入 `/v1/status`。
- [x] 新增 `/operations` 独立运维页面和 API Console 运维分组。
- [x] 使用 Meridian 交易日和 `08:55-15:30 Asia/Shanghai` 监控窗口抑制非交易日、盘前和 OC 关停后误报。
- [x] 生产只读验收：6 个 gateway 收盘后均为 `off_hours`，24 条 output stream lag 为 0，DLQ pending 为 0，下单账户仍为 0。

### N10 账本生产化与环境隔离

状态：`doing`

目标：消除测试/生产共库按账户区分的长期风险，并把应用层幂等提升为数据库约束。

范围：

- [x] 测试/生产改用独立 PostgreSQL：测试为 `relay_trader_test`，生产为 `relay_trader`；配置增加 `database.expected_name`，DSN 指向错误库时拒绝启动。
- [x] 清理历史查询请求键和 1 组旧缺陷重复键，为 `orders(account_id, idempotency_key)` 增加部分唯一约束；下单改为 PostgreSQL insert-only 原子占位，并发同单回查为 replay、不重复发布 Redis 命令。
- [x] 将 `orders/fills/order_events` 的唯一键、外键、upsert 冲突目标和研究视图切到 `account_id + trade_date + gateway_order_id`。
- [x] 从 PostgreSQL 原始流归档重放生产订单/成交，验证订单事件和成交同日外键完整性。
- [x] 形成 OC 同日 ID 冲突、订单/成交错配的复现报告和联合验收标准。
- [x] 交易质量接口增加证券/方向/业务类型错配和终态时间完整性检查；终态事件与盘后权威查询统一使用同日事件证据。
- [x] 2026-07-31 次交易日复验：6,898 个实时/查询订单身份和 8,318 个实时/查询成交身份错配均为 0，`307000051387` 的实时上下文错位已关闭。
- [ ] 验收 OC 查询命令 PEL 修复：`70c966d/7110a76` 已部署，等待下一次 OC 启动确认六账户现存 18 条 pending 清零且恢复关联字段正确。
- [x] 增加临时 PostgreSQL migration/repository 集成测试脚本，自动建库、迁移、测试和销毁。
- [x] 增加 custom-format 备份、SHA256/manifest 校验和临时库恢复脚本；完成 2026-07-31 全量生产备份及按交易日幂等回放演练，孤立记录、重复幂等键和未验证约束均为 0。

### N11 交易日任务与人工复核闭环

状态：`done`

目标：让盘前/盘后任务不只“运行完成”，还可清楚判断结果是否可信并形成可归档复核材料。

范围：

- [x] 输出账户级人工复核报告，汇总资产、持仓、订单、成交、未终态订单、对账差异和异常账户，并支持按交易日查询与 JSON 导出。
- [x] 非交易日 `trading_day.phase` 返回明确的 `non_trading`，避免仅按时钟显示 `continuous`。
- [x] 增加任务失败、账户异常、刷新超时和快照阻断告警：使用默认关闭的通用 Webhook 聚合投递，带稳定幂等键、失败重试和同一 `job_runs` 投递审计；非交易日正常跳过与 dry-run 不告警。
- 保留 09:01 盘前初始化和 15:01 生产盘后结算口径。
- [x] 多账户刷新改为全账户先发命令、共享 45 秒新鲜度窗口，消除按账户串行等待。
- [x] Redis output stream 改为单次聚合 `XREAD`，消除空 stream 每轮约 18 秒的顺序阻塞。
- [x] 明确 14:56 策略停单、15:01 权威结算、15:30 OC 关停的分阶段窗口。

### N12 API Console、回归测试与发布

状态：`doing`

目标：形成可重复执行的接口、页面和发布验收流程。

范围：

- [x] API catalog 增加一致性检查，覆盖条目结构、Go handler、源码 schema 和在线 `/v1/schema`。
- [x] API Console 支持命名请求集合、本地保存、JSON 导入导出和响应断言集合；Base URL 不入集合且加载后不自动发送。
- [ ] `/trade` 增加批量下单手动测试视图；生产环境保持 `trading_enabled=false`，写入测试只在券商测试环境执行。
- [x] Playwright 覆盖环境/账户切换、生产只读护栏、日期、分页、排序、K 线、订单详情、绩效页、jobs 页和运维页，并在网络层禁止写请求。
- [x] 增加统一生产只读发布验收脚本，以及配置、备份、观察和版本回滚检查清单。
- [x] 将 API/worker 拆为独立常驻进程，使用本机 worker 健康端口和 PostgreSQL 事件桥保持 SSE；补齐独立日志、自启动、重启和上一版本回滚入口。

## 里程碑细化

### P0 文档门户与恢复机制

- [x] 初始化项目目录。
- [x] 创建 README 恢复卡片。
- [x] 建立 `docs/ARCHITECTURE.md`。
- [x] 启动 9092 文档门户。
- [x] 固化最终服务口径 `http://relay-trader.quantstage.com`。
- [x] 在首页提供开发路线图入口。

### P1 工程化底座

- [x] 定义服务运行模式：`docs`、`api`、`worker`。
- [x] 明确真实凭据放在部署机本地配置文件，模板文件可提交。
- [x] 增加基础配置加载：端口、数据库、Redis、账户路由。
- [x] 增加结构化日志。
- [x] 增加统一错误码和响应 envelope。
- [x] 增加基础单元测试和健康检查测试。
- [x] 增加配置加载单元测试。

### P2 标准交易接口设计

- [x] 明确接口体参考源：Redis Stream 对接文档和 `/home/Titans/resource/include` C++ 头文件。
- [x] 定义账户模型。
- [x] 定义资金模型。
- [x] 定义持仓模型，覆盖 A 股 T+1 可卖数量。
- [x] 定义下单、批量下单、撤单请求。
- [x] 定义订单、成交、订单事件、成交事件模型。
- [x] 定义标准错误码和状态机。
- [x] 增加 `/v1/schema` 发现接口骨架。
- [x] 记录前置测试环境已启动，可用于后续 Redis Stream 联调。

### P3 多账户路由

- [x] 定义 `account_id -> broker_id + gateway_id + stream_prefix` 映射。
- [x] 定义账户启停、只读、可交易、账户环境类型等状态。
- [x] 增加路由冲突校验。
- [x] 增加多账户查询过滤。
- [x] 增加 `GET /v1/account-routes` 路由诊断接口，展示账户权限、环境、stream prefix 和 Redis stream key。

### P4 Redis Stream 前置对接

- [x] 记录前置测试环境已启动。
- [x] 实现只读探测命令 `relayctl redis-probe`。
- [x] 定义 stream prefix、`cmd.trade/cmd.query/reply/event/hb/dlq` 命名辅助。
- [x] 定义 Redis Stream `body` 消息摘要解析，不打印完整 body。
- [x] 增加 Redis body envelope 解析，提取 routing、reply、event、payload 和 adapter_context。
- [x] 增加 `relayctl ledger-sync`，支持批量读取 `reply/event` 并写入 PostgreSQL 账本。
- [x] 使用真实 Redis 小批量联调 `reply/event` 归档。
- [x] 在 9092 docs/api 模式启动轻量后台同步循环，持续消费测试 Redis `reply/event` 更新本地账本。
- [x] 实现 Redis command envelope 和 `cmd.trade` 单笔下单写入。
- [x] 实现撤单命令写入 `cmd.trade`。
- [x] 实现批量下单命令写入 `cmd.trade`。
- [x] 实现账户资金/持仓查询命令写入 `cmd.query`。
- [x] 实现订单/成交查询命令写入 `cmd.query`。
- [x] 消费 `reply` 并归档 raw。
- [x] 合并 `asset_page/position_page/order_page/fill_page` reply 到 PostgreSQL 账本。
- [x] 将下单类 `rejected/failed` reply 回写本地订单终态，并保留前置/柜台错误信息。
- [x] 消费 `event` 并归档 raw。
- [x] 将字段完整的 `order.event/fill.event` 持续消费接入 9092 轻量后台同步循环。
- [x] 将持续消费迁移到正式 worker 并持久化 stream 位点。
- [x] worker 原始归档 `hb`。
- [x] worker 原始归档 `dlq`。
- [x] 实现 consumer 位点和重放策略。
- [x] 实现幂等键和 `gateway_order_id` 管理。
- [x] 支持外部订单按 `order_stream_id` 建立稳定单笔委托 ID，并保留原始篮子/柜台 ID。
- [x] 支持 `synthetic_from_fill` 最小订单事件和 ETF `transfer.event` 语义，普通成交与零价划转分离。
- [x] 接入 `BROKER_NOT_READY` 瞬时错误，不把柜台未登录/重连误写为业务拒单。
- [x] 将 `hb` 合并为 gateway 心跳状态。
- [x] 增加 `dlq` 告警和处置状态。
- [x] 增加 Redis output stream lag、最近消费时间和 checkpoint 健康状态。

### P5 交易账表持久化

- [x] 明确 PostgreSQL 为最终账本候选，Redis 不作为最终账本。
- [x] 建立数据模型和字段映射设计文档。
- [x] 选择 migration 文件口径：工具无关 SQL，兼容 `psql`、`golang-migrate`、`goose`。
- [x] 建立 `accounts`、`gateways`、`account_gateway_routes`。
- [x] 建立 `orders`、`order_events`。
- [x] 建立 `fills`。
- [x] 建立 `raw_stream_messages`。
- [x] 建立 `positions`、`position_snapshots`。
- [x] 建立 `cash_ledger`、`asset_snapshots`。
- [x] 建立 `reconciliation_runs`、`reconciliation_inputs`、`reconciliation_breaks`。
- [x] 安装 PostgreSQL client。
- [x] 增加数据库连接和 migration runner。
- [x] 增加 `relayctl migrate status/up/down`。
- [x] 使用真实 PostgreSQL DSN 执行首版 migration。
- [x] 增加账本 repository 骨架，覆盖账户、订单、订单事件、成交和原始 stream 消息写入。
- [x] 增加账本 repository 单元测试，不依赖真实数据库即可验证 SQL 参数和 JSON payload。
- [x] 增加可选 PostgreSQL 账本集成测试，可通过 `RELAY_LEDGER_TEST_DATABASE_URL` 启用真实写库验证。
- [x] 将 Redis `reply/event` 批量归档接入 `raw_stream_messages`。
- [x] 新订单先写订单草稿，再用缺字段 `order.event` 更新订单状态并追加事件。
- [x] 新增 `stream_checkpoints` 表，持久化 Redis Stream 消费位点、处理计数和最近错误摘要。
- [x] 成交唯一键最终收敛为 `account_id + trade_date + gateway_order_id + fill_id`，查询页多条成交使用稳定派生 stream ID。
- [x] 当前持仓全量查询完成后清理旧批次残留行，日终快照等待本轮资金/持仓刷新确认。
- [x] 增加 open/close 资产快照、持仓当日盈亏字段和研究侧绩效 view migration。
- [ ] 让历史 `order.event.payload` 补齐 `trade_side/business_type` 后启用无草稿事件重建订单主表。
- [x] 测试/生产 PostgreSQL 使用独立 DSN，并以 `database.expected_name` 防止配置串库。
- [x] 清理历史重复幂等键后增加 `orders(account_id, idempotency_key)` 部分唯一约束；查询回包不再把 `orders:query:*` 请求键写入订单幂等字段。
- [x] 增加基于临时 PostgreSQL 的 CI-ready 集成测试脚本。

### P6 9092 正式交易 API 与 SDK

- [x] `GET /healthz` 正式服务健康检查骨架。
- [x] `GET /v1/status` 服务状态和依赖健康检查，覆盖账户摘要、PostgreSQL、Redis、订单服务、行情代理、事件流和自动刷新。
- [x] `GET /v1/accounts` 配置态账户列表骨架。
- [x] 增加 Apifox 风格接口测试台骨架 `/api-console`。
- [x] 文档门户模式同源挂载 `/v1/*` API handler，接口测试台可直接发送请求查看返回。
- [x] `POST /v1/orders` 单笔下单：订单草稿落盘、Redis `cmd.trade` 写入、命令 raw 归档。
- [x] 使用测试 Redis 完成一次单笔下单 API 冒烟，订单回流后落盘到 `filled`。
- [x] 测试下单参考 Meridian `2026-06-12` 分钟线，示例 `600000.SH` `15:00` close 为 `9.67`。
- [x] `GET /v1/accounts/{account_id}/asset`。
- [x] `POST /v1/accounts/{account_id}/asset/refresh`。
- [x] `GET /v1/accounts/{account_id}/positions`。
- [x] `POST /v1/accounts/{account_id}/positions/refresh`。
- [x] `POST /v1/accounts/{account_id}/orders/refresh`。
- [x] `POST /v1/accounts/{account_id}/fills/refresh`。
- [x] `POST /v1/orders/batch`。
- [x] `POST /v1/orders/{gateway_order_id}/cancel`。
- [x] `GET /v1/orders`。
- [x] `GET /v1/fills`。
- [x] `GET /v1/orders` 和 `GET /v1/fills` 默认按 `Asia/Shanghai` 当日过滤。
- [x] `GET /v1/history/orders` 和 `GET /v1/history/fills`。
- [x] `GET /v1/accounts/{account_id}/positions/history`，读取 `position_snapshots` 历史持仓快照。
- [x] `GET /v1/events/stream`。
- [x] 规划 Python SDK 的包形态、核心方法、错误处理和实盘语义。
- [x] 参考 Meridian SDK，明确内网 HTTP tar.gz 安装包和 pip 安装方式。
- [x] 初始化 `sdk/python/relay_sdk` 包。
- [x] 实现 SDK 账户、资金、持仓、订单和成交查询。
- [x] 实现 SDK 资金、持仓、订单和成交刷新指令。
- [x] 实现 SDK 下单、批量下单、撤单。
- [x] 实现 SDK 事件订阅和 `wait_order_terminal` 基础能力。
- [x] 实现 SDK 订单状态和成交回报回调：`on_order_status()`、`on_fill()`、`watch_order_status()`、`watch_fills()`。
- [x] 增加 SDK mock API 单元测试。
- [x] 增加 SDK 集成测试。
- [x] 增加 SDK 打包脚本和 `/sdk/relay-sdk-<version>.tar.gz` 下载入口。
- [x] 发布 `public/sdk/relay-sdk-0.1.0.tar.gz` 和 SHA256 校验文件。
- [x] 发布 `public/sdk/relay-sdk-0.1.1.tar.gz` 和 SHA256 校验文件。
- [x] 发布 `public/sdk/relay-sdk-0.1.2.tar.gz` 和 SHA256 校验文件。
- [x] 发布 `public/sdk/relay-sdk-0.1.3.tar.gz` 和 SHA256 校验文件。
- [x] 发布 `public/sdk/relay-sdk-0.1.4.tar.gz` 和 SHA256 校验文件，支持历史查询和任务报告落盘。
- [x] 发布 `public/sdk/relay-sdk-0.1.5.tar.gz` 和 SHA256 校验文件，支持收盘结算快照落盘。
- [x] 发布 `public/sdk/relay-sdk-0.1.6.tar.gz` 和 SHA256 校验文件，支持 job run 显式字段和 `completed` 状态兼容。
- [x] 发布 `public/sdk/relay-sdk-0.1.7.tar.gz` 和 SHA256 校验文件，支持 performance、Meridian bars 和 reconciliation helper。
- [x] 发布 `public/sdk/relay-sdk-0.1.8.tar.gz` 和 SHA256 校验文件，修复不同订单复用 `fill_id` 时的成交回调去重。
- [x] 发布 `public/sdk/relay-sdk-0.1.9.tar.gz` 和 SHA256 校验文件，支持绩效序列 `benchmark_security_id` 基准对照。
- [x] 发布 `public/sdk/relay-sdk-0.1.10.tar.gz` 和 SHA256 校验文件，`Position` 增加 `day_unrealized_pnl` 当日持仓浮盈字段。
- [x] 发布 `public/sdk/relay-sdk-0.1.11.tar.gz` 和 SHA256 校验文件，`submit_order()` 增加策略归因字段，`Order/Fill` 解析 `trade_date/business_type/strategy_*`。
- [x] 发布 `public/sdk/relay-sdk-0.1.12.tar.gz` 和 SHA256 校验文件，`get_positions()` 支持 `snapshot_type`，新增 `get_meridian_adjust_factors()`。
- [x] 发布 `public/sdk/relay-sdk-0.1.13.tar.gz` 和 SHA256 校验文件，新增 economic NAV 预览/重建、NAV 查询和 NAV 对账查询 helper。
- [x] 发布 `public/sdk/relay-sdk-0.1.14.tar.gz` 和 SHA256 校验文件，新增 T+1 economic NAV reconciliation 预览/落库 helper。
- [x] 发布 `public/sdk/relay-sdk-0.1.15.tar.gz` 和 SHA256 校验文件，新增 NAV 对账人工确认/阻断 helper。
- [x] 发布 `public/sdk/relay-sdk-0.1.16.tar.gz` 和 SHA256 校验文件，新增证券/策略贡献只读 helper。
- [x] 发布 `public/sdk/relay-sdk-0.1.18.tar.gz` 和 SHA256 校验文件，新增交易质量统计 helper，并将订单/成交回调去重键切换到交易日作用域。
- [x] 发布 `public/sdk/relay-sdk-0.1.19.tar.gz` 和 SHA256 校验文件，新增 `ComponentTransfer` 与当日/历史 ETF 划转查询。
- [x] 发布 `public/sdk/relay-sdk-0.1.20.tar.gz` 和 SHA256 校验文件，新增 Meridian ETF PCF components/cash-components/status 只读 helper。
- [x] 增加 SDK 版本发布检查清单。

### P6.1 接口测试台

- [x] 左侧接口集合。
- [x] 中间请求编辑区：method、base URL、path、query、headers、body。
- [x] 右侧响应查看区：HTTP 状态、耗时、响应 JSON。
- [x] 早期未实现交易写接口默认禁用发送；正式 handler 接入后已开放测试账户链路。
- [x] 9092 文档门户同源暴露 `/v1/*`，基础接口可直接从测试台发送。
- [x] 每个接口按 path/query/body 参数生成表单。
- [x] 响应结果支持 JSON 和表格视图。
- [x] 页面模板、样式、脚本和接口 catalog 从 Go handler 中拆分到 `web/` 资源目录。
- [x] 支持 `GET /v1/events/stream` SSE 事件流连接和最近事件预览。
- [x] 增加订单和成交前置查询刷新模板。
- [x] 增加 9092 页面轻量冒烟测试脚本，覆盖首页、文档、测试索引、API Console、交易终端、静态资源、基础 API 和 SDK 下载入口。
- [ ] API handler 完成后自动同步 endpoint 状态。
- [x] 增加命名请求样例的浏览器本地保存和版本化 JSON 导入导出。
- [x] 增加 HTTP status、JSON 路径存在/等值/类型和耗时断言，并由 Playwright 覆盖保存、恢复、导入导出和执行。

### P6.2 手动交易测试终端

- [x] 参考 Stitch 设计稿确定成熟交易软件式页面布局。
- [x] 新增 `/trade` 全屏终端页面，不复用文档门户文章外壳。
- [x] 使用本地模板和静态资源实现，不依赖 Tailwind CDN、Google Fonts 或外部 icon font。
- [x] 接入账户列表、资金、持仓、订单和成交查询。
- [x] 接入单笔下单和撤单。
- [x] 接入资金/持仓刷新指令。
- [x] 订单列表采用 3 秒轮询，状态签名变化时行高亮并写入推送日志。
- [x] 订单详情展示状态轨迹、订单 JSON 和成交执行记录。
- [x] 接入 `GET /v1/events/stream` SSE 实时推送，订单、成交、资金、持仓事件触发页面合并刷新，并保留 3 秒轮询兜底。
- [x] 接入 Meridian `/v1/market/snapshots` 薄代理，替换 `/trade` 盘口占位数据。
- [x] 接入订单/成交前置刷新指令，订单监控区可手动刷新委托和成交。
- [x] 订单监控表和订单详情展示 `reject_message`、柜台错误和 raw adapter context。
- [x] 订单累计成交量存在但成交明细缺失时，向前生成标记型汇总成交，避免订单/成交账本数量口径断裂。
- [x] 交易测试视图压缩右侧持仓版面，资金持仓独立工作区保留完整展示。
- [x] 交易测试主界面接入 ECharts 分钟 K 线，使用 Meridian `bars` 的 open/high/low/close 绘制 candlestick，并按当前账户、标的、交易日标注买卖委托/成交点。
- [x] 资金持仓、订单和成交支持按交易日查询、服务端 cursor 分页和表头排序。
- [x] 证券名称和类型通过 Meridian instruments 补齐，当前持仓通过 Meridian level1 SSE 实时估值。
- [x] 账户别名可在终端修改并落库，不影响真实账户路由和权限。
- [x] 申购/赎回在订单与成交表中单独显示，K 线点位按对应分钟 close 标注。
- [x] 修复 `/trade#asset`、`/trade%23asset` 和 `/trade/asset` 的终端入口兼容。
- [x] 持仓行点击可联动交易标的和行情/K 线。
- [x] 资金持仓页支持按当前账户和所选交易日导出资金摘要与完整分页持仓 CSV。
- [ ] 增加批量下单测试视图。
- [ ] 增加 Playwright 页面冒烟测试。

### P7 交易日流程与盘后对账

- [x] 明确盘后、快照、PnL 等后台批处理可优先采用 cron 管理。
- [x] 明确业务时间统一使用 `Asia/Shanghai`。
- [x] 规划 `pre_open_init` 盘前初始化流程。
- [x] 规划 `post_close_settlement` 收盘后结算流程。
- [x] 增加统一时间工具，集中提供 `Asia/Shanghai` location、业务日期和 API 展示格式。
- [x] 检查订单/成交/资金/持仓账本 API 的历史时间字段展示是否全部转换为 `Asia/Shanghai`，并省略零值时间。
- [x] 增加 Python 任务入口。
- [x] 实现 `python -m relay.jobs.pre_open_init` 任务骨架。
- [x] 实现 `python -m relay.jobs.post_close_settlement` 任务骨架。
- [x] 任务报告输出交易日、依赖状态、账户范围、刷新回执、账本快照摘要和未终态订单列表。
- [x] 建立任务运行账表，记录日流程报告、耗时、终态和错误摘要。
- [x] 将 `pre_open_init` 与 `post_close_settlement` 报告写入任务运行账表。
- [x] `/v1/status` 暴露交易日、交易阶段和日流程最近运行状态。
- [x] 新增 `/jobs` 后台任务状态监控页，展示任务状态、交易日、开始/完成时间、耗时、错误摘要和 report JSON。
- [x] 拉取柜台资金、持仓、订单、成交查询结果。
- [x] 盘前初始化写入 `asset_snapshots(open)` 日初资产快照，作为日内绩效基线；open 快照只写资产，不覆盖日终持仓快照。
- [x] 写入日终 `asset_snapshots(close)`、`position_snapshots` 和 `reconciliation_runs` 对账批次。
- [x] 对比 Redis 原始消息窗口摘要和内部账表摘要。
- [x] 记录 `reconciliation_inputs` 和 `reconciliation_breaks` 差异。
- [x] 生产盘前初始化固定为交易日 09:01，盘后结算固定为交易日 15:01 `Asia/Shanghai`。
- [x] 任务使用 Meridian 交易日信息跳过非交易日，并在 `/jobs` 区分计划、上次运行和历史记录。
- [x] 盘前/盘后任务支持账户级异常隔离和陈旧快照阻断，单账户未就绪不再误判整体失败。
- [x] 非交易日 `trading_day.phase` 返回 `non_trading`，不再仅按时钟显示交易阶段。
- [x] 输出按账户聚合任务、快照和对账差异的人工复核报告，并支持页面查询与 JSON 导出。

### P8 历史数据与盈亏统计

- [x] 接入 Meridian `bars` 同源薄代理，保留 Meridian `market_bar.v1` 原始字段。
- [x] `bars` 请求当天或空日期时通过 Meridian 交易日接口回退到最近交易日。
- [x] 接入 Meridian `metadata/instruments` 和 `snapshots` 作为 `/trade` 代码补全和行情刷新薄代理。
- [x] 计算账户日终权益。
- [x] 计算第一版完整已实现盈亏、浮动盈亏、费用和收益率：保留 `settled_profit/unrealized_pnl/day_unrealized_pnl/fee_total/return_rate`，新增 `realized_pnl/gross_pnl/net_pnl` 研究侧口径，其中 `gross_pnl` 使用当日持仓浮动贡献。
- [x] 提供第一版日终 PnL 输入汇总：上一 close 净资产、日盈亏、收益率、持仓快照汇总和成交汇总。
- [x] 绩效 v2 接入当日 `asset_snapshots(open)`，在日绩效、绩效序列、CSV 和 `/trade#performance` 展示日初资产、隔夜调整、日内盈亏和 open-to-close 收益率。
- [x] 提供账户 close 净值绩效序列：日收益、累计收益和最大回撤。
- [x] 在 `/trade` 交易测试主界面使用 Meridian `bars` 绘制当日分钟 K 线和成交量，辅助理解下单点位。
- [x] 基于 Meridian `bars` 生成账户绩效序列、回撤和研究侧导出输入：`benchmark_security_id` 输出基准收益、基准回撤、超额收益并进入 CSV。
- [x] 提供研究侧导出输入第一版：账户绩效序列 CSV。
- [x] 生成研究侧数据库导出视图：`research_account_daily_performance_v1` 和 `research_order_fill_export_v1`。
- [x] 完成绩效分析页面第一版设计文档，明确净值曲线、收益贡献、交易归因和数据质量口径。
- [x] 完成 `/trade#performance` Phase 2 UI：经济净值摘要、NAV 对账、净值/基准/超额收益/回撤主图和六项正式数据质量区均已落地。
- [x] 增加 `performance/contributions` 只读聚合接口和按证券贡献表。
- [x] 增加交易质量统计：有成交订单率、完全成交率、数量成交率、撤单率、拒单率、未终态和异常订单；区间关联使用 `trade_date + gateway_order_id`，避免柜台 ID 跨日复用。
- [x] 修复生产账表跨日覆盖：migration `000012`、权威订单查询快照覆盖和 raw archive 重放恢复近 15,000 条订单，孤立成交归零；OC 同日 ID 冲突转入联合整改。
- [x] 用 2026-07-22 至 2026-07-24 生产只读样本复核历史 ETF T0 订单组、IOPV 命中率、成分划转排除和贡献总额；三账户 T0、股票截面和逆回购结果与人工审计一致，多组 IOPV 查询并发后最慢样本约 4.55 秒。
- [x] 接入 Meridian ETF PCF 最小申赎单位校验；上游 PCF 缺失或赎回量非整数倍时输出质量标记，不在 Relay 中猜测单位或重建 PCF 标准。
- [ ] 后续按数据完整度评估精确成本引擎、公司行为和最终清算差异归因；现金流水、逆回购和 ETF 申赎第一版口径已落地。

### P9 模拟柜台

状态：`deferred`

- [x] 明确 relay 暂缓内置模拟柜台；实盘调试优先使用券商测试环境。
- [x] 明确历史数据驱动的模拟撮合放在回测引擎，不放在 relay 内部，避免实盘边界和行情撮合边界混淆。
- [ ] 如后续需要接入外部模拟柜台，应通过同一前置/Redis Stream 协议进入 relay，而不是在 relay 内实现撮合。

### P10 运维发布

- [x] 明确当前容器内 9092 使用生产只读配置、cron 自启动和健康守护的部署方式。
- [x] 增加 `scripts/relay-docs-service.sh` 启动、停止、重启和状态脚本。
- [x] 增加 9092 `@reboot`/分钟 watchdog 安装脚本，生产下单权限开启时默认拒绝自动启动。
- [x] 增加交易日 cron 示例、生产任务安装、`flock` 任务锁和日志目录约定。
- [x] 增加 SDK 版本发布和安装包维护清单。
- [ ] 将 API、worker 和 docs 职责拆分为独立常驻进程，并提供统一部署/回滚入口。
- [ ] 增加 metrics 和日志采集。
- [ ] 增加 Redis lag、DLQ、心跳超时告警。
- [x] 增加数据库备份、临时恢复和按交易日 raw archive 重放手册，并完成生产全量演练。
- [ ] 增加发布检查清单。
