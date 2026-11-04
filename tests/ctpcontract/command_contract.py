"""Offline trading-command candidate; not an HTTP adapter or trading gate."""

import copy
from decimal import Decimal

from query_contract import FORMATS, SCHEMA, StrictValidator, require


def ref(name):
    return {"$ref": f"#/$defs/{name}"}


def obj(properties):
    return {"type": "object", "properties": properties,
            "required": list(properties), "additionalProperties": False}


MARKERS = {"account_id": ref("account"), "product_type": {"const": "futures"},
           "schema_version": {"const": "ctp.f0.draft.2"}}
TRADE = {key: copy.deepcopy(SCHEMA["$defs"]["trade_row"]["properties"][key]) for key in (
    "gateway_order_id", "client_order_id", "trade_date", "symbol", "exchange",
    "business_type", "trade_side", "offset_type", "hedge_type")}
TRADE.update(order_qty=ref("lots"), limit_price=ref("price"))
ENVELOPE = {"protocol": {"const": "relay.stream.v1"}, "message_type": {"const": "command"},
            **{k: ref("id") for k in ("message_id", "request_id", "correlation_id", "idempotency_key")},
            "sent_at": ref("time")}


def schema_for(action):
    if action == "order.submit":
        payload = obj({**MARKERS, **TRADE})
    elif action == "order.batch.submit":
        child = obj({**MARKERS, **TRADE})
        child["required"].remove("product_type")
        child["required"].remove("schema_version")
        payload = obj({**MARKERS, "orders": {"type": "array", "minItems": 1, "items": child}})
    else:
        payload = obj({**MARKERS, "gateway_order_id": ref("id"),
                       "trade_date": ref("day"), "cancel_id": ref("id")})
    schema = obj({**ENVELOPE, "action": {"const": action}, "payload": payload})
    schema["$defs"] = SCHEMA["$defs"]
    return StrictValidator(schema, format_checker=FORMATS)


VALIDATORS = {action: schema_for(action) for action in (
    "order.submit", "order.batch.submit", "order.cancel")}


def validate_command(body, *, routing, trading_day, max_children=100):
    """Trusted stream route and accepted TradingDay are supplied independently."""
    StrictValidator({"$defs": SCHEMA["$defs"], "$ref": "#/$defs/routing"}).validate(routing)
    require(type(max_children) is int and max_children > 0, "invalid local batch bound")
    require(isinstance(body, dict) and isinstance(body.get("action"), str)
            and body["action"] in VALIDATORS, "unsupported command")
    action = body["action"]
    VALIDATORS[action].validate(body)
    payload = body["payload"]
    require(payload["account_id"] == routing["account_id"], "stream account mismatch")
    if action == "order.cancel":
        # Cancel targets an explicit historical order day, not today's new-order day.
        return copy.deepcopy(payload)
    rows = payload["orders"] if action == "order.batch.submit" else [payload]
    require(len(rows) <= max_children, "local batch resource limit")
    gateway_ids, client_ids = set(), set()
    for row in rows:
        require(row["account_id"] == payload["account_id"], "cross-account batch child")
        require(row["trade_date"] == trading_day, "new order TradingDay mismatch")
        require(row["exchange"] == "SHFE", "first-phase exchange not supported")
        price = row["limit_price"]
        require(Decimal(price) > 0 and int(price.replace(".", "")) <= 18446744073709551615,
                "invalid order price")
        for key, seen in (("gateway_order_id", gateway_ids), ("client_order_id", client_ids)):
            require(row[key] not in seen, f"duplicate batch {key}")
            seen.add(row[key])
    # Only the two discriminators may be inherited; explicit null is rejected above.
    result = copy.deepcopy(payload)
    if action == "order.batch.submit":
        for row in result["orders"]:
            row.setdefault("product_type", payload["product_type"])
            row.setdefault("schema_version", payload["schema_version"])
    return result
