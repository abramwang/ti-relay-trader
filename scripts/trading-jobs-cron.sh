#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MARKER_BEGIN="# RELAY_TRADER_CRON_BEGIN"
MARKER_END="# RELAY_TRADER_CRON_END"
CRON_LOG_DIR="${RELAY_CRON_LOG_DIR:-/var/log/relay}"
PERFORMANCE_ACCOUNT_IDS="${RELAY_PERFORMANCE_ACCOUNT_IDS:-307000051387,307000051388,307000051389,314000046830}"

current_crontab() {
  crontab -l 2>/dev/null || true
}

without_managed_block() {
  current_crontab | sed "/^${MARKER_BEGIN}$/,/^${MARKER_END}$/d"
}

install_cron() {
  mkdir -p "$CRON_LOG_DIR/reports"
  {
    without_managed_block
    cat <<EOF
$MARKER_BEGIN
CRON_TZ=Asia/Shanghai
TZ=Asia/Shanghai
RELAY_HOME=$ROOT_DIR
RELAY_CONFIG_PATH=$ROOT_DIR/config/relay.prod.yaml
PYTHONPATH=$ROOT_DIR/src:$ROOT_DIR/sdk/python
RELAY_BASE_URL=http://127.0.0.1:9092
RELAY_PERFORMANCE_ACCOUNT_IDS=$PERFORMANCE_ACCOUNT_IDS
RELAY_SETTLEMENT_HTTP_TIMEOUT_SECONDS=60

# Relay A-share pre-open initialization, 09:01 Asia/Shanghai.
1 9 * * 1-5 cd \$RELAY_HOME && flock -n /tmp/relay-pre-open-init.lock python3 -m relay.jobs.pre_open_init --settlement-timeout-seconds \$RELAY_SETTLEMENT_HTTP_TIMEOUT_SECONDS --persist --trigger cron --output $CRON_LOG_DIR/reports/pre_open_init.json >> $CRON_LOG_DIR/pre_open_init.log 2>&1

# Settlement starts at 15:01. Daily performance follows only after settlement succeeds.
1 15 * * 1-5 cd \$RELAY_HOME && flock -n /tmp/relay-post-close-settlement.lock \$RELAY_HOME/scripts/run-post-close-pipeline.sh >> $CRON_LOG_DIR/post_close_pipeline.log 2>&1
$MARKER_END
EOF
  } | crontab -
  echo "relay trading jobs cron installed"
}

uninstall_cron() {
  without_managed_block | crontab -
  echo "relay trading jobs cron removed"
}

status_cron() {
  current_crontab | sed -n "/^${MARKER_BEGIN}$/,/^${MARKER_END}$/p"
}

case "${1:-status}" in
  install)
    install_cron
    ;;
  uninstall)
    uninstall_cron
    ;;
  status)
    status_cron
    ;;
  *)
    echo "usage: $0 {install|uninstall|status}" >&2
    exit 2
    ;;
esac
