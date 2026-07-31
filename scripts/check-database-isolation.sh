#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_CONFIG="${1:-$ROOT/config/relay.local.yaml}"
PROD_CONFIG="${2:-$ROOT/config/relay.prod.yaml}"

die() {
  printf 'check-database-isolation: %s\n' "$*" >&2
  exit 1
}

config_value() {
  local config="$1"
  local section="$2"
  local key="$3"
  awk -v target_section="$section" -v target_key="$key" '
    $0 ~ "^" target_section ":" { inside = 1; next }
    inside && $0 ~ "^[^[:space:]]" { inside = 0 }
    inside && $0 ~ "^[[:space:]]+" target_key ":" {
      sub(/^[^:]+:[[:space:]]*/, "")
      gsub(/^"|"$/, "")
      print
      exit
    }
  ' "$config"
}

database_identity() {
  local dsn="$1"
  psql "$dsn" -X -At -F '|' -v ON_ERROR_STOP=1 <<'SQL'
SELECT
    COALESCE(inet_server_addr()::text, 'local'),
    inet_server_port(),
    current_database(),
    COALESCE((SELECT max(version) FROM relay_schema_migrations), 0);
SQL
}

check_config() {
  local label="$1"
  local config="$2"
  local expected_environment="$3"
  [[ -f "$config" ]] || die "missing $label config: $config"

  local environment expected_name dsn identity actual_name migration_version
  environment="$(config_value "$config" service environment)"
  expected_name="$(config_value "$config" database expected_name)"
  dsn="$(config_value "$config" database dsn)"
  [[ "$environment" == "$expected_environment" ]] || die "$label service.environment must be $expected_environment"
  [[ -n "$expected_name" ]] || die "$label database.expected_name is required"
  [[ -n "$dsn" ]] || die "$label database.dsn is required"

  identity="$(database_identity "$dsn")"
  actual_name="$(printf '%s' "$identity" | cut -d '|' -f 3)"
  migration_version="$(printf '%s' "$identity" | cut -d '|' -f 4)"
  [[ "$actual_name" == "$expected_name" ]] || die "$label connected to $actual_name, expected $expected_name"
  printf '%s: environment=%s database=%s migration=%s\n' "$label" "$environment" "$actual_name" "$migration_version" >&2
  printf '%s' "$identity"
}

test_identity="$(check_config test "$TEST_CONFIG" test)"
prod_identity="$(check_config production "$PROD_CONFIG" production)"

if [[ "$test_identity" == "$prod_identity" ]]; then
  die "test and production resolve to the same PostgreSQL database"
fi

printf 'database isolation: ok\n'
