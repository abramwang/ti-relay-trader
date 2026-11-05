# OC 给 Relay：完整命令校验、意图编码与有限分页交付

日期：2026-11-05，Asia/Shanghai。承接已接收的逐 ticket 查询候选交付。
本轮不重复讨论已确认原则，交付可复核的具体编码、容量及真实离线输出。
**F0 未冻结，运行入口仍关闭；没有登录、真实查询、报撤单、结算确认、Redis 连接、部署或环境切换。**

## 1. 交付及验证

| 提交 | 实际实现 | 当步验证 |
| --- | --- | --- |
| `fcdc22b` | 七类完整命令形状、可信路由/Stream 绑定，独立授权和新意图日期检查 | Ubuntu 编译、16 项 CTest |
| `a6c23a6` | 版本化意图字节、幂等/消息查找命名空间、原始 body 指纹，12 份向量 | Ubuntu 编译、18 项 CTest |
| `09810e4` | 命令与入队 ticket 绑定，完整 reply 编码、有限分页、整批超限失败 | Ubuntu 编译、20 项 CTest |

最终另通过五个非 PIE ASan/UBSan 入口：command、intent、query_reply、query_projection、session；
启用 detect_leaks=1、halt_on_error=1。原 PIE 异常不关闭，CentOS7 与真实持久恢复未验收。
没有修改华鑫代码或发布目录。全部样例为 OC 合成数据，不是柜台回包或 Relay 独立验收。

## 2. 完整命令校验

`validateCommand(raw, trusted_scope, trusted_stream_suffix)` 接收原始 JSON，返回不可由任意 JSON
直接构造的 ValidatedCommand。保留原 body、原 envelope 与独立规范 payload，价格规范化不覆盖审计原文。

- envelope 仅允许 r1 的九个必填字段；不接受 routing、原生账号、额外 null 或未知业务字段。
- 三类交易动作与四类只读查询分别绑定 cmd.trade/cmd.query。环境、路由、Gateway、逻辑账户、BrokerID/InvestorID/UserID 来自调用方可信配置，不由命令选择。
- 重复 JSON key、无效 UTF-8/NUL、非法日期、无 +08:00 的 sent_at、布尔/浮点/指数形式手数、缺失/空 ID 均拒绝。sent_at 支持 1 至 9 位小数，不以自然日期代替 TradingDay；这不是消息新鲜度判定。
- 新单仅 SHFE/F/B或S/O或C或CT或CY/speculation，symbol 保持原码且不超过 SDK 80 字节；不转大写、不补后缀、不推断平今/平昨。
- 批量仅在 product_type/schema_version 缺省时继承；显式 null/冲突、跨账户/交易日、批内重复 gateway_order_id 或 client_order_id 全部拒绝。不继承缺失的子单账户、日期或身份。
- 撤单只接受 r1 的目标 gateway_order_id、目标 trade_date、独立 cancel_id；不接受裸 OrderRef/SysID 定位字段。只读查询 payload 严格仅三字段，无历史或过滤参数。

当前本地资源候选：原始命令最大 65,536 UTF-8 字节（包含空白），深度沿用32；逻辑 ID 最大
128 UTF-8 字节，字符规则为有效 UTF-8、非空、不含 ASCII 0x00 至 0x20/0x7f；标点不被改写。
路由 broker_id 仍是1至32个既定字符，账户6至20位数字。批量最多100子项，仍同时受64KiB限制；
**100不是保证所有100子项均能容纳**。这些容量不是双方已冻结的线上默认。

### 校验不授权执行

`requireTradingEnabled` 在 false 时拒绝全部交易写，包括撤单；不阻断四类只读查询。
它与语法校验分开，便于审计/恢复时读取旧交易命令，不能把 ValidatedCommand 当作放行证明。

`checkNewIntentBinding` 仅用于持久幂等/冲突查找之后建立新意图：新单日须等于可信已确认 TradingDay；
撤单日须等于已确认目标订单日，允许与当前柜台日不同，但必须有匹配账户/原生身份/Gateway/订单的目标。
未知目标拒绝，不拿“格式正确”当找到目标。该结构只是目标绑定检查，尚未提供持久原生撤单定位。
历史重投先读取已保存意图，不因日切重新绑定日期，也不授权重报。
价格档位、动态限价、结算、账户权限、可平量及完整调度仍需后续门禁；本轮没有 SDK 执行调用。

## 3. 意图字节候选 v1

`encodeIntent(ValidatedCommand)` 只编码，不存储、不去重、不自动重放。
与 externalOrderId/fillId 的七段实体身份算法完全分开，未更改原算法或 wire 字段。

定义 `L(s) = 十进制UTF-8字节长度 + ":" + UTF-8原字节`。长度无前导零，不加分隔换行、BOM、
终止 NUL 或 Unicode 规范化；值中的冒号/引号/反斜杠不是额外转义分隔符。
前缀为 ASCII `ctp.intent.v1/`，之后依次拼接以下各字段的 L(s)：

| 顺序 | 字段 |
| --- | --- |
| 固定1至7 | env、路由 broker_id、逻辑 account_id、gateway_id、原生 native_broker_id、investor_id、user_id |
| 固定8至11 | protocol=relay.stream.v1、product_type=futures、schema_version=ctp.f0.draft.2、action |
| submit/batch 后缀首项 | order_count：submit为1，batch为实际子项数，普通十进制整数文本 |
| 每个子项的12字段 | account_id、gateway_order_id、client_order_id、trade_date、symbol、exchange、business_type、trade_side、offset_type、hedge_type、order_qty、limit_price |
| cancel 后缀 | gateway_order_id、trade_date、cancel_id |
| 四类 query 后缀 | 无；不能为原命令补当前日/会话来改变重放身份 |

子项按原数组顺序编码，不排序。数量先严格整数校验后转十进制文本。
价格先检查原始64字节/9位小数/正数/未缩放uint64范围，再去小数尾零；`1.00`与`1`相同，
`18446744073709551615.0`先拒绝，不能先去零绕过范围。编码附录中的字段名只用于审阅，名字本身不编码。
指纹为上述完整字节的 SHA256 小写hex；字段列表和完整编码保留，不能只存hash而丢失核对依据。

### 三种不同身份

| 用途 | 字节规则 |
| --- | --- |
| 幂等查找 | `ctp.idempotency.v1/` + L(env)+L(route broker)+L(logical account)+L(gateway)+L(idempotency_key) |
| 原命令查找 | `ctp.message.v1/` + 相同四项路由 + L(message_id) |
| 原文证据 | SHA256(收到的原始 body 字节)，包含空白、键序和原转义形式 |

查找值分别另给 SHA256。幂等查找不含 action/原生身份，以便同键换动作或凭据身份漂移形成冲突，
而不是落入新命名空间绕过去。原生身份包含于意图，即使 body 相同也必须复核可信绑定，不能只比 raw hash。
意图排除 message_id/request_id/correlation_id/idempotency_key/sent_at、进程会话、连接代次、
SDK编号及凭据版本；经济身份、目标日和批次顺序不排除。没有 security_id/目录版本字段，仍由 Relay 持久绑定标准映射。

同 message_id 不同原文应留证拒绝；不同 message_id 同幂等键只有相同意图才可恢复原输出，
不能重写旧回报关联字段。这里只提供判断所需字节，不声称上述持久判断或输出恢复已接线。
查询需要新采样时须使用新命令/幂等身份，旧键不能因“今天不同”被当作自动重新查询的许可。

12份候选向量含位于字符串中的分隔符/UTF-8、价格同值、传输变化、原生身份变化、批量换序、撤单及四类查询。
实际 C++ 输出与独立 OC Python 字节实现一致；不是 Relay 已经复验或冻结新编码。

## 4. 有限分页及完整输出

`bindQueryReply` 在入队时保存已校验命令与 ScheduledQuery，或在未入队失败边界保存无ticket绑定。
`buildQueryReplies` 校验路由/原生owner/进程、ticket、kind、scope、代次及原入队上下文，再调用上一轮
逐ticket标准转换。不能收到结果后按action套最近一次命令；运行时的pending绑定表仍未接线。
新进程不能把旧job交给此入口补页，当前代次/TradingDay失效不能输出旧成功快照。

现在直接生成完整 reply 信封及紧凑 UTF-8 body，不再由测试补信封。包括原命令四个关联ID、
可信 routing、produced_at、action/result/status/code、payload及原始query_evidence。
produced_at由可信调用层一次提供，所有页一致；不会用它替换柜台业务时间或TradingDay。

| 本地容量候选 | 数值 |
| --- | --- |
| 每页最大行数 | 200 |
| 最大页数 | 256 |
| 整次标准行数上限 | 10,000；仍受原始 QueryBook 行数/8MiB及标准payload 8MiB约束 |
| 单页完整body | 262,144 UTF-8字节，包含信封、所有ID、chunk、审计字段及JSON转义 |
| 整次body总和 | 8,388,608 UTF-8字节，包含每页重复信封，不含Redis entry自身开销 |

调用方可降低这些预算，不能绕过硬上限。分片预留completed终页的实际开销，非末页partial表示
少1字节，因此可能保守少装一行，不会超字节预算。先验证完整候选，再构建全部页；index从0连续，
只有一个completed/is_last=true，其他页partial/false。空持仓单页成功，完整空资金仍失败。

任何行/页数/字节超限，丢弃所有尚未交付的候选页，返回唯一 failed/QUERY_FAILED，
不含items/chunk。query_evidence保留原ticket/请求/错误/代次/会话，snapshot_applied=false，
failure_stage=output_limits；原始完成事实保持raw_completed=true，不冒称柜台失败。
若预算连失败信封都放不下则抛本机错误、返回零页；将来调用者必须保留pending/诊断，不能因此ACK。
这些内部原因不是新增业务错误枚举。

每页 message_id 候选为 `ctp-query-v1-` + SHA256(`ctp.query.reply.v1/` + 不含message_id的紧凑body字节)。
使用当前 JSON 编码器的稳定键序；不是要求 Relay 用另一序列化器重建ID或重新序列化raw。
输出为只读内存字节批次，无逐页publish回调，失败不能暴露此前的partial。
**稳定编码不等于跨进程重放**：必须持久保存完整body及IDs/完成证明，重启后只能重发原字节，
不能重跑编码器、改produced_at、换session或重算当前快照。durable outbox/发布确认/XACK仍未实现。

订单/成交标准行转换继续明确未实现：相应查询可以校验和输出真实失败形状，但没有成功order_page/fill_page交付。
本轮未偷接旧股票式实时回调，也未新增交易accepted回执分页。

## 5. 可转交材料与实际边界结果

只需转交本文件及两份纯JSON，不交换源码/二进制，不要求Relay运行OC测试。
各case是相互独立的合成场景，复用fixture ID不表示可以把不同case当同一条Stream序列；禁止在线投递。

| 文件 | SHA256 |
| --- | --- |
| tests/fixtures/ctp_intent_vectors.v1.json | `cd2e9f38e2d4216a17cfa0802bcff3093256dbd5920ee39fe5e9f5cddf0d69b4` |
| command_review/query_reply_examples.v1.json | `b60073574d767acad27866d7727ea1f28c7ed77902984757d129c7a5ec7a640b` |
| command_review/ctest_20261105.txt | `4daa588055bb209a2b6791001a3ddda7179c89211cfb0e24fc31a99e8bca2624` |
| command_review/asan_ubsan_20261105.txt | `620cff728366d2279c22c028f7752ff1508b061e65b78b2f4e2f4db8af8c53c7` |

实际C++生成12组、15条reply原文，通过Relay提供的查询schema及格式检查，独立Python另核对
字节数、输出ID与页序。查询JSON同时包含本地limits及6项边界观测：

| 合成输入/预算 | 实际结果 |
| --- | --- |
| 单行body需1254字节，预算1254/1253 | 前者completed；后者唯一failed，失败body979字节 |
| 三页需3760字节，预算3760/3759 | 前者2个partial+1个completed；后者仅一个failed，不返回前两页 |
| 每页1行，256/257行，页上限256 | 前者256页；后者仅一个failed，不暴露已构建的256页 |

命令测试还覆盖64KiB恰好/多1字节、100/101子项、128/129字节ID、80/81字节合约及显式继承/冲突。
全量20项CTest含两个可选schema入口；不启用schema依赖但有Python时为18项。测试依赖沿用上一轮
build/schema_deps，不增加生产Python依赖。两份日志为工具原始输出，未重绘/修改内容。

## 6. Relay 需复核及下一步

请只复核本轮新增的精确意图字节/命名空间向量，以及128字节ID、100子项和上述分页容量候选；
若容量需要更小，可按双方较小值继续调整，不把当前值写成已在线验收。金额、实体身份、凭据AAD、
原六份样例、标准映射职责不重开设计。failure_stage=output_limits仅是审计值，业务仍QUERY_FAILED。

OC下一步仍是原始订单/成交标准化、持久身份和未关联成交，随后持久命令/结果/outbox/ACK及真实
崩溃窗口。完整命令语法、意图编码、有限分页组件已交付，不再列为“没有实现”；实际Stream调度、
授权门禁接线与恢复未完成仍明确保留。本轮认可不构成F0冻结或任何在线操作授权。
