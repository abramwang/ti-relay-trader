# 华鑫 TEST 受控撤单故障对接说明

日期：2026-10-06。业务时间使用 Asia/Shanghai。

功能已实现，默认关闭；尚未进行真实柜台联合验收。实现提交为 `10c908c`、`ecb07b1`，
后续 CLI、测试和文档提交可通过 `git log` 查看。本文操作仅在另行协调的 TEST 验收窗口执行，
不授权下单、切换环境或重启当前在线 OC。

## 功能与边界

固定只允许托管 TEST 进程、账户 `00030484`、TEST Redis 编译配置和 A/B TEST 直连柜台。
配置白名单必须恰好为 `00030484`，解密后的实际柜台登录用户也必须为 `00030484`。
如果将故障功能配置为启用而运行条件不符，OC 启动失败；正常生产配置不创建管理 socket。

管理入口为本机 Unix socket，仅允许与 OC 相同 UID 的进程连接。部署为 root 时管理操作也使用 root。
socket 权限为 `0600`，审计目录为 `0700`。CLI 不连接 Redis，也不向输出 Stream 写消息。

公共交易 API、SDK、Stream、命令身份和订单主键不变。armed 计划不参与心跳准入判定。
一个订单同时最多有一个 armed/triggered 计划。计划只匹配精确的账户、单笔 gateway_order_id、
`order.cancel` 和当前 counter_session_id，正常幂等去重在消费计划之前执行。

## 三条执行路径

| 模式 | 实际执行 | 异步业务结果 | 查询要求 |
| --- | --- | --- | --- |
| cancel_rejected | 不调用柜台，经正常撤单回执/事件发布方法返回确定拒撤 | accepted/order_action_receipt，随后 order.cancel.event/order.cancel.rejected，code=BROKER_CANCEL_REJECTED | 原订单不被错误事件改成 rejected；后续使用新的人工撤单身份清理 |
| cancel_outcome_unknown，cancel_applied | 真实调用 orderDelete；真实动作回调写入计划审计而暂不完成 pending；订单终态回报仍正常发布 | 进入现有 pending-cancel 容器，5 秒后通过原超时循环产生 CANCEL_RESPONSE_TIMEOUT DLQ | 完整订单查询必须确认 cancelled；若柜台拒撤或订单成交，不能把分支预设视为已通过 |
| cancel_outcome_unknown，cancel_not_applied | 先消费计划、提交执行审计；在调用柜台前截断，建立同一 pending-cancel 容器中的受控 pending | 同样由 5 秒超时产生 CANCEL_RESPONSE_TIMEOUT DLQ | 需要完整查询证明订单仍未终态，保留人工处置状态 |

两条 unknown 路径不需要重启。`cancel_not_applied` 的 adapter_request_id 是本进程内部负数标记，
不是券商请求编号；`cancel_applied` 使用真实券商请求编号。真实柜台动作回调即使迟到，仍只写计划审计，
不再生成第二份动作结果。真实订单/成交回报和普通查询保持正常处理。

两条 unknown 路径均显式携带 `reconciliation_required=true`、`retry_safe=false`、
`order_state_changed=false`。注入错误本身不改变订单状态；真实撤单成功时，订单状态由正常 order.event 更新。
未知结果走 DLQ，不新增 unknown 事件枚举或查询式 final chunk。

accepted 回执仍只表示命令被 OC 接收；Relay HTTP 的发布回执按原有规则处理。
确定拒撤的 `retry_safe=true` 表示未调用柜台，可按新的人工撤单身份再处理；
相同 message_id/idempotency_key 的重放只重放已有回执，不能重复触发拒撤事件或调用柜台。

如果真实 orderDelete 在创建 pending 前返回失败，按原有 CANCEL_SUBMIT_FAILED/ORDER_NOT_FOUND 处理，
计划审计标记该失败；不得把这个用例当成 unknown 分支验收通过。

## 联合验收窗口启用

在 TEST 发布模板 `config.test.ini` 中配置：

```ini
[test_cancel_fault]
enabled = true
account_whitelist = 00030484
```

只有协调好升级/重启窗口后，才部署这一 TEST 配置。配置在进程启动时读取，不支持热启用。
发布模板默认仍为 false，升级时须按验收窗口显式选择是否启用。

部署后的 CLI 为 `/opt/oc-huaxin-test/cancel_fault.py`，使用 Python 3 标准库，无额外 pip 依赖。
默认 socket 为：

```text
/opt/oc-huaxin-test/runtime/00030484/test-cancel-fault/control.sock
```

先查看当前会话和历史计划：

```bash
python3 /opt/oc-huaxin-test/cancel_fault.py list
```

从 `counter_session_id` 复制当前会话值。gateway_order_id 必须来自该账户已有的、可撤的真实订单，
不要使用篮子 ID，也不要由 CLI 自动找订单。

确定拒撤示例：

```bash
python3 /opt/oc-huaxin-test/cancel_fault.py arm \
  --plan-id cancel-reject-case-1 \
  --mode cancel_rejected \
  --account-id 00030484 \
  --gateway-order-id '<目标单笔gateway_order_id>' \
  --session '<当前hxproc会话>' \
  --created-by '<授权操作人员>' \
  --reason 'Relay联合验收确定拒撤' \
  --ttl-seconds 120
```

未知结果示例：

```bash
python3 /opt/oc-huaxin-test/cancel_fault.py arm \
  --plan-id cancel-unknown-case-1 \
  --mode cancel_outcome_unknown \
  --final-branch cancel_applied \
  --account-id 00030484 \
  --gateway-order-id '<目标单笔gateway_order_id>' \
  --session '<当前hxproc会话>' \
  --created-by '<授权操作人员>' \
  --reason 'Relay联合验收未知结果' \
  --ttl-seconds 120
```

未执行柜台撤单的分支将 `--final-branch` 改为 `cancel_not_applied`，并使用新的 plan_id。
arm 成功之后，由 Relay/Chronos 按普通 HTTP 撤单入口发送授权撤单；CLI 不生成业务命令。

查看与解除：

```bash
python3 /opt/oc-huaxin-test/cancel_fault.py inspect --plan-id cancel-reject-case-1
python3 /opt/oc-huaxin-test/cancel_fault.py disarm --plan-id cancel-reject-case-1
python3 /opt/oc-huaxin-test/cancel_fault.py list
```

disarm 只接受尚未触发的 armed 计划，状态变为 expired，原因保留为 operator_disarmed。
不能删除 triggered/resolved 历史、业务回报或原始命令。CLI 的 `--socket` 仅用于另一路径的本机开发实例；
服务端仍执行同样的环境、会话和 peer UID 校验。

## 审计与状态

每个计划保存为：

```text
/opt/oc-huaxin-test/runtime/00030484/test-cancel-fault/<plan_id>.json
```

字段包括创建身份/原因、peer_uid、account_id、gateway_order_id、action、mode、会话、TTL、触发信息、
执行路径、柜台调用返回值、原始撤单动作回调字段、实际输出 body、message_id 和 Redis XADD 返回的 Stream ID。
受控拒撤 event 和超时 DLQ 使用按 session/plan/output 类型的 Redis Lua 原子发布去重，
响应丢失后的重试读取第一次发布的原始 body 和 Stream ID。内部去重 key 位于 TEST cmd.trade 名称下的
`:test-cancel-fault:` 后缀空间，TTL 沿用配置 idempotency_ttl_seconds；不新增公开 Stream。
审计时间为 Unix 毫秒；显示/对齐时转换到 Asia/Shanghai，不能按无时区字符串猜测。
若旧协议未单独提供 cancel_id，审计使用 idempotency_key 作为撤单身份，并同时保留原命令 body。

状态说明：

| 状态 | 含义 |
| --- | --- |
| armed | 尚未消费，只有 expires_at 之前可以触发 |
| triggered | 已原子消费并提交审计；TTL 不会删除或结束该执行 |
| resolved | 拒撤结果或未知结果执行已完成，或者旧进程被中断；不表示业务订单已撤成 |
| expired | 尚未触发即到期、人工解除，或原进程会话结束 |

进程重启后旧 armed 计划变为 expired，原因 process_session_ended。
旧 triggered 计划保留为 resolved，原因 process_interrupted_reconciliation_required；
不自动重新注入、不自动重撤。恢复后的命令仍按原有持久幂等/未知结果恢复规则处理。
历史审计不会按 TTL 删除；最多保留 1024 个可加载计划，达到上限拒绝新 arm，维护时可归档历史文件。

## 输出字段核对

以下为字段结构示意，未伪装成已发生的柜台回报。真实输出应从计划 evidence.body 和对应 Redis raw 消息取得。

确定拒撤 event 的关键字段：

```json
{
  "event_type": "order.cancel.event",
  "event_name": "order.cancel.rejected",
  "action": "order.cancel",
  "origin_message_id": "<原撤单message_id>",
  "payload": {
    "account_id": "00030484",
    "gateway_order_id": "<目标单笔委托>",
    "cancel_status": "rejected",
    "code": "BROKER_CANCEL_REJECTED",
    "message": "controlled TEST cancel rejection (no broker call)",
    "order_state_changed": false,
    "retry_safe": true,
    "reconciliation_required": false
  },
  "adapter_context": {
    "controlled_test_fault": true,
    "fault_plan_id": "<plan_id>"
  }
}
```

未知结果 DLQ 的关键字段：

```json
{
  "message_type": "deadletter",
  "action": "order.cancel",
  "origin_message_id": "<原撤单message_id>",
  "request_id": "<原request_id>",
  "code": "CANCEL_RESPONSE_TIMEOUT",
  "payload": {
    "account_id": "00030484",
    "gateway_order_id": "<目标单笔委托>",
    "cancel_status": "unknown",
    "reconciliation_required": true,
    "retry_safe": false,
    "order_state_changed": false,
    "controlled_test_fault": true,
    "fault_plan_id": "<plan_id>",
    "original_body": "<原命令JSON对象>"
  }
}
```

正式消息仍包含原有完整 envelope、订单身份和时间字段。`original_body` 实际为 JSON 对象，示意中的文字不是协议数据。

## 验收责任与当前验证结果

OC 离线测试覆盖：生产/生产账户拒绝、错误前置、错误 UID、错误账户/订单/动作/session、TTL 边界、
重复触发、重复柜台执行拒绝、两种分支的柜台调用次数、触发后 TTL 保留审计、旧 armed/triggered 重启失效。
同时验证本机 socket/CLI 和 RESP 模拟服务下的发布响应丢失重试及原始 Stream ID/body 恢复；
RESP 模拟测试不等同于真实 Redis Lua/柜台在线验收。
全项目编译及单元测试通过。真实 Relay HTTP -> Stream -> 柜台 -> 账本/SSE 的联合验收尚待窗口授权。

Relay 验收时按 message_id/idempotency_key/gateway_order_id 精确过滤，独立保存撤单动作、订单及 raw 审计。
unknown 后发送普通完整订单查询，所有分页均收齐且取得唯一 completed，才能使用柜台状态判定最终分支。
Chronos 按 attempt_id + gateway_order_id 挂起/恢复任务，恢复后不自动重撤。

结束时检查 list 中 armed=0。未终态订单须明确保留，或用新的人工授权撤单身份清理。
不能通过删除 DLQ、计划审计、构造查询末页或合成订单终态来结束验收。
