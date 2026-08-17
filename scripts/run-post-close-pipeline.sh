#!/usr/bin/env bash
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PYTHON_BIN="${RELAY_PYTHON_BIN:-python3}"
REPORT_DIR="${RELAY_JOB_REPORT_DIR:-/var/log/relay/reports}"
PERFORMANCE_LOCK="${RELAY_PERFORMANCE_LOCK:-/tmp/relay-performance-daily.lock}"
PERFORMANCE_ACCOUNT_IDS="${RELAY_PERFORMANCE_ACCOUNT_IDS:-}"
PERFORMANCE_HTTP_TIMEOUT_SECONDS="${RELAY_PERFORMANCE_HTTP_TIMEOUT_SECONDS:-30}"
SETTLEMENT_HTTP_TIMEOUT_SECONDS="${RELAY_SETTLEMENT_HTTP_TIMEOUT_SECONDS:-60}"
RELAY_CONFIG_PATH="${RELAY_CONFIG_PATH:-$ROOT_DIR/config/relay.prod.yaml}"
RELAYCTL_BIN="${RELAYCTL_BIN:-$ROOT_DIR/.runtime/bin/relayctl}"

mkdir -p "$REPORT_DIR"

capture_report="$REPORT_DIR/post_close_capture.json"
post_report="$REPORT_DIR/post_close_settlement.json"
performance_report="$REPORT_DIR/performance_daily.json"
performance_publish_report="$REPORT_DIR/performance_publish.json"
target_args=()
if [[ -n "${RELAY_TARGET_DATE:-}" ]]; then
  target_args=(--target-date "$RELAY_TARGET_DATE")
fi

relayctl_needs_build() {
  [[ -x "$RELAYCTL_BIN" ]] || return 0
  find "$ROOT_DIR/cmd/relayctl" "$ROOT_DIR/internal" "$ROOT_DIR/go.mod" "$ROOT_DIR/go.sum" -newer "$RELAYCTL_BIN" -print -quit 2>/dev/null | grep -q .
}

build_relayctl() {
  if ! relayctl_needs_build; then
    return 0
  fi
  mkdir -p "$(dirname "$RELAYCTL_BIN")"
  local candidate="${RELAYCTL_BIN}.build.$$"
  echo "relay post-close pipeline: building relayctl"
  if ! (cd "$ROOT_DIR" && go build -o "$candidate" ./cmd/relayctl); then
    rm -f "$candidate"
    return 1
  fi
  mv "$candidate" "$RELAYCTL_BIN"
}

echo "relay post-close pipeline: starting broker close capture"
if ! "$PYTHON_BIN" -m relay.jobs.post_close_capture \
  --persist \
  --trigger cron \
  --settlement-timeout-seconds "$SETTLEMENT_HTTP_TIMEOUT_SECONDS" \
  --output "$capture_report" \
  "${target_args[@]}"; then
  echo "relay post-close pipeline: broker close capture failed; settlement and performance skipped" >&2
  exit 1
fi

readarray -t capture_state < <("$PYTHON_BIN" - "$capture_report" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    report = json.load(handle)
if report.get("skipped"):
    print("skipped")
    print("")
elif report.get("ok") is not True:
    print("failed")
    print("")
else:
    snapshot = report.get("broker_close_snapshot") or {}
    result = snapshot.get("result") or {}
    successful_accounts = [
        str(item.get("account_id"))
        for item in result.get("accounts", [])
        if item.get("asset_snapshot_written") is True and not item.get("errors")
    ]
    if snapshot.get("ok") is not True or snapshot.get("error") or result.get("status") != "completed" or not successful_accounts:
        print("failed")
        print("")
    else:
        trading_day = report.get("trading_day") or {}
        print("ready")
        print(str(trading_day.get("target_trade_date") or ""))
        for account_id in successful_accounts:
            print(account_id)
PY
)

case "${capture_state[0]:-failed}" in
  skipped)
    echo "relay post-close pipeline: non-trading day; settlement and performance skipped"
    exit 0
    ;;
  ready)
    ;;
  *)
    echo "relay post-close pipeline: broker close capture report is not successful; settlement and performance skipped" >&2
    exit 1
    ;;
esac

trade_date="${capture_state[1]:-}"
if [[ ! "$trade_date" =~ ^[0-9]{8}$ ]]; then
  echo "relay post-close pipeline: invalid broker capture target_trade_date [$trade_date]" >&2
  exit 1
fi

settlement_account_args=()
for account_id in "${capture_state[@]:2}"; do
  if [[ -n "$account_id" ]]; then
    settlement_account_args+=(--account-id "$account_id")
  fi
done
if [[ ${#settlement_account_args[@]} -eq 0 ]]; then
  echo "relay post-close pipeline: broker capture returned no successful accounts" >&2
  exit 1
fi

echo "relay post-close pipeline: broker close captured for $trade_date; starting settlement"
if ! "$PYTHON_BIN" -m relay.jobs.post_close_settlement \
  "${settlement_account_args[@]}" \
  --target-date "$trade_date" \
  --skip-refresh \
  --persist \
  --trigger post_close_capture_success \
  --settlement-timeout-seconds "$SETTLEMENT_HTTP_TIMEOUT_SECONDS" \
  --output "$post_report"; then
  echo "relay post-close pipeline: settlement deferred or failed; broker close snapshot remains available" >&2
  exit 1
fi

readarray -t settlement_state < <("$PYTHON_BIN" - "$post_report" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    report = json.load(handle)
snapshot = report.get("settlement_snapshot") or {}
result = snapshot.get("result") or {}
if report.get("ok") is True and not report.get("skipped") and snapshot.get("ok") is True and result.get("status") == "completed" and not result.get("account_error_count", 0):
    print("ready")
else:
    print("failed")
PY
)

if [[ "${settlement_state[0]:-failed}" != "ready" ]]; then
  echo "relay post-close pipeline: settlement report is not successful; performance skipped" >&2
  exit 1
fi

if [[ -z "$PERFORMANCE_ACCOUNT_IDS" ]]; then
  echo "relay post-close pipeline: RELAY_PERFORMANCE_ACCOUNT_IDS is required" >&2
  exit 2
fi

performance_args=()
performance_account_ids=()
IFS=',' read -r -a configured_accounts <<< "$PERFORMANCE_ACCOUNT_IDS"
for account_id in "${configured_accounts[@]}"; do
  account_id="${account_id//[[:space:]]/}"
  if [[ -n "$account_id" ]]; then
    performance_args+=(--account-id "$account_id")
    performance_account_ids+=("$account_id")
  fi
done
if [[ ${#performance_args[@]} -eq 0 ]]; then
  echo "relay post-close pipeline: no valid performance accounts configured" >&2
  exit 2
fi

performance_accounts_csv="$(IFS=,; echo "${performance_account_ids[*]}")"
echo "relay post-close pipeline: settlement succeeded for $trade_date; publishing performance NAV"
if ! build_relayctl; then
  echo "relay post-close pipeline: relayctl build failed" >&2
  exit 1
fi
if ! "$RELAYCTL_BIN" performance-rebuild \
  -config "$RELAY_CONFIG_PATH" \
  -accounts "$performance_accounts_csv" \
  -date-from "$trade_date" \
  -date-to "$trade_date" \
  -persist \
  -timeout 10m \
  > "$performance_publish_report"; then
  echo "relay post-close pipeline: performance publication failed" >&2
  exit 1
fi

echo "relay post-close pipeline: performance publication completed; recording quality report"
if ! flock -n "$PERFORMANCE_LOCK" "$PYTHON_BIN" -m relay.jobs.performance_daily \
  "${performance_args[@]}" \
  --target-date "$trade_date" \
  --timeout "$PERFORMANCE_HTTP_TIMEOUT_SECONDS" \
  --persist \
  --trigger post_close_success \
  --output "$performance_report"; then
  echo "relay post-close pipeline: performance failed" >&2
  exit 1
fi

echo "relay post-close pipeline: completed for $trade_date"
