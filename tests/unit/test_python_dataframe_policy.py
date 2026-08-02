from __future__ import annotations

import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CONSTRAINT = ROOT / "configs" / "constraints" / "quantstage-pandas.txt"
SDK_ROOT = ROOT / "sdk" / "python"


class PythonDataFramePolicyTest(unittest.TestCase):
    def test_pandas_production_constraint_is_exact(self) -> None:
        requirements = [
            line.strip()
            for line in CONSTRAINT.read_text(encoding="utf-8").splitlines()
            if line.strip() and not line.lstrip().startswith("#")
        ]
        self.assertEqual(requirements, ["pandas==2.3.3"])

    def test_public_sdk_keeps_dataframe_dependencies_optional(self) -> None:
        pyproject = (SDK_ROOT / "pyproject.toml").read_text(encoding="utf-8")
        dependency_match = re.search(r"(?m)^dependencies\s*=\s*\[(.*?)\]", pyproject, re.DOTALL)
        self.assertIsNotNone(dependency_match)
        self.assertNotIn("pandas", dependency_match.group(1).lower())

        imported_modules = []
        for source_path in (SDK_ROOT / "relay_sdk").rglob("*.py"):
            source = source_path.read_text(encoding="utf-8")
            if re.search(r"(?m)^\s*(?:from|import)\s+(?:pandas|numpy|pyarrow|polars)\b", source):
                imported_modules.append(str(source_path.relative_to(ROOT)))
        self.assertEqual(imported_modules, [])


if __name__ == "__main__":
    unittest.main()
