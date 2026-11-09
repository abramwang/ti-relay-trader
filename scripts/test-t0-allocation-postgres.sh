#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PG_BIN="${RELAY_TEST_PG_BIN:-/usr/lib/postgresql/17/bin}"
for tool in initdb pg_ctl; do
  [[ -x "$PG_BIN/$tool" ]] || { printf 'Missing %s/%s\n' "$PG_BIN" "$tool" >&2; exit 1; }
done
# Unix-socket-only disposable cluster. Never read service configuration or credentials.
WORK="$(mktemp -d /tmp/relay-t0-pg.XXXXXX)"
RUN=()
if [[ "$(id -u)" == 0 ]]; then
  chown nobody "$WORK"
  RUN=(runuser -u nobody --)
fi
cleanup() {
  "${RUN[@]}" "$PG_BIN/pg_ctl" -D "$WORK/data" -m immediate -w stop >/dev/null 2>&1 || true
  rm -rf -- "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
"${RUN[@]}" mkdir "$WORK/socket"
"${RUN[@]}" "$PG_BIN/initdb" -D "$WORK/data" -A trust --no-locale -U relay_t0_test >"$WORK/init.log"
"${RUN[@]}" "$PG_BIN/pg_ctl" -D "$WORK/data" -l "$WORK/server.log" -o "-k $WORK/socket -h '' -p 15433" -w start
cd "$ROOT"
RELAY_T0_ALLOCATION_TEST_DATABASE_URL="host=$WORK/socket port=15433 user=relay_t0_test dbname=postgres sslmode=disable" \
  go test -race ./internal/ledger -run TestT0Allocation -count=1 -v
