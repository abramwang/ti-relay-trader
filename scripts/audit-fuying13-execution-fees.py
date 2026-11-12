#!/usr/bin/env python3
"""One-time cash/OC execution fee reconciliation, not a per-order fee importer."""

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
    "residual", Path(__file__).with_name("audit-fuying13-nav-residual.py"))
R = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(R)
ZERO = Decimal(0)
RESIDUAL_HASH = "2f83c667ddcbe944554611d2e559e5f18311012741fe3a733ca6f6537e0dcf5d"


def quantity(value):
    if type(value) is not int or value <= 0 or value > 10**12:
        raise ValueError("invalid execution quantity")
    return value


def execution_buckets(snapshot):
    R.snapshot_check(snapshot)
    if type(snapshot["order_group_count"]) is not int or snapshot["order_group_count"] != len(snapshot["orders"]):
        raise ValueError("order coverage incomplete")
    buckets, orders, fill_pks, fill_ids = {}, set(), set(), set()
    for o in snapshot["orders"]:
        day = o["trade_date"]
        if R.iso(day.replace("-", "")) != day or not "2026-10-12" <= day <= "2026-11-09":
            raise ValueError("execution date outside review")
        identity = (day, o["gateway_order_id"])
        if (o["account_id"] != R.B.ACCOUNT or not identity[1] or identity in orders
                or o["exchange"] not in ("SH", "SZ") or not re.fullmatch(r"\d{6}", o["symbol"])
                or o["symbol"] == "204001" or o["trade_side"] not in ("B", "S") or o["business_type"] != "S"):
            raise ValueError("invalid/duplicate ordinary order")
        orders.add(identity)
        q, gross = 0, ZERO
        for f in o["fills"]:
            fid = (day, o["exchange"], f["fill_id"])
            if (type(f["fill_pk"]) is not int or f["fill_pk"] <= 0 or f["fill_pk"] in fill_pks
                    or not f["fill_id"] or f["fill_id"].startswith("relay-summary:") or fid in fill_ids):
                raise ValueError("duplicate/synthetic execution")
            fill_pks.add(f["fill_pk"]); fill_ids.add(fid)
            qty, price = quantity(f["qty"]), R.number(f["price"])
            if price <= 0:
                raise ValueError("nonpositive ordinary price")
            q += qty; gross += price * qty
        if q != quantity(o["quantity"]) or gross != R.number(o["gross_amount"]):
            raise ValueError("execution totals disagree")
        if len(o["fee_records"]) != 1:
            raise ValueError("fee snapshot ambiguous/missing")
        fee = o["fee_records"][0]
        if (fee["record_scope"] != "order" or not fee["fee_record_id"] or not fee["fee_as_of"]
                or fee["fee_complete"] is not True or fee["association_complete"] is not True
                or fee["fee_source"] in (None, "", "unavailable") or fee["currency"] != "CNY"):
            raise ValueError("incomplete fee evidence")
        amount = R.number(fee["total_fee"])
        if amount < 0:
            raise ValueError("negative fee snapshot")
        key = (day, o["symbol"], o["trade_side"])
        b = buckets.setdefault(key, {"quantity": 0, "gross": ZERO, "fee": ZERO,
                                    "exchange": o["exchange"], "order_ids": [], "fee_record_ids": []})
        if b["exchange"] != o["exchange"]:
            raise ValueError("cash symbol ambiguous across exchanges")
        b["quantity"] += q; b["gross"] += gross; b["fee"] += amount
        b["order_ids"].append(o["gateway_order_id"]); b["fee_record_ids"].append(fee["fee_record_id"])
    if type(snapshot["fill_count"]) is not int or len(fill_pks) != snapshot["fill_count"]:
        raise ValueError("execution count incomplete")
    return buckets


def cash_buckets(flows):
    buckets, seen, rounded_price_counterexamples = {}, set(), []
    for f in flows:
        raw = f["raw_fields"]
        if not "20261012" <= raw["交易日期"] <= "20261109":
            continue
        subject = raw["业务科目"]
        if subject not in ("买入成交清算资金", "卖出成交清算资金"):
            continue
        fid = f["broker_flow_id"]
        if f["account_id"] != R.B.ACCOUNT or not fid or fid in seen:
            raise ValueError("invalid/duplicate cash identity")
        seen.add(fid)
        match = re.fullmatch(r".*\((\d{6})\)(\d+)股\*([0-9]+\.[0-9]+)", raw["操作摘要"])
        if not match or match[1] != raw["股票代码"]:
            raise ValueError("cash summary code/quantity unavailable")
        qty = quantity(int(match[2]))
        side = "B" if subject.startswith("买") else "S"
        incoming, outgoing, amount = R.number(raw["入金"]), R.number(raw["出金"]), R.number(f["actual_amount"])
        if incoming < 0 or outgoing < 0 or incoming-outgoing != amount or (side == "B" and (incoming != 0 or amount >= 0)) or (side == "S" and (outgoing != 0 or amount <= 0)):
            raise ValueError("cash direction/amount mismatch")
        key = (R.iso(raw["交易日期"]), match[1], side)
        b = buckets.setdefault(key, {"quantity": 0, "cash": ZERO, "summary_gross": ZERO, "flow_ids": []})
        summary_gross = R.number(match[3]) * qty
        b["quantity"] += qty; b["cash"] += amount; b["summary_gross"] += summary_gross; b["flow_ids"].append(fid)
        wrong_fee = -amount-summary_gross if side == "B" else summary_gross-amount
        if wrong_fee < 0:
            rounded_price_counterexamples.append({"broker_flow_id": fid, "trade_date": key[0],
                "symbol": match[1], "invalid_fee_using_summary_price": R.money(wrong_fee), "raw_fields": raw})
    return buckets, rounded_price_counterexamples


def compare(executions, cash):
    if set(executions) != set(cash):
        raise ValueError("execution/cash group coverage differs")
    rows = []
    for key in sorted(executions):
        e, c = executions[key], cash[key]
        if e["quantity"] != c["quantity"]:
            raise ValueError(f"execution/cash quantity mismatch {key}")
        fee = -c["cash"]-e["gross"] if key[2] == "B" else e["gross"]-c["cash"]
        if fee < 0:
            raise ValueError(f"negative implied fee, execution/cash scope needs review {key}")
        rows.append({"trade_date": key[0], "security_id": key[1]+"."+e["exchange"], "side": key[2],
            "quantity": e["quantity"], "oc_gross_amount": R.money(e["gross"]), "signed_broker_cash": R.money(c["cash"]),
            "implied_fee": R.money(fee), "oc_fee_snapshot": R.money(e["fee"]),
            "fee_effect_on_pnl": R.money(e["fee"]-fee),
            "summary_gross_minus_execution_gross": R.money(c["summary_gross"]-e["gross"]),
            "oc_gross_has_subcent_precision": e["gross"] % Decimal("0.01") != 0,
            "implied_fee_is_final": False,
            "broker_flow_ids": c["flow_ids"], "gateway_order_ids": e["order_ids"], "fee_record_ids": e["fee_record_ids"],
            "association_scope": "account_trade_date_security_side", "per_order_fee_verified": False})
    return rows


def daily_review(rows, nav, residual):
    oc, implied, parent = (collections.defaultdict(Decimal) for _ in range(3))
    for row in rows:
        oc[row["trade_date"]] += R.number(row["oc_fee_snapshot"])
        implied[row["trade_date"]] += R.number(row["implied_fee"])
    for row in residual["parent_fee_evidence"]:
        parent[row["trade_date"]] += R.number(row["total_fee"])
    model = {c["trade_date"]: c for c in nav["contributions"]}
    if len(model) != len(nav["contributions"]):
        raise ValueError("duplicate contribution date")
    days = []
    scenario = residual["scenarios"][1]
    if scenario["name"] != "explicit_plus_pcf_candidates":
        raise ValueError("wrong diagnostic scenario")
    if set(oc) != {d["trade_date"] for d in scenario["days"]}:
        raise ValueError("fee day coverage differs")
    for d in scenario["days"]:
        date = d["trade_date"]; c = model[date]
        if c["account_id"] != R.B.ACCOUNT or c["summary"]["missing_items"] != 0:
            raise ValueError("incomplete contribution")
        t0 = [x for x in c["contributions"] if x.get("redemption_quantity", 0) > 0]
        repo = [x for x in c["contributions"] if x["strategy_type"] == "cash_management"]
        def total(items, key):
            return sum((R.number(x[key]) for x in items), ZERO)
        original = (R.number(c["summary"]["effective_fee"]) - total(t0, "effective_fee")
                    + total(t0, "actual_fee") - parent[date] - total(repo, "effective_fee"))
        if original != oc[date]:
            raise ValueError(f"OC fee snapshot changed from frozen model {date}")
        delta = oc[date] - implied[date]
        before = R.number(d["diagnostic_remainder_excluding_other_change"])
        days.append({"trade_date": date, "ordinary_model_fee": R.money(original),
            "cash_implied_fee": R.money(implied[date]), "fee_effect_on_pnl": R.money(delta),
            "previous_diagnostic_remainder": R.money(before), "remaining_diagnostic_remainder": R.money(before-delta),
            "economic_nav_publishable": False})
    return days


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence-dir", type=Path, default=Path("outputs/recovery/fuying13_cash_matrix_20261112"))
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    paths = {"snapshot": args.evidence_dir/"execution_cash_snapshot.json",
             "nav": args.evidence_dir/"nav_scenarios_v1.json", "residual": args.evidence_dir/"residual_verified_v2.json",
             "flows": Path("reference/314000045768_资金流水.csv")}
    raw = {k: p.read_bytes() for k, p in paths.items()}
    hashes = {k: hashlib.sha256(v).hexdigest() for k, v in raw.items()}
    if hashes["nav"] != R.HASHES["nav"] or hashes["residual"] != RESIDUAL_HASH or hashes["flows"] != R.M.OLD.HASHES["cash_flows"]:
        raise ValueError("frozen source changed")
    snapshot, nav, residual = (json.loads(raw[k]) for k in ("snapshot", "nav", "residual"))
    flows = R.M.EXT.read_cash(paths["flows"])
    R.M.EXT.unique_rows(flows)
    cash, counterexamples = cash_buckets(flows)
    rows = compare(execution_buckets(snapshot), cash)
    days = daily_review(rows, nav, residual)
    if hashes != {k: hashlib.sha256(p.read_bytes()).hexdigest() for k, p in paths.items()}:
        raise ValueError("source changed during review")
    report = {"schema": "fuying13_execution_fee_review.v1", "account_id": R.B.ACCOUNT,
              "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(), "source_sha256": hashes,
              "fill_count": snapshot["fill_count"], "order_count": snapshot["order_group_count"],
              "cash_row_count": sum(len(b["flow_ids"]) for b in cash.values()), "buckets": rows, "days": days,
              "rounded_summary_price_counterexamples": counterexamples,
              "subcent_execution_gross_buckets": sum(row["oc_gross_has_subcent_precision"] for row in rows),
              "total_snapshot_fee_minus_implied_fee": R.money(sum((R.number(d["fee_effect_on_pnl"]) for d in days), ZERO)),
              "all_diagnostic_remainders_zero": all(R.number(d["remaining_diagnostic_remainder"]) == 0 for d in days),
              "assumptions": ["cash-implied aggregate fee depends on OC execution gross; not a broker fee breakdown",
                  "rounded cash summary price is not execution gross", "no per-order cash allocation inferred",
                  "zero diagnostic residual does not verify ETF candidate source or asset inclusion",
                  "subcent execution gross can carry source-price precision effects; implied fees are not final fee statements",
                  "differences do not establish a universal rounding rule or complete fee semantics"],
              "asset_overlap_verified": False, "candidate_source_verified": False,
              "per_order_cash_association_verified": False, "fee_rule_verified": False,
              "economic_nav_publishable": False, "database_writes": 0, "broker_commands": 0}
    args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w", encoding="utf-8") as out:
        json.dump(report, out, ensure_ascii=False, indent=2, allow_nan=False)
        out.write("\n"); out.flush(); os.fsync(out.fileno())
    print(json.dumps({k: report[k] for k in ("fill_count", "order_count", "cash_row_count",
        "total_snapshot_fee_minus_implied_fee", "all_diagnostic_remainders_zero", "economic_nav_publishable")}, ensure_ascii=False))


if __name__ == "__main__":
    main()
