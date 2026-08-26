from __future__ import annotations

import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "run-canonical-performance.sh"


class CanonicalPerformanceScriptTest(unittest.TestCase):
    def run_script(self, *, complete: bool, temp: Path) -> subprocess.CompletedProcess[str]:
        fake_python = temp / "python"
        if not fake_python.exists():
            fake_python.write_text(
                textwrap.dedent(
                    """\
                    #!/usr/bin/python3
                    import json
                    import os
                    from pathlib import Path
                    import sys

                    calls = Path(os.environ["CANONICAL_CALLS"])
                    with calls.open("a", encoding="utf-8") as handle:
                        handle.write(" ".join(sys.argv[1:]) + "\\n")
                    if len(sys.argv) > 1 and sys.argv[1] == "-":
                        sys.argv = sys.argv[1:]
                        exec(compile(sys.stdin.read(), "<stdin>", "exec"), {"__name__": "__main__"})
                    output = Path(sys.argv[sys.argv.index("--output") + 1])
                    output.parent.mkdir(parents=True, exist_ok=True)
                    output.write_text(json.dumps({
                        "ok": True,
                        "canonical_completed": os.environ["CANONICAL_COMPLETE"] == "true",
                    }), encoding="utf-8")
                    """
                ),
                encoding="utf-8",
            )
            fake_python.chmod(fake_python.stat().st_mode | stat.S_IXUSR)
        env = {
            **os.environ,
            "RELAY_PYTHON_BIN": str(fake_python),
            "RELAY_JOB_REPORT_DIR": str(temp / "reports"),
            "RELAY_JOB_STATE_DIR": str(temp / "state"),
            "RELAY_PERFORMANCE_ACCOUNT_IDS": "acct-1,acct-2",
            "RELAY_TARGET_DATE": "20260826",
            "CANONICAL_COMPLETE": "true" if complete else "false",
            "CANONICAL_CALLS": str(temp / "calls.log"),
        }
        return subprocess.run(
            [str(SCRIPT)],
            cwd=ROOT,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )

    def test_waiting_run_does_not_create_completion_marker(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            temp = Path(temp_dir)
            result = self.run_script(complete=False, temp=temp)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertFalse((temp / "state" / "performance-canonical-20260826.done").exists())

    def test_completed_run_creates_marker_and_suppresses_retries(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            temp = Path(temp_dir)
            first = self.run_script(complete=True, temp=temp)
            self.assertEqual(first.returncode, 0, first.stderr)
            marker = temp / "state" / "performance-canonical-20260826.done"
            self.assertTrue(marker.exists())
            calls_before = (temp / "calls.log").read_text(encoding="utf-8").splitlines()

            second = self.run_script(complete=True, temp=temp)
            self.assertEqual(second.returncode, 0, second.stderr)
            calls_after = (temp / "calls.log").read_text(encoding="utf-8").splitlines()
            self.assertEqual(calls_after, calls_before)
            self.assertIn("already completed", second.stdout)


if __name__ == "__main__":
    unittest.main()
