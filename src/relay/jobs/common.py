"""Shared implementation for relay trading-day jobs."""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Callable, Iterable, Mapping
from urllib import parse, request

try:
    from zoneinfo import ZoneInfo
except ImportError:  # pragma: no cover - Python 3.8 fallback.
    ZoneInfo = None  # type: ignore[assignment]

try:
    from relay_sdk import RelayClient
except ModuleNotFoundError:  # pragma: no cover - convenience for repo-local cron.
    REPO_ROOT = Path(__file__).resolve().parents[3]
    sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))
    from relay_sdk import RelayClient

from .alerts import dispatch_daily_job_alert


TIMEZONE_NAME = "Asia/Shanghai"
DEFAULT_BASE_URL = "http://relay-trader.quantstage.com"
DEFAULT_MERIDIAN_BASE_URL = "http://meridian-data.quantstage.com"
DEFAULT_QUERY_LIMIT = 500
DEFAULT_REFRESH_TIMEOUT_SECONDS = 60.0
DEFAULT_REFRESH_POLL_SECONDS = 1.0
DEFAULT_SETTLEMENT_TIMEOUT_SECONDS = 30.0
FRESHNESS_CHECK_STEPS = {"asset", "positions"}
REQUIRED_JOB_DEPENDENCIES = ("database", "redis", "order_service", "market", "event_stream")


def business_timezone() -> timezone:
    if ZoneInfo is not None:
        return ZoneInfo(TIMEZONE_NAME)  # type: ignore[return-value]
    return timezone(timedelta(hours=8), TIMEZONE_NAME)


BUSINESS_TZ = business_timezone()


@dataclass(frozen=True)
class TradingDayInfo:
    requested_date: str
    target_trade_date: str
    is_trading_day: bool
    source: str
    raw: Mapping[str, Any]

    def to_dict(self) -> dict[str, Any]:
        return {
            "requested_date": self.requested_date,
            "target_trade_date": self.target_trade_date,
            "is_trading_day": self.is_trading_day,
            "source": self.source,
            "raw": dict(self.raw),
        }


@dataclass(frozen=True)
class JobOptions:
    job_name: str
    base_url: str = DEFAULT_BASE_URL
    meridian_base_url: str = DEFAULT_MERIDIAN_BASE_URL
    account_ids: tuple[str, ...] = ()
    target_date: str = ""
    timeout: float = 10.0
    settlement_timeout_seconds: float = DEFAULT_SETTLEMENT_TIMEOUT_SECONDS
    refresh_wait_seconds: float = 1.0
    refresh_timeout_seconds: float = DEFAULT_REFRESH_TIMEOUT_SECONDS
    refresh_poll_seconds: float = DEFAULT_REFRESH_POLL_SECONDS
    query_limit: int = DEFAULT_QUERY_LIMIT
    dry_run: bool = False
    skip_refresh: bool = False
    persist: bool = False
    trigger: str = "manual"
    allow_non_trading_day: bool = False
    skip_trading_day_check: bool = False
    output: str = ""
    indent: int = 2


def parse_args(job_name: str, description: str) -> JobOptions:
    parser = argparse.ArgumentParser(description=description)
    parser.add_argument("--base-url", default=os.getenv("RELAY_BASE_URL", DEFAULT_BASE_URL), help="relay 9092 base URL")
    parser.add_argument(
        "--meridian-base-url",
        default=os.getenv("MERIDIAN_BASE_URL", DEFAULT_MERIDIAN_BASE_URL),
        help="Meridian data service base URL",
    )
    parser.add_argument("--account-id", action="append", default=[], help="account id to process; can be repeated")
    parser.add_argument("--target-date", default="", help="business date in YYYYMMDD or YYYY-MM-DD; defaults to today in Asia/Shanghai")
    parser.add_argument("--timeout", type=float, default=10.0, help="HTTP timeout in seconds")
    parser.add_argument(
        "--settlement-timeout-seconds",
        type=float,
        default=DEFAULT_SETTLEMENT_TIMEOUT_SECONDS,
        help="HTTP timeout for the multi-account settlement snapshot request",
    )
    parser.add_argument("--refresh-wait-seconds", type=float, default=1.0, help="seconds to wait after publishing refresh commands")
    parser.add_argument(
        "--refresh-timeout-seconds",
        type=float,
        default=DEFAULT_REFRESH_TIMEOUT_SECONDS,
        help="maximum seconds to wait until refreshed asset/positions are visible in the local ledger",
    )
    parser.add_argument(
        "--refresh-poll-seconds",
        type=float,
        default=DEFAULT_REFRESH_POLL_SECONDS,
        help="local ledger polling interval while waiting for refreshed asset/positions",
    )
    parser.add_argument("--query-limit", type=int, default=DEFAULT_QUERY_LIMIT, help="orders/fills sample limit")
    parser.add_argument("--dry-run", action="store_true", help="do not publish refresh commands")
    parser.add_argument("--skip-refresh", action="store_true", help="skip refresh commands and only query local ledger")
    parser.add_argument("--persist", action="store_true", help="persist the final report through relay POST /v1/jobs/runs")
    parser.add_argument("--trigger", default="manual", help="job trigger label persisted with --persist, for example cron or manual")
    parser.add_argument("--allow-non-trading-day", action="store_true", help="run account flow even when target date is not a trading day")
    parser.add_argument("--skip-trading-day-check", action="store_true", help="do not call Meridian trading-day endpoint")
    parser.add_argument("--output", default="", help="optional JSON report path")
    parser.add_argument("--indent", type=int, default=2, help="JSON indentation; use 0 for compact output")
    args = parser.parse_args()
    account_ids = tuple(_split_account_ids(args.account_id or os.getenv("RELAY_ACCOUNT_ID", "")))
    return JobOptions(
        job_name=job_name,
        base_url=args.base_url,
        meridian_base_url=args.meridian_base_url,
        account_ids=account_ids,
        target_date=normalize_trade_date(args.target_date),
        timeout=args.timeout,
        settlement_timeout_seconds=max(args.settlement_timeout_seconds, args.timeout, 0.1),
        refresh_wait_seconds=max(args.refresh_wait_seconds, 0.0),
        refresh_timeout_seconds=max(args.refresh_timeout_seconds, 0.0),
        refresh_poll_seconds=max(args.refresh_poll_seconds, 0.05),
        query_limit=max(args.query_limit, 1),
        dry_run=args.dry_run,
        skip_refresh=args.skip_refresh,
        persist=args.persist,
        trigger=args.trigger,
        allow_non_trading_day=args.allow_non_trading_day,
        skip_trading_day_check=args.skip_trading_day_check,
        output=args.output,
        indent=max(args.indent, 0),
    )


def run_pre_open_init(options: JobOptions, *, client: Any | None = None, trading_day: TradingDayInfo | None = None) -> dict[str, Any]:
    return run_daily_job(
        options,
        client=client,
        trading_day=trading_day,
        phase="pre_open_init",
        refresh_steps=("orders", "fills", "asset", "positions"),
        check_non_terminal_orders=True,
        settle_snapshots=True,
        snapshot_type="open",
        snapshot_report_key="open_snapshot",
    )


def run_post_close_settlement(options: JobOptions, *, client: Any | None = None, trading_day: TradingDayInfo | None = None) -> dict[str, Any]:
    return run_daily_job(
        options,
        client=client,
        trading_day=trading_day,
        phase="post_close_settlement",
        refresh_steps=("orders", "fills", "fees", "asset", "positions"),
        check_non_terminal_orders=True,
        settle_snapshots=True,
        snapshot_type="close",
        snapshot_report_key="settlement_snapshot",
    )


def run_daily_performance(options: JobOptions, *, client: Any | None = None, trading_day: TradingDayInfo | None = None) -> dict[str, Any]:
    relay_client = client or RelayClient(options.base_url, timeout=options.timeout, trust_env=False)
    report = run_daily_job(
        options,
        client=relay_client,
        trading_day=trading_day,
        phase="performance_daily",
        refresh_steps=(),
        check_non_terminal_orders=True,
    )
    if report.get("skipped") or not report.get("accounts"):
        return report

    ready_accounts: list[str] = []
    attention_accounts: list[str] = []
    blocked_accounts: list[str] = []
    not_applicable_accounts: list[str] = []
    for account_report in report["accounts"]:
        account_id = str(account_report["account_id"])
        cost_value, cost_report = capture_call(
            "preview_cost_ledger",
            relay_client.preview_cost_ledger,
            account_id=account_id,
            trade_date=report["trading_day"]["target_trade_date"],
            include_result=False,
        )
        nav_value, nav_report = capture_call(
            "preview_economic_nav",
            relay_client.preview_economic_nav,
            account_id=account_id,
            trade_date=report["trading_day"]["target_trade_date"],
            include_result=False,
        )
        cost = result_to_jsonable(cost_value) if cost_value is not None else {}
        nav = result_to_jsonable(nav_value) if nav_value is not None else {}
        cost_status = str(cost.get("status") or "error" if isinstance(cost, Mapping) else "error")
        nav_status = str(nav.get("status") or "error" if isinstance(nav, Mapping) else "error")
        flags = sorted(
            {
                str(flag)
                for value in (cost, nav)
                if isinstance(value, Mapping)
                for flag in value.get("quality_flags", [])
            }
        )
        errors = [
            item["error"]
            for item in (cost_report, nav_report)
            if item.get("error")
        ]
        fee_incomplete = any(
            flag in {
                "net_performance_fee_incomplete",
                "net_performance_fee_estimated",
                "order_fee_day_incomplete",
                "missing_fee_rule",
                "missing_repo_fee",
            }
            for flag in flags
        )
        not_applicable = performance_account_not_applicable(
            account_report=account_report,
            cost=cost,
            nav=nav,
            errors=errors,
            cost_status=cost_status,
            nav_status=nav_status,
            flags=flags,
        )
        if not_applicable:
            status = "not_applicable"
            not_applicable_accounts.append(account_id)
        elif errors or cost_status == "blocked" or nav_status == "blocked":
            status = "blocked"
            blocked_accounts.append(account_id)
        elif fee_incomplete:
            status = "attention"
            attention_accounts.append(account_id)
        else:
            status = "ready"
            ready_accounts.append(account_id)
        account_report["performance"] = {
            "status": status,
            "cost_ledger_status": cost_status,
            "economic_nav_status": nav_status,
            "fee_complete": not fee_incomplete,
            "quality_flags": flags,
            "errors": errors,
            "reason": "empty_account_without_performance_baseline" if not_applicable else "",
            "cost_ledger": performance_calculation_summary(cost, include_positions=True),
            "economic_nav": performance_calculation_summary(nav),
        }

    report["performance_summary"] = {
        "accounts": len(report["accounts"]),
        "ready": len(ready_accounts),
        "attention": len(attention_accounts),
        "blocked": len(blocked_accounts),
        "not_applicable": len(not_applicable_accounts),
    }
    report["performance_ready_accounts"] = ready_accounts
    report["performance_attention_accounts"] = attention_accounts
    report["performance_blocked_accounts"] = blocked_accounts
    report["performance_not_applicable_accounts"] = not_applicable_accounts
    if attention_accounts or blocked_accounts:
        report.setdefault("warnings", []).append(
            "daily performance has account-level attention or blocked results"
        )
    return report


def performance_account_not_applicable(
    *,
    account_report: Mapping[str, Any],
    cost: Any,
    nav: Any,
    errors: list[str],
    cost_status: str,
    nav_status: str,
    flags: list[str],
) -> bool:
    """Identify a clean-start account with no capital or trading activity."""
    if errors or cost_status == "blocked" or nav_status != "blocked":
        return False
    if "empty_clean_start_continuation" not in flags or "missing_positive_economic_nav" not in flags:
        return False

    snapshot = account_report.get("snapshot")
    snapshot = snapshot if isinstance(snapshot, Mapping) else {}
    asset = snapshot.get("asset")
    asset = asset if isinstance(asset, Mapping) else {}
    cost_summary = cost.get("summary") if isinstance(cost, Mapping) else {}
    cost_summary = cost_summary if isinstance(cost_summary, Mapping) else {}
    cash_flows = nav.get("cash_flows") if isinstance(nav, Mapping) else {}
    cash_flows = cash_flows if isinstance(cash_flows, Mapping) else {}

    activity_values = (
        snapshot.get("orders_count"),
        snapshot.get("fills_count"),
        snapshot.get("positions_count"),
        asset.get("net_asset"),
        asset.get("cash_available"),
        asset.get("market_value"),
        cost_summary.get("open_quantity"),
        cost_summary.get("buy_quantity"),
        cost_summary.get("sell_quantity"),
        cost_summary.get("close_quantity"),
        cash_flows.get("external_flow_count"),
        cash_flows.get("settlement_count"),
        cash_flows.get("income_expense_count"),
        cash_flows.get("internal_flow_count"),
        cash_flows.get("external_net_flow"),
        cash_flows.get("settlement_adjustment"),
        cash_flows.get("income_expense"),
        cash_flows.get("internal_transfer"),
    )
    try:
        return all(abs(float(value or 0)) <= 0.000001 for value in activity_values)
    except (TypeError, ValueError):
        return False


def performance_calculation_summary(value: Any, *, include_positions: bool = False) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        return {}
    fields = (
        "account_id",
        "trade_date",
        "status",
        "formula_version",
        "persisted",
        "opening_source",
        "summary",
        "nav",
        "reconciliation",
        "daily_performance",
        "cash_flows",
        "reverse_repo",
        "valuation",
        "quality_flags",
        "calculated_at",
    )
    result = {field: result_to_jsonable(value[field]) for field in fields if field in value}
    if include_positions:
        positions = value.get("positions")
        result["positions_count"] = len(positions) if isinstance(positions, list) else 0
    return result


def run_daily_job(
    options: JobOptions,
    *,
    client: Any | None,
    trading_day: TradingDayInfo | None,
    phase: str,
    refresh_steps: tuple[str, ...],
    check_non_terminal_orders: bool,
    settle_snapshots: bool = False,
    snapshot_type: str = "close",
    snapshot_report_key: str = "settlement_snapshot",
) -> dict[str, Any]:
    started_at = now_iso()
    relay_client = client or RelayClient(options.base_url, timeout=options.timeout, trust_env=False)
    requested_date = options.target_date or today_trade_date()
    report: dict[str, Any] = {
        "ok": True,
        "job": phase,
        "timezone": TIMEZONE_NAME,
        "base_url": options.base_url,
        "started_at": started_at,
        "finished_at": "",
        "dry_run": options.dry_run,
        "skip_refresh": options.skip_refresh,
        "skipped": False,
        "errors": [],
    }

    if trading_day is None:
        trading_day_value, trading_day_report = capture_call(
            "resolve_trading_day",
            resolve_trading_day,
            options,
            requested_date,
        )
        report["trading_day_query"] = trading_day_report
        if trading_day_report.get("error") or not isinstance(trading_day_value, TradingDayInfo):
            report["trading_day"] = {
                "requested_date": requested_date,
                "target_trade_date": requested_date,
                "is_trading_day": None,
                "source": "unavailable",
                "raw": {},
            }
            report["ok"] = False
            report["errors"].append(
                trading_day_report.get("error", "resolve_trading_day: invalid trading-day result")
            )
            return finish_report(report)
        trading_day = trading_day_value
    report["trading_day"] = trading_day.to_dict()
    if not trading_day.is_trading_day and not options.allow_non_trading_day:
        report["skipped"] = True
        report["skip_reason"] = "target date is not an A-share trading day"
        return finish_report(report)

    status_value, status_report = capture_call("status", relay_client.status)
    report["status"] = status_report
    if status_report.get("error"):
        report["ok"] = False
        report["errors"].append(status_report["error"])
        return finish_report(report)
    status_error = daily_job_status_error(status_value)
    if status_error:
        report["ok"] = False
        report["errors"].append(status_error)
        return finish_report(report)
    if isinstance(status_value, Mapping) and status_value.get("status") == "degraded":
        report.setdefault("warnings", []).append(
            "relay status is degraded, but all daily-job dependencies are healthy"
        )

    accounts_value, accounts_report = capture_call("list_accounts", relay_client.list_accounts)
    report["accounts_query"] = accounts_report
    if accounts_report.get("error"):
        report["ok"] = False
        report["errors"].append(accounts_report["error"])
        return finish_report(report)

    accounts = select_accounts(accounts_value or [], options.account_ids)
    account_reports = [
        start_account_flow(
            relay_client,
            account_id,
            options=options,
            refresh_steps=refresh_steps,
        )
        for account_id in accounts
    ]
    wait_for_refreshed_ledgers(
        relay_client,
        account_reports,
        steps=refresh_steps,
        options=options,
    )
    for account_report in account_reports:
        complete_account_flow(
            relay_client,
            account_report,
            trade_date=trading_day.target_trade_date,
            query_limit=options.query_limit,
            check_non_terminal_orders=check_non_terminal_orders,
            include_fees="fees" in refresh_steps,
        )

    report["accounts"] = account_reports
    account_errors: list[dict[str, Any]] = []
    snapshot_blocked_accounts: list[str] = []
    for account_report in account_reports:
        account_id = str(account_report["account_id"])
        if account_report.get("errors"):
            account_errors.append({"account_id": account_id, "errors": list(account_report["errors"])})
        if account_report.get("snapshot_blocked"):
            snapshot_blocked_accounts.append(account_id)
    if account_errors:
        report["account_error_count"] = len(account_errors)
        report["account_errors"] = account_errors
    if snapshot_blocked_accounts:
        report["snapshot_blocked_accounts"] = snapshot_blocked_accounts

    if settle_snapshots and accounts:
        settlement_run_id = f"{phase}-{trading_day.target_trade_date}"
        run_id_key = "settlement_run_id" if snapshot_type == "close" else f"{snapshot_type}_snapshot_run_id"
        report[run_id_key] = settlement_run_id
        snapshot_accounts = [account_id for account_id in accounts if account_id not in set(snapshot_blocked_accounts)]
        report["snapshot_account_ids"] = snapshot_accounts
        if not snapshot_accounts:
            error = f"{snapshot_type} snapshot skipped: no account has confirmed refreshed asset/positions"
            report[snapshot_report_key] = {"ok": False, "error": error}
            report["ok"] = False
            report["errors"].append(error)
            return finish_report(report)
        snapshot_client = settlement_snapshot_client(relay_client, options)
        settlement_value, settlement_report = capture_call(
            "record_settlement_snapshot",
            snapshot_client.record_settlement_snapshot,
            trade_date=trading_day.target_trade_date,
            account_ids=snapshot_accounts,
            run_id=settlement_run_id,
            snapshot_type=snapshot_type,
            source=phase,
            dry_run=options.dry_run,
        )
        report[snapshot_report_key] = settlement_report
        if settlement_report.get("error"):
            report["ok"] = False
            report["errors"].append(settlement_report["error"])
        elif isinstance(settlement_value, Mapping) and settlement_value.get("status") == "failed":
            report["ok"] = False
            errors = settlement_value.get("errors")
            if errors:
                report["errors"].append(f"{snapshot_type} snapshot failed: {errors}")

    return finish_report(report)


def settlement_snapshot_client(client: Any, options: JobOptions) -> Any:
    if not isinstance(client, RelayClient) or client.timeout >= options.settlement_timeout_seconds:
        return client
    return RelayClient(
        client.base_url,
        account_id=client.account_id,
        timeout=options.settlement_timeout_seconds,
        api_key=client.api_key,
        trust_env=False,
    )


def start_account_flow(
    client: Any,
    account_id: str,
    *,
    options: JobOptions,
    refresh_steps: tuple[str, ...],
) -> dict[str, Any]:
    account_report: dict[str, Any] = {
        "account_id": account_id,
        "refresh": [],
        "snapshot": {},
        "errors": [],
    }
    if not options.dry_run and not options.skip_refresh:
        refresh_started_at = datetime.now(BUSINESS_TZ)
        account_report["refresh_started_at"] = refresh_started_at.isoformat()
        freshness_refresh_errors: list[str] = []
        for step in refresh_steps:
            _value, result = capture_call(f"refresh_{step}", getattr(client, f"refresh_{step}"), account_id)
            account_report["refresh"].append({"step": step, **result})
            if result.get("error"):
                account_report["errors"].append(result["error"])
                if step in FRESHNESS_CHECK_STEPS:
                    freshness_refresh_errors.append(result["error"])
        if freshness_refresh_errors:
            account_report["snapshot_blocked"] = True
            account_report["refresh_freshness"] = {
                "ok": False,
                "error": "asset/positions refresh command failed; snapshot blocked to avoid stale settlement data",
                "refresh_errors": freshness_refresh_errors,
            }
    return account_report


def complete_account_flow(
    client: Any,
    account_report: dict[str, Any],
    *,
    trade_date: str,
    query_limit: int,
    check_non_terminal_orders: bool,
    include_fees: bool = False,
) -> None:
    account_id = str(account_report["account_id"])
    asset_value, asset_report = capture_call("get_asset", client.get_asset, account_id, include_result=False)
    positions_value, positions_report = capture_call("get_positions", client.get_positions, account_id, include_result=False)
    orders_value, orders_report = capture_call(
        "list_orders",
        client.list_orders,
        account_id=account_id,
        trade_date=trade_date,
        history=True,
        limit=query_limit,
        include_result=False,
    )
    fills_value, fills_report = capture_call(
        "list_fills",
        client.list_fills,
        account_id=account_id,
        trade_date=trade_date,
        history=True,
        limit=query_limit,
        include_result=False,
    )
    fees_value: Any = []
    fees_report: dict[str, Any] = {"ok": True, "skipped": True}
    if include_fees:
        fees_value, fees_report = capture_call(
            "list_order_fees",
            client.list_order_fees,
            account_id,
            trade_date=trade_date,
            limit=query_limit,
            include_result=False,
        )
    snapshot_reports = {
        "asset": asset_report,
        "positions": positions_report,
        "orders": orders_report,
        "fills": fills_report,
    }
    if include_fees:
        snapshot_reports["fees"] = fees_report
    snapshot_values = {
        "asset": asset_value,
        "positions": positions_value,
        "orders": orders_value,
        "fills": fills_value,
        "fees": fees_value,
    }
    account_report["queries"] = snapshot_reports
    account_report["snapshot"] = summarize_snapshot(snapshot_values, check_non_terminal_orders=check_non_terminal_orders)
    for result in snapshot_reports.values():
        if result.get("error"):
            account_report["errors"].append(result["error"])


def summarize_snapshot(snapshot: Mapping[str, Any], *, check_non_terminal_orders: bool) -> dict[str, Any]:
    asset = snapshot.get("asset")
    positions = snapshot.get("positions") or []
    orders = snapshot.get("orders") or []
    fills = snapshot.get("fills") or []
    fees = snapshot.get("fees") or []
    non_terminal_orders = [order for order in orders if not bool(getattr(order, "is_terminal", False))]
    summary = {
        "asset": model_summary(asset, fields=("account_id", "net_asset", "cash_available", "market_value")),
        "positions_count": len(positions),
        "positions_latest_updated_at": format_optional_datetime(
            latest_model_datetime(positions, fields=("updated_at", "captured_at"))
        ),
        "orders_count": len(orders),
        "fills_count": len(fills),
        "fees_count": len(fees),
        "complete_fees_count": sum(
            1
            for fee in fees
            if bool(getattr(fee, "fee_complete", False))
            and bool(getattr(fee, "association_complete", False))
        ),
        "non_terminal_orders": len(non_terminal_orders),
    }
    asset_updated_at = model_datetime(asset, fields=("updated_at", "captured_at"))
    if asset_updated_at is not None:
        summary["asset_updated_at"] = format_optional_datetime(asset_updated_at)
    if check_non_terminal_orders and non_terminal_orders:
        summary["non_terminal_order_ids"] = [
            str(getattr(order, "gateway_order_id", "")) for order in non_terminal_orders[:20]
        ]
    return summary


def wait_for_refreshed_ledgers(
    client: Any,
    account_reports: list[dict[str, Any]],
    *,
    steps: tuple[str, ...],
    options: JobOptions,
) -> None:
    if options.dry_run or options.skip_refresh or not account_reports:
        return
    required = tuple(step for step in steps if step in FRESHNESS_CHECK_STEPS)
    if not required:
        for account_report in account_reports:
            account_report["refresh_freshness"] = {
                "ok": True,
                "skipped": True,
                "reason": "no asset/positions refresh steps",
            }
        return
    if options.refresh_timeout_seconds <= 0:
        for account_report in account_reports:
            if not account_report.get("snapshot_blocked"):
                account_report["refresh_freshness"] = {
                    "ok": True,
                    "skipped": True,
                    "reason": "refresh freshness wait disabled",
                }
        return

    pending = {
        str(account_report["account_id"]): account_report
        for account_report in account_reports
        if not account_report.get("snapshot_blocked")
    }
    if not pending:
        return
    if options.refresh_wait_seconds > 0:
        time.sleep(options.refresh_wait_seconds)

    started_waiting_at = time.monotonic()
    deadline = time.monotonic() + options.refresh_timeout_seconds
    attempts = {account_id: 0 for account_id in pending}
    last_reports: dict[str, dict[str, Any]] = {}
    while pending:
        for account_id, account_report in list(pending.items()):
            attempts[account_id] += 1
            refresh_started_at = datetime.fromisoformat(str(account_report["refresh_started_at"]))
            freshness = refreshed_ledger_status(client, account_id, required, refresh_started_at)
            terminal_status = refreshed_query_terminal_status(client, account_report)
            freshness["query_terminals"] = terminal_status["commands"]
            freshness["query_terminals_ok"] = terminal_status["ok"]
            freshness["query_terminal_failure"] = terminal_status["terminal_failure"]
            freshness["ok"] = bool(freshness.get("ok")) and bool(terminal_status["ok"])
            freshness["attempts"] = attempts[account_id]
            last_reports[account_id] = freshness
            if freshness.get("ok"):
                freshness["fresh_after_seconds"] = round(time.monotonic() - started_waiting_at, 3)
                account_report["refresh_freshness"] = freshness
                del pending[account_id]
            elif terminal_status["terminal_failure"]:
                freshness["error"] = query_terminal_error(terminal_status)
                account_report["refresh_freshness"] = freshness
                account_report["snapshot_blocked"] = True
                account_report["errors"].append(str(freshness["error"]))
                del pending[account_id]

        remaining = deadline - time.monotonic()
        if remaining <= 0:
            for account_id, account_report in pending.items():
                freshness = last_reports.get(account_id, {
                    "ok": False,
                    "account_id": account_id,
                    "required": list(required),
                })
                freshness["error"] = refresh_timeout_error(freshness, options.refresh_timeout_seconds)
                freshness["timed_out"] = True
                account_report["refresh_freshness"] = freshness
                account_report["snapshot_blocked"] = True
                account_report["errors"].append(str(freshness["error"]))
            return
        if not pending:
            return
        time.sleep(min(options.refresh_poll_seconds, remaining))


def refresh_timeout_error(report: Mapping[str, Any], timeout_seconds: float) -> str:
    commands = report.get("query_terminals")
    command_states = "-"
    if isinstance(commands, Mapping):
        command_states = ",".join(
            f"{step}:{str(value.get('state') or 'unknown')}"
            for step, value in commands.items()
            if isinstance(value, Mapping)
        ) or "-"
    return (
        f"asset/positions refresh not visible in local ledger after "
        f"{timeout_seconds:.1f}s; "
        f"asset_updated_at={report.get('asset_updated_at') or '-'}, "
        f"positions_latest_updated_at={report.get('positions_latest_updated_at') or '-'}, "
        f"positions_count={report.get('positions_count', 0)}, "
        f"query_terminals={command_states}"
    )


def refreshed_query_terminal_status(client: Any, account_report: Mapping[str, Any]) -> dict[str, Any]:
    commands: dict[str, dict[str, Any]] = {}
    terminal_failure = False
    refreshes = account_report.get("refresh")
    if not isinstance(refreshes, list) or not refreshes:
        return {"ok": False, "terminal_failure": True, "commands": commands}

    for refresh in refreshes:
        if not isinstance(refresh, Mapping):
            continue
        step = str(refresh.get("step") or "").strip()
        if refresh.get("error"):
            commands[step or "unknown"] = {
                "state": "invalid",
                "error": str(refresh.get("error")),
            }
            terminal_failure = True
            continue
        result = refresh.get("result")
        if not isinstance(result, Mapping):
            commands[step or "unknown"] = {
                "state": "invalid",
                "error": "refresh receipt missing result",
            }
            terminal_failure = True
            continue
        message_id = str(result.get("message_id") or "").strip()
        expected_action = str(result.get("action") or "").strip()
        if not message_id:
            commands[step or "unknown"] = {
                "state": "invalid",
                "action": expected_action,
                "error": "refresh receipt missing message_id",
            }
            terminal_failure = True
            continue
        try:
            value = client.get_query_status(message_id)
            status = result_to_jsonable(value)
            if not isinstance(status, Mapping):
                raise RuntimeError("query status response is invalid")
            command = dict(status)
        except Exception as exc:  # noqa: BLE001 - transient status lookup remains pending until timeout.
            commands[step] = {
                "origin_message_id": message_id,
                "action": expected_action,
                "state": "pending",
                "error": str(exc),
            }
            continue

        action = str(command.get("action") or "").strip()
        state = str(command.get("state") or "pending").strip()
        success = bool(command.get("success")) and state == "completed"
        if expected_action and action and action != expected_action:
            state = "invalid"
            success = False
            command["state"] = state
            command["success"] = False
            command["error"] = f"query action mismatch: expected {expected_action}, got {action or '-'}"
        commands[step] = command
        if state in {"failed", "invalid"}:
            terminal_failure = True
        elif not success:
            continue

    return {
        "ok": bool(commands) and all(
            isinstance(command, Mapping)
            and command.get("state") == "completed"
            and bool(command.get("success"))
            for command in commands.values()
        ),
        "terminal_failure": terminal_failure,
        "commands": commands,
    }


def query_terminal_error(report: Mapping[str, Any]) -> str:
    failures: list[str] = []
    commands = report.get("commands")
    if isinstance(commands, Mapping):
        for step, value in commands.items():
            if not isinstance(value, Mapping) or value.get("state") not in {"failed", "invalid"}:
                continue
            replies = value.get("replies")
            code = ""
            if isinstance(replies, list) and replies and isinstance(replies[-1], Mapping):
                code = str(replies[-1].get("code") or "")
            failures.append(
                f"{step}:{value.get('state')}:{code or value.get('error') or '-'}"
            )
    return "query terminal validation failed; " + (", ".join(failures) or "unknown command outcome")


def refreshed_ledger_status(
    client: Any,
    account_id: str,
    required: tuple[str, ...],
    refresh_started_at: datetime,
) -> dict[str, Any]:
    cutoff = refresh_started_at - timedelta(seconds=2)
    report: dict[str, Any] = {
        "ok": False,
        "account_id": account_id,
        "refresh_started_at": refresh_started_at.isoformat(),
        "required": list(required),
    }

    asset = None
    asset_error = ""
    if "asset" in required:
        try:
            asset = client.get_asset(account_id)
        except Exception as exc:  # noqa: BLE001 - report and keep polling.
            asset_error = str(exc)
    asset_updated_at = model_datetime(asset, fields=("updated_at", "captured_at"))
    asset_fresh = "asset" not in required or (asset_updated_at is not None and asset_updated_at >= cutoff)
    if asset_error:
        report["asset_error"] = asset_error
    report["asset_fresh"] = asset_fresh
    report["asset_updated_at"] = format_optional_datetime(asset_updated_at)

    positions: list[Any] | None = None
    positions_error = ""
    if "positions" in required:
        try:
            positions = list(client.get_positions(account_id) or [])
        except Exception as exc:  # noqa: BLE001 - report and keep polling.
            positions_error = str(exc)
    positions_latest_updated_at = latest_model_datetime(positions or [], fields=("updated_at", "captured_at"))
    positions_count = len(positions or [])
    if "positions" in required:
        if positions_count == 0 and asset_fresh:
            positions_fresh = True
        else:
            positions_fresh = positions_latest_updated_at is not None and positions_latest_updated_at >= cutoff
    else:
        positions_fresh = True
    if positions_error:
        report["positions_error"] = positions_error
    report["positions_fresh"] = positions_fresh
    report["positions_count"] = positions_count
    report["positions_latest_updated_at"] = format_optional_datetime(positions_latest_updated_at)
    report["ok"] = asset_fresh and positions_fresh and not asset_error and not positions_error
    return report


def resolve_trading_day(options: JobOptions, requested_date: str) -> TradingDayInfo:
    if options.skip_trading_day_check:
        return TradingDayInfo(
            requested_date=requested_date,
            target_trade_date=requested_date,
            is_trading_day=True,
            source="skip_trading_day_check",
            raw={},
        )
    query = parse.urlencode({"date": requested_date})
    url = f"{options.meridian_base_url.rstrip('/')}/v1/metadata/trading-day?{query}"
    opener = request.build_opener(request.ProxyHandler({}))
    with opener.open(url, timeout=options.timeout) as response:
        payload = json.loads(response.read().decode("utf-8"))
    data = payload.get("data") if isinstance(payload, Mapping) else None
    if not isinstance(data, Mapping):
        raise RuntimeError("Meridian trading-day response missing data")
    target = normalize_trade_date(str(data.get("previous_or_current_trading_date", "")))
    if not target:
        raise RuntimeError("Meridian trading-day response missing previous_or_current_trading_date")
    explicit_is_trading_day = data.get("is_trading_day")
    is_trading_day = explicit_is_trading_day if isinstance(explicit_is_trading_day, bool) else target == requested_date
    return TradingDayInfo(
        requested_date=requested_date,
        target_trade_date=target,
        is_trading_day=is_trading_day,
        source=url,
        raw=payload,
    )


def select_accounts(accounts: Iterable[Any], requested: tuple[str, ...]) -> list[str]:
    requested_set = {item for item in requested if item}
    selected: list[str] = []
    for account in accounts:
        account_id = str(getattr(account, "account_id", "")).strip()
        if not account_id:
            continue
        if requested_set:
            if account_id in requested_set:
                selected.append(account_id)
            continue
        if bool(getattr(account, "enabled", True)):
            selected.append(account_id)
    return selected


def daily_job_status_error(status: Any) -> str:
    if not isinstance(status, Mapping):
        return "relay status response is invalid"
    status_name = str(status.get("status", "")).strip()
    if status_name == "ok":
        return ""
    if status_name != "degraded":
        return f"relay status is {status_name!r}"
    dependencies = status.get("dependencies")
    if not isinstance(dependencies, Mapping):
        return "relay status is 'degraded' and dependency details are unavailable"
    for name in REQUIRED_JOB_DEPENDENCIES:
        dependency = dependencies.get(name)
        if not isinstance(dependency, Mapping):
            return f"relay required dependency {name!r} is unavailable"
        dependency_status = str(dependency.get("status", "")).strip()
        if dependency_status != "ok":
            return f"relay required dependency {name!r} is {dependency_status!r}"
    return ""


def capture_call(
    name: str,
    func: Callable[..., Any],
    *args: Any,
    include_result: bool = True,
    **kwargs: Any,
) -> tuple[Any, dict[str, Any]]:
    try:
        result = func(*args, **kwargs)
        report: dict[str, Any] = {"ok": True}
        if include_result:
            report["result"] = result_to_jsonable(result)
        return result, report
    except Exception as exc:  # noqa: BLE001 - jobs must report and continue per account.
        return None, {"ok": False, "error": f"{name}: {exc}"}


def result_to_jsonable(value: Any) -> Any:
    if isinstance(value, (str, int, float, bool)) or value is None:
        return value
    if isinstance(value, Mapping):
        return {str(key): result_to_jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [result_to_jsonable(item) for item in value]
    raw = getattr(value, "raw", None)
    if isinstance(raw, Mapping):
        return result_to_jsonable(raw)
    return model_summary(value)


def model_summary(value: Any, fields: tuple[str, ...] = ()) -> dict[str, Any]:
    if value is None:
        return {}
    names = fields or tuple(name for name in dir(value) if not name.startswith("_"))
    summary: dict[str, Any] = {}
    for name in names:
        try:
            item = getattr(value, name)
        except Exception:  # noqa: BLE001 - ignore unusual model properties.
            continue
        if callable(item):
            continue
        if isinstance(item, (str, int, float, bool)) or item is None:
            summary[name] = item
    return summary


def latest_model_datetime(values: Iterable[Any], *, fields: tuple[str, ...]) -> datetime | None:
    latest: datetime | None = None
    for value in values:
        item = model_datetime(value, fields=fields)
        if item is not None and (latest is None or item > latest):
            latest = item
    return latest


def model_datetime(value: Any, *, fields: tuple[str, ...]) -> datetime | None:
    if value is None:
        return None
    for field in fields:
        item = getattr(value, field, None)
        parsed = parse_business_datetime(item)
        if parsed is not None:
            return parsed
    raw = getattr(value, "raw", None)
    if isinstance(raw, Mapping):
        for field in fields:
            parsed = parse_business_datetime(raw.get(field))
            if parsed is not None:
                return parsed
    return None


def parse_business_datetime(value: Any) -> datetime | None:
    if value in ("", None):
        return None
    if isinstance(value, datetime):
        parsed = value
    elif isinstance(value, str):
        text = value.strip()
        if not text:
            return None
        if text.endswith("Z"):
            text = f"{text[:-1]}+00:00"
        try:
            parsed = datetime.fromisoformat(text)
        except ValueError:
            return None
    else:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=BUSINESS_TZ)
    return parsed.astimezone(BUSINESS_TZ)


def format_optional_datetime(value: datetime | None) -> str:
    return value.isoformat() if value is not None else ""


def normalize_trade_date(value: str) -> str:
    return "".join(ch for ch in str(value).strip() if ch.isdigit())[:8]


def today_trade_date() -> str:
    return datetime.now(BUSINESS_TZ).strftime("%Y%m%d")


def now_iso() -> str:
    return datetime.now(BUSINESS_TZ).isoformat()


def finish_report(report: dict[str, Any]) -> dict[str, Any]:
    report["finished_at"] = now_iso()
    return report


def emit_report(report: Mapping[str, Any], options: JobOptions) -> None:
    text = json.dumps(report, ensure_ascii=False, indent=(options.indent or None), sort_keys=True)
    if options.output:
        output = Path(options.output)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(text + "\n", encoding="utf-8")
    print(text)


def main_for(job_name: str, description: str, runner: Callable[[JobOptions], Mapping[str, Any]]) -> None:
    options = parse_args(job_name, description)
    try:
        report = dict(runner(options))
    except Exception as exc:  # noqa: BLE001 - top-level job report.
        report = finish_report(
            {
                "ok": False,
                "job": job_name,
                "timezone": TIMEZONE_NAME,
                "base_url": options.base_url,
                "started_at": now_iso(),
                "finished_at": "",
                "errors": [str(exc)],
            }
        )
    report["trigger"] = options.trigger
    persisted_run_id = ""
    if options.persist:
        trading_day_report = report.get("trading_day")
        target_trade_date = ""
        if isinstance(trading_day_report, Mapping):
            target_trade_date = normalize_trade_date(str(trading_day_report.get("target_trade_date", "")))
        if not target_trade_date:
            target_trade_date = options.target_date or normalize_trade_date(str(report.get("started_at", "")))
        persisted_value, persistence = capture_call(
            "record_job_run",
            RelayClient(options.base_url, timeout=options.timeout, trust_env=False).record_job_run,
            report,
            job_name=job_name,
            trigger=options.trigger,
            target_trade_date=target_trade_date,
            include_result=False,
        )
        if isinstance(persisted_value, Mapping):
            persisted_run_id = str(persisted_value.get("run_id") or "")
            if persisted_run_id:
                persistence["run_id"] = persisted_run_id
        report["persistence"] = persistence
        if persistence.get("error"):
            report["ok"] = False
            report.setdefault("errors", []).append(persistence["error"])
    report["alert_delivery"] = dispatch_daily_job_alert(report)
    if options.persist and persisted_run_id:
        _value, final_persistence = capture_call(
            "update_job_run_alert_delivery",
            RelayClient(options.base_url, timeout=options.timeout, trust_env=False).record_job_run,
            report,
            job_name=job_name,
            trigger=options.trigger,
            run_id=persisted_run_id,
            target_trade_date=target_trade_date,
            include_result=False,
        )
        report["persistence"]["final_report_saved"] = not bool(final_persistence.get("error"))
        if final_persistence.get("error"):
            report.setdefault("warnings", []).append(final_persistence["error"])
    emit_report(report, options)
    raise SystemExit(0 if report.get("ok") else 1)


def _split_account_ids(values: Iterable[str] | str) -> list[str]:
    if isinstance(values, str):
        values = [values]
    output: list[str] = []
    for value in values:
        for item in str(value).split(","):
            item = item.strip()
            if item:
                output.append(item)
    return output
