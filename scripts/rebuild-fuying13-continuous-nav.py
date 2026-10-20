#!/usr/bin/env python3
"""Reconstruct account NAV from verified posting/source ledgers, without DB writes."""

from __future__ import annotations

import argparse
import collections
import csv
import gzip
import importlib.util
import json
from datetime import datetime
from decimal import Decimal
from pathlib import Path
from zoneinfo import ZoneInfo


SPEC = importlib.util.spec_from_file_location("history", Path(__file__).with_name("rebuild-fuying13-history.py"))
HISTORY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HISTORY)
BROKER = HISTORY.BROKER
RECONSTRUCTION_HASH = "34da2590ce02030fb9c651b2eb08a7f9fb513dfe9f456d2372595fe80ce89270"
PREFIX_HASH = "b7ad9c118c9db17e2524a83c665f0cac7ac9e6d1b641d4f7f411e6455a1a9af3"
FORMULA = "broker_reconstructed_economic_nav.v1"
SOURCE = "broker_fuying13_continuous_ledgers_20261020"


def settlement_kind(row):
    amount = BROKER.cents(row["实付金额"])
    fees = sum(BROKER.cents(row[key]) for key in ("实收佣金", "印花税", "交易规费", "过户费"))
    if amount < 0 and amount == -BROKER.cents(row["过户费"]):
        return "transfer_fee"
    if amount < 0 and amount == -fees:
        return "commission"
    return "cash"


def posting_kind(row):
    raw = row["raw_fields"]
    if "过户费" in raw["操作摘要"]:
        return "transfer_fee"
    if raw["业务科目"] == "unknown type" and BROKER.cents(row["actual_amount"]) < 0:
        return "commission"
    return "cash"


def etf_ledger_bridge(settlements, postings, dates):
    source_keys, posted_keys = collections.Counter(), collections.Counter()
    source_dates, required_dates = collections.Counter(), collections.Counter()
    sources, paid, shifts = collections.Counter(), collections.Counter(), collections.Counter()
    known = collections.Counter()
    for number, row in settlements:
        if row["方向"] not in ("ETF申购", "ETF赎回"):
            continue
        amount = BROKER.cents(row["实付金额"])
        if not amount:
            continue
        day = BROKER.date(row["成交日期"])
        kind = settlement_kind(row)
        key = (kind, row["代码"].split(".")[0], amount)
        source_keys[key] += 1
        source_dates[(day, *key)] += 1
        sources[day] += amount
    for row in postings:
        if not row["etf_related"]:
            continue
        raw = row["raw_fields"]
        day = BROKER.date(raw["交易日期"])
        amount = BROKER.cents(row["actual_amount"])
        if not amount:
            continue
        kind = posting_kind(row)
        key = (kind, raw["股票代码"], amount)
        posted_keys[key] += 1
        paid[day] += amount
        origin = row["source_trade_date"]
        if origin:
            BROKER.date(origin)
            if origin > day:
                raise ValueError("cash source date occurs after posting")
            # Cash distributions use booking-date delivery rows; stock transfer fees already use source dates.
            anchor = day if kind == "cash" else origin
            required_dates[(anchor, *key)] += 1
            known[kind] += 1
            if kind == "cash":
                shifts[origin] += amount
                shifts[day] -= amount
    if source_keys != posted_keys:
        raise ValueError(f"ETF monetary multiset mismatch: delivery={sum((source_keys-posted_keys).values())}, cash={sum((posted_keys-source_keys).values())}")
    if required_dates - source_dates:
        raise ValueError("explicit source/booking-date anchors do not match the typed delivery ledger")
    if (set(sources) | set(paid) | set(shifts)) - set(dates):
        raise ValueError("ETF cash contains dates outside the audited funds coverage")
    balances, changes = {}, {}
    balance = 0
    for day in dates:
        change = sources[day] + shifts[day] - paid[day]
        balance += change
        balances[day] = balance
        changes[day] = {"delivery_net": BROKER.money(sources[day]), "explicit_cash_source_shift": BROKER.money(shifts[day]),
                        "posted_net": BROKER.money(paid[day]), "net_receivable_payable": BROKER.money(balance)}
    if balance:
        raise ValueError("actual source/posting ledger does not close at the export cutoff")
    return balances, changes, {"matched_actual_rows": sum(source_keys.values()), "unmatched_rows": 0,
                               "explicit_anchor_rows": dict(known), "actual_cutoff_net_receivable_payable": "0.00",
                               "unexported_etf_entitlements_verified": False,
                               "zero_balance_scope": "exported_actual_cash_only_not_all_outstanding_entitlements",
                               "scope": "actual_exported_cash_not_broker_final_clearing_certificate"}


def return_denominator(open_cents, flows):
    denominator = Decimal(open_cents) / 100
    details = []
    for flow in flows:
        raw = flow["raw_fields"]
        clock = datetime.strptime(raw["交易时间"], "%H:%M:%S")
        seconds = clock.hour * 3600 + clock.minute * 60 + clock.second
        weight = Decimal(max(0, min(19800, 54000 - seconds))) / 19800
        amount = BROKER.cents(flow["actual_amount"])
        denominator += Decimal(amount) / 100 * weight
        details.append({"flow_id": flow["broker_flow_id"], "amount": amount / 100,
                        "effective_time": raw["交易时间"], "weight": str(weight)})
    if denominator <= 0:
        raise ValueError("nonpositive actual capital-weighted denominator")
    return denominator.quantize(Decimal(".000001")), details


def build_days(book, valuations, prefix, claims, bridge, bank, statistics=None):
    result, cumulative, previous = [], Decimal(1), None
    for day in sorted(book):
        row = book[day]
        is_prefix = day <= "20250707"
        raw_total = BROKER.cents(row["total"])
        if is_prefix:
            early = prefix[day]
            close = BROKER.cents(early["close_asset"])
            strategy = BROKER.cents(early["strategy_net_flow"])
            cash = BROKER.cents(early.get("cash") or valuations.get(day, {}).get("cash", "0"))
            repo = BROKER.cents(early.get("repo_principal") or valuations.get(day, {}).get("repo_principal", "0"))
            market = dividend = receivable = 0
        else:
            value = valuations[day]
            if value["missing_prices"]:
                raise ValueError("missing verified security close")
            cash, market, repo, dividend = (BROKER.cents(value[key]) for key in (
                "cash", "meridian_market_value", "repo_principal", "dividend_timing_asset_adjustment_preview"))
            receivable = claims[day]
            other = raw_total - cash - BROKER.cents(row["market_value"]) - repo
            close = raw_total + market - BROKER.cents(row["market_value"]) + dividend + receivable
            strategy = 0
        if close <= 0:
            raise ValueError(f"nonpositive reconstructed account asset on {day}")
        opening_anchor = previous is None
        opening = close if opening_anchor else previous
        external = BROKER.cents(row["deposit"]) - BROKER.cents(row["withdrawal"])
        actual_external = sum(BROKER.cents(flow["actual_amount"]) for flow in bank.get(day, []))
        if external != actual_external:
            raise ValueError("external cash does not match the funds report")
        pnl = 0 if opening_anchor else close - opening - external - strategy
        if is_prefix:
            if not opening_anchor and pnl != BROKER.cents(prefix[day]["daily_pnl"]):
                raise ValueError("approved early daily PnL changed")
            denominator, details = Decimal(opening) / 100, []
            policy = "daily_end_net_flow_convention"
        else:
            denominator, details = return_denominator(opening, bank.get(day, []))
            policy = "actual_bank_cash_modified_dietz"
        rate = (Decimal(pnl) / 100 / denominator).quantize(Decimal(".000000000001"))
        if 1 + rate <= 0:
            raise ValueError("nonpositive unit NAV factor")
        cumulative = (cumulative * (1 + rate)).quantize(Decimal(".000000000001"))
        if day == "20250707" and abs(cumulative - Decimal(prefix[day]["daily_end_net_flow_convention_unit_nav"])) > Decimal(".000000000002"):
            raise ValueError("approved April unit NAV changed")
        flags = ["broker_dual_ledger_account_nav", "historical_actual_cash_restatement", "security_attribution_separate",
                 "as_of_export_not_private_final_clearing_certificate", policy]
        if is_prefix:
            flags += ["prior_bond_strategy_excluded", "cash_posting_income_basis"]
        else:
            flags.append("unexported_etf_entitlements_unverified")
            if other:
                flags.append("broker_reported_other_asset_retained_unclassified")
        if opening_anchor:
            flags.append("observed_opening_anchor_no_daily_return")
        components = {
            "publication_scope": "product_economic_nav_excluding_prior_bonds", "account_level_history": True,
            "account_ledger_verified": True, "source_ref": "docs/FUYING13_CONTINUOUS_NAV_RECOVERY_20261020.md",
            "strategy_capital_net_flow": strategy / 100, "funding_net_flow": (external + strategy) / 100,
            "return_policy": policy, "bank_flow_details": details, "observed_opening_anchor": opening_anchor,
            "full_private_final_clearing_verified": False, "recurring_import": False,
            "broker_statement": {"cash_total": cash / 100, "market_value": market / 100,
                                 "reported_close_asset": raw_total / 100, "reported_daily_pnl": float(row["pnl"]),
                                 "reported_market_value": float(row["market_value"]),
                                 "row_number": row["source_row"], "reconstructed_minus_reported": (close - raw_total) / 100},
            "market_valuation": {"price_source": "meridian_1d_close_and_verified_broker_ledgers",
                                 "open_position_value": None, "close_visible_cash": cash / 100,
                                 "close_position_value": market / 100, "reverse_repo_principal": repo / 100,
                                 "dividend_timing_asset_adjustment": dividend / 100,
                                 "broker_reported_other_asset": 0 if is_prefix else other / 100,
                                 "etf_settlement_actual_net_receivable": receivable / 100},
            "etf_source_posting_bridge": bridge.get(day, {}),
            "ledger_matching": {"matched_actual_rows": 98819, "unmatched_rows": 0, "export_cutoff": "2026-10-19"},
            "trading_observation": (statistics or {}).get(day, {}),
        }
        result.append({"account_id": BROKER.ACCOUNT, "trade_date": datetime.strptime(day, "%Y%m%d").strftime("%Y-%m-%d"),
                       "formula_version": FORMULA, "status": "finalized" if is_prefix else "provisional", "source": SOURCE,
                       "open_economic_nav": opening / 100, "external_net_flow": external / 100,
                       "account_day_pnl": pnl / 100, "close_economic_nav": close / 100,
                       "return_denominator": 0 if opening_anchor else float(denominator),
                       "daily_return": float(rate), "cumulative_nav": float(cumulative),
                       "pnl_components": components, "quality_flags": flags})
        previous = close
    return result


def run(evidence, reconstruction, prefix_path, output):
    report = HISTORY.verify_evidence(evidence)
    if HISTORY.sha_file(reconstruction / "report.json") != RECONSTRUCTION_HASH or HISTORY.sha_file(prefix_path) != PREFIX_HASH:
        raise ValueError("approved quantity/price or April prefix evidence changed")
    prices = json.loads((reconstruction / "report.json").read_text())
    for name, digest in prices["artifact_sha256"].items():
        if Path(name).name != name or HISTORY.sha_file(reconstruction / name) != digest:
            raise ValueError("quantity/valuation artifact hash mismatch")
    valuations = {row["trade_date"]: row for row in map(json.loads, gzip.open(reconstruction / "daily_positions_valuations.jsonl.gz", "rt"))}
    prefix = {row["trade_date"]: row for row in csv.DictReader(prefix_path.open())}
    book = {row["trade_date"]: row for row in report["book_days_not_economic_nav"]}
    settlements = list(BROKER.rows(evidence / report["files"]["settlements"]["snapshot"], "settlements"))
    postings = list(map(json.loads, gzip.open(evidence / "cash_evidence.jsonl.gz", "rt")))
    bank = collections.defaultdict(list)
    for row in postings:
        if row["raw_fields"]["业务科目"] in ("银证转帐取", "银证转帐存"):
            bank[row["raw_fields"]["交易日期"]].append(row)
    claims, bridge, matched = etf_ledger_bridge(settlements, postings, sorted(book))
    statistics = collections.defaultdict(lambda: {"fills_count": 0, "buy_amount": 0, "sell_amount": 0, "turnover": 0, "fee_total": 0})
    for _, row in settlements:
        if row["代码"] in BROKER.PRIOR_STRATEGY_BONDS:
            continue
        day, side = row["成交日期"], row["方向"]
        fees = sum(BROKER.cents(row[key]) for key in ("实收佣金", "印花税", "交易规费", "过户费"))
        statistics[day]["fee_total"] += fees
        if side in ("买入", "卖出"):
            statistics[day]["fills_count"] += 1
            key = "buy_amount" if side == "买入" else "sell_amount"
            amount = BROKER.cents(row["成交金额"])
            statistics[day][key] += amount
            statistics[day]["turnover"] += amount
    for day, stats in statistics.items():
        for key in ("buy_amount", "sell_amount", "turnover", "fee_total"):
            stats[key] /= 100
        stats["fee_source"] = "verified_broker_delivery_not_deducted_twice"
        stats["positions_count"] = len(valuations.get(day, {}).get("effective_positions", {}))
    navs = build_days(book, valuations, prefix, claims, bridge, bank, statistics)
    if len(navs) != 375 or navs[0]["trade_date"] != "2025-04-01" or navs[-1]["trade_date"] != "2026-10-19":
        raise ValueError("reviewed funds coverage changed")
    provenance = {"source_audit_sha256": HISTORY.SOURCE_AUDIT_SHA256, "quantity_valuation_report_sha256": RECONSTRUCTION_HASH,
                  "approved_prefix_sha256": PREFIX_HASH, "funds_sha256": report["files"]["funds"]["sha256"],
                  "cash_flows_sha256": report["files"]["cash_flows"]["sha256"], "settlements_sha256": report["files"]["settlements"]["sha256"]}
    for nav in navs:
        nav["pnl_components"]["provenance"] = provenance
    result = {"account_id": BROKER.ACCOUNT, "formula_version": FORMULA, "source": SOURCE,
              "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(), "nav_rows": len(navs),
              "etf_ledger_bridge": matched, "source_evidence": provenance,
              "daily_pnl_sum": str(sum(Decimal(str(nav["account_day_pnl"])) for nav in navs)),
              "last_point": navs[-1], "database_writes": 0,
              "publication_eligible": False,
              "publication_hold_reasons": ["unexported_etf_entitlements_unverified"],
              "status": "diagnostic_only_unexported_etf_entitlements_unverified"}
    output.mkdir(mode=0o700)
    (output / "INCOMPLETE").write_text("NAV recovery output has not completed\n")
    (output / "nav_plan.json").write_text(HISTORY.json_text({"report": result, "navs": navs}))
    (output / "report.json").write_text(HISTORY.json_text({**result, "plan_sha256": HISTORY.sha_file(output / "nav_plan.json")}))
    (output / "INCOMPLETE").unlink()
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--reconstruction", type=Path, required=True)
    parser.add_argument("--prefix", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    print(HISTORY.json_text(run(args.evidence, args.reconstruction, args.prefix, args.output)))
