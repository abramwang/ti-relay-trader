#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/switch-relay-env.sh test
  scripts/switch-relay-env.sh production
  scripts/switch-relay-env.sh production --allow-production-trading

Switch the local 9092 relay service between local test and production
configuration files. The selected config survives container/process restarts,
and the script never prints Redis/PostgreSQL credentials.

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

RUNTIME_SERVICE="$ROOT/scripts/relay-runtime-service.sh"
ACTIVE_CONFIG="$ROOT/.runtime/active-config.yaml"
ACTIVE_ENV_FILE="$ROOT/.runtime/expected-environment"

"$RUNTIME_SERVICE" stop
mkdir -p "$ROOT/.runtime"
ln -sfn "$(realpath "$CONFIG")" "$ACTIVE_CONFIG"
printf '%s\n' "$EXPECTED_ENV" > "$ACTIVE_ENV_FILE"

if [[ "$ALLOW_PROD_TRADING" == "true" ]]; then
  export RELAY_ALLOW_PRODUCTION_TRADING=true
fi
prepare_relay_env
printf 'Starting relay runtime with %s...\n' "$(realpath --relative-to="$ROOT" "$CONFIG" 2>/dev/null || printf '%s' "$CONFIG")"
"$RUNTIME_SERVICE" start

status_summary="$(curl -fsS "http://127.0.0.1:9092/v1/status" | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print("{} {} trading_enabled={}".format(d["environment"], d["status"], d["accounts"]["trading_enabled"]))')"
printf 'Relay switched: %s\n' "$status_summary"
"$RUNTIME_SERVICE" status
