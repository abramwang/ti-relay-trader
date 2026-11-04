import copy
import json
import unittest

from jsonschema import ValidationError

from query_contract import decode_json, fixture_pages, validate_page


class QueryContractTest(unittest.TestCase):
    def setUp(self):
        self.pages = fixture_pages()

    def reject(self, body):
        with self.assertRaises((ValueError, ValidationError)):
            validate_page(body)

    def test_five_baselines_and_input_immutability(self):
        self.assertEqual(set(self.pages), {"asset", "positions", "orders", "fills", "queue_expired"})
        before = copy.deepcopy(self.pages)
        for body in self.pages.values():
            validate_page(body)
        self.assertEqual(self.pages, before)

    def test_missing_required_envelope_fields(self):
        for field in ("protocol", "message_type", "message_id", "origin_message_id", "routing", "produced_at", "status", "code", "action", "result_type", "payload", "adapter_context"):
            with self.subTest(field=field):
                body = copy.deepcopy(self.pages["asset"])
                del body[field]
                self.reject(body)

    def test_unknown_or_stock_fields_rejected(self):
        for path, field in (((), "surprise"), (("payload",), "account"), (("payload", "items", 0), "net_asset")):
            body = copy.deepcopy(self.pages["asset"])
            obj = body
            for part in path:
                obj = obj[part]
            obj[field] = 0
            self.reject(body)

    def test_account_and_day_mismatch(self):
        for name in ("asset", "positions", "orders", "fills"):
            body = copy.deepcopy(self.pages[name])
            body["payload"]["items"][0]["account_id"] = "990000000002"
            self.reject(body)
        body = copy.deepcopy(self.pages["asset"])
        body["payload"]["items"][0]["futures"]["scope"]["trading_day"] = "20261103"
        self.reject(body)
        for name in ("orders", "fills"):
            body = copy.deepcopy(self.pages[name])
            body["payload"]["items"][0]["trade_date"] = "20261103"
            self.reject(body)

    def test_strict_numbers(self):
        for value in (True, 1.0, "1", None, -1, 2147483648):
            body = copy.deepcopy(self.pages["positions"])
            body["payload"]["items"][0]["futures"]["position"] = value
            self.reject(body)
        # Text 1e0 and 1.0 retain decimal-token provenance through decode_json.
        for token in ("1e0", "1.0", "true", "null"):
            text = json.dumps(self.pages["positions"]).replace('"position": 1', '"position": ' + token)
            self.reject(decode_json(text))
        for key in ("connection_generation", "sdk_request_id", "sdk_return_code", "callback_error_id"):
            for value in (True, 1.0, "1"):
                body = copy.deepcopy(self.pages["asset"])
                body["adapter_context"]["query_evidence"][key] = value
                self.reject(body)

    def test_money_is_unrounded_text(self):
        for name, field in (("asset", "balance"), ("positions", "position_cost")):
            for value in (1.2, 0, None, "NaN", "1e2", "+1", "01", " 1", "1\n", "Infinity"):
                body = copy.deepcopy(self.pages[name])
                body["payload"]["items"][0]["futures"][field] = value
                self.reject(body)
            body = copy.deepcopy(self.pages[name])
            value = "-0.00000000000000001"
            body["payload"]["items"][0]["futures"][field] = value
            validate_page(body)
            self.assertEqual(body["payload"]["items"][0]["futures"][field], value)

    def test_empty_results_and_failed_results(self):
        for name in ("positions", "orders", "fills"):
            body = copy.deepcopy(self.pages[name])
            body["payload"]["items"] = []
            validate_page(body)
        body = copy.deepcopy(self.pages["asset"])
        body["payload"]["items"] = []
        self.reject(body)
        for field, value in (("chunk", {"index": 0, "is_last": True}), ("code", "OK")):
            body = copy.deepcopy(self.pages["queue_expired"])
            body[field] = value
            self.reject(body)
        body = copy.deepcopy(self.pages["queue_expired"])
        body["payload"]["items"] = []
        self.reject(body)
        for key in ("sdk_request_id", "sdk_return_code", "callback_error_id"):
            body = copy.deepcopy(self.pages["queue_expired"])
            body["adapter_context"]["query_evidence"][key] = 1
            self.reject(body)

    def test_page_status_and_action_pair(self):
        for name in ("asset", "positions", "orders", "fills"):
            body = copy.deepcopy(self.pages[name])
            body["status"] = "partial"
            body["chunk"]["is_last"] = False
            validate_page(body)
            body["chunk"]["is_last"] = True
            self.reject(body)
        body = copy.deepcopy(self.pages["asset"])
        body["result_type"] = "position_page"
        self.reject(body)
        for value in (False, None, 1):
            body = copy.deepcopy(self.pages["asset"])
            body["adapter_context"]["query_evidence"]["snapshot_applied"] = value
            self.reject(body)

    def test_dates_and_unknown_source_time(self):
        for value in ("20260230", "2026-11-04", 20261104):
            body = copy.deepcopy(self.pages["asset"])
            body["payload"]["trade_date"] = value
            self.reject(body)
        for value in ("2026-11-03T13:03:00Z", "2026-11-03T21:03:00", "2026-02-30T21:03:00+08:00"):
            body = copy.deepcopy(self.pages["asset"])
            body["produced_at"] = value
            self.reject(body)
        for name, field in (("orders", "submitted_at"), ("fills", "matched_at")):
            body = copy.deepcopy(self.pages[name])
            body["payload"]["items"][0][field] = None
            validate_page(body)
            self.assertIsNone(body["payload"]["items"][0][field])

    def test_native_id_and_fee(self):
        for field, value in (("trade_id", 5678), ("order_sys_id", ""), ("sdk_request_id", 14)):
            body = copy.deepcopy(self.pages["fills"])
            body["payload"]["items"][0]["native_ids"][field] = value
            self.reject(body)
        for key, value in (("fill_id", "ctp-fill-v1-" + "0" * 64), ("fee_complete", True), ("price", "0"), ("price", "18446744073709551616")):
            body = copy.deepcopy(self.pages["fills"])
            body["payload"]["items"][0][key] = value
            self.reject(body)

    def test_position_dimensions_and_duplicate_rows(self):
        for value in (None, 0):
            body = copy.deepcopy(self.pages["positions"])
            body["payload"]["items"][0]["futures"]["invest_unit_id"] = value
            self.reject(body)
        body = copy.deepcopy(self.pages["positions"])
        opposite = copy.deepcopy(body["payload"]["items"][0])
        opposite["futures"]["posi_direction"] = "3"
        body["payload"]["items"].append(opposite)
        validate_page(body)
        for name in ("asset", "positions", "orders", "fills"):
            body = copy.deepcopy(self.pages[name])
            body["payload"]["items"].append(copy.deepcopy(body["payload"]["items"][0]))
            self.reject(body)

    def test_order_quantities_and_historical_session(self):
        body = copy.deepcopy(self.pages["orders"])
        row = body["payload"]["items"][0]
        row["gateway_status"] = "filled"
        self.reject(body)
        row.update(cum_filled_qty=1, leaves_qty=0, counter_session_id="ctpproc-older")
        validate_page(body)
        row["gateway_status"] = "cancelled"
        row.update(cum_filled_qty=0, leaves_qty=1)
        validate_page(body)
        row["leaves_qty"] = 2
        self.reject(body)

    def test_trusted_request_context(self):
        body = self.pages["asset"]
        evidence = body["adapter_context"]["query_evidence"]
        expected = {key: body[key] for key in ("routing", "action", "origin_message_id")}
        expected.update({key: evidence[key] for key in ("ticket", "counter_session_id", "connection_generation", "sdk_request_id")})
        expected["trade_date"] = body["payload"]["trade_date"]
        validate_page(body, expected=expected)
        for key in expected:
            wrong = copy.deepcopy(expected)
            wrong[key] = None
            with self.assertRaises(ValueError):
                validate_page(body, expected=wrong)

    def test_bad_json(self):
        for text in ('{"a":1,"a":2}', '{"a":NaN}', '{"a":Infinity}', '{} {}'):
            with self.assertRaises(ValueError):
                decode_json(text)

    def test_no_trailing_controls_or_trimmed_native_ids(self):
        for field in ("symbol", "exchange", "gateway_order_id"):
            body = copy.deepcopy(self.pages["orders"])
            body["payload"]["items"][0][field] += "\n"
            self.reject(body)
        for value in ("    00001234", "00001234\n", "00001234 ", "00001234\u0000"):
            body = copy.deepcopy(self.pages["orders"])
            body["payload"]["items"][0]["native_ids"]["order_sys_id"] = value
            self.reject(body)

    def test_multiple_fund_scopes_are_not_summed(self):
        body = copy.deepcopy(self.pages["asset"])
        second = copy.deepcopy(body["payload"]["items"][0])
        second["futures"]["scope"]["currency_id"] = "USD"
        second["futures"]["balance"] = "27.345678901234567"
        body["payload"]["items"].append(second)
        before = copy.deepcopy(body)
        validate_page(body)
        self.assertEqual(body, before)

    def test_external_order_digest_is_scope_bound(self):
        body = copy.deepcopy(self.pages["orders"])
        body["payload"]["items"][0]["gateway_order_id"] = "external-ctp-v1-429907f7d5b63b955664140887b7001952eb4eaaea32f99efe4c78649c09f0d6"
        validate_page(body)
        body["routing"]["env"] = "prod"
        self.reject(body)


if __name__ == "__main__":
    unittest.main()
