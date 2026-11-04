"""Optional cross-project, in-memory validator review. Never collect or publish data.

Run with Meridian's Python environment and --meridian-root. A nonzero exit means
an observed validator decision differs from the strict type/completeness contract.
The synthesized rows and mocked source hash are not broker or publication evidence.
"""

import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
from unittest.mock import patch


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--meridian-root", required=True, type=Path)
    args = parser.parse_args()
    root = args.meridian_root.resolve()
    sys.dont_write_bytecode = True
    implementation = root / "scripts/build_futures_instrument_catalog.py"
    spec = importlib.util.spec_from_file_location("meridian_review", implementation)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    read = module.read_json
    contract = read(root / "configs/contracts/ctp-futures-level1-v1.json")
    source = root / "configs/fixtures/ctp-futures-instrument-query-shape-v2.json"
    shape = read(source)
    shadow = read(root / contract["shfe_alignment"]["official_directory_shadow_path"])
    base = copy.deepcopy(shape)
    base.update(status="ready", request_scope="exchange_full",
                provider="synthetic_in_memory_review_never_publish")
    base.pop("production_eligible", None)
    base.pop("fixture_origin", None)
    rows = []
    for official in shadow["instruments"]:
        row = copy.deepcopy(shape["instruments"][0])
        row.update(instrument_id=official["source_instrument_id"],
                   exchange_instrument_id=official["source_instrument_id"],
                   product_id=official["product_id"].lower(),
                   delivery_year=official["contract_month"] // 100,
                   delivery_month=official["contract_month"] % 100)
        for key in ("volume_multiple", "price_tick", "open_date", "expire_date",
                    "start_delivery_date", "end_delivery_date"):
            row[key] = official[key]
        rows.append(row)
    base["instruments"] = rows
    base["row_count"] = len(rows)
    base["query_completions"][0]["row_count"] = len(rows)

    results = []
    cases = ("synthetic_control", "fractional_order_bound", "boolean_order_bound",
             "fractional_row_count", "boolean_request_id", "string_is_trading",
             "partial_scope", "missing_last", "duplicate_identity")
    for name in cases:
        payload = copy.deepcopy(base)
        if name == "fractional_order_bound":
            payload["instruments"][0]["min_limit_order_volume"] = 1.9
        elif name == "boolean_order_bound":
            payload["instruments"][0]["min_limit_order_volume"] = True
        elif name == "fractional_row_count":
            payload["row_count"] = len(rows) + 0.9
        elif name == "boolean_request_id":
            payload["query_completions"][0]["request_id"] = True
        elif name == "string_is_trading":
            payload["instruments"][0]["is_trading"] = "false"
        elif name == "partial_scope":
            payload["request_scope"] = "single_contract"
        elif name == "missing_last":
            payload["query_completions"][0]["is_last_received"] = False
        elif name == "duplicate_identity":
            payload["instruments"][1] = copy.deepcopy(payload["instruments"][0])
        result = {"case": name, "expected_accepted": name == "synthetic_control"}
        try:
            # Only replace input loading; run the real validator and Shadow checks.
            with patch.object(module, "read_json", side_effect=lambda path:
                              payload if path == source else read(path)):
                normalized, _ = module.validate_ctp_query_snapshot(root, contract, source)
            result.update(accepted=True,
                          first_min_limit=normalized[0]["min_limit_order_volume"],
                          first_trading_status=normalized[0]["trading_status"])
        except (ValueError, RuntimeError, TypeError) as exc:
            result.update(accepted=False, error=str(exc))
        result["passed"] = result["accepted"] == result["expected_accepted"]
        results.append(result)
    failed = sum(not result["passed"] for result in results)
    print(json.dumps({"synthetic_only": True, "published": False,
                      "validator_sha256": hashlib.sha256(implementation.read_bytes()).hexdigest(),
                      "cases": results, "failed": failed}, ensure_ascii=False, indent=2))
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
