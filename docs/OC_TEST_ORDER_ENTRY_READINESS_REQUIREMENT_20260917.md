# OC TEST Order-Entry Readiness Requirement

Date: `2026-09-17`

Scope: Huaxin 7x24 TEST counter only

Protocol: `relay.stream.v1`

Implementation: OC commit `42150cb` accepted on `2026-09-17`

## Incident

In OC process session `hxproc-0eb87c8f23897e76`, heartbeat continuously reported:

```json
{
  "state": "UP",
  "state_text": "running",
  "broker_ready": true,
  "order_snapshot_ready": true,
  "accepting_trade_commands": true
}
```

At `2026-09-17 15:41:46 Asia/Shanghai`, two legal 100-share buy orders for different Shenzhen securities were both rejected immediately with adapter status `-10/fail` and `BROKER_REJECTED / 当前状态禁止此项操作`. Both events also carried `broker_trade_date=20450806` and `broker_trade_date_outlier=true`.

## Required Behavior

1. `accepting_trade_commands` must describe actual counter order-entry availability, not only whether OC can consume Redis commands.
2. When the counter is in a state that would return the global rejection `当前状态禁止此项操作`, heartbeat must publish `accepting_trade_commands=false` before Relay sends another command whenever OC can observe that state.
3. Use a stable `state_text`, recommended `counter_order_entry_not_ready`, while this condition remains active.
4. When the counter becomes available again, publish `accepting_trade_commands=true` with fresh heartbeat evidence.
5. Keep the existing process-lifetime `counter_session_id` rule. A short reconnect retains the ID; an OC process restart creates a new ID.
6. Confirm whether `broker_trade_date=20450806` is an expected Huaxin TEST internal business date. Continue placing it only in `adapter_context`; never replace Relay's standard `payload.trade_date` with it.
7. Do not change command receipts, order IDs, stream names, or terminal order-event semantics.

The broker API exposes no reliable proactive recovery field. OC therefore lowers `accepting_trade_commands` after this exact rejection and keeps it false until an operator-confirmed process restart creates a new `counter_session_id`. Relay does not add a timer, probe order, or order-ledger-derived fallback.

## Acceptance

1. During the unavailable state, heartbeat has `accepting_trade_commands=false` and Relay returns `order_entry_ready=false` before command publication.
2. A legal minimal order is accepted only after a fresh heartbeat reports the recovered state.
3. Ordinary symbol-, position-, price-, or risk-specific rejection does not lower account-wide readiness.
4. `broker_trade_date` remains audit-only and standard `trade_date` remains the actual Asia/Shanghai submission date.
5. Production heartbeat and normal A-share session behavior remain unchanged.
