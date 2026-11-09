import copy
import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("same_day", ROOT / "scripts/audit-fuying13-same-day-receipts.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


class SameDayReceiptAuditTest(unittest.TestCase):
    def run_audit(self, mutate=None):
        account = M.B.ACCOUNT
        def funds(day, cash, total, pnl):
            return {"客户号": account, "交易日": day, "客户资金": cash, "总资产": total,
                    "市值": "0.00", "当日盈亏": pnl, "入金": "0.00", "出金": "0.00"}
        def delivery(day, code, side, amount, balance, gross, fee="0.00"):
            return {"成交日期": day, "代码": code, "方向": side, "实付金额": amount,
                    "本次资金余额": balance, "成交金额": gross, "实收佣金": fee, "成交数量": "1"}
        def execution(day, code, side, ref, gross):
            return {"日期": day, "代码": code, "方向": side, "委托编号": ref, "成交金额": gross}
        def flow(identity, code, subject, amount, summary):
            return {"account_id": account, "broker_flow_id": identity, "source_row": int(identity), "actual_amount": amount,
                    "source_trade_date": None, "cash_posted_at": "2026-10-12T19:06:00+08:00",
                    "raw_fields": {"交易日期": M.DAY, "股票代码": code, "业务科目": subject, "操作摘要": summary}}
        rows = {
            "funds": [(2, funds(M.PREVIOUS,"100.00","1100.00","0.00")), (3, funds(M.DAY,"1205.00","1705.00","605.00"))],
            "settlements": [
                (2, delivery(M.PREVIOUS,"204001.SH","融券","-1000.00","100.00","1000.00")),
                (3, delivery(M.DAY,"204001.SH","融券购回","1010.00","1110.00","1010.00")),
                (4, delivery(M.DAY,"510310.SH","ETF赎回","600.00","1710.00","600.00")),
                (5, delivery(M.DAY,"204001.SH","融券","-505.00","1205.00","500.00","5.00"))],
            "executions": [(2, execution(M.PREVIOUS,"204001.SH","融券","repo1","1000.00")),
                           (3, execution(M.DAY,"510310.SH","ETF赎回","redemption1","600.00"))],
        }
        cash = [flow("3","204001","回购融券购回资金划入","1010.00","融券购回:成交日期20261009，申报委托号repo1"),
                flow("4","510310","ETF赎回替代现金划入","600.00","ETF赎回申报委托号redemption1"),
                flow("5","204001","回购融券成交划出资金","-505.00","repo")]
        if mutate:
            mutate(rows, cash)
        with tempfile.TemporaryDirectory() as tmp:
            reference=Path(tmp)
            for name in M.B.FILES.values():
                (reference/f"{account}_{name}.csv").write_bytes(b"synthetic test only")
            with patch.object(M.B,"rows", side_effect=lambda path,kind: copy.deepcopy(rows[kind])), patch.object(M.CHAIN.EXT,"read_cash",return_value=cash):
                return M.audit(reference)

    def test_proves_cash_not_full_settlement(self):
        result=self.run_audit()
        self.assertEqual(result["included_cash_total"],"600.00")
        self.assertEqual(result["repo_interest"],"10.00")
        self.assertEqual(result["repo_fee"],"5.00")
        self.assertEqual(len(result["balance_chain"]),3)
        self.assertFalse(result["settlement_complete"])
        self.assertFalse(result["economic_nav_publishable"])

    def test_rejects_duplicate_or_changed_cash(self):
        for mutation in [lambda r,c:c.append(copy.deepcopy(c[1])),
                         lambda r,c:c[1].update(actual_amount="599.00")]:
            with self.assertRaises(ValueError): self.run_audit(mutation)

    def test_rejects_wrong_security_or_reused_order_reference(self):
        def reused(rows,cash):
            number,row=rows["executions"][1]
            rows["executions"].append((4,{**row,"日期":M.PREVIOUS}))
        for mutation in [lambda r,c:r["executions"][1][1].update({"代码":"159915.SZ"}), reused]:
            with self.assertRaises(ValueError): self.run_audit(mutation)

    def test_rejects_unknown_assets_or_unmatched_repo(self):
        def assets(rows,cash):
            rows["funds"][1][1].update({"总资产":"1706.00","当日盈亏":"606.00"})
        for mutation in [assets, lambda r,c:c[0]["raw_fields"].update({"操作摘要":"wrong repo"})]:
            with self.assertRaises(ValueError): self.run_audit(mutation)


if __name__ == "__main__":
    unittest.main()
