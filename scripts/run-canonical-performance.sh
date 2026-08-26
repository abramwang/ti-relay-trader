#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PYTHON_BIN="${RELAY_PYTHON_BIN:-python3}"
REPORT_DIR="${RELAY_JOB_REPORT_DIR:-/var/log/relay/reports}"
STATE_DIR="${RELAY_JOB_STATE_DIR:-/var/log/relay/state}"
PERFORMANCE_ACCOUNT_IDS="${RELAY_PERFORMANCE_ACCOUNT_IDS:-}"
RELAY_CONFIG_PATH="${RELAY_CONFIG_PATH:-$ROOT_DIR/config/relay.prod.yaml}"
RELAYCTL_BIN="${RELAYCTL_BIN:-$ROOT_DIR/.runtime/bin/relayctl}"
export PYTHONPATH="$ROOT_DIR/src:$ROOT_DIR/sdk/python${PYTHONPATH:+:$PYTHONPATH}"

if [[ -z "$PERFORMANCE_ACCOUNT_IDS" ]]; then
  echo "relay canonical performance: RELAY_PERFORMANCE_ACCOUNT_IDS is required" >&2
  exit 2
fi

target_date="${RELAY_TARGET_DATE:-$(TZ=Asia/Shanghai date +%Y%m%d)}"
if [[ ! "$target_date" =~ ^[0-9]{8}$ ]]; then
  echo "relay canonical performance: invalid target date [$target_date]" >&2
  exit 2
fi

mkdir -p "$REPORT_DIR" "$STATE_DIR"
report="$REPORT_DIR/performance_canonical.json"
marker="$STATE_DIR/performance-canonical-$target_date.done"
if [[ -f "$marker" ]]; then
  echo "relay canonical performance: already completed for $target_date"
  exit 0
fi

account_args=()
IFS=',' read -r -a configured_accounts <<< "$PERFORMANCE_ACCOUNT_IDS"
for account_id in "${configured_accounts[@]}"; do
  account_id="${account_id//[[:space:]]/}"
  if [[ -n "$account_id" ]]; then
    account_args+=(--account-id "$account_id")
  fi
done
if [[ ${#account_args[@]} -eq 0 ]]; then
  echo "relay canonical performance: no valid performance accounts configured" >&2
  exit 2
fi

export RELAY_CONFIG_PATH RELAYCTL_BIN
set +e
"$PYTHON_BIN" -m relay.jobs.performance_canonical \
  "${account_args[@]}" \
  --target-date "$target_date" \
  --timeout "${RELAY_PERFORMANCE_HTTP_TIMEOUT_SECONDS:-30}" \
  --persist \
  --trigger meridian_watermark_poll \
  --output "$report"
status=$?
set -e

if [[ -f "$report" ]] && "$PYTHON_BIN" - "$report" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    report = json.load(handle)
raise SystemExit(0 if report.get("canonical_completed") is True else 1)
PY
then
  printf '%s\n' "$target_date" > "$marker"
fi

exit "$status"
