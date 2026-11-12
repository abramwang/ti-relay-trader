import copy
import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("matrix", ROOT / "scripts/audit-fuying13-settlement-matrix.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


def order(day="2026-10-14", code="159915", ref="A620001817"):
    return {"account_id": M.B.ACCOUNT, "trade_date": day, "symbol": code, "exchange": "SZ",
            "gateway_order_id": "external-" + day + ref, "order_stream_id": "12002" + ref,
            "trade_side": "R", "business_type": "E", "cum_filled_qty": 1000000, "is_terminal": True}


def snapshot(orders=None):
    orders = orders if orders is not None else [order()]
    return {"database_name": "relay_trader", "transaction_read_only": "on", "account_id": M.B.ACCOUNT,
            "date_from": "20260601", "date_to": M.END, "count": len(orders), "orders": orders}


def flow(text, day, amount, subject, code="159915", identity="cash"):
    return {"account_id": M.B.ACCOUNT, "broker_flow_id": identity,
            "source_trade_date": M.B.source_trade_date(text), "actual_amount": amount,
            "raw_fields": {"操作摘要": text, "交易日期": day, "股票代码": code, "业务科目": subject}}


def pcf():
    return {"responses": [{"source_trade_date": "20261014",
             "query": {"security_id": "159915.SZ", "trade_date": "20261015"},
             "response": {"data": [{"security_id": "159915.SZ", "pre_trade_date": 20261014,
                          "trade_date": 20261015, "schema_version": "etf_cash_component.v1",
                          "unit_subscribe_redeem": "1000000", "cash_component": "707.14"}],
                          "meta": {"count": 1}}}]}


class SettlementMatrixTest(unittest.TestCase):
    def test_pg_identity_and_broker_component_exclusion(self):
        broker = [(1, {"方向": "ETF赎回", "日期": "20261014", "代码": "159915.SZ",
                       "成交量": "1000000", "委托编号": "A620001817"}),
                  (2, {"方向": "ETF赎回", "日期": "20261014", "代码": "300001.SZ",
                       "成交量": "100", "委托编号": "A620001817"})]
        index = M.build_order_index(snapshot(), broker)
        self.assertEqual(len(index), 1)
        self.assertEqual(index[0]["broker_execution_rows"], [1])
        self.assertFalse(index[0]["evidence_conflict"])

    def test_snapshot_account_count_duplicates_and_prefix(self):
        for mutate in [lambda s: s.update(transaction_read_only="off"),
                       lambda s: s.update(count=0), lambda s: s.update(account_id="other"),
                       lambda s: s["orders"][0].update(account_id="other"),
                       lambda s: s["orders"][0].update(order_stream_id="garbage"),
                       lambda s: s["orders"][0].update(trade_side="S"),
                       lambda s: s["orders"].append(copy.deepcopy(s["orders"][0]))]:
            s = snapshot()
            mutate(s)
            with self.assertRaises(ValueError): M.build_order_index(s, [])
        s = snapshot()
        s["orders"][0]["order_stream_id"] = "12001A180000001"
        self.assertEqual(len(M.build_order_index(s, [])), 1)

    def test_conflicting_quantity_quarantined_not_overwritten(self):
        broker = [(1, {"方向": "ETF赎回", "日期": "20261014", "代码": "159915.SZ",
                       "成交量": "2000000", "委托编号": "A620001817"})]
        index = M.build_order_index(snapshot(), broker)
        leg = M.resolve_leg(flow("深圳ETF赎回159915佣金:A620001817", "20261014", "-1.00", "unknown type"), index)
        self.assertEqual(leg["reason"], "redemption_evidence_conflict")
        self.assertIsNone(leg["resolved_source_trade_date"])
        self.assertEqual(index[0]["quantity"], 1000000)

    def test_same_day_and_explicit_later_parent(self):
        index = M.build_order_index(snapshot(), [])
        same = M.resolve_leg(flow("深圳ETF赎回159915(159900)替代现金:A620001817", "20261014", "100.00", "ETF赎回替代现金划入"), index)
        later = M.resolve_leg(flow("深圳ETF申赎159915现金差额代收付，成交日20261014，A620001817", "20261016", "707.14", "ETF现金差额划入"), index)
        self.assertEqual(same["resolved_source_trade_date"], "20261014")
        self.assertEqual(later["association_scope"], "order")
        check = M.cash_pcf_review([later], [], index, pcf())
        self.assertEqual(check["explicit_cash_component_rows_checked"], 1)

    def test_same_token_reused_across_days(self):
        index = M.build_order_index(snapshot([order(), order(day="2026-10-15")]), [])
        no_day = flow("深圳ETF赎回159915佣金:A620001817", "20261015", "-1.00", "unknown type")
        self.assertIsNone(M.resolve_leg(no_day, index)["resolved_source_trade_date"])
        explicit = flow("深圳ETF申赎159915现金差额代收付，成交日20261014，A620001817", "20261016", "1.00", "ETF现金差额划入")
        self.assertEqual(M.resolve_leg(explicit, index)["resolved_source_trade_date"], "20261014")

    def test_reuse_outside_pg_is_not_ignored(self):
        broker = [(1, {"方向": "ETF赎回", "日期": "20260601", "代码": "300001.SZ",
                       "成交量": "100", "委托编号": "A620001817"})]
        index = M.build_order_index(snapshot(), broker)
        leg = flow("深圳ETF赎回159915佣金:A620001817", "20261014", "-1.00", "unknown type")
        self.assertEqual(M.resolve_leg(leg, index)["reason"], "order_reference_reused_in_broker_history")

    def test_aggregate_fee_never_invents_basket_allocation(self):
        index = M.build_order_index(snapshot([order(), order(ref="A620002000")]), [])
        f = flow("深圳ETF赎回159915(300001)过户费，成交日20261014", "20261015", "-0.10", "unknown type", "300001")
        leg = M.resolve_leg(f, index)
        self.assertEqual(leg["association_scope"], "account_source_day_etf")
        self.assertEqual(len(leg["candidate_orders"]), 2)
        self.assertIsNone(leg["order_reference"])

    def test_wrong_direction_code_subject_or_date_rejected(self):
        original = flow("深圳ETF申赎159915现金差额代收付，成交日20261014，A620001817", "20261016", "707.14", "ETF现金差额划入")
        index = M.build_order_index(snapshot(), [])
        for mutate in [lambda r: r.update(actual_amount="-707.14"),
                       lambda r: r.update(source_trade_date="20261015"),
                       lambda r: r["raw_fields"].update({"股票代码": "159869"}),
                       lambda r: r["raw_fields"].update({"业务科目": "ETF现金差额划出"}),
                       lambda r: r["raw_fields"].update({"交易日期": "20261013"})]:
            r = copy.deepcopy(original)
            mutate(r)
            with self.assertRaises(ValueError): M.resolve_leg(r, index)

    def test_private_origin_not_inferred_from_matching_pcf(self):
        index = M.build_order_index(snapshot(), [])
        # Synthetic no-origin sample: equal amounts are still only candidates.
        f = flow("B888ETF现金差额场外交收，证券代码:159915", "20261016", "707.14", "ETF现金差额划入")
        leg = M.resolve_leg(f, index)
        check = M.cash_pcf_review([], [leg], index, pcf())
        candidate = check["unresolved_candidates"][0]
        self.assertEqual(len(candidate["pcf_order_multiset_candidates"]), 1)
        self.assertFalse(candidate["included_in_known_carry"])
        self.assertFalse(candidate["private_source_verified"])
        self.assertIsNone(leg["resolved_source_trade_date"])

    def test_pcf_missing_or_changed_evidence_rejected(self):
        index = M.build_order_index(snapshot(), [])
        leg = M.resolve_leg(flow("深圳ETF申赎159915现金差额代收付，成交日20261014，A620001817", "20261016", "707.14", "ETF现金差额划入"), index)
        for field, value in [("cash_component", "707.15"), ("unit_subscribe_redeem", "300000"),
                             ("pre_trade_date", 20261013), ("security_id", "159869.SZ")]:
            evidence = pcf()
            evidence["responses"][0]["response"]["data"][0][field] = value
            with self.assertRaises(ValueError): M.cash_pcf_review([leg], [], index, evidence)
        with self.assertRaises(ValueError): M.cash_pcf_review([leg], [], index, {"responses": []})
        evidence = pcf()
        evidence["responses"].append(copy.deepcopy(evidence["responses"][0]))
        with self.assertRaises(ValueError): M.cash_pcf_review([leg], [], index, evidence)

    def test_signed_carry_releases_without_second_income(self):
        rows = []
        for i, (posted, amount) in enumerate([("20261012", "200.00"), ("20261013", "100.00"),
                                              ("20261013", "-5.00"), ("20261014", "50.00")]):
            row = flow("synthetic", posted, amount, "fixture", identity=str(i))
            row["resolved_source_trade_date"] = "20261012"
            rows.append(row)
        funds = {d: {"当日盈亏": "0.00"} for d in ["20261009", "20261012", "20261013", "20261014"]}
        path = M.timing_path(rows, funds)
        self.assertEqual([r["known_close_carry"] for r in path], ["145.00", "50.00", "0.00"])
        self.assertEqual(sum(M.B.cents(r["known_timing_pnl_adjustment"]) for r in path), 0)
        self.assertTrue(all(not r["asset_overlap_verified"] and not r["economic_nav_publishable"] for r in path))
        with self.assertRaises(ValueError): M.timing_path(rows + [rows[0]], funds)
        rows[0]["account_id"] = "other"
        with self.assertRaises(ValueError): M.timing_path(rows, funds)


if __name__ == "__main__":
    unittest.main()
