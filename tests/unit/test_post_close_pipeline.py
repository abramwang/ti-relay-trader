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
    def run_pipeline(self, state: str) -> tuple[subprocess.CompletedProcess[str], str]:
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
                    if "relay.jobs.post_close_settlement" in sys.argv:
                        state = os.environ["PIPELINE_POST_STATE"]
                        report = {
                            "ok": state != "failed",
                            "skipped": state == "skipped",
                            "trading_day": {"target_trade_date": "20260803"},
                            "settlement_snapshot": {"ok": state == "ready"},
                        }
                        output.write_text(json.dumps(report), encoding="utf-8")
                        raise SystemExit(1 if state == "failed" else 0)
                    output.write_text(json.dumps({"ok": True}), encoding="utf-8")
                    """
                ),
                encoding="utf-8",
            )
            fake_python.chmod(fake_python.stat().st_mode | stat.S_IXUSR)
            env = {
                **os.environ,
                "RELAY_PYTHON_BIN": str(fake_python),
                "RELAY_JOB_REPORT_DIR": str(temp / "reports"),
                "RELAY_PERFORMANCE_LOCK": str(temp / "performance.lock"),
                "RELAY_PERFORMANCE_ACCOUNT_IDS": "acct-1,acct-2",
                "PIPELINE_CALLS": str(calls),
                "PIPELINE_POST_STATE": state,
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
        self.assertIn("relay.jobs.post_close_settlement", calls)
        self.assertIn("relay.jobs.performance_daily", calls)
        self.assertIn("--target-date 20260803", calls)
        self.assertIn("--trigger post_close_success", calls)
        self.assertIn("--settlement-timeout-seconds 60", calls)

    def test_does_not_run_performance_after_failed_settlement(self) -> None:
        result, calls = self.run_pipeline("failed")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("relay.jobs.performance_daily", calls)

    def test_does_not_run_performance_without_successful_close_snapshot(self) -> None:
        result, calls = self.run_pipeline("incomplete")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("relay.jobs.performance_daily", calls)

    def test_does_not_run_performance_on_non_trading_day(self) -> None:
        result, calls = self.run_pipeline("skipped")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("relay.jobs.performance_daily", calls)


if __name__ == "__main__":
    unittest.main()
