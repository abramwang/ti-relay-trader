# Chronos TEST 撤单故障需求评审与阶段回执

日期：2026-10-06，时区 Asia/Shanghai。回执版本：1。
需求：/home/ti-chronos-strategy/docs/RELAY-TEST-CANCEL-FAULT-INJECTION-REQUIREMENT-20261005.md。
状态：Relay 第一阶段语义修正；真实故障控制面及远端矩阵未验收。

## 已完成的 Relay 修正

1. 撤单 payload.reconciliation_required 改为可区分缺失与显式 false。
   确定拒撤尊重 OC 显式 false；旧报文缺失该字段仍保守标记需对账。
2. action=order.cancel 的 COMMAND_OUTCOME_UNKNOWN DLQ 与 reply 都写独立撤单尝试，
   状态 outcome_unknown；CANCEL_RESPONSE_TIMEOUT 对应 timeout。
3. 上述不确定结果强制 reconciliation_required=true、retry_safe=false，
   即使回报缺字段或错误声称可重试，也不开放自动重撤。
4. 使用 origin_message_id 作为原撤单尝试关联键；原订单状态、下单拒绝字段和累计成交不修改。
5. 原始 raw Stream 报文和 payload 审计保留，安全投影不回写原始 retry_safe。

HTTP 202 仍仅代表命令发布，正常撤单仍须以订单 cancelled 为准。
没有增加故障 CLI/HTTP 入口，没有向 reply/event/DLQ 注入数据，没有发单、撤单或切换环境。
数据库 schema 仍为 31；公开 schema/capability 未增加。relay-sdk 当前为 0.1.41，
无需为本阶段修正发布新 SDK；需求中的 0.1.37 是消费端引用版本，不代表当前发布版本。

## 本地自动化验证

- 明确拒撤显式 false/true、旧报文缺失字段三种情况。
- unknown 的 reply/DLQ、timeout 的 reply/DLQ：错误或缺失安全标记均失败关闭。
- 顶层 unknown 与 payload accepted/OK 冲突时，不能让 payload 覆盖结果未知保护。
- unknown submit DLQ 不被误写为撤单尝试。
- 撤单结果不更新订单；原命令身份和 raw 审计保留。
- 既有 orderflow 撤单幂等重放测试覆盖订单后来终态也复用原命令、不重复发布，
  以及不同请求复用幂等键时冲突。以上是本地验证，不替代 OC 实际写入次数证据。

`go test ./...` 和 `go test -race ./internal/redisstream ./internal/orderflow ./internal/events` 通过。
本轮未运行真实柜台故障、计划 TTL/生产隔离或远端 SSE 去重用例，不能将本地测试视为这些项已验收。

## 尚未完成

| 工作 | 责任与边界 |
| --- | --- |
| TEST-only 精确一次故障计划、TTL、查询与清理 | OC 真实执行入口；Relay 不伪造远端结果 |
| reply/event 乱序不回退撤单投影 | Relay；当前 upsert 仍可能被迟到接受回报覆盖 |
| 同一撤单结果的 SSE 语义去重 | Relay；当前按消费变化发通知，不能宣称跨流 exactly-once |
| cancel_id/idempotency_key 与完整原命令的审计串联 | Relay/OC；当前 attempt_id=origin_message_id，需联合核对原命令证据 |
| unknown 两种最终分支的全分页恢复、人工处置 | 联合验收，不能仅凭等待时长推断 |
| 异步拒撤与挂起任务 durable 投影 | Chronos，按 attempt_id + gateway_order_id 关联 |

因此现在还不能通知 Chronos 开始两类真实故障矩阵；Direct/TWAP 正常写链的已有验收不受影响。
给 OC 的最小需求：/home/ti-relay-trader/docs/OC_TEST_CANCEL_FAULT_INJECTION_REQUIREMENT_20261006.md。
OC 实现后再补实际配置/执行/查询/清理步骤、版本、样例、数据库/SSE 证据和联合验收结论。

## 发布说明

本阶段只改 Relay 回报解析，不需要 OC 重启或 SDK 升级；Relay 新二进制需重启服务加载。
生产没有新增故障入口或开关。正常确定结果按同一既有协议解释，
生产以后加载新版本时也得到不确定撤单失败关闭保护，而非 TEST 专属交易逻辑。
不得为验收擅自重启 OC、重发旧命令或使用生产账户。

2026-10-06 17:58 Asia/Shanghai 已重启 TEST Relay 加载修正；/v1/status 为 test/ok，
数据库、Redis、worker 和事件桥等核心依赖均为 ok。未切换环境或重启 OC。
