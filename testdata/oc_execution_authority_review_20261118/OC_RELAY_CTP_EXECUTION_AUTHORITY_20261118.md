# OC 给 Relay：交易授权职责收敛

日期：2026-11-18，Asia/Shanghai。业务实现 `7d20c4b`，准备工具适配 `59711d4`。已实现、仅离线验证，未部署；本轮未连接业务 Redis/柜台、登录、确认结算或报撤。

## 业务入口

交易业务授权唯一归属 Relay。OC 不再对命令施加本地测评时段、单笔/多笔 profile、固定合约/买入方向/一手或累计报撤额度。没有另建授权服务、模拟柜台或临时交易程序。

主程序、CLI 名称和 runtime_mode 保持原名 `test_broker_assessment`，物理 Stream、命令、订单/成交身份及回执终态不变。名称是兼容入口标识，不再代表测评授权模式。新的配置使用 `execution` 段，不新增运行 profile：

```json
{"execution":{"trade_date":"20261119","confirm_settlement_once":false,"defer_unregistered_replay":true},"process_duration_seconds":32400,"fill_identity_version":"v2"}
```

这只是配置差项；真实账户、路由、原 epoch/状态和凭据沿用已核实配置。trade_date 是预期原生 TradingDay 的身份绑定，不是自然日期授权；不能拿上面的示例日期直接启动。

删除原 `assessment` 段和 `broker.trading_enabled`。新配置混入旧字段会失败，旧配置的业务预检/启动明确返回 `legacy_assessment_authorization_removed_use_execution_config`，不会把旧有限授权静默变成无限授权。旧解析/窗口函数仅保留在注入式历史回归和离线历史工具中；公共业务启动函数也拒绝旧模式，不能通过 CLI 继续运行另一套授权。

模板 `config.execution.example.json`、操作方法 `TEST_RESIDENT_GUIDE.md`。默认预检不联网；显式启动会认证/登录并消费命令。`confirm_settlement_once` 默认 false；只有 Relay/用户明确决定确认时才执行一次，原 once 及真实成功状态继续防重。没有新增自动结算动作。

## 心跳最小差异

实际编码在同目录 `execution_authority_examples.json`，由新主循环离线输出提取，带 `synthetic_only=true`、`real_sdk_calls=false`。禁止投到实际账户路由。

| 位置 | 本版语义 |
| --- | --- |
| `adapter_context.admission_evidence.authorization_owner` | 新增固定值 `relay`，明确能力与授权分离 |
| `admission_evidence.process_lifecycle` | `{"mode":"resident_execution.v1","process_duration_seconds":32400}`；只是进程资源时限，无交易开始/结束/phase |
| `admission_evidence.assessment_id/expires_at/permission_profile/symbol/exchange` | 不再输出原全局测评授权字段；实际查询/命令的原生标的不变 |
| `accepting_query_commands` | 登录/来源/恢复/查询调度可用性，不受报撤授权或结算未确认连带关闭 |
| `accepting_trade_commands` | 连接、真实结算、编号、当前订单编号证据、可靠执行及调度能力；不依赖行情缓存是否过期，不表示 Relay 已授权 |
| `accepting_cancel_commands` | 独立撤单执行能力；不要求“当前已有可撤订单”才能报告能力；具体命令目标、最新状态和未决尝试仍逐笔校验 |
| `native_order_snapshot_ready/order_projection_ready` | 保留区别，不把原生完整改写成标准投影完整 |
| `submit_attempts/cancel_attempts` | 保留历史计数供审计，不再是 OC 的报撤额度 |
| `constraint_symbol/static_constraints/native_limits` | 仍是当前实际观测事实，可能尚无值或过期；不能据其推断本次权限或每个合约的行情 |
| `per_command_native_validation` | 仍为 true，双方替代价格准入未验证前暂不删除逐单检查 |

顶层和 payload 布尔镜像、`operation_reasons`、session、connection_generation、TradingDay、恢复及来源绑定继续保留。新增样例包含未确认结算但查询就绪，以及旧15:00截止之后仍具备执行能力的合成状态；后者不是实际柜台夜间可交易证明。

## 保留的执行边界

- 仅现有 TEST 官方柜台/SDK 接线；生产入口没有开放。当前业务能力为 SHFE 普通限价投机单，B/S、O/C/CT/CY，本地订单撤单；批量及其他未实现动作仍拒绝，不冒称取消授权就获得全部产品能力。
- 新单使用当前会话完整原生订单证据分配/核对 OrderRef。默认外部延期模式下不以外部标准投影失败阻断本地编号证据；不额外要求持仓投影完成作为 OC 自己的账户许可。Relay 仍负责资金/持仓及策略风险。
- 无命令时不自主刷新行情。独立事实动作保持可用；收到新单后暂保留必要原生合约/价格检查，来源仍为柜台，不标成 Meridian。旧 `counter_price_check_max_orders` 测评例外不再可配置。
- 首次接收仍拒绝 sent_at 超过 5 秒或来自未来的命令；约束准备最多 15 秒，SDK 调用前再次核验有效期。已发送/已写 dispatch 的不明确窗口继续保守阻断，不伪造成“肯定没发送”。
- 身份/来源/路由、幂等、SDK编码、流控、持久化/outbox/发布确认后 ACK 不变。明确失败继续服务；未知执行不自动重试，普通拒撤不改原订单终态。
- 现有 journal/查询/回调容量和进程上限属于资源保护，不是交易授权；达到容量不得删历史绕过。session 代次/交易日变化仍保守停止，自动重连后恢复和跨日 once 尚未完成普通生产验收。

## Relay 接线

请同步修改旧 assessment 窗口/profile/计数依赖，按 `authorization_owner=relay` 解释新能力；不能为兼容新版而忽略旧版 false。Relay 的 API/SDK/CLI 统一授权及命令写权限继续由 Relay 控制，OC 不建立新的签批令牌或授权通道。

联合部署前先让严格解析器接收这里的真实编码，再验证“Relay 禁止报单时零发布、可独立允许撤单”。OC 离线测试不能证明 Relay 零发布，此项仍由 Relay/联合验证完成。禁止未经授权人工 XADD 绕过其入口。

原路由、epoch、identity、journal、outbox、审计和 once 均保留。新版本配置不兼容旧授权配置，必须明确迁移并共同部署；不能以删除旧状态解决兼容问题。资金 BizType=00 原失败保持，不据本轮更新推导完整经济范围。

## 验证范围

Ubuntu 构建与新增六组注入式主循环用例通过：空闲无自主动作、未确认结算但可查询、午休及收盘后的多合约/多手卖出和平今/平昨、过期命令零报单、普通柜台拒绝及重复投递后继续。CLI 验证新配置默认预检不联网、拒绝旧业务配置及生产指向。样例均为测试替身，不是实际订单或在线柜台证据。

完整 53 项 CTest 首跑 51 项通过，耗时 596.68 秒。两项后续分别处理，不改写首跑日志：

- `ctp_asset_evidence_test`：低权限测试用户无法读取原受限归档；按原权限单独运行通过，没有放宽证据文件权限。
- `ctp_readonly_prepare_test`：发现准备工具仍要求旧 `trading_enabled`，已在 `59711d4` 适配 execution 配置并补旧配置禁止初始化回归。增量构建、准备工具与 CLI 两入口重跑通过。

因此 53 个入口均取得通过结果，但不是一次全绿执行。历史普通/异步拒绝、幂等、事实查询、恢复、schema、SDK 链接等入口沿用本次完整套件结果；没有重新运行 sanitizer、CentOS7 或真实柜台测试。

本机小型交付目录：`/home/Titian_Cpp/reference/execution_authority_review_20261118/`。包含本说明、配置模板、常驻指南、实际离线编码样例、构建/CTest 原日志和 `SHA256SUMS`；不含源码包、二进制或真实凭据，也未复制到 Relay 远端主机。

不要求再次交换旧材料，不将离线结果称为整日稳定、生产可用或新的交易授权。
