# Chronos Relay Python SDK 验收回执

- 日期：`2026-09-02`
- Relay SDK：`relay-sdk==0.1.32`
- SHA256：`d9177418dda1ec2239903c9c7d9f511814b321c093fac70b380b8f95483e867e`
- API Schema：`relay.trading.v1alpha1`
- 结论：Chronos 提出的 Relay 侧 P0/P1 接口需求已实现，可进入 Chronos 消费端验收
- 安全状态：全部生产验证均为只读，写请求数为 `0`

## 结论边界

本回执表示 Relay 服务、Python SDK、发布包和生产只读链路已经满足
`RELAY-PYTHON-SDK-REQUIREMENTS.md`。Chronos 仍需在自己的受管 Python runtime 中完成
依赖安装、能力门禁和消费端状态机验收，之后才能把自身接入状态改为完成。

生产低频写场景，包括批量部分拒绝、交易命令结果未知和柜台未就绪，继续等待自然交易机会验证。
这些场景已有确定的 SDK 类型、状态和失败关闭逻辑，不为验收向生产账户制造订单。

## 验收矩阵

| Chronos 需求 | Relay 实现 | 验证结果 |
| --- | --- | --- |
| 当前/历史订单、成交、持仓类型化分页 | `OrderPage/FillPage/PositionPage`、`list_*_page()`、`iter_*()` | 通过 |
| 完整页结束语义与审计 envelope | `next_cursor`、`count/query/request_id/time/is_complete` | 通过 |
| 分页保护 | `max_pages/max_items`、重复 cursor、query drift、count mismatch | 通过 |
| 分页边界 | 0/1/500/501/1000+、历史路由、中途连接失败 | 通过 |
| 生产多页读取 | 9,830 笔订单、13,129 笔成交、206 条历史持仓 | 通过，0 写请求 |
| SSE 事件游标 | `RelayEvent.event_id`、`Last-Event-ID` | 通过 |
| SSE 有限恢复 | API 进程内 2,048 事件回放、有限退避、idle timeout | 通过 |
| SSE 缺口显式化 | `relay.gap`、`RelayStreamGapError`、强制四账本对账 | 通过 |
| SSE 故障用例 | 断开、半包、无心跳、重复、乱序、未知事件、重启、游标过期 | 通过 |
| 生产 SSE 恢复 | `fresh/resumed/server_restart`，当日 214 订单、517 成交、0 持仓全量对账 | 通过，0 写请求 |
| 显式交易 ID | 单笔、批量、撤单均保留调用方提供的 ID | 通过 |
| 批量子单结果 | `BatchCommandReceipt` 与异步 child outcome resolver | 通过 |
| 命令状态路由 | `/v1/command-status/{message_id}`、`get_command_status()` | 通过；旧路由已移除 |
| 稳定错误和重试矩阵 | `retry_decision()`，写入/撤单失败默认先对账 | 通过 |
| 公共故障注入点 | `RelayClient(..., opener=...)` | 通过 |
| Schema 能力发现 | `get_schema()`、`require_capabilities()` | 通过 |
| 价格精度 | JSON number 往返、`Decimal(str())`、整数微元 half-up、Meridian tick | 通过 |
| 发布一致性 | tar.gz、SHA256、版本、User-Agent、变更日志、历史版本索引 | 通过 |

## 关键调用口径

Chronos 启动时应先执行能力门禁：

```python
from relay_sdk import RelayClient

client = RelayClient(base_url=relay_url, account_id=account_id, opener=opener)
client.require_capabilities(
    "ledger.cursor_pagination.v1",
    "events.cursor_resume.v1",
    "orders.batch_child_outcomes.v1",
    "orders.explicit_command_ids.v1",
)
```

全量账本必须使用 `iter_orders()`、`iter_fills()` 和 `iter_positions()`；旧的
`list_orders()`、`list_fills()`、`get_positions()` 仍是单页接口。

长期事件消费使用 `stream_events_resilient(on_reconcile_required=...)`。发生重连或 gap 时，
必须先用 Relay 返回的完整资金、持仓、订单和成交快照替换本地状态，再继续处理增量事件。

批量交易的 HTTP 成功仅代表 Relay 接收并发布命令。Chronos 应使用
`wait_batch_order_outcomes()` 获取逐子单的 `accepted/rejected/broker_not_ready/outcome_unknown`，
最终业务状态仍以订单和成交账本为准。

## 价格契约

1. Relay 继续通过 JSON number 传输价格，最多保留 6 位小数。
2. Chronos 入站使用 `Decimal(str(value))` 和 `ROUND_HALF_UP` 转为整数微元。
3. 最小价位只读取 Meridian `metadata_instrument.v2.price_tick/price_decimals`。
4. 出站微元转回 float 后必须再次归一并与原整数完全一致。
5. 非有限值、非正值或不满足 Meridian 最小价位的价格失败关闭。
6. 当前生产范围为沪深股票、ETF 和可转债；北交所留作未来能力。

SDK 单测已覆盖普通股票、ETF、可转债和 `11.19999999` 传输噪声。

## 安装与交接

```bash
python -m pip install \
  "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.32.tar.gz"

curl -O http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.32.tar.gz.sha256
sha256sum -c relay-sdk-0.1.32.tar.gz.sha256
```

Chronos 下一步应把原 `coverage=single_page_unproven` 校验改为 `iter_*()` 全量覆盖，保存
`request_id/time/query/count/next_cursor` 审计字段，并用自身故障注入 opener 验证分页中断、SSE gap
和写入结果未知三类失败关闭路径。
