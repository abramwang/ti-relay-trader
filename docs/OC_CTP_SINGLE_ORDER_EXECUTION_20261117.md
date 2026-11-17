# 东吴 TEST 单笔报撤执行单

日期：2026-11-17，Asia/Shanghai。协调与命令发送：Relay；真实柜台执行：OC。

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

## 心跳对接的具体差项

Relay目前支持原draft.2普通心跳、test_transport_recovery_only及test_broker_read_only。后两者不能作为交易准入；若OC新增运行模式，请直接给实际JSON，Relay同步适配，不为赶时间忽略未知模式。

报单最低检查redis_ready、broker_ready、settlement_confirmed、order_snapshot_ready、accepting_trade_commands；撤单单独检查accepting_cancel_commands，不因报单关闭就阻止合法撤单。还需adapter_context已有connection_generation、transport_recovery_clear与当前绑定匹配。即便这些字段通过，OC仍负责最终原生合约/账户/风控检查，Relay不声称一个布尔值证明完整资金经济范围。

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
- 仅独立库`relay_ctp_dongwu_20261117_test`应用迁移46。原5笔只读命令未变，交易发布记录0；没有业务库/生产迁移、9092重启或网页权限变化。
- 迁移前备份：`outputs/recovery/ctp_dongwu_asset_retest_20261117/before-trade-publication-5rE5Bu.dump`，SHA256 `5af8b34828b5dbdb99b6abe066ff5d09cced3c6503fe546e7e8603876f0ab0ca`。
- **仍待OC真实交易模式JSON/部署及窗口、rb2701柜台支持与必要就绪证明。** 未知runtime_mode仍拒绝，拿到实际样例后适配，不临时放宽解析器。此处event收件只负责原始审计和撤单前独立验证，不把pending标delivered，不冒称终端业务订单账本已发布。
