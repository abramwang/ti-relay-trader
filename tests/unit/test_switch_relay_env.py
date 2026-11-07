import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class SwitchEnvironmentTest(unittest.TestCase):
    def invoke(self, extra=(), trading=False):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "scripts").mkdir()
            (root / "config").mkdir()
            (root / "bin").mkdir()
            shutil.copy2(Path(__file__).resolve().parents[2] / "scripts/switch-relay-env.sh", root / "scripts/switch-relay-env.sh")
            (root / "config/relay.prod.yaml").write_text(
                'service:\n  environment: "production"\naccounts:\n  - trading_enabled: ' + str(trading).lower() + '\n'
            )
            programs = {
                "scripts/relay-runtime-service.sh": '#!/bin/sh\nprintf "runtime:%s\\n" "$1" >> "$TRACE"\n',
                "bin/go": '#!/bin/sh\nprintf "migration\\n" >> "$TRACE"\n',
                "bin/curl": '#!/bin/sh\nprintf \'{"data":{"environment":"production","status":"ok","accounts":{"trading_enabled":0}}}\\n\'\n',
            }
            for name, body in programs.items():
                path = root / name
                path.write_text(body)
                path.chmod(0o755)
            trace = root / "trace"
            env = dict(os.environ, PATH=str(root / "bin") + os.pathsep + os.environ["PATH"], TRACE=str(trace))
            result = subprocess.run(["bash", str(root / "scripts/switch-relay-env.sh"), "production", *extra], env=env, capture_output=True, text=True, timeout=10)
            actions = trace.read_text().splitlines() if trace.exists() else []
            selected = (root / ".runtime/active-config.yaml").is_symlink()
            return result, actions, selected

    def test_explicit_skip_preserves_schema(self):
        result, actions, selected = self.invoke(["--skip-migrations"])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(actions, ["runtime:stop", "runtime:start", "runtime:status"])
        self.assertTrue(selected)

    def test_default_still_migrates_before_stop(self):
        result, actions, selected = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(actions, ["migration", "runtime:stop", "runtime:start", "runtime:status"])
        self.assertTrue(selected)

    def test_skip_does_not_bypass_trading_guard(self):
        result, actions, selected = self.invoke(["--skip-migrations"], trading=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(actions, [])
        self.assertFalse(selected)


if __name__ == "__main__":
    unittest.main()
