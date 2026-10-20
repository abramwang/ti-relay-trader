#!/usr/bin/env python3
"""Audit actual ETF cash origins and published NAV gaps, without publishing NAV."""

from __future__ import annotations

import argparse
import collections
import gzip
import importlib.util
import itertools
import json
import re
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo


SPEC = importlib.util.spec_from_file_location("fuying_history", Path(__file__).with_name("rebuild-fuying13-history.py"))
HISTORY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HISTORY)
BROKER = HISTORY.BROKER
FORMULAS = {"broker_cash_repo_carveout.v1", "broker_statement_nav.v1"}


def etf_order_reference(summary):
    refs = set(re.findall(r"申报委托号([A-Za-z0-9]+)(?![A-Za-z0-9])", summary))
    refs.update(re.findall(r"^深圳ETF(?:赎回|申购)[0-9]{6}(?:\([0-9]{6}\))?(?:佣金|替代现金)[:：]([A-Za-z0-9]+)\s*$", summary))
    if len(refs) > 1:
        return None
    return next(iter(refs), None)


def order_index(executions):
    index = collections.defaultdict(lambda: collections.defaultdict(list))
    for row_number, row in executions:
        if row["方向"] not in ("ETF申购", "ETF赎回"):
            continue
        identity = row["委托编号"].strip()
        if not identity:
            continue
        day = BROKER.date(row["日期"])
        index[(identity, row["方向"])][day].append(row_number)
    return index


def resolve_origin(row, index):
    raw = row["raw_fields"]
    summary = raw["操作摘要"]
    posted = BROKER.date(raw["交易日期"])
    explicit = row["source_trade_date"]
    identity = etf_order_reference(summary)
    sides = [side for side in ("ETF申购", "ETF赎回") if side in summary]
    candidates = index.get((identity, sides[0]), {}) if identity and len(sides) == 1 else {}
    days = sorted(candidates)
    result = {"resolved_source_trade_date": explicit, "order_reference": identity,
              "source_date_resolution": "explicit_source_date" if explicit else "unresolved",
              "candidate_trade_dates": days}
    if explicit:
        BROKER.date(explicit)
        if explicit > posted:
            raise ValueError("explicit ETF origin occurs after cash posting")
        if days and explicit not in candidates:
            result["source_date_resolution"] = "explicit_origin_conflicts_with_execution_reference"
        return result
    if not identity or len(sides) != 1:
        result["source_date_resolution"] = "missing_unambiguous_etf_order_reference"
    elif not days:
        result["source_date_resolution"] = "order_reference_not_found_in_export"
    elif len(days) != 1:
        result["source_date_resolution"] = "order_reference_reused_across_dates"
    elif days[0] > posted:
        result["source_date_resolution"] = "execution_after_cash_posting"
    else:
        day = days[0]
        result.update(resolved_source_trade_date=day,
                      source_date_resolution="unique_historical_etf_order_reference",
                      matched_execution_rows=candidates[day])
    return result


def published_coverage(dates, response):
    if response.get("ok") is not True:
        raise ValueError("published NAV response is not successful")
    navs = response["data"]["navs"]
    if len(navs) != response["data"]["count"]:
        raise ValueError("published NAV response is truncated")
    observed = set(dates)
    published = set()
    for nav in navs:
        if nav["account_id"] != BROKER.ACCOUNT:
            raise ValueError("published NAV account mismatch")
        day = BROKER.date(nav["trade_date"].replace("-", ""))
        if not nav["is_current"] or nav["status"] == "blocked":
            continue
        if not (nav["formula_version"] in FORMULAS or nav["formula_version"].startswith("performance_economic_nav.v2")):
            continue
        if day not in observed or day in published:
            raise ValueError("duplicate or out-of-export published date")
        published.add(day)
    groups = []
    for state, group in itertools.groupby(sorted(observed), key=lambda day: day in published):
        days = list(group)
        groups.append({"published": state, "date_from": days[0], "date_to": days[-1], "observed_fund_days": len(days)})
    return {"observed_fund_days": len(observed), "published_days": len(published),
            "unpublished_days": len(observed - published), "segments": groups,
            "count_scope": "broker_export_fund_dates_not_inferred_calendar"}


def run(evidence, published_navs, output):
    report = HISTORY.verify_evidence(evidence)
    response = json.loads(published_navs.read_text())
    coverage = published_coverage([row["trade_date"] for row in report["book_days_not_economic_nav"]], response)
    executions = BROKER.rows(evidence / report["files"]["executions"]["snapshot"], "executions")
    index = order_index(executions)
    output.mkdir(mode=0o700)
    (output / "INCOMPLETE").write_text("ETF origin audit has not completed\n")
    counts, amounts, unresolved_subjects = collections.Counter(), collections.Counter(), collections.Counter()
    restored = same_day = explicit_conflicts = 0
    changes = collections.Counter()
    seen_flows = set()
    with gzip.open(evidence / "cash_evidence.jsonl.gz", "rt", encoding="utf-8") as source, gzip.open(output / "etf_cash_origins.jsonl.gz", "wt", encoding="utf-8", compresslevel=3) as target:
        for line in source:
            row = json.loads(line)
            if not row["etf_related"]:
                continue
            if row["account_id"] != BROKER.ACCOUNT or row["broker_flow_id"] in seen_flows:
                raise ValueError("cash account mismatch or duplicate flow identity")
            seen_flows.add(row["broker_flow_id"])
            resolution = resolve_origin(row, index)
            state = resolution["source_date_resolution"]
            amount = BROKER.cents(row["actual_amount"])
            counts[state] += 1
            amounts[state] += amount
            origin = resolution["resolved_source_trade_date"]
            posted = row["raw_fields"]["交易日期"]
            if state == "unique_historical_etf_order_reference":
                restored += 1
                same_day += origin == posted
            if state == "explicit_origin_conflicts_with_execution_reference":
                explicit_conflicts += 1
            if origin and state != "explicit_origin_conflicts_with_execution_reference":
                changes[origin] += amount
                changes[posted] -= amount
            else:
                unresolved_subjects[row["raw_fields"]["业务科目"]] += 1
            BROKER.dump_line(target, {**row, **resolution, "economic_nav_publishable": False,
                                     "actual_posted_cash_not_final_clearing_proof": True})
    daily = []
    balance = 0
    for day in sorted({row["trade_date"] for row in report["book_days_not_economic_nav"]} | set(changes)):
        balance += changes[day]
        daily.append({"trade_date": day, "retrospective_actual_cash_timing_adjustment": BROKER.money(balance), "economic_nav_publishable": False})
    if balance:
        raise ValueError("known actual cash timing adjustments do not reverse in full")
    (output / "actual_cash_timing_preview.json").write_text(HISTORY.json_text(daily))
    result = {"account_id": BROKER.ACCOUNT,
              "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
              "source_report_sha256": HISTORY.SOURCE_AUDIT_SHA256,
              "published_nav_response_sha256": HISTORY.sha_file(published_navs),
              "coverage": coverage, "etf_cash_rows": len(seen_flows),
              "origin_resolution_counts": dict(counts),
              "origin_resolution_signed_amounts": {key: BROKER.money(value) for key, value in amounts.items()},
              "newly_resolved_rows": restored, "newly_resolved_same_day_rows": same_day,
              "explicit_origin_conflicts": explicit_conflicts,
              "unresolved_subject_counts": dict(unresolved_subjects),
              "economic_nav_publishable": False, "published_nav_rows": 0, "database_writes": 0,
              "remaining_gates": ["No-reference or nonunique-reference cash origins remain unresolved.",
                                  "Actual posted rows do not prove private ETF clearing completeness or outstanding rights.",
                                  "PCF, final fees, dividend timing and current-day cutoff require separate validation.",
                                  "The complete cost/NAV chain and existing published cumulative baseline must be rebuilt together."],
              "artifact_sha256": {name: HISTORY.sha_file(output / name) for name in ("etf_cash_origins.jsonl.gz", "actual_cash_timing_preview.json")}}
    (output / "report.json").write_text(HISTORY.json_text(result))
    (output / "INCOMPLETE").unlink()
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--published-navs", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    print(HISTORY.json_text(run(args.evidence, args.published_navs, args.output)))
