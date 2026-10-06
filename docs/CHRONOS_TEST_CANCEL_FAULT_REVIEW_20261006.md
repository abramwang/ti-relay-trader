# Chronos TEST 撤单故障需求评审与阶段回执

日期：2026-10-06，时区 Asia/Shanghai。回执版本：2。
需求：/home/ti-chronos-strategy/docs/RELAY-TEST-CANCEL-FAULT-INJECTION-REQUIREMENT-20261005.md。
状态：OC 已交付默认关闭的 TEST 控制面；Relay 本地准备完成，远端矩阵待窗口授权。

## 已完成的 Relay 修正

1. 撤单 payload.reconciliation_required 改为可区分缺失与显式 false。
   确定拒撤尊重 OC 显式 false；旧报文缺失该字段仍保守标记需对账。
2. action=order.cancel 的 COMMAND_OUTCOME_UNKNOWN DLQ 与 reply 都写独立撤单尝试，
   状态 outcome_unknown；CANCEL_RESPONSE_TIMEOUT 对应 timeout。
3. 上述不确定结果强制 reconciliation_required=true、retry_safe=false，
   即使回报缺字段或错误声称可重试，也不开放自动重撤。
4. 使用 origin_message_id 作为原撤单尝试关联键；原订单状态、下单拒绝字段和累计成交不修改。
5. 原始 raw Stream 报文和 payload 审计保留，安全投影不回写原始 retry_safe。
6. 数据库 upsert 阻止迟到 accepted 覆盖拒撤及未知结果，阻止低优先级 reply 覆盖柜台 event；
   unknown/timeout 不由后来的确定动作回报自动清除。原订单后续查询仍正常更新，人工/消费端对账另行留证。
7. SQL 原子返回是否实际变更；相同结果重消费仍归档 raw，但不再次触发账本变更通知。
8. 修正查询扫描层会将明确拒撤的 reconciliation_required=false 重新变成 true 的遗漏；
   timeout/unknown 的旧数据读取也保持不可自动重试。
9. raw_payload 改为保存完整 JSON，保留 OC controlled_test_fault、fault_plan_id 和 original_body 对象。
10. SSE 重放窗口按 account_id + attempt_id + gateway_order_id + status 去重，不再为同一结果推进第二个游标。
    不同人工撤单身份或新结果阶段不受影响。

HTTP 202 仍仅代表命令发布，正常撤单仍须以订单 cancelled 为准。
没有增加故障 CLI/HTTP 入口，没有向 reply/event/DLQ 注入数据，没有发单、撤单或切换环境。
数据库 schema 仍为 31，HTTP 命令、Redis wire 和 capability key 不变。
GET /v1/command-status/{origin_message_id} 新增可选 cancel_attempts 审计关联，
原 state/success/terminal 仍表示命令接受，不与异步撤单结果混为一谈。
accepted 回执和 timeout 撤单尝试可以同时存在，后者必须按 reconciliation_required/retry_safe 对账。
relay-sdk 当前为 0.1.41，无需升级，可使用 status.raw.get("cancel_attempts", [])
读取关联项并用 OrderCancelAttempt.from_dict 解析，也可沿用独立撤单尝试全分页接口。

## 本地自动化验证

- 明确拒撤显式 false/true、旧报文缺失字段三种情况。
- unknown 的 reply/DLQ、timeout 的 reply/DLQ：错误或缺失安全标记均失败关闭。
- 顶层 unknown 与 payload accepted/OK 冲突时，不能让 payload 覆盖结果未知保护。
- unknown submit DLQ 不被误写为撤单尝试。
- 撤单结果不更新订单；原命令身份和 raw 审计保留。
- 既有 orderflow 撤单幂等重放测试覆盖订单后来终态也复用原命令、不重复发布，
  以及不同请求复用幂等键时冲突。以上是本地验证，不替代 OC 实际写入次数证据。

`go test ./...` 和 `go test -race ./internal/ledger ./internal/redisstream ./internal/orderflow ./internal/events` 通过。
TEST PostgreSQL 临时表、独立序列及回滚事务验收覆盖 accepted/rejected/timeout、乱序、重复、
同一身份错误订单匹配、显式 false 读取、完整 fault 审计及 command-status 关联；没有修改真实业务行。
Python SDK 仓库单测 56/56 通过，包括 accepted 回执与 unknown 审计并存的兼容解析。
本轮未运行真实柜台故障、计划 TTL/生产隔离或远端 SSE 去重用例，不能将本地测试视为这些项已验收。

## 尚未完成

| 工作 | 责任与边界 |
| --- | --- |
| TEST-only 精确一次故障计划、TTL、查询与清理 | OC 已交付，默认关闭；线上部署、启用及隔离证据待验收 |
| reply/event 乱序不回退撤单投影 | Relay 已通过 SQL/临时库测试，仍需真实链路复核 |
| 同一撤单结果的 SSE 语义去重 | Relay 已通过本地重放测试；不宣称分布式 exactly-once，真实链路待复核 |
| cancel_id/idempotency_key 与完整原命令的审计串联 | Relay 保留 OC original_body 和原命令 raw，线上按 origin_message_id 联合核对 |
| unknown 两种最终分支的全分页恢复、人工处置 | 联合验收，不能仅凭等待时长推断 |
| 异步拒撤与挂起任务 durable 投影 | Chronos，按 attempt_id + gateway_order_id 关联 |

现在可以协调 Chronos/OC 的 TEST 验收窗口，但不能把 OC 默认关闭的功能视为已在线启用。
Direct/TWAP 正常写链的已有验收不受影响。
给 OC 的最小需求：/home/ti-relay-trader/docs/OC_TEST_CANCEL_FAULT_INJECTION_REQUIREMENT_20261006.md。
OC 原说明归档：docs/OC_TEST_CANCEL_FAULT_INTEGRATION_20261006.md，声明实现提交 10c908c/ecb07b1；
在线部署版本仍需操作人员确认。操作步骤见 docs/TEST_CANCEL_FAULT_ACCEPTANCE_RUNBOOK_20261006.md。
真实验收后再补 HTTP、数据库/SSE、计划证据和最终结论。

SSE 去重边界：在当前 Hub 的有界重放窗口内复用原游标；不提供跨进程的持久 exactly-once 投递。
服务重启、游标过期或 gap 后必须使用原身份全分页对账，不能把未收到通知解释为未发生。
数据库与通知目前没有事务性 outbox，崩溃窗口可能缺通知；原始审计与账本查询仍是恢复依据。

## 发布说明

本阶段只改 Relay 回报解析，不需要 OC 重启或 SDK 升级；Relay 新二进制需重启服务加载。
生产没有新增故障入口或开关。正常确定结果按同一既有协议解释，
生产以后加载新版本时也得到不确定撤单失败关闭保护，而非 TEST 专属交易逻辑。
不得为验收擅自重启 OC、重发旧命令或使用生产账户。

2026-10-06 17:58 Asia/Shanghai 已重启 TEST Relay 加载修正；/v1/status 为 test/ok，
数据库、Redis、worker 和事件桥等核心依赖均为 ok。未切换环境或重启 OC。

2026-10-06 18:55 Asia/Shanghai 已发布 v2 对应 Relay 修正，TEST API/核心依赖均为 ok。
只读复查现存撤单命令 msg-order-cancel-1791212328767749753-39：
command-status 为 accepted/success，关联原身份的单条 accepted 尝试正确返回，未发送新命令。
本轮没有启用 OC 故障功能、arm 计划、发单、撤单、切环境或重启 OC。
