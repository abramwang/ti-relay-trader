from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "src"))

from relay.jobs.alerts import AlertConfig, dispatch_daily_job_alert, send_test_alert  # noqa: E402
from relay.jobs import common  # noqa: E402


class FakeResponse:
    def __init__(self, status: int) -> None:
        self.status = status

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, traceback):
        return False

    def getcode(self) -> int:
        return self.status

    def read(self, _limit: int = -1) -> bytes:
        return b"{}"


class FakeOpener:
    def __init__(self, statuses: list[int]) -> None:
        self.statuses = list(statuses)
        self.requests = []

    def open(self, webhook_request, timeout: float):
        self.requests.append((webhook_request, timeout))
        return FakeResponse(self.statuses.pop(0))


def base_report() -> dict:
    return {
        "ok": True,
        "job": "post_close_settlement",
        "trigger": "cron",
        "base_url": "http://relay-trader.quantstage.com",
        "started_at": "2026-07-31T15:01:00+08:00",
        "finished_at": "2026-07-31T15:01:45+08:00",
        "trading_day": {
            "requested_date": "20260731",
            "target_trade_date": "20260731",
            "is_trading_day": True,
        },
        "errors": [],
        "accounts": [],
    }


class JobAlertTest(unittest.TestCase):
    def test_successful_job_does_not_require_alert(self) -> None:
        delivery = dispatch_daily_job_alert(base_report(), config=AlertConfig())

        self.assertFalse(delivery["required"])
        self.assertEqual(delivery["status"], "not_required")

    def test_non_trading_day_skip_does_not_require_alert(self) -> None:
        report = base_report()
        report.update({"skipped": True, "skip_reason": "not an A-share trading day"})

        delivery = dispatch_daily_job_alert(report, config=AlertConfig())

        self.assertEqual(delivery["status"], "not_required")
        self.assertEqual(delivery["reason"], "non_trading_day_or_normal_skip")

    def test_dry_run_alert_is_suppressed(self) -> None:
        report = base_report()
        report.update({"ok": False, "dry_run": True, "errors": ["test failure"]})

        delivery = dispatch_daily_job_alert(report, config=AlertConfig(enabled=True, webhook_url="http://unused"))

        self.assertEqual(delivery["status"], "suppressed")
        self.assertEqual(delivery["reason"], "dry_run")

    def test_blocked_account_builds_critical_aggregated_alert(self) -> None:
        report = base_report()
        report["snapshot_blocked_accounts"] = ["acct-2"]
        report["account_error_count"] = 1
        report["account_errors"] = [{"account_id": "acct-2", "errors": ["positions stale"]}]
        report["accounts"] = [
            {
                "account_id": "acct-2",
                "refresh_freshness": {"ok": False, "timed_out": True},
            }
        ]
        opener = FakeOpener([500, 204])
        config = AlertConfig(
            enabled=True,
            environment="production",
            webhook_url="https://alerts.example/relay",
            webhook_token="secret-token",
            max_attempts=3,
        )

        delivery = dispatch_daily_job_alert(report, config=config, opener=opener, sleep=lambda _seconds: None)

        self.assertEqual(delivery["status"], "delivered")
        self.assertEqual(delivery["severity"], "critical")
        self.assertEqual(delivery["attempts"], 2)
        self.assertEqual(
            delivery["issue_types"],
            ["account_errors", "snapshot_blocked", "refresh_timeout"],
        )
        webhook_request, timeout = opener.requests[-1]
        payload = json.loads(webhook_request.data.decode("utf-8"))
        self.assertEqual(payload["schema_version"], "relay.alert.v1")
        self.assertEqual(payload["accounts"], ["acct-2"])
        self.assertEqual(payload["job"]["trade_date"], "20260731")
        self.assertEqual(webhook_request.get_header("Authorization"), "Bearer secret-token")
        self.assertEqual(webhook_request.get_header("Idempotency-key"), payload["dedupe_key"])
        self.assertEqual(timeout, 5.0)
        self.assertNotIn("webhook_url", delivery)
        self.assertNotIn("secret-token", json.dumps(delivery))

    def test_required_alert_is_explicit_when_channel_disabled(self) -> None:
        report = base_report()
        report.update({"ok": False, "errors": ["database unavailable"]})

        delivery = dispatch_daily_job_alert(report, config=AlertConfig(environment="production"))

        self.assertTrue(delivery["required"])
        self.assertFalse(delivery["configured"])
        self.assertEqual(delivery["status"], "disabled")
        self.assertEqual(delivery["issue_types"], ["task_failed"])

    def test_snapshot_result_account_failure_is_alerted_as_blocked(self) -> None:
        report = base_report()
        report["settlement_snapshot"] = {
            "ok": True,
            "result": {
                "accounts": [
                    {
                        "account_id": "acct-3",
                        "asset_snapshot_written": False,
                        "errors": ["asset snapshot not found"],
                    }
                ]
            },
        }

        delivery = dispatch_daily_job_alert(report, config=AlertConfig())

        self.assertEqual(delivery["severity"], "critical")
        self.assertEqual(delivery["issue_types"], ["account_errors", "snapshot_blocked"])

    def test_main_persists_alert_delivery_into_same_job_run(self) -> None:
        options = common.JobOptions(job_name="post_close_settlement", persist=True, trigger="cron")
        calls = []
        emitted = {}

        class FakeJobClient:
            def record_job_run(self, report, **kwargs):
                calls.append((json.loads(json.dumps(report)), dict(kwargs)))
                return {"run_id": "job-run-1"}

        client = FakeJobClient()
        with (
            mock.patch.object(common, "parse_args", return_value=options),
            mock.patch.object(common, "RelayClient", return_value=client),
            mock.patch.object(
                common,
                "dispatch_daily_job_alert",
                return_value={"required": True, "configured": True, "status": "delivered"},
            ),
            mock.patch.object(common, "emit_report", side_effect=lambda report, _options: emitted.update(report)),
        ):
            with self.assertRaises(SystemExit) as raised:
                common.main_for("post_close_settlement", "test", lambda _options: base_report())

        self.assertEqual(raised.exception.code, 0)
        self.assertEqual(len(calls), 2)
        self.assertNotIn("alert_delivery", calls[0][0])
        self.assertEqual(calls[1][1]["run_id"], "job-run-1")
        self.assertEqual(calls[1][0]["alert_delivery"]["status"], "delivered")
        self.assertTrue(emitted["persistence"]["final_report_saved"])

    def test_alert_config_loads_untracked_env_file_with_environment_override(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "relay.alerts.env"
            path.write_text(
                "\n".join(
                    [
                        "RELAY_ALERT_ENABLED=true",
                        "RELAY_ALERT_ENVIRONMENT=test",
                        "RELAY_ALERT_WEBHOOK_URL='https://alerts.example/relay'",
                        "RELAY_ALERT_TIMEOUT_SECONDS=7",
                    ]
                ),
                encoding="utf-8",
            )

            config = AlertConfig.load(
                config_path=path,
                environ={"RELAY_ALERT_ENVIRONMENT": "production"},
            )

        self.assertTrue(config.enabled)
        self.assertEqual(config.environment, "production")
        self.assertEqual(config.webhook_url, "https://alerts.example/relay")
        self.assertEqual(config.timeout_seconds, 7.0)

    def test_channel_test_uses_same_transport_without_business_report(self) -> None:
        opener = FakeOpener([204])
        config = AlertConfig(
            enabled=True,
            environment="production",
            public_url="http://relay-trader.quantstage.com",
            webhook_url="https://alerts.example/relay",
        )

        result = send_test_alert(config=config, opener=opener, sleep=lambda _seconds: None)

        self.assertEqual(result["status"], "delivered")
        payload = json.loads(opener.requests[0][0].data.decode("utf-8"))
        self.assertEqual(payload["alert_type"], "webhook_test")
        self.assertEqual(payload["severity"], "info")
        self.assertEqual(payload["links"]["jobs"], "http://relay-trader.quantstage.com/jobs")

    def test_invalid_alert_config_does_not_abort_business_job(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "relay.alerts.env"
            path.write_text("UNSUPPORTED_SECRET=value\n", encoding="utf-8")

            with mock.patch.dict("os.environ", {"RELAY_ALERT_CONFIG_PATH": str(path)}, clear=True):
                delivery = dispatch_daily_job_alert(base_report())

        self.assertEqual(delivery["status"], "misconfigured")
        self.assertFalse(delivery["required"])


if __name__ == "__main__":
    unittest.main()
