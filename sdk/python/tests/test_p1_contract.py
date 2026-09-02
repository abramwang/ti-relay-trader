from __future__ import annotations

import json
import unittest
from decimal import ROUND_HALF_UP, Decimal
from io import BytesIO

from relay_sdk import (
    RelayBrokerNotReadyError,
    RelayCancelRejectedError,
    RelayClient,
    RelayCommandOutcomeUnknownError,
    RelayConnectionError,
    RelayError,
    RelayIdempotencyError,
    RelayQueryInterruptedError,
    retry_decision,
)


class MemoryResponse(BytesIO):
    status = 200


class RecordingOpener:
    def __init__(self) -> None:
        self.requests = []

    def open(self, req, timeout=None):
        self.requests.append((req, timeout))
        return MemoryResponse(json.dumps({"ok": True, "data": {"status": "ok"}}).encode("utf-8"))


class RelayP1ContractTests(unittest.TestCase):
    def test_json_price_round_trip_uses_decimal_micro_units(self):
        cases = (
            ("stock", 11.2, "0.01", 11_200_000),
            ("stock_transport_noise", 11.19999999, "0.01", 11_200_000),
            ("etf", 4.818, "0.001", 4_818_000),
            ("convertible_bond", 123.456, "0.001", 123_456_000),
        )
        for name, price, tick, expected_micros in cases:
            with self.subTest(name=name):
                decoded = json.loads(json.dumps({"price": price}))["price"]
                micros = int(
                    Decimal(str(decoded)).quantize(
                        Decimal("0.000001"), rounding=ROUND_HALF_UP
                    )
                    * 1_000_000
                )
                tick_micros = int(Decimal(tick) * 1_000_000)
                self.assertEqual(micros, expected_micros)
                self.assertEqual(micros % tick_micros, 0)

    def test_public_opener_injection_controls_http_transport(self):
        opener = RecordingOpener()
        client = RelayClient("http://relay.invalid", timeout=3.5, opener=opener)

        self.assertEqual(client.status()["status"], "ok")
        self.assertIs(client.opener, opener)
        self.assertEqual(len(opener.requests), 1)
        req, timeout = opener.requests[0]
        self.assertEqual(req.full_url, "http://relay.invalid/v1/status")
        self.assertEqual(req.get_method(), "GET")
        self.assertEqual(req.get_header("User-agent"), "relay-sdk/0.1.33")
        self.assertEqual(timeout, 3.5)

    def test_retry_matrix_fails_closed_for_writes(self):
        read_transport = retry_decision(RelayConnectionError("down"), operation="read")
        write_transport = retry_decision(RelayConnectionError("down"), operation="write")
        self.assertTrue(read_transport.automatic_retry)
        self.assertFalse(read_transport.requires_reconciliation)
        self.assertFalse(write_transport.automatic_retry)
        self.assertTrue(write_transport.requires_reconciliation)

        broker_query = retry_decision(RelayBrokerNotReadyError("not ready"), operation="query")
        broker_write = retry_decision(RelayBrokerNotReadyError("not ready"), operation="write")
        self.assertTrue(broker_query.automatic_retry)
        self.assertFalse(broker_write.automatic_retry)
        self.assertTrue(broker_write.requires_reconciliation)

        interrupted = retry_decision(RelayQueryInterruptedError("interrupted"), operation="query")
        self.assertTrue(interrupted.automatic_retry)

        for error in (
            RelayIdempotencyError("conflict"),
            RelayCancelRejectedError("cancel rejected"),
            RelayCommandOutcomeUnknownError("unknown"),
        ):
            decision = error.retry_decision("write")
            self.assertFalse(decision.automatic_retry)
        self.assertTrue(RelayCommandOutcomeUnknownError("unknown").retry_decision("write").requires_reconciliation)

        overloaded = RelayError("unavailable", status_code=503)
        self.assertTrue(overloaded.retry_decision("read").automatic_retry)
        self.assertFalse(overloaded.retry_decision("write").automatic_retry)
        self.assertTrue(overloaded.retry_decision("write").requires_reconciliation)


if __name__ == "__main__":
    unittest.main()
