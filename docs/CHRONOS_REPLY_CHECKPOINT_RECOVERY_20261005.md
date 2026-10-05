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

已实现可审计的跨会话只读恢复工具：先依据归档原始 reply 结构化旧错误，再以新 message_id
查询资金、持仓、订单、成交，验证四类唯一完整成功终态、全量分页账本一致性和无待对账事项。
恢复审计必须记录原始错误 ID、旧/新 session、四类查询 ID、完成时间和核对摘要。
完整在线恢复尚未验收，因此当前不能通知 Chronos 已满足端到端恢复要求，也不能启动交易验收。

## 显式只读恢复工具

工具只支持 TEST，默认不自动运行，不挂 cron，不由健康轮询触发；生产立即拒绝。

```bash
# 只结构化原错误：不发布任何柜台查询，可在休市执行。
go run ./cmd/relayctl checkpoint-reconcile -config .runtime/active-config.yaml -account 00030484 -normalize-only

# 在有效时段、新鲜唯一就绪会话下执行四类只读对账。
go run ./cmd/relayctl checkpoint-reconcile -config .runtime/active-config.yaml -account 00030484 -timeout 2m
```

任一步失败或超时保留 active_error，不删除历史报文、不移动消费游标、不修改订单终态。
工具顺序发起四类全新查询，验证每条命令只有一个完整成功终态，所有中间页动作/账户/类型匹配；
成功码接受空值或协议 OK，不接受失败后成功的混合回报作为本轮恢复证据。
重新读取归档的全部查询页，再以每页 500 条遍历当前正持仓、当日订单和成交，逐条检查：

- 资金比较 OC 现金及可用资金；Meridian 行情估值不属于柜台链路恢复前提。
- 持仓比较证券集合、数量、可用量和总成本。
- 订单比较证券、方向、业务类型、数量和状态，成交数量必须逐笔闭合。
- 历史任何未终态订单、未知结果命令、开放对账差异均保守阻断；本工具不把旧订单自动标成撤单。
- ETF 成分股划转存在时保留阻断，等待单独对账，不将其当作普通成交。
- 对账前后 reply/event 最新 ID 与消费位点必须一致；再次验证查询页数量和身份，防止迟到矛盾回报。
- 对账期间 heartbeat 不得出现其他 session；有界扫描溢出、跨日期、游标并发变化均不清告警。

成功时将原/新会话、四条命令及逐页回报、完成时间、账本页数/条数/SHA256 摘要、
核对结论写入 checkpoint.last_recovered_error，恢复来源为 cross_session_full_readonly_reconciliation。
LastError、原始 reply、累计错误计数及当前消费位点全部保留。

本轮已在 TEST 执行 normalize-only：原错误为 1791184129654-0、account.positions.query、BROKER_NOT_READY，
旧会话 hxproc-06f32d57d79c7b23；当前会话 hxproc-f628d7f70535b145。
17:49–18:00 Asia/Shanghai 为休市时段，未发布查询或交易，完整恢复留待有效窗口验证。
18:03 已发布当前 TEST API/内嵌 worker；完整恢复命令在休市正确返回 blocked，未发布查询，
reply 长度仍为 775、原消费位点和 active_error 保持不变。全量 Go 及 redisstream/ledger/relayctl race 测试通过。
