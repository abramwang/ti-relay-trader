"""Historical close baseline evidence and strict full-account validation."""

from dataclasses import dataclass, field
from datetime import date
import hashlib
import json
from typing import Any, Mapping

from .errors import RelayPaginationError


def canonical_date(value: str) -> str:
    text = str(value).strip()
    if len(text) == 8 and text.isdigit():
        text = f"{text[:4]}-{text[4:6]}-{text[6:]}"
    return date.fromisoformat(text).isoformat()


def invalid_baseline(message: str, envelope: Mapping[str, Any], code: str = "POSITION_BASELINE_RESPONSE_INVALID") -> RelayPaginationError:
    return RelayPaginationError(message, code=code, request_id=str(envelope.get("request_id") or ""), raw_response=envelope)


def exact_int(value: Any, name: str, envelope: Mapping[str, Any]) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise invalid_baseline(f"{name} must be a non-negative integer", envelope)
    return value


@dataclass(frozen=True)
class PositionBaselineRow:
    security_id: str
    symbol: str
    exchange: str
    quantity: int
    sellable_qty: int
    source_updated_at: str
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    def digest_value(self):
        return [self.security_id, self.symbol, self.exchange, self.quantity, self.sellable_qty, self.source_updated_at]


@dataclass(frozen=True)
class PositionBaseline:
    baseline_id: str
    environment: str
    account_id: str
    broker_id: str
    gateway_id: str
    trade_date: str
    snapshot_type: str
    completeness: str
    missing_security_means_zero: bool
    total_rows: int
    max_rows: int
    content_sha256: str
    counter_session_id: str
    proof: Mapping[str, bool]
    quality_flags: tuple[str, ...]
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @property
    def is_complete(self) -> bool:
        return self.completeness == "complete" and self.missing_security_means_zero


@dataclass(frozen=True)
class PositionBaselinePage:
    baseline: PositionBaseline
    positions: tuple[PositionBaselineRow, ...]
    count: int
    offset: int
    next_cursor: str
    page_complete: bool
    query: Mapping[str, Any]
    request_id: str
    time: str
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_envelope(cls, envelope: Mapping[str, Any]) -> "PositionBaselinePage":
        data = envelope.get("data", {})
        if not isinstance(data, Mapping):
            raise invalid_baseline("baseline data must be an object", envelope)
        b = data.get("baseline", {})
        if not isinstance(b, Mapping):
            raise invalid_baseline("baseline metadata must be an object", envelope)
        if b.get("schema_version") != "position_baseline.v1" or b.get("digest_format") != "position_baseline_rows.v1":
            raise invalid_baseline("unsupported or missing baseline schema/digest", envelope)
        if b.get("completeness") not in {"complete", "partial", "unavailable"} or b.get("environment") not in {"production", "test"}:
            raise invalid_baseline("invalid baseline state/scope", envelope)
        if not isinstance(b.get("missing_security_means_zero"), bool) or b.get("execution_ready") is not False:
            raise invalid_baseline("missing or unsafe baseline semantics", envelope)
        digest = b.get("content_sha256", "")
        identity = b.get("baseline_id", "")
        if not isinstance(digest,str) or len(digest) != 64 or any(x not in "0123456789abcdef" for x in digest) or not isinstance(identity,str) or not identity.startswith("pb-") or len(identity) != 67 or any(x not in "0123456789abcdef" for x in identity[3:]):
            raise invalid_baseline("invalid baseline identifier/digest", envelope)
        total = exact_int(b.get("total_rows"), "total_rows", envelope)
        maximum = exact_int(b.get("max_rows"), "max_rows", envelope)
        if maximum < 1 or total > maximum:
            raise invalid_baseline("baseline exceeds its declared limit", envelope)
        if not isinstance(b.get("proof"), Mapping) or not isinstance(data.get("positions"), list) or not isinstance(data.get("query"), Mapping):
            raise invalid_baseline("baseline proof/rows/query are missing or invalid", envelope)
        proof = dict(b["proof"])
        complete = b["completeness"] == "complete"
        required = ("close_snapshot_published", "broker_snapshot_published", "asset_query_completed", "positions_query_completed", "archive_pages_closed", "asset_archive_closed", "snapshot_matches_full_query", "stale_rows_excluded")
        if complete != b["missing_security_means_zero"] or (complete and any(proof.get(key) is not True for key in required)):
            raise invalid_baseline("completeness has no full-account proof", envelope)
        if complete and (b.get("quality_flags") != [] or any(not b.get(key) for key in ("source_run_id", "capture_run_id", "position_query_message_id", "asset_query_message_id", "collection_started_at", "collection_completed_at", "business_as_of", "generated_at"))):
            raise invalid_baseline("complete baseline lacks provenance/time evidence", envelope)
        rows = []
        for raw in data.get("positions", []):
            if not isinstance(raw, Mapping):
                raise invalid_baseline("position must be an object", envelope)
            symbol, exchange = raw.get("symbol", ""), raw.get("exchange", "")
            if not isinstance(symbol,str) or not isinstance(exchange,str) or not symbol or not exchange or raw.get("security_id") != symbol + "." + exchange:
                raise invalid_baseline("position identity is inconsistent", envelope)
            quantity = exact_int(raw.get("quantity"), "quantity", envelope)
            available = exact_int(raw.get("sellable_qty"), "sellable_qty", envelope)
            source_time = raw.get("source_updated_at", "")
            if not isinstance(source_time,str) or (complete and (quantity == 0 or not source_time)):
                raise invalid_baseline("complete baseline contains invalid quantity/source time", envelope)
            rows.append(PositionBaselineRow(raw["security_id"], symbol, exchange, quantity, available, source_time, dict(raw)))
        count = exact_int(data.get("count"), "count", envelope)
        offset = exact_int(data.get("offset"), "offset", envelope)
        next_cursor = data.get("next_cursor", "")
        finished = data.get("page_complete")
        if count != len(rows) or offset + count > total or not isinstance(finished, bool) or not isinstance(next_cursor, str):
            raise invalid_baseline("page count/coverage is invalid", envelope)
        if finished != (offset + count == total) or bool(next_cursor) == finished or (count == 0 and not finished):
            raise invalid_baseline("page termination/cursor is invalid", envelope)
        baseline = PositionBaseline(identity, b["environment"], str(b.get("account_id") or ""), str(b.get("broker_id") or ""), str(b.get("gateway_id") or ""), str(b.get("trade_date") or ""), str(b.get("snapshot_type") or ""), b["completeness"], b["missing_security_means_zero"], total, maximum, digest, str(b.get("counter_session_id") or ""), proof, tuple(b.get("quality_flags") or []), dict(b))
        return cls(baseline, tuple(rows), count, offset, next_cursor, finished, dict(data.get("query") or {}), str(envelope.get("request_id") or ""), str(envelope.get("time") or ""), dict(envelope))


@dataclass(frozen=True)
class PositionBaselineSnapshot:
    baseline: PositionBaseline
    positions: tuple[PositionBaselineRow, ...]
    pages: tuple[PositionBaselinePage, ...]


def position_baseline_digest(rows) -> str:
    body = json.dumps([row.digest_value() for row in rows], ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(body).hexdigest()
