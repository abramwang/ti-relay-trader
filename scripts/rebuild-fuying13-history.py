#!/usr/bin/env python3
"""Rebuild private historical quantity/valuation evidence, not live orders or NAV."""

from __future__ import annotations

import argparse
import collections
import csv
import gzip
import hashlib
import importlib.util
import json
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timedelta
from decimal import Decimal
from pathlib import Path
from zoneinfo import ZoneInfo


SPEC = importlib.util.spec_from_file_location("broker_audit", Path(__file__).with_name("audit-fuying13-history.py"))
BROKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BROKER)
START = "20250703"
OPENING_CENTS = 1000086708
SOURCE_AUDIT_SHA256 = "6fb763abb09c475cf38f139f28a7557e293dd4cb3376dfc7c3182bb881814ef6"


def json_text(value):
    return json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n"


def sha_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_evidence(directory):
    if (directory / "INCOMPLETE").exists():
        raise ValueError("incomplete source evidence")
    if sha_file(directory / "report.json") != SOURCE_AUDIT_SHA256:
        raise ValueError("reviewed handover evidence changed")
    report = json.loads((directory / "report.json").read_text())
    if report["account_id"] != BROKER.ACCOUNT or not report["evidence_audit_passed"]:
        raise ValueError("unverified account/source audit")
    for name, digest in report["artifact_sha256"].items():
        if Path(name).name != name or sha_file(directory / name) != digest:
            raise ValueError("source artifact hash mismatch")
    for kind, item in report["files"].items():
        digest = hashlib.sha256()
        if Path(item["snapshot"]).name != item["snapshot"]:
            raise ValueError("invalid snapshot path")
        with gzip.open(directory / item["snapshot"], "rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
        if digest.hexdigest() != item["sha256"]:
            raise ValueError(f"source snapshot hash mismatch: {kind}")
    if report["last_bond_sale_date"] != "20250702" or report["handover_previous_close"] != BROKER.money(OPENING_CENTS):
        raise ValueError("handover evidence changed")
    return report


class MeridianEvidence:
    def __init__(self, base_url, directory, reuse_directory=None):
        self.base_url = base_url.rstrip("/")
        self.directory = directory
        self.directory.mkdir(mode=0o700, exist_ok=True)
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.requests = 0
        self.network_requests = 0
        self.cache_hits = 0
        self.reuse_directory = reuse_directory

    def get(self, path, params):
        url = self.base_url + path + "?" + urllib.parse.urlencode(sorted(params.items()))
        identity = hashlib.sha256(url.encode()).hexdigest()
        body_path = self.directory / (identity + ".response.json")
        record_path = self.directory / (identity + ".request.json")
        if not record_path.exists() and self.reuse_directory:
            old_record = self.reuse_directory / record_path.name
            old_body = self.reuse_directory / body_path.name
            if old_record.exists():
                record = json.loads(old_record.read_text())
                if record["url"] != url or sha_file(old_body) != record["sha256"]:
                    raise ValueError("reused Meridian evidence mismatch")
                if record["status"] == 200:
                    body_path.write_bytes(old_body.read_bytes())
                    record_path.write_bytes(old_record.read_bytes())
        if record_path.exists():
            self.cache_hits += 1
            record = json.loads(record_path.read_text())
            if record["url"] != url or sha_file(body_path) != record["sha256"]:
                raise ValueError("cached Meridian evidence mismatch")
            status = record["status"]
            body = body_path.read_bytes()
        else:
            self.network_requests += 1
            try:
                with self.opener.open(url, timeout=30) as response:
                    status, body = response.status, response.read()
            except urllib.error.HTTPError as exc:
                status, body = exc.code, exc.read()
            body_path.write_bytes(body)
            record_path.write_text(json_text({
                "url": url, "status": status, "sha256": hashlib.sha256(body).hexdigest(),
                "captured_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
            }))
        self.requests += 1
        payload = json.loads(body)
        if status != 200 or payload.get("error") is not None or not isinstance(payload.get("data"), list):
            raise ValueError(f"Meridian query incomplete: HTTP {status}, {path}; raw response retained")
        return payload


def quantity_delta(row, instruments):
    side, qty, code = row["方向"], BROKER.quantity(row["成交数量"]), row["代码"]
    if side in ("买入", "托管转入", "转托转入"):
        return qty
    if side in ("卖出", "转托转出"):
        return -qty
    if side in ("ETF申购", "ETF赎回") and qty:
        kind = instruments[code]["instrument_type"]
        if kind not in ("stock", "etf"):
            raise ValueError("unknown ETF component/main instrument type")
        return qty * (1 if kind == "etf" else -1) * (1 if side == "ETF申购" else -1)
    if side not in ("ETF申购", "ETF赎回", "手工冻结", "红利", "股息税", "融券", "融券购回"):
        raise ValueError("unreviewed historical position direction")
    return 0


def quantity_history(settlements, dates, instruments):
    events = collections.defaultdict(list)
    corporate = []
    postings = collections.Counter()
    for row_number, row in sorted(settlements, key=lambda x: (x[1]["成交日期"], -x[0])):
        if row["成交日期"] >= START and row["方向"] != "手工冻结":
            if row["成交日期"] not in dates:
                raise ValueError("position event without funds day")
            events[row["成交日期"]].append((row_number, row))
    daily = {}
    audited = 0
    intermediate = []
    for day in dates:
        internal_transfers = collections.Counter()
        for row_number, row in events[day]:
            code, side = row["代码"], row["方向"]
            delta = quantity_delta(row, instruments)
            if not delta:
                continue
            audited += 1
            postings[code] += delta
            expected = int(row["股份余额"])
            if postings[code] != expected:
                raise ValueError(f"broker quantity balance mismatch: row {row_number}, {code}")
            if side in ("转托转入", "转托转出"):
                internal_transfers[code] += delta
            if postings[code] < 0:
                if side != "转托转出":
                    raise ValueError("negative non-transfer quantity")
                intermediate.append({"source_row": row_number, "trade_date": day, "security_id": code, "quantity": postings[code]})
            if side == "托管转入":
                corporate.append({"source_row": row_number, "posting_date": day, "security_id": code, "quantity_delta": delta})
        if any(internal_transfers.values()):
            raise ValueError("unresolved nonzero custody transfer requires an in-kind capital-flow review")
        if any(qty < 0 for qty in postings.values()):
            raise ValueError("negative closing quantity")
        daily[day] = {code: qty for code, qty in postings.items() if qty}
    return daily, corporate, audited, intermediate


def effective_quantities(posted, corporate, factors):
    # Posting balances can include split entitlements before Meridian's ex-date.
    result = {day: dict(positions) for day, positions in posted.items()}
    actions = []
    for event in corporate:
        matches = [factor for factor in factors if factor["security_id"] == event["security_id"]
                   and str(factor.get("announcement_date")) == event["posting_date"]]
        if len(matches) != 1:
            raise ValueError("custody entry lacks a unique Meridian corporate-action link")
        factor = matches[0]
        ex_date = BROKER.date(str(factor["ex_date"]))
        ratio = BROKER.number(str(factor["ex_factor"]))
        if ratio <= 1 or ex_date <= event["posting_date"]:
            raise ValueError("unreviewed corporate-action timing/ratio")
        code, delta = event["security_id"], event["quantity_delta"]
        for day, positions in result.items():
            if event["posting_date"] <= day < ex_date:
                positions[code] = positions.get(code, 0) - delta
                if positions[code] < 0:
                    raise ValueError("negative effective quantity before ex-date")
                if not positions[code]:
                    del positions[code]
        entitlement_base = result[event["posting_date"]].get(code, 0)
        if Decimal(entitlement_base) * (ratio - 1) != delta:
            raise ValueError("Meridian split ratio does not explain the actual credited shares")
        actions.append({**event, "effective_date": ex_date, "entitlement_base": entitlement_base, "meridian_factor": factor})
    return result, actions


def repo_history(settlements, dates):
    lots = collections.defaultdict(collections.deque)
    events = collections.defaultdict(list)
    for row_number, row in sorted(settlements, key=lambda x: (x[1]["成交日期"], -x[0])):
        if row["方向"] in ("融券", "融券购回"):
            events[row["成交日期"]].append((row_number, row))
    balances, principal, details = {}, 0, []
    for day in sorted(set(events) | set(dates)):
        for row_number, row in events[day]:
            key = (row["代码"], BROKER.quantity(row["成交数量"]))
            rate = BROKER.number(row["成交价格"])
            if row["方向"] == "融券":
                amount = BROKER.cents(row["成交金额"])
                BROKER.repo_quantity(key[0], key[1], amount)
                lots[key].append((day, row_number, amount, rate))
                principal += amount
            else:
                if not lots[key]:
                    raise ValueError("repo repayment lacks a matching principal placement")
                exact = [index for index, lot in enumerate(lots[key]) if lot[3] == rate]
                candidates = exact or list(range(len(lots[key])))
                source_day, source_row, amount, source_rate = lots[key][candidates[0]]
                del lots[key][candidates[0]]
                if source_day >= day:
                    raise ValueError("unreviewed same-day repo maturity")
                principal -= amount
                details.append({"repayment_date": day, "repayment_row": row_number, "placement_date": source_day,
                                "placement_row": source_row, "principal": BROKER.money(amount),
                                "link_scope": "principal_not_order_identity", "ambiguous_lot_identity": len(candidates) > 1,
                                "placement_rate": str(source_rate), "repayment_rate": str(rate),
                                "actual_interest": BROKER.money(BROKER.cents(row["实付金额"]) - amount)})
            if principal < 0:
                raise ValueError("negative repo receivable")
        if day in dates:
            balances[day] = principal
    return balances, details


def daily_bars(payload, security_id, start, end):
    rows = payload["data"]
    if len(rows) >= 1000 or payload.get("meta", {}).get("archive_complete") is not True:
        raise ValueError("unconfirmed or truncated Meridian archive")
    result = {}
    for row in rows:
        day = BROKER.date(str(row["trade_date"]))
        if row.get("security_id") != security_id or row.get("frequency") != "1d" or row.get("adjustment") != "none":
            raise ValueError("Meridian bar selector mismatch")
        if not start <= day <= end or day in result:
            raise ValueError("duplicate/out-of-range Meridian daily bar")
        close = BROKER.number(str(row["close"]))
        if close <= 0:
            raise ValueError("nonpositive historical close")
        result[day] = close
    return result


def date_windows(first, last):
    current = datetime.strptime(first, "%Y%m%d")
    end = datetime.strptime(last, "%Y%m%d")
    while current <= end:
        stop = min(current + timedelta(days=364), end)
        yield current.strftime("%Y%m%d"), stop.strftime("%Y%m%d")
        current = stop + timedelta(days=1)


def dividend_timing_reviews(dividends, factors, positions):
    adjustments = collections.Counter()
    reviews = []
    days = sorted(positions)
    for source_row, dividend in dividends:
        code, paid_day = dividend["代码"], dividend["成交日期"]
        entitled = BROKER.quantity(dividend["成交数量"])
        actual = BROKER.cents(dividend["实付金额"])
        candidates = collections.defaultdict(list)
        for factor in factors:
            if factor["security_id"] != code:
                continue
            ex_day = BROKER.date(str(factor["ex_date"]))
            if not START <= ex_day <= days[-1]:
                continue
            if ex_day > paid_day and str(factor.get("announcement_date")) != paid_day:
                continue
            prior = [day for day in days if day < ex_day]
            if prior and entitled > 0 and positions[prior[-1]].get(code, 0) == entitled:
                candidates[ex_day].append(factor)
        review = {"source_row": source_row, "security_id": code, "cash_posting_date": paid_day,
                  "entitled_quantity": entitled, "actual_cash": BROKER.money(actual),
                  "candidate_ex_dates": sorted(candidates), "economic_nav_publishable": False}
        if len(candidates) == 1 and actual > 0:
            ex_day = next(iter(candidates))
            review.update({"ex_date": ex_day, "status": "linked_entitlement_timing_preview", "meridian_factors": candidates[ex_day]})
            for day in days:
                if ex_day <= day < paid_day:
                    adjustments[day] += actual
                elif paid_day <= day < ex_day:
                    adjustments[day] -= actual
        else:
            review["status"] = "unresolved_entitlement_or_ex_date"
        reviews.append(review)
    return reviews, adjustments


def compare_oc_positions(rebuilt, evidence):
    if evidence.get("account_id") != BROKER.ACCOUNT:
        raise ValueError("OC position evidence account mismatch")
    snapshots = collections.defaultdict(dict)
    for row in evidence.get("positions") or []:
        day, code = row["trade_date"].replace("-", ""), row["security_id"]
        if code in snapshots[day] or row["quantity"] <= 0:
            raise ValueError("duplicate/nonpositive OC comparison row")
        snapshots[day][code] = row["quantity"]
    mismatches = []
    comparable = sorted(set(rebuilt) & set(snapshots))
    equal = 0
    for day in comparable:
        if rebuilt[day] == snapshots[day]:
            equal += 1
        for code in sorted(set(rebuilt[day]) | set(snapshots[day])):
            if rebuilt[day].get(code, 0) != snapshots[day].get(code, 0):
                mismatches.append({"trade_date": day, "security_id": code,
                                   "broker_rebuilt_quantity": rebuilt[day].get(code, 0),
                                   "oc_close_quantity": snapshots[day].get(code, 0)})
    return {"comparison_scope": "positive_snapshot_days_not_page_completion_proof", "days_compared": len(comparable),
            "equal_days": equal, "mismatch_days": len({row["trade_date"] for row in mismatches}), "mismatches": mismatches}


def actual_etf_cash_accrual(cash_rows, dates):
    # Retrospective actual cash is not information available on the source day.
    changes = collections.Counter()
    origin_rows = 0
    unlinked = 0
    for row in cash_rows:
        if not row["etf_related"]:
            continue
        posted = row["raw_fields"]["交易日期"]
        origin = row["source_trade_date"]
        if not origin:
            unlinked += 1
            continue
        if origin > posted:
            raise ValueError("ETF source date after actual cash posting")
        amount = BROKER.cents(row["actual_amount"])
        changes[origin] += amount
        changes[posted] -= amount
        origin_rows += 1
    balance = 0
    result = {}
    checkpoints = iter(sorted(changes.items()))
    pending = next(checkpoints, None)
    for day in dates:
        while pending is not None and pending[0] <= day:
            balance += pending[1]
            pending = next(checkpoints, None)
        result[day] = balance
    return result, {"explicit_origin_rows": origin_rows, "unlinked_origin_rows": unlinked,
                    "scope": "retrospective_actual_posted_cash_only_not_final_clearing_completeness"}


def run(args):
    report = verify_evidence(args.evidence)
    if args.confirmed_start != START or BROKER.cents(args.opening_asset) != OPENING_CENTS or not args.confirmed_by.strip():
        raise ValueError("requires the explicitly approved handover date, amount and operator")
    identity = {"source_report_sha256": sha_file(args.evidence / "report.json"), "handover_date": START,
                "opening_asset": BROKER.money(OPENING_CENTS), "confirmed_by": args.confirmed_by,
                "meridian_base_url": args.meridian_base_url.rstrip("/")}
    approval = args.output / "handover.json"
    if args.resume:
        if not (args.output / "INCOMPLETE").exists() or json.loads(approval.read_text()) != identity:
            raise ValueError("resume requires the same incomplete recovery and confirmation")
    else:
        args.output.mkdir(mode=0o700, exist_ok=False)
        (args.output / "INCOMPLETE").write_text("Reconstruction incomplete; not a published performance series.\n")
        approval.write_text(json_text(identity))
    meridian = MeridianEvidence(args.meridian_base_url, args.output / "meridian", args.reuse_market_cache)
    settlements = list(BROKER.rows(args.evidence / report["files"]["settlements"]["snapshot"], "settlements"))
    dates = sorted(row["trade_date"] for row in report["book_days_not_economic_nav"] if row["trade_date"] >= START)
    codes = sorted({row["代码"] for _, row in settlements if row["方向"] in ("ETF申购", "ETF赎回") and BROKER.quantity(row["成交数量"])})
    instruments = {}
    for start in range(0, len(codes), 100):
        batch = codes[start:start + 100]
        payload = meridian.get("/v1/metadata/instruments", {"security_ids": ",".join(batch), "limit": 1000})
        if len(payload["data"]) >= 1000:
            raise ValueError("truncated Meridian instruments")
        for row in payload["data"]:
            code = row["security_id"]
            if code not in batch or code in instruments:
                raise ValueError("duplicate/unrequested instrument")
            instruments[code] = row
    if set(instruments) != set(codes):
        raise ValueError("incomplete instrument coverage")
    posted, corporate, checked, intermediate = quantity_history(settlements, dates, instruments)
    corporate_codes = sorted({event["security_id"] for event in corporate})
    factors = meridian.get("/v1/metadata/adjust-factors", {
        "security_ids": ",".join(corporate_codes), "start_date": START, "end_date": dates[-1], "limit": 1000,
    })["data"] if corporate_codes else []
    if len(factors) >= 1000:
        raise ValueError("truncated corporate-action query")
    effective, actions = effective_quantities(posted, corporate, factors)
    repos, repayments = repo_history(settlements, dates)
    dividends = [(number, row) for number, row in settlements if row["方向"] == "红利" and row["成交日期"] >= START]
    expected_dividends = collections.Counter((row["成交日期"], row["代码"].split(".")[0], BROKER.cents(row["实付金额"])) for _, row in dividends)
    actual_dividends = collections.Counter()
    for _, row in BROKER.rows(args.evidence / report["files"]["cash_flows"]["snapshot"], "cash_flows"):
        if row["业务科目"] == "股票红利划入" and row["交易日期"] >= START:
            actual_dividends[(row["交易日期"], row["股票代码"], BROKER.cents(row["入金"]) - BROKER.cents(row["出金"]))] += 1
    if expected_dividends != actual_dividends:
        raise ValueError("dividend deliveries do not match actual posted cash")
    dividend_codes = sorted({row["代码"] for _, row in dividends})
    dividend_factors = meridian.get("/v1/metadata/adjust-factors", {
        "security_ids": ",".join(dividend_codes), "start_date": START, "end_date": dates[-1], "limit": 1000,
    })["data"] if dividend_codes else []
    if len(dividend_factors) >= 1000:
        raise ValueError("truncated dividend factors")
    dividend_reviews, dividend_adjustments = dividend_timing_reviews(dividends, dividend_factors, effective)
    with gzip.open(args.evidence / "cash_evidence.jsonl.gz", "rt", encoding="utf-8") as source:
        etf_accrual, etf_accrual_scope = actual_etf_cash_accrual(map(json.loads, source), dates)
    needs = collections.defaultdict(list)
    for day, positions in effective.items():
        for code in positions:
            needs[code].append(day)
    prices, price_errors = {}, {}
    for index, (code, needed_days) in enumerate(sorted(needs.items()), 1):
        first, last = min(needed_days), max(needed_days)
        try:
            prices[code] = {}
            for start, end in date_windows(first, last):
                payload = meridian.get("/v1/market/bars", {
                    "security_id": code, "frequency": "1d", "adjustment": "none",
                    "start_date": start, "end_date": end, "limit": 1000,
                })
                prices[code].update(daily_bars(payload, code, start, end))
        except (ValueError, OSError) as exc:
            price_errors[code] = str(exc)
        if index % 10 == 0 or index == len(needs):
            print(f"historical close evidence: {index}/{len(needs)} securities", flush=True)
    book = {row["trade_date"]: row for row in report["book_days_not_economic_nav"]}
    outputs = []
    gold = []
    previous_asset = OPENING_CENTS
    previous_partial_asset = OPENING_CENTS
    pure_cash_prefix = True
    cumulative = Decimal(1)
    for day in dates:
        row = book[day]
        missing = [code for code in effective[day] if day not in prices.get(code, {})]
        market = sum((Decimal(qty) * prices[code][day] for code, qty in effective[day].items() if day in prices.get(code, {})), Decimal(0))
        market_cents = int((market * 100).to_integral_value()) if not missing else None
        cash = BROKER.cents(row["cash"])
        base = cash + market_cents + repos[day] if market_cents is not None else None
        partial_asset = base + dividend_adjustments[day] + etf_accrual[day] if base is not None else None
        partial_pnl = (partial_asset - previous_partial_asset - BROKER.cents(row["deposit"]) + BROKER.cents(row["withdrawal"])) if partial_asset is not None and previous_partial_asset is not None else None
        previous_partial_asset = partial_asset
        pure_cash_prefix = pure_cash_prefix and day < report["first_etf_execution_date"] and not effective[day]
        if pure_cash_prefix:
            pnl = base - previous_asset - BROKER.cents(row["deposit"]) + BROKER.cents(row["withdrawal"])
            if base != BROKER.cents(row["total"]) or pnl != BROKER.cents(row["pnl"]) or row["deposit"] != "0.00" or row["withdrawal"] != "0.00":
                raise ValueError("cash/repo-only prefix fails the approved asset/return bridge")
            cumulative *= Decimal(base) / Decimal(previous_asset)
            gold.append({"trade_date": day, "open_asset_excluding_fund_occupancy": BROKER.money(previous_asset),
                         "close_asset_excluding_fund_occupancy": BROKER.money(base), "daily_pnl": BROKER.money(pnl)})
            previous_asset = base
        outputs.append({
            "trade_date": day, "posted_positions": posted[day], "effective_positions": effective[day],
            "cash": row["cash"], "repo_principal": BROKER.money(repos[day]),
            "meridian_market_value": BROKER.money(market_cents) if market_cents is not None else None,
            "broker_market_value": row["market_value"],
            "market_value_difference": BROKER.money(market_cents - BROKER.cents(row["market_value"])) if market_cents is not None else None,
            "visible_asset_excluding_unverified_etf_claims": BROKER.money(base) if base is not None else None,
            "dividend_timing_asset_adjustment_preview": BROKER.money(dividend_adjustments[day]),
            "visible_asset_with_dividend_timing_preview": BROKER.money(base + dividend_adjustments[day]) if base is not None else None,
            "known_actual_etf_cash_accrual": BROKER.money(etf_accrual[day]),
            "partial_economic_asset_preview": BROKER.money(partial_asset) if partial_asset is not None else None,
            "partial_daily_pnl_preview": BROKER.money(partial_pnl) if partial_pnl is not None else None,
            "broker_total_minus_partial_asset": BROKER.money(BROKER.cents(row["total"]) - partial_asset) if partial_asset is not None else None,
            "broker_reported_total": row["total"], "missing_prices": missing,
            "economic_nav_publishable": False, "etf_clearing_review_required": not pure_cash_prefix,
        })
    with gzip.open(args.output / "daily_positions_valuations.jsonl.gz", "wt", encoding="utf-8", compresslevel=3) as target:
        for row in outputs:
            BROKER.dump_line(target, row)
    with (args.output / "cash_repo_gold.csv").open("w", encoding="utf-8", newline="") as target:
        writer = csv.DictWriter(target, fieldnames=["trade_date", "open_asset_excluding_fund_occupancy", "close_asset_excluding_fund_occupancy", "daily_pnl"])
        writer.writeheader()
        writer.writerows(gold)
    result = {
        **identity, "account_id": BROKER.ACCOUNT, "generated_at": datetime.now(ZoneInfo("Asia/Shanghai")).isoformat(),
        "source_evidence": str(args.evidence), "database_writes": 0, "published_nav_rows": 0,
        "quantity_days": len(dates), "source_quantity_events_verified": checked, "quantity_balance_mismatches": 0,
        "intermediate_internal_transfer_balances": intermediate, "corporate_actions": actions,
        "repo_repayments": repayments, "valuation_security_count": len(needs), "meridian_queries": meridian.requests,
        "meridian_network_requests": meridian.network_requests, "meridian_cache_hits": meridian.cache_hits,
        "price_errors": price_errors, "missing_price_days": sum(bool(row["missing_prices"]) for row in outputs),
        "book_market_value_mismatch_days": sum(row["market_value_difference"] not in (None, "0.00") for row in outputs),
        "cash_repo_verified_prefix": gold, "prefix_cumulative_nav_preview": str(cumulative),
        "dividend_timing_reviews": dividend_reviews,
        "etf_actual_cash_accrual_scope": etf_accrual_scope,
        "existing_cost_inception_unchanged": "2026-09-09", "unresolved_etf_claims_are_not_zero": True,
        "artifact_sha256": {name: sha_file(args.output / name) for name in ["daily_positions_valuations.jsonl.gz", "cash_repo_gold.csv", "handover.json"]},
    }
    if args.oc_close_evidence:
        oc_data = json.loads(args.oc_close_evidence.read_text())
        result["oc_close_comparison"] = compare_oc_positions(effective, oc_data)
        oc_copy = args.output / "oc_close_positions.json"
        oc_copy.write_bytes(args.oc_close_evidence.read_bytes())
        result["artifact_sha256"][oc_copy.name] = sha_file(oc_copy)
    (args.output / "report.json").write_text(json_text(result))
    (args.output / "INCOMPLETE").unlink()
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--confirmed-start", required=True)
    parser.add_argument("--opening-asset", required=True)
    parser.add_argument("--confirmed-by", required=True)
    parser.add_argument("--meridian-base-url", default="http://meridian-data.quantstage.com")
    parser.add_argument("--reuse-market-cache", type=Path, help="verified prior GET evidence; copied into this recovery")
    parser.add_argument("--oc-close-evidence", type=Path, help="optional read-only PostgreSQL close-position export")
    parser.add_argument("--resume", action="store_true")
    result = run(parser.parse_args())
    print(json_text({key: value for key, value in result.items() if key not in (
        "repo_repayments", "corporate_actions", "dividend_timing_reviews", "intermediate_internal_transfer_balances", "oc_close_comparison",
    )}))
