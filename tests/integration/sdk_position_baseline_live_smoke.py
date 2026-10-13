#!/usr/bin/env python3
"""GET-only historical close baseline acceptance; never refresh the counter."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "sdk" / "python"))

from relay_sdk import RelayClient, RelayPaginationError  # noqa: E402


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://relay-trader.quantstage.com")
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--trade-date", required=True)
    parser.add_argument("--expected-count", type=int, required=True)
    parser.add_argument("--expected-environment", choices=("production", "test"), default="production")
    parser.add_argument("--page-size", type=int, default=2)
    parser.add_argument("--unavailable-date", default="")
    args = parser.parse_args()

    client = RelayClient(args.base_url, account_id=args.account_id, timeout=30, trust_env=False)
    client.require_capabilities("positions.close_baseline.v1")
    snapshot = client.get_position_baseline(
        trade_date=args.trade_date, page_size=args.page_size,
        expected_environment=args.expected_environment,
    )
    assert snapshot.baseline.is_complete and not snapshot.baseline.raw["execution_ready"]
    assert len(snapshot.positions) == snapshot.baseline.total_rows == args.expected_count
    repeated = client.get_position_baseline(
        trade_date=args.trade_date, baseline_id=snapshot.baseline.baseline_id,
        expected_environment=args.expected_environment,
    )
    assert repeated.baseline.raw == snapshot.baseline.raw
    assert repeated.positions == snapshot.positions
    unavailable = None
    if args.unavailable_date:
        page = client.get_position_baseline_page(trade_date=args.unavailable_date, require_complete=False)
        assert page.baseline.completeness == "unavailable"
        assert not page.baseline.missing_security_means_zero
        try:
            client.get_position_baseline(trade_date=args.unavailable_date)
        except RelayPaginationError as exc:
            assert exc.code == "POSITION_BASELINE_INCOMPLETE"
        else:
            raise AssertionError("missing historical baseline accepted as an empty account")
        unavailable = {"trade_date": args.unavailable_date, "request_id": page.request_id, "status": page.baseline.completeness}
    print(json.dumps({
        "ok": True, "get_only": True, "account_id": args.account_id,
        "baseline": dict(snapshot.baseline.raw), "pages": len(snapshot.pages),
        "page_request_ids": [page.request_id for page in snapshot.pages],
        "repeat_request_ids": [page.request_id for page in repeated.pages],
        "unavailable": unavailable,
    }, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
