import copy
import importlib.util
import json
import hashlib
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("special_records", ROOT / "scripts/audit-fuying13-special-records.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


class SpecialRecordsTest(unittest.TestCase):
    def fixture(self):
        account, stream, ref = M.B.ACCOUNT, "110010620724308", "0620724308"
        gateway = "external-huaxin-" + account + "01-" + stream
        raw = {"account_id": account + "01", "trade_date": "20261012", "gateway_order_id": gateway,
               "order_stream_id": stream, "order_id": 6, "symbol": "", "exchange": "SH",
               "trade_side": "R", "business_type": "E", "qty": 1, "price": 100.01,
               "basket_id": "etfarb#510310", "fill_id": "f", "record_type": "trade_fill",
               "is_transfer": False, "matched_at": "2026-10-12T10:10:30+08:00", "match_timestamp": 1791771030000}
        fields = {"order_id": 6, "order_stream_id": stream, "symbol": "510310", "exchange": "SH",
                  "trade_side": "R", "business_type": "E", "status": "filled", "is_terminal": True}
        issue = {"raw": raw, "parent_order_review": {"state": "unique_redemption_parent_identity",
                 "order": {"key": json.dumps([account, "2026-10-12", gateway, ""]), "fields": fields}}}
        cash, executions, deliveries = [], [], []
        for n, amount in enumerate(("90.00", "10.01"), 2):
            cash.append({"account_id": account, "broker_flow_id": str(n), "actual_amount": amount,
                         "source_trade_date": None, "cash_posted_at": "2026-10-12T19:00:00+08:00",
                         "raw_fields": {"交易日期": "20261012", "股票代码": "510310", "业务科目": "ETF赎回替代现金划入",
                                        "操作摘要": "510310ETF赎回替代现金，申报委托号" + ref}})
            executions.append((n, {"日期": "20261012", "时间": "10:10:30", "代码": "510310.SH", "方向": "ETF赎回",
                                   "委托编号": ref, "成交金额": amount, "成交量": "0", "成交价": "0.000"}))
            deliveries.append((n, {"成交日期": "20261012", "成交时间": "10:10:30", "代码": "510310.SH", "方向": "ETF赎回",
                                   "成交金额": amount, "成交数量": "0", "成交价格": "0.000", "实付金额": amount}))
        return issue, cash, executions, deliveries

    def test_splits_not_ordinary_execution_or_final_settlement(self):
        data = self.fixture()
        before = copy.deepcopy(data)
        r = M.review_record("2026-10-12", *data)
        self.assertEqual(r["original_amount"], "100.01")
        self.assertEqual(len(r["cash_rows"]), 2)
        self.assertEqual(r["broker_evidence"]["executions"]["state"], "cash_split_multiset_matched")
        for key in ("ordinary_fill", "settlement_complete", "cost_publishable", "economic_nav_publishable", "inferred_cash_posting_at_match_time"):
            self.assertFalse(r[key])
        self.assertEqual(data, before)

    def test_rejects_identity_amount_time_and_partial_splits(self):
        def wrong_split(i, c, e, d):
            e[0][1]["成交金额"], e[1][1]["成交金额"] = "89.00", "11.01"
        cases = [lambda i,c,e,d: i["raw"].update(account_id="other"),
                 lambda i,c,e,d: i["raw"].update(price=100.02),
                 lambda i,c,e,d: i["raw"].update(qty=900000),
                 lambda i,c,e,d: i["raw"].update(match_timestamp=1),
                 lambda i,c,e,d: i["parent_order_review"].update(state="unproven"),
                 lambda i,c,e,d: c.pop(),
                 lambda i,c,e,d: c[0]["raw_fields"].update(股票代码="516630"),
                 lambda i,c,e,d: c[0]["raw_fields"].update(交易日期="20261013"),
                 lambda i,c,e,d: c[0].update(cash_posted_at="2026-10-12T09:00:00+08:00"),
                 lambda i,c,e,d: c[0].update(actual_amount="-90.00"),
                 lambda i,c,e,d: c[0].update(source_trade_date="20261009"),
                 lambda i,c,e,d: e.append(copy.deepcopy(e[0])),
                 lambda i,c,e,d: e[0][1].update(成交量="100"),
                 lambda i,c,e,d: e[0][1].update(委托编号="wrong"),
                 lambda i,c,e,d: e[0][1].update(日期="20261009"),
                 lambda i,c,e,d: d[0][1].update(实付金额="89.99"), wrong_split]
        for index, mutate in enumerate(cases):
            with self.subTest(index=index):
                data = self.fixture()
                mutate(*data)
                with self.assertRaises(ValueError):
                    M.review_record("2026-10-12", *data)

    def test_export_outside_coverage_not_reported_as_match(self):
        data = self.fixture()
        for _, row in data[2]:
            row.update(日期="20261009", 委托编号="older")
        for _, row in data[3]:
            row["成交日期"] = "20261009"
        r = M.review_record("2026-10-12", *data)
        self.assertEqual(r["broker_evidence"]["executions"]["state"], "outside_export_coverage")
        self.assertEqual(r["broker_evidence"]["settlements"]["state"], "outside_export_coverage")

    def test_sha_guard_precedes_broker_read(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp) / "query.json"
            p.write_text("{}")
            with self.assertRaisesRegex(ValueError, "SHA mismatch"):
                M.audit(p, "wrong", Path(tmp) / "absent")

    def run_audit(self, mutation=None):
        issue, cash, executions, deliveries = self.fixture()
        for flow in cash:
            flow["raw_fields"].update(入金=flow["actual_amount"], 出金="0")
        funds = [(2, {"交易日": "20261009", "客户资金": "100.00", "总资产": "100.00", "入金": "0", "出金": "0", "当日盈亏": "0"}),
                 (3, {"交易日": "20261012", "客户资金": "200.01", "总资产": "200.01", "入金": "0", "出金": "0", "当日盈亏": "100.01"})]
        issues = [issue]
        if mutation:
            mutation(issues, cash, funds)
        report = {"account_id": M.B.ACCOUNT, "environment": "production", "database_writes": 0, "broker_commands": 0,
                  "cost_publishable": False, "economic_nav_publishable": False, "book_coverage_requested": True,
                  "summary": {"book:fills:invalid_records": len(issues)}, "days": [{"trade_date": "2026-10-12", "attempts": [{"queries": [
                      {"step": "fills", "state": "terminal_archive_consistent", "book_coverage": {"request_state": "account_day_unfiltered_request", "rows": {"state": "not_proven"}, "decode_issues": issues}}]}]}]}
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "report.json"
            path.write_text(json.dumps(report))
            sha = hashlib.sha256(path.read_bytes()).hexdigest()
            for name in M.B.FILES.values():
                (root / f"{M.B.ACCOUNT}_{name}.csv").write_bytes(b"synthetic fixture")
            rows = {"funds": funds, "executions": executions, "settlements": deliveries}
            with patch.object(M.EXT, "read_cash", return_value=cash), patch.object(M.B, "rows", side_effect=lambda p,k: copy.deepcopy(rows[k])):
                return M.audit(path, sha, root)

    def test_cash_bridge_and_consumption_guards(self):
        r = self.run_audit()
        self.assertEqual(r["summary"], {"records": 1, "cash_rows": 2, "amount": "100.01", "execution_covered": 1})
        self.assertTrue(r["records"][0]["included_in_broker_eod_cash_bridge"])
        self.assertEqual(r["database_writes"], 0)
        def reused(issues, cash, funds):
            extra = copy.deepcopy(issues[0])
            extra["raw"]["fill_id"] = "different-record-same-cash"
            issues.append(extra)
        for mutate in [lambda i,c,f: c.append(copy.deepcopy(c[0])),
                       lambda i,c,f: i.append(copy.deepcopy(i[0])), reused,
                       lambda i,c,f: f[1][1].update(客户资金="200.02")]:
            with self.assertRaises(ValueError):
                self.run_audit(mutate)


if __name__ == "__main__":
    unittest.main()
