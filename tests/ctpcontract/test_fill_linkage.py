import copy
import json
import unittest

from jsonschema import ValidationError

from query_contract import fixture_pages, identity, validate_page
from fill_linkage import FillLinkage


class FillLinkageTest(unittest.TestCase):
    def setUp(self):
        pages = fixture_pages()
        self.route = pages["orders"]["routing"]
        self.order = pages["orders"]["payload"]["items"][0]
        self.fill = pages["fills"]["payload"]["items"][0]
        self.serial = 0

    def model(self, **kwargs):
        return FillLinkage(self.route, native_broker="4900", investor="FIXTURE_INV", **kwargs)

    def observe(self, model, kind, row, evidence_id=None):
        self.serial += 1
        return model.observe(evidence_id or f"evidence-{self.serial}", kind, self.route,
                             json.dumps(row, separators=(",", ":")))

    def digest(self, kind, row, final):
        scope = [self.route[k] for k in ("env", "broker_id", "account_id", "gateway_id")]
        return identity(kind, scope + [row["trade_date"], row["exchange"], final])

    def test_both_arrival_orders_link_without_fake_order(self):
        for first in ("fill", "order"):
            model = self.model()
            self.observe(model, first, self.fill if first == "fill" else self.order)
            if first == "fill":
                self.assertEqual(model.report()["order_count"], 0)
                self.assertEqual(model.report()["pending_count"], 1)
            second = "order" if first == "fill" else "fill"
            self.observe(model, second, self.order if second == "order" else self.fill)
            report = model.report()
            self.assertFalse(report["blocked"])
            self.assertEqual(report["unique_fill_count"], 1)
            self.assertEqual(report["links"][self.fill["fill_id"]]["order_key"],
                             ("20261104", self.order["gateway_order_id"]))

    def test_internal_missing_hint_does_not_relax_wire_schema(self):
        row = copy.deepcopy(self.fill)
        del row["gateway_order_id"]
        model = self.model()
        self.observe(model, "fill", row)
        self.observe(model, "order", self.order)
        self.assertFalse(model.report()["blocked"])
        page = copy.deepcopy(fixture_pages()["fills"])
        page["payload"]["items"] = [row]
        with self.assertRaises(ValidationError):
            validate_page(page)

    def test_exact_evidence_replay_and_conflicting_body_retention(self):
        model = self.model()
        self.observe(model, "fill", self.fill, "source-1")
        self.assertEqual(self.observe(model, "fill", self.fill, "source-1"), "replayed")
        self.assertEqual(len(model.audit), 1)
        bad = copy.deepcopy(self.fill)
        bad["price"] = "3601"
        with self.assertRaisesRegex(ValueError, "evidence identity reused"):
            self.observe(model, "fill", bad, "source-1")
        self.assertEqual(len(model.audit), 2)
        self.assertTrue(model.report()["blocked"])

    def test_repeated_query_and_event_count_once_across_sessions(self):
        model = self.model()
        self.observe(model, "order", self.order)
        self.observe(model, "fill", self.fill)
        repeat = copy.deepcopy(self.fill)
        repeat.update(counter_session_id="restarted-process", captured_at="2026-11-04T09:00:00+08:00")
        repeat["native_ids"]["user_id"] = "EXTERNAL"
        self.observe(model, "fill", repeat)
        self.assertEqual(model.report()["unique_fill_count"], 1)
        self.assertFalse(model.report()["blocked"])
        self.assertEqual(len(model.fills[self.fill["fill_id"]]["observations"]), 2)

    def test_external_hint_links_to_existing_local_identity_without_second_fill(self):
        model = self.model()
        external = copy.deepcopy(self.fill)
        external["gateway_order_id"] = "external-ctp-v1-" + self.digest("order", external, external["native_ids"]["order_sys_id"])
        self.observe(model, "fill", external)
        self.observe(model, "order", self.order)
        self.observe(model, "fill", self.fill)
        self.assertEqual(model.report()["order_count"], 1)
        self.assertEqual(model.report()["unique_fill_count"], 1)
        self.assertFalse(model.report()["blocked"])

    def test_conflicting_execution_facts_retained_not_overwritten(self):
        for field, value in (("qty", 2), ("price", "3601"), ("symbol", "rb2701"),
                             ("trade_side", "S"), ("offset_type", "C")):
            model = self.model()
            self.observe(model, "fill", self.fill)
            bad = copy.deepcopy(self.fill)
            bad[field] = value
            with self.assertRaisesRegex(ValueError, "conflicting execution"):
                self.observe(model, "fill", bad)
            self.assertEqual(model.fills[self.fill["fill_id"]]["observations"], [self.fill])
            self.assertEqual(len(model.audit), 2)

    def test_order_id_hint_is_not_enough_without_native_order_evidence(self):
        model = self.model()
        row = copy.deepcopy(self.order)
        del row["native_ids"]["order_sys_id"]
        self.observe(model, "order", row)
        self.observe(model, "fill", self.fill)
        self.assertEqual(model.report()["pending_count"], 1)
        self.observe(model, "order", self.order)
        self.assertFalse(model.report()["blocked"])

    def test_sysid_collision_and_gateway_remap_fail_closed(self):
        for key, value in (("gateway_order_id", "another-local-order"), ("order_sys_id", "00009999")):
            model = self.model()
            self.observe(model, "order", self.order)
            other = copy.deepcopy(self.order)
            if key == "order_sys_id":
                other["native_ids"][key] = value
            else:
                other[key] = value
            with self.assertRaises(ValueError):
                self.observe(model, "order", other)
            self.assertEqual(model.report()["order_count"], 1)
            self.assertTrue(model.report()["blocked"])

    def test_wrong_local_hint_and_economic_mismatch_block_association(self):
        for key, value in (("gateway_order_id", "unrelated-local-order"), ("trade_side", "S"),
                           ("symbol", "rb2701"), ("offset_type", "CY")):
            model = self.model()
            bad = copy.deepcopy(self.fill)
            bad[key] = value
            self.observe(model, "fill", bad)
            self.observe(model, "order", self.order)
            self.assertTrue(model.report()["blocked"])
            self.assertEqual(model.report()["links"][bad["fill_id"]]["state"], "conflict")

    def test_unique_fill_total_not_snapshot_cumulative_controls_overfill_check(self):
        model = self.model()
        self.observe(model, "order", self.order)  # cum_filled_qty=0 may lag executions.
        self.observe(model, "fill", self.fill)
        self.assertFalse(model.report()["blocked"])
        second = copy.deepcopy(self.fill)
        second["native_ids"]["trade_id"] = "00005679"
        second["fill_id"] = "ctp-fill-v1-" + self.digest("fill", second, "00005679")
        self.observe(model, "fill", second)
        self.assertIn("unique fills exceed order quantity", model.report()["conflicts"])
        self.assertEqual(model.report()["unique_fill_count"], 2)

    def test_day_reuse_is_separate_and_night_timestamp_unchanged(self):
        model = self.model()
        self.observe(model, "order", self.order)
        self.observe(model, "fill", self.fill)
        order, fill = copy.deepcopy(self.order), copy.deepcopy(self.fill)
        order["trade_date"] = fill["trade_date"] = "20261105"
        fill["fill_id"] = "ctp-fill-v1-" + self.digest("fill", fill, fill["native_ids"]["trade_id"])
        self.observe(model, "fill", fill)
        self.assertEqual(model.report()["pending_count"], 1)
        self.observe(model, "order", order)
        self.assertEqual(model.report()["unique_fill_count"], 2)
        self.assertFalse(model.report()["blocked"])
        self.assertEqual(model.fills[self.fill["fill_id"]]["observations"][0]["matched_at"],
                         "2026-11-03T21:01:01+08:00")

    def test_native_investor_authorization_and_foreign_environment(self):
        model = self.model()
        route = dict(self.route, env="prod")
        with self.assertRaisesRegex(ValueError, "foreign routing"):
            model.observe("foreign", "fill", route, json.dumps(self.fill))
        self.assertEqual(len(model.audit), 0)
        row = copy.deepcopy(self.fill)
        row["native_ids"]["investor_id"] = "OTHER_INV"
        with self.assertRaisesRegex(ValueError, "authorization"):
            self.observe(model, "fill", row)
        self.assertEqual(len(model.audit), 1)
        self.assertTrue(model.report()["blocked"])

    def test_fee_evidence_is_versioned_not_added_or_execution_conflict(self):
        model = self.model()
        self.observe(model, "order", self.order)
        self.observe(model, "fill", self.fill)
        for fee in ("1.20", "1.30"):
            row = copy.deepcopy(self.fill)
            row.update(fee=fee, fee_complete=True)
            self.observe(model, "fill", row)
        self.assertEqual(model.report()["unique_fill_count"], 1)
        self.assertFalse(model.report()["blocked"])
        self.assertEqual([r["fee"] for r in model.fills[self.fill["fill_id"]]["observations"]],
                         [None, "1.20", "1.30"])

    def test_null_time_can_be_enriched_but_conflicting_known_time_cannot(self):
        model = self.model()
        row = copy.deepcopy(self.fill)
        row["matched_at"] = None
        self.observe(model, "fill", row)
        self.observe(model, "fill", self.fill)
        row["matched_at"] = "2026-11-03T21:01:02+08:00"
        with self.assertRaisesRegex(ValueError, "conflicting source time"):
            self.observe(model, "fill", row)

    def test_invalid_identity_or_missing_native_fact_stays_in_audit(self):
        for key in ("fill_id", "order_sys_id", "trade_id"):
            model = self.model()
            row = copy.deepcopy(self.fill)
            if key == "fill_id":
                row[key] = "ctp-fill-v1-" + "0" * 64
            else:
                del row["native_ids"][key]
            with self.assertRaises(ValueError):
                self.observe(model, "fill", row)
            self.assertEqual(len(model.audit), 1)
            self.assertEqual(model.report()["unique_fill_count"], 0)
            self.assertTrue(model.report()["blocked"])

    def test_bounded_evidence_never_evicted_or_successfully_acked(self):
        for kwargs in ({"max_evidence": 1}, {"max_bytes": 1}):
            model = self.model(**kwargs)
            with self.assertRaisesRegex(ValueError, "resource limit"):
                self.observe(model, "fill", self.fill)
                self.observe(model, "order", self.order)
            self.assertTrue(model.report()["blocked"])
            self.assertLessEqual(len(model.audit), kwargs.get("max_evidence", 1))

    def test_snapshot_updates_do_not_choose_order_lifecycle_by_arrival(self):
        model = self.model()
        self.observe(model, "order", self.order)
        done = copy.deepcopy(self.order)
        done.update(gateway_status="filled", cum_filled_qty=1, leaves_qty=0)
        self.observe(model, "order", done)
        self.assertEqual(model.orders[("20261104", self.order["gateway_order_id"])]["gateway_status"], "working")
        self.assertEqual(len(model.audit), 2)

    def test_report_and_inputs_do_not_mutate_linkage(self):
        route = copy.deepcopy(self.route)
        model = FillLinkage(route, native_broker="4900", investor="FIXTURE_INV")
        route["env"] = "prod"
        self.observe(model, "fill", self.fill)
        report = model.report()
        report["links"].clear()
        report["conflicts"].append("external")
        self.assertEqual(model.report()["unique_fill_count"], 1)
        self.assertEqual(model.report()["conflicts"], [])


if __name__ == "__main__":
    unittest.main()
