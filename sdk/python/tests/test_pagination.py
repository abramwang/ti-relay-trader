from __future__ import annotations

import unittest
from typing import Any, Mapping

from relay_sdk import Asset, Fill, Order, Position, RelayClient, RelayConnectionError, RelayPaginationError


def order_row(index: int) -> dict[str, Any]:
    return {
        "account_id": "acct-1",
        "gateway_order_id": f"gw-{index:04d}",
        "trade_date": "20260902",
        "symbol": "600000",
        "exchange": "SH",
        "status": "filled",
        "is_terminal": True,
    }


def fill_row(index: int) -> dict[str, Any]:
    return {
        "account_id": "acct-1",
        "gateway_order_id": f"gw-{index:04d}",
        "fill_id": f"fill-{index:04d}",
        "trade_date": "20260902",
        "symbol": "600000",
        "exchange": "SH",
        "qty": 100,
    }


def position_row(index: int) -> dict[str, Any]:
    return {
        "account_id": "acct-1",
        "trade_date": "20260902",
        "snapshot_type": "close",
        "symbol": f"{index:06d}",
        "exchange": "SH",
        "quantity": 100,
    }


class ScriptedClient(RelayClient):
    def __init__(self, responder):
        super().__init__("http://relay.invalid", account_id="acct-1")
        self.responder = responder
        self.calls: list[tuple[str, str, Mapping[str, Any]]] = []

    def _request_envelope(self, method, path, *, query=None, json_body=None):
        normalized_query = dict(query or {})
        self.calls.append((method, path, normalized_query))
        return self.responder(method, path, normalized_query)


def paged_responder(item_key: str, rows: list[dict[str, Any]], *, max_page_size: int = 500):
    def respond(_method: str, _path: str, query: Mapping[str, Any]) -> Mapping[str, Any]:
        requested_limit = int(query.get("limit") or 100)
        limit = min(requested_limit, max_page_size)
        cursor = str(query.get("cursor") or "")
        offset = int(cursor or 0)
        items = rows[offset : offset + limit]
        if len(items) > limit:
            items = items[:limit]
        next_cursor = ""
        if offset + len(items) < len(rows) or (limit == max_page_size and len(items) == limit):
            next_cursor = str(offset + limit)
        normalized_query = {key: value for key, value in query.items() if value not in (None, "")}
        normalized_query["limit"] = limit
        normalized_query["cursor"] = cursor
        return {
            "ok": True,
            "data": {
                item_key: items,
                "count": len(items),
                "next_cursor": next_cursor,
                "query": normalized_query,
            },
            "request_id": f"req-{offset}",
            "time": "2026-09-02T10:00:00+08:00",
        }

    return respond


class RelayPaginationTests(unittest.TestCase):
    def test_order_page_preserves_typed_items_and_envelope(self):
        client = ScriptedClient(paged_responder("orders", [order_row(0), order_row(1)]))

        page = client.list_orders_page(trade_date="20260902", limit=1)

        self.assertIsInstance(page.items[0], Order)
        self.assertEqual(page.count, 1)
        self.assertEqual(page.next_cursor, "1")
        self.assertFalse(page.is_complete)
        self.assertEqual(page.query["trade_date"], "20260902")
        self.assertEqual(page.request_id, "req-0")
        self.assertEqual(page.time, "2026-09-02T10:00:00+08:00")

    def test_iter_orders_covers_cardinality_boundaries(self):
        for total, expected_calls in ((0, 1), (1, 1), (500, 2), (501, 2), (1001, 3)):
            with self.subTest(total=total):
                client = ScriptedClient(paged_responder("orders", [order_row(i) for i in range(total)]))
                orders = list(client.iter_orders(trade_date="20260902", page_size=500))
                self.assertEqual(len(orders), total)
                self.assertEqual(len({order.gateway_order_id for order in orders}), total)
                self.assertEqual(len(client.calls), expected_calls)

    def test_iter_order_pages_preserves_every_audit_envelope(self):
        client = ScriptedClient(paged_responder("orders", [order_row(i) for i in range(1001)]))

        pages = list(client.iter_order_pages(trade_date="20260902", page_size=500))

        self.assertEqual([page.count for page in pages], [500, 500, 1])
        self.assertEqual([page.request_id for page in pages], ["req-0", "req-500", "req-1000"])
        self.assertEqual([page.next_cursor for page in pages], ["500", "1000", ""])
        self.assertEqual([page.is_complete for page in pages], [False, False, True])
        self.assertTrue(all(page.query["trade_date"] == "20260902" for page in pages))

    def test_reconciliation_includes_all_page_audits(self):
        rows_by_path = {
            "/v1/accounts/acct-1/positions": ("positions", [position_row(i) for i in range(501)]),
            "/v1/orders": ("orders", [order_row(i) for i in range(1001)]),
            "/v1/fills": ("fills", [fill_row(i) for i in range(1)]),
        }

        def respond(method, path, query):
            item_key, rows = rows_by_path[path]
            return paged_responder(item_key, rows)(method, path, query)

        class ReconciliationClient(ScriptedClient):
            def get_asset_raw(self, account_id=None):
                return Asset(account_id=account_id or self.account_id)

        client = ReconciliationClient(respond)
        snapshot = client.reconcile_current_state(page_size=500)

        self.assertEqual((len(snapshot.positions), len(snapshot.orders), len(snapshot.fills)), (501, 1001, 1))
        self.assertEqual((len(snapshot.position_pages), len(snapshot.order_pages), len(snapshot.fill_pages)), (2, 3, 1))
        self.assertEqual(snapshot.position_pages[-1].next_cursor, "")
        self.assertEqual(snapshot.order_pages[-1].request_id, "req-1000")
        self.assertTrue(snapshot.fill_pages[-1].is_complete)

    def test_iter_fills_uses_history_route_and_typed_models(self):
        client = ScriptedClient(paged_responder("fills", [fill_row(i) for i in range(501)]))

        fills = list(
            client.iter_fills(
                account_id="acct-1",
                history=True,
                date_from="20260901",
                date_to="20260902",
                page_size=500,
            )
        )

        self.assertEqual(len(fills), 501)
        self.assertTrue(all(isinstance(item, Fill) for item in fills))
        self.assertTrue(all(path == "/v1/history/fills" for _, path, _ in client.calls))
        self.assertEqual({call[2]["date_from"] for call in client.calls}, {"20260901"})

    def test_iter_positions_uses_history_route_and_fixed_query(self):
        client = ScriptedClient(paged_responder("positions", [position_row(i) for i in range(1001)]))

        positions = list(
            client.iter_positions(
                history=True,
                date_from="20260901",
                date_to="20260902",
                snapshot_type="close",
                enrich=False,
                page_size=500,
            )
        )

        self.assertEqual(len(positions), 1001)
        self.assertTrue(all(isinstance(item, Position) for item in positions))
        self.assertTrue(all(path == "/v1/accounts/acct-1/positions/history" for _, path, _ in client.calls))
        self.assertEqual({call[2]["snapshot_type"] for call in client.calls}, {"close"})
        self.assertEqual([call[2].get("cursor") for call in client.calls], [None, "500", "1000"])

    def test_iter_rejects_repeated_cursor(self):
        def respond(_method, _path, query):
            cursor = str(query.get("cursor") or "")
            return {
                "ok": True,
                "data": {
                    "orders": [order_row(0)],
                    "count": 1,
                    "next_cursor": "repeat",
                    "query": {"trade_date": "20260902", "limit": 1, "cursor": cursor},
                },
                "request_id": "req-loop",
                "time": "2026-09-02T10:00:00+08:00",
            }

        client = ScriptedClient(respond)
        with self.assertRaises(RelayPaginationError) as raised:
            list(client.iter_orders(trade_date="20260902", page_size=1))
        self.assertEqual(raised.exception.code, "PAGINATION_CURSOR_LOOP")
        self.assertEqual(raised.exception.request_id, "req-loop")

    def test_iter_rejects_query_drift(self):
        calls = 0

        def respond(_method, _path, query):
            nonlocal calls
            calls += 1
            return {
                "ok": True,
                "data": {
                    "orders": [order_row(calls)],
                    "count": 1,
                    "next_cursor": str(calls),
                    "query": {
                        "trade_date": "20260902" if calls == 1 else "20260903",
                        "limit": 1,
                        "cursor": str(query.get("cursor") or ""),
                    },
                },
                "request_id": f"req-{calls}",
                "time": "2026-09-02T10:00:00+08:00",
            }

        client = ScriptedClient(respond)
        with self.assertRaises(RelayPaginationError) as raised:
            list(client.iter_orders(trade_date="20260902", page_size=1))
        self.assertEqual(raised.exception.code, "PAGINATION_QUERY_DRIFT")

    def test_iter_rejects_count_mismatch(self):
        def respond(_method, _path, query):
            return {
                "ok": True,
                "data": {
                    "fills": [fill_row(0)],
                    "count": 2,
                    "next_cursor": "",
                    "query": {"limit": query["limit"], "cursor": ""},
                },
                "request_id": "req-count",
            }

        client = ScriptedClient(respond)
        with self.assertRaises(RelayPaginationError) as raised:
            list(client.iter_fills(page_size=500))
        self.assertEqual(raised.exception.code, "PAGINATION_COUNT_MISMATCH")

    def test_iter_enforces_page_and_item_bounds(self):
        client = ScriptedClient(paged_responder("orders", [order_row(i) for i in range(1001)]))
        self.assertEqual(len(list(client.iter_orders(page_size=500, max_items=501))), 501)
        self.assertEqual(len(client.calls), 2)

        client = ScriptedClient(paged_responder("orders", [order_row(i) for i in range(501)]))
        with self.assertRaises(RelayPaginationError) as raised:
            list(client.iter_orders(page_size=500, max_pages=1))
        self.assertEqual(raised.exception.code, "PAGINATION_LIMIT_EXCEEDED")

        client = ScriptedClient(paged_responder("orders", [order_row(0)]))
        self.assertEqual(list(client.iter_orders(max_items=0)), [])
        self.assertEqual(client.calls, [])
        with self.assertRaises(ValueError):
            client.iter_orders(max_pages=0)
        with self.assertRaises(ValueError):
            client.iter_orders(max_items=-1)

    def test_iter_propagates_midstream_connection_failure(self):
        calls = 0

        def respond(_method, _path, query):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise RelayConnectionError("connection lost")
            return paged_responder("orders", [order_row(0), order_row(1)], max_page_size=1)(
                _method, _path, query
            )

        client = ScriptedClient(respond)
        with self.assertRaises(RelayConnectionError):
            list(client.iter_orders(page_size=1))
        self.assertEqual(calls, 2)


if __name__ == "__main__":
    unittest.main()
