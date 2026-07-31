#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="${1:-$ROOT/config/relay.local.yaml}"
TEMP_DATABASE="relay_trader_ci_$(date +%Y%m%d%H%M%S)_$$"

die() {
  printf 'test-postgres-integration: %s\n' "$*" >&2
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

[[ -f "$CONFIG" ]] || die "missing config: $CONFIG"
ADMIN_DSN="${RELAY_DATABASE_ADMIN_URL:-$(config_dsn "$CONFIG")}"
[[ -n "$ADMIN_DSN" ]] || die "set RELAY_DATABASE_ADMIN_URL or configure database.dsn"

TEMP_DSN="$({
  RELAY_SOURCE_DSN="$ADMIN_DSN" RELAY_DATABASE_NAME="$TEMP_DATABASE" python3 - <<'PY'
import os
from urllib.parse import urlsplit, urlunsplit

source = os.environ["RELAY_SOURCE_DSN"]
name = os.environ["RELAY_DATABASE_NAME"]
parts = urlsplit(source)
if parts.scheme not in {"postgres", "postgresql"} or not parts.netloc:
    raise SystemExit("temporary database test requires a PostgreSQL URL DSN")
print(urlunsplit((parts.scheme, parts.netloc, "/" + name, parts.query, parts.fragment)))
PY
} 2>/dev/null)" || die "could not derive temporary database DSN"

cleanup() {
  dropdb --maintenance-db="$ADMIN_DSN" --if-exists --force "$TEMP_DATABASE" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

printf 'creating temporary PostgreSQL database %s\n' "$TEMP_DATABASE"
createdb --maintenance-db="$ADMIN_DSN" "$TEMP_DATABASE"

cd "$ROOT"
RELAY_DATABASE_URL="$TEMP_DSN" go run ./cmd/relayctl migrate up >/dev/null
LATEST_VERSION="$(find migrations/postgres -maxdepth 1 -type f -name '*.up.sql' -printf '%f\n' | sort | tail -n 1 | cut -d '_' -f 1 | sed 's/^0*//')"
[[ -n "$LATEST_VERSION" ]] || die "could not determine latest migration version"
RELAY_DATABASE_URL="$TEMP_DSN" go run ./cmd/relayctl migrate status | grep -q "\"version\": $LATEST_VERSION" || die "latest migration is missing"
RELAY_LEDGER_TEST_DATABASE_URL="$TEMP_DSN" go test ./internal/ledger -run TestRepositoryWritesToPostgres -count=1 -v
go test ./tests/unit -run Migration -count=1

printf 'temporary PostgreSQL migration/repository integration: ok\n'
