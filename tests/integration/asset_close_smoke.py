#!/usr/bin/env python3
"""Check close assets, repo principal and nullable PnL in the page and CSV, using GET only."""

from __future__ import annotations

import argparse
import csv
import io
import json
from datetime import datetime
from pathlib import Path
from urllib.parse import urlsplit
from urllib.request import ProxyHandler, build_opener
from zoneinfo import ZoneInfo

from playwright.sync_api import sync_playwright


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--account-id", required=True)
    parser.add_argument("--trade-date", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--repo-only-fixture", action="store_true")
    parser.add_argument("--browser-date", help="browser-only Shanghai date override, YYYYMMDD")
    args = parser.parse_args()
    base = args.base_url.rstrip("/")
    output = Path(args.output_dir)
    output.mkdir(parents=True, mode=0o700, exist_ok=True)
    opener = build_opener(ProxyHandler({}))
    asset_path = f"/v1/accounts/{args.account_id}/asset"
    with opener.open(f"{base}{asset_path}?trade_date={args.trade_date}", timeout=20) as response:
        asset = json.load(response)["data"]["asset"]
    if args.repo_only_fixture:
        asset = dict(asset, cash_total=1000, cash_available=1000, market_value=0,
                     net_asset=4000, reverse_repo_receivable=3000,
                     account_day_pnl_status="pending", account_day_pnl=None, day_profit=-9999999)
    status = asset.get("account_day_pnl_status", "")
    pending_label = {"pending": "待核算", "blocked": "待复核", "snapshot_mismatch": "待核对"}.get(status)
    assert pending_label or status == "available", asset
    results = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        for width, height in ((1600, 1100), (430, 932)):
            page = browser.new_page(viewport={"width": width, "height": height})
            if args.browser_date:
                page.clock.set_fixed_time(datetime.strptime(args.browser_date, "%Y%m%d").replace(hour=10, tzinfo=ZoneInfo("Asia/Shanghai")))
            errors, writes = [], []
            asset_requests = []
            page.on("pageerror", lambda error: errors.append(str(error)))

            def route_api(route):
                request = route.request
                path = urlsplit(request.url).path
                if path == asset_path:
                    asset_requests.append(request.url)
                if request.method != "GET":
                    writes.append(path)
                    route.abort()
                elif args.repo_only_fixture and path == asset_path:
                    route.fulfill(json={"ok": True, "data": {"asset": asset}})
                elif args.repo_only_fixture and path in (asset_path.replace("/asset", "/positions"), asset_path.replace("/asset", "/positions/history")):
                    route.fulfill(json={"ok": True, "data": {"positions": [], "count": 0, "next_cursor": ""}})
                else:
                    route.continue_()

            page.route("**/v1/**", route_api)
            page.goto(f"{base}/trade#asset", wait_until="domcontentloaded")
            page.locator(f'#accountTabs button[title*="{args.account_id}"]').click()
            page.locator("#assetTradeDate").fill(args.trade_date)
            page.locator("#queryAssetButton").click()
            page.wait_for_function(
                """expected => {
                    const number = id => Number(document.getElementById(id).textContent.replaceAll(',', ''));
                    const pnl = document.getElementById('dayProfit').textContent;
                    return Math.abs(number('netAsset') - expected.asset) < .01 &&
                        Math.abs(number('reverseRepoReceivable') - expected.repo) < .01 &&
                        (expected.pending ? pnl === expected.pending : Math.abs(number('dayProfit') - expected.pnl) < .01);
                }""",
                arg={"asset": asset["net_asset"], "repo": asset["reverse_repo_receivable"],
                     "pending": pending_label, "pnl": asset.get("account_day_pnl")}, timeout=30000,
            )
            assert "逆回购应收" in page.locator("#netAsset").get_attribute("title")
            with page.expect_download() as pending:
                page.locator("#exportAssetButton").click()
            csv_path = output / f"asset-{width}.csv"
            pending.value.save_as(csv_path)
            rows = csv.reader(io.StringIO(csv_path.read_text(encoding="utf-8-sig")))
            summary = {row[0]: row[1] for row in rows if len(row) == 2}
            assert abs(float(summary["总资产"]) - asset["net_asset"]) < .01, summary
            assert abs(float(summary["逆回购应收"]) - asset["reverse_repo_receivable"]) < .01, summary
            if pending_label:
                assert summary["当日盈亏"] == "" and summary["平仓盈亏"] == "", summary
                assert summary["当日盈亏状态"] == pending_label, summary
            else:
                assert abs(float(summary["当日盈亏"]) - asset["account_day_pnl"]) < .000001, summary
                assert summary["当日盈亏状态"] == "已发布", summary
            assert not errors and not writes, (errors, writes)
            if args.browser_date and args.browser_date != args.trade_date:
                assert asset_requests and all("trade_date=" in url for url in asset_requests), asset_requests
            page.screenshot(path=str(output / f"asset-{width}.png"), full_page=True)
            results.append({"width": width, "net_asset": asset["net_asset"],
                            "reverse_repo_receivable": asset["reverse_repo_receivable"],
                            "pnl_status": status, "display_pnl": page.locator("#dayProfit").inner_text(),
                            "csv_pnl": summary["当日盈亏"], "errors": errors, "write_requests": writes})
            page.close()
        browser.close()
    print(json.dumps({"account_id": args.account_id, "trade_date": args.trade_date, "results": results}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
