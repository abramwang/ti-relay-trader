#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="${1:-$ROOT/config/relay.prod.yaml}"
OUTPUT_DIR="${2:-$ROOT/outputs/backups}"
TIMESTAMP="$(TZ=Asia/Shanghai date +%Y%m%dT%H%M%S%z)"
umask 077

die() {
  printf 'backup-postgres: %s\n' "$*" >&2
  exit 1
}

config_value() {
  local section="$1"
  local key="$2"
  awk -v target_section="$section" -v target_key="$key" '
    $0 ~ "^" target_section ":" { inside = 1; next }
    inside && $0 ~ "^[^[:space:]]" { inside = 0 }
    inside && $0 ~ "^[[:space:]]+" target_key ":" {
      sub(/^[^:]+:[[:space:]]*/, "")
      gsub(/^"|"$/, "")
      print
      exit
    }
  ' "$CONFIG"
}

[[ -f "$CONFIG" ]] || die "missing config: $CONFIG"
DSN="${RELAY_DATABASE_URL:-$(config_value database dsn)}"
EXPECTED_NAME="$(config_value database expected_name)"
[[ -n "$DSN" ]] || die "database DSN is required"
[[ "$EXPECTED_NAME" =~ ^[A-Za-z0-9_]+$ ]] || die "database.expected_name is missing or unsafe"

ACTUAL_NAME="$(psql "$DSN" -X -At -v ON_ERROR_STOP=1 -c 'SELECT current_database()')"
[[ "$ACTUAL_NAME" == "$EXPECTED_NAME" ]] || die "connected to $ACTUAL_NAME, expected $EXPECTED_NAME"

mkdir -p "$OUTPUT_DIR"
chmod 700 "$OUTPUT_DIR"
BASE="$OUTPUT_DIR/${ACTUAL_NAME}_${TIMESTAMP}"
ARCHIVE="$BASE.dump"
PARTIAL="$ARCHIVE.partial"
MANIFEST="$BASE.manifest.json"
SHA_FILE="$ARCHIVE.sha256"

cleanup() {
  rm -f "$PARTIAL" "$MANIFEST.partial"
}
trap cleanup EXIT INT TERM

DATABASE_SNAPSHOT="$(psql "$DSN" -X -At -v ON_ERROR_STOP=1 <<'SQL'
SELECT jsonb_build_object(
    'database', current_database(),
    'server_version', current_setting('server_version'),
    'server_version_num', current_setting('server_version_num')::integer,
    'captured_at', to_char(clock_timestamp() AT TIME ZONE 'Asia/Shanghai', 'YYYY-MM-DD"T"HH24:MI:SS.US'),
    'schema_version', COALESCE((SELECT max(version) FROM relay_schema_migrations), 0),
    'unvalidated_constraints', (SELECT count(*) FROM pg_constraint WHERE NOT convalidated),
    'database_size_bytes', pg_database_size(current_database()),
    'raw_received_date_from', (SELECT min((received_at AT TIME ZONE 'Asia/Shanghai')::date) FROM raw_stream_messages),
    'raw_received_date_to', (SELECT max((received_at AT TIME ZONE 'Asia/Shanghai')::date) FROM raw_stream_messages),
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

printf 'backing up PostgreSQL database %s\n' "$ACTUAL_NAME"
pg_dump "$DSN" \
  --format=custom \
  --compress=zstd:6 \
  --no-owner \
  --no-acl \
  --lock-wait-timeout=10s \
  --file="$PARTIAL"
mv "$PARTIAL" "$ARCHIVE"

ARCHIVE_SHA256="$(sha256sum "$ARCHIVE" | awk '{print $1}')"
ARCHIVE_BYTES="$(stat -c '%s' "$ARCHIVE")"
printf '%s  %s\n' "$ARCHIVE_SHA256" "$(basename "$ARCHIVE")" > "$SHA_FILE"

RELAY_DATABASE_SNAPSHOT="$DATABASE_SNAPSHOT" \
RELAY_ARCHIVE_FILE="$(basename "$ARCHIVE")" \
RELAY_ARCHIVE_SHA256="$ARCHIVE_SHA256" \
RELAY_ARCHIVE_BYTES="$ARCHIVE_BYTES" \
RELAY_PG_DUMP_VERSION="$(pg_dump --version)" \
python3 - "$MANIFEST.partial" <<'PY'
import json
import os
import sys

manifest = {
    "format": "relay.postgres.backup.v1",
    "archive": {
        "file": os.environ["RELAY_ARCHIVE_FILE"],
        "sha256": os.environ["RELAY_ARCHIVE_SHA256"],
        "bytes": int(os.environ["RELAY_ARCHIVE_BYTES"]),
        "pg_dump_version": os.environ["RELAY_PG_DUMP_VERSION"],
    },
    "database_snapshot": json.loads(os.environ["RELAY_DATABASE_SNAPSHOT"]),
}
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    json.dump(manifest, handle, ensure_ascii=True, indent=2, sort_keys=True)
    handle.write("\n")
PY
mv "$MANIFEST.partial" "$MANIFEST"

printf 'backup archive: %s\n' "$ARCHIVE"
printf 'backup manifest: %s\n' "$MANIFEST"
printf 'backup sha256: %s\n' "$SHA_FILE"
printf 'backup bytes: %s\n' "$ARCHIVE_BYTES"
