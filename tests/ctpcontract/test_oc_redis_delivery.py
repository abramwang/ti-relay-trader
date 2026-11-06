"""Inspect received evidence; never execute OC binaries or publish fixtures."""

import hashlib
import re
import unittest

from query_contract import ROOT, decode_json, validate_page


DELIVERY = ROOT.parent / "redis_review"
FINAL = (
    "shared_test_redis_recovery.final.v1.json",
    "private_redis_recovery.final.v1.json",
    "asan_private_redis_recovery.v1.json",
)
HEAD = "07598735ea3f8bdc3a26e754a9f042b5f0b0f688"
WINDOWS = {
    # step, calls, pending_before/after, durable_ack, outputs_before/after
    "before_intent": ("not_started", 0, 1, 1, False, 0, 0),
    "after_intent": ("proven_not_submitted", 0, 1, 1, False, 0, 0),
    "after_dispatch": ("outcome_unknown", 0, 1, 1, False, 0, 0),
    "after_sdk": ("outcome_unknown", 1, 1, 1, False, 0, 0),
    "after_result": ("restore_output", 1, 1, 0, True, 0, 1),
    "after_outbox": ("republish_same_output", 1, 1, 0, True, 0, 1),
    "after_publish": ("republish_same_output", 1, 1, 0, True, 1, 2),
    "after_confirm": ("ack_original", 1, 1, 0, True, 1, 1),
    "after_acked": ("already_complete", 1, 0, 0, True, 1, 1),
    "after_ack": ("ack_original", 1, 0, 0, False, 1, 1),
}
SAFETY = {
    "duplicate_body": "redis_duplicate_field", "invalid_json": "invalid_json",
    "wrong_account": "command_account_mismatch", "deleted_pending_body": "redis_pending_body_deleted",
    "missing_group": "redis_command_failed", "epoch_changed_before_read": "OC_EPOCH_CHANGED",
    "epoch_changed_before_publish": "OC_EPOCH_CHANGED", "deleted_output": "OC_OUTPUT_CHANGED",
    "deleted_input_before_ack": "OC_INPUT_CHANGED", "pending_owner_changed": "OC_PENDING_OWNER_CHANGED",
    "epoch_changed_before_ack": "OC_EPOCH_CHANGED", "server_run_id_mismatch": "OC_SOURCE_CHANGED",
    "journal_source_changed": "journal_transport_source_changed", "same_message_conflict": "journal_command_conflict",
    "pending_backpressure": "OC_BACKPRESSURE",
}


def load(name):
    return decode_json((DELIVERY / name).read_text())


class OCRedisDeliveryTest(unittest.TestCase):
    def test_manifest_and_versions(self):
        entries = (DELIVERY / "SHA256SUMS").read_text().splitlines()
        self.assertEqual(len(entries), 8)
        for entry in entries:
            digest, name = entry.split()
            self.assertEqual(hashlib.sha256((DELIVERY / name).read_bytes()).hexdigest(), digest)
        for name in FINAL:
            doc = load(name)
            self.assertEqual(doc["source_head"], HEAD)
            self.assertIs(doc["source_dirty"], False)
            self.assertIs(doc["synthetic_only"], True)
            self.assertIs(doc["cleanup_verified"], True)
            self.assertEqual(doc["sdk"], "fake; no broker connection")
            self.assertEqual(doc["redis_version"], "8.2.2" if name == FINAL[0] else "7.2.14")
            self.assertTrue(doc["collected_at"].endswith("+08:00"))
            self.assertRegex(doc["binary_sha256"], r"^[a-f0-9]{64}$")
            self.assertEqual((len(doc["crash_cases"]), len(doc["safety_cases"]), len(doc["extra_cases"])), (20, 15, 2))
        old = load("shared_test_redis_recovery.v1.json")
        self.assertEqual((len(old["crash_cases"]), len(old["safety_cases"])), (20, 14))
        self.assertFalse(old.get("extra_cases"))

    def check_outputs(self, outputs):
        ids = []
        bodies = []
        for output in outputs:
            self.assertEqual(hashlib.sha256(output["body"].encode()).hexdigest(), output["sha256"])
            self.assertRegex(output["stream_id"], r"^[0-9]+-[0-9]+$")
            ids.append(tuple(map(int, output["stream_id"].split("-"))))
            bodies.append(decode_json(output["body"]))
        self.assertEqual(ids, sorted(set(ids)))
        return bodies

    def test_crash_evidence_matrix_and_original_bytes(self):
        for name in FINAL:
            doc = load(name)
            seen = set()
            for case in doc["crash_cases"]:
                with self.subTest(file=name, action=case["action"], window=case["name"]):
                    seen.add((case["action"], case["name"]))
                    step, calls, before, after, ack, outputs_before, outputs_after = WINDOWS[case["name"]]
                    self.assertEqual(case["observed_step"], step)
                    self.assertEqual((case["sdk_calls_before"], case["sdk_calls_after"]), (calls, calls))
                    self.assertIs(case["sdk_is_fake"], True)
                    self.assertEqual((case["pending_before"], case["pending_after"]), (before, after))
                    self.assertIs(case["durable_ack"], ack)
                    outcome = "confirmed" if ack and case["name"] != "after_acked" else "not_attempted"
                    if case["name"] == "after_ack":
                        outcome = "uncertain_requires_reconciliation"
                    self.assertEqual(case["ack_outcome"], outcome)
                    self.assertEqual((len(case["outputs_before"]), len(case["outputs_after"])), (outputs_before, outputs_after))
                    self.check_outputs(case["outputs_before"])
                    bodies = self.check_outputs(case["outputs_after"])
                    self.assertEqual(case["outputs_after"][:outputs_before], case["outputs_before"])
                    command = decode_json(case["raw_command"])
                    self.assertEqual(command["action"], case["action"])
                    self.assertEqual(case["stream"], f'relay:test:v1:{doc["broker_id"]}:{command["payload"]["account_id"]}:cmd.trade')
                    for body in bodies:
                        self.assertEqual(body["origin_message_id"], command["message_id"])
                        self.assertEqual(body["action"], command["action"])
                        self.assertEqual(body["routing"]["account_id"], command["payload"]["account_id"])
                    if outputs_after:
                        self.assertEqual(len({o["body"] for o in case["outputs_after"]}), 1)
            self.assertEqual(seen, {(a, w) for a in ("order.submit", "order.cancel") for w in WINDOWS})

    def test_safety_evidence(self):
        for name in FINAL:
            cases = load(name)["safety_cases"]
            self.assertEqual({c["name"]: c["error"] for c in cases}, SAFETY)
            for case in cases:
                self.assertIs(case["pass"], True)
        # These entries record errors/pass, not complete before/after PEL evidence.

    def test_query_pages_and_alias_evidence(self):
        for name in FINAL:
            query, alias = load(name)["extra_cases"]
            self.assertEqual(query["name"], "query_second_publication_confirmation_lost")
            bodies = self.check_outputs(query["outputs_after"])
            self.assertEqual([b["chunk"]["index"] for b in bodies], [0, 1, 1, 2])
            for body in bodies:
                validate_page(body)
            self.assertEqual(query["outputs_before"], query["outputs_after"][:2])
            self.assertEqual(query["outputs_after"][1]["body"], query["outputs_after"][2]["body"])
            self.assertEqual(len({b["message_id"] for b in bodies}), 3)
            self.assertEqual((query["sdk_calls"], query["pending_after"], query["unique_pages"]), (1, 0, 3))
            self.assertIs(query["durable_ack"], True)
            self.assertEqual(alias["name"], "same_and_new_message_same_intent_cross_day")
            self.assertEqual((alias["delivery_count"], alias["sdk_calls"], alias["pending_after"]), (3, 1, 0))
            outputs = self.check_outputs(alias["outputs"])
            self.assertEqual(len(outputs), 1)
            self.assertEqual(outputs[0]["origin_message_id"], decode_json(alias["original_command"])["message_id"])
        # Alias input bodies/dates are not supplied; counters are not full intent proof.

    def test_supplied_ctest_logs(self):
        for filename, count in (("ctest_20261106.txt", 27), ("asan_ubsan_20261106.txt", 2)):
            log = (DELIVERY / filename).read_text()
            self.assertIn(f"100% tests passed, 0 tests failed out of {count}", log)
            self.assertEqual(len(re.findall(r"Test\s+#\d+:.*Passed", log)), count)


if __name__ == "__main__":
    unittest.main()
