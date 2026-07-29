# relay-sdk

Python SDK for the Relay Trader 9092 API.

## Install

Editable install from this repository:

```bash
python -m pip install -e sdk/python
```

Future internal package install:

```bash
python -m pip install "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.18.tar.gz"
```

## Quick Start

```python
from relay_sdk import RelayClient

client = RelayClient(
    base_url="http://relay-trader.quantstage.com",
    account_id="00030484",
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
| `submit_orders(...)` | Redis `cmd.trade` + draft order ledger | Batch order command. Each child order still needs its own durable idempotency identity. |
| `cancel_order(...)` | Redis `cmd.trade` | Cancel command. Final result still comes from order callbacks, `wait_order_terminal()`, or `list_orders()`. |
| `refresh_asset()` | Redis `cmd.query` | Ask OC to query broker asset. Ledger updates after OC reply is merged. |
| `refresh_positions()` | Redis `cmd.query` | Ask OC to query broker positions. A completed full page clears stale current positions not returned by the broker. |
| `refresh_orders()` | Redis `cmd.query` | Ask OC to query broker orders. Useful for external orders and final status reconciliation. |
| `refresh_fills()` | Redis `cmd.query` | Ask OC to query broker fills. Useful for end-of-day reconciliation. |
| `record_job_run(report, ...)` | PostgreSQL `job_runs` | Persist daily job report JSON and summary status. |
| `record_settlement_snapshot(...)` | PostgreSQL settlement tables | Persist open/close asset and position snapshots, and reconciliation run inputs. |
| `rebuild_economic_nav(trade_date=..., status=...)` | PostgreSQL `performance_nav_versions` + `performance_nav_reconciliations` | Persist the current economic NAV version; server-side performance write permission must be enabled. |

Example settlement write:

```python
client.refresh_asset()
client.refresh_positions()
client.refresh_orders()
client.refresh_fills()

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
    source="post_close_settlement",
    dry_run=False,
)
```

`record_settlement_snapshot()` does not query OC by itself. Run refresh commands
first and wait until the local ledger has merged fresh asset/position replies.

P8 helper methods are available for strategy and research tooling:

- `get_performance_daily(trade_date=...)`
- `get_performance_series(date_from=..., date_to=..., benchmark_security_id=...)`
- `get_performance_series_csv(date_from=..., date_to=..., benchmark_security_id=...)`
- `preview_economic_nav(trade_date=...)`
- `rebuild_economic_nav(trade_date=..., status="provisional")`
- `list_economic_nav(trade_date=...)`
- `list_nav_reconciliations(trade_date=...)`
- `confirm_nav_reconciliation(trade_date=..., operator=..., force=False)`
- `block_nav_reconciliation(trade_date=..., operator=...)`
- `list_reconciliation_breaks(...)`
- `get_meridian_bars(security_id=..., trade_date=...)`
- `get_meridian_adjust_factors(security_id=..., start_date=..., end_date=...)`

Meridian bars parameters follow Meridian's API. The relay SDK exposes common
`trade_date` minute-bar arguments and forwards extra query parameters when
needed.

## Callbacks

```python
def on_order(order, event):
    print(order.gateway_order_id, order.status, order.filled_qty)

def on_fill(fill, event):
    print(fill.gateway_order_id, fill.fill_id, fill.qty, fill.price)

order_sub = client.on_order_status(on_order, gateway_order_id=receipt.gateway_order_id)
fill_sub = client.on_fill(on_fill)

# Later, before shutdown:
order_sub.stop()
fill_sub.stop()
```

`on_order_status()` and `on_fill()` run in background daemon threads. For scripts
that prefer blocking control flow, use `watch_order_status()` or `watch_fills()`.
