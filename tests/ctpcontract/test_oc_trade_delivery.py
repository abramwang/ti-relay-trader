"""Review received OC output bytes; do not run or claim OC C++/online tests."""

import hashlib
from pathlib import Path
import unittest

from query_contract import ROOT, decode_json, validate_page


DELIVERY = ROOT.parent / "oc-trade-review-20261106"
PAGING = "command_review/query_reply_examples.v1.json"
TRADE = "trade_review/order_fill_query_examples.v1.json"


def load(name):
    return decode_json((DELIVERY / name).read_text())


class OCTradeDeliveryTest(unittest.TestCase):
    def test_original_manifest_and_schema(self):
        entries = (DELIVERY / "SHA256SUMS").read_text().splitlines()
        self.assertEqual(len(entries), 4)
        for line in entries:
            digest, filename = line.split()
            self.assertEqual(hashlib.sha256((DELIVERY / filename).read_bytes()).hexdigest(), digest)
        self.assertEqual(hashlib.sha256((DELIVERY / PAGING).read_bytes()).hexdigest(),
                         "b60073574d767acad27866d7727ea1f28c7ed77902984757d129c7a5ec7a640b")
        self.assertEqual(hashlib.sha256((DELIVERY / TRADE).read_bytes()).hexdigest(),
                         "2357304ca1be22e8132c02c6b6815f572596c09c1b17afc37e98adb54faa4fa4")
        self.assertEqual(hashlib.sha256((ROOT / "query-page.schema.json").read_bytes()).hexdigest(),
                         "988891918ed1abc6889832d1aa446a515d77e52ca8d5e9d359f566f63beee47c")

    def test_all_actual_bodies_and_budget(self):
        count = successes = 0
        for filename in (PAGING, TRADE):
            document = load(filename)
            self.assertTrue(document["synthetic_only"])
            self.assertEqual(len(document["cases"]), 12)
            for case in document["cases"]:
                with self.subTest(case=case["name"]):
                    bodies = [decode_json(raw) for raw in case["raw_bodies"]]
                    self.assertEqual(case["successful"], bodies[-1]["status"] == "completed")
                    successes += case["successful"]
                    count += len(bodies)
                    for body in bodies:
                        validate_page(body)
                    if case["successful"]:
                        for index, body in enumerate(bodies):
                            self.assertEqual(body["chunk"], {"index": index, "is_last": index == len(bodies) - 1})
                        limits = case.get("limits", {"page_bytes": 262144, "total_bytes": 8388608, "rows": 10000})
                        sizes = [len(raw.encode("utf-8")) for raw in case["raw_bodies"]]
                        self.assertLessEqual(max(sizes), limits["page_bytes"])
                        self.assertLessEqual(sum(sizes), limits["total_bytes"])
                        self.assertLessEqual(sum(len(b["payload"]["items"]) for b in bodies), limits["rows"])
                    else:
                        self.assertEqual(len(bodies), 1)
                        self.assertNotIn("chunk", bodies[0])
                        self.assertNotIn("items", bodies[0]["payload"])
        self.assertEqual((count, successes), (27, 8))

    def test_failure_reasons_preserve_raw_completion(self):
        failures = {"fill_first_pending": "persisted_fill_association_pending",
                    "empty_query_cannot_hide_pending": "persisted_fill_association_pending",
                    "duplicate_fill_rows_rejected": "duplicate_business_key",
                    "unknown_order_state_retained": "unsupported_order_state",
                    "same_fill_conflict": "persisted_fill_conflict",
                    "unknown_is_not_external": "order_association_pending",
                    "missing_id_raw_evidence_retained": "missing_source_field"}
        cases = {c["name"]: c for c in load(TRADE)["cases"]}
        for name, reason in failures.items():
            body = decode_json(cases[name]["raw_bodies"][0])
            self.assertEqual((body["status"], body["code"], body["message"]), ("failed", "QUERY_FAILED", reason))
            self.assertTrue(body["adapter_context"]["standard_validation"]["raw_completed"])
            evidence = body["adapter_context"]["query_evidence"]
            self.assertFalse(evidence["snapshot_applied"])
            self.assertEqual(evidence["sdk_return_code"], 0)
            self.assertIsNone(evidence["callback_error_id"])

    def test_capacity_observations_are_not_missing_raw_pages(self):
        # These six entries are OC measurements, not actual bodies we can replay.
        observations = {b["name"]: b for b in load(PAGING)["boundaries"]}
        self.assertEqual(len(observations), 6)
        self.assertEqual(observations["exact_single_page_bytes"]["actual_total_bytes"], 1254)
        self.assertEqual(observations["single_page_one_byte_over"]["page_bytes_limit"], 1253)
        self.assertEqual(observations["exact_total_bytes"]["actual_total_bytes"], 3760)
        self.assertEqual(observations["total_one_byte_over"]["total_bytes_limit"], 3759)
        self.assertEqual(observations["exact_256_pages"]["page_count"], 256)
        for name in ("single_page_one_byte_over", "total_one_byte_over", "would_need_257_pages"):
            self.assertFalse(observations[name]["successful"])
            self.assertEqual(observations[name]["first_status"], "failed")
            self.assertEqual(observations[name]["page_count"], 1)
