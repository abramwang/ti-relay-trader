# CTP F0 字段、身份、凭据与样例附录

版本：`ctp.f0.draft.1`，2026-11-03，Asia/Shanghai。**Relay 首批候选交付，待 OC 对照 SDK 确认；未冻结、未部署。**

本附录接续 [开发规划](FUTURES_TRADING_SUPPORT_PLAN_20261103.md) 和 [F0 回复审阅](OC_CTP_F0_FEEDBACK_REVIEW_20261103.md)。可以直接转给 OC，使用第 7 节命令做离线验证；不需要 Redis、数据库、真实账号或 SDK 会话。

## 1. 本轮与 M2 的关系

已阅读 OC 源码基线 `7c4e49a` 的交付说明：有界队列、结果回收、快照/合约缓存接线和静态约束已由 OC 报告实现；Relay 没有独立运行 OC 构建，不能把其 7 项 CTest、非 PIE 50 次重复通过写成 Relay 验收通过。原 PIE 异常仍待跟踪，M2 不标整体完成，主程序仍不允许正常启动。

本轮新增可执行的离线契约样例及测试，不修改生产 Go 模型/解析器、SDK、迁移、权限、凭据或任务。**当前 Relay 不能安全消费本附录中的期货 payload，禁止将样例发布到任何业务 Stream。**

OC 排队 ticket 是本地任务身份，不是北向 message_id/request_id、SDK request_id 或命令回执。所有时间边界仍采用 OC 本次报告的单调时钟：队列/在途/未取结果合计上限 4，排队路径串行且请求间隔至少 1 秒，默认任务期限 60 秒、范围 1-300 秒，响应不超过 15 秒及剩余总期限；不据此修改 Relay 线上超时配置。

## 2. 版本、路由和公共结构

以下均为新 CTP 路径的候选约定。已部署华鑫结构及旧数据不改。

| 字段/对象 | 类型和约束 | HTTP / SDK / 存储 |
| --- | --- | --- |
| protocol | 固定 `relay.stream.v1` | 复用现有传输层，不换总线 |
| product_type | CTP payload 必填 `futures`；未知值拒绝/隔离 | API/SDK 显式类型分派；数据库期货表不走股票默认值 |
| schema_version | CTP payload 必填 `ctp.f0.draft.1`（仅离线） | 冻结发布时明确替换版本，不静默接受未知版本 |
| account_id | Relay 逻辑账户字符串，同环境唯一 | 本轮示例 `990000000001`，只是虚构数字账号，未分配；暂不扩展现有数字账号校验 |
| routing | 输出消息必含 env/broker_id/gateway_id/account_id | 候选 route 为 `test/ctp_4900/990000000001/990000000001`，不等于原生身份 |
| 命令 envelope | 沿用 protocol/message_type/message_id/request_id/correlation_id/idempotency_key/action/payload/sent_at | 现有命令没有 routing 字段，按 Stream 路由；不得把输出消息的 routing 直接照搬进命令 |
| Stream | `relay:{env}:v1:{broker_id}:{gateway_id}:{suffix}`，entry 只有 body | 六类 suffix 不变；输出 routing 与 Stream、可信账户绑定必须匹配 |
| trade_date | wire 为 8 位日期字符串，语义是已确认 TradingDay；不得用接收日替代 | API 现有 ISO 日期查询口径保留，适配层明确转 YYYY-MM-DD，数据库 DATE，SDK 不按 matched_at 重算 |
| 发生时刻 | RFC3339，+08:00；无法确认自然日期时 null | 与 produced_at/captured_at 分开存储；空值不降级为接收时刻 |
| 批次/代际 | origin_message_id 标识查询；会话及连接代次另存 | 只在同一请求/作用域的完整结果上替换快照，不宣称跨查询原子快照 |

asset_page/position_page/order_page/fill_page 的 CTP payload 统一带产品判别、版本、交易日和 `items`。**资产使用 items 多行，而不是华鑫 payload.account 单行**，后续实现必须先按类型分派，防止把缺少股票字段读成零资产。

## 3. 交易字段矩阵

核对当前 `internal/orderflow/service.go` 后确认：现有华鑫命令直接发送 SubmitOrderRequest 的 `price/qty`；`limit_price/order_qty` 是已有订单回报常用字段，并非现有命令统一转换。原规划在此表述不准确。下表给出的是 **CTP 专用候选映射**，需要新增适配代码及 OC 确认，不能声称现在已有实现。

| HTTP / SDK 输入 | CTP wire 输入 | 类型/规则 | 账本/API/SDK 输出与实现计划 |
| --- | --- | --- | --- |
| account_id | account_id | 字符串，必须与路由绑定相符 | 复用逻辑账户主键 |
| gateway_order_id / client_order_id | 同名 | 发柜台前固定 gateway ID；缺 client ID 不捏造源客户端编号 | TEXT；首次提交意图和重试关联持久化 |
| price | limit_price | **F 候选采用普通十进制字符串**；正值，最多 9 位小数，无指数/空白/正号，不先转 float | SQL NUMERIC；期货 SDK 用 Decimal/字符串；旧股票 float 接口不变 |
| qty | order_qty | JSON 整数，正数、单位手，拒绝小数和溢出 | BIGINT/SDK int；另按真实合约限价手数范围检查 |
| symbol / exchange | 同名 | 完整原生 InstrumentID，保留大小写；首期 SHFE | TEXT；与 Meridian security_id 显式关联 |
| business_type | 同名 | F | 订单和成交均保留；不复用 S/E |
| trade_side | 同名 | B/S，买卖不等于多空 | 订单/成交均保留 |
| offset_type | 同名 | O/C/CT/CY 必填 | 订单/成交均保留；原生 OffsetFlag 映射及适用性须 OC 签认 |
| hedge_type | 同名 | 首期 speculation 必填 | 不隐式支持套保或套利 |
| idempotency_key | envelope 同名 | 同键同意图返回原结果；不同意图冲突 | 账户路由、合约、方向/开平/投机、精确价格/数量进入指纹 |
| orders | payload.orders | 批量每个子单独立 gateway/client ID，共享命令 message_id | 逐子单回执关联，不宣称柜台原子批量 |
| cancel_id | payload.cancel_id | 独立撤单尝试身份；目标 gateway ID +交易日明确 | 不用裸 OrderRef 定位；原生撤单参数由持久映射恢复 |

价格字符串是待 OC 确认的明确设计选择，不代表 M2 内部字符串形式已经决定 wire。选择它是为避免 SDK/JSON 解码先经二进制浮点损失精度。fixture 中 `3600.000000000` 与 `3600` 经济数值相等，后续指纹应按精确十进制规范化，不按输入文本区别幂等意图；不允许静默取整成价格档位。

订单沿用 order_qty/cum_filled_qty/leaves_qty/limit_price/gateway_status；CTP 不返回数值 order_id。成交 qty/price 单独保留、增加 offset_type/hedge_type；fee 未取得为 null 且 fee_complete=false。SDK、API、数据库均须保留未知，不把 null 转 0。逐笔手续费不从账户 Commission 分摊补齐。

## 4. 资金、持仓及原生身份

### 4.1 期货专用行

每行包含 account_id、captured_at；持仓另有 symbol/exchange。期货字段置于 `futures` 对象，完整原始 SDK 行另保存在审计层。以下是首批必须保留字段，不代表 SDK 所有字段或最终会计范围已验证。

| 对象 | 字段 | 类型/存储规则 |
| --- | --- | --- |
| futures.scope（资金） | broker_id/account_id/currency_id/biz_type/trading_day/settlement_id | 前五项字符串，settlement_id 整数；原生 AccountID 不等于逻辑 account_id；全部纳入采样作用域，TradingDay/SettlementID 不丢失 |
| futures（资金） | balance/available/curr_margin/frozen_margin/frozen_cash/frozen_commission/commission/position_profit/close_profit/deposit/withdraw | 普通有符号十进制字符串，SQL NUMERIC；不是最终净资产证明。金额精度不套价格的 9 位限制，具体无损编码/非有限哨兵规则待 OC SDK 对照，非法值不当零 |
| futures（资金） | scope_verified | false，直到原生范围经样本证明；缺 scope 不合并、不输出股票 cash_total/net_asset/market_value |
| futures（持仓维度） | posi_direction/hedge_flag/position_date/invest_unit_id | SDK 原生字符值以字符串保留；前三者必须有效，明确空 InvestUnitID 为 `""`，缺字段/null 为不完整 |
| futures（持仓数量） | position/today_position/yd_position/long_frozen/short_frozen | 整数，单位手，不能直接加前三项；未知不补零 |
| futures（持仓金额） | position_cost/open_cost/use_margin | 精确十进制字符串；不解释成股票 avg_cost 或证券市值 |

业务持仓键由账户路由、exchange、symbol、posi_direction、hedge_flag、position_date、invest_unit_id 组成；历史快照再加交易日、快照类型、请求代际。资金不跨 BizType/币种/AccountID 求和。只在单个完整作用域内应用快照，不让一行失败后的剩余行覆盖旧持仓。

### 4.2 native_ids 与确定性键

| 字段 | 候选类型 | 缺失规则 |
| --- | --- | --- |
| broker_id/investor_id/user_id | 字符串 | 原生身份来自可信绑定及请求上下文，不能由逻辑 ID 拆解；查询未返回 UserID 时可省略并保留来源说明 |
| order_ref/order_sys_id/trade_id | 字符串 | 前导零保留；源值确实为空则规范为空，尚未取得可省略；空值不得参与最终唯一键 |
| front_id/session_id/sdk_request_id | JSON 整数 | 未取得时省略或 null；不能伪造 0，也不是北向 request_id |
| counter_session_id | payload 独立字符串 | OC 进程生命周期；不能替代 FrontID/SessionID |

候选规范化规则：原生定长字符数组按 SDK 安全解码，保留原始值/必要字节证据；仅对 OrderRef/OrderSysID/TradeID 的两端 ASCII 空格 0x20 去填充。保留大小写和前导零；其余字段不擅自 trim。内部空白、控制字符、无终止且超过字段范围的输入进入冲突/不完整审计，不做截断修复。该去填充规则必须经 OC fixture 对照后冻结。

候选身份编码采用长度前缀 UTF-8 字节，避免分隔符歧义：`ctp.identity.v1/{kind}/` 后对每个分量写入 `十进制字节数:原字节`，长度无前导零、不附换行。kind 为 order 或 fill。分量固定顺序：env、route broker_id、logical account_id、gateway_id、TradingDay、ExchangeID、规范 OrderSysID（order）或 TradeID（fill）。

完整字节例子见 `TestIdentityRules`。建议外部订单 ID 为 `external-ctp-v1-` 加上述 order 编码的 SHA256 小写 hex；成交标准 fill_id 为 `ctp-fill-v1-` 加 fill 编码 SHA256。还须保存原生元组作碰撞/内容核验，哈希不是丢掉原始事实的理由。本地已知订单优先关联原 gateway ID，不能因为得到 SysID 就另造 external 单。

无 SysID 的外部单只在已证明的路由/TradingDay/FrontID/SessionID/OrderRef 下暂存，禁止用裸 OrderRef 发布最终外部订单。成交先到未关联时持久暂存，不发布虚构委托或以占位价满足外键；恢复关联后仍按原生 fill 键去重。fixture 的事件已具备 gateway 关联，未绑定 fill 暂存实现仍待 M3/F1。

同身份后续订单状态和累计量可正常变化；不可把所有 payload 差异当 ID 冲突。不可变合约/方向/原委托量冲突与有证据的更正事件分别处理；同原生成交不同经济内容必须留证并阻断静默覆盖。

## 5. 凭据字节契约与公开测试向量

沿用现有 `oc.secret.v1/A256GCM` envelope，不修改华鑫明文。CTP 候选明文固定字段见 `testdata/ctp/credentials.json` 中 plaintext_utf8：credential_type=ctp、credential_schema=ctp.login.v1、broker_id、investor_id、user_id、password、app_id、auth_code。

所有身份及密钥/版本按外部可信配置校验后再解密；内层 broker_id 为原生编号，外层 broker_id 为路由，不要求两字符串相同，而是与明确绑定逐项核对。UserID/InvestorID 不相互回填。明文必须是 UTF-8 JSON 对象，必填字符串非空，拒绝 NUL/重复键/未知类型版本；密码与授权值不得 trim。字段最大字节长度待 OC 对指定 6.7.13 头文件回签后填入，不能借旧头文件或本轮虚构字符串长度代替厂商边界。

Redis 键沿用现有算法：`relay:{env}:v1:{route_broker_id}:{logical_account_id}:secret:credentials:v{version}`；`:current` 为十进制版本字符串。版本写入并回读认证后才更新 current，停用/轮换审计沿用既有要求。示例 gateway=logical account，不由原生 InvestorID 推导键。

AAD 是以下字符串的 UTF-8 原字节，ASCII 分隔符 `|`，无 BOM、空白、末尾分隔符或换行：

```text
oc.secret.v1|{env}|{route_broker_id}|{logical_account_id}|{credential_version}|{key_id}
```

credential_version 用正整数十进制、无前导零。密钥 32 字节，nonce 12 字节，GCM tag 16 字节，nonce/ciphertext/tag 各自使用标准带 padding Base64；ciphertext 字段不包含 tag。解密时 ciphertext 与 tag 依次连接。issued_at 沿用现有格式校验，不在既有 AAD 内，不能仅依赖它作防回滚证据；版本/current 与可信身份绑定才是独立校验对象。

`credentials.json` 包含确切 key_hex、AAD 文本及 hex、明文原字节、nonce/ciphertext/tag、完整 envelope 和两个键名。**全为公开虚构数据；固定密钥和 nonce 只用于测试，严禁部署。** 正常运行必须使用受保护密钥及安全随机 nonce。

测试实际复用了 Relay 的 AAD、键名和 envelope 身份检查，并用 Go AES-GCM 验证解密、错环境/券商/账户/版本/key_id/AAD 换行、篡改密文/tag、错 nonce 和错 key。当前华鑫 Decrypt 不接受 CTP 明文，测试明确验证这一点；本轮没有交付 CTP 凭据管理器、内层字段校验或真实密钥配置。

## 6. M2 结果到 wire 的候选规则

- commander 后续统一走 enqueueQuery，不混用旧立即查询绕过节流；ticket 与北向命令及 SDK 请求分别关联。队列满时明确失败，不丢弃其他任务证据。
- 必须先 takeScheduledQueryResults，再核对本 ticket、作用域/连接代次/TradingDay、原始完成和类型化应用结果。仅旧缓存可读或旧快照成功不能证明本次成功。
- 只有 raw 完整、类型化应用成功、北向分页完整且唯一终态时，才允许 completed/末页和快照替换；完成原始结果但映射失败，返回 failed，旧快照及旧采集时间不变。
- 候选审计扩展为 adapter_context.query_evidence：ticket（字符串，仅本地标识）、sdk_request_id/sdk_return_code/callback_error_id（整数或 null）、connection_generation（整数）、counter_session_id、snapshot_applied（布尔）、可选 failure_stage。未发出任务的 SDK 三项为 null，没有虚构末页；具体 failure_stage 名称不作为新业务错误码。
- 保留现有 partial/completed/failed 语义；完成页候选 chunk.index 从 0 连续、is_last 唯一。失败使用既有适用错误码，示例 queue_expired 采用 QUERY_FAILED；技术失败细分需 OC 确认，不把内部 -10001 等直接用作 wire 枚举。
- 完整零持仓可以替换为空持仓；零资金行不能当成 0 元账户资产。重复消息重放按消息身份处理，不能把重复终态都应用一次；不同唯一终态矛盾必须核查。
- 合约 TTL 从 SDK 请求发出时计龄，不能取出结果后重置 300 秒。失败刷新、过期、代次变化立即失去静态准入资格；历史采样可供审计，不是仍可下单。128 槽释放属于 OC 缓存实现，本轮不复刻缓存或测 OC 容量。
- 静态通过仍需账户权限、动态价格约束、开平/可平量、快照、结算及本地权限；不得据其置 order_snapshot_ready。缺合约/行情不影响四类只读采集。
- 新单/撤单心跳独立字段不绕过 trading_enabled=false；只读样例两者均 false。订单 accepted 不带查询 chunk，也不意味着订单终态。
- ACK 时点需持久意图/结果/输出证据，不能因入队获得 ticket 即 ACK。SDK 调用前后未知窗口必须能恢复而不自动重发。具体崩溃/拒撤/批量失败状态机样例仍是下一批共同回归，不把本轮真值表等同于该实现通过。

## 7. 已交付文件与复现

| 文件 | 本轮覆盖 |
| --- | --- |
| `testdata/ctp/messages.json` | 10 个独立合成消息：单笔、两子单批量、撤单、accepted、订单、成交、资金页、持仓页、未提交排队到期、只读心跳 |
| `testdata/ctp/query_cases.json` | 10 项快照应用真值表，包括错误 ticket/旧连接、排队到期、SDK 超时、原始完成但应用失败、缺末页、重复终态、资源上限 |
| `testdata/ctp/credentials.json` | 可跨语言核验的 AES-GCM 确定性公开正例及精确输入 |
| `tests/ctpcontract/contract_test.go` | envelope 路由解码、消息内容断言、身份字节规则、凭据正反例及快照真值表测试；只在测试中运行 |

```bash
go test ./tests/ctpcontract -count=1 -v
go test ./internal/credentials ./internal/redisstream ./internal/orderflow -count=1
```

只有维护候选契约、逐字节审查差异时才运行 `go test ./tests/ctpcontract -update-ctp-vectors -count=1` 重生成公开向量；正常验收不加 update。消息不是一条完整成交生命周期的录制，更不是授权发送的请求。

Relay 本轮实际执行不带 update 的上述四包测试，全部通过：`tests/ctpcontract`、`internal/credentials`、`internal/redisstream`、`internal/orderflow`。新增测试还覆盖价格字符串/手数的词法负例；未运行完整项目测试、OC 测试或任何在线验收。

这些测试证明候选样例与现有 envelope/加密基础设施及本文规则自洽，**不证明生产解析器、数据库迁移、OC SDK 或真实交易可用**。未交付项：完整 JSON Schema/所有字段 SDK 长度、动态约束/行情确认、M3 崩溃恢复与拒撤 fixture、CTP 运行时代码和双方共同回归。F0 不能勾为全部完成。

## 8. 请 OC 回签的具体差异

1. 产品判别/版本和 funds.items/futures 嵌套是否可实现，BizType/SettlementID 与明确空 InvestUnitID 是否无损；补齐 SDK 长度和金额无损编码规则。
2. CTP 专用价格字符串与 limit_price/order_qty 映射是否接受；与现有华鑫命令 price/qty 区分，不能改华鑫共享路径。C/CT/CY 的原生 flag 对应及合约适用范围需明确回签。
3. native_ids 字段、三个编号去边缘 ASCII 空格、长度前缀身份编码/外部 ID、缺 SysID/缺自然日期及成交先到策略是否一致。
4. 使用公开向量独立解密并验证错误输入拒绝；确认内外 broker ID 绑定和字段长度，不读取或写入真实托管凭据。
5. 确认 query_evidence、类型化应用失败的北向失败、chunk.index 及独立撤单权限；之后补齐 ACK/重放/未知结果共同样例。

本轮无需用户提供真实账号或安排柜台窗口。先完成具体材料回签与离线验证，再推进 F1/F2；Meridian 覆盖核验可以并行，不把未核验资料写成已支持。
