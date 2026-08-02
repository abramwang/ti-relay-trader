#!/usr/bin/env python3
"""Read-only Playwright interaction coverage for the Relay trading terminal."""

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
    parser.add_argument("--symbol", default="600000.SH")
    parser.add_argument("--width", type=int, default=1600)
    parser.add_argument("--height", type=int, default=1000)
    parser.add_argument("--output", default="/tmp/relay-trade-interaction-smoke.png")
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
        page = browser.new_page(
            viewport={"width": args.width, "height": args.height},
            device_scale_factor=1,
            accept_downloads=False,
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

        def guard_api_write(route: object) -> None:
            request = route.request
            if request.method not in {"GET", "HEAD", "OPTIONS"}:
                write_requests.append(f"{request.method} {request.url}")
                route.abort()
                return
            route.continue_()

        page.route("**/v1/**", guard_api_write)

        page.goto(args.base_url.rstrip("/") + "/trade", wait_until="domcontentloaded", timeout=30_000)
        page.wait_for_function(
            """() => document.querySelectorAll('#orderAccount option').length >= 1 &&
                document.querySelector('#environmentBadge')?.textContent.includes('生产环境')""",
            timeout=30_000,
        )
        account_tab = page.locator("#accountTabs button").filter(has_text=args.account_id)
        if account_tab.count() != 1:
            raise AssertionError(f"account tab {args.account_id} is unavailable")
        account_tab.click()
        page.wait_for_function(
            """(accountID) => document.querySelector('#orderAccount')?.value === accountID &&
                document.querySelector('#submitOrderButton')?.disabled === true &&
                (document.querySelector('#riskAlert')?.textContent || '').includes('只读保护')""",
            arg=args.account_id,
            timeout=30_000,
        )

        page.locator("#orderForm").dispatch_event("submit")
        page.wait_for_function(
            """() => (document.querySelector('#terminalToast')?.textContent || '').includes('未发送下单请求')""",
            timeout=5_000,
        )

        page.locator('[data-view-link="batch"]').click()
        page.locator("#batchPanel").wait_for(state="visible", timeout=10_000)
        page.wait_for_function(
            """() => (document.querySelector('#batchGuardTitle')?.textContent || '').includes('生产环境只读保护') &&
                document.querySelector('#validateBatchButton')?.disabled === true &&
                document.querySelector('#submitBatchButton')?.disabled === true""",
            timeout=5_000,
        )
        page.locator("#validateBatchButton").click(force=True)

        page.locator('[data-view-link="asset"]').click()
        page.locator("#assetPanel").wait_for(state="visible", timeout=10_000)
        page.locator("#assetTradeDate").fill(args.trade_date)
        page.locator("#queryAssetButton").click()
        page.wait_for_function(
            """(tradeDate) => {
                const rows = document.querySelectorAll('#positionsBody tr[data-position-security-id]');
                const pageInfo = document.querySelector('#positionsPageInfo')?.textContent || '';
                return rows.length > 1 && pageInfo.includes('第 1 页') &&
                    pageInfo.includes(`${tradeDate.slice(0, 4)}-${tradeDate.slice(4, 6)}-${tradeDate.slice(6)}`);
            }""",
            arg=args.trade_date,
            timeout=45_000,
        )
        position_count = page.locator("#positionsBody tr[data-position-security-id]").count()
        symbol_header = page.locator('th[data-sort-table="positions"][data-sort-key="symbol"]')
        symbol_header.click()
        page.wait_for_function(
            """() => document.querySelector('th[data-sort-table="positions"][data-sort-key="symbol"]')
                ?.getAttribute('aria-sort') === 'ascending'""",
            timeout=5_000,
        )
        ascending_symbols = page.locator("#positionsBody tr[data-position-security-id] td:first-child").all_inner_texts()
        if ascending_symbols != sorted(ascending_symbols):
            raise AssertionError(f"position symbol ascending sort failed: {ascending_symbols[:8]}")
        symbol_header.click()
        descending_symbols = page.locator("#positionsBody tr[data-position-security-id] td:first-child").all_inner_texts()
        if descending_symbols != sorted(descending_symbols, reverse=True):
            raise AssertionError(f"position symbol descending sort failed: {descending_symbols[:8]}")

        positions_next_enabled = page.locator("#positionsNextPage").is_enabled()
        if positions_next_enabled:
            first_page_symbol = descending_symbols[0]
            page.locator("#positionsNextPage").click()
            page.wait_for_function(
                """(firstSymbol) =>
                    (document.querySelector('#positionsPageInfo')?.textContent || '').includes('第 2 页') &&
                    (document.querySelector('#positionsBody tr[data-position-security-id] td:first-child')?.textContent || '').trim() !== firstSymbol""",
                arg=first_page_symbol,
                timeout=10_000,
            )
            second_page_symbol = page.locator("#positionsBody tr[data-position-security-id] td:first-child").first.inner_text()
            if first_page_symbol == second_page_symbol:
                raise AssertionError("position pagination did not change rows")
            page.locator("#positionsPrevPage").click()
            page.wait_for_function(
                """() => (document.querySelector('#positionsPageInfo')?.textContent || '').includes('第 1 页')""",
                timeout=10_000,
            )

        position_row = page.locator("#positionsBody tr[data-position-security-id]").first
        focused_security_id = position_row.get_attribute("data-position-security-id") or ""
        position_row.locator("td").first.click()
        page.wait_for_function(
            """(securityID) => {
                const symbol = securityID.split('.')[0];
                return document.querySelector('#terminalShell')?.classList.contains('view-trade') &&
                    document.querySelector('#symbolInput')?.value === symbol;
            }""",
            arg=focused_security_id,
            timeout=15_000,
        )

        symbol, exchange = args.symbol.split(".", 1)
        page.locator("#symbolInput").fill(symbol)
        page.locator("#exchangeInput").select_option(exchange)
        page.locator("#chartTradeDateInput").fill(args.trade_date)
        page.locator("#reloadChartButton").click()
        page.wait_for_function(
            """(securityID) => {
                const status = document.querySelector('#minuteChartStatus')?.textContent || '';
                return status.includes(securityID) && status.includes('条') && !status.includes('查询中');
            }""",
            arg=args.symbol,
            timeout=60_000,
        )
        page.locator("#minuteChart canvas").wait_for(state="visible", timeout=10_000)
        chart_diagnostics = page.evaluate(
            """() => {
                const canvas = document.querySelector('#minuteChart canvas');
                const pixels = canvas
                    ? canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data
                    : [];
                let painted = 0;
                for (let index = 0; index < pixels.length; index += 16) {
                    if (pixels[index + 3] > 0) painted += 1;
                }
                return { width: canvas?.width || 0, height: canvas?.height || 0, painted };
            }"""
        )
        if chart_diagnostics["painted"] < 500:
            raise AssertionError(f"minute K-line chart is blank: {chart_diagnostics}")

        page.locator('[data-view-link="orders"]').click()
        page.locator("#ordersPanel").wait_for(state="visible", timeout=10_000)
        page.locator("#ordersTradeDate").fill(args.trade_date)
        page.locator("#queryOrdersButton").click()
        page.wait_for_function(
            """() => document.querySelectorAll('#blotterContent tr[data-order-id]').length > 1 &&
                (document.querySelector('#ordersPageInfo')?.textContent || '').includes('第 1 页')""",
            timeout=45_000,
        )
        order_count = page.locator("#blotterContent tr[data-order-id]").count()
        order_symbol_header = page.locator('th[data-sort-table="orders"][data-sort-key="symbol"]')
        order_symbol_header.click()
        page.wait_for_function(
            """() => document.querySelector('th[data-sort-table="orders"][data-sort-key="symbol"]')
                ?.getAttribute('aria-sort') === 'ascending'""",
            timeout=5_000,
        )
        order_symbols = page.locator("#blotterContent tr[data-order-id] td:nth-child(2)").all_inner_texts()
        if order_symbols != sorted(order_symbols):
            raise AssertionError(f"order symbol ascending sort failed: {order_symbols[:8]}")

        detail_row = page.locator("#blotterContent tr[data-order-id]").nth(1)
        selected_order_id = detail_row.get_attribute("data-order-id") or ""
        detail_row.click()
        page.wait_for_function(
            """(orderID) => (document.querySelector('#detailSub')?.textContent || '').includes(orderID) &&
                (document.querySelector('#rawJson')?.textContent || '').includes(orderID)""",
            arg=selected_order_id,
            timeout=10_000,
        )

        orders_next_enabled = page.locator("#ordersNextPage").is_enabled()
        if orders_next_enabled:
            first_page_order = page.locator("#blotterContent tr[data-order-id]").first.get_attribute("data-order-id")
            page.locator("#ordersNextPage").click()
            page.wait_for_function(
                """() => (document.querySelector('#ordersPageInfo')?.textContent || '').includes('第 2 页')""",
                timeout=20_000,
            )
            second_page_order = page.locator("#blotterContent tr[data-order-id]").first.get_attribute("data-order-id")
            if first_page_order == second_page_order:
                raise AssertionError("order pagination did not change rows")

        diagnostics = page.evaluate(
            """() => ({
                environment: document.querySelector('#environmentBadge')?.textContent || '',
                account: document.querySelector('#orderAccount')?.value || '',
                accountCount: document.querySelectorAll('#orderAccount option').length,
                submitDisabled: Boolean(document.querySelector('#submitOrderButton')?.disabled),
                riskText: document.querySelector('#riskAlert')?.textContent || '',
                batchGuard: document.querySelector('#batchGuardMessage')?.textContent || '',
                batchValidateDisabled: Boolean(document.querySelector('#validateBatchButton')?.disabled),
                batchSubmitDisabled: Boolean(document.querySelector('#submitBatchButton')?.disabled),
                currentView: document.querySelector('#terminalShell')?.className || '',
                orderPage: document.querySelector('#ordersPageInfo')?.textContent || '',
                detail: document.querySelector('#detailSub')?.textContent || '',
                horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth,
            })"""
        )
        diagnostics.update(
            {
                "positionRows": position_count,
                "positionsPaginated": positions_next_enabled,
                "focusedSecurityID": focused_security_id,
                "orderRows": order_count,
                "ordersPaginated": orders_next_enabled,
                "chart": chart_diagnostics,
            }
        )
        page.screenshot(path=str(output), full_page=False)
        browser.close()

    if diagnostics["environment"] != "生产环境" or diagnostics["account"] != args.account_id:
        raise AssertionError(f"environment/account switching failed: {diagnostics}")
    if diagnostics["accountCount"] < 2 or not diagnostics["submitDisabled"]:
        raise AssertionError(f"read-only trading protection failed: {diagnostics}")
    if "只读保护" not in diagnostics["riskText"]:
        raise AssertionError(f"read-only risk message is missing: {diagnostics}")
    if "券商测试环境" not in diagnostics["batchGuard"] or not diagnostics["batchValidateDisabled"] or not diagnostics["batchSubmitDisabled"]:
        raise AssertionError(f"batch production guard failed: {diagnostics}")
    if diagnostics["horizontalOverflow"]:
        raise AssertionError(f"terminal has horizontal overflow: {diagnostics}")
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


if __name__ == "__main__":
    raise SystemExit(main())
