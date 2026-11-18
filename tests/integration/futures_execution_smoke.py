#!/usr/bin/env python3
"""UI-only TEST lifecycle. Intercept every API; never access Redis or a broker."""
import argparse
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
from urllib.parse import urlparse, parse_qs

from playwright.sync_api import sync_playwright


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:19194")
    parser.add_argument("--output", default="outputs/recovery/futures-terminal-execution-20261119")
    args = parser.parse_args()
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    errors, results = [], []
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        for width, height in [(1600, 1000), (390, 844)]:
            page = browser.new_page(viewport={"width": width, "height": height})
            page.on("pageerror", lambda e: errors.append(str(e)))
            page.on("dialog", lambda dialog: dialog.accept())
            page.add_init_script("window.EventSource=class extends EventTarget {close(){}}")
            state = {"orders": [], "commands": {}, "writes": [], "unknown": False, "enabled": True, "gets": []}

            def intercept(route):
                req = route.request
                url = urlparse(req.url)
                path = url.path
                if not req.url.startswith(args.base_url):
                    route.abort()
                    return
                if not path.startswith("/v1/"):
                    route.continue_()
                    return
                if path in ("/v1/futures/orders", "/v1/futures/cancels") and req.method == "POST":
                    body = req.post_data_json
                    state["writes"].append([path, body])
                    key = body["idempotency_key"]
                    data = {**body, "message_id": "message-" + key, "broker_state_known": False, "retry_safe": False,
                            "publication": {"command_id": len(state["writes"]), "state": "published", "command_published": True}}
                    if path == "/v1/futures/orders":
                        data["gateway_order_id"] = "relay-web-order-" + key
                        o = body["order"]
                        if not state["unknown"]:
                            state["orders"].append({"account_id": o["account_id"], "gateway_order_id": data["gateway_order_id"],
                                "counter_session_id": "synthetic-session", "trade_date": o["trade_date"], "symbol": "rb2701", "exchange": "SHFE",
                                "trade_side": "B", "offset_type": "O", "order_qty": 1, "cum_filled_qty": 0, "leaves_qty": 1,
                                "limit_price": o["price"], "gateway_status": "working", "submitted_at": "2026-11-19T09:05:00+08:00", "native_ids": {"order_sys_id": "synthetic-1"}})
                    else:
                        data["cancel_id"] = "cancel-" + key
                        for o in state["orders"]:
                            if o["gateway_order_id"] == body["gateway_order_id"]:
                                o["gateway_status"], o["leaves_qty"] = "cancelled", 0
                    state["commands"][key] = data
                    if state["unknown"]:
                        data["publication"].update(state="attempted", command_published=None)
                        route.abort()
                    else:
                        route.fulfill(status=202, json={"ok": True, "data": data})
                    return
                if req.method != "GET":
                    errors.append("unexpected write " + path)
                    route.abort()
                    return
                if path in ("/v1/futures/order-command", "/v1/futures/cancel-command"):
                    state["gets"].append(req.url)
                    data = state["commands"][parse_qs(url.query)["idempotency_key"][0]]
                elif path == "/v1/status":
                    data = {"environment": "test", "status": "ok", "trading_day": {"date": "20261119", "trade_date": "20261119"}}
                elif path == "/v1/accounts":
                    data = {"accounts": []}
                elif path == "/v1/futures/terminal":
                    data = {"accounts": [{"account_id": "300052141", "alias": "东吴 TEST", "route": {"env": "test", "account_id": "300052141", "broker_id": "ctp_4900", "gateway_id": "synthetic"},
                        "connection_state": "execution", "queries": [], "local_orders": state["orders"],
                        "heartbeat": {"counter_session_id": "synthetic-session", "produced_at": datetime.now(timezone.utc).isoformat()},
                        "execution": {"submit_enabled": state["enabled"], "cancel_enabled": state["enabled"], "trade_date": "20261119",
                            "price_source": "meridian_futures_snapshot.v1", "valid_until": (datetime.now(timezone.utc) + timedelta(seconds=60)).isoformat()}}]}
                elif path == "/v1/meridian/futures/instruments":
                    data = {"data": [{"security_id": "RB2701.SHFE", "source_instrument_id": "rb2701", "instrument_name": "螺纹钢", "exchange_id": "SHFE", "price_tick": 1, "volume_multiple": 10, "trading_status": "active"}], "meta": {}}
                elif path == "/v1/meridian/futures/snapshots":
                    now = datetime.now(timezone.utc).isoformat()
                    data = {"data": [{"security_id": "RB2701.SHFE", "trading_day": 20261119, "last": 3000, "pre_settlement": 2990,
                        "exchange_timestamp": now, "received_at": now, "bids": [{"level": 1, "price": 2999, "volume": 2, "available": True}],
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
            page.locator("#submitOrderButton").click()
            try:
                page.wait_for_function("document.querySelector('#futuresExecutionRecords').textContent.includes('柜台已报')", timeout=10000)
            except Exception:
                print(json.dumps({"errors": errors, "writes": state["writes"], "records": page.locator("#futuresExecutionRecords").inner_text(), "invalid": page.locator("#orderForm :invalid").evaluate_all("els=>els.map(e=>({id:e.id,message:e.validationMessage}))")}, ensure_ascii=False))
                page.screenshot(path=str(output / f"failed-{width}.png"), full_page=True)
                raise
            assert len(state["writes"]) == 1
            assert state["writes"][0][1]["order"]["price"] == "3000.000"
            page.locator('[data-futures-query="order.list.query"]').click()
            page.locator("[data-futures-cancel]").click()
            page.wait_for_function("document.querySelector('#futuresExecutionRecords').textContent.includes('已撤单')")
            assert len(state["writes"]) == 2
            assert state["writes"][0][1]["idempotency_key"] != state["writes"][1][1]["idempotency_key"]
            assert page.locator("[data-futures-cancel]").is_disabled()
            page.locator("#futuresExecutionRecords").scroll_into_view_if_needed()
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth + 2")
            assert page.locator("#futuresExecutionRecords").evaluate("e=>e.scrollWidth <= e.clientWidth+1")
            page.screenshot(path=str(output / f"execution-{width}.png"), full_page=True)
            state["unknown"] = True
            page.locator("#submitOrderButton").click()
            page.wait_for_function("document.querySelector('#futuresExecutionRecords').textContent.includes('结果未知')")
            assert len(state["writes"]) == 3
            unknown_key = state["writes"][-1][1]["idempotency_key"]
            page.reload(wait_until="domcontentloaded")
            page.locator("#futuresAccountEntry").wait_for(state="visible")
            page.locator("#marketSelector").select_option("SHFE")
            page.locator("#futuresExecutionRecords button").first.click()
            page.wait_for_function("document.querySelector('#futuresExecutionRecords').textContent.includes('发送结果未知')")
            assert unknown_key in state["gets"][-1]
            page.locator("#orderForm").dispatch_event("submit")
            page.wait_for_timeout(300)
            assert len(state["writes"]) == 3
            assert page.locator("#submitOrderButton").is_disabled()
            state["enabled"] = False
            page.locator("#futuresAccountRefresh").click()
            page.wait_for_function("document.querySelector('#futuresExecutionPermission').textContent.includes('报单：关闭')")
            results.append({"viewport": [width,height], "synthetic_posts": 3, "status_gets": len(state["gets"]), "reload_no_retry": True})
            page.close()
        browser.close()
    assert not errors, errors
    (output / "browser-result.json").write_text(json.dumps({"synthetic_only": True,"results": results,"errors": errors},ensure_ascii=False,indent=2)+"\n")
    print(json.dumps(results))


if __name__ == "__main__":
    main()
