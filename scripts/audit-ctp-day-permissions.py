#!/usr/bin/env python3
"""Audit OC offline lifecycle/day-permission evidence without network or state writes."""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace


def require(condition, reason):
    if not condition:
        raise ValueError(reason)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":")).encode()


def values(collection):
    return collection.values() if isinstance(collection, dict) else (collection or [])


def audit(directory):
    spec = importlib.util.spec_from_file_location("builder", Path(__file__).with_name("build-ctp-query-review.py"))
    builder = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(builder)
    expected = {"README.txt", "VERSIONS.txt", "BUILD_ARTIFACT_SHA256", "OC_RELAY_CTP_NEXTDAY_DELIVERY_20261118.md", "final-ctest.log", "day-permissions.json", "day-permissions.log", "scheduled-process.json", "scheduled-process.log"}
    checked = {}
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        sha, name = line.split(maxsplit=1)
        require(name in expected and name not in checked and not (directory / name).is_symlink(), "manifest membership")
        require(digest((directory / name).read_bytes()) == sha, "manifest digest")
        checked[name] = sha
    require(set(checked) == expected, "manifest coverage")
    pins = {"day-permissions.json": "a5d2fd5a05e1f1293d636dcfd4070b5f60701a72b5f17ed9fee2559be7ff035a", "scheduled-process.json": "a730ce598b59ab8f5b2cb04c99a34b40257fc9027ec9b25dc1656270107f318a"}
    summaries, drafts = [], 0
    for filename, pin in pins.items():
        require(checked[filename] == pin, "delivery identity")
        doc = json.loads((directory / filename).read_bytes())
        require(doc["real_sdk"] is False and doc["real_redis"] is False, "offline boundary")
        require(len(doc["cases"]) == (6 if filename == "day-permissions.json" else 3), "case coverage")
        for case in doc["cases"]:
            previous, entries = "", []
            raw_audit = case["audit_prefix"].encode()
            for sequence, line in enumerate(raw_audit.splitlines(), 1):
                wrapper = json.loads(line)
                record = wrapper["record"]
                require(record["sequence"] == sequence and record["previous"] == previous and digest(canonical(record)) == wrapper["sha256"], "audit chain")
                previous = wrapper["sha256"]
                entries.append(record["entry"])
            wrapper = json.loads(case["journal_raw"])
            journal = wrapper["state"]
            require(digest(canonical(journal)) == wrapper["sha256"], "journal digest")
            received = [r for r in entries if r.get("kind") == "command_received"]
            require(len(received) == len(case["inputs"]), "input audit coverage")
            for item, record in zip(case["inputs"], received):
                require(all(item[k] == record[v] for k, v in (("body", "body"), ("entry_id", "stream_id"), ("stream", "stream"), ("group", "group"))), "command receipt bytes")
                command = json.loads(item["body"])
                results = [r for r in entries if r.get("kind") == "query_result" and r.get("origin_message_id") == command["message_id"]]
                if results:
                    result = results[0]
                    route = case["heartbeats"][0]["routing"]
                    target = dict(source_id="offline-only", server_run_id="0" * 40, epoch="offline-only", group=item["group"], counter_session_id=result["counter_session_id"], trade_date=result["trade_date"], connection_generation=result["connection_generation"])
                    options = SimpleNamespace(audit_sha256=digest(raw_audit), account=route["account_id"], broker=route["broker_id"], gateway=route["gateway_id"], native_broker="4900", user="300052141", investor="300052141", command_id=drafts+1, timeout_ms=60000, batch_id="offline-only", review_id="offline-only")
                    builder.build(raw_audit, item["body"].encode(), target, options)
                    drafts += 1
            outputs = []
            for record in values(journal["records"]):
                outputs.extend((record.get("result") or {}).get("outputs", []))
            outputs.extend(e["output"] for e in values(journal.get("events")))
            by_id = {o["message_id"]: o for o in outputs}
            for output in case["outputs"]:
                saved = by_id[output["message_id"]]
                require(digest(saved["body"].encode()) == saved["sha256"] and json.loads(saved["body"]) == output, "outbox original body")
            if case["scenario"] == "multi_contract_multi_quantity":
                require(case["sdk_submit"] == case["sdk_cancel"] == 2, "duplicate SDK execution")
                require([s["qty"] for s in case["native_submits"]] == [2, 3], "native quantities")
                require([s["symbol"] for s in case["native_submits"]] == ["rb2701", "cu2701"], "native contracts")
                for submit, cancel in zip(case["native_submits"], case["native_cancels"]):
                    require(all(submit[k] == cancel[k] for k in ("order_ref", "order_sys_id", "symbol")), "cancel target mismatch")
            if case["scenario"] in ("quantity_above_native_limit", "price_out_of_range", "unavailable_contract", "wrong_exchange", "early_login_only", "no_settlement_authority"):
                require(case["sdk_submit"] == case["sdk_cancel"] == 0, "forbidden SDK action")
            if case["scenario"] == "cancel_unknown":
                require(case["exit_code"] == 4 and case["sdk_cancel"] == 1, "unknown cancel retried")
            summaries.append(dict(file=filename, scenario=case["scenario"], heartbeats=len(case["heartbeats"]), audit_records=len(entries), outputs=len(case["outputs"]), sdk_submit=case["sdk_submit"], sdk_cancel=case["sdk_cancel"], exit_code=case["exit_code"]))
    return dict(status="offline_material_verified", live_acceptance=False, manifest_files=len(checked), query_review_drafts=drafts, scenarios=summaries)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    print(json.dumps(audit(args.directory), indent=2, ensure_ascii=False))
