from __future__ import annotations

import os
from pathlib import Path
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
PIPELINE = ROOT / "scripts" / "run-post-close-pipeline.sh"


class PostClosePipelineTest(unittest.TestCase):
    def run_pipeline(self, capture_state: str, settlement_state: str = "ready", accounts: str = "acct-1,acct-2") -> tuple[subprocess.CompletedProcess[str], str]:
        with tempfile.TemporaryDirectory() as temp_dir:
            temp = Path(temp_dir)
            fake_python = temp / "python"
            calls = temp / "calls.log"
            fake_python.write_text(
                textwrap.dedent(
                    """\
                    #!/usr/bin/python3
                    import json
                    import os
                    from pathlib import Path
                    import sys

                    if len(sys.argv) > 1 and sys.argv[1] == "-":
                        sys.argv = sys.argv[1:]
                        exec(compile(sys.stdin.read(), "<stdin>", "exec"), {"__name__": "__main__"})

                    with open(os.environ["PIPELINE_CALLS"], "a", encoding="utf-8") as handle:
                        handle.write(" ".join(sys.argv[1:]) + "\\n")
                    output = Path(sys.argv[sys.argv.index("--output") + 1])
                    output.parent.mkdir(parents=True, exist_ok=True)
                    if "relay.jobs.post_close_capture" in sys.argv:
                        state = os.environ["PIPELINE_CAPTURE_STATE"]
                        report = {
                            "ok": state != "failed",
                            "skipped": state == "skipped",
                            "trading_day": {"target_trade_date": "20260803"},
                            "broker_close_snapshot": {
                                "ok": state not in ("failed", "incomplete"),
                                "result": {
                                    "status": "completed",
                                    "trade_date": "2026-08-03", "snapshot_type": "broker_close",
                                    "account_error_count": 0,
                                    "accounts": [
                                        {"account_id": "acct-1", "asset_snapshot_written": True, "positions_count": 0, "position_snapshots_written": 0, "errors": []},
                                        {"account_id": "acct-2", "asset_snapshot_written": True, "positions_count": 2, "position_snapshots_written": 2, "errors": []},
                                    ],
                                },
                            },
                        }
                        if state == "partial_positions":
                            report["broker_close_snapshot"]["result"]["accounts"][1]["position_snapshots_written"] = 1
                        elif state == "old_date":
                            report["broker_close_snapshot"]["result"]["trade_date"] = "2026-07-31"
                        output.write_text(json.dumps(report), encoding="utf-8")
                        raise SystemExit(1 if state == "failed" else 0)
                    if "relay.jobs.post_close_settlement" in sys.argv:
                        state = os.environ["PIPELINE_SETTLEMENT_STATE"]
                        report = {
                            "ok": state != "failed",
                            "skipped": False,
                            "trading_day": {"target_trade_date": "20260803"},
                            "settlement_snapshot": {
                                "ok": state != "failed",
                                "result": {
                                    "status": "failed" if state == "failed" else "completed",
                                    "trade_date": "2026-08-03", "snapshot_type": "close", "input_snapshot_type": "broker_close",
                                    "account_error_count": 0,
                                    "accounts": [
                                        {"account_id": "acct-1", "asset_snapshot_written": True, "positions_count": 0, "position_snapshots_written": 0},
                                        {"account_id": "acct-2", "asset_snapshot_written": True, "positions_count": 2, "position_snapshots_written": 2},
                                    ],
                                },
                            },
                        }
                        snapshot = report["settlement_snapshot"]["result"]
                        if state == "partial":
                            snapshot["account_error_count"] = 1
                            snapshot["accounts"][1]["errors"] = ["missing market price"]
                            snapshot["accounts"][1]["asset_snapshot_written"] = False
                        elif state == "partial_positions":
                            snapshot["accounts"][1]["position_snapshots_written"] = 1
                        elif state == "all_failed":
                            for item in snapshot["accounts"]:
                                item["errors"] = ["snapshot failed"]
                        elif state == "old_date":
                            snapshot["trade_date"] = "2026-07-31"
                        elif state == "duplicate":
                            snapshot["accounts"][1]["account_id"] = "acct-1"
                        elif state == "unknown_account":
                            snapshot["accounts"].append({"account_id": "not-captured", "asset_snapshot_written": True, "positions_count": 0, "position_snapshots_written": 0})
                        elif state == "missing_counts":
                            for item in snapshot["accounts"]:
                                del item["positions_count"]
                        elif state == "global_error":
                            snapshot["errors"] = ["reconciliation transaction failed"]
                        elif state == "dry_run":
                            snapshot["dry_run"] = True
                        elif state == "capture_only":
                            snapshot["snapshot_only"] = True
                        elif state == "wrong_input":
                            snapshot["input_snapshot_type"] = "open"
                        output.write_text(json.dumps(report), encoding="utf-8")
                        raise SystemExit(1 if state == "failed" else 0)
                    output.write_text(json.dumps({"ok": True}), encoding="utf-8")
                    """
                ),
                encoding="utf-8",
            )
            fake_python.chmod(fake_python.stat().st_mode | stat.S_IXUSR)
            fake_relayctl = temp / "relayctl"
            fake_relayctl.write_text(
                '#!/bin/sh\nprintf "relayctl %s\\n" "$*" >> "$PIPELINE_CALLS"\nprintf "{}\\n"\n',
                encoding="utf-8",
            )
            fake_relayctl.chmod(fake_relayctl.stat().st_mode | stat.S_IXUSR)
            # Never let the integration harness build or run the real financial publisher.
            os.utime(fake_relayctl, (4102444800, 4102444800))
            env = {
                **os.environ,
                "RELAY_PYTHON_BIN": str(fake_python),
                "RELAY_JOB_REPORT_DIR": str(temp / "reports"),
                "RELAY_PERFORMANCE_LOCK": str(temp / "performance.lock"),
                "RELAY_PERFORMANCE_ACCOUNT_IDS": accounts,
                "RELAYCTL_BIN": str(fake_relayctl),
                "PIPELINE_CALLS": str(calls),
                "PIPELINE_CAPTURE_STATE": capture_state,
                "PIPELINE_SETTLEMENT_STATE": settlement_state,
            }
            result = subprocess.run(
                [str(PIPELINE)],
                cwd=ROOT,
                env=env,
                check=False,
                capture_output=True,
                text=True,
            )
            return result, calls.read_text(encoding="utf-8") if calls.exists() else ""

    def test_runs_performance_after_successful_settlement(self) -> None:
        result, calls = self.run_pipeline("ready")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("relay.jobs.post_close_capture", calls)
        self.assertIn("relay.jobs.post_close_settlement", calls)
        self.assertIn("relay.jobs.performance_daily", calls)
        self.assertIn("--target-date 20260803", calls)
        self.assertIn("--trigger post_close_success", calls)
        self.assertIn("--refresh-timeout-seconds 180", calls)
        self.assertIn("--settlement-timeout-seconds 60", calls)
        self.assertIn("--trigger post_close_capture_success", calls)
        self.assertIn("--skip-refresh", calls)
        self.assertIn("--account-id acct-1 --account-id acct-2", calls)
        self.assertIn("relayctl performance-rebuild", calls)
        self.assertIn("-accounts acct-1,acct-2", calls)

    def test_only_captured_configured_accounts_are_published(self) -> None:
        result, calls = self.run_pipeline("ready", accounts="acct-2,not-captured")
        self.assertEqual(result.returncode, 0, result.stderr)
        publisher = next(line for line in calls.splitlines() if line.startswith("relayctl "))
        self.assertIn("-accounts acct-2 ", publisher)
        self.assertNotIn("acct-1", publisher)
        self.assertNotIn("not-captured", publisher)

    def test_does_not_run_settlement_after_failed_capture(self) -> None:
        result, calls = self.run_pipeline("failed")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("relay.jobs.post_close_settlement", calls)
        self.assertNotIn("relay.jobs.performance_daily", calls)

    def test_does_not_run_settlement_without_successful_broker_snapshot(self) -> None:
        result, calls = self.run_pipeline("incomplete")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("relay.jobs.post_close_settlement", calls)
        self.assertNotIn("relay.jobs.performance_daily", calls)

    def test_does_not_run_performance_on_non_trading_day(self) -> None:
        result, calls = self.run_pipeline("skipped")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("relay.jobs.post_close_settlement", calls)
        self.assertNotIn("relay.jobs.performance_daily", calls)

    def test_keeps_broker_snapshot_when_settlement_is_deferred(self) -> None:
        result, calls = self.run_pipeline("ready", "failed")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("relay.jobs.post_close_capture", calls)
        self.assertIn("relay.jobs.post_close_settlement", calls)
        self.assertNotIn("relay.jobs.performance_daily", calls)
        self.assertIn("broker close snapshot remains available", result.stderr)

    def test_account_settlement_failures_do_not_block_healthy_empty_account(self) -> None:
        for state in ("partial", "partial_positions"):
            with self.subTest(state=state):
                result, calls = self.run_pipeline("ready", state)
                self.assertEqual(result.returncode, 0, result.stderr)
                publisher = next(line for line in calls.splitlines() if line.startswith("relayctl "))
                self.assertIn("-accounts acct-1 ", publisher)
                self.assertNotIn("acct-2", publisher)
                quality = next(line for line in calls.splitlines() if "relay.jobs.performance_daily" in line)
                self.assertNotIn("acct-2", quality)
                self.assertIn("settlement incomplete for accounts: acct-2", result.stderr)

    def test_invalid_settlement_evidence_cannot_publish(self) -> None:
        for state in ("all_failed", "old_date", "duplicate", "missing_counts", "global_error", "dry_run", "capture_only", "wrong_input"):
            with self.subTest(state=state):
                result, calls = self.run_pipeline("ready", state)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("relayctl ", calls)
                self.assertNotIn("relay.jobs.performance_daily", calls)

    def test_settlement_cannot_add_account_not_captured_or_publish_twice(self) -> None:
        result, calls = self.run_pipeline("ready", "unknown_account", "acct-1,acct-1,not-captured")
        self.assertEqual(result.returncode, 0, result.stderr)
        publisher = next(line for line in calls.splitlines() if line.startswith("relayctl "))
        self.assertIn("-accounts acct-1 ", publisher)
        self.assertNotIn("not-captured", publisher)
        self.assertNotIn("acct-1,acct-1", publisher)

    def test_incomplete_capture_cannot_be_restored_by_later_settlement_report(self) -> None:
        result, calls = self.run_pipeline("partial_positions")
        self.assertEqual(result.returncode, 0, result.stderr)
        settlement = next(line for line in calls.splitlines() if "relay.jobs.post_close_settlement" in line)
        publisher = next(line for line in calls.splitlines() if line.startswith("relayctl "))
        self.assertNotIn("acct-2", settlement)
        self.assertNotIn("acct-2", publisher)

    def test_wrong_capture_date_stops_before_settlement(self) -> None:
        result, calls = self.run_pipeline("old_date")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("relay.jobs.post_close_settlement", calls)

    def test_no_configured_successful_account_does_not_publish(self) -> None:
        result, calls = self.run_pipeline("ready", "partial", "acct-2")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("relayctl ", calls)
        self.assertNotIn("relay.jobs.performance_daily", calls)
        self.assertIn("performance deferred for acct-2", result.stderr)


if __name__ == "__main__":
    unittest.main()
