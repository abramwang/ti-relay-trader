"""Conservative offline order evidence merge; no event clock or new wire fields."""

import copy

from fill_linkage import FillLinkage, source_instant
from query_contract import decode_json, require


TERMINAL = {"filled", "cancelled", "rejected"}
ACTIVE_RANK = {"accepted": 0, "working": 1}


class OrderProjection:
    def __init__(self, routing, *, native_broker, investor, trade_date, gateway_order_id):
        self.evidence = FillLinkage(routing, native_broker=native_broker, investor=investor)
        self.key = (trade_date, gateway_order_id)
        self.current = None
        self.known_submission_time = None
        self.conflicts = []

    def _conflict(self, reason):
        if reason not in self.conflicts:
            self.conflicts.append(reason)
        return "conflict"

    def observe(self, evidence_id, text):
        row = decode_json(text)
        require(isinstance(row, dict) and (row.get("trade_date"), row.get("gateway_order_id")) == self.key,
                "foreign order evidence")
        outcome = self.evidence.observe(evidence_id, "order", self.evidence.routing, text)
        if outcome == "replayed":
            return outcome
        if self.conflicts or self.evidence.conflicts:
            return "conflict"
        known = source_instant(row["submitted_at"])
        if known is not None:
            if self.known_submission_time is not None and self.known_submission_time != known:
                return self._conflict("conflicting known submission time")
            self.known_submission_time = known
        if self.current is None:
            self.current = copy.deepcopy(row)
            return "selected"
        old = self.current
        before, after = old["gateway_status"], row["gateway_status"]
        old_qty, new_qty = old["cum_filled_qty"], row["cum_filled_qty"]
        if before in TERMINAL:
            if after not in TERMINAL:
                return "stale" if new_qty <= old_qty else self._conflict("active quantity exceeds terminal evidence")
            if before != after or (old_qty, old["leaves_qty"]) != (new_qty, row["leaves_qty"]):
                return self._conflict("contradictory terminal evidence")
            return "equivalent"
        if after in TERMINAL:
            if new_qty < old_qty:
                return self._conflict("terminal cumulative quantity regressed")
        else:
            state_delta = ACTIVE_RANK[after] - ACTIVE_RANK[before]
            quantity_delta = new_qty - old_qty
            if state_delta * quantity_delta < 0:
                return self._conflict("incomparable state and cumulative evidence")
            if state_delta < 0 or quantity_delta < 0:
                return "stale"
            if state_delta == 0 and quantity_delta == 0:
                return "equivalent"
        self.current = copy.deepcopy(row)
        return "selected"

    def view(self):
        return {"last_uncontested": copy.deepcopy(self.current),
                "blocked": bool(self.conflicts or self.evidence.conflicts),
                "conflicts": list(self.conflicts) + list(self.evidence.conflicts)}
