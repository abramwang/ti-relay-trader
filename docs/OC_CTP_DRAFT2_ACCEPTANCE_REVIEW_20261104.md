# Relay 对 OC draft.2 共享样例回签的确认

日期：2026-11-04，Asia/Shanghai。来源：用户转交《OC 给 Relay 的 draft.2 共享样例复验回签》，OC 提交 `1aed721/f15a0f7`。本轮确认离线选定约束与材料身份，不冻结 F0、不开放运行入口。

转交前更新：Meridian V1/V2 已整改，Relay 独立复验9例、19项Python测试及假SPI门禁均通过，双方已确认离线收口；SHFE真实样本/生产目录/行情仍未验收。见 [整改复核](MERIDIAN_CTP_V2_RECTIFICATION_ACCEPTANCE_20261104.md)。只需转交本文及必要协议样例，不需要程序或源码包；六份共享JSON和CTP交易wire未改变，不要求OC重复复验已通过的原样例。

## 1. 样例身份已核对

OC 提供的七份指纹与 Relay 提交 **`8381d97`** 的 `testdata/ctp/` 完全一致。该提交才是本批异常/恢复样例的上游基线；`2e25e62` 仅是历史 draft.1，不再作为本批交付标识。

六份 JSON 当前原字节仍不变：

| 文件 | SHA256 |
| --- | --- |
| messages.json | 6e8d2f269557ecbad96c021a315e6a67fa5268e2bcc055d7a490bcc1a6c705c8 |
| query_cases.json | 285bc9276fa4cebb322924104f74e6e03fc3067c289fad64e47a48e65c9e0369 |
| credentials.json | debc867b6604561845a7e9921dccd0ece626f4771ec41e855e8f044293e06cc3 |
| fault_messages.json | ff67da36fcdb036dedb8b0112f33ac6755adf8c8adefc1cd29b9bedee9e9ede0 |
| recovery_cases.json | dd65bdbc69a4f7e7623c4de566f99214feeffcfefe64d534c449fd2779b5b575 |
| meridian_mapping.json | b9bf89287feced8cae6785d3cd26a77ce44023f7a6e33a8beaf4a29d93582666 |

`8381d97` 的 README 指纹为 `59e785874b9b484d665e3d889ca072deaca6d1804efdd4122c048c840d3f6571`。当前 README 已补后续协调/复核说明，hash 不再相同；这不是 JSON 样例变化，不要求 OC 为文档更新重写已留存证据。

## 2. 可以关闭的待办与保留边界

- 已收到 OC 对 draft.2 产品标记、身份、外部 UserID、夜盘、费用 null、批量继承与凭据字节边界的选定约束复验。
- 四个异常样例的源命令关联、失败子项及未知结果语义已由 OC 报告通过，不再列为“尚未提供 JSON”或“尚未回签”。
- 九个恢复窗口的纯判定、十项查询参考表、八项合成标的映射已由 OC 报告通过；合成映射不提供 SHFE 交易准入。
- OC 报告 Ubuntu 11项 CTest、四入口非 PIE ASan/UBSan 通过；Relay 未执行其 C++ 源码，不能改写为 Relay 独立运行通过。CentOS7、原 PIE 问题及实际持久恢复仍未验收。

`parseBatchOrderTerms` 不是完整命令校验器；参考表不等于真实分页应用；ACK 纯判定不等于已实现持久化、重放或崩溃恢复。F0 整体继续未完成。

## 3. 接受 OC 的恢复安全补充

接受“默认不能假定写前保证”。同一已核验命令只有 intent、没有 dispatch 时：

- 已证明每次 SDK 调用之前都同步持久化写前标记：可分类为 proven_not_submitted。
- 该顺序尚未证明：必须分类为 outcome_unknown。
- 两类都不授予 SDK 自动重放权限，not_started 也不授权自动发送。

Relay 已补强测试侧参考函数，写前保证作为显式参数；原九个共享 JSON 字节不变，测试明确以已证明前提运行。另在有/无写前保证两种情形下各枚举64组事实：11组可表达、53组矛盾拒绝。允许本地已知拒绝没有 dispatch；不能为其虚构 SDK 调用。

六个事实字段必须显式为 JSON 布尔值，缺失/null/数字/字符串不得默认为 false。本轮增加42个错误形状及全 false 正例；这些仅是离线测试辅助解码，不是生产账本或 wire 解析器。

ack_safe 仅为参考事实满足门禁；already_complete 不表示需要再发一次 ACK。实际实现仍须证明命令身份、存储顺序、同输出重放和接收端去重。

## 4. 金额候选的 Relay 答复

继续认可 draft.2 的语义方向：SDK binary64 可往返的普通十进制文本、classic locale、max_digits10 后展开指数，不做分厘舍入；未知费用 null，必需无效值不能补零或让旧快照冒充新查询成功。不把这种表达称为恢复券商原始十进制账务精度，也不套用价格九位限制。

本次附件提到 `OC_RELAY_CTP_FAULT_RECOVERY_RESPONSE_20261104.md` 和 money 测试已实现，但未附其内容、具体编码器实现或输入/输出向量。Relay 因此只能确认上述原则，不能认可一个尚未读到的具体哨兵/长度/舍入实现。**不是要求 OC 重做已实现的 serializer**，只需向 Relay 补交已有回签正文及金额候选向量：输入 binary64 位模式、期望文本或错误、字段必需/可选语义，包含负数、正负零、极小/极大值、无效哨兵和非有限值。具体规则经双方复验后冻结，不凭测试入口名称宣告实现验收通过。

## 5. 协议对接材料

按用户最新要求，双方各自实现、测试和部署；交接协议文档、字段定义、消息样例、输入/输出向量及验收结果，不互传二进制，也不要求源码包或运行对方程序。

OC 已收到并复验六份 JSON。本次只需本文；原字段说明可直接查阅 [字段附录](CTP_F0_CONTRACT_DRAFT_20261103.md) 和 [draft.2修正](CTP_F0_DRAFT2_REVIEW_20261103.md)。后续差异按版本、文件指纹、具体报文和预期结果核对，不以共享实现代替协议契约。

此前固定提交源码归档及解压回归属于 Relay 本地历史验证，不是 OC 交付或验收前提，不需要转交或更新。附录仍为候选，不能把文档交付当成完整schema或生产运行时已完成。

## 6. 下一步分工

同日新增交付：[四类查询页字段契约](CTP_QUERY_PAGE_CONTRACT_20261104.md)、`testdata/ctp/query-page.schema.json` 和 `query_pages.json`。请按文档核对字段差异即可，不要求运行Relay程序；原六份共享JSON不变。候选仅覆盖查询输出，不代表完整命令schema或数据库实现完成。

OC 继续四类 wire 转换、完整命令校验和真实持久恢复，向 Relay 补已有金额材料；不重复身份设计、不等待 SHFE 在线行情才写离线代码。Relay 负责完整字段矩阵、未关联成交、同消息重投与不同消息矛盾终态的后续共同样例和独立验证。

Meridian类型校验/请求隔离的离线整改项已关闭。按用户最新指示，SHFE行情直接推进，不等待Trader ReqQryInstrument或账户级约束；[边界修正](MERIDIAN_SHFE_MARKET_BOUNDARY_UPDATE_20261104.md)由Relay协调。可选Trader快照完成证明的connection_generation仍须真实，但不新增OC交易wire字段、不要求OC为行情扫描全目录。交易侧约束和权限继续由OC/Relay独立验收；本轮未授权Relay登录、部署、报撤单或切换环境，不改变华鑫链路。
