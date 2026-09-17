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

## Resolution

OC commit `42150cb` now owns the Huaxin TEST-specific state rule:

1. The exact global state rejection latches the current OC process as unavailable for new orders.
2. OC publishes `state=DEGRADED`, `state_text=counter_order_entry_not_ready`, and `accepting_trade_commands=false` before the rejected order event.
3. Queries continue; cancellation remains available when broker login and the initial order snapshot allow it.
4. Recovery requires an operator-confirmed OC restart and a fresh `counter_session_id`; OC does not infer recovery from a timer or probe order.
5. Production behavior is unchanged.

Relay therefore does not inspect rejected order history or implement a TEST-specific cooldown. Fresh OC heartbeat state is the sole dynamic counter-availability input. `RelayClient.verify_ready(force=True)` fails closed with the general reason `OC_TRADE_COMMANDS_PAUSED` while `accepting_trade_commands=false`. The static TEST schedule remains an independent outer boundary.

See [OC TEST order-entry readiness requirement](OC_TEST_ORDER_ENTRY_READINESS_REQUIREMENT_20260917.md) for the original requirement and accepted implementation boundary.

## Chronos Retest

1. Install `relay-sdk==0.1.39`.
2. Call `verify_ready(force=True)` before the first write.
3. Reproduce the exact global state rejection and confirm the degraded heartbeat precedes its `order.event`.
4. Confirm the next `verify_ready(force=True)` raises `RelayBrokerNotReadyError` with `OC_TRADE_COMMANDS_PAUSED` and sends no new command.
5. Confirm queries and eligible cancellations still work while new orders are locked.
6. Restart OC after counter recovery, then confirm a new `counter_session_id` and fresh `accepting_trade_commands=true` heartbeat restore readiness.
