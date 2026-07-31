#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCHIVE="${1:-}"
TRADE_DATE="${2:-}"
CONFIG="${3:-$ROOT/config/relay.prod.yaml}"
REPORT_DIR="${RELAY_RESTORE_REPORT_DIR:-$ROOT/outputs/restore-drills}"
TIMESTAMP="$(TZ=Asia/Shanghai date +%Y%m%dT%H%M%S%z)"
TEMP_DATABASE="relay_restore_${TIMESTAMP//[^0-9]/}_$$"
umask 077

die() {
  printf 'restore-postgres-drill: %s\n' "$*" >&2
  exit 1
}

config_dsn() {
  awk '
    /^database:/ { inside = 1; next }
    inside && /^[^[:space:]]/ { inside = 0 }
    inside && /^[[:space:]]+dsn:/ {
      sub(/^[^:]+:[[:space:]]*/, "")
      gsub(/^"|"$/, "")
      print
      exit
    }
  ' "$1"
}

[[ -n "$ARCHIVE" ]] || die "usage: scripts/restore-postgres-drill.sh <backup.dump> [YYYY-MM-DD] [config]"
[[ -f "$ARCHIVE" ]] || die "backup archive not found: $ARCHIVE"
[[ -f "$CONFIG" ]] || die "config not found: $CONFIG"
MANIFEST="${ARCHIVE%.dump}.manifest.json"
SHA_FILE="$ARCHIVE.sha256"
[[ -f "$MANIFEST" ]] || die "backup manifest not found: $MANIFEST"
[[ -f "$SHA_FILE" ]] || die "backup checksum not found: $SHA_FILE"

(cd "$(dirname "$ARCHIVE")" && sha256sum -c "$(basename "$SHA_FILE")" >/dev/null) || die "backup checksum mismatch"
pg_restore --list "$ARCHIVE" >/dev/null || die "backup archive catalog is invalid"

if [[ -z "$TRADE_DATE" ]]; then
  TRADE_DATE="$(python3 - "$MANIFEST" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    print(json.load(handle)["database_snapshot"].get("raw_received_date_to") or "")
PY
)"
fi
[[ "$TRADE_DATE" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "trade date must be YYYY-MM-DD"

ADMIN_DSN="${RELAY_DATABASE_ADMIN_URL:-$(config_dsn "$CONFIG")}"
[[ -n "$ADMIN_DSN" ]] || die "database admin DSN is required"
TEMP_DSN="$({
  RELAY_SOURCE_DSN="$ADMIN_DSN" RELAY_DATABASE_NAME="$TEMP_DATABASE" python3 - <<'PY'
import os
from urllib.parse import urlsplit, urlunsplit

source = os.environ["RELAY_SOURCE_DSN"]
name = os.environ["RELAY_DATABASE_NAME"]
parts = urlsplit(source)
if parts.scheme not in {"postgres", "postgresql"} or not parts.netloc:
    raise SystemExit("restore drill requires a PostgreSQL URL DSN")
print(urlunsplit((parts.scheme, parts.netloc, "/" + name, parts.query, parts.fragment)))
PY
} 2>/dev/null)" || die "could not derive restore database DSN"

cleanup() {
  dropdb --maintenance-db="$ADMIN_DSN" --if-exists --force "$TEMP_DATABASE" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

printf 'creating temporary restore database %s\n' "$TEMP_DATABASE"
createdb --maintenance-db="$ADMIN_DSN" "$TEMP_DATABASE"
printf 'restoring backup archive\n'
SERVER_VERSION_NUM="$(psql "$TEMP_DSN" -X -At -v ON_ERROR_STOP=1 -c "SELECT current_setting('server_version_num')::integer")"
SERVER_MAJOR="$((SERVER_VERSION_NUM / 10000))"
RESTORE_MAJOR="$(pg_restore --version | sed -E 's/.* ([0-9]+)(\..*)?$/\1/')"
if (( RESTORE_MAJOR > SERVER_MAJOR && SERVER_MAJOR < 17 )); then
  printf 'using PostgreSQL %s compatibility restore path for pg_restore %s\n' "$SERVER_MAJOR" "$RESTORE_MAJOR"
  pg_restore \
    --exit-on-error \
    --no-owner \
    --no-acl \
    --file=- \
    "$ARCHIVE" \
    | sed '/^SET transaction_timeout = 0;$/d' \
    | psql "$TEMP_DSN" -X -v ON_ERROR_STOP=1 >/dev/null
else
  pg_restore \
    --exit-on-error \
    --no-owner \
    --no-acl \
    --dbname="$TEMP_DSN" \
    "$ARCHIVE"
fi

RESTORED_SNAPSHOT="$(psql "$TEMP_DSN" -X -At -v ON_ERROR_STOP=1 <<'SQL'
SELECT jsonb_build_object(
    'database', current_database(),
    'schema_version', COALESCE((SELECT max(version) FROM relay_schema_migrations), 0),
    'unvalidated_constraints', (SELECT count(*) FROM pg_constraint WHERE NOT convalidated),
    'table_counts', jsonb_build_object(
        'accounts', (SELECT count(*) FROM accounts),
        'orders', (SELECT count(*) FROM orders),
        'order_events', (SELECT count(*) FROM order_events),
        'fills', (SELECT count(*) FROM fills),
        'component_transfers', (SELECT count(*) FROM etf_component_transfers),
        'raw_stream_messages', (SELECT count(*) FROM raw_stream_messages),
        'asset_snapshots', (SELECT count(*) FROM asset_snapshots),
        'positions', (SELECT count(*) FROM positions),
        'position_snapshots', (SELECT count(*) FROM position_snapshots),
        'job_runs', (SELECT count(*) FROM job_runs),
        'reconciliation_runs', (SELECT count(*) FROM reconciliation_runs),
        'performance_nav_versions', (SELECT count(*) FROM performance_nav_versions)
    )
)::text;
SQL
)"

RELAY_MANIFEST="$MANIFEST" RELAY_RESTORED_SNAPSHOT="$RESTORED_SNAPSHOT" python3 - <<'PY'
import json
import os

with open(os.environ["RELAY_MANIFEST"], encoding="utf-8") as handle:
    expected = json.load(handle)["database_snapshot"]
actual = json.loads(os.environ["RELAY_RESTORED_SNAPSHOT"])
if expected["schema_version"] != actual["schema_version"]:
    raise SystemExit(f"schema version mismatch: {actual['schema_version']} != {expected['schema_version']}")
if expected["unvalidated_constraints"] != actual["unvalidated_constraints"]:
    raise SystemExit("restored constraint validation state does not match backup manifest")
if expected["table_counts"] != actual["table_counts"]:
    raise SystemExit("restored table counts do not match backup manifest")
PY

trade_date_sql="$(printf '%s' "$TRADE_DATE" | sed "s/'/''/g")"
BEFORE_REPLAY="$(psql "$TEMP_DSN" -X -At -v ON_ERROR_STOP=1 <<SQL
SELECT jsonb_build_object(
    'trade_date', '$trade_date_sql',
    'raw_stream_messages', (SELECT count(*) FROM raw_stream_messages WHERE (received_at AT TIME ZONE 'Asia/Shanghai')::date = '$trade_date_sql'::date),
    'orders', (SELECT count(*) FROM orders WHERE trade_date = '$trade_date_sql'::date),
    'order_events', (SELECT count(*) FROM order_events WHERE trade_date = '$trade_date_sql'::date),
    'fills', (SELECT count(*) FROM fills WHERE trade_date = '$trade_date_sql'::date),
    'component_transfers', (SELECT count(*) FROM etf_component_transfers WHERE trade_date = '$trade_date_sql'::date)
)::text;
SQL
)"

mkdir -p "$REPORT_DIR"
chmod 700 "$REPORT_DIR"
REPLAY_REPORT="$REPORT_DIR/replay_${TRADE_DATE}_${TIMESTAMP}.json"
printf 'replaying archived ledger for %s\n' "$TRADE_DATE"
(
  cd "$ROOT"
  RELAY_DATABASE_URL="$TEMP_DSN" go run ./cmd/relayctl ledger-replay \
    -date-from "$TRADE_DATE" \
    -date-to "$TRADE_DATE" \
    -timeout 30m
) > "$REPLAY_REPORT"

AFTER_REPLAY="$(psql "$TEMP_DSN" -X -At -v ON_ERROR_STOP=1 <<SQL
SELECT jsonb_build_object(
    'trade_date', '$trade_date_sql',
    'raw_stream_messages', (SELECT count(*) FROM raw_stream_messages WHERE (received_at AT TIME ZONE 'Asia/Shanghai')::date = '$trade_date_sql'::date),
    'orders', (SELECT count(*) FROM orders WHERE trade_date = '$trade_date_sql'::date),
    'order_events', (SELECT count(*) FROM order_events WHERE trade_date = '$trade_date_sql'::date),
    'fills', (SELECT count(*) FROM fills WHERE trade_date = '$trade_date_sql'::date),
    'component_transfers', (SELECT count(*) FROM etf_component_transfers WHERE trade_date = '$trade_date_sql'::date)
)::text;
SQL
)"

QUALITY="$(psql "$TEMP_DSN" -X -At -v ON_ERROR_STOP=1 <<'SQL'
SELECT jsonb_build_object(
    'unvalidated_constraints', (SELECT count(*) FROM pg_constraint WHERE NOT convalidated),
    'orphan_order_events', (
        SELECT count(*) FROM order_events event
        LEFT JOIN orders ON orders.account_id = event.account_id
            AND orders.trade_date = event.trade_date
            AND orders.gateway_order_id = event.gateway_order_id
        WHERE orders.order_pk IS NULL
    ),
    'orphan_fills', (
        SELECT count(*) FROM fills fill
        LEFT JOIN orders ON orders.account_id = fill.account_id
            AND orders.trade_date = fill.trade_date
            AND orders.gateway_order_id = fill.gateway_order_id
        WHERE orders.order_pk IS NULL
    ),
    'duplicate_order_idempotency_groups', (
        SELECT count(*) FROM (
            SELECT account_id, idempotency_key
            FROM orders
            WHERE idempotency_key IS NOT NULL
            GROUP BY account_id, idempotency_key
            HAVING count(*) > 1
        ) duplicate_keys
    )
)::text;
SQL
)"

DRILL_REPORT="$REPORT_DIR/restore_${TRADE_DATE}_${TIMESTAMP}.json"
RELAY_MANIFEST="$MANIFEST" \
RELAY_RESTORED_SNAPSHOT="$RESTORED_SNAPSHOT" \
RELAY_BEFORE_REPLAY="$BEFORE_REPLAY" \
RELAY_AFTER_REPLAY="$AFTER_REPLAY" \
RELAY_QUALITY="$QUALITY" \
RELAY_REPLAY_REPORT="$REPLAY_REPORT" \
RELAY_TRADE_DATE="$TRADE_DATE" \
RELAY_DRILL_TIMESTAMP="$TIMESTAMP" \
python3 - "$DRILL_REPORT" <<'PY'
import json
import os
import sys

with open(os.environ["RELAY_MANIFEST"], encoding="utf-8") as handle:
    backup = json.load(handle)
with open(os.environ["RELAY_REPLAY_REPORT"], encoding="utf-8") as handle:
    replay = json.load(handle)
before = json.loads(os.environ["RELAY_BEFORE_REPLAY"])
after = json.loads(os.environ["RELAY_AFTER_REPLAY"])
quality = json.loads(os.environ["RELAY_QUALITY"])
issues = []
if before != after:
    issues.append("trade-date row counts changed after idempotent replay")
expected_unvalidated = backup["database_snapshot"].get("unvalidated_constraints", 0)
if quality.get("unvalidated_constraints") != expected_unvalidated:
    issues.append("constraint validation state changed after restore/replay")
for key, value in quality.items():
    if key == "unvalidated_constraints":
        continue
    if value:
        issues.append(f"{key}={value}")
report = {
    "format": "relay.postgres.restore_drill.v1",
    "status": "passed" if not issues else "failed",
    "trade_date": os.environ["RELAY_TRADE_DATE"],
    "executed_at": os.environ["RELAY_DRILL_TIMESTAMP"],
    "backup": backup["archive"],
    "restored_snapshot": json.loads(os.environ["RELAY_RESTORED_SNAPSHOT"]),
    "before_replay": before,
    "after_replay": after,
    "quality": quality,
    "replay": replay,
    "issues": issues,
}
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump(report, handle, ensure_ascii=True, indent=2, sort_keys=True)
    handle.write("\n")
if issues:
    raise SystemExit("; ".join(issues))
PY

rm -f "$REPLAY_REPORT"
printf 'restore drill: passed\n'
printf 'restore report: %s\n' "$DRILL_REPORT"
