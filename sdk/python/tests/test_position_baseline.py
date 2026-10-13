import copy
import hashlib
import json
from io import BytesIO
import unittest
from urllib.parse import parse_qs, urlsplit
from urllib.error import HTTPError

from relay_sdk import RelayClient, RelayPaginationError


class Response(BytesIO):
    status = 200


class BaselineOpener:
    def __init__(self, count=3, mutate=None, state="complete"):
        self.requests = []
        self.rows = [{"security_id": f"{i:06d}.SH", "symbol": f"{i:06d}", "exchange": "SH", "quantity": 100, "sellable_qty": 50, "source_updated_at": "2026-10-09T15:01:01+08:00"} for i in range(count)]
        fields = ("security_id", "symbol", "exchange", "quantity", "sellable_qty", "source_updated_at")
        digest = hashlib.sha256(json.dumps([[row[f] for f in fields] for row in self.rows], separators=(",", ":")).encode()).hexdigest()
        self.metadata = {"schema_version": "position_baseline.v1", "digest_format": "position_baseline_rows.v1", "baseline_id": "pb-" + "a"*64, "environment": "production", "account_id": "a", "broker_id": "huaxin", "gateway_id": "g", "trade_date": "2026-10-09", "snapshot_type": "close", "completeness": state, "missing_security_means_zero": state == "complete", "execution_ready": False, "total_rows": count, "max_rows": 100000, "content_sha256": digest, "proof": {key: True for key in ("close_snapshot_published", "broker_snapshot_published", "asset_query_completed", "positions_query_completed", "archive_pages_closed", "asset_archive_closed", "snapshot_matches_full_query", "stale_rows_excluded")}}
        self.mutate = mutate
        self.metadata.update({"quality_flags": [], "source_run_id": "close-run", "capture_run_id": "capture-run", "position_query_message_id": "pos-query", "asset_query_message_id": "asset-query", "collection_started_at": "2026-10-09T15:01:00+08:00", "collection_completed_at": "2026-10-09T15:01:01+08:00", "business_as_of": "2026-10-09T15:01:02+08:00", "generated_at": "2026-10-09T15:01:03+08:00"})

    def open(self, req, timeout=None):
        self.requests.append(req)
        query = {k:v[0] for k,v in parse_qs(urlsplit(req.full_url).query).items()}
        offset = int(query.get("cursor") or 0)
        limit = int(query["limit"])
        rows = copy.deepcopy(self.rows[offset:offset+limit])
        end = offset+len(rows)
        data = {"baseline": copy.deepcopy(self.metadata), "positions": rows, "count": len(rows), "offset": offset, "page_complete": end==len(self.rows), "next_cursor": "" if end==len(self.rows) else str(end), "query": {"account_id": query["account_id"] if "account_id" in query else "a", "environment": "production", "trade_date": query["trade_date"], "snapshot_type": "close", "limit": limit, "cursor": query.get("cursor", ""), "baseline_id": query.get("baseline_id", "")}}
        data["query"].update({"broker_id": "huaxin", "gateway_id": "g"})
        if self.mutate:
            self.mutate(data, len(self.requests))
        return Response(json.dumps({"ok": True, "data": data, "request_id": f"baseline-{len(self.requests)}", "time": "2026-10-13T10:00:00+08:00"}).encode())


class PositionBaselineTests(unittest.TestCase):
    def test_changed_http_receipt_is_preserved_and_not_auto_retried(self):
        class ChangedOpener:
            calls = 0

            def open(self, req, timeout=None):
                self.calls += 1
                body = {"ok": False, "request_id": "changed-request", "error": {"code": "POSITION_BASELINE_CHANGED", "message": "restart from first page"}}
                raise HTTPError(req.full_url, 409, "Conflict", {}, BytesIO(json.dumps(body).encode()))

        opener = ChangedOpener()
        with self.assertRaises(RelayPaginationError) as caught:
            RelayClient(account_id="a", opener=opener).get_position_baseline(trade_date="20261009")
        exc = caught.exception
        self.assertEqual(exc.code, "POSITION_BASELINE_CHANGED")
        self.assertEqual(exc.status_code, 409)
        self.assertEqual(exc.request_id, "changed-request")
        self.assertEqual(exc.raw_response["error"]["message"], "restart from first page")
        self.assertFalse(exc.retry_decision().automatic_retry)
        self.assertEqual(opener.calls, 1)

    def test_complete_read_pins_generation_and_keeps_get_evidence(self):
        opener = BaselineOpener()
        snapshot = RelayClient(account_id="a", opener=opener).get_position_baseline(trade_date="20261009", page_size=2, expected_environment="production")
        self.assertTrue(snapshot.baseline.is_complete)
        self.assertEqual(len(snapshot.positions), 3)
        self.assertEqual(len(snapshot.pages), 2)
        self.assertEqual(snapshot.pages[0].request_id, "baseline-1")
        self.assertEqual(snapshot.pages[1].offset, 2)
        for req in opener.requests:
            self.assertEqual(req.get_method(), "GET")
            self.assertIsNone(req.data)
            self.assertEqual(urlsplit(req.full_url).path, "/v1/accounts/a/positions/baseline")
        self.assertIn("baseline_id=pb-", opener.requests[1].full_url)

    def test_empty_partial_and_unavailable_are_different(self):
        snap = RelayClient(account_id="a", opener=BaselineOpener(0)).get_position_baseline(trade_date="20261009")
        self.assertEqual(snap.positions, ())
        self.assertTrue(snap.baseline.missing_security_means_zero)
        for state in ("partial", "unavailable"):
            opener = BaselineOpener(0, state=state)
            client = RelayClient(account_id="a", opener=opener)
            with self.assertRaises(RelayPaginationError) as caught:
                client.get_position_baseline(trade_date="20261009")
            self.assertEqual(caught.exception.code, "POSITION_BASELINE_INCOMPLETE")
            self.assertEqual(len(opener.requests), 1)
            page = client.get_position_baseline_page(trade_date="20261009", require_complete=False)
            self.assertFalse(page.baseline.missing_security_means_zero)

    def test_ten_thousand_plus_rows_are_not_truncated(self):
        snapshot = RelayClient(account_id="a", opener=BaselineOpener(10001)).get_position_baseline(trade_date="20261009")
        self.assertEqual(len(snapshot.positions), 10001)
        self.assertEqual(len(snapshot.pages), 21)

    def test_invalid_pages_scope_and_generation_fail_closed(self):
        for name in ("account", "environment", "gateway", "session", "count", "gap", "duplicate", "digest", "proof"):
            with self.subTest(name=name):
                def mutate(data, call):
                    if name == "digest": data["baseline"]["content_sha256"] = "0"*64
                    elif name == "proof": data["baseline"]["proof"]["asset_archive_closed"] = False
                    elif call == 2:
                        if name == "account": data["baseline"]["account_id"] = "b"
                        elif name == "environment": data["baseline"]["environment"] = "test"
                        elif name == "gateway": data["baseline"]["gateway_id"] = "other"
                        elif name == "session": data["baseline"]["counter_session_id"] = "new-session"
                        elif name == "count": data["count"] += 1
                        elif name == "gap": data["offset"] -= 1
                        elif name == "duplicate": data["positions"][0] = copy.deepcopy(opener.rows[0])
                opener = BaselineOpener(mutate=mutate)
                with self.assertRaises(RelayPaginationError):
                    RelayClient(account_id="a", opener=opener).get_position_baseline(trade_date="20261009", page_size=2)

    def test_limits_and_dates_never_silently_truncate(self):
        for args in ({"max_rows": 2}, {"max_pages": 1, "page_size": 2}):
            with self.assertRaises(RelayPaginationError):
                RelayClient(account_id="a", opener=BaselineOpener()).get_position_baseline(trade_date="20261009", **args)
        opener = BaselineOpener()
        client = RelayClient(account_id="a", opener=opener)
        for day in ("", "not-a-date", "20260230"):
            with self.assertRaises(ValueError): client.get_position_baseline(trade_date=day)
        for limit in (0, 501, True, 1.5):
            with self.assertRaises(ValueError): client.get_position_baseline_page(trade_date="20261009", limit=limit)
        self.assertEqual(opener.requests, [])


if __name__ == "__main__":
    unittest.main()
