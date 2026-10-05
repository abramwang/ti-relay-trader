# Reply checkpoint 瞬时错误恢复

更新时间：2026-10-05，Asia/Shanghai。

## 实现

`operations/status` 的每条 Stream 增加 `active_error` 与 `last_recovered_error`。
历史 `last_error` 保留作审计；已恢复记录不再参与当前健康聚合。
字段持久化到 checkpoint metadata，无需数据库迁移。

自动恢复仅处理原始 query reply 的 `BROKER_NOT_READY`，要求：

- 同账户、券商、gateway，最新新鲜 heartbeat 与错误前 heartbeat 为同一 counter session。
- 当前账户 order entry ready，Redis、柜台、订单快照及命令接收均明确为 true。
- 命令 pending 为零，所有受监控 Stream 无 lag，无 pending DLQ，监控查询无错误。
- 错误后到当前的 heartbeat 检查窗口中无其他 session，超过检查上限时保留阻断。
- 无归档 `COMMAND_OUTCOME_UNKNOWN` 或 open reconciliation break。
- 原始 reply 完整解释 checkpoint 当前错误；聚合错误或原始证据缺失不自动恢复。

恢复使用 checkpoint cursor + 原错误的条件更新，保留消费位点、原始错误和累计计数。
审计保存错误码、Stream ID、账户、gateway、session、观察时间、恢复心跳时间和依据。
新失败覆盖 active 状态；正常消费批次仅推进游标，不解除活动错误。
错误定位独立保存为 metadata.last_error_stream_id，优先使用 active_error.stream_id；
旧记录仅从 last_error 提取合法 Stream ID 作为定位符，错误语义仍必须由原始 reply 证明。
结果未知错误不会通过成功的无关批次或心跳解除。

## 验收

回归覆盖缺失 readiness 各字段、pending 命令、历史错误保留、恢复后的健康聚合、结果未知继续阻断。
在线环境仍为 TEST，发布不切换环境、不产生交易命令。
15:30–15:40 为测试柜台休市窗口，readiness 返回 OUTSIDE_TEST_COUNTER_WINDOW；
此时 off_hours 健康不代表旧错误已被恢复，需进入下一交易窗口后核对恢复审计。

## 保守边界

账户存在历史结果未知命令或未完成对账时不会自动恢复，需先取得明确对账证据。
本轮不修改 OC 协议或 Python SDK，也不要求消费方解析 last_error 文本。

## Chronos 消费方反馈与剩余任务

Chronos 的 RELAY-REPLY-CHECKPOINT-RECOVERY-ACCEPTANCE-20261005.md 确认字段已部署，
但端到端恢复尚未通过：原错误 1791184129654-0 与当前 OC 属于不同 session。
本轮修复消费游标推进造成的原错误定位丢失，并删除无关成功批次清除错误的路径。
跨会话错误继续保留，不把 off_hours 或新心跳当作对账成功。

下一步实现可审计的跨会话只读恢复：先依据原始 reply 结构化旧错误，再以新 message_id
查询资金、持仓、订单、成交，验证四类唯一完整成功终态、全量分页账本一致性和无待对账事项。
恢复审计必须记录原始错误 ID、旧/新 session、四类查询 ID、完成时间和核对摘要。
该流程尚未实现或在线验收，因此当前不能通知 Chronos 已满足端到端恢复要求，也不能启动交易验收。
