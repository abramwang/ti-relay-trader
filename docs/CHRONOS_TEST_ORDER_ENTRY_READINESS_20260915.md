# Chronos 测试柜台下单准入接口说明

日期：`2026-09-15`

## 结论

Relay 已补齐账户级下单准入接口和 Python SDK 类型化检查，用于解决华鑫 7x24 测试柜台“OC 在线但当前时段禁止报单”的联调歧义。该时间表只配置在 Relay 测试环境，生产环境继续使用正常 A 股交易时段逻辑。

本次 `2026-09-15 13:14:44 Asia/Shanghai` 的两笔拒单发生在下午窗口 `13:15` 开始前约 16 秒，Relay 命令传输、同步回执、异步订单状态和拒单落库链路均正常。

## 服务端接口

```text
GET /v1/accounts/{account_id}/readiness?force=true
```

核心字段：

- `order_entry_ready`：Relay 已知准入条件是否全部满足。
- `broker_session_state`：`starting | ready | blocked | disconnected`。
- `order_entry_block_reason`：未就绪的稳定原因代码。
- `observed_at`：本次状态计算时间，按带时区 RFC3339 返回。
- `next_transition_at`：配置时段的下一次开闭切换时间。
- `counter_mode`：当前为 `huaxin_7x24_test`。
- `order_entry_readiness_source`：当前为 `oc_heartbeat+configured_test_schedule`。
- OC 原始准入字段仍保留：`redis_ready`、`broker_ready`、`order_snapshot_ready`、`accepting_trade_commands`、`last_heartbeat_at`。

机器可读能力：

```text
accounts.order_entry_readiness.v1
```

## 测试柜台时间表

以下时间均为 `Asia/Shanghai`，起点包含、终点不包含：

```text
01:15-01:25  01:30-03:30  03:40-05:40
07:15-07:25  07:30-09:30  09:40-11:40
13:15-13:25  13:30-15:30  15:40-17:40
19:15-19:25  19:30-21:30  21:40-23:40
```

中间 5 分钟、10 分钟休市段及每轮结算阶段返回：

```text
order_entry_ready=false
broker_session_state=blocked
order_entry_block_reason=OUTSIDE_TEST_COUNTER_WINDOW
```

测试时间表不依赖 Meridian 交易日信息，因此周末和节假日仍可按柜台窗口联调。生产配置若出现 `order_entry_schedule`，Relay 会拒绝启动。

## Python SDK 0.1.36

```python
from relay_sdk import RelayClient

client = RelayClient(
    base_url="http://relay-trader.quantstage.com",
    account_id="00030484",
)

client.require_capabilities("accounts.order_entry_readiness.v1")
readiness = client.verify_ready()
```

`get_account_readiness(force=False)` 返回 `AccountReadiness`。`verify_ready(force=True)` 强制跳过 Relay 的 5 秒状态缓存；未就绪时抛出 `RelayBrokerNotReadyError`，并且不会发送订单命令。

`order_entry_ready=true` 表示权限、OC 心跳、柜台登录、订单快照、OC 命令接收和已配置测试时段均通过，不代表具体订单一定被柜台业务规则接受。同步 `accepted` 仍只表示 Relay/OC 已接收命令，最终结果继续通过订单账本或 SSE 判断。

## 已完成验证

- Go 配置、时段边界、运维状态和账户 API 单元测试通过。
- Python SDK 52 项单元测试通过，发布包 SHA256 和包内容检查通过。
- 测试环境 `13:41 Asia/Shanghai` 实测返回 `order_entry_ready=true`、`broker_session_state=ready`、`next_transition_at=15:30`。
- 测试 OC 心跳字段全部 ready，4 条 Stream `lag=0`，DLQ pending=0。

下一轮订单闭环仍需用户明确授权，并使用新的业务 ID 在可报单窗口内执行。
