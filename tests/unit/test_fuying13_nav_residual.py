import copy
import importlib.util
import unittest
from decimal import Decimal as D
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("residual", ROOT / "scripts/audit-fuying13-nav-residual.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


def snapshot():
    return {"database_name": "relay_trader", "transaction_read_only": "on", "account_id": M.B.ACCOUNT,
            "repo_orders": [{"account_id": M.B.ACCOUNT, "trade_date": "2026-10-12", "gateway_order_id": "repo",
                "order_stream_id": "110010622213688", "symbol": "204001", "exchange": "SH", "trade_side": "S",
                "business_type": "S", "cum_filled_qty": 10, "is_terminal": True}],
            "records": [{"account_id": M.B.ACCOUNT, "trade_date": "2026-10-12", "gateway_order_id": "parent",
                "security_id": "510310.SH", "fee_record_id": "fee", "record_scope": "order",
                "fee_complete": True, "association_complete": True, "fee_source": "broker", "total_fee": "4.00"}]}


def receipt():
    return {"account_id": M.B.ACCOUNT, "broker_flow_id": "receipt", "actual_amount": "1001.00",
            "raw_fields": {"交易日期": "20261013", "业务科目": "回购融券购回资金划入", "股票代码": "204001",
                "入金": "1001.00", "出金": "0", "操作摘要": "融券购回204001:成交日期20261012，申报委托号0622213688"}}


def inputs():
    day = {"trade_date": "2026-10-12", "conditional_day_pnl": 30, "broker_unclassified_asset_preserved": 8}
    contribution = {"account_id": M.B.ACCOUNT, "trade_date": "2026-10-12",
        "summary": {"missing_items": 0, "net_contribution": 10},
        "contributions": [{"strategy_type": "etf_redemption_t0", "redemption_quantity": 100,
            "estimated_exit_value": 100, "linked_component_sales": 90, "actual_fee": 6, "effective_fee": 3},
            {"strategy_type": "etf_redemption_t0", "strategy_id": "ETF_T0_COMPONENT:parent", "close_value": 5},
            {"strategy_type": "cash_management", "security_id": "204001.SH", "gross_contribution": 3}]}
    return day, contribution


class ResidualTest(unittest.TestCase):
    def test_fee_cash_not_counted_twice_and_repo_timing(self):
        day, contribution = inputs()
        before = copy.deepcopy(contribution)
        r = M.decompose(day, contribution, D(18), D(-1), D(4), D(1), D(5))
        self.assertEqual(D(r["cash_vs_model_t0_adjustment"]), D(13))
        self.assertEqual(D(r["remaining_execution_fee_snapshot"]), D(2))
        self.assertEqual(D(r["repo_posting_vs_model_adjustment"]), D(-2))
        self.assertEqual(D(r["remaining_residual_with_other_assets"]), D(9))
        self.assertEqual(D(r["diagnostic_remainder_excluding_other_change"]), D(6))
        self.assertFalse(r["economic_nav_publishable"])
        self.assertFalse(r["unclassified_asset_removal_authorized"])
        self.assertEqual(contribution, before)

    def test_candidate_stays_separate(self):
        d, c = inputs()
        a = M.decompose(d, c, D(18), D(0), D(4), D(1), D(5))
        b = M.decompose(d, c, D(18), D(-1), D(4), D(1), D(5))
        self.assertEqual(a["known_source_cash"], b["known_source_cash"])
        self.assertEqual(D(a["cash_vs_model_t0_adjustment"]) - D(b["cash_vs_model_t0_adjustment"]), 1)

    def test_balanced_daily_shift_cannot_prove_candidate_source(self):
        d, c = inputs()
        a = M.decompose(d, c, D(18), D(0), D(4), D(1), D(5))
        d["conditional_day_pnl"] -= 1
        b = M.decompose(d, c, D(18), D(-1), D(4), D(1), D(5))
        self.assertEqual(a["diagnostic_remainder_excluding_other_change"], b["diagnostic_remainder_excluding_other_change"])
        self.assertFalse(b["economic_nav_publishable"])

    def test_model_errors_do_not_become_zero(self):
        for change in (lambda c: c.update(account_id="other"), lambda c: c.update(trade_date="2026-10-13"),
                       lambda c: c["summary"].update(missing_items=1),
                       lambda c: c["contributions"][0].update(estimated_exit_value=None),
                       lambda c: c["contributions"][2].update(security_id="other")):
            d, c = inputs()
            change(c)
            with self.assertRaises(ValueError): M.decompose(d, c, D(18), D(0), D(4), D(1), D(0))
        d, c = inputs()
        with self.assertRaises(ValueError): M.decompose(d, c, D(18), D(0), D(7), D(1), D(0))

    def test_decimal_rejects_nonfinancial_inputs(self):
        for x in (None, True, "NaN", "Infinity", "1e2", "1,000", "", "100000000000000"):
            with self.assertRaises(ValueError): M.number(x)
        self.assertEqual(M.number("1.123456"), D("1.123456"))

    def test_parent_fee_coverage(self):
        orders = [{"source_trade_date": "20261012", "gateway_order_id": "parent", "security_id": "510310.SH"}]
        s = snapshot()
        fee, evidence = M.parent_fees(s, orders)
        self.assertEqual(fee["2026-10-12"], 4)
        self.assertEqual(len(evidence), 1)
        for change in (lambda s: s.update(account_id="other"), lambda s: s.update(transaction_read_only="off"),
                       lambda s: s.update(records=[]), lambda s: s["records"].append(copy.deepcopy(s["records"][0])),
                       lambda s: s["records"][0].update(fee_complete=False),
                       lambda s: s["records"][0].update(association_complete=False),
                       lambda s: s["records"][0].update(security_id="wrong"),
                       lambda s: s["records"][0].update(total_fee="-1")):
            s = snapshot(); change(s)
            with self.assertRaises(ValueError): M.parent_fees(s, orders)

    def test_repo_principal_is_not_income(self):
        interest, evidence = M.repo_receipts(snapshot(), [receipt()], ["2026-10-13"])
        self.assertEqual(interest["2026-10-13"], 1)
        self.assertEqual(evidence[0]["principal"], "1000")
        self.assertEqual(evidence[0]["source_trade_date"], "2026-10-12")

    def test_repo_rejects_duplicates_incomplete_and_unknown_identity(self):
        for changes in (lambda s, f: f.append(copy.deepcopy(f[0])),
                        lambda s, f: s["repo_orders"].clear(),
                        lambda s, f: s["repo_orders"][0].update(cum_filled_qty=True),
                        lambda s, f: s["repo_orders"][0].update(account_id="other"),
                        lambda s, f: s["repo_orders"][0].update(is_terminal=False),
                        lambda s, f: f[0]["raw_fields"].update(股票代码="204007"),
                        lambda s, f: f[0]["raw_fields"].update(入金="1002"),
                        lambda s, f: f[0]["raw_fields"].update(操作摘要="unknown")):
            s, f = snapshot(), [receipt()]; changes(s, f)
            with self.assertRaises(ValueError): M.repo_receipts(s, f, ["2026-10-13"])
        with self.assertRaises(ValueError): M.repo_receipts(snapshot(), [], ["2026-10-13"])

    def test_opening_asset_is_derived_not_assumed_zero(self):
        funds = [(1, {"客户号": M.B.ACCOUNT, "交易日": "20261009", "总资产": "1105", "客户资金": "100", "市值": "5"})]
        s = snapshot(); s["repo_orders"][0]["trade_date"] = "2026-10-09"
        self.assertEqual(M.opening_other_asset(s, funds, 1105), 0)
        funds[0][1]["总资产"] = "1107"
        self.assertEqual(M.opening_other_asset(s, funds, 1107), 2)
        with self.assertRaises(ValueError): M.opening_other_asset(s, funds, 1105)

    def test_grouped_parent_quantity_coverage(self):
        matrix = {"source_orders": [{"source_trade_date": "20261012", "security_id": "159915.SZ", "quantity": 100},
                                    {"source_trade_date": "20261012", "security_id": "159915.SZ", "quantity": 200}]}
        models = {"2026-10-12": {"contributions": [{"security_id": "159915.SZ", "redemption_quantity": 300}]}}
        M.redemption_coverage(matrix, models, ["2026-10-12"])
        models["2026-10-12"]["contributions"][0]["redemption_quantity"] = 100
        with self.assertRaises(ValueError): M.redemption_coverage(matrix, models, ["2026-10-12"])

    def test_positive_asset_does_not_prove_positive_cash(self):
        matrix = {"unresolved_cash": [{"raw_fields": {"交易日期": "20261013"},
                                       "actual_amount": "-8.00", "broker_flow_id": "cash"}]}
        days = [{"trade_date": "2026-10-12", "broker_unclassified_asset_preserved": 8},
                {"trade_date": "2026-10-13", "broker_unclassified_asset_preserved": 0}]
        r = M.asset_scope_observations(matrix, days)[0]
        self.assertTrue(r["absolute_amount_matches"])
        self.assertEqual(r["following_cash_signed_amount"], "-8.00")
        self.assertFalse(r["asset_overlap_verified"])


if __name__ == "__main__":
    unittest.main()
