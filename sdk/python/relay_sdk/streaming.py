"""SSE helpers for relay event streams."""

from __future__ import annotations

import json
from typing import Iterator

from .models import RelayEvent


def iter_sse_events(response) -> Iterator[RelayEvent]:
    """Yield ``RelayEvent`` objects from a urllib HTTPResponse."""

    event_name = ""
    event_id = ""
    data_lines: list[str] = []
    for raw_line in response:
        line = raw_line.decode("utf-8", errors="replace").rstrip("\r\n")
        if not line:
            if data_lines:
                payload = json.loads("\n".join(data_lines))
                if not isinstance(payload, dict):
                    raise ValueError("SSE data must decode to a JSON object")
                if event_name and "type" not in payload:
                    payload["type"] = event_name
                if event_id and "id" not in payload:
                    payload["id"] = event_id
                yield RelayEvent.from_dict(payload)
            event_name = ""
            event_id = ""
            data_lines = []
            continue
        if line.startswith(":"):
            continue
        field, _, value = line.partition(":")
        value = value[1:] if value.startswith(" ") else value
        if field == "event":
            event_name = value
        elif field == "id":
            event_id = value
        elif field == "data":
            data_lines.append(value)
