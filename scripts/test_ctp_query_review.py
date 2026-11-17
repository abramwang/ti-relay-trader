import copy
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
import unittest


spec = importlib.util.spec_from_file_location("builder", Path(__file__).with_name("build-ctp-query-review.py"))
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class QueryReviewTests(unittest.TestCase):
    def setUp(self):
        self.options = SimpleNamespace(command_id=1, batch_id="batch", review_id="review", timeout_ms=60000,
                                       broker="ctp_fixture", gateway="fixture-gw", account="990000000001",
                                       native_broker="9999", user="FIXTURE_USER", investor="FIXTURE_INV")
        self.target = dict(source_id="fixture-source", server_run_id="a" * 40, epoch="fixture-epoch",
                           group="fixture-group", counter_session_id="fixture-session", trade_date="20261117",
                           connection_generation=1)
        self.command = builder.canonical(dict(protocol="relay.stream.v1", message_type="command", message_id="msg",
                                             request_id="req", action="account.asset.query", payload=dict(
                                                 account_id=self.options.account, product_type="futures", schema_version="ctp.f0.draft.2")))
        self.scope = ["test", "ctp_fixture", "990000000001", "fixture-gw", "9999", "FIXTURE_USER", "FIXTURE_INV"]
        self.entries = [
            dict(kind="command_received", body=self.command.decode(), stream="relay:test:v1:ctp_fixture:fixture-gw:cmd.query",
                 group="fixture-group"),
            dict(kind="query_result", action="account.asset.query", request_id="req", origin_message_id="msg", ticket=1,
                 sdk_request_id=3, counter_session_id="fixture-session", connection_generation=1, trade_date="20261117",
                 raw_application="missing_biz_type", sdk_return_code=0,
                 raw=dict(sdk_request_id=3, state="completed", last_seen=True, callback_error=None)),
        ]

    def audit(self, entries=None):
        previous, lines = "", []
        for sequence, entry in enumerate(entries or self.entries, 1):
            record = dict(sequence=sequence, previous=previous, scope=json.dumps(self.scope),
                          counter_session_id="fixture-session", entry=entry)
            previous = builder.digest(builder.canonical(record))
            lines.append(builder.canonical(dict(record=record, sha256=previous)) + b"\n")
        raw = b"".join(lines)
        self.options.audit_sha256 = builder.digest(raw)
        return raw

    def build(self, audit=None):
        return builder.build(self.audit() if audit is None else audit, self.command, self.target, self.options)

    def test_failed_result_preserved_and_evidence_original(self):
        raw = self.audit()
        review, evidence, report = self.build(raw)
        self.assertEqual(evidence, raw)
        self.assertEqual(review["evidence_sha256"], builder.digest(raw))
        self.assertEqual(review["query_context"]["SDKRequestID"], 3)
        self.assertEqual(report["raw_application"], "missing_biz_type")
        self.assertIsNone(report["callback_error"])
        self.assertFalse(report["registered_or_published"])

    def test_result_context_changes_rejected(self):
        for key, value in (("counter_session_id", "old"), ("connection_generation", 2), ("connection_generation", True), ("trade_date", "20261118"),
                           ("action", "fill.list.query"), ("request_id", "another"), ("sdk_request_id", 4),
                           ("ticket", True), ("ticket", 0)):
            with self.subTest(key=key, value=value):
                entries = copy.deepcopy(self.entries)
                entries[1][key] = value
                with self.assertRaises(ValueError):
                    self.build(self.audit(entries))

    def test_foreign_scope_rejected(self):
        self.scope[2] = "another"
        with self.assertRaises(ValueError):
            self.build()

    def test_reply_not_command(self):
        value = builder.decode(self.command)
        value["message_type"] = "reply"
        self.command = builder.canonical(value)
        with self.assertRaises(ValueError):
            self.build()

    def test_duplicate_result_rejected(self):
        self.entries.append(copy.deepcopy(self.entries[-1]))
        with self.assertRaises(ValueError):
            self.build()

    def test_planned_request_not_execution(self):
        self.entries[-1]["kind"] = "query_request"
        with self.assertRaises(ValueError):
            self.build()

    def test_request_and_result_must_agree(self):
        request = copy.deepcopy(self.entries[-1])
        request["kind"] = "query_request"
        self.entries.insert(1, request)
        self.build()
        request["ticket"] = 2
        with self.assertRaises(ValueError):
            self.build()

    def test_chain_truncation_mutation_rejected(self):
        raw = self.audit()
        for mutation in (raw.splitlines(keepends=True)[1], raw[:-1], raw.replace(b'"sequence":1', b'"sequence":2')):
            self.options.audit_sha256 = builder.digest(mutation)
            with self.subTest(mutation=mutation[:20]), self.assertRaises(ValueError):
                self.build(mutation)
        self.options.audit_sha256 = "0" * 64
        with self.assertRaises(ValueError):
            self.build(raw)

    def test_duplicate_json_and_nonfinite_rejected(self):
        for raw in (b'{"x":1,"x":2}', b'{"x":NaN}', b'{"x":Infinity}'):
            with self.assertRaises(ValueError):
                builder.decode(raw)

    def test_transport_and_command_bytes_must_match(self):
        for key, value in (("body", self.command.decode() + " "), ("stream", "another"), ("group", "other")):
            entries = copy.deepcopy(self.entries)
            entries[0][key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.build(self.audit(entries))

    def test_wrong_order_rejected(self):
        with self.assertRaises(ValueError):
            self.build(self.audit(self.entries[::-1]))

    def test_oversized_evidence_rejected_without_truncation(self):
        self.entries[-1]["raw"]["sample"] = "a" * 262144
        with self.assertRaises(ValueError):
            self.build()


if __name__ == "__main__":
    unittest.main()
