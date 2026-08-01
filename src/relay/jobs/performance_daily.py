"""Read-only daily performance calculation and quality-gate job."""

from __future__ import annotations

from .common import main_for, run_daily_performance


def main() -> None:
    main_for(
        "performance_daily",
        "Calculate account cost ledgers and economic NAVs after OC and Meridian data are complete.",
        run_daily_performance,
    )


if __name__ == "__main__":
    main()
