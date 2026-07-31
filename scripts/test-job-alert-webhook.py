#!/usr/bin/env python3
"""Send a credential-safe relay.alert.v1 channel test."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO_ROOT / "src"))

from relay.jobs.alerts import AlertConfig, send_test_alert  # noqa: E402


def main() -> int:
    parser = argparse.ArgumentParser(description="Test the configured relay job-alert webhook")
    parser.add_argument("--config", default="", help="alert env file; defaults to config/relay.alerts.env")
    args = parser.parse_args()
    try:
        config = AlertConfig.load(config_path=args.config or None)
        result = send_test_alert(config=config)
    except Exception as exc:  # noqa: BLE001 - command must return a concise, credential-safe failure.
        result = {"status": "failed", "error": f"{type(exc).__name__}: {exc}"[:1000]}
    print(json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if result.get("status") == "delivered" else 1


if __name__ == "__main__":
    raise SystemExit(main())
