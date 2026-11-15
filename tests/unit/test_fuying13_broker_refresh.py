import importlib.util
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
