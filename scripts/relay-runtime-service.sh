#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_SERVICE="$ROOT_DIR/scripts/relay-docs-service.sh"
WORKER_SERVICE="$ROOT_DIR/scripts/relay-worker-service.sh"
CRON_LOG_FILE="${RELAY_RUNTIME_CRON_LOG_FILE:-/var/log/relay/relay-runtime-service-cron.log}"

runtime_config() {
  if [[ -n "${RELAY_CONFIG_PATH:-}" ]]; then
    printf '%s\n' "$RELAY_CONFIG_PATH"
  elif [[ -e "$ROOT_DIR/.runtime/active-config.yaml" || -L "$ROOT_DIR/.runtime/active-config.yaml" ]]; then
    printf '%s\n' "$ROOT_DIR/.runtime/active-config.yaml"
  else
    printf '%s\n' "$ROOT_DIR/config/relay.prod.yaml"
  fi
}

uses_external_worker() {
  local config
  config="$(runtime_config)"
  [[ -f "$config" ]] || return 1
  grep -Eq '^[[:space:]]*embedded_ledger_sync:[[:space:]]*false[[:space:]]*(#.*)?$' "$config"
}

start_runtime() {
  if uses_external_worker; then
    "$WORKER_SERVICE" start
  else
    "$WORKER_SERVICE" stop
  fi
  "$API_SERVICE" start
}

stop_runtime() {
  "$API_SERVICE" stop
  "$WORKER_SERVICE" stop
}

restart_runtime() {
  if uses_external_worker; then
    "$WORKER_SERVICE" restart
  else
    "$WORKER_SERVICE" stop
  fi
  "$API_SERVICE" restart
}

status_runtime() {
  local failed=0
  "$API_SERVICE" status || failed=1
  if uses_external_worker; then
    "$WORKER_SERVICE" status || failed=1
  else
    echo "relay-worker disabled: API uses embedded ledger sync"
  fi
  return "$failed"
}

install_cron() {
  mkdir -p "$(dirname "$CRON_LOG_FILE")"
  local current
  current="$(mktemp)"
  crontab -l 2>/dev/null > "$current" || true
  sed -i '/# RELAY_DOCS_AUTOSTART_BEGIN/,/# RELAY_DOCS_AUTOSTART_END/d' "$current"
  sed -i '/# RELAY_RUNTIME_AUTOSTART_BEGIN/,/# RELAY_RUNTIME_AUTOSTART_END/d' "$current"
  {
    cat "$current"
    cat <<EOF
# RELAY_RUNTIME_AUTOSTART_BEGIN
@reboot cd $ROOT_DIR && $ROOT_DIR/scripts/relay-runtime-service.sh start >> $CRON_LOG_FILE 2>&1
* * * * * cd $ROOT_DIR && $ROOT_DIR/scripts/relay-runtime-service.sh start >> $CRON_LOG_FILE 2>&1
# RELAY_RUNTIME_AUTOSTART_END
EOF
  } | crontab -
  rm -f "$current"
  echo "relay runtime cron autostart installed"
}

uninstall_cron() {
  local current
  current="$(mktemp)"
  crontab -l 2>/dev/null > "$current" || true
  sed -i '/# RELAY_RUNTIME_AUTOSTART_BEGIN/,/# RELAY_RUNTIME_AUTOSTART_END/d' "$current"
  crontab "$current"
  rm -f "$current"
  echo "relay runtime cron autostart removed"
}

case "${1:-status}" in
  start)
    start_runtime
    ;;
  stop)
    stop_runtime
    ;;
  restart)
    restart_runtime
    ;;
  status)
    status_runtime
    ;;
  rollback-api)
    "$API_SERVICE" rollback
    ;;
  rollback-worker)
    "$WORKER_SERVICE" rollback
    ;;
  logs-api)
    "$API_SERVICE" logs
    ;;
  logs-worker)
    "$WORKER_SERVICE" logs
    ;;
  install-cron)
    install_cron
    ;;
  uninstall-cron)
    uninstall_cron
    ;;
  *)
    echo "usage: $0 {start|stop|restart|status|rollback-api|rollback-worker|logs-api|logs-worker|install-cron|uninstall-cron}" >&2
    exit 2
    ;;
esac
