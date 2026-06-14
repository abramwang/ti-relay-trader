# relay-sdk

Python SDK for the Relay Trader 9092 API.

## Install

Editable install from this repository:

```bash
python -m pip install -e sdk/python
```

Future internal package install:

```bash
python -m pip install "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.0.tar.gz"
```

## Quick Start

```python
from relay_sdk import RelayClient

client = RelayClient(
    base_url="http://relay-trader.quantstage.com",
    account_id="00030484",
)

asset = client.get_asset()
orders = client.list_orders(limit=20)

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
still comes from `list_orders()`, `wait_order_terminal()`, or `stream_events()`.
