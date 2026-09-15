#!/usr/bin/env python3
"""Read-only visual smoke test for the portal overview layout."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from playwright.sync_api import sync_playwright


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--width", type=int, default=1600)
    parser.add_argument("--height", type=int, default=900)
    parser.add_argument("--output", default="/tmp/relay-home-smoke.png")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    console_errors: list[str] = []
    page_errors: list[str] = []
    response_errors: list[str] = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(
            viewport={"width": args.width, "height": args.height},
            device_scale_factor=1,
        )
        page.on(
            "console",
            lambda message: console_errors.append(message.text)
            if message.type == "error"
            else None,
        )
        page.on("pageerror", lambda error: page_errors.append(str(error)))
        page.on(
            "response",
            lambda response: response_errors.append(f"{response.status} {response.url}")
            if response.status >= 400
            else None,
        )

        page.goto(args.base_url.rstrip("/") + "/", wait_until="networkidle", timeout=30_000)
        diagnostics = page.evaluate(
            """() => {
                const main = document.querySelector('.page-main');
                const dashboard = document.querySelector('.overview-dashboard');
                const primary = document.querySelector('.overview-primary');
                const entryGrid = document.querySelector('.entry-grid');
                const routePanel = document.querySelector('.route-panel');
                const entries = Array.from(document.querySelectorAll('.entry'));
                const routeRows = Array.from(document.querySelectorAll('.route-panel tbody tr'));
                const entryRect = entryGrid?.getBoundingClientRect();
                const routeRect = routePanel?.getBoundingClientRect();
                const lastEntryBottom = Math.max(
                    ...entries.map((entry) => entry.getBoundingClientRect().bottom)
                );
                const lastRouteBottom = routeRows.length
                    ? routeRows[routeRows.length - 1].getBoundingClientRect().bottom
                    : 0;
                const mainStyle = main ? getComputedStyle(main) : null;
                return {
                    entryCount: entries.length,
                    routeRowCount: routeRows.length,
                    entryGridClipped: entryGrid
                        ? entryGrid.scrollHeight > entryGrid.clientHeight + 1
                        : true,
                    entriesOverlapRoute: routeRect
                        ? lastEntryBottom > routeRect.top + 1
                        : true,
                    routeRowsClipped: routeRect
                        ? lastRouteBottom > routeRect.bottom + 1
                        : true,
                    primaryClipped: primary
                        ? primary.scrollHeight > primary.clientHeight + 1
                        : true,
                    dashboardClipped: dashboard
                        ? dashboard.scrollHeight > dashboard.clientHeight + 1
                        : true,
                    pageMainCanScroll: main
                        ? main.scrollHeight <= main.clientHeight + 1 ||
                            ['auto', 'scroll'].includes(mainStyle?.overflowY)
                        : false,
                    documentOverflow:
                        document.documentElement.scrollWidth > window.innerWidth,
                    entryGridHeight: entryRect?.height || 0,
                    routePanelHeight: routeRect?.height || 0,
                };
            }"""
        )
        page.screenshot(path=str(output), full_page=True)
        browser.close()

    if diagnostics["entryCount"] < 5 or diagnostics["routeRowCount"] < 1:
        raise AssertionError(f"overview content is incomplete: {diagnostics}")
    for key in (
        "entryGridClipped",
        "entriesOverlapRoute",
        "routeRowsClipped",
        "primaryClipped",
        "dashboardClipped",
        "documentOverflow",
    ):
        if diagnostics[key]:
            raise AssertionError(f"overview layout failed {key}: {diagnostics}")
    if not diagnostics["pageMainCanScroll"]:
        raise AssertionError(f"overview workspace cannot scroll: {diagnostics}")
    if console_errors or page_errors or response_errors:
        raise AssertionError(
            json.dumps(
                {
                    "console_errors": console_errors,
                    "page_errors": page_errors,
                    "response_errors": response_errors,
                    "diagnostics": diagnostics,
                },
                ensure_ascii=False,
                indent=2,
            )
        )

    print(
        json.dumps(
            {"screenshot": str(output), "diagnostics": diagnostics},
            ensure_ascii=False,
            indent=2,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
