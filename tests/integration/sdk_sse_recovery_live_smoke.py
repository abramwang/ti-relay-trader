#!/usr/bin/env python3
"""Validate Relay SSE cursor, gap, and SDK reconciliation behavior read-only."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))

from relay_sdk import RelayClient  # noqa: E402


def main() -> None:
    parser = argparse.ArgumentParser(description="Run Relay SDK SSE recovery smoke")
    parser.add_argument("--base-url", default="http://relay-trader.quantstage.com")
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--timeout", type=float, default=30.0)
    args = parser.parse_args()

    client = RelayClient(args.base_url, account_id=args.account_id, timeout=10.0, trust_env=False)

    fresh_stream = client.stream_events(idle_timeout=args.timeout)
    fresh = next(fresh_stream)
    fresh_stream.close()
    require(fresh.event_type == "relay.connected", f"unexpected fresh event: {fresh.event_type!r}")
    require(fresh.data.get("resume_status") == "fresh", f"unexpected fresh status: {fresh.data!r}")
    cursor = str(fresh.data.get("current_cursor") or "")
    require(cursor.startswith("evt-"), f"missing stable event cursor: {cursor!r}")
    require(not bool(fresh.data.get("reconciliation_required")), "fresh stream unexpectedly requires reconciliation")

    resumed_stream = client.stream_events(last_event_id=cursor, idle_timeout=args.timeout)
    resumed = next(resumed_stream)
    resumed_stream.close()
    require(resumed.event_type == "relay.connected", f"unexpected resumed event: {resumed.event_type!r}")
    require(resumed.data.get("resume_status") == "resumed", f"unexpected resume status: {resumed.data!r}")
    require(bool(resumed.data.get("reconciliation_required")), "resumed stream did not require reconciliation")

    gap_stream = client.stream_events(last_event_id="evt-old-process-42", idle_timeout=args.timeout)
    gap_connected = next(gap_stream)
    gap = next(gap_stream)
    gap_stream.close()
    require(gap_connected.data.get("resume_status") == "gap", f"missing gap handshake: {gap_connected.data!r}")
    require(gap.event_type == "relay.gap", f"unexpected gap event: {gap.event_type!r}")
    require(bool(gap.data.get("reconciliation_required")), "gap did not require reconciliation")

    reconciliations = []
    resilient = client.stream_events_resilient(
        last_event_id=cursor,
        on_reconcile_required=lambda snapshot: reconciliations.append(snapshot) or False,
        max_reconnects=0,
        idle_timeout=args.timeout,
    )
    try:
        next(resilient)
    except StopIteration:
        pass
    finally:
        resilient.close()
    require(len(reconciliations) == 1, f"expected one full reconciliation, got {len(reconciliations)}")
    snapshot = reconciliations[0]
    require(snapshot.account_id == args.account_id, "reconciliation account mismatch")
    require(snapshot.asset is not None and snapshot.asset.account_id == args.account_id, "asset reconciliation failed")

    print(
        json.dumps(
            {
                "ok": True,
                "base_url": args.base_url,
                "account_id": args.account_id,
                "fresh_status": fresh.data.get("resume_status"),
                "resume_status": resumed.data.get("resume_status"),
                "gap_reason": gap.data.get("reason"),
                "reconciliation": {
                    "orders": len(snapshot.orders),
                    "fills": len(snapshot.fills),
                    "positions": len(snapshot.positions),
                    "reason": snapshot.reason,
                },
                "write_requests_sent": 0,
            },
            ensure_ascii=False,
            indent=2,
            sort_keys=True,
        )
    )


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


if __name__ == "__main__":
    main()
