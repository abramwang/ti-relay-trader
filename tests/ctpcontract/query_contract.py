"""Offline query-contract checks, not a service decoder or snapshot applicator."""

import datetime as dt
from decimal import Decimal
import hashlib
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker, validators


ROOT = Path(__file__).resolve().parents[2] / "testdata/ctp"


def reject_constant(value):
    raise ValueError(f"non-JSON numeric constant: {value}")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON member: {key}")
        result[key] = value
    return result


def decode_json(text):
    return json.loads(text, object_pairs_hook=unique_object, parse_float=Decimal,
                      parse_constant=reject_constant)


FORMATS = FormatChecker()


@FORMATS.checks("ctp-trading-day", raises=ValueError)
def trading_day(value):
    return isinstance(value, str) and dt.datetime.strptime(value, "%Y%m%d") is not None


@FORMATS.checks("shanghai-time", raises=ValueError)
def shanghai_time(value):
    return isinstance(value, str) and dt.datetime.fromisoformat(value).utcoffset() == dt.timedelta(hours=8)


# JSON Schema's mathematical integer also permits 1.0; this protocol does not.
StrictValidator = validators.extend(
    Draft202012Validator,
    type_checker=Draft202012Validator.TYPE_CHECKER.redefine(
        "integer", lambda checker, value: type(value) is int
    ),
)
SCHEMA = decode_json((ROOT / "query-page-fill-v2.schema.json").read_text())
Draft202012Validator.check_schema(SCHEMA)
VALIDATOR = StrictValidator(SCHEMA, format_checker=FORMATS)


def identity(kind, parts, version="v1"):
    require(version == "v1" or (version == "v2" and kind == "fill"), "unsupported identity version")
    framed = f"ctp.identity.{version}/{kind}/".encode("ascii")
    for part in parts:
        encoded = part.encode("utf-8")
        framed += str(len(encoded)).encode("ascii") + b":" + encoded
    return hashlib.sha256(framed).hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def validate_fill_identity(row, scope_parts):
    ids = row["native_ids"]
    version = "v2" if row["fill_id"].startswith("ctp-fill-v2-") else "v1"
    parts = scope_parts + [ids["trade_id"]]
    if version == "v2":
        require(row["exchange"] == "SHFE", "fill identity v2 requires SHFE")
        parts += [ids["order_sys_id"], row["trade_side"]]
    require(row["fill_id"] == f"ctp-fill-{version}-" + identity("fill", parts, version), "fill identity mismatch")
    return version


def validate_page(body, *, expected=None):
    """Validate one candidate page. Caller-owned context and raw audit are separate."""
    VALIDATOR.validate(body)
    route = body["routing"]
    payload = body["payload"]
    evidence = body["adapter_context"]["query_evidence"]
    if expected is not None:
        for key in ("routing", "action", "origin_message_id"):
            require(body[key] == expected[key], f"trusted context mismatch: {key}")
        for key in ("ticket", "counter_session_id", "connection_generation", "sdk_request_id"):
            require(evidence[key] == expected[key], f"query evidence mismatch: {key}")
        if "trade_date" in payload:
            require(payload["trade_date"] == expected["trade_date"], "unexpected TradingDay")
    if "account_id" in payload:
        require(payload["account_id"] == route["account_id"], "payload account differs from routing")
    if body["status"] == "failed":
        return

    seen = set()
    fill_version = None
    for row in payload["items"]:
        require(row["account_id"] == route["account_id"], "row account differs from routing")
        action = body["action"]
        if action == "account.asset.query":
            scope = row["futures"]["scope"]
            require(scope["trading_day"] == payload["trade_date"], "fund scope TradingDay mismatch")
            key = tuple(scope[k] for k in ("broker_id", "account_id", "currency_id", "biz_type", "trading_day", "settlement_id"))
        elif action == "account.positions.query":
            f = row["futures"]
            key = (row["symbol"], row["exchange"], f["posi_direction"], f["hedge_flag"], f["position_date"], f["invest_unit_id"])
        else:
            require(row["trade_date"] == payload["trade_date"], "trade row TradingDay mismatch")
            ids = row["native_ids"]
            # Row session is historical order evidence, not the current query session.
            scope_parts = [route["env"], route["broker_id"], route["account_id"], route["gateway_id"], row["trade_date"], row["exchange"]]
            gateway_id = row["gateway_order_id"]
            if gateway_id.startswith("external-ctp-v1-"):
                require(bool(ids.get("order_sys_id")), "external order lacks OrderSysID")
                require(gateway_id == "external-ctp-v1-" + identity("order", scope_parts + [ids["order_sys_id"]]), "external order identity mismatch")
            price_key = "limit_price" if action == "order.list.query" else "price"
            price = row[price_key]
            require(int(price.replace(".", "")) <= 18446744073709551615, "price unscaled value exceeds uint64")
            if action == "order.list.query":
                total, filled, leaves = row["order_qty"], row["cum_filled_qty"], row["leaves_qty"]
                require(filled + leaves <= total, "order quantities exceed order size")
                if row["gateway_status"] in ("accepted", "working"):
                    require(filled + leaves == total, "active order quantity bridge mismatch")
                if row["gateway_status"] == "filled":
                    require(filled == total and leaves == 0, "filled order quantity mismatch")
                key = (row["trade_date"], gateway_id)
            else:
                require(Decimal(price) > 0, "ordinary execution requires positive price")
                version = validate_fill_identity(row, scope_parts)
                require(fill_version is None or fill_version == version, "mixed fill identity versions")
                fill_version = version
                require(not row["fee_complete"] or row["fee"] is not None, "complete fee cannot be unknown")
                key = row["fill_id"]
        require(key not in seen, "duplicate business identity within page")
        seen.add(key)


def fixture_pages():
    pages = {}
    for filename in ("messages.json", "query_pages.json"):
        document = decode_json((ROOT / filename).read_text())
        require(document["synthetic_only"] is True, "non-synthetic fixture")
        for case in document["cases"]:
            body = case["body"]
            if body.get("action") in ("account.asset.query", "account.positions.query", "order.list.query", "fill.list.query"):
                pages[case["name"]] = body
    return pages
