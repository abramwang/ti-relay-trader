#!/usr/bin/env python3
"""Validate API Console catalog coverage against schema and registered handlers."""

from __future__ import annotations

import argparse
import json
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[1]
CATALOG_PATH = REPO_ROOT / "cmd" / "relay-docs" / "web" / "static" / "api-console.catalog.json"
SCHEMA_SOURCE_PATH = REPO_ROOT / "internal" / "trading" / "catalog.go"
SERVER_SOURCE_PATH = REPO_ROOT / "internal" / "api" / "server.go"
ALLOWED_METHODS = {"GET", "POST", "PATCH", "PUT", "DELETE"}
ALLOWED_STATUSES = {"ready", "needs-config", "planned", "blocked"}
ALLOWED_FIELD_SOURCES = {"path", "query", "body", "body_json"}


def main() -> int:
    parser = argparse.ArgumentParser(description="Check API Console catalog consistency")
    parser.add_argument("--base-url", default="", help="optionally compare against a running /v1/schema")
    args = parser.parse_args()

    errors: list[str] = []
    catalog = load_catalog(errors)
    registered_routes = source_registered_routes()
    source_schema_routes = source_schema_route_pairs()
    validate_catalog(catalog, registered_routes, source_schema_routes, errors)

    live_schema_routes: set[tuple[str, str]] = set()
    if args.base_url:
        try:
            live_schema_routes = fetch_live_schema_routes(args.base_url)
            catalog_pairs = catalog_route_pairs(catalog)
            for pair in sorted(live_schema_routes - catalog_pairs):
                errors.append(f"live schema route is missing from API catalog: {pair[0]} {pair[1]}")
            if live_schema_routes != source_schema_routes:
                errors.append(
                    "running /v1/schema differs from internal/trading/catalog.go: "
                    f"live={len(live_schema_routes)} source={len(source_schema_routes)}"
                )
        except Exception as exc:  # noqa: BLE001 - report a single consistency failure.
            errors.append(f"live schema check failed: {type(exc).__name__}: {exc}")

    report = {
        "ok": not errors,
        "catalog_entries": len(catalog),
        "catalog_routes": len(catalog_route_pairs(catalog)),
        "source_schema_routes": len(source_schema_routes),
        "registered_handler_patterns": len(registered_routes),
        "live_schema_routes": len(live_schema_routes),
        "errors": errors,
    }
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 1 if errors else 0


def load_catalog(errors: list[str]) -> list[dict[str, Any]]:
    try:
        value = json.loads(CATALOG_PATH.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"cannot load API catalog: {exc}")
        return []
    if not isinstance(value, list):
        errors.append("API catalog root must be a JSON array")
        return []
    return [item for item in value if isinstance(item, dict)]


def source_registered_routes() -> set[str]:
    text = SERVER_SOURCE_PATH.read_text(encoding="utf-8")
    return set(re.findall(r'mux\.HandleFunc\("([^"]+)"', text))


def source_schema_route_pairs() -> set[tuple[str, str]]:
    text = SCHEMA_SOURCE_PATH.read_text(encoding="utf-8")
    return {
        (method.upper(), path)
        for method, path in re.findall(r'\{Method:\s*"([^"]+)",\s*Path:\s*"([^"]+)"', text)
    }


def catalog_route_pairs(catalog: list[dict[str, Any]]) -> set[tuple[str, str]]:
    return {
        (str(item.get("method", "")).upper(), str(item.get("path", "")))
        for item in catalog
        if item.get("method") and item.get("path")
    }


def validate_catalog(
    catalog: list[dict[str, Any]],
    registered_routes: set[str],
    schema_routes: set[tuple[str, str]],
    errors: list[str],
) -> None:
    ids: set[str] = set()
    pairs: set[tuple[str, str]] = set()
    for index, item in enumerate(catalog):
        label = str(item.get("id") or f"index {index}")
        endpoint_id = str(item.get("id") or "").strip()
        method = str(item.get("method") or "").upper()
        path = str(item.get("path") or "").strip()
        status = str(item.get("status") or "").strip()
        fields = item.get("fields")

        if not endpoint_id:
            errors.append(f"catalog {label}: id is required")
        elif endpoint_id in ids:
            errors.append(f"catalog {label}: duplicate id")
        ids.add(endpoint_id)
        if method not in ALLOWED_METHODS:
            errors.append(f"catalog {label}: unsupported method {method!r}")
        if not path.startswith("/"):
            errors.append(f"catalog {label}: path must start with /")
        if status not in ALLOWED_STATUSES:
            errors.append(f"catalog {label}: unsupported status {status!r}")
        pair = (method, path)
        if pair in pairs:
            errors.append(f"catalog {label}: duplicate method/path {method} {path}")
        pairs.add(pair)
        if path and not matches_registered_handler(path, registered_routes):
            errors.append(f"catalog {label}: no Go handler pattern covers {method} {path}")
        if not isinstance(fields, list):
            errors.append(f"catalog {label}: fields must be an array")
            continue

        field_names: set[str] = set()
        path_fields: set[str] = set()
        for field in fields:
            if not isinstance(field, dict):
                errors.append(f"catalog {label}: every field must be an object")
                continue
            name = str(field.get("name") or "").strip()
            source = str(field.get("source") or "").strip()
            if not name:
                errors.append(f"catalog {label}: field name is required")
            elif name in field_names:
                errors.append(f"catalog {label}: duplicate field {name}")
            field_names.add(name)
            if source not in ALLOWED_FIELD_SOURCES:
                errors.append(f"catalog {label}: field {name} has unsupported source {source!r}")
            if source == "path":
                path_fields.add(name)
                if not bool(field.get("required")):
                    errors.append(f"catalog {label}: path field {name} must be required")
        placeholders = set(re.findall(r"\{([^}]+)\}", path))
        if placeholders != path_fields:
            errors.append(
                f"catalog {label}: path placeholders {sorted(placeholders)} do not match path fields {sorted(path_fields)}"
            )

    for method, path in sorted(schema_routes - pairs):
        errors.append(f"source schema route is missing from API catalog: {method} {path}")


def matches_registered_handler(path: str, routes: set[str]) -> bool:
    if path in routes:
        return True
    literal_prefix = path.split("{", 1)[0]
    return any(route.endswith("/") and literal_prefix.startswith(route) for route in routes)


def fetch_live_schema_routes(base_url: str) -> set[tuple[str, str]]:
    url = base_url.rstrip("/") + "/v1/schema"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    request_value = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with opener.open(request_value, timeout=10) as response:
            payload = json.load(response)
    except urllib.error.HTTPError as exc:
        raise RuntimeError(f"HTTP {exc.code} from {url}") from exc
    data = payload.get("data", payload) if isinstance(payload, dict) else {}
    routes = data.get("http_routes", []) if isinstance(data, dict) else []
    return {
        (str(item.get("method", "")).upper(), str(item.get("path", "")))
        for item in routes
        if isinstance(item, dict) and item.get("method") and item.get("path")
    }


if __name__ == "__main__":
    sys.exit(main())
