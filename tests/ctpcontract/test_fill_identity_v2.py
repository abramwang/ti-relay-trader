import copy
import json
from pathlib import Path
import unittest

from jsonschema import ValidationError
from query_contract import fixture_pages, identity, validate_page
from fill_linkage import FillLinkage


class FillIdentityV2Test(unittest.TestCase):
    def setUp(self):
        self.page = copy.deepcopy(fixture_pages()["fills"])
        self.old = copy.deepcopy(self.page["payload"]["items"][0])
        self.new = copy.deepcopy(self.old)
        self.route = self.page["routing"]
        self.scope = [self.route[k] for k in ("env", "broker_id", "account_id", "gateway_id")]
        self.set_id(self.new)

    def set_id(self, row):
        parts = self.scope + [row["trade_date"], row["exchange"], row["native_ids"]["trade_id"], row["native_ids"]["order_sys_id"], row["trade_side"]]
        row["fill_id"] = "ctp-fill-v2-" + identity("fill", parts, "v2")

    def test_original_vectors(self):
        path = Path(__file__).resolve().parents[2] / "testdata/fixtures/ctp_fill_identity_v2_candidate_20261117.json"
        for case in json.loads(path.read_text())["cases"]:
            self.assertEqual("ctp-fill-v2-" + identity("fill", case["parts"], "v2"), case["fill_id"])

    def test_two_sides_and_same_version_replay(self):
        other = copy.deepcopy(self.new)
        other["trade_side"] = "S"
        other["native_ids"]["order_sys_id"] = "other-order"
        self.set_id(other)
        self.page["payload"]["items"] = [self.new, other]
        validate_page(self.page)
        model = FillLinkage(self.route, native_broker="4900", investor="FIXTURE_INV")
        for i, row in enumerate([self.new, other, self.new]):
            model.observe(str(i), "fill", self.route, json.dumps(row))
        self.assertEqual(len(model.fills), 2)

    def test_mixed_versions_not_counted(self):
        for pair in ([self.old, self.new], [self.new, self.old]):
            self.page["payload"]["items"] = pair
            with self.assertRaises(ValueError):
                validate_page(self.page)
            model = FillLinkage(self.route, native_broker="4900", investor="FIXTURE_INV")
            model.observe("first", "fill", self.route, json.dumps(pair[0]))
            with self.assertRaises(ValueError):
                model.observe("second", "fill", self.route, json.dumps(pair[1]))
            self.assertEqual(len(model.fills), 1)
            self.assertEqual(len(model.audit), 2)

    def test_wrong_hash_and_other_exchange(self):
        for change in ("hash", "exchange"):
            row = copy.deepcopy(self.new)
            if change == "hash":
                row["fill_id"] = "ctp-fill-v2-" + "0" * 64
            else:
                row["exchange"] = "DCE "
                self.set_id(row)
            self.page["payload"]["items"] = [row]
            with self.assertRaises((ValueError, ValidationError)):
                validate_page(self.page)

    def test_exchange_neutral_identity_keeps_venue_in_key(self):
        ids = set()
        for exchange in ("SHFE", "CFFEX", "DCE", "CZCE", "INE", "GFEX", "TESTEX"):
            row = copy.deepcopy(self.new)
            row["exchange"] = exchange
            self.set_id(row)
            self.page["payload"]["items"] = [row]
            validate_page(self.page)
            ids.add(row["fill_id"])
        self.assertEqual(len(ids), 7)


if __name__ == "__main__":
    unittest.main()
