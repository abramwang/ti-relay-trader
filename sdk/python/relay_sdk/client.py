"""HTTP client for the Relay Trader 9092 API."""

from __future__ import annotations

import json
import os
import socket
import threading
import time
import uuid
from typing import Any, Callable, Iterable, Mapping
from urllib import error as urlerror
from urllib import parse, request

from .errors import RelayConnectionError, RelayError, RelayTimeoutError, error_from_payload
from .models import Account, Asset, CommandReceipt, ComponentTransfer, Fill, Order, Position, RelayEvent
from .streaming import iter_sse_events


TERMINAL_STATUSES = {"filled", "cancelled", "rejected"}
SDK_VERSION = "0.1.21"
JOB_STATUS_ALIASES = {"completed": "succeeded"}
OrderStatusCallback = Callable[[Order, RelayEvent], object]
FillCallback = Callable[[Fill, RelayEvent], object]
CancelRejectedCallback = Callable[[RelayEvent], object]


class CallbackSubscription:
    """Background callback subscription returned by ``on_*`` helpers."""

    def __init__(self, target: Callable[[threading.Event], None], *, daemon: bool = True) -> None:
        self._stop_event = threading.Event()
        self._target = target
        self._error: BaseException | None = None
        self._thread = threading.Thread(target=self._run, daemon=daemon)

    def start(self) -> "CallbackSubscription":
        self._thread.start()
        return self

    def stop(self) -> None:
        self._stop_event.set()

    def close(self) -> None:
        self.stop()

    def join(self, timeout: float | None = None) -> None:
        self._thread.join(timeout)

    @property
    def is_alive(self) -> bool:
        return self._thread.is_alive()

    @property
    def error(self) -> BaseException | None:
        return self._error

    def _run(self) -> None:
        try:
            self._target(self._stop_event)
        except BaseException as exc:  # noqa: BLE001 - surfaced through ``error``.
            self._error = exc


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

    def status(self) -> Mapping[str, Any]:
        """Return relay service and dependency health from ``GET /v1/status``."""

        return self._request("GET", "/v1/status")

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
        trade_date: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
        snapshot_type: str | None = None,
        history: bool | None = None,
    ) -> list[Position]:
        account_id = self._resolve_account(account_id)
        path = f"/v1/accounts/{parse.quote(account_id)}/positions"
        if history:
            path += "/history"
        data = self._request(
            "GET",
            path,
            query={
                "symbol": symbol,
                "exchange": exchange,
                "trade_date": trade_date,
                "date_from": date_from,
                "date_to": date_to,
                "snapshot_type": snapshot_type,
                "history": history,
            },
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
        trade_date: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
        history: bool | None = None,
        limit: int | None = 100,
    ) -> list[Order]:
        query = {
            "account_id": account_id or self.account_id or None,
            "gateway_order_id": gateway_order_id,
            "symbol": symbol,
            "exchange": exchange,
            "status": status,
            "trade_date": trade_date,
            "date_from": date_from,
            "date_to": date_to,
            "history": history,
            "limit": limit,
        }
        path = "/v1/history/orders" if history else "/v1/orders"
        data = self._request("GET", path, query=query)
        return [Order.from_dict(item) for item in data.get("orders", [])]

    def list_fills(
        self,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        symbol: str | None = None,
        exchange: str | None = None,
        trade_date: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
        history: bool | None = None,
        limit: int | None = 100,
    ) -> list[Fill]:
        query = {
            "account_id": account_id or self.account_id or None,
            "gateway_order_id": gateway_order_id,
            "symbol": symbol,
            "exchange": exchange,
            "trade_date": trade_date,
            "date_from": date_from,
            "date_to": date_to,
            "history": history,
            "limit": limit,
        }
        path = "/v1/history/fills" if history else "/v1/fills"
        data = self._request("GET", path, query=query)
        return [Fill.from_dict(item) for item in data.get("fills", [])]

    def list_transfers(
        self,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        symbol: str | None = None,
        exchange: str | None = None,
        trade_date: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
        basket_id: str | None = None,
        history: bool | None = None,
        limit: int | None = 100,
    ) -> list[ComponentTransfer]:
        """List ETF component transfers without mixing them into ordinary fills."""

        query = {
            "account_id": account_id or self.account_id or None,
            "gateway_order_id": gateway_order_id,
            "symbol": symbol,
            "exchange": exchange,
            "trade_date": trade_date,
            "date_from": date_from,
            "date_to": date_to,
            "basket_id": basket_id,
            "history": history,
            "limit": limit,
        }
        path = "/v1/history/transfers" if history else "/v1/transfers"
        data = self._request("GET", path, query=query)
        return [ComponentTransfer.from_dict(item) for item in data.get("transfers", [])]

    def record_job_run(
        self,
        report: Mapping[str, Any],
        *,
        job_name: str | None = None,
        trigger: str = "manual",
        status: str | None = None,
        run_id: str | None = None,
        target_trade_date: str | None = None,
        timezone: str | None = None,
        started_at: str | None = None,
        finished_at: str | None = None,
        duration_ms: int | None = None,
    ) -> Mapping[str, Any]:
        """Persist a trading-day job report into relay's local ledger.

        Supported statuses are ``running``, ``succeeded``, ``skipped``, and
        ``failed``. ``completed`` is accepted as an SDK-side alias for
        ``succeeded``.
        """

        normalized_status = JOB_STATUS_ALIASES.get(status or "", status)
        payload = {
            "run_id": run_id or report.get("run_id"),
            "job_name": job_name,
            "target_trade_date": target_trade_date,
            "timezone": timezone,
            "trigger": trigger,
            "status": normalized_status,
            "started_at": started_at or report.get("started_at"),
            "finished_at": finished_at or report.get("finished_at"),
            "duration_ms": duration_ms,
            "report": dict(report),
        }
        data = self._request("POST", "/v1/jobs/runs", json_body=payload)
        return data.get("run", data)

    def record_settlement_snapshot(
        self,
        *,
        trade_date: str,
        account_ids: Iterable[str] | None = None,
        run_id: str | None = None,
        snapshot_type: str = "close",
        source: str = "post_close_settlement",
        dry_run: bool = False,
    ) -> Mapping[str, Any]:
        """Persist post-close asset/position snapshots and a reconciliation run."""

        payload = {
            "run_id": run_id,
            "trade_date": trade_date,
            "account_ids": list(account_ids or ([self.account_id] if self.account_id else [])),
            "snapshot_type": snapshot_type,
            "source": source,
            "dry_run": dry_run,
        }
        return self._request("POST", "/v1/settlements/snapshots", json_body=payload)

    def get_performance_daily(
        self,
        *,
        trade_date: str,
        account_id: str | None = None,
    ) -> Mapping[str, Any]:
        """Return one account's daily close equity and PnL summary."""

        account_id = self._resolve_account(account_id)
        return self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/daily",
            query={"trade_date": trade_date},
        )

    def get_performance_contributions(
        self,
        *,
        trade_date: str | None = None,
        account_id: str | None = None,
    ) -> Mapping[str, Any]:
        """Return read-only security and strategy contribution attribution.

        When ``trade_date`` is omitted, relay resolves the current or most
        recent Meridian trading day.
        """

        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/contributions",
            query={"trade_date": trade_date},
        )
        return data.get("contribution", data)

    def get_trade_quality(
        self,
        *,
        trade_date: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
        account_id: str | None = None,
    ) -> Mapping[str, Any]:
        """Return read-only order execution and ledger consistency quality.

        Use ``trade_date`` for one day, or ``date_from``/``date_to`` for a
        range. Relay reads its local order and fill ledgers and never refreshes
        the broker counter for this request.
        """

        if trade_date and (date_from or date_to):
            raise ValueError("trade_date cannot be combined with date_from or date_to")
        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/trade-quality",
            query={
                "trade_date": trade_date,
                "date_from": date_from,
                "date_to": date_to,
            },
        )
        return data.get("trade_quality", data)

    def get_performance_series(
        self,
        *,
        date_from: str,
        date_to: str,
        account_id: str | None = None,
        benchmark_security_id: str | None = None,
    ) -> Mapping[str, Any]:
        """Return close-equity performance series for an account."""

        account_id = self._resolve_account(account_id)
        return self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/series",
            query={
                "date_from": date_from,
                "date_to": date_to,
                "benchmark_security_id": benchmark_security_id,
            },
        )

    def get_performance_series_csv(
        self,
        *,
        date_from: str,
        date_to: str,
        account_id: str | None = None,
        benchmark_security_id: str | None = None,
    ) -> str:
        """Return the account performance series CSV text."""

        account_id = self._resolve_account(account_id)
        return self._request_text(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/series.csv",
            query={
                "date_from": date_from,
                "date_to": date_to,
                "benchmark_security_id": benchmark_security_id,
            },
        )

    def preview_economic_nav(
        self,
        *,
        trade_date: str,
        account_id: str | None = None,
        status: str | None = None,
    ) -> Mapping[str, Any]:
        """Calculate economic NAV without writing relay's ledger."""

        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/economic-nav/preview",
            query={"trade_date": trade_date, "status": status},
        )
        return data.get("economic_nav", data)

    def rebuild_economic_nav(
        self,
        *,
        trade_date: str,
        account_id: str | None = None,
        status: str = "provisional",
    ) -> Mapping[str, Any]:
        """Recalculate and persist the current economic NAV version.

        Relay only accepts this request when server-side
        ``performance.settings_write_enabled`` is enabled.
        """

        account_id = self._resolve_account(account_id)
        data = self._request(
            "POST",
            f"/v1/accounts/{parse.quote(account_id)}/performance/economic-nav/rebuild",
            json_body={"trade_date": trade_date, "status": status},
        )
        return data.get("economic_nav", data)

    def preview_economic_nav_reconciliation(
        self,
        *,
        trade_date: str,
        account_id: str | None = None,
        observed_trade_date: str | None = None,
    ) -> Mapping[str, Any]:
        """Preview T+1 observed-open-asset reconciliation without writing."""

        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/economic-nav/reconcile",
            query={"trade_date": trade_date, "observed_trade_date": observed_trade_date},
        )
        return data.get("economic_nav_reconciliation", data)

    def rebuild_economic_nav_reconciliation(
        self,
        *,
        trade_date: str,
        account_id: str | None = None,
        observed_trade_date: str | None = None,
    ) -> Mapping[str, Any]:
        """Persist T+1 observed-open-asset reconciliation.

        Relay only accepts this request when server-side
        ``performance.settings_write_enabled`` is enabled.
        """

        account_id = self._resolve_account(account_id)
        data = self._request(
            "POST",
            f"/v1/accounts/{parse.quote(account_id)}/performance/economic-nav/reconcile",
            json_body={"trade_date": trade_date, "observed_trade_date": observed_trade_date},
        )
        return data.get("economic_nav_reconciliation", data)

    def list_economic_nav(
        self,
        *,
        account_id: str | None = None,
        trade_date: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
    ) -> list[Mapping[str, Any]]:
        """Return current versioned economic NAV rows from relay's ledger."""

        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/economic-nav",
            query={"trade_date": trade_date, "date_from": date_from, "date_to": date_to},
        )
        navs = data.get("navs", [])
        return [item for item in navs if isinstance(item, Mapping)]

    def list_nav_reconciliations(
        self,
        *,
        account_id: str | None = None,
        trade_date: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
    ) -> list[Mapping[str, Any]]:
        """Return economic NAV reconciliation rows from relay's ledger."""

        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/nav-reconciliations",
            query={"trade_date": trade_date, "date_from": date_from, "date_to": date_to},
        )
        items = data.get("reconciliations", [])
        return [item for item in items if isinstance(item, Mapping)]

    def confirm_nav_reconciliation(
        self,
        *,
        trade_date: str,
        operator: str,
        account_id: str | None = None,
        reconciliation_id: str | None = None,
        note: str | None = None,
        force: bool = False,
    ) -> Mapping[str, Any]:
        """Confirm T+1 reconciliation and finalize the current economic NAV."""

        return self._review_nav_reconciliation(
            "confirm",
            trade_date=trade_date,
            operator=operator,
            account_id=account_id,
            reconciliation_id=reconciliation_id,
            note=note,
            force=force,
        )

    def block_nav_reconciliation(
        self,
        *,
        trade_date: str,
        operator: str,
        account_id: str | None = None,
        reconciliation_id: str | None = None,
        note: str | None = None,
    ) -> Mapping[str, Any]:
        """Block T+1 reconciliation and mark the current economic NAV blocked."""

        return self._review_nav_reconciliation(
            "block",
            trade_date=trade_date,
            operator=operator,
            account_id=account_id,
            reconciliation_id=reconciliation_id,
            note=note,
            force=False,
        )

    def list_reconciliation_breaks(
        self,
        *,
        run_id: str | None = None,
        account_id: str | None = None,
        status: str | None = None,
        limit: int | None = 100,
    ) -> list[Mapping[str, Any]]:
        """Return post-close reconciliation breaks from relay's ledger."""

        data = self._request(
            "GET",
            "/v1/reconciliations/breaks",
            query={
                "run_id": run_id,
                "account_id": account_id or self.account_id or None,
                "status": status,
                "limit": limit,
            },
        )
        breaks = data.get("breaks", [])
        return [item for item in breaks if isinstance(item, Mapping)]

    def get_meridian_bars(
        self,
        *,
        security_id: str,
        trade_date: str | None = None,
        frequency: str = "1m",
        adjustment: str = "none",
        start_time: str | None = None,
        end_time: str | None = None,
        limit: int | None = 300,
        **extra_query: Any,
    ) -> Mapping[str, Any]:
        """Proxy Meridian market bars through relay.

        Relay forwards Meridian's bars query parameters without redefining the
        schema. If ``trade_date`` is omitted or equals today's Asia/Shanghai
        date, relay will resolve the previous/current trading day before
        querying Meridian.
        """

        query = {
            "security_id": security_id,
            "trade_date": trade_date,
            "frequency": frequency,
            "adjustment": adjustment,
            "start_time": start_time,
            "end_time": end_time,
            "limit": limit,
        }
        query.update(extra_query)
        return self._request("GET", "/v1/meridian/market/bars", query=query)

    def get_meridian_adjust_factors(
        self,
        *,
        security_id: str | None = None,
        security_ids: str | Iterable[str] | None = None,
        trade_date: str | None = None,
        start_date: str | None = None,
        end_date: str | None = None,
        limit: int | None = None,
        **extra_query: Any,
    ) -> Mapping[str, Any]:
        """Proxy Meridian adjustment factors through relay.

        Relay forwards Meridian's metadata parameters as-is and preserves the
        upstream response shape.
        """

        if isinstance(security_ids, str) or security_ids is None:
            joined_security_ids = security_ids
        else:
            joined_security_ids = ",".join(str(item) for item in security_ids)
        query = {
            "security_id": security_id,
            "security_ids": joined_security_ids,
            "trade_date": trade_date,
            "start_date": start_date,
            "end_date": end_date,
            "limit": limit,
        }
        query.update(extra_query)
        return self._request("GET", "/v1/meridian/metadata/adjust-factors", query=query)

    def get_meridian_etf_components(
        self,
        *,
        security_id: str | None = None,
        security_ids: str | Iterable[str] | None = None,
        security_id_pattern: str | None = None,
        component_security_id: str | None = None,
        trade_date: str | None = None,
        start_date: str | None = None,
        end_date: str | None = None,
        limit: int | None = None,
        cursor: str | None = None,
        **extra_query: Any,
    ) -> Mapping[str, Any]:
        """Return Meridian ETF PCF component rows through relay."""

        query = {
            "security_id": security_id,
            "security_ids": _join_query_values(security_ids),
            "security_id_pattern": security_id_pattern,
            "component_security_id": component_security_id,
            "trade_date": trade_date,
            "start_date": start_date,
            "end_date": end_date,
            "limit": limit,
            "cursor": cursor,
        }
        query.update(extra_query)
        return self._request("GET", "/v1/meridian/market/etf-components", query=query)

    def get_meridian_etf_cash_components(
        self,
        *,
        security_id: str | None = None,
        security_ids: str | Iterable[str] | None = None,
        security_id_pattern: str | None = None,
        trade_date: str | None = None,
        start_date: str | None = None,
        end_date: str | None = None,
        limit: int | None = None,
        cursor: str | None = None,
        **extra_query: Any,
    ) -> Mapping[str, Any]:
        """Return Meridian ETF cash components and redemption units through relay."""

        query = {
            "security_id": security_id,
            "security_ids": _join_query_values(security_ids),
            "security_id_pattern": security_id_pattern,
            "trade_date": trade_date,
            "start_date": start_date,
            "end_date": end_date,
            "limit": limit,
            "cursor": cursor,
        }
        query.update(extra_query)
        return self._request("GET", "/v1/meridian/market/etf-cash-components", query=query)

    def get_meridian_etf_pcf_status(self) -> Mapping[str, Any]:
        """Return Meridian ETF PCF synchronization and quality status."""

        return self._request("GET", "/v1/meridian/market/etf-pcf-status")

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
        trade_date: str | None = None,
        strategy_type: str | None = None,
        strategy_id: str | None = None,
        basket_id: str | None = None,
        parent_order_id: str | None = None,
        t0_order_group_id: str | None = None,
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
            "trade_date": trade_date,
            "strategy_type": strategy_type,
            "strategy_id": strategy_id,
            "basket_id": basket_id,
            "parent_order_id": parent_order_id,
            "t0_order_group_id": t0_order_group_id,
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

    def on_order_status(
        self,
        callback: OrderStatusCallback,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        symbol: str | None = None,
        exchange: str | None = None,
        limit: int | None = 100,
        include_snapshot: bool = False,
        dedupe: bool = True,
        daemon: bool = True,
    ) -> CallbackSubscription:
        """Start a background order-status callback subscription."""

        subscription = CallbackSubscription(
            lambda stop_event: self.watch_order_status(
                callback,
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                symbol=symbol,
                exchange=exchange,
                limit=limit,
                include_snapshot=include_snapshot,
                dedupe=dedupe,
                stop_event=stop_event,
            ),
            daemon=daemon,
        )
        return subscription.start()

    def on_fill(
        self,
        callback: FillCallback,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        symbol: str | None = None,
        exchange: str | None = None,
        limit: int | None = 100,
        include_snapshot: bool = False,
        dedupe: bool = True,
        daemon: bool = True,
    ) -> CallbackSubscription:
        """Start a background fill callback subscription."""

        subscription = CallbackSubscription(
            lambda stop_event: self.watch_fills(
                callback,
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                symbol=symbol,
                exchange=exchange,
                limit=limit,
                include_snapshot=include_snapshot,
                dedupe=dedupe,
                stop_event=stop_event,
            ),
            daemon=daemon,
        )
        return subscription.start()

    def on_cancel_rejected(
        self,
        callback: CancelRejectedCallback,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        daemon: bool = True,
    ) -> CallbackSubscription:
        """Start a background callback for rejected or uncertain cancel attempts."""

        subscription = CallbackSubscription(
            lambda stop_event: self.watch_cancel_rejections(
                callback,
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                stop_event=stop_event,
            ),
            daemon=daemon,
        )
        return subscription.start()

    def watch_order_status(
        self,
        callback: OrderStatusCallback,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        symbol: str | None = None,
        exchange: str | None = None,
        limit: int | None = 100,
        include_snapshot: bool = False,
        dedupe: bool = True,
        stop_event: threading.Event | None = None,
    ) -> None:
        """Block and invoke ``callback(order, event)`` when order state changes.

        Returning ``False`` from the callback stops the watch loop.
        """

        seen: dict[str, tuple[Any, ...]] = {}

        def emit(event: RelayEvent) -> bool:
            orders = self.list_orders(
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                symbol=symbol,
                exchange=exchange,
                limit=limit,
            )
            for order in orders:
                key = _order_key(order)
                state = _order_state(order)
                if dedupe and seen.get(key) == state:
                    continue
                seen[key] = state
                if callback(order, event) is False:
                    return False
            return True

        if include_snapshot and not emit(_snapshot_event("order.snapshot")):
            return

        for event in self.stream_events(account_id=account_id):
            if stop_event is not None and stop_event.is_set():
                return
            if event.event_type != "order.changed":
                continue
            if not emit(event):
                return

    def watch_fills(
        self,
        callback: FillCallback,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        symbol: str | None = None,
        exchange: str | None = None,
        limit: int | None = 100,
        include_snapshot: bool = False,
        dedupe: bool = True,
        stop_event: threading.Event | None = None,
    ) -> None:
        """Block and invoke ``callback(fill, event)`` when new fills arrive.

        Returning ``False`` from the callback stops the watch loop.
        """

        seen: set[str] = set()

        def emit(event: RelayEvent) -> bool:
            fills = self.list_fills(
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                symbol=symbol,
                exchange=exchange,
                limit=limit,
            )
            for fill in fills:
                key = _fill_key(fill)
                if dedupe and key in seen:
                    continue
                seen.add(key)
                if callback(fill, event) is False:
                    return False
            return True

        if include_snapshot and not emit(_snapshot_event("fill.snapshot")):
            return

        for event in self.stream_events(account_id=account_id):
            if stop_event is not None and stop_event.is_set():
                return
            if event.event_type != "fill.changed":
                continue
            if not emit(event):
                return

    def watch_cancel_rejections(
        self,
        callback: CancelRejectedCallback,
        *,
        account_id: str | None = None,
        gateway_order_id: str | None = None,
        stop_event: threading.Event | None = None,
    ) -> None:
        """Block and invoke ``callback(event)`` for failed cancel outcomes."""

        for event in self.stream_events(account_id=account_id):
            if stop_event is not None and stop_event.is_set():
                return
            if event.event_type != "order.cancel.rejected":
                continue
            attempt = event.data.get("cancel_attempt")
            if gateway_order_id and (
                not isinstance(attempt, Mapping) or str(attempt.get("gateway_order_id") or "") != gateway_order_id
            ):
                continue
            if callback(event) is False:
                return

    def _refresh(self, kind: str, account_id: str | None) -> CommandReceipt:
        account_id = self._resolve_account(account_id)
        data = self._request("POST", f"/v1/accounts/{parse.quote(account_id)}/{kind}/refresh")
        return CommandReceipt.from_dict(data)

    def _review_nav_reconciliation(
        self,
        action: str,
        *,
        trade_date: str,
        operator: str,
        account_id: str | None,
        reconciliation_id: str | None,
        note: str | None,
        force: bool,
    ) -> Mapping[str, Any]:
        account_id = self._resolve_account(account_id)
        data = self._request(
            "POST",
            f"/v1/accounts/{parse.quote(account_id)}/performance/nav-reconciliations/{parse.quote(action)}",
            json_body={
                "trade_date": trade_date,
                "operator": operator,
                "reconciliation_id": reconciliation_id,
                "note": note,
                "force": force,
            },
        )
        return data.get("nav_reconciliation_review", data)

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

    def _request_text(
        self,
        method: str,
        path: str,
        *,
        query: Mapping[str, Any] | None = None,
    ) -> str:
        response = self._open(method, path, query=query)
        return response.read().decode("utf-8")

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
            "User-Agent": f"relay-sdk/{SDK_VERSION}",
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


def _join_query_values(values: str | Iterable[str] | None) -> str | None:
    if isinstance(values, str) or values is None:
        return values
    return ",".join(str(item) for item in values)


def _snapshot_event(event_type: str) -> RelayEvent:
    return RelayEvent(event_type=event_type, source="relay-sdk")


def _order_key(order: Order) -> str:
    identity = order.gateway_order_id or order.client_order_id or f"{order.order_id}:{order.symbol}"
    return "|".join([order.account_id, order.trade_date, identity])


def _order_state(order: Order) -> tuple[Any, ...]:
    return (
        order.status,
        order.gateway_status,
        order.cum_filled_qty,
        order.leaves_qty,
        order.avg_fill_price,
        order.is_terminal,
        order.reject_message,
    )


def _fill_key(fill: Fill) -> str:
    if fill.fill_id:
        return "|".join([fill.account_id, fill.trade_date, fill.gateway_order_id, fill.fill_id])
    return "|".join(
        [
            fill.account_id,
            fill.trade_date,
            fill.gateway_order_id,
            fill.order_stream_id,
            str(fill.match_timestamp),
            str(fill.qty),
            str(fill.price),
        ]
    )
