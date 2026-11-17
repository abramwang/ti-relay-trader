# 东吴 TEST 单笔报撤执行单

日期：2026-11-17，Asia/Shanghai。协调与命令发送：Relay；真实柜台执行：OC。

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
| 环境 / 路由 | test / ctp_4900 / ctp-dongwu-asset-retest-20261117 / 300052141 |
| 原生身份 | BrokerID=4900，UserID=300052141，InvestorID=300052141 |
| 测评模式 | production_mode=false；保留本轮绑定，不重建旧丢失路由 |
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
  --env test --broker ctp_4900 --gateway ctp-dongwu-asset-retest-20261117 \
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
