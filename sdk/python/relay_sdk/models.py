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
    symbol: str = ""
    name: str = ""
    exchange: str = ""
    quantity: int = 0
    sellable_qty: int = 0
    initial_qty: int = 0
    today_qty: int = 0
    avg_cost: float = 0.0
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
            symbol=_text(data, "symbol"),
            name=_text(data, "name"),
            exchange=_text(data, "exchange"),
            quantity=_int(data, "quantity"),
            sellable_qty=_int(data, "sellable_qty"),
            initial_qty=_int(data, "initial_qty"),
            today_qty=_int(data, "today_qty"),
            avg_cost=_float(data, "avg_cost"),
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
    raw: Mapping[str, Any] = field(default_factory=dict, repr=False)

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "Order":
        return cls(
            account_id=_text(data, "account_id"),
            client_order_id=_text(data, "client_order_id"),
            gateway_order_id=_text(data, "gateway_order_id"),
            order_id=_int(data, "order_id"),
            order_stream_id=_text(data, "order_stream_id"),
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
    price: float = 0.0
    qty: int = 0
    fee: float = 0.0
    trade_date: str = ""
    match_timestamp: int = 0
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
            price=_float(data, "price"),
            qty=_int(data, "qty"),
            fee=_float(data, "fee"),
            trade_date=_text(data, "trade_date"),
            match_timestamp=_int(data, "match_timestamp"),
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
