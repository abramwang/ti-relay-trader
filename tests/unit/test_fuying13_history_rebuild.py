import importlib.util
import io
import json
import tempfile
import unittest
import urllib.error
from datetime import datetime
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("fuying13_rebuild", ROOT / "scripts/rebuild-fuying13-history.py")
REBUILD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REBUILD)


def position(day, code, side, qty, balance):
    return {"成交日期": day, "代码": code, "方向": side, "成交数量": str(qty), "股份余额": str(balance)}


def repo(day, side, qty=10, gross="10000", paid="-10000.10", rate="1.400"):
    return {"成交日期": day, "代码": "204001.SH", "方向": side, "成交数量": str(qty),
            "成交金额": gross, "实付金额": paid, "成交价格": rate}


class Fuying13HistoryRebuildTest(unittest.TestCase):
    def test_cached_get_is_verified_and_tampering_fails_closed(self):
        class Response(io.BytesIO):
            status = 200
        with tempfile.TemporaryDirectory() as directory:
            client = REBUILD.MeridianEvidence("http://example.invalid", Path(directory) / "cache")
            client.opener = mock.Mock()
            client.opener.open.return_value = Response(b'{"data":[]}')
            client.get("/bars", {"start_date": "20250703"})
            client.get("/bars", {"start_date": "20250703"})
            self.assertEqual(client.network_requests, 1)
            self.assertEqual(client.cache_hits, 1)
            next(client.directory.glob("*.response.json")).write_bytes(b"{}")
            with self.assertRaisesRegex(ValueError, "evidence mismatch"):
                client.get("/bars", {"start_date": "20250703"})

    def test_failed_http_evidence_does_not_poison_a_new_recovery(self):
        class Response(io.BytesIO):
            status = 200
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            old = REBUILD.MeridianEvidence("http://example.invalid", root / "old")
            old.opener = mock.Mock()
            old.opener.open.side_effect = urllib.error.HTTPError("http://example.invalid", 503, "incomplete", {}, io.BytesIO(b'{"error":{"code":"archive_incomplete"}}'))
            with self.assertRaisesRegex(ValueError, "HTTP 503"):
                old.get("/bars", {})
            fresh = REBUILD.MeridianEvidence("http://example.invalid", root / "fresh", old.directory)
            fresh.opener = mock.Mock()
            fresh.opener.open.return_value = Response(b'{"data":[]}')
            fresh.get("/bars", {})
            self.assertEqual(fresh.network_requests, 1)
            self.assertEqual(fresh.cache_hits, 0)
            self.assertEqual(json.loads(next(old.directory.glob("*.request.json")).read_text())["status"], 503)

    def test_source_pin_and_resume_confirmation_prevent_accidental_reuse(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "report.json").write_text("{}")
            with self.assertRaisesRegex(ValueError, "handover evidence changed"):
                REBUILD.verify_evidence(root)
            output = root / "out"
            output.mkdir()
            (output / "INCOMPLETE").write_text("incomplete")
            (output / "handover.json").write_text("{}")
            args = SimpleNamespace(evidence=root, output=output, confirmed_start="20250703", opening_asset="10000867.08", confirmed_by="user", meridian_base_url="http://example.invalid", resume=True)
            with mock.patch.object(REBUILD, "verify_evidence", return_value={}):
                with self.assertRaisesRegex(ValueError, "same incomplete recovery"):
                    REBUILD.run(args)

    def test_date_windows_respect_native_range_limit_without_skipped_days(self):
        windows = list(REBUILD.date_windows("20250703", "20261019"))
        self.assertEqual(windows[0][0], "20250703")
        self.assertEqual(windows[-1][1], "20261019")
        for first, last in windows:
            self.assertLessEqual((datetime.strptime(last, "%Y%m%d") - datetime.strptime(first, "%Y%m%d")).days + 1, 365)
        self.assertEqual((datetime.strptime(windows[1][0], "%Y%m%d") - datetime.strptime(windows[0][1], "%Y%m%d")).days, 1)

    def test_dividend_uses_actual_cash_and_exact_entitlement_not_rounded_price(self):
        dividend = {"代码": "ETF", "成交日期": "20250707", "成交数量": "100", "实付金额": "12.34", "成交价格": "0.123"}
        factors = [{"security_id": "ETF", "ex_date": 20250704, "announcement_date": 20250703}]
        positions = {"20250703": {"ETF": 100}, "20250704": {}, "20250707": {}}
        reviews, adjustments = REBUILD.dividend_timing_reviews([(2, dividend)], factors, positions)
        self.assertEqual(reviews[0]["actual_cash"], "12.34")
        self.assertEqual(adjustments["20250704"], 1234)
        self.assertEqual(adjustments["20250707"], 0)
        self.assertFalse(reviews[0]["economic_nav_publishable"])
        dividend["成交数量"] = "101"
        reviews, adjustments = REBUILD.dividend_timing_reviews([(2, dividend)], factors, positions)
        self.assertEqual(reviews[0]["status"], "unresolved_entitlement_or_ex_date")
        self.assertFalse(adjustments)

    def test_pre_ex_dividend_is_separate_from_current_cum_dividend_market_value(self):
        dividend = {"代码": "STOCK", "成交日期": "20250703", "成交数量": "1000", "实付金额": "268"}
        factors = [{"security_id": "STOCK", "ex_date": 20250704, "announcement_date": 20250703}]
        reviews, adjustments = REBUILD.dividend_timing_reviews([(2, dividend)], factors, {"20250703": {"STOCK": 1000}, "20250704": {"STOCK": 1000}})
        self.assertEqual(adjustments["20250703"], -26800)
        self.assertEqual(adjustments["20250704"], 0)
        self.assertEqual(reviews[0]["ex_date"], "20250704")

    def test_oc_comparison_does_not_treat_absent_pages_as_proven_empty(self):
        report = REBUILD.compare_oc_positions({"20250703": {"ETF": 100}, "20250704": {}}, {
            "account_id": REBUILD.BROKER.ACCOUNT, "positions": [{"trade_date": "2025-07-03", "security_id": "ETF", "quantity": 90}],
        })
        self.assertEqual(report["days_compared"], 1)
        self.assertEqual(report["mismatch_days"], 1)
        self.assertEqual(report["mismatches"][0]["broker_rebuilt_quantity"], 100)

    def test_known_etf_actual_cash_does_not_invent_unknown_or_use_expected_t_plus(self):
        def cash(posted, origin, amount):
            return {"etf_related": True, "raw_fields": {"交易日期": posted}, "source_trade_date": origin, "actual_amount": amount}
        balances, scope = REBUILD.actual_etf_cash_accrual([
            cash("20250707", "20250703", "100.00"), cash("20250704", "20250703", "-1.00"),
            cash("20250707", None, "999.00"),
        ], ["20250703", "20250704", "20250707"])
        self.assertEqual(balances, {"20250703": 9900, "20250704": 10000, "20250707": 0})
        self.assertEqual(scope["unlinked_origin_rows"], 1)
        self.assertEqual(scope["explicit_origin_rows"], 2)
        with self.assertRaisesRegex(ValueError, "after actual cash"):
            REBUILD.actual_etf_cash_accrual([cash("20250703", "20250707", "1")], ["20250703"])

    def test_native_instrument_type_separates_parent_and_component(self):
        instruments = {"ETF": {"instrument_type": "etf"}, "STOCK": {"instrument_type": "stock"}}
        rows = [
            (6, position("20250703", "ETF", "买入", 100, 100)),
            (5, position("20250703", "ETF", "ETF赎回", 100, 0)),
            (4, position("20250703", "STOCK", "ETF赎回", 50, 50)),
            (3, position("20250703", "STOCK", "卖出", 50, 0)),
        ]
        daily, corporate, checked, intermediate = REBUILD.quantity_history(rows, ["20250703", "20250704"], instruments)
        self.assertEqual(daily, {"20250703": {}, "20250704": {}})
        self.assertEqual(checked, 4)
        self.assertEqual(corporate, [])
        self.assertEqual(intermediate, [])
        self.assertEqual(REBUILD.quantity_delta(position("20250703", "ETF", "ETF申购", 100, 100), instruments), 100)
        self.assertEqual(REBUILD.quantity_delta(position("20250703", "STOCK", "ETF申购", 50, 0), instruments), -50)

    def test_posting_order_and_balances_fail_closed(self):
        row = position("20250703", "STOCK", "买入", 100, 99)
        with self.assertRaisesRegex(ValueError, "quantity balance mismatch"):
            REBUILD.quantity_history([(2, row)], ["20250703"], {})
        row["股份余额"] = "100"
        row["方向"] = "unknown"
        with self.assertRaisesRegex(ValueError, "unreviewed historical"):
            REBUILD.quantity_history([(2, row)], ["20250703"], {})

    def test_internal_transfer_pair_is_not_external_capital(self):
        rows = [(3, position("20250703", "STOCK", "转托转出", 10, -10)),
                (2, position("20250703", "STOCK", "转托转入", 10, 0))]
        daily, _, _, intermediate = REBUILD.quantity_history(rows, ["20250703"], {})
        self.assertEqual(daily["20250703"], {})
        self.assertEqual(len(intermediate), 1)
        with self.assertRaisesRegex(ValueError, "in-kind capital-flow"):
            REBUILD.quantity_history(rows[:1], ["20250703"], {})

    def test_negative_trade_is_not_hidden_as_an_internal_transfer(self):
        with self.assertRaisesRegex(ValueError, "negative non-transfer"):
            REBUILD.quantity_history([(2, position("20250703", "STOCK", "卖出", 10, -10))], ["20250703"], {})

    def test_meridian_ex_date_not_broker_posting_date_drives_split(self):
        posted = {"20260703": {"ETF": 540400}, "20260706": {"ETF": 456700}}
        event = {"source_row": 2, "posting_date": "20260703", "security_id": "ETF", "quantity_delta": 270200}
        factor = {"security_id": "ETF", "announcement_date": 20260703, "ex_date": 20260706, "ex_factor": 2}
        effective, actions = REBUILD.effective_quantities(posted, [event], [factor])
        self.assertEqual(effective["20260703"]["ETF"], 270200)
        self.assertEqual(effective["20260706"]["ETF"], 456700)
        self.assertEqual(posted["20260703"]["ETF"], 540400)
        self.assertEqual(actions[0]["entitlement_base"], 270200)

    def test_corporate_action_requires_explicit_date_and_exact_entitlement(self):
        event = {"posting_date": "20260703", "security_id": "ETF", "quantity_delta": 10}
        factor = {"security_id": "ETF", "announcement_date": 20260703, "ex_date": 20260706, "ex_factor": 2}
        with self.assertRaisesRegex(ValueError, "unique Meridian"):
            REBUILD.effective_quantities({"20260703": {"ETF": 20}}, [event], [])
        with self.assertRaisesRegex(ValueError, "split ratio"):
            REBUILD.effective_quantities({"20260703": {"ETF": 21}}, [event], [factor])

    def test_repo_principal_once_and_actual_not_theoretical_interest(self):
        rows = [(3, repo("20250702", "融券")),
                (2, repo("20250703", "融券购回", gross="10001.23", paid="10001.23", rate="1.401"))]
        balances, repayments = REBUILD.repo_history(rows, ["20250702", "20250703"])
        self.assertEqual(balances, {"20250702": 1000000, "20250703": 0})
        self.assertEqual(repayments[0]["actual_interest"], "1.23")
        self.assertEqual(repayments[0]["placement_rate"], "1.400")
        self.assertEqual(repayments[0]["repayment_rate"], "1.401")
        self.assertEqual(repayments[0]["link_scope"], "principal_not_order_identity")
        with self.assertRaisesRegex(ValueError, "matching principal"):
            REBUILD.repo_history(rows[1:], ["20250703"])

    def test_repo_prefers_matching_rate_but_preserves_identity_ambiguity(self):
        rows = [(5, repo("20250702", "融券", rate="1.4")),
                (4, repo("20250702", "融券", rate="1.5")),
                (3, repo("20250703", "融券购回", gross="10001", paid="10001", rate="1.5")),
                (2, repo("20250703", "融券购回", gross="10001", paid="10001", rate="1.4"))]
        _, repayments = REBUILD.repo_history(rows, ["20250703"])
        self.assertEqual(repayments[0]["placement_row"], 4)
        self.assertFalse(repayments[0]["ambiguous_lot_identity"])
        rows[1][1]["成交价格"] = "1.4"
        _, repayments = REBUILD.repo_history(rows, ["20250703"])
        self.assertTrue(repayments[0]["ambiguous_lot_identity"])

    def test_daily_bars_require_native_unadjusted_complete_archive(self):
        row = {"security_id": "ETF", "trade_date": 20250703, "frequency": "1d", "adjustment": "none", "close": 1.234}
        payload = {"data": [row], "meta": {"archive_complete": True}}
        self.assertEqual(str(REBUILD.daily_bars(payload, "ETF", "20250703", "20250703")["20250703"]), "1.234")
        payload["meta"]["archive_complete"] = False
        with self.assertRaisesRegex(ValueError, "unconfirmed"):
            REBUILD.daily_bars(payload, "ETF", "20250703", "20250703")
        payload["meta"]["archive_complete"] = True
        payload["data"] = [row, row]
        with self.assertRaisesRegex(ValueError, "duplicate"):
            REBUILD.daily_bars(payload, "ETF", "20250703", "20250703")
        payload["data"] = [{**row, "adjustment": "pre"}]
        with self.assertRaisesRegex(ValueError, "selector mismatch"):
            REBUILD.daily_bars(payload, "ETF", "20250703", "20250703")


if __name__ == "__main__":
    unittest.main()
