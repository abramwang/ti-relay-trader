import copy
import importlib.util
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("cash_extension", ROOT / "scripts/audit-fuying13-cash-extension.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


def cash(identity="a", posted="20261020", origin="20261016", amount="100.00"):
    negative = amount.startswith("-")
    return {"account_id": M.BROKER.ACCOUNT, "broker_flow_id": identity,
            "actual_amount": amount, "source_trade_date": origin, "etf_related": True,
            "raw_fields": {"流水号": identity, "交易日期": posted, "交易时间": "19:00:00",
                           "股票代码": "159915", "股票名称": "ETF", "名称": "product",
                           "入金": "0" if negative else amount, "出金": amount[1:] if negative else "0",
                           "业务科目": "ETF现金差额划入", "操作摘要": "ETF cash"}}


def fund(day, total="1000.00", available="100.00", pnl="0.00"):
    return {"交易日": day, "总资产": total, "客户资金": available, "市值": "900.00",
            "入金": "0.00", "出金": "0.00", "当日盈亏": pnl}


def nav(day, total="1000.00", pnl="0.00"):
    return {"account_id": M.BROKER.ACCOUNT, "trade_date": day,
            "account_day_pnl": pnl, "close_economic_nav": total,
            "pnl_components": {"broker_statement": {"reported_close_asset": total,
                               "cash_total": "100.00", "reported_market_value": "900.00",
                               "reported_daily_pnl": pnl}}}


class CashExtensionTest(unittest.TestCase):
    def component_evidence(self):
        deliveries = [(2, {"成交日期": "20261016", "代码": "510310.SH", "方向": "ETF赎回",
                           "成交数量": "0", "实付金额": "-4229.40", "实收佣金": "0",
                           "印花税": "0", "交易规费": "0", "过户费": "0"})]
        pcf = [{"response": {"ok": True, "data": {"meta": {"count": 1}, "data": [{
            "trade_date": 20261016, "pre_trade_date": 20261015, "security_id": "510310.SH",
            "unit_subscribe_redeem": "900000", "cash_component": "-4229.4"}]}}}]
        orders = [{"account_id": M.BROKER.ACCOUNT, "trade_date": "2026-10-15", "symbol": "510310",
                   "exchange": "SH", "cum_filled_qty": 900000, "order_stream_id": "abc", "trade_side": "R"}]
        return deliveries, pcf, orders

    def test_processing_day_is_only_a_source_candidate(self):
        evidence = self.component_evidence()
        before = copy.deepcopy(evidence)
        candidate = M.component_date_candidates(*evidence)[0]
        self.assertEqual(candidate["candidate_economic_source_date"], "20261015")
        self.assertEqual(candidate["delivery_date"], "20261016")
        self.assertFalse(candidate["economic_nav_publishable"])
        self.assertEqual(evidence, before)

    def test_component_requires_matching_cash_multiset_and_redemption(self):
        deliveries, pcf, orders = self.component_evidence()
        self.assertEqual(M.component_date_candidates(deliveries * 2, pcf, orders), [])
        orders[0]["trade_side"] = "P"
        self.assertEqual(M.component_date_candidates(deliveries, pcf, orders), [])

    def test_component_rejects_truncation_duplicates_and_wrong_quantity(self):
        deliveries, pcf, orders = self.component_evidence()
        with self.assertRaises(ValueError):
            M.component_date_candidates(deliveries, pcf * 2, orders)
        with self.assertRaises(ValueError):
            M.component_date_candidates(deliveries, pcf, orders * 2)
        orders[0]["cum_filled_qty"] = 100
        with self.assertRaises(ValueError):
            M.component_date_candidates(deliveries, pcf, orders)
        pcf[0]["response"]["data"]["meta"]["count"] = 2
        with self.assertRaises(ValueError):
            M.component_date_candidates(deliveries, pcf, orders)

    def test_component_does_not_infer_fees_as_cash(self):
        deliveries, pcf, orders = self.component_evidence()
        deliveries[0][1]["过户费"] = "4229.40"
        self.assertEqual(M.component_date_candidates(deliveries, pcf, orders), [])

    def test_overlap_ignores_display_only_and_numeric_format(self):
        old = cash()
        new = copy.deepcopy(old)
        new["raw_fields"].update(股票名称="renamed", 入金="100")
        additions, stats = M.compare_exports([old], [new])
        self.assertEqual(additions, [])
        self.assertEqual(stats["display_only_changes"], 1)

    def test_overlap_financial_changes_fail(self):
        for key, value in [("入金", "101"), ("交易日期", "20261021"),
                           ("交易时间", "20:00:00"), ("操作摘要", "different"), ("股票代码", "510310")]:
            old, new = cash(), cash()
            new["raw_fields"][key] = value
            with self.assertRaises(ValueError):
                M.compare_exports([old], [new])

    def test_old_outside_range_is_allowed_but_internal_missing_fails(self):
        old = cash(posted="20261019")
        self.assertEqual(len(M.compare_exports([old], [cash("b")])[0]), 1)
        with self.assertRaises(ValueError):
            M.compare_exports([cash()], [cash("b")])

    def test_duplicates_and_account_mismatch_fail(self):
        with self.assertRaises(ValueError):
            M.compare_exports([], [cash(), cash()])
        other = cash()
        other["account_id"] = "another"
        with self.assertRaises(ValueError):
            M.compare_exports([], [other])

    def test_same_amount_different_ids_not_deduplicated(self):
        rows, _ = M.compare_exports([], [cash("a"), cash("b")])
        resolved, _ = M.resolved_deltas(rows, {})
        self.assertEqual(M.carry(resolved, "20261016"), 20000)

    def test_known_cash_releases_on_posting_date(self):
        rows, _ = M.resolved_deltas([cash()], {})
        self.assertEqual(M.carry(rows, "20261015"), 0)
        self.assertEqual(M.carry(rows, "20261016"), 10000)
        self.assertEqual(M.carry(rows, "20261019"), 10000)
        self.assertEqual(M.carry(rows, "20261020"), 0)

    def test_previous_source_rights_remain_on_both_sides_until_received(self):
        rows, _ = M.resolved_deltas([
            cash("previous", origin="20261015", amount="37606.88"),
            cash("current", posted="20261021", origin="20261016", amount="786965.46")], {})
        opening, closing = M.carry(rows, "20261015"), M.carry(rows, "20261016")
        self.assertEqual(opening, 3760688)
        self.assertEqual(closing, 82457234)
        self.assertEqual(-81006156 + closing - opening, -2309610)
        self.assertEqual(M.carry(rows, "20261019"), closing)
        self.assertEqual(M.carry(rows, "20261020"), 78696546)
        self.assertEqual(M.carry(rows, "20261021"), 0)

    def test_liabilities_and_same_day_cash(self):
        rows, _ = M.resolved_deltas([cash(amount="-2.31"), cash("b", origin="20261020")], {})
        self.assertEqual(M.carry(rows, "20261016"), -231)
        self.assertEqual(M.carry(rows, "20261020"), 0)

    def test_unknown_origin_is_not_assumed_posting_day(self):
        rows, unresolved = M.resolved_deltas([cash(origin=None)], {})
        self.assertEqual(rows, [])
        self.assertEqual(len(unresolved), 1)
        self.assertIsNone(unresolved[0]["resolved_source_trade_date"])

    def test_future_explicit_origin_fails(self):
        with self.assertRaises(ValueError):
            M.resolved_deltas([cash(origin="20261021")], {})

    def test_conflicting_order_origin_not_applied(self):
        row = cash()
        row["raw_fields"]["操作摘要"] = "深圳ETF赎回159915佣金:A123"
        rows, unresolved = M.resolved_deltas([row], {("A123", "ETF赎回"): {"20261015": [2]}})
        self.assertEqual(rows, [])
        self.assertEqual(unresolved[0]["source_date_resolution"], "explicit_origin_conflicts_with_execution_reference")

    def test_missing_funds_at_end_is_not_fabricated(self):
        funds, stats = M.fund_bridges([(2, fund("20261015")), (3, fund("20261016"))], [cash()])
        self.assertEqual(stats["cash_dates_without_funds"], ["20261020"])
        self.assertNotIn("20261020", funds)

    def test_cash_and_bank_bridge_fail_closed(self):
        with self.assertRaises(ValueError):
            M.fund_bridges([(2, fund("20261015")), (3, fund("20261016"))], [cash(posted="20261016")])
        row = cash()
        row["raw_fields"]["业务科目"] = "银证转帐存"
        with self.assertRaises(ValueError):
            M.fund_bridges([(2, fund("20261019")), (3, fund("20261020", "1100", "200", "100"))], [row])

    def test_no_cash_day_hole_or_duplicate_funds(self):
        with self.assertRaises(ValueError):
            M.fund_bridges([(2, fund("20261019")), (3, fund("20261021", "1100", "200", "100"))], [cash()])
        with self.assertRaises(ValueError):
            M.fund_bridges([(2, fund("20261019")), (3, fund("20261019"))], [])

    def test_daily_impact_releases_and_never_publishes(self):
        rows, _ = M.resolved_deltas([cash()], {})
        days = ["20261015", "20261016", "20261019", "20261020"]
        result = M.preview([nav(day) for day in days], rows, {day: fund(day) for day in days})
        self.assertEqual([r["conditional_day_pnl"] for r in result], [None, "100.00", "0.00", "-100.00"])
        self.assertTrue(all(r["economic_nav_publishable"] is False for r in result))

    def test_unknown_opening_is_not_zero(self):
        result = M.preview([nav("20261016")], [], {"20261016": fund("20261016")})
        self.assertIsNone(result[0]["conditional_day_pnl"])

    def test_changed_published_input_or_duplicate_day_fails(self):
        with self.assertRaises(ValueError):
            M.preview([nav("20261016")], [], {"20261016": fund("20261016", total="1001")})
        with self.assertRaises(ValueError):
            M.preview([nav("20261016"), nav("20261016")], [], {"20261016": fund("20261016")})


if __name__ == "__main__":
    unittest.main()
