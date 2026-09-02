# Changelog

## 0.1.31 - 2026-09-02

### Added

- Added process-scoped monotonic SSE cursors, a 2,048-event replay window, and
  `Last-Event-ID` resume support.
- Added explicit `relay.gap` events for API restarts, expired or invalid
  cursors, slow consumers, and PostgreSQL event-bridge reconnections.
- Added `RelayEvent.event_id`, `stream`, and `last_stream_id` fields.
- Added `stream_events_resilient()` with bounded exponential reconnects,
  configurable idle timeout, duplicate suppression, out-of-order detection,
  and mandatory full current-ledger reconciliation.
- Added `reconcile_current_state()` and typed `StreamReconciliation` snapshots
  covering asset, positions, orders, and fills.

### Changed

- Built-in order and fill callbacks now query every cursor page instead of a
  single 100-row page.
- A reconnect never silently resumes callbacks: callers must provide a
  reconciliation callback, while built-in watchers perform the reconciliation
  internally.

### Compatibility

- `stream_events()` remains a single-connection iterator and now accepts
  optional `last_event_id` and `idle_timeout` arguments.
- No Redis Stream or OC wire schema changed. Replay is intentionally scoped to
  one API process; a process restart produces an explicit gap instead of a
  false replay guarantee.

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
