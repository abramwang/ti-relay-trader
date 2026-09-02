# relay-sdk

Python SDK for the Relay Trader 9092 API.

## Install

Editable install from this repository:

```bash
python -m pip install -e sdk/python
```

Internal package install:

```bash
python -m pip install "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.32.tar.gz"
```

## Quick Start

```python
from relay_sdk import RelayClient

client = RelayClient(
    base_url="http://relay-trader.quantstage.com",
    account_id="00030484",
)

client.require_capabilities(
    "ledger.cursor_pagination.v1",
    "events.cursor_resume.v1",
    "orders.batch_child_outcomes.v1",
    "orders.explicit_command_ids.v1",
)

asset = client.get_asset()
status = client.status()
orders = client.list_orders(limit=20)
bars = client.get_meridian_bars(
    security_id="600000.SH",
    trade_date="20260612",
    frequency="1m",
    start_time="09:30:00",
    end_time="15:00:00",
)
contributions = client.get_performance_contributions(trade_date="20260724")
quality = client.get_trade_quality(date_from="20260722", date_to="20260724")
review = client.get_daily_review_report(trade_date="20260731")

receipt = client.submit_order(
    symbol="600000",
    exchange="SH",
    side="B",
    price=9.67,
    qty=100,
    client_order_id="strategy-a-0001",
    strategy_type="stock_cross_section",
    strategy_id="alpha-basket-v1",
    basket_id="basket-20260724-001",
)

print(receipt.gateway_order_id, receipt.status)
```

## Complete Ledger Pagination

The legacy `list_orders()`, `list_fills()`, and `get_positions()` methods keep
their single-page list behavior. Use the typed page methods when cursor and
request audit data are needed:

```python
page = client.list_orders_page(trade_date="20260902", limit=500)
print(page.count, page.next_cursor, page.request_id, page.time)

for order in client.iter_orders(
    history=True,
    date_from="20260801",
    date_to="20260902",
    page_size=500,
    max_pages=1000,
):
    process(order)
```

`iter_orders()`, `iter_fills()`, and `iter_positions()` stop only when Relay
returns an empty `next_cursor`. They raise `RelayPaginationError` on repeated
cursors, count mismatches, normalized-query drift, or page-limit exhaustion.
Set `max_items` only when intentionally requesting a bounded sample.

## Recoverable Event Stream

`stream_events()` exposes each SSE `event_id` and supports a single resumed
connection through `last_event_id`. Long-running consumers should use
`stream_events_resilient()` and provide a reconciliation callback:

```python
def reconciled(snapshot):
    replace_orders(snapshot.orders)
    replace_fills(snapshot.fills)
    replace_asset(snapshot.asset)
    replace_positions(snapshot.positions)

for event in client.stream_events_resilient(
    on_reconcile_required=reconciled,
    max_reconnects=5,
    idle_timeout=30,
):
    handle(event)
```

Relay replays events only within the current API process and its bounded replay
window. Restarts, expired cursors, event-bridge reconnects, or subscriber
overflow produce an explicit `relay.gap`; the SDK will not continue past a gap
without completing `reconcile_current_state()` and invoking the callback.

Refresh methods return a command receipt. Use its `message_id` to verify that
OC produced one completed final query reply:

```python
receipt = client.refresh_asset()
status = client.get_command_status(receipt.message_id)
print(status.state, status.success, status.expected_result_type)
```

`/v1/command-status/{message_id}` is the single status route for query and
trade commands. The older `/v1/query-status` name is not retained.

## Batch Child Outcomes

`submit_orders()` returns a `BatchCommandReceipt`. Every `children` item keeps
the caller's three IDs and reports Relay acceptance or idempotent replay. Use
the asynchronous resolver for broker-level child outcomes:

```python
receipt = client.submit_orders(
    [
        {
            "symbol": "600000",
            "exchange": "SH",
            "trade_side": "B",
            "business_type": "S",
            "price": 9.67,
            "qty": 100,
            "client_order_id": "chronos-client-1",
            "gateway_order_id": "chronos-gateway-1",
            "idempotency_key": "chronos-order-1",
        }
    ],
    idempotency_key="chronos-batch-1",
)
result = client.wait_batch_order_outcomes(receipt, timeout=30)
for child in result.children:
    print(child.gateway_order_id, child.acceptance, child.outcome, child.order_status)
```

`acceptance` is `accepted` or `replayed`. `outcome` is `pending`, `accepted`,
`rejected`, `broker_not_ready`, or `outcome_unknown`. A successful batch HTTP
response never fabricates broker acceptance for a child that is still only a
Relay `created` draft.

## Capabilities And Test Transport

`get_schema()` returns a typed `SchemaCatalog`; `require_capabilities()` fails
closed on a schema mismatch or missing capability. Deterministic tests may
inject any urllib-compatible public opener without overriding `_request`:

```python
client = RelayClient(base_url="http://relay.invalid", opener=my_test_opener)
```

Use `retry_decision(error, operation=...)` with `read`, `query`, `write`,
`cancel`, or `stream`. Only reads, interrupted queries, and broker-not-ready
queries are automatically retryable. Write/cancel transport timeouts and
`COMMAND_OUTCOME_UNKNOWN` require ledger reconciliation first; idempotency,
business, and cancel rejections are never automatically retried.

`Position` exposes `total_cost`, `avg_cost_source`, and `cost_complete`. A false
`cost_complete` with an explicit source must not be treated as a trusted
performance cost.

`submit_order()` and `cancel_order()` return command receipts. A successful
receipt means relay accepted and published the command; the final exchange state
still comes from `list_orders()`, `wait_order_terminal()`, callbacks, or
`stream_events()`.

If a submit request replays the same `gateway_order_id`, `idempotency_key`, and
payload, relay returns the existing order with `receipt.replayed == True` and
does not publish another Redis command. Conflicting idempotency keys raise
`RelayIdempotencyError`.

`submit_order()` accepts optional `trade_date`, `strategy_type`, `strategy_id`,
`basket_id`, `parent_order_id`, and `t0_order_group_id` fields. Relay stores
them with the draft order and forwards them to OC as attribution metadata for
later performance analysis; they do not change broker trading semantics.

Use `business_type="S"` for secondary-market stock and ETF orders. ETF
creation/redemption is not implemented by `/v1/orders` yet; do not use
`business_type="E"` for ordinary ETF buy/sell orders.

`record_job_run()` supports `status="running"`, `"succeeded"`, `"skipped"`,
and `"failed"`. The SDK accepts `status="completed"` as an alias for
`"succeeded"` and exposes `target_trade_date`, `timezone`, and `duration_ms`
as explicit keyword arguments.

## Write Methods

Methods that publish commands or persist relay ledger records:

| Method | Target | Notes |
| --- | --- | --- |
| `submit_order(...)` | Redis `cmd.trade` + draft order ledger | Single order command. Success means relay accepted the command, not final broker/exchange status. |
| `submit_orders(...)` | Redis `cmd.trade` + draft order ledger | Returns `BatchCommandReceipt` with per-child Relay acceptance/replay and caller IDs. |
| `get_batch_order_outcomes(...)` | PostgreSQL order/raw reply ledger | Reads each child outcome by batch `message_id`; never sends a trading command. |
| `wait_batch_order_outcomes(...)` | PostgreSQL order/raw reply ledger | Polls until every child is accepted, rejected, broker-not-ready, or outcome-unknown. |
| `cancel_order(...)` | Redis `cmd.trade` | Cancel command. Final result still comes from order callbacks, `wait_order_terminal()`, or `list_orders()`. |
| `refresh_asset()` | Redis `cmd.query` | Ask OC to query broker asset. Ledger updates after OC reply is merged. |
| `refresh_positions()` | Redis `cmd.query` | Ask OC to query broker positions. A completed full page clears stale current positions not returned by the broker. |
| `refresh_orders()` | Redis `cmd.query` | Ask OC to query broker orders. Useful for external orders and final status reconciliation. |
| `refresh_fills()` | Redis `cmd.query` | Ask OC to query broker fills. Useful for end-of-day reconciliation. |
| `refresh_fees()` | Redis `cmd.query` | Ask OC for order-level actual fees for its current broker trading day. Historical dates are not supported by OC. |
| `record_job_run(report, ...)` | PostgreSQL `job_runs` | Persist daily job report JSON and summary status. |
| `record_settlement_snapshot(...)` | PostgreSQL settlement tables | Persist open/close asset and position snapshots, and reconciliation run inputs. |
| `rebuild_economic_nav(trade_date=..., status=...)` | PostgreSQL `performance_nav_versions` + `performance_nav_reconciliations` | Persist the current economic NAV version; server-side performance write permission must be enabled. |

Daily-job review reads are available through `list_job_runs()` and
`get_daily_review_report()`. The latter returns account-level open/close
snapshots, authoritative settlement order/fill counts, open reconciliation
breaks, and a `passed`/`attention`/`blocked` conclusion.

Example settlement write:

```python
client.refresh_asset()
client.refresh_positions()
client.refresh_orders()
client.refresh_fills()
client.refresh_fees()

fees = client.list_order_fees(trade_date="20260801", fee_complete=True)

client.record_job_run(
    {"run_id": "post_close_settlement-20260625", "ok": True},
    job_name="post_close_settlement",
    trigger="cron",
    status="succeeded",
    target_trade_date="20260625",
    timezone="Asia/Shanghai",
)

client.record_settlement_snapshot(
    trade_date="20260625",
    account_ids=["501000114077"],
    run_id="post_close_settlement-20260625",
    snapshot_type="close",
    input_snapshot_type="broker_close",
    source="post_close_settlement",
    captured_at=None,
    snapshot_only=False,
    dry_run=False,
)
```

`get_trade_quality()` 返回服务端当前交易质量公式。`trade_quality.v3` 的 `summary` 分别提供 `rejected_orders`、`rejected_orders_with_reason` 和 `rejected_orders_missing_reason`；有完整柜台原因的业务拒单保留在拒单统计中，不进入 `anomalies`。

`record_settlement_snapshot()` does not query OC by itself. The production
post-close flow first refreshes OC and writes `snapshot_type="broker_close"`,
then promotes that immutable input with `input_snapshot_type="broker_close"`
to the official `close` snapshot after Meridian is available.
The optional `captured_at` is reserved for audited recovery and must be an
RFC3339 timestamp whose business date matches `trade_date`. Use
`snapshot_only=True` with it to persist source asset/positions without current
quote enrichment, order/fill reads, or reconciliation writes.

P8 helper methods are available for strategy and research tooling:

- `get_performance_daily(trade_date=...)`
- `get_performance_series(date_from=..., date_to=..., benchmark_security_id=...)`
- `get_performance_series_csv(date_from=..., date_to=..., benchmark_security_id=...)`
- `preview_cost_ledger(trade_date=...)`
- `rebuild_cost_ledger(trade_date=...)`
- `preview_economic_nav(trade_date=...)`
- `rebuild_economic_nav(trade_date=..., status="provisional")`
- `list_economic_nav(trade_date=...)`
- `list_nav_reconciliations(trade_date=...)`
- `confirm_nav_reconciliation(trade_date=..., operator=..., force=False)`
- `block_nav_reconciliation(trade_date=..., operator=...)`
- `list_reconciliation_breaks(...)`
- `get_meridian_bars(security_id=..., trade_date=...)`
- `get_meridian_instruments(security_ids=..., instrument_type=...)`
- `get_meridian_metadata_status()`
- `get_meridian_adjust_factors(security_id=..., start_date=..., end_date=...)`
- `get_meridian_etf_components(security_id=..., trade_date=...)`
- `get_meridian_etf_cash_components(security_id=..., trade_date=...)`
- `get_meridian_etf_pcf_status()`

Meridian bars parameters follow Meridian's API. The relay SDK exposes common
`trade_date` minute-bar arguments and forwards extra query parameters when
needed. Instruments and metadata status preserve Meridian
`metadata_instrument.v2` and `metadata_status.v2`, including authoritative
`price_tick`, `price_decimals`, source, and rule date. The current production
scope is SH/SZ; BJ is a future capability. ETF PCF methods preserve Meridian's `etf_component.v1`,
`etf_cash_component.v1`, and `etf_pcf_status.v1` payloads; in particular,
`unit_subscribe_redeem` remains the authoritative minimum creation/redemption
unit.

## Callbacks

```python
def on_order(order, event):
    print(order.gateway_order_id, order.status, order.filled_qty)

def on_fill(fill, event):
    print(fill.gateway_order_id, fill.fill_id, fill.qty, fill.price)

order_sub = client.on_order_status(on_order, gateway_order_id=receipt.gateway_order_id)
fill_sub = client.on_fill(on_fill)
cancel_sub = client.on_cancel_rejected(
    lambda event: print(event.data["cancel_attempt"]),
    gateway_order_id=receipt.gateway_order_id,
)

# Later, before shutdown:
order_sub.stop()
fill_sub.stop()
cancel_sub.stop()
```

`on_order_status()`, `on_fill()`, and `on_cancel_rejected()` run in background
daemon threads. For scripts that prefer blocking control flow, use the matching
`watch_*` methods. A cancel rejection reports only the failed cancel action; it
does not change the original order to rejected.
