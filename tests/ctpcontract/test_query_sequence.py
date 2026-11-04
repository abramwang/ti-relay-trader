import copy
import itertools
import json
import unittest

from query_contract import fixture_pages
from query_sequence import QuerySequence


def encoded(body):
    return json.dumps(body, ensure_ascii=False, separators=(",", ":"))


def context(body):
    result = {k: copy.deepcopy(body[k]) for k in ("routing", "action", "origin_message_id")}
    evidence = body["adapter_context"]["query_evidence"]
    result.update({k: evidence[k] for k in ("ticket", "counter_session_id", "connection_generation", "sdk_request_id")})
    result["trade_date"] = body["payload"].get("trade_date", "20261104")
    return result


class QuerySequenceTest(unittest.TestCase):
    def setUp(self):
        self.fixtures = fixture_pages()
        self.base = self.fixtures["orders"]
        self.expected = context(self.base)

    def new(self, **limits):
        return QuerySequence(self.expected, now=10, deadline=20, **limits)

    def pages(self, count=3):
        pages = []
        for i in range(count):
            body = copy.deepcopy(self.base)
            body["message_id"] += f"-{i}"
            body["chunk"] = {"index": i, "is_last": i == count - 1}
            body["status"] = "completed" if i == count - 1 else "partial"
            row = body["payload"]["items"][0]
            row["gateway_order_id"] = f"fixture-order-{i}"
            row["native_ids"]["order_sys_id"] = f"0000123{i}"
            row["native_ids"]["order_ref"] = f"00000000000{i}"
            pages.append(body)
        return pages

    def failure(self):
        body = copy.deepcopy(self.base)
        body.update(message_id="fixture-failed", status="failed", code="QUERY_FAILED")
        del body["chunk"]
        del body["payload"]["items"]
        body["adapter_context"]["query_evidence"]["snapshot_applied"] = False
        return body

    def test_all_arrival_permutations(self):
        for permutation in itertools.permutations(self.pages()):
            seq = self.new()
            for i, body in enumerate(permutation):
                self.assertEqual(seq.accept(encoded(body), now=11 + i), "ready" if i == 2 else "collecting")
                if i < 2:
                    with self.assertRaises(ValueError):
                        seq.candidate()
            self.assertEqual([r["gateway_order_id"] for r in seq.candidate()], [f"fixture-order-{i}" for i in range(3)])

    def test_exact_replay_does_not_count_twice(self):
        seq = self.new()
        for body in self.pages():
            text = encoded(body)
            seq.accept(text, now=11)
            before = (seq.rows, seq.bytes, len(seq.pages))
            self.assertEqual(seq.accept(text, now=11), "replayed")
            self.assertEqual((seq.rows, seq.bytes, len(seq.pages)), before)
        self.assertEqual(seq.accept(text, now=21), "replayed")
        self.assertEqual(seq.state, "ready")

    def test_changed_body_and_new_id_for_same_page_conflict(self):
        for kind in ("content", "id", "whitespace"):
            seq = self.new()
            page = self.pages()[0]
            text = encoded(page)
            seq.accept(text, now=11)
            if kind == "content":
                page["produced_at"] = "2026-11-03T21:04:00+08:00"
            elif kind == "id":
                page["message_id"] += "-new"
            other = text + " " if kind == "whitespace" else encoded(page)
            with self.assertRaises(ValueError):
                seq.accept(other, now=12)
            self.assertEqual(seq.state, "invalid")

    def test_missing_first_middle_or_final_expires(self):
        for missing in range(3):
            seq = self.new()
            for i, page in enumerate(self.pages()):
                if i != missing:
                    seq.accept(encoded(page), now=11)
            self.assertEqual(seq.advance(20), "expired")
            with self.assertRaises(ValueError):
                seq.accept(encoded(self.pages()[missing]), now=20)
            with self.assertRaises(ValueError):
                seq.candidate()

    def test_failure_does_not_expose_partial_candidate(self):
        seq = self.new()
        seq.accept(encoded(self.pages()[0]), now=11)
        failed = encoded(self.failure())
        self.assertEqual(seq.accept(failed, now=12), "failed")
        self.assertEqual(seq.accept(failed, now=13), "replayed")
        with self.assertRaises(ValueError):
            seq.candidate()

    def test_late_conflict_revokes_candidate_not_broker_facts(self):
        seq = self.new()
        text = encoded(self.base)
        seq.accept(text, now=11)
        self.assertEqual(len(seq.candidate()), 1)
        other = copy.deepcopy(self.base)
        other["message_id"] += "-second-terminal"
        with self.assertRaises(ValueError):
            seq.accept(encoded(other), now=21)
        with self.assertRaises(ValueError):
            seq.candidate()
        self.assertEqual(seq.messages[self.base["message_id"]], text)

    def test_previous_pages_can_replay_after_failure_without_recovery(self):
        seq = self.new()
        first = encoded(self.pages()[0])
        seq.accept(first, now=11)
        seq.accept(encoded(self.failure()), now=12)
        self.assertEqual(seq.accept(first, now=13), "replayed")
        self.assertEqual(seq.state, "failed")
        with self.assertRaises(ValueError):
            seq.candidate()

    def test_failure_success_terminal_conflicts_both_orders(self):
        for pages in ((self.failure(), self.base), (self.base, self.failure()),
                      (self.pages()[2], self.failure())):
            seq = self.new()
            seq.accept(encoded(pages[0]), now=11)
            with self.assertRaises(ValueError):
                seq.accept(encoded(pages[1]), now=12)
            self.assertEqual(seq.state, "invalid")

    def test_two_successful_terminals_and_page_beyond_terminal(self):
        a, b, c = self.pages()
        b["status"], b["chunk"]["is_last"] = "completed", True
        for first, second in ((b, c), (c, b)):
            seq = self.new()
            seq.accept(encoded(first), now=11)
            with self.assertRaises(ValueError):
                seq.accept(encoded(second), now=12)
        c["status"], c["chunk"]["is_last"] = "partial", False
        for first, second in ((b, c), (c, b)):
            seq = self.new()
            seq.accept(encoded(first), now=11)
            with self.assertRaises(ValueError):
                seq.accept(encoded(second), now=12)

    def test_cross_page_duplicate_business_identity_all_types(self):
        for name in ("asset", "positions", "orders", "fills"):
            first = copy.deepcopy(self.fixtures[name])
            last = copy.deepcopy(first)
            first.update(status="partial", chunk={"index": 0, "is_last": False})
            last.update(message_id=last["message_id"] + "-last", chunk={"index": 1, "is_last": True})
            seq = QuerySequence(context(first), now=10, deadline=20)
            seq.accept(encoded(first), now=11)
            with self.assertRaisesRegex(ValueError, "duplicate business identity"):
                seq.accept(encoded(last), now=12)
            self.assertEqual(seq.state, "invalid")

    def test_empty_complete_only_three_query_types(self):
        for name in ("asset", "positions", "orders", "fills"):
            body = copy.deepcopy(self.fixtures[name])
            body["payload"]["items"] = []
            seq = QuerySequence(context(body), now=10, deadline=20)
            if name == "asset":
                with self.assertRaises(ValueError):
                    seq.accept(encoded(body), now=11)
            else:
                self.assertEqual(seq.accept(encoded(body), now=11), "ready")
                self.assertEqual(seq.candidate(), [])

    def test_wrong_scope_does_not_poison_but_wrong_evidence_does(self):
        for key in ("routing", "origin_message_id"):
            seq = self.new()
            bad = copy.deepcopy(self.base)
            if key == "routing":
                bad[key]["env"] = "prod"
            else:
                bad[key] = "other-query"
            with self.assertRaisesRegex(ValueError, "foreign query"):
                seq.accept(encoded(bad), now=11)
            self.assertEqual(seq.accept(encoded(self.base), now=12), "ready")
        for key in ("ticket", "counter_session_id", "connection_generation", "sdk_request_id"):
            seq = self.new()
            bad = copy.deepcopy(self.base)
            bad["adapter_context"]["query_evidence"][key] = 2 if key in ("connection_generation", "sdk_request_id") else "other"
            with self.assertRaises(ValueError):
                seq.accept(encoded(bad), now=11)
            self.assertEqual(seq.state, "invalid")

    def test_interrupt_and_new_query_are_independent(self):
        seq = self.new()
        seq.accept(encoded(self.pages()[0]), now=11)
        seq.interrupt("connection generation changed")
        with self.assertRaises(ValueError):
            seq.accept(encoded(self.pages()[2]), now=12)
        fresh = copy.deepcopy(self.base)
        fresh["origin_message_id"] += "-new-query"
        fresh["message_id"] += "-new-reply"
        fresh["adapter_context"]["query_evidence"].update(
            ticket="new-ticket", counter_session_id="new-session", connection_generation=2,
            sdk_request_id=15)
        new = QuerySequence(context(fresh), now=12, deadline=22)
        with self.assertRaisesRegex(ValueError, "foreign query"):
            new.accept(encoded(self.base), now=13)
        self.assertEqual(new.accept(encoded(fresh), now=13), "ready")

    def test_resource_limits_no_partial_candidate(self):
        for limits, pages in (({"max_pages": 2}, self.pages()),
                              ({"max_rows": 1}, self.pages()),
                              ({"max_bytes": 1}, [self.base]),
                              ({"max_bytes": len(encoded(self.pages()[0]).encode()) + 1}, self.pages())):
            seq = self.new(**limits)
            with self.assertRaises(ValueError):
                for page in pages:
                    seq.accept(encoded(page), now=11)
            with self.assertRaises(ValueError):
                seq.candidate()

    def test_monotonic_time_and_deadline(self):
        for now in (9, float("nan"), float("inf"), True, "11"):
            seq = self.new()
            with self.assertRaises(ValueError):
                seq.accept(encoded(self.base), now=now)
        seq = self.new()
        with self.assertRaises(ValueError):
            seq.accept(encoded(self.base), now=20)

    def test_input_and_output_are_detached(self):
        expected = copy.deepcopy(self.expected)
        seq = QuerySequence(expected, now=10, deadline=20)
        expected["routing"]["env"] = "prod"
        seq.accept(encoded(self.base), now=11)
        candidate = seq.candidate()
        candidate.clear()
        self.assertEqual(len(seq.candidate()), 1)

    def test_queue_expired_has_no_fabricated_sdk_request(self):
        body = self.fixtures["queue_expired"]
        seq = QuerySequence(context(body), now=10, deadline=20)
        self.assertEqual(seq.accept(encoded(body), now=11), "failed")
        self.assertIsNone(seq.expected["sdk_request_id"])

    def test_malformed_or_invalid_related_page_fails_closed(self):
        for text in ("{", "null", '{"x":1,"x":2}', encoded(self.base).replace('"order_qty":1', '"order_qty":1.0')):
            seq = self.new()
            with self.assertRaises(ValueError):
                seq.accept(text, now=11)
            self.assertEqual(seq.state, "invalid")


if __name__ == "__main__":
    unittest.main()
