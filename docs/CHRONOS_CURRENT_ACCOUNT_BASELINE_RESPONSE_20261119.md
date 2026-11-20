# Chronos 当前账户基线对接回执

日期：2026-11-19，Asia/Shanghai。契约草案：`draft.1`。

对应需求：Chronos `RELAY-CURRENT-ACCOUNT-BASELINE-REQUIREMENTS-20261119.md`。
本轮交付范围与字段合同，供消费端并行开发；后续已完成内部候选存储、来源组装、本地修订、来源健康观察及显式持久收件，见第9至13节（最新增量2026-11-20）。下文新HTTP接口、能力名和 SDK 方法仍为**待实现合同，不是已上线能力**。
已验收的历史 close 基线、SDK 0.1.43 和原三笔订单的独立处置保持不变。

## 1. 结论与已核实差距

接受此需求，作为证券账户的独立工作项推进，不等待期货生产化或绩效重建。
首次冻结应使用目标持仓减当前完整持仓，并核对可用资金、可卖数量及在途责任；不能重用昨日调仓差额。
不要求 OC 建账户账本或策略准入，不以此次需求为由打开查询/交易权限。

本轮代码核对结果：

| 位置 | 当前实现与边界 |
| --- | --- |
| `internal/api/position_baseline.go` | 只接受 historical close，拒绝 `current` 是既定范围，不是查询参数拼错 |
| `internal/ledger/position_baseline.go` | 已有历史来源证明、固定身份、摘要及分页校验；不证明当前现金、在途及交易准入 |
| `sdk/python/relay_sdk/client.py` 的 `reconcile_current_state()` | 顺序 GET 资金、持仓、订单、成交；翻完独立分页不等于同代际快照 |
| `internal/redisstream/ledger_sync.go` | 当前持仓按回包页 upsert、完成页清理旧行；不能直接给这些活动表的分页加一个 UUID 就称完整基线 |
| `internal/events/hub.go`、`internal/events/postgres.go` | SSE 游标来自进程内有限缓冲；PG 通知桥也不是持久账户版本日志，不能充当跨资源提交水位 |

本轮 SDK 源码为 `StreamReconciliation` 增加只读属性：
`consistency_level=independent_reads`、`current_baseline_verified=false`、`missing_security_means_zero=false`。
网络行为不变，不从调用方布尔值取得证明；尚未发布新 wheel。
不得通过改这些文案、连续两次读到相同内容或等待一段静默时间宣布新基线成立。

## 2. 接口与能力边界

首期是一个逻辑证券账户的当前执行事实，不是全产品经济资产、费用终结或期货账户基线。
资金作用域可以明确限定为该交易柜台；华鑫极速柜台可用资金不得改名为证券账户全部现金或产品净资产。
账户中的 ETF、外部委托及不支持的业务影响不能静默过滤；无法覆盖的影响列为缺项。

拟发布能力分为 `accounts.current_baseline.v1`（完整只读合同）与
`accounts.current_baseline.refresh.v1`（显式刷新）。能力不是账户权限；实现、回归及部署就绪后才加入 `/v1/schema`。
只有固定分页、尚无一致性证明时，不发布完整只读能力。历史 `positions.close_baseline.v1` 不改名、不扩义。

| 方法/路径（均待实现） | 语义 |
| --- | --- |
| `GET /v1/accounts/{account_id}/baselines/current?trade_date=YYYYMMDD` | 解析该日最近一次已发布版本，返回 manifest；不得跳过最新失败/部分版本，偷偷退回较早完整版本 |
| `GET /v1/accounts/{account_id}/baselines/{baseline_id}` | 按 ID 重读不可变 manifest，不改成最新 ID |
| `GET /v1/accounts/{account_id}/baselines/{baseline_id}/{resource}?limit=500&cursor=...` | 固定版本的 `positions/orders/fills` 分页；资金及完成证据在 manifest 内 |
| `GET /v1/accounts/{account_id}/baselines/{baseline_id}/validity` | 只读检查当前有效性，返回核验时刻、当前账本版本及失效原因 |
| `POST /v1/accounts/{account_id}/baselines/refresh` | 显式有权限的异步采集；body 为 `trade_date`、`idempotency_key`，返回 202 和 `refresh_id`，不是查询完成 |
| `GET /v1/accounts/{account_id}/baselines/refresh/{refresh_id}` | 返回协调任务及各子查询的命令身份、终态、失败和产出的 baseline ID；GET 不续发任务 |

`trade_date` 必填且按柜台交易日核对，不用服务自然日期补成当前日。
环境取服务部署上下文，券商/路由取受信配置；调用者不能传 env/gateway 绕过隔离。
固定版本读取允许回看已失效事实，但不得据此执行；换账户/环境访问不返回其他作用域内容。
GET 一律 `Cache-Control: no-store`，只读持久状态，不查 OC、不调用 Meridian、不确认结算。
刷新沿用现有查询权限和幂等，读权限不能隐含授予刷新权限。

## 3. Manifest 字段

以下字段位于既有 HTTP envelope 的 `data`。时间为带时区 RFC3339；交易日为 `YYYYMMDD`；
金额/价格使用普通十进制字符串，数量为整数；缺失值为 null，不补零，不重新解释原始报文。
版本号使用十进制字符串避免 JavaScript 整数精度损失，只在同一账户作用域内比较。

| 字段 | 类型与含义 |
| --- | --- |
| `schema_version` | 固定 `relay.account_baseline.v1` |
| `baseline_id` | 不透明稳定 ID；绑定整份 manifest、资源内容及来源版本，不能只由账户/日期生成 |
| `scope` | `environment`（test/production）、`account_id`、`broker_id`、`gateway_id`、`trade_date`、`counter_session_id`、`connection_generation` |
| `publication_generation` | 同作用域持久递增发布代次；不能用 API 进程启动序号替代 |
| `ledger_revision` | 发布事务绑定的账户持久修订号，与 Redis Stream ID、SSE cursor 分开 |
| `collection_started_at/collection_completed_at/published_at` | 分别为采集起点、协调结束、发布提交时间；部分失败也保留真实结束时间 |
| `valid_until` | 固定时间有效期，取所需事实中最早到期值；不是每次 GET 重新续期 |
| `completeness` | `complete/partial/unavailable`；所有必要资源和责任范围覆盖才可 complete |
| `missing_security_means_zero` | 仅整份基线 complete、持仓集合及来源覆盖已核对才允许 true，且只描述本 ID 的持仓时点；缺页、部分或无法核对时为 false |
| `cash` | 可用现金与明确的资金/冻结口径，见下一节；缺失时 null |
| `resources` | positions/orders/fills 各自的 `completeness/total_rows/content_sha256`；未确定总数为 null，不写0 |
| `queries` | 各资源查询的原始完成证据；包含失败和未执行项，不只列成功查询 |
| `consistency` | 发布视图、来源水位及覆盖证明，见第5节；不包含“允许下单”授权 |
| `limitations` | 结构化 `{code,resource,record_ids,detail}`；保留未知责任、冻结范围不明等事实 |

`queries[]` 必须包括 `resource/action/origin_message_id/query_id/counter_session_id/connection_generation/`
`trade_date/started_at/completed_at/status/last_page_seen/page_count/row_count/pages`。
`query_id` 仅在源确实提供时非空；消息身份不能伪称 SDK 请求 ID。
每个 `pages[]` 记录 `page_index/message_id/stream/entry_id/body_sha256/row_count/is_last/received_at`。
摘要核对原 body UTF-8 字节。末页、成功终态、连续页、数量、身份、重复页一致性及归档完整都必须校验。
单一末页不证明前页已齐，原始回调完成不等于标准转换成功；查询覆盖范围需额外注明。
没有开始的一项没有虚构命令或末页，不能仅因为其他三项成功就标完整。
缺少新会话/连接身份的事实可以留作参考，但不可声称已证明同一当前会话。

## 4. 资源与冻结口径

### 持仓

每行提供 `security_id/symbol/exchange/quantity/sellable_qty/frozen_qty/`
`sellable_includes_frozen_deduction/source_observed_at/source_query_message_id`。
证券统一使用既有标准代码，不靠证券名称关联。完整批次构造替换集合，不能把旧活动表未更新的行混入。
多股东/多投资单元只有在证明为不重叠可加作用域时合并；有歧义则保留来源并降为 partial。
已证实数量为零的行可以不进入数量集合，但零仓/删除的来源证据必须可查；完整空查询应明确 `total_rows=0`。

`frozen_qty` 无原始字段或可靠规则时为 null；`quantity-sellable_qty` 还可能包含 T+1 不可卖数量，不能当成冻结。
`sellable_includes_frozen_deduction` 是三值 true/false/null，并保存其字段映射依据。
策略不得对已扣冻可卖量再次扣相同订单；null 时不得猜测，应在消费端限制相应卖出判断。
平均成本、浮盈、日盈亏不作为数量基线完整性的前置，不把费用/绩效工作混入首次冻结。

### 资金

`cash` 包含 `currency/cash_available/cash_total/frozen_cash/frozen_fee/scope/`
`available_includes_frozen_deduction/source_observed_at/source_query_message_id/semantics_evidence`。
`scope` 明确覆盖的柜台/资金账户/币种/属主，不能将可取额度与可用资金相加，也不把极速范围升级为全账户。
未知冻结分项保留 null；`available_includes_frozen_deduction` 同样三值，不凭字段名认定。
普通柜台/ETF待结/逆回购应收不自动加进可下单现金；本基线不计算产品净值。

### 订单与命令责任

`orders` 是本次完整柜台查询事实与 Relay 全部未决责任的并集，不限制未决项的源交易日或旧 gateway。
本日已终态订单也保留以核对成交；跨日已终态只按关联需要纳入，不做无界历史回放。
各行保留稳定 `record_id`、来源 `local/external`、源作用域/会话/交易日、
`origin_message_id/client_order_id/gateway_order_id/order_stream_id`、证券、买卖/业务类型、
`order_qty/cum_filled_qty/leaves_qty/status/is_terminal/execution_state/source_observed_at`。
无柜台 ID 的 created、本地已登记未发布、发布未知及 SDK 执行未知不能因无法 join 柜台单而消失。
命令状态与订单终态分开；失败回执不是取消成交或释放资金的充分证据。
查询未返回跨日委托不能自动判其终结，不能据 pending=0、换日或换会话清空旧责任。
尚不能确定数量/状态的记录保留 null 和限制；存在在途并不自动使事实集合不完整，也不宣称可新执行。

冻结归属逐单记录 `cash_freeze_inclusion/sellable_freeze_inclusion`，取
`included/not_included/unknown`，并附证据引用。本地预留与柜台已冻部分按同一命令身份核对，不能重复扣减。
若缺字段，必须为 unknown；这是消费端风险判断的输入，不让 OC 再作策略审批。

### 成交

`fills` 包含完整本日柜台成交、持续接收的真实成交，以及关联未决订单所需的已留存跨日成交。
每行保留真实成交唯一键、源订单身份/日期、证券、方向、数量、价格、源成交时间、接收时间及来源引用。
使用已验证身份去重；无真实逐笔依据的订单汇总合成成交不得冒充此覆盖证据，也不能与逐笔重复计入。
覆盖范围在 `consistency.fill_coverage` 明确列出：当前日完整查询范围、跨日关联范围、缺失区间及缺项。
OC无历史查询能力时依赖 Relay 持久账本，无法补证就明确缺口，不要求用户每天手工导入。
这些成交用于核对，**不得让客户端再累加到已经查询到的持仓上**。

## 5. 一致性、发布与事件

采用“固定 Relay 发布视图 + 明确来源覆盖 + 失效核验”，不声称柜台跨服务原子快照。

`consistency` 字段至少为：

| 字段 | 语义 |
| --- | --- |
| `level` | `relay_published_view`（仅本地固定可读）或 `reconciled_current`（完成跨资源核对）；二者均非柜台全局原子 |
| `broker_atomic` | 始终 false，未来无新证据不升级 |
| `coverage_verified` | 来源完整、事件覆盖和各资源影响已核清；不能因 PG 使用一次事务便置 true |
| `query_effects_reconciled` | 采集期间的委托/成交/资金/划转等影响是否已与各自查询采样核对 |
| `source_watermarks` | 每物理流分别记录来源标识/部署身份、流名、归档及已应用位点；来源变化或位点缺失明确失败，不把多个 Stream ID 排成全局时间序 |
| `fill_coverage` | 上述成交日期/范围/缺项证明，不用本地“最后一笔时间”代替柜台覆盖 |

实现次序：先按唯一采集 ID 收集原四查询的完整原文，不直接从不断变化的当前持仓表拼装；
再与本地未决命令、实际事件及来源水位核对；最后一个 PG 事务固化全部资源、manifest、账户修订号和发布代次。
读取后续页面只查此 ID 的发布行，不查 latest 活动行。

一期采用保守冲突处理：采集期间发现会影响数量、现金或冻结的变化而无法判断是否已计入某查询，
本次保留 partial/覆盖不明，不按接收时间或一个净成交数量自动修补持仓及现金。
可以在新的显式采集中核对，但不得在 GET 或 SDK 内无限重查。
本地新命令/预留也进入同一账户修订序列，不能只盯柜台订单事件。

**未收到事件不等于没有外部交易。** 两次查询相同、查询终态、Redis lag=0、低延迟或新鲜心跳，
任何一项单独都不证明柜台查询与通知的经济边界。需核验实际查询范围、源序列/采样边界及相关变动的包含关系。
当前协议无法证明时返回 `coverage_verified=false`，可供参考，不能给首次冻结完整证明。
Relay先核实现有华鑫证据；确需新增源字段时只向 OC 索要原生查询范围/序列等事实，不让其建设对账系统。

账户版本与对应写入在同一事务提交，覆盖原文入账、命令登记/预留、外部事件、修正和人工维护等入口。
需要盘点全部写入器；只给一条 worker 加计数器不能宣布完成。失败事务不推进；重复同文不产生新的经济影响。
源切换、断流、处理失败或尚未完成的查询使有效性不可证明，不能仍返回 current。

现有 SSE 只用来提示失效/变化，不充当精确成交增量。未来通知附 `ledger_revision`；
revision 与状态同事务持久化，通知可以重复或遗漏，客户端仍通过 validity 核验。
`relay.gap`、进程 epoch 变化或订阅中断后先停用基线并重读，不把 trigger event ID 当已吸收成交边界。
SDK恢复操作是替换已核对的基线，不是“持仓加 fills”；需要逐笔经济回放的客户端另需持久事件合同，不混在一期。

## 6. 固定分页与有效性

每页 `data` 包含 `baseline_id/publication_generation/ledger_revision/resource/offset/`
`count/total_rows/items/page_sha256/next_cursor/page_complete`。
单页1..500；不接受证券/状态过滤来冒充全账户。固定稳定主键排序，游标绑定账户、ID、资源及分页位置。
`page_complete=true` 仅表示该资源没有后页，不等于其他资源完整或基线仍有效。
每资源最多100000行，超过则明确失败，不截断/忽略在途；历史无关终态不为此无界加入。

摘要格式拟定 `relay.sorted_json.v1`：UTF-8、对象键递归排序、紧凑 JSON、非 ASCII 不转义、
不转义 `/<> &`、无尾部换行，禁止 NaN/Infinity；金额为字符串避免各语言浮点序列化差异。
以 Python `json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)` 为精确编码规则，
Go 实现须由共同非 ASCII/转义/空集/多页向量验证，不能直接假定默认 marshal 一致。
`page_sha256` 是该页 items 数组的 SHA256；`content_sha256` 是固定顺序全资源数组的 SHA256，不随分页大小变化。
原始 OC body 的摘要另存，不能用标准化行摘要替代原文摘要。

validity 返回 `baseline_id/checked_at/status/current_ledger_revision/valid_until/reasons`。
`status` 为 `current/invalidated/expired/unverifiable`；只有同作用域、版本/会话/交易日未失效、
必要来源健康及覆盖验证通过、仍在有效期内才为 current。
一旦失效不能因为余额又恢复相同或重连后安静下来而复活旧 ID。
有效时长为服务端显式策略，响应返回真实 `valid_until`，客户端不得自设“5秒或30秒足够”的证明。
到期比较使用服务端时间；心跳在线不续期资源采样。

页面仍可读与 current 分开：新成交/外部交易/资金变化/新命令预留/修正/会话变化使旧版本失效，
已有固定页面保持原内容，完成分页后必须再次验证。无法读取旧版时返回明确错误，不跳到最新页。

| HTTP/错误（拟定） | 含义 |
| --- | --- |
| 400 `BAD_REQUEST` | 非法参数、重复参数、未知字段或错资源 |
| 403 `PERMISSION_DENIED` | 读/刷新权限不足，不能当空账户 |
| 404 `ACCOUNT_BASELINE_UNAVAILABLE` | 尚无可读发布，不能自动发刷新 |
| 409 `ACCOUNT_BASELINE_SCOPE_CHANGED` | 固定ID与当前请求作用域不符 |
| 410 `ACCOUNT_BASELINE_RETIRED` | 指定ID已不可读，保留身份/原因，不切最新 |
| 422 `ACCOUNT_BASELINE_TOO_LARGE` | 超过明示上限，未截断 |
| 503 `ACCOUNT_BASELINE_VERIFICATION_UNAVAILABLE` | 无法可靠读取/核验，不能回退空结果或旧成功 |

已发布 partial/unavailable 诊断可200返回，SDK严格读取必须拒绝并保留全部证据。
有效性检查200也可能 `invalidated/expired/unverifiable`，HTTP成功不是准入成功。
`current` 只证明核验时刻，不能保证之后永不变化。冻结后、发送前应再次核验；
将来以 baseline ID/revision 作为服务端提交前置并与本地预留同事务比较，防止本地并发复用同一份资金/可卖量。
未接通该前置前，不把只读基线当执行锁或一次性下单许可；外部柜台竞态仍由最后的柜台校验处理。

## 7. SDK 与消费端并行安排

计划方法（当前尚不存在）：

- `get_current_account_baseline(account_id, trade_date, require_complete=True)`：能力握手，解析固定 ID，读完三资源并核验总数、摘要、范围、连续页，最后核验 validity。
- `get_account_baseline(account_id, baseline_id, require_complete=True)`：严格按指定ID读取manifest、三资源全页与最终validity；不解析latest，不在原ID失效时切换新ID。与上一方法复用同一完整性校验实现。
- `get_account_baseline_page(account_id, baseline_id, resource, ...)`：保留原 envelope/页摘要，可诊断 partial，不隐含刷新。
- `validate_account_baseline(account_id, baseline_id)`：独立 GET，返回结构化有效性，不自动替换 ID。
- `refresh_account_baseline(account_id, trade_date, idempotency_key)`：显式 POST，只有调用它才尝试有权限的柜台刷新；未知结果保留原 key/任务身份，不自动以新 key 重发。

严格方法分别报告 unavailable、partial、expired、invalidated、scope_changed、permission_denied、coverage_unverified，
保留 manifest、全部原页、查询证据和失败详情。发现变化停止本次读取，不悄悄重试成另一代，不返回空数组作成功降级。
刷新同 key/同参数复用原协调任务及各子命令，同 key/改参数冲突；子查询是否发出不明时先核对原身份。

Chronos现在可先实现能力缺失停冻、上述模型/错误分支、固定 ID 分页及独立核验流程，使用明确合成夹具。
真实首次冻结须等待 Relay 发布能力和双方实测；目标减当前、在途处理与策略许可仍由 Chronos决定，Relay保留最终服务权限/风险校验。
存在已知在途时可以交付完整事实，不要求清空订单；当前三笔旧单不得因本需求被删除、伪造终态或自动撤销。

## 8. 开发顺序与验收

| 步骤 | Relay交付 | 完成条件 |
| --- | --- | --- |
| CB1 契约与旧接口边界 | 本文、路线图、SDK独立读取标记 | 本轮已提供草案及源码标记；新能力未发布 |
| CB2 固定发布与完整性 | PG不可变候选及内部固定读取、归档四查询组装与跨日未决订单并集已实现；来源覆盖、未落订单的命令责任及HTTP仍待 | 内部501跨页/并发/回滚/原文校验已通过；源查询完整性不因存储或末页检查通过而成立 |
| CB3 修订与来源覆盖 | 已实现本地修订、健康观察、显式来源绑定及不可变收件/账表/位点原子提交；worker接管、源连续性、事件包含关系与有效期仍待 | 本地修订/收件证据不等于源覆盖；资金与可卖冻结不重复计、外部/执行未知保留、会话/日期/修正/断流失效；不能证明覆盖就不发布完整能力 |
| CB4 SDK与刷新 | typed SDK、原页与原因、独立刷新协调、发布能力 | 读不产生柜台命令；同key/未知不重发；SDK固定完整读取与最终validity验证 |
| CB5 联合验收 | 获授权TEST的资金/持仓/委托/成交对照、Chronos独立消费验收 | 不以单方自测代替；通过后才接真实首次冻结及服务端提交前置 |

必测：非空/完整空；501行翻页时新增/清零/成交；只有资金成功；缺末页/缺前页/重复冲突；
本日、跨日、外部及无柜台ID责任；已冻/未冻/未知语义；同ID重读/变环境/会话/日切/到期；
SSE gap/通知丢失；不同资源先后采样及事件包含关系未知；SDK不重复加成交、不自动刷新或补单。

下一开发项为 CB3显式worker接管/收件接线及源连续性、采样期间事件包含关系核验，并补齐未落订单的命令/相关成交责任及有效期策略，再接只读HTTP；不等待 OC 新建任何业务对账功能。
不修改生产环境、账户权限、现有仓位或原订单责任；不执行柜台查询、不部署或迁移在线数据库。

本轮验证：Python SDK全量74项测试通过，包含独立读取的501条分页、空集、SSE游标不能升级为证明及只读属性不可由构造参数覆盖；
历史基线HTTP与完整分页收集定向Go测试通过，明确保留`current`拒绝。后续内部存储并发与失效回归见第9至11节；新HTTP及CB2-CB5在线联合验收尚未执行。

## 9. 固定候选存储增量

11-19后续源码：`internal/ledger/current_baseline.go`、`current_baseline_json.go`及迁移`000052_current_account_baselines`。
这一步只实现存储/读取，不是自动采集器，也未安装HTTP路由或发布capability；不将下面的内部函数当作已可调用的服务端接口。

- `PublishCandidate`在一个PG事务保存manifest、三资源全部行及持久发布代次；仅同环境/账户串行分配代次。中途失败无半份发布，同采集ID/同内容返回原版本，改内容报冲突，不静默修订。
- `Manifest/Latest/Page`只读已保存版本。ID绑定来源作用域、会话/代次、采集ID/时间和内容；缺少会话/代次不创建候选，不由时钟补值。Latest不跳过较新的失败候选或跨会话退回旧结果。
- 每页最多500行、每资源100000行、总候选64MiB、资金与查询元数据4MiB；无截断成功。记录按稳定键排序，重复键拒绝。固定游标绑定ID/资源/位置，保留旧版本，不把活动表变化拼入下一页。
- SQL拒绝更新/删除已发布manifest和行；逐页校验条数/连续位置/行摘要，完整资源摘要供全页消费校验。数据库提交与进程内对象无绑定，重新创建读者可续读。
- `relay.sorted_json.v1`已实现整数/字符串/null及嵌套结构规范化。拒绝重复键、非整数JSON数值、溢出、非法UTF-8/未配对代理转义及过深嵌套；金额仍为十进制字符串。与独立Python编码的含中文/转义/空值向量摘要一致。

**存储本身不提供业务完整证明。** 资源行是待核对观察值；存储接口不代替第10节的来源校验，也不验证经济覆盖。
因此当前实现无条件保留`coverage_unverified`，只输出partial/unavailable，
`coverage_verified/query_effects_reconciled/missing_security_means_zero=false`、
`valid_until=null`。第11节增加本地修订证据；没有该证据的旧候选仍为`ledger_revision=null`。
即便调用者在queries里填completed，也不能升级成complete。
存储条数仅为已保存行数；零行仍可能没有采集到数据，不证明柜台空仓。没有`validity.status=current`或交易授权返回值。

验证：全量`go test ./...`、ledger vet通过；仅Unix socket临时PG执行000052上/下迁移与race测试。
覆盖501行读取中发布新版本、完整旧页重读、空观察、跨日无柜台ID记录保留、跨账户/环境/会话/资源拒绝、
8路同ID并发防重及8路不同ID代次唯一、插入中途失败整批回滚、更新/删除拒绝、缺行/摘要损坏失败。
既有历史close的10001行隔离SQL回归同时通过；线上只读验收按默认跳过，没有连接生产数据库。
尚未证明真实查询完整性、全账户未决并集、跨资源经济一致性或交易有效性；来源组装及本地修订接续见下两节，保持CB2 doing。

## 10. 来源组装与跨日未决增量

11-19后续源码：`internal/ledger/current_baseline_sources.go`与`internal/accountbaseline/`。
仅内部函数和离线/隔离PG回归，没有命令发送器、HTTP接线、后台采集或生产迁移。

- `ReadSources`在一次只读repeatable-read事务中，按四笔明确命令ID读取`body_text`原文和账户全部未决订单。沿用归档的命令方向in、回复方向out；用已有origin索引定位，不扫描整份账户JSON历史。环境必须来自受信部署上下文并匹配scope，不能凭账户号推断环境。
- `Assemble`检查原命令、物理流、账户/环境/路由、动作/result_type、重复身份、接收窗口、唯一成功结束页及页序。保留原文位置和SHA256；缺原文不从JSONB重新序列化补证，失败或晚到矛盾不改成成功空集合。
- 复用现有证券查询解码器，但先检查原始数量/状态字段。缺少数量不补零；资金/价格从原始十进制token精确转成字符串，不经float64；缺费用、可卖/冻结值及扣冻语义保留null或unknown。不会将可用资金解释为产品净资产，也不把数量减可卖当冻结。
- 当前持仓仅来自所选查询，不混入旧活动持仓。重复证券、多作用域无法区分、组件划转覆盖未接通或转换失败会留下具体诊断和原文引用，不把已丢行的子集标完整。不从订单汇总生成成交，也不将成交再次加到查询持仓。
- 订单并集不限制本地未决的交易日、旧路由或是否已有柜台编号。相同不可变意图合为一行并保留两端观察；查询终态不自动消除本地未决。同ID价格/数量等冲突保留双方，并标明不可相加、需核对。当前还未涵盖没有订单行的全部命令、持续事件和关联跨日成交，固定标记`command_and_event_coverage_unverified`。
- 原文实际携带的会话/代次/日期逐项比较，冲突拒绝该资源投影，缺失则明确未核验。`produced_at`只记为`source_published_at`；未知柜台采样时刻为null，不能拿回包发布时间补成经济采样边界。

**已识别的来源缺口：** 当前华鑫对接文档保证结束页，但未约定连续页号/总页数，也未保证每页携带连接代次与柜台采样边界。
因此内部报告分开返回`terminal_sequence_valid`、`context_matched`、`projection_valid`和`archive_complete`。
前几项可按实际原文通过，`archive_complete`仍为false；Stream ID间距、归档行数及末页均不能单独证明无漏页。
失败的`observed_rows`为null；成功投影的计数只说明已观察行数，不代表柜台总数。
后续先核对持久收件/来源位点能提供的等效证明，再列必要的源事实缺口，不要求OC增加业务审批或重做账本。

验证覆盖：原文读取→组装→固定发布→501行分页；跨日/未发布/不明终态保留；同订单观察合并与不可变意图冲突；
缺/空items、缺/双末页、重排、重复身份、错误账户/环境/会话/代次/日期、过滤命令、缺原文与JSON重复键；
金额精度、未知冻结/成交时间、订单汇总不造逐笔、查询失败不丢本地责任。临时PG源表修改后旧发布仍保持原内容。
全量Go测试、ledger/accountbaseline vet、隔离PG race通过；既有close回归保留，在线样本测试默认跳过。

已阅读Chronos消费端评审并接受两项补充：第7节增加按固定ID的完整严格读取方法；
执行接通前另行落实条件提交的幂等优先顺序、同事务版本/预留及自身切片接续，未完成前不把validity成功当执行锁。
当前仍只产出partial/unavailable候选，不发布新capability、不打开冻结或交易权限。

## 11. 本地修订与保守失效增量

11-19后续源码：`internal/ledger/current_baseline_revision.go`及迁移`000053_account_baseline_revisions`。
仅内部接线和隔离PG测试；未迁移线上数据库、重启服务、查询柜台或改变环境/交易权限。

### 同事务修订

- 12张表的实际写入触发修订：账户与路由、订单/订单事件/成交、持仓/资金快照/现金账、ETF划转、撤单尝试、TEST遗留订单处置、原始Stream收件。数据库触发器覆盖这些表的INSERT/UPDATE/DELETE/TRUNCATE；事实写入与修订同事务提交或回滚，完全相同的UPDATE不递增。
- 同账户修订串行递增；账户归属变更同时推进新旧账户。无法归属账户的原文及整表TRUNCATE推进独立全局计数，保守影响全部候选，不把未知来源忽略掉。普通`hb`原文不推进经济修订；心跳健康、来源切换及缺失需另行接持久有效性证据。
- `ReadSources`在同一repeatable-read事务保存`revision_evidence={epoch,account_revision,unscoped_revision}`。三者须共同核对；`ledger_revision`只是其中账户计数，不是源Stream/SSE游标，不能只比较这个数字就认定有效。
- epoch防止显式重装追踪后数值碰巧相同而复活旧候选。修订不允许删除、截断或回退。读取时核验24个观察触发器实际存在且启用；缺失则不继续发布带追踪证明的新候选。无法据此证明此前从未有人停用触发器或绕过数据库权限。

### 发布与只读检查

- 新候选发布先持有同账户锁及全局共享锁，再比较采集时的三段修订证据。有并发事实提交则返回`ErrCurrentBaselineStale`，不消耗发布代次、不保存半份资源；必须重新采集本地来源，不能改旧候选的版本字段强行发布。
- 相同采集身份和相同内容的重试先返回原发布，即使它后来失效；这不是重新认证有效。原固定页保留原`ledger_revision`，不拼入活动表新值。
- 内部`CheckValidity`只读当前数据库事务，不发查询、不写失效状态。修订变化、较新发布或受信当前scope不匹配时返回`invalidated`并列原因；追踪缺失则标`revision_tracking_unavailable`。检查是事务时点观察，不代替提交时锁和资源预留。
- 即使本地版本一致，仍为`unverifiable`，保留覆盖、查询影响、来源健康及有效期未核验原因；不返回`current`，`valid_until`仍为null。缺修订证据的旧版本也不会自动升级。金额/持仓恢复成原数值不回退修订，旧候选不能因此重新有效。

### 验证与剩余边界

隔离Unix socket PG按真实迁移链安装000001至000053，覆盖24个观察触发器、8路并发写入、未提交不可见、回滚、账户隔离/归属变更、无账户原文、普通心跳排除、TRUNCATE及修订不可删除；
覆盖发布等待实际写事务后拒绝旧版本、相同请求幂等、501行旧分页不变、恢复原金额仍失效、会话变化、停用追踪及down/up后epoch变化。
首轮发现数据库连接缓存旧函数OID导致重装后误判追踪不可用，已改按当前目录核验并完整重跑通过。
全量Go、ledger/accountbaseline vet和上述PG race通过；既有历史close回归保留，线上验收仍默认跳过。

本节当时尚缺源健康和会话变化的持久失效，第12节已补已观察变化；源收件连续性、采样期间事件包含关系、未落订单的命令及相关历史成交并集、有效期策略、HTTP/SDK仍待。
触发器会给写入增加同账户锁与计数更新；上线前需评估真实写入负载及事务锁顺序，正常数据库回滚不授权自动重发柜台指令。
未来新增源表或写入路径须补覆盖盘点，不把当前12表观察宣称为全部业务责任已经齐全。

## 12. 来源健康观察与变化失效

2026-11-20增量：`internal/ledger/current_baseline_health.go`、来源组装/validity接线、迁移`000054_account_baseline_source_health`。
没有部署、迁移线上库、修改权限或环境；只使用本机隔离PG和合成输入，不发OC查询或交易。

### 实际接线

- 与四查询和本地未决订单使用同一个只读repeatable-read快照，保存当前物理`hb`流最近收到原文的归档ID、Entry ID、UTF-8 SHA256、收件/发布时间；严格核对账户路由、会话、代次、交易日和broker/Redis就绪字段。缺失或矛盾保留具体原因，不补当前日期/代次，不从旧账户心跳拼装ready。
- `source_health`另存reply/event/hb/dlq四条checkpoint的存在性、处理位点/时刻、历史错误次数、原错误文本及active_error。不存在不填0，当前错误与明确`active_error=null`的历史已恢复错误分开；累计错误次数不自动归零。
- 对已观察的query回复逐页核对本地reply checkpoint是否至少追上该Entry ID；落后或缺失标`reply_checkpoint_not_caught_up`。比较Redis ID的两个整数分量，不按字符串顺序，也不假设编号连续。追上只说明已记录的本地处理位点，不能把`archive_complete`改成true。
- 新来源上下文表只保存健康相关语义。普通心跳的新message_id/时间/累计数变化不推进经济修订；路由、会话、代次、日期、连接/查询就绪及错误状态变化推进同账户修订，原文更新/删除也保守失效。交易权限不由该表产生。
- checkpoint首次建立、删除/截断、处理错误变化、错误计数变化、游标或处理时间回退推进全局未知计数。该旧表没有可信账户列，暂不根据字符串猜账户；可能保守影响同部署其他账户，不会跨独立TEST/生产库操作。正常向前处理不递增健康修订。
- 上述观察与源表变化同事务提交或回滚。故障/换会话后再恢复原状态仍保留递增版本，旧候选不能复活；固定manifest/page保留原观察，`CheckValidity`另外返回当前本地观察。发布时检查新增五个触发器存在且启用，追踪缺失拒绝带健康证据的新发布；相同采集ID重试仍返回原版本。
- 安装/卸载54推进全局计数，避免曾未追踪健康的旧候选沿用相同版本。心跳最近原文使用专用索引，单条原文/每条checkpoint元数据各限1MiB；超限明确失败，不静默截断。

### 已查明的覆盖缺口

当前证券`ledger_sync_loop`先处理一批消息，再单独保存checkpoint；有处理错误时也可能推进位点。
读取失败只写运行日志，既有checkpoint没有持久SourceID/run_id/epoch绑定，也没有每次XREAD的请求起点、原条目集合与原子收件区间。
因此本地checkpoint、收件最大ID、现时lag=0和成功末页不能证明中途未裁剪、未丢页、未断流或已完整应用。
原文归档是可更新的，不可把它当成已经有不可变连续收件审计；修改观察会使基线失效，但不补回过去缺失的消息。

本轮`continuity_verified=false`，`source_continuity_unverified`始终保留；尚未制定基线心跳新鲜度/有效期策略，另列`heartbeat_freshness_policy_unset`，不能把“最近收到”解释为当前新鲜或授权。
`valid_until`仍null，候选只为partial/unavailable，validity仅保守invalidated/unverifiable，不发布新capability。
后续先补Relay持久来源绑定/收件区间与错误中断证据，再核对源查询分页及采样/事件包含关系能否等效证明；不足部分列明确字段需求，不让OC重做账本、策略准入或审批平台。

### 回归

新增6组health测试及组装位点测试，覆盖原文/作用域/镜像冲突/缺字段/未来时间、正常心跳与位点推进不误失效、临时会话变化再恢复仍失效、事务回滚、错误恢复仍保留历史、位点回退/截断、停用追踪、54上/下迁移及固定501行旧页不变。
全量Go与ledger/accountbaseline vet通过；Unix socket隔离PG的race及既有历史close回归通过。真实服务在线数据、断网/淘汰与写入负载尚未验收，不能将合成测试提升为来源无遗漏证明。

## 13. 显式来源绑定与持久收件

2026-11-20接续增量：`internal/ledger/stream_receipts.go`、`internal/redisstream/ledger_receipts.go`及迁移`000055_ledger_stream_receipts`。
这是复用既有证券解析器和Repository的可选内部路径，**未接入运行中worker、未迁移业务库、未重启或切环境**；没有柜台命令或生产Redis操作。第14节已补默认关闭的worker源码与更严格的首次交接条件，以下保留本节当时的实现边界。

### 已实现

- 首次接收须显式登记环境、账户、broker/gateway、物理流角色、部署SourceID、Redis DB及观察到的run_id；四条输出流分别绑定。已有绑定不能重复初始化或静默换源。首次非零位点只是明确的采集起点，不证明此前历史完整。
- 每批保存请求起点、读取前后源观察、实际返回的Entry ID/原body字节/SHA256、应用报告、结果及结束位点。原文为BYTEA，保留空内容和坏UTF-8；批次和条目禁止更新、删除或截断，不覆盖同ID的旧观察。
- 专用RESP2客户端使用只读INFO/XINFO/XRANGE/XREAD，拒绝重复body字段、非预期流、乱序/重复位点及超限。观察run_id/DB、删除/裁剪迹象和已处理锚点原文；已处理锚点变化、读取前后来源改变拒绝应用。同run_id删流重建后改变锚点也会拒绝。
- 现有账本处理、checkpoint、不可变收件、owner核验及cursor在同一PG事务中完成。解析或写账到一半出错时，以savepoint回滚本批业务写入，保留原文及失败报告，cursor不前进并阻断该流后续应用；不得为继续收件清空未决记录。资金成功页缺少账户/核心金额字段也明确拒绝，不采用旧解析器的零值回退。
- 只读采集失败保留失败观察和已取得的合法原文，不推进位点；后续显式重读可追加新观察，但历史fault不清零。同entry原文冲突永久保留双方观察并阻断，不以最后一条覆盖。同attempt与同内容重试只返回原receipt，不再次执行账本处理。
- 新路径的接收者显式接管须提供原owner及精确cursor；事务内隔离陈旧owner，不复用其写入资格。来源/owner/故障变化同事务推进本地修订；普通成功空读/进度统计不递增。新增四流收件诊断进入固定manifest及当前validity，缺表、缺绑定、无采集、失败/未核对历史错误分别列因，不提供交易权限。

### 未完成及启用边界

- 当前旧`ledger_sync_loop`仍运行原路径，不持有上述owner；**新owner不能自动隔离旧worker**。在线启用前须实现并验证停止旧写入、核对旧checkpoint/已应用事实、明确起点及接管配置，禁止两种路径并跑。没有自动从0重扫、自动迁移旧checkpoint或修复历史缺页。
- `local_capture_epoch`只标识Relay本地登记，不是Redis/OC命名空间epoch。run_id未变、锚点一致或一次XREAD成功都不能证明无丢失，尤其不能排除内容完全相同的重建。`continuity_verified=false`及`stream_epoch_verified=false`保持；不将本地receipt升级为query/event采样覆盖。
- 既有`raw_stream_messages.body_text`写入会去掉首尾空白，且仍是可变归档；本轮不改其全局行为。逐字节原文证明只来自新receipt的BYTEA/SHA，查询组装器后续须显式关联两者，不把旧归档摘要说成Redis原字节摘要。
- 结构异常或超限RESP返回会记录`response_decoded=false`，不推进位点；本阶段没有保存任意原始RESP网络帧，不能宣称畸形结构也已逐字节完整取证。合法body解析失败则完整保存在BYTEA。
- 单批最多256条/4MiB原文，证据和报告各最多1MiB；每流累计最多10万批、10万条观察、64MiB原文加证据/报告。不是PG实际磁盘用量或长期保留方案；超限拒绝，不删除历史续额度。正常业务skip与失败原因的在线分类、失败后的显式修复流程、常驻空读频率/保留策略仍需在启用前收口。
- 已有query末页、最新位点及收件“applied”只证明本次本地处理完成，不证明券商完整采样、全部命令/成交责任、跨资源经济一致性或新鲜有效期。HTTP/SDK及Chronos交易前冻结能力仍未发布。

### 隔离验证

测试复用全部证券迁移及真实账本writer，覆盖同attempt并发/响应丢失重试、旧owner拒写、来源变更、二进制原文、读取失败后重读、部分应用回滚、SQL失败整体回滚、不可变记录、容量、健康诊断和55上下迁移。独立Unix socket Redis验证实际RESP收件、显式续接、空读及同run_id删除重建锚点变更；不连接共享TEST或生产Redis，不发送OC命令。复现命令与实际结果见`tests/README.md`当前基线段落。

## 14. 单写者交接与可选worker接线

2026-11-20后续源码增量。没有运行配置变更、服务重启、业务库迁移、环境切换或柜台命令。
不要求OC增加协议或重复业务门禁；本轮工作属于Relay自身收件责任。

### 交接与互斥

- 独立worker、API内嵌同步和手工`SyncLedger`共用逐物理流的PostgreSQL session advisory lock。批量获取按排序进行，部分获取失败释放已持有锁。所有账表及checkpoint写入使用持锁的同一条固定数据库连接；连接失效不能改用连接池继续写入，避免锁已丢失但旧进程仍写账。
- 首次登记`Open`与显式换owner的`Resume`取得同一锁域的事务锁；存在运行中的新版writer时拒绝接管。新收件登记后，即使新worker停止，旧同步入口也拒绝重新写该流。没有自动回退、删绑定或重扫历史。
- 首次登记核对原checkpoint角色、精确位点、累计错误及当前错误；无checkpoint只能从`0-0`登记。已有归档但无checkpoint、归档最大位点超前、跳位点、回退或未核对错误均拒绝。归档落后不补造缺行，因此该检查不是历史完整性证明。
- 在同一事务保存不可变`bootstrap-v1`本地边界记录，包含原checkpoint及归档尾位点；sequence=0，不计XREAD批次数、不作为来源健康成功观察。边界记录和receiver一起提交或回滚，不改旧checkpoint/归档。后续读取保留原owner/cursor；明确更换owner仍需原owner及精确cursor，新配置不得沿用陈旧owner。
- **旧二进制没有上述锁，仍须操作员先明确停掉。** 不能只查advisory lock就认定旧进程已退出。上述互斥覆盖三个常规Stream写入入口，不约束DB管理员、历史修复脚本或所有直接Repository调用，不宣称已阻止任意外部写库。

### 可选独立worker

仅源码支持显式`worker.ledger_receipt_manifest`绝对路径，默认空值保持旧模式；首次只允许`service.environment=test`、`service.mode=worker`、`redis.env=test`及`worker.embedded_ledger_sync=false`。生产配置拒绝启用，API内嵌/手工同步不代替该入口。

私有文件为`ledger.receipt-worker.v1`，结构为`version`及`streams`数组，每项包含已登记的`binding`和`owner`。binding沿用第13节环境/账户/broker/gateway/role/SourceID/run_id/DB字段；不包含Redis口令，不输出owner到基线或日志。必须完整、唯一覆盖当前配置的全部华鑫prefix及reply/event/hb/dlq四流，包括配置仍保留的停用账户来源；不能通过漏列流来伪装完整收件。所有流须与同一Redis来源/run_id/DB及明确账户映射一致。

- manifest限64KiB、普通私有文件，拒绝组/其他用户权限、重复JSON键、未知字段、错误scope和不完整流集合；不从gateway猜逻辑账户。
- 启动先取得全部writer锁，再核验持久owner、来源绑定和边界记录。不会自动`Open/Resume`、创建Stream/消费组、改源或重置cursor。旧版登记若缺`bootstrap-v1`不自动补证，需后续显式处理。
- 接收复用第13节原文及账表事务，每流空读后等待5秒，有数据后250ms继续排空；互不因其他活跃流而提高空读频率。不承诺基线实时性或TTL，保留原累计容量限制。
- 第一轮所有流实际收件成功后才调用worker就绪通知；业务通知及既有自动刷新回调只在本批COMMIT后调用。通知仍是提示，不是持久事件交付证明；提交后、通知前进程中断不会重放账本来补通知。
- 读取/来源/容量或应用失败返回错误并停止本轮，不跳过、不清fault、不自动重试或退回legacy。失败应用持久阻断；读取失败可在明确核对后以原绑定重新启动，但历史错误继续保留。停止过程中若数据库事务未成功提交，不冒称已留下完整receipt。
- 新模式需要额外一条长期固定DB连接，部署前给API/通知等其他工作保留连接容量；持锁连接须直连PG或使用session pooling，不能经transaction pooling把后端会话切换掉。并未调整现有生产连接数或启动新worker。

### 验收与下一项

隔离PG验证旧writer占用拒绝登记/接管、部分锁获取回滚、连接被终止后不能借池继续写、停新worker也不能降回旧同步；明确边界、未知旧错误、位点错配均保留失败。独立Redis验证完整四流worker两次启动使用同owner/游标、不重复应用，通知前已提交原文、空流不改消费组、失败阻断不报ready；严格manifest与旧三入口保护另有回归。

补跑发现并修复同链路的既有排序缺陷：SQL选择`raw_message_pk::text`后以未限定的同名列排序，实际按文本将10排在9之前，可能使查询末页提前；同采集时刻的心跳也可能选到较旧记录。已明确按源表数字主键排序，未决订单源读取同样处理。回归固定分页ID为9/10/11，并验证同时间心跳取数字ID10而非9，不靠随机map遍历碰巧通过。

本节当时未提供自动启用或批量接管命令；后续第15节已补操作员预检/显式交接，仍不自动启用。下一步完善失败处置，并关联原文receipt与查询组装；再落实来源连续性、采样边界及全部在途命令/成交覆盖。生产启用、真实负载及断连/故障修复仍需独立验收。
`continuity_verified/coverage_verified/query_effects_reconciled/missing_security_means_zero`仍false，`valid_until=null`，HTTP/SDK完整基线能力尚未发布。固定候选不能据本轮worker接线变成交易授权。

## 15. 操作员计划与原子交接

2026-11-20接续完成`relayctl ledger-receipts`及迁移`000056_ledger_stream_handoffs`。
本轮只有源码、文档和隔离测试；没有业务库迁移、停止旧worker、启用新worker或连接业务Redis。
仅接受明确的TEST配置和名称以`_test`结尾的数据库，拒绝生产。命令不读取环境变量替换数据库/Redis地址，不提供自动迁移或环境切换。

### 两步执行

1. `plan`为默认模式。使用数据库只读事务及只读Redis观察，核对完整配置的四流集合、来源/run_id/DB、原owner/精确cursor、checkpoint/归档边界和当前锚点，生成私有计划。不会登记receiver、更新checkpoint、创建Stream/消费组或取走业务消息。新版writer占用时拒绝；只读预检也不能证明未改造的旧二进制已停止。
2. `apply`必须另行指定原计划摘要及`-legacy-writers-stopped`。这是操作员确认，不是程序自动停服。执行时重新核验数据库名/schema/本地revision epoch、全部旧状态及源观察，在同一PG事务登记或接管全部流；任一条不符合则全部回滚。不会清除blocked/fault、复用旧写入资格或更改已应用游标。

计划的`handoff_id`和摘要随成功交接永久保存；新增表禁止更新、删除、截断。注册保留第14节`bootstrap-v1`，接管只换owner，旧原文/批次/历史错误不变。数据库epoch是本地修订身份，不是物理PG服务器或Redis命名空间连续性证明。

输入使用`ledger.receipt-handoff-request.v1`，示意如下，四项都须填写实际已核对的binding，不能直接执行占位值：

```json
{
  "version": "ledger.receipt-handoff-request.v1",
  "action": "register",
  "streams": [
    {
      "binding": {
        "environment": "test", "account_id": "EXPLICIT_ACCOUNT",
        "broker_id": "huaxin", "gateway_id": "EXPLICIT_GATEWAY",
        "role": "reply", "source_id": "EXPLICIT_SOURCE",
        "server_run_id": "ACTUAL_40_LOWERCASE_HEX", "redis_db": 0
      },
      "cursor": "0-0"
    }
  ]
}
```

上例仅展示一项结构，实际必须完整列出配置中每个prefix的`reply/event/hb/dlq`，否则拒绝。首次登记`register`不得提供`previous_owner`；`0-0`只适用于没有旧checkpoint/归档的流，有历史时填写精确已处理位点。接管改用`takeover`，每项还须提供当前`previous_owner`和精确cursor，不能凭`$`、当前最大ID或计划时间猜测。整个请求不得混合首次登记和接管。

配置必须显式关闭内嵌同步，包含明确账户映射；工具以外部worker身份验证全部流。提供直接PG连接或session pooling，不使用transaction pooling。输入文件0600、输出父目录0700，使用绝对路径；拒绝输入符号链接、重复/未知JSON字段和已有输出文件。请求/manifest最多64KiB、计划最多4MiB；owner只存私有文件和本地数据库，不作为公开基线字段输出。

```bash
relayctl ledger-receipts -config /private/isolated-test.yaml \
  -input /private/receipt-request.json -output /private/receipt-plan.json

relayctl ledger-receipts -mode apply -config /private/isolated-test.yaml \
  -input /private/receipt-plan.json -plan-sha256 PLAN_DIGEST_FROM_PLAN_OUTPUT \
  -legacy-writers-stopped -output /private/receipt-worker.json
```

`plan_sha256`是工具对解码后规范计划的摘要，不是格式化文件的`sha256sum`；不要手工改计划。预检和执行默认30秒，最多2分钟，不为慢查询无限占锁。成功导出manifest也不会启动服务或改运行配置；启用worker仍属另一次显式操作及验收。

### 失败与取回结果

- 提交前失败不产生部分登记或部分换owner；未确认提交结果时保留原计划，不清表、不新建计划绕过。
- DB已提交但进程响应/manifest写盘失败时，使用**同一计划、同一摘要、新输出路径**再次`apply`。返回持久保存的同一组owner，不再次换owner、不重读Redis、不重放业务；仍核对当前DB及owner。已有writer占锁则拒绝，已被后续接管的旧owner也拒绝导出。
- 文件写入使用排他创建并同步文件/目录；失败可能留下部分文件，不覆盖或把它当成功文件。取回结果不清除后来发生的阻断，worker启动仍独立检查当前状态。
- 源消失/锚点变化、旧checkpoint错误、归档超前、receiver已存在、位点错配、责任阻断等均保持失败。本工具不是修复缺页、清理坏消息或绕过故障的入口。

隔离回归覆盖四流全成全败、计划不写DB、计划过期状态/源变化、SQL末步失败回滚、响应丢失后幂等取回、旧owner隔离、与在途写入竞争、不可变操作记录、私有文件/参数及完整集合限制；真实本地Redis验证计划→登记→worker→接管→续读不重复业务、不改消费组。测试不接业务Redis，也不发送柜台命令。

本节之后的第16节已将不可变receipt与query组装证据关联；完整失败处置和源采样覆盖仍待完成。本工具没有消除来源连续性、经济一致性或当前有效期的缺证。
候选继续partial/unavailable，所有覆盖标记保持false、`valid_until=null`。无需OC为此增加协议或审批，尚不可向Chronos宣称完整current能力已经交付。

## 16. 查询页面与不可变收件关联

2026-11-20源码增量，复用迁移55，无新业务协议或数据库迁移。保持当前生产环境、运行进程和权限；没有连接业务Redis、查询柜台或部署新路径。

### 证据与组装

- 四查询归档、逐页收件关联、receiver健康和未决订单来自同一只读repeatable-read快照。按精确物理`stream_key + entry_id`查已有索引，不按相同message_id、时间接近或金额推测关系，也不跨账户/路由匹配。
- 每个关联批次只读取核验一次，验证原文BYTEA/SHA、批次证据与应用报告摘要、实际条目数量/ordinal/位点顺序、应用结果与结束位点；受原100000行及64MiB预算约束，超限明确拒绝，不截断成成功集合。引用保存attempt、批次序号、ordinal、原文字节数/摘要、起止位点、结果/错误、证据摘要和本地收件时间，不公开owner或完整body。
- `matched`必须有且只有一次成功本地应用，所有保留观察对同一条目的原文一致。只接受旧`body_text`与原文精确相等，或恰好是旧链路`TrimSpace`结果。不存在JSON重排、数字舍入、业务字段修正等宽松等价规则。
- 匹配后直接用receipt原文解析查询。证据的`body_source=durable_receipt`，`body_sha256`是本次解析字节摘要；`archive_body_sha256`独立保留旧归档摘要，`receipt.archive_match`区分`exact/legacy_trimmed`。旧路径仍标`body_source=legacy_archive`。两份摘要不混称原始Redis报文，缺旧原文也不从JSONB补造。
- 失败后同内容重读成功可关联，但所有失败观察仍列在`receipt.observations`，额外保留`reply_receipt_prior_failure`和账户历史fault；不会把第一次失败改成成功，也不会重复累加一页的持仓数量。
- `retained_pages_receipted=true`**只表示当前本地已经看到的各页都有上述关联**。它不是`archive_complete`，不证明未收到的页面不存在，也不证明整批查询成功、柜台采样原子性、命令/事件经济覆盖或交易准入。

### 缺证与冲突

| 逐页状态 | 当前处理 |
| --- | --- |
| `unavailable` / `receiver_missing` | 没有可用的新收件来源；保留旧归档参考组装和缺证标记，不宣称原文收件完整 |
| `before_capture` | 物理位点在明确交接起点之前或等于起点；只能作为旧归档参考，不将bootstrap当成历史XREAD证明 |
| `missing` | 登记起点之后没有对应receipt，拒绝该资源的成功组装，不用checkpoint追平替代 |
| `not_applied` | 只有读取/应用失败收件；原文及失败保留，旧归档成功页不能覆盖此结论 |
| `conflict` / `archive_mismatch` | 同条目原文不一致、重复成功应用或旧归档有实质变化；拒绝该资源组装，不取最后一条、不自动修账 |
| `scope_mismatch` / `binding_mismatch` | 账户路由或物理流不匹配，拒绝，不跨源拼接 |
| `protection_unavailable` | receiver观察或原文/批次不可变保护未启用，拒绝依赖该链路的组装 |

收件内容或摘要损坏、批次结构不自洽会使整次来源读取明确失败。收件健康核验现在同时检查receiver观察触发器和四个不可变触发器；新候选发布前再次检查，停用保护不会被当作已验证原文。旧固定manifest/page仍保存当时的证据，不随归档改写而变化；没有因此复活失效版本。

### 验证与后续

隔离真实schema中经既有收件/解析/账本处理器采集合成四查询，再读取、组装和发布501行分页；覆盖双摘要、失败后成功两次观察、旧归档篡改、仅失败收件、同ID内容冲突、错物理流/账户绑定、登记后缺证、交接前历史、保护停用及原文损坏。Redis读接口在这些新增组装用例中为合成替身，不当作柜台回包；同一隔离脚本仍执行既有本地Redis真实收件回归。

本轮只关联已归档页，没有扫描全流补出未归档查询、处理畸形原始RESP或自动修复failed批次。命令原文仍来自旧归档，没有借此升级命令投递完整性。下一步补失败处置及查询/事件采样覆盖核验，再接全部在途责任和有效期。
所有完整性/经济覆盖承诺仍保持false，`valid_until=null`，候选继续partial/unavailable，HTTP/SDK current能力未发布；OC无需增加二次授权、账本或审批机制。

## 17. 独立命令观察与账本关联

2026-11-20继续补齐仅读取`orders`会漏掉的命令证据。新增内部候选的可选`commands`资源和迁移`000057_account_baseline_commands`，不新增OC wire或交易动作。没有部署、连接业务数据库/Redis、迁移生产、切环境或发送柜台命令。

### 来源和展示语义

- 与四查询、未决订单及本地修订在同一只读repeatable-read事务，按账户读取已归档`cmd.trade`。包含旧交易日/旧gateway，不只取最新N条。新增账户/数字归档ID的部分索引，避免为此遍历该账户的全部行情/心跳历史。
- 这是**归档观察，不是已证明的未决命令清单**。每条保留原`body_text`字符串、摘要、物理流/条目、原命令ID、索引ID、原解析错误和子单序号。`observation_only=true`、`quantity_additive=false`，`execution_verified/reply_coverage_proven=false`。原文金额不经float64重算，原payload以字符串保留，不把档案ID或本地采集时间当柜台订单ID/委托时间。
- 批量报单逐子单匹配`origin_message_id + gateway_order_id`，再核对账户、源交易日、证券/交易所、方向/业务类型、数量及十进制价格。缺源日不从当前日或sent_at猜填。一个子单有订单行不能覆盖其余子单；重复子单身份、同消息不同内容、账本意图冲突、错误账户/环境及畸形原文独立留证。
- `ledger_intent_observed`只表示本地已有相同意图的订单观察，关联记录保留原status/is_terminal，不证明柜台已执行或已终结。历史终态关联放在命令观察内，不重新加入当前未决订单、持仓或冻结数量。相同命令原文的多次归档仍为多条非加总观察，共享逻辑命令键，不冒称多笔委托。
- `unrepresented/unverifiable/ledger_intent_conflict/duplicate_child_identity`不自动补单或修账。撤单单列`cancel_outcome_unverified`，不凭accepted回执、目标订单已终态或成功空查询把本次撤单判成成功，也不改原委托状态。

### 固定发布与边界

每资源最多100000行，本轮另限制命令目标总数100000；原文、关联和输出共同受64MiB预算约束。超限整次明确失败，不静默截断历史。此阶段的有界归档读取尚不是可持续的全部在途责任索引，长期历史超过预算需先改进登记/闭合索引，而不是删历史或按最近日期缩短覆盖。

`commands`沿用固定版本、最多500行分页及摘要。旧三资源manifest/page不改写、不补空集合；读取其commands返回unavailable。新采集即使为空也仅是“本地归档为空”，完整性仍unavailable；新命令或账本变更使修订失效，历史固定页不变。迁移回退在存在任何包含commands的manifest时拒绝，包括零行版本，不能通过删证据降级。

**尚未解决的覆盖缺口**：证券链路当前先发布Redis、再归档，崩溃窗口中缺失的归档无法由本轮补出；账户/stream_role索引缺失或错归类的命令也未获完整覆盖。报单草稿可以保留部分未知责任，但撤单尚无统一发送前登记。命令回执、实时订单/成交及采样包含关系尚未闭合。本轮不把“已关联本地订单”升级为投递、执行或经济完整性证明，也不依据该资源计算可用资金/可卖量。

单元及隔离PG回归覆盖无订单行、部分批量、撤单受理不改终态、旧路由/跨日、原文与索引冲突、重复观察、十进制原文、501条固定分页、后续命令使修订失效、旧manifest不补空和迁移回退保护。仅合成业务数据，不是在线柜台验收。

本节之后的第18节已补默认关闭TEST发送前登记，回执/事件闭合及来源采样和有效期仍待完成；OC不增加账本或第二套交易权限。HTTP/SDK完整current能力仍未发布，所有覆盖标志保持false、`valid_until=null`。

## 18. 发送前登记与未知发布

2026-11-20接续实现证券报单、批量报单、撤单的发送前持久登记，迁移`000058_securities_command_registration`。仅源码、隔离PG/Redis和故障测试，未部署、未执行业务迁移、未连接业务库或共享Redis、未发送柜台命令，当前生产环境及权限保持不变。CTP路径和OC协议不改。

### 单次发送与持久状态

新增三个只追加表分别保存原始命令、发送尝试和Redis发布回执。账户/动作/幂等键唯一，消息ID唯一；保存实际将发送的完整body字符串和摘要，意图摘要不经浮点重算。不可变触发器禁止UPDATE/DELETE/TRUNCATE，任何阶段变更触发账户基线修订失效。存在登记后禁止迁移回退，不删历史来降级。

| 状态 | 已知事实 | 重复请求 |
| --- | --- | --- |
| `registered_not_dispatched` | 登记已提交，未取得发送尝试记录 | 保留原身份，不自动续发；不能据此假造失败订单 |
| `dispatch_unknown` | 发送尝试已持久提交，是否实际写入Redis未证明 | 返回409并提示核对原身份、不要重发；不改为未发送 |
| `published` | 已取得物理Stream ID，发布回执与旧原文归档在同一事务提交 | 参数相同返回原发布结果，不再次XADD；不是柜台受理或订单终态 |

只有首次成功登记的调用可以竞争一次持久发送尝试。登记或尝试提交未获确认时不发送；进程中断后不会自动认领旧登记。发送使用独立Redis客户端，显式关闭XADD自动重试，发送原登记字节，不重新生成message_id或sent_at。读完XADD但丢失响应时仍只调用一次；不以网络错误推定Redis未执行。

发布回执与`raw_stream_messages.body_text`同事务写入；旧归档位置已占用则拒绝，不覆盖。归档写失败回滚发布回执，留下未知责任。回执提交后HTTP响应丢失，可用原幂等键取回原结果；本轮没有扫描器或后台补发器，也不提供用新身份绕过未知责任的修复入口。

单笔和批量重试核对原始登记意图，批量核对完整子单集合及次序，不能把子集当整批成功；撤单在检查目标当前状态前核对原请求，保存独立撤单身份，不用本地订单终态推定该撤单成功。已登记但结果未知均保持409，而不是返回可重试的假“未发送”状态；不同参数复用原幂等键仍是冲突。

### 默认关闭与启用边界

配置项为`service.securities_command_registration`，默认false。首次启用只允许`service.environment=test`、`redis.env=test`、数据库明确命名为`*_test`；运行时校验实际数据库名、完整迁移及不可变/修订触发器。必须使用支持原文单次发送的发布器和真实持久store，缺失时拒绝，不退回旧路径。配置/构造保护不代替部署身份核对；本轮未修改任何运行配置。

新版关闭开关时，若账户已有登记历史，会拒绝新增旧式发布，防止静默降回仅发送后归档。该检查**不是跨版本writer的原子接管协议**；启用仍须先停止旧发送者、核对旧在途责任并统一新版本/开关，不能同时运行未改造旧二进制，也不能把“库里有登记表”解释成全部命令已经受保护。

旧生产路径和只读查询继续原逻辑，未登记的历史发送无法由本轮补出。报单草稿仍先创建，批量草稿创建尚非一个事务；草稿本身不授予发送资格，部分草稿或无登记且无旧归档的重复请求保留未知，不自动恢复发送。没有新增券商确认、交易次数、时段或OC审批机制。

### 基线与剩余缺口

登记与发送阶段从同一repeatable-read来源快照读取，即使没有订单行或发送后归档也独立列在`commands`资源中。保持原文/摘要/原消息和阶段时间，`source=durable_command_registration`，`observation_only=true`、`quantity_additive=false`、`execution_verified=false`。登记与归档是两种非加总观察，不能算成两笔订单或两份冻结。

未安装迁移明确标`command_registration_unavailable`；安装但尚无记录也不证明全writer覆盖。已安装的记录仍带`registered_command_execution_unverified`。资源总行数/预算、固定分页和历史版本不改写沿用既有规则。发送尝试和发布回执变化会使旧候选失效，旧固定页仍保留当时阶段。

本轮尚未关联柜台命令回执、订单/成交/撤单结果，也没有证明跨采样的经济包含关系、来源连续性和TTL。所有完整性标志保持false，`valid_until=null`，不能向Chronos宣称完整current接口已就绪。下一项先做回执及事件闭合，再进行显式TEST启用与真实链路验证；无需OC为此增加功能。

隔离回归覆盖并发登记/发送尝试、修改参数冲突、三类命令成功/丢响应后的重建服务重试、关闭开关防回退、缺迁移拒发、归档事务失败、固定基线不变和降级/触发器保护。另以本机故障TCP端点验证XADD丢响应不重试，Unix socket Redis验证原文字节；均不是柜台实盘或生产启用验收。
