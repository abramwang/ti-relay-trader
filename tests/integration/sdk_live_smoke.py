#!/usr/bin/env python3
"""Read-only live smoke test for relay-sdk against a running 9092 service."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))

from relay_sdk import RelayClient  # noqa: E402


def main() -> None:
    parser = argparse.ArgumentParser(description="Run a read-only relay-sdk smoke test")
    parser.add_argument("--base-url", default="http://relay-trader.quantstage.com", help="relay 9092 base URL")
    parser.add_argument("--account-id", default="", help="account id to test; defaults to first account from /v1/accounts")
    parser.add_argument("--timeout", type=float, default=5.0, help="HTTP timeout in seconds")
    parser.add_argument("--skip-events", action="store_true", help="skip SSE connection smoke")
    args = parser.parse_args()

    client = RelayClient(args.base_url, account_id=args.account_id, timeout=args.timeout, trust_env=False)
    status = client.status()
    require(status.get("status") == "ok", f"service status is not ok: {status.get('status')!r}")

    accounts = client.list_accounts()
    require(accounts, "no accounts returned by /v1/accounts")
    account_id = args.account_id or first_account_id(accounts)
    require(account_id, "could not determine account_id")
    if args.account_id:
        require(any(account.account_id == args.account_id for account in accounts), f"account {args.account_id} not in /v1/accounts")

    asset = client.get_asset(account_id)
    require(asset.account_id == account_id, f"asset account_id mismatch: {asset.account_id!r}")
    positions = client.get_positions(account_id)
    orders = client.list_orders(account_id=account_id, limit=5)
    fills = client.list_fills(account_id=account_id, limit=5)

    event_summary: dict[str, Any] = {"skipped": True}
    if not args.skip_events:
        event = next(iter(client.stream_events(account_id=account_id)))
        require(event.event_type, "SSE first event has empty event_type")
        event_summary = {
            "skipped": False,
            "event_type": event.event_type,
            "account_ids": list(event.account_ids),
            "source": event.source,
        }

    summary = {
        "ok": True,
        "base_url": args.base_url,
        "account_id": account_id,
        "status": status.get("status"),
        "dependencies": dependency_statuses(status),
        "accounts": len(accounts),
        "positions": len(positions),
        "orders_sample": len(orders),
        "fills_sample": len(fills),
        "event": event_summary,
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2, sort_keys=True))


def first_account_id(accounts: list[Any]) -> str:
    for account in accounts:
        if account.enabled and account.account_id:
            return account.account_id
    return accounts[0].account_id if accounts else ""


def dependency_statuses(status: dict[str, Any]) -> dict[str, str]:
    dependencies = status.get("dependencies")
    if not isinstance(dependencies, dict):
        return {}
    output: dict[str, str] = {}
    for name, value in dependencies.items():
        if isinstance(value, dict):
            output[name] = str(value.get("status", ""))
    return output


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


if __name__ == "__main__":
    main()
