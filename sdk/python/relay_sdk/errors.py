"""Error types raised by relay-sdk."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping


@dataclass(frozen=True)
class RetryDecision:
    """Machine-readable retry guidance for one failed SDK operation."""

    operation: str
    automatic_retry: bool
    requires_reconciliation: bool
    retry_after_seconds: float | None
    action: str
    code: str = ""


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

    def retry_decision(self, operation: str = "read") -> RetryDecision:
        return retry_decision(self, operation=operation)


class RelayConnectionError(RelayError):
    """Raised when the SDK cannot connect to relay."""


class RelayTimeoutError(RelayError):
    """Raised when an HTTP request or wait operation times out."""


class RelayBrokerNotReadyError(RelayError):
    """Raised when OC is running but the broker counter session is not ready."""


class RelayRejectedError(RelayError):
    """Raised when relay or the front gateway rejects a command."""


class RelayIdempotencyError(RelayRejectedError):
    """Raised for idempotency conflicts."""


class RelayOrderStateError(RelayRejectedError):
    """Raised when an order state does not allow the requested operation."""


class RelayCancelRejectedError(RelayRejectedError):
    """Raised when the broker explicitly rejects a cancel action."""


class RelayCommandOutcomeUnknownError(RelayError):
    """Raised when OC restarts before a trade command outcome is known."""


class RelayQueryInterruptedError(RelayError):
    """Raised when an OC restart interrupts a query command."""


class RelayPaginationError(RelayError):
    """Raised when a paginated read cannot prove complete, stable coverage."""


class RelayStreamGapError(RelayError):
    """Raised when an event-stream gap requires a full ledger reconciliation."""


class RelayStreamDisconnectedError(RelayConnectionError):
    """Raised after the bounded event-stream reconnect budget is exhausted."""


class RelayCapabilityError(RelayError):
    """Raised when Relay does not advertise a required SDK capability."""


def retry_decision(error: BaseException, *, operation: str = "read") -> RetryDecision:
    """Classify whether an operation may be retried without guessing its outcome.

    ``operation`` must be ``read``, ``query``, ``write``, ``cancel``, or
    ``stream``. Write and cancel transport failures always require ledger
    reconciliation before a caller decides what to do next.
    """

    operation = str(operation).strip().lower()
    if operation not in {"read", "query", "write", "cancel", "stream"}:
        raise ValueError("operation must be read, query, write, cancel, or stream")

    code = str(getattr(error, "code", "") or "").strip().upper()
    status_code = getattr(error, "status_code", None)
    is_command = operation in {"write", "cancel"}

    if isinstance(error, RelayCommandOutcomeUnknownError) or code == "COMMAND_OUTCOME_UNKNOWN":
        return RetryDecision(operation, False, True, None, "reconcile orders and fills before any new command", code)
    if isinstance(error, RelayCancelRejectedError):
        return RetryDecision(operation, False, True, None, "read the original order and cancel-attempt audit", code)
    if isinstance(error, (RelayIdempotencyError, RelayOrderStateError, RelayRejectedError)):
        return RetryDecision(operation, False, False, None, "fix the request or stop; do not retry automatically", code)
    if isinstance(error, RelayBrokerNotReadyError) or code == "BROKER_NOT_READY":
        if operation == "query":
            return RetryDecision(operation, True, False, 1.0, "retry the query with a new request after broker readiness", code)
        return RetryDecision(operation, False, is_command, None, "wait for broker readiness; reconcile writes before resubmitting", code)
    if isinstance(error, RelayQueryInterruptedError) or code == "QUERY_INTERRUPTED":
        if operation == "query":
            return RetryDecision(operation, True, False, 0.5, "retry the query with a new request", code)
        return RetryDecision(operation, False, False, None, "retry only through a query operation", code)
    if isinstance(error, RelayStreamGapError):
        return RetryDecision(operation, False, True, None, "perform full ledger reconciliation and reconnect with the new cursor", code)
    if isinstance(error, RelayStreamDisconnectedError):
        return RetryDecision(operation, False, True, None, "enter a fault state and perform full ledger reconciliation", code)
    if isinstance(error, (RelayConnectionError, RelayTimeoutError, TimeoutError, OSError)):
        if operation in {"read", "query"}:
            return RetryDecision(operation, True, False, 0.5, "retry with bounded exponential backoff", code)
        return RetryDecision(operation, False, is_command, None, "reconcile the command outcome before retrying", code)
    if status_code in {429, 502, 503, 504}:
        if operation in {"read", "query"}:
            return RetryDecision(operation, True, False, 0.5, "retry with bounded exponential backoff", code)
        return RetryDecision(operation, False, is_command, None, "reconcile the command outcome before retrying", code)
    return RetryDecision(operation, False, is_command, None, "inspect the error before deciding whether to retry", code)


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
    if code == "BROKER_NOT_READY":
        return RelayBrokerNotReadyError(message, **kwargs)
    if code == "IDEMPOTENCY_CONFLICT":
        return RelayIdempotencyError(message, **kwargs)
    if code in {"BROKER_CANCEL_REJECTED", "CANCEL_RESPONSE_TIMEOUT"}:
        return RelayCancelRejectedError(message, **kwargs)
    if code == "COMMAND_OUTCOME_UNKNOWN":
        return RelayCommandOutcomeUnknownError(message, **kwargs)
    if code == "QUERY_INTERRUPTED":
        return RelayQueryInterruptedError(message, **kwargs)
    if code in {"ORDER_TERMINAL_NOT_CANCELABLE", "ORDER_NOT_READY_FOR_CANCEL", "CONFLICT"}:
        return RelayOrderStateError(message, **kwargs)
    if code.endswith("REJECTED") or code.endswith("FAILED") or status_code == 403:
        return RelayRejectedError(message, **kwargs)
    return RelayError(message, **kwargs)
