#!/usr/bin/env python3
"""Read-only visual smoke test for Relay runtime operations."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from playwright.sync_api import sync_playwright


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--width", type=int, default=1600)
    parser.add_argument("--height", type=int, default=1100)
    parser.add_argument("--output", default="/tmp/relay-operations-smoke.png")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    console_errors: list[str] = []
    page_errors: list[str] = []
    response_errors: list[str] = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(
            viewport={"width": args.width, "height": args.height},
            device_scale_factor=1,
        )
        page.on(
            "console",
            lambda message: console_errors.append(message.text)
            if message.type == "error"
            else None,
        )
        page.on("pageerror", lambda error: page_errors.append(str(error)))
        page.on(
            "response",
            lambda response: response_errors.append(
                f"{response.status} {response.url}"
            )
            if response.status >= 400
            else None,
        )

        page.goto(
            args.base_url.rstrip("/") + "/operations",
            wait_until="domcontentloaded",
            timeout=30_000,
        )
        page.locator("#gatewayBody tr").first.wait_for(
            state="visible", timeout=20_000
        )
        page.wait_for_function(
            """() => {
                const updated = document.querySelector("#operationsUpdatedAt")
                    ?.textContent || "";
                return updated.includes("刷新");
            }""",
            timeout=20_000,
        )
        page.wait_for_timeout(300)

        diagnostics = page.evaluate(
            """() => ({
                gatewayRows: document.querySelectorAll("#gatewayBody tr").length,
                streamRows: document.querySelectorAll("#streamBody tr").length,
                accountOptions:
                    document.querySelectorAll("#operationsAccountFilter option").length,
                monitoringWindow:
                    document.querySelector("#monitoringWindow")?.textContent || "",
                writeMode:
                    document.querySelector("#dlqWriteMode")?.textContent || "",
                pendingDLQ:
                    document.querySelector("#pendingDLQ")?.textContent || "",
                bodyOverflow:
                    document.documentElement.scrollWidth > window.innerWidth,
                panelWidths: Array.from(
                    document.querySelectorAll(".operations-panel")
                ).map((panel) => Math.round(panel.getBoundingClientRect().width)),
            })"""
        )
        page.screenshot(path=str(output), full_page=True)
        browser.close()

    if diagnostics["gatewayRows"] != 6:
        raise AssertionError(f"expected six gateways: {diagnostics}")
    if diagnostics["streamRows"] != 24:
        raise AssertionError(f"expected 24 output streams: {diagnostics}")
    if diagnostics["accountOptions"] != 7:
        raise AssertionError(f"account selector is incomplete: {diagnostics}")
    if "只读" not in diagnostics["writeMode"]:
        raise AssertionError(f"production operation guard is unclear: {diagnostics}")
    if diagnostics["bodyOverflow"]:
        raise AssertionError(f"page has horizontal overflow: {diagnostics}")
    if any(width < 1000 for width in diagnostics["panelWidths"]):
        raise AssertionError(f"operations panels are undersized: {diagnostics}")
    if console_errors or page_errors or response_errors:
        raise AssertionError(
            json.dumps(
                {
                    "console_errors": console_errors,
                    "page_errors": page_errors,
                    "response_errors": response_errors,
                    "diagnostics": diagnostics,
                },
                ensure_ascii=False,
                indent=2,
            )
        )

    print(
        json.dumps(
            {"screenshot": str(output), "diagnostics": diagnostics},
            ensure_ascii=False,
            indent=2,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
