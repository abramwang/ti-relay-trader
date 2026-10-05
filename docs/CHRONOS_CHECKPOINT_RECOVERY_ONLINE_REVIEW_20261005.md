# Chronos Reply checkpoint 恢复在线复核结果

时间：2026-10-05，Asia/Shanghai。环境：TEST，账户：00030484。

## 结论

19:30:34 完成跨会话四类只读全量对账并恢复 checkpoint；API、reply/event/hb/dlq 均正常，
active_error=null、lag=0、pending DLQ=0。原 last_error、raw reply、消费位点和累计错误计数保留。
可以通知 Chronos 继续只读消费端复验和账户基线重建；本轮未执行 Direct 或其他交易验收。

## 原错误与会话

| 项目 | 证据 |
| --- | --- |
| 原始错误 ID | 1791184129654-0 |
| 原动作 / 错误码 | account.positions.query / BROKER_NOT_READY |
| 原错误时间 | 2026-10-05 15:08:49.654 +08:00 |
| 原 OC 会话 | hxproc-06f32d57d79c7b23 |
| 当前 OC 会话 | hxproc-fe8bf34c4f301af7 |
| 最终消费位点 | 1791199834349-0 |
| 恢复依据 | cross_session_full_readonly_reconciliation |
| 恢复完成时间 | 2026-10-05 19:30:34.657447278 +08:00 |

## 四类新查询

每条命令都只有一个 completed/is_last=true 成功终态，全部中间页类型、动作和账户匹配。

| 动作 | message_id | reply 页数 | 完成时间 +08:00 |
| --- | --- | --- | --- |
| account.asset.query | msg-asset-query-1791199833700803209-1 | 1 | 19:30:33.713029 |
| account.positions.query | msg-positions-query-1791199833917598688-3 | 40 | 19:30:34.051022 |
| order.list.query | msg-orders-query-1791199834130859315-5 | 1 | 19:30:34.139426 |
| fill.list.query | msg-fills-query-1791199834342710136-7 | 1 | 19:30:34.349820 |

资金现金/可用资金一致，39 条正持仓的证券集合、数量、可卖量及总成本一致；当前业务日订单与成交均为 0。
持仓由 39 条数据页和 1 条空 final 页完整收口。账本按 500 条分页遍历，不截取前 500 条。
全部源 reply 页与账本页 SHA256、条数、终态时间和四条命令 ID 已持久化到 last_recovered_error。
对账前后 reply/event 位点稳定，heartbeat 连续为当前会话；历史结果未知命令和开放对账差异均为 0。

## TEST 历史遗留订单处置

首次复核发现 32 笔历史 working：9 月 6 日 1 笔、9 月 15 日 20 笔、9 月 16 日 11 笔。
全部累计成交量为 0，leaves_qty=order_qty；其中 21 笔缺原会话 ID，11 笔来自 hxproc-ae77457b695c2caa。
当前查询为空不构成旧订单已经撤单的证明，不能凭日期或 ORDER_NOT_FOUND 自动终态化。

用户明确确认这 32 笔订单不可能在当前 TEST 柜台执行，并授权独立遗留处置。现已：

- TEST 库应用 migration 31，写入 test_legacy_order_resolutions 32 条记录。
- 每条记录保留原订单快照、执行人 user、确认时间、原因、原命令及拒撤证据引用。
- 所有原订单继续 working，is_terminal=false；订单、成交、撤单尝试和 raw 不修改、不删除。
- 处置只适用于已确认的当前 counter_session_id 和完全不变的订单快照。
- 会话变化或订单 ID、状态、数量、成交、原命令等快照字段变化，旧处置不会继续解除风险门禁。
- 重复执行同一处置不新增记录；现存记录仍为 32 条，原 working 事实保留 32/32。
- 不回填缺失的原会话 ID，不将人工处置标为券商终态，不修改生产逻辑或生产数据库。

一次性 SQL：docs/sql/TEST_LEGACY_ORDER_RESOLUTION_20261005.sql。
该脚本严格限定 relay_trader_test、账户、三个历史日期和 32 笔零成交 working，不进入每日任务。
本次结果不能外推为生产 ORDER_NOT_FOUND 自动免责，也不宣称通用遗留订单处置 API 已完成。

## 本轮修正

1. 历史 working 不再阻止取得新的只读证据，但未处置的历史订单仍阻止最终清除告警。
2. reply 已归档而 worker checkpoint 尚未提交，或心跳 pending 尚未刷新时，在超时预算内等待交接；
   最终仍要求 pending/lag=0，session 变化、休市、DLQ 和真实就绪失败不作为瞬时交接忽略。
3. 阻断尝试以 last_reconciliation_attempt 保存原因、查询证据和遗留订单清单；恢复成功后最新尝试同步为 recovered。
4. 已恢复工具调用幂等返回 already_recovered 和原恢复审计，不发布新查询。

last_reconciliation_attempt 表示最近一次尝试，不是全部尝试历史；原始查询命令/reply 及旧错误长期保留。
全量 Go 与 redisstream/ledger/relayctl race 测试通过，当前 TEST API/内嵌 worker 已更新。

## Chronos 复验入口

GET /v1/status：environment=test、status=ok。
GET /v1/operations/status?force=true：reply.active_error=null；last_recovered_error 中包含原/新会话、
四条 queries、reconciliation.page_audit 和 test_legacy_order_resolutions。
检查原订单时仍会看到 working，这是保留的柜台历史事实，不是新会话活动委托。
重建基线后，最小 Direct 写验收由 Chronos 按既定测试授权安排；Relay 本轮没有发出任何交易指令。
