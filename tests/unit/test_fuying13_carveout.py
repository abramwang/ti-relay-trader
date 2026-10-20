import copy
import importlib.util
import unittest
from decimal import Decimal
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("carveout", ROOT / "scripts/preview-fuying13-carveout.py")
CARVE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CARVE)


def cash(day, amount, subject="季度利息入本金", code="", identity="flow-1"):
    return {"actual_amount": amount, "broker_flow_id": identity, "source_row": 2,
            "cash_posted_at": day + "T18:00:00+08:00",
            "raw_fields": {"交易日期": day, "业务科目": subject, "股票代码": code}}


def funds(day, amount, pnl="0.00", deposit="0.00", withdrawal="0.00", repo="0.00"):
    return {"trade_date": day, "cash": amount, "market_value": "0.00", "pnl": pnl,
            "total": str(Decimal(amount) + Decimal(repo)), "deposit": deposit,
            "withdrawal": withdrawal, "source_row": 2}


class Fuying13CarveoutTest(unittest.TestCase):
    def fixture(self):
        rows = [
            funds("20250401", "218030.91"), funds("20250623", "218087.84", "56.93"),
            funds("20250624", "2122886.03", "-18095201.81", "20000000.00"),
            funds("20250627", "12786297.58", "10663411.55"),
            funds("20250701", "10010481.58", "7084184.00", withdrawal="9860000.00"),
            funds("20250702", "1867.08", "5334595.50", withdrawal="5344210.00", repo="9999000.00"),
        ]
        repos = {row["trade_date"]: 0 for row in rows}
        repos["20250702"] = 999900000
        transfers = [{"trade_date": day, "amount": amount} for day, amount in [
            ("20250624", "-18095201.81"), ("20250627", "10663411.55"),
            ("20250701", "7084184.00"), ("20250702", "5334695.49"),
        ]]
        bank = [cash("20250624", "20000000.00", "银证转帐存"),
                cash("20250701", "-9860000.00", "银证转帐取"), cash("20250702", "-5344210.00", "银证转帐取")]
        operating = [cash("20250623", "56.93"), cash("20250702", "-9999099.99", "回购融券成交划出资金")]
        continuation = [{"trade_date": "20250703", "open_asset_excluding_fund_occupancy": "10000867.08",
                         "close_asset_excluding_fund_occupancy": "10001251.97", "daily_pnl": "384.89"}]
        return rows, repos, transfers, bank, operating, continuation

    def test_bond_principal_and_gain_are_capital_not_product_profit(self):
        rows = CARVE.preview_days(*self.fixture())
        daily = {row["trade_date"]: row for row in rows}
        self.assertEqual(daily["20250624"]["daily_pnl"], "0.00")
        self.assertEqual(daily["20250627"]["daily_pnl"], "0.00")
        self.assertEqual(daily["20250701"]["daily_pnl"], "0.00")
        self.assertEqual(daily["20250702"]["daily_pnl"], "-99.99")
        self.assertEqual(sum(Decimal(row["daily_pnl"] or "0") for row in rows[:-1]), Decimal("-43.06"))
        self.assertTrue(rows[0]["is_observed_opening_anchor"])
        self.assertIsNone(rows[0]["daily_pnl"])
        self.assertTrue(all(not row["economic_nav_publishable"] for row in rows))

    def test_unitization_uses_explicit_conventions_without_reset_at_handover(self):
        rows = CARVE.preview_days(*self.fixture())
        before, after = rows[-2], rows[-1]
        for model in CARVE.MODELS:
            self.assertNotEqual(before[model + "_unit_nav"], "1.000000000000000000000000")
            observed_ratio = Decimal(after[model + "_unit_nav"]) / Decimal(before[model + "_unit_nav"])
            expected = Decimal("10001251.97") / Decimal("10000867.08")
            self.assertLess(abs(observed_ratio - expected), Decimal("1e-23"))
        self.assertNotEqual(before[CARVE.MODELS[0] + "_unit_nav"], before[CARVE.MODELS[1] + "_unit_nav"])

    def test_cash_asset_bank_or_continuation_gaps_fail_closed(self):
        for target, message in [("cash", "cash/repo carve-out"), ("bank", "bank postings"), ("continuation", "continuation gold")]:
            with self.subTest(target=target):
                args = list(copy.deepcopy(self.fixture()))
                if target == "cash":
                    args[0][2]["cash"] = "2122886.04"
                elif target == "bank":
                    args[0][2]["deposit"] = "19999999.99"
                else:
                    args[5][0]["open_asset_excluding_fund_occupancy"] = "10000867.09"
                with self.assertRaisesRegex(ValueError, message):
                    CARVE.preview_days(*args)

    def test_nonpositive_return_denominator_is_not_replaced_with_another_asset(self):
        args = list(self.fixture())
        args[0][1]["withdrawal"] = "1000000.00"
        args[0][1]["pnl"] = "1000056.93"
        args[3].append(cash("20250623", "-1000000.00", "银证转帐取"))
        args[4][0]["actual_amount"] = "1000056.93"
        with self.assertRaisesRegex(ValueError, "nonpositive daily-flow"):
            CARVE.preview_days(*args)

    def test_transfer_has_unique_cash_evidence_and_does_not_invent_time(self):
        delivery = [(2, {"成交日期": "20250624", "方向": "买入", "代码": "254539.SH", "实付金额": "-1000.10", "成交时间": "00:00:00"})]
        posting = cash("20250624", "-1000.10", "买入成交清算资金", "254539")
        bank = cash("20250624", "2000.00", "银证转帐存", identity="bank")
        transfers, banks, operating = CARVE.classify_flows([posting, bank], delivery)
        self.assertEqual(transfers[0]["amount"], "-1000.10")
        self.assertEqual(transfers[0]["kind"], "strategy_capital_transfer_not_bank_flow")
        self.assertIsNone(transfers[0]["effective_at"])
        self.assertEqual(transfers[0]["broker_trade_time_raw"], "00:00:00")
        self.assertEqual(banks, [bank])
        self.assertFalse(operating)
        with self.assertRaisesRegex(ValueError, "unique actual cash-flow"):
            CARVE.classify_flows([posting, posting], delivery)

    def test_unknown_cash_business_and_unmatched_bond_legs_are_not_ignored(self):
        with self.assertRaisesRegex(ValueError, "unreviewed cash/repo"):
            CARVE.classify_flows([cash("20250624", "100", "ETF现金替代退款")], [])
        with self.assertRaisesRegex(ValueError, "unmatched bond cash"):
            CARVE.classify_flows([cash("20250624", "100", "卖出成交清算资金", "254539")], [])
        row = {"成交日期": "20250624", "方向": "买入", "代码": "600000.SH", "实付金额": "-100", "成交时间": "09:30:00"}
        with self.assertRaisesRegex(ValueError, "unreviewed non-bond"):
            CARVE.classify_flows([], [(2, row)])


if __name__ == "__main__":
    unittest.main()
