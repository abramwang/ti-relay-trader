#!/usr/bin/env python3
"""Fixed-scope broker cash timing review, not a settlement/NAV writer.

Uses existing strict broker parsers and a read-only PG redemption snapshot.
No network, account commands, recurring import, or estimated private cash.
"""

import argparse
import collections
import hashlib
import importlib.util
import json
import os
import re
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

SPEC = importlib.util.spec_from_file_location(
    "cash_review", Path(__file__).with_name("audit-fuying13-cross-day-settlement.py"))
OLD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(OLD)
EXT, B = OLD.EXT, OLD.B
START, END = "20261012", "20261109"


def build_order_index(snapshot, executions):
    if (snapshot.get("database_name") != "relay_trader"
            or snapshot.get("transaction_read_only") != "on"
            or snapshot.get("account_id") != B.ACCOUNT
            or snapshot.get("date_from") != "20260601" or snapshot.get("date_to") != END
            or type(snapshot.get("count")) is not int
            or snapshot.get("count") != len(snapshot["orders"])):
        raise ValueError("wrong or incomplete read-only redemption snapshot")
    index = {}
    seen = set()
    for order in snapshot["orders"]:
        day = B.date(order["trade_date"].replace("-", ""))
        stream = order["order_stream_id"]
        prefixes = {"SH": ("11001",), "SZ": ("12001", "12002")}.get(order["exchange"])
        if (order["account_id"] != B.ACCOUNT or not prefixes or not stream.startswith(prefixes)
                or not re.fullmatch(r"[A-Za-z0-9]+", stream[5:])
                or order["trade_side"] != "R" or order["business_type"] != "E"
                or type(order["is_terminal"]) is not bool
                or type(order["cum_filled_qty"]) is not int or order["cum_filled_qty"] <= 0
                or not "20260601" <= day <= END or not re.fullmatch(r"\d{6}", order["symbol"])):
            raise ValueError("invalid redemption identity/scope")
        identity = (day, order["gateway_order_id"])
        key = (day, order["symbol"], stream[5:])
        if not identity[1] or identity in seen or key in index:
            raise ValueError("duplicate redemption identity")
        seen.add(identity)
        index[key] = {"source_trade_date": day, "security_id": order["symbol"] + "." + order["exchange"],
                      "broker_order_reference": stream[5:], "gateway_order_id": identity[1],
                      "quantity": order["cum_filled_qty"], "is_terminal": order["is_terminal"],
                      "pg_evidence": True, "evidence_conflict": False, "broker_execution_rows": []}
    broker = collections.defaultdict(list)
    reference_dates = collections.defaultdict(set)
    for number, row in executions:
        if row["方向"] != "ETF赎回" or B.quantity(row["成交量"]) <= 0:
            continue
        day = B.date(row["日期"])
        if not "20260601" <= day <= END:
            continue
        reference_dates[row["委托编号"]].add(day)
        security = row["代码"]
        if not re.fullmatch(r"\d{6}\.(SH|SZ)", security) or not row["委托编号"]:
            raise ValueError("invalid broker redemption")
        broker[(day, security[:6], row["委托编号"])].append((number, row))
    for key, rows in broker.items():
        # Broker ETF redemption exports also label constituent transfers as
        # "ETF redemption". Only PG's actual parent orders define the ETF here.
        if key not in index:
            continue
        qty = sum(B.quantity(row["成交量"]) for _, row in rows)
        security = rows[0][1]["代码"]
        if any(row["代码"] != security for _, row in rows):
            raise ValueError("broker identity crosses exchanges")
        if index[key]["quantity"] != qty or index[key]["security_id"] != security:
            index[key]["evidence_conflict"] = True
        index[key]["broker_quantity"] = qty
        index[key]["broker_execution_rows"] = [n for n, _ in rows]
    for order in index.values():
        order["broker_reference_dates"] = sorted(reference_dates[order["broker_order_reference"]])
    return list(index.values())


def parse_leg(row):
    raw = row["raw_fields"]
    text, subject = raw["操作摘要"], raw["业务科目"]
    # Pattern-specific identities distinguish ETF parents from component codes.
    patterns = [
        (r"深圳ETF赎回(\d{6})佣金:([A-Za-z0-9]+)", "redemption_fee", "unknown type", (1, 2, None, None)),
        (r"(\d{6})ETF赎回，申报委托号([A-Za-z0-9]+)", "redemption_fee", "unknown type", (1, 2, None, None)),
        (r"(\d{6})ETF赎回过户费，申报委托号([A-Za-z0-9]+)", "transfer_fee", "unknown type", (None, 2, None, 1)),
        (r"深圳ETF赎回(\d{6})\((\d{6})\)过户费，成交日(\d{8})", "transfer_fee", "unknown type", (1, None, 3, 2)),
        (r"(\d{6})ETF赎回替代现金，申报委托号([A-Za-z0-9]+)", "replacement_cash", "ETF赎回替代现金划入", (1, 2, None, None)),
        (r"深圳ETF赎回(\d{6})\((\d{6})\)替代现金:([A-Za-z0-9]+)", "replacement_cash", "ETF赎回替代现金划入", (1, 3, None, 2)),
        (r"(\d{8})ETF申赎(\d{6})替代现金退款，股票代码(\d{6})", "refund", "ETF现金替代退款", (2, None, 1, 3)),
        (r"(\d{8})ETF申赎(\d{6})替代现金补款，股票代码(\d{6})", "supplement", "ETF现金替代补款", (2, None, 1, 3)),
        (r"深圳ETF申赎(\d{6})退补款代收付，成交日(\d{8})，([A-Za-z0-9]+)，成份股:(\d+)", "refund_or_supplement", None, (1, 3, 2, None)),
        (r"深圳ETF申赎(\d{6})现金差额代收付，成交日(\d{8})，([A-Za-z0-9]+)", "cash_component", None, (1, 3, 2, None)),
        (r"B\d+ETF现金差额场外交收，证券代码:(\d{6})", "cash_component_without_origin", None, (1, None, None, None)),
    ]
    for pattern, kind, want_subject, fields in patterns:
        match = re.fullmatch(pattern, text)
        if not match:
            continue
        if want_subject is not None and subject != want_subject:
            raise ValueError("cash subject conflicts with summary")
        code, ref, origin, component = (match[n] if n else None for n in fields)
        if kind == "refund_or_supplement":
            kind = {"ETF现金替代退款": "refund", "ETF现金替代补款": "supplement"}.get(subject)
            if kind is None:
                raise ValueError("unexpected refund/supplement subject")
        amount = B.cents(row["actual_amount"])
        incoming = kind in ("refund", "replacement_cash")
        outgoing = kind in ("redemption_fee", "transfer_fee", "supplement")
        if kind.startswith("cash_component"):
            if subject not in ("ETF现金差额划入", "ETF现金差额划出"):
                raise ValueError("cash component subject mismatch")
            incoming, outgoing = subject.endswith("划入"), subject.endswith("划出")
        if (incoming and amount <= 0) or (outgoing and amount >= 0):
            raise ValueError("cash direction disagrees with subject")
        raw_code = component if kind == "transfer_fee" else code
        if raw_code and raw["股票代码"] != raw_code:
            raise ValueError("parent/component code mismatch")
        if origin != row["source_trade_date"]:
            raise ValueError("explicit source parser disagreement")
        return {"kind": kind, "security_code": code, "order_reference": ref,
                "explicit_source_trade_date": origin, "component_code": component}
    return {"kind": "unrecognized", "security_code": None, "order_reference": None,
            "explicit_source_trade_date": None, "component_code": None}


def resolve_leg(row, orders):
    parsed = parse_leg(row)
    result = {**row, **parsed, "resolved_source_trade_date": None,
              "association_scope": "unresolved", "candidate_orders": []}
    ref, origin, code = (parsed[k] for k in ("order_reference", "explicit_source_trade_date", "security_code"))
    if not ref and not origin:
        result["reason"] = "missing_private_source_or_order" if parsed["kind"] != "unrecognized" else "unknown_cash_semantics"
        return result
    candidates = [o for o in orders if (not code or o["security_id"][:6] == code)
                  and (not ref or o["broker_order_reference"] == ref)
                  and (not origin or o["source_trade_date"] == origin)]
    dates = {o["source_trade_date"] for o in candidates}
    securities = {o["security_id"] for o in candidates}
    result["candidate_orders"] = candidates
    if ref and not origin and any(set(o["broker_reference_dates"]) - dates for o in candidates):
        result["reason"] = "order_reference_reused_in_broker_history"
        return result
    if any(o["evidence_conflict"] for o in candidates):
        result["reason"] = "redemption_evidence_conflict"
        return result
    if not candidates or len(dates) != 1 or len(securities) != 1 or (ref and len(candidates) != 1):
        result["reason"] = "missing_or_nonunique_redemption_identity"
        return result
    day = next(iter(dates))
    if day > row["raw_fields"]["交易日期"]:
        raise ValueError("redemption occurs after posted cash")
    result.update(resolved_source_trade_date=day, security_id=next(iter(securities)),
                  association_scope="order" if ref else "account_source_day_etf",
                  reason="explicit_source_and_order" if ref and origin else "explicit_source" if origin else "unique_order_reference_in_reviewed_coverage")
    return result


def timing_path(resolved, funds):
    EXT.unique_rows(resolved)
    events = collections.defaultdict(int)
    for row in resolved:
        source, posted = row["resolved_source_trade_date"], row["raw_fields"]["交易日期"]
        if not source or B.date(source) > B.date(posted):
            raise ValueError("invalid source/posting timing")
        amount = B.cents(row["actual_amount"])
        events[source] += amount
        events[posted] -= amount
    running = 0
    balances = {}
    for day in sorted(set(events) | set(funds)):
        running += events[day]
        balances[day] = running
    if running != 0:
        raise ValueError("observed timing legs do not release")
    result, previous = [], None
    for day in sorted(funds):
        if previous is not None and START <= day <= END:
            opening, closing = balances[previous], balances[day]
            result.append({"trade_date": day, "known_open_carry": B.money(opening),
                           "known_close_carry": B.money(closing), "known_timing_pnl_adjustment": B.money(closing-opening),
                           "reported_broker_pnl": funds[day]["当日盈亏"],
                           "conditional_pnl_before_overlap_review": B.money(B.cents(funds[day]["当日盈亏"]) + closing-opening),
                           "asset_overlap_verified": False, "economic_nav_publishable": False})
        previous = day
    return result


def grouped_legs(rows):
    groups = collections.defaultdict(list)
    for row in rows:
        key = (row["resolved_source_trade_date"] or "unresolved", row["raw_fields"]["交易日期"],
               row.get("security_id") or row.get("security_code") or "unknown", row["kind"], row["association_scope"])
        groups[key].append(row)
    return [{"source_trade_date": k[0], "posted_date": k[1], "security_id": k[2], "kind": k[3],
             "association_scope": k[4], "rows": len(v),
             "amount": B.money(sum(B.cents(r["actual_amount"]) for r in v)),
             "flow_ids": sorted(r["broker_flow_id"] for r in v)} for k, v in sorted(groups.items())]


def cash_pcf_review(resolved, unresolved, orders, evidence):
    scoped = [o for o in orders if START <= o["source_trade_date"] <= END]
    expected_keys = {(o["source_trade_date"], o["security_id"]) for o in scoped}
    pcfs = {}
    for item in evidence["responses"]:
        query = item["query"]
        row = OLD.SAME.one(OLD.body(item["response"]), "cash PCF")
        source, security = str(row["pre_trade_date"]), row["security_id"]
        key = (source, security)
        if (key in pcfs or key not in expected_keys or row["schema_version"] != "etf_cash_component.v1"
                or source != item["source_trade_date"] or security != query["security_id"]
                or str(row["trade_date"]) != query["trade_date"] or B.date(source) >= B.date(query["trade_date"])):
            raise ValueError("cash PCF scope changed")
        unit = B.quantity(str(row["unit_subscribe_redeem"]))
        if unit <= 0:
            raise ValueError("invalid PCF redemption unit")
        pcfs[key] = (unit, B.cents(str(row["cash_component"])))
    if set(pcfs) != expected_keys:
        raise ValueError("cash PCF coverage incomplete")
    verified = []
    for leg in resolved:
        if leg["kind"] != "cash_component":
            continue
        if leg["association_scope"] != "order" or len(leg["candidate_orders"]) != 1:
            raise ValueError("cash component needs unique parent")
        order = leg["candidate_orders"][0]
        unit, amount = pcfs[(order["source_trade_date"], order["security_id"])]
        if order["quantity"] % unit or B.cents(leg["actual_amount"]) != amount * (order["quantity"] // unit):
            raise ValueError("actual cash component disagrees with PCF/basket units")
        verified.append({"flow_id": leg["broker_flow_id"], "source_trade_date": order["source_trade_date"],
                         "security_id": order["security_id"], "gateway_order_id": order["gateway_order_id"],
                         "basket_units": order["quantity"] // unit, "actual_amount": leg["actual_amount"]})
    candidates = []
    for group in grouped_legs(unresolved):
        if group["kind"] != "cash_component_without_origin":
            continue
        legs = [r for r in unresolved if r["broker_flow_id"] in group["flow_ids"]]
        observed = collections.Counter(B.cents(r["actual_amount"]) for r in legs)
        matches = []
        for (source, security), (unit, amount) in sorted(pcfs.items()):
            if security[:6] != group["security_id"] or source >= group["posted_date"]:
                continue
            parents = [o for o in scoped if o["source_trade_date"] == source and o["security_id"] == security]
            if any(o["quantity"] % unit for o in parents):
                continue
            expected = collections.Counter(amount * (o["quantity"] // unit) for o in parents)
            if expected == observed:
                matches.append({"source_trade_date": source, "security_id": security,
                                "gateway_order_ids": [o["gateway_order_id"] for o in parents]})
        candidates.append({**group, "pcf_order_multiset_candidates": matches,
                           "private_source_verified": False, "included_in_known_carry": False})
    return {"pcf_groups_checked": len(pcfs), "explicit_cash_component_rows_checked": len(verified),
            "explicit_cash_components": verified, "unresolved_candidates": candidates,
            "private_settlement_complete": False}


def audit(reference, snapshot, pcf):
    paths = {k: reference / f"{B.ACCOUNT}_{v}.csv" for k, v in B.FILES.items()}
    hashes = {k: hashlib.sha256(p.read_bytes()).hexdigest() for k, p in paths.items()}
    if hashes != OLD.HASHES:
        raise ValueError("broker exports changed; review new evidence before use")
    cash = EXT.read_cash(paths["cash_flows"])
    EXT.unique_rows(cash)
    funds, bridges = EXT.fund_bridges(list(B.rows(paths["funds"], "funds")), cash)
    orders = build_order_index(snapshot, list(B.rows(paths["executions"], "executions")))
    selected = [r for r in cash if r["etf_related"] and START <= r["raw_fields"]["交易日期"] <= END]
    resolved, unresolved = [], []
    for row in selected:
        leg = resolve_leg(row, orders)
        (resolved if leg["resolved_source_trade_date"] else unresolved).append(leg)
    path = timing_path(resolved, funds)
    pcf_review = cash_pcf_review(resolved, unresolved, orders, pcf)
    if hashes != {k: hashlib.sha256(p.read_bytes()).hexdigest() for k, p in paths.items()}:
        raise ValueError("broker files changed during review")
    same_day = [r for r in resolved if r["resolved_source_trade_date"] == r["raw_fields"]["交易日期"] and r["kind"] == "replacement_cash"]
    return {"schema": "fuying13_settlement_matrix.v1", "account_id": B.ACCOUNT,
            "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
            "date_from": START, "date_to": END, "source_sha256": hashes, "funds_bridges": bridges,
            "source_orders": [o for o in orders if START <= o["source_trade_date"] <= END],
            "order_evidence_conflicts": [o for o in orders if o["evidence_conflict"]],
            "selected_rows": len(selected), "resolved_rows": len(resolved), "unresolved_rows": len(unresolved),
            "same_day_replacement_cash_already_in_cash": B.money(sum(B.cents(r["actual_amount"]) for r in same_day)),
            "known_later_net_cash": B.money(sum(B.cents(r["actual_amount"]) for r in resolved if r["resolved_source_trade_date"] < r["raw_fields"]["交易日期"])),
            "groups": grouped_legs(resolved + unresolved), "resolved_cash": resolved, "unresolved_cash": unresolved,
            "cash_pcf_review": pcf_review,
            "known_cash_timing_path": path, "unresolved_cash_signed_sum": B.money(sum(B.cents(r["actual_amount"]) for r in unresolved)),
            "scope": "observed explicit-source cash only; not complete private rights or account NAV",
            "all_rights_closed": False, "asset_overlap_verified": False, "economic_nav_publishable": False,
            "database_writes": 0, "broker_commands": 0}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", type=Path, default=Path("reference"))
    parser.add_argument("--orders", type=Path, required=True)
    parser.add_argument("--cash-pcf", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists():
        parser.error("output must be new")
    raw = args.orders.read_bytes()
    pcf = args.cash_pcf.read_bytes()
    result = audit(args.reference, json.loads(raw), json.loads(pcf))
    result["order_snapshot_sha256"] = hashlib.sha256(raw).hexdigest()
    result["cash_pcf_sha256"] = hashlib.sha256(pcf).hexdigest()
    args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w", encoding="utf-8") as out:
        json.dump(result, out, ensure_ascii=False, indent=2, allow_nan=False)
        out.write("\n")
        out.flush()
        os.fsync(out.fileno())
    print(json.dumps({k: result[k] for k in ("selected_rows", "resolved_rows", "unresolved_rows",
        "same_day_replacement_cash_already_in_cash", "known_later_net_cash", "unresolved_cash_signed_sum", "economic_nav_publishable")}, ensure_ascii=False))


if __name__ == "__main__":
    main()
