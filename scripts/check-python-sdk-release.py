#!/usr/bin/env python3
"""Validate relay-sdk version metadata, archive contents, checksum, and tests."""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
import sys
import tarfile
import tomllib
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
SDK_DIR = REPO_ROOT / "sdk" / "python"
PUBLIC_SDK_DIR = REPO_ROOT / "public" / "sdk"


def main() -> None:
    parser = argparse.ArgumentParser(description="Check relay-sdk release artifacts")
    parser.add_argument("--build", action="store_true", help="rebuild tar.gz before checking")
    parser.add_argument("--live-smoke", action="store_true", help="also run read-only live smoke against 9092")
    parser.add_argument("--base-url", default="http://relay-trader.quantstage.com", help="relay base URL for live smoke")
    parser.add_argument("--account-id", default="", help="account id for live smoke")
    args = parser.parse_args()

    version = read_project_version()
    assert_version_consistency(version)
    if args.build:
        run([sys.executable, "scripts/build-python-sdk.py"])

    archive = PUBLIC_SDK_DIR / f"relay-sdk-{version}.tar.gz"
    checksum = PUBLIC_SDK_DIR / f"relay-sdk-{version}.tar.gz.sha256"
    require(archive.exists(), f"missing archive: {archive.relative_to(REPO_ROOT)}")
    require(checksum.exists(), f"missing checksum: {checksum.relative_to(REPO_ROOT)}")
    verify_checksum(archive, checksum)
    verify_archive_contents(archive, version)
    run_sdk_unit_tests()
    if args.live_smoke:
        cmd = [
            sys.executable,
            "tests/integration/sdk_live_smoke.py",
            "--base-url",
            args.base_url,
        ]
        if args.account_id:
            cmd.extend(["--account-id", args.account_id])
        run(cmd)

    print(f"relay-sdk {version} release check passed")


def read_project_version() -> str:
    project = tomllib.loads((SDK_DIR / "pyproject.toml").read_text(encoding="utf-8"))["project"]
    return str(project["version"])


def assert_version_consistency(version: str) -> None:
    init_text = (SDK_DIR / "relay_sdk" / "__init__.py").read_text(encoding="utf-8")
    client_text = (SDK_DIR / "relay_sdk" / "client.py").read_text(encoding="utf-8")
    init_version = match_assignment(init_text, "__version__")
    client_version = match_assignment(client_text, "SDK_VERSION")
    require(init_version == version, f"__version__ {init_version!r} != pyproject {version!r}")
    require(client_version == version, f"SDK_VERSION {client_version!r} != pyproject {version!r}")


def match_assignment(text: str, name: str) -> str:
    match = re.search(rf'^{name}\s*=\s*["\']([^"\']+)["\']', text, flags=re.MULTILINE)
    require(match is not None, f"missing {name} assignment")
    return match.group(1)


def verify_checksum(archive: Path, checksum: Path) -> None:
    expected = checksum.read_text(encoding="utf-8").split()[0]
    actual = hashlib.sha256(archive.read_bytes()).hexdigest()
    require(actual == expected, f"sha256 mismatch for {archive.name}: {actual} != {expected}")


def verify_archive_contents(archive: Path, version: str) -> None:
    package_root = f"relay_sdk-{version}"
    required = {
        f"{package_root}/pyproject.toml",
        f"{package_root}/README.md",
        f"{package_root}/relay_sdk/__init__.py",
        f"{package_root}/relay_sdk/client.py",
        f"{package_root}/relay_sdk/models.py",
        f"{package_root}/relay_sdk/errors.py",
        f"{package_root}/relay_sdk/streaming.py",
        f"{package_root}/tests/test_client.py",
    }
    with tarfile.open(archive, "r:gz") as tar:
        names = set(tar.getnames())
    missing = sorted(required - names)
    require(not missing, f"archive missing files: {missing}")
    forbidden = sorted(name for name in names if "__pycache__" in name or name.endswith((".pyc", ".pyo")))
    require(not forbidden, f"archive contains generated files: {forbidden[:5]}")


def run_sdk_unit_tests() -> None:
    env = os.environ.copy()
    env["PYTHONPATH"] = str(SDK_DIR)
    run([sys.executable, "-m", "unittest", "discover", "-s", "sdk/python/tests", "-v"], env=env)


def run(cmd: list[str], *, env: dict[str, str] | None = None) -> None:
    print("+", " ".join(cmd))
    subprocess.run(cmd, cwd=REPO_ROOT, env=env, check=True)


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


if __name__ == "__main__":
    main()
