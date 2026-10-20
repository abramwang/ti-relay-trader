#!/usr/bin/env python3
"""Read-only regression: published metrics, progressive details and cancellation."""

from __future__ import annotations

import argparse
import json
from time import perf_counter

from playwright.sync_api import Error, sync_playwright


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--account-id", default="314000046830")
    parser.add_argument("--date-from", default="20260101")
    parser.add_argument("--date-to", default="20261019")
    parser.add_argument("--replacement-from", default="20260920")
    args = parser.parse_args()
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1600, "height": 1100})
        errors: list[str] = []
        requests: list[str] = []
        failures: list[str] = []
        held = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.on("request", lambda request: requests.append(request.url))
        page.on("requestfailed", lambda request: failures.append(request.url))

        def route_request(route):
            if route.request.method != "GET":
                route.abort()
            elif "/performance/trade-quality?" in route.request.url and f"date_from={args.date_from}" in route.request.url:
                held.append(route)
            else:
                route.continue_()

        page.route("**/v1/**", route_request)
        page.goto(args.base_url.rstrip("/") + "/trade#performance", wait_until="domcontentloaded")
        page.locator(f'#accountTabs button[title*="{args.account_id}"]').click()
        page.wait_for_function(
            """() => !document.querySelector('#loadPerformanceButton').disabled &&
                document.querySelector('#performanceStatus').textContent.includes('已加载') &&
                !document.querySelector('#performanceStatus').textContent.includes('明细计算中')""",
            timeout=90_000,
        )
        requests.clear()
        page.locator("#perfDateFrom").fill(args.date_from)
        page.locator("#perfDateTo").fill(args.date_to)
        started = perf_counter()
        page.locator("#loadPerformanceButton").click()
        page.wait_for_function(
            """() => document.querySelector('#performanceStatus').textContent.includes('明细计算中') &&
                !document.querySelector('#loadPerformanceButton').disabled &&
                document.querySelector('#perfAccountDayPnl').textContent !== '--'""",
            timeout=5_000,
        )
        elapsed = round((perf_counter() - started) * 1000, 1)
        page.wait_for_timeout(100)
        assert held, "quality request was not held"
        assert not any("/economic-nav/preview?" in url for url in requests), requests
        primary = page.locator("#perfAccountDayPnl").inner_text()
        page.locator("#perfDateFrom").fill(args.replacement_from)
        page.locator("#loadPerformanceButton").click()
        page.wait_for_function(
            """() => !document.querySelector('#loadPerformanceButton').disabled &&
                document.querySelector('#performanceStatus').textContent.includes('已加载') &&
                !document.querySelector('#performanceStatus').textContent.includes('明细计算中')""",
            timeout=60_000,
        )
        assert any("/performance/trade-quality?" in url and f"date_from={args.date_from}" in url for url in failures), failures
        assert page.locator("#perfDateFrom").input_value() == args.replacement_from
        assert "查询失败" not in page.locator("#performanceStatus").inner_text()
        assert not errors, errors
        print(json.dumps({"primary_metrics_ms": elapsed, "published_pnl": primary, "replaced_request_cancelled": True, "status": page.locator("#performanceStatus").inner_text()}, ensure_ascii=False))
        for route in held:
            try:
                route.abort()
            except Error:
                pass
        page.unroute_all(behavior="ignoreErrors")
        browser.close()


if __name__ == "__main__":
    main()
