#!/usr/bin/env python3
"""Reconstruct historical posting dates from a unique, independently anchored cash chain."""

from __future__ import annotations

import argparse
import collections
import importlib.util
import json
import os
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo


SPEC = importlib.util.spec_from_file_location("cash_extension", Path(__file__).with_name("audit-fuying13-cash-extension.py"))
EXT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(EXT)
BROKER, HISTORY = EXT.BROKER, EXT.HISTORY


def build_chain(deliveries, opening, closing):
    outgoing, incoming, identities = {}, {}, set()
    for number, raw in deliveries:
        if number in identities:
            raise ValueError("duplicate delivery row identity")
        identities.add(number)
        amount = BROKER.cents(raw["实付金额"])
        if not amount:
            continue
        after = BROKER.cents(raw["本次资金余额"])
        before = after - amount
        if before in outgoing or after in incoming:
            raise ValueError("ambiguous balance branch; refusing to choose an ordering")
        edge = {"source_row": number, "raw": raw, "before": before, "after": after, "amount": amount}
        outgoing[before], incoming[after] = edge, edge
    chain, used, current = [], set(), opening
    while current in outgoing:
        edge = outgoing[current]
        if edge["source_row"] in used:
            raise ValueError("cash balance cycle")
        chain.append(edge)
        used.add(edge["source_row"])
        current = edge["after"]
    if current != closing or len(used) != len(outgoing):
        raise ValueError("incomplete cash chain or disconnected delivery records")
    return chain


def assign_posting_days(chain, funds, opening_date, cash_rows):
    dates = sorted(day for day in funds if day > opening_date)
    cash_by_day = collections.defaultdict(collections.Counter)
    for row in cash_rows:
        raw, amount = row["raw_fields"], BROKER.cents(row["actual_amount"])
        if amount:
            cash_by_day[raw["交易日期"]][(raw["股票代码"], amount)] += 1
    cursor, current = 0, BROKER.cents(funds[opening_date]["客户资金"])
    assignments, segments = [], []
    for day in dates:
        target, start = BROKER.cents(funds[day]["客户资金"]), cursor
        expected = collections.Counter()
        while current != target and cursor < len(chain):
            edge = chain[cursor]
            if edge["before"] != current:
                raise ValueError("broken balance edge")
            current = edge["after"]
            expected[(edge["raw"]["代码"].split(".")[0], edge["amount"])] += 1
            assignments.append({"source_row": edge["source_row"],
                                "recorded_delivery_date": edge["raw"]["成交日期"],
                                "inferred_posting_date": day, "security_id": edge["raw"]["代码"],
                                "side": edge["raw"]["方向"], "amount": BROKER.money(edge["amount"]),
                                "balance_before": BROKER.money(edge["before"]),
                                "balance_after": BROKER.money(edge["after"]),
                                "economic_source_date": None})
            cursor += 1
        if current != target or expected != cash_by_day[day]:
            raise ValueError(f"funds anchor or per-security cash multiset mismatch on {day}")
        segments.append({"posting_date": day, "rows": cursor - start, "closing_cash": BROKER.money(current),
                         "cash_multiset_verified": True})
    if cursor != len(chain):
        raise ValueError("unused cash edges beyond closing anchor")
    return assignments, segments


def other_asset_review(plan, funds, assignments):
    days = []
    for nav in plan:
        day = nav["trade_date"].replace("-", "")
        if day not in funds:
            continue
        value = nav["pnl_components"].get("market_valuation", {})
        if "reverse_repo_principal" not in value:
            raise ValueError("missing verified repo principal")
        row = funds[day]
        cash, market, total = (BROKER.cents(row[k]) for k in ["客户资金", "市值", "总资产"])
        other = total - cash - market - BROKER.cents(str(value["reverse_repo_principal"]))
        if other != BROKER.cents(str(value["broker_reported_other_asset"])):
            raise ValueError("other-asset baseline differs from frozen plan")
        candidates = [a for a in assignments if a["recorded_delivery_date"] == day
                      and a["inferred_posting_date"] > day and a["side"] in ("ETF申购", "ETF赎回")]
        days.append({"trade_date": day, "broker_reported_other_asset": BROKER.money(other),
                     "later_posted_etf_rows_recorded_on_this_day": candidates,
                     "scope_verified": False, "economic_nav_publishable": False,
                     "explanation": "Equal amounts and balance ordering do not establish the economic meaning of other assets."})
    return days


def run(evidence, cash_path, funds_path, plan_path, start, end, output):
    start, end = BROKER.date(start), BROKER.date(end)
    if end < start:
        raise ValueError("invalid date range")
    report = HISTORY.verify_evidence(evidence)
    hashes = {"cash": HISTORY.sha_file(cash_path), "funds": HISTORY.sha_file(funds_path),
              "plan": HISTORY.sha_file(plan_path), "old_audit": HISTORY.SOURCE_AUDIT_SHA256}
    if hashes["plan"] != EXT.PLAN_HASH:
        raise ValueError("frozen plan changed")
    cash = EXT.read_cash(cash_path)
    EXT.unique_rows(cash)
    funds, validation = EXT.fund_bridges(list(BROKER.rows(funds_path, "funds")), cash)
    previous = sorted(day for day in funds if day < start)
    if not previous or start not in funds or end not in funds:
        raise ValueError("missing opening or closing funds anchor")
    opening = previous[-1]
    selected_funds = {day: row for day, row in funds.items() if opening <= day <= end}
    deliveries = [(n, r) for n, r in BROKER.rows(evidence / report["files"]["settlements"]["snapshot"], "settlements")
                  if start <= r["成交日期"] <= end]
    chain = build_chain(deliveries, BROKER.cents(funds[opening]["客户资金"]), BROKER.cents(funds[end]["客户资金"]))
    assignments, segments = assign_posting_days(chain, selected_funds, opening, cash)
    plan = json.loads(plan_path.read_text())["navs"]
    shifted = [a for a in assignments if a["recorded_delivery_date"] != a["inferred_posting_date"]]
    grouped = collections.defaultdict(lambda: {"rows": 0, "amount": 0})
    for row in shifted:
        key = row["recorded_delivery_date"], row["inferred_posting_date"]
        grouped[key]["rows"] += 1
        grouped[key]["amount"] += BROKER.cents(row["amount"])
    result = {"account_id": BROKER.ACCOUNT, "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
              "source_hashes": hashes, "date_from": start, "date_to": end, "opening_anchor_date": opening,
              "opening_cash": funds[opening]["客户资金"], "closing_cash": funds[end]["客户资金"],
              "chain_edges": len(chain), "segments": segments, "assignments": assignments,
              "shifted_date_rows": len(shifted), "shifted_date_groups": [
                  {"recorded_date": key[0], "posting_date": key[1], "rows": value["rows"],
                   "amount": BROKER.money(value["amount"])} for key, value in sorted(grouped.items())],
              "other_asset_review": other_asset_review(plan, selected_funds, assignments),
              "funds_validation": validation, "database_writes": 0, "broker_commands": 0,
              "economic_nav_publishable": False, "other_asset_scope_verified": False,
              "proof_scope": "Unique exported balance chain and daily cash multisets; not economic source dates or other-asset classification."}
    for key, path in [("cash", cash_path), ("funds", funds_path), ("plan", plan_path)]:
        if HISTORY.sha_file(path) != hashes[key]:
            raise ValueError("input changed during audit")
    output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with os.fdopen(os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as target:
        json.dump(result, target, ensure_ascii=False, indent=2, allow_nan=False)
        target.flush()
        os.fsync(target.fileno())
    return {k: v for k, v in result.items() if k not in ("assignments", "other_asset_review")}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("evidence", "cash", "funds", "plan", "output"):
        parser.add_argument("--" + flag, type=Path, required=True)
    parser.add_argument("--date-from", required=True)
    parser.add_argument("--date-to", required=True)
    args = parser.parse_args()
    print(json.dumps(run(args.evidence, args.cash, args.funds, args.plan,
                         args.date_from, args.date_to, args.output), ensure_ascii=False, indent=2))
