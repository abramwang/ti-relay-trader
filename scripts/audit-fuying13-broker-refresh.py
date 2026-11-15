#!/usr/bin/env python3
"""One-time broker refresh evidence; no database or broker commands."""

import argparse
import collections
import hashlib
import importlib.util
import json
import os
from pathlib import Path

SPEC = importlib.util.spec_from_file_location("cash_extension", Path(__file__).with_name("audit-fuying13-cash-extension.py"))
C = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(C)
B = C.BROKER
FEES = ("实收佣金", "印花税", "交易规费", "过户费")
NAMES = {"funds": "资金", "cash_flows": "资金流水", "settlements": "交割单", "orders": "委托"}
BASE = Path("outputs/recovery/broker_refresh_20261110/corrected_768")


def settlement_fees(rows):
    result = {}
    for line, raw in rows:
        day = B.date(raw["成交日期"])
        if raw["客户号"] != B.ACCOUNT:
            raise ValueError("settlement account mismatch")
        fees = {key: B.cents(raw[key]) for key in FEES}
        if min(fees.values()) < 0:
            raise ValueError("negative fees require a separate reversal review")
        fee = sum(fees.values())
        if raw["方向"] in ("买入", "卖出", "融券"):
            gross, paid = B.cents(raw["成交金额"]), B.cents(raw["实付金额"])
            if (gross if raw["方向"] == "卖出" else -gross) - fee != paid:
                raise ValueError("ordinary gross/fee/cash identity failed")
        item = result.setdefault(day, {"fee_cents": 0, "fee_components_cents": dict.fromkeys(FEES, 0), "source_rows": [], "rows": 0, "repo_principal_cents": 0})
        if raw["方向"] == "融券":
            B.repo_quantity(raw["代码"], B.quantity(raw["成交数量"]), gross)
            item["repo_principal_cents"] += gross
        item["rows"] += 1
        item["fee_cents"] += fee
        if fee:
            item["source_rows"].append(line)
        for key, amount in fees.items():
            item["fee_components_cents"][key] += amount
    return result


def audit(reference):
    source, records, captured = {}, {}, {}
    for kind, name in NAMES.items():
        path = reference / f"{B.ACCOUNT}_{name}.csv"
        data = path.read_bytes()
        rows = list(B.rows(path, kind))
        if hashlib.sha256(path.read_bytes()).digest() != hashlib.sha256(data).digest():
            raise ValueError("source changed while reading")
        captured[path.name] = data
        field = {"funds": "交易日", "cash_flows": "交易日期", "settlements": "成交日期", "orders": "日期"}[kind]
        dates = [B.date(r[field]) for _, r in rows]
        source[kind] = {"path": str(path), "sha256": hashlib.sha256(data).hexdigest(), "rows": len(rows), "date_from": min(dates), "date_to": max(dates)}
        records[kind] = rows
    cash = C.read_cash(reference / f"{B.ACCOUNT}_资金流水.csv")
    old_cash = C.read_cash(BASE / f"{B.ACCOUNT}_资金流水.csv")
    additions, overlap = C.compare_exports(old_cash, cash)
    funds, bridges = C.fund_bridges(records["funds"], cash)
    old_funds = {r["交易日"]: r for _, r in B.rows(BASE / f"{B.ACCOUNT}_资金.csv", "funds")}
    for day, raw in funds.items():
        if day in old_funds and raw != old_funds[day]:
            raise ValueError("historical funds changed; requires explicit revision review")
    fees = settlement_fees(records["settlements"])
    latest = {day: {"funds": raw, "fees": fees.get(day)} for day, raw in sorted(funds.items()) if day >= "20261009"}
    return {"schema": "relay.fuying13_broker_refresh.v1", "account_id": B.ACCOUNT,
            "sources": source, "cash_overlap": overlap, "cash_bridge": bridges,
            "new_cash_rows": additions, "days": latest,
            "orders_identity_available": False, "settlement_order_identity_available": False,
            "fee_scope": "statement_rows_by_displayed_date_not_order_finality",
            "asset_inclusion_verified": False, "final_settlement_verified": False,
            "database_writes": 0, "broker_commands": 0}, captured


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--reference", type=Path, default=Path("reference"))
    p.add_argument("--output-dir", type=Path, required=True)
    args = p.parse_args()
    report, captured = audit(args.reference)
    args.output_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    for name, data in captured.items():
        with os.fdopen(os.open(args.output_dir / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as f:
            f.write(data)
    target = args.output_dir / "audit.json"
    data = (json.dumps(report, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()
    with os.fdopen(os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as f:
        f.write(data)
    print(json.dumps({"output": str(target), "sha256": hashlib.sha256(data).hexdigest(), "cash_overlap": report["cash_overlap"], "cash_bridge": report["cash_bridge"], "new_cash_rows": len(report["new_cash_rows"]), "database_writes": 0}, ensure_ascii=False))


if __name__ == "__main__":
    main()
