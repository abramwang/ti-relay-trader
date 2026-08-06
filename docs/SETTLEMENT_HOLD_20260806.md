# 2026-08-06 Settlement Hold

## Decision

The `2026-08-06` production post-close settlement is intentionally deferred.
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
did not issue the normal 15:01 broker queries. The following inputs are not
authoritative close data and must remain blocked:

| Input | Latest available source time | Status |
| --- | --- | --- |
| Orders, fills, ETF transfers | 14:37:04 or earlier by account | Complete; user confirmed no later orders |
| Asset and positions: `501000114077` | approximately 13:57 | Not an authoritative close query |
| Asset and positions: `314000046830` | approximately 13:57 | Not an authoritative close query |
| Asset and positions: other four accounts | approximately 09:01 | Open data only |
| Broker order fees | no `fee.list.query` on 2026-08-06 | Missing |
| Meridian settlement market data | reported incomplete | Awaiting upstream completion |

The database contains six `open` asset snapshots, 233 `open` position
snapshots, and six intraday asset records for the date. It contains no close
asset snapshot, no close position snapshot, no reconciliation run, and no NAV
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

## Resume Gate

Do not run `post_close_settlement --target-date 20260806` merely because
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

