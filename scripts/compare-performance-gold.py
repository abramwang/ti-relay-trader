#!/usr/bin/env python3
"""Compare manual performance gold rows with Relay economic NAV previews."""

from __future__ import annotations

import argparse
import csv
import json
import sys
import urllib.error
import urllib.parse
import urllib.request
from decimal import Decimal
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--gold", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--timeout", type=float, default=30.0)
    parser.add_argument("--tolerance", type=Decimal, default=Decimal("0.01"))
    return parser.parse_args()


def decimal_value(value: object) -> Decimal:
    return Decimal(str(value or 0))


def preview_url(base_url: str, account_id: str, trade_date: str) -> str:
    account = urllib.parse.quote(account_id, safe="")
    query = urllib.parse.urlencode({"trade_date": trade_date.replace("-", "")})
    return f"{base_url.rstrip('/')}/v1/accounts/{account}/performance/economic-nav/preview?{query}"


def load_preview(opener: urllib.request.OpenerDirector, url: str, timeout: float) -> dict:
    with opener.open(url, timeout=timeout) as response:
        payload = json.load(response)
    return payload["data"]["economic_nav"]


def compare(args: argparse.Namespace) -> dict:
    with args.gold.open(encoding="utf-8", newline="") as source:
        gold_rows = list(csv.DictReader(source))

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    rows = []
    for gold in gold_rows:
        trade_date = gold["trade_date"]
        item = {
            "trade_date": trade_date,
            "manual_open_asset": float(gold["open_asset_excluding_fund_occupancy"]),
            "manual_close_asset": float(gold["close_asset_excluding_fund_occupancy"]),
            "manual_daily_pnl": float(gold["daily_pnl"]),
        }
        try:
            preview = load_preview(
                opener,
                preview_url(args.base_url, args.account_id, trade_date),
                args.timeout,
            )
        except (KeyError, OSError, ValueError, urllib.error.HTTPError) as exc:
            item.update({"available": False, "error": str(exc)})
            rows.append(item)
            continue

        nav = preview.get("nav") or {}
        repo = preview.get("reverse_repo") or {}
        close_diff = decimal_value(nav.get("close_economic_nav")) - decimal_value(item["manual_close_asset"])
        pnl_diff = decimal_value(nav.get("account_day_pnl")) - decimal_value(item["manual_daily_pnl"])
        item.update(
            {
                "available": True,
                "status": nav.get("status"),
                "formula_version": nav.get("formula_version"),
                "relay_close_asset": nav.get("close_economic_nav"),
                "relay_daily_pnl": nav.get("account_day_pnl"),
                "close_diff": float(close_diff),
                "pnl_diff": float(pnl_diff),
                "close_within_tolerance": abs(close_diff) <= args.tolerance,
                "pnl_within_tolerance": abs(pnl_diff) <= args.tolerance,
                "principal_treatment": repo.get("principal_treatment"),
                "principal_cash_overlap": repo.get("principal_cash_overlap", 0),
                "principal_receivable": repo.get("principal_receivable", 0),
                "estimated_net_interest": repo.get("estimated_net_interest", 0),
                "recognized_net_interest": repo.get("recognized_net_interest", 0),
                "quality_flags": preview.get("quality_flags") or nav.get("quality_flags") or [],
            }
        )
        rows.append(item)

    available = [item for item in rows if item["available"]]
    result = {
        "account_id": args.account_id,
        "base_url": args.base_url,
        "gold": str(args.gold),
        "tolerance": float(args.tolerance),
        "summary": {
            "rows": len(rows),
            "available_rows": len(available),
            "unavailable_rows": len(rows) - len(available),
            "close_within_tolerance": sum(bool(item.get("close_within_tolerance")) for item in available),
            "pnl_within_tolerance": sum(bool(item.get("pnl_within_tolerance")) for item in available),
            "blocked_rows": sum(item.get("status") == "blocked" for item in available),
        },
        "rows": rows,
    }
    return result


def main() -> int:
    args = parse_args()
    result = compare(args)
    output = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
    if args.output:
        args.output.write_text(output, encoding="utf-8")
    sys.stdout.write(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
