#!/usr/bin/env python3
"""Offline April cash/repo carve-out preview; never publish account NAV or flows."""

from __future__ import annotations

import argparse
import collections
import csv
import gzip
import importlib.util
import json
from datetime import datetime
from decimal import Decimal, localcontext
from pathlib import Path
from zoneinfo import ZoneInfo


SPEC = importlib.util.spec_from_file_location("history_rebuild", Path(__file__).with_name("rebuild-fuying13-history.py"))
HISTORY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HISTORY)
BROKER = HISTORY.BROKER
FIRST = "20250401"
LAST = "20250702"
MODELS = ("daily_end_net_flow_convention", "daily_begin_net_flow_convention")
GOLD_CSV_SHA256 = "bac4481f0cd3ef409ace60d75cba27db4aaeebf2cc1ee13fc5ea3955e4b2638a"


def classify_flows(cash_rows, settlements):
    bond_cash = collections.defaultdict(list)
    bank, operating = [], []
    for row in cash_rows:
        raw = row["raw_fields"]
        day = BROKER.date(raw["交易日期"])
        if not FIRST <= day <= LAST:
            continue
        amount = BROKER.cents(row["actual_amount"])
        code, subject = raw["股票代码"], raw["业务科目"]
        if subject in ("买入成交清算资金", "卖出成交清算资金") and code + ".SH" in BROKER.PRIOR_STRATEGY_BONDS:
            bond_cash[(day, code, subject, amount)].append(row)
        elif subject in ("银证转帐存", "银证转帐取"):
            bank.append(row)
        else:
            if subject not in ("季度利息入本金", "回购融券成交划出资金", "回购融券购回资金划入"):
                raise ValueError("unreviewed cash/repo operating cash flow")
            operating.append(row)
    transfers = []
    for source_row, row in settlements:
        day, side, code = row["成交日期"], row["方向"], row["代码"]
        if not FIRST <= day <= LAST:
            continue
        if side in ("融券", "融券购回"):
            continue
        if side not in ("买入", "卖出") or code not in BROKER.PRIOR_STRATEGY_BONDS:
            raise ValueError("prefix contains unreviewed non-bond securities or business")
        amount = BROKER.cents(row["实付金额"])
        subject = "买入成交清算资金" if side == "买入" else "卖出成交清算资金"
        if not amount or (side == "买入") != (amount < 0):
            raise ValueError("invalid bond settlement cash direction")
        candidates = bond_cash.pop((day, code.split(".")[0], subject, amount), [])
        if len(candidates) != 1:
            raise ValueError("bond delivery lacks a unique actual cash-flow match")
        cash = candidates[0]
        transfers.append({
            "scope": "hypothetical_cash_repo_subportfolio_only", "trade_date": day,
            "security_id": code, "amount": BROKER.money(amount),
            "kind": "strategy_capital_transfer_not_bank_flow", "source_delivery_row": source_row,
            "broker_flow_id": cash["broker_flow_id"], "source_cash_row": cash["source_row"],
            "broker_trade_time_raw": row["成交时间"], "cash_posted_at_raw": cash["cash_posted_at"],
            "effective_at": None, "economic_transfer_time_precision": "date_only",
        })
    if bond_cash:
        raise ValueError("unmatched bond cash postings")
    return transfers, bank, operating


def preview_days(funds, repo_balances, transfers, bank, operating, continuation):
    rows = sorted((row for row in funds if FIRST <= row["trade_date"] <= LAST), key=lambda row: row["trade_date"])
    if not rows or rows[0]["trade_date"] != FIRST or rows[-1]["trade_date"] != LAST:
        raise ValueError("incomplete April-to-handover funds coverage")
    amounts = {}
    for name, items in (("strategy", transfers), ("bank", bank), ("operating", operating)):
        daily = collections.Counter()
        for item in items:
            if name == "strategy":
                day, amount = item["trade_date"], item["amount"]
            else:
                day, amount = item["raw_fields"]["交易日期"], item["actual_amount"]
            daily[day] += BROKER.cents(amount)
        amounts[name] = daily
    result, previous = [], None
    units = {model: Decimal(1) for model in MODELS}
    with localcontext() as context:
        context.prec = 40
        for row in rows:
            day = row["trade_date"]
            cash = BROKER.cents(row["cash"])
            principal = repo_balances[day]
            asset = cash + principal
            if BROKER.cents(row["market_value"]) or BROKER.cents(row["total"]) != asset:
                raise ValueError("cash/repo carve-out has unreviewed other assets")
            bank_net, strategy_net = amounts["bank"][day], amounts["strategy"][day]
            if bank_net != BROKER.cents(row["deposit"]) - BROKER.cents(row["withdrawal"]):
                raise ValueError("bank postings do not match reported deposits/withdrawals")
            pnl = None
            if previous is None:
                if asset <= 0 or bank_net or strategy_net or amounts["operating"][day] or BROKER.cents(row["pnl"]):
                    raise ValueError("first row is not a clean observed opening anchor")
            else:
                if cash - previous["cash"] != bank_net + strategy_net + amounts["operating"][day]:
                    raise ValueError("actual daily cash bridge mismatch")
                pnl = asset - previous["asset"] - bank_net - strategy_net
                if pnl != amounts["operating"][day] + principal - previous["principal"]:
                    raise ValueError("cash/repo operating PnL bridge mismatch")
                if BROKER.cents(row["pnl"]) - strategy_net != pnl:
                    raise ValueError("reported PnL minus excluded cash legs does not reconcile")
            item = {
                "trade_date": day, "phase": "pre_handover_cash_repo_carveout", "source_funds_row": row["source_row"],
                "cash": row["cash"], "repo_principal": BROKER.money(principal), "close_asset": BROKER.money(asset),
                "bank_net_flow": BROKER.money(bank_net), "strategy_net_flow": BROKER.money(strategy_net),
                "daily_pnl": BROKER.money(pnl) if pnl is not None else None,
                "is_observed_opening_anchor": previous is None, "economic_nav_publishable": False,
            }
            for model in MODELS:
                denominator = previous["asset"] if previous else None
                if denominator is not None and model == "daily_begin_net_flow_convention":
                    denominator += bank_net + strategy_net
                if denominator is not None:
                    if denominator <= 0:
                        raise ValueError("nonpositive daily-flow return denominator")
                    units[model] *= 1 + Decimal(pnl) / Decimal(denominator)
                item[model + "_denominator"] = BROKER.money(denominator) if denominator is not None else None
                item[model + "_unit_nav"] = format(units[model], ".24f")
            result.append(item)
            previous = {"cash": cash, "principal": principal, "asset": asset}
        if previous["asset"] != HISTORY.OPENING_CENTS or not continuation or continuation[0]["trade_date"] != HISTORY.START:
            raise ValueError("carve-out does not join the approved July handover")
        for row in continuation:
            opening = BROKER.cents(row["open_asset_excluding_fund_occupancy"])
            closing = BROKER.cents(row["close_asset_excluding_fund_occupancy"])
            pnl = BROKER.cents(row["daily_pnl"])
            if opening != previous["asset"] or closing - opening != pnl:
                raise ValueError("continuation gold asset/PnL bridge mismatch")
            item = {"trade_date": row["trade_date"], "phase": "approved_handover_cash_repo_gold",
                    "close_asset": BROKER.money(closing), "bank_net_flow": "0.00", "strategy_net_flow": "0.00",
                    "daily_pnl": BROKER.money(pnl), "is_observed_opening_anchor": False, "economic_nav_publishable": False}
            for model in MODELS:
                units[model] *= 1 + Decimal(pnl) / Decimal(opening)
                item[model + "_denominator"] = BROKER.money(opening)
                item[model + "_unit_nav"] = format(units[model], ".24f")
            result.append(item)
            previous["asset"] = closing
    return result


def run(evidence, reconstruction, output):
    source = HISTORY.verify_evidence(evidence)
    if (reconstruction / "INCOMPLETE").exists():
        raise ValueError("incomplete July reconstruction")
    july = json.loads((reconstruction / "report.json").read_text())
    if july["account_id"] != BROKER.ACCOUNT or july["source_report_sha256"] != HISTORY.SOURCE_AUDIT_SHA256:
        raise ValueError("July reconstruction source/account mismatch")
    for name, digest in july["artifact_sha256"].items():
        if Path(name).name != name or HISTORY.sha_file(reconstruction / name) != digest:
            raise ValueError("July reconstruction artifact mismatch")
    gold_csv = reconstruction / "cash_repo_gold.csv"
    if HISTORY.sha_file(gold_csv) != GOLD_CSV_SHA256:
        raise ValueError("reviewed July gold evidence changed")
    with gold_csv.open(encoding="utf-8", newline="") as source_file:
        continuation = list(csv.DictReader(source_file))
    if continuation != july["cash_repo_verified_prefix"]:
        raise ValueError("July report disagrees with its confirmed gold CSV")
    output.mkdir(mode=0o700, exist_ok=False)
    marker = output / "INCOMPLETE"
    marker.write_text("Conditional preview incomplete; never publish as account NAV.\n", encoding="ascii")
    settlements = list(BROKER.rows(evidence / source["files"]["settlements"]["snapshot"], "settlements"))
    with gzip.open(evidence / "cash_evidence.jsonl.gz", "rt", encoding="utf-8") as source_file:
        transfers, bank, operating = classify_flows(map(json.loads, source_file), settlements)
    prefix_dates = sorted(row["trade_date"] for row in source["book_days_not_economic_nav"] if FIRST <= row["trade_date"] <= LAST)
    repos, _ = HISTORY.repo_history(settlements, prefix_dates)
    series = preview_days(source["book_days_not_economic_nav"], repos, transfers, bank, operating, continuation)
    with (output / "preview.csv").open("w", encoding="utf-8", newline="") as target:
        columns = list(dict.fromkeys(key for row in series for key in row))
        writer = csv.DictWriter(target, fieldnames=columns)
        writer.writeheader()
        writer.writerows(series)
    (output / "strategy_transfers.json").write_text(HISTORY.json_text(transfers))
    (output / "bank_postings.json").write_text(HISTORY.json_text(bank))
    before = [row for row in series if row["trade_date"] <= LAST]
    total = lambda items: sum(BROKER.cents(row["daily_pnl"]) for row in items if row["daily_pnl"] is not None)
    report = {
        "account_id": BROKER.ACCOUNT, "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
        "scope": "conditional_cash_repo_subportfolio_excluding_bonds_not_account_total_return",
        "source_report_sha256": HISTORY.SOURCE_AUDIT_SHA256, "july_report_sha256": HISTORY.sha_file(reconstruction / "report.json"),
        "prefix_days": len(before), "prefix_return_intervals": len(before) - 1, "joined_preview_days": len(series),
        "opening_observed_asset": series[0]["close_asset"], "bond_cash_legs": len(transfers), "bank_cash_legs": len(bank),
        "prefix_pnl": BROKER.money(total(before)), "through_july7_pnl": BROKER.money(total(series)),
        "last_prefix_point": before[-1], "last_joined_point": series[-1], "handover_bridge_verified": True,
        "assumptions": ["All cash not invested in the excluded bonds, and reverse repo, is assigned to the preview subportfolio.",
                        "Opening cash and the entire June cash-posted interest are assigned to the preview subportfolio, pending ownership and accrual-cutoff confirmation."],
        "income_recognition_policy": "cash_posting_basis_preview_not_full_accrual",
        "opening_accrued_interest_verified": False,
        "interest_postings": [row for row in operating if row["raw_fields"]["业务科目"] == "季度利息入本金"],
        "ownership_confirmed": False, "daily_flow_policy_confirmed": False,
        "return_models_are_conventions_not_intraday_error_bounds": True, "no_transfer_timestamps_invented": True,
        "economic_nav_publishable": False, "database_writes": 0, "production_inception_changed": False,
        "first_row_is_observed_anchor_not_proof_of_zero_inception": True,
        "remaining_work": ["Confirm opening/unallocated cash ownership, interest ownership/accrual cutoff and the capital-flow convention.",
                           "Complete the separate ETF settlement work from July 8 before joining the full historical NAV."],
        "artifact_sha256": {name: HISTORY.sha_file(output / name) for name in ("preview.csv", "strategy_transfers.json", "bank_postings.json")},
    }
    (output / "report.json").write_text(HISTORY.json_text(report))
    marker.unlink()
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--reconstruction", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    print(HISTORY.json_text(run(args.evidence, args.reconstruction, args.output)))
