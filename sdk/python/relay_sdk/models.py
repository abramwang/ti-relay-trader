"""Data models returned by relay-sdk.

The models expose common relay fields as attributes while keeping the original
JSON object in ``raw`` for forward compatibility.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Mapping


def _text(data: Mapping[str, Any], name: str, default: str = "") -> str:
    value = data.get(name, default)
    if value is None:
        return default
    return str(value)


def _int(data: Mapping[str, Any], name: str, default: int = 0) -> int:
    value = data.get(name, default)
    if value in ("", None):
        return default
    return int(value)


def _float(data: Mapping[str, Any], name: str, default: float = 0.0) -> float:
    value = data.get(name, default)
    if value in ("", None):
        return default
    return float(value)


def _bool(data: Mapping[str, Any], name: str, default: bool = False) -> bool:
    value = data.get(name, default)
    if isinstance(value, bool):
        return value
    if value in ("true", "True", "1", 1):
        return True
    if value in ("false", "False", "0", 0):
        return False
    return default


def _batch_index(order: "Order", fallback: int) -> int:
    value = order.adapter_context.get("batch_index")
    try:
        return int(value) if value is not None else fallback
    except (TypeError, ValueError):
        return fallback


def _order_outcome(order: "Order", *, replayed: bool = False) -> str:
    status = order.status.strip().lower()
    if status == "rejected":
        return "rejected"
    if status and status != "created":
        return "accepted"
    if replayed and order.is_terminal:
        return "accepted"
    return "pending"


@dataclass(frozen=True)
class Account:
    account_id: str = ""
    broker_id: str = ""
    gateway_id: str = ""
    enabled: bool = False
    trading_enabled: bool = False
    simulated: bool = False
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "Account":
        return cls(
            account_id=_text(data, "account_id"),
            broker_id=_text(data, "broker_id"),
            gateway_id=_text(data, "gateway_id"),
            enabled=_bool(data, "enabled"),
            trading_enabled=_bool(data, "trading_enabled"),
            simulated=_bool(data, "simulated"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class Asset:
    account_id: str = ""
    cash_available: float = 0.0
    cash_total: float = 0.0
    net_asset: float = 0.0
    market_value: float = 0.0
    stock_value: float = 0.0
    fund_value: float = 0.0
    day_profit: float = 0.0
    position_profit: float = 0.0
    close_profit: float = 0.0
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "Asset":
        return cls(
            account_id=_text(data, "account_id"),
            cash_available=_float(data, "cash_available"),
            cash_total=_float(data, "cash_total"),
            net_asset=_float(data, "net_asset"),
            market_value=_float(data, "market_value"),
            stock_value=_float(data, "stock_value"),
            fund_value=_float(data, "fund_value"),
            day_profit=_float(data, "day_profit"),
            position_profit=_float(data, "position_profit"),
            close_profit=_float(data, "close_profit"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class Position:
    account_id: str = ""
    trade_date: str = ""
    snapshot_type: str = ""
    symbol: str = ""
    name: str = ""
    exchange: str = ""
    quantity: int = 0
    sellable_qty: int = 0
    initial_qty: int = 0
    today_qty: int = 0
    avg_cost: float = 0.0
    total_cost: float = 0.0
    avg_cost_source: str = ""
    cost_complete: bool = False
    last_price: float = 0.0
    market_value: float = 0.0
    unrealized_pnl: float = 0.0
    day_unrealized_pnl: float = 0.0
    shareholder_id: str = ""
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "Position":
        return cls(
            account_id=_text(data, "account_id"),
            trade_date=_text(data, "trade_date"),
            snapshot_type=_text(data, "snapshot_type"),
            symbol=_text(data, "symbol"),
            name=_text(data, "name"),
            exchange=_text(data, "exchange"),
            quantity=_int(data, "quantity"),
            sellable_qty=_int(data, "sellable_qty"),
            initial_qty=_int(data, "initial_qty"),
            today_qty=_int(data, "today_qty"),
            avg_cost=_float(data, "avg_cost"),
            total_cost=_float(data, "total_cost"),
            avg_cost_source=_text(data, "avg_cost_source"),
            cost_complete=_bool(data, "cost_complete"),
            last_price=_float(data, "last_price"),
            market_value=_float(data, "market_value"),
            unrealized_pnl=_float(data, "unrealized_pnl"),
            day_unrealized_pnl=_float(data, "day_unrealized_pnl"),
            shareholder_id=_text(data, "shareholder_id"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class Order:
    account_id: str = ""
    client_order_id: str = ""
    gateway_order_id: str = ""
    order_id: int = 0
    order_stream_id: str = ""
    trade_date: str = ""
    symbol: str = ""
    name: str = ""
    exchange: str = ""
    trade_side: str = ""
    business_type: str = ""
    limit_price: float = 0.0
    order_qty: int = 0
    cum_filled_qty: int = 0
    leaves_qty: int = 0
    avg_fill_price: float = 0.0
    status: str = ""
    gateway_status: str = ""
    is_terminal: bool = False
    reject_code: str = ""
    reject_message: str = ""
    origin_message_id: str = ""
    request_id: str = ""
    idempotency_key: str = ""
    strategy_type: str = ""
    strategy_id: str = ""
    basket_id: str = ""
    parent_order_id: str = ""
    t0_order_group_id: str = ""
    adapter_context: Mapping[str, Any] = field(default_factory=dict)
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "Order":
        return cls(
            account_id=_text(data, "account_id"),
            client_order_id=_text(data, "client_order_id"),
            gateway_order_id=_text(data, "gateway_order_id"),
            order_id=_int(data, "order_id"),
            order_stream_id=_text(data, "order_stream_id"),
            trade_date=_text(data, "trade_date"),
            symbol=_text(data, "symbol"),
            name=_text(data, "name"),
            exchange=_text(data, "exchange"),
            trade_side=_text(data, "trade_side"),
            business_type=_text(data, "business_type"),
            limit_price=_float(data, "limit_price"),
            order_qty=_int(data, "order_qty"),
            cum_filled_qty=_int(data, "cum_filled_qty"),
            leaves_qty=_int(data, "leaves_qty"),
            avg_fill_price=_float(data, "avg_fill_price"),
            status=_text(data, "status"),
            gateway_status=_text(data, "gateway_status"),
            is_terminal=_bool(data, "is_terminal"),
            reject_code=_text(data, "reject_code"),
            reject_message=_text(data, "reject_message"),
            origin_message_id=_text(data, "origin_message_id"),
            request_id=_text(data, "request_id"),
            idempotency_key=_text(data, "idempotency_key"),
            strategy_type=_text(data, "strategy_type"),
            strategy_id=_text(data, "strategy_id"),
            basket_id=_text(data, "basket_id"),
            parent_order_id=_text(data, "parent_order_id"),
            t0_order_group_id=_text(data, "t0_order_group_id"),
            adapter_context=dict(data.get("adapter_context")) if isinstance(data.get("adapter_context"), Mapping) else {},
            raw=dict(data),
        )

    @property
    def filled_qty(self) -> int:
        return self.cum_filled_qty


@dataclass(frozen=True)
class Fill:
    fill_id: str = ""
    account_id: str = ""
    gateway_order_id: str = ""
    order_id: int = 0
    order_stream_id: str = ""
    symbol: str = ""
    name: str = ""
    exchange: str = ""
    trade_side: str = ""
    business_type: str = ""
    price: float = 0.0
    qty: int = 0
    fee: float = 0.0
    trade_date: str = ""
    match_timestamp: int = 0
    strategy_type: str = ""
    strategy_id: str = ""
    basket_id: str = ""
    parent_order_id: str = ""
    t0_order_group_id: str = ""
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "Fill":
        return cls(
            fill_id=_text(data, "fill_id"),
            account_id=_text(data, "account_id"),
            gateway_order_id=_text(data, "gateway_order_id"),
            order_id=_int(data, "order_id"),
            order_stream_id=_text(data, "order_stream_id"),
            symbol=_text(data, "symbol"),
            name=_text(data, "name"),
            exchange=_text(data, "exchange"),
            trade_side=_text(data, "trade_side"),
            business_type=_text(data, "business_type"),
            price=_float(data, "price"),
            qty=_int(data, "qty"),
            fee=_float(data, "fee"),
            trade_date=_text(data, "trade_date"),
            match_timestamp=_int(data, "match_timestamp"),
            strategy_type=_text(data, "strategy_type"),
            strategy_id=_text(data, "strategy_id"),
            basket_id=_text(data, "basket_id"),
            parent_order_id=_text(data, "parent_order_id"),
            t0_order_group_id=_text(data, "t0_order_group_id"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class OrderFeeRecord:
    account_id: str = ""
    fee_record_id: str = ""
    trade_date: str = ""
    record_scope: str = "order"
    gateway_order_id: str = ""
    order_id: int = 0
    order_stream_id: str = ""
    fill_id: str = ""
    symbol: str = ""
    exchange: str = ""
    trade_side: str = ""
    business_type: str = ""
    order_amount: float = 0.0
    turnover: float = 0.0
    commission: float = 0.0
    stamp_tax: float = 0.0
    transfer_fee: float = 0.0
    handling_fee: float = 0.0
    regulatory_fee: float = 0.0
    settlement_fee: float = 0.0
    other_fee: float = 0.0
    total_fee: float = 0.0
    currency: str = "CNY"
    fee_complete: bool = False
    fee_source: str = "unavailable"
    fee_as_of: str = ""
    settled_at: str = ""
    association_complete: bool = False
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "OrderFeeRecord":
        return cls(
            account_id=_text(data, "account_id"),
            fee_record_id=_text(data, "fee_record_id"),
            trade_date=_text(data, "trade_date"),
            record_scope=_text(data, "record_scope", "order"),
            gateway_order_id=_text(data, "gateway_order_id"),
            order_id=_int(data, "order_id"),
            order_stream_id=_text(data, "order_stream_id"),
            fill_id=_text(data, "fill_id"),
            symbol=_text(data, "symbol"),
            exchange=_text(data, "exchange"),
            trade_side=_text(data, "trade_side"),
            business_type=_text(data, "business_type"),
            order_amount=_float(data, "order_amount"),
            turnover=_float(data, "turnover"),
            commission=_float(data, "commission"),
            stamp_tax=_float(data, "stamp_tax"),
            transfer_fee=_float(data, "transfer_fee"),
            handling_fee=_float(data, "handling_fee"),
            regulatory_fee=_float(data, "regulatory_fee"),
            settlement_fee=_float(data, "settlement_fee"),
            other_fee=_float(data, "other_fee"),
            total_fee=_float(data, "total_fee"),
            currency=_text(data, "currency", "CNY"),
            fee_complete=_bool(data, "fee_complete"),
            fee_source=_text(data, "fee_source", "unavailable"),
            fee_as_of=_text(data, "fee_as_of"),
            settled_at=_text(data, "settled_at"),
            association_complete=_bool(data, "association_complete"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class ComponentTransfer:
    fill_id: str = ""
    account_id: str = ""
    gateway_order_id: str = ""
    order_id: int = 0
    order_stream_id: str = ""
    symbol: str = ""
    name: str = ""
    exchange: str = ""
    price: float = 0.0
    qty: int = 0
    trade_side: str = ""
    business_type: str = ""
    record_type: str = ""
    transfer_type: str = ""
    component_symbol: str = ""
    component_name: str = ""
    component_exchange: str = ""
    component_qty: int = 0
    component_value: float | None = None
    cash_substitution: bool = False
    broker_trade_side: str = ""
    broker_business_type: str = ""
    trade_date: str = ""
    match_timestamp: int = 0
    basket_id: str = ""
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "ComponentTransfer":
        raw_component_value = data.get("component_value")
        component_value = None if raw_component_value in ("", None) else float(raw_component_value)
        return cls(
            fill_id=_text(data, "fill_id"),
            account_id=_text(data, "account_id"),
            gateway_order_id=_text(data, "gateway_order_id"),
            order_id=_int(data, "order_id"),
            order_stream_id=_text(data, "order_stream_id"),
            symbol=_text(data, "symbol"),
            name=_text(data, "name"),
            exchange=_text(data, "exchange"),
            price=_float(data, "price"),
            qty=_int(data, "qty"),
            trade_side=_text(data, "trade_side"),
            business_type=_text(data, "business_type"),
            record_type=_text(data, "record_type"),
            transfer_type=_text(data, "transfer_type"),
            component_symbol=_text(data, "component_symbol"),
            component_name=_text(data, "component_name"),
            component_exchange=_text(data, "component_exchange"),
            component_qty=_int(data, "component_qty"),
            component_value=component_value,
            cash_substitution=_bool(data, "cash_substitution"),
            broker_trade_side=_text(data, "broker_trade_side"),
            broker_business_type=_text(data, "broker_business_type"),
            trade_date=_text(data, "trade_date"),
            match_timestamp=_int(data, "match_timestamp"),
            basket_id=_text(data, "basket_id"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class OrderPage:
    items: tuple[Order, ...] = ()
    count: int = 0
    next_cursor: str = ""
    query: Mapping[str, Any] = field(default_factory=dict)
    request_id: str = ""
    time: str = ""
    is_complete: bool = True
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_envelope(cls, envelope: Mapping[str, Any]) -> "OrderPage":
        data = envelope.get("data") if isinstance(envelope.get("data"), Mapping) else {}
        rows = data.get("orders") if isinstance(data.get("orders"), list) else []
        next_cursor = _text(data, "next_cursor").strip()
        return cls(
            items=tuple(Order.from_dict(item) for item in rows if isinstance(item, Mapping)),
            count=_int(data, "count", len(rows)),
            next_cursor=next_cursor,
            query=dict(data.get("query")) if isinstance(data.get("query"), Mapping) else {},
            request_id=_text(envelope, "request_id"),
            time=_text(envelope, "time"),
            is_complete=not bool(next_cursor),
            raw=dict(envelope),
        )

    @property
    def orders(self) -> tuple[Order, ...]:
        return self.items


@dataclass(frozen=True)
class FillPage:
    items: tuple[Fill, ...] = ()
    count: int = 0
    next_cursor: str = ""
    query: Mapping[str, Any] = field(default_factory=dict)
    request_id: str = ""
    time: str = ""
    is_complete: bool = True
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_envelope(cls, envelope: Mapping[str, Any]) -> "FillPage":
        data = envelope.get("data") if isinstance(envelope.get("data"), Mapping) else {}
        rows = data.get("fills") if isinstance(data.get("fills"), list) else []
        next_cursor = _text(data, "next_cursor").strip()
        return cls(
            items=tuple(Fill.from_dict(item) for item in rows if isinstance(item, Mapping)),
            count=_int(data, "count", len(rows)),
            next_cursor=next_cursor,
            query=dict(data.get("query")) if isinstance(data.get("query"), Mapping) else {},
            request_id=_text(envelope, "request_id"),
            time=_text(envelope, "time"),
            is_complete=not bool(next_cursor),
            raw=dict(envelope),
        )

    @property
    def fills(self) -> tuple[Fill, ...]:
        return self.items


@dataclass(frozen=True)
class PositionPage:
    items: tuple[Position, ...] = ()
    count: int = 0
    next_cursor: str = ""
    query: Mapping[str, Any] = field(default_factory=dict)
    request_id: str = ""
    time: str = ""
    is_complete: bool = True
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_envelope(cls, envelope: Mapping[str, Any]) -> "PositionPage":
        data = envelope.get("data") if isinstance(envelope.get("data"), Mapping) else {}
        rows = data.get("positions") if isinstance(data.get("positions"), list) else []
        next_cursor = _text(data, "next_cursor").strip()
        return cls(
            items=tuple(Position.from_dict(item) for item in rows if isinstance(item, Mapping)),
            count=_int(data, "count", len(rows)),
            next_cursor=next_cursor,
            query=dict(data.get("query")) if isinstance(data.get("query"), Mapping) else {},
            request_id=_text(envelope, "request_id"),
            time=_text(envelope, "time"),
            is_complete=not bool(next_cursor),
            raw=dict(envelope),
        )

    @property
    def positions(self) -> tuple[Position, ...]:
        return self.items


@dataclass(frozen=True)
class CommandReceipt:
    account_id: str = ""
    action: str = ""
    message_id: str = ""
    stream_key: str = ""
    stream_id: str = ""
    idempotency_key: str = ""
    request_id: str = ""
    order: Order | None = None
    orders: tuple[Order, ...] = ()
    cancel_id: str = ""
    replayed: bool = False
    published: Mapping[str, Any] = field(default_factory=dict)
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "CommandReceipt":
        order_data = data.get("order") if isinstance(data.get("order"), Mapping) else None
        orders_data = data.get("orders") if isinstance(data.get("orders"), list) else []
        first_order = orders_data[0] if orders_data and isinstance(orders_data[0], Mapping) else {}
        return cls(
            account_id=_text(data, "account_id") or _text(order_data or {}, "account_id") or _text(first_order, "account_id"),
            action=_text(data, "action"),
            message_id=_text(data, "message_id"),
            stream_key=_text(data, "stream_key"),
            stream_id=_text(data, "stream_id"),
            idempotency_key=_text(data, "idempotency_key"),
            request_id=_text(data, "request_id"),
            order=Order.from_dict(order_data) if order_data else None,
            orders=tuple(Order.from_dict(item) for item in orders_data if isinstance(item, Mapping)),
            cancel_id=_text(data, "cancel_id"),
            replayed=_bool(data, "replayed"),
            published=data.get("published") if isinstance(data.get("published"), Mapping) else {},
            raw=dict(data),
        )

    @property
    def gateway_order_id(self) -> str:
        if self.order:
            return self.order.gateway_order_id
        if self.orders:
            return self.orders[0].gateway_order_id
        return ""

    @property
    def client_order_id(self) -> str:
        if self.order:
            return self.order.client_order_id
        if self.orders:
            return self.orders[0].client_order_id
        return ""

    @property
    def status(self) -> str:
        return self.order.status if self.order else ""


@dataclass(frozen=True)
class BatchOrderOutcome:
    index: int = 0
    account_id: str = ""
    gateway_order_id: str = ""
    client_order_id: str = ""
    idempotency_key: str = ""
    acceptance: str = "accepted"
    outcome: str = "pending"
    order_status: str = ""
    terminal: bool = False
    code: str = ""
    message: str = ""
    order: Order | None = None


@dataclass(frozen=True)
class BatchCommandReceipt(CommandReceipt):
    children: tuple[BatchOrderOutcome, ...] = ()

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "BatchCommandReceipt":
        base = CommandReceipt.from_dict(data)
        acceptance = "replayed" if base.replayed else "accepted"
        children = tuple(
            BatchOrderOutcome(
                index=_batch_index(order, index),
                account_id=order.account_id,
                gateway_order_id=order.gateway_order_id,
                client_order_id=order.client_order_id,
                idempotency_key=order.idempotency_key,
                acceptance=acceptance,
                outcome=_order_outcome(order, replayed=base.replayed),
                order_status=order.status,
                terminal=order.is_terminal,
                code=order.reject_code,
                message=order.reject_message,
                order=order,
            )
            for index, order in enumerate(base.orders)
        )
        return cls(
            account_id=base.account_id,
            action=base.action,
            message_id=base.message_id,
            stream_key=base.stream_key,
            stream_id=base.stream_id,
            idempotency_key=base.idempotency_key,
            request_id=base.request_id,
            order=base.order,
            orders=base.orders,
            cancel_id=base.cancel_id,
            replayed=base.replayed,
            published=base.published,
            raw=base.raw,
            children=children,
        )


@dataclass(frozen=True)
class BatchOrderOutcomes:
    account_id: str = ""
    message_id: str = ""
    children: tuple[BatchOrderOutcome, ...] = ()
    complete: bool = False
    command_status: "CommandStatus | None" = None


@dataclass(frozen=True)
class SchemaCatalog:
    version: str = ""
    capabilities: tuple[str, ...] = ()
    http_routes: tuple[Mapping[str, Any], ...] = ()
    redis_actions: tuple[str, ...] = ()
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "SchemaCatalog":
        capabilities = data.get("capabilities") if isinstance(data.get("capabilities"), list) else []
        routes = data.get("http_routes") if isinstance(data.get("http_routes"), list) else []
        actions = data.get("redis_actions") if isinstance(data.get("redis_actions"), list) else []
        return cls(
            version=_text(data, "version"),
            capabilities=tuple(str(item) for item in capabilities),
            http_routes=tuple(dict(item) for item in routes if isinstance(item, Mapping)),
            redis_actions=tuple(str(item) for item in actions),
            raw=dict(data),
        )

    def supports(self, capability: str) -> bool:
        return str(capability).strip() in self.capabilities


@dataclass(frozen=True)
class CommandReplyStatus:
    message_id: str = ""
    account_id: str = ""
    action: str = ""
    status: str = ""
    code: str = ""
    message: str = ""
    result_type: str = ""
    is_last: bool = False
    request_id: str = ""
    stream_key: str = ""
    stream_id: str = ""
    received_at: str = ""
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "CommandReplyStatus":
        return cls(
            message_id=_text(data, "message_id"),
            account_id=_text(data, "account_id"),
            action=_text(data, "action"),
            status=_text(data, "status"),
            code=_text(data, "code"),
            message=_text(data, "message"),
            result_type=_text(data, "result_type"),
            is_last=_bool(data, "is_last"),
            request_id=_text(data, "request_id"),
            stream_key=_text(data, "stream_key"),
            stream_id=_text(data, "stream_id"),
            received_at=_text(data, "received_at"),
            raw=dict(data),
        )


@dataclass(frozen=True)
class CommandStatus:
    origin_message_id: str = ""
    account_id: str = ""
    action: str = ""
    expected_result_type: str = ""
    state: str = "pending"
    terminal: bool = False
    success: bool = False
    contradictory: bool = False
    reply_count: int = 0
    terminal_count: int = 0
    replies: tuple[CommandReplyStatus, ...] = ()
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "CommandStatus":
        replies = data.get("replies") if isinstance(data.get("replies"), list) else []
        return cls(
            origin_message_id=_text(data, "origin_message_id"),
            account_id=_text(data, "account_id"),
            action=_text(data, "action"),
            expected_result_type=_text(data, "expected_result_type"),
            state=_text(data, "state") or "pending",
            terminal=_bool(data, "terminal"),
            success=_bool(data, "success"),
            contradictory=_bool(data, "contradictory"),
            reply_count=_int(data, "reply_count"),
            terminal_count=_int(data, "terminal_count"),
            replies=tuple(CommandReplyStatus.from_dict(item) for item in replies if isinstance(item, Mapping)),
            raw=dict(data),
        )


@dataclass(frozen=True)
class RelayEvent:
    event_id: str = ""
    event_type: str = ""
    account_ids: tuple[str, ...] = ()
    time: str = ""
    source: str = ""
    stream: str = ""
    last_stream_id: str = ""
    data: Mapping[str, Any] = field(default_factory=dict)
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "RelayEvent":
        account_ids = data.get("account_ids") or []
        if not isinstance(account_ids, list):
            account_ids = []
        event_type = _text(data, "type") or _text(data, "event")
        event_data = data.get("data") if isinstance(data.get("data"), Mapping) else {}
        return cls(
            event_id=_text(data, "id"),
            event_type=event_type,
            account_ids=tuple(str(item) for item in account_ids),
            time=_text(data, "time"),
            source=_text(data, "source"),
            stream=_text(data, "stream"),
            last_stream_id=_text(data, "last_stream_id") or _text(event_data, "last_stream_id"),
            data=event_data,
            raw=dict(data),
        )

    @property
    def id(self) -> str:
        return self.event_id


@dataclass(frozen=True)
class StreamReconciliation:
    account_id: str
    reason: str
    last_event_id: str = ""
    current_event_id: str = ""
    asset: Asset | None = None
    positions: tuple[Position, ...] = ()
    orders: tuple[Order, ...] = ()
    fills: tuple[Fill, ...] = ()
    trigger_event: RelayEvent | None = field(default=None, repr=False)
