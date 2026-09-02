from __future__ import annotations

import unittest
from io import BytesIO

from relay_sdk import (
    Asset,
    Order,
    RelayClient,
    RelayConnectionError,
    RelayEvent,
    RelayStreamDisconnectedError,
    RelayStreamGapError,
    RelayTimeoutError,
    StreamReconciliation,
)
from relay_sdk.streaming import iter_sse_events


def event(event_type: str, event_id: str = "", **data) -> RelayEvent:
    return RelayEvent.from_dict(
        {
            "id": event_id,
            "type": event_type,
            "time": "2026-09-02T10:00:00+08:00",
            "account_ids": ["acct-1"],
            "data": data,
        }
    )


class ScriptedStreamClient(RelayClient):
    def __init__(self, streams):
        super().__init__("http://relay.invalid", account_id="acct-1")
        self.streams = list(streams)
        self.stream_calls = []
        self.reconciliation_calls = []

    def stream_events(self, account_id=None, *, last_event_id=None, idle_timeout=30.0):
        self.stream_calls.append((account_id, last_event_id, idle_timeout))
        scripted = self.streams.pop(0)
        if isinstance(scripted, BaseException):
            raise scripted
        return iter(scripted)

    def reconcile_current_state(
        self,
        account_id=None,
        *,
        reason="manual",
        last_event_id="",
        trigger_event=None,
        page_size=500,
        max_pages=1000,
    ):
        self.reconciliation_calls.append((reason, last_event_id, trigger_event, page_size, max_pages))
        return StreamReconciliation(
            account_id=account_id or self.account_id,
            reason=reason,
            last_event_id=last_event_id,
            current_event_id=trigger_event.event_id if trigger_event else "",
            asset=Asset(account_id=account_id or self.account_id),
            trigger_event=trigger_event,
        )


class RelayStreamingTests(unittest.TestCase):
    def test_parser_preserves_id_and_drops_incomplete_tail(self):
        stream = BytesIO(
            b"id: evt-epoch-1\n"
            b"event: order.changed\n"
            b'data: {"data":{"orders":1}}\n'
            b"\n"
            b"id: evt-epoch-2\n"
            b"event: fill.changed\n"
            b'data: {"data":{"fills":1}}'
        )

        parsed = list(iter_sse_events(stream))

        self.assertEqual(len(parsed), 1)
        self.assertEqual(parsed[0].event_id, "evt-epoch-1")
        self.assertEqual(parsed[0].event_type, "order.changed")

    def test_resilient_stream_reconnects_with_cursor_and_reconciles(self):
        first = event("order.changed", "evt-epoch-1", orders=1)
        duplicate = event("order.changed", "evt-epoch-1", orders=1)
        second = event("future.event", "evt-epoch-2", future=True)
        client = ScriptedStreamClient(
            [
                [event("relay.connected", resume_status="fresh"), first],
                [
                    event("relay.connected", resume_status="resumed", reconciliation_required=True),
                    duplicate,
                    second,
                ],
            ]
        )
        reconciliations = []
        stream = client.stream_events_resilient(
            on_reconcile_required=lambda snapshot: reconciliations.append(snapshot),
            max_reconnects=1,
            backoff_initial=0,
        )

        self.assertEqual(next(stream).event_type, "relay.connected")
        self.assertEqual(next(stream).event_id, "evt-epoch-1")
        self.assertEqual(next(stream).event_type, "relay.connected")
        self.assertEqual(next(stream).event_id, "evt-epoch-2")
        stream.close()

        self.assertEqual(client.stream_calls[0][1], None)
        self.assertEqual(client.stream_calls[1][1], "evt-epoch-1")
        self.assertEqual(len(reconciliations), 1)
        self.assertEqual(reconciliations[0].reason, "stream_resumed")

    def test_gap_requires_explicit_reconciliation_callback(self):
        client = ScriptedStreamClient(
            [[event("relay.connected", resume_status="fresh"), event("relay.gap", "evt-new-0", reason="server_restart")]]
        )
        stream = client.stream_events_resilient(max_reconnects=0)
        self.assertEqual(next(stream).event_type, "relay.connected")
        with self.assertRaises(RelayStreamGapError) as raised:
            next(stream)
        self.assertEqual(raised.exception.code, "EVENT_STREAM_GAP")

    def test_reconnect_budget_is_finite(self):
        client = ScriptedStreamClient(
            [
                RelayConnectionError("down-1"),
                RelayConnectionError("down-2"),
                RelayConnectionError("down-3"),
            ]
        )
        stream = client.stream_events_resilient(
            on_reconcile_required=lambda _snapshot: True,
            max_reconnects=2,
            backoff_initial=0,
        )
        with self.assertRaises(RelayStreamDisconnectedError) as raised:
            next(stream)
        self.assertEqual(raised.exception.code, "EVENT_STREAM_RECONNECT_EXHAUSTED")
        self.assertEqual(len(client.stream_calls), 3)

    def test_idle_timeout_uses_same_bounded_reconnect_budget(self):
        client = ScriptedStreamClient(
            [
                RelayTimeoutError("heartbeat idle timeout"),
                RelayTimeoutError("heartbeat idle timeout"),
            ]
        )
        stream = client.stream_events_resilient(
            on_reconcile_required=lambda _snapshot: True,
            max_reconnects=1,
            backoff_initial=0,
            idle_timeout=0.1,
        )
        with self.assertRaises(RelayStreamDisconnectedError):
            next(stream)
        self.assertEqual([call[2] for call in client.stream_calls], [0.1, 0.1])

    def test_out_of_order_event_forces_reconciliation_and_is_not_yielded(self):
        client = ScriptedStreamClient(
            [[event("order.changed", "evt-epoch-2"), event("order.changed", "evt-epoch-1")]]
        )
        stream = client.stream_events_resilient(
            on_reconcile_required=lambda _snapshot: True,
            max_reconnects=0,
        )
        self.assertEqual(next(stream).event_id, "evt-epoch-2")
        with self.assertRaises(RelayStreamDisconnectedError):
            next(stream)
        self.assertEqual(client.reconciliation_calls[0][0], "event_out_of_order")

    def test_watch_order_status_reads_more_than_one_page(self):
        class CallbackClient(ScriptedStreamClient):
            def iter_orders(self, **_kwargs):
                return iter(
                    Order(
                        account_id="acct-1",
                        trade_date="20260902",
                        gateway_order_id=f"gw-{index}",
                        status="filled",
                    )
                    for index in range(501)
                )

        client = CallbackClient([[event("order.changed", "evt-epoch-1")]])
        seen = []

        def callback(order, _event):
            seen.append(order.gateway_order_id)
            return len(seen) < 501

        client.watch_order_status(callback, limit=100)
        self.assertEqual(len(seen), 501)
        self.assertEqual(len(set(seen)), 501)


if __name__ == "__main__":
    unittest.main()
