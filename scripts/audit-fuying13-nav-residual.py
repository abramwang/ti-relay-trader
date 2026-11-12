#!/usr/bin/env python3
"""Offline residual decomposition; neither an asset correction nor a NAV writer."""

import argparse
import collections
import hashlib
import importlib.util
import json
import os
import re
from datetime import datetime
from decimal import Decimal
from pathlib import Path
from zoneinfo import ZoneInfo

SPEC = importlib.util.spec_from_file_location(
    "matrix", Path(__file__).with_name("audit-fuying13-settlement-matrix.py"))
M = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(M)
B = M.B
ZERO = Decimal(0)
HASHES = {
    "matrix": "7550662d6ef5f8addff9a07fc796c01c4e560fb13f1ea4d30f96be524d8d17eb",
    "nav": "4455044be0b1e3f8d113fcabab497951cf2dbe07989bc13acb019b3fd5e6ecf9",
}


def number(value):
    if isinstance(value, bool) or not re.fullmatch(r"-?\d+(?:\.\d+)?", str(value)):
        raise ValueError("invalid decimal")
    n = Decimal(str(value))
    if not n.is_finite() or abs(n) >= Decimal("1e14"):
        raise ValueError("invalid money bound")
    return n


def money(value):
    return format(value, "f")


def iso(day):
    return datetime.strptime(day, "%Y%m%d").strftime("%Y-%m-%d")


def snapshot_check(snapshot):
    if (snapshot.get("database_name") != "relay_trader"
            or snapshot.get("transaction_read_only") != "on"
            or snapshot.get("account_id") != B.ACCOUNT):
        raise ValueError("invalid readonly evidence scope")


def parent_fees(snapshot, orders):
    snapshot_check(snapshot)
    expected = {(iso(o["source_trade_date"]), o["gateway_order_id"]): o for o in orders}
    if len(expected) != len(orders):
        raise ValueError("duplicate parent")
    fees, evidence, seen = collections.defaultdict(Decimal), [], set()
    for r in snapshot["records"]:
        key = (r["trade_date"], r["gateway_order_id"])
        o = expected.get(key)
        if (not o or key in seen or r["account_id"] != B.ACCOUNT
                or r["security_id"] != o["security_id"] or not r["fee_record_id"]
                or r["record_scope"] != "order" or r["fee_complete"] is not True
                or r["association_complete"] is not True
                or r["fee_source"] in (None, "", "unavailable")):
            raise ValueError("ambiguous/incomplete parent fee snapshot")
        seen.add(key)
        amount = number(r["total_fee"])
        if amount < 0:
            raise ValueError("negative parent fee")
        fees[r["trade_date"]] += amount
        evidence.append(r)
    if seen != set(expected):
        raise ValueError("missing parent fee")
    return fees, evidence


def repo_receipts(snapshot, flows, dates):
    snapshot_check(snapshot)
    orders = {}
    for o in snapshot["repo_orders"]:
        stream = o["order_stream_id"]
        key = (o["trade_date"].replace("-", ""), stream[5:])
        if (o["account_id"] != B.ACCOUNT or not re.fullmatch(r"11001\d{10}", stream)
                or o["symbol"] != "204001" or o["exchange"] != "SH"
                or o["trade_side"] != "S" or o["business_type"] != "S"
                or type(o["cum_filled_qty"]) is not int or o["cum_filled_qty"] <= 0
                or o["is_terminal"] is not True or key in orders):
            raise ValueError("invalid repo order")
        orders[key] = o
    interest, evidence, seen = collections.defaultdict(Decimal), [], set()
    for f in flows:
        r = f["raw_fields"]
        if iso(r["交易日期"]) not in dates or r["业务科目"] != "回购融券购回资金划入":
            continue
        match = re.fullmatch(r"融券购回204001:成交日期(\d{8})，申报委托号(\d{10})", r["操作摘要"])
        if not match:
            raise ValueError("unknown repo receipt summary")
        key = match.groups()
        o = orders.get(key)
        if (not o or key in seen or r["交易日期"] <= key[0]
                or r["股票代码"] != "204001" or f["account_id"] != B.ACCOUNT
                or number(r["出金"]) != 0 or number(r["入金"]) != number(f["actual_amount"])):
            raise ValueError("repo receipt identity/direction mismatch")
        seen.add(key)
        principal = Decimal(o["cum_filled_qty"]) * 100
        amount = number(f["actual_amount"]) - principal
        if amount < 0 or amount > principal / 100:
            raise ValueError("unverified repo principal/interest")
        day = iso(r["交易日期"])
        interest[day] += amount
        evidence.append({"trade_date": day, "source_trade_date": iso(key[0]),
                         "gateway_order_id": o["gateway_order_id"],
                         "broker_flow_id": f["broker_flow_id"], "principal": money(principal),
                         "received_interest": money(amount), "raw_fields": r})
    if set(interest) != set(dates):
        raise ValueError("reviewed daily repo receipt coverage incomplete")
    return interest, evidence


def cash_by_source(matrix):
    known, candidates, seen = collections.defaultdict(Decimal), collections.defaultdict(Decimal), set()
    for r in matrix["resolved_cash"]:
        if r["account_id"] != B.ACCOUNT or r["broker_flow_id"] in seen or not r["resolved_source_trade_date"]:
            raise ValueError("invalid explicit cash")
        seen.add(r["broker_flow_id"])
        known[iso(r["resolved_source_trade_date"])] += number(r["actual_amount"])
    unresolved = {r["broker_flow_id"]: r for r in matrix["unresolved_cash"]}
    if len(unresolved) != len(matrix["unresolved_cash"]):
        raise ValueError("duplicate unresolved cash")
    used = set()
    for g in matrix["cash_pcf_review"]["unresolved_candidates"]:
        if g["private_source_verified"] or g["included_in_known_carry"] or len(g["pcf_order_multiset_candidates"]) != 1:
            raise ValueError("candidate promoted or ambiguous")
        source = g["pcf_order_multiset_candidates"][0]["source_trade_date"]
        for fid in g["flow_ids"]:
            if fid not in unresolved or fid in seen or fid in used:
                raise ValueError("candidate reused/missing")
            r = unresolved[fid]
            if r["account_id"] != B.ACCOUNT or r["resolved_source_trade_date"]:
                raise ValueError("invalid candidate identity/source")
            used.add(fid)
            candidates[iso(source)] += number(r["actual_amount"])
    if used != set(unresolved):
        raise ValueError("uncovered candidate")
    return known, candidates


def decompose(day, contribution, known, candidate, parent_fee, received_interest, previous_other):
    if contribution["account_id"] != B.ACCOUNT or contribution["trade_date"] != day["trade_date"]:
        raise ValueError("contribution date/account mismatch")
    if contribution["summary"]["missing_items"] != 0:
        raise ValueError("incomplete model")
    rows = contribution["contributions"]
    parents = [x for x in rows if x.get("redemption_quantity", 0) > 0]
    repo = [x for x in rows if x["strategy_type"] == "cash_management"]
    if len(repo) != 1 or repo[0]["security_id"] != "204001.SH":
        raise ValueError("unsupported repo contribution")
    for x in parents:
        if x["strategy_type"] != "etf_redemption_t0" or x.get("estimated_exit_value") is None:
            raise ValueError("missing T0 model exit")
    if not parents and (known or candidate or parent_fee):
        raise ValueError("cash without source model")
    def total(key):
        return sum((number(x[key]) for x in parents), ZERO)
    held = sum((number(x["close_value"]) for x in rows
                if x.get("strategy_id", "").startswith("ETF_T0_COMPONENT:")), ZERO)
    ordinary_fee = total("actual_fee") - parent_fee
    if ordinary_fee < 0:
        raise ValueError("parent fee exceeds all execution fees")
    # Remove parent fee snapshots from execution fees before using signed broker
    # fee cash legs. Otherwise the redemption charge would be counted twice.
    correction = (total("linked_component_sales") + held + known + candidate
                  - ordinary_fee - total("estimated_exit_value") + total("effective_fee"))
    repo_adjustment = received_interest - number(repo[0]["gross_contribution"])
    residual = number(day["conditional_day_pnl"]) - number(contribution["summary"]["net_contribution"])
    other = number(day["broker_unclassified_asset_preserved"])
    remaining = residual - correction - repo_adjustment
    return {"trade_date": day["trade_date"], "starting_model_residual": money(residual),
            "known_source_cash": money(known), "candidate_source_cash": money(candidate),
            "linked_component_sales": money(total("linked_component_sales")),
            "held_component_value": money(held), "model_parent_fee_removed": money(parent_fee),
            "remaining_execution_fee_snapshot": money(ordinary_fee),
            "cash_vs_model_t0_adjustment": money(correction),
            "repo_received_interest": money(received_interest),
            "repo_model_interest": money(number(repo[0]["gross_contribution"])),
            "repo_posting_vs_model_adjustment": money(repo_adjustment),
            "remaining_residual_with_other_assets": money(remaining),
            "unclassified_asset": money(other), "unclassified_asset_change": money(other - previous_other),
            "diagnostic_remainder_excluding_other_change": money(remaining - other + previous_other),
            "unclassified_asset_removal_authorized": False, "economic_nav_publishable": False}


def opening_other_asset(snapshot, funds, expected_open):
    rows = [r for _, r in funds if r["交易日"] == "20261009"]
    if len(rows) != 1 or rows[0]["客户号"] != B.ACCOUNT:
        raise ValueError("opening broker asset missing/wrong account")
    row = rows[0]
    if number(row["总资产"]) != number(expected_open):
        raise ValueError("opening broker asset differs from NAV preview")
    principal = sum((Decimal(o["cum_filled_qty"]) * 100 for o in snapshot["repo_orders"]
                     if o["trade_date"] == "2026-10-09"), ZERO)
    return number(row["总资产"]) - number(row["客户资金"]) - number(row["市值"]) - principal


def redemption_coverage(matrix, models, dates):
    expected, observed = collections.Counter(), collections.Counter()
    for o in matrix["source_orders"]:
        expected[(iso(o["source_trade_date"]), o["security_id"])] += o["quantity"]
    for date in dates:
        for x in models[date]["contributions"]:
            qty = x.get("redemption_quantity", 0)
            if type(qty) is not int or qty < 0:
                raise ValueError("invalid model redemption quantity")
            if qty:
                observed[(date, x["security_id"])] += qty
    if expected != observed:
        raise ValueError("model redemption scope differs from cash parents")


def asset_scope_observations(matrix, days):
    grouped = collections.defaultdict(list)
    for r in matrix["unresolved_cash"]:
        grouped[iso(r["raw_fields"]["交易日期"])].append(r)
    result = []
    for index, d in enumerate(days):
        other = number(d["broker_unclassified_asset_preserved"])
        if not other:
            continue
        next_day = days[index + 1]["trade_date"] if index + 1 < len(days) else None
        cash = grouped.get(next_day, [])
        signed = sum((number(r["actual_amount"]) for r in cash), ZERO)
        result.append({"trade_date": d["trade_date"], "unclassified_asset": money(other),
                       "following_observed_date": next_day, "following_cash_signed_amount": money(signed),
                       "flow_ids": [r["broker_flow_id"] for r in cash],
                       "absolute_amount_matches": bool(cash) and abs(signed) == abs(other),
                       "match_basis": "adjacent observed date and absolute amount only; not economic inclusion proof",
                       "asset_overlap_verified": False})
    return result


def audit(nav, matrix, snapshot, flows, funds):
    for r in (nav, matrix):
        if r["account_id"] != B.ACCOUNT or r["database_writes"] != 0 or r["broker_commands"] != 0 or r["economic_nav_publishable"]:
            raise ValueError("invalid frozen preview")
    if matrix["asset_overlap_verified"] or matrix["all_rights_closed"]:
        raise ValueError("evidence boundary changed")
    known, candidate = cash_by_source(matrix)
    fees, fee_evidence = parent_fees(snapshot, matrix["source_orders"])
    models = {x["trade_date"]: x for x in nav["contributions"]}
    if len(models) != len(nav["contributions"]):
        raise ValueError("duplicate model day")
    scenarios = nav["cash_nav_scenarios"]
    if [s["name"] for s in scenarios] != ["explicit_source_cash_only", "explicit_plus_pcf_candidates"]:
        raise ValueError("unknown scenario")
    dates = [d["trade_date"] for d in scenarios[0]["days"]]
    if len(set(dates)) != len(dates) or dates != sorted(dates):
        raise ValueError("duplicate/unsorted scenario days")
    redemption_coverage(matrix, models, dates)
    interest, repo_evidence = repo_receipts(snapshot, flows, dates)
    initial_other = opening_other_asset(snapshot, funds, scenarios[0]["days"][0]["reported_open_asset"])
    result = []
    for s in scenarios:
        if s["economic_nav_publishable"] or [d["trade_date"] for d in s["days"]] != dates:
            raise ValueError("inconsistent scenario")
        rows, previous_other = [], initial_other
        for d in s["days"]:
            date = d["trade_date"]
            c = candidate[date] if s["name"] == "explicit_plus_pcf_candidates" else ZERO
            row = decompose(d, models[date], known[date], c, fees[date], interest[date], previous_other)
            rows.append(row)
            previous_other = number(d["broker_unclassified_asset_preserved"])
        result.append({"name": s["name"], "days": rows})
    return {"account_id": B.ACCOUNT, "schema": "fuying13_nav_residual_review.v1",
            "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
            "scenarios": result, "parent_fee_evidence": fee_evidence, "repo_receipt_evidence": repo_evidence,
            "opening_unclassified_asset": money(initial_other),
            "asset_scope_observations": asset_scope_observations(matrix, scenarios[0]["days"]),
            "assumptions": ["observed cash only, not proof of complete private settlement",
                "PCF source candidates remain unverified", "ordinary execution fees retain OC snapshot scope",
                "repo adjustment compares receipt-date cash with model accrual, not a new NAV policy",
                "unclassified assets remain in authoritative totals; subtraction only diagnoses residual"],
            "asset_overlap_verified": False, "economic_nav_publishable": False,
            "database_writes": 0, "broker_commands": 0}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--evidence-dir", type=Path, default=Path("outputs/recovery/fuying13_cash_matrix_20261112"))
    p.add_argument("--output", type=Path, required=True)
    args = p.parse_args()
    paths = {"matrix": args.evidence_dir / "matrix_verified.json", "nav": args.evidence_dir / "nav_scenarios_v1.json",
             "snapshot": args.evidence_dir / "fee_repo_snapshot.json", "flows": Path("reference/314000045768_资金流水.csv"),
             "funds": Path("reference/314000045768_资金.csv")}
    raw = {k: path.read_bytes() for k, path in paths.items()}
    hashes = {k: hashlib.sha256(v).hexdigest() for k, v in raw.items()}
    if (any(hashes[k] != h for k, h in HASHES.items())
            or hashes["flows"] != M.OLD.HASHES["cash_flows"] or hashes["funds"] != M.OLD.HASHES["funds"]):
        raise ValueError("frozen evidence changed")
    flows = M.EXT.read_cash(paths["flows"])
    M.EXT.unique_rows(flows)
    report = audit(json.loads(raw["nav"]), json.loads(raw["matrix"]), json.loads(raw["snapshot"]),
                   flows, list(B.rows(paths["funds"], "funds")))
    if hashes != {k: hashlib.sha256(path.read_bytes()).hexdigest() for k, path in paths.items()}:
        raise ValueError("evidence changed during audit")
    report["source_sha256"] = hashes
    args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w", encoding="utf-8") as f:
        json.dump(report, f, ensure_ascii=False, indent=2, allow_nan=False)
        f.write("\n")
        f.flush()
        os.fsync(f.fileno())
    print(json.dumps({"days": len(report["scenarios"][0]["days"]),
                      "repo_receipts": len(report["repo_receipt_evidence"]), "parent_fees": len(report["parent_fee_evidence"]),
                      "database_writes": 0, "economic_nav_publishable": False}))


if __name__ == "__main__":
    main()
