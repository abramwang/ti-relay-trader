"""HTTP client for the Relay Trader 9092 API."""

from __future__ import annotations

import json
import os
import socket
import time
import uuid
from typing import Any, Iterable, Mapping
from urllib import error as urlerror
from urllib import parse, request

from .errors import RelayConnectionError, RelayError, RelayTimeoutError, error_from_payload
from .models import Account, Asset, CommandReceipt, Fill, Order, Position, RelayEvent
from .streaming import iter_sse_events


TERMINAL_STATUSES = {"filled", "cancelled", "rejected"}


class RelayClient:
    """Client for relay's 9092 HTTP API."""

    def __init__(
        self,
        base_url: str | None = None,
        *,
        account_id: str | None = None,
        timeout: float = 10.0,
        api_key: str | None = None,
        trust_env: bool = False,
    ) -> None:
        self.base_url = (base_url or os.getenv("RELAY_BASE_URL") or "http://relay-trader.quantstage.com").rstrip("/")
        self.account_id = account_id or os.getenv("RELAY_ACCOUNT_ID") or ""
        self.timeout = timeout
        self.api_key = api_key or os.getenv("RELAY_API_KEY") or ""
        self.trust_env = trust_env
        self._opener = request.build_opener() if trust_env else request.build_opener(request.ProxyHandler({}))

    def list_accounts(self) -> list[Account]:
        data = self._request("GET", "/v1/accounts")
        return [Account.from_dict(item) for item in data.get("accounts", [])]

    def get_asset(self, account_id: str | None = None) -> Asset:
        account_id = self._resolve_account(account_id)
        data = self._request("GET", f"/v1/accounts/{parse.quote(account_id)}/asset")
        return Asset.from_dict(data.get("asset", data))

    def get_positions(
        self,
        account_id: str | None = None,
        *,
        symbol: str | None = None,
        exchange: str | None = None,
    ) -> list[Position]:
        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/positions",
            query={"symbol": symbol, "exchange": exchange},
        )
        return [Position.from_dict(item) for item in data.get("positions", [])]

    def refresh_asset(self, account_id: str | None = None) -> CommandReceipt:
        return self._refresh("asset", account_id)

    def refresh_positions(self, account_id: str | None = None) -> CommandReceipt:
        return self._refresh("positions", account_id)

    def refresh_orders(self, account_id: str | None = None) -> CommandReceipt:
        return self._refresh("orders", account_id)

    def refresh_fills(self, account_id: str | None = None) -> CommandReceipt:
        return self._refresh("fills", account_id)

    def list_orders(
        self,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        symbol: str | None = None,
        exchange: str | None = None,
        status: str | None = None,
        limit: int | None = 100,
    ) -> list[Order]:
        query = {
            "account_id": account_id or self.account_id or None,
            "gateway_order_id": gateway_order_id,
            "symbol": symbol,
            "exchange": exchange,
            "status": status,
            "limit": limit,
        }
        data = self._request("GET", "/v1/orders", query=query)
        return [Order.from_dict(item) for item in data.get("orders", [])]

    def list_fills(
        self,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        symbol: str | None = None,
        exchange: str | None = None,
        limit: int | None = 100,
    ) -> list[Fill]:
        query = {
            "account_id": account_id or self.account_id or None,
            "gateway_order_id": gateway_order_id,
            "symbol": symbol,
            "exchange": exchange,
            "limit": limit,
        }
        data = self._request("GET", "/v1/fills", query=query)
        return [Fill.from_dict(item) for item in data.get("fills", [])]

    def submit_order(
        self,
        *,
        symbol: str,
        exchange: str,
        side: str | None = None,
        trade_side: str | None = None,
        price: float,
        qty: int,
        account_id: str | None = None,
        business_type: str = "S",
        offset_type: str = "C",
        client_order_id: str | None = None,
        gateway_order_id: str | None = None,
        idempotency_key: str | None = None,
    ) -> CommandReceipt:
        account_id = self._resolve_account(account_id)
        gateway_order_id = gateway_order_id or self._new_id("gw", account_id)
        client_order_id = client_order_id or gateway_order_id
        idempotency_key = idempotency_key or f"order:{account_id}:{gateway_order_id}"
        payload = {
            "account_id": account_id,
            "client_order_id": client_order_id,
            "gateway_order_id": gateway_order_id,
            "symbol": symbol,
            "exchange": exchange,
            "trade_side": trade_side or side,
            "business_type": business_type,
            "offset_type": offset_type,
            "price": price,
            "qty": qty,
            "idempotency_key": idempotency_key,
        }
        data = self._request("POST", "/v1/orders", json_body=payload)
        return CommandReceipt.from_dict(data)

    def submit_orders(
        self,
        orders: Iterable[Mapping[str, Any]],
        *,
        account_id: str | None = None,
        idempotency_key: str | None = None,
    ) -> CommandReceipt:
        account_id = self._resolve_account(account_id)
        normalized = []
        for index, order in enumerate(orders):
            item = dict(order)
            item.setdefault("account_id", account_id)
            item.setdefault("gateway_order_id", self._new_id(f"gw{index + 1}", account_id))
            item.setdefault("client_order_id", item["gateway_order_id"])
            item.setdefault("idempotency_key", f"order:{account_id}:{item['gateway_order_id']}")
            normalized.append(item)
        batch_key = idempotency_key or f"batch:{account_id}:{uuid.uuid4().hex}"
        data = self._request(
            "POST",
            "/v1/orders/batch",
            json_body={"account_id": account_id, "orders": normalized, "idempotency_key": batch_key},
        )
        return CommandReceipt.from_dict(data)

    def cancel_order(
        self,
        gateway_order_id: str,
        *,
        account_id: str | None = None,
        cancel_id: str | None = None,
        idempotency_key: str | None = None,
    ) -> CommandReceipt:
        account_id = self._resolve_account(account_id)
        cancel_id = cancel_id or self._new_id("cancel", account_id)
        idempotency_key = idempotency_key or f"cancel:{account_id}:{gateway_order_id}:{cancel_id}"
        payload = {
            "account_id": account_id,
            "gateway_order_id": gateway_order_id,
            "cancel_id": cancel_id,
            "idempotency_key": idempotency_key,
        }
        data = self._request("POST", f"/v1/orders/{parse.quote(gateway_order_id)}/cancel", json_body=payload)
        return CommandReceipt.from_dict(data)

    def wait_order_terminal(
        self,
        gateway_order_id: str,
        *,
        account_id: str | None = None,
        timeout: float = 30.0,
        poll_interval: float = 1.0,
    ) -> Order:
        deadline = time.monotonic() + timeout
        last_order: Order | None = None
        while time.monotonic() <= deadline:
            orders = self.list_orders(account_id=account_id, gateway_order_id=gateway_order_id, limit=1)
            if orders:
                last_order = orders[0]
                if last_order.is_terminal or last_order.status in TERMINAL_STATUSES:
                    return last_order
            time.sleep(poll_interval)
        raise RelayTimeoutError(
            f"order {gateway_order_id} did not reach terminal state within {timeout}s",
            gateway_order_id=gateway_order_id,
            raw_response=last_order.raw if last_order else None,
        )

    def stream_events(self, account_id: str | None = None) -> Iterable[RelayEvent]:
        account_id = account_id or self.account_id
        query = {"account_id": account_id} if account_id else None
        response = self._open("GET", "/v1/events/stream", query=query)
        return iter_sse_events(response)

    def _refresh(self, kind: str, account_id: str | None) -> CommandReceipt:
        account_id = self._resolve_account(account_id)
        data = self._request("POST", f"/v1/accounts/{parse.quote(account_id)}/{kind}/refresh")
        return CommandReceipt.from_dict(data)

    def _resolve_account(self, account_id: str | None) -> str:
        resolved = account_id or self.account_id
        if not resolved:
            raise RelayError("account_id is required")
        return resolved

    def _request(
        self,
        method: str,
        path: str,
        *,
        query: Mapping[str, Any] | None = None,
        json_body: Mapping[str, Any] | None = None,
    ) -> Mapping[str, Any]:
        response = self._open(method, path, query=query, json_body=json_body)
        body = response.read().decode("utf-8")
        payload = json.loads(body) if body else {}
        if isinstance(payload, Mapping) and payload.get("ok") is False:
            raise error_from_payload(payload, status_code=response.status)
        if isinstance(payload, Mapping) and "data" in payload:
            data = payload.get("data")
            return data if isinstance(data, Mapping) else {"value": data}
        return payload if isinstance(payload, Mapping) else {"value": payload}

    def _open(
        self,
        method: str,
        path: str,
        *,
        query: Mapping[str, Any] | None = None,
        json_body: Mapping[str, Any] | None = None,
    ):
        url = self._url(path, query)
        headers = {
            "Accept": "application/json",
            "User-Agent": "relay-sdk/0.1.0",
        }
        data = None
        if json_body is not None:
            data = json.dumps(json_body, separators=(",", ":")).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"
        req = request.Request(url, data=data, headers=headers, method=method)
        try:
            return self._opener.open(req, timeout=self.timeout)
        except urlerror.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            try:
                payload = json.loads(body) if body else {}
            except json.JSONDecodeError:
                payload = {"error": {"message": body or exc.reason}}
            raise error_from_payload(payload, status_code=exc.code) from exc
        except socket.timeout as exc:
            raise RelayTimeoutError(f"relay request timed out: {url}") from exc
        except urlerror.URLError as exc:
            reason = getattr(exc, "reason", exc)
            if isinstance(reason, socket.timeout):
                raise RelayTimeoutError(f"relay request timed out: {url}") from exc
            raise RelayConnectionError(f"relay connection failed: {reason}") from exc

    def _url(self, path: str, query: Mapping[str, Any] | None = None) -> str:
        path = path if path.startswith("/") else "/" + path
        filtered = {}
        for key, value in (query or {}).items():
            if value is None or value == "":
                continue
            filtered[key] = value
        suffix = "?" + parse.urlencode(filtered, doseq=True) if filtered else ""
        return self.base_url + path + suffix

    @staticmethod
    def _new_id(prefix: str, account_id: str) -> str:
        return f"sdk-{prefix}-{account_id}-{int(time.time() * 1000)}-{uuid.uuid4().hex[:8]}"
