#!/usr/bin/env python3
"""Smoke test key 9092 pages, static assets, API probes, and SDK downloads."""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_TIMEOUT_SECONDS = 10.0


@dataclass(frozen=True)
class Response:
    url: str
    status: int
    headers: dict[str, str]
    body: bytes
    elapsed_ms: float

    @property
    def text(self) -> str:
        return self.body.decode("utf-8", errors="replace")


@dataclass(frozen=True)
class Check:
    name: str
    path: str
    mode: str
    contains: tuple[str, ...] = ()
    min_bytes: int = 1
    expected_json: tuple[str, ...] = ()


class SmokeFailure(RuntimeError):
    pass


def main() -> int:
    parser = argparse.ArgumentParser(description="Run 9092 page smoke checks")
    parser.add_argument("--base-url", default="http://127.0.0.1:9092", help="relay 9092 base URL")
    parser.add_argument("--timeout", type=float, default=DEFAULT_TIMEOUT_SECONDS, help="per-request timeout in seconds")
    parser.add_argument("--sdk-version", default="", help="SDK version to check; defaults to sdk/python/pyproject.toml")
    args = parser.parse_args()

    base_url = args.base_url.rstrip("/")
    sdk_version = args.sdk_version.strip() or read_sdk_version()
    checks = build_checks(sdk_version)
    results: list[dict[str, Any]] = []
    failed = False

    for check in checks:
        started = time.perf_counter()
        try:
            response = fetch(base_url, check.path, timeout=args.timeout)
            validate(check, response)
            results.append(
                {
                    "name": check.name,
                    "path": check.path,
                    "ok": True,
                    "status": response.status,
                    "bytes": len(response.body),
                    "elapsed_ms": round(response.elapsed_ms, 3),
                }
            )
        except Exception as exc:  # noqa: BLE001 - this script reports all smoke failures uniformly.
            failed = True
            results.append(
                {
                    "name": check.name,
                    "path": check.path,
                    "ok": False,
                    "error": str(exc),
                    "elapsed_ms": round((time.perf_counter() - started) * 1000, 3),
                }
            )

    report = {
        "ok": not failed,
        "base_url": base_url,
        "sdk_version": sdk_version,
        "checks": results,
    }
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 1 if failed else 0


def read_sdk_version() -> str:
    pyproject = REPO_ROOT / "sdk" / "python" / "pyproject.toml"
    for line in pyproject.read_text(encoding="utf-8").splitlines():
        if line.strip().startswith("version"):
            _, value = line.split("=", 1)
            return value.strip().strip('"')
    raise SmokeFailure(f"could not read SDK version from {pyproject}")


def build_checks(sdk_version: str) -> list[Check]:
    return [
        Check("home", "/", "text", ("relay 文档门户", "/api-console", "/trade", "/docs/python-sdk")),
        Check("docs-readme", "/docs/readme", "text", ("线程恢复卡片", "当前进展")),
        Check("tests-index", "/tests", "text", ("测试目录索引", "tests/integration")),
        Check("api-console", "/api-console", "text", ("接口测试台", "api-console-v2", "api-console.js")),
        Check("trade-terminal", "/trade", "text", ("Relay Trader", "trade-terminal.js", "绩效分析")),
        Check("job-status", "/jobs", "text", ("后台任务状态", "job-status.js", "盘前初始化")),
        Check("api-console-css", "/assets/api-console.css", "text", (".api-console-v2",)),
        Check("api-console-js", "/assets/api-console.js", "text", ("api-console.catalog.json", "fetch")),
        Check("trade-terminal-css", "/assets/trade-terminal.css", "text", (".terminal-shell",)),
        Check("trade-terminal-js", "/assets/trade-terminal.js", "text", ("loadBars", "echarts")),
        Check("job-status-css", "/assets/job-status.css", "text", (".jobs-status",)),
        Check("job-status-js", "/assets/job-status.js", "text", ("loadJobs", "/v1/jobs/runs")),
        Check("echarts", "/assets/echarts.min.js", "bytes", min_bytes=100_000),
        Check("api-console-catalog", "/assets/api-console.catalog.json", "json", expected_json=("status", "meridian-bars")),
        Check("healthz", "/healthz", "json", expected_json=("status", "ok")),
        Check("v1-status", "/v1/status", "json", expected_json=("ok", "dependencies")),
        Check(
            "sdk-sha256",
            f"/sdk/relay-sdk-{sdk_version}.tar.gz.sha256",
            "text",
            (f"relay-sdk-{sdk_version}.tar.gz",),
        ),
        Check("sdk-archive", f"/sdk/relay-sdk-{sdk_version}.tar.gz", "bytes", min_bytes=8_000),
    ]


def fetch(base_url: str, path: str, *, timeout: float) -> Response:
    url = urllib.parse.urljoin(base_url + "/", path.lstrip("/"))
    request = urllib.request.Request(url, headers={"User-Agent": "relay-page-smoke/1.0"})
    started = time.perf_counter()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(request, timeout=timeout) as response:
            body = response.read()
            status = int(response.status)
            headers = {key.lower(): value for key, value in response.headers.items()}
    except urllib.error.HTTPError as exc:
        body = exc.read()
        raise SmokeFailure(f"HTTP {exc.code} for {url}: {body[:240]!r}") from exc
    except urllib.error.URLError as exc:
        raise SmokeFailure(f"request failed for {url}: {exc}") from exc
    return Response(
        url=url,
        status=status,
        headers=headers,
        body=body,
        elapsed_ms=(time.perf_counter() - started) * 1000,
    )


def validate(check: Check, response: Response) -> None:
    if response.status != 200:
        raise SmokeFailure(f"expected HTTP 200, got {response.status}")
    if len(response.body) < check.min_bytes:
        raise SmokeFailure(f"expected at least {check.min_bytes} bytes, got {len(response.body)}")

    if check.mode == "bytes":
        return
    if check.mode == "text":
        text = response.text
        missing = [needle for needle in check.contains if needle not in text]
        if missing:
            raise SmokeFailure(f"missing text markers: {missing}")
        return
    if check.mode == "json":
        try:
            payload = json.loads(response.text)
        except json.JSONDecodeError as exc:
            raise SmokeFailure(f"invalid JSON: {exc}") from exc
        flattened = json.dumps(payload, ensure_ascii=False, sort_keys=True)
        missing = [needle for needle in check.expected_json if needle not in flattened]
        if missing:
            raise SmokeFailure(f"missing JSON markers: {missing}")
        return
    raise SmokeFailure(f"unknown check mode {check.mode!r}")


if __name__ == "__main__":
    sys.exit(main())
