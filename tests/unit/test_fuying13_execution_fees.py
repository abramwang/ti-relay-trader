import copy
import importlib.util
import unittest
from decimal import Decimal as D
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("execution_fees", ROOT / "scripts/audit-fuying13-execution-fees.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


def snapshot():
    return {"database_name": "relay_trader", "transaction_read_only": "on", "account_id": M.R.B.ACCOUNT,
            "fill_count": 2, "order_group_count": 1, "orders": [{
                "account_id": M.R.B.ACCOUNT, "trade_date": "2026-10-12", "gateway_order_id": "order",
                "symbol": "159915", "exchange": "SZ", "trade_side": "B", "business_type": "S",
                "quantity": 100, "gross_amount": "123.450000", "fills": [
                    {"fill_pk": 1, "fill_id": "fill1", "qty": 50, "price": "1.234"},
                    {"fill_pk": 2, "fill_id": "fill2", "qty": 50, "price": "1.235"}],
                "fee_records": [{"fee_record_id": "fee", "record_scope": "order", "fee_as_of": "2026-10-12T15:01:00+08:00",
                    "total_fee": "0.009999", "fee_complete": True, "association_complete": True,
                    "fee_source": "broker", "currency": "CNY"}]}]}


def cash():
    return [{"account_id": M.R.B.ACCOUNT, "broker_flow_id": "cash", "actual_amount": "-123.46",
             "raw_fields": {"交易日期": "20261012", "业务科目": "买入成交清算资金", "股票代码": "159915",
                "入金": "0", "出金": "123.46", "操作摘要": "0899211571买创业板(159915)100股*1.235"}}]


class ExecutionFeeTest(unittest.TestCase):
    def test_rounded_price_is_not_authoritative_gross(self):
        s, f = snapshot(), cash(); before = copy.deepcopy((s, f))
        c, warnings = M.cash_buckets(f)
        rows = M.compare(M.execution_buckets(s), c)
        self.assertEqual(D(rows[0]["implied_fee"]), D("0.01"))
        self.assertEqual(D(rows[0]["fee_effect_on_pnl"]), D("-0.000001"))
        self.assertEqual(D(warnings[0]["invalid_fee_using_summary_price"]), D("-0.04"))
        self.assertFalse(rows[0]["per_order_fee_verified"])
        self.assertEqual((s, f), before)

    def test_sell_direction(self):
        s, f = snapshot(), cash()
        s["orders"][0]["trade_side"] = "S"
        f[0]["actual_amount"] = "123.44"
        f[0]["raw_fields"].update(业务科目="卖出成交清算资金", 入金="123.44", 出金="0", 操作摘要="0899211571卖创业板(159915)100股*1.235")
        rows = M.compare(M.execution_buckets(s), M.cash_buckets(f)[0])
        self.assertEqual(D(rows[0]["implied_fee"]), D("0.01"))

    def test_grouping_does_not_claim_order_mapping(self):
        s = snapshot(); order = copy.deepcopy(s["orders"][0]); order["gateway_order_id"] = "order2"
        order["fee_records"][0]["fee_record_id"] = "fee2"
        for f in order["fills"]: f["fill_pk"] += 2; f["fill_id"] += "b"
        s["orders"].append(order); s.update(fill_count=4, order_group_count=2)
        f = cash(); f.append(copy.deepcopy(f[0])); f[1]["broker_flow_id"] = "cash2"
        rows = M.compare(M.execution_buckets(s), M.cash_buckets(f)[0])
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["quantity"], 200)
        self.assertEqual(len(rows[0]["gateway_order_ids"]), 2)
        self.assertFalse(rows[0]["per_order_fee_verified"])

    def test_snapshot_rejects_invalid_evidence(self):
        for change in (lambda s: s.update(transaction_read_only="off"), lambda s: s.update(account_id="other"),
                       lambda s: s.update(fill_count=1), lambda s: s.update(order_group_count=True),
                       lambda s: s["orders"][0].update(business_type="E"),
                       lambda s: s["orders"][0].update(symbol="204001"),
                       lambda s: s["orders"][0].update(trade_side="R"),
                       lambda s: s["orders"][0].update(trade_date="2026-11-10"),
                       lambda s: s["orders"][0].update(quantity=99),
                       lambda s: s["orders"][0].update(gross_amount="124"),
                       lambda s: s["orders"][0]["fills"][1].update(fill_pk=1),
                       lambda s: s["orders"][0]["fills"][1].update(fill_id="fill1"),
                       lambda s: s["orders"][0]["fills"][0].update(fill_id="relay-summary:order"),
                       lambda s: s["orders"][0]["fills"][0].update(qty=True),
                       lambda s: s["orders"][0]["fills"][0].update(price="NaN"),
                       lambda s: s["orders"][0]["fee_records"].clear(),
                       lambda s: s["orders"][0]["fee_records"][0].update(currency="USD"),
                       lambda s: s["orders"][0]["fee_records"][0].update(fee_complete=False),
                       lambda s: s["orders"][0]["fee_records"][0].update(association_complete=False)):
            s = snapshot(); change(s)
            with self.assertRaises(ValueError): M.execution_buckets(s)

    def test_cash_rejects_mismatched_identity_quantity_direction(self):
        for change in (lambda f: f.append(copy.deepcopy(f[0])), lambda f: f[0].update(account_id="other"),
                       lambda f: f[0].update(actual_amount="-124"),
                       lambda f: f[0]["raw_fields"].update(股票代码="510310"),
                       lambda f: f[0]["raw_fields"].update(入金="1"),
                       lambda f: f[0]["raw_fields"].update(操作摘要="unrecognized")):
            f = cash(); change(f)
            with self.assertRaises(ValueError): M.cash_buckets(f)
        c = M.cash_buckets(cash())[0]; e = M.execution_buckets(snapshot())
        with self.assertRaises(ValueError): M.compare(e, {})
        c[next(iter(c))]["quantity"] = 99
        with self.assertRaises(ValueError): M.compare(e, c)

    def test_negative_fee_is_not_clamped(self):
        e = M.execution_buckets(snapshot()); c = M.cash_buckets(cash())[0]
        c[next(iter(c))]["cash"] = D("-123.40")
        with self.assertRaises(ValueError): M.compare(e, c)

    def test_subcent_gross_is_not_final_fee_evidence(self):
        s = snapshot()
        s["orders"][0]["fills"][0]["price"] = "1.23401"
        s["orders"][0]["gross_amount"] = "123.450500"
        row = M.compare(M.execution_buckets(s), M.cash_buckets(cash())[0])[0]
        self.assertTrue(row["oc_gross_has_subcent_precision"])
        self.assertFalse(row["implied_fee_is_final"])
        self.assertEqual(D(row["implied_fee"]), D("0.009500"))

    def test_model_fee_matches_and_residual_is_preserved(self):
        rows = M.compare(M.execution_buckets(snapshot()), M.cash_buckets(cash())[0])
        nav = {"contributions": [{"account_id": M.R.B.ACCOUNT, "trade_date": "2026-10-12",
            "summary": {"missing_items": 0, "effective_fee": "0.009999"}, "contributions": []}]}
        residual = {"parent_fee_evidence": [], "scenarios": [{}, {"name": "explicit_plus_pcf_candidates",
            "days": [{"trade_date": "2026-10-12", "diagnostic_remainder_excluding_other_change": "-0.000001"}]}]}
        d = M.daily_review(rows, nav, residual)[0]
        self.assertEqual(D(d["remaining_diagnostic_remainder"]), 0)
        self.assertFalse(d["economic_nav_publishable"])
        residual["scenarios"][1]["days"][0]["diagnostic_remainder_excluding_other_change"] = "1"
        d = M.daily_review(rows, nav, residual)[0]
        self.assertEqual(D(d["remaining_diagnostic_remainder"]), D("1.000001"))
        nav["contributions"][0]["summary"]["effective_fee"] = "0.01"
        with self.assertRaises(ValueError): M.daily_review(rows, nav, residual)


if __name__ == "__main__":
    unittest.main()
