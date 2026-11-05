import random
import struct
import unittest

from jsonschema import ValidationError

from money_contract import encode_bits
from query_contract import ROOT, SCHEMA, StrictValidator, decode_json


class MoneyContractTest(unittest.TestCase):
    def setUp(self):
        self.fixture = decode_json((ROOT / "oc_money_review_vectors.json").read_text())
        self.validator = StrictValidator({"$defs": SCHEMA["$defs"], "$ref": "#/$defs/money"})

    def test_all_56_required_optional_results(self):
        self.assertEqual(len(self.fixture["cases"]), 28)
        for case in self.fixture["cases"]:
            for field, required in (("required", True), ("optional", False)):
                with self.subTest(bits=case["bits"], field=field):
                    result = encode_bits(case["bits"], required=required)
                    self.assertEqual(result, case[field])
                    if result.get("value") is not None:
                        self.validator.validate(result["value"])
                        self.assertEqual(struct.pack(">d", float(result["value"])).hex(), case["bits"])

    def test_three_declared_rounding_modes_are_rejected(self):
        self.assertEqual(len(self.fixture["rounding_cases"]), 3)
        for case in self.fixture["rounding_cases"]:
            self.assertEqual(encode_bits(case["bits"], required=True, rounding_mode=case["rounding_mode"]),
                             {"error": case["error"]})
        # This tests the declared policy, not C/C++ fenv or OC thread behavior.

    def test_independent_deterministic_finite_roundtrips(self):
        rng = random.Random(20261105)
        checked = 0
        while checked < 2048:
            bits = f"{rng.getrandbits(64):016x}"
            result = encode_bits(bits, required=True)
            if "error" in result:
                continue
            self.validator.validate(result["value"])
            self.assertEqual(struct.pack(">d", float(result["value"])).hex(), bits)
            checked += 1

    def test_schema_length_and_missing_are_not_zero(self):
        self.validator.validate("-0")
        self.assertEqual(encode_bits(None, required=False), {"value": None})
        self.assertEqual(encode_bits("0000000000000000", required=False), {"value": "0"})
        for value in ("1" * 344, "1e300", None, 0):
            with self.assertRaises(ValidationError):
                self.validator.validate(value)
        for bits in ("0", "x" * 16, 0, True):
            with self.assertRaises(ValueError):
                encode_bits(bits, required=True)


if __name__ == "__main__":
    unittest.main()
