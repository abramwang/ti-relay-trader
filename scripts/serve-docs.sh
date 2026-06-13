#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
exec go run ./cmd/relay-docs -addr "${RELAY_DOCS_ADDR:-0.0.0.0:9092}" -root .
