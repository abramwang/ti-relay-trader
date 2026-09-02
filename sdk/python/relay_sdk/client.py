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

from .errors import (
    RelayConnectionError,
    RelayError,
    RelayPaginationError,
    RelayStreamDisconnectedError,
    RelayStreamGapError,
    RelayTimeoutError,
    error_from_payload,
)
from .models import (
    Account,
    Asset,
    CommandReceipt,
    ComponentTransfer,
    Fill,
    FillPage,
    Order,
    OrderPage,
    OrderFeeRecord,
    Position,
    PositionPage,
    QueryCommandStatus,
    RelayEvent,
    StreamReconciliation,
)
from .streaming import iter_sse_events


TERMINAL_STATUSES = {"filled", "cancelled", "rejected"}
SDK_VERSION = "0.1.31"
JOB_STATUS_ALIASES = {"completed": "succeeded"}
OrderStatusCallback = Callable[[Order, RelayEvent], object]
FillCallback = Callable[[Fill, RelayEvent], object]
CancelRejectedCallback = Callable[[RelayEvent], object]
StreamReconciliationCallback = Callable[[StreamReconciliation], object]


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

    def get_asset(self, account_id: str | None = None, *, enrich: bool | None = None) -> Asset:
        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/asset",
            query={"enrich": enrich},
        )
        return Asset.from_dict(data.get("asset", data))

    def get_asset_raw(self, account_id: str | None = None) -> Asset:
        """Return the locally stored broker asset without market-data enrichment."""

        return self.get_asset(account_id, enrich=False)

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
        enrich: bool | None = None,
    ) -> list[Position]:
        page = self.get_positions_page(
            account_id,
            symbol=symbol,
            exchange=exchange,
            trade_date=trade_date,
            date_from=date_from,
            date_to=date_to,
            snapshot_type=snapshot_type,
            history=history,
            enrich=enrich,
        )
        return list(page.items)

    def get_positions_page(
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
        enrich: bool | None = None,
        limit: int | None = 500,
        cursor: str | None = None,
    ) -> PositionPage:
        """Return one typed current or historical position page."""

        account_id = self._resolve_account(account_id)
        path = f"/v1/accounts/{parse.quote(account_id)}/positions"
        if history:
            path += "/history"
        envelope = self._request_envelope(
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
                "enrich": enrich,
                "limit": limit,
                "cursor": cursor,
            },
        )
        return PositionPage.from_envelope(envelope)

    def iter_positions(
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
        enrich: bool | None = None,
        page_size: int = 500,
        cursor: str | None = None,
        max_pages: int = 1000,
        max_items: int | None = None,
    ) -> Iterable[Position]:
        """Iterate positions until the server returns an empty cursor."""

        return _iterate_pages(
            lambda next_cursor: self.get_positions_page(
                account_id,
                symbol=symbol,
                exchange=exchange,
                trade_date=trade_date,
                date_from=date_from,
                date_to=date_to,
                snapshot_type=snapshot_type,
                history=history,
                enrich=enrich,
                limit=page_size,
                cursor=next_cursor,
            ),
            cursor=cursor,
            max_pages=max_pages,
            max_items=max_items,
        )

    def get_positions_raw(self, account_id: str | None = None) -> list[Position]:
        """Return locally stored broker positions without names, quotes, or PnL enrichment."""

        return self.get_positions(account_id, enrich=False)

    def refresh_asset(self, account_id: str | None = None) -> CommandReceipt:
        return self._refresh("asset", account_id)

    def refresh_positions(self, account_id: str | None = None) -> CommandReceipt:
        return self._refresh("positions", account_id)

    def refresh_orders(self, account_id: str | None = None) -> CommandReceipt:
        return self._refresh("orders", account_id)

    def refresh_fills(self, account_id: str | None = None) -> CommandReceipt:
        return self._refresh("fills", account_id)

    def refresh_fees(self, account_id: str | None = None) -> CommandReceipt:
        """Ask OC for order-level fees for its current broker trading day."""

        return self._refresh("fees", account_id)

    def get_query_status(self, origin_message_id: str) -> QueryCommandStatus:
        """Return the archived OC reply terminal state for a published query."""

        message_id = str(origin_message_id).strip()
        if not message_id:
            raise ValueError("origin_message_id is required")
        data = self._request("GET", f"/v1/query-status/{parse.quote(message_id, safe='')}")
        return QueryCommandStatus.from_dict(data)

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
        return list(
            self.list_orders_page(
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                symbol=symbol,
                exchange=exchange,
                status=status,
                trade_date=trade_date,
                date_from=date_from,
                date_to=date_to,
                history=history,
                limit=limit,
            ).items
        )

    def list_orders_page(
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
        cursor: str | None = None,
    ) -> OrderPage:
        """Return one typed current or historical order page."""

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
            "cursor": cursor,
        }
        path = "/v1/history/orders" if history else "/v1/orders"
        envelope = self._request_envelope("GET", path, query=query)
        return OrderPage.from_envelope(envelope)

    def iter_orders(
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
        page_size: int = 500,
        cursor: str | None = None,
        max_pages: int = 1000,
        max_items: int | None = None,
    ) -> Iterable[Order]:
        """Iterate orders with cursor-loop, query-drift, and bound checks."""

        return _iterate_pages(
            lambda next_cursor: self.list_orders_page(
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                symbol=symbol,
                exchange=exchange,
                status=status,
                trade_date=trade_date,
                date_from=date_from,
                date_to=date_to,
                history=history,
                limit=page_size,
                cursor=next_cursor,
            ),
            cursor=cursor,
            max_pages=max_pages,
            max_items=max_items,
        )

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
        return list(
            self.list_fills_page(
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                symbol=symbol,
                exchange=exchange,
                trade_date=trade_date,
                date_from=date_from,
                date_to=date_to,
                history=history,
                limit=limit,
            ).items
        )

    def list_fills_page(
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
        cursor: str | None = None,
    ) -> FillPage:
        """Return one typed current or historical fill page."""

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
            "cursor": cursor,
        }
        path = "/v1/history/fills" if history else "/v1/fills"
        envelope = self._request_envelope("GET", path, query=query)
        return FillPage.from_envelope(envelope)

    def iter_fills(
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
        page_size: int = 500,
        cursor: str | None = None,
        max_pages: int = 1000,
        max_items: int | None = None,
    ) -> Iterable[Fill]:
        """Iterate fills with cursor-loop, query-drift, and bound checks."""

        return _iterate_pages(
            lambda next_cursor: self.list_fills_page(
                account_id=account_id,
                gateway_order_id=gateway_order_id,
                symbol=symbol,
                exchange=exchange,
                trade_date=trade_date,
                date_from=date_from,
                date_to=date_to,
                history=history,
                limit=page_size,
                cursor=next_cursor,
            ),
            cursor=cursor,
            max_pages=max_pages,
            max_items=max_items,
        )

    def list_order_fees(
        self,
        account_id: str | None = None,
        *,
        trade_date: str | None = None,
        date_from: str | None = None,
        date_to: str | None = None,
        gateway_order_id: str | None = None,
        fee_complete: bool | None = None,
        limit: int | None = 100,
    ) -> list[OrderFeeRecord]:
        """Return persisted OC order-level actual fee records."""

        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/fees",
            query={
                "trade_date": trade_date,
                "date_from": date_from,
                "date_to": date_to,
                "gateway_order_id": gateway_order_id,
                "fee_complete": fee_complete,
                "limit": limit,
            },
        )
        return [OrderFeeRecord.from_dict(item) for item in data.get("fees", [])]

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

    def list_job_runs(
        self,
        *,
        job_names: Iterable[str] | None = None,
        trade_date: str | None = None,
        limit: int | None = None,
    ) -> list[Mapping[str, Any]]:
        """Return daily-job runs, optionally scoped to one trade date."""

        data = self._request(
            "GET",
            "/v1/jobs/runs",
            query={
                "job_name": ",".join(str(item) for item in (job_names or []) if str(item).strip()) or None,
                "trade_date": trade_date,
                "limit": limit,
            },
        )
        runs = data.get("runs", [])
        return [item for item in runs if isinstance(item, Mapping)]

    def get_daily_review_report(self, *, trade_date: str | None = None) -> Mapping[str, Any]:
        """Return the account-level pre-open/post-close review report."""

        return self._request(
            "GET",
            "/v1/reconciliations/review-report",
            query={"trade_date": trade_date},
        )

    def record_settlement_snapshot(
        self,
        *,
        trade_date: str,
        account_ids: Iterable[str] | None = None,
        run_id: str | None = None,
        snapshot_type: str = "close",
        input_snapshot_type: str | None = None,
        source: str = "post_close_settlement",
        captured_at: str | None = None,
        snapshot_only: bool = False,
        dry_run: bool = False,
    ) -> Mapping[str, Any]:
        """Persist post-close asset/position snapshots and a reconciliation run."""

        payload = {
            "run_id": run_id,
            "trade_date": trade_date,
            "account_ids": list(account_ids or ([self.account_id] if self.account_id else [])),
            "snapshot_type": snapshot_type,
            "input_snapshot_type": input_snapshot_type,
            "source": source,
            "captured_at": captured_at,
            "snapshot_only": snapshot_only,
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

    def preview_cost_ledger(
        self,
        *,
        trade_date: str,
        account_id: str | None = None,
    ) -> Mapping[str, Any]:
        """Calculate the trusted position cost ledger without writing it."""

        account_id = self._resolve_account(account_id)
        data = self._request(
            "GET",
            f"/v1/accounts/{parse.quote(account_id)}/performance/cost-ledger/preview",
            query={"trade_date": trade_date},
        )
        return data.get("cost_ledger", data)

    def rebuild_cost_ledger(
        self,
        *,
        trade_date: str,
        account_id: str | None = None,
    ) -> Mapping[str, Any]:
        """Recalculate and persist the trusted position cost ledger.

        Relay only accepts this request when server-side
        ``performance.settings_write_enabled`` is enabled.
        """

        account_id = self._resolve_account(account_id)
        data = self._request(
            "POST",
            f"/v1/accounts/{parse.quote(account_id)}/performance/cost-ledger/rebuild",
            query={"trade_date": trade_date},
        )
        return data.get("cost_ledger", data)

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

    def get_meridian_instruments(
        self,
        *,
        security_id: str | None = None,
        security_ids: str | Iterable[str] | None = None,
        instrument_type: str | None = None,
        exchange: str | None = None,
        status: str | None = None,
        limit: int | None = None,
        cursor: str | None = None,
        **extra_query: Any,
    ) -> Mapping[str, Any]:
        """Return Meridian ``metadata_instrument.v2`` records through relay.

        The response preserves Meridian's authoritative ``price_tick`` fields.
        Relay's current production scope is SH/SZ; BJ is a future capability.
        """

        query = {
            "security_id": security_id,
            "security_ids": _join_query_values(security_ids),
            "instrument_type": instrument_type,
            "exchange": exchange,
            "status": status,
            "limit": limit,
            "cursor": cursor,
        }
        query.update(extra_query)
        return self._request("GET", "/v1/meridian/metadata/instruments", query=query)

    def get_meridian_metadata_status(self) -> Mapping[str, Any]:
        """Return Meridian ``metadata_status.v2`` price tick quality status."""

        return self._request("GET", "/v1/meridian/metadata/status")

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

    def stream_events(
        self,
        account_id: str | None = None,
        *,
        last_event_id: str | None = None,
        idle_timeout: float = 30.0,
    ) -> Iterable[RelayEvent]:
        """Read one SSE connection, optionally resuming from a server cursor."""

        if idle_timeout <= 0:
            raise ValueError("idle_timeout must be positive")
        account_id = account_id or self.account_id
        query = {"account_id": account_id} if account_id else None
        headers = {"Last-Event-ID": last_event_id} if last_event_id else None
        response = self._open(
            "GET",
            "/v1/events/stream",
            query=query,
            headers=headers,
            timeout=idle_timeout,
        )

        def read_events() -> Iterable[RelayEvent]:
            try:
                yield from iter_sse_events(response)
            finally:
                response.close()

        return read_events()

    def reconcile_current_state(
        self,
        account_id: str | None = None,
        *,
        reason: str = "manual",
        last_event_id: str = "",
        trigger_event: RelayEvent | None = None,
        page_size: int = 500,
        max_pages: int = 1000,
    ) -> StreamReconciliation:
        """Read the complete current ledger after an event-stream discontinuity."""

        account_id = self._resolve_account(account_id)
        asset = self.get_asset_raw(account_id)
        positions = tuple(
            self.iter_positions(
                account_id,
                enrich=False,
                page_size=page_size,
                max_pages=max_pages,
            )
        )
        orders = tuple(
            self.iter_orders(
                account_id=account_id,
                page_size=page_size,
                max_pages=max_pages,
            )
        )
        fills = tuple(
            self.iter_fills(
                account_id=account_id,
                page_size=page_size,
                max_pages=max_pages,
            )
        )
        return StreamReconciliation(
            account_id=account_id,
            reason=reason,
            last_event_id=last_event_id,
            current_event_id=trigger_event.event_id if trigger_event else "",
            asset=asset,
            positions=positions,
            orders=orders,
            fills=fills,
            trigger_event=trigger_event,
        )

    def stream_events_resilient(
        self,
        account_id: str | None = None,
        *,
        last_event_id: str | None = None,
        on_reconcile_required: StreamReconciliationCallback | None = None,
        max_reconnects: int = 5,
        backoff_initial: float = 0.5,
        backoff_max: float = 8.0,
        idle_timeout: float = 30.0,
        reconciliation_page_size: int = 500,
        reconciliation_max_pages: int = 1000,
        stop_event: threading.Event | None = None,
    ) -> Iterable[RelayEvent]:
        """Read SSE with bounded reconnects and mandatory gap reconciliation."""

        account_id = self._resolve_account(account_id)
        if max_reconnects < 0:
            raise ValueError("max_reconnects must be non-negative")
        if backoff_initial < 0 or backoff_max < 0:
            raise ValueError("reconnect backoff must be non-negative")
        cursor = str(last_event_id or "").strip()
        reconnects = 0
        connection_number = 0

        while stop_event is None or not stop_event.is_set():
            connection_number += 1
            reconciled_this_connection = False
            try:
                for event in self.stream_events(
                    account_id=account_id,
                    last_event_id=cursor or None,
                    idle_timeout=idle_timeout,
                ):
                    if stop_event is not None and stop_event.is_set():
                        return

                    requires_reconciliation = event.event_type == "relay.gap" or _event_requires_reconciliation(event)
                    if connection_number > 1 and event.event_type != "relay.heartbeat" and not reconciled_this_connection:
                        requires_reconciliation = True
                    if requires_reconciliation and not reconciled_this_connection:
                        reason = _stream_reconciliation_reason(event, connection_number)
                        if on_reconcile_required is None:
                            raise RelayStreamGapError(
                                f"event stream requires full reconciliation: {reason}",
                                code="EVENT_STREAM_GAP",
                                raw_response=event.raw,
                            )
                        snapshot = self.reconcile_current_state(
                            account_id,
                            reason=reason,
                            last_event_id=cursor,
                            trigger_event=event,
                            page_size=reconciliation_page_size,
                            max_pages=reconciliation_max_pages,
                        )
                        if on_reconcile_required(snapshot) is False:
                            return
                        reconciled_this_connection = True

                    relation = _event_cursor_relation(cursor, event.event_id)
                    if relation == "duplicate":
                        continue
                    if relation == "out_of_order":
                        if not reconciled_this_connection:
                            if on_reconcile_required is None:
                                raise RelayStreamGapError(
                                    "event stream cursor moved backwards",
                                    code="EVENT_STREAM_OUT_OF_ORDER",
                                    raw_response=event.raw,
                                )
                            snapshot = self.reconcile_current_state(
                                account_id,
                                reason="event_out_of_order",
                                last_event_id=cursor,
                                trigger_event=event,
                                page_size=reconciliation_page_size,
                                max_pages=reconciliation_max_pages,
                            )
                            if on_reconcile_required(snapshot) is False:
                                return
                            reconciled_this_connection = True
                        continue
                    if relation == "epoch_changed" and not reconciled_this_connection:
                        if on_reconcile_required is None:
                            raise RelayStreamGapError(
                                "event stream cursor epoch changed without a gap signal",
                                code="EVENT_STREAM_EPOCH_CHANGED",
                                raw_response=event.raw,
                            )
                        snapshot = self.reconcile_current_state(
                            account_id,
                            reason="event_cursor_epoch_changed",
                            last_event_id=cursor,
                            trigger_event=event,
                            page_size=reconciliation_page_size,
                            max_pages=reconciliation_max_pages,
                        )
                        if on_reconcile_required(snapshot) is False:
                            return
                        reconciled_this_connection = True
                    if event.event_id:
                        cursor = event.event_id
                    yield event

                raise RelayConnectionError("relay event stream disconnected before completion")
            except (RelayConnectionError, RelayTimeoutError, socket.timeout, TimeoutError, OSError) as exc:
                reconnects += 1
                if reconnects > max_reconnects:
                    raise RelayStreamDisconnectedError(
                        f"event stream reconnect budget exhausted after {max_reconnects} attempts",
                        code="EVENT_STREAM_RECONNECT_EXHAUSTED",
                        raw_response={"last_event_id": cursor, "cause": str(exc)},
                    ) from exc
                delay = min(backoff_max, backoff_initial * (2 ** (reconnects - 1)))
                if stop_event is not None:
                    if stop_event.wait(delay):
                        return
                elif delay > 0:
                    time.sleep(delay)

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

        page_size = _callback_page_size(limit, 500)

        def emit_orders(orders: Iterable[Order], event: RelayEvent, *, filter_locally: bool = False) -> bool:
            for order in orders:
                if filter_locally and not _order_matches(order, gateway_order_id, symbol, exchange):
                    continue
                key = _order_key(order)
                state = _order_state(order)
                if dedupe and seen.get(key) == state:
                    continue
                seen[key] = state
                if callback(order, event) is False:
                    return False
            return True

        def emit(event: RelayEvent) -> bool:
            return emit_orders(
                self.iter_orders(
                    account_id=account_id,
                    gateway_order_id=gateway_order_id,
                    symbol=symbol,
                    exchange=exchange,
                    page_size=page_size,
                ),
                event,
            )

        def reconcile(snapshot: StreamReconciliation) -> object:
            event = snapshot.trigger_event or _snapshot_event("order.reconciliation")
            return emit_orders(snapshot.orders, event, filter_locally=True)

        if include_snapshot and not emit(_snapshot_event("order.snapshot")):
            return

        for event in self.stream_events_resilient(
            account_id=account_id,
            on_reconcile_required=reconcile,
            stop_event=stop_event,
        ):
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

        page_size = _callback_page_size(limit, 500)

        def emit_fills(fills: Iterable[Fill], event: RelayEvent, *, filter_locally: bool = False) -> bool:
            for fill in fills:
                if filter_locally and not _fill_matches(fill, gateway_order_id, symbol, exchange):
                    continue
                key = _fill_key(fill)
                if dedupe and key in seen:
                    continue
                seen.add(key)
                if callback(fill, event) is False:
                    return False
            return True

        def emit(event: RelayEvent) -> bool:
            return emit_fills(
                self.iter_fills(
                    account_id=account_id,
                    gateway_order_id=gateway_order_id,
                    symbol=symbol,
                    exchange=exchange,
                    page_size=page_size,
                ),
                event,
            )

        def reconcile(snapshot: StreamReconciliation) -> object:
            event = snapshot.trigger_event or _snapshot_event("fill.reconciliation")
            return emit_fills(snapshot.fills, event, filter_locally=True)

        if include_snapshot and not emit(_snapshot_event("fill.snapshot")):
            return

        for event in self.stream_events_resilient(
            account_id=account_id,
            on_reconcile_required=reconcile,
            stop_event=stop_event,
        ):
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

        for event in self.stream_events_resilient(
            account_id=account_id,
            on_reconcile_required=lambda _snapshot: True,
            stop_event=stop_event,
        ):
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
        payload = self._request_envelope(method, path, query=query, json_body=json_body)
        if "data" in payload:
            data = payload.get("data")
            return data if isinstance(data, Mapping) else {"value": data}
        return payload

    def _request_envelope(
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
        headers: Mapping[str, str] | None = None,
        timeout: float | None = None,
    ):
        url = self._url(path, query)
        request_headers = {
            "Accept": "application/json",
            "User-Agent": f"relay-sdk/{SDK_VERSION}",
        }
        data = None
        if json_body is not None:
            data = json.dumps(json_body, separators=(",", ":")).encode("utf-8")
            request_headers["Content-Type"] = "application/json"
        if self.api_key:
            request_headers["Authorization"] = f"Bearer {self.api_key}"
        request_headers.update(headers or {})
        req = request.Request(url, data=data, headers=request_headers, method=method)
        try:
            return self._opener.open(req, timeout=self.timeout if timeout is None else timeout)
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
            filtered[key] = str(value).lower() if isinstance(value, bool) else value
        suffix = "?" + parse.urlencode(filtered, doseq=True) if filtered else ""
        return self.base_url + path + suffix

    @staticmethod
    def _new_id(prefix: str, account_id: str) -> str:
        return f"sdk-{prefix}-{account_id}-{int(time.time() * 1000)}-{uuid.uuid4().hex[:8]}"


def _join_query_values(values: str | Iterable[str] | None) -> str | None:
    if isinstance(values, str) or values is None:
        return values
    return ",".join(str(item) for item in values)


def _iterate_pages(
    load_page: Callable[[str | None], Any],
    *,
    cursor: str | None,
    max_pages: int,
    max_items: int | None,
) -> Iterable[Any]:
    """Build a bounded iterator that fails closed on pagination drift."""

    if max_pages <= 0:
        raise ValueError("max_pages must be positive")
    if max_items is not None and max_items < 0:
        raise ValueError("max_items must be non-negative")

    def generate() -> Iterable[Any]:
        if max_items == 0:
            return

        current_cursor = str(cursor or "").strip()
        seen_cursors = {current_cursor} if current_cursor else set()
        expected_query: str | None = None
        page_count = 0
        item_count = 0

        while True:
            if page_count >= max_pages:
                raise RelayPaginationError(
                    f"pagination exceeded max_pages={max_pages} before reaching an empty cursor",
                    code="PAGINATION_LIMIT_EXCEEDED",
                )

            page = load_page(current_cursor or None)
            page_count += 1
            items = tuple(page.items)
            if page.count != len(items):
                raise RelayPaginationError(
                    f"page count mismatch: server={page.count}, decoded={len(items)}",
                    code="PAGINATION_COUNT_MISMATCH",
                    request_id=page.request_id or None,
                    raw_response=page.raw,
                )

            query_signature = _pagination_query_signature(page.query)
            if query_signature is not None:
                if expected_query is None:
                    expected_query = query_signature
                elif query_signature != expected_query:
                    raise RelayPaginationError(
                        "server normalized query changed while pagination was in progress",
                        code="PAGINATION_QUERY_DRIFT",
                        request_id=page.request_id or None,
                        raw_response=page.raw,
                    )

            for item in items:
                if max_items is not None and item_count >= max_items:
                    return
                yield item
                item_count += 1

            if max_items is not None and item_count >= max_items:
                return

            next_cursor = str(page.next_cursor or "").strip()
            if not next_cursor:
                return
            if next_cursor in seen_cursors:
                raise RelayPaginationError(
                    f"server repeated pagination cursor {next_cursor!r}",
                    code="PAGINATION_CURSOR_LOOP",
                    request_id=page.request_id or None,
                    raw_response=page.raw,
                )
            seen_cursors.add(next_cursor)
            current_cursor = next_cursor

    return generate()


def _pagination_query_signature(query: Mapping[str, Any]) -> str | None:
    if not query:
        return None
    fixed_query = {key: value for key, value in query.items() if key != "cursor"}
    return json.dumps(fixed_query, sort_keys=True, separators=(",", ":"), default=str)


def _callback_page_size(limit: int | None, maximum: int) -> int:
    if limit is None or limit <= 0:
        return maximum
    return min(limit, maximum)


def _order_matches(
    order: Order,
    gateway_order_id: str | None,
    symbol: str | None,
    exchange: str | None,
) -> bool:
    if gateway_order_id and order.gateway_order_id != gateway_order_id:
        return False
    if symbol and symbol not in {order.symbol, f"{order.symbol}.{order.exchange}"}:
        return False
    if exchange and order.exchange.upper() != exchange.upper():
        return False
    return True


def _fill_matches(
    fill: Fill,
    gateway_order_id: str | None,
    symbol: str | None,
    exchange: str | None,
) -> bool:
    if gateway_order_id and fill.gateway_order_id != gateway_order_id:
        return False
    if symbol and symbol not in {fill.symbol, f"{fill.symbol}.{fill.exchange}"}:
        return False
    if exchange and fill.exchange.upper() != exchange.upper():
        return False
    return True


def _event_requires_reconciliation(event: RelayEvent) -> bool:
    value = event.data.get("reconciliation_required")
    return value is True or str(value).lower() in {"1", "true", "yes"}


def _stream_reconciliation_reason(event: RelayEvent, connection_number: int) -> str:
    reason = str(event.data.get("reason") or "").strip()
    if reason:
        return reason
    resume_status = str(event.data.get("resume_status") or "").strip()
    if resume_status:
        return f"stream_{resume_status}"
    if connection_number > 1:
        return "stream_reconnected"
    return "stream_gap"


def _event_cursor_relation(previous: str, current: str) -> str:
    previous = str(previous or "").strip()
    current = str(current or "").strip()
    if not previous or not current:
        return "forward"
    if previous == current:
        return "duplicate"
    previous_parts = _parse_event_cursor(previous)
    current_parts = _parse_event_cursor(current)
    if previous_parts is None or current_parts is None:
        return "forward"
    if previous_parts[0] != current_parts[0]:
        return "epoch_changed"
    if current_parts[1] < previous_parts[1]:
        return "out_of_order"
    return "forward"


def _parse_event_cursor(cursor: str) -> tuple[str, int] | None:
    if not cursor.startswith("evt-") or "-" not in cursor[len("evt-") :]:
        return None
    epoch, sequence_text = cursor[len("evt-") :].rsplit("-", 1)
    try:
        sequence = int(sequence_text)
    except ValueError:
        return None
    if not epoch or sequence < 0:
        return None
    return epoch, sequence


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
