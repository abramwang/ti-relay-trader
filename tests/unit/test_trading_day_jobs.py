from __future__ import annotations

import sys
import unittest
from datetime import datetime, timedelta
from pathlib import Path
from types import SimpleNamespace


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "src"))
sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))

from relay.jobs.common import (  # noqa: E402
    BUSINESS_TZ,
    JobOptions,
    TradingDayInfo,
    run_daily_performance,
    run_post_close_settlement,
    run_pre_open_init,
    refreshed_query_terminal_status,
    settlement_snapshot_client,
)
from relay_sdk import RelayClient  # noqa: E402


class FakeReceipt:
    def __init__(self, account_id: str, action: str) -> None:
        self.message_id = f"msg-{account_id}-{action}"
        self.raw = {
            "account_id": account_id,
            "action": action,
            "message_id": self.message_id,
            "stream_id": f"{action}-1",
        }


class FakeClient:
    def __init__(self) -> None:
        self.refresh_calls: list[tuple[str, str]] = []
        self.settlement_calls: list[dict[str, object]] = []
        self.status_value: dict[str, object] = {"status": "ok", "timezone": "Asia/Shanghai"}
        self.status_values: list[dict[str, object]] = []
        self.status_calls = 0
        self.accounts = [
            SimpleNamespace(account_id="acct-1", enabled=True),
            SimpleNamespace(account_id="acct-disabled", enabled=False),
        ]
        self.asset_errors: set[str] = set()
        stale_at = datetime.now(BUSINESS_TZ) - timedelta(days=1)
        self.asset_updated_at: dict[str, datetime] = {}
        self.position_updated_at: dict[str, datetime] = {}
        self.default_asset_updated_at = stale_at
        self.default_position_updated_at = stale_at
        self.lagging_positions: set[str] = set()
        self.cost_status: dict[str, str] = {}
        self.nav_status: dict[str, str] = {}
        self.performance_flags: dict[str, list[str]] = {}
        self.empty_accounts: set[str] = set()
        self.query_statuses: dict[str, dict[str, object]] = {}
        self.query_actions: dict[str, str] = {}

    def status(self):
        self.status_calls += 1
        if self.status_values:
            return self.status_values.pop(0)
        return self.status_value

    def list_accounts(self):
        return self.accounts

    def refresh_orders(self, account_id: str):
        return self._refresh(account_id, "order.list.query")

    def refresh_fills(self, account_id: str):
        return self._refresh(account_id, "fill.list.query")

    def refresh_fees(self, account_id: str):
        return self._refresh(account_id, "fee.list.query")

    def refresh_asset(self, account_id: str):
        return self._refresh(account_id, "account.asset.query")

    def refresh_positions(self, account_id: str):
        return self._refresh(account_id, "account.positions.query")

    def get_query_status(self, origin_message_id: str):
        status = self.query_statuses.get(origin_message_id)
        if status is not None:
            return dict(status)
        action = self.query_actions.get(origin_message_id, "")
        expected = {
            "order.list.query": "order_page",
            "fill.list.query": "fill_page",
            "fee.list.query": "fee_page",
            "account.asset.query": "asset_page",
            "account.positions.query": "position_page",
        }.get(action, "")
        return {
            "origin_message_id": origin_message_id,
            "action": action,
            "expected_result_type": expected,
            "state": "completed",
            "terminal": True,
            "success": True,
            "contradictory": False,
            "reply_count": 1,
            "terminal_count": 1,
            "replies": [{"status": "completed", "result_type": expected, "is_last": True}],
        }

    def get_asset(self, account_id: str):
        if account_id in self.asset_errors:
            raise RuntimeError("asset snapshot not found")
        if account_id in self.empty_accounts:
            return SimpleNamespace(
                account_id=account_id,
                net_asset=0.0,
                cash_available=0.0,
                market_value=0.0,
                updated_at=self.asset_updated_at.get(account_id, self.default_asset_updated_at),
            )
        return SimpleNamespace(
            account_id=account_id,
            net_asset=1000.0,
            cash_available=500.0,
            market_value=500.0,
            updated_at=self.asset_updated_at.get(account_id, self.default_asset_updated_at),
        )

    def get_positions(self, account_id: str):
        if account_id in self.empty_accounts:
            return []
        return [
            SimpleNamespace(
                account_id=account_id,
                symbol="600000",
                quantity=100,
                updated_at=self.position_updated_at.get(account_id, self.default_position_updated_at),
            )
        ]

    def list_orders(self, *, account_id: str, limit: int, trade_date: str | None = None, history: bool | None = None):
        if account_id in self.empty_accounts:
            return []
        return [
            SimpleNamespace(account_id=account_id, gateway_order_id="gw-working", is_terminal=False),
            SimpleNamespace(account_id=account_id, gateway_order_id="gw-filled", is_terminal=True),
        ]

    def list_fills(self, *, account_id: str, limit: int, trade_date: str | None = None, history: bool | None = None):
        if account_id in self.empty_accounts:
            return []
        return [SimpleNamespace(account_id=account_id, fill_id="fill-1")]

    def list_order_fees(self, account_id: str, *, limit: int, trade_date: str | None = None):
        if account_id in self.empty_accounts:
            return []
        return [
            SimpleNamespace(
                account_id=account_id,
                fee_record_id="fee-1",
                fee_complete=True,
                association_complete=True,
            )
        ]

    def record_settlement_snapshot(self, **kwargs):
        self.settlement_calls.append(dict(kwargs))
        account_ids = list(kwargs.get("account_ids") or [])
        account_reports = []
        warnings = []
        for account_id in account_ids:
            errors = []
            if account_id in self.asset_errors:
                errors.append("asset: asset snapshot not found")
                warnings.append(f"{account_id}: asset: asset snapshot not found")
            account_reports.append({"account_id": account_id, "errors": errors})
        return {
            "run_id": kwargs.get("run_id"),
            "trade_date": kwargs.get("trade_date"),
            "status": "completed",
            "asset_snapshots": len([account_id for account_id in account_ids if account_id not in self.asset_errors]),
            "position_snapshots": 1,
            "account_error_count": len(self.asset_errors.intersection(account_ids)),
            "accounts": account_reports,
            "warnings": warnings,
        }

    def preview_cost_ledger(self, *, account_id: str, trade_date: str):
        return {
            "account_id": account_id,
            "trade_date": trade_date,
            "status": self.cost_status.get(account_id, "calculated"),
            "persisted": False,
            "quality_flags": self.performance_flags.get(account_id, []),
        }

    def preview_economic_nav(self, *, account_id: str, trade_date: str):
        return {
            "account_id": account_id,
            "trade_date": trade_date,
            "status": self.nav_status.get(account_id, "provisional"),
            "persisted": False,
            "quality_flags": self.performance_flags.get(account_id, []),
        }

    def _refresh(self, account_id: str, action: str) -> FakeReceipt:
        self.refresh_calls.append((account_id, action))
        if action == "account.asset.query":
            self.asset_updated_at[account_id] = datetime.now(BUSINESS_TZ)
        if action == "account.positions.query" and account_id not in self.lagging_positions:
            self.position_updated_at[account_id] = datetime.now(BUSINESS_TZ)
        receipt = FakeReceipt(account_id, action)
        self.query_actions[receipt.message_id] = action
        return receipt


class BatchGateClient(FakeClient):
    def __init__(self) -> None:
        super().__init__()
        self.accounts = [
            SimpleNamespace(account_id="acct-1", enabled=True),
            SimpleNamespace(account_id="acct-2", enabled=True),
            SimpleNamespace(account_id="acct-3", enabled=True),
        ]

    def _refresh(self, account_id: str, action: str) -> FakeReceipt:
        self.refresh_calls.append((account_id, action))
        receipt = FakeReceipt(account_id, action)
        self.query_actions[receipt.message_id] = action
        return receipt

    def _release_refreshes(self) -> None:
        position_queries = {
            account_id
            for account_id, action in self.refresh_calls
            if action == "account.positions.query"
        }
        if len(position_queries) != len(self.accounts):
            return
        refreshed_at = datetime.now(BUSINESS_TZ)
        for account in self.accounts:
            self.asset_updated_at[account.account_id] = refreshed_at
            self.position_updated_at[account.account_id] = refreshed_at

    def get_asset(self, account_id: str):
        self._release_refreshes()
        return super().get_asset(account_id)

    def get_positions(self, account_id: str):
        self._release_refreshes()
        return super().get_positions(account_id)


def trading_day(is_trading_day: bool = True) -> TradingDayInfo:
    return TradingDayInfo(
        requested_date="20260615",
        target_trade_date="20260615" if is_trading_day else "20260612",
        is_trading_day=is_trading_day,
        source="test",
        raw={},
    )


class TradingDayJobTest(unittest.TestCase):
    def test_settlement_snapshot_uses_dedicated_longer_timeout(self) -> None:
        client = RelayClient(
            "http://relay.example.test",
            account_id="acct-1",
            timeout=10,
            api_key="test-key",
            trust_env=False,
        )

        snapshot_client = settlement_snapshot_client(
            client,
            JobOptions(job_name="post_close_settlement"),
        )

        self.assertIsNot(snapshot_client, client)
        self.assertEqual(snapshot_client.timeout, 30)
        self.assertEqual(snapshot_client.base_url, client.base_url)
        self.assertEqual(snapshot_client.account_id, client.account_id)
        self.assertEqual(snapshot_client.api_key, client.api_key)

    def test_daily_performance_isolates_account_quality_results(self) -> None:
        client = FakeClient()
        client.accounts = [
            SimpleNamespace(account_id="acct-ready", enabled=True),
            SimpleNamespace(account_id="acct-attention", enabled=True),
            SimpleNamespace(account_id="acct-blocked", enabled=True),
        ]
        client.performance_flags["acct-attention"] = ["net_performance_fee_incomplete"]
        client.cost_status["acct-blocked"] = "blocked"
        client.performance_flags["acct-blocked"] = ["position_quantity_not_reconciled"]

        report = run_daily_performance(
            JobOptions(job_name="performance_daily"),
            client=client,
            trading_day=trading_day(),
        )

        self.assertTrue(report["ok"])
        self.assertEqual(
            report["performance_summary"],
            {"accounts": 3, "ready": 1, "attention": 1, "blocked": 1, "not_applicable": 0},
        )
        self.assertEqual(report["performance_ready_accounts"], ["acct-ready"])
        self.assertEqual(report["performance_attention_accounts"], ["acct-attention"])
        self.assertEqual(report["performance_blocked_accounts"], ["acct-blocked"])
        self.assertEqual(report["accounts"][0]["performance"]["status"], "ready")
        self.assertEqual(report["accounts"][1]["performance"]["status"], "attention")
        self.assertFalse(report["accounts"][1]["performance"]["fee_complete"])
        self.assertEqual(report["accounts"][2]["performance"]["status"], "blocked")

    def test_daily_performance_marks_empty_clean_start_not_applicable(self) -> None:
        client = FakeClient()
        client.accounts = [SimpleNamespace(account_id="acct-empty", enabled=True)]
        client.empty_accounts.add("acct-empty")
        client.nav_status["acct-empty"] = "blocked"
        client.performance_flags["acct-empty"] = [
            "empty_clean_start_continuation",
            "missing_positive_economic_nav",
        ]

        report = run_daily_performance(
            JobOptions(job_name="performance_daily"),
            client=client,
            trading_day=trading_day(),
        )

        self.assertEqual(
            report["performance_summary"],
            {"accounts": 1, "ready": 0, "attention": 0, "blocked": 0, "not_applicable": 1},
        )
        self.assertEqual(report["performance_not_applicable_accounts"], ["acct-empty"])
        self.assertEqual(report["performance_blocked_accounts"], [])
        self.assertEqual(report["accounts"][0]["performance"]["status"], "not_applicable")
        self.assertEqual(
            report["accounts"][0]["performance"]["reason"],
            "empty_account_without_performance_baseline",
        )
        self.assertNotIn(
            "daily performance has account-level attention or blocked results",
            report.get("warnings", []),
        )

    def test_daily_performance_does_not_skip_empty_marker_with_trading_activity(self) -> None:
        client = FakeClient()
        client.accounts = [SimpleNamespace(account_id="acct-active", enabled=True)]
        client.nav_status["acct-active"] = "blocked"
        client.performance_flags["acct-active"] = [
            "empty_clean_start_continuation",
            "missing_positive_economic_nav",
        ]

        report = run_daily_performance(
            JobOptions(job_name="performance_daily"),
            client=client,
            trading_day=trading_day(),
        )

        self.assertEqual(report["performance_blocked_accounts"], ["acct-active"])
        self.assertEqual(report["performance_not_applicable_accounts"], [])
        self.assertEqual(report["accounts"][0]["performance"]["status"], "blocked")

    def test_stream_runtime_attention_does_not_block_daily_job(self) -> None:
        client = FakeClient()
        client.status_value = {
            "status": "degraded",
            "dependencies": {
                "database": {"status": "ok"},
                "redis": {"status": "ok"},
                "order_service": {"status": "ok"},
                "market": {"status": "ok"},
                "event_stream": {"status": "ok"},
                "stream_runtime": {"status": "attention"},
            },
        }

        report = run_pre_open_init(
            JobOptions(job_name="pre_open_init", refresh_wait_seconds=0),
            client=client,
            trading_day=trading_day(),
        )

        self.assertTrue(report["ok"])
        self.assertEqual(len(client.refresh_calls), 4)
        self.assertIn("all daily-job dependencies are healthy", report["warnings"][0])

    def test_degraded_required_dependency_blocks_daily_job_with_trade_date(self) -> None:
        client = FakeClient()
        client.status_value = {
            "status": "degraded",
            "dependencies": {
                "database": {"status": "error"},
                "redis": {"status": "ok"},
                "order_service": {"status": "ok"},
                "market": {"status": "ok"},
                "event_stream": {"status": "ok"},
            },
        }

        report = run_pre_open_init(
            JobOptions(job_name="pre_open_init", refresh_wait_seconds=0),
            client=client,
            trading_day=trading_day(),
        )

        self.assertFalse(report["ok"])
        self.assertEqual(report["trading_day"]["target_trade_date"], "20260615")
        self.assertIn("database", report["errors"][0])
        self.assertEqual(client.refresh_calls, [])

    def test_pre_open_waits_for_transient_required_dependency(self) -> None:
        client = FakeClient()
        client.status_values = [
            {
                "status": "degraded",
                "dependencies": {
                    "database": {"status": "ok"},
                    "redis": {"status": "timeout"},
                    "order_service": {"status": "ok"},
                    "market": {"status": "ok"},
                    "event_stream": {"status": "ok"},
                },
            },
            {"status": "ok", "timezone": "Asia/Shanghai"},
        ]

        report = run_pre_open_init(
            JobOptions(
                job_name="pre_open_init",
                refresh_wait_seconds=0,
                dependency_ready_timeout_seconds=0.05,
                dependency_retry_seconds=0.01,
            ),
            client=client,
            trading_day=trading_day(),
        )

        self.assertTrue(report["ok"])
        self.assertEqual(client.status_calls, 2)
        self.assertTrue(report["dependency_wait"]["recovered"])
        self.assertEqual(report["dependency_wait"]["attempts"], 2)
        self.assertIn("recovered after 2 checks", report["warnings"][-1])

    def test_pre_open_refreshes_enabled_accounts(self) -> None:
        client = FakeClient()
        report = run_pre_open_init(
            JobOptions(job_name="pre_open_init", refresh_wait_seconds=0),
            client=client,
            trading_day=trading_day(),
        )

        self.assertTrue(report["ok"])
        self.assertFalse(report["skipped"])
        self.assertEqual(
            client.refresh_calls,
            [
                ("acct-1", "order.list.query"),
                ("acct-1", "fill.list.query"),
                ("acct-1", "account.asset.query"),
                ("acct-1", "account.positions.query"),
            ],
        )
        self.assertEqual(report["accounts"][0]["snapshot"]["positions_count"], 1)
        self.assertEqual(report["accounts"][0]["snapshot"]["non_terminal_orders"], 1)
        self.assertEqual(len(client.settlement_calls), 1)
        self.assertEqual(client.settlement_calls[0]["snapshot_type"], "open")
        self.assertEqual(client.settlement_calls[0]["source"], "pre_open_init")
        self.assertEqual(client.settlement_calls[0]["trade_date"], "20260615")
        self.assertEqual(report["open_snapshot"]["result"]["status"], "completed")

    def test_daily_job_dispatches_all_accounts_before_shared_refresh_wait(self) -> None:
        client = BatchGateClient()
        report = run_pre_open_init(
            JobOptions(
                job_name="pre_open_init",
                refresh_wait_seconds=0,
                refresh_timeout_seconds=0.05,
                refresh_poll_seconds=0.01,
            ),
            client=client,
            trading_day=trading_day(),
        )

        self.assertTrue(report["ok"])
        self.assertEqual(report.get("snapshot_blocked_accounts"), None)
        self.assertEqual(
            [account_id for account_id, action in client.refresh_calls if action == "account.positions.query"],
            ["acct-1", "acct-2", "acct-3"],
        )
        self.assertEqual(client.settlement_calls[0]["account_ids"], ["acct-1", "acct-2", "acct-3"])

    def test_non_trading_day_skips_without_error(self) -> None:
        client = FakeClient()
        report = run_pre_open_init(
            JobOptions(job_name="pre_open_init", refresh_wait_seconds=0),
            client=client,
            trading_day=trading_day(is_trading_day=False),
        )

        self.assertTrue(report["ok"])
        self.assertTrue(report["skipped"])
        self.assertEqual(client.refresh_calls, [])

    def test_account_query_errors_are_reported_without_failing_job(self) -> None:
        client = FakeClient()
        client.accounts = [
            SimpleNamespace(account_id="acct-1", enabled=True),
            SimpleNamespace(account_id="acct-new", enabled=True),
        ]
        client.asset_errors = {"acct-new"}
        report = run_pre_open_init(
            JobOptions(
                job_name="pre_open_init",
                refresh_wait_seconds=0,
                refresh_timeout_seconds=0.01,
                refresh_poll_seconds=0.01,
            ),
            client=client,
            trading_day=trading_day(),
        )

        self.assertTrue(report["ok"])
        self.assertEqual(report.get("errors"), [])
        self.assertEqual(report["account_error_count"], 1)
        self.assertEqual(report["account_errors"][0]["account_id"], "acct-new")
        self.assertTrue(any("get_asset" in error for error in report["accounts"][1]["errors"]))
        self.assertEqual(report["snapshot_blocked_accounts"], ["acct-new"])
        self.assertEqual(report["open_snapshot"]["result"]["status"], "completed")
        self.assertEqual(report["open_snapshot"]["result"]["account_error_count"], 0)
        self.assertEqual(client.settlement_calls[0]["account_ids"], ["acct-1"])

    def test_post_close_can_run_for_selected_account_on_non_trading_day(self) -> None:
        client = FakeClient()
        report = run_post_close_settlement(
            JobOptions(
                job_name="post_close_settlement",
                account_ids=("acct-1",),
                allow_non_trading_day=True,
                refresh_wait_seconds=0,
            ),
            client=client,
            trading_day=trading_day(is_trading_day=False),
        )

        self.assertTrue(report["ok"])
        self.assertFalse(report["skipped"])
        self.assertEqual(len(report["accounts"]), 1)
        self.assertEqual(report["accounts"][0]["snapshot"]["non_terminal_order_ids"], ["gw-working"])
        self.assertEqual(report["accounts"][0]["snapshot"]["fees_count"], 1)
        self.assertEqual(report["accounts"][0]["snapshot"]["complete_fees_count"], 1)
        self.assertIn(("acct-1", "fee.list.query"), client.refresh_calls)
        self.assertEqual(len(client.settlement_calls), 1)
        self.assertEqual(client.settlement_calls[0]["trade_date"], "20260612")
        self.assertEqual(client.settlement_calls[0]["account_ids"], ["acct-1"])
        self.assertEqual(report["settlement_snapshot"]["result"]["status"], "completed")

    def test_post_close_blocks_stale_positions_snapshot(self) -> None:
        client = FakeClient()
        client.lagging_positions = {"acct-1"}
        report = run_post_close_settlement(
            JobOptions(
                job_name="post_close_settlement",
                account_ids=("acct-1",),
                refresh_wait_seconds=0,
                refresh_timeout_seconds=0.01,
                refresh_poll_seconds=0.01,
            ),
            client=client,
            trading_day=trading_day(),
        )

        self.assertFalse(report["ok"])
        self.assertEqual(report["snapshot_blocked_accounts"], ["acct-1"])
        self.assertEqual(report["snapshot_account_ids"], [])
        self.assertIn("no account has confirmed refreshed asset/positions", report["settlement_snapshot"]["error"])
        self.assertEqual(client.settlement_calls, [])

    def test_post_close_blocks_fresh_asset_with_failed_query_terminal(self) -> None:
        client = FakeClient()
        message_id = "msg-acct-1-account.asset.query"
        client.query_statuses[message_id] = {
            "origin_message_id": message_id,
            "account_id": "acct-1",
            "action": "account.asset.query",
            "expected_result_type": "asset_page",
            "state": "failed",
            "terminal": True,
            "success": False,
            "contradictory": False,
            "reply_count": 2,
            "terminal_count": 1,
            "replies": [
                {"status": "partial", "result_type": "asset_page", "is_last": False},
                {"status": "failed", "result_type": "error_result", "code": "QUERY_EMPTY_RESULT"},
            ],
        }

        report = run_post_close_settlement(
            JobOptions(
                job_name="post_close_settlement",
                account_ids=("acct-1",),
                refresh_wait_seconds=0,
                refresh_timeout_seconds=0.05,
                refresh_poll_seconds=0.01,
            ),
            client=client,
            trading_day=trading_day(),
        )

        account = report["accounts"][0]
        self.assertTrue(account["refresh_freshness"]["asset_fresh"])
        self.assertTrue(account["refresh_freshness"]["query_terminal_failure"])
        self.assertTrue(account["snapshot_blocked"])
        self.assertIn("QUERY_EMPTY_RESULT", account["errors"][0])
        self.assertFalse(report["ok"])
        self.assertEqual(client.settlement_calls, [])

    def test_query_terminal_without_archived_reply_remains_pending(self) -> None:
        client = FakeClient()
        message_id = "msg-acct-1-account.asset.query"
        client.query_statuses[message_id] = {
            "origin_message_id": message_id,
            "state": "pending",
            "terminal": False,
            "success": False,
            "reply_count": 0,
            "terminal_count": 0,
            "replies": [],
        }
        report = {
            "refresh": [{
                "step": "asset",
                "ok": True,
                "result": {
                    "message_id": message_id,
                    "action": "account.asset.query",
                },
            }],
        }

        status = refreshed_query_terminal_status(client, report)

        self.assertFalse(status["ok"])
        self.assertFalse(status["terminal_failure"])
        self.assertEqual(status["commands"]["asset"]["state"], "pending")


if __name__ == "__main__":
    unittest.main()
