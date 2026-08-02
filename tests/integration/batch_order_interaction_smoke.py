#!/usr/bin/env python3
"""Playwright coverage for the test-only batch order terminal workflow."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from playwright.sync_api import Route, sync_playwright


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--account-id", default="314000046830")
    parser.add_argument("--width", type=int, default=1600)
    parser.add_argument("--height", type=int, default=1000)
    parser.add_argument("--output", default="/tmp/relay-batch-order-smoke.png")
    return parser.parse_args()


def envelope(data: dict[str, object]) -> str:
    return json.dumps({"ok": True, "data": data}, ensure_ascii=False)


def main() -> int:
    args = parse_args()
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    console_errors: list[str] = []
    page_errors: list[str] = []
    submitted: list[dict[str, object]] = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": args.width, "height": args.height})
        page.on(
            "console",
            lambda message: console_errors.append(message.text)
            if message.type == "error"
            else None,
        )
        page.on("pageerror", lambda error: page_errors.append(str(error)))

        def mock_status(route: Route) -> None:
            route.fulfill(
                status=200,
                content_type="application/json",
                body=envelope(
                    {
                        "status": "ok",
                        "environment": "test",
                        "public_url": args.base_url,
                        "time": "2026-08-02T12:00:00+08:00",
                        "dependencies": {
                            "database": {"status": "ok"},
                            "redis": {"status": "ok"},
                        },
                        "trading_day": {
                            "trade_date": "20260731",
                            "previous_or_current_trading_date": "20260731",
                        },
                    }
                ),
            )

        def mock_accounts(route: Route) -> None:
            route.fulfill(
                status=200,
                content_type="application/json",
                body=envelope(
                    {
                        "accounts": [
                            {
                                "account_id": args.account_id,
                                "alias": "批量测试账户",
                                "enabled": True,
                                "trading_enabled": True,
                                "simulated": False,
                            }
                        ]
                    }
                ),
            )

        def capture_batch(route: Route) -> None:
            payload = route.request.post_data_json
            submitted.append(payload)
            orders = [
                {
                    **order,
                    "account_id": payload["account_id"],
                    "status": "created",
                }
                for order in payload["orders"]
            ]
            route.fulfill(
                status=202,
                content_type="application/json",
                body=envelope(
                    {
                        "orders": orders,
                        "message_id": "msg-batch-browser-test",
                        "stream_key": "relay:test:v1:huaxin:test:cmd.trade",
                        "stream_id": "100-0",
                        "request_id": "req-batch-browser-test",
                        "idempotency_key": payload["idempotency_key"],
                        "published": {
                            "stream_key": "relay:test:v1:huaxin:test:cmd.trade",
                            "stream_id": "100-0",
                            "body_bytes": 512,
                        },
                    }
                ),
            )

        page.route("**/v1/status", mock_status)
        page.route("**/v1/accounts", mock_accounts)
        page.route("**/v1/orders/batch", capture_batch)

        page.goto(args.base_url.rstrip("/") + "/trade#batch", wait_until="domcontentloaded", timeout=30_000)
        page.wait_for_function(
            """() => document.querySelector('#batchGuard')?.dataset.status === 'ready' &&
                document.querySelector('#batchAccount')?.value &&
                document.querySelector('#validateBatchButton')?.disabled === false""",
            timeout=30_000,
        )

        page.locator("#batchPasteInput").fill(
            "600000.SH,B,9.67,100\n000001.SZ,S,11.24,200"
        )
        page.locator("#importBatchButton").click()
        if page.locator("#batchOrdersBody tr[data-batch-row-id]").count() != 2:
            raise AssertionError("batch import did not create two rows")

        page.locator("#validateBatchButton").click()
        page.wait_for_function(
            """() => !document.querySelector('#submitBatchButton')?.disabled &&
                (document.querySelector('#batchValidationCount')?.textContent || '').includes('通过 2')""",
            timeout=5_000,
        )

        qty_input = page.locator('#batchOrdersBody tr[data-batch-row-id] input[data-batch-field="qty"]').first
        qty_input.fill("300")
        if not page.locator("#submitBatchButton").is_disabled():
            raise AssertionError("editing a validated batch did not invalidate confirmation")
        page.locator("#validateBatchButton").click()
        page.locator("#submitBatchButton").click()
        page.locator("#batchConfirmDialog").wait_for(state="visible", timeout=5_000)
        page.locator("#batchConfirmInput").fill(args.account_id[-4:])
        page.locator("#confirmBatchSubmitButton").click()
        page.wait_for_function(
            """() => (document.querySelector('#batchMessageID')?.textContent || '') === 'msg-batch-browser-test' &&
                (document.querySelector('#batchResultStatus')?.textContent || '').includes('已发布 2 笔') &&
                document.querySelector('#submitBatchButton')?.disabled === true""",
            timeout=10_000,
        )

        diagnostics = page.evaluate(
            """() => ({
                environment: document.querySelector('#environmentBadge')?.textContent || '',
                guard: document.querySelector('#batchGuardMessage')?.textContent || '',
                rows: document.querySelectorAll('#batchOrdersBody tr[data-batch-row-id]').length,
                buyAmount: document.querySelector('#batchBuyAmount')?.textContent || '',
                sellAmount: document.querySelector('#batchSellAmount')?.textContent || '',
                result: document.querySelector('#batchResultStatus')?.textContent || '',
                horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth,
            })"""
        )
        page.screenshot(path=str(output), full_page=False)
        browser.close()

    if len(submitted) != 1:
        raise AssertionError(f"expected one intercepted batch request, got {len(submitted)}")
    request = submitted[0]
    if request.get("account_id") != args.account_id or len(request.get("orders", [])) != 2:
        raise AssertionError(f"unexpected batch request: {request}")
    if request["orders"][0]["qty"] != 300 or request["orders"][1]["trade_side"] != "S":
        raise AssertionError(f"edited batch values were not submitted: {request}")
    if not request.get("idempotency_key") or any(
        not order.get("gateway_order_id") or not order.get("client_order_id")
        for order in request["orders"]
    ):
        raise AssertionError(f"batch identities are incomplete: {request}")
    if diagnostics["environment"] != "测试环境" or diagnostics["horizontalOverflow"]:
        raise AssertionError(f"batch terminal diagnostics failed: {diagnostics}")
    if console_errors or page_errors:
        raise AssertionError(
            json.dumps(
                {
                    "console_errors": console_errors,
                    "page_errors": page_errors,
                    "diagnostics": diagnostics,
                },
                ensure_ascii=False,
                indent=2,
            )
        )

    print(
        json.dumps(
            {"screenshot": str(output), "diagnostics": diagnostics, "request": request},
            ensure_ascii=False,
            indent=2,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
