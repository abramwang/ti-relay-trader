#!/usr/bin/env python3
"""Prove complete read-only SDK pagination against a real Relay ledger."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Iterable


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))

from relay_sdk import Fill, Order, Position, RelayClient  # noqa: E402


def main() -> None:
    parser = argparse.ArgumentParser(description="Run Relay SDK multi-page read-only smoke")
    parser.add_argument("--base-url", default="http://relay-trader.quantstage.com")
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--date-from", required=True)
    parser.add_argument("--date-to", required=True)
    parser.add_argument("--page-size", type=int, default=500)
    parser.add_argument("--min-orders", type=int, default=501)
    parser.add_argument("--min-fills", type=int, default=501)
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    client = RelayClient(
        args.base_url,
        account_id=args.account_id,
        timeout=args.timeout,
        trust_env=False,
    )
    common = {
        "account_id": args.account_id,
        "history": True,
        "date_from": args.date_from,
        "date_to": args.date_to,
    }

    first_order_page = client.list_orders_page(**common, limit=args.page_size)
    first_fill_page = client.list_fills_page(**common, limit=args.page_size)
    first_position_page = client.get_positions_page(
        args.account_id,
        history=True,
        date_from=args.date_from,
        date_to=args.date_to,
        snapshot_type="close",
        enrich=False,
        limit=args.page_size,
    )
    for name, page in (
        ("orders", first_order_page),
        ("fills", first_fill_page),
        ("positions", first_position_page),
    ):
        require(page.request_id, f"{name} page is missing request_id")
        require(page.time.endswith("+08:00"), f"{name} page time is not Asia/Shanghai: {page.time!r}")
        require(page.count == len(page.items), f"{name} first-page count mismatch")
        require(page.query, f"{name} page is missing normalized query")

    orders = list(client.iter_orders(**common, page_size=args.page_size, max_pages=10_000))
    fills = list(client.iter_fills(**common, page_size=args.page_size, max_pages=10_000))
    positions = list(
        client.iter_positions(
            args.account_id,
            history=True,
            date_from=args.date_from,
            date_to=args.date_to,
            snapshot_type="close",
            enrich=False,
            page_size=args.page_size,
            max_pages=10_000,
        )
    )

    require(len(orders) >= args.min_orders, f"orders only returned {len(orders)}, expected >= {args.min_orders}")
    require(len(fills) >= args.min_fills, f"fills only returned {len(fills)}, expected >= {args.min_fills}")
    require(all(isinstance(item, Order) for item in orders), "orders contain an untyped item")
    require(all(isinstance(item, Fill) for item in fills), "fills contain an untyped item")
    require(all(isinstance(item, Position) for item in positions), "positions contain an untyped item")
    require_unique(orders, lambda item: (item.trade_date, item.gateway_order_id), "orders")
    require_unique(fills, lambda item: (item.trade_date, item.gateway_order_id, item.fill_id), "fills")
    require_unique(
        positions,
        lambda item: (item.trade_date, item.snapshot_type, item.exchange, item.symbol),
        "positions",
    )

    print(
        json.dumps(
            {
                "ok": True,
                "base_url": args.base_url,
                "account_id": args.account_id,
                "date_from": args.date_from,
                "date_to": args.date_to,
                "page_size": args.page_size,
                "orders": len(orders),
                "fills": len(fills),
                "positions": len(positions),
                "coverage": "complete_empty_cursor",
                "write_requests_sent": 0,
            },
            ensure_ascii=False,
            indent=2,
            sort_keys=True,
        )
    )


def require_unique(items: Iterable[Any], key, name: str) -> None:
    values = [key(item) for item in items]
    missing = [value for value in values if any(part in (None, "") for part in value)]
    require(not missing, f"{name} contain {len(missing)} rows with incomplete identity")
    require(len(values) == len(set(values)), f"{name} contain duplicate ledger identities")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


if __name__ == "__main__":
    main()
