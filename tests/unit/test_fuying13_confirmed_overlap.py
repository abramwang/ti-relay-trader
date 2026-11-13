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
            {"flow_ids": ids, "posted_date": "20261019", "amount": "-12688.20",
             "private_source_verified": False, "included_in_known_carry": False,
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


def combined_inputs():
    nav, matrix = inputs()
    scope = {"account_id": M.ACCOUNT, "economic_nav_publishable": False,
             "database_writes": 0, "broker_commands": 0}
    repo, fees, residual = (copy.deepcopy(scope) for _ in range(3))
    repo["interest_bridge"] = {"account_id": M.ACCOUNT, "days": []}
    fees["days"] = []
    residual["opening_unclassified_asset"] = "0"
    residual["scenarios"] = [{"name": "explicit_plus_pcf_candidates", "days": []}]
    nav["contributions"] = []
    previous_other = M.ZERO
    for i, row in enumerate(nav["cash_nav_scenarios"][0]["days"]):
        day = row["trade_date"]
        other = M.R.number(row["broker_unclassified_asset_preserved"])
        delta = other - previous_other
        nav["contributions"].append({"account_id": M.ACCOUNT, "trade_date": day,
                                     "summary": {"missing_items": 0, "net_contribution": "0"}})
        repo["interest_bridge"]["days"].append({"trade_date": day,
            "open_receivable_micros": (i+1)*1000000, "close_receivable_micros": (i+2)*1000000,
            "accrued_interest_micros": (i+2)*1000000, "received_interest_micros": (i+1)*1000000,
            "released_receivable_micros": (i+1)*1000000, "settlement_variance_micros": 0,
            "cash_to_accrual_adjustment_micros": 1000000, "recognized_interest_micros": (i+2)*1000000})
        fees["days"].append({"trade_date": day, "ordinary_model_fee": "1", "cash_implied_fee": "1.1",
                             "fee_effect_on_pnl": "-0.1", "remaining_diagnostic_remainder": "0"})
        residual["scenarios"][0]["days"].append({"trade_date": day,
            "repo_model_interest": str(i+2), "repo_received_interest": str(i+1),
            "repo_posting_vs_model_adjustment": "-1", "starting_model_residual": "0",
            "cash_vs_model_t0_adjustment": str(M.Decimal("1.1")-delta),
            "unclassified_asset": str(other), "unclassified_asset_change": str(delta)})
        previous_other = other
    return nav, matrix, repo, fees, residual


def component_inputs():
    args = combined_inputs()
    combined = M.compose(*args)
    funds = []
    for i, row in enumerate(args[0]["cash_nav_scenarios"][0]["days"]):
        row["repo_principal_already_in_reported_asset"] = "0"
        total = M.R.number(row["reported_close_asset"])
        component = M.R.number(row["broker_unclassified_asset_preserved"])
        funds.append((i+2, {"客户号": M.ACCOUNT, "交易日": row["trade_date"].replace("-", ""),
                           "总资产": str(total), "市值": "0", "客户资金": str(total-component)}))
    return args[0], combined, funds


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


class CombinedOverlapTest(unittest.TestCase):
    def test_composes_assets_and_attribution_without_deducting_fees_twice(self):
        args = combined_inputs()
        before = copy.deepcopy(args)
        result = M.compose(*args)
        a, b = result["combined_days"]
        self.assertEqual(M.R.number(a["combined_close_asset"]), M.Decimal("100002"))
        self.assertEqual(a["combined_close_asset"], b["combined_open_asset"])
        self.assertEqual(M.R.number(b["combined_close_asset"]), M.Decimal("87314.80"))
        self.assertEqual(M.R.number(result["combined_summary"]["period_pnl"]), 2)
        self.assertEqual(M.R.number(result["combined_summary"]["period_residual"]), 0)
        self.assertEqual(args, before)
        self.assertEqual(result, M.compose(*args))

    def test_old_zero_diagnostic_is_not_reused_as_new_daily_success(self):
        result = M.compose(*combined_inputs())
        self.assertFalse(result["combined_summary"]["all_daily_residuals_zero"])
        self.assertEqual(result["combined_summary"]["nonzero_residual_after_other_change_days"], [M.DAY, M.POSTED])
        self.assertEqual([M.R.number(d["diagnostic_after_subtracting_other_change"]) for d in result["combined_days"]],
                         [M.Decimal("12688.20"), M.Decimal("-12688.20")])
        self.assertFalse(result["economic_nav_publishable"])
        self.assertIn("signed_overlap_interpretation_not_reconciled", result["remaining_gates"])

    def test_repo_integer_precision_and_each_identity_checked(self):
        keys = ("open_receivable_micros", "close_receivable_micros", "accrued_interest_micros",
                "received_interest_micros", "released_receivable_micros", "settlement_variance_micros",
                "cash_to_accrual_adjustment_micros", "recognized_interest_micros")
        for key in keys:
            args = combined_inputs()
            args[2]["interest_bridge"]["days"][0][key] += 1
            with self.subTest(key=key), self.assertRaises(ValueError):
                M.compose(*args)
        for value in (True, 1.0, "1", 10**18):
            args = combined_inputs()
            args[2]["interest_bridge"]["days"][0]["open_receivable_micros"] = value
            with self.subTest(value=value), self.assertRaises(ValueError):
                M.compose(*args)

    def test_missing_extra_duplicate_and_out_of_order_days_fail(self):
        for index in (2, 3, 4):
            for mode in ("missing", "duplicate", "reverse"):
                args = combined_inputs()
                rows = (args[2]["interest_bridge"]["days"] if index == 2 else args[3]["days"] if index == 3
                        else args[4]["scenarios"][0]["days"])
                if mode == "missing":
                    rows.pop()
                elif mode == "duplicate":
                    rows.append(copy.deepcopy(rows[-1]))
                else:
                    rows.reverse()
                with self.subTest(index=index, mode=mode), self.assertRaises(ValueError):
                    M.compose(*args)

    def test_wrong_accounts_and_writes_fail(self):
        for index in (2, 3, 4):
            for key, value in (("account_id", "307000051388"), ("economic_nav_publishable", True),
                               ("database_writes", 1), ("broker_commands", 1)):
                args = combined_inputs()
                args[index][key] = value
                with self.subTest(index=index, key=key), self.assertRaises(ValueError):
                    M.compose(*args)

    def test_changed_fees_not_silently_absorbed(self):
        for key in ("ordinary_model_fee", "cash_implied_fee", "fee_effect_on_pnl", "remaining_diagnostic_remainder"):
            args = combined_inputs()
            args[3]["days"][0][key] = "9"
            with self.subTest(key=key), self.assertRaises(ValueError):
                M.compose(*args)

    def test_incomplete_or_changed_model_fails(self):
        for key, value in (("missing_items", 1), ("net_contribution", "1")):
            args = combined_inputs()
            args[0]["contributions"][0]["summary"][key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                M.compose(*args)

    def test_nonzero_residual_not_clipped_or_assigned_to_profit(self):
        args = combined_inputs()
        d = args[4]["scenarios"][0]["days"][0]
        d["cash_vs_model_t0_adjustment"] = str(M.R.number(d["cash_vs_model_t0_adjustment"])-M.Decimal("0.000001"))
        args[3]["days"][0]["remaining_diagnostic_remainder"] = "0.000001"
        result = M.compose(*args)
        self.assertEqual(M.R.number(result["combined_summary"]["period_residual"]), M.Decimal("0.000001"))
        self.assertFalse(result["economic_nav_publishable"])


class ComponentBasisTest(unittest.TestCase):
    def test_display_sign_separate_from_cash_sign(self):
        args = component_inputs()
        before = copy.deepcopy(args)
        result = M.component_basis_preview(*args)
        scenario = result["scenarios"][0]
        a, b = scenario["days"]
        self.assertEqual(M.R.number(a["removed_display_component"]), M.Decimal("12688.20"))
        self.assertEqual(M.R.number(a["pnl_change_from_cash_repo_curve"]), M.Decimal("-12688.20"))
        self.assertEqual(M.R.number(b["pnl_change_from_cash_repo_curve"]), M.Decimal("12688.20"))
        self.assertEqual(a["conditional_close_asset"], b["conditional_open_asset"])
        self.assertEqual(M.R.number(scenario["period_residual"]), 0)
        self.assertTrue(scenario["all_daily_residuals_zero"])
        self.assertFalse(result["economic_nav_publishable"])
        self.assertFalse(result["display_component_removal_authorized"])
        self.assertFalse(scenario["economic_nav_publishable"])
        self.assertEqual(before, args)
        self.assertEqual(result, M.component_basis_preview(*args))

    def test_candidate_does_not_upgrade_other_day_confirmation(self):
        nav, combined, funds = component_inputs()
        # Add a separately unconfirmed positive display component on posting day.
        cash_row = nav["cash_nav_scenarios"][0]["days"][1]
        cash_row["reported_close_asset"] = "87321.80"
        combined["combined_days"][1]["broker_other_asset"] = "10"
        combined["combined_days"][1]["combined_close_asset"] = "87324.80"
        combined["combined_days"][1]["original_cash_pnl"] = "10"
        funds[1][1]["总资产"] = "87321.80"
        result = M.component_basis_preview(nav, combined, funds)
        self.assertEqual(M.R.number(result["scenarios"][0]["days"][1]["removed_display_component"]), 0)
        self.assertEqual(M.R.number(result["scenarios"][0]["period_residual"]), 10)
        self.assertEqual(M.R.number(result["scenarios"][1]["period_residual"]), 0)
        self.assertFalse(result["display_component_removal_authorized"])

    def test_wrong_identity_duplicate_or_missing_funds_rejected(self):
        for mode in ("wrong", "duplicate", "missing"):
            nav, combined, funds = component_inputs()
            if mode == "wrong":
                funds[0][1]["客户号"] = "307000051388"
            elif mode == "duplicate":
                funds.append(copy.deepcopy(funds[0]))
            else:
                funds.pop()
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                M.component_basis_preview(nav, combined, funds)

    def test_component_requires_cash_market_repo_identity(self):
        for key in ("总资产", "客户资金", "市值"):
            args = component_inputs()
            args[2][0][1][key] = "1"
            with self.subTest(key=key), self.assertRaises(ValueError):
                M.component_basis_preview(*args)

    def test_nonzero_opening_component_not_silently_zeroed(self):
        args = component_inputs()
        args[1]["combined_days"][0]["broker_other_asset_change"] = "0"
        with self.assertRaises(ValueError):
            M.component_basis_preview(*args)

    def test_missing_or_reordered_days_rejected(self):
        for reverse in (True, False):
            args = component_inputs()
            if reverse:
                args[1]["combined_days"].reverse()
            else:
                args[1]["combined_days"].pop()
            with self.assertRaises(ValueError):
                M.component_basis_preview(*args)

    def test_comparison_scope_and_asset_changes_rejected(self):
        for key, value in (("account_id", "307000051388"), ("economic_nav_publishable", True),
                           ("database_writes", 1), ("broker_commands", 1)):
            args = component_inputs()
            args[1][key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                M.component_basis_preview(*args)
        args = component_inputs()
        args[1]["combined_days"][0]["combined_close_asset"] = "100003"
        with self.assertRaises(ValueError):
            M.component_basis_preview(*args)


if __name__ == "__main__":
    unittest.main()
