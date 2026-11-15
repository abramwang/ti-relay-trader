from __future__ import annotations

import os
from pathlib import Path
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "trading-jobs-cron.sh"


class TradingJobsCronScriptTest(unittest.TestCase):
    def test_installs_canonical_polling_after_meridian_parent_window(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            temp = Path(temp_dir)
            crontab_file = temp / "crontab.txt"
            fake_crontab = temp / "crontab"
            fake_crontab.write_text(
                textwrap.dedent(
                    """\
                    #!/usr/bin/env bash
                    set -euo pipefail
                    if [[ "${1:-}" == "-l" ]]; then
                      [[ -f "$FAKE_CRONTAB_FILE" ]] && cat "$FAKE_CRONTAB_FILE"
                      exit 0
                    fi
                    if [[ -n "${1:-}" ]]; then
                      cat "$1" > "$FAKE_CRONTAB_FILE.next"
                    else
                      cat > "$FAKE_CRONTAB_FILE.next"
                    fi
                    mv "$FAKE_CRONTAB_FILE.next" "$FAKE_CRONTAB_FILE"
                    """
                ),
                encoding="utf-8",
            )
            fake_crontab.chmod(fake_crontab.stat().st_mode | stat.S_IXUSR)
            env = {
                **os.environ,
                "PATH": f"{temp}:{os.environ['PATH']}",
                "FAKE_CRONTAB_FILE": str(crontab_file),
                "RELAY_CRON_LOG_DIR": str(temp / "logs"),
            }
            env.pop("RELAY_PERFORMANCE_ACCOUNT_IDS", None)
            unrelated = "# unrelated job\n5 2 * * * /bin/true\n"
            crontab_file.write_text(unrelated, encoding="utf-8")

            result = subprocess.run(
                [str(SCRIPT), "install"],
                cwd=ROOT,
                env=env,
                check=False,
                capture_output=True,
                text=True,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            installed = crontab_file.read_text(encoding="utf-8")
            self.assertIn("10,20,30,40,50 17 * * 1-5", installed)
            self.assertIn("*/10 18 * * 1-5", installed)
            self.assertIn("0,10 19 * * 1-5", installed)
            self.assertIn(
                "RELAY_PERFORMANCE_ACCOUNT_IDS=307000051387,307000051388,314000046830,314000045768\n",
                installed,
            )
            self.assertNotIn("40,50 16 * * 1-5", installed)
            self.assertTrue(installed.startswith(unrelated))
            self.assertIn("1 9 * * 1-5", installed)
            self.assertIn("1 15 * * 1-5", installed)
            self.assertNotIn("501000114077", installed)
            self.assertNotIn("307000051389", installed)

            following = "TZ=UTC\n17 2 * * * /bin/true\n"
            installed += following
            crontab_file.write_text(installed, encoding="utf-8")

            repeated = subprocess.run([str(SCRIPT), "install"], cwd=ROOT, env=env, capture_output=True, text=True)
            self.assertEqual(repeated.returncode, 0, repeated.stderr)
            self.assertEqual(crontab_file.read_text(encoding="utf-8"), installed)
            env["RELAY_PERFORMANCE_ACCOUNT_IDS"] = "custom-account"
            overridden = subprocess.run([str(SCRIPT), "install"], cwd=ROOT, env=env, capture_output=True, text=True)
            self.assertEqual(overridden.returncode, 0, overridden.stderr)
            updated = crontab_file.read_text(encoding="utf-8")
            self.assertTrue(updated.startswith(unrelated))
            self.assertIn("RELAY_PERFORMANCE_ACCOUNT_IDS=custom-account\n", updated)
            self.assertNotIn("314000045768", updated)
            self.assertTrue(updated.endswith(following))

            invalid = updated + "# RELAY_TRADER_CRON_BEGIN\n"
            crontab_file.write_text(invalid, encoding="utf-8")
            refused = subprocess.run([str(SCRIPT), "install"], cwd=ROOT, env=env, capture_output=True, text=True)
            self.assertNotEqual(refused.returncode, 0)
            self.assertEqual(crontab_file.read_text(encoding="utf-8"), invalid)


if __name__ == "__main__":
    unittest.main()
