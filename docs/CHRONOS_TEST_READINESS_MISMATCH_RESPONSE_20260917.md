# Chronos TEST Readiness Mismatch Response

Date: `2026-09-17`

Environment: `test` only

Account: `00030484`

Counter session: `hxproc-0eb87c8f23897e76`

## Finding

Chronos evidence is confirmed. At `15:41:21 Asia/Shanghai`, Relay observed a fresh OC heartbeat with `state=UP`, `broker_ready=true`, `order_snapshot_ready=true`, and `accepting_trade_commands=true`; the configured Huaxin 7x24 window `15:40-17:40` was also active. Relay therefore returned `order_entry_ready=true`.

The batch published at `15:41:46` then produced two terminal rejects:

| Security | Gateway order ID | Result |
| --- | --- | --- |
| `300498.SZ` | `relay-gateway-b23bb37ebf847fce0d51668745d065b5295e1c09d39628903c4b9c4828003b8c` | `BROKER_REJECTED / 当前状态禁止此项操作` |
| `000981.SZ` | `relay-gateway-ec35b4c0111f7d934e7222750675b9c6a61083c4e52565b3a6a0aa609d6b70b7` | `BROKER_REJECTED / 当前状态禁止此项操作` |

Both orders have zero fills, `leaves_qty=100`, terminal rejection state, the same command identity, and the same OC process session. Relay's synchronous command receipt and asynchronous final-state separation worked as designed.

The mismatch is between OC's heartbeat readiness claim and the counter's actual order-entry state. Relay had no stronger pre-trade field available. `broker_trade_date=20450806` is preserved only as an outlier audit value; Relay normalized the standard trade date to `20260917`. The evidence does not prove that the outlier date caused the rejection, so OC must confirm that relationship.

## Relay Mitigation

Relay now derives a TEST-only rejection circuit from the authoritative order ledger:

1. Match only `BROKER_REJECTED` whose message contains `当前状态禁止此项操作`.
2. Require `environment=test` and `counter_mode=huaxin_7x24_test`.
3. Scope the issue to the current `counter_session_id`; an OC restart creates a clean session.
4. Hold readiness closed for five minutes after the reject.
5. Return `order_entry_block_reason=TEST_COUNTER_STATE_REJECTED_COOLDOWN` and `order_entry_cooldown=true`.
6. Set `next_transition_at` to the earlier of cooldown expiry and the configured window transition.
7. Expose `last_issue_code`, `last_issue_message`, and `last_issue_at` for audit.

`RelayClient.verify_ready(force=True)` therefore fails closed before a subsequent write during the cooldown. The original batch can still contain multiple children because its order events arrive after the single batch command has already been published.

The circuit does not apply to production, previous OC sessions, or other business rejection messages. It does not modify order state or change the meaning of HTTP `202 Accepted`.

## Remaining OC Requirement

Relay's circuit is defensive, not a substitute for authoritative OC readiness. OC should make `accepting_trade_commands` reflect the counter's actual business state and set it to false before accepting Relay commands whenever the counter would return the global state rejection. See [OC TEST order-entry readiness requirement](OC_TEST_ORDER_ENTRY_READINESS_REQUIREMENT_20260917.md).

## Chronos Retest

1. Install `relay-sdk==0.1.38`.
2. Call `verify_ready(force=True)` before the first write.
3. If a legal minimal order receives the global state rejection, call `verify_ready(force=True)` again.
4. Confirm the second call raises `RelayBrokerNotReadyError` with code `TEST_COUNTER_STATE_REJECTED_COOLDOWN` and sends no new command.
5. After five minutes, or after OC restarts into a different process session, confirm readiness can recover from fresh heartbeat state.
