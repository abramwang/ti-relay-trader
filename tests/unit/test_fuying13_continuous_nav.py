import importlib.util
import unittest
from decimal import Decimal
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("continuous_nav", ROOT / "scripts/rebuild-fuying13-continuous-nav.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def delivery(day, amount, code="510310.SH", fee="0", transfer="0", qty="0"):
    return {"成交日期": day, "实付金额": amount, "代码": code, "方向": "ETF赎回", "成交数量": qty,
            "实收佣金": fee, "印花税": "0", "交易规费": "0", "过户费": transfer}


def posting(day, amount, subject="ETF现金替代退款", origin=None, code="510310", summary="ETF现金替代退款"):
    return {"etf_related": True, "actual_amount": amount, "source_trade_date": origin,
            "raw_fields": {"交易日期": day, "股票代码": code, "业务科目": subject, "操作摘要": summary}}


class Fuying13ContinuousNAVTest(unittest.TestCase):
    def test_typed_cash_multisets_preserve_multiplicity(self):
        rows = [(2, delivery("20261019", "100")), (3, delivery("20261019", "100"))]
        cash = [posting("20261019", "100", origin="20261016") for _ in range(2)]
        balance, _, audit = MODULE.etf_ledger_bridge(rows, cash, ["20261016", "20261019"])
        self.assertEqual(balance, {"20261016": 20000, "20261019": 0})
        self.assertEqual(audit["matched_actual_rows"], 2)
        with self.assertRaises(ValueError):
            MODULE.etf_ledger_bridge(rows, cash[:1], ["20261016", "20261019"])

    def test_source_date_transfer_fee_is_not_shifted_twice(self):
        rows = [(2, delivery("20261016", "-0.03", "300953.SZ", transfer="0.03", qty="100"))]
        cash = [posting("20261019", "-0.03", "unknown type", "20261016", "300953", "ETF赎回过户费，成交日20261016")]
        balance, _, _ = MODULE.etf_ledger_bridge(rows, cash, ["20261016", "20261019"])
        self.assertEqual(balance["20261016"], -3)
        self.assertEqual(balance["20261019"], 0)

    def test_unlabelled_cash_difference_uses_delivery_source_date(self):
        rows = [(2, delivery("20261016", "-4229.40"))]
        cash = [posting("20261019", "-4229.40", "ETF现金差额划出")]
        balance, _, _ = MODULE.etf_ledger_bridge(rows, cash, ["20261016", "20261019"])
        self.assertEqual(balance["20261016"], -422940)
        self.assertEqual(balance["20261019"], 0)

    def test_same_day_fees_do_not_require_parent_identity(self):
        rows = [(2, delivery("20261016", "-2.00", fee="2.00", qty="100"))]
        cash = [posting("20261016", "-2.00", "unknown type", summary="ETF赎回，申报委托号missing")]
        balance, _, _ = MODULE.etf_ledger_bridge(rows, cash, ["20261016"])
        self.assertEqual(balance["20261016"], 0)

    def test_actual_substitution_topup_is_not_a_commission(self):
        row = posting("20261019", "-20", "ETF现金替代补款", "20261016")
        self.assertEqual(MODULE.posting_kind(row), "cash")
        balance, _, _ = MODULE.etf_ledger_bridge([(2, delivery("20261019", "-20"))], [row], ["20261016", "20261019"])
        self.assertEqual(balance["20261016"], -2000)

    def test_wrong_security_amount_kind_or_anchor_fails(self):
        rows = [(2, delivery("20261019", "100"))]
        for cash in [posting("20261019", "99"), posting("20261019", "100", code="510300"),
                     posting("20261019", "100", origin="20261020")]:
            with self.assertRaises(ValueError):
                MODULE.etf_ledger_bridge(rows, [cash], ["20261016", "20261019"])
        with self.assertRaises(ValueError):
            MODULE.etf_ledger_bridge([(2, delivery("20261016", "100"))],
                                     [posting("20261019", "100", origin="20261016")], ["20261016", "20261019"])

    def test_actual_bank_timing_weights_are_bounded_and_positive(self):
        flows = [{"actual_amount": "100.00", "broker_flow_id": "deposit", "raw_fields": {"交易时间": "09:30:00"}},
                 {"actual_amount": "-20.00", "broker_flow_id": "withdrawal", "raw_fields": {"交易时间": "15:00:00"}}]
        denominator, details = MODULE.return_denominator(10000, flows)
        self.assertEqual(denominator, Decimal("200.000000"))
        self.assertEqual([Decimal(row["weight"]) for row in details], [Decimal(1), Decimal(0)])
        with self.assertRaises(ValueError):
            MODULE.return_denominator(0, [])

    def test_reported_other_asset_and_actual_liability_are_separate(self):
        book = {"20250707": {"total": "100", "deposit": "0", "withdrawal": "0", "cash": "100", "market_value": "0", "pnl": "0", "source_row": 2},
                "20250708": {"total": "120", "deposit": "0", "withdrawal": "0", "cash": "100", "market_value": "10", "pnl": "20", "source_row": 3}}
        prefix = {"20250707": {"close_asset": "100", "strategy_net_flow": "0", "cash": "100", "repo_principal": "0", "daily_pnl": "0", "daily_end_net_flow_convention_unit_nav": "1"}}
        valuations = {"20250708": {"cash": "100", "meridian_market_value": "10", "repo_principal": "5", "dividend_timing_asset_adjustment_preview": "0", "missing_prices": []}}
        result = MODULE.build_days(book, valuations, prefix, {"20250708": -200}, {}, {})
        self.assertEqual(result[1]["close_economic_nav"], 118)
        self.assertEqual(result[1]["account_day_pnl"], 18)
        self.assertEqual(result[1]["pnl_components"]["market_valuation"]["broker_reported_other_asset"], 5)
        self.assertIn("broker_reported_other_asset_retained_unclassified", result[1]["quality_flags"])

    def test_external_cash_is_never_assumed_from_asset_difference(self):
        book = {"20250708": {"total": "120", "deposit": "20", "withdrawal": "0", "cash": "100", "market_value": "10", "pnl": "0", "source_row": 2}}
        valuations = {"20250708": {"cash": "100", "meridian_market_value": "10", "repo_principal": "5", "dividend_timing_asset_adjustment_preview": "0", "missing_prices": []}}
        with self.assertRaises(ValueError):
            MODULE.build_days(book, valuations, {}, {"20250708": 0}, {}, {})


if __name__ == "__main__":
    unittest.main()
