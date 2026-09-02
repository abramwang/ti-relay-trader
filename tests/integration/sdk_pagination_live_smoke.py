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

    order_pages = list(client.iter_order_pages(**common, page_size=args.page_size, max_pages=10_000))
    fill_pages = list(client.iter_fill_pages(**common, page_size=args.page_size, max_pages=10_000))
    position_pages = list(
        client.iter_position_pages(
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
    for name, pages in (
        ("orders", order_pages),
        ("fills", fill_pages),
        ("positions", position_pages),
    ):
        require(pages, f"{name} did not return a terminal page")
        require(pages[-1].next_cursor == "", f"{name} did not reach an empty cursor")
        require(pages[-1].is_complete, f"{name} terminal page is not complete")
        for page in pages:
            require(page.request_id, f"{name} page is missing request_id")
            require(page.time.endswith("+08:00"), f"{name} page time is not Asia/Shanghai: {page.time!r}")
            require(page.count == len(page.items), f"{name} page count mismatch")
            require(page.query, f"{name} page is missing normalized query")

    orders = [item for page in order_pages for item in page.items]
    fills = [item for page in fill_pages for item in page.items]
    positions = [item for page in position_pages for item in page.items]

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
                "order_pages": len(order_pages),
                "fill_pages": len(fill_pages),
                "position_pages": len(position_pages),
                "coverage": "complete_empty_cursor",
                "page_audit_coverage": "all_pages",
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
