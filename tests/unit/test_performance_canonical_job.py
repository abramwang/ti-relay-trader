from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys
from datetime import datetime
from types import SimpleNamespace
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "src"))
sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))

from relay.jobs.common import JobOptions, TradingDayInfo  # noqa: E402
from relay.jobs.performance_canonical import (  # noqa: E402
    CANONICAL_PRICE_SOURCE,
    LEVEL1_FALLBACK_FLAG,
    evaluate_canonical_watermark,
    run_canonical_performance,
)


def trading_day() -> TradingDayInfo:
    return TradingDayInfo(
        requested_date="20260826",
        target_trade_date="20260826",
        is_trading_day=True,
        source="test",
        raw={},
    )


def watermark(*, target: int = 20260826, status: str = "ready") -> dict[str, object]:
    return {
        "meta": {"schema_version": "postclose_reference_watermark.v1"},
        "data": {
            "target_trade_date": target,
            "status": status,
            "generated_at": "2026-08-26T16:35:00+08:00",
            "pipeline": {"status": "success", "target_trade_date": target},
            "datasets": [
                {"name": name, "status": status, "published_watermark": target}
                for name in (
                    "daily_bar_stock_none",
                    "daily_bar_etf_none",
                    "daily_bar_index_none",
                )
            ],
        },
    }


def nav(
    account_id: str,
    *,
    version: int,
    close: float,
    price_source: str,
    fallback: bool,
) -> dict[str, object]:
    return {
        "performance_nav_pk": version,
        "account_id": account_id,
        "trade_date": "2026-08-26",
        "version": version,
        "status": "provisional",
        "formula_version": "performance_economic_nav.test",
        "close_economic_nav": close,
        "account_day_pnl": close - 900,
        "daily_return": (close - 900) / 900,
        "source": "relay.economic_nav.rebuild",
        "quality_flags": [LEVEL1_FALLBACK_FLAG] if fallback else [],
        "pnl_components": {"market_valuation": {"price_source": price_source}},
    }


class FakeClient:
    def __init__(self) -> None:
        self.accounts = [
            SimpleNamespace(account_id="acct-ready", enabled=True),
            SimpleNamespace(account_id="acct-empty", enabled=True),
        ]
        self.navs = {
            "acct-ready": [
                nav(
                    "acct-ready",
                    version=1,
                    close=1000,
                    price_source="meridian_level1_pre_close_and_last",
                    fallback=True,
                )
            ],
            "acct-empty": [],
        }
        self.job_runs: list[dict[str, object]] = []
        self.recorded_jobs: list[dict[str, object]] = []

    def list_job_runs(self, **_kwargs):
        return self.job_runs

    def list_accounts(self):
        return self.accounts

    def list_economic_nav(self, *, account_id: str, **_kwargs):
        return list(self.navs[account_id])

    def record_job_run(self, report, **kwargs):
        self.recorded_jobs.append({"report": report, **kwargs})
        return {"run_id": "performance_daily-test"}


class CanonicalPerformanceJobTest(unittest.TestCase):
    def test_watermark_requires_schema_target_and_all_daily_datasets(self) -> None:
        ready = evaluate_canonical_watermark(watermark(), "20260826")
        self.assertTrue(ready["ready"])

        delayed = evaluate_canonical_watermark(watermark(target=20260825), "20260826")
        self.assertFalse(delayed["ready"])
        self.assertIn("target_trade_date_not_reached", delayed["reasons"])
        self.assertIn("daily_bar_stock_none_not_ready", delayed["reasons"])

    def test_waits_without_rebuilding_before_watermark_is_ready(self) -> None:
        client = FakeClient()
        command_calls: list[object] = []

        report = run_canonical_performance(
            JobOptions(job_name="performance_canonical", target_date="20260826"),
            client=client,
            trading_day=trading_day(),
            watermark_loader=lambda _base, _timeout: watermark(target=20260825),
            relayctl_builder=lambda: Path("/tmp/relayctl"),
            command_runner=lambda *args, **kwargs: command_calls.append((args, kwargs)),  # type: ignore[arg-type,return-value]
        )

        self.assertTrue(report["ok"])
        self.assertTrue(report["skipped"])
        self.assertTrue(report["waiting_for_meridian"])
        self.assertFalse(report["canonical_completed"])
        self.assertEqual(command_calls, [])

    def test_cron_poll_reuses_daily_run_id_before_deadline(self) -> None:
        report = run_canonical_performance(
            JobOptions(
                job_name="performance_canonical",
                target_date="20260826",
                trigger="meridian_watermark_poll",
                watermark_retry_until="18:50",
            ),
            client=FakeClient(),
            trading_day=trading_day(),
            watermark_loader=lambda _base, _timeout: watermark(target=20260825),
            current_time=datetime.fromisoformat("2026-08-26T17:10:00+08:00"),
        )

        self.assertTrue(report["ok"])
        self.assertTrue(report["waiting_for_meridian"])
        self.assertEqual(report["run_id"], "performance_canonical-20260826-watermark-poll")
        self.assertFalse(report["watermark_poll"]["deadline_exceeded"])

    def test_cron_poll_becomes_blocked_at_retry_deadline(self) -> None:
        report = run_canonical_performance(
            JobOptions(
                job_name="performance_canonical",
                target_date="20260826",
                trigger="meridian_watermark_poll",
                watermark_retry_until="18:50",
            ),
            client=FakeClient(),
            trading_day=trading_day(),
            watermark_loader=lambda _base, _timeout: watermark(target=20260825),
            current_time=datetime.fromisoformat("2026-08-26T18:50:00+08:00"),
        )

        self.assertFalse(report["ok"])
        self.assertTrue(report["blocked_by_meridian"])
        self.assertFalse(report.get("waiting_for_meridian", False))
        self.assertFalse(report["skipped"])
        self.assertTrue(report["watermark_poll"]["deadline_exceeded"])
        self.assertIn("before 18:50", report["errors"][0])

    def test_rebuilds_and_compares_level1_nav_after_watermark_is_ready(self) -> None:
        client = FakeClient()

        def run_command(command, **_kwargs):
            self.assertIn("acct-ready,acct-empty", command)
            client.navs["acct-ready"] = [
                nav(
                    "acct-ready",
                    version=2,
                    close=1001,
                    price_source=CANONICAL_PRICE_SOURCE,
                    fallback=False,
                )
            ]
            return subprocess.CompletedProcess(
                command,
                0,
                stdout=json.dumps(
                    {
                        "date_from": "2026-08-26",
                        "date_to": "2026-08-26",
                        "persist": True,
                        "items": [
                            {
                                "account_id": "acct-ready",
                                "trade_date": "2026-08-26",
                                "cost_ledger": {"status": "calculated"},
                                "economic_nav": {
                                    "status": "provisional",
                                    "persisted": True,
                                    "valuation": {"price_source": CANONICAL_PRICE_SOURCE},
                                    "nav": {"performance_nav_pk": 2, "version": 2},
                                },
                            },
                            {
                                "account_id": "acct-empty",
                                "trade_date": "2026-08-26",
                                "cost_ledger": {"status": "calculated"},
                                "economic_nav": {"status": "blocked", "persisted": False},
                            },
                        ],
                    }
                ),
                stderr="",
            )

        def quality_runner(*_args, **_kwargs):
            return {
                "ok": True,
                "finished_at": "2026-08-26T16:36:00+08:00",
                "performance_summary": {
                    "accounts": 2,
                    "ready": 1,
                    "attention": 0,
                    "blocked": 0,
                    "not_applicable": 1,
                    "published": 1,
                    "preview_only": 0,
                },
                "performance_ready_accounts": ["acct-ready"],
                "performance_attention_accounts": [],
                "performance_blocked_accounts": [],
                "performance_not_applicable_accounts": ["acct-empty"],
                "performance_published_accounts": ["acct-ready"],
                "performance_preview_only_accounts": [],
                "warnings": [],
                "errors": [],
            }

        report = run_canonical_performance(
            JobOptions(
                job_name="performance_canonical",
                target_date="20260826",
                persist=True,
            ),
            client=client,
            trading_day=trading_day(),
            watermark_loader=lambda _base, _timeout: watermark(),
            relayctl_builder=lambda: Path("/tmp/relayctl"),
            command_runner=run_command,
            quality_runner=quality_runner,
        )

        self.assertTrue(report["ok"])
        self.assertTrue(report["canonical_completed"])
        self.assertEqual(report["performance_blocked_accounts"], [])
        self.assertEqual(report["comparison_summary"]["compared_accounts"], 1)
        self.assertEqual(report["comparison_summary"]["max_abs_close_economic_nav_delta"], 1)
        comparison = report["nav_version_comparisons"][0]
        self.assertEqual(comparison["previous"]["price_source"], "meridian_level1_pre_close_and_last")
        self.assertEqual(comparison["canonical"]["price_source"], CANONICAL_PRICE_SOURCE)
        self.assertEqual(client.recorded_jobs[0]["job_name"], "performance_daily")
        self.assertEqual(client.recorded_jobs[0]["trigger"], "meridian_canonical_ready")

    def test_skips_rebuild_when_completed_job_is_already_persisted(self) -> None:
        client = FakeClient()
        client.job_runs = [
            {
                "run_id": "performance_canonical-existing",
                "target_trade_date": "2026-08-26",
                "status": "succeeded",
                "report": {"canonical_completed": True},
            }
        ]

        report = run_canonical_performance(
            JobOptions(job_name="performance_canonical", target_date="20260826"),
            client=client,
            trading_day=trading_day(),
            watermark_loader=lambda _base, _timeout: watermark(),
        )

        self.assertTrue(report["canonical_completed"])
        self.assertTrue(report["skipped"])
        self.assertEqual(report["completed_run_id"], "performance_canonical-existing")
        self.assertNotIn("result", report["previous_run_query"])


if __name__ == "__main__":
    unittest.main()
