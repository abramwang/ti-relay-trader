#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
PYTHON="${RELAY_CTP_CONTRACT_PYTHON:-python3}"
: "${RELAY_FUTURES_LOCAL_REDIS_SERVER:?Set an absolute local Redis server binary; no external Redis URL is accepted}"
[[ "$RELAY_FUTURES_LOCAL_REDIS_SERVER" == /* && -x "$RELAY_FUTURES_LOCAL_REDIS_SERVER" ]] || exit 1
PG_BIN="${RELAY_TEST_PG_BIN:-/usr/lib/postgresql/17/bin}"
[[ -x "$PG_BIN/initdb" && -x "$PG_BIN/pg_ctl" ]] || { printf 'PostgreSQL test binaries missing\n' >&2; exit 1; }
"$PYTHON" -c 'import jsonschema' || { printf 'Use the CTP contract venv via RELAY_CTP_CONTRACT_PYTHON\n' >&2; exit 1; }

# Never inherit a previously authorized shared endpoint for ordinary go tests.
unset RELAY_FUTURES_TEST_REDIS_CONFIG RELAY_FUTURES_TEST_REDIS_WRITES RELAY_FUTURES_TEST_DATABASE_URL
mkdir -p outputs/recovery
WORK="$(mktemp -d "$ROOT/outputs/recovery/ctp-offline-$(TZ=Asia/Shanghai date +%Y%m%dT%H%M%S).XXXXXX")"
STAGE=starting
finish() {
  code=$?
  printf '{"exit_status":%s,"last_stage":"%s","broker_acceptance":false,"service_restarted":false}\n' "$code" "$STAGE" >"$WORK/result.json"
  printf '\nOffline rehearsal evidence: %s (exit %s)\n' "$WORK" "$code"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
STAGE=python_review
"$PYTHON" -m unittest discover -s scripts -p test_ctp_query_review.py -v 2>&1 | tee "$WORK/python-review.log"
STAGE=python_contract
"$PYTHON" -m unittest discover -s tests/ctpcontract -v 2>&1 | tee "$WORK/python-contract.log"
STAGE=go_unit
env -u RELAY_FUTURES_LOCAL_REDIS_SERVER go test ./... 2>&1 | tee "$WORK/go-unit.log"
STAGE=go_vet
go vet ./... 2>&1 | tee "$WORK/go-vet.log"
STAGE=postgres_full
env -u RELAY_FUTURES_LOCAL_REDIS_SERVER bash scripts/test-futures-postgres.sh 2>&1 | tee "$WORK/postgres-full.log"
STAGE=isolated_integration
bash scripts/test-futures-postgres.sh -run '^Test(LocalCTPJointRehearsal|LocalRedis)' 2>&1 | tee "$WORK/isolated-integration.log"
STAGE=cli_build
mkdir "$WORK/bin"
for tool in inspect capture query replies trade; do
  go build -o "$WORK/bin/relay-ctp-$tool" "./cmd/relay-ctp-$tool"
  "$WORK/bin/relay-ctp-$tool" --help >"$WORK/$tool-help.log" 2>&1
done
sha256sum "$WORK"/bin/* >"$WORK/binaries.sha256"
STAGE=complete
