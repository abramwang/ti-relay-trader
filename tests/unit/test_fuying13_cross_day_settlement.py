import copy
import importlib.util
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("cross_day", ROOT / "scripts/audit-fuying13-cross-day-settlement.py")
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)


def response(rows):
    return {"data": rows, "meta": {"count": len(rows)}}


def flow(identity, day, amount, subject, summary, source=M.DAY):
    return {"account_id": M.B.ACCOUNT, "broker_flow_id": identity, "source_trade_date": source,
            "actual_amount": amount, "raw_fields": {"交易日期": day, "股票代码": "510310",
                "业务科目": subject, "操作摘要": summary}}


class CrossDaySettlementAuditTest(unittest.TestCase):
    def refund_fixture(self):
        pcf, cash = [], []
        for i in range(300):
            code = f"{i:06d}"
            pcf.append({"security_id": M.ETF, "trade_date": 20261012, "schema_version": "etf_component.v2",
                        "component_security_id": code + (".SZ" if i < 99 else ".SH"), "stock_amount": "100"})
            if i < 99:
                for j in range(3):
                    cash.append(flow(f"refund-{i}-{j}", "20261013", "1.00", "ETF现金替代退款",
                                     f"20261012ETF申赎510310替代现金退款，股票代码{code}"))
        return cash, response(pcf)

    def candidate_fixture(self):
        cash = [flow(f"cash-{i}", "20261014", "-2908.98", "ETF现金差额划出", "no private source", None) for i in range(3)]
        pcf = response([{"security_id": M.ETF, "schema_version": "etf_cash_component.v1", "trade_date": 20261013,
                         "pre_trade_date": 20261012, "unit_subscribe_redeem": "900000", "cash_component": "-2908.98"}])
        deliveries = [(i, {"成交日期": "20261013", "代码": M.ETF, "方向": "ETF赎回", "成交数量": "0",
                            "实付金额": "-2908.98", "实收佣金": "0", "印花税": "0", "交易规费": "0", "过户费": "0"}) for i in range(3)]
        return cash, pcf, deliveries

    def test_complete_refund_multiset(self):
        cash, pcf = self.refund_fixture()
        self.assertEqual(len(M.refund_coverage(cash, {"ok": True, "data": pcf}, 3)), 297)

    def test_equal_total_does_not_replace_component_coverage(self):
        cash, pcf = self.refund_fixture()
        cash[0]["raw_fields"]["操作摘要"] = cash[3]["raw_fields"]["操作摘要"]
        with self.assertRaises(ValueError): M.refund_coverage(cash, pcf, 3)

    def test_duplicate_wrong_account_date_and_sign_fail(self):
        for mutate in [lambda c: c.append(copy.deepcopy(c[0])), lambda c: c[0].update(account_id="other"),
                       lambda c: c[0].update(source_trade_date="20261014"),
                       lambda c: c[0].update(actual_amount="-1.00"),
                       lambda c: c[0]["raw_fields"].update({"交易日期": "20261014"})]:
            cash, pcf = self.refund_fixture()
            mutate(cash)
            with self.assertRaises(ValueError): M.refund_coverage(cash, pcf, 3)

    def test_partial_or_duplicate_pcf_fails(self):
        for mutate in [lambda p: p["meta"].update(count=301),
                       lambda p: p["data"][0].update(component_security_id=p["data"][1]["component_security_id"]),
                       lambda p: p["data"][0].update(trade_date=20261013)]:
            cash, pcf = self.refund_fixture()
            mutate(pcf)
            with self.assertRaises(ValueError): M.refund_coverage(cash, pcf, 3)

    def test_matching_cash_remains_candidate_not_fact(self):
        cash, pcf, deliveries = self.candidate_fixture()
        got = M.cash_component_candidate(cash, pcf, deliveries, 3)
        self.assertEqual(got["amount"], "-8726.94")
        self.assertFalse(got["explicit_private_source_verified"])
        self.assertFalse(got["included_in_confirmed_source_cash"])

    def test_candidate_requires_cash_delivery_and_pcf(self):
        for mutate in [lambda c,p,d: c.pop(), lambda c,p,d: d.pop(),
                       lambda c,p,d: c.append(copy.deepcopy(c[0])),
                       lambda c,p,d: c[0].update(actual_amount="-2908.97"),
                       lambda c,p,d: p["data"][0].update(pre_trade_date=20261014),
                       lambda c,p,d: d[0][1].update({"实收佣金": "1"})]:
            cash, pcf, deliveries = self.candidate_fixture()
            mutate(cash, pcf, deliveries)
            with self.assertRaises(ValueError): M.cash_component_candidate(cash, pcf, deliveries, 3)

    def test_release_not_second_income_or_claim_of_all_rights_closed(self):
        cash, _ = self.refund_fixture()
        rows = M.rights_path(cash, ["20261012", "20261013", "20261014"])
        self.assertEqual([r["known_receipts_remaining"] for r in rows], ["297.00", "0.00", "0.00"])
        self.assertTrue(all(not r["all_rights_closed"] for r in rows))
        with self.assertRaises(ValueError): M.rights_path(cash, ["20261009"])


if __name__ == "__main__":
    unittest.main()
