"""Pre-open initialization job for a relay trading day."""

from __future__ import annotations

from .common import main_for, run_pre_open_init


def main() -> None:
    main_for(
        "pre_open_init",
        "Run relay pre-open initialization: dependency check, trading-day resolution, refresh commands, and snapshots.",
        run_pre_open_init,
    )


if __name__ == "__main__":
    main()
