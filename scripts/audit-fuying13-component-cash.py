#!/usr/bin/env python3
"""One-time offline component/cash evidence review; never publish or repair DB."""

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

SPEC = importlib.util.spec_from_file_location(
    "special_cash", Path(__file__).with_name("audit-fuying13-special-records.py"))
S = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(S)
B, EXT, TZ = S.B, S.EXT, S.TZ
DATES = ("2026-10-14", "2026-10-15", "2026-10-16", "2026-10-23")
METHODS = {"store.ListOrders", "store.ListFills", "store.ListComponentTransfers", "store.ListPositionSnapshots"}


def reject_constant(value):
    raise ValueError("nonfinite JSON number: " + value)


def decode(raw):
    return json.loads(raw, parse_float=Decimal, parse_constant=reject_constant)


def encode_decimal(value):
    if isinstance(value, Decimal):
        return str(value)
    raise TypeError("unsupported evidence value")


def checked_json(path, expected):
    raw = path.read_bytes()
    if hashlib.sha256(raw).hexdigest() != expected:
        raise ValueError("evidence SHA mismatch: " + str(path))
    return decode(raw)


def compact(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def load_capture(directory, manifest_sha):
    m = checked_json(directory / "manifest.json", manifest_sha)
    if (m["schema"] != "relay.research_engine_inputs.v1" or m["account_id"] != B.ACCOUNT
            or m["complete"] is not True or m["run_error"] or m["database_writes"] != 0
            or m["broker_commands"] != 0 or m["economic_nav_publishable"] is not False):
        raise ValueError("incomplete/unsafe capture")
    pages, counts, digest, size = {}, Counter(), hashlib.sha256(), 0
    with (directory / "inputs.jsonl").open("rb") as stream:
        for n, line in enumerate(stream, 1):
            size += len(line)
            if size > 256 << 20 or len(line) > 32 << 20 or n > 10000:
                raise ValueError("capture budget exceeded")
            digest.update(line)
            r = decode(line)
            if r["sequence"] != n:
                raise ValueError("capture sequence mismatch")
            counts[r["method"]] += 1
            req = r["request"]
            if r["method"] not in METHODS or req.get("trade_date") not in DATES:
                continue
            expected = {"account_id", "trade_date", "history", "limit", "cursor"}
            if r["method"] == "store.ListPositionSnapshots":
                expected.add("snapshot_type")
            if (set(req) != expected or req["account_id"] != B.ACCOUNT or req["history"] is not True
                    or req["limit"] != 500 or r["error"] or not re.fullmatch(r"0|[1-9][0-9]*", req["cursor"])):
                raise ValueError("filtered/failed capture request")
            key = (r["method"], req["trade_date"], req.get("snapshot_type", ""), int(req["cursor"]))
            rows = r["response"] if r["response"] is not None else []
            if not isinstance(rows, list) or len(rows) > 500:
                raise ValueError("invalid capture page")
            if key in pages and pages[key]["rows"] != rows:
                raise ValueError("repeated page changed")
            if key not in pages:
                pages[key] = {"rows": rows, "sequences": []}
            pages[key]["sequences"].append(n)
    if size != m["bytes"] or sum(counts.values()) != m["calls"] or dict(counts) != m["methods"] or digest.hexdigest() != m["sha256"]:
        raise ValueError("capture content integrity failure")
    return m, pages


def page_rows(pages, method, day, snapshot=""):
    result, sequences, offset = [], [], 0
    while True:
        page = pages.get((method, day, snapshot, offset))
        if page is None:
            raise ValueError("missing final/full capture page")
        rows = page["rows"]
        for r in rows:
            if r["account_id"] != B.ACCOUNT or r["trade_date"] != day or (snapshot and r["snapshot_type"] != snapshot):
                raise ValueError("capture row scope mismatch")
            security(r)
            if method in ("store.ListFills", "store.ListComponentTransfers") and (type(r["qty"]) is not int or r["qty"] <= 0):
                raise ValueError("invalid execution/transfer quantity")
        result.extend(rows)
        sequences.extend(page["sequences"])
        if len(rows) < 500:
            break
        offset += 500
    if any(k[:3] == (method, day, snapshot) and k[3] > offset for k in pages):
        raise ValueError("page after terminal page")
    seen = set()
    for r in result:
        key = security(r) if snapshot else (r["gateway_order_id"], r.get("fill_id", ""))
        if key in seen:
            raise ValueError("duplicate row identity")
        seen.add(key)
    return result, sequences


def security(row):
    if not re.fullmatch(r"[0-9]{6}", row["symbol"]) or row["exchange"] not in ("SH", "SZ"):
        raise ValueError("invalid security")
    return row["symbol"] + "." + row["exchange"]


def quantities(rows):
    result = {}
    for r in rows:
        key, q = security(r), B.quantity(str(r["quantity"]))
        if key in result:
            raise ValueError("duplicate position security")
        result[key] = q
    return {k: q for k, q in result.items() if q}


def transfer_projection_sha(rows):
    # Reproduce the existing Go core-field digest, not a new wire convention.
    encoded = []
    for r in rows:
        fields = {k: r[k] for k in ("symbol", "exchange", "trade_side", "business_type", "order_id", "order_stream_id",
                                   "qty", "component_symbol", "component_exchange", "component_qty", "transfer_type", "cash_substitution")}
        fields["matched_at"] = r["matched_at"]
        for k in ("price", "component_value"):
            value = r[k]
            if value is None:
                fields[k] = None
            else:
                d = Decimal(str(value))
                if not d.is_finite() or d * 1000000 != (d * 1000000).to_integral_value():
                    raise ValueError("unsupported projection precision")
                fields[k] = format(d, ".6f")
        encoded.append(compact({"key": compact([B.ACCOUNT, r["trade_date"], r["gateway_order_id"], r["fill_id"]]),
                                "fields": dict(sorted(fields.items()))}))
    return hashlib.sha256(compact(sorted(encoded)).encode()).hexdigest()


def review_cash_leg(t, parent, cash, opening, closing, fills):
    day, stream = t["trade_date"], t["order_stream_id"]
    match = re.fullmatch(r"12002(A[0-9]{9})", stream)
    if (any(type(t[k]) is not int for k in ("qty", "component_qty", "order_id", "match_timestamp"))
            or any(type(parent[k]) is not int for k in ("cum_filled_qty", "order_qty", "order_id"))
            or not match or t["gateway_order_id"] != "external-huaxin-" + B.ACCOUNT + "01-" + stream
            or t["account_id"] != B.ACCOUNT or t["symbol"] != "159900" or t["exchange"] != "SZ"
            or t["component_symbol"] != t["symbol"] or t["component_exchange"] != t["exchange"]
            or t["component_qty"] != 1 or t["qty"] != 1 or t["cash_substitution"] is not False
            or t["component_value"] is not None or t["trade_side"] != "R" or t["business_type"] != "E"
            or t["is_transfer"] is not True or t["record_type"] != "etf_component_transfer"
            or t["transfer_type"] != "etf_redemption_component_transfer" or not t["fill_id"]
            or any(parent[k] != t[k] for k in ("account_id", "trade_date", "gateway_order_id", "order_id", "order_stream_id", "trade_side", "business_type", "exchange"))
            or parent["symbol"] != "159869" or t["basket_id"] != "etfarb#" + parent["symbol"]
            or parent["status"] != "filled" or parent["is_terminal"] is not True
            or parent["cum_filled_qty"] <= 0 or parent["cum_filled_qty"] != parent["order_qty"]):
        raise ValueError("cash candidate/parent identity unproven")
    key, amount = security(t), B.cents(str(t["price"]))
    at = datetime.fromisoformat(t["matched_at"])
    if amount <= 0 or at.tzinfo is None or at.astimezone(TZ).date().isoformat() != day or int(at.timestamp()*1000) != t["match_timestamp"]:
        raise ValueError("candidate time/amount unproven")
    if opening.get(key, 0) or closing.get(key, 0) or any(security(f) == key for f in fills):
        raise ValueError("candidate also appears as physical stock/fill")
    ref = match[1]
    related = [r for r in cash if EXT.ORIGINS.etf_order_reference(r["raw_fields"]["操作摘要"]) == ref
               and r["raw_fields"]["业务科目"] == "ETF赎回替代现金划入"]
    expected_summary = f"深圳ETF赎回{parent['symbol']}({t['symbol']})替代现金:{ref}"
    if len(related) != 1:
        raise ValueError("cash identity missing/reused/ambiguous")
    c, raw = related[0], related[0]["raw_fields"]
    posted = datetime.fromisoformat(c["cash_posted_at"])
    if (c["account_id"] != B.ACCOUNT or raw["交易日期"] != day.replace("-", "")
            or raw["股票代码"] != parent["symbol"] or raw["操作摘要"] != expected_summary
            or B.cents(c["actual_amount"]) != amount or posted.tzinfo is None or posted <= at
            or posted.astimezone(TZ).date().isoformat() != day
            or c["source_trade_date"] not in (None, day.replace("-", ""))):
        raise ValueError("cash identity/date/amount differs")
    return {"trade_date": day, "gateway_order_id": t["gateway_order_id"], "fill_id": t["fill_id"],
            "broker_order_reference": ref, "original_transfer": t, "parent_order": parent, "cash_row": c,
            "amount": B.money(amount), "state": "specific_historical_cash_leg_proven",
            "physical_quantity": 0, "additional_broker_eod_receivable": "0.00",
            "intraday_cash_inclusion_verified": False, "final_settlement_verified": False}


def audit(capture, manifest_sha, book_path, book_sha, opening_path, opening_sha, reference):
    manifest, pages = load_capture(capture, manifest_sha)
    book, op = checked_json(book_path, book_sha), checked_json(opening_path, opening_sha)
    for r in (book, op):
        if (r["account_id"] != B.ACCOUNT or r["environment"] != "production"
                or r["version_id"] != manifest["baseline_version_id"] or r["payload_sha256"] != manifest["baseline_payload_sha256"]
                or r["database_writes"] != 0 or r["broker_commands"] != 0
                or r["cost_publishable"] is not False or r["economic_nav_publishable"] is not False):
            raise ValueError("independent audit scope mismatch")
    paths = {k: reference / f"{B.ACCOUNT}_{v}.csv" for k, v in B.FILES.items()}
    hashes = {k: hashlib.sha256(p.read_bytes()).hexdigest() for k, p in paths.items()}
    cash = EXT.read_cash(paths["cash_flows"])
    EXT.unique_rows(cash)
    funds, bridges = EXT.fund_bridges(list(B.rows(paths["funds"], "funds")), cash)
    executions = list(B.rows(paths["executions"], "executions"))
    deliveries = list(B.rows(paths["settlements"], "settlements"))
    days, used_cash = [], set()
    for day in DATES:
        data, provenance = {}, {}
        for name, method, snapshot in (("orders", "store.ListOrders", ""), ("fills", "store.ListFills", ""),
                                      ("transfers", "store.ListComponentTransfers", ""),
                                      ("open", "store.ListPositionSnapshots", "open"), ("close", "store.ListPositionSnapshots", "close")):
            data[name], provenance[name] = page_rows(pages, method, day, snapshot)
        opening, closing = quantities(data["open"]), quantities(data["close"])
        previous = [x for x in op["opening_reviews"] if x["previous_trade_date"] == day]
        current = [x for x in op["opening_reviews"] if x["trade_date"] == day]
        if (len(previous) != 1 or len(current) != 1 or any(x["state"] != "opening_quantities_verified" for x in previous+current)
                or previous[0]["previous_close_baseline"]["completeness"] != "complete"
                or closing != quantities(previous[0]["previous_close_positions"])
                or opening != quantities(current[0]["pre_open_positions"])):
            raise ValueError("independent complete position proof differs")
        archived = [q for d in book["days"] if d["trade_date"] == day for a in d["attempts"] for q in a["queries"]
                    if q["step"] == "fills" and q.get("book_coverage")]
        if len(archived) != 1 or archived[0]["state"] != "terminal_archive_consistent":
            raise ValueError("query archive scope incomplete")
        coverage = archived[0]["book_coverage"]
        transfer_check = coverage["component_transfers"]
        projection = transfer_projection_sha(data["transfers"])
        if (coverage["request_state"] != "account_day_unfiltered_request" or transfer_check["differences"]
                or transfer_check["matched"] != len(data["transfers"]) or transfer_check["book_rows"] != len(data["transfers"])
                or transfer_check["query_rows"] != len(data["transfers"])
                or projection != transfer_check["query_projection_sha256"] or projection != transfer_check["book_projection_sha256"]):
            raise ValueError("captured transfers differ from independently matched query/book projection")
        orders = {r["gateway_order_id"]: r for r in data["orders"]}
        legs, physical = [], Counter()
        for t in data["transfers"]:
            parent = orders[t["gateway_order_id"]]
            if parent["trade_side"] != "R" or parent["business_type"] != "E":
                raise ValueError("non-redemption transfer needs review")
            if security(t) == security(parent):
                continue
            if t["cash_substitution"] or t["component_qty"] != t["qty"] or t["component_symbol"] != t["symbol"] or t["component_exchange"] != t["exchange"]:
                raise ValueError("new transfer convention requires review")
            if t["price"] != 0:
                leg = review_cash_leg(t, parent, cash, opening, closing, data["fills"])
                if leg["cash_row"]["broker_flow_id"] in used_cash:
                    raise ValueError("cash reused across transfers")
                used_cash.add(leg["cash_row"]["broker_flow_id"])
                leg["broker_exports"] = review_exports(leg, executions, deliveries)
                if day.replace("-", "") not in funds or day.replace("-", "") == min(funds):
                    raise ValueError("missing adjacent cash bridge")
                legs.append(leg)
            else:
                physical[security(t)] += B.quantity(str(t["component_qty"]))
        inventory = []
        carried = {lot["security_id"] for lot in current[0]["research_open_lots"]
                   if lot["cost_bucket"].startswith("ETF_T0_COMPONENT:")}
        for key in sorted(set(physical) | carried):
            buys = sum(f["qty"] for f in data["fills"] if security(f) == key and f["trade_side"] == "B")
            sells = sum(f["qty"] for f in data["fills"] if security(f) == key and f["trade_side"] == "S")
            delta = opening.get(key, 0) + physical[key] + buys - sells - closing.get(key, 0)
            inventory.append({"security_id": key, "open_quantity": opening.get(key, 0), "physical_transfers": physical[key],
                              "buy_quantity": buys, "sell_quantity": sells, "close_quantity": closing.get(key, 0), "residual": delta})
        days.append({"trade_date": day, "capture_sequences": provenance, "transfer_projection_sha256": projection,
                     "original_query_transfer_state": transfer_check["state"], "transfer_rows": len(data["transfers"]),
                     "cash_legs": legs, "component_inventory": inventory,
                     "inventory_state": "aggregate_quantity_matched" if all(x["residual"] == 0 for x in inventory) else "requires_review",
                     "parent_sale_allocation_verified": False})
    if hashes != {k: hashlib.sha256(p.read_bytes()).hexdigest() for k, p in paths.items()}:
        raise ValueError("broker source changed during review")
    return {"schema": "fuying13_component_cash_audit.v1", "account_id": B.ACCOUNT, "generated_at": datetime.now(TZ).isoformat(),
            "manifest_sha256": manifest_sha, "input_sha256": manifest["sha256"], "book_evidence_sha256": book_sha,
            "opening_evidence_sha256": opening_sha, "source_sha256": hashes, "fund_bridges": bridges, "days": days,
            "summary": {"days": len(days), "cash_legs": len(used_cash), "amount": B.money(sum(B.cents(x["amount"]) for d in days for x in d["cash_legs"])),
                        "inventory_breaks": sum(x["residual"] != 0 for d in days for x in d["component_inventory"])},
            "database_writes": 0, "broker_commands": 0, "cost_publishable": False, "economic_nav_publishable": False,
            "scope": "Specific historical cash explanations only; no security enum, raw rewrite, extra receivable, fee finality or proof of per-parent cost allocation."}


def review_exports(leg, executions, deliveries):
    day, t, parent = leg["trade_date"].replace("-", ""), leg["original_transfer"], leg["parent_order"]
    clock = datetime.fromisoformat(t["matched_at"]).astimezone(TZ).strftime("%H:%M:%S")
    result = {}
    for kind, rows in (("executions", executions), ("settlements", deliveries)):
        dk, tk, qk, pk = ("日期", "时间", "成交量", "成交价") if kind == "executions" else ("成交日期", "成交时间", "成交数量", "成交价格")
        dates = [B.date(value) for value in {r[dk] for _, r in rows}]
        if not dates:
            raise ValueError("empty broker export")
        covered = min(dates) <= day <= max(dates)
        matches = [(n, r) for n, r in rows if r[dk] == day and r[tk] == clock and r["代码"] == security(parent)
                   and r["方向"] == "ETF赎回" and B.cents(r["成交金额"]) > 0
                   and (kind != "executions" or r["委托编号"] == leg["broker_order_reference"])]
        if covered and (len(matches) != 1 or B.cents(matches[0][1]["成交金额"]) != B.cents(leg["amount"])
                        or B.quantity(matches[0][1][qk]) != 0 or B.number(matches[0][1][pk]) != 0):
            raise ValueError("broker cash execution differs")
        if kind == "settlements" and matches and B.cents(matches[0][1]["实付金额"]) != B.cents(leg["amount"]):
            raise ValueError("delivery cash amount differs")
        result[kind] = {"state": "cash_record_matched" if covered else "outside_export_coverage",
                        "coverage": [min(dates), max(dates)], "rows": [{"source_row": n, "raw": r} for n, r in matches]}
    return result


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for name in ("capture", "book-evidence", "opening-evidence", "output"):
        p.add_argument("--" + name, required=True, type=Path)
    for name in ("manifest-sha256", "book-sha256", "opening-sha256"):
        p.add_argument("--" + name, required=True)
    p.add_argument("--reference", type=Path, default=Path("reference"))
    a = p.parse_args()
    result = audit(a.capture, a.manifest_sha256, a.book_evidence, a.book_sha256, a.opening_evidence, a.opening_sha256, a.reference)
    encoded = json.dumps(result, ensure_ascii=False, indent=2, default=encode_decimal)
    with os.fdopen(os.open(a.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as target:
        target.write(encoded + "\n")
    print(json.dumps(result["summary"], ensure_ascii=False))


if __name__ == "__main__":
    main()
