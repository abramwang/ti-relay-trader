#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -n "${1:-}" ]]; then
  CONFIG="$1"
  ADMIN_DSN="$(awk '/^database:/ { inside=1; next } inside && /^[^[:space:]]/ { inside=0 } inside && /^[[:space:]]+dsn:/ {sub(/^[^:]+:[[:space:]]*/, ""); gsub(/^"|"$/, ""); print; exit}' "$CONFIG")"
  NAME="relay_cycle_ci_$(date +%Y%m%d%H%M%S)_$$"
  TEMP_DSN="$(RELAY_SOURCE_DSN="$ADMIN_DSN" RELAY_DATABASE_NAME="$NAME" python3 - <<'PY'
import os
from urllib.parse import urlsplit, urlunsplit
p = urlsplit(os.environ['RELAY_SOURCE_DSN'])
if p.scheme not in ('postgres', 'postgresql') or not p.netloc:
    raise SystemExit('PostgreSQL URL required')
print(urlunsplit((p.scheme, p.netloc, '/' + os.environ['RELAY_DATABASE_NAME'], p.query, p.fragment)))
PY
)"
  cleanup_remote() { dropdb --maintenance-db="$ADMIN_DSN" --if-exists --force "$NAME" >/dev/null 2>&1 || true; }
  trap cleanup_remote EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  createdb --maintenance-db="$ADMIN_DSN" "$NAME"
  cd "$ROOT"
  RELAY_CYCLE_REVIEW_TEST_DATABASE_URL="$TEMP_DSN" go test -race ./internal/performance -run TestCycleReview -count=1 -v
  exit
fi
PG_BIN="${RELAY_TEST_PG_BIN:-/usr/lib/postgresql/17/bin}"
WORK="$(mktemp -d /tmp/relay-cycle-review-pg.XXXXXX)"
RUN=()
if [[ "$(id -u)" == 0 ]]; then chown nobody "$WORK"; RUN=(runuser -u nobody --); fi
cleanup() {
  "${RUN[@]}" "$PG_BIN/pg_ctl" -D "$WORK/data" -m immediate -w stop >/dev/null 2>&1 || true
  rm -rf -- "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
"${RUN[@]}" mkdir "$WORK/socket"
"${RUN[@]}" "$PG_BIN/initdb" -D "$WORK/data" -A trust --no-locale -U relay_cycle_test >"$WORK/init.log"
"${RUN[@]}" "$PG_BIN/pg_ctl" -D "$WORK/data" -l "$WORK/server.log" -o "-k $WORK/socket -h '' -p 15436" -w start
cd "$ROOT"
RELAY_CYCLE_REVIEW_TEST_DATABASE_URL="host=$WORK/socket port=15436 user=relay_cycle_test dbname=postgres sslmode=disable" \
  go test -race ./internal/performance -run TestCycleReview -count=1 -v
