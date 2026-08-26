"""Rebuild provisional performance after Meridian canonical daily bars are ready."""

from __future__ import annotations

import json
import math
import os
import subprocess
from dataclasses import replace
from pathlib import Path
from typing import Any, Callable, Mapping
from urllib import request

from .common import (
    TIMEZONE_NAME,
    JobOptions,
    TradingDayInfo,
    capture_call,
    finish_report,
    main_for,
    normalize_trade_date,
    now_iso,
    resolve_trading_day,
    result_to_jsonable,
    run_daily_performance,
    select_accounts,
    today_trade_date,
)

try:
    from relay_sdk import RelayClient
except ModuleNotFoundError:  # pragma: no cover - same repo-local fallback as common.py.
    import sys

    REPO_ROOT = Path(__file__).resolve().parents[3]
    sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))
    from relay_sdk import RelayClient


JOB_NAME = "performance_canonical"
WATERMARK_SCHEMA = "postclose_reference_watermark.v1"
LEVEL1_FALLBACK_FLAG = "meridian_level1_close_fallback"
DAILY_UNAVAILABLE_FLAG = "meridian_daily_bars_unavailable"
CANONICAL_PRICE_SOURCE = "meridian_1d_pre_close_and_close"
REQUIRED_DAILY_DATASETS = (
    "daily_bar_stock_none",
    "daily_bar_etf_none",
    "daily_bar_index_none",
)
DEFAULT_DELTA_WARNING_CNY = 50.0
DEFAULT_DELTA_WARNING_BP = 0.1


def run_canonical_performance(
    options: JobOptions,
    *,
    client: Any | None = None,
    trading_day: TradingDayInfo | None = None,
    watermark_loader: Callable[[str, float], Mapping[str, Any]] | None = None,
    relayctl_builder: Callable[[], Path] | None = None,
    command_runner: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
    quality_runner: Callable[..., dict[str, Any]] = run_daily_performance,
) -> dict[str, Any]:
    relay_client = client or RelayClient(options.base_url, timeout=options.timeout, trust_env=False)
    requested_date = options.target_date or today_trade_date()
    report: dict[str, Any] = {
        "ok": True,
        "job": JOB_NAME,
        "timezone": TIMEZONE_NAME,
        "base_url": options.base_url,
        "started_at": now_iso(),
        "finished_at": "",
        "skipped": False,
        "errors": [],
        "canonical_completed": False,
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
            report["ok"] = False
            report["errors"].append(
                trading_day_report.get("error", "resolve_trading_day: invalid result")
            )
            report["trading_day"] = {
                "requested_date": requested_date,
                "target_trade_date": requested_date,
                "is_trading_day": None,
                "source": "unavailable",
                "raw": {},
            }
            return finish_report(report)
        trading_day = trading_day_value
    report["trading_day"] = trading_day.to_dict()
    target_trade_date = normalize_trade_date(trading_day.target_trade_date)
    if not trading_day.is_trading_day and not options.allow_non_trading_day:
        report["skipped"] = True
        report["canonical_completed"] = True
        report["skip_reason"] = "target date is not an A-share trading day"
        return finish_report(report)

    existing_runs, existing_runs_report = capture_call(
        "list_canonical_job_runs",
        relay_client.list_job_runs,
        job_names=[JOB_NAME],
        trade_date=target_trade_date,
        limit=20,
        include_result=False,
    )
    report["previous_run_query"] = existing_runs_report
    completed_run = matching_completed_run(existing_runs, target_trade_date)
    if completed_run:
        report["skipped"] = True
        report["canonical_completed"] = True
        report["skip_reason"] = "canonical performance already completed for this watermark"
        report["completed_run_id"] = str(completed_run.get("run_id") or "")
        return finish_report(report)

    loader = watermark_loader or load_meridian_watermark
    watermark_value, watermark_report = capture_call(
        "load_meridian_canonical_watermark",
        loader,
        options.meridian_base_url,
        options.timeout,
        include_result=False,
    )
    if watermark_report.get("error") or not isinstance(watermark_value, Mapping):
        report["ok"] = False
        report["errors"].append(
            watermark_report.get("error", "Meridian canonical watermark response is invalid")
        )
        report["meridian_watermark"] = {"ready": False, "reasons": ["watermark_unavailable"]}
        return finish_report(report)
    watermark = evaluate_canonical_watermark(watermark_value, target_trade_date)
    report["meridian_watermark"] = watermark
    if not watermark["ready"]:
        report["skipped"] = True
        report["waiting_for_meridian"] = True
        report["skip_reason"] = "Meridian canonical daily watermark has not reached the target trade date"
        return finish_report(report)

    accounts_value, accounts_report = capture_call(
        "list_accounts",
        relay_client.list_accounts,
        include_result=False,
    )
    report["accounts_query"] = accounts_report
    if accounts_report.get("error"):
        report["ok"] = False
        report["errors"].append(accounts_report["error"])
        return finish_report(report)
    account_ids = select_accounts(accounts_value or [], options.account_ids)
    if not account_ids:
        report["ok"] = False
        report["errors"].append("no enabled performance accounts selected")
        return finish_report(report)

    previous_navs: dict[str, Mapping[str, Any]] = {}
    candidate_accounts: list[str] = []
    nav_query_errors: list[str] = []
    for account_id in account_ids:
        navs_value, navs_report = capture_call(
            "list_economic_nav",
            relay_client.list_economic_nav,
            account_id=account_id,
            trade_date=target_trade_date,
            include_result=False,
        )
        if navs_report.get("error"):
            nav_query_errors.append(f"{account_id}: {navs_report['error']}")
            continue
        current = current_nav(navs_value, target_trade_date)
        if current:
            previous_navs[account_id] = current
        if not current or not is_canonical_nav(current):
            candidate_accounts.append(account_id)
    if nav_query_errors:
        report["ok"] = False
        report["errors"].extend(nav_query_errors)
        return finish_report(report)

    report["selected_account_ids"] = account_ids
    report["rebuild_account_ids"] = candidate_accounts
    report["already_canonical_account_ids"] = [
        account_id for account_id in account_ids if account_id not in candidate_accounts
    ]
    if candidate_accounts:
        try:
            relayctl = (relayctl_builder or build_relayctl)()
            rebuild_report = run_relayctl_rebuild(
                relayctl,
                target_trade_date,
                candidate_accounts,
                command_runner=command_runner,
            )
        except Exception as exc:  # noqa: BLE001 - retain a structured cron failure.
            report["ok"] = False
            report["errors"].append(f"canonical performance rebuild: {exc}")
            return finish_report(report)
        report["rebuild"] = summarize_rebuild_report(rebuild_report)

    quality_options = replace(
        options,
        job_name="performance_daily",
        account_ids=tuple(account_ids),
        target_date=target_trade_date,
        persist=False,
        trigger="meridian_canonical_ready",
        skip_refresh=True,
    )
    quality_report = quality_runner(
        quality_options,
        client=relay_client,
        trading_day=trading_day,
    )
    quality_report["trigger"] = "meridian_canonical_ready"
    report["performance_quality"] = performance_quality_summary(quality_report)
    report["performance_summary"] = result_to_jsonable(quality_report.get("performance_summary", {}))
    report["performance_ready_accounts"] = list(quality_report.get("performance_ready_accounts", []))
    report["performance_attention_accounts"] = list(quality_report.get("performance_attention_accounts", []))
    report["performance_blocked_accounts"] = list(quality_report.get("performance_blocked_accounts", []))
    report["performance_not_applicable_accounts"] = list(
        quality_report.get("performance_not_applicable_accounts", [])
    )
    report["performance_published_accounts"] = list(
        quality_report.get("performance_published_accounts", [])
    )
    report["performance_preview_only_accounts"] = list(
        quality_report.get("performance_preview_only_accounts", [])
    )

    if options.persist:
        quality_value, quality_persistence = capture_call(
            "record_canonical_performance_daily",
            relay_client.record_job_run,
            quality_report,
            job_name="performance_daily",
            trigger="meridian_canonical_ready",
            target_trade_date=target_trade_date,
            include_result=False,
        )
        if isinstance(quality_value, Mapping):
            quality_persistence["run_id"] = str(quality_value.get("run_id") or "")
        report["performance_daily_persistence"] = quality_persistence
        if quality_persistence.get("error"):
            report["errors"].append(quality_persistence["error"])

    comparisons: list[dict[str, Any]] = []
    canonical_source_errors: list[str] = []
    for account_id in account_ids:
        navs_value, navs_report = capture_call(
            "list_economic_nav",
            relay_client.list_economic_nav,
            account_id=account_id,
            trade_date=target_trade_date,
            include_result=False,
        )
        if navs_report.get("error"):
            canonical_source_errors.append(f"{account_id}: {navs_report['error']}")
            continue
        current = current_nav(navs_value, target_trade_date)
        if account_id in report["performance_published_accounts"] and not is_canonical_nav(current):
            canonical_source_errors.append(f"{account_id}: published NAV is not based on canonical daily bars")
        previous = previous_navs.get(account_id)
        if previous and current:
            comparisons.append(compare_nav_versions(account_id, previous, current))
    report["nav_version_comparisons"] = comparisons
    report["comparison_summary"] = summarize_comparisons(comparisons)

    blocked_accounts = report["performance_blocked_accounts"]
    preview_only_accounts = report["performance_preview_only_accounts"]
    if not quality_report.get("ok", False):
        report["errors"].append("canonical performance quality task failed")
    if blocked_accounts:
        report["errors"].append(
            "canonical performance has blocked accounts: " + ",".join(blocked_accounts)
        )
    if preview_only_accounts:
        report["errors"].append(
            "canonical performance was not published for accounts: " + ",".join(preview_only_accounts)
        )
    report["errors"].extend(canonical_source_errors)
    report["ok"] = not report["errors"]
    report["canonical_completed"] = report["ok"]

    comparison_summary = report["comparison_summary"]
    if comparison_summary["warning_accounts"]:
        report.setdefault("warnings", []).append(
            "canonical daily bars changed NAV beyond the configured comparison tolerance"
        )
    return finish_report(report)


def load_meridian_watermark(base_url: str, timeout: float) -> Mapping[str, Any]:
    url = f"{base_url.rstrip('/')}/v1/quality/postclose-reference"
    opener = request.build_opener(request.ProxyHandler({}))
    with opener.open(url, timeout=timeout) as response:
        payload = json.loads(response.read().decode("utf-8"))
    if not isinstance(payload, Mapping):
        raise RuntimeError("Meridian watermark response is not an object")
    return payload


def evaluate_canonical_watermark(payload: Mapping[str, Any], target_trade_date: str) -> dict[str, Any]:
    target = int(normalize_trade_date(target_trade_date) or 0)
    meta = payload.get("meta") if isinstance(payload.get("meta"), Mapping) else {}
    data = payload.get("data") if isinstance(payload.get("data"), Mapping) else {}
    datasets = data.get("datasets") if isinstance(data.get("datasets"), list) else []
    by_name = {
        str(item.get("name") or ""): item
        for item in datasets
        if isinstance(item, Mapping)
    }
    reasons: list[str] = []
    if str(meta.get("schema_version") or "") != WATERMARK_SCHEMA:
        reasons.append("unexpected_watermark_schema")
    if int_value(data.get("target_trade_date")) != target:
        reasons.append("target_trade_date_not_reached")
    if str(data.get("status") or "") != "ready":
        reasons.append("watermark_status_not_ready")
    required: list[dict[str, Any]] = []
    for name in REQUIRED_DAILY_DATASETS:
        item = by_name.get(name, {})
        status = str(item.get("status") or "")
        published = int_value(item.get("published_watermark"))
        required.append({"name": name, "status": status or "missing", "published_watermark": published})
        if status != "ready" or published < target:
            reasons.append(f"{name}_not_ready")
    pipeline = data.get("pipeline") if isinstance(data.get("pipeline"), Mapping) else {}
    return {
        "ready": not reasons,
        "schema_version": str(meta.get("schema_version") or ""),
        "target_trade_date": int_value(data.get("target_trade_date")),
        "requested_trade_date": target,
        "status": str(data.get("status") or ""),
        "generated_at": str(data.get("generated_at") or ""),
        "pipeline": {
            "status": str(pipeline.get("status") or ""),
            "target_trade_date": int_value(pipeline.get("target_trade_date")),
            "started_at": str(pipeline.get("started_at") or ""),
            "finished_at": str(pipeline.get("finished_at") or ""),
            "error": str(pipeline.get("error") or ""),
        },
        "required_datasets": required,
        "reasons": reasons,
    }


def matching_completed_run(runs: Any, target_trade_date: str) -> Mapping[str, Any] | None:
    if not isinstance(runs, list):
        return None
    target = normalize_trade_date(target_trade_date)
    for run in runs:
        if not isinstance(run, Mapping):
            continue
        report = run.get("report") if isinstance(run.get("report"), Mapping) else {}
        if (
            normalize_trade_date(str(run.get("target_trade_date") or "")) == target
            and str(run.get("status") or "") in {"succeeded", "completed"}
            and report.get("canonical_completed") is True
        ):
            return run
    return None


def current_nav(values: Any, target_trade_date: str) -> Mapping[str, Any]:
    if not isinstance(values, list):
        return {}
    target = normalize_trade_date(target_trade_date)
    for value in values:
        if isinstance(value, Mapping) and normalize_trade_date(str(value.get("trade_date") or "")) == target:
            return value
    return {}


def nav_price_source(nav: Mapping[str, Any]) -> str:
    components = nav.get("pnl_components") if isinstance(nav.get("pnl_components"), Mapping) else {}
    valuation = components.get("market_valuation") if isinstance(components.get("market_valuation"), Mapping) else {}
    return str(valuation.get("price_source") or "")


def is_canonical_nav(nav: Mapping[str, Any]) -> bool:
    flags = {str(flag) for flag in nav.get("quality_flags", [])}
    return (
        bool(nav)
        and nav_price_source(nav) == CANONICAL_PRICE_SOURCE
        and LEVEL1_FALLBACK_FLAG not in flags
        and DAILY_UNAVAILABLE_FLAG not in flags
        and str(nav.get("status") or "") != "blocked"
    )


def build_relayctl() -> Path:
    root = Path(__file__).resolve().parents[3]
    configured = os.getenv("RELAYCTL_BIN", "").strip()
    target = Path(configured) if configured else root / ".runtime" / "bin" / "relayctl"
    source_roots = (root / "cmd" / "relayctl", root / "internal")
    source_files = [root / "go.mod", root / "go.sum"]
    for source_root in source_roots:
        source_files.extend(path for path in source_root.rglob("*.go") if path.is_file())
    newest_source = max((path.stat().st_mtime for path in source_files if path.exists()), default=0.0)
    if target.is_file() and target.stat().st_mtime >= newest_source:
        return target
    target.parent.mkdir(parents=True, exist_ok=True)
    candidate = target.with_name(f"{target.name}.build.{os.getpid()}")
    try:
        completed = subprocess.run(
            ["go", "build", "-o", str(candidate), "./cmd/relayctl"],
            cwd=root,
            check=False,
            capture_output=True,
            text=True,
        )
        if completed.returncode != 0:
            raise RuntimeError(completed.stderr.strip() or "go build relayctl failed")
        candidate.replace(target)
    finally:
        candidate.unlink(missing_ok=True)
    return target


def run_relayctl_rebuild(
    relayctl: Path,
    target_trade_date: str,
    account_ids: list[str],
    *,
    command_runner: Callable[..., subprocess.CompletedProcess[str]],
) -> Mapping[str, Any]:
    root = Path(__file__).resolve().parents[3]
    config_path = os.getenv("RELAY_CONFIG_PATH", "").strip() or str(root / "config" / "relay.prod.yaml")
    command = [
        str(relayctl),
        "performance-rebuild",
        "-config",
        config_path,
        "-accounts",
        ",".join(account_ids),
        "-date-from",
        target_trade_date,
        "-date-to",
        target_trade_date,
        "-persist",
        "-timeout",
        "10m",
    ]
    completed = command_runner(
        command,
        cwd=root,
        check=False,
        capture_output=True,
        text=True,
    )
    if completed.returncode != 0:
        raise RuntimeError(completed.stderr.strip() or f"relayctl exited {completed.returncode}")
    try:
        payload = json.loads(completed.stdout)
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"relayctl returned invalid JSON: {exc}") from exc
    if not isinstance(payload, Mapping):
        raise RuntimeError("relayctl rebuild report is not an object")
    return payload


def summarize_rebuild_report(report: Mapping[str, Any]) -> dict[str, Any]:
    items = report.get("items") if isinstance(report.get("items"), list) else []
    summaries: list[dict[str, Any]] = []
    for item in items:
        if not isinstance(item, Mapping):
            continue
        cost = item.get("cost_ledger") if isinstance(item.get("cost_ledger"), Mapping) else {}
        nav = item.get("economic_nav") if isinstance(item.get("economic_nav"), Mapping) else {}
        nav_record = nav.get("nav") if isinstance(nav.get("nav"), Mapping) else {}
        summaries.append(
            {
                "account_id": str(item.get("account_id") or ""),
                "trade_date": str(item.get("trade_date") or ""),
                "cost_status": str(cost.get("status") or ""),
                "nav_status": str(nav.get("status") or ""),
                "persisted": bool(nav.get("persisted")),
                "nav_version": int_value(nav_record.get("version")),
                "nav_pk": int_value(nav_record.get("performance_nav_pk")),
                "price_source": str(
                    (nav.get("valuation") or {}).get("price_source")
                    if isinstance(nav.get("valuation"), Mapping)
                    else ""
                ),
                "error": str(item.get("error") or ""),
            }
        )
    return {
        "date_from": str(report.get("date_from") or ""),
        "date_to": str(report.get("date_to") or ""),
        "persist": bool(report.get("persist")),
        "items": summaries,
    }


def performance_quality_summary(report: Mapping[str, Any]) -> dict[str, Any]:
    return {
        "ok": bool(report.get("ok")),
        "finished_at": str(report.get("finished_at") or ""),
        "summary": result_to_jsonable(report.get("performance_summary", {})),
        "ready_accounts": list(report.get("performance_ready_accounts", [])),
        "attention_accounts": list(report.get("performance_attention_accounts", [])),
        "blocked_accounts": list(report.get("performance_blocked_accounts", [])),
        "not_applicable_accounts": list(report.get("performance_not_applicable_accounts", [])),
        "published_accounts": list(report.get("performance_published_accounts", [])),
        "preview_only_accounts": list(report.get("performance_preview_only_accounts", [])),
        "warnings": list(report.get("warnings", [])),
        "errors": list(report.get("errors", [])),
    }


def compare_nav_versions(
    account_id: str,
    previous: Mapping[str, Any],
    canonical: Mapping[str, Any],
) -> dict[str, Any]:
    previous_close = float_value(previous.get("close_economic_nav"))
    canonical_close = float_value(canonical.get("close_economic_nav"))
    close_delta = canonical_close - previous_close
    pnl_delta = float_value(canonical.get("account_day_pnl")) - float_value(previous.get("account_day_pnl"))
    return_delta = float_value(canonical.get("daily_return")) - float_value(previous.get("daily_return"))
    warning_cny = env_float("RELAY_CANONICAL_NAV_DELTA_WARNING_CNY", DEFAULT_DELTA_WARNING_CNY)
    warning_bp = env_float("RELAY_CANONICAL_NAV_DELTA_WARNING_BP", DEFAULT_DELTA_WARNING_BP)
    threshold = max(warning_cny, abs(previous_close) * warning_bp / 10000)
    return {
        "account_id": account_id,
        "previous": nav_audit_summary(previous),
        "canonical": nav_audit_summary(canonical),
        "close_economic_nav_delta": round(close_delta, 6),
        "account_day_pnl_delta": round(pnl_delta, 6),
        "daily_return_delta": round(return_delta, 12),
        "daily_return_delta_bp": round(return_delta * 10000, 6),
        "warning_threshold_cny": round(threshold, 6),
        "exceeds_warning": abs(close_delta) > threshold,
    }


def nav_audit_summary(nav: Mapping[str, Any]) -> dict[str, Any]:
    return {
        "performance_nav_pk": int_value(nav.get("performance_nav_pk")),
        "version": int_value(nav.get("version")),
        "status": str(nav.get("status") or ""),
        "source": str(nav.get("source") or ""),
        "price_source": nav_price_source(nav),
        "close_economic_nav": float_value(nav.get("close_economic_nav")),
        "account_day_pnl": float_value(nav.get("account_day_pnl")),
        "daily_return": float_value(nav.get("daily_return")),
        "quality_flags": [str(flag) for flag in nav.get("quality_flags", [])],
        "created_at": str(nav.get("created_at") or ""),
        "updated_at": str(nav.get("updated_at") or ""),
    }


def summarize_comparisons(comparisons: list[Mapping[str, Any]]) -> dict[str, Any]:
    warning_accounts = [
        str(item.get("account_id") or "")
        for item in comparisons
        if item.get("exceeds_warning")
    ]
    return {
        "compared_accounts": len(comparisons),
        "changed_accounts": sum(
            1 for item in comparisons if abs(float_value(item.get("close_economic_nav_delta"))) > 0.000001
        ),
        "warning_accounts": warning_accounts,
        "max_abs_close_economic_nav_delta": round(
            max((abs(float_value(item.get("close_economic_nav_delta"))) for item in comparisons), default=0.0),
            6,
        ),
        "max_abs_daily_return_delta_bp": round(
            max((abs(float_value(item.get("daily_return_delta_bp"))) for item in comparisons), default=0.0),
            6,
        ),
    }


def int_value(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def float_value(value: Any) -> float:
    try:
        result = float(value or 0)
    except (TypeError, ValueError):
        return 0.0
    return result if math.isfinite(result) else 0.0


def env_float(name: str, default: float) -> float:
    value = float_value(os.getenv(name, default))
    return value if value >= 0 else default


def main() -> None:
    main_for(
        JOB_NAME,
        "Rebuild daily performance after Meridian canonical daily-bar watermarks reach the trade date.",
        run_canonical_performance,
    )


if __name__ == "__main__":
    main()
