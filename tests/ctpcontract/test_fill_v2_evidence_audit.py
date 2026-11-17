import importlib.util
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("fill_v2_audit", ROOT / "scripts/audit-ctp-fill-v2-review.py")
AUDIT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(AUDIT)


class EvidenceAuditTests(unittest.TestCase):
    def test_raw_decoding_preserves_fixed_field_and_signed_integer(self):
        evidence = {"collected_at": "source", "fields_hex": {
            "OrderSysID": "202030303132330078", "SessionID": "ffffffff",
            "Price": "000000000000f03f", "Direction": "30"}}
        selected = {"OrderSysID": {"hex": "20203030313233", "terminated": True},
                    "SessionID": -1, "Price_binary64": "3ff0000000000000",
                    "Direction": "30", "collected_at": "source"}
        AUDIT.check_selected(evidence, selected)
        selected["OrderSysID"]["hex"] = "3030313233"
        with self.assertRaises(ValueError):
            AUDIT.check_selected(evidence, selected)

    def test_changed_raw_price_rejected(self):
        with self.assertRaises(ValueError):
            AUDIT.check_selected({"fields_hex": {"Price": "000000000000f03f"}},
                                 {"Price_binary64": "0000000000000000"})

    def test_no_terminator_not_invented(self):
        AUDIT.check_selected({"fields_hex": {"TradeID": "303031"}},
                             {"TradeID": {"hex": "303031", "terminated": False}})
        with self.assertRaises(ValueError):
            AUDIT.check_selected({"fields_hex": {"TradeID": "303031"}},
                                 {"TradeID": {"hex": "303031", "terminated": True}})

    def test_utf8_framing_and_direction(self):
        parts = ["test", "ctp_4900", "300052141", "ctp-dongwu-assessment-pm-20261117",
                 "20261117", "SHFE", "5013", "31174", "B"]
        encoded, identity = AUDIT.identity(parts, "v2")
        self.assertEqual(identity, "ctp-fill-v2-c43bada1b2248121701a3ae04bb765280df13289d2cb918ef6a2339410a4f8c7")
        self.assertTrue(encoded.endswith("4:50135:311741:B"))
        parts[-1] = "S"
        self.assertNotEqual(AUDIT.identity(parts, "v2")[1], identity)
