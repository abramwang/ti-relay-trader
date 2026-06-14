"""Error types raised by relay-sdk."""

from __future__ import annotations

from typing import Any, Mapping


class RelayError(Exception):
    """Base error for relay API failures."""

    def __init__(
        self,
        message: str,
        *,
        code: str | None = None,
        request_id: str | None = None,
        correlation_id: str | None = None,
        gateway_order_id: str | None = None,
        status_code: int | None = None,
        raw_response: Mapping[str, Any] | None = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.code = code
        self.request_id = request_id
        self.correlation_id = correlation_id
        self.gateway_order_id = gateway_order_id
        self.status_code = status_code
        self.raw_response = dict(raw_response or {})

    def __str__(self) -> str:
        parts = []
        if self.code:
            parts.append(self.code)
        if self.status_code:
            parts.append(f"HTTP {self.status_code}")
        prefix = f"[{', '.join(parts)}] " if parts else ""
        return prefix + self.message


class RelayConnectionError(RelayError):
    """Raised when the SDK cannot connect to relay."""


class RelayTimeoutError(RelayError):
    """Raised when an HTTP request or wait operation times out."""


class RelayRejectedError(RelayError):
    """Raised when relay or the front gateway rejects a command."""


class RelayIdempotencyError(RelayRejectedError):
    """Raised for idempotency conflicts."""


class RelayOrderStateError(RelayRejectedError):
    """Raised when an order state does not allow the requested operation."""


def error_from_payload(
    payload: Mapping[str, Any] | None,
    *,
    status_code: int | None = None,
    default_message: str = "relay request failed",
) -> RelayError:
    """Build the most specific SDK error from a relay error envelope."""

    payload = dict(payload or {})
    error = payload.get("error") if isinstance(payload.get("error"), Mapping) else {}
    data = payload.get("data") if isinstance(payload.get("data"), Mapping) else {}
    code = str(error.get("code") or data.get("code") or "")
    message = str(error.get("message") or data.get("message") or default_message)
    request_id = str(payload.get("request_id") or error.get("request_id") or "")
    gateway_order_id = str(data.get("gateway_order_id") or error.get("gateway_order_id") or "")
    correlation_id = str(data.get("correlation_id") or error.get("correlation_id") or "")

    kwargs = {
        "code": code or None,
        "request_id": request_id or None,
        "correlation_id": correlation_id or None,
        "gateway_order_id": gateway_order_id or None,
        "status_code": status_code,
        "raw_response": payload,
    }
    if code == "IDEMPOTENCY_CONFLICT":
        return RelayIdempotencyError(message, **kwargs)
    if code in {"ORDER_TERMINAL_NOT_CANCELABLE", "ORDER_NOT_READY_FOR_CANCEL", "CONFLICT"}:
        return RelayOrderStateError(message, **kwargs)
    if code.endswith("REJECTED") or code.endswith("FAILED") or status_code == 403:
        return RelayRejectedError(message, **kwargs)
    return RelayError(message, **kwargs)
