import csv
import gzip
import importlib.util
import io
import json
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("fuying13_audit", ROOT / "scripts/audit-fuying13-history.py")
AUDIT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(AUDIT)


class Fuying13HistoryAuditTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data = {
            "funds": [
                ["客户号", "客户资金", "入金", "出金", "市值", "当日盈亏", "总资产", "交易日", ""],
                [AUDIT.ACCOUNT, "100", "0", "0", "0", "0", "100", "20250701", ""],
                [AUDIT.ACCOUNT, "130", "1000", "0", "0", "30", "1130", "20250702", ""],
                [AUDIT.ACCOUNT, "1135", "0", "0", "0", "5", "1135", "20250703", ""],
                [AUDIT.ACCOUNT, "135", "0", "0", "1000", "0", "1135", "20250708", ""],
            ],
            "cash_flows": [["交易日期", "交易时间", "资金账户", "流水号", "股票代码", "股票名称", "业务科目", "投资者", "名称", "入金", "出金", "资金余额", "操作摘要", ""]],
            "settlements": [["客户号", "代码", "方向", "成交数量", "成交价格", "成交金额", "实付金额", "股份余额", "成交日期", "成交时间"]],
            "executions": [["客户号", "代码", "方向", "成交量", "成交价", "成交金额", "日期", "时间", "委托编号", "成交编号"]],
        }
        self.flow("20250702", "deposit", "", "银证转帐存", "1000", "0")
        for code in sorted(AUDIT.PRIOR_STRATEGY_BONDS):
            self.settlement("20250702", code, "卖出", "1", "10", "10", "10")
            self.flow("20250702", code, code.split(".")[0], "卖出成交清算资金", "10", "0")
        self.settlement("20250702", "204001.SH", "融券", "1", "1.4", "1000", "-1000")
        self.flow("20250702", "repo", "204001", "回购融券成交划出资金", "0", "1000")
        self.settlement("20250703", "204001.SH", "融券购回", "1", "1.4", "1005", "1005")
        self.flow("20250703", "repo-return", "204001", "回购融券购回资金划入", "1005", "0")
        self.settlement("20250708", "600000.SH", "买入", "100", "10", "1000", "-1000")
        self.flow("20250708", "buy", "600000", "买入成交清算资金", "0", "1000")
        self.data["executions"] += [
            [AUDIT.ACCOUNT, "204001.SH", "通用回购逆回购", "1", "1.400", "1000", "20250702", "10:00:00", "00001\t", "00002\t"],
            [AUDIT.ACCOUNT, "600000.SH", "买入", "100", "10.000", "1000", "20250708", "10:00:00", "A00003\t", "00004\t"],
            [AUDIT.ACCOUNT, "600000.SH", "ETF赎回", "10", "0", "0", "20250708", "10:00:00", "A00005\t", "00006\t"],
            [AUDIT.ACCOUNT, "600000.SH", "ETF赎回", "10", "0", "0", "20250708", "10:00:00", "A00005\t", "00006\t"],
        ]

    def flow(self, day, identity, code, subject, incoming, outgoing):
        # The real export omits its advertised cash-balance column.
        self.data["cash_flows"].append([day, "18:00:00", AUDIT.ACCOUNT + "01", identity, code, "name", subject, AUDIT.ACCOUNT, "product", incoming, outgoing, "summary", ""])

    def settlement(self, day, code, side, qty, price, amount, paid):
        self.data["settlements"].append([AUDIT.ACCOUNT, code, side, qty, price, amount, paid, "0", day, "10:00:00"])

    def write_sources(self):
        paths = {}
        for kind, values in self.data.items():
            path = self.root / f"{AUDIT.ACCOUNT}_{AUDIT.FILES[kind]}.csv"
            with path.open("w", encoding="utf-8-sig", newline="") as target:
                csv.writer(target).writerows(values)
            paths[kind] = path
        return paths

    def check(self, **kwargs):
        paths = self.write_sources()
        files = {kind: {"sha256": "test-only-" + kind} for kind in paths}
        return AUDIT.audit(paths, files, **kwargs)

    def test_nonzero_anchor_and_bond_exclusion_do_not_publish_nav(self):
        trades, cash = io.StringIO(), io.StringIO()
        result = self.check(execution_output=trades, cash_output=cash)
        self.assertTrue(result["evidence_audit_passed"])
        self.assertFalse(result["economic_nav_publishable"])
        self.assertFalse(result["opening_balance_is_zero"])
        self.assertEqual(result["adjacent_identity_and_cash_bridges"], 3)
        self.assertEqual(result["handover_candidate"], "20250703")
        self.assertFalse(result["handover_confirmed"])
        self.assertEqual(result["ordinary_executions_exactly_matched"], 2)
        self.assertEqual(result["unmatched_delivery_rows"], 3)
        self.assertEqual(result["repeated_identical_export_rows_preserved"], 1)
        self.assertEqual(result["database_writes"], 0)
        normalized = [json.loads(line) for line in trades.getvalue().splitlines()]
        self.assertEqual(normalized[0]["broker_order_reference"], "00001")
        self.assertEqual(normalized[0]["broker_execution_id"], "00002")
        self.assertEqual(normalized[0]["executed_quantity"], 10)
        self.assertIsNone(normalized[0]["original_order_quantity"])
        self.assertIsNone(normalized[0]["order_final_status"])
        self.assertEqual(len(normalized), 2)  # No zero-price ETF transfers in ordinary executions.
        postings = [json.loads(line) for line in cash.getvalue().splitlines()]
        self.assertTrue(all(x["cash_posted_at"].endswith("+08:00") for x in postings))

    def test_exact_matching_ignores_numeric_format_but_not_time(self):
        self.check()
        self.data["executions"][2][7] = "10:00:01"
        with self.assertRaisesRegex(ValueError, "exact delivery match"):
            self.check()

    def test_broken_asset_or_cash_bridge_is_not_accepted(self):
        for column, message in [(1, "cash bridge"), (6, "asset identity")]:
            with self.subTest(column=column):
                old = self.data["funds"][3][column]
                self.data["funds"][3][column] = "1136"
                with self.assertRaisesRegex(ValueError, message):
                    self.check()
                self.data["funds"][3][column] = old

    def test_duplicate_flow_and_ordinary_execution_fail(self):
        for kind, message in [("cash_flows", "duplicate flow"), ("executions", "duplicate ordinary")]:
            with self.subTest(kind=kind):
                self.data[kind].append(self.data[kind][1].copy())
                with self.assertRaisesRegex(ValueError, message):
                    self.check()
                self.data[kind].pop()

    def test_unmatched_ordinary_delivery_is_not_a_zero_fill(self):
        self.data["executions"].pop(2)
        with self.assertRaisesRegex(ValueError, "unmatched delivery"):
            self.check()

    def test_unproved_or_remaining_bonds_prevent_boundary(self):
        self.data["settlements"][1][7] = "10"
        with self.assertRaisesRegex(ValueError, "final holdings"):
            self.check()

    def test_off_funds_date_freeze_is_audited_not_a_trade(self):
        self.settlement("20250705", "600000.SH", "手工冻结", "100", "0", "0", "0")
        report = self.check()
        self.assertEqual(report["nonfinancial_freezes_without_funds_date"], {"20250705": 1})
        self.data["settlements"][-1][6] = "-0.01"
        with self.assertRaisesRegex(ValueError, "financial settlement has no funds date"):
            self.check()

    def test_wrong_account_and_csv_shape_fail(self):
        self.data["funds"][1][0] = "other"
        with self.assertRaisesRegex(ValueError, "account mismatch"):
            self.check()
        self.data["funds"][1][0] = AUDIT.ACCOUNT
        self.data["cash_flows"][1].append("unexpected")
        self.data["cash_flows"][1].append("extra")
        with self.assertRaisesRegex(ValueError, "CSV width"):
            self.check()

    def test_frozen_sources_hashes_and_incomplete_failure_marker(self):
        self.write_sources()
        output = self.root / "evidence"
        result = AUDIT.run(self.root, output)
        self.assertFalse((output / "INCOMPLETE").exists())
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)
        self.assertEqual(len(result["artifact_sha256"]), 6)
        with gzip.open(output / "executions.csv.gz", "rt", encoding="utf-8-sig") as source:
            self.assertEqual(len(list(csv.reader(source))), len(self.data["executions"]))
        with self.assertRaises(FileExistsError):
            AUDIT.run(self.root, output)
        self.data["funds"][2][6] = "99999"
        self.write_sources()
        failed = self.root / "failed"
        with self.assertRaises(ValueError):
            AUDIT.run(self.root, failed)
        self.assertTrue((failed / "INCOMPLETE").exists())
        self.assertFalse((failed / "report.json").exists())

    def test_repo_units_and_source_dates_are_not_guessed(self):
        self.assertEqual(AUDIT.repo_quantity("204001.SH", 9999, 999900000), 99990)
        self.assertEqual(AUDIT.repo_quantity("131810.SZ", 100010, 1000100000), 100010)
        for code, qty, principal in [("204001.SH", 9999, 999900), ("unknown", 1, 10000)]:
            with self.assertRaises(ValueError):
                AUDIT.repo_quantity(code, qty, principal)
        self.assertEqual(AUDIT.source_trade_date("深圳ETF申赎159869现金差额，成交日20250708，A0001"), "20250708")
        self.assertEqual(AUDIT.source_trade_date("20250708ETF替代现金补款"), "20250708")
        self.assertIsNone(AUDIT.source_trade_date("ETF现金差额场外交收，证券代码:510310"))
        with self.assertRaises(ValueError):
            AUDIT.cents("0.001")
        with self.assertRaises(ValueError):
            AUDIT.number("NaN")


if __name__ == "__main__":
    unittest.main()
