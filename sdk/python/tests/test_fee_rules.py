from __future__ import annotations

import json
import unittest
from io import BytesIO
from urllib.parse import parse_qs, urlsplit

from relay_sdk import FeeRule, RelayClient, RelayPaginationError


class Response(BytesIO):
    status = 200


class Opener:
    def __init__(self, data):
        self.data = data
        self.requests = []

    def open(self, req, timeout=None):
        self.requests.append(req)
        return Response(json.dumps({
            "ok": True, "data": self.data, "request_id": "fee-audit",
            "time": "2026-10-11T10:00:00+08:00",
        }).encode())


class FeeRuleTests(unittest.TestCase):
    def test_public_read_preserves_evidence_and_metadata(self):
        rule = {
            "rule_id": "r", "account_id": "a", "version": 3,
            "commission_rate": 0, "minimum_commission": 0,
            "raw_payload": {"verification": {"status": "confirmed"}},
        }
        opener = Opener({"rules": [rule], "count": 1, "is_complete": True, "query": {"effective_on": "2026-10-10"}})
        page = RelayClient(account_id="a", opener=opener).get_fee_rules(effective_on="20261010")
        self.assertTrue(page.is_complete)
        self.assertEqual(page.count, 1)
        self.assertEqual(page.request_id, "fee-audit")
        self.assertEqual(page.time, "2026-10-11T10:00:00+08:00")
        self.assertEqual(page.query["effective_on"], "2026-10-10")
        self.assertEqual(page.rules[0].raw, rule)
        self.assertEqual(page.rules[0].raw_payload, rule["raw_payload"])
        self.assertEqual(page.rules[0].commission_rate, 0)
        self.assertIsNone(page.rules[0].stamp_duty_rate)
        req = opener.requests[0]
        self.assertEqual(req.get_method(), "GET")
        self.assertIsNone(req.data)
        self.assertEqual(urlsplit(req.full_url).path, "/v1/performance/fee-rules")
        self.assertEqual(parse_qs(urlsplit(req.full_url).query), {
            "account_id": ["a"], "effective_on": ["20261010"], "status": ["active"], "limit": ["500"],
        })

    def test_empty_complete_is_not_a_zero_fee_rule(self):
        page = RelayClient(opener=Opener({"rules": [], "count": 0, "is_complete": True})).get_fee_rules()
        self.assertEqual(page.rules, ())
        self.assertTrue(page.is_complete)
        self.assertIsNone(FeeRule.from_dict({}).commission_rate)

    def test_truncated_or_old_response_fails_closed(self):
        for complete in (False, None):
            data = {"rules": [], "count": 0}
            if complete is not None:
                data["is_complete"] = complete
            client = RelayClient(opener=Opener(data))
            with self.assertRaises(RelayPaginationError) as caught:
                client.get_fee_rules()
            self.assertEqual(caught.exception.code, "FEE_RULE_LIST_INCOMPLETE")
            self.assertEqual(caught.exception.request_id, "fee-audit")
            self.assertEqual(caught.exception.raw_response["data"], data)
            self.assertIs(client.get_fee_rules(require_complete=False).is_complete, complete)

    def test_count_mismatch_is_invalid_even_with_partial_reads_allowed(self):
        client = RelayClient(opener=Opener({"rules": [], "count": 1, "is_complete": True}))
        with self.assertRaises(RelayPaginationError) as caught:
            client.get_fee_rules(require_complete=False)
        self.assertEqual(caught.exception.code, "FEE_RULE_RESPONSE_INVALID")

    def test_explicit_account_and_query_validation(self):
        opener = Opener({"rules": [], "count": 0, "is_complete": True})
        client = RelayClient(account_id="default", opener=opener)
        client.get_fee_rules("actual", status=None)
        self.assertIn("account_id=actual", opener.requests[-1].full_url)
        self.assertNotIn("status=", opener.requests[-1].full_url)
        for limit in (0, -1, 501, True, 1.5):
            with self.assertRaises(ValueError):
                client.get_fee_rules(limit=limit)
        with self.assertRaises(ValueError):
            client.get_fee_rules(status="unknown")
        self.assertEqual(len(opener.requests), 1)


if __name__ == "__main__":
    unittest.main()
