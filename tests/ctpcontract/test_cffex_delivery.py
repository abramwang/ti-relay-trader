"""OC synthetic CFFEX samples only; no network, real accounts or publishing."""
import copy
import hashlib
from pathlib import Path
import unittest

from jsonschema import ValidationError
from command_contract import validate_command
from query_contract import decode_json, validate_page, validate_fill_identity

ROOT = Path(__file__).resolve().parents[2] / "testdata/oc_ctp_cffex_execution_review_20261119"
ROUTE = {"env": "test", "broker_id": "ctp_4900", "gateway_id": "300052141", "account_id": "300052141"}


class CFFEXDeliveryTests(unittest.TestCase):
    def test_original_outputs_and_independent_fill_identity(self):
        doc = decode_json((ROOT / "execution_examples.json").read_text())
        self.assertTrue(doc["synthetic_only"])
        self.assertFalse(doc["real_sdk_calls"] or doc["real_redis"])
        total, fills, queries = 0, 0, 0
        for case in doc["cases"]:
            commands = {}
            for entry in case["inputs"]:
                command = decode_json(entry["body"])
                if command["payload"].get("limit_price") == "not-a-price":
                    with self.assertRaises((ValueError, ValidationError)):
                        validate_command(command, routing=ROUTE, trading_day="20261117")
                else:
                    validate_command(command, routing=ROUTE, trading_day="20261117")
                key = command["message_id"]
                if key in commands:
                    self.assertEqual(commands[key], command)
                commands[key] = command
            for output in case["outputs"]:
                raw = output["body"].encode()
                self.assertEqual(len(raw), output["body_bytes"])
                self.assertEqual(hashlib.sha256(raw).hexdigest(), output["body_sha256"])
                total += 1
                body = decode_json(output["body"])
                self.assertEqual(body["routing"], ROUTE)
                original = commands[body["origin_message_id"]]
                for key in ("request_id", "correlation_id", "idempotency_key"):
                    self.assertEqual(body[key], original[key])
                rows = []
                if body.get("result_type") in {"order_page", "fill_page", "position_page"}:
                    validate_page(body)
                    queries += 1
                    if body["result_type"] == "fill_page":
                        rows = body["payload"].get("items", [])
                elif body.get("event_type") == "fill.event":
                    rows = [body["payload"]]
                for row in rows:
                    parts = [ROUTE[k] for k in ("env", "broker_id", "account_id", "gateway_id")]
                    parts += [row["trade_date"], row["exchange"]]
                    self.assertEqual(validate_fill_identity(row, parts), "v2")
                    self.assertEqual(row["exchange"], "CFFEX")
                    self.assertIsNone(row["fee"])
                    self.assertFalse(row["fee_complete"])
                    for field in ("trade_side", "order_sys_id", "trade_id"):
                        altered = copy.deepcopy(row)
                        if field == "trade_side":
                            altered[field] = "S" if row[field] == "B" else "B"
                        else:
                            altered["native_ids"][field] += "0"
                        with self.assertRaises(ValueError):
                            validate_fill_identity(altered, parts)
                    fills += 1
        self.assertEqual((total, fills, queries), (215, 32, 8))


if __name__ == "__main__":
    unittest.main()
