# Changelog

## 0.1.30 - 2026-09-02

### Added

- Added typed `OrderPage`, `FillPage`, and `PositionPage` responses that retain
  server `count`, `next_cursor`, normalized `query`, envelope `request_id`, and
  Asia/Shanghai response `time`.
- Added `list_orders_page()`, `list_fills_page()`, `get_positions_page()`, and
  bounded `iter_orders()`, `iter_fills()`, and `iter_positions()` helpers.
- Added explicit pagination failures for repeated cursors, server count
  mismatches, normalized-query drift, and `max_pages` exhaustion.
- Added boundary tests for 0, 1, 500, 501, and more than 1,000 records plus a
  production read-only multi-page smoke test.

### Compatibility

- Existing `list_orders()`, `list_fills()`, and `get_positions()` keep their
  single-page list return type and existing default page sizes.
- No HTTP API or Redis Stream wire schema changed in this release.
- The SSE reconnect and gap-recovery contract remains pending; consumers must
  still treat the current event stream as a wake-up signal rather than a
  replayable ledger.

### Upgrade

No code change is required for existing callers. Callers that need proven full
ledger coverage should migrate from a legacy list method to its `iter_*()`
counterpart and leave `max_items=None`.
