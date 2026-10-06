# TEST 撤单故障联合验收步骤

日期：2026-10-06，时区 Asia/Shanghai。状态：待授权窗口执行，不是实测报告。
OC 协议 relay.stream.v1，实现提交声明 10c908c/ecb07b1。
实际 CLI/配置以 docs/OC_TEST_CANCEL_FAULT_INTEGRATION_20261006.md 为准。

## 启动前

1. 用户确认 TEST 窗口、允许的目标订单、清理授权；不能自动切环境或向生产账户造单。
2. 操作人员在 OC 所在机器确认部署版本，并按窗口配置 test_cancel_fault.enabled=true。
   仅在另行授权的重启窗口加载配置，Relay 不远程猜测配置或重启 OC。
3. 同 UID 在 OC 主机运行 `python3 /opt/oc-huaxin-test/cancel_fault.py list`，保存 session 和原始输出。
   管理 Unix socket 不在 Relay 主机；CLI 不直接写 Redis。
4. Relay /v1/status 为 test，账户恰为 00030484，当前 OC 心跳新鲜、可撤准入有效。
   CLI session、订单 session、心跳 session 必须一致；若不一致停止。
5. 由 Chronos 准备本会话真实 working/partially_filled 目标单及固定撤单身份。
   没有合适目标单时等待用户另行授权下单，不自动复用历史 working 或选择整个篮子。
6. 保存原订单字段、累计成交、下单拒绝信息、SSE 游标、Redis 最新 ID 和 pending/lag。
   新增 SSE 订阅先建立，再 arm，避免靠事后通知数推断事件覆盖。

## 三个用例

在 OC 主机按原对接说明 arm，使用唯一 plan_id、精确单笔 Gateway Order ID、当前 session、
真实 created-by/reason、TTL 120 秒；Chronos 在 TTL 内通过普通 Relay cancel HTTP 发命令。
不要由 CLI 生成业务命令。每次只做一个用例，完整留证后再进入下一个。

| 用例 | 模式/参数 | 必需证据 |
| --- | --- | --- |
| A | cancel_rejected | HTTP 202；OC 接受回执；单一 rejected 尝试/通知；原订单未被改 rejected，数量/下单拒绝字段未变；reconciliation_required=false、retry_safe=true；柜台调用次数 0 |
| B | cancel_outcome_unknown / cancel_applied | 正常接收；5 秒超时循环的 CANCEL_RESPONSE_TIMEOUT DLQ；原身份 timeout 尝试；reconciliation_required=true、retry_safe=false；完整订单查询最终 cancelled；实际柜台调用证据 |
| C | cancel_outcome_unknown / cancel_not_applied | 同一超时链及不可重试标记；完整查询仍未终态；计划审计表明未调用柜台；保持人工处置，不自动重撤 |

B 若订单成交、柜台拒撤或 orderDelete 同步失败，按真实结果保存，不能宣称 cancel_applied 验收通过。
C 即使预设“未执行”，也要取得完整查询；不能只凭超过 5 秒或计划 resolved 推断业务结果。

每个用例先冻结并保存 cancel_id、idempotency_key、HTTP 回执 message_id，
原命令 body、计划 trigger 和后续原始输出须逐项一致。
使用完全相同的撤单请求做幂等重放；只能复用原回执和命令身份，不新增 OC 写入或尝试。
重复结果投递与 SSE 断线恢复用原游标检查，不用新的撤单业务 ID 模拟重复。

## Relay 查询与对账

- GET /v1/command-status/{原撤单message_id}：查看命令接收与 cancel_attempts 两个维度。
  state=accepted/success=true 不等于已撤成，也不能忽略其中 timeout。
- SDK 0.1.41 用 `status.raw.get("cancel_attempts", [])` 查看关联审计；
  `OrderCancelAttempt.from_dict(item)` 解析，或用既有撤单尝试分页迭代器。
- GET /v1/order-cancel-attempts?account_id=00030484&gateway_order_id=目标ID：遍历所有 next_cursor，
  按 attempt_id/origin_message_id 精确核对原撤单，不能仅查看第一页。
- POST /v1/accounts/00030484/orders/refresh 返回新的只读查询 message_id；
  等其 command-status 为唯一合法 completed，收齐查询 raw 页，并遍历该业务日完整订单分页。
  只读取本地旧订单或只查询目标 ID 不等同于完成柜台全量对账。
- 若涉及成交数量变化，同时刷新成交并取得唯一成功末页、完整分页；
  若 session/gap/readiness 变化，按正式恢复流程重建基线，不能自动清告警或重撤。
- unknown 尝试保留为原失败审计；后续 cancelled 是订单事实，不能把原 timeout 改写成成功。

原始 OC 输出、故障计划 JSON、Relay raw 消息、撤单尝试、SSE 游标和全页摘要均需归档。
计划 original_body 是 JSON 对象；Unix 毫秒按 Asia/Shanghai 转换，不按文本猜时区。
受控故障标记仅用于审计，不绕过任何交易或对账准入。

## 收口与退出

用户授权后才可用新的人工撤单身份清理 A/C 未终态单；原失败身份不得重用为新动作。
确认 OC list 的 armed=0；未触发计划按原 CLI disarm，triggered/resolved 审计不能删除。
确认目标订单均有真实终态或显式人工保留，原命令不重复发布，pending/lag 和 DLQ 均可解释。
未知结果相关 DLQ 保留，仅在完整对账和授权后按正式审核流程处置；不得直接删除来消除告警。
窗口结束是否恢复 enabled=false 或重启 OC，由操作人员另行协调，不自动执行。

## 验收范围

OC 已报告离线环境/账户/UID/session/TTL 等安全测试；Relay 不将其等同于线上证据。
生产没有故障入口；错误账号、订单、action、session 和过期计划仍需在受控窗口核对。
Relay 本地已验证乱序 SQL、重复消费、同一结果有界 SSE 重放及旧 SDK 审计兼容；
跨进程通知并非持久 exactly-once，重启/游标过期依旧必须全量对账。
Chronos 还需证明异步 rejected 与挂起任务 durable 投影、unknown 不重撤和 recovery-only 不恢复算法。
