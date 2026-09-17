# Changelog

## 0.1.38 - 2026-09-17

- Expose TEST counter rejection-circuit evidence through typed readiness fields:
  `order_entry_cooldown`, `last_issue_code`, `last_issue_message`, and
  `last_issue_at`.
- `verify_ready(force=True)` now fails closed during Relay's five-minute
  `TEST_COUNTER_STATE_REJECTED_COOLDOWN` after the Huaxin 7x24 counter reports
  the global state rejection `BROKER_REJECTED / 当前状态禁止此项操作`.

### Safety

- The rejection circuit is limited to the configured TEST 7x24 counter and the
  exact global-state rejection. It does not alter production readiness or
  classify ordinary order-specific business rejections as account outages.

## 0.1.37 - 2026-09-16

- Add typed, cursor-paginated cancel-attempt ledger access through
  `list_cancel_attempts_page()`, `iter_cancel_attempt_pages()`, and
  `iter_cancel_attempts()`.
- Preserve rejection codes, retry guidance, reconciliation flags, command
  identities, and authoritative occurrence time for offline reconstruction.
- Relay now withholds a `filled` projection until its quantities are closed;
  consumers no longer observe `filled` with nonzero leaves or incomplete
  cumulative quantity.

### Safety

- `ORDER_NOT_FOUND` remains rejection evidence and does not change the order's
  state. Cross-day TEST resolution remains fail-closed until OC exposes a
  stable counter-session identity.

## 0.1.36 - 2026-09-15

- Add typed account order-entry readiness with `get_account_readiness()` and
  fail-closed `verify_ready()` helpers.
- Expose configured test-counter windows separately from OC heartbeat health;
  production trading-session behavior is unchanged.

## 0.1.35 - 2026-09-14

- Add `Asset.reverse_repo_receivable`; enriched current assets and standard close snapshots now include reverse-repo principal once in `net_asset`, bounded by the asset snapshot capture time.

## 0.1.34 - 2026-09-14

### Fixed

- `submit_order()`, `submit_orders()`, and `cancel_order()` now require the
  successful typed receipt action to be `order.submit`,
  `order.batch.submit`, and `order.cancel`, respectively.
- Missing or mismatched write receipt actions raise
  `RelayCommandOutcomeUnknownError` with
  `WRITE_RECEIPT_ACTION_MISMATCH`; callers must reconcile the ledger and must
  not automatically repeat the command.
- Idempotent write replays retain the original command message, stream,
  request, account, and caller order identities. Replayed cancels no longer
  publish a second Redis command, including after the order becomes terminal.

### Compatibility

- Request paths, request payloads, Redis Streams, OC wire schema, and order
  state transitions are unchanged.

## 0.1.33 - 2026-09-02

### Added

- Added public `iter_order_pages()`, `iter_fill_pages()`, and
  `iter_position_pages()` helpers so consumers can retain every page's
  `request_id`, `time`, normalized `query`, `count`, and `next_cursor` while
  reading a complete ledger.
- Added the typed page collections `order_pages`, `fill_pages`, and
  `position_pages` to `StreamReconciliation`, preserving the complete audit
  evidence used to rebuild current state after an SSE gap.

### Compatibility

- Existing `iter_orders()`, `iter_fills()`, and `iter_positions()` remain
  business-object iterators and retain their existing signatures.
- Page and item iterators share the same count, query-drift, repeated-cursor,
  empty-cursor, and page-limit validation path.
- No Relay HTTP API, Redis Stream, or OC wire schema changed in this release.

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
