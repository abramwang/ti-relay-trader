# OC 给 Relay：逐 ticket 查询候选及材料版 2 复核交付

日期：2026-11-05，Asia/Shanghai。对应《Relay 对 OC r1 回签的确认与补充材料》。
**本轮继续实现，不重复请求总体原则回签；F0 未冻结，交易运行入口仍关闭。**

## 1. 本轮结论与提交

接受 Relay 对标准映射、入队前失败、旧完整输出恢复、原生持仓及金额候选的确认。
不直接对接 Meridian，不再交换源码包/二进制，不要求运行对方测试。

| 提交 | 实际交付 | 验证 |
| --- | --- | --- |
| `e00f80c` | 调度结果绑定逐 ticket 的原始应用决定及不可变快照，不再借用同类最新缓存 | Ubuntu 编译及 12 项 CTest |
| `ae47189` | 四类只读 payload 校验、资金/持仓完整标准候选、失败候选、身份/数值/容量门禁 | Ubuntu 编译及 13 项 CTest；两个入口非 PIE ASan/UBSan 含泄漏检查 |
| `2448837` | 接收四份纯协议材料；按 schema 收紧原生字符、采用 queue_expired；实际 C++ 金额与失败样例复核，独立 schema 测试 | Ubuntu 编译及 15 项 CTest；三个入口非 PIE ASan/UBSan 含泄漏检查 |

源码基线由 `984ad27` 推进至 `2448837`。所有样例是合成数据；未登录、执行真实查询、确认结算、
报撤单、连接业务 Redis、部署或切环境。华鑫链路未修改。

## 2. 已接收材料

来信原样存档为 [Relay 增量确认](RELAY_CTP_R1_CONFIRMATION_20261105.md)，SHA256：
`7ceeb114e9e76f6d08b1ed726e965fcb919e81a6449dc5428d33f00bf84fd9f6`。
用户已补齐四份文件到 `reference/testdata/ctp/`，本机计算值与来信完全相同：

| 文件 | SHA256 |
| --- | --- |
| query-page.schema.json | `988891918ed1abc6889832d1aa446a515d77e52ca8d5e9d359f566f63beee47c` |
| query_pages.json | `df78b499815383acdd784d58912f14a043ad3aa04416d6e250a2bebc4f025c30` |
| query_edge_cases.json | `b4d5406fd3bd68c557db3ae9f57dfaae90517f66802a3eec954261191eb353f3` |
| oc_money_review_vectors.json | `de274e9e0c62df63a24cd455386a538459eba7f6323a3ec6024420e7a013ec1d` |

四份 JSON 与用户上传的 README 原样加入 Git；原六份 JSON 未变。schema 材料身份为
`urn:relay:ctp:query-page:draft:2`，wire 版本不变。**“缺少新查询协议材料”的待办解除。**
Relay 转录金额文件保留自己的版本和指纹，不冒用 OC 原两份 JSON 的 SHA256。

## 3. 已实现的查询组件

### 逐 ticket 原始应用

`ScheduledQueryResult.raw_application` 带 `decision` 和 `shared_ptr<const QuerySnapshot>`。
驱动 `takeScheduledQueryResults()` 在应用每个结果时立即保存该结果的引用；成功 A、成功 B、
失败 C 同批提取，A 仍指向 A，B 仍指向 B，C 没有可冒用的成功快照。
`QuerySnapshotStore::applyWithEvidence()` 不更改旧的原始结果内容。

重连后已取出的历史引用仍可读，但不会恢复当前 ready；已完成却尚未应用的旧代次结果会被拒绝。
这些是内存证据，不是持久暂存或跨进程恢复。字段明确名为 raw_application，不冒充标准
query_evidence.snapshot_applied。

### 标准候选及请求校验

新增 `ctp_f0_query_projection.{h,cpp}`，纯 C++ 函数，无网络、文件、SDK 调用或 Redis 输出。
入口为 `checkReadOnlyQueryPayload`、`beforeEnqueueFailure`、`projectScheduledQuery`。

- 四类只读请求 payload 仅接受 account_id/product_type/schema_version；历史、日期、合约、游标及其他字段，包括额外 null，都拒绝。这里只校验 payload/action，不是完整命令信封/鉴权/调度器。
- 可信上下文含路由、逻辑账户、原生 owner、进程会话和当前代次/TradingDay。标准候选逐项核对 job、submission、原始 QueryResult 及该 ticket 的快照证据；不读取 querySnapshot(kind) 替代结果。
- 资金保留全部 AccountID/CurrencyID/BizType/TradingDay/SettlementID 作用域；11 项金额直接来自对应 SDK 字段，经既有金额编码器输出，scope_verified=false，不加总或补 NAV。
- 持仓保留原码及多空/套保/今昨/投资单元维度；支持 schema 明确的 3×6×2 种原生枚举组合，数量非负，不推断 Position 等于今昨相加，不强改为投机。
- 所有行成功转换之后才产生不可变 StandardQueryCandidate；只有此时 snapshot_applied=true。任一行不合法，失败候选没有 items/chunk，不发布部分标准事实，原始行及完成证据不改写。

资金完整空失败、持仓完整空成功保持不变。重复标准业务键采取保守拒绝策略，包含完全相同重复；
原始副本和重复数量保留，不静默去重/合并。资金不同作用域可分别输出；持仓若仅原始 SettlementID
不同、但在当前标准字段中变成同一业务键，则失败而非生成两条不可区分记录。以后若双方确认
可安全消除同值重复，再独立调整，不将本轮策略说成柜台永不重复。

投影容量是本地 8 MiB payload 编码限制，同时检查已有原始行数/容量上限；实际 JSON 超限拒绝
整个候选，不截断。它不是含信封/分页的最终消息字节上限，也不是双方已冻结容量。

### 按实际 schema 修正的差异

原生代码按 `[!-~]` 校验；InvestUnitID 仅允许明确空串或不含空格的原码。`" unit-1 "` 不 trim
成 `"unit-1"`，也不当正常行输出：原始证据仍保留带空格值，标准投影失败。
UTF-8/SDK 字节上限继续检查，80 字符合约不截断。排队过期使用 `failure_stage=queue_expired`，
保留真实 ticket/代次，三项 SDK 字段为 null。

## 4. 失败形状及仍未接线的部分

| 情形 | 候选行为 |
| --- | --- |
| 入队前未登录/队列满 | ticket=null、SDK 三项=null、snapshot_applied=false、before_enqueue；代次有真实值才填；未知 TradingDay 省略，不造 0/空串 |
| 已入队但未发送 | 保留字符串 ticket/正整数代次，SDK 三项仍 null；队列过期标记 queue_expired |
| SDK 同步失败/回调失败/未完成 | 保留真实请求号、原始同步返回和可空回调错误，failed/QUERY_FAILED，不借旧快照成功 |
| 原始完成但投影失败 | 保留 raw_completed=true 和原始 SDK 证据，failed/QUERY_FAILED、snapshot_applied=false，不改成柜台失败 |
| 证据绑定冲突 | 抛内部 ContractError，禁止据冲突对象构造正常回包；未来命令层须用原可信上下文形成可审计失败 |

`reason` 与 failure_stage 是内部/审计诊断，未新增业务错误枚举。ctx 由调用者从可信运行状态提供，
本轮类型检查不等于已完成 Redis 来源绑定或托管凭据接线。

**StandardQueryCandidate 不是完整 reply。** 尚无生产 envelope 编码、message_id 分配、分页、
原文保存、发布与 ACK。测试中补的 envelope/chunk 只用来检验候选字段与 schema 的兼容性，
不是已上线或拟直接发送的消息。
订单/成交的 payload 请求可以检查，但标准行转换尚未实现，调用对应投影只会得到明确失败，
即使原始集合为空也不冒充已实现的成功查询。

真实持久指纹/命令幂等、订单别名、未关联成交持久化、实时原始事件通道、原文 outbox 及 XACK
仍未实现。旧完整结果可历史重投但不恢复当前 ready 的边界接受；本轮只验证其样例结构，
没有重启恢复或原文落盘证明，不以 schema 测试代替 C6。

## 5. 实际复核证据

`ctp_f0_query_material_test` 对四份文件逐字节校验指纹，并执行：

- 用实际 beforeEnqueueFailure 生成两份候选，与 Relay before_login/queue_full 的 payload、query_evidence、action/result/status/code 完全比较；不比较合成 message 文本或伪称已经生成完整信封。
- 将 Relay 转录的 28 个位模式按 required/optional 送入实际 C++ moneyField，再设置三种 C 舍入模式检查拒绝，共 59 项通过；调用线程舍入状态保持不变。

`ctp_query_schema_test.py` 是 OC 自己编写的可选离线测试，使用实际 schema 和标准
JSON Schema 2020-12 验证器，不执行 Relay 源码：

- 7 份 Relay body（两份正常页、三份失败、两份历史保存页）结构与日期格式通过。
- 10 份由实际 C++ 转换器生成的候选，套测试专用信封后通过；覆盖资金、多作用域、持仓、空持仓和各类失败。
- 9 个结构反例被拒绝，包括空资金成功、无效日期、金额超长、投资单元空格、合约超长、未知套保，以及入队前伪造代次/SDK 返回/空成功 items。

schema 结构验证不等于完整执行语义验证：严格原始整数词法、业务数量关系、来源真实性、
跨页冲突、持久恢复仍需相应实现和测试，不拿 JSON Schema 的 integer 定义代替所有 JSON token 检查。

本机 Ubuntu 全量编译与 **15 项 CTest** 通过（基础 14 项，另显式开启可选 schema 入口）。
三个入口 `ctp_f0_query_projection_test`、`ctp_f0_query_material_test`、`ctp_session_test` 在
非 PIE ASan/UBSan、detect_leaks=1、halt_on_error=1 下通过。
开发过程中额外检查发现并修复了拒绝非法持仓时 JSON 临时构造的泄漏路径；保留异常测试，
不是关闭泄漏检查规避。未据此宣称旧 PIE 问题、CentOS7 或真实恢复已验收。

| 原始输出文件 | SHA256 |
| --- | --- |
| query_review/evidence/oc_ctest_20261105.txt | `1b70ca99a998f5b934fbb22b34f2fa0216b9c733353869b7fc5e969c5631f45d` |
| query_review/evidence/asan_ubsan_20261105.txt | `fe6471456f94188ae7f7d3f3c336577206c9c6e1878878827917ca0db18ca602` |

本轮 Python 为 3.8.10，jsonschema 4.19.2 及其依赖只安装到本地 build/schema_deps，未改系统包
或生产依赖；已保存测试依赖版本清单。首次安装需网络或缓存，不承诺无缓存离线安装。
OC 自己复现可用以下命令，Relay 无须执行：

```bash
python3 -m pip install --target oceanus/src/oc_trader_commander_ctp/build/schema_deps -r oceanus/src/oc_trader_commander_ctp/tests/requirements-query-schema.txt
cmake -S oceanus/src/oc_trader_commander_ctp -B oceanus/src/oc_trader_commander_ctp/build -DCTP_SCHEMA_DEPENDENCY_DIR="$PWD/oceanus/src/oc_trader_commander_ctp/build/schema_deps"
cmake --build oceanus/src/oc_trader_commander_ctp/build -- -j4
cd oceanus/src/oc_trader_commander_ctp/build
ctest --output-on-failure
```

只需基础离线构建时，将 CTP_SCHEMA_DEPENDENCY_DIR 置空；这只是关闭可选 schema 测试，
不会开启运行时，也不影响生产依赖。

## 6. 下一步

本轮不再重复讨论已经对齐的标准映射责任、入队前 null、金额候选和旧完整输出历史恢复边界。
接下来优先补完整命令校验/独立意图编码向量及有限分页输出，再推进原始订单/成交事件、
持久身份/未关联事实、outbox/ACK 和真实进程故障测试。未完成项保留，达到相应门禁后再请求
在线验收窗口；本次材料认可不授权登录、交易、部署或环境切换。
