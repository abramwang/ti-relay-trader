"""External alert delivery for relay trading-day jobs."""

from __future__ import annotations

import hashlib
import json
import os
import time
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Callable, Mapping
from urllib import error, request


SCHEMA_VERSION = "relay.alert.v1"
DEFAULT_TIMEOUT_SECONDS = 5.0
DEFAULT_MAX_ATTEMPTS = 3
BUSINESS_TZ = timezone(timedelta(hours=8), "Asia/Shanghai")
REPO_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_CONFIG_PATH = REPO_ROOT / "config" / "relay.alerts.env"
SUPPORTED_ENV_KEYS = {
    "RELAY_ALERT_ENABLED",
    "RELAY_ALERT_ENVIRONMENT",
    "RELAY_ALERT_PUBLIC_URL",
    "RELAY_ALERT_WEBHOOK_URL",
    "RELAY_ALERT_WEBHOOK_TOKEN",
    "RELAY_ALERT_TIMEOUT_SECONDS",
    "RELAY_ALERT_MAX_ATTEMPTS",
}


@dataclass(frozen=True)
class AlertConfig:
    enabled: bool = False
    environment: str = "unknown"
    public_url: str = ""
    webhook_url: str = ""
    webhook_token: str = ""
    timeout_seconds: float = DEFAULT_TIMEOUT_SECONDS
    max_attempts: int = DEFAULT_MAX_ATTEMPTS
    config_path: str = ""

    @classmethod
    def load(
        cls,
        *,
        environ: Mapping[str, str] | None = None,
        config_path: str | Path | None = None,
    ) -> "AlertConfig":
        process_env = dict(os.environ if environ is None else environ)
        path_value = config_path or process_env.get("RELAY_ALERT_CONFIG_PATH") or DEFAULT_CONFIG_PATH
        path = Path(path_value).expanduser()
        values = load_env_file(path)
        values.update({key: value for key, value in process_env.items() if key in SUPPORTED_ENV_KEYS})
        return cls(
            enabled=parse_bool(values.get("RELAY_ALERT_ENABLED", "false")),
            environment=values.get("RELAY_ALERT_ENVIRONMENT", "unknown").strip() or "unknown",
            public_url=values.get("RELAY_ALERT_PUBLIC_URL", "").strip().rstrip("/"),
            webhook_url=values.get("RELAY_ALERT_WEBHOOK_URL", "").strip(),
            webhook_token=values.get("RELAY_ALERT_WEBHOOK_TOKEN", "").strip(),
            timeout_seconds=parse_positive_float(
                values.get("RELAY_ALERT_TIMEOUT_SECONDS"), DEFAULT_TIMEOUT_SECONDS
            ),
            max_attempts=parse_positive_int(values.get("RELAY_ALERT_MAX_ATTEMPTS"), DEFAULT_MAX_ATTEMPTS),
            config_path=str(path),
        )


def load_env_file(path: Path) -> dict[str, str]:
    if not path.is_file():
        return {}
    values: dict[str, str] = {}
    for line_number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        if "=" not in line:
            raise ValueError(f"invalid alert config at line {line_number}: expected KEY=VALUE")
        key, value = line.split("=", 1)
        key = key.strip()
        if key not in SUPPORTED_ENV_KEYS:
            raise ValueError(f"unsupported alert config key at line {line_number}: {key}")
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
            value = value[1:-1]
        values[key] = value
    return values


def dispatch_daily_job_alert(
    report: Mapping[str, Any],
    *,
    config: AlertConfig | None = None,
    opener: Any | None = None,
    sleep: Callable[[float], None] = time.sleep,
) -> dict[str, Any]:
    try:
        loaded_config = config or AlertConfig.load()
    except Exception as exc:  # noqa: BLE001 - alert config must never abort the business job.
        fallback_config = AlertConfig()
        alert, suppression_reason = build_daily_job_alert(report, fallback_config)
        return {
            "required": alert is not None,
            "configured": False,
            "status": "misconfigured",
            "reason": f"alert config: {sanitize_error(exc)}",
            **({"severity": alert["severity"], "issue_types": alert["issue_types"]} if alert else {}),
            **({"suppression_reason": suppression_reason} if not alert else {}),
        }
    alert, suppression_reason = build_daily_job_alert(report, loaded_config)
    configured = loaded_config.enabled and bool(loaded_config.webhook_url)
    if alert is None:
        status = "suppressed" if suppression_reason == "dry_run" else "not_required"
        return {
            "required": False,
            "configured": configured,
            "status": status,
            "reason": suppression_reason,
        }
    metadata: dict[str, Any] = {
        "required": True,
        "configured": configured,
        "severity": alert["severity"],
        "issue_types": list(alert["issue_types"]),
        "dedupe_key": alert["dedupe_key"],
    }
    if not loaded_config.enabled:
        return {**metadata, "status": "disabled", "reason": "RELAY_ALERT_ENABLED is false"}
    if not loaded_config.webhook_url:
        return {**metadata, "status": "misconfigured", "reason": "webhook URL is empty"}
    return {
        **metadata,
        **deliver_webhook(alert, loaded_config, opener=opener, sleep=sleep),
    }


def build_daily_job_alert(
    report: Mapping[str, Any],
    config: AlertConfig,
) -> tuple[dict[str, Any] | None, str]:
    if bool(report.get("dry_run")):
        return None, "dry_run"
    if bool(report.get("skipped")) and bool(report.get("ok", True)):
        return None, "non_trading_day_or_normal_skip"

    issue_types: list[str] = []
    if not bool(report.get("ok")):
        issue_types.append("task_failed")

    account_errors = normalized_account_errors(report.get("account_errors"))
    snapshot_account_errors, snapshot_result_blocked = snapshot_result_issues(report)
    account_errors = merge_account_errors(account_errors + snapshot_account_errors)
    if account_errors or int_value(report.get("account_error_count")) > 0:
        issue_types.append("account_errors")

    blocked_accounts = sorted(
        set(normalized_strings(report.get("snapshot_blocked_accounts"))) | set(snapshot_result_blocked)
    )
    if blocked_accounts:
        issue_types.append("snapshot_blocked")

    timeout_accounts = refresh_timeout_accounts(report.get("accounts"))
    if timeout_accounts:
        issue_types.append("refresh_timeout")

    performance_blocked_accounts = sorted(
        set(normalized_strings(report.get("performance_blocked_accounts")))
    )
    if performance_blocked_accounts:
        issue_types.append("performance_blocked")

    performance_attention_accounts = sorted(
        set(normalized_strings(report.get("performance_attention_accounts")))
    )
    if performance_attention_accounts:
        issue_types.append("performance_attention")

    if not issue_types:
        return None, "job_completed_without_alertable_issues"

    account_ids = sorted(
        set(blocked_accounts)
        | set(timeout_accounts)
        | set(performance_blocked_accounts)
        | set(performance_attention_accounts)
        | {item["account_id"] for item in account_errors if item.get("account_id")}
    )
    severity = (
        "critical"
        if any(
            issue_type in issue_types
            for issue_type in ("task_failed", "snapshot_blocked", "performance_blocked")
        )
        else "warning"
    )
    job_name = str(report.get("job") or "daily_job")
    trade_date = target_trade_date(report)
    environment = config.environment
    errors = collect_error_messages(report, account_errors)
    key_material = {
        "environment": environment,
        "job": job_name,
        "trade_date": trade_date,
        "issue_types": issue_types,
        "account_ids": account_ids,
    }
    digest = hashlib.sha256(
        json.dumps(key_material, ensure_ascii=True, sort_keys=True).encode("utf-8")
    ).hexdigest()[:16]
    dedupe_key = f"relay:{environment}:{job_name}:{trade_date}:{digest}"
    public_url = config.public_url or str(report.get("base_url") or "").rstrip("/")
    job_label = {
        "pre_open_init": "盘前初始化",
        "post_close_settlement": "盘后结算",
        "performance_daily": "每日绩效计算",
    }.get(job_name, job_name)
    account_text = f"，涉及 {len(account_ids)} 个账户" if account_ids else ""
    title = f"[Relay][{severity.upper()}] {job_label}异常"
    message = f"{trade_date or '未知交易日'} {job_label}出现 {', '.join(issue_types)}{account_text}。"
    alert = {
        "schema_version": SCHEMA_VERSION,
        "source": "relay.daily_job",
        "environment": environment,
        "severity": severity,
        "alert_type": "daily_job_anomaly",
        "issue_types": issue_types,
        "dedupe_key": dedupe_key,
        "title": title,
        "message": message,
        "occurred_at": str(report.get("finished_at") or now_iso()),
        "job": {
            "name": job_name,
            "trigger": str(report.get("trigger") or "unknown"),
            "trade_date": trade_date,
            "started_at": str(report.get("started_at") or ""),
            "finished_at": str(report.get("finished_at") or ""),
            "ok": bool(report.get("ok")),
        },
        "accounts": account_ids,
        "errors": errors[:20],
        "details": {
            "account_error_count": len(account_errors) or int_value(report.get("account_error_count")),
            "snapshot_blocked_accounts": blocked_accounts,
            "refresh_timeout_accounts": timeout_accounts,
            "performance_blocked_accounts": performance_blocked_accounts,
            "performance_attention_accounts": performance_attention_accounts,
        },
        "links": {"jobs": f"{public_url}/jobs"} if public_url else {},
    }
    return alert, ""


def deliver_webhook(
    payload: Mapping[str, Any],
    config: AlertConfig,
    *,
    opener: Any | None = None,
    sleep: Callable[[float], None] = time.sleep,
) -> dict[str, Any]:
    http = opener or request.build_opener(request.ProxyHandler({}))
    body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    headers = {
        "Content-Type": "application/json; charset=utf-8",
        "Accept": "application/json",
        "User-Agent": "relay-trader-alerts/1",
        "X-Relay-Alert-Schema": SCHEMA_VERSION,
        "Idempotency-Key": str(payload.get("dedupe_key") or ""),
    }
    if config.webhook_token:
        headers["Authorization"] = f"Bearer {config.webhook_token}"

    attempted_at = now_iso()
    started = time.monotonic()
    last_error = ""
    last_status_code = 0
    attempts = 0
    for attempt in range(1, config.max_attempts + 1):
        attempts = attempt
        webhook_request = request.Request(config.webhook_url, data=body, headers=headers, method="POST")
        try:
            with http.open(webhook_request, timeout=config.timeout_seconds) as response:
                response_status = getattr(response, "status", None)
                last_status_code = int(response_status if response_status is not None else response.getcode())
                response.read(4096)
            if 200 <= last_status_code < 300:
                return {
                    "status": "delivered",
                    "attempts": attempts,
                    "status_code": last_status_code,
                    "attempted_at": attempted_at,
                    "delivered_at": now_iso(),
                    "duration_ms": round((time.monotonic() - started) * 1000),
                }
            last_error = f"webhook returned HTTP {last_status_code}"
            if last_status_code < 500 and last_status_code != 429:
                break
        except error.HTTPError as exc:
            last_status_code = int(exc.code)
            last_error = f"webhook returned HTTP {exc.code}"
            if exc.code < 500 and exc.code != 429:
                break
        except Exception as exc:  # noqa: BLE001 - delivery metadata must remain reportable.
            last_error = sanitize_error(exc, sensitive=(config.webhook_url, config.webhook_token))
        if attempt < config.max_attempts:
            sleep(min(0.25 * (2 ** (attempt - 1)), 1.0))

    result = {
        "status": "failed",
        "attempts": attempts,
        "attempted_at": attempted_at,
        "duration_ms": round((time.monotonic() - started) * 1000),
        "error": last_error or "webhook delivery failed",
    }
    if last_status_code:
        result["status_code"] = last_status_code
    return result


def send_test_alert(
    *,
    config: AlertConfig | None = None,
    opener: Any | None = None,
    sleep: Callable[[float], None] = time.sleep,
) -> dict[str, Any]:
    loaded_config = config or AlertConfig.load()
    if not loaded_config.enabled:
        return {"status": "disabled", "configured": False, "reason": "RELAY_ALERT_ENABLED is false"}
    if not loaded_config.webhook_url:
        return {"status": "misconfigured", "configured": False, "reason": "webhook URL is empty"}
    occurred_at = now_iso()
    minute_key = occurred_at[:16].replace(":", "")
    payload = {
        "schema_version": SCHEMA_VERSION,
        "source": "relay.alert_test",
        "environment": loaded_config.environment,
        "severity": "info",
        "alert_type": "webhook_test",
        "issue_types": [],
        "dedupe_key": f"relay:{loaded_config.environment}:webhook_test:{minute_key}",
        "title": "[Relay][INFO] 告警通道测试",
        "message": "Relay 通用 Webhook 配置与投递链路测试成功。",
        "occurred_at": occurred_at,
        "job": {},
        "accounts": [],
        "errors": [],
        "details": {},
        "links": {"jobs": f"{loaded_config.public_url}/jobs"} if loaded_config.public_url else {},
    }
    return {
        "configured": True,
        "dedupe_key": payload["dedupe_key"],
        **deliver_webhook(payload, loaded_config, opener=opener, sleep=sleep),
    }


def normalized_account_errors(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    output: list[dict[str, Any]] = []
    for item in value:
        if not isinstance(item, Mapping):
            continue
        errors = normalized_strings(item.get("errors"))
        output.append({"account_id": str(item.get("account_id") or ""), "errors": errors})
    return output


def snapshot_result_issues(report: Mapping[str, Any]) -> tuple[list[dict[str, Any]], list[str]]:
    errors: list[dict[str, Any]] = []
    blocked: list[str] = []
    for wrapper_name in ("open_snapshot", "settlement_snapshot"):
        wrapper = report.get(wrapper_name)
        if not isinstance(wrapper, Mapping):
            continue
        result = wrapper.get("result")
        if not isinstance(result, Mapping) or not isinstance(result.get("accounts"), list):
            continue
        for account in result["accounts"]:
            if not isinstance(account, Mapping):
                continue
            account_id = str(account.get("account_id") or "")
            account_errors = normalized_strings(account.get("errors"))
            snapshot_not_written = (
                "asset_snapshot_written" in account and not bool(account.get("asset_snapshot_written"))
            )
            if account_errors:
                errors.append({"account_id": account_id, "errors": account_errors})
            if account_id and (account_errors or snapshot_not_written):
                blocked.append(account_id)
    return errors, sorted(set(blocked))


def merge_account_errors(values: list[dict[str, Any]]) -> list[dict[str, Any]]:
    merged: dict[str, list[str]] = {}
    for item in values:
        account_id = str(item.get("account_id") or "")
        messages = merged.setdefault(account_id, [])
        for message in item.get("errors", []):
            if message not in messages:
                messages.append(message)
    return [{"account_id": account_id, "errors": messages} for account_id, messages in merged.items()]


def refresh_timeout_accounts(value: Any) -> list[str]:
    if not isinstance(value, list):
        return []
    output: list[str] = []
    for account in value:
        if not isinstance(account, Mapping):
            continue
        freshness = account.get("refresh_freshness")
        if not isinstance(freshness, Mapping):
            continue
        if bool(freshness.get("timed_out")):
            account_id = str(account.get("account_id") or freshness.get("account_id") or "")
            if account_id:
                output.append(account_id)
    return sorted(set(output))


def collect_error_messages(
    report: Mapping[str, Any],
    account_errors: list[dict[str, Any]],
) -> list[str]:
    messages = normalized_strings(report.get("errors"))
    for item in account_errors:
        account_id = item.get("account_id") or "unknown"
        messages.extend(f"{account_id}: {message}" for message in item.get("errors", []))
    return [message[:1000] for message in messages]


def target_trade_date(report: Mapping[str, Any]) -> str:
    trading_day = report.get("trading_day")
    if isinstance(trading_day, Mapping):
        value = str(trading_day.get("target_trade_date") or trading_day.get("requested_date") or "")
        digits = "".join(character for character in value if character.isdigit())
        if len(digits) >= 8:
            return digits[:8]
    return ""


def normalized_strings(value: Any) -> list[str]:
    if not isinstance(value, (list, tuple)):
        return []
    return [str(item) for item in value if str(item).strip()]


def int_value(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def parse_bool(value: str) -> bool:
    return str(value).strip().lower() in {"1", "true", "yes", "on"}


def parse_positive_float(value: Any, default: float) -> float:
    try:
        parsed = float(value)
    except (TypeError, ValueError):
        return default
    return parsed if parsed > 0 else default


def parse_positive_int(value: Any, default: int) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        return default
    return parsed if parsed > 0 else default


def sanitize_error(exc: Exception, *, sensitive: tuple[str, ...] = ()) -> str:
    message = f"{type(exc).__name__}: {exc}"
    for value in sensitive:
        if value:
            message = message.replace(value, "<redacted>")
    return message[:1000]


def now_iso() -> str:
    return datetime.now(BUSINESS_TZ).isoformat()
