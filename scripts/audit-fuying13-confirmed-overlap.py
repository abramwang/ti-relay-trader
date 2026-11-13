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
COMBINED_HASHES = {
    "repo": "c58f563374abc8c4d74209687226ec7ce8aadac2f323770ef2b33ee932f9d05f",
    "fees": "daa6a2d47e1abd22f20f87c27f6e37a339d83358dcbee275a2bb18f53e211369",
    "residual": "2f83c667ddcbe944554611d2e559e5f18311012741fe3a733ca6f6537e0dcf5d",
}


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


def dated(rows):
    result = {}
    last = ""
    for row in rows:
        day = row["trade_date"]
        if R.iso(day.replace("-", "")) != day or day <= last:
            raise ValueError("duplicate/unordered reconciliation day")
        result[day] = row
        last = day
    return result


def micro(row, key):
    value = row[key]
    if type(value) is not int or abs(value) >= 10**18:
        raise ValueError("invalid repo micro amount")
    return Decimal(value) / 1000000


def compose(nav, matrix, repo, fees, residual):
    result = preview(nav, matrix)
    for report in (repo, fees, residual):
        if (report["account_id"] != ACCOUNT or report["economic_nav_publishable"]
                or report["database_writes"] != 0 or report["broker_commands"] != 0):
            raise ValueError("unexpected combined evidence scope")
    if repo["interest_bridge"]["account_id"] != ACCOUNT:
        raise ValueError("wrong repo bridge account")
    models = dated(nav["contributions"])
    fdays = dated(fees["days"])
    rdays = dated(repo["interest_bridge"]["days"])
    candidates = [s for s in residual["scenarios"] if s["name"] == "explicit_plus_pcf_candidates"]
    if len(candidates) != 1:
        raise ValueError("missing/ambiguous residual scenario")
    ddays = dated(candidates[0]["days"])
    cash = dated(next(s for s in nav["cash_nav_scenarios"]
                      if s["name"] == "explicit_plus_pcf_candidates")["days"])
    dates = [r["trade_date"] for r in result["days"]]
    if any(list(mapping) != dates for mapping in (fdays, rdays, ddays, cash)):
        raise ValueError("combined day coverage differs")
    # The frozen model has one extra cost-chain day; never use it as a NAV day.
    if any(day not in models for day in dates):
        raise ValueError("missing model day")
    combined, previous_close = [], None
    previous_repo, previous_other = None, R.number(residual["opening_unclassified_asset"])
    total_pnl, total_attributed = ZERO, ZERO
    for o in result["days"]:
        day = o["trade_date"]
        b, f, d, c, model = rdays[day], fdays[day], ddays[day], cash[day], models[day]
        if model["account_id"] != ACCOUNT or model["summary"]["missing_items"] != 0:
            raise ValueError("incomplete/wrong account contribution")
        op, cl = micro(b, "open_receivable_micros"), micro(b, "close_receivable_micros")
        accrued = micro(b, "accrued_interest_micros")
        received = micro(b, "received_interest_micros")
        released = micro(b, "released_receivable_micros")
        variance = micro(b, "settlement_variance_micros")
        adjustment = micro(b, "cash_to_accrual_adjustment_micros")
        if (min(op, cl, accrued, received, released) < 0 or cl != op + accrued - released
                or variance != received - released or adjustment != cl - op
                or micro(b, "recognized_interest_micros") != accrued + variance
                or previous_repo is not None and previous_repo != op
                or accrued != R.number(d["repo_model_interest"])
                or received != R.number(d["repo_received_interest"])
                or received - accrued != R.number(d["repo_posting_vs_model_adjustment"])):
            raise ValueError("repo bridge identity/continuity failed")
        fee_delta = R.number(f["ordinary_model_fee"]) - R.number(f["cash_implied_fee"])
        if (min(R.number(f["ordinary_model_fee"]), R.number(f["cash_implied_fee"])) < 0
                or fee_delta != R.number(f["fee_effect_on_pnl"])):
            raise ValueError("fee delta inconsistent")
        base_model = R.number(model["summary"]["net_contribution"])
        t0_delta = R.number(d["cash_vs_model_t0_adjustment"])
        base_pnl = R.number(c["conditional_day_pnl"])
        other = R.number(c["broker_unclassified_asset_preserved"])
        if (base_pnl - base_model != R.number(d["starting_model_residual"])
                or other != R.number(d["unclassified_asset"])
                or other - previous_other != R.number(d["unclassified_asset_change"])):
            raise ValueError("original residual evidence changed")
        # Fees are already in broker cash. Correct the attribution comparison,
        # never deduct these fees again from the asset-based curve.
        corrected_model = base_model + t0_delta + fee_delta + variance
        new_open = R.number(o["after_open"]) + op
        new_close = R.number(o["after_close"]) + cl
        pnl = new_close - new_open
        if (new_open <= 0 or new_close <= 0
                or previous_close is not None and previous_close != new_open
                or pnl != base_pnl + adjustment + R.number(o["pnl_change"])):
            raise ValueError("combined asset continuity failed")
        before_residual = base_pnl + adjustment - corrected_model
        after_residual = pnl - corrected_model
        old_remainder = (base_pnl - base_model - t0_delta
                         - (received - accrued) - (other - previous_other) - fee_delta)
        if (old_remainder != R.number(f["remaining_diagnostic_remainder"])
                or after_residual - before_residual != R.number(o["pnl_change"])):
            raise ValueError("combined reconciliation identity failed")
        combined.append({"trade_date": day, "combined_open_asset": str(new_open),
            "combined_close_asset": str(new_close), "combined_conditional_pnl": str(pnl),
            "original_cash_pnl": str(base_pnl), "repo_asset_adjustment": str(adjustment),
            "confirmed_overlap_pnl_change": o["pnl_change"],
            "original_model_contribution": str(base_model), "cash_vs_model_t0_adjustment": str(t0_delta),
            "fee_attribution_adjustment": str(fee_delta), "repo_settlement_variance": str(variance),
            "corrected_model_contribution": str(corrected_model),
            "broker_other_asset": str(other), "broker_other_asset_change": str(other-previous_other),
            "residual_before_overlap": str(before_residual), "residual_after_overlap": str(after_residual),
            "diagnostic_after_subtracting_other_change": str(after_residual-other+previous_other),
            "has_arithmetic_residual": after_residual != 0, "economic_nav_publishable": False})
        previous_close, previous_repo, previous_other = new_close, cl, other
        total_pnl += pnl
        total_attributed += corrected_model
    if total_pnl != R.number(combined[-1]["combined_close_asset"]) - R.number(combined[0]["combined_open_asset"]):
        raise ValueError("combined period identity failed")
    result["schema"] = "fuying13_combined_inclusion_preview.v1"
    result["combined_days"] = combined
    result["combined_summary"] = {
        "days": len(combined), "period_pnl": str(total_pnl),
        "period_attributed_pnl": str(total_attributed), "period_residual": str(total_pnl-total_attributed),
        "nonzero_residual_days": [r["trade_date"] for r in combined if r["has_arithmetic_residual"]],
        "nonzero_residual_after_other_change_days": [r["trade_date"] for r in combined
            if R.number(r["diagnostic_after_subtracting_other_change"]) != 0],
        "all_daily_residuals_zero": all(not r["has_arithmetic_residual"] for r in combined)}
    result["remaining_gates"] = [
        "signed_overlap_interpretation_not_reconciled" if result["combined_summary"]["nonzero_residual_after_other_change_days"]
        else "signed_overlap_interpretation_requires_independent_evidence",
        "other_snapshot_asset_scope_unconfirmed", "private_cash_source_dates_unconfirmed",
        "repo_interest_inclusion_unconfirmed", "complete_private_settlement_coverage_unverified"]
    result["asset_scope_review"] = [
        {"trade_date": r["trade_date"], "reported_component": r["broker_other_asset"],
         "business_inclusion_confirmed": r["trade_date"] == DAY,
         "accounting_sign_independently_verified": False}
        for r in combined if R.number(r["broker_other_asset"]) != 0]
    result["source_date_review"] = [
        {"flow_ids": list(g["flow_ids"]), "posted_date": g["posted_date"],
         "amount": g["amount"], "candidates": g["pcf_order_multiset_candidates"],
         "source_verified": False,
         "business_inclusion_confirmed_snapshot": DAY if set(g["flow_ids"]) == FLOW_IDS else None}
        for g in matrix["cash_pcf_review"]["unresolved_candidates"]]
    result["assumptions"].extend([
        "repo gross interest assumed outside reported assets; no principal added",
        "cash-implied fees correct attribution only; not a second cash deduction",
        "zero period residual does not prove daily NAV; no residual assigned to income"])
    return result


def component_basis_preview(nav, combined, funds_rows):
    """Separate broker presentation from cash-based valuation, without publishing."""
    for report in (nav, combined):
        if (report["account_id"] != ACCOUNT or report["economic_nav_publishable"]
                or report["database_writes"] != 0 or report["broker_commands"] != 0):
            raise ValueError("unexpected component comparison scope")
    cash = dated(next(s for s in nav["cash_nav_scenarios"]
                      if s["name"] == "explicit_plus_pcf_candidates")["days"])
    overlap_days = dated(combined["days"])
    funds = {}
    for row_number, row in funds_rows:
        day = R.iso(row["交易日"])
        if row["客户号"] != ACCOUNT or day in funds:
            raise ValueError("wrong/duplicate broker funds identity")
        funds[day] = (row_number, row)
    rows = combined["combined_days"]
    if not rows or list(cash) != [r["trade_date"] for r in rows] or list(overlap_days) != list(cash):
        raise ValueError("component basis day coverage differs")
    verified = []
    for row in rows:
        day = row["trade_date"]
        if day not in funds:
            raise ValueError("missing broker funds date")
        number, f = funds[day]
        c = cash[day]
        cash_total, market, total = (R.number(f[k]) for k in ("客户资金", "市值", "总资产"))
        principal = R.number(c["repo_principal_already_in_reported_asset"])
        component = R.number(row["broker_other_asset"])
        if (principal < 0 or total != R.number(c["reported_close_asset"])
                or component != total-cash_total-market-principal):
            raise ValueError("broker component decomposition failed")
        verified.append({"trade_date": day, "funds_row": number, "cash_total": str(cash_total),
                         "market_value": str(market), "repo_principal": str(principal),
                         "reported_total": str(total), "display_component": str(component),
                         "cash_market_repo_basis": str(cash_total+market+principal)})
    # This review has a zero-component opening anchor. Do not silently extend
    # it to another period with an unreviewed opening component.
    first = rows[0]
    opening_component = R.number(first["broker_other_asset"])-R.number(first["broker_other_asset_change"])
    if opening_component != 0:
        raise ValueError("opening component requires separate evidence")
    scenarios = []
    for all_components in (False, True):
        name = "confirmed_snapshot_display_component_only" if not all_components else "all_display_components_sensitivity_only"
        previous_removed, previous_close = ZERO, None
        days, total_pnl, total_residual = [], ZERO, ZERO
        for original, anchor in zip(rows, verified):
            day = original["trade_date"]
            display = R.number(anchor["display_component"])
            removed = display if all_components or day == DAY else ZERO
            # Undo the quarantined signed-debit assumption, then subtract only
            # the displayed component once. Signed pending cash stays intact.
            old = overlap_days[day]
            old_close_delta = R.number(old["after_close"])-R.number(old["before_close"])
            old_open_delta = R.number(old["after_open"])-R.number(old["before_open"])
            baseline_open = R.number(original["combined_open_asset"])-old_open_delta
            baseline_close = R.number(original["combined_close_asset"])-old_close_delta
            if baseline_close-baseline_open != R.number(original["original_cash_pnl"])+R.number(original["repo_asset_adjustment"]):
                raise ValueError("comparison baseline does not reconcile")
            op, cl = baseline_open-previous_removed, baseline_close-removed
            if min(op, cl) <= 0 or previous_close is not None and op != previous_close:
                raise ValueError("component basis continuity failed")
            pnl = cl-op
            residual = pnl-R.number(original["corrected_model_contribution"])
            days.append({"trade_date": day, "conditional_open_asset": str(op),
                         "conditional_close_asset": str(cl), "conditional_pnl": str(pnl),
                         "removed_display_component": str(removed), "residual": str(residual),
                         "pnl_change_from_cash_repo_curve": str(previous_removed-removed),
                         "economic_nav_publishable": False})
            previous_removed, previous_close = removed, cl
            total_pnl += pnl
            total_residual += residual
        scenarios.append({"name": name, "days": days, "period_pnl": str(total_pnl),
                          "period_residual": str(total_residual),
                          "nonzero_residual_days": [d["trade_date"] for d in days if R.number(d["residual"]) != 0],
                          "all_daily_residuals_zero": all(R.number(d["residual"]) == 0 for d in days),
                          "economic_nav_publishable": False})
    return {"verified_broker_decomposition": verified, "scenarios": scenarios,
            "source_dates_verified": False, "display_component_removal_authorized": False,
            "broker_reported_totals_modified": False, "economic_nav_publishable": False,
            "assumptions": ["cash, market and repo used as a separate comparison basis, not an overwrite of broker totals",
                            "signed pending cash remains in both scenarios; candidate sources are unchanged",
                            "all-component scenario is sensitivity only, not inferred confirmation",
                            "business linkage does not independently authorize excluding the displayed component"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence-dir", type=Path,
                        default=Path("outputs/recovery/fuying13_cash_matrix_20261112"))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--combined", action="store_true", help="compose repo bridge and cash fee evidence offline")
    args = parser.parse_args()
    paths = {"matrix": args.evidence_dir / "matrix_verified.json",
             "nav": args.evidence_dir / "nav_scenarios_v1.json"}
    expected = dict(R.HASHES)
    if args.combined:
        paths.update({"repo": args.evidence_dir / "repo_bridge_v1.json",
                      "fees": args.evidence_dir / "execution_fees_verified_v2.json",
                      "residual": args.evidence_dir / "residual_verified_v2.json"})
        expected.update(COMBINED_HASHES)
    raw = {key: path.read_bytes() for key, path in paths.items()}
    hashes = {key: hashlib.sha256(value).hexdigest() for key, value in raw.items()}
    if hashes != expected:
        raise ValueError("frozen input changed")
    evidence = {key: json.loads(value) for key, value in raw.items()}
    if args.combined:
        suffixes = {"funds": "资金", "cash_flows": "资金流水", "settlements": "历史交割单", "executions": "历史成交"}
        for key, suffix in suffixes.items():
            path = Path("reference") / f"{ACCOUNT}_{suffix}.csv"
            if hashlib.sha256(path.read_bytes()).hexdigest() != evidence["matrix"]["source_sha256"][key]:
                raise ValueError(f"broker source changed: {key}")
    report = (compose(evidence["nav"], evidence["matrix"], evidence["repo"], evidence["fees"], evidence["residual"])
              if args.combined else preview(evidence["nav"], evidence["matrix"]))
    if args.combined:
        report["component_basis_preview"] = component_basis_preview(evidence["nav"], report,
            list(R.M.B.rows(Path("reference") / f"{ACCOUNT}_资金.csv", "funds")))
        # Recheck after parsing; a concurrent export must not mix old report
        # hashes with new rows in the comparison basis.
        for key, suffix in suffixes.items():
            path = Path("reference") / f"{ACCOUNT}_{suffix}.csv"
            if hashlib.sha256(path.read_bytes()).hexdigest() != evidence["matrix"]["source_sha256"][key]:
                raise ValueError(f"broker source changed during review: {key}")
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
                      "combined_summary": report.get("combined_summary"),
                      "component_basis_summary": [
                          {k: s[k] for k in ("name", "nonzero_residual_days", "all_daily_residuals_zero")}
                          for s in report.get("component_basis_preview", {}).get("scenarios", [])],
                      "economic_nav_publishable": False}, ensure_ascii=False))


if __name__ == "__main__":
    main()
