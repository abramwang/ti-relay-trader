#!/usr/bin/env python3
"""Read-only visual smoke test for the daily-job review workspace."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from playwright.sync_api import sync_playwright


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--trade-date", default="2026-07-31")
    parser.add_argument("--width", type=int, default=1600)
    parser.add_argument("--height", type=int, default=1000)
    parser.add_argument("--output", default="/tmp/relay-jobs-smoke.png")
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
            lambda response: response_errors.append(f"{response.status} {response.url}")
            if response.status >= 400
            else None,
        )

        page.goto(
            args.base_url.rstrip("/") + "/jobs",
            wait_until="domcontentloaded",
            timeout=30_000,
        )
        page.locator("#reviewTradeDate").fill(args.trade_date)
        page.locator("#reviewTradeDate").dispatch_event("change")
        page.locator("#reviewAccountsBody tr").first.wait_for(
            state="visible", timeout=20_000
        )
        page.wait_for_function(
            """() => document.querySelectorAll('#reviewAccountsBody tr').length === 6""",
            timeout=20_000,
        )
        page.wait_for_timeout(300)

        diagnostics = page.evaluate(
            """() => ({
                reviewRows: document.querySelectorAll('#reviewAccountsBody tr').length,
                jobRows: document.querySelectorAll('#jobRunsBody tr').length,
                reviewStatus: document.querySelector('#reviewStatus')?.textContent || '',
                summary: document.querySelector('#reviewSummary')?.textContent || '',
                selectedDate: document.querySelector('#reviewTradeDate')?.value || '',
                jobTimes: Array.from(
                    document.querySelectorAll('#jobRunsBody tr td:nth-child(5)')
                ).map((cell) => cell.textContent || ''),
                alertHeader: Array.from(document.querySelectorAll('.history-panel th'))
                    .map((cell) => cell.textContent?.trim() || '').includes('告警'),
                alertCells: document.querySelectorAll('#jobRunsBody tr td:nth-child(9)').length,
                alertCardLabels: document.querySelectorAll('.job-card dt').length
                    ? Array.from(document.querySelectorAll('.job-card dt')).filter(
                        (cell) => cell.textContent?.trim() === '告警通知'
                    ).length
                    : 0,
                documentOverflow:
                    document.documentElement.scrollWidth > window.innerWidth,
                reviewTableOverflow:
                    document.querySelector('.review-table-wrap')?.scrollWidth >
                    document.querySelector('.review-table-wrap')?.clientWidth,
            })"""
        )
        page.screenshot(path=str(output), full_page=True)
        browser.close()

    if diagnostics["reviewRows"] != 6 or diagnostics["jobRows"] != 2:
        raise AssertionError(f"daily review rows are incomplete: {diagnostics}")
    if "已阻断" not in diagnostics["reviewStatus"]:
        raise AssertionError(f"review conclusion is not rendered: {diagnostics}")
    if "通过" not in diagnostics["summary"] or "开放差异" not in diagnostics["summary"]:
        raise AssertionError(f"review summary is incomplete: {diagnostics}")
    if diagnostics["selectedDate"] != args.trade_date:
        raise AssertionError(f"trade-date selection did not stick: {diagnostics}")
    if not any("15:05:01" in value for value in diagnostics["jobTimes"]):
        raise AssertionError(f"job times are not rendered in Asia/Shanghai: {diagnostics}")
    if not diagnostics["alertHeader"] or diagnostics["alertCells"] != 2 or diagnostics["alertCardLabels"] != 2:
        raise AssertionError(f"alert delivery state is not rendered: {diagnostics}")
    if diagnostics["documentOverflow"]:
        raise AssertionError(f"page has horizontal document overflow: {diagnostics}")
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
