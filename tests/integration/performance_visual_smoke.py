#!/usr/bin/env python3
"""Read-only visual smoke test for the Relay performance workspace."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from playwright.sync_api import sync_playwright


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--date-from", default="20260701")
    parser.add_argument("--date-to", default="20260729")
    parser.add_argument("--width", type=int, default=1600)
    parser.add_argument("--height", type=int, default=1280)
    parser.add_argument("--output", default="/tmp/relay-performance-smoke.png")
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
            args.base_url.rstrip("/") + "/trade#performance",
            wait_until="domcontentloaded",
            timeout=30_000,
        )
        page.locator("#performancePanel").wait_for(state="visible", timeout=15_000)
        page.wait_for_function(
            """() => {
                const status =
                    document.querySelector("#performanceStatus")?.textContent || "";
                return !document.querySelector("#loadPerformanceButton")?.disabled &&
                    (status.includes("已加载") || status.includes("查询失败"));
            }""",
            timeout=60_000,
        )
        page.locator("#perfDateFrom").fill(args.date_from)
        page.locator("#perfDateTo").fill(args.date_to)
        page.locator("#perfBenchmarkInput").fill("000001.SH")
        page.locator("#loadPerformanceButton").click()
        page.wait_for_function(
            """() => (
                document.querySelector("#performanceStatus")?.textContent || ""
            ).includes("查询中")""",
            timeout=10_000,
        )
        expected_range = {
            "dateFrom": (
                f"{args.date_from[:4]}-{args.date_from[4:6]}-{args.date_from[6:]}"
            ),
            "dateTo": f"{args.date_to[:4]}-{args.date_to[4:6]}-{args.date_to[6:]}",
        }
        page.wait_for_function(
            """(expected) => {
                const text = document.querySelector("#performanceStatus")?.textContent || "";
                const button = document.querySelector("#loadPerformanceButton");
                const hint =
                    document.querySelector("#performanceRangeHint")?.textContent || "";
                return !button?.disabled &&
                    (text.includes("已加载") || text.includes("查询失败")) &&
                    hint.includes(expected.dateFrom) &&
                    hint.includes(expected.dateTo);
            }""",
            arg=expected_range,
            timeout=60_000,
        )
        status = page.locator("#performanceStatus").inner_text()
        if "查询失败" in status:
            raise AssertionError(status)
        page.locator("#performanceChart canvas").wait_for(
            state="visible", timeout=15_000
        )
        page.wait_for_timeout(400)

        diagnostics = page.evaluate(
            """() => {
                const canvas = document.querySelector("#performanceChart canvas");
                const shell = document.querySelector("#terminalShell");
                const pixels = canvas
                    ? canvas.getContext("2d").getImageData(
                        0, 0, canvas.width, canvas.height
                    ).data
                    : [];
                let painted = 0;
                for (let index = 0; index < pixels.length; index += 16) {
                    if (pixels[index + 3] > 0) painted += 1;
                }
                return {
                    chartWidth: canvas?.width || 0,
                    chartHeight: canvas?.height || 0,
                    paintedSamples: painted,
                    qualityItems: document.querySelectorAll(
                        ".performance-quality-item"
                    ).length,
                    horizontalOverflow:
                        document.documentElement.scrollWidth > window.innerWidth,
                    shellWidth: shell?.getBoundingClientRect().width || 0,
                    status:
                        document.querySelector("#performanceStatus")?.textContent || "",
                    rangeHint:
                        document.querySelector("#performanceRangeHint")?.textContent || "",
                    qualityStatus:
                        document.querySelector("#performanceQualityStatus")
                            ?.textContent || "",
                };
            }"""
        )
        page.screenshot(path=str(output), full_page=False)
        browser.close()

    if diagnostics["chartWidth"] < 500 or diagnostics["chartHeight"] < 250:
        raise AssertionError(f"performance chart is undersized: {diagnostics}")
    if diagnostics["paintedSamples"] < 500:
        raise AssertionError(f"performance chart is blank: {diagnostics}")
    if diagnostics["qualityItems"] != 7:
        raise AssertionError(f"quality checks are incomplete: {diagnostics}")
    if diagnostics["horizontalOverflow"]:
        raise AssertionError(f"page has horizontal overflow: {diagnostics}")
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
