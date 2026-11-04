# Relay 致 OC：CTP 统一对接稿

日期：2026-11-05，业务时区 Asia/Shanghai。材料版本：Relay送审版r1。

这是Relay本轮统一交接文档，汇总11月4日至5日的查询、关联、状态与账本方案，替代分批转交内部草稿的方式。**可仅转交本文；它是Relay本轮定稿的送审材料，不是双方已经冻结的生产协议。** 第11节列出OC需确认/补证据的具体事项，未确认项不能默认上线。

不交换源码包/二进制，不要求运行对方测试。OC与Meridian的协调由Relay负责；OC不需要与Meridian直接对接。本文不授权真实登录、结算确认、查询、报撤单、部署、重启、切环境或开放生产权限。既有华鑫生产链路不改。

## 1. 范围与现有基线

- 首期SHFE普通期货、限价、投机；买卖B/S、开平O/C/CT/CY。其他交易所和业务逐项验收，不因已有CFFEX行情而默认具备CTP交易权限。
- 沿用 `relay.stream.v1`、既有Stream及body字段；payload标记 `product_type=futures`、`schema_version=ctp.f0.draft.2`。本文件r1只是材料版本，不替换wire版本。
- OC `1aed721/f15a0f7`选定约束及原共享样例报告通过，不代表Relay独立运行了OC测试，也不代表完整交易协议、金额编码和持久恢复已验收。
- 原六份共享JSON字节不变。本轮增加查询页样例/schema和离线参考验证，没有部署期货解析器、数据库或API/SDK交易支持。

## 2. 路由、身份与标的

命令从可信Stream/配置确定环境、路由券商、逻辑账户和Gateway；现有命令不带routing，不把输出的routing复制回命令。输出routing必须与来源绑定。路由broker_id与原生BrokerID不同，InvestorID与UserID不互填。实际身份关系需授权证据，不从逻辑账号拆后缀。

逻辑账户当前6至20位数字，路由券商1至32位小写字母/数字/下划线/连字符。SDK原生有效字节上限：BrokerID10、InvestorID/AccountID12、UserID15、Password40、AppID32、AuthCode16、InstrumentID80、ExchangeID8、OrderRef12、OrderSysID/TradeID20、CurrencyID3、InvestUnitID16、日期/时间8。不截断，保留大小写和前导零。

OrderRef/OrderSysID/TradeID仅去两端ASCII空格填充；原始值另留审计。其他字段不擅自trim。CTP原生编号保持字符串，不填入股票int64 order_id，也不补0。

行情唯一标准来自Meridian：`security_id/source_instrument_id/exchange_id/product_id/volume_multiple/price_tick`。HTTP/SDK以security_id选择真实合约，Relay显式映射为OC原码symbol/exchange；不拼后缀、不强制大写、不自动把主连换成合约。目录缺映射时保留柜台事实，相关新单失败关闭。

Meridian SHFE行情独立推进，不以Trader ReqQryInstrument或全交易所目录为前置条件。OC提供交易侧约束/权限证据；静态合约检查、行情存在和可下单是三件事。

## 3. 交易命令字段

共同envelope必填：protocol、message_type=command、message_id、request_id、correlation_id、idempotency_key、action、sent_at、payload。ID为非空字符串；sent_at为合法带+08:00自然时间。CTP未知业务字段拒绝，不用默认股票值补齐。

| action | payload必填及规则 |
| --- | --- |
| order.submit | account_id、product_type、schema_version、gateway_order_id、client_order_id、trade_date、symbol、exchange、business_type=F、trade_side、offset_type、hedge_type=speculation、order_qty、limit_price |
| order.batch.submit | 父account_id/product_type/schema_version及非空orders；子项为submit字段，但product_type/schema_version仅在缺省时继承。显式null/冲突拒绝；账户、业务、交易日逐项匹配，gateway/client ID各自批内唯一 |
| order.cancel | account_id/product_type/schema_version、gateway_order_id、trade_date、cancel_id。trade_date是目标订单已确认交易日，不强改为当前日；原生撤单定位从持久映射取得，不接受裸OrderRef替代 |

wire中client_order_id由Relay适配层保证提供；这不要求策略必须手填，SDK/HTTP是否允许生成属于Relay实现。HTTP现有price/qty映射为CTP limit_price/order_qty，不改华鑫旧命令。未来HTTP/SDK期货价格显式字符串/Decimal，不先转float再发送。

新单order_qty为严格JSON整数1至2147483647，不能是1.0、1e0、true或字符串。价格普通正十进制字符串，无指数/空白/正号，最多9位小数、64字节，未缩放整数不超过uint64；报告中的0价不因此成为合法新单价。字段检查不证明价位、手数限额、开平适用性、动态价格带、权限、结算确认或可平量。

trade_date为已确认CTP TradingDay，首次命令身份建立后固定；重试不能因跨午夜换日期。批量不跨账户或产品，候选检查全部通过不代表SDK调用原子成功。Relay离线测试的100子项上限只是本地资源参数，不是双方已约定的生产批量上限。

同幂等键的意图必须绑定路由、交易日、标准合约映射、方向/开平/套保、价格数量、逻辑子单身份及批次顺序；同意图复用原输出、不同意图冲突。撤单尝试有独立cancel_id。精确指纹编码/价格尾零归一化顺序与实际持久恢复仍需第11节确认，不把本轮字段检查当成幂等实现。

accepted/order_action_receipt仅表示命令受理链路结果，不表示委托成交，不新增交易命令查询式final chunk。批量子项仍独立产生事件，accepted_count/failed_count及失败明细必须与实际调用一致。拒撤不把原订单改rejected；执行结果未知不自动重报或重撤。

## 4. 四类查询输出

| action | result_type | 行字段 |
| --- | --- | --- |
| account.asset.query | asset_page | account_id、captured_at、futures |
| account.positions.query | position_page | account_id、symbol、exchange、captured_at、futures |
| order.list.query | order_page | 订单公共字段及数量/状态/原生身份 |
| fill.list.query | fill_page | 成交公共字段及数量/价格/费用/原生身份 |

回复必含protocol/message_type=reply/message_id/origin_message_id/produced_at/routing/action/status/code/result_type/payload/adapter_context。payload必含产品/版本；成功还含trade_date/items，失败不得输出伪造items/chunk。成功status=partial/completed、code=OK；chunk.index从0连续，partial的is_last=false、completed=true，唯一成功末页。

adapter_context.query_evidence必含ticket、sdk_request_id、sdk_return_code、callback_error_id、connection_generation、counter_session_id、snapshot_applied，可选failure_stage。ticket与命令ID/SDK请求号不同。成功须原请求完整且类型化应用成功，SDK请求号正整数、同步返回0、callback_error_id为0或null；null不伪称柜台返回0。失败snapshot_applied=false；queue_expired等明确未发出时SDK三项全部null。

snapshot_applied仅指OC本地类型化应用，不是Relay已落库。成功partial表示北向分页尚未发完，不表示OC可以把部分原始查询当完整快照。OC先提取调度结果、核对本ticket/会话/代次/交易日及类型化结果，再输出；旧缓存可读不能代替本次查询成功。

### 资金与持仓

资金futures.scope：broker_id/account_id/currency_id/biz_type/trading_day/settlement_id全部必填，最后一项非负整数，其余字符串。资金futures还必含balance、available、curr_margin、frozen_margin、frozen_cash、frozen_commission、commission、position_profit、close_profit、deposit、withdraw及scope_verified=false。金额均普通有符号十进制字符串，不套价格9位限制，不分厘舍入，不当作产品会计净资产证明。

持仓futures必含posi_direction、hedge_flag、position_date、invest_unit_id、position、today_position、yd_position、long_frozen、short_frozen、position_cost、open_cost、use_margin。前三项原生单字符，具体枚举仍需适配验证；投资单元显式空串允许，缺失/null不等于空。数量严格非负整数手数，金额字符串；不推断Position等于今昨相加、不跨多空/桶/投资单元净额合并。

资金全部作用域、持仓全部维度分别保留。资金成功页不为空；完整空持仓/订单/成交允许空集合。范围未证实不合计多币种/账户/业务类型，不末行覆盖。失败不清零、不刷新旧数据采集时间。

### 订单与成交

公共必填：account_id/gateway_order_id/counter_session_id/trade_date/symbol/exchange/business_type=F/trade_side/offset_type/hedge_type/native_ids。client_order_id可省略；行产品/版本可省略但有值须一致；captured_at是可选采集时间。

订单还必含order_qty/cum_filled_qty/leaves_qty/limit_price/gateway_status/submitted_at；状态accepted/working/filled/cancelled/rejected。部分成交仍为working并保留累计量，不新增原生状态枚举。累计+剩余不超委托量，活动单闭合，全成累计=委托且剩余0；撤单后的未成交余量可以保留，不意味着仍可执行。源submitted_at可null，不能用接收时间替代。reject_code/reject_message按证据保留，不编造。

成交还必含fill_id/qty/price/matched_at/fee/fee_complete；数量正整数、普通成交价正字符串，源matched_at可null。未知费用null/false，不分摊账户Commission；fee_complete不等于最终交割。更新费用证据不重复计成交、不累加同一记录的多个版本。

native_ids必含原生broker_id/investor_id；实际可得的user_id/order_ref/order_sys_id/front_id/session_id/sdk_request_id保留。订单sdk_request_id仅是OrderField.RequestID，不用查询请求号覆盖。成交必含trade_id和非空order_sys_id，禁止伪造TradeField没有的sdk_request_id；外部UserID不能改为采集登录用户。

## 5. 查询序列与迟到冲突

- Relay先绑定可信原命令、路由、action、ticket、SDK请求号、会话/代次和TradingDay，再组装。支持乱序到达，但收齐0至N前不能发布候选。
- 同message_id、同body原字节重投不重复计数；同ID不同体、同索引换ID、第二个不同成功终态、成功/失败矛盾、跨页重复业务键均保留证据并阻断，不以最后到达者覆盖。
- 字节一致重投包括不重新序列化改变空白/键序；这是持久原输出重放的候选要求，需OC确认是否可保证。不是修改Redis entry ID要求。
- 本地单调时钟控制期限和资源；缺页超时、断线/代次失效不自动续接旧查询。完整查询的原文重投不会因超过原期限而撤回既有完成状态。
- ready仅为完整候选，不等于事务提交、ACK或交易就绪。晚到矛盾终态需标记质量并核查，不能假装回滚外部已消费的数据。
- 原query_cases的terminal_count=2指两份不同终态，不含同一原文重投；本轮不改华鑫已有恢复逻辑。

## 6. 未关联成交：首期不扩展消息格式

本稿选择最小改动路线，**维持现有fill.event/fill_page中gateway_order_id必填，不新增事件种类或临时占位ID**：

1. OC已持久确认gateway身份，但Relay尚未收到订单：可以发送正常成交事实；Relay按原生fill键先保存pending，等真实订单到达再关联，不造假订单满足外键。
2. OC自身尚未确认gateway身份：OC持久保存原始成交和待关联状态。关联有证据后再输出标准成交，重启不能丢失；查询不能因为省略这些行而声称完整成功。未完成关联时保留证据，查询以既有失败路径表达不完整并说明原因，不虚构成功空集合。
3. 已证明的外部订单才沿用external-ctp-v1确定性身份；“本地暂未查到”不等于已证明外部。已知本地订单优先原gateway，不另造external订单再自行合并。
4. 缺原生TradeID/OrderSysID等证据时不补造ID，不强塞规范成交；保留原始异常并明确采集/查询不完整。

Relay内部测试过“无gateway的待关联对象”，仅用于存储设计，不要求OC发送这种wire。本节仍需OC确认其持久暂存和补关联实现/边界；实现前不能宣称未关联成交问题已解决。若OC不能保证保留/重发，须先回报缺口再共同改契约，而非默默丢行或新增私有字段。

## 7. 身份、去重与状态合并

确定性编码沿用 `ctp.identity.v1/{kind}/`，每段为UTF-8字节长度十进制+冒号+原字节。七段依次为env/route broker_id/logical account_id/gateway_id/TradingDay/ExchangeID/规范OrderSysID或TradeID。外部订单为external-ctp-v1-加SHA256小写hex，成交为ctp-fill-v1-加SHA256小写hex。保留原元组核查，不能只留哈希。

成交唯一键不含gateway_order_id；补关联不能变成第二笔成交。原生账户授权、合约/方向/开平/套保及唯一SysID关联须一致；同TradeID不同经济内容、同SysID被两笔实际订单争用均留证阻断，不自动改号合并。去重后成交总量超过原委托量阻断；暂时超过旧订单快照累计量不直接判错，实时与查询可能不同步。

Relay当前保守状态合并规则：

| 证据关系 | 处理 |
| --- | --- |
| 活动状态与累计量共同向前 | 使用整条已验证新证据，不拼字段制造状态 |
| 活动状态与累计量共同落后 | 保留旧证据，不回退当前投影 |
| 状态前进但累计量倒退，或相反 | 不可比较，标记冲突，不凭接收时间猜 |
| 已终态后出现活动单，累计量不超过终态 | 视为旧状态证据，不能复活订单 |
| 已终态后活动单累计量更大 | 冲突，不能无声忽略真实新增执行证据 |
| 两个不同终态、或同终态累计/余量不同 | 保留冲突待核查，不自动纠正为某个终态 |
| 拒撤/命令unknown/命令accepted | 不作为原订单状态终态或成交证明 |

这些是Relay接收侧防回退候选，不声称柜台绝不更正回报。真实更正须有明确源证据与修订语义，不能按该表永久隐藏；第11节请OC给出已有更正/乱序行为证据，无须为排序伪造sequence。

TradingDay独立于自然时间，夜盘不按接收日改日。源时间未知为null；已知时间矛盾留证，合法格式精度不同但同一时刻不算矛盾，比较保留到9位小数的区别。原时间文本和采集时间均不改写。源时间补全、别名/费用修订不代表新的成交。

## 8. Relay 存储与北向责任

Relay扩展期货专用订单、独立成交事实、原生别名/关联、费用版本、查询批次、资金/多维持仓快照；不把期货写入股票现金/市值/成本表。规范事实允许先pending再关联，只有关联表引用真实订单，不靠假订单满足股票外键。

原始体、来源、处理结果和待重试责任先持久保存，再事务提交，之后才推进位点/ACK。ACK不是会计数据已有效的证明。查询成功还需Relay原子发布；资金/持仓可切完整批次，订单/成交查询只能补充事实，不能删除重建或覆盖更晚实时回报。跨查询不是柜台原子快照，各自采样时间分别保留。

HTTP/SDK复用统一交易入口和回调，显式产品/schema/capability分派；价格金额保留字符串/Decimal，时间nullable，期货编号不用int64。普通回调只发布满足关联要求的类型化成交；待关联/冲突质量必须可观察，原始审计不丢失。幂等回调身份固定，费用/关联更新不是新成交。具体API实现和数据库迁移属于Relay后续F1/F2，不要求OC设计或实现Relay表结构。

## 9. 凭据、金额与恢复

凭据沿用oc.secret.v1/A256GCM，CTP明文：credential_type=ctp、credential_schema=ctp.login.v1、broker_id、investor_id、user_id、password、app_id、auth_code。CTP类型与华鑫分派，不能回退使用华鑫密钥或dynamic_password。按外层可信环境/券商/账号/版本/key_id绑定后解密，内层原生身份另验。

AAD原字节为 `oc.secret.v1|{env}|{route_broker_id}|{logical_account_id}|{credential_version}|{key_id}`，UTF-8无BOM/换行/额外空格；版本正整数十进制无前导零。密钥32字节、nonce12、tag16，标准带padding Base64，ciphertext不含tag。公开固定向量仅测试，不能部署；当前样例无需重做。

金额serializer仍按SDK binary64可往返的普通十进制文本方向，classic locale、max_digits10后展开指数，不做分厘舍入，也不宣称恢复原始券商十进制账务精度。OC已报告实现，Relay尚需已有位模式/期望文本或错误向量，明确哨兵、非有限、负零、极值及长度边界；不是要求重写。

恢复：只有有证明的durable写前顺序、身份、SDK发出事实、结果/outbox和ACK链，才能判已完成或明确未发出；缺证明为unknown，不自动重报/撤单。参考真值表不是fsync/崩溃验收。OC与Relay各自提供明确落点的崩溃窗口结果，不交换运行程序。

## 10. Relay 实测与未实现边界

69项Python离线测试通过：17单页、18分页序列、18成交关联、6交易命令、10订单投影；含4状态24种到达排列、不同终态双向冲突、缺页/重复/资源限制、原生身份/费用版本/时间精度。四个Go包回归通过。均为合成证据，不代表OC代码验收、真实柜台、数据库事务或生产可交易。

本轮没有完整事件/heartbeat/查询命令schema冻结，没有持久暂存/SDK调用/数据库迁移/HTTP期货交易适配上线。当前运行服务不应消费这些期货样例。既有资金历史修复和绩效任务不受影响。

机器材料为已有 `testdata/ctp/messages.json / query_pages.json / query-page.schema.json / fault_messages.json / recovery_cases.json / credentials.json / meridian_mapping.json / query_cases.json`。可按具体差异查样例，不需要转发Relay源码、压缩包或二进制。本文已给关键字段和期望行为；需要新共同样例时由Relay补协议材料，不要求OC去找Meridian。

## 11. 请 OC 一次性回签

| 编号 | 需确认或提供的事实 | 不要求做的事 |
| --- | --- | --- |
| C1 | 第3节三类命令严格字段、批量继承/唯一ID、TradingDay绑定和撤单目标日；幂等指纹现有实际覆盖/编码及批量上限 | 不更改华鑫，不把只读字段验证视为放权 |
| C2 | 第4/5节四类查询行字段及证据能否按本ticket完整输出；原文持久重投能否保证，四类查询请求是否仅账户/产品/版本且只支持当前日 | 不由Relay猜查询过滤字段，不为该任务增加历史查询能力 |
| C3 | 第6节维持现有wire的OC待关联持久保存、恢复重发及查询不完整表达是否可实现/已实现；提供已有样例或明确缺口 | 不补假订单、占位ID或私有新事件 |
| C4 | 第7节保守状态合并是否有已知柜台反例、终态更正或特殊余量口径；提供原始证据及实际说明 | 不伪造全局sequence，不仅凭接收时间覆盖 |
| C5 | 已有金额serializer说明及位模式向量、极值/哨兵/长度/尾零边界 | 不重复开发已完成编码器 |
| C6 | 持久命令身份、意图/SDK调用/输出/ACK真实顺序及重启窗口证据；区分离线合成和在线验收 | 不把参考真值表写成实际崩溃验收通过 |

请逐项标记“已实现并有证据 / 已实现待验证 / 需调整 / 存在分歧”，给源码基线、字段差异和样例即可。Relay收到后统一修订schema/样例版本及剩余项；**F0须双方明确冻结，不能因本稿成文或单方测试通过自动关闭。** 无外部阻塞的内部开发可并行推进，不必等SHFE在线行情。
