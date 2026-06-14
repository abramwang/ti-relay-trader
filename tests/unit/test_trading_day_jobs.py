from __future__ import annotations

import sys
import unittest
from pathlib import Path
from types import SimpleNamespace


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "src"))
sys.path.insert(0, str(REPO_ROOT / "sdk" / "python"))

from relay.jobs.common import JobOptions, TradingDayInfo, run_post_close_settlement, run_pre_open_init  # noqa: E402


class FakeReceipt:
    def __init__(self, account_id: str, action: str) -> None:
        self.raw = {"account_id": account_id, "action": action, "stream_id": f"{action}-1"}


class FakeClient:
    def __init__(self) -> None:
        self.refresh_calls: list[tuple[str, str]] = []
        self.settlement_calls: list[dict[str, object]] = []

    def status(self):
        return {"status": "ok", "timezone": "Asia/Shanghai"}

    def list_accounts(self):
        return [
            SimpleNamespace(account_id="acct-1", enabled=True),
            SimpleNamespace(account_id="acct-disabled", enabled=False),
        ]

    def refresh_orders(self, account_id: str):
        return self._refresh(account_id, "order.list.query")

    def refresh_fills(self, account_id: str):
        return self._refresh(account_id, "fill.list.query")

    def refresh_asset(self, account_id: str):
        return self._refresh(account_id, "account.asset.query")

    def refresh_positions(self, account_id: str):
        return self._refresh(account_id, "account.positions.query")

    def get_asset(self, account_id: str):
        return SimpleNamespace(account_id=account_id, net_asset=1000.0, cash_available=500.0, market_value=500.0)

    def get_positions(self, account_id: str):
        return [SimpleNamespace(account_id=account_id, symbol="600000", quantity=100)]

    def list_orders(self, *, account_id: str, limit: int, trade_date: str | None = None, history: bool | None = None):
        return [
            SimpleNamespace(account_id=account_id, gateway_order_id="gw-working", is_terminal=False),
            SimpleNamespace(account_id=account_id, gateway_order_id="gw-filled", is_terminal=True),
        ]

    def list_fills(self, *, account_id: str, limit: int, trade_date: str | None = None, history: bool | None = None):
        return [SimpleNamespace(account_id=account_id, fill_id="fill-1")]

    def record_settlement_snapshot(self, **kwargs):
        self.settlement_calls.append(dict(kwargs))
        return {
            "run_id": kwargs.get("run_id"),
            "trade_date": kwargs.get("trade_date"),
            "status": "completed",
            "asset_snapshots": 1,
            "position_snapshots": 1,
        }

    def _refresh(self, account_id: str, action: str) -> FakeReceipt:
        self.refresh_calls.append((account_id, action))
        return FakeReceipt(account_id, action)


def trading_day(is_trading_day: bool = True) -> TradingDayInfo:
    return TradingDayInfo(
        requested_date="20260615",
        target_trade_date="20260615" if is_trading_day else "20260612",
        is_trading_day=is_trading_day,
        source="test",
        raw={},
    )


class TradingDayJobTest(unittest.TestCase):
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
        self.assertEqual(len(client.settlement_calls), 1)
        self.assertEqual(client.settlement_calls[0]["trade_date"], "20260612")
        self.assertEqual(client.settlement_calls[0]["account_ids"], ["acct-1"])
        self.assertEqual(report["settlement_snapshot"]["result"]["status"], "completed")


if __name__ == "__main__":
    unittest.main()
