import copy
import importlib.util
import unittest
from pathlib import Path


SPEC = importlib.util.spec_from_file_location(
    "overlap", Path(__file__).resolve().parents[2] / "scripts/audit-fuying13-confirmed-overlap.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


def inputs():
    ids = sorted(M.FLOW_IDS)
    matrix = {"account_id": M.ACCOUNT, "resolved_cash": [], "unresolved_cash": [
        {"broker_flow_id": fid, "account_id": M.ACCOUNT, "resolved_source_trade_date": None,
         "actual_amount": "-4229.40", "raw_fields": {"股票代码": "510310", "交易日期": "20261019"}}
        for fid in ids], "cash_pcf_review": {"unresolved_candidates": [
            {"flow_ids": ids, "private_source_verified": False, "included_in_known_carry": False,
             "pcf_order_multiset_candidates": [{"source_trade_date": "20261015"}]}]}}
    days = [
        {"trade_date": M.DAY, "reported_open_asset": "100000", "reported_close_asset": "100000",
         "known_open_carry": "0", "known_close_carry": "0", "candidate_open_carry": "-12688.20",
         "candidate_close_carry": "-12688.20", "conditional_open_asset": "87311.80",
         "conditional_close_asset": "87311.80", "conditional_day_pnl": "0",
         "broker_unclassified_asset_preserved": "12688.20"},
        {"trade_date": M.POSTED, "reported_open_asset": "100000", "reported_close_asset": "87311.80",
         "known_open_carry": "0", "known_close_carry": "0", "candidate_open_carry": "-12688.20",
         "candidate_close_carry": "0", "conditional_open_asset": "87311.80",
         "conditional_close_asset": "87311.80", "conditional_day_pnl": "0",
         "broker_unclassified_asset_preserved": "0"}]
    nav = {"account_id": M.ACCOUNT, "economic_nav_publishable": False, "database_writes": 0,
           "broker_commands": 0, "cash_nav_scenarios": [
               {"name": "explicit_plus_pcf_candidates", "days": days}]}
    return nav, matrix


class ConfirmedOverlapTest(unittest.TestCase):
    def test_signed_overlay_removed_only_once_and_cash_preserved(self):
        nav, matrix = inputs()
        original = copy.deepcopy((nav, matrix))
        result = M.preview(nav, matrix)
        a, b = result["days"]
        self.assertEqual(a["after_close"], "100000.00")
        self.assertEqual(a["after_close"], b["after_open"])
        self.assertEqual(a["pnl_change"], "12688.20")
        self.assertEqual(b["pnl_change"], "-12688.20")
        self.assertEqual(b["after_close"], "87311.80")
        self.assertEqual(M.R.number(result["period_pnl_change"]), 0)
        self.assertFalse(result["economic_nav_publishable"])
        self.assertFalse(result["source_date_verified"])
        self.assertFalse(result["accounting_sign_independently_verified"])
        self.assertEqual(original, (nav, matrix))
        self.assertEqual(result, M.preview(nav, matrix))

    def test_wrong_account_rejected(self):
        for target in ("matrix", "nav", "flow"):
            nav, matrix = inputs()
            obj = {"matrix": matrix, "nav": nav, "flow": matrix["unresolved_cash"][0]}[target]
            obj["account_id"] = "307000051388"
            with self.assertRaises(ValueError):
                M.preview(nav, matrix)

    def test_missing_duplicate_and_changed_cash_rejected(self):
        for mutation in ("missing", "missing_row", "duplicate", "amount", "date", "symbol", "source"):
            nav, matrix = inputs()
            row = matrix["unresolved_cash"][0]
            if mutation == "missing":
                matrix["cash_pcf_review"]["unresolved_candidates"] = []
            elif mutation == "missing_row":
                matrix["unresolved_cash"].pop()
            elif mutation == "duplicate":
                matrix["unresolved_cash"].append(copy.deepcopy(row))
            elif mutation == "amount":
                row["actual_amount"] = "4229.40"
            elif mutation == "date":
                row["raw_fields"]["交易日期"] = "20261020"
            elif mutation == "symbol":
                row["raw_fields"]["股票代码"] = "516630"
            else:
                row["resolved_source_trade_date"] = "20261015"
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                M.preview(nav, matrix)

    def test_candidate_not_promoted_or_extended(self):
        for key in ("private_source_verified", "included_in_known_carry"):
            nav, matrix = inputs()
            matrix["cash_pcf_review"]["unresolved_candidates"][0][key] = True
            with self.assertRaises(ValueError):
                M.preview(nav, matrix)

    def test_missing_release_and_unordered_days_rejected(self):
        for mode in ("missing", "reverse", "duplicate"):
            nav, matrix = inputs()
            days = nav["cash_nav_scenarios"][0]["days"]
            if mode == "missing":
                days.pop()
            elif mode == "reverse":
                days.reverse()
            else:
                days.append(copy.deepcopy(days[-1]))
            with self.assertRaises(ValueError):
                M.preview(nav, matrix)

    def test_asset_or_overlay_change_rejected(self):
        for key in ("reported_close_asset", "conditional_open_asset", "conditional_day_pnl",
                    "candidate_close_carry", "broker_unclassified_asset_preserved"):
            nav, matrix = inputs()
            nav["cash_nav_scenarios"][0]["days"][0][key] = "1"
            with self.subTest(key=key), self.assertRaises(ValueError):
                M.preview(nav, matrix)

    def test_no_publication_or_write_evidence(self):
        for key in ("economic_nav_publishable", "database_writes", "broker_commands"):
            nav, matrix = inputs()
            nav[key] = 1
            with self.assertRaises(ValueError):
                M.preview(nav, matrix)


if __name__ == "__main__":
    unittest.main()
