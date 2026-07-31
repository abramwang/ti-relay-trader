#!/usr/bin/env python3
"""Run the repeatable Relay production read-only release acceptance suite."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo


REPO_ROOT = Path(__file__).resolve().parents[1]
SHANGHAI = ZoneInfo("Asia/Shanghai")


def parse_args() -> argparse.Namespace:
    today = datetime.now(SHANGHAI).date()
    parser = argparse.ArgumentParser(
        description="Run unit, SDK, API, and Playwright checks without sending trading writes"
    )
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--account-id", default="314000046830")
    parser.add_argument("--trade-date", default=today.strftime("%Y%m%d"))
    parser.add_argument("--symbol", default="600000.SH")
    parser.add_argument("--performance-date-from", default=(today - timedelta(days=30)).strftime("%Y%m%d"))
    parser.add_argument("--performance-date-to", default=today.strftime("%Y%m%d"))
    parser.add_argument("--single-viewport", action="store_true", help="only run the 1600px browser viewport")
    parser.add_argument("--skip-browser", action="store_true", help="skip Playwright checks")
    parser.add_argument(
        "--report",
        default="",
        help="JSON report path; defaults to /tmp/relay-readonly-release-<trade_date>.json",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    base_url = args.base_url.rstrip("/")
    report_path = Path(args.report or f"/tmp/relay-readonly-release-{args.trade_date}.json")
    report_path.parent.mkdir(parents=True, exist_ok=True)

    preflight = verify_read_only_target(base_url, args.account_id)
    commands = build_commands(args, base_url)
    results: list[dict[str, Any]] = []

    print(
        f"read-only preflight passed: environment={preflight['environment']} "
        f"accounts={preflight['accounts_configured']} trading_enabled=0 account={args.account_id}"
    )
    for name, command in commands:
        started = time.monotonic()
        print(f"\n[{name}] + {' '.join(command)}", flush=True)
        completed = subprocess.run(command, cwd=REPO_ROOT, check=False)
        result = {
            "name": name,
            "command": command,
            "exit_code": completed.returncode,
            "duration_seconds": round(time.monotonic() - started, 3),
            "status": "passed" if completed.returncode == 0 else "failed",
        }
        results.append(result)

    failed = [result for result in results if result["exit_code"] != 0]
    report = {
        "schema": "relay.release_check.v1",
        "generated_at": datetime.now(SHANGHAI).isoformat(),
        "timezone": "Asia/Shanghai",
        "mode": "production_read_only",
        "base_url": base_url,
        "account_id": args.account_id,
        "trade_date": args.trade_date,
        "preflight": preflight,
        "status": "passed" if not failed else "failed",
        "checks": results,
    }
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"\nrelease report: {report_path}")
    if failed:
        print("failed checks: " + ", ".join(result["name"] for result in failed), file=sys.stderr)
        return 1
    print(f"all {len(results)} read-only release checks passed")
    return 0


def verify_read_only_target(base_url: str, account_id: str) -> dict[str, Any]:
    status = request_json(f"{base_url}/v1/status").get("data", {})
    accounts_payload = request_json(f"{base_url}/v1/accounts").get("data", {})
    accounts = accounts_payload.get("accounts") or []
    environment = str(status.get("environment") or "")
    trading_enabled = int((status.get("accounts") or {}).get("trading_enabled") or 0)

    require(environment == "production", f"target environment must be production, got {environment!r}")
    require(trading_enabled == 0, f"production trading guard failed: {trading_enabled} account(s) enabled")
    require(accounts, "no accounts returned by /v1/accounts")
    selected = next((account for account in accounts if account.get("account_id") == account_id), None)
    require(selected is not None, f"account {account_id} is not available")
    require(not selected.get("trading_enabled"), f"account {account_id} has trading enabled")

    return {
        "environment": environment,
        "service_status": status.get("status"),
        "accounts_configured": len(accounts),
        "trading_enabled": trading_enabled,
        "selected_account_enabled": bool(selected.get("enabled")),
        "selected_account_trading_enabled": bool(selected.get("trading_enabled")),
    }


def request_json(url: str) -> dict[str, Any]:
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    last_error: Exception | None = None
    for attempt in range(3):
        try:
            with opener.open(url, timeout=20) as response:
                return json.load(response)
        except (OSError, urllib.error.HTTPError, ValueError) as exc:
            last_error = exc
            if attempt < 2:
                time.sleep(1)
    raise SystemExit(f"read-only release preflight failed for {url}: {last_error}")


def build_commands(args: argparse.Namespace, base_url: str) -> list[tuple[str, list[str]]]:
    python = sys.executable
    commands: list[tuple[str, list[str]]] = [
        (
            "api-catalog",
            [python, "scripts/check-api-catalog.py", "--base-url", base_url],
        ),
        ("go-unit", ["go", "test", "./..."]),
        (
            "python-unit",
            [python, "-m", "unittest", "discover", "-s", "tests/unit", "-p", "test_*.py"],
        ),
        ("python-sdk-release", [python, "scripts/check-python-sdk-release.py"]),
        ("page-smoke", [python, "tests/integration/page_smoke.py", "--base-url", base_url]),
        (
            "sdk-live-readonly",
            [
                python,
                "tests/integration/sdk_live_smoke.py",
                "--base-url",
                base_url,
                "--account-id",
                args.account_id,
                "--allow-degraded",
            ],
        ),
    ]
    if args.skip_browser:
        return commands

    browser_python = REPO_ROOT / ".venv" / "bin" / "python"
    require(browser_python.exists(), "missing .venv Playwright runtime; see tests/README.md")
    viewports = [(1600, 1000)]
    if not args.single_viewport:
        viewports.append((1280, 800))

    jobs_trade_date = datetime.strptime(args.trade_date, "%Y%m%d").strftime("%Y-%m-%d")
    for width, height in viewports:
        suffix = f"{width}x{height}"
        commands.extend(
            [
                (
                    f"trade-terminal-{suffix}",
                    [
                        str(browser_python),
                        "tests/integration/trade_terminal_interaction_smoke.py",
                        "--base-url",
                        base_url,
                        "--account-id",
                        args.account_id,
                        "--trade-date",
                        args.trade_date,
                        "--symbol",
                        args.symbol,
                        "--width",
                        str(width),
                        "--height",
                        str(height),
                        "--output",
                        f"/tmp/relay-trade-release-{suffix}.png",
                    ],
                ),
                (
                    f"api-console-{suffix}",
                    [
                        str(browser_python),
                        "tests/integration/api_console_interaction_smoke.py",
                        "--base-url",
                        base_url,
                        "--account-id",
                        args.account_id,
                        "--trade-date",
                        args.trade_date,
                        "--width",
                        str(width),
                        "--height",
                        str(height),
                        "--output",
                        f"/tmp/relay-api-console-release-{suffix}.png",
                    ],
                ),
                (
                    f"performance-{suffix}",
                    [
                        str(browser_python),
                        "tests/integration/performance_visual_smoke.py",
                        "--base-url",
                        base_url,
                        "--date-from",
                        args.performance_date_from,
                        "--date-to",
                        args.performance_date_to,
                        "--width",
                        str(width),
                        "--height",
                        str(max(height, 800)),
                        "--output",
                        f"/tmp/relay-performance-release-{suffix}.png",
                    ],
                ),
                (
                    f"jobs-{suffix}",
                    [
                        str(browser_python),
                        "tests/integration/jobs_visual_smoke.py",
                        "--base-url",
                        base_url,
                        "--trade-date",
                        jobs_trade_date,
                        "--width",
                        str(width),
                        "--height",
                        str(height),
                        "--output",
                        f"/tmp/relay-jobs-release-{suffix}.png",
                    ],
                ),
                (
                    f"operations-{suffix}",
                    [
                        str(browser_python),
                        "tests/integration/operations_visual_smoke.py",
                        "--base-url",
                        base_url,
                        "--width",
                        str(width),
                        "--height",
                        str(height),
                        "--output",
                        f"/tmp/relay-operations-release-{suffix}.png",
                    ],
                ),
            ]
        )
    return commands


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


if __name__ == "__main__":
    raise SystemExit(main())
