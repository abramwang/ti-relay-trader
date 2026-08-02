#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_NAME="relay-worker"
ACTIVE_CONFIG="$ROOT_DIR/.runtime/active-config.yaml"
ACTIVE_ENV_FILE="$ROOT_DIR/.runtime/expected-environment"
DEFAULT_CONFIG="$ROOT_DIR/config/relay.prod.yaml"
if [[ -e "$ACTIVE_CONFIG" || -L "$ACTIVE_CONFIG" ]]; then
  DEFAULT_CONFIG="$ACTIVE_CONFIG"
fi
DEFAULT_EXPECTED_ENV="production"
if [[ -r "$ACTIVE_ENV_FILE" ]]; then
  read -r persisted_environment < "$ACTIVE_ENV_FILE" || true
  case "$persisted_environment" in
    test|production) DEFAULT_EXPECTED_ENV="$persisted_environment" ;;
  esac
fi
BIN="${RELAY_WORKER_BIN:-$ROOT_DIR/.runtime/bin/relay-worker}"
PREVIOUS_BIN="${RELAY_WORKER_PREVIOUS_BIN:-$ROOT_DIR/.runtime/bin/relay-worker.previous}"
PID_FILE="${RELAY_WORKER_PID_FILE:-$ROOT_DIR/.runtime/run/relay-worker.pid}"
LOG_FILE="${RELAY_WORKER_LOG_FILE:-/var/log/relay/relay-worker.log}"
CONFIG="${RELAY_CONFIG_PATH:-$DEFAULT_CONFIG}"
EXPECTED_ENV="${RELAY_EXPECTED_ENV:-$DEFAULT_EXPECTED_ENV}"
HEALTH_ADDR="${RELAY_WORKER_HEALTH_ADDR:-127.0.0.1:19092}"
HEALTH_URL="${RELAY_WORKER_HEALTH_URL:-http://127.0.0.1:19092/readyz}"

ensure_dirs() {
  mkdir -p "$(dirname "$BIN")" "$(dirname "$PREVIOUS_BIN")" "$(dirname "$PID_FILE")" "$(dirname "$LOG_FILE")"
}

pid_value() {
  if [[ -f "$PID_FILE" ]]; then
    cat "$PID_FILE"
  fi
}

process_args() {
  local pid="${1:-}"
  [[ -n "$pid" ]] || return 1
  ps -p "$pid" -o args= 2>/dev/null || true
}

is_service_pid() {
  local pid="${1:-}"
  [[ -n "$pid" ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  process_args "$pid" | grep -F -- "$BIN" >/dev/null
}

is_expected_service_pid() {
  local pid="${1:-}"
  local args
  is_service_pid "$pid" || return 1
  args="$(process_args "$pid")"
  grep -F -- "-config $CONFIG" <<<"$args" >/dev/null || return 1
  grep -F -- "-health-addr $HEALTH_ADDR" <<<"$args" >/dev/null
}

running_pids() {
  pgrep -f "$BIN .* -config $CONFIG" 2>/dev/null || true
}

health_ok() {
  curl -fsS "$HEALTH_URL" >/dev/null 2>&1
}

current_environment() {
  curl -fsS "$HEALTH_URL" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("environment", ""))' 2>/dev/null || true
}

status_summary() {
  curl -fsS "$HEALTH_URL" 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
print("{} {} started_at={}".format(d.get("environment", ""), d.get("status", ""), d.get("started_at", "")))' 2>/dev/null || true
}

prepare_relay_env() {
  unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
  local bypass="localhost,127.0.0.1,.quantstage.com,quantstage.com"
  if [[ -n "${NO_PROXY:-}" ]]; then
    export NO_PROXY="$NO_PROXY,$bypass"
  else
    export NO_PROXY="$bypass"
  fi
  export no_proxy="$NO_PROXY"
}

validate_config() {
  [[ -f "$CONFIG" ]] || {
    echo "$SERVICE_NAME missing config: $CONFIG" >&2
    return 1
  }
  if [[ "$EXPECTED_ENV" == "production" ]]; then
    grep -Eq '^[[:space:]]*environment:[[:space:]]*"?production"?[[:space:]]*(#.*)?$' "$CONFIG" || {
      echo "$SERVICE_NAME refuses to start: production config must declare service.environment=production" >&2
      return 1
    }
    if grep -Eq '^[[:space:]]*trading_enabled:[[:space:]]*true[[:space:]]*(#.*)?$' "$CONFIG"; then
      if [[ "${RELAY_ALLOW_PRODUCTION_TRADING:-false}" != "true" ]]; then
        echo "$SERVICE_NAME refuses to autostart production trading config" >&2
        return 1
      fi
    fi
  fi
}

needs_build() {
  [[ -x "$BIN" ]] || return 0
  find "$ROOT_DIR/cmd/relay-worker" "$ROOT_DIR/internal" "$ROOT_DIR/go.mod" "$ROOT_DIR/go.sum" -newer "$BIN" -print -quit 2>/dev/null | grep -q .
}

build_binary() {
  ensure_dirs
  if [[ "${RELAY_SKIP_BUILD:-false}" != "true" ]] && needs_build; then
    local candidate
    candidate="${BIN}.build.$$"
    echo "$SERVICE_NAME building binary: $BIN"
    (cd "$ROOT_DIR" && go build -o "$candidate" ./cmd/relay-worker)
    if [[ -x "$BIN" ]] && ! cmp -s "$candidate" "$BIN"; then
      cp -p "$BIN" "$PREVIOUS_BIN"
    fi
    mv "$candidate" "$BIN"
  fi
}

start_service() {
  ensure_dirs
  validate_config
  local pid env
  pid="$(pid_value || true)"
  if health_ok; then
    env="$(current_environment)"
    if is_expected_service_pid "$pid" && [[ "$env" == "$EXPECTED_ENV" ]]; then
      echo "$SERVICE_NAME already healthy pid=$pid $(status_summary)"
      return 0
    fi
    echo "$SERVICE_NAME refuses to start: $HEALTH_URL is served by an unexpected process" >&2
    return 1
  fi
  if is_service_pid "$pid"; then
    echo "$SERVICE_NAME pid=$pid is running but unhealthy; restarting"
    stop_service
  fi
  build_binary
  echo "$SERVICE_NAME starting config=$CONFIG health_addr=$HEALTH_ADDR"
  (
    cd "$ROOT_DIR"
    prepare_relay_env
    setsid "$BIN" -config "$CONFIG" -health-addr "$HEALTH_ADDR" >> "$LOG_FILE" 2>&1 < /dev/null &
    echo $! > "$PID_FILE"
  )
  for _ in $(seq 1 30); do
    env="$(current_environment)"
    if health_ok && [[ "$env" == "$EXPECTED_ENV" ]]; then
      echo "$SERVICE_NAME started pid=$(pid_value) $(status_summary)"
      return 0
    fi
    sleep 0.5
  done
  tail -n 80 "$LOG_FILE" >&2 || true
  echo "$SERVICE_NAME failed to become ready" >&2
  return 1
}

stop_service() {
  local pids pid
  pids="$(running_pids | sort -u || true)"
  pid="$(pid_value || true)"
  if [[ -n "$pid" ]] && is_service_pid "$pid"; then
    pids="$(printf '%s\n%s\n' "$pids" "$pid" | sed '/^$/d' | sort -u)"
  fi
  if [[ -z "$pids" ]]; then
    rm -f "$PID_FILE"
    echo "$SERVICE_NAME not running"
    return 0
  fi
  printf '%s\n' "$pids" | while read -r pid; do
    [[ -n "$pid" ]] || continue
    kill "$pid" 2>/dev/null || true
  done
  for _ in $(seq 1 20); do
    if [[ -z "$(running_pids)" ]]; then
      rm -f "$PID_FILE"
      echo "$SERVICE_NAME stopped"
      return 0
    fi
    sleep 0.2
  done
  running_pids | while read -r pid; do
    [[ -n "$pid" ]] || continue
    kill -9 "$pid" 2>/dev/null || true
  done
  rm -f "$PID_FILE"
  echo "$SERVICE_NAME force stopped"
}

status_service() {
  local pid env
  pid="$(pid_value || true)"
  env="$(current_environment)"
  if health_ok && is_expected_service_pid "$pid" && [[ "$env" == "$EXPECTED_ENV" ]]; then
    echo "$SERVICE_NAME healthy pid=$pid $(status_summary)"
    return 0
  fi
  if is_service_pid "$pid"; then
    echo "$SERVICE_NAME running but unhealthy pid=$pid environment=${env:-unknown}"
    return 1
  fi
  echo "$SERVICE_NAME not running"
  return 1
}

rollback_service() {
  [[ -x "$PREVIOUS_BIN" ]] || {
    echo "$SERVICE_NAME rollback unavailable: $PREVIOUS_BIN does not exist" >&2
    return 1
  }
  stop_service
  local current
  current="${BIN}.rollback.$$"
  mv "$BIN" "$current"
  mv "$PREVIOUS_BIN" "$BIN"
  mv "$current" "$PREVIOUS_BIN"
  if RELAY_SKIP_BUILD=true start_service; then
    echo "$SERVICE_NAME rollback started successfully"
    return 0
  fi
  echo "$SERVICE_NAME rollback failed; restoring newer binary" >&2
  stop_service || true
  mv "$BIN" "$current"
  mv "$PREVIOUS_BIN" "$BIN"
  mv "$current" "$PREVIOUS_BIN"
  RELAY_SKIP_BUILD=true start_service || true
  return 1
}

show_logs() {
  ensure_dirs
  tail -n "${RELAY_LOG_LINES:-120}" "$LOG_FILE"
}

case "${1:-status}" in
  start)
    start_service
    ;;
  stop)
    stop_service
    ;;
  restart)
    build_binary
    stop_service
    RELAY_SKIP_BUILD=true start_service
    ;;
  status)
    status_service
    ;;
  rollback)
    rollback_service
    ;;
  logs)
    show_logs
    ;;
  *)
    echo "usage: $0 {start|stop|restart|status|rollback|logs}" >&2
    exit 2
    ;;
esac
