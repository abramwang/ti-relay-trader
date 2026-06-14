"""Post-close settlement job for a relay trading day."""

from __future__ import annotations

from .common import main_for, run_post_close_settlement


def main() -> None:
    main_for(
        "post_close_settlement",
        "Run relay post-close settlement: final refresh, ledger snapshots, and settlement summary inputs.",
        run_post_close_settlement,
    )


if __name__ == "__main__":
    main()
