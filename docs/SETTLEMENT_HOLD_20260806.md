# 2026-08-06 Settlement Hold And Recovery

## Recovery Status (2026-08-17)

The historical hold has been partially resolved for the three first-wave
performance accounts: `307000051387`, `307000051388`, and `314000046830`.
The original raw archive and hold evidence remain unchanged.

- Meridian now returns every required unadjusted `1d` bar for the three
  accounts: 199 distinct securities, 199 rows, zero missing close prices.
- The 2026-08-07 09:01 position snapshots were rolled back to 2026-08-06 only
  after a per-security bridge proved
  `open + ordinary fills + ETF redemption/transfer = next open`. All three
  accounts have zero quantity mismatches and zero residual quantity. Reverse
  repo instruments were correctly excluded from persistent positions.
- The next-open cash source for `307000051387/1388` was reduced by the two
  confirmed `73,448.939998 CNY` receipts attributed to the 2026-08-05 ETF
  redemption. `314000046830` had no such adjustment.
- Recovery inputs were first stored as `reconcile`, dry-run against Meridian,
  then promoted through the normal `broker_close -> close` API path. The
  resulting settlement contains three asset snapshots, 228 position rows,
  12 reconciliation inputs, zero account errors, and zero open breaks.
- `314000046830` performance is provisional and publishable. The two ETF T0
  accounts remain blocked by cash attribution residuals of `-74,811.87` and
  `-95,070.64 CNY`; those NAVs were not published.

The historical Level1 gap no longer blocks position valuation. ETF redemption
IOPV uses the last complete one-minute Meridian bar when Level1 is absent;
`159381.SZ` resolves to the 09:37 bar with `iopv=1.1382` and records
`minute_iopv_fallback`. Daily close bars are never substituted for redemption
time IOPV.

## Decision

The `2026-08-06` production post-close settlement was intentionally deferred.
Meridian's same-day market-data chain was reported incomplete before the
scheduled 15:01 Asia/Shanghai run. Relay removed the 15:01 cron entry before it
fired, did not write a close snapshot, and did not start performance
calculation. The normal cron entry was restored at 19:36 after today's trigger
time had passed, so future trading days remain scheduled normally.

The `/jobs` audit record is:

- job: `post_close_settlement`
- trade date: `2026-08-06`
- trigger: `manual_hold`
- status: `skipped`
- settlement deferred: `true`

## Preserved Trading Data

Relay's independent worker remained online for the full session and persisted
Redis Stream commands, replies, events, and heartbeats to PostgreSQL. The user
confirmed that no real-account orders were submitted after 14:37.

| Account | Orders | Terminal | Fills | ETF transfers | Last order/fill |
| --- | ---: | ---: | ---: | ---: | --- |
| `501000114077` | 306 | 306 | 328 | 198 | 14:20:54 |
| `314000046830` | 454 | 454 | 420 | 0 | 14:21:24 |
| `314000045768` | 959 | 959 | 1,321 | 297 | 14:37:04 |
| `307000051388` | 685 | 685 | 709 | 50 | 09:59:36 |
| `307000051389` | 0 | 0 | 0 | 0 | - |
| `307000051387` | 681 | 681 | 739 | 50 | 13:03:57 |
| **Total** | **3,085** | **3,085** | **3,517** | **595** | **14:37:04** |

Raw archive evidence for the Asia/Shanghai calendar day:

- 42,675 `raw_stream_messages`
- 10,002 `order.event` messages
- 3,517 `fill.event` messages
- 595 `transfer.event` messages
- 27,959 heartbeat messages, last received at 15:30:00.097202
- 0 raw parse errors
- 24 output streams healthy, lag 0, pending DLQ 0 at final review

## Inputs Not Captured At Close

The Codex thread was interrupted until after OC had shut down. Relay therefore
did not issue the normal 15:01 broker queries. At the time of the hold, the
following inputs were not authoritative close data and therefore remained
blocked:

| Input | Latest available source time | Status |
| --- | --- | --- |
| Orders, fills, ETF transfers | 14:37:04 or earlier by account | Complete; user confirmed no later orders |
| Asset and positions: `501000114077` | approximately 13:57 | Not an authoritative close query |
| Asset and positions: `314000046830` | approximately 13:57 | Not an authoritative close query |
| Asset and positions: other four accounts | approximately 09:01 | Open data only |
| Broker order fees | no `fee.list.query` on 2026-08-06 | Missing |
| Meridian settlement market data | reported incomplete | Awaiting upstream completion |

At hold time the database contained six `open` asset snapshots, 233 `open`
position snapshots, and six intraday asset records for the date. It contained
no close asset snapshot, close position snapshot, reconciliation run, or NAV
version for `2026-08-06`.

## Backup

A PostgreSQL custom-format backup was captured after OC shutdown:

- archive: `outputs/backups/relay_trader_20260806T193310+0800.dump`
- manifest: `outputs/backups/relay_trader_20260806T193310+0800.manifest.json`
- archive bytes: `127814185`
- SHA256: `b3f256e1cfb6b833edcd893b949663abc1248fc02391a119699e0872eb9b04d8`
- checksum verification: passed
- database schema version: 24
- unvalidated constraints: 0

The backup directory is intentionally excluded from Git. This document records
the audit coordinates without storing credentials or raw production data.

## Original Resume Gate

This gate was applied before the 2026-08-17 audited recovery. Do not run
`post_close_settlement --target-date 20260806` merely because
Meridian becomes healthy. Resume only after all of these conditions are met:

1. Meridian confirms the required 2026-08-06 bars, benchmark, instrument, and
   ETF PCF inputs are complete.
2. An authoritative source for six-account close assets and positions is
   available and explicitly approved. Morning or 13:57 current tables must not
   be relabeled as close snapshots.
3. Actual 2026-08-06 broker fees are available, or the performance result is
   explicitly kept provisional with a documented fee gap.
4. A dry-run shows no account error, quantity bridge break, or Meridian input
   gap before close snapshots are written.
5. Settlement and performance are run separately so close snapshot quality can
   be reviewed before performance starts.

After this incident Relay split the future production flow into
`post_close_capture -> post_close_settlement -> performance_daily`.
`post_close_capture` writes immutable `broker_close` asset/position snapshots
without requiring Meridian; settlement later promotes those snapshots and does
not query OC again. It was not retroactive at deployment time; the three
audited historical `broker_close` snapshots described above were added later
through the explicit recovery path, not by replaying OC.
