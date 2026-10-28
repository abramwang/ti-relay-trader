import copy
import importlib.util
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("balance_chain", ROOT / "scripts/audit-fuying13-balance-chain.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


def delivery(number, amount, after, day="20261015", code="510310.SH"):
    return number, {"实付金额": amount, "本次资金余额": after, "成交日期": day,
                    "代码": code, "方向": "ETF赎回"}


def cash(day, amount, code="510310"):
    return {"actual_amount": amount, "raw_fields": {"交易日期": day, "股票代码": code}}


class BalanceChainTest(unittest.TestCase):
    def test_unordered_records_form_unique_chain(self):
        rows = [delivery(3, "-5", "105"), delivery(1, "20", "120"), delivery(2, "-10", "110")]
        before = copy.deepcopy(rows)
        chain = M.build_chain(rows, 10000, 10500)
        self.assertEqual([e["source_row"] for e in chain], [1, 2, 3])
        self.assertEqual(rows, before)

    def test_duplicate_identity_or_branch_is_rejected(self):
        for rows in [[delivery(1, "20", "120"), delivery(1, "-10", "110")],
                     [delivery(1, "20", "120"), delivery(2, "10", "110")],
                     [delivery(1, "20", "120"), delivery(2, "10", "120")]]:
            with self.assertRaises(ValueError):
                M.build_chain(rows, 10000, 12000)

    def test_cycle_and_disconnected_data_rejected(self):
        with self.assertRaises(ValueError):
            M.build_chain([delivery(1, "20", "120"), delivery(2, "-20", "100")], 10000, 10000)
        with self.assertRaises(ValueError):
            M.build_chain([delivery(1, "20", "120"), delivery(2, "10", "300")], 10000, 12000)

    def test_wrong_endpoint_and_missing_balance_rejected(self):
        with self.assertRaises(ValueError):
            M.build_chain([delivery(1, "20", "120")], 10000, 13000)
        with self.assertRaises(ValueError):
            M.build_chain([delivery(1, "20", "")], 10000, 12000)

    def test_zero_amount_records_do_not_reset_balance(self):
        chain = M.build_chain([delivery(1, "0", "0"), delivery(2, "20", "120")], 10000, 12000)
        self.assertEqual(len(chain), 1)

    def test_posting_day_is_proved_not_assumed_from_delivery(self):
        chain = M.build_chain([delivery(1, "-20", "80", "20261015")], 10000, 8000)
        assignments, segments = M.assign_posting_days(chain, {
            "20261015": {"客户资金": "100"}, "20261016": {"客户资金": "80"}},
            "20261015", [cash("20261016", "-20")])
        self.assertEqual(assignments[0]["recorded_delivery_date"], "20261015")
        self.assertEqual(assignments[0]["inferred_posting_date"], "20261016")
        self.assertIsNone(assignments[0]["economic_source_date"])
        self.assertTrue(segments[0]["cash_multiset_verified"])

    def test_same_net_cash_is_not_enough(self):
        chain = M.build_chain([delivery(1, "-20", "80")], 10000, 8000)
        funds = {"20261015": {"客户资金": "100"}, "20261016": {"客户资金": "80"}}
        for flows in [[cash("20261016", "-10"), cash("20261016", "-10")],
                      [cash("20261016", "-20", "159915")],
                      [cash("20261015", "-20")]]:
            with self.assertRaises(ValueError):
                M.assign_posting_days(chain, funds, "20261015", flows)

    def test_same_amount_multiple_rows_are_preserved(self):
        chain = M.build_chain([delivery(1, "-10", "90"), delivery(2, "-10", "80")], 10000, 8000)
        _, segments = M.assign_posting_days(chain, {
            "20261015": {"客户资金": "100"}, "20261016": {"客户资金": "80"}}, "20261015",
            [cash("20261016", "-10"), cash("20261016", "-10")])
        self.assertEqual(segments[0]["rows"], 2)

    def test_missing_intermediate_anchor_rejected(self):
        chain = M.build_chain([delivery(1, "-20", "80")], 10000, 8000)
        with self.assertRaises(ValueError):
            M.assign_posting_days(chain, {"20261015": {"客户资金": "100"},
                                         "20261016": {"客户资金": "90"}, "20261019": {"客户资金": "80"}},
                                  "20261015", [cash("20261016", "-20")])

    def test_inactive_day_can_be_empty_but_not_hide_a_zero_net_cycle(self):
        funds = {"20261015": {"客户资金": "100"}, "20261016": {"客户资金": "100"}}
        _, segments = M.assign_posting_days([], funds, "20261015", [])
        self.assertEqual(segments[0]["rows"], 0)
        with self.assertRaises(ValueError):
            M.assign_posting_days([], funds, "20261015", [cash("20261016", "10"), cash("20261016", "-10")])

    def test_other_asset_is_not_automatically_classified_or_removed(self):
        nav = {"trade_date": "2026-10-16", "pnl_components": {"market_valuation": {
            "reverse_repo_principal": "100", "broker_reported_other_asset": "20"}}}
        funds = {"20261016": {"客户资金": "80", "市值": "200", "总资产": "400"}}
        before = copy.deepcopy((nav, funds))
        result = M.other_asset_review([nav], funds, [])
        self.assertEqual(result[0]["broker_reported_other_asset"], "20.00")
        self.assertFalse(result[0]["scope_verified"])
        self.assertFalse(result[0]["economic_nav_publishable"])
        self.assertEqual((nav, funds), before)
        funds["20261016"]["总资产"] = "401"
        with self.assertRaises(ValueError):
            M.other_asset_review([nav], funds, [])


if __name__ == "__main__":
    unittest.main()
