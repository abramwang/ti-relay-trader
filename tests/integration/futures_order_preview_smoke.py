#!/usr/bin/env python3
"""Synthetic UI preview only. All API requests are intercepted; no trading."""
import argparse
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:19193")
    parser.add_argument("--output", default="outputs/recovery/futures-order-preview-20261118")
    args = parser.parse_args()
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    errors, writes, results = [], [], []
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        for width, height in [(1600, 1000), (390, 844)]:
            page = browser.new_page(viewport={"width": width, "height": height})
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.add_init_script("window.EventSource = class extends EventTarget {close(){}}")
            previews = []
            failure = {"on": False}

            def intercept(route):
                req = route.request
                path = urlparse(req.url).path
                if not req.url.startswith(args.base_url):
                    route.abort()
                    return
                if not path.startswith("/v1/"):
                    route.continue_()
                    return
                data = {}
                if path == "/v1/futures/orders/preview" and req.method == "POST":
                    intent = req.post_data_json
                    previews.append(intent)
                    if failure["on"]:
                        route.fulfill(status=503, json={"ok": False, "error": {"message": "行情暂不可用"}})
                        return
                    data = {"mode": "order_preview_only", "intent": intent, "can_submit": False,
                            "command_published": False, "account_permission_enabled": False,
                            "broker_accepting_submit": True, "price_valid": True,
                            "blockers": ["publication_not_connected", "relay_account_read_only"],
                            "quote": {"preparation": {"market": {"price_tick": "1"},
                                "valid_until": (datetime.now(timezone.utc) + timedelta(seconds=4)).isoformat()}}}
                elif req.method != "GET":
                    writes.append(path)
                    route.abort()
                    return
                elif path == "/v1/status":
                    data = {"environment": "test", "status": "ok", "trading_day": {"date": "20261119", "trade_date": "20261119"}}
                elif path == "/v1/accounts":
                    data = {"accounts": []}
                elif path == "/v1/futures/terminal":
                    data = {"accounts": [{"account_id": "300052141", "alias": "东吴 TEST", "route": {"broker_id": "ctp_4900", "gateway_id": "synthetic"}, "connection_state": "assessment", "queries": []}]}
                elif path == "/v1/meridian/futures/instruments":
                    data = {"data": [{"security_id": "RB2701.SHFE", "source_instrument_id": "rb2701", "instrument_name": "螺纹钢", "exchange_id": "SHFE", "price_tick": 1, "volume_multiple": 10, "trading_status": "active"}], "meta": {}}
                elif path == "/v1/meridian/futures/snapshots":
                    data = {"data": [{"security_id": "RB2701.SHFE", "trading_day": 20261119, "last": 3000, "pre_settlement": 2990,
                        "exchange_timestamp": "2026-11-19T09:05:00+08:00", "received_at": "2026-11-19T09:05:00+08:00",
                        "bids": [{"level": 1, "price": 2999, "volume": 2, "available": True}],
                        "asks": [{"level": 1, "price": 3000, "volume": 3, "available": True}]}], "meta": {"cache_status": "ready", "stale_data": False}}
                else:
                    data = {"data": [], "meta": {}, "accounts": [], "positions": [], "orders": [], "fills": [], "transfers": []}
                route.fulfill(json={"ok": True, "data": data})

            page.route("**/*", intercept)
            page.goto(args.base_url + "/trade", wait_until="domcontentloaded")
            page.locator("#futuresAccountEntry").wait_for(state="visible")
            page.locator("#marketSelector").select_option("SHFE")
            page.locator("#symbolInput").fill("RB")
            page.locator("#symbolSuggest button").filter(has_text="RB2701.SHFE").click()
            page.wait_for_function("document.querySelector('#quoteLast').textContent === '3,000'")
            page.locator("#priceInput").fill("3000.000")
            page.locator("#futuresOrderPreviewButton").click()
            page.wait_for_function("document.querySelector('#futuresOrderPreviewResult').textContent.includes('价格档位通过')")
            assert previews[-1]["price"] == "3000.000"
            assert previews[-1]["trade_date"] == "20261119"
            assert page.locator("#submitOrderButton").is_disabled()
            assert "Relay权限：未开放" in page.locator("#futuresOrderPreviewResult").inner_text()
            page.locator("#futuresOrderPreviewResult").scroll_into_view_if_needed()
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth + 2")
            assert page.locator("#futuresOrderPreviewResult").evaluate("el => el.scrollWidth <= el.clientWidth + 1")
            page.screenshot(path=str(output / f"preview-{width}.png"), full_page=True)
            page.wait_for_function("document.querySelector('#futuresOrderPreviewResult').textContent.includes('已过期')")
            page.locator("#qtyInput").fill("2")
            assert "待核对" in page.locator("#futuresOrderPreviewResult").inner_text()
            failure["on"] = True
            page.locator("#futuresOrderPreviewButton").click()
            page.wait_for_function("document.querySelector('#futuresOrderPreviewResult').textContent.includes('核对失败')")
            page.wait_for_timeout(500)
            assert len(previews) == 2
            page.locator("#orderForm").dispatch_event("submit")
            assert not writes
            page.locator("#marketSelector").select_option("stock")
            assert page.locator("#futuresOrderPreviewButton").is_hidden()
            results.append({"viewport": [width, height], "previews": len(previews), "no_trading_requests": True})
            page.close()
        browser.close()
    assert not errors, errors
    (output / "browser-result.json").write_text(json.dumps({"synthetic_only": True, "results": results, "errors": errors, "writes": writes}, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(results))


if __name__ == "__main__":
    main()
