#!/usr/bin/env python3
"""Verify published account PnL in the asset page and CSV without broker writes."""

from __future__ import annotations

import argparse
import csv
import io
import json
from pathlib import Path
from urllib.request import ProxyHandler, build_opener

from playwright.sync_api import sync_playwright


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--trade-date", required=True)
    parser.add_argument("--output-dir", required=True)
    args = parser.parse_args()
    base = args.base_url.rstrip("/")
    output = Path(args.output_dir)
    output.mkdir(parents=True, exist_ok=True)
    opener = build_opener(ProxyHandler({}))
    with opener.open(
        f"{base}/v1/accounts/{args.account_id}/performance/economic-nav?trade_date={args.trade_date}",
        timeout=60,
    ) as response:
        navs = json.load(response)["data"]["navs"]
    nav = next(item for item in navs if item["is_current"] and item["status"] != "blocked")
    expected = float(nav["account_day_pnl"])
    results = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        for width, height in ((1600, 1100), (430, 932)):
            page = browser.new_page(viewport={"width": width, "height": height})
            errors: list[str] = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            page.route(
                "**/v1/**",
                lambda route: route.continue_()
                if route.request.method == "GET"
                else route.abort(),
            )
            page.goto(f"{base}/trade#asset", wait_until="domcontentloaded")
            page.locator(f'#accountTabs button[title*="{args.account_id}"]').click()
            page.wait_for_function(
                """expected => {
                    const active = document.querySelector('#accountTabs button.active');
                    const pnl = document.querySelector('#dayProfit');
                    return active?.title.includes(expected.account) &&
                        pnl?.title.includes(expected.formula) &&
                        Math.abs(Number(pnl.textContent.replaceAll(',', '')) - expected.pnl) < 0.01;
                }""",
                arg={"account": args.account_id, "formula": nav["formula_version"], "pnl": expected},
                timeout=30_000,
            )
            page.wait_for_function("!document.querySelector('#exportAssetButton').disabled")
            with page.expect_download() as pending:
                page.locator("#exportAssetButton").click()
            download = pending.value
            csv_path = output / f"asset-{width}.csv"
            download.save_as(csv_path)
            rows = list(csv.reader(io.StringIO(csv_path.read_text(encoding="utf-8-sig"))))
            summary = {row[0]: row[1] for row in rows if len(row) == 2}
            assert abs(float(summary["当日盈亏"]) - expected) < 0.000001, summary
            assert summary["当日盈亏来源"] == nav["formula_version"], summary
            assert not errors, errors
            screenshot = output / f"asset-{width}.png"
            page.screenshot(path=str(screenshot), full_page=True)
            results.append({
                "width": width,
                "day_pnl": page.locator("#dayProfit").inner_text(),
                "day_pnl_source": page.locator("#dayProfit").get_attribute("title"),
                "csv_day_pnl": summary["当日盈亏"],
                "csv_close_pnl": summary["平仓盈亏"],
                "screenshot": str(screenshot),
                "errors": errors,
            })
            page.close()
        browser.close()
    print(json.dumps({"account_id": args.account_id, "trade_date": args.trade_date, "results": results}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
