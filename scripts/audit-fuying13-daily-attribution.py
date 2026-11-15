#!/usr/bin/env python3
"""Offline daily attribution bridge using sealed evidence; no publication or DB."""

import argparse
from collections import defaultdict
from decimal import Decimal, ROUND_HALF_UP
import hashlib
import json
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ACCOUNT = "314000045768"
INPUTS = {
    "cycles": ("outputs/recovery/fuying13_cycle_review_20261115/profit-v13.json", "74df9ba348970e03910164408840843f6f1b2a92478d577bc487bde701b45072"),
    "cost": ("outputs/recovery/fuying13_engine_capture_20261114/run-1-result.json", "44770cd720b96bb3fc8345b1548ce9fec8b7356835a1e7add7bfb03185f21eef"),
    "fees": ("outputs/recovery/fuying13_cycle_review_20261115/fees-invariant-v2/audit.json", "300c54615ab38cad5f8b6746fd6805d3462bb9d10ebb180adf43b231fb0ab68f"),
    "receipts": ("outputs/recovery/fuying13_cash_matrix_20261112/residual_verified_v2.json", "2f83c667ddcbe944554611d2e559e5f18311012741fe3a733ca6f6537e0dcf5d"),
}
ZERO = Decimal(0)
MICRO = Decimal("0.000001")


def number(value):
    if isinstance(value, bool) or not isinstance(value, (str, int, Decimal)):
        raise ValueError("invalid numeric evidence")
    n = Decimal(value)
    if not n.is_finite() or abs(n) >= Decimal("1e12"):
        raise ValueError("money outside audit bound")
    return n


def require(ok, message):
    if not ok:
        raise ValueError(message)


def decode(raw):
    return json.loads(raw, parse_float=Decimal, parse_constant=lambda s: number(s))


def encode_decimal(value):
    if isinstance(value, Decimal):
        return format(value, "f")
    raise TypeError("unsupported evidence value")


def checked_input(path, digest):
    raw = path.read_bytes()
    require(hashlib.sha256(raw).hexdigest() == digest, "sealed input hash changed: " + str(path))
    return decode(raw)


def row_key(date, row):
    return date, row["security_id"], row.get("strategy_id", "")


def daily_cycle_parts(profits, contributions, members, fee_orders):
    """Use actual source cash and marked inventory, not daily IOPV estimates."""
    orders = {}
    for o in fee_orders:
        key = o["trade_date"], o["gateway_order_id"]
        require(key not in orders, "duplicate fee order")
        orders[key] = o
    buckets = {}
    seen_fills = set()
    for m in members:
        key = m["trade_date"], m["gateway_order_id"]
        fill = key + (m["fill_id"],)
        require(fill not in seen_fills, "duplicate cycle fill")
        seen_fills.add(fill)
        b = buckets.setdefault(key, {"cycle": m["cycle_id"], "quantity": 0, "gross": 0, "side": m["side"], "security": m["security_id"]})
        require((b["cycle"], b["side"], b["security"]) == (m["cycle_id"], m["side"], m["security_id"]), "shared fee order")
        b["quantity"] += m["quantity"]
        b["gross"] += m["gross_micros"]
    fees = defaultdict(lambda: ZERO)
    for key, b in buckets.items():
        o = orders.get(key)
        require(o is not None and o["status"] == "matched" and o["fee_cents"] is not None, "unmatched ordinary cycle fee")
        require((o["quantity"], o["oc_gross_micros"], o["side"], o["security_id"]) == (b["quantity"], b["gross"], b["side"], b["security"]), "partial cycle fee allocation")
        require(o["fee_cents"] >= 0, "negative execution fee")
        fees[b["cycle"], key[0]] += number(o["fee_cents"]) / 100

    rows, parents = {}, {}
    for day in contributions:
        date = day["trade_date"]
        for x in day["contributions"]:
            key = row_key(date, x)
            require(key not in rows, "duplicate contribution identity")
            rows[key] = x
            if x.get("redemption_quantity", 0) > 0:
                parents[date, x.get("strategy_id", "")] = date + "/" + x["security_id"]
    used_rows, used_orders = set(), set(buckets)
    parts = []
    for p in profits:
        t, cycle = p["trading"], p["cycle_id"]
        date, security = cycle.split("/")
        group = [(key, x) for key, x in rows.items() if key[0] == date and x["security_id"] == security and x.get("redemption_quantity", 0) > 0]
        require(group and sum(x["redemption_quantity"] for _, x in group) == t["redemption_quantity"], "redemption quantity coverage")
        gross = sum((number(x["linked_component_sales"]) - number(x["buy_amount"]) - number(x["open_value"]) + number(x["close_value"]) for _, x in group), ZERO)
        require(sum(number(x["linked_component_sales"]) for _, x in group) == number(t["component_sale_cents"]) / 100, "component sale coverage")
        net = gross + number(p["observed_net_cash_cents"]) / 100 - fees[cycle, date]
        selected = list(group)
        part_rows = [{"trade_date": date, "kind": "redemption_source", "net": net, "fee": fees[cycle, date], "old_net": sum(number(x["net_contribution"]) for _, x in group)}]
        if t["acquisition_date"] != date:
            prior = [(key, x) for key, x in rows.items() if key[0] == t["acquisition_date"] and x["security_id"] == security and x["strategy_type"] == "etf_redemption_t0" and x.get("redemption_quantity", 0) == 0 and x["buy_quantity"] == t["acquisition_quantity"] and x["close_quantity"] == t["acquisition_quantity"]]
            require(len(prior) == 1 and len(group) == 1, "overnight cost ownership ambiguous")
            key, x = prior[0]
            require(x["buy_amount"] == number(t["acquisition_gross_cents"]) / 100 and number(x["close_value"]) == sum(number(r["open_value"]) for _, r in group) and x["strategy_id"] == group[0][1]["strategy_id"], "overnight value continuity")
            fee = fees[cycle, key[0]]
            n = number(x["close_value"]) - number(x["open_value"]) + number(x["sell_amount"]) - number(x["buy_amount"]) - fee
            part_rows.append({"trade_date": key[0], "kind": "overnight_etf", "net": n, "fee": fee, "old_net": number(x["net_contribution"])})
            selected.append((key, x))
        else:
            require(sum(number(x["buy_amount"]) for _, x in group) == number(t["acquisition_gross_cents"]) / 100, "same-day acquisition cost mismatch")

        component_positions = defaultdict(list)
        for key, x in rows.items():
            sid = x.get("strategy_id", "")
            if not sid.startswith("ETF_T0_COMPONENT:"):
                continue
            source, parent = sid[len("ETF_T0_COMPONENT:"):].split(":", 1)
            if parents.get((source, parent)) != cycle:
                continue
            component_positions[x["security_id"]].append((key, x))
        for component, chain in component_positions.items():
            chain.sort()
            require(chain[0][0][0] == date and chain[0][1]["open_quantity"] == 0 and chain[-1][1]["close_quantity"] == 0, "incomplete component inventory chain")
            prior_value, prior_qty = ZERO, 0
            for key, x in chain:
                require(number(x["open_value"]) == prior_value and x["open_quantity"] == prior_qty and x["buy_quantity"] == 0, "component mark continuity")
                fee = ZERO
                if x["sell_quantity"]:
                    candidates = [(ok, o) for ok, o in orders.items() if ok[0] == key[0] and o["security_id"] == component and o["side"] == "S"]
                    require(candidates and sum(o["quantity"] for _, o in candidates) == x["sell_quantity"] and sum(o["oc_gross_micros"] for _, o in candidates) == number(x["sell_amount"]) * 1000000, "component fee bucket mismatch")
                    for ok, o in candidates:
                        require(ok not in used_orders and o["status"] == "matched" and o["fee_cents"] is not None and o["fee_cents"] >= 0, "duplicate/unmatched component fee")
                        used_orders.add(ok)
                        fee += number(o["fee_cents"]) / 100
                n = number(x["close_value"]) - number(x["open_value"]) + number(x["sell_amount"]) - fee
                part_rows.append({"trade_date": key[0], "kind": "carried_component", "security_id": component, "net": n, "fee": fee, "old_net": number(x["net_contribution"])})
                prior_value, prior_qty = number(x["close_value"]), x["close_quantity"]
                selected.append((key, x))
        require(sum(r["net"] for r in part_rows) == number(p["net_cycle_pnl_cents"]) / 100, "daily components do not recover reviewed cycle PnL")
        require(sum(r["fee"] for r in part_rows) == number(t["ordinary_execution_fee_cents"]) / 100, "cycle fees not consumed exactly once")
        for key, _ in selected:
            require(key not in used_rows, "contribution assigned twice")
            used_rows.add(key)
        for r in part_rows:
            r["cycle_id"] = cycle
            r["correction"] = r["net"] - r["old_net"]
            r["quality_flags"] = p["quality_flags"]
            parts.append(r)
    require(set(f[0] for f in fees) <= {p["cycle_id"] for p in profits}, "orphan cycle fee")
    return sorted(parts, key=lambda r: (r["trade_date"], r["cycle_id"], r["kind"], r.get("security_id", "")))


def build_report(cycles, cost, fee_evidence, receipt_evidence):
    for source in (cycles, cost, fee_evidence, receipt_evidence):
        require(source["account_id"] == ACCOUNT and source["database_writes"] == 0 and source["broker_commands"] == 0, "wrong account/unsafe evidence")
        require(not source.get("economic_nav_publishable", False) and not source.get("final_settlement", False), "unexpected finality claim")
    require(cost["cost_chain_complete"] and not cost["error"] and cost["database_read_only"], "incomplete cost capture")
    c = cycles["cycle_profit_review"]
    parts = daily_cycle_parts(c["profits"], cost["contributions"], c["ordinary_fee_members"], fee_evidence["oc_comparison"]["orders"])
    receipt_days, ids = defaultdict(lambda: ZERO), set()
    for r in receipt_evidence["repo_receipt_evidence"]:
        raw = r["raw_fields"]
        require(r["broker_flow_id"] not in ids and raw["投资者"] == ACCOUNT and raw["流水号"] == r["broker_flow_id"] and raw["交易日期"] == r["trade_date"].replace("-", "") and raw["业务科目"] == "回购融券购回资金划入", "repo identity mismatch")
        require(number(raw["入金"]) - number(raw["出金"]) == number(r["principal"]) + number(r["received_interest"]) and number(r["principal"]) > 0 and number(r["received_interest"]) >= 0, "repo principal/interest mismatch")
        ids.add(r["broker_flow_id"])
        receipt_days[r["trade_date"]] += number(r["received_interest"])
    by_day = {}
    for d in cost["contributions"]:
        require(d["account_id"] == ACCOUNT and d["trade_date"] not in by_day, "cost date/account mismatch")
        by_day[d["trade_date"]] = d
    days = []
    for a in cycles["account_days"]:
        date = a["trade_date"]
        r = {"trade_date": date, "published_pnl": number(a["published_pnl_cents"]) / 100, "status": "missing_contribution_evidence"}
        d = by_day.get(date)
        if d is None:
            days.append(r)
            continue
        require(d["summary"]["missing_items"] == 0, "missing contribution value")
        model = sum((number(x["net_contribution"]) for x in d["contributions"] if x.get("net_contribution") is not None), ZERO)
        require(model.quantize(MICRO, rounding=ROUND_HALF_UP) == number(d["summary"]["net_contribution"]), "model aggregate mismatch")
        corrections = [p for p in parts if p["trade_date"] == date]
        delta = sum((p["correction"] for p in corrections), ZERO)
        repo = sum((number(x["gross_contribution"]) for x in d["contributions"] if x["strategy_type"] == "cash_management"), ZERO)
        strategies = defaultdict(lambda: ZERO)
        for x in d["contributions"]:
            if x.get("net_contribution") is not None:
                strategies[x["strategy_type"]] += number(x["net_contribution"])
        r.update(status="repo_receipt_evidence_missing", model_net=model, model_strategies=dict(strategies), t0_correction=delta, t0_daily_parts=corrections, model_repo_interest=repo,
                 original_residual=r["published_pnl"]-model, after_t0_residual=r["published_pnl"]-model-delta,
                 other_asset_component_change=number(a["unclassified_close_component_cents"]-a["unclassified_open_component_cents"])/100,
                 original_quality_flags=d.get("quality_flags", []))
        if date in receipt_days:
            adjustment = receipt_days[date] - repo
            corrected = model + delta + adjustment
            r.update(status="conditional_bridge_not_final", observed_repo_interest=receipt_days[date], repo_timing_correction=adjustment, corrected_net=corrected, remaining_residual=r["published_pnl"]-corrected)
            revised = dict(strategies)
            if delta or "etf_redemption_t0" in revised:
                revised["etf_redemption_t0"] = revised.get("etf_redemption_t0", ZERO) + delta
            revised["cash_management"] = revised.get("cash_management", ZERO) + adjustment
            require(sum(revised.values(), ZERO) == corrected, "strategy aggregate mismatch")
            r["conditional_strategies"] = revised
            r["residual_after_asset_component_comparison"] = r["remaining_residual"] - r["other_asset_component_change"]
            r["asset_component_classification_verified"] = False
        days.append(r)
    return {"schema":"relay.daily_attribution_bridge.v1", "account_id":ACCOUNT, "database_writes":0, "broker_commands":0,
            "economic_nav_publishable":False, "final_settlement":False, "source_sha256":{k:v[1] for k,v in INPUTS.items()},
            "scope":"Historical conditional daily bridge. Original model and NAV unchanged; missing observations are not zero. Repo uses observed posting basis, not a new final accrual policy.",
            "cycle_count":len(c["profits"]), "daily_cycle_parts":parts, "days":days,
            "summary": {"bridged_days":sum(d["status"] == "conditional_bridge_not_final" for d in days),
                        "uncovered_dates":[d["trade_date"] for d in days if d["status"] != "conditional_bridge_not_final"],
                        "max_absolute_remaining_comparison":max((abs(d["residual_after_asset_component_comparison"]) for d in days if "residual_after_asset_component_comparison" in d), default=None)}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    path = Path(args.output)
    require(not path.exists(), "output must be new")
    inputs = {k:checked_input(ROOT / p, h) for k,(p,h) in INPUTS.items()}
    result = build_report(inputs["cycles"], inputs["cost"], inputs["fees"], inputs["receipts"])
    data = json.dumps(result, ensure_ascii=False, sort_keys=True, indent=2, default=encode_decimal).encode()+b"\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd,"wb") as f:
        f.write(data)
        f.flush()
        os.fsync(f.fileno())
    print(json.dumps({"sha256":hashlib.sha256(data).hexdigest(),"days":len(result["days"]),"cycles":result["cycle_count"],"database_writes":0}))


if __name__ == "__main__":
    main()
