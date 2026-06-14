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


TIMEZONE_NAME = "Asia/Shanghai"
DEFAULT_BASE_URL = "http://relay-trader.quantstage.com"
DEFAULT_MERIDIAN_BASE_URL = "http://meridian-data.quantstage.com"
DEFAULT_QUERY_LIMIT = 500


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
    refresh_wait_seconds: float = 1.0
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
    parser.add_argument("--refresh-wait-seconds", type=float, default=1.0, help="seconds to wait after publishing refresh commands")
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
        refresh_wait_seconds=max(args.refresh_wait_seconds, 0.0),
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
    )


def run_post_close_settlement(options: JobOptions, *, client: Any | None = None, trading_day: TradingDayInfo | None = None) -> dict[str, Any]:
    return run_daily_job(
        options,
        client=client,
        trading_day=trading_day,
        phase="post_close_settlement",
        refresh_steps=("orders", "fills", "asset", "positions"),
        check_non_terminal_orders=True,
        settle_snapshots=True,
    )


def run_daily_job(
    options: JobOptions,
    *,
    client: Any | None,
    trading_day: TradingDayInfo | None,
    phase: str,
    refresh_steps: tuple[str, ...],
    check_non_terminal_orders: bool,
    settle_snapshots: bool = False,
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

    status_value, status_report = capture_call("status", relay_client.status)
    report["status"] = status_report
    if status_report.get("error"):
        report["ok"] = False
        report["errors"].append(status_report["error"])
        return finish_report(report)
    if not isinstance(status_value, Mapping) or status_value.get("status") != "ok":
        report["ok"] = False
        report["errors"].append(f"relay status is {getattr(status_value, 'get', lambda _name, _default=None: None)('status')!r}")
        return finish_report(report)

    if trading_day is None:
        trading_day = resolve_trading_day(options, requested_date)
    report["trading_day"] = trading_day.to_dict()
    if not trading_day.is_trading_day and not options.allow_non_trading_day:
        report["skipped"] = True
        report["skip_reason"] = "target date is not an A-share trading day"
        return finish_report(report)

    accounts_value, accounts_report = capture_call("list_accounts", relay_client.list_accounts)
    report["accounts_query"] = accounts_report
    if accounts_report.get("error"):
        report["ok"] = False
        report["errors"].append(accounts_report["error"])
        return finish_report(report)

    accounts = select_accounts(accounts_value or [], options.account_ids)
    report["accounts"] = []
    for account_id in accounts:
        account_report = run_account_flow(
            relay_client,
            account_id,
            options=options,
            trade_date=trading_day.target_trade_date,
            refresh_steps=refresh_steps,
            check_non_terminal_orders=check_non_terminal_orders,
        )
        report["accounts"].append(account_report)
        if account_report.get("errors"):
            report["ok"] = False

    if settle_snapshots and accounts:
        settlement_run_id = f"{phase}-{trading_day.target_trade_date}"
        report["settlement_run_id"] = settlement_run_id
        settlement_value, settlement_report = capture_call(
            "record_settlement_snapshot",
            relay_client.record_settlement_snapshot,
            trade_date=trading_day.target_trade_date,
            account_ids=accounts,
            run_id=settlement_run_id,
            snapshot_type="close",
            source=phase,
            dry_run=options.dry_run,
        )
        report["settlement_snapshot"] = settlement_report
        if settlement_report.get("error"):
            report["ok"] = False
            report["errors"].append(settlement_report["error"])
        elif isinstance(settlement_value, Mapping) and settlement_value.get("status") == "failed":
            report["ok"] = False
            errors = settlement_value.get("errors")
            if errors:
                report["errors"].append(f"settlement snapshot failed: {errors}")

    return finish_report(report)


def run_account_flow(
    client: Any,
    account_id: str,
    *,
    options: JobOptions,
    trade_date: str,
    refresh_steps: tuple[str, ...],
    check_non_terminal_orders: bool,
) -> dict[str, Any]:
    account_report: dict[str, Any] = {
        "account_id": account_id,
        "refresh": [],
        "snapshot": {},
        "errors": [],
    }
    if not options.dry_run and not options.skip_refresh:
        for step in refresh_steps:
            _value, result = capture_call(f"refresh_{step}", getattr(client, f"refresh_{step}"), account_id)
            account_report["refresh"].append({"step": step, **result})
            if result.get("error"):
                account_report["errors"].append(result["error"])
        if options.refresh_wait_seconds > 0:
            time.sleep(options.refresh_wait_seconds)

    asset_value, asset_report = capture_call("get_asset", client.get_asset, account_id, include_result=False)
    positions_value, positions_report = capture_call("get_positions", client.get_positions, account_id, include_result=False)
    orders_value, orders_report = capture_call(
        "list_orders",
        client.list_orders,
        account_id=account_id,
        trade_date=trade_date,
        history=True,
        limit=options.query_limit,
        include_result=False,
    )
    fills_value, fills_report = capture_call(
        "list_fills",
        client.list_fills,
        account_id=account_id,
        trade_date=trade_date,
        history=True,
        limit=options.query_limit,
        include_result=False,
    )
    snapshot_reports = {
        "asset": asset_report,
        "positions": positions_report,
        "orders": orders_report,
        "fills": fills_report,
    }
    snapshot_values = {
        "asset": asset_value,
        "positions": positions_value,
        "orders": orders_value,
        "fills": fills_value,
    }
    account_report["queries"] = snapshot_reports
    account_report["snapshot"] = summarize_snapshot(snapshot_values, check_non_terminal_orders=check_non_terminal_orders)
    for result in snapshot_reports.values():
        if result.get("error"):
            account_report["errors"].append(result["error"])
    return account_report


def summarize_snapshot(snapshot: Mapping[str, Any], *, check_non_terminal_orders: bool) -> dict[str, Any]:
    asset = snapshot.get("asset")
    positions = snapshot.get("positions") or []
    orders = snapshot.get("orders") or []
    fills = snapshot.get("fills") or []
    non_terminal_orders = [order for order in orders if not bool(getattr(order, "is_terminal", False))]
    summary = {
        "asset": model_summary(asset, fields=("account_id", "net_asset", "cash_available", "market_value")),
        "positions_count": len(positions),
        "orders_count": len(orders),
        "fills_count": len(fills),
        "non_terminal_orders": len(non_terminal_orders),
    }
    if check_non_terminal_orders and non_terminal_orders:
        summary["non_terminal_order_ids"] = [
            str(getattr(order, "gateway_order_id", "")) for order in non_terminal_orders[:20]
        ]
    return summary


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
    with request.urlopen(url, timeout=options.timeout) as response:
        payload = json.loads(response.read().decode("utf-8"))
    data = payload.get("data") if isinstance(payload, Mapping) else None
    if not isinstance(data, Mapping):
        raise RuntimeError("Meridian trading-day response missing data")
    target = normalize_trade_date(str(data.get("previous_or_current_trading_date", "")))
    if not target:
        raise RuntimeError("Meridian trading-day response missing previous_or_current_trading_date")
    return TradingDayInfo(
        requested_date=requested_date,
        target_trade_date=target,
        is_trading_day=target == requested_date,
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
    if options.persist:
        _value, persistence = capture_call(
            "record_job_run",
            RelayClient(options.base_url, timeout=options.timeout, trust_env=False).record_job_run,
            report,
            job_name=job_name,
            trigger=options.trigger,
        )
        report["persistence"] = persistence
        if persistence.get("error"):
            report["ok"] = False
            report.setdefault("errors", []).append(persistence["error"])
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
