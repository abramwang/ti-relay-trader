import copy
import itertools
import json
import unittest

from jsonschema import ValidationError

from command_contract import QUERY_ACTIONS, validate_command
from query_contract import ROOT, decode_json, fixture_pages, validate_page
from query_recovery import recover_complete_query
from query_sequence import QuerySequence


class OCSignoffReviewTest(unittest.TestCase):
    def setUp(self):
        self.fixture = decode_json((ROOT / "query_edge_cases.json").read_text())
        self.recovery = self.fixture["recovery"]
        self.saved = [json.dumps(body, separators=(",", ":")) for body in self.recovery["complete_saved_bodies"]]

    def recover(self, saved=None, received=None, **proof):
        return recover_complete_query(
            self.recovery["original_context"], self.saved if saved is None else saved,
            self.saved if received is None else received,
            **dict({"context_verified": True, "durable_complete_output_verified": True}, **proof))

    def test_three_failure_samples_validate_without_fake_facts(self):
        for case in self.fixture["cases"]:
            body = case["body"]
            validate_page(body)
            self.assertNotIn("items", body["payload"])
            self.assertNotIn("chunk", body)
        login, queue, projection = [c["body"] for c in self.fixture["cases"]]
        self.assertNotIn("trade_date", login["payload"])
        self.assertIsNone(login["adapter_context"]["query_evidence"]["connection_generation"])
        self.assertIsNone(queue["adapter_context"]["query_evidence"]["ticket"])
        self.assertTrue(projection["adapter_context"]["standard_validation"]["raw_completed"])
        self.assertFalse(projection["adapter_context"]["query_evidence"]["snapshot_applied"])

    def test_unissued_failure_cannot_claim_sdk_ticket_or_success(self):
        body = self.fixture["cases"][0]["body"]
        for key, value in (("sdk_request_id", 1), ("sdk_return_code", 0), ("callback_error_id", 0),
                           ("ticket", "made-up-ticket"), ("connection_generation", 0),
                           ("failure_stage", "queue_expired"), ("snapshot_applied", True)):
            bad = copy.deepcopy(body)
            bad["adapter_context"]["query_evidence"][key] = value
            with self.assertRaises(ValidationError):
                validate_page(bad)
        for name in ("asset", "positions", "orders", "fills"):
            for key in ("ticket", "connection_generation"):
                bad = copy.deepcopy(fixture_pages()[name])
                bad["adapter_context"]["query_evidence"][key] = None
                with self.assertRaises(ValidationError):
                    validate_page(bad)

    def test_unknown_day_omitted_not_null_empty_or_zero(self):
        body = copy.deepcopy(self.fixture["cases"][0]["body"])
        for day in (None, "", "00000000", 0):
            body["payload"]["trade_date"] = day
            with self.assertRaises(ValidationError):
                validate_page(body)

    def test_raw_position_enums_not_limited_to_new_order_speculation(self):
        for direction, hedge, date in itertools.product(("1", "2", "3"), ("1", "2", "3", "5", "6", "7"), ("1", "2")):
            body = copy.deepcopy(fixture_pages()["positions"])
            body["payload"]["items"][0]["futures"].update(posi_direction=direction, hedge_flag=hedge, position_date=date)
            validate_page(body)
        for key, value in (("posi_direction", "4"), ("hedge_flag", "4"), ("position_date", "3"), ("hedge_flag", 1)):
            body = copy.deepcopy(fixture_pages()["positions"])
            body["payload"]["items"][0]["futures"][key] = value
            with self.assertRaises(ValidationError):
                validate_page(body)

    def test_four_query_commands_have_only_three_payload_fields(self):
        messages = decode_json((ROOT / "messages.json").read_text())
        base = next(c["body"] for c in messages["cases"] if c["name"] == "submit")
        for action in QUERY_ACTIONS:
            body = copy.deepcopy(base)
            body["action"] = action
            body["payload"] = {k: base["payload"][k] for k in ("account_id", "product_type", "schema_version")}
            validate_command(body, routing=self.recovery["original_context"]["routing"])
            for key in ("trade_date", "symbol", "exchange", "cursor", "limit"):
                bad = copy.deepcopy(body)
                bad["payload"][key] = "not-a-query-filter"
                with self.assertRaises(ValidationError):
                    validate_command(bad, routing=self.recovery["original_context"]["routing"])

    def test_complete_old_output_replay_is_historical_not_current_readiness(self):
        result = self.recover(received=[self.saved[1], self.saved[0], self.saved[1]])
        self.assertEqual(len(result["items"]), 2)
        self.assertEqual(result["original_context"]["counter_session_id"], "ctpproc-fixture-1")
        self.assertFalse(result["current_readiness"])
        self.assertFalse(result["replace_current_snapshot"])
        self.assertEqual(json.loads(self.saved[0])["produced_at"], "2026-11-03T21:03:00+08:00")

    def test_saved_partial_or_incomplete_delivery_is_not_recovered(self):
        for saved, received in ((self.saved[:1], self.saved[:1]), (self.saved[1:], self.saved[1:]),
                                ([], []), (self.saved, self.saved[:1]), (self.saved + self.saved[:1], self.saved)):
            with self.assertRaises(ValueError):
                self.recover(saved, received)

    def test_recovery_proof_is_explicit_and_not_truthiness(self):
        for key in ("context_verified", "durable_complete_output_verified"):
            for value in (False, None, 1, "true"):
                with self.assertRaisesRegex(ValueError, "verified original context"):
                    self.recover(**{key: value})

    def test_old_partial_does_not_join_new_query(self):
        seq = QuerySequence(self.recovery["new_context"], now=0, deadline=10)
        with self.assertRaisesRegex(ValueError, "foreign query"):
            seq.accept(self.saved[0], now=1)
        self.assertEqual(seq.state, "collecting")
        for key in ("ticket", "counter_session_id", "connection_generation", "sdk_request_id"):
            original = copy.deepcopy(self.recovery["original_context"])
            original[key] = self.recovery["new_context"][key]
            with self.assertRaises(ValueError):
                recover_complete_query(original, self.saved, self.saved, context_verified=True,
                                       durable_complete_output_verified=True)

    def test_recovery_does_not_regenerate_body_or_timestamps(self):
        for mutation in (lambda text: text + " ",
                         lambda text: text.replace("ctpproc-fixture-1", "ctpproc-fixture-2"),
                         lambda text: text.replace("21:03:00", "21:03:01")):
            with self.assertRaisesRegex(ValueError, "differs from original"):
                self.recover(received=[mutation(self.saved[0]), self.saved[1]])


if __name__ == "__main__":
    unittest.main()
