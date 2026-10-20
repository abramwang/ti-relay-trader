#!/usr/bin/env python3
"""One-time, offline audit of the 2026-10-20 Fuying13 broker exports.

Creates private evidence artifacts, never orders, broker commands or published NAV.
"""

from __future__ import annotations

import argparse
import collections
import csv
import gzip
import hashlib
import json
import re
from contextlib import ExitStack
from datetime import datetime
from decimal import Decimal, InvalidOperation
from pathlib import Path
from zoneinfo import ZoneInfo


ACCOUNT = "314000045768"
PRIOR_STRATEGY_BONDS = {"254539.SH", "254200.SH", "114399.SH"}
FILES = {
    "funds": "资金",
    "cash_flows": "资金流水",
    "settlements": "历史交割单",
    "executions": "历史成交",
}
ORDINARY = {"买入", "卖出", "通用回购逆回购", "融券"}


def number(value: str) -> Decimal:
    try:
        result = Decimal(value.strip())
    except InvalidOperation as exc:
        raise ValueError(f"invalid number: {value!r}") from exc
    if not result.is_finite():
        raise ValueError("non-finite number")
    return result


def cents(value: str) -> int:
    scaled = number(value) * 100
    if scaled != scaled.to_integral_value():
        raise ValueError(f"sub-cent amount: {value!r}")
    return int(scaled)


def quantity(value: str) -> int:
    result = number(value)
    if result < 0 or result != result.to_integral_value():
        raise ValueError(f"invalid quantity: {value!r}")
    return int(result)


def money(value: int) -> str:
    return format(Decimal(value) / 100, ".2f")


def date(value: str) -> str:
    value = value.strip()
    parsed = datetime.strptime(value, "%Y%m%d")
    if parsed.strftime("%Y%m%d") != value:
        raise ValueError(f"invalid compact date: {value!r}")
    return value


def clock(value: str) -> str:
    value = value.strip()
    if datetime.strptime(value, "%H:%M:%S").strftime("%H:%M:%S") != value:
        raise ValueError(f"invalid clock: {value!r}")
    return value


def dump_line(target, item: dict) -> None:
    target.write(json.dumps(item, ensure_ascii=False, sort_keys=True) + "\n")


def rows(path: Path, kind: str):
    opener = gzip.open if path.suffix == ".gz" else open
    with opener(path, "rt", encoding="utf-8-sig", newline="") as source:
        reader = csv.reader(source)
        header = [x.strip() for x in next(reader)]
        for row_number, values in enumerate(reader, 2):
            columns = header
            if kind == "cash_flows" and len(header) == 14 and len(values) == 13:
                if header[11:13] != ["资金余额", "操作摘要"]:
                    raise ValueError("unrecognized omitted cash-balance column")
                columns = header[:11] + header[12:]
            if len(columns) != len(values):
                raise ValueError(f"{kind} row {row_number}: unexpected CSV width")
            item = {k: v.strip() for k, v in zip(columns, values) if k}
            if kind == "cash_flows":
                if item["投资者"] != ACCOUNT or item["资金账户"] != ACCOUNT + "01":
                    raise ValueError("cash-flow account mismatch")
            elif item["客户号"] != ACCOUNT:
                raise ValueError(f"{kind} account mismatch")
            yield row_number, item


def execution_key(row: dict, settlement: bool) -> tuple:
    return (
        date(row["成交日期" if settlement else "日期"]),
        clock(row["成交时间" if settlement else "时间"]),
        row["代码"],
        row["方向"].replace("通用回购逆回购", "融券"),
        quantity(row["成交数量" if settlement else "成交量"]),
        number(row["成交价格" if settlement else "成交价"]),
        cents(row["成交金额"]),
    )


def repo_quantity(code: str, qty: int, principal_cents: int) -> int:
    # Explicit units in this export; a different contract fails instead of guessing.
    multiplier = {"204001.SH": 10, "131810.SZ": 1}.get(code)
    if multiplier is None or qty * multiplier * 10000 != principal_cents:
        raise ValueError("unverified reverse-repo quantity/principal convention")
    return qty * multiplier


def source_trade_date(summary: str) -> str | None:
    match = re.search(r"成交日(\d{8})(?!\d)|^(\d{8})ETF", summary)
    return date(next(x for x in match.groups() if x)) if match else None


def audit(paths: dict[str, Path], files: dict, execution_output=None, cash_output=None) -> dict:
    funds = {}
    for row_number, raw in rows(paths["funds"], "funds"):
        day = date(raw["交易日"])
        if day in funds:
            raise ValueError("duplicate funds date")
        funds[day] = {
            "date": day,
            "source_row": row_number,
            **{key: cents(raw[column]) for key, column in {
                "cash": "客户资金", "deposit": "入金", "withdrawal": "出金",
                "market_value": "市值", "pnl": "当日盈亏", "total": "总资产",
            }.items()},
        }
    if len(funds) < 2:
        raise ValueError("at least two funds dates required")
    days = sorted(funds)
    cash_net = collections.Counter()
    external_net = collections.Counter()
    flow_ids = set()
    flow_subjects = collections.Counter()
    etf_cash_counts = collections.Counter()
    financial_flows = collections.Counter()
    financial_subjects = {
        "买入": "买入成交清算资金", "卖出": "卖出成交清算资金",
        "融券": "回购融券成交划出资金", "融券购回": "回购融券购回资金划入",
    }
    for row_number, raw in rows(paths["cash_flows"], "cash_flows"):
        day, at = date(raw["交易日期"]), clock(raw["交易时间"])
        if day not in funds or not raw["流水号"] or raw["流水号"] in flow_ids:
            raise ValueError("missing funds date or missing/duplicate flow identity")
        flow_ids.add(raw["流水号"])
        incoming, outgoing = cents(raw["入金"]), cents(raw["出金"])
        if incoming < 0 or outgoing < 0:
            raise ValueError("negative flow leg")
        amount = incoming - outgoing
        cash_net[day] += amount
        subject = raw["业务科目"]
        flow_subjects[subject] += 1
        if subject in ("银证转帐取", "银证转帐存"):
            external_net[day] += amount
        if subject in financial_subjects.values():
            financial_flows[(day, raw["股票代码"], subject, amount)] += 1
        is_etf = subject.startswith("ETF") or subject == "unknown type" and "ETF" in raw["操作摘要"]
        origin = source_trade_date(raw["操作摘要"]) if is_etf else None
        if origin and origin > day:
            raise ValueError("cash posting precedes explicit source trade date")
        if is_etf:
            etf_cash_counts["explicit_source_date" if origin else "source_date_unverified"] += 1
        if cash_output is not None:
            dump_line(cash_output, {
                "source_sha256": files["cash_flows"]["sha256"], "source_row": row_number,
                "account_id": ACCOUNT, "broker_flow_id": raw["流水号"],
                "cash_posted_at": datetime.strptime(day + " " + at, "%Y%m%d %H:%M:%S").replace(tzinfo=ZoneInfo("Asia/Shanghai")).isoformat(),
                "actual_amount": money(amount), "source_trade_date": origin,
                "etf_related": is_etf, "raw_fields": raw,
            })
    for i, day in enumerate(days):
        row = funds[day]
        if row["deposit"] - row["withdrawal"] != external_net[day]:
            raise ValueError(f"external-flow bridge mismatch on {day}")
        if i:
            previous = funds[days[i - 1]]
            if row["cash"] - previous["cash"] != cash_net[day]:
                raise ValueError(f"cash bridge mismatch on {day}")
            if row["total"] != previous["total"] + row["deposit"] - row["withdrawal"] + row["pnl"]:
                raise ValueError(f"asset identity mismatch on {day}")

    delivery = collections.defaultdict(collections.deque)
    financial_delivery = collections.Counter()
    bonds = []
    custody = []
    nonfinancial_off_funds_dates = collections.Counter()
    settlement_count = 0
    for row_number, raw in rows(paths["settlements"], "settlements"):
        settlement_count += 1
        day = date(raw["成交日期"])
        side = raw["方向"]
        if day not in funds:
            if side != "手工冻结" or any(cents(raw[column]) for column in ("成交金额", "实付金额")):
                raise ValueError("financial settlement has no funds date")
            nonfinancial_off_funds_dates[day] += 1
        if side in ORDINARY:
            delivery[execution_key(raw, True)].append((row_number, raw))
        if side in financial_subjects:
            financial_delivery[(day, raw["代码"].split(".")[0], financial_subjects[side], cents(raw["实付金额"]))] += 1
        if raw["代码"] in PRIOR_STRATEGY_BONDS:
            bonds.append({"source_row": row_number, **raw})
        if side == "托管转入":
            custody.append({"source_row": row_number, **raw})
    if financial_delivery != financial_flows:
        raise ValueError("ordinary/repo settlement cash legs do not match cash flows")

    ordinary_ids = set()
    full_rows = collections.Counter()
    etf_shapes = collections.Counter()
    execution_count, matched = 0, 0
    first_etf = None
    for row_number, raw in rows(paths["executions"], "executions"):
        execution_count += 1
        day = date(raw["日期"])
        if day not in funds:
            raise ValueError("execution has no funds date")
        full_rows[tuple(sorted(raw.items()))] += 1
        side = raw["方向"]
        key = execution_key(raw, False)
        if not raw["成交编号"] or not raw["委托编号"]:
            raise ValueError("missing broker execution/order reference")
        if side not in ORDINARY:
            if side not in ("ETF申购", "ETF赎回"):
                raise ValueError(f"unreviewed execution direction: {side}")
            first_etf = min(first_etf or day, day)
            etf_shapes[(side, key[4] > 0, key[5] > 0, key[6] > 0)] += 1
            continue
        identity = (day, raw["成交编号"])
        if identity in ordinary_ids:
            raise ValueError("duplicate ordinary execution identity")
        ordinary_ids.add(identity)
        if not delivery[key]:
            raise ValueError(f"ordinary execution lacks an exact delivery match: row {row_number}")
        delivery_row, settlement = delivery[key].popleft()
        matched += 1
        normalized_qty = repo_quantity(key[2], key[4], key[6]) if side == "通用回购逆回购" else key[4]
        if key[4] <= 0 or key[5] <= 0 or key[6] <= 0:
            raise ValueError("nonpositive ordinary execution")
        if execution_output is not None:
            dump_line(execution_output, {
                "account_id": ACCOUNT, "source_sha256": files["executions"]["sha256"],
                "source_row": row_number, "delivery_sha256": files["settlements"]["sha256"],
                "delivery_row": delivery_row, "broker_order_reference": raw["委托编号"],
                "broker_execution_id": raw["成交编号"], "trade_date": day,
                "security_code": raw["代码"], "executed_quantity": normalized_qty,
                "original_order_quantity": None, "order_final_status": None,
                "raw_fields": raw, "delivery_fields": settlement,
            })
    unmatched = [raw for queue in delivery.values() for _, raw in queue]
    if any(raw["代码"] not in PRIOR_STRATEGY_BONDS for raw in unmatched):
        raise ValueError("unmatched delivery beyond the explicitly excluded bond strategy")
    if not bonds:
        raise ValueError("missing prior-strategy bond evidence")
    last_bond_day = max(row["成交日期"] for row in bonds)
    last_balances = {}
    # The broker file is reverse posting order, including within a date.
    for row in sorted(bonds, key=lambda x: (x["成交日期"], -x["source_row"])):
        last_balances[row["代码"]] = quantity(row["股份余额"])
    if set(last_balances) != PRIOR_STRATEGY_BONDS or any(last_balances.values()):
        raise ValueError("excluded bond strategy still has nonzero or unproved final holdings")
    candidates = [day for day in days if day > last_bond_day]
    if not candidates:
        raise ValueError("no post-bond funds dates")
    handover = candidates[0]
    book_days = []
    for day in days:
        row = funds[day]
        book_days.append({
            "trade_date": day, "source_row": row["source_row"],
            "scope": "before_handover_candidate_not_published" if day < handover else "handover_date_pending_confirmation",
            **{key: money(row[key]) for key in ("cash", "market_value", "deposit", "withdrawal", "total", "pnl")},
        })
    return {
        "account_id": ACCOUNT, "business_timezone": "Asia/Shanghai",
        "evidence_audit_passed": True, "economic_nav_publishable": False,
        "database_writes": 0, "wire_changes": False, "recurring_import": False,
        "files": files, "funds_days": len(days), "funds_from": days[0], "funds_to": days[-1],
        "adjacent_identity_and_cash_bridges": len(days) - 1,
        "opening_cash": money(funds[days[0]]["cash"]), "opening_balance_is_zero": funds[days[0]]["total"] == 0,
        "cash_flow_rows": len(flow_ids), "cash_flow_subjects": dict(flow_subjects),
        "settlement_rows": settlement_count, "execution_rows": execution_count,
        "nonfinancial_freezes_without_funds_date": dict(nonfinancial_off_funds_dates),
        "ordinary_executions_exactly_matched": matched,
        "unmatched_delivery_rows": len(unmatched), "excluded_bond_rows": bonds,
        "last_bond_sale_date": last_bond_day, "last_bond_balances": last_balances,
        "handover_candidate": handover, "handover_confirmed": False,
        "handover_previous_close": money(funds[last_bond_day]["total"]),
        "first_etf_execution_date": first_etf,
        "etf_cash_date_coverage": dict(etf_cash_counts),
        "etf_execution_shapes": [{"direction": key[0], "positive_quantity": key[1], "positive_price": key[2], "positive_amount": key[3], "rows": count} for key, count in sorted(etf_shapes.items())],
        "repeated_identical_export_rows_preserved": sum(count - 1 for count in full_rows.values()),
        "custody_transfer_rows_require_corporate_action_review": custody,
        "book_days_not_economic_nav": book_days,
        "remaining_work": [
            "Confirm the management handover date; exclude the prior bond strategy, not arbitrary asset loss adjustments.",
            "Rebuild full daily holdings, corporate actions, repo principal and ETF pending rights before publishing economic NAV.",
            "Associate ETF actual cash by explicit source facts, retaining unknown dates and final-settlement completeness.",
            "Historical order quantities, rejected/cancelled orders and final states cannot be reconstructed from execution references.",
        ],
    }


def run(reference: Path, output: Path) -> dict:
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    marker = output / "INCOMPLETE"
    marker.write_text("Audit not complete; do not consume these artifacts.\n", encoding="ascii")
    files, paths = {}, {}
    for kind, suffix in FILES.items():
        source = reference / f"{ACCOUNT}_{suffix}.csv"
        target = output / f"{kind}.csv.gz"
        digest = hashlib.sha256()
        with source.open("rb") as reader, gzip.open(target, "wb", compresslevel=3) as writer:
            for chunk in iter(lambda: reader.read(1024 * 1024), b""):
                digest.update(chunk)
                writer.write(chunk)
        paths[kind] = target
        files[kind] = {"source": str(source), "snapshot": target.name, "sha256": digest.hexdigest()}
    with ExitStack() as stack:
        execution_output = stack.enter_context(gzip.open(output / "matched_executions.jsonl.gz", "wt", encoding="utf-8", compresslevel=3))
        cash_output = stack.enter_context(gzip.open(output / "cash_evidence.jsonl.gz", "wt", encoding="utf-8", compresslevel=3))
        result = audit(paths, files, execution_output, cash_output)
    result["generated_at"] = datetime.now(ZoneInfo("Asia/Shanghai")).isoformat()
    result["artifact_sha256"] = {
        path.name: hashlib.sha256(path.read_bytes()).hexdigest()
        for path in sorted(output.iterdir()) if path.suffix == ".gz"
    }
    (output / "report.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    marker.unlink()
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", type=Path, default=Path("reference"))
    parser.add_argument("--output", type=Path, required=True, help="new private directory; parent must already exist")
    args = parser.parse_args()
    report = run(args.reference, args.output)
    print(json.dumps({key: value for key, value in report.items() if key not in ("book_days_not_economic_nav", "excluded_bond_rows", "custody_transfer_rows_require_corporate_action_review")}, ensure_ascii=False, indent=2))
