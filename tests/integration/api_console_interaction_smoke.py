#!/usr/bin/env python3
"""Read-only Playwright form and response checks for the Relay API Console."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from playwright.sync_api import sync_playwright


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--account-id", default="314000046830")
    parser.add_argument("--trade-date", default="20260731")
    parser.add_argument("--width", type=int, default=1600)
    parser.add_argument("--height", type=int, default=900)
    parser.add_argument("--output", default="/tmp/relay-api-console-interaction-smoke.png")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    console_errors: list[str] = []
    page_errors: list[str] = []
    response_errors: list[str] = []
    write_requests: list[str] = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": args.width, "height": args.height}, device_scale_factor=1)
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

        def guard_api_write(route: object) -> None:
            request = route.request
            if request.method not in {"GET", "HEAD", "OPTIONS"}:
                write_requests.append(f"{request.method} {request.url}")
                route.abort()
                return
            route.continue_()

        page.route("**/v1/**", guard_api_write)
        page.add_init_script(
            """() => localStorage.removeItem('relay.api_console.collections.v1')"""
        )

        page.goto(args.base_url.rstrip("/") + "/api-console", wait_until="domcontentloaded", timeout=30_000)
        page.wait_for_function(
            """() => document.querySelectorAll('.endpoint-item').length > 50""",
            timeout=15_000,
        )
        page.locator("#baseUrlInput").fill(args.base_url.rstrip("/"))

        select_endpoint(page, "/v1/status")
        configure_status_assertions(page)
        page.locator("#collectionNameInput").fill("生产状态冒烟")
        page.locator("#saveCollectionButton").click()
        page.wait_for_function(
            """() => document.querySelectorAll('#savedCollectionSelect option').length === 2""",
            timeout=5_000,
        )
        send_and_wait(page)
        status_json = page.locator("#jsonOutput").inner_text()
        if '"dependencies"' not in status_json or '"environment": "production"' not in status_json:
            raise AssertionError("status response assertion failed")
        if page.locator("#assertionSummary").inner_text() != "2/2 通过":
            raise AssertionError("saved response assertions did not pass")

        with page.expect_download(timeout=5_000) as download_info:
            page.locator("#exportCollectionButton").click()
        export_path = output.with_suffix(".collections.json")
        download_info.value.save_as(export_path)
        exported = json.loads(export_path.read_text(encoding="utf-8"))
        if exported.get("schema_version") != "relay.api_console_collection.v1":
            raise AssertionError("collection export schema assertion failed")
        if len(exported.get("collections", [])) != 1:
            raise AssertionError("collection export count assertion failed")

        page.evaluate("localStorage.removeItem('relay.api_console.collections.v1')")
        page.reload(wait_until="domcontentloaded")
        page.wait_for_function(
            """() => document.querySelectorAll('.endpoint-item').length > 50""",
            timeout=15_000,
        )
        page.locator("#collectionFileInput").set_input_files(str(export_path))
        page.wait_for_function(
            """() => document.querySelectorAll('#savedCollectionSelect option').length === 2""",
            timeout=5_000,
        )
        imported_option = page.locator("#savedCollectionSelect option").nth(1)
        page.locator("#savedCollectionSelect").select_option(imported_option.get_attribute("value"))
        page.locator("#baseUrlInput").fill(args.base_url.rstrip("/"))
        if page.locator("#collectionNameInput").input_value() != "生产状态冒烟":
            raise AssertionError("imported collection name assertion failed")
        if page.locator(".assertion-row").count() != 2:
            raise AssertionError("imported assertion count failed")
        send_and_wait(page)
        if page.locator("#assertionSummary").inner_text() != "2/2 通过":
            raise AssertionError("imported response assertions did not pass")

        select_endpoint(page, "/v1/accounts", exact=True)
        send_and_wait(page)
        account_rows = page.locator("#tableOutput tbody tr").count()
        if account_rows < 2 or args.account_id not in page.locator("#tableOutput").inner_text():
            raise AssertionError("account table assertion failed")

        select_endpoint(page, "/v1/history/orders")
        fill_field(page, "account_id", args.account_id)
        fill_field(page, "trade_date", args.trade_date)
        fill_field(page, "limit", "20")
        preview = page.locator("#requestPreview").inner_text()
        if args.account_id not in preview or args.trade_date not in preview or "limit=20" not in preview:
            raise AssertionError(f"request preview did not include form values: {preview}")
        send_and_wait(page, timeout=30_000)
        order_rows = page.locator("#tableOutput tbody tr").count()
        table_text = page.locator("#tableOutput").inner_text()
        if order_rows < 1 or "gateway_order_id" not in table_text:
            raise AssertionError("historical order table assertion failed")

        diagnostics = page.evaluate(
            """() => ({
                endpointCount: document.querySelectorAll('.endpoint-item').length,
                selectedPath: document.querySelector('#endpointPath')?.textContent || '',
                requestPreview: document.querySelector('#requestPreview')?.textContent || '',
                responseStatus: document.querySelector('#responseStatus')?.textContent || '',
                responseMeta: document.querySelector('#responseMeta')?.textContent || '',
                resultColumns: document.querySelectorAll('#tableOutput thead th').length,
                savedCollections: document.querySelectorAll('#savedCollectionSelect option').length - 1,
                assertionSummary: document.querySelector('#assertionSummary')?.textContent || '',
                horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth,
            })"""
        )
        diagnostics.update({"accountRows": account_rows, "orderRows": order_rows})
        page.screenshot(path=str(output), full_page=False)
        browser.close()

    if diagnostics["responseStatus"] != "HTTP 200" or diagnostics["horizontalOverflow"]:
        raise AssertionError(f"API Console response/layout assertion failed: {diagnostics}")
    if write_requests or console_errors or page_errors or response_errors:
        raise AssertionError(
            json.dumps(
                {
                    "write_requests": write_requests,
                    "console_errors": console_errors,
                    "page_errors": page_errors,
                    "response_errors": response_errors,
                    "diagnostics": diagnostics,
                },
                ensure_ascii=False,
                indent=2,
            )
        )

    print(json.dumps({"screenshot": str(output), "diagnostics": diagnostics}, ensure_ascii=False, indent=2))
    return 0


def configure_status_assertions(page: object) -> None:
    first = page.locator(".assertion-row").nth(0)
    first.locator('select[aria-label="断言类型"]').select_option("status_equals")
    first.locator('input[aria-label="期望值"]').fill("200")

    page.locator("#addAssertionButton").click()
    second = page.locator(".assertion-row").nth(1)
    second.locator('select[aria-label="断言类型"]').select_option("json_path_equals")
    second.locator('input[aria-label="JSON 路径"]').fill("data.environment")
    second.locator('input[aria-label="期望值"]').fill("production")


def select_endpoint(page, path: str, *, exact: bool = False) -> None:
    candidates = page.locator(".endpoint-item")
    matches = []
    for index in range(candidates.count()):
        item = candidates.nth(index)
        endpoint_path = item.locator(".endpoint-status").inner_text()
        matches_path = endpoint_path == path if exact else path in endpoint_path
        if matches_path:
            matches.append(item)
    if len(matches) != 1:
        raise AssertionError(f"expected one API Console endpoint for {path}, got {len(matches)}")
    matches[0].click()
    page.wait_for_function(
        """(expected) => document.querySelector('#endpointPath')?.textContent === expected""",
        arg=path,
        timeout=5_000,
    )


def fill_field(page, name: str, value: str) -> None:
    field = page.locator(f'#paramGrid [name="{name}"]')
    if field.count() != 1:
        raise AssertionError(f"API Console field {name} is unavailable")
    field.fill(value)
    field.dispatch_event("input")


def send_and_wait(page, *, timeout: int = 15_000) -> None:
    preview = page.locator("#requestPreview").inner_text()
    method, relative_url = preview.split(" ", 1)
    path = relative_url.split("?", 1)[0]
    with page.expect_response(
        lambda response: response.request.method == method and path in response.url,
        timeout=timeout,
    ) as response_info:
        page.locator("#sendButton").click()
    if response_info.value.status != 200:
        raise AssertionError(f"API Console transport returned HTTP {response_info.value.status}")
    page.wait_for_function(
        """() => (document.querySelector('#responseStatus')?.textContent || '').startsWith('HTTP ')""",
        timeout=timeout,
    )
    status = page.locator("#responseStatus").inner_text()
    if status != "HTTP 200":
        raise AssertionError(f"API Console returned {status}: {page.locator('#responseMeta').inner_text()}")


if __name__ == "__main__":
    raise SystemExit(main())
