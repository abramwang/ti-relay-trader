#!/usr/bin/env python3
"""Read-only verification of the 2026-11-17 OC review, never a state importer."""

import argparse
from collections import Counter, defaultdict
from decimal import Decimal
import hashlib
import json
from pathlib import Path
import struct


def require(condition, description):
    if not condition:
        raise ValueError(description)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def wrapped(path):
    value = json.loads(path.read_bytes())
    require(digest(canonical(value["state"])) == value["sha256"], f"state digest: {path.name}")
    return value["state"]


def identity(parts, version, kind="fill"):
    encoded = f"ctp.identity.{version}/{kind}/" + "".join(f"{len(p.encode())}:{p}" for p in parts)
    prefix = f"ctp-fill-{version}-" if kind == "fill" else "external-ctp-v1-"
    return encoded, prefix + digest(encoded.encode())


def check_selected(evidence, selected):
    fields = evidence["fields_hex"]
    for name, value in selected.items():
        if name == "collected_at":
            expected = evidence[name]
        elif name.endswith("_binary64"):
            expected = bytes.fromhex(fields[name.removesuffix("_binary64")])[::-1].hex()
        elif isinstance(value, dict):
            raw = bytes.fromhex(fields[name])
            expected = {"hex": raw.split(b"\0", 1)[0].hex(), "terminated": b"\0" in raw}
        elif isinstance(value, int):
            expected = int.from_bytes(bytes.fromhex(fields[name]), "little", signed=True)
        else:
            expected = fields[name]
        require(value == expected, f"selected raw mismatch: {name}")


def native(fields, name):
    return bytes.fromhex(fields[name]).split(b"\0", 1)[0].decode("ascii").strip(" ")


def audit(root):
    original = root / "original-exit" / "state"
    before = {p: digest(p.read_bytes()) for p in original.iterdir() if p.is_file()}
    journal = wrapped(original / "commands.v1.json")
    identities = wrapped(original / "identities.v1.json")
    review = json.loads((root / "fill-v2-review.json").read_bytes())
    callbacks = journal["callbacks"]
    require(set(callbacks) == set(review["rows"]), "callback coverage")
    require(len(callbacks) == review["callback_count"] == 50, "callback count")
    for name in ("records", "messages", "deliveries", "conflicts"):
        require(not journal[name], f"unexpected journal {name}")
    for name, filename in (("journal", "commands.v1.json"), ("identity", "identities.v1.json")):
        require(review["sources"][f"{name}_sha256"] == before[original / filename], "source file digest")
    require(review["scope"] == journal["scope"] == identities["scope"], "scope mismatch")
    scope = journal["scope"]
    route = [scope[k] for k in ("env", "broker_id", "account_id", "gateway_id")]
    legacy = defaultdict(lambda: defaultdict(list))
    orders = defaultdict(list)
    fills = []
    outcomes = Counter()
    for callback_id, stored in callbacks.items():
        evidence = stored["evidence"]
        row = review["rows"][callback_id]
        require(digest(canonical(evidence)) == stored["sha256"] == row["original_evidence_sha256"], "callback digest")
        require(row["original_callback_id"] == callback_id, "callback reference")
        require(row["original_outcome"] == stored["outcome"], "outcome overwritten")
        require(row["sequence"] == evidence["sequence"], "sequence changed")
        require(row["original_session"] == evidence["owner"]["counter_session_id"], "session changed")
        require(row["publishable"] is False and row["business_resolved"] is False, "candidate published/resolved")
        require(row["origin_message_id"] is None, "invented command origin")
        check_selected(evidence, row["selected_raw"])
        outcomes[stored["outcome"]] += 1
        fields = evidence["fields_hex"]
        if "candidate_order" in row:
            order = row["candidate_order"]
            key = (native(fields, "TradingDay"), native(fields, "ExchangeID"), native(fields, "OrderSysID"))
            require(tuple(json.loads(row["native_order_key"])) == key, "order key mismatch")
            require(order["symbol"] == native(fields, "InstrumentID"), "order symbol")
            require(order["trade_side"] == {"30": "B", "31": "S"}[fields["Direction"]], "order direction")
            require(order["offset_type"] == {"0": "O", "1": "C", "3": "CT", "4": "CY"}[native(fields, "CombOffsetFlag")], "order offset")
            require(order["order_qty"] == int.from_bytes(bytes.fromhex(fields["VolumeTotalOriginal"]), "little"), "order quantity")
            orders[key].append(order)
        if "candidate_fill" not in row:
            continue
        fill = row["candidate_fill"]
        day, exchange, trade_id, sys_id = (native(fields, k) for k in ("TradingDay", "ExchangeID", "TradeID", "OrderSysID"))
        side = {"30": "B", "31": "S"}[fields["Direction"]]
        parts = route + [day, exchange, trade_id, sys_id, side]
        encoded, fill_id = identity(parts, "v2")
        old_id = identity(parts[:7], "v1")[1]
        require(parts == row["parts"] and encoded == row["encoded"], "v2 bytes mismatch")
        require(fill_id == row["candidate_fill_id"] == fill["fill_id"], "v2 digest mismatch")
        require(old_id == row["legacy_fill_id"], "legacy digest mismatch")
        require(row["candidate_external_order_id"] == identity(route + [day, exchange, sys_id], "v1", "order")[1], "external order identity")
        for target, source in (("trade_id", "TradeID"), ("order_sys_id", "OrderSysID"), ("order_ref", "OrderRef"), ("broker_id", "BrokerID"), ("investor_id", "InvestorID"), ("user_id", "UserID")):
            require(fill["native_ids"][target] == native(fields, source), f"fill native {target}")
        require(fill["trade_side"] == side and fill["trade_date"] == day and fill["exchange"] == exchange, "fill scope")
        require(fill["symbol"] == native(fields, "InstrumentID"), "fill symbol")
        require(fill["offset_type"] == {"30": "O", "31": "C", "33": "CT", "34": "CY"}[fields["OffsetFlag"]], "fill offset")
        require(fill["qty"] == int.from_bytes(bytes.fromhex(fields["Volume"]), "little", signed=True), "fill quantity")
        require(Decimal(fill["price"]) == Decimal(str(struct.unpack("<d", bytes.fromhex(fields["Price"]))[0])), "fill price")
        source_day = native(fields, "TradeDate")
        require(fill["matched_at"] == f"{source_day[:4]}-{source_day[4:6]}-{source_day[6:]}T{native(fields, 'TradeTime')}+08:00", "fill source time")
        require(fill["captured_at"] == evidence["collected_at"], "fill capture time")
        require(fill["fee"] is None and fill["fee_complete"] is False, "invented fees")
        legacy[old_id][fill_id].append(callback_id)
        fills.append(((day, exchange, sys_id), fill))
    require(outcomes == {"pending": 32, "audit_only": 18}, "outcome totals")
    require(len(orders) == 12 and sum(map(len, orders.values())) == 22, "order coverage")
    for key, fill in fills:
        require(any(all(o[k] == fill[k] for k in ("symbol", "trade_side", "offset_type")) and o["order_qty"] >= fill["qty"] for o in orders[key]), "fill/order native association")
    require(dict(legacy) == review["legacy_to_candidates"], "legacy mapping")
    require(len(fills) == len({f[1]["fill_id"] for f in fills}) == 10, "fill coverage")
    require(sum(len(v) > 1 for v in legacy.values()) == 4, "split count")
    observations = identities["observations"]
    require(len(observations) == len(review["legacy_observations"]) == 24, "observation count")
    for item in review["legacy_observations"]:
        old = observations[item["observation_id"]]
        require(digest(canonical(old)) == item["observation_sha256"], "observation hash")
        require(old["evidence_id"] == item["callback_id"] and old["outcome_at_observation"] == item["outcome_at_observation"], "observation linkage")
    previous = ""
    kinds = Counter()
    audit_lines = (original / "readonly.audit.jsonl").read_text().splitlines()
    for index, line in enumerate(audit_lines, 1):
        value = json.loads(line)
        record = value["record"]
        require(digest(canonical(record)) == value["sha256"] and record["previous"] == previous and record["sequence"] == index, "audit chain")
        previous = value["sha256"]
        kinds[record["entry"]["kind"]] += 1
    require(not any(k.startswith("settlement_") for k in kinds), "unexpected settlement audit")
    require(not (original / "settlement-confirmation.once.json").exists(), "unexpected once file in supplied state")
    require(before == {p: digest(p.read_bytes()) for p in before}, "source changed during review")
    return {"status": "offline_evidence_verified", "callbacks": 50, "pending": 32, "audit_only": 18,
            "order_callbacks": 22, "native_orders": 12, "candidate_fills": 10,
            "legacy_keys": 6, "split_legacy_keys": 4, "identity_observations": 24,
            "identity_revision": identities["revision"], "audit_chain_records": len(audit_lines),
            "source_unchanged": True, "runtime_migration_verified": False, "broker_trading_verified": False}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence_directory", type=Path)
    args = parser.parse_args()
    print(json.dumps(audit(args.evidence_directory), indent=2))
