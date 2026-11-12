#!/usr/bin/env python3
"""Preview the confirmed Oct 16 inclusion without rewriting broker assets or NAV."""

import argparse
import hashlib
import importlib.util
import json
import os
from datetime import datetime
from decimal import Decimal
from pathlib import Path
from zoneinfo import ZoneInfo

SPEC = importlib.util.spec_from_file_location(
    "residual", Path(__file__).with_name("audit-fuying13-nav-residual.py"))
R = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(R)
ACCOUNT = "314000045768"
DAY = "2026-10-16"
POSTED = "2026-10-19"
FLOW_IDS = {"2740045451", "2740045450", "2740045444"}
ZERO = Decimal(0)


def cash_legs(matrix):
    if matrix["account_id"] != ACCOUNT:
        raise ValueError("wrong matrix account")
    legs, used = [], set()
    rows = matrix["resolved_cash"] + matrix["unresolved_cash"]
    lookup = {r["broker_flow_id"]: r for r in rows}
    if len(lookup) != len(rows):
        raise ValueError("duplicate cash identity")
    for group in matrix["cash_pcf_review"]["unresolved_candidates"]:
        if not FLOW_IDS.intersection(group["flow_ids"]):
            continue
        if (set(group["flow_ids"]) != FLOW_IDS or len(group["flow_ids"]) != 3
                or group["private_source_verified"] or group["included_in_known_carry"]
                or len(group["pcf_order_multiset_candidates"]) != 1):
            raise ValueError("confirmed group changed or promoted")
        source = R.iso(group["pcf_order_multiset_candidates"][0]["source_trade_date"])
        for fid in group["flow_ids"]:
            row = lookup.get(fid)
            if (row is None or fid in used or row["account_id"] != ACCOUNT or row["resolved_source_trade_date"]
                    or row["raw_fields"]["股票代码"] != "510310"
                    or R.iso(row["raw_fields"]["交易日期"]) != POSTED
                    or R.number(row["actual_amount"]) != Decimal("-4229.40")
                    or not source <= DAY < POSTED):
                raise ValueError("cash inclusion identity/amount/date mismatch")
            used.add(fid)
            legs.append({"broker_flow_id": fid, "candidate_source_date": source,
                         "posted_date": POSTED, "signed_amount": row["actual_amount"]})
    if used != FLOW_IDS:
        raise ValueError("incomplete confirmed cash group")
    return legs


def preview(nav, matrix):
    if (nav["account_id"] != ACCOUNT or nav["economic_nav_publishable"]
            or nav["database_writes"] != 0 or nav["broker_commands"] != 0):
        raise ValueError("unexpected NAV evidence scope")
    legs = cash_legs(matrix)
    scenarios = [s for s in nav["cash_nav_scenarios"]
                 if s["name"] == "explicit_plus_pcf_candidates"]
    if len(scenarios) != 1:
        raise ValueError("candidate scenario missing/ambiguous")
    scenario = scenarios[0]
    overlap = sum((R.number(x["signed_amount"]) for x in legs), ZERO)
    days, seen = [], set()
    previous_day, previous_close, previous_reported = "", None, None
    previous_overlap, delta_sum = ZERO, ZERO
    for row in scenario["days"]:
        day = row["trade_date"]
        if R.iso(day.replace("-", "")) != day or day <= previous_day:
            raise ValueError("unordered/invalid snapshot dates")
        seen.add(day)
        opening = R.number(row["conditional_open_asset"])
        closing = R.number(row["conditional_close_asset"])
        reported_open = R.number(row["reported_open_asset"])
        reported_close = R.number(row["reported_close_asset"])
        if (opening <= 0 or closing <= 0
                or opening != reported_open + R.number(row["known_open_carry"]) + R.number(row["candidate_open_carry"])
                or closing != reported_close + R.number(row["known_close_carry"]) + R.number(row["candidate_close_carry"])
                or closing - opening != R.number(row["conditional_day_pnl"])
                or previous_reported is not None and reported_open != previous_reported):
            raise ValueError("broken input asset bridge")
        # Confirmation covers this close only, not an inferred source day or
        # every day between source and payment. Never subtract the positive
        # broker component itself: remove only the signed duplicate overlay.
        included = overlap if day == DAY else ZERO
        if day == DAY and R.number(row["broker_unclassified_asset_preserved"]) != -overlap:
            raise ValueError("confirmed broker component changed")
        new_open, new_close = opening - previous_overlap, closing - included
        if (new_open <= 0 or new_close <= 0
                or previous_close is not None and new_open != previous_close):
            raise ValueError("overlap preview continuity failed")
        pnl = new_close - new_open
        delta = pnl - R.number(row["conditional_day_pnl"])
        delta_sum += delta
        days.append({"trade_date": day, "reported_close_asset_preserved": str(reported_close),
                     "included_signed_overlay": str(included),
                     "before_open": str(opening), "before_close": str(closing),
                     "after_open": str(new_open), "after_close": str(new_close),
                     "before_pnl": str(R.number(row["conditional_day_pnl"])),
                     "after_pnl": str(pnl), "pnl_change": str(delta)})
        previous_overlap, previous_close, previous_reported = included, new_close, reported_close
        previous_day = day
    if not {DAY, POSTED}.issubset(seen) or delta_sum != 0 or previous_overlap != 0:
        raise ValueError("missing release day or nonconserving preview")
    changed = [r["trade_date"] for r in days if R.number(r["pnl_change"]) != 0]
    if changed != [DAY, POSTED]:
        raise ValueError("unexpected affected days")
    return {"schema": "fuying13_confirmed_inclusion_preview.v1", "account_id": ACCOUNT,
            "confirmation": {"date": "2026-11-12", "quote": "确实就是包含上述扣款",
                             "evidence_type": "user_confirmation", "snapshot_date": DAY,
                             "business_inclusion_confirmed": True},
            "cash_legs": legs, "days": days, "period_pnl_change": str(delta_sum),
            "assumptions": ["signed debit overlay already reflected in the confirmed broker snapshot",
                            "PCF candidate source dates remain unconfirmed",
                            "no confirmation inferred for other snapshot dates",
                            "other asset, repo and private settlement gates remain unchanged"],
            "source_date_verified": False, "accounting_sign_independently_verified": False,
            "broker_reported_assets_modified": False, "cash_flows_modified": False,
            "economic_nav_publishable": False, "database_writes": 0, "broker_commands": 0}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence-dir", type=Path,
                        default=Path("outputs/recovery/fuying13_cash_matrix_20261112"))
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    paths = {"matrix": args.evidence_dir / "matrix_verified.json",
             "nav": args.evidence_dir / "nav_scenarios_v1.json"}
    raw = {key: path.read_bytes() for key, path in paths.items()}
    hashes = {key: hashlib.sha256(value).hexdigest() for key, value in raw.items()}
    if hashes != R.HASHES:
        raise ValueError("frozen input changed")
    report = preview(json.loads(raw["nav"]), json.loads(raw["matrix"]))
    report["source_sha256"] = hashes
    report["generated_at"] = datetime.now(ZoneInfo("Asia/Shanghai")).isoformat()
    args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with os.fdopen(os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600),
                   "w", encoding="utf-8") as f:
        json.dump(report, f, ensure_ascii=False, indent=2, allow_nan=False)
        f.write("\n")
        f.flush()
        os.fsync(f.fileno())
    print(json.dumps({"changed_days": [r for r in report["days"] if R.number(r["pnl_change"])],
                      "period_pnl_change": report["period_pnl_change"],
                      "economic_nav_publishable": False}, ensure_ascii=False))


if __name__ == "__main__":
    main()
