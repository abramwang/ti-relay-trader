#!/usr/bin/env python3
"""One-time offline explanation of historical malformed ETF records; no repair."""

import argparse
from collections import Counter
from datetime import datetime
from decimal import Decimal
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
from zoneinfo import ZoneInfo

SPEC = importlib.util.spec_from_file_location(
    "cash_extension", Path(__file__).with_name("audit-fuying13-cash-extension.py"))
EXT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(EXT)
B = EXT.BROKER
TZ = ZoneInfo("Asia/Shanghai")


def review_record(day, issue, cash, executions, deliveries):
    raw, parent = issue["raw"], issue["parent_order_review"]
    order = parent.get("order", {})
    fields = order.get("fields", {})
    key = json.loads(order.get("key", "[]"))
    stream = raw.get("order_stream_id", "")
    match = re.fullmatch(r"11001([0-9]{10})", stream)
    if (parent["state"] != "unique_redemption_parent_identity" or not match
            or key != [B.ACCOUNT, day, raw["gateway_order_id"], ""]
            or raw["gateway_order_id"] != "external-huaxin-" + B.ACCOUNT + "01-" + stream
            or raw["account_id"] not in (B.ACCOUNT, B.ACCOUNT + "01")
            or raw["trade_date"] != day.replace("-", "")
            or raw["symbol"] != "" or raw["trade_side"] != "R"
            or raw["business_type"] != "E" or raw["exchange"] != "SH"
            or raw["qty"] != 1 or raw["record_type"] != "trade_fill"
            or raw["is_transfer"] is not False or not raw["fill_id"]
            or fields.get("order_stream_id") != stream
            or fields.get("order_id") != raw["order_id"]
            or fields.get("trade_side") != "R" or fields.get("business_type") != "E"
            or fields.get("exchange") != "SH" or fields.get("status") != "filled"
            or fields.get("is_terminal") is not True
            or raw["basket_id"] != "etfarb#" + str(fields.get("symbol"))):
        raise ValueError("special record/parent scope not proven")
    ref, code = match[1], fields["symbol"]
    amount = B.cents(str(raw["price"]))
    at = datetime.fromisoformat(raw["matched_at"])
    if amount <= 0 or at.tzinfo is None or at.astimezone(TZ).date().isoformat() != day:
        raise ValueError("invalid record amount/time")
    if int(at.timestamp() * 1000) != raw["match_timestamp"]:
        raise ValueError("match time disagrees with timestamp")
    compact, clock = day.replace("-", ""), at.astimezone(TZ).strftime("%H:%M:%S")
    related = [r for r in cash if EXT.ORIGINS.etf_order_reference(r["raw_fields"]["操作摘要"]) == ref
               and r["raw_fields"]["业务科目"] == "ETF赎回替代现金划入"]
    # No amount-only pairing, earliest-date guesses or silent partial selection.
    if not related or any(r["account_id"] != B.ACCOUNT
            or r["raw_fields"]["交易日期"] != compact
            or r["raw_fields"]["股票代码"] != code
            or (r["source_trade_date"] is not None and r["source_trade_date"] != compact)
            or B.cents(r["actual_amount"]) <= 0
            or datetime.fromisoformat(r["cash_posted_at"]) < at for r in related):
        raise ValueError("cash reference/date/direction ambiguous")
    if sum(B.cents(r["actual_amount"]) for r in related) != amount:
        raise ValueError("cash split total differs from original record")
    parts = Counter(B.cents(r["actual_amount"]) for r in related)
    evidence = {}
    for kind, rows in (("executions", executions), ("settlements", deliveries)):
        date_key, time_key = ("日期", "时间") if kind == "executions" else ("成交日期", "成交时间")
        dates = [B.date(value) for value in {r[date_key] for _, r in rows}]
        if not dates:
            raise ValueError("empty broker export")
        covered = min(dates) <= compact <= max(dates)
        selected = [(n, r) for n, r in rows if r[date_key] == compact
                    and r[time_key] == clock and r["代码"] == code + ".SH"
                    and r["方向"] == "ETF赎回" and B.cents(r["成交金额"]) > 0
                    and (kind != "executions" or r["委托编号"] == ref)]
        if kind == "executions":
            ref_days = {r["日期"] for _, r in rows if r["方向"] == "ETF赎回" and r["委托编号"] == ref}
            if ref_days and ref_days != {compact}:
                raise ValueError("broker order reference reused across days")
        if covered:
            if Counter(B.cents(r["成交金额"]) for _, r in selected) != parts:
                raise ValueError("broker split multiset mismatch: " + kind)
            for _, r in selected:
                qty_key, price_key = ("成交量", "成交价") if kind == "executions" else ("成交数量", "成交价格")
                if B.quantity(r[qty_key]) != 0 or B.number(r[price_key]) != 0:
                    raise ValueError("cash leg has ordinary execution quantity/price")
                if kind == "settlements" and B.cents(r["实付金额"]) != B.cents(r["成交金额"]):
                    raise ValueError("delivery net differs from cash amount")
        elif selected:
            raise ValueError("inconsistent export coverage")
        evidence[kind] = {"state": "cash_split_multiset_matched" if covered else "outside_export_coverage",
                          "coverage": [min(dates), max(dates)],
                          "rows": [{"source_row": n, "raw": r} for n, r in selected]}
    return {"trade_date": day, "fill_id": raw["fill_id"], "gateway_order_id": raw["gateway_order_id"],
            "broker_order_reference": ref, "parent_security_id": code + ".SH",
            "raw_issue": issue, "original_amount": B.money(amount), "cash_rows": related,
            "broker_evidence": evidence, "state": "same_day_substitution_cash_split_proven",
            "ordinary_fill": False, "inferred_cash_posting_at_match_time": False,
            "settlement_complete": False, "cost_publishable": False, "economic_nav_publishable": False}


def audit(report_path, expected_sha, reference):
    original = report_path.read_bytes()
    if hashlib.sha256(original).hexdigest() != expected_sha:
        raise ValueError("query evidence SHA mismatch")
    report = json.loads(original, parse_float=Decimal)
    if (report["account_id"] != B.ACCOUNT or report["environment"] != "production"
            or report["database_writes"] != 0 or report["broker_commands"] != 0
            or report["cost_publishable"] is not False or report["economic_nav_publishable"] is not False
            or report["book_coverage_requested"] is not True):
        raise ValueError("query evidence scope mismatch")
    paths = {k: reference / f"{B.ACCOUNT}_{v}.csv" for k, v in B.FILES.items()}
    hashes = {k: hashlib.sha256(p.read_bytes()).hexdigest() for k, p in paths.items()}
    cash = EXT.read_cash(paths["cash_flows"])
    EXT.unique_rows(cash)
    funds = list(B.rows(paths["funds"], "funds"))
    by_day, bridges = EXT.fund_bridges(funds, cash)
    executions = list(B.rows(paths["executions"], "executions"))
    deliveries = list(B.rows(paths["settlements"], "settlements"))
    rows, seen, used_flows, used_export_rows = [], set(), set(), set()
    for day in report["days"]:
        for attempt in day["attempts"]:
            for q in attempt["queries"]:
                coverage = q.get("book_coverage", {})
                for issue in coverage.get("decode_issues", []):
                    if (q["step"] != "fills" or q["state"] != "terminal_archive_consistent"
                            or coverage["request_state"] != "account_day_unfiltered_request"
                            or coverage["rows"]["state"] != "not_proven"):
                        raise ValueError("failed page/archive scope changed")
                    identity = (day["trade_date"], issue["raw"]["gateway_order_id"], issue["raw"]["fill_id"])
                    if identity in seen:
                        raise ValueError("duplicate special identity")
                    seen.add(identity)
                    r = review_record(day["trade_date"], issue, cash, executions, deliveries)
                    compact = day["trade_date"].replace("-", "")
                    if compact not in by_day or compact == min(by_day):
                        raise ValueError("no adjacent funds cash bridge")
                    for flow in r["cash_rows"]:
                        if flow["broker_flow_id"] in used_flows:
                            raise ValueError("cash flow reused across special records")
                        used_flows.add(flow["broker_flow_id"])
                    for kind, item in r["broker_evidence"].items():
                        for source in item["rows"]:
                            key = kind, source["source_row"]
                            if key in used_export_rows:
                                raise ValueError("export row reused")
                            used_export_rows.add(key)
                    r["included_in_broker_eod_cash_bridge"] = True
                    rows.append(r)
    if not rows or len(rows) != report["summary"]["book:fills:invalid_records"]:
        raise ValueError("special record count mismatch")
    if hashes != {k: hashlib.sha256(p.read_bytes()).hexdigest() for k, p in paths.items()} or report_path.read_bytes() != original:
        raise ValueError("source changed during review")
    return {"schema": "fuying13_special_cash_records_audit.v1", "account_id": B.ACCOUNT,
            "generated_at": datetime.now(TZ).isoformat(), "query_evidence_sha256": expected_sha,
            "source_sha256": hashes, "records": rows, "fund_bridges": bridges,
            "summary": {"records": len(rows), "cash_rows": len(used_flows),
                        "amount": B.money(sum(B.cents(r["original_amount"]) for r in rows)),
                        "execution_covered": sum(r["broker_evidence"]["executions"]["state"] == "cash_split_multiset_matched" for r in rows)},
            "database_writes": 0, "broker_commands": 0, "cost_publishable": False,
            "economic_nav_publishable": False, "settlement_complete": False,
            "scope": "historical evidence explanation only; failed source pages remain failed; broker EOD inclusion does not prove OC intraday cash scope or all remaining ETF rights"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--query-evidence", required=True, type=Path)
    parser.add_argument("--query-sha256", required=True)
    parser.add_argument("--reference", default=Path("reference"), type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    result = audit(args.query_evidence, args.query_sha256, args.reference)
    # Decimal preserves source amounts in evidence without rounding binary floats.
    def encode_decimal(value):
        if isinstance(value, Decimal):
            return str(value)
        raise TypeError("unsupported evidence value")
    encoded = json.dumps(result, ensure_ascii=False, indent=2, default=encode_decimal)
    with os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as target:
        target.write(encoded + "\n")
    print(json.dumps(result["summary"], ensure_ascii=False))


if __name__ == "__main__":
    main()
