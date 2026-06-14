# relay-sdk

Python SDK for the Relay Trader 9092 API.

## Install

Editable install from this repository:

```bash
python -m pip install -e sdk/python
```

Future internal package install:

```bash
python -m pip install "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.7.tar.gz"
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

receipt = client.submit_order(
    symbol="600000",
    exchange="SH",
    side="B",
    price=9.67,
    qty=100,
    client_order_id="strategy-a-0001",
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

Use `business_type="S"` for secondary-market stock and ETF orders. ETF
creation/redemption is not implemented by `/v1/orders` yet; do not use
`business_type="E"` for ordinary ETF buy/sell orders.

`record_job_run()` supports `status="running"`, `"succeeded"`, `"skipped"`,
and `"failed"`. The SDK accepts `status="completed"` as an alias for
`"succeeded"` and exposes `target_trade_date`, `timezone`, and `duration_ms`
as explicit keyword arguments.

P8 helper methods are available for strategy and research tooling:

- `get_performance_daily(trade_date=...)`
- `get_performance_series(date_from=..., date_to=...)`
- `get_performance_series_csv(date_from=..., date_to=...)`
- `list_reconciliation_breaks(...)`
- `get_meridian_bars(security_id=..., trade_date=...)`

Meridian bars use `trade_date`, not `start_date` / `end_date`.

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
