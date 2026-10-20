import importlib.util
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("etf_origins", ROOT / "scripts/audit-fuying13-etf-origins.py")
ORIGINS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ORIGINS)


def flow(summary, posted="20261016", origin=None):
    return {"source_trade_date": origin, "raw_fields": {"交易日期": posted, "操作摘要": summary}}


def execution(identity="0620917503", day="20261016", side="ETF赎回"):
    return {"委托编号": identity, "日期": day, "方向": side}


class Fuying13ETFOriginsTest(unittest.TestCase):
    def test_exact_references_preserve_leading_zeros(self):
        self.assertEqual(ORIGINS.etf_order_reference("688343ETF赎回过户费，申报委托号0620917503"), "0620917503")
        self.assertEqual(ORIGINS.etf_order_reference("深圳ETF赎回159915佣金:A620004722"), "A620004722")
        self.assertEqual(ORIGINS.etf_order_reference("深圳ETF赎回159915(300146)替代现金:L010039359"), "L010039359")
        self.assertEqual(ORIGINS.etf_order_reference("深圳ETF赎回159915佣金:20117765"), "20117765")
        self.assertIsNone(ORIGINS.etf_order_reference("ETF赎回，申报委托号01，申报委托号02"))
        self.assertIsNone(ORIGINS.etf_order_reference("B882979288ETF现金差额场外交收，证券代码:510310"))

    def test_components_are_multiple_rows_not_multiple_origin_dates(self):
        index = ORIGINS.order_index([(2, execution()), (3, execution())])
        result = ORIGINS.resolve_origin(flow("688343ETF赎回过户费，申报委托号0620917503"), index)
        self.assertEqual(result["resolved_source_trade_date"], "20261016")
        self.assertEqual(result["source_date_resolution"], "unique_historical_etf_order_reference")
        self.assertEqual(result["matched_execution_rows"], [2, 3])

    def test_cross_day_receipt_uses_unique_order_date_not_posting_date(self):
        index = ORIGINS.order_index([(2, execution("A620004722", "20261015"))])
        result = ORIGINS.resolve_origin(flow("深圳ETF赎回159915佣金:A620004722"), index)
        self.assertEqual(result["resolved_source_trade_date"], "20261015")

    def test_reused_order_id_cannot_be_chosen_by_nearest_date(self):
        index = ORIGINS.order_index([(2, execution(day="20261014")), (3, execution(day="20261015"))])
        result = ORIGINS.resolve_origin(flow("688343ETF赎回过户费，申报委托号0620917503"), index)
        self.assertIsNone(result["resolved_source_trade_date"])
        self.assertEqual(result["source_date_resolution"], "order_reference_reused_across_dates")

    def test_side_and_full_reference_must_match(self):
        index = ORIGINS.order_index([(2, execution("620917503")), (3, execution(side="ETF申购")), (4, execution(side="卖出"))])
        result = ORIGINS.resolve_origin(flow("688343ETF赎回过户费，申报委托号0620917503"), index)
        self.assertIsNone(result["resolved_source_trade_date"])

    def test_future_execution_is_not_an_origin(self):
        index = ORIGINS.order_index([(2, execution(day="20261019"))])
        result = ORIGINS.resolve_origin(flow("688343ETF赎回过户费，申报委托号0620917503"), index)
        self.assertEqual(result["source_date_resolution"], "execution_after_cash_posting")
        self.assertIsNone(result["resolved_source_trade_date"])

    def test_explicit_source_conflict_is_audited_not_overwritten(self):
        index = ORIGINS.order_index([(2, execution(day="20261015"))])
        result = ORIGINS.resolve_origin(flow("ETF赎回，申报委托号0620917503", origin="20261014"), index)
        self.assertEqual(result["resolved_source_trade_date"], "20261014")
        self.assertEqual(result["source_date_resolution"], "explicit_origin_conflicts_with_execution_reference")
        with self.assertRaises(ValueError):
            ORIGINS.resolve_origin(flow("ETF赎回", origin="20261019"), index)

    def test_no_reference_stays_unresolved(self):
        result = ORIGINS.resolve_origin(flow("B882979288ETF现金差额场外交收，证券代码:510310"), {})
        self.assertIsNone(result["resolved_source_trade_date"])

    def test_coverage_keeps_missing_dates_and_independent_segments(self):
        nav = {"account_id": ORIGINS.BROKER.ACCOUNT, "trade_date": "2025-04-01", "is_current": True,
               "status": "finalized", "formula_version": "broker_cash_repo_carveout.v1"}
        response = {"ok": True, "data": {"count": 1, "navs": [nav]}}
        result = ORIGINS.published_coverage(["20250401", "20250402"], response)
        self.assertEqual(result["published_days"], 1)
        self.assertEqual(result["unpublished_days"], 1)
        self.assertEqual(len(result["segments"]), 2)
        response["data"]["count"] = 2
        with self.assertRaises(ValueError):
            ORIGINS.published_coverage(["20250401", "20250402"], response)

    def test_coverage_rejects_wrong_account_and_duplicate_dates(self):
        nav = {"account_id": "another", "trade_date": "2025-04-01", "is_current": True,
               "status": "finalized", "formula_version": "broker_cash_repo_carveout.v1"}
        response = {"ok": True, "data": {"count": 1, "navs": [nav]}}
        with self.assertRaises(ValueError):
            ORIGINS.published_coverage(["20250401"], response)
        nav["account_id"] = ORIGINS.BROKER.ACCOUNT
        response["data"] = {"count": 2, "navs": [nav, nav]}
        with self.assertRaises(ValueError):
            ORIGINS.published_coverage(["20250401"], response)


if __name__ == "__main__":
    unittest.main()
