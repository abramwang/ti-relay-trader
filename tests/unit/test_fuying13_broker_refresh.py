import importlib.util
import copy
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("refresh", Path(__file__).resolve().parents[2] / "scripts/audit-fuying13-broker-refresh.py")
R = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(R)


class FeeAuditTest(unittest.TestCase):
    def row(self):
        return {"客户号": R.B.ACCOUNT, "成交日期": "20261112", "方向": "买入", "成交金额": "100.00", "实付金额": "-101.00", "实收佣金": "1.00", "印花税": "0.00", "交易规费": "0.00", "过户费": "0.00"}

    def test_fee_cash_identity(self):
        result = R.settlement_fees([(2, self.row())])["20261112"]
        self.assertEqual(result["fee_cents"], 100)
        self.assertEqual(result["source_rows"], [2])

    def test_reject_wrong_account_or_fee_equation(self):
        for key, value in (("客户号", "307000051387"), ("实付金额", "-100.00"), ("实收佣金", "-1.00")):
            with self.subTest(key=key):
                row = self.row()
                row[key] = value
                with self.assertRaises(ValueError):
                    R.settlement_fees([(2, row)])

    def test_zero_quantity_transfer_fee_keeps_display_date(self):
        row = self.row()
        row.update({"方向": "ETF赎回", "成交金额": "0.00", "实付金额": "-0.25", "实收佣金": "0.00", "过户费": "0.25"})
        result = R.settlement_fees([(2, row)])["20261112"]
        self.assertEqual(result["fee_cents"], 25)
        self.assertEqual(result["fee_components_cents"]["过户费"], 25)


class ExecutionFeeAssociationTest(unittest.TestCase):
    def pair(self, order="001", fill="0001", fee="1.00", qty="100"):
        execution = {"客户号": R.B.ACCOUNT, "日期": "20261105", "时间": "10:00:00",
                     "代码": "510310.SH", "方向": "买入", "成交价": "1.00",
                     "成交量": qty, "成交金额": qty + ".00", "委托编号": order, "成交编号": fill}
        delivery = {"客户号": R.B.ACCOUNT, "成交日期": "20261105", "成交时间": "10:00:00",
                    "代码": "510310.SH", "方向": "买入", "成交价格": "1.00",
                    "成交数量": qty, "成交金额": qty + ".00", "实付金额": str(-R.B.number(qty) - R.B.number(fee)),
                    "实收佣金": fee, "印花税": "0", "交易规费": "0", "过户费": "0"}
        return execution, delivery

    def test_unique_and_equal_fee_multiset(self):
        a, x = self.pair()
        b, y = self.pair("002", "0002")
        report = R.execution_fees([(2, a), (3, b)], [(4, x), (5, y)])
        self.assertEqual([o["fee_cents"] for o in report["orders"]], [100, 100])
        self.assertEqual(report["match_groups"][0]["basis"], "equal_fee_multiset")
        self.assertFalse(report["match_groups"][0]["row_identity_unique"])
        self.assertFalse(report["final_settlement_verified"])
        self.assertEqual(report, R.execution_fees([(3, b), (2, a)], [(5, y), (4, x)]))
        self.assertEqual(R.execution_fees([(2, a)], [(4, x)])["match_groups"][0]["basis"], "unique_row")

    def test_different_fees_same_order_can_sum(self):
        a, x = self.pair()
        b, y = self.pair("001", "0002", "2.00")
        report = R.execution_fees([(2, a), (3, b)], [(4, x), (5, y)])
        self.assertEqual(report["orders"][0]["fee_cents"], 300)
        self.assertEqual(report["match_groups"][0]["basis"], "single_order_multiset")

    def test_different_fees_distinct_orders_are_unresolved(self):
        a, x = self.pair()
        b, y = self.pair("002", "0002", "2.00")
        report = R.execution_fees([(2, a), (3, b)], [(4, x), (5, y)])
        self.assertEqual(report["summary"]["resolved_orders"], 0)
        self.assertTrue(all(o["fee_cents"] is None for o in report["orders"]))
        self.assertEqual(report["summary"]["statement_fee_cents"], 300)

    def test_equal_total_different_components_remains_ambiguous(self):
        a, x = self.pair()
        b, y = self.pair("002", "0002")
        y.update({"实收佣金": "0.50", "过户费": "0.50"})
        report = R.execution_fees([(2, a), (3, b)], [(4, x), (5, y)])
        self.assertEqual(report["summary"]["resolved_orders"], 0)

    def test_multiplicity_account_identity_and_tuple_guards(self):
        a, x = self.pair()
        for key, value in (("客户号", "wrong"), ("成交编号", ""), ("委托编号", ""), ("成交量", "0"),
                           ("代码", "510310.SZ"), ("时间", "10:01:00"), ("日期", "20261106"),
                           ("方向", "卖出"), ("成交价", "1.01"), ("成交金额", "101.00")):
            with self.subTest(key=key):
                b = dict(a, **{key: value})
                with self.assertRaises(ValueError):
                    R.execution_fees([(2, b)], [(4, x)])
        with self.assertRaises(ValueError):
            R.execution_fees([(2, a), (3, a)], [(4, x), (5, x)])
        with self.assertRaises(ValueError):
            R.execution_fees([(2, a)], [(4, x), (5, x)])

    def test_etf_parent_fees_excluded_and_partial_not_complete(self):
        a, x = self.pair()
        b, y = self.pair("002", "0002", "2.00")
        c, z = self.pair("001", "0003", "3.00", "200")
        parent = dict(x, **{"方向": "ETF赎回", "实收佣金": "100.00"})
        report = R.execution_fees([(2, a), (3, b), (6, c)], [(4, x), (5, y), (7, z), (8, parent)])
        self.assertEqual(report["summary"]["statement_fee_cents"], 600)
        self.assertEqual(report["orders"][0]["resolved_fee_subtotal_cents"], 300)
        self.assertIsNone(report["orders"][0]["fee_cents"])

    def snapshot(self):
        return {"database_name": "relay_trader", "transaction_read_only": "on", "account_id": R.B.ACCOUNT,
                "order_group_count": 1, "fill_count": 1, "orders": [{
                    "trade_date": "2026-11-05", "gateway_order_id": "external-huaxin-" + R.B.ACCOUNT + "01-110001",
                    "account_id": R.B.ACCOUNT, "symbol": "510310", "exchange": "SH", "trade_side": "B", "business_type": "S",
                    "quantity": 100, "gross_amount": "100.00", "fills": [{"fill_id": "0001", "fill_pk": 1, "qty": 100, "price": "1.00"}],
                    "fee_records": [{"record_scope": "order", "fee_record_id": "fee-1", "fee_as_of": "2026-11-05T15:00:00+08:00",
                                     "fee_complete": True, "association_complete": True, "fee_source": "broker", "currency": "CNY", "total_fee": "1.006"}]}]}

    def test_oc_match_and_no_money_tolerance(self):
        a, x = self.pair()
        report = R.execution_fees([(2, a)], [(3, x)])
        snapshot = self.snapshot()
        compared = R.compare_execution_snapshot(report, snapshot)
        self.assertEqual(compared["matched"], 1)
        self.assertEqual(compared["orders"][0]["fee_delta_micros"], -6000)
        snapshot["orders"][0]["gross_amount"] = "100.006"
        snapshot["orders"][0]["fills"][0]["price"] = "1.00006"
        compared = R.compare_execution_snapshot(report, snapshot)
        self.assertEqual(compared["matched"], 0)
        self.assertEqual(compared["orders"][0]["status"], "gross_mismatch")
        self.assertIsNone(compared["orders"][0]["fee_cents"])

    def test_oc_suffix_not_sufficient_and_scope_fails_closed(self):
        a, x = self.pair()
        report = R.execution_fees([(2, a)], [(3, x)])
        for key, value in (("symbol", "510300"), ("exchange", "SZ"), ("trade_date", "2026-11-04"),
                           ("trade_side", "S"), ("gateway_order_id", "external-huaxin-30700005138701-110001")):
            s = self.snapshot()
            s["orders"][0][key] = value
            with self.subTest(key=key):
                self.assertEqual(R.compare_execution_snapshot(report, s)["matched"], 0)
        s = self.snapshot()
        s["transaction_read_only"] = "off"
        with self.assertRaises(ValueError):
            R.compare_execution_snapshot(report, s)

    def test_suffix_collision_and_duplicate_oc_mapping(self):
        a, x = self.pair("01")
        b, y = self.pair("001", "0002")
        report = R.execution_fees([(2, a), (3, b)], [(4, x), (5, y)])
        self.assertEqual(R.compare_execution_snapshot(report, self.snapshot())["matched"], 0)
        report = R.execution_fees([(2, a)], [(4, x)])
        s = self.snapshot()
        s["order_group_count"] = s["fill_count"] = 2
        other = copy.deepcopy(s["orders"][0])
        other["gateway_order_id"] = other["gateway_order_id"].replace("110001", "220001")
        other["fills"][0].update({"fill_pk": 2, "fill_id": "0002"})
        s["orders"].append(other)
        with self.assertRaises(ValueError):
            R.compare_execution_snapshot(report, s)
