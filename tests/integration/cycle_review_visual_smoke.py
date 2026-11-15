#!/usr/bin/env python3
"""Read-only UI regression for published cycle reviews; mutations are blocked."""

import argparse
import copy
import json
from pathlib import Path

from playwright.sync_api import expect, sync_playwright


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--output", default="outputs/recovery/cycle_review_ui_20261115")
    args = parser.parse_args()
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    account = "314000045768"
    path = f"/v1/accounts/{account}/performance/cycle-reviews"
    errors, writes = [], []
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page(viewport={"width": 1600, "height": 1050})
        page.on("pageerror", lambda error: errors.append(str(error)))

        def guard(route):
            if route.request.method not in ("GET", "HEAD"):
                writes.append(route.request.url)
                route.abort()
            else:
                route.continue_()

        page.route("**/*", guard)
        page.goto(args.base_url + "/trade#performance", wait_until="domcontentloaded")
        page.locator(f'#accountTabs button[title*="{account}"]').click()

        def load(start, end):
            expect(page.locator("#loadPerformanceButton")).to_be_enabled(timeout=60000)
            page.locator("#perfDateFrom").fill(start)
            page.locator("#perfDateTo").fill(end)
            page.locator("#loadPerformanceButton").click()

        load("20261012", "20261113")
        expect(page.locator("#cycleReviewStatus")).to_have_text("14 组 · 清算未确认", timeout=60000)
        expect(page.locator("#cycleReviewRows tr")).to_have_count(14)
        expect(page.locator("#cycleFeeState")).to_have_text("14 / 14 组已核对")
        expect(page.locator("#cycleReviewRows tr").first.locator("td").nth(6)).to_have_text("-7,702.84")
        carried = page.locator('[data-cycle-id="2026-10-15/510310.SH"]')
        carried.locator("summary").click()
        expect(carried).to_contain_text("600026.SH")
        expect(carried).to_contain_text("601872.SH")
        expect(carried).to_contain_text("后续卖出已核对")
        expect(carried).to_contain_text("6,216.00 元（已计入周期）")
        expect(carried).to_contain_text("9,450.00 元（已计入周期）")
        carried.locator("details").scroll_into_view_if_needed()
        page.wait_for_timeout(4000)
        page.screenshot(path=str(output / "carried-components.png"))
        carried.locator("summary").click()
        unknown = page.locator('[data-cycle-id="2026-10-14/159869.SZ"]')
        unknown.locator("summary").click()
        expect(unknown).to_contain_text("159900.SZ")
        expect(unknown).to_contain_text("划转性质或去向待核验")
        unknown.locator("summary").click()
        page.locator("#cycleReviewRows summary").first.click()
        assert page.locator(".cycle-review-scroll").evaluate("e => e.scrollHeight <= e.clientHeight + 1")
        assert page.locator(".cycle-review-section").evaluate("e => e.scrollHeight <= e.clientHeight + 1")
        page.wait_for_timeout(4000)
        page.locator(".cycle-review-section").screenshot(path=str(output / "desktop.png"))
        page.locator(".cycle-review-footer").scroll_into_view_if_needed()
        page.screenshot(path=str(output / "desktop-bottom.png"))
        rect = page.locator(".cycle-review-footer").bounding_box()
        assert 94 <= rect["y"] and rect["y"] + rect["height"] <= 1022, rect
        page.set_viewport_size({"width": 390, "height": 844})
        page.locator(".cycle-review-toolbar").scroll_into_view_if_needed()
        page.screenshot(path=str(output / "mobile.png"))
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        rect = page.locator(".cycle-review-scroll").bounding_box()
        assert rect["x"] >= 0 and rect["x"] + rect["width"] <= 391, rect
        carried.locator("summary").click()
        carried.locator(".cycle-component-gap").last.scroll_into_view_if_needed()
        page.screenshot(path=str(output / "mobile-components.png"))
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        carried.locator("summary").click()
        page.set_viewport_size({"width": 1600, "height": 1050})
        load("20261104", "20261104")
        expect(page.locator("#cycleReviewStatus")).to_have_text("1 组 · 清算未确认", timeout=60000)
        expect(page.locator("#cycleReviewRows td").nth(6)).to_have_text("1,134.73")
        load("20261105", "20261113")
        expect(page.locator("#cycleReviewStatus")).to_have_text("区间内无核对记录", timeout=60000)

        raw = page.request.get(args.base_url + path + "?date_from=20261012&date_to=20261113").json()
        assert raw["data"]["account_nav_publishable"] is False
        assert raw["data"]["final_settlement"] is False
        old = page.request.get(args.base_url + path + "?date_from=20261012&date_to=20261113&version_id=fuying13-cycle-20261115-v1").json()["data"]
        assert old["version_id"] == "fuying13-cycle-20261115-v1"
        assert [r["profit"] for r in old["cycles"]] == [r["profit"] for r in raw["data"]["cycles"]]
        assert sum(len(r["component_review"]["same_day_gaps"]) for r in raw["data"]["cycles"]) == 11
        fixture = copy.deepcopy(raw)
        fixture["data"]["cycles"] = fixture["data"]["cycles"] * 2
        page.route("**" + path + "?*", lambda route: route.fulfill(json=fixture))
        load("20261012", "20261113")
        expect(page.locator("#cycleReviewStatus")).to_have_text("28 组 · 清算未确认", timeout=60000)
        expect(page.locator("#cycleReviewRows tr")).to_have_count(20)
        page.locator("#cycleReviewNext").click()
        expect(page.locator("#cycleReviewRows tr")).to_have_count(8)
        expect(page.locator("#cycleReviewPage")).to_have_text("2 / 2")
        page.unroute("**" + path + "?*")
        page.route("**" + path + "?*", lambda route: route.fulfill(status=503, json={"error": {"message": "cycle review unavailable"}}))
        load("20261012", "20261113")
        expect(page.locator("#cycleReviewStatus")).to_have_text("读取失败", timeout=60000)
        expect(page.locator("#cycleReviewRows")).not_to_contain_text("-7,702.84")
        expect(page.locator("#cycleAccountState")).to_have_text("连续暂定净值")
        page.unroute("**" + path + "?*")
        page.locator('#accountTabs button[title*="307000051388"]').click()
        expect(page.locator("#cycleReviewStatus")).to_have_text("尚无核对版本", timeout=60000)
        expect(page.locator("#cycleFeeState")).to_have_text("--")
        assert not errors, errors
        assert not writes, writes
        report = {"passed": True, "cycles": 14, "checks": ["published-values", "component-gaps", "later-sales-already-included", "immutable-old-version", "date-filter", "empty-range", "account-isolation", "pagination-fixture", "error-isolation", "desktop", "mobile"], "page_errors": errors, "writes": writes}
        (output / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps(report, ensure_ascii=False))
        browser.close()


if __name__ == "__main__":
    main()
