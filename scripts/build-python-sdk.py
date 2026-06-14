#!/usr/bin/env python3
"""Build the relay Python SDK source archive served by the docs portal."""

from __future__ import annotations

import argparse
import hashlib
import tarfile
import tomllib
from pathlib import Path


def main() -> None:
    parser = argparse.ArgumentParser(description="Build relay-sdk source tar.gz")
    parser.add_argument("--sdk-dir", default="sdk/python", help="Python SDK source directory")
    parser.add_argument("--out-dir", default="public/sdk", help="Output directory for archives")
    args = parser.parse_args()

    repo_root = Path(__file__).resolve().parents[1]
    sdk_dir = (repo_root / args.sdk_dir).resolve()
    out_dir = (repo_root / args.out_dir).resolve()
    pyproject_path = sdk_dir / "pyproject.toml"
    project = tomllib.loads(pyproject_path.read_text(encoding="utf-8"))["project"]
    name = project["name"]
    version = project["version"]

    out_dir.mkdir(parents=True, exist_ok=True)
    archive_name = f"{name}-{version}.tar.gz"
    archive_path = out_dir / archive_name
    package_root = f"{name.replace('-', '_')}-{version}"

    files = list(iter_source_files(sdk_dir))
    with tarfile.open(archive_path, "w:gz", format=tarfile.PAX_FORMAT) as archive:
        for path in files:
            rel = path.relative_to(sdk_dir)
            add_file(archive, path, str(Path(package_root) / rel))

    digest = hashlib.sha256(archive_path.read_bytes()).hexdigest()
    checksum_path = archive_path.with_suffix(archive_path.suffix + ".sha256")
    checksum_path.write_text(f"{digest}  {archive_name}\n", encoding="utf-8")
    print(archive_path.relative_to(repo_root))
    print(checksum_path.relative_to(repo_root))


def iter_source_files(sdk_dir: Path):
    include_roots = [
        sdk_dir / "pyproject.toml",
        sdk_dir / "README.md",
    ]
    for path in include_roots:
        if path.exists():
            yield path
    for folder in ("relay_sdk", "tests"):
        root = sdk_dir / folder
        if not root.exists():
            continue
        for path in sorted(root.rglob("*")):
            if path.is_dir():
                continue
            if "__pycache__" in path.parts:
                continue
            if path.suffix in {".pyc", ".pyo"}:
                continue
            yield path


def add_file(archive: tarfile.TarFile, path: Path, arcname: str) -> None:
    info = archive.gettarinfo(str(path), arcname)
    info.uid = 0
    info.gid = 0
    info.uname = ""
    info.gname = ""
    info.mtime = 0
    with path.open("rb") as file_obj:
        archive.addfile(info, file_obj)


if __name__ == "__main__":
    main()
