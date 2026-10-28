#!/usr/bin/env python3
"""Audit an extension of frozen broker cash evidence. Never publish or write DB."""

from __future__ import annotations

import argparse
import collections
import gzip
import importlib.util
import json
import os
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo


SPEC = importlib.util.spec_from_file_location("origins", Path(__file__).with_name("audit-fuying13-etf-origins.py"))
ORIGINS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ORIGINS)
HISTORY, BROKER = ORIGINS.HISTORY, ORIGINS.BROKER
PLAN_HASH = "2aaeabcb3b65fa9201bf5ddc0c038b61c1dddf42253d6cbe0bfa3a2d212b40cd"
DISPLAY_FIELDS = {"股票名称", "名称"}
MONEY_FIELDS = {"入金", "出金", "资金余额"}


def signature(raw):
    return {k: BROKER.cents(v) if k in MONEY_FIELDS and v else v
            for k, v in raw.items() if k not in DISPLAY_FIELDS}


def read_cash(path):
    result = []
    for number, raw in BROKER.rows(path, "cash_flows"):
        day, at = BROKER.date(raw["交易日期"]), BROKER.clock(raw["交易时间"])
        incoming, outgoing = BROKER.cents(raw["入金"]), BROKER.cents(raw["出金"])
        if incoming < 0 or outgoing < 0 or incoming and outgoing:
            raise ValueError("invalid cash debit/credit legs")
        subject = raw["业务科目"]
        etf = subject.startswith("ETF") or subject == "unknown type" and "ETF" in raw["操作摘要"]
        origin = BROKER.source_trade_date(raw["操作摘要"]) if etf else None
        if origin and origin > day:
            raise ValueError("source date after cash posting")
        result.append({"account_id": BROKER.ACCOUNT, "source_row": number,
                       "broker_flow_id": raw["流水号"], "raw_fields": raw,
                       "actual_amount": BROKER.money(incoming - outgoing),
                       "cash_posted_at": datetime.strptime(day + at, "%Y%m%d%H:%M:%S").replace(
                           tzinfo=ZoneInfo("Asia/Shanghai")).isoformat(),
                       "etf_related": etf, "source_trade_date": origin})
    return result


def unique_rows(rows):
    result = {}
    for row in rows:
        identity = row["broker_flow_id"]
        if row["account_id"] != BROKER.ACCOUNT or not identity or identity in result:
            raise ValueError("wrong account or missing/duplicate cash identity")
        result[identity] = row
    return result


def compare_exports(old, new):
    old, new = unique_rows(old), unique_rows(new)
    if not new:
        raise ValueError("empty new export")
    first, last = min(r["raw_fields"]["交易日期"] for r in new.values()), max(
        r["raw_fields"]["交易日期"] for r in new.values())
    overlap = names = 0
    for identity, row in old.items():
        if first <= row["raw_fields"]["交易日期"] <= last and identity not in new:
            raise ValueError("old cash row missing inside new coverage")
        if identity not in new:
            continue
        if signature(row["raw_fields"]) != signature(new[identity]["raw_fields"]):
            raise ValueError("overlapping cash identity changed financial facts")
        overlap += 1
        names += any(row["raw_fields"].get(k) != new[identity]["raw_fields"].get(k) for k in DISPLAY_FIELDS)
    return [row for identity, row in new.items() if identity not in old], {
        "new_coverage": [first, last], "overlap_rows": overlap,
        "display_only_changes": names, "financial_conflicts": 0}


def fund_bridges(funds, cash):
    by_day = {}
    for _, raw in funds:
        day = BROKER.date(raw["交易日"])
        if day in by_day:
            raise ValueError("duplicate funds date")
        by_day[day] = raw
    if len(by_day) < 2:
        raise ValueError("two funds dates required")
    net, external = collections.Counter(), collections.Counter()
    for row in cash:
        raw, amount = row["raw_fields"], BROKER.cents(row["actual_amount"])
        net[raw["交易日期"]] += amount
        if raw["业务科目"] in ("银证转帐存", "银证转帐取"):
            external[raw["交易日期"]] += amount
    days = sorted(by_day)
    for i, day in enumerate(days):
        row = by_day[day]
        flow = BROKER.cents(row["入金"]) - BROKER.cents(row["出金"])
        if external[day] != flow:
            raise ValueError("bank flow differs from funds report")
        if i:
            previous = by_day[days[i - 1]]
            if BROKER.cents(row["客户资金"]) - BROKER.cents(previous["客户资金"]) != net[day]:
                raise ValueError("cash bridge mismatch")
            if BROKER.cents(row["总资产"]) - BROKER.cents(previous["总资产"]) != flow + BROKER.cents(row["当日盈亏"]):
                raise ValueError("asset bridge mismatch")
    missing = sorted(set(net) - set(by_day))
    if any(days[0] < day < days[-1] for day in missing):
        raise ValueError("cash date missing inside funds coverage")
    return by_day, {"funds_days": len(days), "cash_bridges_passed": len(days) - 1,
                    "cash_dates_without_funds": missing}


def resolved_deltas(additions, index):
    resolved, unresolved = [], []
    for row in additions:
        if not row["etf_related"]:
            continue
        match = ORIGINS.resolve_origin(row, index)
        item = {**row, **match, "private_final_clearing_verified": False}
        if match["resolved_source_trade_date"] and match["source_date_resolution"] != "explicit_origin_conflicts_with_execution_reference":
            resolved.append(item)
        else:
            unresolved.append(item)
    return resolved, unresolved


def carry(rows, day):
    # Only known incremental cash, not a claim that unobserved rights are zero.
    return sum(BROKER.cents(r["actual_amount"]) for r in rows
               if r["resolved_source_trade_date"] <= day < r["raw_fields"]["交易日期"])


def preview(nav_rows, resolved, funds):
    rows = sorted(nav_rows, key=lambda r: r["trade_date"])
    result = []
    for i, nav in enumerate(rows):
        if nav["account_id"] != BROKER.ACCOUNT:
            raise ValueError("NAV account mismatch")
        day = BROKER.date(nav["trade_date"].replace("-", ""))
        if i and rows[i - 1]["trade_date"] == nav["trade_date"]:
            raise ValueError("duplicate NAV date")
        if day not in funds:
            continue
        recorded = nav["pnl_components"]["broker_statement"]
        for field, column in [("reported_close_asset", "总资产"), ("cash_total", "客户资金"),
                              ("reported_market_value", "市值"), ("reported_daily_pnl", "当日盈亏")]:
            if BROKER.cents(str(recorded[field])) != BROKER.cents(funds[day][column]):
                raise ValueError("new funds changed the published NAV input")
        closing = carry(resolved, day)
        opening = carry(resolved, rows[i - 1]["trade_date"].replace("-", "")) if i else None
        valuation = nav["pnl_components"].get("market_valuation", {})
        result.append({"trade_date": day, "published_day_pnl": str(nav["account_day_pnl"]),
                       "published_close_asset": str(nav["close_economic_nav"]),
                       "existing_unclassified_other_asset": valuation.get("broker_reported_other_asset"),
                       "other_asset_overlap_verified": False,
                       "known_additional_open_rights": BROKER.money(opening) if opening is not None else None,
                       "known_additional_close_rights": BROKER.money(closing),
                       "conditional_day_pnl": BROKER.money(BROKER.cents(str(nav["account_day_pnl"])) + closing - opening) if opening is not None else None,
                       "conditional_close_asset": BROKER.money(BROKER.cents(str(nav["close_economic_nav"])) + closing),
                       "economic_nav_publishable": False})
    return result


def component_date_candidates(deliveries, pcf_responses, orders):
    """Cross-check processing dates; never replace a private cash source identity."""
    result, seen = [], set()
    identities = set()
    for order in orders:
        identity = (order["account_id"], order["trade_date"], order["order_stream_id"])
        if order["account_id"] != BROKER.ACCOUNT or not order["order_stream_id"] or identity in identities:
            raise ValueError("invalid/duplicate order evidence")
        identities.add(identity)
    for response in pcf_responses:
        body = response["response"]
        if body.get("ok") is not True:
            raise ValueError("unsuccessful PCF evidence")
        rows = body["data"]["data"]
        if body["data"]["meta"]["count"] != len(rows):
            raise ValueError("PCF evidence is truncated")
        for pcf in rows:
            day, origin = BROKER.date(str(pcf["trade_date"])), BROKER.date(str(pcf["pre_trade_date"]))
            security = pcf["security_id"]
            if (security, day) in seen or origin >= day:
                raise ValueError("duplicate PCF identity or invalid previous date")
            seen.add((security, day))
            unit, amount = BROKER.quantity(str(pcf["unit_subscribe_redeem"])), BROKER.cents(str(pcf["cash_component"]))
            if not unit or not amount:
                continue
            source_orders = [o for o in orders if o["trade_date"].replace("-", "") == origin
                             and o["symbol"] + "." + o["exchange"] == security and o["cum_filled_qty"] > 0
                             and o["trade_side"] == "R"]
            if not source_orders:
                continue
            expected = collections.Counter()
            for order in source_orders:
                if order["account_id"] != BROKER.ACCOUNT or order["cum_filled_qty"] % unit:
                    raise ValueError("PCF candidate order account/quantity mismatch")
                expected[amount * (order["cum_filled_qty"] // unit)] += 1
            candidates = [(n, r) for n, r in deliveries if r["成交日期"] == day and r["代码"] == security
                          and r["方向"] == "ETF赎回" and BROKER.quantity(r["成交数量"]) == 0
                          and all(BROKER.cents(r[k]) == 0 for k in ("实收佣金", "印花税", "交易规费", "过户费"))
                          and BROKER.cents(r["实付金额"]) in expected]
            observed = collections.Counter(BROKER.cents(r["实付金额"]) for _, r in candidates)
            if not candidates or observed != expected:
                continue
            result.append({"security_id": security, "delivery_date": day,
                           "candidate_economic_source_date": origin, "pcf_cash_per_unit": BROKER.money(amount),
                           "unit_subscribe_redeem": unit, "delivery_rows": [n for n, _ in candidates],
                           "aggregate_cash": BROKER.money(sum(BROKER.cents(r["实付金额"]) for _, r in candidates)),
                           "candidate_orders": [o["order_stream_id"] for o in source_orders],
                           "resolution": "pcf_and_order_multiset_inference_not_explicit_private_source",
                           "economic_nav_publishable": False})
    return result


def run(evidence, cash_path, funds_path, plan_path, output, pcf_path=None, orders_path=None):
    report = HISTORY.verify_evidence(evidence)
    if HISTORY.sha_file(plan_path) != PLAN_HASH:
        raise ValueError("original published plan changed")
    hashes = {"cash": HISTORY.sha_file(cash_path), "funds": HISTORY.sha_file(funds_path),
              "original_plan": PLAN_HASH, "old_audit": HISTORY.SOURCE_AUDIT_SHA256}
    with gzip.open(evidence / "cash_evidence.jsonl.gz", "rt", encoding="utf-8") as source:
        old = [json.loads(line) for line in source]
    new = read_cash(cash_path)
    additions, overlap = compare_exports(old, new)
    funds, bridges = fund_bridges(list(BROKER.rows(funds_path, "funds")), new)
    index = ORIGINS.order_index(BROKER.rows(evidence / report["files"]["executions"]["snapshot"], "executions"))
    resolved, unresolved = resolved_deltas(additions, index)
    plan = json.loads(plan_path.read_text())["navs"]
    days = preview(plan, resolved, funds)
    candidates = []
    if bool(pcf_path) != bool(orders_path):
        raise ValueError("PCF and order evidence must be supplied together")
    if pcf_path:
        hashes.update(pcf=HISTORY.sha_file(pcf_path), orders=HISTORY.sha_file(orders_path))
        candidates = component_date_candidates(
            list(BROKER.rows(evidence / report["files"]["settlements"]["snapshot"], "settlements")),
            json.loads(pcf_path.read_text()), json.loads(orders_path.read_text())["orders"])
    groups = collections.defaultdict(lambda: {"rows": 0, "cents": 0, "flow_ids": []})
    for row in resolved:
        raw = row["raw_fields"]
        key = (row["resolved_source_trade_date"], raw["交易日期"], raw["股票代码"], raw["业务科目"])
        groups[key]["rows"] += 1
        groups[key]["cents"] += BROKER.cents(row["actual_amount"])
        groups[key]["flow_ids"].append(row["broker_flow_id"])
    source_posting = [{"source_trade_date": key[0], "posted_date": key[1], "security_code": key[2],
                       "subject": key[3], "rows": value["rows"], "amount": BROKER.money(value["cents"]),
                       "flow_ids": value["flow_ids"]} for key, value in sorted(groups.items())]
    cash_cutoff = overlap["new_coverage"][1]
    result = {"account_id": BROKER.ACCOUNT, "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
              "source_hashes": hashes, "export_comparison": overlap, "funds_validation": bridges,
              "additional_cash_rows": len(additions), "resolved_additional_etf_rows": len(resolved),
              "unresolved_additional_etf_rows": len(unresolved), "source_posting_groups": source_posting,
              "resolved_cash": resolved, "unresolved_cash": unresolved, "daily_conditional_preview": days,
              "old_cash_component_date_candidates": candidates,
              "known_incremental_rights_at_cash_cutoff": BROKER.money(carry(resolved, cash_cutoff)),
              "zero_balance_scope": "new_observed_cash_only_not_all_outstanding_entitlements",
              "economic_nav_publishable": False, "private_final_clearing_verified": False,
              "database_writes": 0, "broker_commands": 0,
              "remaining_gates": ["Unresolved cash origins are not zero rights.",
                                  "PCF inference is excluded from this explicit-source preview.",
                                  "Old unlabelled delivery dates may be processing dates, not economic source dates.",
                                  "Reported other assets must not overlap added ETF rights; economic scope is unverified.",
                                  "Existing NAV and inventory/cash classification still require separate review.",
                                  "No NAV is constructed after the original plan cutoff."]}
    if HISTORY.sha_file(cash_path) != hashes['cash'] or HISTORY.sha_file(funds_path) != hashes['funds']:
        raise ValueError("source file changed during audit")
    if pcf_path and (HISTORY.sha_file(pcf_path) != hashes['pcf'] or HISTORY.sha_file(orders_path) != hashes['orders']):
        raise ValueError("PCF/order evidence changed during audit")
    output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with os.fdopen(os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as target:
        json.dump(result, target, ensure_ascii=False, indent=2, allow_nan=False)
        target.flush()
        os.fsync(target.fileno())
    return {k: v for k, v in result.items() if k not in {
        "resolved_cash", "unresolved_cash", "source_posting_groups", "daily_conditional_preview"}}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("evidence", "cash", "funds", "plan", "output"):
        parser.add_argument("--" + flag, type=Path, required=True)
    parser.add_argument("--pcf-evidence", type=Path)
    parser.add_argument("--orders", type=Path)
    args = parser.parse_args()
    print(json.dumps(run(args.evidence, args.cash, args.funds, args.plan, args.output,
                         args.pcf_evidence, args.orders), ensure_ascii=False, indent=2))
