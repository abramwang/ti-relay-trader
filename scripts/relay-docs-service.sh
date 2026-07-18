#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_NAME="relay-docs"
BIN="${RELAY_DOCS_BIN:-/tmp/relay-docs}"
PID_FILE="${RELAY_DOCS_PID_FILE:-/tmp/relay-docs.pid}"
LOG_FILE="${RELAY_DOCS_LOG_FILE:-/tmp/relay-docs.log}"
CRON_LOG_FILE="${RELAY_DOCS_CRON_LOG_FILE:-/var/log/relay/relay-docs-service-cron.log}"
ADDR="${RELAY_DOCS_ADDR:-0.0.0.0:9092}"
CONFIG="${RELAY_CONFIG_PATH:-$ROOT_DIR/config/relay.prod.yaml}"
EXPECTED_ENV="${RELAY_EXPECTED_ENV:-production}"
HEALTH_URL="${RELAY_HEALTH_URL:-http://127.0.0.1:9092/healthz}"
STATUS_URL="${RELAY_STATUS_URL:-http://127.0.0.1:9092/v1/status}"

cron_block() {
  cat <<EOF
# RELAY_DOCS_AUTOSTART_BEGIN
@reboot cd $ROOT_DIR && $ROOT_DIR/scripts/relay-docs-service.sh start >> $CRON_LOG_FILE 2>&1
* * * * * cd $ROOT_DIR && $ROOT_DIR/scripts/relay-docs-service.sh start >> $CRON_LOG_FILE 2>&1
# RELAY_DOCS_AUTOSTART_END
EOF
}

ensure_dirs() {
  mkdir -p "$(dirname "$BIN")" "$(dirname "$PID_FILE")" "$(dirname "$LOG_FILE")" "$(dirname "$CRON_LOG_FILE")"
}

pid_value() {
  if [[ -f "$PID_FILE" ]]; then
    cat "$PID_FILE"
  fi
}

is_service_pid() {
  local pid="${1:-}"
  [[ -n "$pid" ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  process_args "$pid" | grep -F -- "$BIN" >/dev/null
}

process_args() {
  local pid="${1:-}"
  [[ -n "$pid" ]] || return 1
  ps -p "$pid" -o args= 2>/dev/null || true
}

is_expected_service_pid() {
  local pid="${1:-}"
  local args
  is_service_pid "$pid" || return 1
  args="$(process_args "$pid")"
  grep -F -- "-config $CONFIG" <<<"$args" >/dev/null || return 1
  grep -F -- "-root $ROOT_DIR" <<<"$args" >/dev/null
}

running_pids() {
  pgrep -f "$BIN .* -root $ROOT_DIR" 2>/dev/null || true
}

health_ok() {
  curl -fsS "$HEALTH_URL" >/dev/null 2>&1
}

current_environment() {
  curl -fsS "$STATUS_URL" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("data", {}).get("environment", ""))' 2>/dev/null || true
}

status_summary() {
  curl -fsS "$STATUS_URL" 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin).get("data", {})
accounts=d.get("accounts", {})
deps=d.get("dependencies", {})
print("{} {} trading_enabled={} redis={} database={}".format(
    d.get("environment", ""),
    d.get("status", ""),
    accounts.get("trading_enabled", ""),
    deps.get("redis", {}).get("status", ""),
    deps.get("database", {}).get("status", ""),
))' 2>/dev/null || true
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
    if ! grep -Eq '^[[:space:]]*environment:[[:space:]]*"?production"?[[:space:]]*(#.*)?$' "$CONFIG"; then
      echo "$SERVICE_NAME refuses to start: production config must declare service.environment=production" >&2
      return 1
    fi
    if grep -Eq '^[[:space:]]*trading_enabled:[[:space:]]*true[[:space:]]*(#.*)?$' "$CONFIG"; then
      if [[ "${RELAY_ALLOW_PRODUCTION_TRADING:-false}" != "true" ]]; then
        echo "$SERVICE_NAME refuses to autostart production trading config; set RELAY_ALLOW_PRODUCTION_TRADING=true only after manual risk check" >&2
        return 1
      fi
    fi
  fi
}

needs_build() {
  [[ -x "$BIN" ]] || return 0
  find "$ROOT_DIR/cmd" "$ROOT_DIR/internal" "$ROOT_DIR/go.mod" "$ROOT_DIR/go.sum" -newer "$BIN" -print -quit 2>/dev/null | grep -q .
}

build_binary() {
  ensure_dirs
  if needs_build; then
    echo "$SERVICE_NAME building binary: $BIN"
    (cd "$ROOT_DIR" && go build -o "$BIN" ./cmd/relay-docs)
  fi
}

write_pid_from_existing() {
  local pid
  while read -r pid; do
    [[ -n "$pid" ]] || continue
    if is_expected_service_pid "$pid"; then
      printf '%s\n' "$pid" > "$PID_FILE"
      return 0
    fi
  done < <(running_pids)
  return 1
}

start_service() {
  ensure_dirs
  validate_config

  local pid env
  pid="$(pid_value || true)"
  if health_ok; then
    if ! is_expected_service_pid "$pid"; then
      write_pid_from_existing || true
      pid="$(pid_value || true)"
    fi
    if is_expected_service_pid "$pid"; then
      echo "$SERVICE_NAME already healthy pid=${pid:-unknown}"
      return 0
    fi
    env="$(current_environment)"
    if [[ "$env" == "$EXPECTED_ENV" ]]; then
      echo "$SERVICE_NAME already healthy pid=${pid:-unknown} $(status_summary)"
      return 0
    fi
  fi

  if is_service_pid "$pid"; then
    echo "$SERVICE_NAME pid=$pid is running but unhealthy or not using expected config; restarting"
    stop_service
  fi

  build_binary
  echo "$SERVICE_NAME starting config=$CONFIG addr=$ADDR"
  (
    cd "$ROOT_DIR"
    prepare_relay_env
    setsid "$BIN" -config "$CONFIG" -root "$ROOT_DIR" -addr "$ADDR" >> "$LOG_FILE" 2>&1 < /dev/null &
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
  echo "$SERVICE_NAME failed to become healthy" >&2
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
  if health_ok && [[ "$env" == "$EXPECTED_ENV" ]]; then
    echo "$SERVICE_NAME healthy pid=${pid:-unknown} $(status_summary)"
    return 0
  fi
  if is_service_pid "$pid"; then
    echo "$SERVICE_NAME running but unhealthy pid=$pid environment=${env:-unknown}"
    return 1
  fi
  echo "$SERVICE_NAME not running"
  return 1
}

install_cron() {
  ensure_dirs
  local current
  current="$(mktemp)"
  crontab -l 2>/dev/null > "$current" || true
  sed -i '/# RELAY_DOCS_AUTOSTART_BEGIN/,/# RELAY_DOCS_AUTOSTART_END/d' "$current"
  {
    cat "$current"
    cron_block
  } | crontab -
  rm -f "$current"
  echo "$SERVICE_NAME cron autostart installed"
}

uninstall_cron() {
  local current
  current="$(mktemp)"
  crontab -l 2>/dev/null > "$current" || true
  sed -i '/# RELAY_DOCS_AUTOSTART_BEGIN/,/# RELAY_DOCS_AUTOSTART_END/d' "$current"
  crontab "$current"
  rm -f "$current"
  echo "$SERVICE_NAME cron autostart removed"
}

case "${1:-status}" in
  start)
    start_service
    ;;
  stop)
    stop_service
    ;;
  restart)
    stop_service
    start_service
    ;;
  status)
    status_service
    ;;
  install-cron)
    install_cron
    ;;
  uninstall-cron)
    uninstall_cron
    ;;
  *)
    echo "usage: $0 {start|stop|restart|status|install-cron|uninstall-cron}" >&2
    exit 2
    ;;
esac
