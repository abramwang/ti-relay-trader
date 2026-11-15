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
        expect(page.locator("#cycleDailyStatus")).to_have_text("25 日 · 9 日有推定时序暴露")
        expect(page.locator("#cycleDailyRows tr")).to_have_count(10)
        day19 = page.locator('[data-cash-day="2026-10-19"]')
        expect(day19.locator("td").nth(3)).to_have_text("12,688.20")
        day19.locator("summary").click()
        expect(day19).to_contain_text("推定源日")
        expect(day19).to_contain_text("实际记账日")
        assert page.locator(".cycle-daily-scroll").first.evaluate("e => e.scrollHeight <= e.clientHeight + 1")
        assert page.locator(".cycle-daily-section").first.evaluate("e => e.scrollHeight <= e.clientHeight + 1")
        day19.scroll_into_view_if_needed()
        page.wait_for_timeout(4000)
        page.screenshot(path=str(output / "daily-desktop.png"))
        page.locator(".cycle-daily-footer").first.scroll_into_view_if_needed()
        rect = page.locator(".cycle-daily-footer").first.bounding_box()
        assert 94 <= rect["y"] and rect["y"] + rect["height"] <= 1022, rect
        page.screenshot(path=str(output / "daily-footer.png"))
        page.set_viewport_size({"width": 390, "height": 844})
        day19.locator("details").scroll_into_view_if_needed()
        page.screenshot(path=str(output / "daily-mobile.png"))
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        page.set_viewport_size({"width": 1600, "height": 1050})
        day19.locator("summary").click()
        page.locator("#cycleDailyNext").click()
        expect(page.locator("#cycleDailyPage")).to_have_text("2 / 3")
        expect(page.locator('[data-cash-day="2026-10-29"]')).to_contain_text("该范围无已观察暴露")
        page.locator("#cycleDailyNext").click()
        expect(page.locator("#cycleDailyRows tr")).to_have_count(5)
        expect(page.locator("#attributionReviewStatus")).to_have_text("25 日 · 条件性归因")
        expect(page.locator("#attributionReviewRows tr")).to_have_count(10)
        page.locator("#attributionReviewNext").click()
        page.locator("#attributionReviewNext").click()
        expect(page.locator("#attributionReviewPage")).to_have_text("3 / 3")
        expect(page.locator("#attributionReviewRows tr")).to_have_count(5)
        last_day = page.locator('[data-attribution-day="2026-11-13"]')
        expect(last_day.locator("td").nth(1)).to_have_text("-921,767.05")
        expect(last_day.locator("td").nth(7)).to_have_text("0.040450")
        last_day.locator("summary").click()
        expect(last_day).to_contain_text("费用补证影响 -113.17")
        expect(last_day).to_contain_text("尚未解释差额 0.000000")
        expect(last_day).to_contain_text("missing_repo_fee")
        last_day.scroll_into_view_if_needed()
        page.screenshot(path=str(output / "attribution-desktop.png"))
        page.set_viewport_size({"width": 390, "height": 844})
        last_day.locator("details").scroll_into_view_if_needed()
        page.screenshot(path=str(output / "attribution-mobile.png"))
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        page.locator("#attributionReviewNext").scroll_into_view_if_needed()
        assert page.locator('[aria-label="每日策略核对"]').evaluate("e => e.scrollHeight <= e.clientHeight + 1")
        last_day.locator("summary").click()
        page.set_viewport_size({"width": 1600, "height": 1050})
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
        expect(unknown).to_contain_text("已匹配券商替代现金")
        expect(unknown).to_contain_text("116,112.60 元（已计入周期，不重复增加）")
        expect(unknown).to_contain_text("2026-10-14 18:52:08+08:00")
        expect(unknown).to_contain_text("2734197192")
        unknown.locator(".cycle-component-gap").scroll_into_view_if_needed()
        page.screenshot(path=str(output / "cash-explanation.png"))
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
        expect(page.locator("#attributionReviewRows tr")).to_have_count(1)
        expect(page.locator('[data-attribution-day="2026-11-04"]')).to_contain_text("50,257.33")
        load("20261105", "20261113")
        expect(page.locator("#cycleReviewStatus")).to_have_text("区间内无核对记录", timeout=60000)
        expect(page.locator("#cycleDailyStatus")).to_have_text("7 日 · 0 日有推定时序暴露")

        raw = page.request.get(args.base_url + path + "?date_from=20261012&date_to=20261113").json()
        assert raw["data"]["account_nav_publishable"] is False
        assert raw["data"]["final_settlement"] is False
        assert len(raw["data"]["attribution_review"]["days"]) == 25
        assert len(raw["data"]["attribution_review"]["statement_daily_comparison"]["days"]) == 4
        old = page.request.get(args.base_url + path + "?date_from=20261012&date_to=20261113&version_id=fuying13-cycle-20261115-v1").json()["data"]
        assert old["version_id"] == "fuying13-cycle-20261115-v1"
        assert old["attribution_review"] is None
        assert [r["profit"] for r in old["cycles"]] == [r["profit"] for r in raw["data"]["cycles"]]
        assert sum(len(r["component_review"]["same_day_gaps"]) for r in raw["data"]["cycles"]) == 11
        cash = [g["cash_explanation"] for r in raw["data"]["cycles"] for g in r["component_review"]["same_day_gaps"] if g.get("cash_explanation")]
        assert len(cash) == len({g["broker_flow_id"] for g in cash}) == 9
        assert sum(g["amount_cents"] for g in cash) == 115123050
        assert all(g["already_included"] for g in cash)
        days = raw["data"]["daily_review"]["days"]
        assert len(days) == 25
        assert len(raw["data"]["daily_review"]["inferred_cash_legs"]) == 10
        assert sum(d["cash_timing"]["inferred_source"]["net_carry_adjustment_cents"] != 0 for d in days) == 7
        assert sum(d["cash_timing"]["inferred_source"]["net_carry_adjustment_cents"] for d in days) == 0
        assert sum(d["account_basis"]["no_observed_timing_or_component_exposure"] for d in days) == 9
        for d in days:
            a, t = d["account_basis"], d["cash_timing"]
            assert a["published_pnl_cents"] == a["broker_basis_pnl_cents"] + t["conditional_timing_adjustment_cents"]
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
        expect(page.locator("#cycleDailyRows")).not_to_contain_text("12,688.20")
        expect(page.locator("#cycleReviewRows")).not_to_contain_text("-7,702.84")
        expect(page.locator("#attributionReviewRows")).not_to_contain_text("-921,767.05")
        expect(page.locator("#cycleAccountState")).to_have_text("连续暂定净值")
        page.unroute("**" + path + "?*")
        page.locator('#accountTabs button[title*="307000051388"]').click()
        expect(page.locator("#cycleReviewStatus")).to_have_text("尚无核对版本", timeout=60000)
        expect(page.locator("#cycleFeeState")).to_have_text("--")
        expect(page.locator("#cycleDailyStatus")).to_have_text("尚无逐日核对版本")
        expect(page.locator("#attributionReviewRows")).to_contain_text("尚无策略核对版本")
        assert not errors, errors
        assert not writes, writes
        report = {"passed": True, "cycles": 14, "checks": ["published-values", "component-gaps", "later-sales-already-included", "immutable-old-version", "daily-timing-identity", "gross-exposure", "daily-pagination", "daily-unclipped-footer", "date-filter", "empty-range", "account-isolation", "pagination-fixture", "error-isolation", "desktop", "mobile"], "page_errors": errors, "writes": writes}
        (output / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps(report, ensure_ascii=False))
        browser.close()


if __name__ == "__main__":
    main()
