import copy
import itertools
import json
import unittest

from jsonschema import ValidationError

from command_contract import validate_command
from fill_linkage import source_instant
from order_projection import OrderProjection
from query_contract import ROOT, decode_json, fixture_pages


class CommandContractTest(unittest.TestCase):
    def setUp(self):
        self.commands = {c["name"]: c["body"] for c in decode_json((ROOT / "messages.json").read_text())["cases"]}
        self.route = fixture_pages()["orders"]["routing"]

    def validate(self, body, **kwargs):
        return validate_command(body, routing=self.route, trading_day="20261104", **kwargs)

    def test_original_commands_and_detached_inheritance(self):
        before = copy.deepcopy(self.commands)
        for name in ("submit", "batch", "cancel"):
            result = self.validate(self.commands[name])
            if name == "batch":
                self.assertTrue(all(r["product_type"] == "futures" for r in result["orders"]))
                result["orders"].clear()
        self.assertEqual(self.commands, before)

    def test_missing_required_fields_and_unrecognized_fields(self):
        for value in (None, True, [], {}, "not-supported"):
            body = copy.deepcopy(self.commands["submit"])
            body["action"] = value
            with self.assertRaises(ValueError):
                self.validate(body)
        for name in ("submit", "batch", "cancel"):
            for key in self.commands[name]:
                body = copy.deepcopy(self.commands[name])
                del body[key]
                with self.assertRaises((ValueError, ValidationError)):
                    self.validate(body)
            body = copy.deepcopy(self.commands[name])
            body["routing"] = self.route
            with self.assertRaises(ValidationError):
                self.validate(body)
        for key in ("price", "qty", "native_ids", "order_id", "security_id"):
            body = copy.deepcopy(self.commands["submit"])
            body["payload"][key] = 1
            with self.assertRaises(ValidationError):
                self.validate(body)

    def test_batch_scope_types_and_unique_children(self):
        for key, value in (("account_id", "990000000002"), ("product_type", None),
                           ("schema_version", "ctp.f0.draft.1"), ("business_type", "S")):
            body = copy.deepcopy(self.commands["batch"])
            body["payload"]["orders"][0][key] = value
            with self.assertRaises((ValueError, ValidationError)):
                self.validate(body)
        for key in ("client_order_id", "gateway_order_id"):
            body = copy.deepcopy(self.commands["batch"])
            rows = body["payload"]["orders"]
            rows[1][key] = rows[0][key]
            with self.assertRaisesRegex(ValueError, "duplicate batch"):
                self.validate(body)
        with self.assertRaisesRegex(ValueError, "resource limit"):
            self.validate(self.commands["batch"], max_children=1)
        body = copy.deepcopy(self.commands["batch"])
        body["payload"]["orders"] = []
        with self.assertRaises(ValidationError):
            self.validate(body)

    def test_strict_price_and_quantity_tokens(self):
        for key, values in (("order_qty", (True, 1.0, "1", 0, 2147483648)),
                            ("limit_price", (3600, "0", "1e3", "1.0000000001", "18446744073709551616"))):
            for value in values:
                body = copy.deepcopy(self.commands["submit"])
                body["payload"][key] = value
                with self.assertRaises((ValueError, ValidationError)):
                    self.validate(body)

    def test_new_order_trading_day_and_cancel_target_day_are_distinct(self):
        body = copy.deepcopy(self.commands["submit"])
        body["payload"]["trade_date"] = "20261103"
        with self.assertRaisesRegex(ValueError, "TradingDay mismatch"):
            self.validate(body)
        cancel = copy.deepcopy(self.commands["cancel"])
        cancel["payload"]["trade_date"] = "20261103"
        self.assertEqual(self.validate(cancel)["trade_date"], "20261103")
        cancel["payload"]["trade_date"] = "20260230"
        with self.assertRaises(ValidationError):
            self.validate(cancel)

    def test_exchange_account_and_explicit_offset(self):
        for key, value in (("exchange", "CFFEX"), ("account_id", "990000000002"),
                           ("offset_type", None), ("hedge_type", "hedge")):
            body = copy.deepcopy(self.commands["submit"])
            body["payload"][key] = value
            with self.assertRaises((ValueError, ValidationError)):
                self.validate(body)
        for offset in ("O", "C", "CT", "CY"):
            body = copy.deepcopy(self.commands["submit"])
            body["payload"]["offset_type"] = offset
            self.validate(body)  # Field validity is not proof of broker applicability.


class OrderProjectionTest(unittest.TestCase):
    def setUp(self):
        page = fixture_pages()["orders"]
        self.route = page["routing"]
        self.base = page["payload"]["items"][0]
        self.base["order_qty"], self.base["leaves_qty"] = 3, 3
        self.serial = 0

    def model(self):
        return OrderProjection(self.route, native_broker="4900", investor="FIXTURE_INV",
                               trade_date=self.base["trade_date"], gateway_order_id=self.base["gateway_order_id"])

    def row(self, status, filled):
        row = copy.deepcopy(self.base)
        row.update(gateway_status=status, cum_filled_qty=filled, leaves_qty=3 - filled)
        return row

    def observe(self, model, row, evidence=None):
        self.serial += 1
        return model.observe(evidence or f"observation-{self.serial}", json.dumps(row))

    def test_all_monotonic_lifecycle_permutations_converge(self):
        rows = [self.row("accepted", 0), self.row("working", 0),
                self.row("working", 1), self.row("filled", 3)]
        for permutation in itertools.permutations(rows):
            model = self.model()
            for row in permutation:
                self.observe(model, row)
            self.assertFalse(model.view()["blocked"])
            self.assertEqual(model.view()["last_uncontested"]["gateway_status"], "filled")

    def test_partial_fill_is_working_not_new_gateway_enum(self):
        model = self.model()
        self.observe(model, self.row("working", 1))
        self.assertEqual(model.view()["last_uncontested"]["cum_filled_qty"], 1)
        self.assertEqual(self.observe(model, self.row("working", 0)), "stale")

    def test_cancelled_remaining_quantity_is_not_reactivated(self):
        model = self.model()
        self.observe(model, self.row("cancelled", 1))
        self.assertEqual(self.observe(model, self.row("working", 1)), "stale")
        self.assertEqual(model.view()["last_uncontested"]["leaves_qty"], 2)
        self.assertEqual(self.observe(model, self.row("working", 2)), "conflict")

    def test_distinct_terminal_states_conflict_in_both_orders(self):
        for pair in itertools.permutations((self.row("cancelled", 0), self.row("filled", 3), self.row("rejected", 0)), 2):
            model = self.model()
            self.observe(model, pair[0])
            self.assertEqual(self.observe(model, pair[1]), "conflict")
            self.assertTrue(model.view()["blocked"])
            self.assertEqual(model.view()["last_uncontested"], pair[0])

    def test_terminal_regression_and_same_terminal_changes_conflict(self):
        for first in (self.row("working", 2), self.row("cancelled", 2)):
            model = self.model()
            self.observe(model, first)
            self.assertEqual(self.observe(model, self.row("cancelled", 1)), "conflict")
        model = self.model()
        self.observe(model, self.row("cancelled", 1))
        self.assertEqual(self.observe(model, self.row("cancelled", 2)), "conflict")

    def test_incomparable_active_state_quantity_blocks(self):
        for pair in ((self.row("accepted", 2), self.row("working", 1)),
                     (self.row("working", 1), self.row("accepted", 2))):
            model = self.model()
            self.observe(model, pair[0])
            self.assertEqual(self.observe(model, pair[1]), "conflict")

    def test_replays_and_later_capture_time_do_not_override_facts(self):
        model = self.model()
        terminal = self.row("filled", 3)
        self.observe(model, terminal, "same-source")
        self.assertEqual(self.observe(model, terminal, "same-source"), "replayed")
        stale = self.row("working", 0)
        stale["captured_at"] = "2026-11-05T15:01:00+08:00"
        self.assertEqual(self.observe(model, stale), "stale")
        self.assertEqual(self.observe(model, terminal), "equivalent")

    def test_bad_identity_or_quantity_blocks_without_overwriting(self):
        for field, value in (("order_qty", 4), ("symbol", "rb2701"), ("cum_filled_qty", 4)):
            model = self.model()
            original = self.row("working", 0)
            self.observe(model, original)
            bad = copy.deepcopy(original)
            bad[field] = value
            with self.assertRaises(ValueError):
                self.observe(model, bad)
            self.assertTrue(model.view()["blocked"])
            self.assertEqual(model.view()["last_uncontested"], original)

    def test_known_time_correction_requires_explicit_evidence(self):
        self.assertNotEqual(source_instant("2026-11-03T21:01:00.000000001+08:00"),
                            source_instant("2026-11-03T21:01:00.000000002+08:00"))
        model = self.model()
        self.observe(model, self.row("working", 0))
        equivalent = self.row("working", 0)
        equivalent["submitted_at"] = "2026-11-03T21:01:00.000+08:00"
        self.assertEqual(self.observe(model, equivalent), "equivalent")
        changed = self.row("working", 1)
        changed["submitted_at"] = "2026-11-03T21:01:02+08:00"
        self.assertEqual(self.observe(model, changed), "conflict")
        model = self.model()
        unknown = self.row("working", 0)
        unknown["submitted_at"] = None
        self.observe(model, unknown)
        self.assertEqual(self.observe(model, equivalent), "equivalent")
        self.assertEqual(self.observe(model, changed), "conflict")

    def test_wrong_target_rejected_and_view_is_detached(self):
        model = self.model()
        self.observe(model, self.base)
        other = copy.deepcopy(self.base)
        other["gateway_order_id"] = "other-order"
        with self.assertRaisesRegex(ValueError, "foreign order"):
            self.observe(model, other)
        view = model.view()
        view["last_uncontested"]["gateway_status"] = "filled"
        self.assertFalse(model.view()["blocked"])
        self.assertEqual(model.view()["last_uncontested"]["gateway_status"], "working")


if __name__ == "__main__":
    unittest.main()
