#!/usr/bin/env python3
"""Read-only comparison of OC v4 offline output with original recorded evidence."""

import argparse
import importlib.util
import json
from pathlib import Path


def audit(directory, original):
    spec = importlib.util.spec_from_file_location(
        "fill_review", Path(__file__).with_name("audit-ctp-fill-v2-review.py"))
    source = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(source)
    require, digest = source.require, source.digest
    original_result = source.audit(original)
    expected = {"README.md", "recorded-flow-20261117-v4.json",
                "recorded-flow-20261117-v4.log", "postclose-ctest-20261117.txt"}
    checked = {}
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        sha, name = line.split(maxsplit=1)
        require(name in expected and name not in checked, "manifest member")
        require(not (directory / name).is_symlink(), "symlink evidence")
        checked[name] = digest((directory / name).read_bytes())
        require(checked[name] == sha, "manifest digest")
    require(set(checked) == expected, "manifest coverage")
    require(checked["recorded-flow-20261117-v4.json"] ==
            "26a57a0b701798a6ba3503b5b472888815aa4993939a017d219cbcb5932be665", "delivery pin")
    doc = json.loads((directory / "recorded-flow-20261117-v4.json").read_bytes())
    require(doc["real_broker"] is False and doc["real_redis"] is False and
            doc["new_commands_sdk_and_query_completion_are_simulated"] is True and
            doc["recorded_callbacks_are_real"] is True, "evidence boundary")
    state = original / "original-exit" / "state"
    for name, filename in (("journal", "commands.v1.json"), ("identity", "identities.v1.json")):
        require(doc[f"source_{name}_sha256"] == digest((state / filename).read_bytes()), "source pin")
    journal = source.wrapped(state / "commands.v1.json")
    review = json.loads((original / "fill-v2-review.json").read_bytes())
    pending = {key: value for key, value in journal["callbacks"].items() if value["outcome"] == "pending"}
    cases = doc["cases"]
    require([c["scenario"] for c in cases] == ["recorded_replay_local_submit_cancel", "deferred_replay_ack_uncertain"], "scenarios")
    heartbeats = []
    for index, case in enumerate(cases):
        migration = case["migration"]
        require(all(value == review[key] for key, value in migration["review"].items()), "derived rows changed")
        require(set(migration["review"]["rows"]) == set(journal["callbacks"]), "50 callback coverage")
        require(set(migration["deferred_callbacks"]) == set(pending), "deferral coverage")
        for key, item in migration["deferred_callbacks"].items():
            require(item == {"business_processed": False, "policy": "local_assessment_only.v1",
                             "reason": "unregistered_native_identity", "source_sha256": pending[key]["sha256"]}, "deferral evidence changed")
        require(migration["pending_callbacks"] == 32 and migration["blocking_callbacks"] == 0 and
                all(migration[k] == 0 for k in ("redis_writes", "sdk_calls", "business_outputs")), "migration side effects")
        require(case["original_callback_bytes_preserved"] is True, "missing preservation assertion")
        require(case["pending_callbacks"] == case["deferred_callbacks"] == (64 if index == 0 else 32) and
                case["blocking_callbacks"] == 0, "runtime callback count")
        require(all(case[k] == (1 if index == 0 else 0) for k in ("sdk_create", "sdk_settlement", "sdk_submit", "sdk_cancel")), "SDK budget")
        require(case["exit_code"] == (0 if index == 0 else 4), "exit result")
        heartbeats.extend(case["heartbeats"])
    outputs = cases[0]["outputs"]
    require(len(outputs) == 6 and cases[1]["outputs"] == [], "output budget")
    require([o["action"] for o in outputs] == ["order.list.query", "account.positions.query", "order.submit", "order.submit", "order.cancel", "order.submit"], "output actions")
    events = [o for o in outputs if o["message_type"] == "event"]
    require([o["payload"]["gateway_status"] for o in events] == ["working", "cancelled"], "local lifecycle")
    require(all(o["event_type"] == "order.event" and o["origin_message_id"] == "offline-cmd.trade-113" for o in events), "external output leaked")
    require(events[0]["payload"]["gateway_order_id"] == events[1]["payload"]["gateway_order_id"], "cancel target changed")
    for position, action, origin in ((2, "order.submit", "offline-cmd.trade-113"), (4, "order.cancel", "offline-cmd.trade-117")):
        receipt = outputs[position]
        require(receipt["message_type"] == "reply" and receipt["status"] == "accepted" and
                receipt["result_type"] == "order_action_receipt" and receipt["action"] == action and
                receipt["origin_message_id"] == origin and receipt["routing"] == events[0]["routing"], "receipt association")
        for key in ("account_id", "gateway_order_id", "counter_session_id", "trade_date", "product_type", "schema_version"):
            require(receipt["payload"][key] == events[0]["payload"][key], "receipt scope")
    require(outputs[4]["payload"]["cancel_id"] == "fixture-cancel", "cancel receipt identity")
    log_rows = [json.loads(line) for line in (directory / "recorded-flow-20261117-v4.log").read_text().splitlines() if line.startswith("{")]
    require(len(heartbeats) == len(log_rows) == 17, "heartbeat/log count")
    for body, row in zip(heartbeats, log_rows):
        adapter = body["adapter_context"]
        require(row["counter_session_id"] == body["counter_session_id"] and
                row["state_text"] == body["state_text"] and
                row["trade_date"] == (body["payload"]["trade_date"] or "") and
                row["connection_generation"] == (adapter["connection_generation"] or 0) and
                row["expires_at"] == adapter["admission_evidence"]["expires_at"], "log summary mismatch")
    return {"status": "offline_increment_verified", "manifest_files": len(checked),
            "original": original_result, "heartbeats": 17, "query_pages": 2,
            "accepted_receipts": 2, "local_order_events": 2, "external_events": 0,
            "live_broker_verified": False, "live_redis_verified": False,
            "runtime_state_independently_executed": False, "material_sha256": checked}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence_directory", type=Path)
    parser.add_argument("original_directory", type=Path)
    args = parser.parse_args()
    print(json.dumps(audit(args.evidence_directory, args.original_directory), indent=2))
