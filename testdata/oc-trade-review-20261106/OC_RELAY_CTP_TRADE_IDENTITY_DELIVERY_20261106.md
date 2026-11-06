# OC 给 Relay：订单/成交标准化与持久身份交付

日期：2026-11-06，Asia/Shanghai。SDK：CTP 6.7.13，首期范围：SHFE 普通限价期货、投机。

本轮收到用户转交的 Relay 复核：12组意图、48个指纹一致，Relay 已采用64KiB命令、128字节ID、
最多100子单。OC不重复修改这些向量。分页实际输出仍待Relay接收复核，不能提前标记验收通过。

**本轮仅离线开发与验证。F0未冻结，commander运行入口仍关闭；未登录柜台、访问业务Redis、
发送报撤单、确认结算、部署或切换环境。不影响华鑫链路。**

## 1. 补交遗漏的分页材料

Relay所缺文件是 `command_review/query_reply_examples.v1.json`，不是金额向量。
本轮保留2026-11-05交付原字节，未用新程序覆盖旧证据。它包含12组独立场景、15条实际编码reply，
以及6项分页容量观测；其中订单/成交未实现的失败场景描述当时版本，不能视为本轮成功能力。

新样例为 `trade_review/order_fill_query_examples.v1.json`，来自本轮实际C++查询输出，
含12组订单/成交场景。两份材料互补，不交换源码/二进制、不要求Relay执行OC测试。
样例全部虚构，包含夜盘自然日期与TradingDay分离；禁止投递实际Stream。

| 文件 | SHA256 |
| --- | --- |
| command_review/query_reply_examples.v1.json | `b60073574d767acad27866d7727ea1f28c7ed77902984757d129c7a5ec7a640b` |
| trade_review/order_fill_query_examples.v1.json | `2357304ca1be22e8132c02c6b6815f572596c09c1b17afc37e98adb54faa4fa4` |

交付包 `trade_review/oc-ctp-trade-review-20261106.tar.gz` 明确包含上述两份JSON、本文件、上轮
命令分页交付文档及SHA256SUMS。不含真实状态文件、配置、凭据、代码或动态库。
收到后先执行 `sha256sum -c SHA256SUMS`；旧文档中的待办和“尚未共同验证”以其日期为准。

## 2. 分步实现

| 提交 | 实际交付 | 当步验证 |
| --- | --- | --- |
| `9cd18f4` | 原生订单/成交标准化、状态/数量/时间/身份一致性校验 | Ubuntu构建、21项CTest |
| `2c01117` | 账户作用域的本地身份与观察记录持久化、重启后关联 | Ubuntu构建、22项CTest |
| `7bcaf77` | 原命令/ticket绑定、完整查询先保存观察记录再输出标准分页 | Ubuntu构建、24项CTest |
| `ee58039` | 成交先到后的身份/经济条款复核，禁止空查询掩盖关联冲突 | Ubuntu构建、24项CTest |

最终实现基线为 `ee58039`。延迟关联一致性检查见第4节。每步构建/测试通过后单独提交。
本轮最终Ubuntu全量24项CTest通过，包含3个可选JSON schema入口；未启用schema但有Python时为21项。
五个非PIE ASan/UBSan入口通过，含泄漏检查。原PIE间歇异常未关闭，CentOS7和在线恢复未验证。

## 3. 标准订单/成交行

沿用现有 `order.list.query/order_page`、`fill.list.query/fill_page` 和候选schema；
不改Stream、协议版本、实体ID算法或交易accepted回执语义。新增失败原因是本地审计原因，
业务失败沿用 `QUERY_FAILED`，不新增交易错误枚举。

| 内容 | 当前规则 |
| --- | --- |
| 标的 | 原生InstrumentID/ExchangeID；SHFE、B/S、O/C/CT/CY、speculation；不大写、不补标准后缀 |
| native_ids | BrokerID/InvestorID；UserID来自回报，有值时保留，不用登录UserID替换外部来源 |
| 原生字符串编号 | OrderRef/OrderSysID/TradeID只去首尾ASCII空格，保留前导零和大小写；原字节另留审计 |
| 委托请求编号 | 订单的native_ids.sdk_request_id来自OrderField.RequestID；查询SDK请求ID另在query_evidence，成交不补造该字段 |
| 日期/时间 | trade_date使用核验过的TradingDay；submitted_at来自InsertDate/InsertTime，matched_at来自TradeDate/TradeTime，均+08:00 |
| 源时间缺失 | 缺日期或时间为null；非空但非法直接失败，不用采集时间或TradingDay补业务自然日期 |
| 价格 | binary64须能精确往返为现有最多9位小数的普通价格文本，再执行原词法/范围校验；classic locale、FE_TONEAREST；不先四舍五入越界值 |
| 成交费用 | fee=null、fee_complete=false；不把未知费用补零，也不从持仓/资金费用反摊 |
| 数量/关联 | 原始委托量、累计量、余量须自洽；合约、交易所、方向、开平、套保及原生别名与持久身份一致 |

已实现的原生状态组合：AllTraded且数量全成 -> filled；PartTradedQueueing/NoTradeQueueing且
数量自洽 -> working；Canceled且未全成 -> cancelled，保留实际余量；InsertRejected结合无成交的
NoTradeNotQueueing/Canceled -> rejected。CancelRejected/ModifyRejected不把原订单改成拒单。
Unknown、触发状态及其他无法确定终态的非排队组合明确失败，不制造accepted/rejected/cancelled。
这里没有声称覆盖CTP全部价格类型、套利组合或强平状态。

没有独立柜台ErrorID的拒单不猜reject_code；StatusMsg在有效UTF-8时可作为reject_message，
未知编码则留在原字段hex审计，不能伪装成已成功转码。普通成交price必须为正；拒单价格可明确为0，
但仍须通过该订单身份/经济条款检查，不能借此重写本地原意图。

## 4. 持久身份及待关联记录

存储作用域同时包含env、逻辑broker/account/gateway及原生BrokerID/InvestorID/UserID。
进程counter_session_id不进入持久账户作用域，因此同一可信配置下的新进程可读原身份；
原观察的进程session、连接generation、TradingDay和采集时间仍逐条保留。

- 本地订单先从ValidatedCommand登记原gateway/client ID、规范意图字节及指纹、原命令ID和原生FrontID/SessionID/OrderRef。此登记不调用SDK，不等于命令执行日志或放行授权。
- 只有本地别名和经济条款匹配，才能从订单事实补上OrderSysID。SysID别名带TradingDay和ExchangeID；本地别名带TradingDay、UserID、FrontID、SessionID和OrderRef。
- 未查到本地记录不等于外部订单。未知订单和成交先保存待关联证据，相关完整标准查询失败，不假造委托价格/总量绕过订单关联。
- `recordVerifiedExternal`仅是给可信协调层的登记接口，要求独立来源证明引用；本轮未实现证明的自动生成/核验。测试中的external_attestation是明确的虚构前提，不是柜台证明，更不是公开管理入口。
- 已确认外部身份使用既有externalOrderId算法，成交使用独立fillId算法；重启、查询顺序或源UserID变化不能使同一个本地订单改挂external ID。
- 成交先到后补SysID时，重新检查已保存成交的合约/方向/开平/原生UserID及OrderRef，不只比较编号。错配和同fill ID异内容均阻断；去重后的总成交量超过原委托量也阻断，不与可能滞后的订单累计量强行对齐。
- 重复原始行保留各自row_index与采集证据；身份索引不重复计量。但同次完整查询的重复业务键不能被悄悄去重成成功页，而是整次失败，供双方继续核验重复语义。

已索引但未关联或冲突的当日成交不会因下一次查询返回空而变成完整成功。缺原生编号、非法状态等
记录也保存审计，但无法全部建立稳定索引，**尚无通用自动修复/冲正/解除阻断流程**。
没有自动发布“之前待关联的fill.event”；后续重新取得的完整查询可用已恢复身份生成一致ID。
事件补发及与输出去重的衔接仍属于后续工作。

### 本地落盘边界

状态为 `identities.v1.json`，固定账户scope、revision及校验和。目录必须预先存在、归当前用户且
无组/其他用户权限；文件0600。持有本地flock；拒绝符号链接、错误scope、损坏或超限文件、
不同线程或fork子进程复用同一store。不增加跨机器选主/抢占。

写入顺序是独占临时文件 -> 完整写入 -> fsync文件 -> rename -> fsync目录 -> 暴露新内存视图。
写盘失败后该实例不可继续使用，不能返回成功或据此ACK。重启只读取已提交状态文件，
不把遗留临时文件提升成已确认事实。SHA256用于损坏检测，不是防文件所有者/root篡改的认证。

上限：单状态文件16MiB，订单10000、成交索引20000、观察记录20000；批次仍受原查询10000行预算。
这些上限同时生效，不保证达到条数上限前不会先达到字节上限。超限明确失败、不自动清空旧身份。
当前是有界全快照写入，成本随保留状态增长；未完成生产吞吐/磁盘容量验证、自动归档压缩或网络文件系统认证。
存储格式仍是未部署候选，不提供早期开发文件的自动迁移或损坏后静默重建。

## 5. 完整查询与观察证据

`buildIdentityQueryReplies`先通过既有绑定层检查原命令、ticket、请求、可信账户/进程/代次和
完整回包边界，再持久记录本次订单/成交观察；取得身份视图后才生成全部标准页。
错ticket不得改存储。已失效代次、SDK失败或缺真实末页不能被当作完整结果。

每行审计保留关键原生命名字段：身份/合约、业务枚举、数量、价格位模式、订单状态、源日期时间，
字符内容采用hex并标记终止状态。**这不是全部SDK字段或C++结构体内存镜像**。
另保留ticket、SDK请求ID/同步码、回调错误或null、真实last_seen、起止采集时间、原命令关联及
raw_application_decision。不能因为标准转换失败就把原始完成事实改成柜台错误。

完整回包即使因缺TradeID等导致类型化原始快照拒绝应用，也先保存上述可审计字段再失败，
不把这条行丢成空成功。未完成/SDK失败的部分回包仍由原查询结果层保留，**本入口尚未把这类
部分结果自动持久化**；后续统一运行审计接线须补齐，不能把当前存储称为全量回调归档。

只有完整标准校验成功才进入已有有限分页；失败不带items/chunk，不暴露此前已构造的partial。
完整空订单/成交查询在无关联阻断时可成功；完整空资金仍沿原规则失败。分页预算未改变。
本轮不新增publish回调，不生成交易命令查询式final chunk。

## 6. 样例与验证

新JSON含5个成功、7个失败场景，每组一个实际完整reply body，均通过已接收的
`query-page.schema.json`（SHA256 `988891918ed1abc6889832d1aa446a515d77e52ca8d5e9d359f566f63beee47c`）。
这是OC本机验证，不是Relay已验收；schema通过也不证明业务覆盖或柜台状态真实。

| 场景 | 预期 |
| --- | --- |
| fill_first_pending / empty_query_cannot_hide_pending | 待关联成交不丢失；下一次空查询不能掩盖它 |
| order_after_restart / fill_after_restart | 重开store并模拟新进程上下文，order/fill仍挂原gateway_order_id，独立fill_id稳定 |
| duplicate_fill_rows_rejected | 原始重复次数保留，整次标准查询失败 |
| unknown_order_state_retained / cancelled_remainder | Unknown不伪造终态；真实撤成保留部分成交及剩余量 |
| same_fill_conflict | 同fill ID经济内容冲突保留并阻断 |
| unknown_is_not_external | 未找到本地关系不自动分配external身份 |
| verified_external_order / verified_external_fill | 明确虚构的独立来源证明下，两种查询身份一致 |
| missing_id_raw_evidence_retained | 原始快照拒绝不吞行，审计后明确失败 |

另有离线用例覆盖账户隔离、跨交易日同SysID、同OrderRef不同会话、延迟关联的6类错配、
价格边界、未知费用、真实空集合、错ticket/不完整结果、锁冲突/损坏/超限和I/O失败后关闭。
4个本地文件提交窗口使用真实fork子进程 `_exit` 后重新打开检查旧/新完整状态，不是仅纯函数判定；
**不等于物理断电、SDK调用、输出发布或Redis ACK故障注入已通过**。

身份查询样例通过同一测试程序关闭/重开store并更换模拟session/generation，尚不是启动完整OC进程
并接收柜台回放的端到端测试。上述fork退出用例仅独立覆盖本地文件提交窗口。

原始CTest日志位于 `trade_review/ctest_20261106.txt`、`trade_review/asan_ubsan_20261106.txt`，
未重绘或修改工具内容。其SHA256依次为
`d4fbe1b0e6231c3e955acbbf46b63ee2187b391b953f377b80edc533313ebede`、
`af9414cdbe026908dc53bf87a486275b741937eec8b815195797f7d62e97cbb5`。
五个非PIE检查入口是trade_projection、identity_store、identity_query、
query_projection、query_reply；参数为ASAN detect_leaks=1及UBSAN halt_on_error=1。

## 7. Relay需复核与下一步

请Relay接收原分页JSON，再核对新增订单/成交样例中的native_ids、状态/数量、业务自然时间/null、
关联失败形状及重启前后身份。无需重复金额、凭据AAD、12组意图编码或标准标的来源设计。
不要求Relay猜测修正未知订单、丢失日期、重复成交或冲突经济条款。

OC下一步是运行时可信外部来源/身份恢复接线、待关联事件输出、命令登记与dispatch标记、
持久结果/outbox/发布确认/ACK。须逐个证明SDK调用次数、同输出重放与原命令pending状态，
不能用本轮文件原子写入测试宣称九个端到端恢复窗口已闭合。
跨交易日命令登记与账本容量继续由Relay按双方分工推进；OC不改期货交易权限，也不等待
SHFE线上行情才做离线恢复开发。任何在线登录或报撤单窗口仍须用户另行安排。
