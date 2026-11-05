"""Independent Relay reproduction of OC's synthetic intent candidate.

Derive fields from raw command + trusted fixture scope, never from OC's expected
field list. This is not a command publisher or durable idempotency implementation.
"""

import copy
import hashlib
import json
from pathlib import Path
import unittest

from command_contract import QUERY_ACTIONS, canonical_price, validate_command
from query_contract import decode_json


FIXTURE = Path(__file__).resolve().parents[2] / "testdata/fixtures/ctp_intent_vectors.v1.json"
ROUTE = ("env", "broker_id", "account_id", "gateway_id")
NATIVE = ("native_broker_id", "investor_id", "user_id")
ORDER = ("account_id", "gateway_order_id", "client_order_id", "trade_date", "symbol",
         "exchange", "business_type", "trade_side", "offset_type", "hedge_type",
         "order_qty", "limit_price")


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def frame(prefix, values):
    return prefix.encode("ascii") + b"".join(
        str(len(value.encode("utf-8"))).encode("ascii") + b":" + value.encode("utf-8")
        for value in values)


def reproduce(case):
    scope = case["scope"]
    body = decode_json(case["raw_body"])
    action = body["action"]
    payload = validate_command(body, routing={k: scope[k] for k in ROUTE}, trading_day="20261105")
    suffix = "cmd.query" if action in QUERY_ACTIONS else "cmd.trade"
    if case["stream_suffix"] != suffix:
        raise ValueError("trusted Stream mismatch")
    fields = [{"name": key, "value": scope[key]} for key in (*ROUTE, *NATIVE)]
    for key, value in (("protocol", body["protocol"]), ("product_type", payload["product_type"]),
                       ("schema_version", payload["schema_version"]), ("action", action)):
        fields.append({"name": key, "value": value})
    if action in ("order.submit", "order.batch.submit"):
        orders = [payload] if action == "order.submit" else payload["orders"]
        fields.append({"name": "order_count", "value": str(len(orders))})
        for index, order in enumerate(orders):
            for key in ORDER:
                value = order[key]
                if key == "limit_price":
                    value = canonical_price(value)
                elif key == "order_qty":
                    value = str(value)
                fields.append({"name": f"orders[{index}].{key}", "value": value})
    elif action == "order.cancel":
        fields.extend({"name": k, "value": payload[k]} for k in ("gateway_order_id", "trade_date", "cancel_id"))
    elif action not in QUERY_ACTIONS:
        raise ValueError("unsupported action")
    outputs = {
        "intent": frame("ctp.intent.v1/", [f["value"] for f in fields]),
        "idempotency_lookup": frame("ctp.idempotency.v1/", [scope[k] for k in ROUTE] + [body["idempotency_key"]]),
        "message_lookup": frame("ctp.message.v1/", [scope[k] for k in ROUTE] + [body["message_id"]]),
        "raw_body": case["raw_body"].encode("utf-8"),
    }
    return fields, outputs


class OCCommandDeliveryTest(unittest.TestCase):
    def setUp(self):
        self.raw = FIXTURE.read_bytes()
        self.document = decode_json(self.raw.decode("utf-8"))
        self.cases = {c["name"]: c for c in self.document["cases"]}

    def test_received_file_fingerprint_and_scope(self):
        self.assertEqual(digest(self.raw), "cd2e9f38e2d4216a17cfa0802bcff3093256dbd5920ee39fe5e9f5cddf0d69b4")
        self.assertTrue(self.document["synthetic_only"])
        self.assertTrue(self.document["not_a_durable_idempotency_implementation"])
        self.assertEqual(len(self.cases), 12)

    def test_all_fields_bytes_and_four_hashes(self):
        for name, case in self.cases.items():
            with self.subTest(case=name):
                fields, outputs = reproduce(case)
                self.assertEqual(fields, case["fields"])
                for key, raw in outputs.items():
                    self.assertEqual(digest(raw), case[key + "_sha256"])
                    if key != "raw_body":
                        self.assertEqual(raw, case[key + "_bytes"].encode("utf-8"))

    def test_price_transport_native_binding_and_batch_order(self):
        values = {name: reproduce(c)[1] for name, c in self.cases.items()}
        base = values["submit"]
        for name in ("equal_price_trailing_zero", "same_intent_new_transport"):
            self.assertEqual(base["intent"], values[name]["intent"])
            self.assertNotEqual(base["raw_body"], values[name]["raw_body"])
        changed = values["same_lookup_changed_native_identity"]
        self.assertEqual(base["idempotency_lookup"], changed["idempotency_lookup"])
        self.assertEqual(base["raw_body"], changed["raw_body"])
        self.assertNotEqual(base["intent"], changed["intent"])
        self.assertNotEqual(values["ordered_batch"]["intent"], values["reversed_batch"]["intent"])

    def test_utf8_lengths_are_not_char_counts_or_json_escaping(self):
        case = self.cases["utf8_byte_length_and_delimiters"]
        fields, outputs = reproduce(case)
        self.assertTrue(any(len(f["value"].encode("utf-8")) != len(f["value"]) for f in fields))
        wrong = "ctp.intent.v1/" + "".join(f'{len(f["value"])}:{f["value"]}' for f in fields)
        self.assertNotEqual(wrong.encode("utf-8"), outputs["intent"])
        self.assertNotEqual(json.dumps([f["value"] for f in fields]).encode(), outputs["intent"])

    def test_query_lookup_not_partitioned_by_action_or_current_day(self):
        original = reproduce(self.cases["submit"])[1]
        for action in QUERY_ACTIONS:
            fields, encoded = reproduce(self.cases[action])
            self.assertEqual(len(fields), 11)
            self.assertEqual(encoded["idempotency_lookup"], original["idempotency_lookup"])
            self.assertEqual(encoded["message_lookup"], original["message_lookup"])
            changed = copy.deepcopy(self.cases[action])
            changed["scope"].update(current_day="20991231", counter_session_id="new-process")
            self.assertEqual(reproduce(changed)[1], encoded)
