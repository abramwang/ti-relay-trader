import copy
from collections import Counter
from datetime import datetime
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("component_cash", ROOT / "scripts/audit-fuying13-component-cash.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


class ComponentCashTest(unittest.TestCase):
    def fixture(self):
        day, stream, ref = "2026-10-14", "12002A620000852", "A620000852"
        at = "2026-10-14T09:39:19+08:00"
        parent = {"account_id": M.B.ACCOUNT, "trade_date": day, "gateway_order_id": "external-huaxin-" + M.B.ACCOUNT + "01-" + stream,
                  "order_id": 32, "order_stream_id": stream, "trade_side": "R", "business_type": "E", "exchange": "SZ",
                  "symbol": "159869", "status": "filled", "is_terminal": True, "cum_filled_qty": 1000000, "order_qty": 1000000}
        t = {**parent, "symbol": "159900", "qty": 1, "component_qty": 1, "component_symbol": "159900", "component_exchange": "SZ",
             "price": M.Decimal("116112.60"), "component_value": None, "cash_substitution": False, "is_transfer": True,
             "record_type": "etf_component_transfer", "transfer_type": "etf_redemption_component_transfer", "fill_id": "f1",
             "basket_id": "etfarb#159869", "matched_at": at, "match_timestamp": int(datetime.fromisoformat(at).timestamp()*1000)}
        cash = [{"account_id": M.B.ACCOUNT, "source_trade_date": None, "broker_flow_id": "c1", "actual_amount": "116112.60",
                 "cash_posted_at": "2026-10-14T18:52:08+08:00", "raw_fields": {"交易日期": "20261014", "股票代码": "159869",
                 "业务科目": "ETF赎回替代现金划入", "操作摘要": "深圳ETF赎回159869(159900)替代现金:" + ref}}]
        return t, parent, cash, {}, {}, []

    def test_specific_identity_not_cash_at_order_time(self):
        data = self.fixture()
        before = copy.deepcopy(data)
        r = M.review_cash_leg(*data)
        self.assertEqual(r["amount"], "116112.60")
        self.assertEqual(r["physical_quantity"], 0)
        self.assertEqual(r["additional_broker_eod_receivable"], "0.00")
        self.assertFalse(r["intraday_cash_inclusion_verified"])
        self.assertFalse(r["final_settlement_verified"])
        self.assertEqual(data, before)

    def test_rejects_identity_money_timing_and_physical_evidence(self):
        cases = [lambda t,p,c,o,z,f: t.update(account_id="other"),
                 lambda t,p,c,o,z,f: t.update(component_symbol="000001"),
                 lambda t,p,c,o,z,f: t.update(qty=True),
                 lambda t,p,c,o,z,f: t.update(price=M.Decimal("116112.61")),
                 lambda t,p,c,o,z,f: t.update(price=M.Decimal("NaN")),
                 lambda t,p,c,o,z,f: t.update(match_timestamp=1),
                 lambda t,p,c,o,z,f: t.update(cash_substitution=True),
                 lambda t,p,c,o,z,f: p.update(order_id=33),
                 lambda t,p,c,o,z,f: p.update(cum_filled_qty=0),
                 lambda t,p,c,o,z,f: p.update(status="working"),
                 lambda t,p,c,o,z,f: c.clear(),
                 lambda t,p,c,o,z,f: c.append(copy.deepcopy(c[0])),
                 lambda t,p,c,o,z,f: c[0].update(account_id="other"),
                 lambda t,p,c,o,z,f: c[0].update(actual_amount="-116112.60"),
                 lambda t,p,c,o,z,f: c[0].update(cash_posted_at="2026-10-14T09:00:00+08:00"),
                 lambda t,p,c,o,z,f: c[0].update(cash_posted_at="2026-10-14T19:00:00"),
                 lambda t,p,c,o,z,f: c[0]["raw_fields"].update(股票代码="159915"),
                 lambda t,p,c,o,z,f: c[0]["raw_fields"].update(操作摘要="深圳ETF赎回159869(159900)替代现金:A620000853"),
                 lambda t,p,c,o,z,f: c[0]["raw_fields"].update(交易日期="20261015"),
                 lambda t,p,c,o,z,f: c[0].update(source_trade_date="20261013"),
                 lambda t,p,c,o,z,f: o.update({"159900.SZ": 1}),
                 lambda t,p,c,o,z,f: z.update({"159900.SZ": 1}),
                 lambda t,p,c,o,z,f: f.append({"symbol": "159900", "exchange": "SZ"})]
        for n, mutate in enumerate(cases):
            with self.subTest(n=n):
                data = self.fixture()
                mutate(*data)
                with self.assertRaises(ValueError):
                    M.review_cash_leg(*data)

    def capture(self, root, records):
        raw = b"".join((json.dumps(r, default=M.encode_decimal)+"\n").encode() for r in records)
        (root / "inputs.jsonl").write_bytes(raw)
        manifest = {"schema": "relay.research_engine_inputs.v1", "account_id": M.B.ACCOUNT, "complete": True,
                    "run_error": "", "database_writes": 0, "broker_commands": 0, "economic_nav_publishable": False,
                    "bytes": len(raw), "calls": len(records), "sha256": hashlib.sha256(raw).hexdigest(),
                    "methods": dict(Counter(r["method"] for r in records))}
        payload = json.dumps(manifest).encode()
        (root / "manifest.json").write_bytes(payload)
        return hashlib.sha256(payload).hexdigest()

    def record(self):
        t = self.fixture()[0]
        return {"sequence": 1, "method": "store.ListComponentTransfers", "error": "", "request": {
            "account_id": M.B.ACCOUNT, "trade_date": "2026-10-14", "history": True, "limit": 500, "cursor": "0"}, "response": [t]}

    def test_capture_digest_sequence_repeated_pages_and_terminal(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            a = self.record()
            a["response"][0]["price"] = 116112
            b = copy.deepcopy(a)
            b["sequence"] = 2
            sha = self.capture(root, [a, b])
            _, pages = M.load_capture(root, sha)
            rows, sequences = M.page_rows(pages, a["method"], "2026-10-14")
            self.assertEqual(len(rows), 1)
            self.assertEqual(sequences, [1, 2])
            b["response"][0]["price"] += 1
            sha = self.capture(root, [a, b])
            with self.assertRaisesRegex(ValueError, "page changed"):
                M.load_capture(root, sha)
            b = copy.deepcopy(a)
            sha = self.capture(root, [a, b])
            with self.assertRaisesRegex(ValueError, "sequence"):
                M.load_capture(root, sha)
            sha = self.capture(root, [a])
            with (root / "inputs.jsonl").open("ab") as f:
                f.write(b" ")
            with self.assertRaises(ValueError):
                M.load_capture(root, sha)
            pages = {(a["method"], "2026-10-14", "", 0): {"rows": [a["response"][0]]*500, "sequences": [1]}}
            with self.assertRaisesRegex(ValueError, "final/full"):
                M.page_rows(pages, a["method"], "2026-10-14")

    def test_projection_multiset_preserves_duplicates_and_changes(self):
        t = self.fixture()[0]
        digest = M.transfer_projection_sha([t])
        self.assertNotEqual(digest, M.transfer_projection_sha([t, t]))
        t["cash_substitution"] = True
        self.assertNotEqual(digest, M.transfer_projection_sha([t]))

    def test_execution_coverage_is_explicit(self):
        leg = M.review_cash_leg(*self.fixture())
        e = [(2, {"日期": "20261014", "时间": "09:39:19", "代码": "159869.SZ", "方向": "ETF赎回", "委托编号": "A620000852",
                   "成交金额": "116112.60", "成交量": "0", "成交价": "0"})]
        d = [(2, {"成交日期": "20261014", "成交时间": "09:39:19", "代码": "159869.SZ", "方向": "ETF赎回",
                   "成交金额": "116112.60", "成交数量": "0", "成交价格": "0", "实付金额": "116112.60"})]
        self.assertEqual(M.review_exports(leg, e, d)["executions"]["state"], "cash_record_matched")
        e[0][1]["成交量"] = "1"
        with self.assertRaises(ValueError):
            M.review_exports(leg, e, d)
        e[0][1]["日期"] = "20261013"
        self.assertEqual(M.review_exports(leg, e, d)["executions"]["state"], "outside_export_coverage")

    def run_audit(self, mutate=None):
        t, parent, cash, _, _, _ = self.fixture()
        day = t["trade_date"]
        capture = {}
        for method, snapshot, rows in (("store.ListOrders", "", [parent]), ("store.ListFills", "", []),
                                      ("store.ListComponentTransfers", "", [t]),
                                      ("store.ListPositionSnapshots", "open", []), ("store.ListPositionSnapshots", "close", [])):
            capture[(method, day, snapshot, 0)] = {"rows": rows, "sequences": [1]}
        manifest = {"baseline_version_id": "v", "baseline_payload_sha256": "a"*64, "sha256": "b"*64}
        shared = {"account_id": M.B.ACCOUNT, "environment": "production", "version_id": "v", "payload_sha256": "a"*64,
                  "database_writes": 0, "broker_commands": 0, "cost_publishable": False, "economic_nav_publishable": False}
        projection = M.transfer_projection_sha([t])
        book = {**shared, "days": [{"trade_date": day, "attempts": [{"queries": [{"step": "fills",
                "state": "terminal_archive_consistent", "book_coverage": {"request_state": "account_day_unfiltered_request",
                "component_transfers": {"state": "not_proven", "differences": [], "matched": 1, "query_rows": 1, "book_rows": 1,
                "query_projection_sha256": projection, "book_projection_sha256": projection}}}]}]}]}
        opening = {**shared, "opening_reviews": [
            {"trade_date": day, "previous_trade_date": "2026-10-13", "state": "opening_quantities_verified", "pre_open_positions": [], "research_open_lots": []},
            {"trade_date": "2026-10-15", "previous_trade_date": day, "state": "opening_quantities_verified",
             "previous_close_baseline": {"completeness": "complete"}, "previous_close_positions": []}]}
        if mutate:
            mutate(capture, book, opening, cash)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for suffix in M.B.FILES.values():
                (root / f"{M.B.ACCOUNT}_{suffix}.csv").write_text("synthetic")
            paths = []
            for name, obj in (("book", book), ("opening", opening)):
                raw = json.dumps(obj).encode()
                p = root / name
                p.write_bytes(raw)
                paths.extend([p, hashlib.sha256(raw).hexdigest()])
            with patch.object(M, "DATES", (day,)), patch.object(M, "load_capture", return_value=(manifest, capture)), \
                 patch.object(M.EXT, "read_cash", return_value=cash), patch.object(M.B, "rows", return_value=[]), \
                 patch.object(M.EXT, "fund_bridges", return_value=({"20261013": {}, "20261014": {}}, {"cash_bridges_passed": 1})), \
                 patch.object(M, "review_exports", return_value={}):
                return M.audit(root, "test", *paths, root)

    def test_full_audit_preserves_failure_and_rejects_bad_evidence(self):
        r = self.run_audit()
        self.assertEqual(r["summary"]["cash_legs"], 1)
        self.assertEqual(r["days"][0]["original_query_transfer_state"], "not_proven")
        self.assertFalse(r["cost_publishable"])
        self.assertFalse(r["economic_nav_publishable"])
        self.assertEqual(r["database_writes"], 0)
        def bad_projection(p,b,o,c):
            b["days"][0]["attempts"][0]["queries"][0]["book_coverage"]["component_transfers"]["query_projection_sha256"] = "c"*64
        for mutate in [lambda p,b,o,c: b.update(account_id="other"), bad_projection,
                       lambda p,b,o,c: o["opening_reviews"][1].update(previous_close_positions=[{"symbol": "000001", "exchange": "SZ", "quantity": 1}]),
                       lambda p,b,o,c: o["opening_reviews"][1]["previous_close_baseline"].update(completeness="partial"),
                       lambda p,b,o,c: c.append(copy.deepcopy(c[0]))]:
            with self.assertRaises(ValueError):
                self.run_audit(mutate)


if __name__ == "__main__":
    unittest.main()
