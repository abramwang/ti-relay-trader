#!/usr/bin/env python3
"""One-time 2026-10-12 broker cash inclusion audit; no database writes."""

import argparse
import hashlib
import importlib.util
import json
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo


SPEC = importlib.util.spec_from_file_location(
    "balance_chain", Path(__file__).with_name("audit-fuying13-balance-chain.py"))
CHAIN = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHAIN)
B = CHAIN.BROKER
DAY, PREVIOUS = "20261012", "20261009"


def one(items, label):
    if len(items) != 1:
        raise ValueError(f"expected one {label}, got {len(items)}")
    return items[0]


def audit(reference):
    paths = {kind: reference / f"{B.ACCOUNT}_{name}.csv" for kind, name in B.FILES.items()}
    hashes = {kind: hashlib.sha256(path.read_bytes()).hexdigest() for kind, path in paths.items()}
    fund_rows = list(B.rows(paths["funds"], "funds"))
    funds = {d: one([r for _, r in fund_rows if r["交易日"] == d], f"funds {d}")
             for d in (PREVIOUS, DAY)}
    cash = CHAIN.EXT.read_cash(paths["cash_flows"])
    CHAIN.EXT.unique_rows(cash)
    deliveries = list(B.rows(paths["settlements"], "settlements"))
    selected = [(n, r) for n, r in deliveries if r["成交日期"] == DAY]
    opening, closing = (B.cents(funds[d]["客户资金"]) for d in (PREVIOUS, DAY))
    chain = CHAIN.build_chain(selected, opening, closing)
    assignments, segments = CHAIN.assign_posting_days(chain, funds, PREVIOUS, cash)
    executions = list(B.rows(paths["executions"], "executions"))
    origins = CHAIN.EXT.ORIGINS
    index = origins.order_index(executions)
    receipts = []
    for row in cash:
        raw = row["raw_fields"]
        if raw["交易日期"] != DAY or raw["业务科目"] != "ETF赎回替代现金划入":
            continue
        resolution = origins.resolve_origin(row, index)
        if (resolution["source_date_resolution"] != "unique_historical_etf_order_reference"
                or resolution["resolved_source_trade_date"] != DAY):
            raise ValueError("receipt has no unambiguous same-day redemption origin")
        amount = B.cents(row["actual_amount"])
        if amount <= 0:
            raise ValueError("receipt is not positive")
        matches = [(n, r) for n, r in executions
                   if r["委托编号"] == resolution["order_reference"]
                   and r["日期"] == DAY and r["方向"] == "ETF赎回"
                   and r["代码"].split(".")[0] == raw["股票代码"]
                   and B.cents(r["成交金额"]) == amount]
        number, execution = one(matches, "receipt execution cash leg")
        receipts.append({"account_id": B.ACCOUNT, "trade_date": "2026-10-12",
                         "source_trade_date": "2026-10-12", "broker_flow_id": row["broker_flow_id"],
                         "broker_order_id": resolution["order_reference"],
                         "security_id": execution["代码"], "amount": B.money(amount),
                         "cash_source_row": row["source_row"], "execution_source_row": number,
                         "cash_posted_at": row["cash_posted_at"], "raw_cash": raw,
                         "raw_execution": execution})
    if not receipts:
        raise ValueError("no verified receipts")

    # Repo income is proved by linked executions and delivery amounts, not a NAV residual.
    repo_n, repo = one([(n, r) for n, r in selected
                        if r["代码"] == "204001.SH" and r["方向"] == "融券"], "new repo")
    return_n, returned = one([(n, r) for n, r in selected
                              if r["代码"] == "204001.SH" and r["方向"] == "融券购回"], "repo return")
    prior_n, prior = one([(n, r) for n, r in deliveries if r["成交日期"] == PREVIOUS
                          and r["代码"] == "204001.SH" and r["方向"] == "融券"], "prior repo")
    prior_exec_n, prior_exec = one([(n, r) for n, r in executions
                                   if r["日期"] == PREVIOUS and r["代码"] == "204001.SH"
                                   and B.cents(r["成交金额"]) == B.cents(prior["成交金额"])], "prior repo execution")
    return_flow = one([r for r in cash if r["raw_fields"]["交易日期"] == DAY
                       and r["raw_fields"]["业务科目"] == "回购融券购回资金划入"
                       and B.cents(r["actual_amount"]) == B.cents(returned["实付金额"])], "repo return cash")
    summary = return_flow["raw_fields"]["操作摘要"]
    if (origins.etf_order_reference(summary) != prior_exec["委托编号"]
            or f"成交日期{PREVIOUS}" not in summary
            or returned["成交数量"] != prior["成交数量"]):
        raise ValueError("repo return is not linked to prior principal")
    principal, fee = B.cents(repo["成交金额"]), B.cents(repo["实收佣金"])
    if B.cents(repo["实付金额"]) != -principal - fee:
        raise ValueError("repo debit does not equal principal plus actual fee")
    interest = B.cents(returned["实付金额"]) - B.cents(prior["成交金额"])
    if interest < 0:
        raise ValueError("negative repo income requires separate review")
    total = B.cents(funds[DAY]["总资产"])
    market = B.cents(funds[DAY]["市值"])
    if total != closing + market + principal:
        raise ValueError("broker asset scope has an unexplained remainder")
    # The parser validates the daily funds identity separately from the cash chain.
    CHAIN.EXT.fund_bridges([(n, r) for n, r in fund_rows if r["交易日"] in funds], cash)
    if hashes != {kind: hashlib.sha256(path.read_bytes()).hexdigest() for kind, path in paths.items()}:
        raise ValueError("source export changed during audit")
    return {"schema": "broker_same_day_etf_receipts_audit.v1", "account_id": B.ACCOUNT,
            "trade_date": "2026-10-12", "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
            "source_sha256": hashes, "funds": funds, "receipts": receipts,
            "included_cash_total": B.money(sum(B.cents(r["amount"]) for r in receipts)),
            "opening_cash": B.money(opening), "closing_cash": B.money(closing),
            "cash_chain_verified": True, "cash_multiset_verified": True,
            "segments": segments, "balance_chain": assignments,
            "repo_principal": B.money(principal), "repo_interest": B.money(interest),
            "repo_fee": B.money(fee), "repo_evidence": {
                "new": {"source_row": repo_n, "raw": repo},
                "returned": {"source_row": return_n, "raw": returned},
                "prior": {"source_row": prior_n, "raw": prior},
                "prior_execution": {"source_row": prior_exec_n, "raw": prior_exec},
                "return_cash": return_flow},
            "settlement_complete": False, "economic_nav_publishable": False,
            "scope": "broker_end_of_day_cash_not_OC_intraday_atomic_snapshot"}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", type=Path, default=Path("reference"))
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    result = audit(args.reference)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x", encoding="utf-8") as target:
        json.dump(result, target, ensure_ascii=False, indent=2)
        target.write("\n")
    print(json.dumps({k: result[k] for k in ("account_id", "trade_date", "included_cash_total",
                                             "repo_interest", "repo_fee", "economic_nav_publishable")}, ensure_ascii=False))
