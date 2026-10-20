#!/usr/bin/env python3
"""GET-only chart validation; opposing flows are injected only in this browser."""

import argparse
import copy
import csv
import io
import json
from pathlib import Path

from playwright.sync_api import sync_playwright


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--account-id", default="314000045768")
    parser.add_argument("--date-from", default="20250401")
    parser.add_argument("--date-to", default="20261019")
    parser.add_argument("--width", type=int, default=1600)
    parser.add_argument("--check-capital-reference-date", default="")
    parser.add_argument("--output-dir", default="outputs/recovery/performance_cash_markers_20261020")
    args = parser.parse_args()
    output = Path(args.output_dir)
    output.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page(viewport={"width": args.width, "height": 1280})
        errors, writes = [], []
        page.on("pageerror", lambda error: errors.append(str(error)))

        def readonly(route):
            if route.request.method not in ("GET", "HEAD", "OPTIONS"):
                writes.append(route.request.method + " " + route.request.url)
                route.abort()
            else:
                route.continue_()

        page.route("**/v1/**", readonly)
        url = args.base_url + f"/v1/accounts/{args.account_id}/performance/series?date_from={args.date_from}&date_to={args.date_to}"
        response = page.request.get(url)
        assert response.ok, response.status
        payload = response.json()
        rows = payload["data"]["series"]
        assert len(rows) >= 2

        def ready():
            page.wait_for_function("""() => {
                const status = document.querySelector('#performanceStatus')?.textContent || '';
                return status.includes('已加载') && !status.includes('明细计算中') &&
                    !document.querySelector('#loadPerformanceButton')?.disabled;
            }""", timeout=60000)

        page.goto(args.base_url + "/trade#performance", wait_until="domcontentloaded")
        ready()
        page.locator(f'#accountTabs button[title*="{args.account_id}"]').click()
        ready()
        page.locator("#perfDateFrom").fill(args.date_from)
        page.locator("#perfDateTo").fill(args.date_to)
        page.locator("#loadPerformanceButton").click()
        ready()
        actual = page.evaluate("""() => {
            const chart = echarts.getInstanceByDom(document.querySelector('#performanceChart'));
            const option = chart.getOption();
            return option.series.filter(s => s.type === 'scatter').map(s => ({
                name: s.name, points: s.data.map(d => ({index: d.row_index, amount: d.amount}))
            }));
        }""")
        assert len(actual) == 3, actual
        for series, kind, sign in zip(actual, ["external", "external", "strategy_capital"], [1, -1, 0]):
            expected = []
            for index, row in enumerate(rows):
                amounts = [e["amount"] for e in row.get("cash_flow_events", [])
                           if e["kind"] == kind and (not sign or e["amount"] * sign > 0)]
                if amounts and row.get("performance_status") != "blocked":
                    expected.append({"index": index, "amount": sum(amounts)})
            assert series["points"] == expected, (series, expected)
        assert any(series["points"] for series in actual), "sample has no cash events"
        layout = page.evaluate("""() => {
            const toolbar = document.querySelector('.performance-chart-toolbar').getBoundingClientRect();
            const chart = document.querySelector('#performanceChart').getBoundingClientRect();
            const legend = Array.from(document.querySelectorAll('.performance-chart-legend span'));
            return {overflow: document.documentElement.scrollWidth > innerWidth,
                legendFits: legend.every(el => { const r = el.getBoundingClientRect();
                    return r.top >= toolbar.top && r.bottom <= toolbar.bottom + 1 && r.right <= toolbar.right + 1; }),
                chartBelowToolbar: chart.top >= toolbar.bottom};
        }""")
        assert not layout["overflow"] and layout["legendFits"] and layout["chartBelowToolbar"], layout
        page.wait_for_timeout(300)
        page.screenshot(path=str(output / f"cash-markers-live-{args.width}.png"))

        if args.check_capital_reference_date:
            date = args.check_capital_reference_date
            selected = next(row for row in rows if row["trade_date"] == date)
            ref = selected["capital_reference"]
            assert abs(ref["daily_return"] - selected["daily_pnl"] / ref["capital"]) < 1e-12
            single_url = args.base_url + f"/v1/accounts/{args.account_id}/performance/series?trade_date={date}"
            single = page.request.get(single_url).json()["data"]["series"][0]
            assert single["capital_reference"] == ref, "reference depends on selected date range"
            csv_url = args.base_url + f"/v1/accounts/{args.account_id}/performance/series.csv?trade_date={date}"
            exported = list(csv.DictReader(io.StringIO(page.request.get(csv_url).text())))
            assert len(exported) == 1 and exported[0]["reference_position_value_date"] == ref["position_value_date"]
            assert abs(float(exported[0]["reference_daily_return"]) - ref["daily_return"]) < 1e-6
            page.locator('[data-performance-table-view="cash-impact"]').click()
            row = page.locator(f'[data-cash-impact-date="{date}"]')
            row.wait_for(state="visible")
            text = row.inner_text()
            assert f'{ref["daily_return"] * 100:.2f}%' in text, text
            assert f'{selected["return_rate"] * 100:.2f}%' in text, text
            assert "固定持仓试算" in text and ref["position_value_date"] in text, text
            tooltip = page.evaluate("""(date) => {
                const option = echarts.getInstanceByDom(document.querySelector('#performanceChart')).getOption();
                return option.tooltip[0].formatter([{axisValue: date, dataIndex: 999}]);
            }""", date)
            assert "资本参考 · 非正式净值" in tooltip and f'{ref["daily_return"] * 100:.2f}%' in tooltip, tooltip
            row.scroll_into_view_if_needed()
            page.screenshot(path=str(output / f"capital-impact-{args.width}.png"))

        # Only the browser's GET response is replaced, never the server or ledger.
        fixture = copy.deepcopy(payload)
        first, second = fixture["data"]["series"][:2]
        first["external_net_flow"] = 0
        first["cash_flow_events"] = [
            {"kind": "external", "amount": 100, "time_precision": "second", "granularity": "transaction", "effective_at": first["trade_date"] + "T01:55:53Z"},
            {"kind": "external", "amount": -100, "time_precision": "second", "granularity": "transaction", "effective_at": first["trade_date"] + "T06:32:10Z"},
            {"kind": "strategy_capital", "amount": 20, "time_precision": "date", "granularity": "daily_net"},
        ]
        second["external_net_flow"] = 50
        second["cash_flow_events"] = [{"kind": "external", "amount": 50, "time_precision": "date", "granularity": "daily_net"}]
        page.route("**/performance/series?*", lambda route: route.fulfill(json=fixture))
        page.locator("#loadPerformanceButton").click()
        ready()
        fixture_result = page.evaluate("""() => {
            const chart = echarts.getInstanceByDom(document.querySelector('#performanceChart'));
            const option = chart.getOption();
            const markers = option.series.filter(s => s.type === 'scatter');
            if (!markers.every(s => s.data.some(d => d.row_index === 0))) throw Error('opposing flows or strategy capital lost');
            const formatter = option.tooltip[0].formatter;
            const first = formatter([{axisValue: option.xAxis[0].data[0], dataIndex: 999}]);
            const second = formatter([{axisValue: option.xAxis[0].data[1], dataIndex: 999}]);
            if (!first.includes('09:55:53') || !first.includes('14:32:10') || !first.includes('策略资本转入')) throw Error(first);
            if (!second.includes('时刻未记录') || !second.includes('日净额') || second.includes('09:55:53')) throw Error(second);
            chart.dispatchAction({type: 'dataZoom', start: 0, end: 5});
            chart.dispatchAction({type: 'showTip', seriesIndex: 5, dataIndex: 0});
            const canvas = document.querySelector('#performanceChart canvas');
            const pixels = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data;
            let painted = 0;
            for (let i = 3; i < pixels.length; i += 16) if (pixels[i]) painted++;
            return {first, second, painted, markerSeries: markers.length};
        }""")
        assert fixture_result["painted"] > 500
        page.wait_for_timeout(400)
        page.screenshot(path=str(output / f"cash-markers-fixture-{args.width}.png"))
        assert not errors and not writes, (errors, writes)
        browser.close()
    print(json.dumps({"rows": len(rows), "actual_markers": actual, "layout": layout, "fixture": fixture_result, "writes": writes}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
