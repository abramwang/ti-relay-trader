#!/usr/bin/env python3
"""Browser-only synthetic market contract tests; all API writes are blocked."""

import argparse
import json
from datetime import datetime, timedelta
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import sync_playwright


def live_readonly(args, output):
    writes, errors, observed = [], [], []
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1600, "height": 1000})
        page.on("pageerror", lambda error: errors.append(str(error)))

        def guard(route):
            if route.request.method not in {"GET", "HEAD", "OPTIONS"}:
                writes.append(route.request.url)
                route.abort()
            else:
                route.continue_()

        page.route("**/v1/**", guard)
        page.goto(args.base_url + "/trade", wait_until="domcontentloaded")
        page.wait_for_function("document.querySelector('#orderAccount').options.length > 0")
        page.wait_for_timeout(1500)
        for exchange, prefix, security in [("SHFE", "AU2612", "AU2612.SHFE"), ("CFFEX", "IF2612", "IF2612.CFFEX")]:
            page.locator("#marketSelector").select_option(exchange)
            page.locator("#symbolInput").fill(prefix)
            page.locator("#symbolSuggest button").filter(has_text=security).click()
            page.locator("#priceInput").click()
            page.wait_for_timeout(3500)
            assert page.locator("#quoteSymbol").inner_text() == security
            assert page.locator("#submitOrderButton").is_disabled()
            assert page.locator("#assetPanel").is_hidden()
            observed.append({"exchange": exchange, "security_id": security, "name": page.locator("#quoteName").inner_text(), "last": page.locator("#quoteLast").inner_text(), "status": page.locator("#futuresQuoteStatus").inner_text(), "bars": page.locator("#minuteChartStatus").inner_text(), "tick": page.locator("#priceInput").get_attribute("step")})
            page.screenshot(path=str(output / f"live-{exchange}.png"))
        browser.close()
    assert not errors and not writes, (errors, writes)
    (output / "live-result.json").write_text(json.dumps({"read_only": True, "observed": observed, "writes": writes, "errors": errors}, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(observed, ensure_ascii=False))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--output", default="outputs/recovery/futures_terminal_20261116")
    parser.add_argument("--live", action="store_true", help="Read actual Meridian market responses; never send account commands")
    args = parser.parse_args()
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    if args.live:
        live_readonly(args, output)
        return
    instruments = [
        {"security_id": "AU2612.SHFE", "instrument_name": "黄金 AU2612", "exchange_id": "SHFE", "price_tick": 0.02, "volume_multiple": 1000, "trading_status": "active", "source": "shfe_official_business_parameters"},
        {"security_id": "RB2701.SHFE", "instrument_name": "螺纹钢 RB2701", "exchange_id": "SHFE", "price_tick": 1, "volume_multiple": 10, "trading_status": "active", "source": "shfe_official_business_parameters"},
        {"security_id": "IF2612.CFFEX", "instrument_name": "沪深300股指期货 IF2612", "exchange_id": "CFFEX", "price_tick": 0.2, "volume_multiple": 300, "trading_status": "active_observed", "source": "cffex_official_daily_observed_contract"},
    ]
    errors, writes, requests, results = [], [], [], []
    failure = {"bars": False}
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        for width, height in [(1600, 1000), (390, 844)]:
            page = browser.new_page(viewport={"width": width, "height": height})
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.add_init_script("""(() => {
              window.futuresSources = [];
              window.EventSource = function(url, options) {
                const source = new EventTarget();
                source.url = url; source.closed = false;
                source.close = () => { source.closed = true; };
                window.futuresSources.push(source);
                return source;
              };
            })();""")

            def route_api(route):
                req = route.request
                if not req.url.startswith(args.base_url):
                    route.abort()
                    return
                if not urlparse(req.url).path.startswith("/v1/"):
                    route.continue_()
                    return
                if req.method not in {"GET", "HEAD", "OPTIONS"}:
                    writes.append(req.url)
                    route.abort()
                    return
                parsed = urlparse(req.url)
                if "/meridian/futures/" not in parsed.path:
                    data = {"data": [], "meta": {}, "accounts": [], "positions": [], "orders": [], "fills": []}
                    if parsed.path == "/v1/accounts":
                        data = {"accounts": [{"account_id": "990000000001", "alias": "合成证券账户", "enabled": True, "trading_enabled": False}]}
                    elif parsed.path == "/v1/status":
                        data = {"environment": "test", "status": "ok", "trading_day": {"date": "20261117", "trade_date": "20261117"}}
                    route.fulfill(json={"ok": True, "data": data})
                    return
                requests.append(req.url)
                params = parse_qs(parsed.query)
                security = params.get("security_id", ["AU2612.SHFE"])[0]
                price = 830.02 if security.startswith("AU") else 4000.2
                meta = {}
                if parsed.path.endswith("/instruments"):
                    exchanges = params.get("exchange_ids", ["CFFEX,SHFE"])[0].split(",")
                    rows = [row for row in instruments if row["exchange_id"] in exchanges]
                    if len(rows) > 1:
                        if "cursor" in params:
                            rows = rows[1:]
                        else:
                            rows = rows[:1]
                            meta["next_cursor"] = "catalog-page-2"
                elif parsed.path.endswith("/snapshots"):
                    rows = [{"security_id": security, "trading_day": 20261117, "exchange_timestamp": "2026-11-16T21:01:00+08:00", "received_at": "2026-11-16T21:01:00+08:00", "last": price, "pre_settlement": price - 2, "pre_close": price - 5, "open_interest": 12345, "volume": 100,
                             "bids": [{"level": 1, "price": price - 0.02, "volume": 4, "available": True}, {"level": 2, "price": None, "volume": None, "available": False}],
                             "asks": [{"level": 1, "price": price + 0.02, "volume": 5, "available": True}]}]
                    meta = {"stale_data": False, "cache_status": "ready", "trade_date": "20261117"}
                else:
                    if failure["bars"]:
                        route.fulfill(status=503, json={"ok": False, "error": {"message": "meridian futures request failed", "detail": {"error": {"code": "archive_incomplete", "message": "测试归档未通过门禁"}}}})
                        return
                    start = 300 if "cursor" in params else 0
                    rows = []
                    for i in range(start, start + 300):
                        moment = datetime.fromisoformat("2026-11-16T21:01:00+08:00") + timedelta(minutes=i)
                        close = price + (i % 20) * 0.02
                        rows.append({"security_id": security, "trade_date": 20261117, "datetime": moment.isoformat(), "open": close - 0.02, "close": close, "high": close + 0.04, "low": close - 0.04, "volume": None if i == 0 else i + 1})
                    meta = {"trade_date": "20261117", "count": len(rows), "next_cursor": "bars-page-2" if start == 0 else "", "schema_version": "futures_minute_bar.v1"}
                route.fulfill(json={"ok": True, "data": {"data": rows, "meta": meta}})

            page.route("**/*", route_api)
            page.goto(args.base_url + "/trade", wait_until="domcontentloaded")
            page.wait_for_function("document.querySelector('#orderAccount').options.length > 0")
            page.wait_for_timeout(1500)
            ledger_date = page.locator("#ordersTradeDate").input_value()
            page.locator("#marketSelector").select_option("SHFE")
            page.locator("#symbolInput").fill("AU")
            page.get_by_role("option", name="AU2612.SHFE", exact=False).wait_for()
            page.locator("#symbolSuggest button").filter(has_text="AU2612.SHFE").click()
            page.wait_for_function("document.querySelector('#quoteLast').textContent === '830.02'")
            page.wait_for_function("document.querySelector('#minuteChartStatus').textContent.includes('600 条')")
            assert page.locator("#ordersTradeDate").input_value() == ledger_date
            assert page.locator("#chartTradeDateInput").input_value() == "20261117"
            assert page.locator("#qtyInput").get_attribute("step") == "1"
            assert page.locator("#priceInput").get_attribute("step") == "0.02"
            assert page.locator("#submitOrderButton").is_disabled()
            assert page.locator("#assetPanel").is_hidden()
            assert page.locator("#ordersPanel").is_hidden()
            assert page.locator("#depthBook .depth-row").count() == 2
            page.locator("#priceInput").click()
            page.locator("#futuresOffset").select_option("CT")
            page.locator("#orderForm").dispatch_event("submit")
            assert not writes
            chart = page.evaluate("""() => {
              const el = document.querySelector('#minuteChart');
              const chart = echarts.getInstanceByDom(el); const option = chart.getOption();
              const canvas = el.querySelector('canvas');
              const data = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data;
              let pixels = 0; for (let i=3; i<data.length; i+=4) if (data[i]) pixels++;
              return {count: option.series[0].data.length, label: option.xAxis[0].data[0], unknownVolume: option.series[1].data[0].value, pixels};
            }""")
            assert chart["count"] == 600 and chart["pixels"] > 1000, chart
            assert chart["label"] == "11-16 21:01" and chart["unknownVolume"] is None, chart
            assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth"), "horizontal overflow"
            assert page.locator("#quotePrice").evaluate("el => el.scrollWidth <= el.clientWidth"), "quote overflow"
            assert page.locator("#futuresOffset").evaluate("el => el.getBoundingClientRect().width > 80")
            page.locator("#tradeChartPanel").scroll_into_view_if_needed()
            page.screenshot(path=str(output / f"futures-{width}.png"), full_page=True)

            # Real EventSource message shape, with no actual trading connection.
            page.evaluate("""() => {
              const source = window.futuresSources.filter(s => !s.closed).at(-1);
              source.dispatchEvent(new MessageEvent('futures_snapshots', {data: JSON.stringify({data:[{security_id:'AU2612.SHFE', trading_day:20261117, last:831.04, pre_settlement:828.02, received_at:'2026-11-16T21:02:00+08:00', exchange_timestamp:'2026-11-16T21:02:00+08:00'}]})}));
            }""")
            page.wait_for_function("document.querySelector('#quoteLast').textContent === '831.04'")
            page.evaluate("window.futuresSources.filter(s => !s.closed).at(-1).onerror()")
            assert "断开" in page.locator("#futuresQuoteStatus").inner_text()
            failure["bars"] = True
            page.locator("#reloadChartButton").click()
            page.wait_for_function("document.querySelector('#minuteChartStatus').textContent.includes('archive_incomplete')")
            assert page.evaluate("echarts.getInstanceByDom(document.querySelector('#minuteChart')).getOption().series.length") == 0
            failure["bars"] = False

            page.locator("#marketSelector").select_option("CFFEX")
            page.locator("#symbolInput").fill("IF")
            page.locator("#symbolSuggest button").filter(has_text="IF2612.CFFEX").click()
            page.wait_for_function("document.querySelector('#priceInput').step === '0.2'")
            assert page.locator("#futuresOffset option[value=CT]").evaluate("el => el.disabled")
            assert "沪深300" in page.locator("#quoteName").inner_text()
            page.locator("#marketSelector").select_option("stock")
            page.wait_for_function("!document.querySelector('#terminalShell').classList.contains('market-futures')")
            assert page.locator("#qtyInput").get_attribute("step") == "100"
            assert page.locator("#assetPanel").is_visible()
            assert page.locator("#futuresOffset").is_hidden()
            results.append({"width": width, "chart": chart})
            page.close()
        browser.close()
    assert not errors, errors
    assert not writes, writes
    (output / "result.json").write_text(json.dumps({"synthetic_market_only": True, "results": results, "writes": writes, "page_errors": errors, "market_request_count": len(requests)}, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(results))


if __name__ == "__main__":
    main()
