#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/switch-relay-env.sh test
  scripts/switch-relay-env.sh production
  scripts/switch-relay-env.sh production --allow-production-trading

Switch the local 9092 relay service between local test and production
configuration files. The script never prints Redis/PostgreSQL credentials.

Safety:
  - test uses config/relay.test.yaml when present, otherwise config/relay.local.yaml.
  - production uses config/relay.prod.yaml.
  - production configs with trading_enabled=true are rejected by default.
USAGE
}

die() {
  printf 'switch-relay-env: %s\n' "$*" >&2
  exit 1
}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET="${1:-}"
ALLOW_PROD_TRADING=false
if [[ $# -gt 0 ]]; then
  shift
fi
for arg in "$@"; do
  case "$arg" in
    --allow-production-trading)
      ALLOW_PROD_TRADING=true
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      die "unknown argument: $arg"
      ;;
  esac
done

case "$TARGET" in
  test)
    if [[ -f "$ROOT/config/relay.test.yaml" ]]; then
      CONFIG="$ROOT/config/relay.test.yaml"
    elif [[ -f "$ROOT/config/relay.local.yaml" ]]; then
      CONFIG="$ROOT/config/relay.local.yaml"
    else
      die "missing config/relay.test.yaml or config/relay.local.yaml"
    fi
    EXPECTED_ENV="test"
    ;;
  production|prod)
    CONFIG="$ROOT/config/relay.prod.yaml"
    EXPECTED_ENV="production"
    [[ -f "$CONFIG" ]] || die "missing config/relay.prod.yaml"
    ;;
  -h|--help|"")
    usage
    exit 0
    ;;
  *)
    usage >&2
    die "target must be test or production"
    ;;
esac

cd "$ROOT"

if [[ "$EXPECTED_ENV" == "production" ]]; then
  if ! grep -Eq '^[[:space:]]*environment:[[:space:]]*"?production"?[[:space:]]*(#.*)?$' "$CONFIG"; then
    die "config/relay.prod.yaml must declare service.environment: production"
  fi
  if grep -Eq '^[[:space:]]*trading_enabled:[[:space:]]*true[[:space:]]*(#.*)?$' "$CONFIG"; then
    if [[ "$ALLOW_PROD_TRADING" != "true" ]]; then
      die "production config has trading_enabled=true; rerun with --allow-production-trading only after manual risk check"
    fi
    if [[ -t 0 ]]; then
      printf 'Type ENABLE PRODUCTION TRADING to continue: '
      read -r confirmation
      [[ "$confirmation" == "ENABLE PRODUCTION TRADING" ]] || die "confirmation mismatch"
    else
      [[ "${RELAY_CONFIRM_PRODUCTION_TRADING:-}" == "ENABLE PRODUCTION TRADING" ]] || die "set RELAY_CONFIRM_PRODUCTION_TRADING for non-interactive production trading switch"
    fi
  fi
fi

if [[ "$EXPECTED_ENV" == "test" ]]; then
  if grep -Eq '^[[:space:]]*environment:[[:space:]]*"?production"?[[:space:]]*(#.*)?$' "$CONFIG"; then
    die "selected test config declares production environment: $CONFIG"
  fi
fi

BIN="${RELAY_DOCS_BIN:-/tmp/relay-docs}"
PID_FILE="${RELAY_DOCS_PID_FILE:-/tmp/relay-docs.pid}"
LOG_FILE="${RELAY_DOCS_LOG_FILE:-/tmp/relay-docs.log}"
ADDR="${RELAY_DOCS_ADDR:-}"

printf 'Building relay docs binary...\n'
go build -o "$BIN" ./cmd/relay-docs

old_pids=()
if [[ -f "$PID_FILE" ]]; then
  old_pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if [[ "$old_pid" =~ ^[0-9]+$ ]] && kill -0 "$old_pid" 2>/dev/null; then
    old_pids+=("$old_pid")
  fi
fi
while IFS= read -r pid; do
  [[ -n "$pid" ]] || continue
  old_pids+=("$pid")
done < <(pgrep -f "$BIN .* -root $ROOT" 2>/dev/null || true)

if [[ ${#old_pids[@]} -gt 0 ]]; then
  printf 'Stopping existing relay docs process...\n'
  printf '%s\n' "${old_pids[@]}" | sort -u | while read -r pid; do
    kill "$pid" 2>/dev/null || true
  done
  sleep 1
fi

printf 'Starting relay 9092 with %s...\n' "$(realpath --relative-to="$ROOT" "$CONFIG" 2>/dev/null || printf '%s' "$CONFIG")"
if [[ -n "$ADDR" ]]; then
  setsid "$BIN" -config "$CONFIG" -root "$ROOT" -addr "$ADDR" > "$LOG_FILE" 2>&1 < /dev/null &
else
  setsid "$BIN" -config "$CONFIG" -root "$ROOT" > "$LOG_FILE" 2>&1 < /dev/null &
fi
new_pid=$!
printf '%s\n' "$new_pid" > "$PID_FILE"

for _ in $(seq 1 20); do
  if curl -fsS "http://127.0.0.1:9092/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

curl -fsS "http://127.0.0.1:9092/healthz" >/dev/null || {
  tail -n 80 "$LOG_FILE" >&2 || true
  die "relay did not become healthy"
}

status_summary="$(curl -fsS "http://127.0.0.1:9092/v1/status" | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print("{} {} trading_enabled={}".format(d["environment"], d["status"], d["accounts"]["trading_enabled"]))')"
printf 'Relay switched: %s\n' "$status_summary"
printf 'PID: %s\nLog: %s\n' "$new_pid" "$LOG_FILE"
