from __future__ import annotations

import json
import threading
import unittest
from io import BytesIO
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib import parse

from relay_sdk import RelayClient, RelayIdempotencyError
from relay_sdk.streaming import iter_sse_events


class RelayHandler(BaseHTTPRequestHandler):
    requests = []

    def do_GET(self):  # noqa: N802
        parsed = parse.urlparse(self.path)
        query = parse.parse_qs(parsed.query)
        RelayHandler.requests.append(("GET", parsed.path, query, None))
        if parsed.path == "/v1/status":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "service": "relay-api",
                        "status": "ok",
                        "dependencies": {"database": {"status": "ok"}},
                    },
                }
            )
            return
        if parsed.path == "/v1/accounts":
            self._json({"ok": True, "data": {"accounts": [{"account_id": "acct-1", "enabled": True}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/asset":
            self._json({"ok": True, "data": {"asset": {"account_id": "acct-1", "net_asset": 123.45}}})
            return
        if parsed.path == "/v1/accounts/acct-1/positions":
            self._json({"ok": True, "data": {"positions": [{"account_id": "acct-1", "symbol": "600000", "quantity": 100}]}})
            return
        if parsed.path == "/v1/accounts/acct-1/positions/history":
            self._json({"ok": True, "data": {"positions": [{"account_id": "acct-1", "trade_date": "2026-06-12", "symbol": "600000", "quantity": 100}]}})
            return
        if parsed.path == "/v1/orders":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "orders": [
                            {
                                "account_id": "acct-1",
                                "gateway_order_id": query.get("gateway_order_id", ["gw-1"])[0],
                                "status": "filled",
                                "is_terminal": True,
                                "cum_filled_qty": 100,
                            }
                        ]
                    },
                }
            )
            return
        if parsed.path == "/v1/fills":
            self._json({"ok": True, "data": {"fills": [{"fill_id": "fill-1", "account_id": "acct-1", "qty": 100}]}})
            return
        if parsed.path == "/v1/history/orders":
            self._json({"ok": True, "data": {"orders": [{"account_id": "acct-1", "gateway_order_id": "gw-history", "status": "filled", "is_terminal": True}]}})
            return
        if parsed.path == "/v1/history/fills":
            self._json({"ok": True, "data": {"fills": [{"fill_id": "fill-history", "account_id": "acct-1", "trade_date": query.get("trade_date", [""])[0], "qty": 100}]}})
            return
        if parsed.path == "/v1/events/stream":
            events = [
                (
                    "order.changed",
                    {
                        "type": "order.changed",
                        "account_ids": ["acct-1"],
                        "time": "2026-06-14T00:00:00Z",
                        "data": {"orders": 1, "last_stream_id": "1-0"},
                    },
                ),
                (
                    "fill.changed",
                    {
                        "type": "fill.changed",
                        "account_ids": ["acct-1"],
                        "time": "2026-06-14T00:00:01Z",
                        "data": {"fills": 1, "last_stream_id": "2-0"},
                    },
                ),
                (
                    "order.changed",
                    {
                        "type": "order.changed",
                        "account_ids": ["acct-1"],
                        "time": "2026-06-14T00:00:02Z",
                        "data": {"orders": 1, "last_stream_id": "3-0"},
                    },
                ),
            ]
            body = b"".join(
                (
                    f"event: {event_name}\n"
                    f"data: {json.dumps(payload, separators=(',', ':'))}\n"
                    "\n"
                ).encode("utf-8")
                for event_name, payload in events
            )
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_error(404)

    def do_POST(self):  # noqa: N802
        parsed = parse.urlparse(self.path)
        length = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(length).decode("utf-8") or "{}")
        RelayHandler.requests.append(("POST", parsed.path, {}, body))
        if parsed.path == "/v1/orders":
            if body.get("gateway_order_id") == "gw-replay":
                self._json(
                    {
                        "ok": True,
                        "data": {
                            "order": {
                                "account_id": body["account_id"],
                                "gateway_order_id": "gw-replay",
                                "client_order_id": body["client_order_id"],
                                "status": "cancelled",
                                "is_terminal": True,
                            },
                            "idempotency_key": body["idempotency_key"],
                            "replayed": True,
                        },
                    }
                )
                return
            order = {
                "account_id": body["account_id"],
                "gateway_order_id": body["gateway_order_id"],
                "client_order_id": body["client_order_id"],
                "status": "created",
            }
            self._json({"ok": True, "data": {"order": order, "stream_id": "1-0", "message_id": "msg-1"}}, status=202)
            return
        if parsed.path == "/v1/orders/gw-1/cancel":
            self._json(
                {
                    "ok": True,
                    "data": {
                        "order": {"account_id": body["account_id"], "gateway_order_id": "gw-1", "status": "working"},
                        "cancel_id": body["cancel_id"],
                    },
                },
                status=202,
            )
            return
        if parsed.path == "/v1/accounts/acct-1/orders/refresh":
            self._json({"ok": True, "data": {"account_id": "acct-1", "action": "order.list.query", "stream_id": "2-0"}}, status=202)
            return
        if parsed.path == "/v1/accounts/acct-1/fills/refresh":
            self._json({"ok": True, "data": {"account_id": "acct-1", "action": "fill.list.query", "stream_id": "3-0"}}, status=202)
            return
        if parsed.path == "/v1/accounts/acct-1/asset/refresh" or parsed.path == "/v1/accounts/acct-1/positions/refresh":
            self._json({"ok": True, "data": {"account_id": "acct-1", "action": "query", "stream_id": "4-0"}}, status=202)
            return
        if parsed.path == "/v1/orders/batch":
            self._json({"ok": True, "data": {"orders": body["orders"], "stream_id": "5-0"}}, status=202)
            return
        if parsed.path == "/v1/jobs/runs":
            self._json({"ok": True, "data": {"run": {"run_id": "job-1", "job_name": body.get("job_name") or body.get("report", {}).get("job")}}}, status=202)
            return
        if parsed.path == "/v1/error":
            self._json({"ok": False, "error": {"code": "IDEMPOTENCY_CONFLICT", "message": "duplicate"}}, status=409)
            return
        self.send_error(404)

    def log_message(self, *_args):
        return

    def _json(self, payload, status=200):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class RelayClientTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        RelayHandler.requests = []
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), RelayHandler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base_url = f"http://127.0.0.1:{cls.server.server_address[1]}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.thread.join(timeout=2)

    def setUp(self):
        RelayHandler.requests = []
        self.client = RelayClient(self.base_url, account_id="acct-1")

    def test_queries_return_models(self):
        self.assertEqual(self.client.status()["status"], "ok")
        self.assertEqual(self.client.list_accounts()[0].account_id, "acct-1")
        self.assertEqual(self.client.get_asset().net_asset, 123.45)
        self.assertEqual(self.client.get_positions()[0].symbol, "600000")
        self.assertEqual(self.client.list_orders(gateway_order_id="gw-1")[0].status, "filled")
        self.assertEqual(self.client.list_fills()[0].fill_id, "fill-1")

    def test_history_queries_use_history_endpoints(self):
        self.assertEqual(self.client.get_positions(history=True, trade_date="20260612")[0].trade_date, "2026-06-12")
        self.assertEqual(self.client.list_orders(history=True, date_from="20260612")[0].gateway_order_id, "gw-history")
        self.assertEqual(self.client.list_fills(history=True, trade_date="20260612")[0].fill_id, "fill-history")
        self.assertEqual(RelayHandler.requests[-3][1], "/v1/accounts/acct-1/positions/history")
        self.assertEqual(RelayHandler.requests[-2][1], "/v1/history/orders")
        self.assertEqual(RelayHandler.requests[-1][1], "/v1/history/fills")

    def test_record_job_run(self):
        run = self.client.record_job_run({"ok": True, "job": "pre_open_init", "trading_day": {"target_trade_date": "20260614"}}, trigger="unit")
        self.assertEqual(run["run_id"], "job-1")
        method, path, _query, body = RelayHandler.requests[-1]
        self.assertEqual((method, path), ("POST", "/v1/jobs/runs"))
        self.assertEqual(body["trigger"], "unit")

    def test_submit_order_generates_traceable_ids(self):
        receipt = self.client.submit_order(symbol="600000", exchange="SH", side="B", price=9.67, qty=100)
        self.assertTrue(receipt.gateway_order_id.startswith("sdk-gw-acct-1-"))
        self.assertEqual(receipt.status, "created")
        method, path, _query, body = RelayHandler.requests[-1]
        self.assertEqual((method, path), ("POST", "/v1/orders"))
        self.assertEqual(body["account_id"], "acct-1")
        self.assertEqual(body["idempotency_key"], f"order:acct-1:{body['gateway_order_id']}")

    def test_submit_order_replay_marker(self):
        receipt = self.client.submit_order(
            symbol="600000",
            exchange="SH",
            side="B",
            price=9.67,
            qty=100,
            gateway_order_id="gw-replay",
            client_order_id="client-replay",
            idempotency_key="idem-replay",
        )

        self.assertTrue(receipt.replayed)
        self.assertEqual(receipt.status, "cancelled")

    def test_refresh_and_cancel(self):
        self.assertEqual(self.client.refresh_orders().action, "order.list.query")
        self.assertEqual(self.client.refresh_fills().action, "fill.list.query")
        self.assertEqual(self.client.cancel_order("gw-1").gateway_order_id, "gw-1")

    def test_wait_order_terminal(self):
        order = self.client.wait_order_terminal("gw-1", timeout=1, poll_interval=0.01)
        self.assertTrue(order.is_terminal)
        self.assertEqual(order.filled_qty, 100)

    def test_error_mapping(self):
        with self.assertRaises(RelayIdempotencyError):
            self.client._request("POST", "/v1/error", json_body={})

    def test_sse_parser(self):
        stream = BytesIO(
            b'event: order.changed\n'
            b'data: {"account_ids":["acct-1"],"time":"2026-06-14T00:00:00Z","data":{"orders":1}}\n'
            b"\n"
        )
        event = next(iter_sse_events(stream))
        self.assertEqual(event.event_type, "order.changed")
        self.assertEqual(event.account_ids, ("acct-1",))
        self.assertEqual(event.data["orders"], 1)

    def test_order_status_callback_fetches_orders_after_event(self):
        seen = []

        subscription = self.client.on_order_status(
            lambda order, event: seen.append((order, event.event_type)),
            gateway_order_id="gw-1",
        )
        subscription.join(timeout=2)

        self.assertFalse(subscription.is_alive)
        self.assertIsNone(subscription.error)
        self.assertEqual(len(seen), 1)
        self.assertEqual(seen[0][0].status, "filled")
        self.assertEqual(seen[0][1], "order.changed")
        self.assertIn(("GET", "/v1/events/stream", {"account_id": ["acct-1"]}, None), RelayHandler.requests)

    def test_fill_callback_fetches_fills_after_event(self):
        seen = []

        self.client.watch_fills(lambda fill, event: seen.append((fill, event.event_type)))

        self.assertEqual(len(seen), 1)
        self.assertEqual(seen[0][0].fill_id, "fill-1")
        self.assertEqual(seen[0][1], "fill.changed")


if __name__ == "__main__":
    unittest.main()
