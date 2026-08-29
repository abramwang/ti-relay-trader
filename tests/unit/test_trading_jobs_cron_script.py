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
                    cat > "$FAKE_CRONTAB_FILE"
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
            self.assertIn("40,50 16 * * 1-5", installed)
            self.assertIn("*/10 17,18 * * 1-5", installed)
            self.assertNotIn("*/10 16,17 * * 1-5", installed)


if __name__ == "__main__":
    unittest.main()
