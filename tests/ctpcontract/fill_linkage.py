"""Offline identity/linkage model, not a wire extension or durable ledger."""

import copy
from datetime import datetime
from decimal import Decimal

from jsonschema import ValidationError

from query_contract import FORMATS, SCHEMA, StrictValidator, decode_json, identity, require


def row_validator(kind):
    definitions = copy.deepcopy(SCHEMA["$defs"])
    if kind == "fill":
        # Internal pending fact only: the published query schema remains unchanged.
        definitions["trade_row"]["required"].remove("gateway_order_id")
    return StrictValidator({"$defs": definitions, "$ref": f"#/$defs/{kind}"},
                           format_checker=FORMATS)


VALIDATORS = {kind: row_validator(kind) for kind in ("order", "fill")}


def source_instant(value):
    if value is None:
        return None
    # Validated +08:00 timestamps allow nanoseconds; datetime alone truncates them.
    seconds, _, fraction = value[:-6].partition(".")
    return datetime.fromisoformat(seconds + value[-6:]), Decimal("0." + fraction) if fraction else Decimal(0)


class FillLinkage:
    def __init__(self, routing, *, native_broker, investor, max_evidence=1024,
                 max_bytes=8 * 1024 * 1024):
        StrictValidator({"$defs": SCHEMA["$defs"], "$ref": "#/$defs/routing"}).validate(routing)
        require(bool(native_broker) and bool(investor), "explicit native authorization required")
        for limit in (max_evidence, max_bytes):
            require(type(limit) is int and limit > 0, "invalid bound")
        self.routing = copy.deepcopy(routing)
        self.native_broker, self.investor = native_broker, investor
        self.max_evidence, self.max_bytes = max_evidence, max_bytes
        self.audit = []
        self.seen = {}
        self.bytes = 0
        self.conflicts = []
        self.orders = {}
        self.native_orders = {}
        self.fills = {}

    def _conflict(self, reason):
        if reason not in self.conflicts:
            self.conflicts.append(reason)
        raise ValueError(reason)

    def _scope(self, row):
        return [self.routing[k] for k in ("env", "broker_id", "account_id", "gateway_id")] + [row["trade_date"], row["exchange"]]

    def _native_order(self, row):
        sysid = row["native_ids"].get("order_sys_id")
        return tuple(self._scope(row) + [sysid]) if sysid else None

    @staticmethod
    def _economics(row):
        return tuple(row[k] for k in ("symbol", "exchange", "business_type", "trade_side", "offset_type", "hedge_type"))

    def observe(self, evidence_id, kind, routing, text):
        """Caller supplies source provenance and raw row text, not broker credentials."""
        require(routing == self.routing, "foreign routing")
        require(kind in VALIDATORS, "unknown observation kind")
        require(isinstance(evidence_id, str) and bool(evidence_id), "missing evidence identity")
        old = self.seen.get(evidence_id)
        if old == (kind, text):
            return "replayed"
        size = len(text.encode("utf-8"))
        if len(self.audit) >= self.max_evidence or self.bytes + size > self.max_bytes:
            self._conflict("resource limit; caller must retain unaccepted evidence")
        self.audit.append((evidence_id, kind, text))
        self.bytes += size
        if old is not None:
            self._conflict("evidence identity reused with different contents")
        self.seen[evidence_id] = (kind, text)
        try:
            row = decode_json(text)
            VALIDATORS[kind].validate(row)
            require(row["account_id"] == self.routing["account_id"], "logical account mismatch")
            ids = row["native_ids"]
            require(ids["broker_id"] == self.native_broker and ids["investor_id"] == self.investor,
                    "native investor authorization mismatch")
            price = row["price" if kind == "fill" else "limit_price"]
            require(int(price.replace(".", "")) <= 18446744073709551615, "price exceeds uint64")
            hint = row.get("gateway_order_id")
            if hint and hint.startswith("external-ctp-v1-"):
                require(bool(ids.get("order_sys_id")), "external order requires OrderSysID")
                require(hint == "external-ctp-v1-" + identity("order", self._scope(row) + [ids["order_sys_id"]]),
                        "external identity mismatch")
            if kind == "fill":
                require(Decimal(price) > 0, "ordinary fill price must be positive")
                require(not row["fee_complete"] or row["fee"] is not None, "complete fee is missing")
                require(row["fill_id"] == "ctp-fill-v1-" + identity("fill", self._scope(row) + [ids["trade_id"]]),
                        "fill identity mismatch")
                self._fill(row)
            else:
                total, filled, leaves = row["order_qty"], row["cum_filled_qty"], row["leaves_qty"]
                require(filled + leaves <= total, "order quantities exceed total")
                if row["gateway_status"] in ("working", "accepted"):
                    require(filled + leaves == total, "active order quantities mismatch")
                if row["gateway_status"] == "filled":
                    require(filled == total and leaves == 0, "filled order quantities mismatch")
                self._order(row)
        except (ValueError, ValidationError) as exc:
            self._conflict(str(exc))
        self._reconcile()
        return "conflict" if self.conflicts else "observed"

    def _order(self, row):
        key = (row["trade_date"], row["gateway_order_id"])
        native = self._native_order(row)
        existing = self.orders.get(key)
        if native is not None:
            require(self.native_orders.get(native, key) == key, "native order claimed by two gateway IDs")
        if existing:
            require(self._economics(existing) == self._economics(row)
                    and existing["order_qty"] == row["order_qty"]
                    and Decimal(existing["limit_price"]) == Decimal(row["limit_price"]),
                    "order identity has conflicting economics")
            previous_native = self._native_order(existing)
            require(previous_native is None or native is None or previous_native == native,
                    "gateway identity changed OrderSysID")
            # Identity anchor only. Never adjudicate current status from arrival order.
            if previous_native is None and native is not None:
                existing["native_ids"]["order_sys_id"] = row["native_ids"]["order_sys_id"]
        else:
            self.orders[key] = copy.deepcopy(row)
        if native is not None:
            self.native_orders[native] = key

    def _fill(self, row):
        key = row["fill_id"]
        core = self._economics(row) + (self._native_order(row), row["qty"], Decimal(row["price"]))
        record = self.fills.get(key)
        if record:
            require(record["core"] == core, "same TradeID has conflicting execution facts")
            known_times = {source_instant(r["matched_at"]) for r in record["observations"] if r["matched_at"] is not None}
            require(row["matched_at"] is None or not known_times or source_instant(row["matched_at"]) in known_times,
                    "same TradeID has conflicting source time")
            record["observations"].append(copy.deepcopy(row))
        else:
            self.fills[key] = {"core": core, "observations": [copy.deepcopy(row)],
                               "order_key": None, "state": "pending"}

    def _reconcile(self):
        totals = {}
        for record in self.fills.values():
            row = record["observations"][0]
            native = self._native_order(row)
            order_key = self.native_orders.get(native)
            if order_key is None:
                continue
            order = self.orders[order_key]
            alias = "external-ctp-v1-" + identity("order", list(native))
            hints = {r["gateway_order_id"] for r in record["observations"] if "gateway_order_id" in r}
            if self._economics(order) != self._economics(row) or not hints.issubset({order_key[1], alias}):
                record["state"] = "conflict"
                if "fill/order association conflict" not in self.conflicts:
                    self.conflicts.append("fill/order association conflict")
                continue
            record["order_key"], record["state"] = order_key, "linked"
            totals[order_key] = totals.get(order_key, 0) + row["qty"]
        for key, qty in totals.items():
            if qty > self.orders[key]["order_qty"]:
                for record in self.fills.values():
                    if record["order_key"] == key:
                        record["state"] = "conflict"
                if "unique fills exceed order quantity" not in self.conflicts:
                    self.conflicts.append("unique fills exceed order quantity")

    def report(self):
        pending = sum(record["state"] == "pending" for record in self.fills.values())
        return {"order_count": len(self.orders), "unique_fill_count": len(self.fills),
                "pending_count": pending, "blocked": bool(pending or self.conflicts),
                "conflicts": list(self.conflicts),
                "links": {key: {"state": record["state"], "order_key": record["order_key"]}
                          for key, record in self.fills.items()}}
