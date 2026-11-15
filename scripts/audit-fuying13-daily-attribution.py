#!/usr/bin/env python3
"""Offline daily attribution bridge using sealed evidence; no publication or DB."""

import argparse
import copy
from collections import defaultdict
from decimal import Decimal, ROUND_HALF_UP
import hashlib
import json
import os
import re
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
EXTENSION_INPUTS = {
    "formal_extension": ("outputs/recovery/fuying13_daily_attribution_20261115/formal-extension-v1.json", "eb8c428aef5ff1069c009af13cdfd6e8d810bb03f79db532a9ee0a2b5d9b04de"),
    "cash_extension": ("outputs/recovery/fuying13_broker_refresh_20261115/review-v2/audit.json", "d085940426ffc75d938a067a392ca0b20bedc5e1973ca7c92d9d890df0e25d15"),
}
EXTENSION_DATES = ["2026-11-10", "2026-11-11", "2026-11-12", "2026-11-13"]


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


def continuation_evidence(cost, receipts, extension, cash, fee_evidence):
    """Extend sealed observations by exact identities, never balance guessing."""
    for s in (extension, cash):
        require(s["account_id"] == ACCOUNT and s["database_writes"] == 0 and s["broker_commands"] == 0, "unsafe continuation evidence")
        require(not s.get("economic_nav_publishable", False) and not s.get("final_settlement_verified", False), "unexpected continuation finality")
    require(extension["schema"] == "relay.formal_daily_review.v1" and extension["environment"] == "production" and extension["database_read_only"] and extension["cost_chain_complete"] and not extension["error"], "incomplete formal continuation")
    require([d["trade_date"] for d in extension["contributions"]] == EXTENSION_DATES[1:], "continuation date coverage")
    expected = {"2026-11-10":"stored_zero_origin_anchor", "2026-11-11":"replay_matches_formal_cost", "2026-11-12":"replay_matches_formal_cost", "2026-11-13":"read_only_replay_from_formal_previous_day_not_persisted"}
    require(extension["cost_evidence"] == expected, "cost evidence scope changed")
    old_dates = {d["trade_date"] for d in cost["contributions"]}
    require(not old_dates.intersection(EXTENSION_DATES[1:]) and EXTENSION_DATES[0] in old_dates, "overlapping contribution continuation")
    cost, receipts = copy.deepcopy(cost), copy.deepcopy(receipts)
    cost["contributions"].extend(copy.deepcopy(extension["contributions"]))
    orders, fee_orders, fill_ids = {}, {}, set()
    quantities = defaultdict(int)
    for o in extension["orders"]:
        if o["symbol"] != "204001":
            continue
        stream = o["order_stream_id"]
        key = (o["trade_date"].replace("-", ""), stream[5:])
        require(o["account_id"] == ACCOUNT and re.fullmatch(r"11001\d{10}", stream) and o["exchange"] == "SH" and o["trade_side"] == "S" and o["business_type"] == "S" and o["is_terminal"] and o["status"] == "filled" and type(o["cum_filled_qty"]) is int and o["cum_filled_qty"] > 0 and key not in orders, "invalid continuation repo order")
        orders[key] = o
    for f in extension["fills"]:
        if f["symbol"] != "204001":
            continue
        key = (f["trade_date"], f["gateway_order_id"], f["fill_id"])
        require(f["account_id"] == ACCOUNT and f["exchange"] == "SH" and f["trade_side"] == "S" and f["business_type"] == "S" and type(f["qty"]) is int and f["qty"] > 0 and key not in fill_ids, "invalid/duplicate repo fill")
        fill_ids.add(key)
        quantities[key[:2]] += f["qty"]
    for o in fee_evidence["orders"]:
        if o["security_id"] != "204001.SH" or o["trade_date"] < "20261109":
            continue
        key = o["trade_date"], o["broker_order_id"]
        require(key not in fee_orders and o["account_id"] == ACCOUNT and o["side"] == "融券" and o["fee_amount_resolved"] and not o["unresolved_groups"] and type(o["fee_cents"]) is int and o["fee_cents"] >= 0, "unresolved/duplicate repo statement")
        fee_orders[key] = o
    require(set(orders) == set(fee_orders), "repo statement/order coverage")
    for key, o in orders.items():
        f = fee_orders[key]
        require(quantities.pop((o["trade_date"], o["gateway_order_id"]), None) == o["cum_filled_qty"] and f["quantity"] * 10 == o["cum_filled_qty"] and f["gross_cents"] == o["cum_filled_qty"] * 10000, "repo fill/principal evidence mismatch")
    require(not quantities, "orphan repo fills")
    seen = {r["broker_flow_id"] for r in receipts["repo_receipt_evidence"]}
    linked, matched_days, used_orders = [], set(), set()
    for row in cash["new_cash_rows"]:
        raw = row["raw_fields"]
        if raw["业务科目"] != "回购融券购回资金划入":
            continue
        match = re.fullmatch(r"融券购回204001:成交日期(\d{8})，申报委托号(\d{10})", raw["操作摘要"])
        require(match is not None, "unknown repo receipt identity")
        key = match.groups()
        o = orders.get(key)
        date = raw["交易日期"]
        date = date[:4]+"-"+date[4:6]+"-"+date[6:]
        require(date in EXTENSION_DATES and o is not None and key not in used_orders and row["broker_flow_id"] not in seen and raw["流水号"] == row["broker_flow_id"] and raw["投资者"] == row["account_id"] == ACCOUNT and raw["股票代码"] == "204001" and raw["交易日期"] > key[0] and number(raw["出金"]) == 0 and number(raw["入金"]) == number(row["actual_amount"]), "repo receipt identity/direction changed")
        principal = number(fee_orders[key]["gross_cents"]) / 100
        interest = number(row["actual_amount"]) - principal
        require(0 <= interest <= principal / 100, "repo amount is not full principal plus interest")
        evidence = {"trade_date":date,"source_trade_date":o["trade_date"],"gateway_order_id":o["gateway_order_id"],"broker_flow_id":row["broker_flow_id"],"principal":principal,"received_interest":interest,"raw_fields":copy.deepcopy(raw)}
        receipts["repo_receipt_evidence"].append(evidence)
        linked.append(evidence)
        seen.add(row["broker_flow_id"])
        used_orders.add(key)
        matched_days.add(date)
    require(matched_days == set(EXTENSION_DATES), "missing continuation repo receipt")
    fee_days = defaultdict(lambda: ZERO)
    for key, o in orders.items():
        if o["trade_date"] in EXTENSION_DATES:
            fee_days[o["trade_date"]] += number(fee_orders[key]["fee_cents"]) / 100
    return cost, receipts, dict(fee_days), linked


def statement_daily_comparison(days, contributions, extension, cash, fee_evidence):
    statements, order_fills, seen_fills = {}, defaultdict(list), set()
    for s in fee_evidence["orders"]:
        if s["trade_date"] not in {d.replace("-", "") for d in EXTENSION_DATES}:
            continue
        key = s["trade_date"], s["security_id"], s["broker_order_id"]
        require(key not in statements and s["account_id"] == ACCOUNT and s["side"] in ("买入", "卖出", "融券") and s["fee_amount_resolved"] and not s["unresolved_groups"] and type(s["fee_cents"]) is int and s["fee_cents"] >= 0, "ambiguous daily statement fee")
        statements[key] = s
    for f in extension["fills"]:
        if f["trade_date"] not in EXTENSION_DATES:
            continue
        key = f["trade_date"], f["gateway_order_id"]
        identity = key + (f["fill_id"],)
        require(f["account_id"] == ACCOUNT and type(f["qty"]) is int and f["qty"] > 0 and identity not in seen_fills, "duplicate/invalid daily fill")
        seen_fills.add(identity)
        order_fills[key].append(f)
    used, differences = set(), []
    for o in extension["orders"]:
        if o["trade_date"] not in EXTENSION_DATES or o["cum_filled_qty"] == 0:
            continue
        stream = o["order_stream_id"]
        require(o["account_id"] == ACCOUNT and o["exchange"] in ("SH", "SZ") and stream and o["gateway_order_id"] == "external-huaxin-"+ACCOUNT+"01-"+stream and o["business_type"] == "S" and o["trade_side"] in ("B", "S"), "unsupported daily order identity")
        candidates = [key for key in statements if key[:2] == (o["trade_date"].replace("-", ""), o["symbol"]+"."+o["exchange"]) and stream.endswith(key[2])]
        require(len(candidates) == 1, "ambiguous statement order suffix")
        key = candidates[0]
        s = statements.get(key)
        fills = order_fills.pop((o["trade_date"], o["gateway_order_id"]), [])
        repo = o["symbol"] == "204001" and o["exchange"] == "SH"
        require(s is not None and key not in used and s["side"] == ("融券" if repo else {"B":"买入","S":"卖出"}[o["trade_side"]]) and s["quantity"]*(10 if repo else 1) == o["cum_filled_qty"] and sum(f["qty"] for f in fills) == o["cum_filled_qty"], "daily statement/order/fill coverage differs")
        for f in fills:
            require(f["symbol"] == o["symbol"] and f["exchange"] == o["exchange"] and f["trade_side"] == o["trade_side"] and f["order_stream_id"] == stream, "daily fill identity differs")
        gross = number(o["cum_filled_qty"])*100 if repo else sum((number(f["price"])*f["qty"] for f in fills), ZERO)
        delta = number(s["gross_cents"])/100-gross
        if delta:
            differences.append({"trade_date":o["trade_date"],"gateway_order_id":o["gateway_order_id"],"broker_order_id":key[2],"security_id":key[1],"statement_gross_minus_fill_gross":delta})
        used.add(key)
    require(used == set(statements) and not order_fills, "incomplete daily fee and execution coverage")
    by_day = {d["trade_date"]:d for d in contributions}
    previous, result = "20261109", []
    for day in days:
        date = day["trade_date"]
        if date not in EXTENSION_DATES:
            continue
        compact = date.replace("-", "")
        funds = cash["days"][compact]["funds"]
        prior = cash["days"][previous]["funds"]
        require(funds["客户号"] == prior["客户号"] == ACCOUNT and funds["交易日"] == compact and prior["交易日"] == previous and number(funds["入金"]) == 0 and number(funds["出金"]) == 0, "unexpected statement identity/external flow")
        rows = [s for k,s in statements.items() if k[0] == compact]
        buy = sum((number(s["gross_cents"])/100 for s in rows if s["side"] == "买入"), ZERO)
        sell = sum((number(s["gross_cents"])/100 for s in rows if s["side"] == "卖出"), ZERO)
        fee = sum((number(s["fee_cents"])/100 for s in rows), ZERO)
        require(fee*100 == cash["days"][compact]["fees"]["fee_cents"], "statement daily fee total differs")
        mv_change = number(funds["市值"])-number(prior["市值"])
        pnl = mv_change+sell-buy+day["observed_repo_interest"]-fee
        require(number(funds["当日盈亏"]) == day["published_pnl"], "published and broker PnL differ")
        model = by_day[date]["contributions"]
        ordinary = [r for r in model if r["strategy_type"] != "cash_management"]
        require(all(r["strategy_type"] in ("etf_cross_section", "stock_cross_section") for r in ordinary), "unexpected T0 in ordinary daily comparison")
        model_mv = sum((number(r["close_value"])-number(r["open_value"]) for r in ordinary), ZERO)
        model_turnover = sum((number(r["sell_amount"])-number(r["buy_amount"]) for r in ordinary), ZERO)
        model_fee = sum((number(r["effective_fee"]) for r in ordinary), ZERO)+day["statement_repo_fee"]
        valuation_effect, turnover_effect, fee_effect = mv_change-model_mv, sell-buy-model_turnover, model_fee-fee
        result.append({"trade_date":date,"statement_order_count":len(rows),"statement_buy_amount":buy,"statement_sell_amount":sell,"statement_fee":fee,
                       "statement_market_value_change":mv_change,"statement_based_pnl":pnl,"published_pnl":day["published_pnl"],"statement_pnl_difference":day["published_pnl"]-pnl,
                       "valuation_precision_effect":valuation_effect,"turnover_precision_effect":turnover_effect,"fee_amount_effect":fee_effect,
                       "unexplained_model_difference":day["remaining_residual"]-valuation_effect-turnover_effect-fee_effect,
                       "order_identity_and_quantity_matched":True,"final_settlement":False})
        previous = compact
    require(len(result) == 4, "incomplete statement comparison dates")
    return {"days":result,"matched_orders":len(used),"matched_fills":len(seen_fills),"gross_differences":differences,
            "scope":"Independent account-day comparison; original fills, fees, assets and NAV unchanged. Amount differences retained, not silently rounded or reallocated."}


def build_report(cycles, cost, fee_evidence, receipt_evidence, extension=None, cash_extension=None):
    for source in (cycles, cost, fee_evidence, receipt_evidence):
        require(source["account_id"] == ACCOUNT and source["database_writes"] == 0 and source["broker_commands"] == 0, "wrong account/unsafe evidence")
        require(not source.get("economic_nav_publishable", False) and not source.get("final_settlement", False), "unexpected finality claim")
    require(cost["cost_chain_complete"] and not cost["error"] and cost["database_read_only"], "incomplete cost capture")
    repo_fees, linked_receipts = {}, []
    if extension is not None:
        cost, receipt_evidence, repo_fees, linked_receipts = continuation_evidence(cost, receipt_evidence, extension, cash_extension, fee_evidence)
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
            old_repo_fee = sum((number(x["effective_fee"]) for x in d["contributions"] if x["strategy_type"] == "cash_management"), ZERO)
            fee_adjustment = old_repo_fee - repo_fees.get(date, old_repo_fee)
            corrected = model + delta + adjustment + fee_adjustment
            r.update(status="conditional_bridge_not_final", observed_repo_interest=receipt_days[date], repo_timing_correction=adjustment, corrected_net=corrected, remaining_residual=r["published_pnl"]-corrected)
            if date in repo_fees:
                r.update(model_repo_fee=old_repo_fee, statement_repo_fee=repo_fees[date], repo_fee_correction=fee_adjustment,
                         cost_evidence=extension["cost_evidence"][date])
            revised = dict(strategies)
            if delta or "etf_redemption_t0" in revised:
                revised["etf_redemption_t0"] = revised.get("etf_redemption_t0", ZERO) + delta
            revised["cash_management"] = revised.get("cash_management", ZERO) + adjustment + fee_adjustment
            require(sum(revised.values(), ZERO) == corrected, "strategy aggregate mismatch")
            r["conditional_strategies"] = revised
            r["residual_after_asset_component_comparison"] = r["remaining_residual"] - r["other_asset_component_change"]
            r["asset_component_classification_verified"] = False
        days.append(r)
    result = {"schema":"relay.daily_attribution_bridge.v1", "account_id":ACCOUNT, "database_writes":0, "broker_commands":0,
            "economic_nav_publishable":False, "final_settlement":False, "source_sha256":{k:v[1] for k,v in INPUTS.items()},
            "scope":"Historical conditional daily bridge. Original model and NAV unchanged; missing observations are not zero. Repo uses observed posting basis, not a new final accrual policy.",
            "cycle_count":len(c["profits"]), "daily_cycle_parts":parts, "days":days,
            "summary": {"bridged_days":sum(d["status"] == "conditional_bridge_not_final" for d in days),
                        "uncovered_dates":[d["trade_date"] for d in days if d["status"] != "conditional_bridge_not_final"],
                        "max_absolute_remaining_comparison":max((abs(d["residual_after_asset_component_comparison"]) for d in days if "residual_after_asset_component_comparison" in d), default=None)}}
    if extension is not None:
        result["source_sha256"].update({k:v[1] for k,v in EXTENSION_INPUTS.items()})
        result["continuation_repo_receipts"] = linked_receipts
        result["cost_evidence"] = extension["cost_evidence"]
        result["statement_daily_comparison"] = statement_daily_comparison(days, cost["contributions"], extension, cash_extension, fee_evidence)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    parser.add_argument("--with-continuation", action="store_true", help="Include sealed November 10-13 read-only evidence")
    args = parser.parse_args()
    path = Path(args.output)
    require(not path.exists(), "output must be new")
    inputs = {k:checked_input(ROOT / p, h) for k,(p,h) in INPUTS.items()}
    extra = {k:checked_input(ROOT / p, h) for k,(p,h) in EXTENSION_INPUTS.items()} if args.with_continuation else {}
    result = build_report(inputs["cycles"], inputs["cost"], inputs["fees"], inputs["receipts"], extra.get("formal_extension"), extra.get("cash_extension"))
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
