"""Broker-only post-close account capture entrypoint."""

from .common import main_for, run_post_close_capture


def main() -> None:
    main_for(
        "post_close_capture",
        "Capture final broker account data without requiring Meridian",
        run_post_close_capture,
    )


if __name__ == "__main__":
    main()
