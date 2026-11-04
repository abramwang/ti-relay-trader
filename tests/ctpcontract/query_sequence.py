"""Bounded offline query assembly model; no persistence, ACK or live application."""

import copy
import math

from jsonschema import ValidationError

from query_contract import decode_json, require, validate_page


class QuerySequence:
    def __init__(self, expected, *, now, deadline, max_pages=256,
                 max_rows=100000, max_bytes=8 * 1024 * 1024):
        require(set(expected) == {"routing", "action", "origin_message_id", "ticket",
                                 "counter_session_id", "connection_generation",
                                 "sdk_request_id", "trade_date"}, "explicit request context required")
        for value in (now, deadline):
            require(type(value) in (int, float) and math.isfinite(value), "invalid monotonic time")
        require(deadline > now, "deadline must follow query start")
        for value in (max_pages, max_rows, max_bytes):
            require(type(value) is int and value > 0, "invalid resource bound")
        self.expected = copy.deepcopy(expected)
        self.deadline = deadline
        self.last_now = now
        self.max_pages, self.max_rows, self.max_bytes = max_pages, max_rows, max_bytes
        self.state = "collecting"
        self.reason = None
        self.pages = {}
        self.messages = {}
        self.final_index = None
        self.rows = 0
        self.bytes = 0
        self._candidate = None

    def _invalidate(self, reason):
        self.state, self.reason, self._candidate = "invalid", reason, None
        raise ValueError(reason)

    def advance(self, now):
        if type(now) not in (int, float) or not math.isfinite(now) or now < self.last_now:
            self._invalidate("invalid monotonic clock")
        self.last_now = now
        if self.state == "collecting" and now >= self.deadline:
            self.state, self.reason = "expired", "query deadline reached"
        return self.state

    def interrupt(self, reason):
        """Caller explicitly reports disconnect, generation change or cancellation."""
        self.state, self.reason, self._candidate = "interrupted", reason, None

    def accept(self, text, *, now):
        self.advance(now)
        if self.state in ("invalid", "expired", "interrupted"):
            raise ValueError(self.reason)
        # This model receives already-dispatched query bodies, not arbitrary streams.
        size = len(text.encode("utf-8"))
        if size > self.max_bytes:
            self._invalidate("single message byte limit")
        try:
            body = decode_json(text)
            require(isinstance(body, dict), "query envelope must be an object")
        except ValueError as exc:
            self._invalidate(str(exc))
        # A different account or command must not contaminate this candidate.
        for key in ("routing", "origin_message_id"):
            require(body.get(key) == self.expected[key], f"foreign query: {key}")
        try:
            validate_page(body, expected=self.expected)
        except (ValueError, ValidationError) as exc:
            # Includes schema/type failures; no partial candidate remains eligible.
            self._invalidate(f"invalid query page: {exc}")
        message_id = body["message_id"]
        if message_id in self.messages:
            if self.messages[message_id] != text:
                self._invalidate("same message_id with different body")
            return "replayed"
        if self.state in ("ready", "failed"):
            self._invalidate("new message after resolved query")
        if self.bytes + size > self.max_bytes:
            self._invalidate("query byte limit")
        if body["status"] == "failed":
            if self.final_index is not None:
                self._invalidate("failure conflicts with successful terminal")
            self.messages[message_id] = text
            self.bytes += size
            self.state, self.reason = "failed", body["code"]
            return self.state
        index = body["chunk"]["index"]
        if index >= self.max_pages or index in self.pages:
            self._invalidate("page index limit or duplicate page identity")
        if self.final_index is not None and index > self.final_index:
            self._invalidate("page beyond successful terminal")
        if body["status"] == "completed":
            if self.final_index is not None or any(i > index for i in self.pages):
                self._invalidate("conflicting successful terminal")
            self.final_index = index
        count = len(body["payload"]["items"])
        if self.rows + count > self.max_rows:
            self._invalidate("query row limit")
        self.messages[message_id] = text
        self.pages[index] = body
        self.bytes += size
        self.rows += count
        if self.final_index is None or len(self.pages) != self.final_index + 1:
            return self.state
        # Validate the complete set again to catch cross-page business duplicates.
        candidate = copy.deepcopy(self.pages[self.final_index])
        candidate["payload"]["items"] = [
            row for i in range(self.final_index + 1) for row in self.pages[i]["payload"]["items"]
        ]
        try:
            validate_page(candidate, expected=self.expected)
        except (ValueError, ValidationError) as exc:
            self._invalidate(f"invalid complete set: {exc}")
        self._candidate = copy.deepcopy(candidate["payload"]["items"])
        self.state = "ready"
        return self.state

    def candidate(self):
        """Return isolated rows, NOT permission to commit/ACK or replace a live ledger."""
        require(self.state == "ready", "query not complete and valid")
        return copy.deepcopy(self._candidate)
