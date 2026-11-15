#!/usr/bin/env python3
"""One-time broker refresh evidence; no database or broker commands."""

import argparse
import collections
import hashlib
import importlib.util
import json
import os
import re
from pathlib import Path

SPEC = importlib.util.spec_from_file_location("cash_extension", Path(__file__).with_name("audit-fuying13-cash-extension.py"))
C = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(C)
B = C.BROKER
FEES = ("实收佣金", "印花税", "交易规费", "过户费")
NAMES = {"funds": "资金", "cash_flows": "资金流水", "settlements": "交割单", "orders": "委托"}
BASE = Path("outputs/recovery/broker_refresh_20261110/corrected_768")


def execution_fees(executions, settlements):
    """Associate fee amounts without inventing a delivery-row identity."""
    if len(executions) + len(settlements) > 100000:
        raise ValueError("fee evidence row limit exceeded")
    settlement_fees(settlements)
    left, right = collections.defaultdict(list), collections.defaultdict(list)
    orders, identities, fills = {}, {}, set()
    for line, row in executions:
        if row["客户号"] != B.ACCOUNT:
            raise ValueError("execution account mismatch")
        if row["方向"] not in ("买入", "卖出", "通用回购逆回购"):
            continue
        key = B.execution_key(row, False)
        day, _, security, side, qty, price, gross = key
        if not re.fullmatch(r"\d{6}\.(SH|SZ)", security) or qty <= 0 or price <= 0 or gross <= 0:
            raise ValueError("invalid ordinary execution")
        order_id, fill_id = row["委托编号"].strip(), row["成交编号"].strip()
        identity = (day, security[-2:], order_id)
        fill_identity = (day, security[-2:], fill_id)
        if not order_id or not fill_id or fill_identity in fills:
            raise ValueError("missing/duplicate ordinary execution identity")
        fills.add(fill_identity)
        if identity in identities and identities[identity] != (security, side):
            raise ValueError("order identity reused across security/side")
        identities[identity] = (security, side)
        order = orders.setdefault(identity, {
            "account_id": B.ACCOUNT, "trade_date": day, "security_id": security,
            "side": side, "broker_order_id": order_id, "quantity": 0,
            "gross_cents": 0, "fee_components_cents": dict.fromkeys(FEES, 0),
            "execution_rows": [], "match_groups": [], "unresolved_groups": [],
        })
        order["quantity"] += qty
        order["gross_cents"] += gross
        order["execution_rows"].append({"row": line, "fill_id": fill_id})
        left[key].append((line, identity))
    for line, row in settlements:
        if row["方向"] in ("买入", "卖出", "融券"):
            right[B.execution_key(row, True)].append((line, tuple(B.cents(row[k]) for k in FEES)))
    if not left or set(left) != set(right) or any(len(left[k]) != len(right[k]) for k in left):
        raise ValueError("execution/delivery multiset coverage mismatch")
    groups = []
    for key in sorted(left):
        source, delivery = left[key], right[key]
        vector_set = {v for _, v in delivery}
        order_set = {identity for _, identity in source}
        basis = ("unique_row" if len(source) == 1 else "equal_fee_multiset"
                 if len(vector_set) == 1 else "single_order_multiset"
                 if len(order_set) == 1 else "ambiguous_fee_allocation")
        key_values = list(key)
        key_values[5] = format(key[5].normalize(), "f")
        group_id = hashlib.sha256(json.dumps(key_values, ensure_ascii=False, separators=(",", ":")).encode()).hexdigest()
        groups.append({"group_id": group_id, "execution_key": key_values, "basis": basis,
                       "execution_rows": sorted(n for n, _ in source),
                       "delivery_rows": sorted(n for n, _ in delivery),
                       "fee_components_cents": {f: sum(v[i] for _, v in delivery) for i, f in enumerate(FEES)},
                       "row_identity_unique": len(source) == 1})
        counts = collections.Counter(identity for _, identity in source)
        for identity, count in counts.items():
            order = orders[identity]
            order["match_groups"].append(group_id)
            if basis == "ambiguous_fee_allocation":
                order["unresolved_groups"].append(group_id)
                continue
            vector = (tuple(sum(v[i] for _, v in delivery) for i in range(len(FEES)))
                      if basis == "single_order_multiset" else tuple(count * v for v in delivery[0][1]))
            for f, amount in zip(FEES, vector):
                order["fee_components_cents"][f] += amount
    result = []
    for identity in sorted(orders):
        order = orders[identity]
        complete = not order["unresolved_groups"]
        order["fee_amount_resolved"] = complete
        order["fee_cents"] = sum(order["fee_components_cents"].values()) if complete else None
        # A partial fee subtotal must not be mistaken for the full order fee.
        order["resolved_fee_subtotal_cents"] = sum(order["fee_components_cents"].values())
        order["order_terminal_verified"] = False
        order["execution_rows"].sort(key=lambda r: r["row"])
        result.append(order)
    return {"schema": "relay.broker_execution_fee_association.v1", "account_id": B.ACCOUNT,
            "orders": result, "match_groups": groups,
            "summary": {"execution_rows": sum(len(v) for v in left.values()), "orders": len(result),
                        "resolved_orders": sum(o["fee_amount_resolved"] for o in result),
                        "group_basis_counts": dict(collections.Counter(g["basis"] for g in groups)),
                        "statement_fee_cents": sum(sum(g["fee_components_cents"].values()) for g in groups)},
            "fee_scope": "ordinary_execution_rows_only_excludes_etf_redemption",
            "final_settlement_verified": False, "economic_nav_publishable": False,
            "database_writes": 0, "broker_commands": 0}


def compare_execution_snapshot(report, snapshot):
    spec = importlib.util.spec_from_file_location("execution_fee_audit", Path(__file__).with_name("audit-fuying13-execution-fees.py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    module.execution_buckets(snapshot)  # Reuse the frozen snapshot's full scope/identity checks.
    index = collections.defaultdict(list)
    for order in report["orders"]:
        if order["side"] != "融券":
            index[(order["trade_date"], order["security_id"], order["side"])].append(order)
    comparisons, used = [], set()
    prefix = "external-huaxin-" + B.ACCOUNT + "01-"
    for oc in sorted(snapshot["orders"], key=lambda o: (o["trade_date"], o["gateway_order_id"])):
        gateway = oc["gateway_order_id"]
        gross_micros = B.number(oc["gross_amount"]) * 1000000
        if gross_micros != gross_micros.to_integral_value():
            raise ValueError("OC gross exceeds supported precision")
        key = (oc["trade_date"].replace("-", ""), oc["symbol"] + "." + oc["exchange"], {"B": "买入", "S": "卖出"}[oc["trade_side"]])
        candidates = [o for o in index[key] if gateway.startswith(prefix)
                      and gateway[len(prefix):].endswith(o["broker_order_id"])
                      and o["quantity"] == oc["quantity"]]
        item = {"trade_date": oc["trade_date"], "gateway_order_id": gateway,
                "security_id": key[1], "side": oc["trade_side"], "quantity": oc["quantity"], "oc_gross_micros": int(gross_micros),
                "status": "unresolved", "candidate_count": len(candidates)}
        if len(candidates) == 1:
            order = candidates[0]
            identity = (key[0], key[1][-2:], order["broker_order_id"])
            if identity in used:
                raise ValueError("broker order mapped to multiple OC orders")
            used.add(identity)
            gross_matches = order["gross_cents"] * 10000 == gross_micros
            item.update({"broker_order_id": order["broker_order_id"], "match_groups": order["match_groups"],
                         "statement_gross_cents": order["gross_cents"],
                         "gross_delta_micros": order["gross_cents"] * 10000 - int(gross_micros),
                         "fee_cents": order["fee_cents"] if gross_matches else None,
                         "status": "gross_mismatch" if not gross_matches else "matched" if order["fee_amount_resolved"] else "fee_ambiguous"})
            if gross_matches and order["fee_amount_resolved"]:
                micro = B.number(oc["fee_records"][0]["total_fee"]) * 1000000
                if micro != micro.to_integral_value():
                    raise ValueError("OC fee exceeds supported precision")
                item["oc_fee_micros"] = int(micro)
                item["fee_delta_micros"] = order["fee_cents"] * 10000 - int(micro)
        comparisons.append(item)
    return {"orders": comparisons, "matched": sum(x["status"] == "matched" for x in comparisons),
            "total": len(comparisons), "status_counts": dict(collections.Counter(x["status"] for x in comparisons)),
            "scope": "observed order executions and fee amounts; not order terminal or private clearing finality"}


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
    p.add_argument("--execution-fees", action="store_true")
    p.add_argument("--oc-snapshot", type=Path)
    p.add_argument("--oc-snapshot-sha256")
    args = p.parse_args()
    if bool(args.oc_snapshot) != bool(args.oc_snapshot_sha256) or (args.oc_snapshot and not args.execution_fees):
        p.error("OC snapshot and SHA256 require --execution-fees and each other")
    if args.execution_fees:
        captured, records, sources = {}, {}, {}
        for kind, name in (("executions", "成交"), ("settlements", "交割单")):
            path = args.reference / f"{B.ACCOUNT}_{name}.csv"
            data = path.read_bytes()
            records[kind] = list(B.rows(path, kind))
            if path.read_bytes() != data:
                raise ValueError("source changed while reading")
            captured[path.name] = data
            sources[kind] = {"path": str(path), "sha256": hashlib.sha256(data).hexdigest()}
        report = execution_fees(records["executions"], records["settlements"])
        report["sources"] = sources
        if args.oc_snapshot:
            data = args.oc_snapshot.read_bytes()
            digest = hashlib.sha256(data).hexdigest()
            if digest != args.oc_snapshot_sha256:
                raise ValueError("OC snapshot SHA256 mismatch")
            report["oc_comparison"] = compare_execution_snapshot(report, json.loads(data))
            sources["oc_snapshot"] = {"path": str(args.oc_snapshot), "sha256": digest}
            captured["oc_execution_snapshot.json"] = data
    else:
        report, captured = audit(args.reference)
    args.output_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    for name, data in captured.items():
        with os.fdopen(os.open(args.output_dir / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as f:
            f.write(data)
    target = args.output_dir / "audit.json"
    data = (json.dumps(report, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()
    with os.fdopen(os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as f:
        f.write(data)
    print(json.dumps({"output": str(target), "sha256": hashlib.sha256(data).hexdigest(),
                      **({"summary": report["summary"], "oc_matched": report.get("oc_comparison", {}).get("matched")} if args.execution_fees else
                         {"cash_overlap": report["cash_overlap"], "cash_bridge": report["cash_bridge"], "new_cash_rows": len(report["new_cash_rows"])}),
                      "database_writes": 0}, ensure_ascii=False))


if __name__ == "__main__":
    main()
