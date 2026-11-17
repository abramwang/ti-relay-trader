#!/usr/bin/env python3
"""Offline OC audit -> operator review draft. Never publishes or accesses a DB."""

import argparse
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re


def require(ok, message):
    if not ok:
        raise ValueError(message)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"),
                      ensure_ascii=False, allow_nan=False).encode("utf-8")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON member")
        result[key] = value
    return result


def decode(raw):
    return json.loads(raw, object_pairs_hook=unique_object,
                      parse_constant=lambda _: require(False, "non-finite JSON number"))


def bounded_read(path, maximum):
    with Path(path).open("rb") as source:
        raw = source.read(maximum + 1)
    require(0 < len(raw) <= maximum, "input missing or exceeds size limit")
    return raw


def positive(value):
    return type(value) is int and 0 < value <= 9223372036854775807


def build(audit, command_raw, target, options):
    require(digest(audit) == options.audit_sha256, "audit file SHA256 mismatch")
    require(len(audit) <= 64 * 1024 * 1024 and audit.endswith(b"\n"), "incomplete/oversized audit")
    command = decode(command_raw)
    require(command.get("protocol") == "relay.stream.v1" and command.get("message_type") == "command",
            "original command required, not a reply")
    require(command.get("action") in {"account.asset.query", "account.positions.query", "order.list.query", "fill.list.query"},
            "only four read-only query actions supported")
    payload = command.get("payload", {})
    require(payload.get("account_id") == options.account and payload.get("product_type") == "futures"
            and payload.get("schema_version") == "ctp.f0.draft.2", "command payload scope mismatch")
    origin, request = command.get("message_id"), command.get("request_id")
    require(isinstance(origin, str) and origin and isinstance(request, str) and request, "missing command identity")
    require(options.broker == "ctp" or options.broker.startswith("ctp_"), "CTP route required")
    require(positive(options.command_id) and 1 <= options.timeout_ms <= 300000, "invalid review bounds")
    for value, limit in ((options.batch_id, 256), (options.review_id, 128)):
        require(isinstance(value, str) and 0 < len(value) <= limit and value.strip() == value,
                "invalid review identity")
    require(set(target) == {"source_id", "server_run_id", "epoch", "group", "counter_session_id", "trade_date", "connection_generation"},
            "target must be an independently reviewed exact binding")
    for key in ("source_id", "server_run_id", "epoch", "group", "counter_session_id"):
        require(isinstance(target[key], str) and 0 < len(target[key]) <= 128 and target[key].strip() == target[key],
                "invalid target identity")
    require(re.fullmatch(r"[0-9a-f]{40}", target["server_run_id"]) is not None
            and re.fullmatch(r"[0-9]{8}", target["trade_date"]) is not None
            and positive(target["connection_generation"]), "invalid target context")
    datetime.strptime(target["trade_date"], "%Y%m%d")
    scope = ["test", options.broker, options.account, options.gateway,
             options.native_broker, options.user, options.investor]
    require(all(isinstance(v, str) and v and v.strip() == v for v in scope), "explicit identity required")
    route = dict(zip(("env", "broker_id", "account_id", "gateway_id"), scope[:4]))
    prefix = f"relay:test:v1:{options.broker}:{options.gateway}"
    selected, results, requests, received = [], [], [], []
    previous = ""
    lines = audit.splitlines(keepends=True)
    for sequence, line in enumerate(lines, 1):
        require(len(line) <= 4 * 1024 * 1024, "audit line exceeds limit")
        wrapper = decode(line)
        require(set(wrapper) == {"record", "sha256"}, "invalid audit wrapper")
        record = wrapper["record"]
        require(type(record.get("sequence")) is int and record["sequence"] == sequence
                and record.get("previous") == previous, "audit must be a contiguous prefix starting at 1")
        require(digest(canonical(record)) == wrapper["sha256"], "audit chain digest mismatch")
        require(decode(record["scope"]) == scope, "audit native/route scope mismatch")
        previous = wrapper["sha256"]
        entry = record["entry"]
        kind = entry.get("kind")
        if kind == "command_received":
            candidate = decode(entry["body"])
            if candidate.get("message_id") != origin:
                continue
            require(entry["body"].encode() == command_raw, "received command differs from original bytes")
            require(entry.get("stream") == prefix + ":cmd.query" and entry.get("group") == target["group"],
                    "received command transport mismatch")
            received.append(sequence)
        elif kind in {"query_request", "query_result"} and entry.get("origin_message_id") == origin:
            require(entry.get("action") == command["action"] and entry.get("request_id") == request,
                    "request/action mismatch")
            for key in ("counter_session_id", "connection_generation", "trade_date"):
                require(entry.get(key) == target[key], "audit target context mismatch")
            require(positive(entry.get("connection_generation")), "invalid connection generation")
            require(positive(entry.get("ticket")) and positive(entry.get("sdk_request_id")), "invalid SDK/ticket identity")
            if kind == "query_result":
                raw = entry.get("raw", {})
                require(positive(raw.get("sdk_request_id")) and raw["sdk_request_id"] == entry["sdk_request_id"], "raw SDK identity mismatch")
                results.append((sequence, entry))
            else:
                requests.append((sequence, entry))
        else:
            continue
        require(record.get("counter_session_id") == target["counter_session_id"], "audit outer session mismatch")
        selected.append((sequence, line))
    require(len(received) >= 1 and len(results) == 1 and len(requests) <= 1,
            "need original command receipt and one unambiguous result, not a planned request or reply")
    sequence, result = results[0]
    require(min(received) < sequence, "result precedes command receipt")
    if requests:
        request_sequence, native_request = requests[0]
        require(min(received) < request_sequence < sequence, "native request audit order mismatch")
        require(native_request["ticket"] == result["ticket"] and native_request["sdk_request_id"] == result["sdk_request_id"],
                "request/result SDK identity mismatch")
    evidence = b"".join(line for _, line in selected)
    require(len(evidence) <= 262144, "selected evidence exceeds receiver limit; do not truncate")
    review = {
        "command_id": options.command_id, "batch_id": options.batch_id, "review_id": options.review_id,
        "evidence_sha256": digest(evidence), "target": target, "timeout_ms": options.timeout_ms,
        "query_context": {
            "Route": route, "Action": command["action"], "OriginMessageID": origin,
            "Ticket": str(result["ticket"]), "SDKRequestID": result["sdk_request_id"],
            "CounterSessionID": target["counter_session_id"], "ConnectionGeneration": target["connection_generation"],
            "TradeDate": target["trade_date"], "NativeBrokerID": options.native_broker,
            "NativeInvestorID": options.investor, "NativeUserID": options.user,
        },
    }
    report = {
        "status": "offline_draft_requires_operator_review", "network_accessed": False,
        "registered_or_published": False, "audit_file_sha256": digest(audit), "audit_records": len(lines),
        "audit_chain_head": previous, "selected_sequences": [seq for seq, _ in selected],
        "command_sha256": digest(command_raw), "evidence_sha256": digest(evidence),
        "raw_application": result.get("raw_application"), "raw_state": result["raw"].get("state"),
        "sdk_return_code": result.get("sdk_return_code"), "last_seen": result["raw"].get("last_seen"),
        "callback_error": result["raw"].get("callback_error"),
        "limitations": ["hash chain is not broker authentication", "original publication/deadline verified by receiver",
                        "draft does not upgrade failed/partial results or authorize retries"],
    }
    return review, evidence, report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for field in ("audit-file", "audit-sha256", "command-file", "target-file", "broker", "gateway", "account",
                  "native-broker", "user", "investor", "batch-id", "review-id", "output-dir"):
        parser.add_argument("--" + field, required=True)
    parser.add_argument("--command-id", type=int, required=True)
    parser.add_argument("--timeout-ms", type=int, default=60000)
    options = parser.parse_args()
    try:
        review, evidence, report = build(bounded_read(options.audit_file, 64 * 1024 * 1024),
                                        bounded_read(options.command_file, 65536),
                                        decode(bounded_read(options.target_file, 4096)), options)
        output = Path(options.output_dir)
        output.mkdir(mode=0o700, parents=False, exist_ok=False)
        for name, raw in (("review.json", canonical(review) + b"\n"), ("evidence.jsonl", evidence),
                          ("report.json", canonical(report) + b"\n")):
            fd = os.open(output / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "wb") as destination:
                destination.write(raw)
                destination.flush()
                os.fsync(destination.fileno())
        print("Offline draft created; validate with relay-ctp-replies before explicit registration. No network or database actions.")
    except (ValueError, KeyError, TypeError, OSError) as error:
        parser.exit(1, f"Cannot build review: {error}\n")


if __name__ == "__main__":
    main()
