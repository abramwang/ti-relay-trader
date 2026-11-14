#!/usr/bin/env python3
"""Read-only research cost UI checks against an explicit server/version."""

import argparse
import csv
import io
import json
from pathlib import Path
from time import perf_counter

from playwright.sync_api import sync_playwright, expect


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:29092")
    parser.add_argument("--output", default="outputs/recovery/research_cost_web_20261114")
    args = parser.parse_args()
    output = Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    account, version = "314000045768", "fuying13-cost-research-20261114-v1"
    comparison_path = f"/v1/accounts/{account}/performance/research-cost-comparison"
    errors, writes = [], []
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page(viewport={"width": 1600, "height": 1050}, accept_downloads=True)
        page.on("pageerror", lambda error: errors.append(str(error)))

        def guard(route):
            if route.request.method not in ("GET", "HEAD"):
                writes.append(route.request.url)
                route.abort()
            else:
                route.continue_()

        page.route("**/*", guard)
        page.goto(args.base_url + "/trade#research", wait_until="domcontentloaded")
        page.locator(f'#accountTabs button[title*="{account}"]').click()
        expect(page.locator("#researchVersion")).to_be_enabled(timeout=15000)
        assert page.locator("#researchVersion").input_value() == ""
        expect(page.locator("#researchLoad")).to_be_disabled()
        page.locator("#researchVersion").select_option(version)
        started = perf_counter()
        page.locator("#researchLoad").click()
        expect(page.locator("#researchCount")).to_have_text("972 / 972 条", timeout=30000)
        elapsed = round((perf_counter() - started) * 1000, 2)
        expect(page.locator("#researchPage")).to_have_text("1 / 20")
        expect(page.locator("#researchRows tr")).to_have_count(50)
        assert page.locator("#researchRows tr").first.locator("td").nth(6).inner_text() == "--"
        page.screenshot(path=str(output / "desktop.png"), full_page=True)
        page.locator("#researchNext").click()
        expect(page.locator("#researchPage")).to_have_text("2 / 20")
        page.locator('[data-research-sort="cost"]').click()
        expect(page.locator("#researchPage")).to_have_text("1 / 20")
        page.locator("#researchRows button").first.click()
        expect(page.locator("dialog.research-detail")).to_be_visible()
        page.screenshot(path=str(output / "detail.png"))
        page.locator("dialog.research-detail button").click()
        page.locator("#researchBasis").select_option("redemption_cost_release_not_loss")
        expect(page.locator("#researchCount")).to_have_text("23 / 972 条")
        page.locator("#researchPageSize").select_option("20")
        expect(page.locator("#researchRows tr")).to_have_count(20)
        with page.expect_download() as download:
            page.locator("#researchExport").click()
        csv_text = Path(download.value.path()).read_text(encoding="utf-8-sig")
        rows = list(csv.reader(io.StringIO(csv_text)))
        assert len(rows) == 24 and all(row[13] == "" for row in rows[1:])
        assert all(row[0] == account and row[1] == version for row in rows[1:])
        page.locator("#researchBasis").select_option("")
        page.locator("#researchDate").select_option("2026-10-12")
        assert all(text == "2026-10-12" for text in page.locator("#researchRows tr td:first-child").all_text_contents())
        page.locator("#researchDate").select_option("")
        page.locator("#researchSearch").fill("nothing-matches")
        expect(page.locator("#researchExport")).to_be_disabled()
        page.locator("#researchSearch").fill("")
        page.set_viewport_size({"width": 390, "height": 844})
        page.locator(".terminal-main").evaluate("e => e.scrollTop = 0")
        page.screenshot(path=str(output / "mobile.png"), full_page=True)
        assert page.evaluate("document.documentElement.scrollWidth <= innerWidth")
        for selector in ("#researchForm", ".research-filters", ".research-table-wrap", ".research-pagination"):
            rect = page.locator(selector).bounding_box()
            assert rect["x"] >= 0 and rect["x"] + rect["width"] <= 391, (selector, rect)
        page.locator(".research-pagination").scroll_into_view_if_needed()
        page.screenshot(path=str(output / "mobile-pagination.png"))
        rect = page.locator("#researchNext").bounding_box()
        assert 94 <= rect["y"] and rect["y"] + rect["height"] < 817
        page.locator("#researchNext").click()
        expect(page.locator("#researchPage")).to_have_text("2 / 49")
        page.set_viewport_size({"width": 1600, "height": 1050})
        page.locator('#accountTabs button[title*="307000051388"]').click()
        expect(page.locator("#researchStatus")).to_have_text("该账户暂无研究版本", timeout=15000)
        expect(page.locator("#researchCount")).to_have_text("0 / 0 条")
        expect(page.locator("#researchExport")).to_be_disabled()

        # Inject only GET responses: exercise stale evidence, missing values,
        # micro-unit differences, malformed responses and in-flight switching.
        data = page.request.get(args.base_url + comparison_path + "?version_id=" + version).json()
        comparison = data["data"]["comparison"]
        comparison["anchor_and_allocations_match"] = False
        comparison["evidence_changes"] = [{"scope": "opening", "identity": "review-fixture", "kind": "changed", "changed_fields": ["close_total_cost"]}]
        row = comparison["days"][0]["rows"][0]
        row["research"]["close_total_cost"] = 0.000001
        row["stored"] = dict(row["research"], close_total_cost=0)
        row["research_minus_stored"] = {"close_cost_micros": 1}
        row["outcome"] = "changed"
        comparison["counts"]["changed"] = 1
        comparison["days"][0]["rows"] = [row]
        comparison["days"] = [comparison["days"][0]]
        page.route("**" + comparison_path + "?*", lambda route: route.fulfill(json=data))
        page.locator(f'#accountTabs button[title*="{account}"]').click()
        expect(page.locator("#researchVersion")).to_be_enabled(timeout=15000)
        page.locator("#researchVersion").select_option(version)
        page.locator("#researchLoad").click()
        expect(page.locator("#researchEvidence")).to_have_attribute("data-state", "stale")
        expect(page.locator("#researchRows tr")).to_have_count(1)
        assert page.locator("#researchRows td").nth(6).inner_text() == "0"
        assert page.locator("#researchRows td").nth(7).inner_text() == "0.000001"
        page.screenshot(path=str(output / "stale-fixture.png"))
        page.unroute("**" + comparison_path + "?*")
        page.route("**" + comparison_path + "?*", lambda route: route.fulfill(status=502, content_type="text/html", body="<html>upstream unavailable</html>"))
        page.locator("#researchLoad").click()
        expect(page.locator("#researchStatus")).to_contain_text("JSON")
        expect(page.locator("#researchExport")).to_be_disabled()
        expect(page.locator("#researchCount")).to_have_text("0 / 0 条")
        page.unroute("**" + comparison_path + "?*")
        comparison["account_id"] = "wrong-account"
        page.route("**" + comparison_path + "?*", lambda route: route.fulfill(json=data))
        page.locator("#researchLoad").click()
        expect(page.locator("#researchStatus")).to_contain_text("校验失败")
        expect(page.locator("#researchExport")).to_be_disabled()
        page.unroute("**" + comparison_path + "?*")
        held = []
        page.route("**" + comparison_path + "?*", lambda route: held.append(route))
        page.locator("#researchLoad").click()
        page.wait_for_timeout(100)
        assert held
        page.locator('#accountTabs button[title*="307000051388"]').click()
        expect(page.locator("#researchStatus")).to_have_text("该账户暂无研究版本", timeout=15000)
        comparison["account_id"] = account
        held[0].fulfill(json=data)
        page.wait_for_timeout(100)
        expect(page.locator("#researchCount")).to_have_text("0 / 0 条")
        assert not writes, writes
        assert not errors, errors
        report = {"passed": True, "account": account, "version": version, "rows": 972, "days": 22, "load_ms": elapsed, "csv_filtered_rows": 23, "writes": writes, "page_errors": errors, "fixture_checks": ["stale", "zero-vs-missing", "micro-unit", "non-json", "scope", "account-race"], "screenshots": str(output)}
        (output / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps(report, ensure_ascii=False))
        browser.close()


if __name__ == "__main__":
    main()
