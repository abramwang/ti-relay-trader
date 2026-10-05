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
新失败覆盖 active 状态；已结构化 BROKER_NOT_READY 后正常消费批次可保留历史并解除活动状态。
结果未知错误不会通过成功的无关批次或心跳解除。

## 验收

回归覆盖缺失 readiness 各字段、pending 命令、历史错误保留、恢复后的健康聚合、结果未知继续阻断。
在线环境仍为 TEST，发布不切换环境、不产生交易命令。
15:30–15:40 为测试柜台休市窗口，readiness 返回 OUTSIDE_TEST_COUNTER_WINDOW；
此时 off_hours 健康不代表旧错误已被恢复，需进入下一交易窗口后核对恢复审计。

## 保守边界

账户存在历史结果未知命令或未完成对账时不会自动恢复，需先取得明确对账证据。
本轮不修改 OC 协议或 Python SDK，也不要求消费方解析 last_error 文本。
