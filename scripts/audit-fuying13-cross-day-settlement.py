#!/usr/bin/env python3
"""One-time read-only review of 768's 2026-10-12 redemption cash legs.

This report separates explicit-source facts from PCF/date candidates. It never
declares all private settlement complete or publishes a NAV.
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
    "same_day", Path(__file__).with_name("audit-fuying13-same-day-receipts.py"))
SAME = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SAME)
B, EXT = SAME.B, SAME.CHAIN.EXT
DAY, ETF = "20261012", "510310.SH"
HASHES = {
    "funds": "a8e5130576756acd0d8ce765b2f3a42901684ca09b78ac50722b3cd37c6c4379",
    "cash_flows": "dae4863b8e337b9c0b28ad37e84503f84a920e3ea066fc8f956771cc2748a224",
    "settlements": "d058cfedec9fa03c13747b720f6c13f01e0c50512274d87e1d5b7f108cc17e52",
    "executions": "8f550c0444b53ce141a4588a2d6baf67341ad4c3f626a3755e57391e558133df",
}


def body(response):
    if "ok" in response:
        if response["ok"] is not True:
            raise ValueError("failed Meridian response")
        response = response["data"]
    rows = response["data"]
    meta = response["meta"]
    if not isinstance(rows, list) or meta["count"] != len(rows):
        raise ValueError("truncated Meridian response")
    return rows


def refund_coverage(cash, pcf, rounds):
    expected, all_ids = set(), set()
    for row in body(pcf):
        identity = row["component_security_id"]
        if (row["security_id"] != ETF or str(row["trade_date"]) != DAY
                or row["schema_version"] != "etf_component.v2" or identity in all_ids):
            raise ValueError("wrong or duplicate component identity")
        all_ids.add(identity)
        if identity.endswith(".SZ") and B.quantity(str(row["stock_amount"])) > 0:
            expected.add(identity[:6])
    if len(all_ids) != 300 or len(expected) != 99:
        raise ValueError("reviewed PCF scope changed")
    counts = collections.Counter()
    receipts = []
    EXT.unique_rows(cash)
    for row in cash:
        raw = row["raw_fields"]
        if raw["业务科目"] != "ETF现金替代退款" or row["source_trade_date"] != DAY:
            continue
        match = re.fullmatch(r"20261012ETF申赎510310替代现金退款，股票代码(\d{6})", raw["操作摘要"])
        if (not match or raw["股票代码"] != "510310" or raw["交易日期"] != "20261013"
                or B.cents(row["actual_amount"]) <= 0 or match[1] not in expected):
            raise ValueError("refund source, posting date, component or sign mismatch")
        counts[match[1]] += 1
        receipts.append(row)
    if set(counts) != expected or any(n != rounds for n in counts.values()):
        raise ValueError("refund component multiset does not cover all reviewed baskets")
    return receipts


def cash_component_candidate(cash, pcf, deliveries, rounds):
    EXT.unique_rows(cash)
    row = SAME.one(body(pcf), "cash PCF")
    if (row["security_id"] != ETF or row["schema_version"] != "etf_cash_component.v1"
            or str(row["trade_date"]) != "20261013" or str(row["pre_trade_date"]) != DAY
            or B.quantity(str(row["unit_subscribe_redeem"])) != 900000):
        raise ValueError("cash PCF source/unit mismatch")
    amount = B.cents(str(row["cash_component"]))
    if amount != -290898:
        raise ValueError("reviewed cash component changed")
    rows = [r for r in cash if r["raw_fields"]["交易日期"] == "20261014"
            and r["raw_fields"]["股票代码"] == "510310"
            and r["raw_fields"]["业务科目"] == "ETF现金差额划出"]
    matches = [(n, r) for n, r in deliveries if r["成交日期"] == "20261013"
               and r["代码"] == ETF and r["方向"] == "ETF赎回"
               and B.quantity(r["成交数量"]) == 0 and B.cents(r["实付金额"]) == amount
               and all(B.cents(r[k]) == 0 for k in ("实收佣金", "印花税", "交易规费", "过户费"))]
    if (len(rows) != rounds or len(matches) != rounds
            or any(B.cents(r["actual_amount"]) != amount or r["source_trade_date"] is not None for r in rows)):
        raise ValueError("cash/deal/PCF multiset mismatch")
    return {"cash_rows": rows, "delivery_rows": [{"row": n, "raw": r} for n, r in matches],
            "amount": B.money(amount * rounds), "candidate_source_trade_date": DAY,
            "posting_date": "20261014", "delivery_date": "20261013",
            "source_evidence": "pcf_and_broker_delivery_multiset_candidate",
            "explicit_private_source_verified": False,
            "included_in_confirmed_source_cash": False}


def rights_path(receipts, dates):
    # Scope is ONLY the provided explicit-source legs, not total fund receivables.
    EXT.unique_rows(receipts)
    if any(r["source_trade_date"] != DAY or B.cents(r["actual_amount"]) <= 0 for r in receipts):
        raise ValueError("rights path requires positive explicit-source refunds")
    if any(B.date(day) < DAY for day in dates):
        raise ValueError("rights path starts before the source day")
    result = []
    total = sum(B.cents(r["actual_amount"]) for r in receipts)
    for day in dates:
        released = sum(B.cents(r["actual_amount"]) for r in receipts if r["raw_fields"]["交易日期"] <= day)
        result.append({"date": day, "known_receipts_remaining": B.money(total - released),
                       "known_receipts_posted": B.money(released), "all_rights_closed": False})
    return result


def audit(reference, components, cost_review):
    hashes = {k: hashlib.sha256((reference / f"{B.ACCOUNT}_{v}.csv").read_bytes()).hexdigest()
              for k, v in B.FILES.items()}
    if hashes != HASHES:
        raise ValueError("reviewed exports changed; refresh evidence review first")
    same = SAME.audit(reference)
    cash = EXT.read_cash(reference / f"{B.ACCOUNT}_资金流水.csv")
    EXT.unique_rows(cash)
    funds, bridges = EXT.fund_bridges(list(B.rows(reference / f"{B.ACCOUNT}_资金.csv", "funds")), cash)
    executions = list(B.rows(reference / f"{B.ACCOUNT}_历史成交.csv", "executions"))
    index = EXT.ORIGINS.order_index(executions)
    order_ids = sorted({r["broker_order_id"] for r in same["receipts"]})
    if len(order_ids) != 3 or B.cents(same["included_cash_total"]) != 380287620:
        raise ValueError("reviewed same-day receipts changed")
    for order in order_ids:
        parent = SAME.one([r for _, r in executions if r["日期"] == DAY and r["代码"] == ETF
                          and r["方向"] == "ETF赎回" and r["委托编号"] == order
                          and B.quantity(r["成交量"]) > 0], "redemption parent")
        if B.quantity(parent["成交量"]) != 900000:
            raise ValueError("redemption quantity changed")
    refunds = refund_coverage(cash, components, len(order_ids))
    deliveries = list(B.rows(reference / f"{B.ACCOUNT}_历史交割单.csv", "settlements"))
    candidate = cash_component_candidate(cash, cost_review["cash_component_pcf"], deliveries, len(order_ids))
    fees = []
    for row in cash:
        raw = row["raw_fields"]
        if raw["交易日期"] != DAY or raw["业务科目"] != "unknown type" or "ETF赎回" not in raw["操作摘要"]:
            continue
        origin = EXT.ORIGINS.resolve_origin(row, index)
        if origin["resolved_source_trade_date"] == DAY and origin["order_reference"] in order_ids:
            if B.cents(row["actual_amount"]) >= 0:
                raise ValueError("redemption fee has unexpected sign")
            fees.append(row)
    if len(fees) != 522 or sum(B.cents(r["actual_amount"]) for r in fees) != -69192:
        raise ValueError("reviewed source-day redemption fee coverage changed")
    if cost_review["account_id"] != B.ACCOUNT or cost_review["database_writes"] != 0 or not cost_review["database_read_only"]:
        raise ValueError("wrong cost review scope")
    source_cost = SAME.one([r for r in cost_review["costs"] if r["trade_date"] == "2026-10-12"], "source cost")
    if (source_cost["summary"]["t0_buy_quantity"] != 2700000
            or source_cost["summary"]["t0_residual_quantity"] != 5900
            or source_cost["summary"]["quantity_breaks"] != 0):
        raise ValueError("reviewed redemption/residual split changed")
    raw_refunds = sum(B.cents(r["actual_amount"]) for r in refunds)
    conditional = raw_refunds + B.cents(candidate["amount"])
    if hashes != {k: hashlib.sha256((reference / f"{B.ACCOUNT}_{v}.csv").read_bytes()).hexdigest() for k, v in B.FILES.items()}:
        raise ValueError("source changed during review")
    return {"schema": "fuying13_cross_day_cash_review.v1", "account_id": B.ACCOUNT,
            "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
            "source_trade_date": DAY, "source_sha256": hashes, "funds_bridges": bridges,
            "same_day_cash_audit": same, "source_redemption_orders": order_ids,
            "source_redemption_fee_rows": fees, "source_redemption_fees": "691.92",
            "refund_rows": refunds, "confirmed_later_refunds": B.money(raw_refunds),
            "refund_association_scope": "account_source_day_etf_not_individual_basket",
            "cash_component_candidate": candidate,
            "known_refund_release": rights_path(refunds, [DAY, "20261013", "20261014"]),
            "conditional_source_close_carry_if_candidate_confirmed": B.money(conditional),
            "conditional_source_day_pnl_if_candidate_confirmed": B.money(B.cents(funds[DAY]["当日盈亏"]) + conditional),
            "cost_chain_complete": cost_review["cost_chain_complete"],
            "first_cost_blocked_date": cost_review["first_cost_blocked_date"],
            "remaining_gates": ["cash component private source and asset inclusion need separate confirmation",
                                "source-day cash is already included; never add it again",
                                "later cost chain has independent blockers; do not propagate blocked NAV"],
            "private_final_clearing_verified": False, "economic_nav_publishable": False,
            "database_writes": 0, "broker_commands": 0}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", type=Path, default=Path("reference"))
    parser.add_argument("--components", type=Path, required=True)
    parser.add_argument("--cost-review", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.output.exists():
        parser.error("output already exists")
    inputs = {"components": args.components.read_bytes(), "cost_review": args.cost_review.read_bytes()}
    result = audit(args.reference, json.loads(inputs["components"]), json.loads(inputs["cost_review"]))
    result["input_sha256"] = {k: hashlib.sha256(v).hexdigest() for k, v in inputs.items()}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w", encoding="utf-8") as target:
        json.dump(result, target, ensure_ascii=False, indent=2)
        target.write("\n")
    print(json.dumps({k: result[k] for k in ("confirmed_later_refunds", "conditional_source_close_carry_if_candidate_confirmed",
        "conditional_source_day_pnl_if_candidate_confirmed", "first_cost_blocked_date", "economic_nav_publishable")}, ensure_ascii=False))
