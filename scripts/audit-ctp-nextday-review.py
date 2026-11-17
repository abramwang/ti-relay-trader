#!/usr/bin/env python3
"""Verify private OC next-day offline evidence; never connect or publish."""

import argparse
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


def audit(directory, original):
    source = module("source_review", "audit-ctp-fill-v2-review.py")
    builder = module("query_review", "build-ctp-query-review.py")
    require, digest, canonical = source.require, source.digest, source.canonical
    expected = {"OC_RELAY_CTP_NEXTDAY_DELIVERY_20261118.md", "audit-export-review.json", "ctest.txt",
                "nextday-recorded.json", "nextday-recorded.log", "offline-preflight.json",
                "sdk-fingerprints.json", "verification.json", "version.txt"}
    checked = {}
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        sha, name = line.split(maxsplit=1)
        require(name in expected and name not in checked and not (directory / name).is_symlink(), "manifest member")
        checked[name] = digest((directory / name).read_bytes())
        require(checked[name] == sha, "file digest")
    require(set(checked) == expected, "manifest coverage")
    require(checked["nextday-recorded.json"] == "f7c6e2dd56b7d13fec08a0e071f8169329290bd48f22bda4a2287138fc1b9ed6", "delivery pin")
    doc = json.loads((directory / "nextday-recorded.json").read_bytes())
    require(doc["real_sdk"] is False and doc["real_redis"] is False and doc["source_is_recorded"] is True
            and doc["simulation_trade_date"] == "20261118" and doc["source_files_unchanged"] is True, "offline boundary")
    state = original / "original-exit" / "state"
    manifest = doc["source_manifest"]
    for key, filename in (("journal", "commands.v1.json"), ("identity", "identities.v1.json"), ("audit", "readonly.audit.jsonl")):
        sha = digest((state / filename).read_bytes())
        require(sha == doc[f"source_{key}_sha256"] == manifest["source_files"][f"{key}_sha256"], "original source")
    journal = source.wrapped(state / "commands.v1.json")
    require(set(manifest["callbacks"]) == set(journal["callbacks"]) and len(manifest["callbacks"]) == 50, "callback coverage")
    for key, item in manifest["callbacks"].items():
        old = journal["callbacks"][key]
        require(item["original_callback_id"] == key and item["original_evidence_sha256"] == old["sha256"]
                and item["original_outcome"] == old["outcome"], "callback identity/outcome")
        require(item["business_resolved"] is False and item["publishable"] is False, "old responsibility released")
        source.check_selected(old["evidence"], item["selected_raw"])
    manifest_sha = digest(canonical(manifest))
    failures, drafts, summaries = [], 0, []
    def values(collection):
        require(isinstance(collection, (dict, list)) or collection is None, "journal collection")
        return collection.values() if isinstance(collection, dict) else (collection or [])
    for case in doc["cases"]:
        audit_bytes = case["audit_prefix"].encode()
        records, previous = [], ""
        for sequence, line in enumerate(audit_bytes.splitlines(), 1):
            wrapper = json.loads(line)
            record = wrapper["record"]
            require(record["sequence"] == sequence and record["previous"] == previous
                    and digest(canonical(record)) == wrapper["sha256"], "audit chain")
            previous = wrapper["sha256"]
            records.append(record)
        wrapped = json.loads(case["journal_raw"])
        require(wrapped["state"] == case["journal"] and digest(canonical(wrapped["state"])) == wrapped["sha256"], "journal bytes")
        outputs = []
        for r in values(case["journal"]["records"]):
            outputs.extend((r.get("result") or {}).get("outputs", []))
        outputs.extend(e["output"] for e in values(case["journal"].get("events")))
        by_id = {o["message_id"]: o for o in outputs}
        for output in case["outputs"]:
            stored = by_id[output["message_id"]]
            require(digest(stored["body"].encode()) == stored["sha256"] and json.loads(stored["body"]) == output, "outbox original")
        for hb in case["heartbeats"]:
            legacy = hb["adapter_context"]["admission_evidence"]["legacy_source"]
            require(legacy["manifest_sha256"] == manifest_sha and legacy["source_scope"] == manifest["source_scope"], "heartbeat source")
            d = legacy["diagnostics"]
            require((d["source_callback_count"], d["original_pending_count"], d["original_audit_only_count"]) == (50, 32, 18)
                    and d["business_processed_count"] == 0 and not d["old_outcomes_mutated"]
                    and not d["business_publication_authorized"], "legacy accounting")
        for index, item in enumerate(case["inputs"]):
            cmd = json.loads(item["body"])
            results = [r["entry"] for r in records if r["entry"].get("kind") == "query_result"
                       and r["entry"].get("origin_message_id") == cmd["message_id"]]
            if not results:
                continue
            result = results[0]
            target = dict(source_id="offline-only", server_run_id="0" * 40, epoch="offline-only", group="offline-group",
                          counter_session_id=result["counter_session_id"], trade_date="20261118", connection_generation=1)
            options = SimpleNamespace(audit_sha256=digest(audit_bytes), account="300052141", broker="ctp_4900",
                                      gateway="ctp-dongwu-readonly-recovery-20261117", native_broker="4900", user="300052141",
                                      investor="300052141", command_id=index+1, timeout_ms=60000,
                                      batch_id="offline-only", review_id="offline-only")
            try:
                builder.build(audit_bytes, item["body"].encode(), target, options)
                drafts += 1
            except ValueError as exc:
                failures.append(dict(scenario=case["scenario"], origin=cmd["message_id"], reason=str(exc)))
        summaries.append(dict(scenario=case["scenario"], audit_records=len(records), sdk_submit=case["sdk_submit"],
                              sdk_cancel=case["sdk_cancel"], exit_code=case["exit_code"], outputs=len(case["outputs"])))
    return dict(status="offline_material_verified", live_acceptance=False, manifest_files=len(checked),
                source_callbacks=50, legacy_pending=32, source_manifest_sha256=manifest_sha, scenarios=summaries,
                query_review_drafts=drafts, query_review_builder_failures=failures,
                query_review_ready=not failures, no_network_or_runtime_writes=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("original", type=Path)
    args = parser.parse_args()
    print(json.dumps(audit(args.directory, args.original), ensure_ascii=False, indent=2))
