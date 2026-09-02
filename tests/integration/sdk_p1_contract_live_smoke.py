#!/usr/bin/env python3
"""Validate SDK P1 capabilities and one real batch read without writes."""

from __future__ import annotations

import argparse
import json
import sys
from collections import Counter, defaultdict
from pathlib import Path
from urllib import error as urlerror
from urllib import request


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))

from relay_sdk import RelayClient, TRADING_SCHEMA_VERSION  # noqa: E402


REQUIRED_CAPABILITIES = (
    "ledger.cursor_pagination.v1",
    "events.cursor_resume.v1",
    "orders.batch_child_outcomes.v1",
    "orders.explicit_command_ids.v1",
)


def main() -> None:
    parser = argparse.ArgumentParser(description="Run Relay SDK P1 read-only live smoke")
    parser.add_argument("--base-url", default="http://relay-trader.quantstage.com")
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--date-from", required=True)
    parser.add_argument("--date-to", required=True)
    args = parser.parse_args()

    client = RelayClient(args.base_url, account_id=args.account_id, timeout=30.0, trust_env=False)
    catalog = client.require_capabilities(*REQUIRED_CAPABILITIES)
    require(catalog.version == TRADING_SCHEMA_VERSION, f"unexpected schema: {catalog.version!r}")
    require_legacy_route_removed(client)

    grouped = defaultdict(list)
    scanned = 0
    for order in client.iter_orders(
        account_id=args.account_id,
        date_from=args.date_from,
        date_to=args.date_to,
        history=True,
        page_size=500,
    ):
        scanned += 1
        if order.origin_message_id and "batch_index" in order.adapter_context:
            grouped[order.origin_message_id].append(order)

    require(grouped, "no historical batch orders found in requested range")
    message_id, source_orders = max(grouped.items(), key=lambda item: len(item[1]))
    outcomes = client.get_batch_order_outcomes(message_id, account_id=args.account_id)
    source_ids = {order.gateway_order_id for order in source_orders}
    outcome_ids = {child.gateway_order_id for child in outcomes.children}
    require(len(outcome_ids) == len(outcomes.children), "batch outcome contains duplicate gateway_order_id")
    require(source_ids == outcome_ids, "origin_message_id batch query did not return the complete source group")
    require(all(child.acceptance == "accepted" for child in outcomes.children), "unexpected live acceptance state")
    require(
        all(child.outcome in {"pending", "accepted", "rejected", "broker_not_ready", "outcome_unknown"} for child in outcomes.children),
        "unknown child outcome value",
    )

    print(
        json.dumps(
            {
                "ok": True,
                "base_url": args.base_url,
                "schema_version": catalog.version,
                "capabilities": list(catalog.capabilities),
                "history_orders_scanned": scanned,
                "batch_found": True,
                "batch_children": len(outcomes.children),
                "batch_complete": outcomes.complete,
                "outcome_counts": dict(sorted(Counter(child.outcome for child in outcomes.children).items())),
                "legacy_query_status_route": "removed",
                "write_requests_sent": 0,
            },
            ensure_ascii=False,
            indent=2,
            sort_keys=True,
        )
    )


def require_legacy_route_removed(client: RelayClient) -> None:
    req = request.Request(f"{client.base_url}/v1/query-status/unused", method="GET")
    try:
        response = client.opener.open(req, timeout=client.timeout)
    except urlerror.HTTPError as exc:
        require(exc.code == 404, f"legacy query-status route returned HTTP {exc.code}")
        return
    response.close()
    raise SystemExit("legacy query-status route is still registered")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


if __name__ == "__main__":
    main()
