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

## Remaining 2026-08-06 Block

This cash-flow export does not safely close the 2026-08-06 economic NAV. The
current previews remain blocked with attribution residuals of
`-74,811.869998 CNY` for `307000051387` and `-95,070.639998 CNY` for
`307000051388`. The remaining uncertainty is not Meridian valuation or ETF
transfer quantity. It is account cash scope and exact fees:

- OC open/close assets for the current Huaxin accounts expose only Huaxin
  fast-counter visible cash.
- The normal-counter balance, frozen public-fund occupancy, cash substitution,
  and counter transfer legs are absent.
- The broker export has no usable balance-after field.
- Aggregate fees are known, but exact per-order identities are ambiguous.

The blocked day was not published merely to remove a warning.

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
