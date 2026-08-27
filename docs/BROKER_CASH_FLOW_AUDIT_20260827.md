# 2026-08-27 Broker Cash Flow One-Time Audit

## Purpose And Boundary

This audit uses two broker-exported cash-flow files to correct the settlement
interpretation for `307000051387` and `307000051388`. It is a one-time repair
of known historical evidence, not a recurring import path. Relay's daily source
of truth remains OC current-day orders, fills, fees, transfers, assets, and
positions together with Meridian market data.

Broker exports must not create synthetic OC orders, fills, or per-order fees.
The original Redis messages, OC ledger, prior NAV versions, and voided cash
entries remain available for audit.

## Source Assessment

| Account | File | Rows | Date range | Unique flow IDs | SHA256 |
| --- | --- | ---: | --- | ---: | --- |
| `307000051387` | `307000051387_资金流水.csv` | 17,378 | 2026-07-27..2026-08-26 | 17,378 | `90fdda12406fdfef226a9d456d11f9ed3859dc4208a5a8b30b98407bdb1acc3e` |
| `307000051388` | `307000051388_资金流水.csv` | 18,048 | 2026-07-27..2026-08-26 | 18,048 | `5cff751b98dc22a619b5ae88f06c8a4875d7d42169359d304732e07d0655d351` |

Both files use UTF-8 BOM. Their header contains 14 cells while every data row
contains 13 cells. The last observed cell is the operation summary; no usable
post-flow balance is present. The files can prove cash-event amount, time,
subject, security, and stable flow ID, but cannot independently reconstruct an
exact account asset snapshot or frozen cash balance.

## Authoritative ETF Cash Events

| Account | Broker flow ID | Effective time, Asia/Shanghai | Source trade date | Amount CNY |
| --- | --- | --- | --- | ---: |
| `307000051387` | `2652005201` | 2026-08-07 18:53:57 | 2026-08-05 | 53,520.64 |
| `307000051388` | `2652032786` | 2026-08-07 18:54:01 | 2026-08-05 | 53,520.64 |
| `307000051387` | `2654325649` | 2026-08-10 19:26:06 | 2026-08-06 | 13,252.30 |
| `307000051388` | `2654338844` | 2026-08-10 19:26:08 | 2026-08-06 | 13,252.30 |

The second pair is not a supplemental payment for the 2026-08-05 redemption.
It belongs to the distinct 2026-08-06 redemption. All four events occurred
after their day's 15:01 `broker_close` snapshot.

The previously inferred visible-cash bridges remain separate observations:

| Account | Recognition date | OC-visible bridge CNY | Interpretation |
| --- | --- | ---: | --- |
| `307000051387` | 2026-08-07 | 73,448.939998 | Exact fast/normal counter allocation unavailable |
| `307000051388` | 2026-08-07 | 73,448.939998 | Exact fast/normal counter allocation unavailable |
| `307000051387` | 2026-08-10 | 53,214.500000 | Exact fast/normal counter allocation unavailable |
| `307000051388` | 2026-08-10 | 13,214.600000 | Exact fast/normal counter allocation unavailable |

The user confirmed that fast-counter funds are moved to the normal counter only
when needed on T+1/T+2 after ETF subscription/redemption so public-fund
occupancy can be deducted. This is conditional activity, not a daily process.
Because the current Huaxin broker interface exposed through OC returns only the
Huaxin fast-counter balance, the observed bridge may contain a counter transfer,
cash-substitution occupancy change, or both. This is a Huaxin counter capability
boundary, not a general limitation imposed on future OC broker adapters.
It is therefore a non-income scope adjustment and must not be netted against a
later broker cash receipt or classified as strategy profit.

## Transaction And Fee Cross-Check

The 2026-08-06 ordinary settlement rows were matched as multisets against OC
orders by security, side, filled quantity, and weighted average price:

| Account | Matched ordinary orders | Derived ordinary fees CNY | Result |
| --- | ---: | ---: | --- |
| `307000051387` | 653 / 653 | 1,613.06 | Aggregate ledger evidence closes |
| `307000051388` | 654 / 654 | 1,610.12 | Aggregate ledger evidence closes |

Account `307000051387` also has one reverse-repo settlement whose implied fee is
26.06 CNY. Exact per-order fee insertion was intentionally not performed:
duplicate economic tuples make 291 rows in 123 groups for `307000051387` and
284 rows in 120 groups for `307000051388` ambiguous at gateway-order identity
level. Aggregate agreement proves the day, but does not justify fabricating a
broker flow ID to Relay order ID mapping.

## Production Repair

Before any write, a PostgreSQL custom-format backup and checksum were created
under the ignored directory
`outputs/backups/relay-20260827-broker-cash-flow-repair/`.

The audited transaction performed these changes:

1. Preserved the four old inferred ETF refund entries and changed them to
   `voided`; no original record was deleted.
2. Inserted the four broker ETF cash events using the broker flow ID as the
   stable identity, exact Asia/Shanghai effective time, source trade date,
   statement hash, and `recurring_import=false`.
3. Stored the four complete OC-visible bridges separately as confirmed
   `settlement_adjustment` entries with `partial_counter_visibility=true`.
4. Annotated the recovered 2026-08-06 snapshots with the statement evidence
   without changing their amounts.
5. Released `performance_economic_nav.v2.5`: an ETF receipt posted after the
   immutable close snapshot is added as `post_close_settlement_cash` to the
   day's economic close NAV. The visible cash snapshot itself remains intact.

The two accounts were rebuilt in trade-date order for 2026-08-07..2026-08-26.
All 28 account-day NAV versions persisted as provisional and none was blocked.
Daily PnL, daily return, and cumulative return did not change from the audited
pre-v2.5 result; only the close economic asset timing was corrected:

| Account | Trade date | Day PnL CNY | Daily return | Post-close cash CNY | Close economic NAV CNY |
| --- | --- | ---: | ---: | ---: | ---: |
| `307000051387` | 2026-08-07 | 113,109.402378 | 0.225617% | 53,520.64 | 50,319,920.799998 |
| `307000051388` | 2026-08-07 | 114,574.137015 | 0.229335% | 53,520.64 | 50,147,386.829998 |
| `307000051387` | 2026-08-10 | 8,613.420000 | 0.017147% | 13,252.30 | 50,294,056.560000 |
| `307000051388` | 2026-08-10 | 3,783.800000 | 0.007552% | 13,252.30 | 50,121,457.700000 |

## Broker Asset Basis Recovery

The user subsequently supplied two separate broker historical-funds exports:

- `reference/307000051387_资金.csv`, SHA-256
  `3e495917c8b271f247503dbe03b9fc76861d7a044a8e427038d7693b2b98d96b`.
- `reference/307000051388_资金.csv`, SHA-256
  `36e6b74870b9cc715ebe4cff57946e2b499249e8909bbb9761d9c69e3cffc52d`.

Each file covers 23 non-zero trading days from account inception through
2026-08-26. Every row satisfies, to CNY 0.01:

```text
close broker asset = previous close broker asset + deposit - withdrawal + broker daily PnL
```

The files also separate customer funds, security market value, and total
assets. `total asset - customer funds - market value` matches reverse-repo
principal on applicable days, so reverse repo is not misclassified as hidden
cash. These files are authoritative for the broker-reported asset basis, but
that basis still excludes public-fund occupancy and outstanding ETF settlement
assets included by Relay economic NAV.

All 46 rows were stored as version-1 confirmed gold records under source
`broker_historical_funds_statement_one_time_audit`. They remain independent
from the formula. Only 2026-08-06 received an explicitly enabled `reconcile`
snapshot; the OC `open`, `broker_close`, and `close` snapshots were not changed.
The reconcile payload records the source hash, row number, broker open/close
assets, deposit/withdrawal, and opening/closing outstanding ETF settlement
assets. It also has `recurring_import=false`.

`performance_economic_nav.v2.6` applies that confirmed basis and carries the
2026-08-05 ETF receivable through 2026-08-06. The resulting account-day values
are:

| Account | Open economic NAV | Close economic NAV | Day PnL | Attribution residual | Status |
| --- | ---: | ---: | ---: | ---: | --- |
| `307000051387` | 50,127,559.927622 | 50,158,740.667622 | 31,180.74 | -1,554.76 | provisional |
| `307000051388` | 49,958,781.232985 | 49,989,895.832985 | 31,114.60 | -1,621.70 | provisional |

Both residuals are within the configured warning tolerance. The old blocked
residuals are gone; fee estimates and unsettled ETF assets still correctly keep
the result provisional. The two accounts were rebuilt in order for
2026-08-06..2026-08-26: 30 current v2.6 rows, zero blocked. For the 28 dates
that already existed, both close NAV and day PnL changed by exactly zero.

The pre-write database backup is
`outputs/backups/relay-20260827-broker-asset-basis/relay_trader_before_asset_basis.dump`
with SHA-256
`881c69a1956a1714f54f8f65437b4f5fc499f302041c549c4481e3808ef16460`.

## Inception-Date Correction

The first rebuild incorrectly treated Relay's first available OC snapshot as
the performance inception. That set `307000051387` to 2026-07-29 and
`307000051388` to 2026-07-28. The broker historical-funds files instead prove
that both accounts were empty on 2026-07-24, received their opening capital
before trading on 2026-07-27, and traded that day.

The broker delivery statements independently contain 32 ETF buys for
`307000051387` and 31 ETF buys for `307000051388` on 2026-07-27. They also
contain the 31 ordinary ETF trades needed to bridge `307000051387` through
2026-07-28 before Relay first received that account from OC. A one-time
recovery inserted clearly sourced historical orders, fills, actual fees, and
position/asset snapshots for those three missing account-days. All per-symbol
quantity bridges close, and positions valued with Meridian unadjusted daily
close prices equal the broker market-value totals to CNY 0.01.

`performance_economic_nav.v2.7` recognizes an explicitly confirmed clean-start
deposit as inception opening capital. It does not record that pre-trading
funding as strategy PnL or as an intraday external flow. The resulting first
days are:

| Account | Opening capital | 2026-07-27 PnL | Close asset | Attribution residual |
| --- | ---: | ---: | ---: | ---: |
| `307000051387` | 51,010,941.93 | 57,387.16 | 51,068,329.09 | 16.90 |
| `307000051388` | 49,964,482.45 | 54,731.88 | 50,019,214.33 | 124.70 |

Both accounts now have 23 consecutive current v2.7 NAV rows from 2026-07-27
through 2026-08-26: 46 provisional rows, zero blocked. The recovery program was
removed after execution and is not a recurring broker-file import path. The
pre-write backup is
`outputs/backups/relay-20260827-inception-recovery/relay_trader_20260827T140816+0800.dump`
with SHA-256
`b22df8ebb9327a9b9b7dde8d450bdcaed948352135488b53a9c7cd413f9728f4`.

## Required Daily OC Capability

Relay does not require OC historical queries. For future trading days, each OC
broker adapter should expose the best current-day facts supported by that
broker before shutdown. A newly integrated broker may provide complete account
equity directly and should not inherit Huaxin-only assumptions:

1. Asset scope: explicit counter scope, fast/normal available cash, total cash,
   frozen/order cash, ETF cash-substitution occupancy or payable/receivable, and
   total account equity when the broker API provides them.
2. Counter transfer event: stable event ID, account, source/destination counter,
   signed amount, exact Asia/Shanghai time, status, and related ETF settlement
   group when known. It is emitted only when such a transfer occurs.
3. Current-day cash-flow query: stable broker flow ID, subject code/name,
   amount and direction, balance after, cash bucket, security, source trade
   date, contract/order references, and final-page marker.
4. The 15:01 capture stores these facts independently of Meridian. Missing one
   account remains an account-level issue and does not fail all accounts.
5. Every account keeps a stable `broker_id` ownership label independent of its
   alias, gateway, and environment. Cash-scope capability and fallback rules are
   selected by broker adapter, never inferred globally from OC.

Until OC supplies these facts, Relay may accept an explicitly confirmed manual
external deposit, withdrawal, or one-off internal counter transfer backed by
auditable evidence. It must not infer routine counter transfers from ETF dates
or build a broker-CSV ingestion job. Broker statements remain exceptional audit
evidence for investigating and repairing a known incident.
