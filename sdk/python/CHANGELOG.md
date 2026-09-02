# Changelog

## 0.1.32 - 2026-09-02

### Added

- Added machine-readable `/v1/schema` capabilities and typed `SchemaCatalog`
  compatibility checks for `relay.trading.v1alpha1`.
- Added the public `opener=` injection point for deterministic HTTP fault
  testing without private SDK method overrides.
- Added `BatchCommandReceipt`, per-child Relay acceptance/replay identities,
  and asynchronous `get_batch_order_outcomes()` / `wait_batch_order_outcomes()`
  resolution from command replies and the complete order ledger.
- Added `RetryDecision` and `retry_decision()` with operation-specific,
  fail-closed guidance for reads, queries, writes, cancels, and streams.
- Added `origin_message_id` order filtering so all children of one batch can be
  recovered without guessing from timestamps or symbols.

### Changed

- Replaced the query-only `/v1/query-status/{message_id}` name and
  `get_query_status()` helper with the generic
  `/v1/command-status/{message_id}` and `get_command_status()` contract. The
  old route is intentionally removed because it had no external consumers.
- `Order` now exposes command IDs, rejection code, and adapter context needed
  for batch result auditing while still retaining the complete raw payload.
- Relay now preserves the first submit command identity on every order and
  backfills historical identities from the raw command archive, so batch
  children remain queryable after later order queries and events.
- Documented and tested the JSON-number price round trip used by Chronos:
  `Decimal(str(value))`, half-up integer micro-units, and Meridian's
  authoritative minimum price tick.

### Retry Safety

- Read and query transport failures may use bounded retry.
- Write or cancel transport failures, cancel rejection/timeouts, and
  `COMMAND_OUTCOME_UNKNOWN` require reconciliation and are not automatically
  retried.
- Idempotency conflicts and business rejections are never automatically
  retried.

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
