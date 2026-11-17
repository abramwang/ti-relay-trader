#!/usr/bin/env python3
"""Read-only browser check of the configured CTP observation account."""
import json
from pathlib import Path
from playwright.sync_api import sync_playwright


def main():
    out = Path("outputs/recovery/futures_terminal_account_20261117")
    out.mkdir(parents=True, exist_ok=True)
    results, writes, errors = [], [], []
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        for width, height in [(1600, 1000), (390, 844)]:
            page = browser.new_page(viewport={"width": width, "height": height})
            page.on("pageerror", lambda e: errors.append(str(e)))

            def guard(route):
                if route.request.method not in {"GET", "HEAD", "OPTIONS"}:
                    writes.append(route.request.url)
                    route.abort()
                else:
                    route.continue_()

            page.route("**/v1/**", guard)
            page.goto("http://127.0.0.1:9092/trade", wait_until="domcontentloaded")
            page.locator("#futuresAccountEntry").wait_for(state="visible")
            assert "300052141" in page.locator("#futuresAccountEntry").inner_text()
            page.locator("#futuresAccountEntry").click()
            page.wait_for_function("document.querySelector('#terminalShell').classList.contains('market-futures')")
            page.wait_for_timeout(1500)
            assert page.locator("#futuresAccount").input_value() == "300052141"
            assert page.locator("#submitOrderButton").is_disabled()
            assert page.locator("#assetPanel").is_hidden()
            assert page.locator("#futuresQueryTable tbody tr").count() == 12
            assert "初始化失败" not in page.locator("body").inner_text()
            assert "历史" in page.locator("#futuresQueryStatus").inner_text()
            page.locator("#futuresQueryTable th button").first.click()
            rows = page.locator("#futuresQueryTable tbody tr td:first-child").all_text_contents()
            assert rows == sorted(rows, reverse=True)
            page.locator('[data-futures-query="account.asset.query"]').click()
            assert "missing_biz_type" in page.locator("#futuresQueryStatus").inner_text()
            assert "暂无可展示" in page.locator("#futuresQueryTable").inner_text()
            page.locator('[data-futures-query="order.list.query"]').click()
            assert "完整查询：0" in page.locator("#futuresQueryTable").inner_text()
            page.locator('[data-futures-query="fill.list.query"]').click()
            assert "完整查询：0" in page.locator("#futuresQueryTable").inner_text()
            page.locator('[data-futures-query="account.positions.query"]').click()
            page.locator("#futuresAccountRefresh").click()
            page.wait_for_timeout(1500)
            assert page.locator("#futuresQueryTable tbody tr").count() == 12
            page.locator("#futuresAccountTitle").scroll_into_view_if_needed()
            page.screenshot(path=str(out / f"account-{width}.png"), full_page=True)
            overflow = page.evaluate("document.documentElement.scrollWidth > innerWidth + 2")
            results.append({"viewport": [width, height], "rows": 12, "horizontal_overflow": overflow, "connection": page.locator("#futuresConnection").inner_text()})
            assert not overflow, results
            # Browser-only larger data and failure states; never publish fixtures.
            sample = page.request.get("http://127.0.0.1:9092/v1/futures/terminal").json()
            query = next(q for q in sample["data"]["accounts"][0]["queries"] if q["action"] == "account.positions.query")
            query["positions"] = [dict(query["positions"][i % 12]) for i in range(45)]
            query["row_count"] = 45
            page.route("**/v1/futures/terminal", lambda route: route.fulfill(json=sample))
            page.locator("#futuresAccountRefresh").click()
            page.wait_for_function("document.querySelector('#futuresQueryCount').textContent.startsWith('45')")
            assert page.locator("#futuresQueryTable tbody tr").count() == 20
            page.locator("#futuresQueryNext").click()
            page.locator("#futuresQueryNext").click()
            assert page.locator("#futuresQueryTable tbody tr").count() == 5
            assert page.locator("#futuresQueryNext").is_disabled()
            query["state"] = "collecting"
            query["row_count"] = None
            page.locator("#futuresAccountRefresh").click()
            page.wait_for_function("document.querySelector('#futuresQueryStatus').textContent.includes('分页未完整')")
            assert "暂无可展示" in page.locator("#futuresQueryTable").inner_text()
            page.close()
        browser.close()
    assert not writes and not errors, (writes, errors)
    (out / "browser-result.json").write_text(json.dumps({"results": results, "writes": writes, "errors": errors}, ensure_ascii=False, indent=2))
    print(json.dumps(results, ensure_ascii=False))


if __name__ == "__main__":
    main()
