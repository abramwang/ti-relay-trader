# 东吴 TEST 单笔报撤执行单

日期：2026-11-17，Asia/Shanghai。协调与命令发送：Relay；真实柜台执行：OC。

## 14:56用户暂停测试，切回生产

用户判断今天测评时间不足，要求优先完成生产每日任务。Relay已切回生产且下单权限保持关闭，本机无CTP发送/接收CLI运行；未向OC发送报撤、登录或停机命令。此前“争取15:00前完成”不再是当前执行安排，不能按历史窗口继续发单或自行切回TEST。独立CTP数据库、原始回调、位点和未完成修复全部保留，后续按用户新的测试安排恢复，不清旧状态或重用旧会话。

## 14:22更新：交易时段内常驻，优先收盘前完成

用户明确“常驻”仅指柜台交易时段内运行，不是全天运行；今天仍争取15:00前完成原定一手有效报单及对应成功撤单。进程生命周期与每条命令的短期授权/时效分离，不增加报撤次数，不自动重试未知执行或重复结算确认。原13:30至13:35窗口保留为已结束历史，不能借常驻直接复用旧会话；新运行版本、实际session及截止须由OC回传，不能先改Relay门禁迎合未交付模式。

Relay14:21:57只读检查：下午约定路由仍只有原3条心跳，最新13:30:33.778 stopping；两命令流长度0、pending/lag=0，run_id未变，未见新进程心跳。原始检查保存于`outputs/recovery/ctp_dongwu_assessment_pm_20261117/preflight-20261117T062157.481755139Z.json`。本轮没有Redis写入或柜台命令。

成交九段编码、原始50条材料已核验，Relay v2接收及防混合源码已完成，见[最新修正回执](OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)。下方“尚未获取原包/仅候选”是最初退出阶段记录，以最新回执为准。当前等待OC完成运行v2、外部回放/来源证明和查询准入拆分并回新心跳；不再重复原则回签，不绕过必要门禁赶测评。只读与报撤运行入口须各自明确，进程常驻本身不代表ready。

本轮已从`2067bcf`构建`.runtime/bin/relay-ctp-replies`及`relay-ctp-trade`，help零连接预检通过；未启动接收或命令发布进程，9092未重启，OC/生产未改。此前回执中“运行工具尚待构建”由此更新，离线源码通过仍不等同在线报撤通过。

## OC退出反馈已到：先修成交身份

OC报告7d83ccd本轮退出为`callback_invalid_fill / fill_identity_conflict`：SHFE同日TradeID5013的买卖两侧分别属于SysID31174/31234，旧七段键碰撞。Relay使用相同v1契约，已离线重现，不能仅OC改ID后直接上线。OC报告50条启动回放（40订单/10成交）、18 audit_only/32 pending，结算once不存在且未进入确认调用；此为OC审计结论，Relay未获取原包。旧13:35窗口已到期，不自动重启/延长。

下一步直接按[成交身份与启动回放修正](OC_CTP_FILL_IDENTITY_AND_STARTUP_FIX_20261117.md)协调：共享v2复合键纯JSON和版本迁移边界已给出，Relay当前仅离线候选，不接受新wire。旧真实心跳/命令及回调审计不改，不通过换路由或强行ready绕过；等双方实现回归后另约窗口。

## 13:30真实启动后立即停止：当前结果

用户通知OC已启动。Relay13:30:25先观察到新epoch/两命令组，尚无心跳；13:30:37观察到3条真实心跳，新session=`ctpproc-ea3476fac51da091a7bda6bd0dae79e4`。

| produced_at（东八区） | 实际状态 |
| --- | --- |
| 13:30:32.809、13:30:32.813 | broker_not_ready，交易日/连接代次null，transport_recovery_clear=true，pending_callback_count=0 |
| 13:30:33.778 | stopping，TradingDay=20261117、连接代次1，transport_recovery_clear=false，pending_callback_count=32，order_ref_ready=true；柜台/结算/订单快照/报撤ready全false |

三条submit_attempts/cancel_attempts均0；没有出现可查询或可报撤就绪心跳。最后一条仍instrument_missing；回调积压32只是排查线索，不把它或恢复标志直接认定为根因。**Relay未登记或发送新查询/报撤，独立PG仍5笔旧查询、交易发布0。** 两命令流均空、pending/lag=0，不存在Relay等待发送或命令消费积压。终端正确显示OC已停止，历史数据不冒充本会话采样。

已请用户立即转OC：提供退出原因、匹配审计和错误，优先核对登录后自动回调类型/积压及退出条件；同时核对`settlement-confirmation.once.json`、是否调用SDK和是否有匹配成功回调。停止心跳settlement_confirmed=false不能证明结算从未调用或未成功，不擅自重新确认/删除once/重启。原13:35硬截止不顺延，后续需按真实责任核对安排，不自动补测。

13:31:11真实hb原文保存到独立PG，13:31:58与Redis逐条entry/body/SHA核对一致：3条、4443字节，范围1794893432823-0至1794893433793-0，拼接原文SHA256=`521e33abcc917d7eadea7f705bbabf1cd494938b73ce87d4f6c673f36542b907`。新hb owner=`7ab0d180bfd73394f1a2e7340525a4fe0ec8abaef887585af601bd89b1329766`，cursor=`1794893433793-0`；接收器已退出，后续续读必须显式使用该owner/cursor并确认停止，不重新从0覆盖。原始pending保留，不伪造delivered。reply0条，没有创建假回复。

证据：`outputs/recovery/ctp_dongwu_assessment_pm_20261117/heartbeat-capture-20261117T053111Z.jsonl`、`verification-20261117T053158Z.json`及同目录原始预检。没有改环境、生产权限、OC状态或Redis绑定；本轮报撤测评尚未通过。

## 已确定窗口：13:30至13:35

用户转交OC最新安排：部署版本`7d83ccd`，私有配置离线预检通过；OC报告12:55:51新前缀仍0键、run_id一致、本地状态为空。此为OC报告，Relay最近独立占用检查仍为下方12:52:48记录，不冒称已独立核验该部署版本。

- 2026-11-17 13:29+08:00：OC再次复核并按已确认参数初始化新路由；失败停止，不清理/重建。
- 13:30+08:00：OC开启真实TEST测评窗口，按已有一次性授权处理结算确认；随后立即回传实际session、connection_generation、TradingDay、截止及部署版本。
- **13:35:00+08:00硬截止，不自动延长。** 启动延迟不顺延截止；没有取得真实cancelled则记录本轮未通过并保留未决责任，不自动补单、再确认结算或另起窗口。
- Relay收到实际新会话后先核对来源/epoch/组和新鲜心跳，再登记并依次发送订单、持仓两初始化查询；原生约束及新鲜行情满足后执行原定一手买开和一次撤单。只凭计划钟点、部署说明或上午心跳不发送。

目前没有新session/代次/TradingDay的实际回传。Relay未设置自动发单/定时任务，未提前登记或发送占位查询；本节是双方执行安排，不代表已启动或初始化成功。剩余具体步骤沿用下文，不再增加原则性回签。

## 12:53下午候选确认：当前启动口径

Relay接受用户转交的OC旧责任核对与下午新候选，不再等待原则性回签。OC声明两份旧journal共5笔查询均有结果/outbox/发布确认/本地持久ACK，无未决交易或结算once文件；这属于OC本地核验，Relay未独立访问其journal或账户锁。Relay本机独立PG核对仍5条原命令、0交易发布，两旧路由owner/cursor保持原值，未重放旧命令。

12:52:48.192341910+08:00，Relay只读全量SCAN新前缀0键，run_id仍为`94f801dc68e3699cc681feb6dbb48f19e46aef9e`；新路由PG命令/receiver均0。只是当前占用核验，不是预留/初始化成功。原始预检存于`outputs/recovery/ctp_dongwu_assessment_pm_20261117/`。

| 项目 | 已接受下午候选 |
| --- | --- |
| assessment_id | dongwu-assessment-20261117-pm-01 |
| env / broker / account | test / ctp_4900 / 300052141 |
| gateway_id | ctp-dongwu-assessment-pm-20261117 |
| group / consumer | oc-ctp-dongwu-assessment-pm-20261117 / oc-ctp-dongwu-assessment-pm-20261117-owner1 |
| epoch | dongwu-assessment-pm-be85ee43d6c54faa617e394ca36b9e15 |
| deployment_id | shared-test-redis，同一来源，不伪造新服务器 |
| Relay source_id | ctp-test-source-v1:720cf56759dc5a71aaf46f01ba780a6b140c98d6a1ef282ad9d884fa472ca6f1 |
| 接收起点 | 新路由首次从0-0开始；不复制旧owner/cursor，当前尚无新receiver |
| 独立PG | relay_ctp_dongwu_20261117_test，保留旧历史，不应用新迁移 |

已通过用户转交明确回复OC：开窗前再次核实来源、占用、旧责任及单次授权后，可以初始化**这条新路由**的epoch/两命令组；有占用或部分失败就停，不能清键重试。目标为13:30起、最多300秒真实窗口，启动后立即回传实际session/代次/TradingDay/截止及部署版本。当前没有定时启动，也未登记/发送新命令。换路由不新增报撤或结算授权次数。

Relay运行工具已构建，上一轮全Go、PG/race和浏览器检查通过。终端受限配置已改为下午新route/epoch，两个旧gateway仅作历史查询；TEST API已重启，网页报撤继续禁用，生产/OC未动。启动前无epoch/心跳时显示连接不可用是正确状态，不伪造ready。真实命令只在新会话核验后逐项登记并发布。

以下12:45记录保留为历史；其“等待候选”已由本节解决，旧asset-retest绑定仍禁止启动。

## 12:45启动前新增阻断

12:45:31只读全量SCAN确认`relay:test:v1:ctp_4900:ctp-dongwu-asset-retest-20261117`再次0键，epoch、两命令流、reply、event、hb均不存在；run_id仍为`94f801dc68e3699cc681feb6dbb48f19e46aef9e`。不能沿用下面的旧绑定启动或重新初始化成首次空路由。下午13:30窗口目标不变，但必须先完成此次责任核对和新隔离绑定确认。

Relay独立PG仍保留5条命令，交易发布0；本轮未对该真实路由写入/删除/设置TTL，没有新查询或报撤。午间回归曾在授权的随机`relay-dev-*`键进行读写并清理各自开发键，共享Redis的其他键及配置未主动修改；目前没有逐键证据认定本次丢失来源，不将run_id不变当作数据完整证明。暂不继续向共享实例写测试样例。

原始预检：`outputs/recovery/ctp_dongwu_asset_retest_20261117/preflight-20261117T044531.575036243Z.json`。终端显示`epoch_missing_or_changed`，历史查询仍可查看，这不是新JSON解析错误。

已通知OC：核对10:33资金复测及本地journal/outbox/ACK、是否存在结算once标记；未决责任未清不启动，不删除旧证据。请回新的隔离报撤gateway/epoch/assessment_id候选，Relay核对占用并绑定新接收状态，不复制旧owner/cursor、不重放旧命令。用户此前允许临时TEST按新隔离路由继续功能验证，不代表免除来源保护或自动授权重做一次未知结算。所有真实操作等双方具体绑定确认后进行。

## 已确定事项

用户已明确：**今天通过 Relay → Redis → OC 现有主程序 → 东吴柜台 → Relay 回报链路完成登录、至少一笔有效报单及成功撤单。不开发或采用临时直连程序。** 用户另行批准仅本次TEST账户300052141的显式结算确认，不默认开启每天自动确认，不涉及生产。

登录已有真实成功证据。资金请求BizType=31、响应00的失败单独保留，不重复同样的资金验收，不补造响应。也不能为测评把必要的资金、结算或订单快照门禁置真。若OC报单准入仍受资金作用域阻断，请直接指出具体判断及所缺原始事实，不将其藏在统一ready字段后面。

## 固定参数及现场参数

| 项目 | 本轮约定 |
| --- | --- |
| 环境 / 路由 | test / ctp_4900 / ctp-dongwu-assessment-pm-20261117 / 300052141；以顶部下午确认表为准 |
| 原生身份 | BrokerID=4900，UserID=300052141，InvestorID=300052141 |
| 测评模式 | production_mode=false；初始化经确认的下午新绑定，不重建旧丢失路由 |
| 首选合约 | Meridian security_id=RB2701.SHFE，source_instrument_id=rb2701，exchange_id=SHFE；须OC确认东吴测试柜台支持 |
| 开平、方向、套保 | trade_side=B、offset_type=O、hedge_type=speculation、business_type=F |
| 数量 | order_qty=1，单位手；若柜台最小数量不是1，暂停并回报，不自动加量 |
| 订单类型 | 普通限价、日内有效、允许部分成交；不使用IOC/FOK或条件单；请OC核对现有编码，不新增wire字段 |
| 价格 | 执行前重新取Meridian快照，初选买一减10个tick；必须为正、整档、在动态限制内，且经OC柜台原生约束检查。无有效报价/范围不匹配即停，不自动截断/抬价 |
| 撤单 | 收到该笔真实可撤订单回报、取得SHFE OrderSysID后立即撤；沿用该gateway_order_id及trade_date，新增唯一cancel_id |
| 数量预算 | 本测评批次最多一笔order.submit和一次order.cancel；未知结果不换ID重报；若意外成交或拒绝，先报告实际状态再决定后续动作 |
| 截止 | 以OC实际启动反馈的日间可交易窗口为准；不依赖已停止的10:37会话，不自动安排新窗口 |

行情参考不是下单指令：11:06:48.500的RB2701买一2974、卖一2975、tick=1，候选价格为2964，Meridian上下限2870/3173。此价格已经是历史样例，**不能直接拿去现场发送**。Meridian目录版本7d109afad57170d71a7e093ee6b72ac3150f38943b4ccecb3f4e0f40916db8d4；tick=1、乘数10、到期2027-01-15。测试柜台自己的合约可交易状态、范围及资金约束由OC核实，实盘行情不保证和测评撮合同步，限价不保证不会成交。

## 双方并行接线

### OC

1. 在现有主程序接通单笔报单、撤单、实时order.event/fill.event，复用已有持久OrderRef、dispatch、outbox和恢复组件。无自动重报、无临时直连。
2. 本次显式执行已授权结算确认，保留真实请求和回调；完成当前会话的必要初始化/快照，不将既有09:24查询当新会话就绪。
3. 给Relay实际交易心跳JSON、runtime_mode（如存在）、新session、connection_generation、TradingDay、窗口与实际部署版本。查询模式不能发送允许报撤的心跳。
4. 反馈rb2701是否支持、手数/tick/价格约束来源及每个未就绪项。订单支持不足时可建议另一个实际支持的SHFE合约，Relay重取目录与行情，不猜代码。
5. 报单回报保留原命令origin_message_id和native_ids；撤单请求接受与最终cancelled分开。SDK同步返回0不能作为受理/成功撤单证明。

### Relay

1. 独立CLI `relay-ctp-trade`已实现，默认纯离线校验；显式TEST库登记后，只有成对发布开关、短期精确原文授权和新鲜交易心跳才允许一次cmd.trade写入。不是A股HTTP接口，也不自动开终端期货按钮。
2. 发布前持久记录attempted；来源/run_id/epoch/组/最新心跳与时效原子复核，网络响应不明或进程中断时禁止重发。每个测评批次以数据库唯一约束限制一次报单和一次撤单。
3. 撤单要求Relay原始收件库中实际订单回报的receipt_id，核对来源、摘要、原命令、账户/会话/交易日/原生身份和可撤状态；先发单回执不能替代订单事件。
4. 独立接收reply/event/hb并持久保留原文，之后核对订单状态/成交和OC审计，不以Redis发送成功或原始收件pending清零当验收成功。

Relay发布与事件收件代码已完成本轮测试及独立TEST工具准备，详情见下文。**尚未发送任何东吴真实报撤命令，不宣称柜台测评或完整交易业务账本已通过。** 下一步直接取得OC实际交易心跳/窗口后接线执行，不需要再发原则性回签。

## 午间接线确认：c83731d

Relay已接收并校验`reference/assessment-review-c83731d.tar.gz`，原样提取至`testdata/oc-assessment-c83731d-20261117/`；六项SHA清单均一致。接受现有主程序的`test_broker_assessment`心跳模式，普通只读及transport-only仍不能作为报撤准入，不需要再提交原则性回签。

- Relay独立解码七个离线场景的48条心跳、4条订单事件，原文不改；事件可携带顶层`action=order.submit`，撤成事件的origin仍关联原报单，不错误要求它改指撤单。没有向真实账户投递样例。
- 当前会话恢复清楚、登录成功后，允许新`order.list.query`及`account.positions.query`取得初始化快照，不依赖尚未就绪的订单快照反向阻断这两查询。不重复四查询，不重发BizType资金查询。
- 交易授权`assessment_id`须与OC心跳一致；绑定新session/代次/交易日及原SourceID/run_id/epoch。报单另校验持仓快照、OrderRef、次数、原生合约静态约束及5秒内原生上下限；原生tick须与Meridian一致。原生范围与Meridian范围都要满足，不自动改价。
- 撤单检查独立`accepting_cancel_commands`、真实可撤订单身份及一次预算，不依赖原生限价或Meridian行情刷新，不因新单预算已消费而禁止撤单。
- `asset_scope_verified=false`原样保留：本轮仅TEST一手测评，最终保证金与账户风险由柜台检查；未证明本地保证金足额或完整资金范围，不能据此放开普通交易/生产准入。资金旧失败不改成成功或零余额。
- 本次结算确认使用用户已有单次授权。OC若出现已有once文件、未知结果或失败，不删除文件、换状态目录或自动重新确认。
- 终端能识别“柜台测评中”，但网页期货交易按钮继续只读；实际报撤仅走受控CLI。原始收件不伪装业务账本已上线。

用户确认下午13:30起进行。**等双方处理上方12:45路由丢失并收到实际窗口后再启动；当前没有自动定时登录或发单。** OC请提供实际`assessment_id`，启动后给新session、代次、TradingDay和带+08:00的截止时间（最多300秒）；rb2701不变，旧asset-retest状态保留但不能使用其丢失的Redis绑定。不得使用10:37已停止会话。

实时顺序：新登录/单次结算确认 → 两条新初始化查询 → 原生约束就绪 → 现场Meridian行情/原文授权 → 一手买开 → 真实working/SysID → 一次撤单 → 真实cancelled及两端审计。真实行情、心跳或绑定不满足则停，不以离线通过替代。

离线`runtime.examples.json` SHA256为`7c05889470381c0195532bb63c57b6260603c409e1c1df992e89cbf94caef8ed`，与同次runtime-test日志原字节一致。样例业务时钟13:30与原生约束采集时刻不同，原样可解析但不能通过当前实时时效门禁；新鲜度正例仅在测试中用明确合成时钟，不冒充真实柜台样本。OC的39项CTest/三项sanitizer为交付日志，本轮Relay未独立运行其C++源码；真实认证、结算、报撤和错误分支仍待在线核验。

## 最小 wire 样例

以下仅供OC编码/离线校验，review-only标识及旧时间/价格不能发布。作用域绑定来自物理路由及独立配置，顶层不增加routing或自造交易字段。limit_price必须为十进制字符串，order_qty必须为整数。

```json
{"protocol":"relay.stream.v1","message_type":"command","message_id":"review-only-submit-20261117","request_id":"review-only-request-submit-20261117","correlation_id":"review-only-assessment-20261117","idempotency_key":"review-only-idem-submit-20261117","action":"order.submit","sent_at":"2026-11-17T11:06:49+08:00","payload":{"account_id":"300052141","product_type":"futures","schema_version":"ctp.f0.draft.2","gateway_order_id":"review-only-order-20261117","client_order_id":"review-only-client-20261117","trade_date":"20261117","symbol":"rb2701","exchange":"SHFE","business_type":"F","trade_side":"B","offset_type":"O","hedge_type":"speculation","order_qty":1,"limit_price":"2964"}}
```

```json
{"protocol":"relay.stream.v1","message_type":"command","message_id":"review-only-cancel-20261117","request_id":"review-only-request-cancel-20261117","correlation_id":"review-only-assessment-20261117","idempotency_key":"review-only-idem-cancel-20261117","action":"order.cancel","sent_at":"2026-11-17T11:07:00+08:00","payload":{"account_id":"300052141","product_type":"futures","schema_version":"ctp.f0.draft.2","gateway_order_id":"review-only-order-20261117","trade_date":"20261117","cancel_id":"review-only-cancel-id-20261117"}}
```

## 通过与停止

- 通过：真实登录成功；报单有柜台有效受理/订单身份；对应撤单最终cancelled；完整保留可能的成交数量、错误信息和两端命令/回报关联。
- 停止并报告：合约不支持、必要门禁未通过、Redis绑定丢失、报单拒绝、全成而不可撤、拒撤、执行结果未知、超过当前运行窗口。不为凑次数自动换合约/方向/数量或反手平仓。
- 不作为本轮前提：完整产品净值、期货绩效、批量报单、长期故障演练及资金BizType标准化整体通过。未完成项仍保留。
- 不作为本轮授权：生产账户、生产环境切换、重建丢失路由、清理历史证据、每日自动结算确认。

## Relay实现与执行入口

本轮不改wire，不自动启动交易或确认结算。CLI执行文件为`.runtime/bin/relay-ctp-trade`和`.runtime/bin/relay-ctp-replies`；后者新增`--stream-role event`原始收件，仍默认reply。CLI不依赖当前9092环境配置，不自动读取生产DSN或Redis凭据。

### 发送前材料

- `command.json`：真正执行时生成唯一message/request/idempotency/order身份及当前发送时间；只有单笔提交或撤单，不能用上文review-only样例。
- `target.json`：沿用查询工具的source_id、server_run_id、epoch、group、counter_session_id、trade_date、connection_generation，必须由当前OC窗口实际信息确认，不复制已停止会话。
- `evidence.json`：现有RegistrationEvidence结构。报单保存Meridian标准代码/原生代码/交易所/目录版本/校验和与gateway_order_id的绑定；撤单保存明确CommandScope和目标gateway_order_id/trade_date。
- `approval.json`：本地执行授权，不传OC。必填assessment_id、command_sha256（原文SHA256）、expires_at（带+08:00）、cancel_receipt_id、market。提交时cancel_receipt_id=0，market保存security_id/source_instrument_id/exchange_id/trade_date/observed_at/publication_snapshot_id/logical_table_checksum/stale/price_tick/best_bid/lower_limit/upper_limit；所有价格是普通十进制字符串。撤单时market=null，cancel_receipt_id指向独立库实际订单事件原文。
- 发布时授权剩余有效期最多60秒；交易心跳最多5秒，报单行情最多5秒。报价档位、范围与最多买一减10tick的静态偏离检查用精确十进制完成；OC仍须执行其原生约束。准备好二进制后现场一次性取行情、形成原文/授权并立即调用，不能手动编辑几分钟后复用旧报价。

默认离线校验的命令结构如下，变量只引用受限本地材料，不含密码：

```bash
.runtime/bin/relay-ctp-trade \
  --env test --broker ctp_4900 --gateway ctp-dongwu-assessment-pm-20261117 \
  --account 300052141 --native-broker 4900 --user 300052141 --investor 300052141 \
  --database relay_ctp_dongwu_20261117_test \
  --command-file "$COMMAND" --target-file "$TARGET" \
  --evidence-file "$EVIDENCE" --approval-file "$APPROVAL"
```

只登记需`--register --persist-test-intent`；真实发布还需`--publish-trade --allow-test-trade-write --deployment-id shared-test-redis`，并显式提供`RELAY_CTP_TRADE_DATABASE_URL`、`RELAY_CTP_TRADE_REDIS_URL`。没有这些成对开关不会发送。发布结果未知时保留原身份和attempted，不改ID、不换批次绕过限额。重放相同授权只返回原发布结果。

事件原文收件使用既有`relay-ctp-replies`的完整scope/target/database/deployment参数，加`--stream-role event --capture --persist-test-events`；连接变量仍为`RELAY_CTP_REPLY_DATABASE_URL`、`RELAY_CTP_REPLY_REDIS_URL`。只读位点用`--stream-role event --status`。续读必须填写实际`--previous-owner --confirm-stopped --cursor`，不能自动抢占或从新0位点覆盖。事件流尚未产生时不创建假event键；报单后出现真实流再收件。reply原文继续用默认role和原权限开关保存。

### 已验证与尚待完成

- Go全包、定向race/vet、独立PostgreSQL提交前持久化/提交失败/回包未知/完成写入失败/并发单次发布/批次预算/撤单原文核验通过。
- 实际共享TEST Redis随机`relay-dev-*`隔离键通过精确原文单次发布、epoch/心跳变化、过期、错误代次、关闭/只读门禁及event收件/显式续读；合成输入未投递东吴或华鑫真实路由。
- 文档两条wire样例已由现有DecodeCommand自动回归；CLI默认零连接及生产/缺失权限拒绝通过。
- 仅独立库`relay_ctp_dongwu_20261117_test`应用迁移46。原5笔只读命令未变，交易发布记录0；没有业务库/生产迁移或网页交易权限变化。本次午间构建更新TEST运行工具及9092心跳展示，不切换环境、不重启OC。
- 迁移前备份：`outputs/recovery/ctp_dongwu_asset_retest_20261117/before-trade-publication-5rE5Bu.dump`，SHA256 `5af8b34828b5dbdb99b6abe066ff5d09cced3c6503fe546e7e8603876f0ab0ca`。
- 新模式回归已覆盖初始化查询、停止/过期、一次报撤预算、assessment身份、原生报价过期及撤单独立准入；Go全包、定向race/vet、临时PG和共享TEST随机开发路由验证通过。
- **仍待OC实际assessment_id/新窗口、rb2701柜台支持与真实就绪证明。** JSON适配已完成，不再等同样材料；未知模式仍拒绝。event收件只负责原始审计和撤单前独立验证，不把pending标delivered，不冒称终端业务订单账本已发布。
