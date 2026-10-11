#!/usr/bin/env python3
"""GET-only acceptance of an approved fee profile through the public SDK."""

import argparse
from datetime import date, timedelta
import hashlib
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "sdk" / "python"))

from relay_sdk import RelayClient, RelayPaginationError  # noqa: E402


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:9092")
    parser.add_argument("--profile", type=Path, default=ROOT / "testdata/performance/huaxin_fee_budget_20261011.json")
    args = parser.parse_args()
    content = args.profile.read_bytes()
    profile = json.loads(content)
    digest = hashlib.sha256(content).hexdigest()
    effective_day = date.fromisoformat(profile["effective_from"])
    result = []
    for account in profile["account_ids"]:
        client = RelayClient(args.base_url, account_id=account)
        page = client.get_fee_rules(effective_on=profile["effective_from"])
        assert page.is_complete is True and page.count == 8
        assert page.query["account_id"] == account
        assert page.query["effective_on"] == profile["effective_from"]
        assert page.request_id and page.time
        scopes = {(r.market, r.instrument_type, r.business_type, r.trade_side): r for r in page.rules}
        assert len(scopes) == 8
        for template in profile["templates"]:
            key = tuple(template[x] for x in ("market", "instrument_type", "business_type", "trade_side"))
            rule = scopes[key]
            assert rule.account_id == account and rule.status == "active"
            assert rule.effective_from == profile["effective_from"]
            assert rule.raw_payload["publication_sha256"] == digest
            assert rule.raw_payload["authority"] == "sample_verified_budget"
            assert rule.raw_payload["not_broker_settlement_rule"] is True
            for field in ("commission_rate", "minimum_commission", "stamp_duty_rate", "transfer_fee_rate", "handling_fee_rate", "other_rate", "fixed_fee"):
                assert getattr(rule, field) == template[field], (account, field)
        assert client.get_fee_rules(effective_on=(effective_day - timedelta(days=1)).isoformat()).count == 0
        assert client.get_fee_rules(effective_on=(effective_day + timedelta(days=1)).isoformat()).count == 8
        try:
            client.get_fee_rules(effective_on=profile["effective_from"], limit=7)
        except RelayPaginationError as exc:
            assert exc.code == "FEE_RULE_LIST_INCOMPLETE"
        else:
            raise AssertionError("truncated rules did not fail closed")
        result.append({"account_id": account, "count": page.count, "is_complete": page.is_complete, "versions": sorted(r.version for r in page.rules), "request_id": page.request_id})
    client = RelayClient(args.base_url)
    total = client.get_fee_rules(effective_on=profile["effective_from"])
    assert total.count == 32 and total.is_complete
    for account in ("501000114077", "307000051389", "unknown-account"):
        assert client.get_fee_rules(account_id=account, effective_on=profile["effective_from"]).count == 0
    print(json.dumps({"publication_id": profile["publication_id"], "total": total.count, "accounts": result, "excluded_and_historical_scope": "passed", "truncation_gate": "passed"}, ensure_ascii=False))


if __name__ == "__main__":
    main()
