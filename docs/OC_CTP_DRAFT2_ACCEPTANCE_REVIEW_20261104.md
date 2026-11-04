# Relay 对 OC draft.2 共享样例回签的确认

日期：2026-11-04，Asia/Shanghai。来源：用户转交《OC 给 Relay 的 draft.2 共享样例复验回签》，OC 提交 `1aed721/f15a0f7`。本轮确认离线选定约束与材料身份，不冻结 F0、不开放运行入口。

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

## 5. 完整材料包

本轮从包含本文的固定提交打包 `.runtime/ctp-f0-review-<完整提交号>.tar.gz`，归档内部根目录为 `relay-ctp-f0/`。包含：

- go.mod/go.sum、被依赖的 internal 源码、tests/ctpcontract 和全部 CTP JSON；
- draft.1 原始字段附录、draft.2 修正说明、异常/恢复说明及本文；
- Meridian 对齐与 Relay 复核记录，供 OC 理解边界，不要求 OC 编写 Meridian 客户端。

包只含所选 Git 已提交源码/文档，不含 config、reference、生产数据库、日志或真实凭据。公开 AES-GCM 合成向量不是托管密钥。包名中的提交锁定实际源码，归档 SHA256 随本轮交付给出；不要把旧 `ctp-f0-draft2-source.tar.gz` 当成本轮完整包。

解压后从 `relay-ctp-f0` 目录运行：

```bash
go test ./tests/ctpcontract ./internal/redisstream ./internal/orderflow ./internal/credentials -count=1
```

需要 Go 1.24 及 go.sum 对应依赖缓存或获准下载依赖，未附 vendor，不承诺无缓存断网安装。跨仓库 Meridian 复核脚本不属于上述测试，不要求 OC 安装 Meridian 环境。包中附录仍是候选，不将“完整交付现有附录”混同“完整 schema 已开发完毕”。

本轮在仓库及独立临时目录解压后的四包测试均通过；使用本机既有 Go 工具链与依赖缓存，不声称在 OC 主机或完全断网的新机器验证通过。

## 6. 下一步分工

OC 继续四类 wire 转换、完整命令校验和真实持久恢复，向 Relay 补已有金额材料；不重复身份设计、不等待 SHFE 在线行情才写离线代码。Relay 负责完整字段矩阵、未关联成交、同消息重投与不同消息矛盾终态的后续共同样例和独立验证。

Meridian 类型校验/请求隔离整改及 SHFE 覆盖仍由 Relay 单独协调。未授权登录、部署、报撤单或切换环境；本轮不改变华鑫运行链路和生产下单权限。
