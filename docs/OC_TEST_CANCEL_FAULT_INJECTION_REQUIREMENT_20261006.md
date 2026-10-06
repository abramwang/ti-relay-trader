# OC TEST 撤单受控故障入口需求

日期：2026-10-06，全部业务时间为 Asia/Shanghai。
状态：OC 已反馈实现提交 10c908c/ecb07b1，默认关闭；在线联合验收待授权窗口。
本文保留最初需求，实际对接方式见 OC_TEST_CANCEL_FAULT_INTEGRATION_20261006.md。
来源：/home/ti-chronos-strategy/docs/RELAY-TEST-CANCEL-FAULT-INJECTION-REQUIREMENT-20261005.md。

## 边界

目标是验证真实 Relay HTTP -> Redis cmd.trade -> OC -> reply/event/DLQ -> Relay 账本/SSE 链路。
Relay 不直接向 OC 输出流伪造回报，不用 HTTP 前置拒绝或客户端断网冒充远端故障。
仅增加独立 TEST 运维入口，不改变 relay.stream.v1、公开交易 API 或 Python SDK 业务协议。
不需要通用故障平台、自动探测订单或 TEST 四时段自动恢复机制。
本需求不授权发送测试订单、切换环境或重启线上 OC；联合验收前另行协调。

## 最小控制面

建议由 OC 提供本机 CLI 或等价的受限管理入口，支持 arm、inspect、list、disarm。
生产进程应不注册入口或强制拒绝；启动与执行时均校验 TEST 配置及明确配置的测试账户白名单。
不能仅凭调用方填写 env=test 或账户名称判断，不得允许生产账户进入白名单。

计划至少记录：

| 分类 | 字段 |
| --- | --- |
| 身份 | plan_id、mode、account_id、gateway_order_id、action=order.cancel |
| 会话 | 创建时 counter_session_id；仅本进程/会话生效 |
| 授权 | created_by、reason、created_at、expires_at |
| 状态 | armed/triggered/resolved/expired，解除/到期原因 |
| 触发 | cancel_id、idempotency_key、origin_message_id、request_id、触发时间 |
| 证据 | 实际路径、reply/event/DLQ message_id 与 Stream ID、结果摘要 |

- TTL 正数且不超过 300 秒；到期不能触发。触发后的审计不能因 TTL 到期丢失。
- 使用 OC 正常幂等去重之后的执行入口，原子消费一次计划；同一命令重放不再次消费或调用柜台。
- 错误账户、Gateway Order ID、action、session 不匹配时不触发，不能自动扩展匹配范围。
- armed 不改变心跳准入；不能因计划存在而关闭 accepting_trade_commands/accepting_cancel_commands。
- 只接受下面的固定模式和结构化参数，不提供任意 payload、SQL 或脚本执行。
- disarm 只解除未触发计划，并留下审计；不得抹除已触发计划、原命令或业务结果。
- OC 重启时旧 armed 计划不得迁移到新 session；保留历史审计并使其失效。
- plan resolved 表示故障执行已完成，不等于订单已撤成或未知结果已解除。

## 模式一：cancel_rejected

消费目标撤单后，通过正常业务结果发布路径产生确定拒撤；不得改原订单为 rejected。
同步 Relay HTTP 回执仍为已发布，OC 异步结果使用现有 order.cancel.event/order.cancel.rejected。
至少带标准 account_id、gateway_order_id、origin_message_id、非空 code/message、
order_state_changed=false、实际 retry_safe 和显式 reconciliation_required。
使用现有 BROKER_CANCEL_REJECTED 等协议错误码，不新增测试专属业务枚举。

在计划审计中明确这是受控 TEST 故障，不伪装成自然券商故障；原始业务回报仍原样归档。
同一撤单可有命令接受 reply 和动作拒绝 event，但不能重复生成同一身份的动作拒绝结果。
故障消费后，新人工授权撤单身份应可正常处理，用于清理遗留 working 订单。

## 模式二：cancel_outcome_unknown

固定参数 final_branch=cancel_applied 或 cancel_not_applied。
故障点必须进入真实 OC pending-cancel、响应超时或重启恢复状态机，
不能仅由 Relay 编造一条 unknown，也不能靠阻断 Chronos HTTP 达到目的。

- 使用现有 COMMAND_OUTCOME_UNKNOWN 或 CANCEL_RESPONSE_TIMEOUT；关联原撤单 origin_message_id。
- 业务结果显式 reconciliation_required=true、retry_safe=false；原订单状态不由该错误改变。
- cancel_applied 必须真的执行撤单，并由后续完整订单查询证明 cancelled。
- cancel_not_applied 必须有 OC 故障执行证据及完整查询证明仍未终态；保留人工处置状态。
- 故障后的普通完整查询应继续可用，不人为伪造成功查询末页或清除未知结果审计。
- 若必须重启，先说明操作步骤及 session 变化；不自动恢复 armed 计划或自动重撤。
- 超时 DLQ 应沿用现有 action=order.cancel 和顶层 origin_message_id，便于账本与命令关联。

由 OC 选择最小可行注入点，并在实现前说明两种最终分支如何进入真实状态机。
不能把“预设最终分支”当作已发生事实；验收必须取得柜台查询证据。

## 联合验收交付

OC 提供代码版本、脱敏 CLI 操作、计划查询/解除方法、真实输出样例和安全测试结果。
安全测试覆盖生产不可用、错误账户/订单/动作/session、TTL 到期、重复命令和进程重启。
Relay 负责订单/撤单独立落库、原始审计、command-status、全分页查询与 SSE；
Chronos 负责按 attempt_id + gateway_order_id 投影挂起任务和恢复后不自动重撤。
联合用例结束后，确认 armed=0；每笔未终态订单都须显式保留或按新人工撤单身份清理。

本轮不要求 OC 改市场时段、行情时钟、账户就绪契约或生产逻辑。
