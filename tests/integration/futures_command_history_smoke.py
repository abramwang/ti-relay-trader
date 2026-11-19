#!/usr/bin/env python3
"""Browser-only command history: every API is intercepted; no broker/Redis I/O."""
import argparse
import json
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import sync_playwright


def account(account_id):
    return {"account_id": account_id, "alias": "中金所测试观察账户",
            "route": {"env": "test", "broker_id": "ctp_4900", "gateway_id": account_id, "account_id": account_id},
            "connection_state": "stopped", "queries": [], "local_orders": []}


def command(number, a):
    state = ["published", "registered", "attempted", "not_published"][number % 4]
    result = {"command_id": number, "account_id": a["account_id"], "gateway_id": a["route"]["gateway_id"],
              "action": "order.submit", "message_id": "synthetic-message-" + str(number),
              "idempotency_key": "synthetic-key-" + str(number), "registered_at": "2026-11-19T01:05:00Z",
              "broker_state_known": False, "retry_safe": False,
              "publication": {"command_id": number, "state": state, "broker_accepted": False,
                              "command_published": {"published": True, "registered": None, "attempted": None, "not_published": False}[state]},
              "orders": [{"account_id": a["account_id"], "gateway_order_id": "synthetic-order-" + str(number),
                          "trade_date": "20261119", "symbol": "IF2612", "exchange": "CFFEX",
                          "trade_side": "B", "offset_type": "O", "order_qty": 2, "limit_price": "3900.20"}]}
    if number == 44:
        result["action"] = "order.cancel"
        result.pop("orders")
        result["cancel"] = {"gateway_order_id": "synthetic-order-43", "trade_date": "20261119", "cancel_id": "synthetic-cancel-44"}
    if number == 43:
        result["action"] = "order.batch.submit"
        result["orders"].append({**result["orders"][0], "symbol": "IM2612", "gateway_order_id": "synthetic-child", "limit_price": "6010.40"})
    if number in {44, 40, 36}:
        receipt = {44: "failed", 40: "accepted", 36: "invalid"}[number]
        result["response"] = {"command_id": number, "action": result["action"], "state": receipt,
                              "response_validated": receipt != "invalid", "native_audit_verified": False,
                              "order_state_changed": False, "retry_safe": False}
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:19194")
    parser.add_argument("--output", default="outputs/recovery/futures-command-history-20261119")
    args = parser.parse_args()
    out = Path(args.output)
    out.mkdir(parents=True, exist_ok=True)
    errors, writes, results = [], [], []
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        for width, height in [(1600, 1000), (390, 844)]:
            page = browser.new_page(viewport={"width": width, "height": height})
            page.on("pageerror", lambda e: errors.append(str(e)))
            page.add_init_script("window.EventSource=class extends EventTarget {close(){}}")
            a, b = account("300052141"), account("300052142")
            a["local_orders"] = [{"account_id": a["account_id"], "gateway_order_id": "synthetic-order-43",
                                  "trade_date": "20261119", "gateway_status": "filled"}]
            for action, key, row in [
                ("account.asset.query", "assets", {"futures": {"scope": {"account_id": a["account_id"], "currency_id": "CNY", "biz_type": "1"}, "balance": "100000", "scope_verified": False}}),
                ("account.positions.query", "positions", {"symbol": "IF2612", "exchange": "CFFEX", "futures": {"posi_direction": "2", "position": 2}}),
                ("order.list.query", "orders", {"gateway_order_id": "synthetic-query-order", "symbol": "IF2612", "exchange": "CFFEX", "gateway_status": "working"}),
                ("fill.list.query", "fills", {"fill_id": "synthetic-fill", "symbol": "IF2612", "exchange": "CFFEX", "qty": 1}),
            ]:
                a["queries"].append({"action": action, "state": "response_ready", "response_validated": True,
                                     "response_persisted": True, "reviewed": False, "business_published": False,
                                     "row_count": 1, "code": "OK", key: [row]})
            state = {"fail": False, "history_gets": []}

            def intercept(route):
                request = route.request
                parsed = urlparse(request.url)
                if not request.url.startswith(args.base_url):
                    route.abort()
                    return
                if not parsed.path.startswith("/v1/"):
                    route.continue_()
                    return
                if request.method != "GET":
                    writes.append(request.url)
                    route.abort()
                    return
                if parsed.path == "/v1/futures/commands":
                    state["history_gets"].append(request.url)
                    if state["fail"]:
                        route.fulfill(status=503, json={"ok": False, "error": {"message": "synthetic failure"}})
                        return
                    query = parse_qs(parsed.query)
                    current = a if query["account_id"] == [a["account_id"]] else b
                    before = int(query.get("before_id", [46])[0])
                    values = list(range(min(45, before - 1), 0, -1)) if current is a else []
                    selected = values[:20]
                    data = {"route": current["route"], "items": [command(n, current) for n in selected]}
                    if len(values) > 20:
                        data["next_before_id"] = selected[-1]
                elif parsed.path == "/v1/futures/terminal":
                    data = {"accounts": [a, b]}
                elif parsed.path == "/v1/status":
                    data = {"environment": "test", "status": "ok", "trading_day": {"date": "20261119", "trade_date": "20261119"}}
                else:
                    data = {"data": [], "meta": {}, "accounts": [], "positions": [], "orders": [], "fills": []}
                route.fulfill(json={"ok": True, "data": data})

            page.route("**/*", intercept)
            page.goto(args.base_url + "/trade", wait_until="domcontentloaded")
            page.locator("#futuresAccountEntry").wait_for(state="visible")
            page.locator("#futuresAccountEntry").click()
            for action in ["account.asset.query", "account.positions.query", "order.list.query", "fill.list.query"]:
                page.locator(f'[data-futures-query="{action}"]').click()
                assert "回包校验通过" in page.locator("#futuresQueryStatus").inner_text()
                assert page.locator("#futuresQueryTable tbody tr td.empty-state").count() == 0
            page.locator('[data-futures-query="commands"]').click()
            page.locator('[data-command-id="45"]').wait_for()
            assert page.locator("#futuresCommandTable tbody tr").count() == 20
            assert page.locator("#submitOrderButton").is_disabled()
            assert page.locator("#futuresQueryWrap").is_hidden()
            assert "未读取到委托回报" in page.locator('[data-command-id="45"]').inner_text()
            assert "发布结果未知" in page.locator('[data-command-id="42"]').inner_text()
            assert "全部成交" in page.locator('[data-command-id="44"]').inner_text()
            assert "命令失败" in page.locator('[data-command-id="44"]').inner_text()
            assert "命令已接收" in page.locator('[data-command-id="40"]').inner_text()
            assert "回执冲突或无效" in page.locator('[data-command-id="36"]').inner_text()
            assert "已撤单" not in page.locator('[data-command-id="44"]').inner_text()
            assert "09:05:00" in page.locator('[data-command-id="45"]').inner_text()
            page.locator("#futuresCommandNext").click()
            page.locator('[data-command-id="25"]').wait_for()
            page.locator("#futuresCommandNext").click()
            page.locator('[data-command-id="5"]').wait_for()
            assert page.locator("#futuresCommandTable tbody tr").count() == 5
            assert page.locator("#futuresCommandNext").is_disabled()
            page.locator("#futuresCommandPrev").click()
            page.locator('[data-command-id="25"]').wait_for()
            page.locator("#futuresCommandRefresh").click()
            page.locator('[data-command-id="45"]').wait_for()
            page.locator('[data-command-id="45"] summary').click()
            assert "synthetic-key-45" in page.locator('[data-command-id="45"] pre').inner_text()
            page.wait_for_timeout(5500)
            assert page.locator('[data-command-id="45"] details').evaluate("e=>e.open")
            page.locator('[data-command-id="45"] summary').click()
            page.locator("#futuresCommandTable").evaluate("e=>e.parentElement.scrollLeft=0")
            page.locator("#futuresCommandView").scroll_into_view_if_needed()
            assert page.evaluate("document.documentElement.scrollWidth <= innerWidth + 2")
            assert page.locator("#futuresCommandView").evaluate("e=>e.scrollWidth <= e.clientWidth+2")
            page.screenshot(path=str(out / f"commands-{width}.png"), full_page=True)
            page.locator(".futures-ledger-panel").screenshot(path=str(out / f"panel-{width}.png"))

            # A new page/document has no locally stored commands but reads the same PG view.
            assert page.evaluate("sessionStorage.getItem('relay.futures.test.commands.v1')") is None
            page.reload(wait_until="domcontentloaded")
            page.locator("#futuresAccountEntry").wait_for(state="visible")
            page.locator("#futuresAccountEntry").click()
            page.locator('[data-futures-query="commands"]').click()
            page.locator('[data-command-id="45"]').wait_for()
            page.locator("#futuresAccount").select_option(b["account_id"])
            page.wait_for_function("document.querySelector('#futuresCommandTable').textContent.includes('当前路由暂无已登记')")
            assert page.locator("#futuresCommandTable [data-command-id]").count() == 0
            state["fail"] = True
            page.locator("#futuresAccount").select_option(a["account_id"])
            page.wait_for_function("document.querySelector('#futuresCommandStatus').dataset.failed === 'true'")
            assert "不代表没有委托" in page.locator("#futuresCommandTable").inner_text()
            assert page.locator("#futuresCommandNext").is_disabled()
            page.locator("#futuresCommandView").scroll_into_view_if_needed()
            page.screenshot(path=str(out / f"error-{width}.png"), full_page=True)
            state["fail"] = False
            page.locator("#futuresCommandRefresh").click()
            page.locator('[data-command-id="45"]').wait_for()
            assert page.locator("#submitOrderButton").is_disabled()
            results.append({"viewport": [width, height], "history_gets": len(state["history_gets"]), "cross_document_history": True, "writes": 0})
            page.close()
        browser.close()
    assert not writes and not errors, (writes, errors)
    (out / "browser-result.json").write_text(json.dumps({"synthetic_only": True, "results": results, "writes": writes, "errors": errors}, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(results))


if __name__ == "__main__":
    main()
