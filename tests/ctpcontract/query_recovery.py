"""Offline recovered-output evidence checks, not durable storage or live readiness."""

import copy

from query_contract import decode_json, require
from query_sequence import QuerySequence


def recover_complete_query(original_context, saved_bodies, received_bodies, *,
                           context_verified, durable_complete_output_verified):
    # Flags are caller-owned proof assumptions, never trusted from a Redis payload.
    require(context_verified is True and durable_complete_output_verified is True,
            "verified original context and durable complete output required")
    saved = QuerySequence(original_context, now=0, deadline=1)
    messages = {}
    for text in saved_bodies:
        body = decode_json(text)
        require(body["message_id"] not in messages, "duplicate saved output identity")
        saved.accept(text, now=0)
        messages[body["message_id"]] = text
    items = saved.candidate()  # A stored partial prefix is never a complete outbox.
    received = set()
    for text in received_bodies:
        body = decode_json(text)
        require(messages.get(body["message_id"]) == text, "replay differs from original output")
        received.add(body["message_id"])
    require(received == set(messages), "incomplete replay delivery")
    return {"kind": "historical_query_result", "original_context": copy.deepcopy(original_context),
            "items": items, "current_readiness": False, "replace_current_snapshot": False}
