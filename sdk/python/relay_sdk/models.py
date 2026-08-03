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
    reject_message: str = ""
    strategy_type: str = ""
    strategy_id: str = ""
    basket_id: str = ""
    parent_order_id: str = ""
    t0_order_group_id: str = ""
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
            reject_message=_text(data, "reject_message"),
            strategy_type=_text(data, "strategy_type"),
            strategy_id=_text(data, "strategy_id"),
            basket_id=_text(data, "basket_id"),
            parent_order_id=_text(data, "parent_order_id"),
            t0_order_group_id=_text(data, "t0_order_group_id"),
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
        return cls(
            account_id=_text(data, "account_id") or _text(order_data or {}, "account_id"),
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
    def status(self) -> str:
        return self.order.status if self.order else ""


@dataclass(frozen=True)
class QueryReplyStatus:
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
    def from_dict(cls, data: Mapping[str, Any]) -> "QueryReplyStatus":
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
class QueryCommandStatus:
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
    replies: tuple[QueryReplyStatus, ...] = ()
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "QueryCommandStatus":
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
            replies=tuple(QueryReplyStatus.from_dict(item) for item in replies if isinstance(item, Mapping)),
            raw=dict(data),
        )


@dataclass(frozen=True)
class RelayEvent:
    event_type: str = ""
    account_ids: tuple[str, ...] = ()
    time: str = ""
    source: str = ""
    data: Mapping[str, Any] = field(default_factory=dict)
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "RelayEvent":
        account_ids = data.get("account_ids") or []
        if not isinstance(account_ids, list):
            account_ids = []
        event_type = _text(data, "type") or _text(data, "event")
        return cls(
            event_type=event_type,
            account_ids=tuple(str(item) for item in account_ids),
            time=_text(data, "time"),
            source=_text(data, "source"),
            data=data.get("data") if isinstance(data.get("data"), Mapping) else {},
            raw=dict(data),
        )
