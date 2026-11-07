"""Review OC's supplied evidence without executing its programs or Redis actions."""

import hashlib
import re
import unittest

from query_contract import ROOT, decode_json, validate_page
from test_oc_command_delivery import reproduce
from test_oc_redis_delivery import WINDOWS, SAFETY


DELIVERY = ROOT.parent / "runtime_review"
FILES = ("shared_redis_20261107.json", "sanitizer_private_20261107.json")
HEAD = "db6518608760cf3fc282d0ffb386786c61bc614d"
FLAGS = ("redis_ready", "broker_ready", "settlement_confirmed", "order_snapshot_ready",
         "accepting_trade_commands", "accepting_cancel_commands")


def load(name):
    return decode_json((DELIVERY / name).read_text())


def prefix(route):
    return f'relay:{route["env"]}:v1:{route["broker_id"]}:{route["gateway_id"]}:'


class OCRuntimeDeliveryTest(unittest.TestCase):
    def output(self, output, role="reply"):
        raw = output["body"].encode()
        if "sha256" in output:
            self.assertEqual(hashlib.sha256(raw).hexdigest(), output["sha256"])
        body = decode_json(output["body"])
        self.assertRegex(output["stream_id"], r"^[0-9]+-[0-9]+$")
        self.assertNotEqual(body["routing"]["account_id"], body["routing"]["gateway_id"])
        self.assertEqual(output["stream"], prefix(body["routing"]) + role)
        return body

    def heartbeat(self, output):
        body = self.output(output, "hb")
        p, a = body["payload"], body["adapter_context"]
        self.assertEqual(body["message_type"], "heartbeat")
        self.assertEqual(a["runtime_mode"], "test_transport_recovery_only")
        self.assertIsNone(a["connection_generation"])
        self.assertIsNone(p["trade_date"])
        self.assertEqual(p["state"], "DEGRADED")
        self.assertIn(p["state_text"], ("broker_disabled", "recovery_blocked", "stopping"))
        for key in (*FLAGS, "state", "state_text", "counter_session_id"):
            self.assertEqual(body[key], p[key])
        for flag in FLAGS[1:]:
            self.assertIs(p[flag], False)
        self.assertTrue(body["produced_at"].endswith("+08:00"))
        return body

    def test_manifest_versions_and_logs(self):
        lines = (DELIVERY / "SHA256SUMS").read_text().splitlines()
        self.assertEqual(len(lines), 8)
        for line in lines:
            digest, name = line.split()
            self.assertEqual(hashlib.sha256((DELIVERY / name).read_bytes()).hexdigest(), digest)
        for name in (*FILES, "main_cli_20261107.json"):
            doc = load(name)
            self.assertEqual(doc["source_head"], HEAD)
            self.assertIs(doc["source_dirty"], False)
            self.assertIs(doc["synthetic_only"], True)
            self.assertIs(doc["cleanup_verified"], True)
            self.assertRegex(doc["binary_sha256"], r"^[a-f0-9]{64}$")
            self.assertEqual(doc["redis_version"], "7.2.14" if name == FILES[1] else "8.2.2")
        for name, count in (("ctest_20261107.txt", 28), ("asan_ubsan_20261107.txt", 1)):
            log = (DELIVERY / name).read_text()
            self.assertIn(f"100% tests passed, 0 tests failed out of {count}", log)
            self.assertEqual(len(re.findall(r"Test\s+#\d+:.*Passed", log)), count)

    def test_crash_recovery_uses_gateway_physical_keys(self):
        for name in FILES:
            doc = load(name)
            self.assertEqual(doc["sdk"], "fake; no broker connection")
            cases = doc["crash_cases"]
            self.assertEqual(len(cases), 20)
            self.assertEqual({(c["action"], c["name"]) for c in cases},
                             {(a, w) for a in ("order.submit", "order.cancel") for w in WINDOWS})
            for c in cases:
                step, calls, before, after, ack, out_before, out_after = WINDOWS[c["name"]]
                self.assertEqual(c["observed_step"], step)
                self.assertEqual((c["sdk_calls_before"], c["sdk_calls_after"]), (calls, calls))
                self.assertEqual((c["pending_before"], c["pending_after"]), (before, after))
                self.assertIs(c["durable_ack"], ack)
                self.assertEqual((len(c["outputs_before"]), len(c["outputs_after"])), (out_before, out_after))
                command = decode_json(c["raw_command"])
                self.assertEqual(command["action"], c["action"])
                if c["name"] == "after_ack":
                    self.assertEqual(c["ack_outcome"], "uncertain_requires_reconciliation")
                for output in c["outputs_after"]:
                    body = self.output(output)
                    self.assertEqual(c["stream"], prefix(body["routing"]) + "cmd.trade")
                    self.assertEqual(body["origin_message_id"], command["message_id"])
                    self.assertEqual(body["routing"]["account_id"], command["payload"]["account_id"])
                self.assertEqual(c["outputs_after"][:out_before], c["outputs_before"])
                self.assertLessEqual(len({o["body"] for o in c["outputs_after"]}), 1)

    def test_safety_traces_preserve_unknown_and_no_extra_sdk(self):
        for name in FILES:
            cases = load(name)["safety_cases"]
            self.assertEqual({c["name"]: c["error"] for c in cases}, SAFETY)
            for c in cases:
                self.assertIs(c["pass"], True)
                self.assertTrue(c["raw_body"])
                journal = c["journal_after"]
                binding = decode_json(journal["transport_binding"])
                self.assertEqual(binding["version"], "ctp.redis.test.transport.v2")
                self.assertEqual(binding["stream_routing"], "gateway_id")
                self.assertEqual(c["stream"], prefix(journal["scope"]) + "cmd.trade")
                trace = c["trace"]
                self.assertEqual(trace[0]["stage"], "before_read")
                self.assertEqual(trace[-1]["stage"], "after_rejected_operation")
                self.assertTrue(all(not p["durable_ack"] for p in trace))
                calls = [p["sdk_calls"] for p in trace]
                self.assertEqual(calls, sorted(calls))
                self.assertLessEqual(max(calls), 1)
                if any(p["stage"] == "after_synthetic_sdk" for p in trace):
                    self.assertEqual(calls[-1], 1)
                else:
                    self.assertEqual(calls[-1], 0)
                if c["name"] == "missing_group":
                    self.assertIsNone(trace[-1]["pending"])
                    self.assertEqual(trace[-1]["pending_error"], "group_unavailable")

    def test_runtime_cases_never_resubmit_or_resolve_ack_gap(self):
        expected = {"empty", "unknown_pending", "intent_only", "dispatch_unknown", "saved_result",
                    "ack_gap", "other_consumer", "duplicate_body", "missing_input", "missing_output", "mismatched_body"}
        for name in FILES:
            cases = load(name)["runtime_cases"]
            self.assertEqual(len(cases), 11)
            self.assertEqual({c["name"] for c in cases}, expected)
            for c in cases:
                clear = c["name"] in ("empty", "saved_result")
                self.assertIs(c["recovery_clear"], clear)
                self.assertEqual(c["sdk_calls_before"], c["sdk_calls_after"])
                h = self.heartbeat(c["heartbeat"])
                self.assertIs(h["adapter_context"]["transport_recovery_clear"], clear)
                self.assertEqual(h["adapter_context"]["recovery_issue_count"], len(c["errors"]))
                if c["name"] == "ack_gap":
                    self.assertEqual((c["pending_before"], c["pending_after"]), (0, 0))
                    self.assertIn("OC_ACK_UNCERTAIN", [e["code"] for e in c["errors"]])
                for output in c["outputs"]:
                    self.output(output)

    def test_alias_original_intent_and_query_pages(self):
        for name in FILES:
            query, alias = load(name)["extra_cases"]
            bodies = [self.output(o) for o in query["outputs_after"]]
            self.assertEqual([b["chunk"]["index"] for b in bodies], [0, 1, 1, 2])
            for body in bodies:
                validate_page(body)
            self.assertEqual(query["outputs_after"][1]["body"], query["outputs_after"][2]["body"])
            self.assertEqual(query["outputs_before"], query["outputs_after"][:2])
            self.assertIs(query["durable_ack"], True)
            self.assertEqual(query["pending_after"], 0)
            original = self.output(alias["outputs"][0])
            scope = dict(original["routing"], native_broker_id="9999", investor_id="FIXTURE_INV", user_id="FIXTURE_USER")
            deliveries = alias["deliveries"]
            self.assertEqual([d["confirmed_day"] for d in deliveries], ["20261105", "20261106", "20261106"])
            self.assertEqual(len({d["stream_id"] for d in deliveries}), 3)
            for d in deliveries:
                self.assertEqual(d["stream"], prefix(scope) + "cmd.trade")
                _, encoded = reproduce(dict(scope=scope, raw_body=d["raw_body"], stream_suffix="cmd.trade"))
                self.assertEqual(encoded["intent"].decode(), d["intent_bytes"])
                self.assertEqual(hashlib.sha256(encoded["intent"]).hexdigest(), d["intent_sha256"])
                self.assertEqual(hashlib.sha256(encoded["idempotency_lookup"]).hexdigest(), d["lookup_sha256"])
                self.assertEqual(d["command_key"], d["lookup_sha256"])
                self.assertEqual((d["sdk_calls"], d["pending_after"]), (1, 0))
                self.assertIs(d["durable_ack"], True)
            self.assertEqual(len({d["intent_bytes"] for d in deliveries}), 1)
            self.assertEqual(deliveries[0]["raw_body"], deliveries[1]["raw_body"])
            self.assertNotEqual(deliveries[1]["raw_body"], deliveries[2]["raw_body"])
            self.assertEqual(original["origin_message_id"], decode_json(deliveries[0]["raw_body"])["message_id"])

    def test_main_process_and_selected_heartbeats(self):
        main = load("main_cli_20261107.json")
        self.assertIs(main["broker_login"], False)
        self.assertIs(main["partial_audit_restart_refused"], True)
        self.assertEqual([r["scenario"] for r in main["runs"]], ["fresh", "restart", "unregistered_pending"])
        self.assertEqual([r["return_code"] for r in main["runs"]], [0, 0, 4])
        self.assertEqual(len({r["session"] for r in main["runs"]}), 3)
        originals = {}
        for run in main["runs"]:
            for output in run["heartbeats"]:
                body = self.heartbeat(output)
                self.assertEqual(body["counter_session_id"], run["session"])
                originals[(output["stream"], output["stream_id"])] = output["body"]
        for example in load("heartbeat_examples.v1.json")["examples"]:
            self.heartbeat(example)
            self.assertEqual(example["body"], originals[(example["stream"], example["stream_id"])])


if __name__ == "__main__":
    unittest.main()
