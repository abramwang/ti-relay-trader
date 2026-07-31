package ledger

const upsertAccountSQL = `
INSERT INTO accounts (
    account_id,
    broker_id,
    status,
    enabled,
    trading_enabled,
    simulated,
    tags,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
ON CONFLICT (account_id) DO UPDATE SET
    broker_id = EXCLUDED.broker_id,
    status = EXCLUDED.status,
    enabled = EXCLUDED.enabled,
    trading_enabled = EXCLUDED.trading_enabled,
    simulated = EXCLUDED.simulated,
    tags = EXCLUDED.tags,
    updated_at = EXCLUDED.updated_at
`

const upsertAccountAliasSQL = `
INSERT INTO accounts (
    account_id,
    broker_id,
    account_name,
    status,
    enabled,
    trading_enabled,
    simulated,
    updated_at
) VALUES (
    $1, $2, $3, 'readonly', TRUE, FALSE, FALSE, $4
)
ON CONFLICT (account_id) DO UPDATE SET
    account_name = EXCLUDED.account_name,
    updated_at = EXCLUDED.updated_at
`

const insertOrderSQL = `
INSERT INTO orders (
    account_id,
    client_order_id,
    gateway_order_id,
    order_id,
    order_stream_id,
    trade_date,
    strategy_type,
    strategy_id,
    basket_id,
    parent_order_id,
    t0_order_group_id,
    symbol,
    name,
    exchange,
    trade_side,
    business_type,
    offset_type,
    limit_price,
    order_qty,
    submitted_qty,
    cum_filled_qty,
    leaves_qty,
    cancelled_qty,
    invalid_qty,
    avg_fill_price,
    fee,
    status,
    gateway_status,
    adapter_status_code,
    adapter_status_name,
    is_terminal,
    reject_code,
    reject_message,
    origin_message_id,
    request_id,
    idempotency_key,
    shareholder_id,
    created_at,
    accepted_at,
    inserted_at,
    last_updated_at,
    terminal_at,
    raw_payload,
    adapter_context
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
    $31, $32, $33, $34, $35, $36, $37, COALESCE($38, now()), $39, $40,
    $41, $42, $43, $44
)
`

const upsertOrderSQL = insertOrderSQL + `
ON CONFLICT (account_id, trade_date, gateway_order_id) DO UPDATE SET
    client_order_id = COALESCE(EXCLUDED.client_order_id, orders.client_order_id),
    order_id = COALESCE(EXCLUDED.order_id, orders.order_id),
    order_stream_id = COALESCE(EXCLUDED.order_stream_id, orders.order_stream_id),
    trade_date = COALESCE(EXCLUDED.trade_date, orders.trade_date),
    strategy_type = COALESCE(EXCLUDED.strategy_type, orders.strategy_type),
    strategy_id = COALESCE(EXCLUDED.strategy_id, orders.strategy_id),
    basket_id = COALESCE(EXCLUDED.basket_id, orders.basket_id),
    parent_order_id = COALESCE(EXCLUDED.parent_order_id, orders.parent_order_id),
    t0_order_group_id = COALESCE(EXCLUDED.t0_order_group_id, orders.t0_order_group_id),
    created_at = COALESCE(EXCLUDED.created_at, orders.created_at),
    symbol = EXCLUDED.symbol,
    name = EXCLUDED.name,
    exchange = EXCLUDED.exchange,
    trade_side = EXCLUDED.trade_side,
    business_type = EXCLUDED.business_type,
    offset_type = EXCLUDED.offset_type,
    limit_price = EXCLUDED.limit_price,
    order_qty = EXCLUDED.order_qty,
    submitted_qty = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.submitted_qty
        ELSE GREATEST(orders.submitted_qty, EXCLUDED.submitted_qty)
    END,
    cum_filled_qty = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' OR EXCLUDED.is_terminal = TRUE
            THEN EXCLUDED.cum_filled_qty
        ELSE GREATEST(orders.cum_filled_qty, EXCLUDED.cum_filled_qty)
    END,
    leaves_qty = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.leaves_qty
        WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.leaves_qty
        ELSE EXCLUDED.leaves_qty
    END,
    cancelled_qty = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.cancelled_qty
        ELSE GREATEST(orders.cancelled_qty, EXCLUDED.cancelled_qty)
    END,
    invalid_qty = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.invalid_qty
        ELSE GREATEST(orders.invalid_qty, EXCLUDED.invalid_qty)
    END,
    avg_fill_price = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.avg_fill_price
        ELSE COALESCE(EXCLUDED.avg_fill_price, orders.avg_fill_price)
    END,
    fee = EXCLUDED.fee,
    status = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.status
        WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.status
        ELSE EXCLUDED.status
    END,
    gateway_status = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.gateway_status
        WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.gateway_status
        ELSE EXCLUDED.gateway_status
    END,
    adapter_status_code = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.adapter_status_code
        ELSE COALESCE(EXCLUDED.adapter_status_code, orders.adapter_status_code)
    END,
    adapter_status_name = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.adapter_status_name
        ELSE COALESCE(EXCLUDED.adapter_status_name, orders.adapter_status_name)
    END,
    is_terminal = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.is_terminal
        ELSE orders.is_terminal OR EXCLUDED.is_terminal
    END,
    reject_code = CASE
        WHEN orders.is_terminal = TRUE
            AND EXCLUDED.is_terminal = FALSE
            AND orders.status = 'rejected'
            THEN orders.reject_code
        WHEN EXCLUDED.status = 'rejected' OR EXCLUDED.gateway_status = 'rejected'
            THEN EXCLUDED.reject_code
        ELSE NULL
    END,
    reject_message = CASE
        WHEN orders.is_terminal = TRUE
            AND EXCLUDED.is_terminal = FALSE
            AND orders.status = 'rejected'
            THEN orders.reject_message
        WHEN EXCLUDED.status = 'rejected' OR EXCLUDED.gateway_status = 'rejected'
            THEN EXCLUDED.reject_message
        ELSE NULL
    END,
    origin_message_id = COALESCE(EXCLUDED.origin_message_id, orders.origin_message_id),
    request_id = COALESCE(EXCLUDED.request_id, orders.request_id),
    idempotency_key = COALESCE(EXCLUDED.idempotency_key, orders.idempotency_key),
    shareholder_id = COALESCE(EXCLUDED.shareholder_id, orders.shareholder_id),
    accepted_at = COALESCE(EXCLUDED.accepted_at, orders.accepted_at),
    inserted_at = COALESCE(EXCLUDED.inserted_at, orders.inserted_at),
    last_updated_at = COALESCE(EXCLUDED.last_updated_at, orders.last_updated_at),
    terminal_at = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' AND EXCLUDED.is_terminal = FALSE THEN NULL
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' AND EXCLUDED.is_terminal = TRUE
            THEN COALESCE(
                EXCLUDED.terminal_at,
                (
                    SELECT MIN(event.produced_at)
                    FROM order_events AS event
                    WHERE event.account_id = EXCLUDED.account_id
                        AND event.trade_date = EXCLUDED.trade_date
                        AND event.gateway_order_id = EXCLUDED.gateway_order_id
                        AND event.is_terminal = TRUE
                        AND (
                            event.status = EXCLUDED.status
                            OR event.gateway_status = EXCLUDED.gateway_status
                        )
                ),
                orders.terminal_at,
                EXCLUDED.last_updated_at,
                now()
            )
        WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.terminal_at
        WHEN EXCLUDED.is_terminal = TRUE THEN COALESCE(EXCLUDED.terminal_at, orders.terminal_at, EXCLUDED.last_updated_at, now())
        ELSE orders.terminal_at
    END,
    raw_payload = EXCLUDED.raw_payload,
    adapter_context = CASE
        WHEN EXCLUDED.adapter_context ? 'relay_reply_status' THEN EXCLUDED.adapter_context
        ELSE orders.adapter_context || EXCLUDED.adapter_context
    END,
    updated_at = now()
`

const appendOrderEventSQL = `
INSERT INTO order_events (
    account_id,
    gateway_order_id,
    event_id,
    event_type,
    status,
    gateway_status,
    is_terminal,
    trade_date,
    strategy_type,
    strategy_id,
    basket_id,
    parent_order_id,
    t0_order_group_id,
    stream_key,
    stream_id,
    origin_message_id,
    request_id,
    correlation_id,
    produced_at,
    payload,
    adapter_context
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21
)
ON CONFLICT DO NOTHING
`

const upsertOrderCancelAttemptSQL = `
INSERT INTO order_cancel_attempts (
    attempt_id,
    account_id,
    trade_date,
    gateway_order_id,
    order_id,
    order_stream_id,
    origin_message_id,
    request_id,
    correlation_id,
    status,
    code,
    message,
    retry_safe,
    order_state_changed,
    reconciliation_required,
    occurred_at,
    stream_key,
    stream_id,
    raw_payload,
    adapter_context
) VALUES (
    $1, $2, $3::date, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
)
ON CONFLICT (account_id, attempt_id) DO UPDATE SET
    trade_date = EXCLUDED.trade_date,
    gateway_order_id = EXCLUDED.gateway_order_id,
    order_id = COALESCE(EXCLUDED.order_id, order_cancel_attempts.order_id),
    order_stream_id = COALESCE(EXCLUDED.order_stream_id, order_cancel_attempts.order_stream_id),
    origin_message_id = COALESCE(EXCLUDED.origin_message_id, order_cancel_attempts.origin_message_id),
    request_id = COALESCE(EXCLUDED.request_id, order_cancel_attempts.request_id),
    correlation_id = COALESCE(EXCLUDED.correlation_id, order_cancel_attempts.correlation_id),
    status = EXCLUDED.status,
    code = COALESCE(EXCLUDED.code, order_cancel_attempts.code),
    message = COALESCE(EXCLUDED.message, order_cancel_attempts.message),
    retry_safe = COALESCE(EXCLUDED.retry_safe, order_cancel_attempts.retry_safe),
    order_state_changed = COALESCE(EXCLUDED.order_state_changed, order_cancel_attempts.order_state_changed),
    reconciliation_required = order_cancel_attempts.reconciliation_required OR EXCLUDED.reconciliation_required,
    occurred_at = EXCLUDED.occurred_at,
    stream_key = COALESCE(EXCLUDED.stream_key, order_cancel_attempts.stream_key),
    stream_id = COALESCE(EXCLUDED.stream_id, order_cancel_attempts.stream_id),
    raw_payload = EXCLUDED.raw_payload,
    adapter_context = order_cancel_attempts.adapter_context || EXCLUDED.adapter_context,
    updated_at = now()
`

const updateOrderStatusSQL = `
UPDATE orders SET
    order_id = COALESCE($3, order_id),
    order_stream_id = COALESCE($4, order_stream_id),
    trade_date = COALESCE($5::date, trade_date),
    strategy_type = COALESCE($6, strategy_type),
    strategy_id = COALESCE($7, strategy_id),
    basket_id = COALESCE($8, basket_id),
    parent_order_id = COALESCE($9, parent_order_id),
    t0_order_group_id = COALESCE($10, t0_order_group_id),
    submitted_qty = GREATEST(submitted_qty, $11),
    cum_filled_qty = CASE WHEN $20 = TRUE THEN $12 ELSE GREATEST(cum_filled_qty, $12) END,
    leaves_qty = CASE WHEN is_terminal = TRUE AND $20 = FALSE THEN leaves_qty WHEN $13 > 0 OR $20 = TRUE THEN $13 ELSE leaves_qty END,
    cancelled_qty = GREATEST(cancelled_qty, $14),
    invalid_qty = GREATEST(invalid_qty, $15),
    avg_fill_price = COALESCE($16, avg_fill_price),
    fee = GREATEST(fee, $17),
    status = CASE WHEN is_terminal = TRUE AND $20 = FALSE THEN status ELSE $18 END,
    gateway_status = CASE WHEN is_terminal = TRUE AND $20 = FALSE THEN gateway_status ELSE $19 END,
    is_terminal = is_terminal OR $20,
    reject_code = CASE
        WHEN is_terminal = TRUE AND $20 = FALSE AND status = 'rejected' THEN reject_code
        WHEN $18 = 'rejected' OR $19 = 'rejected' THEN $21
        ELSE NULL
    END,
    reject_message = CASE
        WHEN is_terminal = TRUE AND $20 = FALSE AND status = 'rejected' THEN reject_message
        WHEN $18 = 'rejected' OR $19 = 'rejected' THEN $22
        ELSE NULL
    END,
    last_updated_at = COALESCE($23, now()),
    terminal_at = CASE WHEN is_terminal = TRUE AND $20 = FALSE THEN terminal_at WHEN $20 = TRUE THEN COALESCE($24, $23, terminal_at, now()) ELSE terminal_at END,
    adapter_context = adapter_context || $25::jsonb,
    updated_at = now()
WHERE account_id = $1
    AND gateway_order_id = $2
    AND trade_date = $5::date
`

const orderSelectColumns = `
SELECT
    account_id,
    client_order_id,
    gateway_order_id,
    order_id,
    order_stream_id,
    trade_date::text,
    strategy_type,
    strategy_id,
    basket_id,
    parent_order_id,
    t0_order_group_id,
    symbol,
    name,
    exchange,
    trade_side,
    business_type,
    offset_type,
    limit_price,
    order_qty,
    submitted_qty,
    cum_filled_qty,
    leaves_qty,
    cancelled_qty,
    invalid_qty,
    avg_fill_price,
    fee,
    status,
    gateway_status,
    adapter_status_code,
    adapter_status_name,
    is_terminal,
    reject_code,
    reject_message,
    origin_message_id,
    request_id,
    idempotency_key,
    shareholder_id,
    created_at,
    accepted_at,
    inserted_at,
    last_updated_at,
    terminal_at,
    adapter_context
FROM orders
`

const getOrderSQL = orderSelectColumns + `
WHERE account_id = $1 AND gateway_order_id = $2
ORDER BY trade_date DESC, COALESCE(last_updated_at, created_at) DESC
LIMIT 1
`

const getOrderByIdempotencyKeySQL = orderSelectColumns + `
WHERE account_id = $1 AND idempotency_key = $2
ORDER BY COALESCE(last_updated_at, created_at) DESC, gateway_order_id DESC
LIMIT 1
`

const insertFillSQL = `
INSERT INTO fills (
    account_id,
    fill_id,
    gateway_order_id,
    order_id,
    order_stream_id,
    business_type,
    symbol,
    name,
    exchange,
    trade_side,
    price,
    qty,
    fee,
    trade_date,
    match_timestamp,
    matched_at,
    shareholder_id,
    strategy_type,
    strategy_id,
    basket_id,
    parent_order_id,
    t0_order_group_id,
    stream_key,
    stream_id,
    origin_message_id,
    request_id,
    raw_payload,
    adapter_context
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26, $27, $28
)
ON CONFLICT DO NOTHING
`

const deleteSummaryFillsForOrderSQL = `
DELETE FROM fills
WHERE account_id = $1
    AND trade_date = $2::date
    AND fill_id LIKE 'relay-summary:%'
    AND (
        gateway_order_id = $3
        OR ($4::text IS NOT NULL AND order_stream_id = $4)
    )
`

const fillSelectColumns = `
SELECT
    fill_id,
    account_id,
    gateway_order_id,
    order_id,
    order_stream_id,
    business_type,
    symbol,
    name,
    exchange,
    trade_side,
    price,
    qty,
    fee,
    trade_date::text,
    match_timestamp,
    matched_at,
    shareholder_id,
    strategy_type,
    strategy_id,
    basket_id,
    parent_order_id,
    t0_order_group_id,
    adapter_context
FROM fills
`

const insertComponentTransferSQL = `
INSERT INTO etf_component_transfers (
    account_id,
    fill_id,
    gateway_order_id,
    order_id,
    order_stream_id,
    symbol,
    name,
    exchange,
    price,
    qty,
    trade_side,
    business_type,
    record_type,
    transfer_type,
    is_transfer,
    component_symbol,
    component_name,
    component_exchange,
    component_qty,
    component_value,
    cash_substitution,
    broker_trade_side,
    broker_business_type,
    trade_date,
    match_timestamp,
    matched_at,
    shareholder_id,
    strategy_type,
    strategy_id,
    basket_id,
    parent_order_id,
    t0_order_group_id,
    stream_key,
    stream_id,
    origin_message_id,
    request_id,
    raw_payload,
    adapter_context
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
    $31, $32, $33, $34, $35, $36, $37, $38
)
ON CONFLICT DO NOTHING
`

const componentTransferSelectColumns = `
SELECT
    fill_id,
    account_id,
    gateway_order_id,
    order_id,
    order_stream_id,
    symbol,
    name,
    exchange,
    price,
    qty,
    trade_side,
    business_type,
    record_type,
    transfer_type,
    is_transfer,
    component_symbol,
    component_name,
    component_exchange,
    component_qty,
    component_value,
    cash_substitution,
    broker_trade_side,
    broker_business_type,
    trade_date::text,
    match_timestamp,
    matched_at,
    shareholder_id,
    strategy_type,
    strategy_id,
    basket_id,
    parent_order_id,
    t0_order_group_id,
    adapter_context
FROM etf_component_transfers
`

const latestAssetSQL = `
SELECT
    account_id,
    cash_available,
    cash_total,
    net_asset,
    market_value,
    stock_value,
    fund_value,
    commission,
    day_profit,
    position_profit,
    close_profit,
    credit,
    captured_at
FROM asset_snapshots
WHERE account_id = $1
ORDER BY trade_date DESC, captured_at DESC, asset_snapshot_pk DESC
LIMIT 1
`

const assetPositionObservationSQL = `
WITH asset AS (
    SELECT
        account_id,
        trade_date,
        snapshot_type,
        cash_available,
        cash_total,
        net_asset,
        market_value,
        stock_value,
        fund_value,
        captured_at
    FROM asset_snapshots
    WHERE account_id = $1
        AND trade_date = $2::date
        AND snapshot_type = $3
    ORDER BY captured_at DESC, asset_snapshot_pk DESC
    LIMIT 1
),
positions AS (
    SELECT
        count(*)::bigint AS positions_count,
        COALESCE(sum(market_value), 0) AS position_market_value,
        max(captured_at) AS position_captured_at
    FROM position_snapshots
    WHERE account_id = $1
        AND trade_date = $2::date
        AND snapshot_type = $3
)
SELECT
    asset.account_id,
    asset.trade_date::text,
    asset.snapshot_type,
    asset.cash_available,
    asset.cash_total,
    asset.net_asset,
    asset.market_value,
    asset.stock_value,
    asset.fund_value,
    positions.positions_count,
    positions.position_market_value,
    asset.captured_at,
    positions.position_captured_at
FROM asset
CROSS JOIN positions
`

const dailyPerformanceSQL = `
WITH asset AS (
    SELECT
        account_id,
        trade_date,
        cash_available,
        cash_total,
        net_asset,
        market_value,
        stock_value,
        fund_value,
        day_profit,
        position_profit,
        close_profit,
        credit,
        captured_at
    FROM asset_snapshots
    WHERE account_id = $1
        AND trade_date = $2::date
        AND snapshot_type = 'close'
    ORDER BY captured_at DESC, asset_snapshot_pk DESC
    LIMIT 1
),
previous_asset AS (
    SELECT net_asset
    FROM asset_snapshots
    WHERE account_id = $1
        AND trade_date < $2::date
        AND snapshot_type = 'close'
    ORDER BY trade_date DESC, captured_at DESC, asset_snapshot_pk DESC
    LIMIT 1
),
open_asset AS (
    SELECT
        net_asset,
        captured_at
    FROM asset_snapshots
    WHERE account_id = $1
        AND trade_date = $2::date
        AND snapshot_type = 'open'
    ORDER BY captured_at DESC, asset_snapshot_pk DESC
    LIMIT 1
),
positions AS (
    SELECT
        count(*)::bigint AS positions_count,
        COALESCE(sum(market_value), 0) AS position_market_value,
        COALESCE(sum(unrealized_pnl), 0) AS unrealized_pnl,
        COALESCE(sum(day_unrealized_pnl), 0) AS day_unrealized_pnl,
        COALESCE(sum(settled_profit), 0) AS settled_profit
    FROM position_snapshots
    WHERE account_id = $1
        AND trade_date = $2::date
        AND snapshot_type = 'close'
),
fills AS (
    SELECT
        count(*)::bigint AS fills_count,
        COALESCE(sum(CASE WHEN trade_side IN ('B', 'P') THEN price * qty ELSE 0 END), 0) AS buy_amount,
        COALESCE(sum(CASE WHEN trade_side IN ('S', 'R') THEN price * qty ELSE 0 END), 0) AS sell_amount,
        COALESCE(sum(fee), 0) AS fee_total
    FROM fills
    WHERE account_id = $1
        AND (
            (trade_date IS NOT NULL AND trade_date = $2::date)
            OR (trade_date IS NULL AND COALESCE(matched_at, created_at) >= $3 AND COALESCE(matched_at, created_at) < $4)
        )
)
SELECT
    asset.account_id,
    asset.trade_date::text,
    asset.cash_available,
    asset.cash_total,
    asset.net_asset,
    COALESCE(previous_asset.net_asset, 0) AS previous_net_asset,
    COALESCE(open_asset.net_asset, previous_asset.net_asset, 0) AS open_net_asset,
    CASE WHEN open_asset.net_asset IS NOT NULL AND COALESCE(previous_asset.net_asset, 0) > 0 THEN open_asset.net_asset - previous_asset.net_asset ELSE 0 END AS overnight_adjustment,
    CASE WHEN COALESCE(previous_asset.net_asset, 0) > 0 THEN asset.net_asset - previous_asset.net_asset ELSE 0 END AS asset_change,
    CASE WHEN COALESCE(open_asset.net_asset, previous_asset.net_asset, 0) > 0 THEN asset.net_asset - COALESCE(open_asset.net_asset, previous_asset.net_asset, 0) ELSE 0 END AS intraday_pnl,
    CASE WHEN COALESCE(open_asset.net_asset, previous_asset.net_asset, 0) > 0 THEN (asset.net_asset - COALESCE(open_asset.net_asset, previous_asset.net_asset, 0)) / COALESCE(open_asset.net_asset, previous_asset.net_asset, 0) ELSE 0 END AS intraday_return,
    CASE
        WHEN open_asset.net_asset IS NOT NULL THEN 'open'
        WHEN previous_asset.net_asset IS NOT NULL THEN 'previous_close_fallback'
        ELSE ''
    END AS open_snapshot_source,
    CASE WHEN COALESCE(previous_asset.net_asset, 0) > 0 THEN asset.net_asset - previous_asset.net_asset ELSE 0 END AS daily_pnl,
    CASE WHEN COALESCE(previous_asset.net_asset, 0) > 0 THEN (asset.net_asset - previous_asset.net_asset) / previous_asset.net_asset ELSE 0 END AS return_rate,
    asset.market_value,
    asset.stock_value,
    asset.fund_value,
    asset.day_profit,
    asset.position_profit,
    asset.close_profit,
    asset.credit,
    positions.positions_count,
    positions.position_market_value,
    positions.unrealized_pnl,
    positions.day_unrealized_pnl,
    positions.settled_profit,
    fills.fills_count,
    fills.buy_amount,
    fills.sell_amount,
    fills.buy_amount + fills.sell_amount AS turnover,
    fills.fee_total,
    open_asset.captured_at AS open_captured_at,
    asset.captured_at
FROM asset
CROSS JOIN positions
CROSS JOIN fills
LEFT JOIN previous_asset ON TRUE
LEFT JOIN open_asset ON TRUE
`

const dailyPerformanceSeriesSQL = `
WITH asset_ranked AS (
    SELECT
        account_id,
        trade_date,
        cash_available,
        cash_total,
        net_asset,
        market_value,
        stock_value,
        fund_value,
        day_profit,
        position_profit,
        close_profit,
        credit,
        captured_at,
        row_number() OVER (PARTITION BY trade_date ORDER BY captured_at DESC, asset_snapshot_pk DESC) AS rn
    FROM asset_snapshots
    WHERE account_id = $1
        AND trade_date <= $3::date
        AND snapshot_type = 'close'
),
asset AS (
    SELECT
        account_id,
        trade_date,
        cash_available,
        cash_total,
        net_asset,
        COALESCE(lag(net_asset) OVER (ORDER BY trade_date), 0) AS previous_net_asset,
        market_value,
        stock_value,
        fund_value,
        day_profit,
        position_profit,
        close_profit,
        credit,
        captured_at
    FROM asset_ranked
    WHERE rn = 1
),
open_asset_ranked AS (
    SELECT
        trade_date,
        net_asset,
        captured_at,
        row_number() OVER (PARTITION BY trade_date ORDER BY captured_at DESC, asset_snapshot_pk DESC) AS rn
    FROM asset_snapshots
    WHERE account_id = $1
        AND trade_date >= $2::date
        AND trade_date <= $3::date
        AND snapshot_type = 'open'
),
open_asset AS (
    SELECT
        trade_date,
        net_asset,
        captured_at
    FROM open_asset_ranked
    WHERE rn = 1
),
positions AS (
    SELECT
        trade_date,
        count(*)::bigint AS positions_count,
        COALESCE(sum(market_value), 0) AS position_market_value,
        COALESCE(sum(unrealized_pnl), 0) AS unrealized_pnl,
        COALESCE(sum(day_unrealized_pnl), 0) AS day_unrealized_pnl,
        COALESCE(sum(settled_profit), 0) AS settled_profit
    FROM position_snapshots
    WHERE account_id = $1
        AND trade_date >= $2::date
        AND trade_date <= $3::date
        AND snapshot_type = 'close'
    GROUP BY trade_date
),
fills AS (
    SELECT
        fill_date,
        count(*)::bigint AS fills_count,
        COALESCE(sum(CASE WHEN trade_side IN ('B', 'P') THEN price * qty ELSE 0 END), 0) AS buy_amount,
        COALESCE(sum(CASE WHEN trade_side IN ('S', 'R') THEN price * qty ELSE 0 END), 0) AS sell_amount,
        COALESCE(sum(fee), 0) AS fee_total
    FROM (
        SELECT
            CASE
                WHEN trade_date IS NOT NULL THEN trade_date
                ELSE (COALESCE(matched_at, created_at) AT TIME ZONE 'Asia/Shanghai')::date
            END AS fill_date,
            trade_side,
            price,
            qty,
            fee
        FROM fills
        WHERE account_id = $1
            AND (
                (trade_date IS NOT NULL AND trade_date >= $2::date AND trade_date <= $3::date)
                OR (trade_date IS NULL AND COALESCE(matched_at, created_at) >= $4 AND COALESCE(matched_at, created_at) < $5)
            )
    ) fill_rows
    GROUP BY fill_date
)
SELECT
    asset.account_id,
    asset.trade_date::text,
    asset.cash_available,
    asset.cash_total,
    asset.net_asset,
    asset.previous_net_asset,
    COALESCE(open_asset.net_asset, NULLIF(asset.previous_net_asset, 0), 0) AS open_net_asset,
    CASE WHEN open_asset.net_asset IS NOT NULL AND asset.previous_net_asset > 0 THEN open_asset.net_asset - asset.previous_net_asset ELSE 0 END AS overnight_adjustment,
    CASE WHEN asset.previous_net_asset > 0 THEN asset.net_asset - asset.previous_net_asset ELSE 0 END AS asset_change,
    CASE WHEN COALESCE(open_asset.net_asset, NULLIF(asset.previous_net_asset, 0), 0) > 0 THEN asset.net_asset - COALESCE(open_asset.net_asset, NULLIF(asset.previous_net_asset, 0), 0) ELSE 0 END AS intraday_pnl,
    CASE WHEN COALESCE(open_asset.net_asset, NULLIF(asset.previous_net_asset, 0), 0) > 0 THEN (asset.net_asset - COALESCE(open_asset.net_asset, NULLIF(asset.previous_net_asset, 0), 0)) / COALESCE(open_asset.net_asset, NULLIF(asset.previous_net_asset, 0), 0) ELSE 0 END AS intraday_return,
    CASE
        WHEN open_asset.net_asset IS NOT NULL THEN 'open'
        WHEN asset.previous_net_asset > 0 THEN 'previous_close_fallback'
        ELSE ''
    END AS open_snapshot_source,
    CASE WHEN asset.previous_net_asset > 0 THEN asset.net_asset - asset.previous_net_asset ELSE 0 END AS daily_pnl,
    CASE WHEN asset.previous_net_asset > 0 THEN (asset.net_asset - asset.previous_net_asset) / asset.previous_net_asset ELSE 0 END AS return_rate,
    asset.market_value,
    asset.stock_value,
    asset.fund_value,
    asset.day_profit,
    asset.position_profit,
    asset.close_profit,
    asset.credit,
    COALESCE(positions.positions_count, 0) AS positions_count,
    COALESCE(positions.position_market_value, 0) AS position_market_value,
    COALESCE(positions.unrealized_pnl, 0) AS unrealized_pnl,
    COALESCE(positions.day_unrealized_pnl, 0) AS day_unrealized_pnl,
    COALESCE(positions.settled_profit, 0) AS settled_profit,
    COALESCE(fills.fills_count, 0) AS fills_count,
    COALESCE(fills.buy_amount, 0) AS buy_amount,
    COALESCE(fills.sell_amount, 0) AS sell_amount,
    COALESCE(fills.buy_amount, 0) + COALESCE(fills.sell_amount, 0) AS turnover,
    COALESCE(fills.fee_total, 0) AS fee_total,
    open_asset.captured_at AS open_captured_at,
    asset.captured_at
FROM asset
LEFT JOIN open_asset ON open_asset.trade_date = asset.trade_date
LEFT JOIN positions ON positions.trade_date = asset.trade_date
LEFT JOIN fills ON fills.fill_date = asset.trade_date
WHERE asset.trade_date >= $2::date
    AND asset.trade_date <= $3::date
ORDER BY asset.trade_date ASC
`

const upsertAssetSnapshotSQL = `
INSERT INTO asset_snapshots (
    trade_date,
    account_id,
    snapshot_type,
    cash_available,
    cash_total,
    net_asset,
    market_value,
    stock_value,
    fund_value,
    commission,
    day_profit,
    position_profit,
    close_profit,
    credit,
    source,
    raw_payload,
    captured_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17
)
ON CONFLICT (trade_date, account_id, snapshot_type) DO UPDATE SET
    cash_available = EXCLUDED.cash_available,
    cash_total = EXCLUDED.cash_total,
    net_asset = EXCLUDED.net_asset,
    market_value = EXCLUDED.market_value,
    stock_value = EXCLUDED.stock_value,
    fund_value = EXCLUDED.fund_value,
    commission = EXCLUDED.commission,
    day_profit = EXCLUDED.day_profit,
    position_profit = EXCLUDED.position_profit,
    close_profit = EXCLUDED.close_profit,
    credit = EXCLUDED.credit,
    source = EXCLUDED.source,
    raw_payload = EXCLUDED.raw_payload,
    captured_at = EXCLUDED.captured_at
`

const upsertReconciliationRunSQL = `
INSERT INTO reconciliation_runs (
    run_id,
    trade_date,
    status,
    source,
    started_at,
    completed_at,
    summary,
    error_message
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
ON CONFLICT (run_id) DO UPDATE SET
    trade_date = EXCLUDED.trade_date,
    status = EXCLUDED.status,
    source = EXCLUDED.source,
    started_at = COALESCE(EXCLUDED.started_at, reconciliation_runs.started_at),
    completed_at = EXCLUDED.completed_at,
    summary = EXCLUDED.summary,
    error_message = EXCLUDED.error_message
`

const upsertReconciliationInputSQL = `
INSERT INTO reconciliation_inputs (
    run_id,
    source,
    input_type,
    payload,
    captured_at
) VALUES (
    $1, $2, $3, $4, $5
)
ON CONFLICT (run_id, source, input_type) DO UPDATE SET
    payload = EXCLUDED.payload,
    captured_at = EXCLUDED.captured_at
`

const upsertReconciliationBreakSQL = `
INSERT INTO reconciliation_breaks (
    run_id,
    account_id,
    break_type,
    severity,
    status,
    object_type,
    object_id,
    internal_payload,
    external_payload,
    description,
    created_at,
    resolved_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
ON CONFLICT (
    run_id,
    COALESCE(account_id, ''),
    break_type,
    object_type,
    COALESCE(object_id, '')
) DO UPDATE SET
    severity = EXCLUDED.severity,
    status = EXCLUDED.status,
    internal_payload = EXCLUDED.internal_payload,
    external_payload = EXCLUDED.external_payload,
    description = EXCLUDED.description,
    resolved_at = EXCLUDED.resolved_at
`

const reconciliationBreakSelectColumns = `
SELECT
    run_id,
    account_id,
    break_type,
    severity,
    status,
    object_type,
    object_id,
    internal_payload,
    external_payload,
    description,
    created_at,
    resolved_at
FROM reconciliation_breaks
`

const rawStreamSummarySQL = `
SELECT
    stream_role,
    COALESCE(message_type, '') AS message_type,
    COALESCE(action, '') AS action,
    COALESCE(event_type, '') AS event_type,
    count(*)::bigint AS count,
    max(received_at) AS last_received_at
FROM raw_stream_messages
WHERE account_id = $1
    AND received_at >= $2
    AND received_at <= $3
GROUP BY stream_role, COALESCE(message_type, ''), COALESCE(action, ''), COALESCE(event_type, '')
ORDER BY stream_role, message_type, action, event_type
`

const positionSelectColumns = `
SELECT
    account_id,
    ''::text AS trade_date,
    symbol,
    name,
    exchange,
    quantity,
    sellable_qty,
    initial_qty,
    today_qty,
    avg_cost,
    last_price,
    market_value,
    unrealized_pnl,
    day_unrealized_pnl,
    settled_profit,
    shareholder_id,
    updated_at
FROM positions
`

const positionSnapshotSelectColumns = `
SELECT
    account_id,
    trade_date::text,
    snapshot_type,
    symbol,
    name,
    exchange,
    quantity,
    sellable_qty,
    initial_qty,
    today_qty,
    avg_cost,
    last_price,
    market_value,
    unrealized_pnl,
    day_unrealized_pnl,
    settled_profit,
    shareholder_id,
    captured_at
FROM position_snapshots
`

const upsertPositionSQL = `
INSERT INTO positions (
    account_id,
    symbol,
    name,
    exchange,
    quantity,
    sellable_qty,
    initial_qty,
    today_qty,
    avg_cost,
    last_price,
    market_value,
    unrealized_pnl,
    day_unrealized_pnl,
    settled_profit,
    shareholder_id,
    source,
    raw_payload,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18
)
ON CONFLICT (account_id, symbol, exchange) DO UPDATE SET
    name = EXCLUDED.name,
    quantity = EXCLUDED.quantity,
    sellable_qty = EXCLUDED.sellable_qty,
    initial_qty = EXCLUDED.initial_qty,
    today_qty = EXCLUDED.today_qty,
    avg_cost = EXCLUDED.avg_cost,
    last_price = EXCLUDED.last_price,
    market_value = EXCLUDED.market_value,
    unrealized_pnl = EXCLUDED.unrealized_pnl,
    day_unrealized_pnl = EXCLUDED.day_unrealized_pnl,
    settled_profit = EXCLUDED.settled_profit,
    shareholder_id = EXCLUDED.shareholder_id,
    source = EXCLUDED.source,
    raw_payload = EXCLUDED.raw_payload,
    updated_at = EXCLUDED.updated_at
`

const deleteStalePositionsSQL = `
DELETE FROM positions
WHERE account_id = $1
  AND updated_at < $2
`

const upsertPositionSnapshotSQL = `
INSERT INTO position_snapshots (
    trade_date,
    account_id,
    snapshot_type,
    symbol,
    name,
    exchange,
    quantity,
    sellable_qty,
    initial_qty,
    today_qty,
    avg_cost,
    last_price,
    market_value,
    unrealized_pnl,
    day_unrealized_pnl,
    settled_profit,
    shareholder_id,
    source,
    raw_payload,
    captured_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
)
ON CONFLICT (trade_date, account_id, snapshot_type, symbol, exchange) DO UPDATE SET
    name = EXCLUDED.name,
    quantity = EXCLUDED.quantity,
    sellable_qty = EXCLUDED.sellable_qty,
    initial_qty = EXCLUDED.initial_qty,
    today_qty = EXCLUDED.today_qty,
    avg_cost = EXCLUDED.avg_cost,
    last_price = EXCLUDED.last_price,
    market_value = EXCLUDED.market_value,
    unrealized_pnl = EXCLUDED.unrealized_pnl,
    day_unrealized_pnl = EXCLUDED.day_unrealized_pnl,
    settled_profit = EXCLUDED.settled_profit,
    shareholder_id = EXCLUDED.shareholder_id,
    source = EXCLUDED.source,
    raw_payload = EXCLUDED.raw_payload,
    captured_at = EXCLUDED.captured_at
`

const archiveRawStreamMessageSQL = `
INSERT INTO raw_stream_messages (
    stream_key,
    stream_id,
    direction,
    stream_role,
    message_type,
    action,
    event_type,
    status,
    code,
    account_id,
    gateway_order_id,
    origin_message_id,
    request_id,
    correlation_id,
    idempotency_key,
    body,
    body_text,
    parse_error,
    received_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19
)
ON CONFLICT (stream_key, stream_id) DO UPDATE SET
    direction = EXCLUDED.direction,
    stream_role = EXCLUDED.stream_role,
    message_type = EXCLUDED.message_type,
    action = EXCLUDED.action,
    event_type = EXCLUDED.event_type,
    status = EXCLUDED.status,
    code = EXCLUDED.code,
    account_id = EXCLUDED.account_id,
    gateway_order_id = EXCLUDED.gateway_order_id,
    origin_message_id = EXCLUDED.origin_message_id,
    request_id = EXCLUDED.request_id,
    correlation_id = EXCLUDED.correlation_id,
    idempotency_key = EXCLUDED.idempotency_key,
    body = EXCLUDED.body,
    body_text = EXCLUDED.body_text,
    parse_error = EXCLUDED.parse_error,
    received_at = EXCLUDED.received_at
`

const streamCheckpointSelectColumns = `
SELECT
    stream_key,
    stream_role,
    last_stream_id,
    last_seen_at,
    last_processed_at,
    last_error,
    processed_count,
    error_count,
    metadata,
    updated_at
FROM stream_checkpoints
`

const getStreamCheckpointSQL = streamCheckpointSelectColumns + `
WHERE stream_key = $1
`

const listStreamCheckpointsSQL = streamCheckpointSelectColumns + `
ORDER BY stream_key
`

const upsertStreamCheckpointSQL = `
INSERT INTO stream_checkpoints (
    stream_key,
    stream_role,
    last_stream_id,
    last_seen_at,
    last_processed_at,
    last_error,
    processed_count,
    error_count,
    metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
ON CONFLICT (stream_key) DO UPDATE SET
    stream_role = EXCLUDED.stream_role,
    last_stream_id = EXCLUDED.last_stream_id,
    last_seen_at = COALESCE(EXCLUDED.last_seen_at, stream_checkpoints.last_seen_at),
    last_processed_at = COALESCE(EXCLUDED.last_processed_at, stream_checkpoints.last_processed_at),
    last_error = EXCLUDED.last_error,
    processed_count = stream_checkpoints.processed_count + EXCLUDED.processed_count,
    error_count = stream_checkpoints.error_count + EXCLUDED.error_count,
    metadata = stream_checkpoints.metadata || EXCLUDED.metadata,
    updated_at = now()
`

const deadLetterPageSQL = `
SELECT
    raw.stream_key,
    raw.stream_id,
    COALESCE(raw.account_id, ''),
    COALESCE(raw.action, ''),
    COALESCE(raw.code, ''),
    COALESCE(raw.body->>'message', ''),
    COALESCE(raw.origin_message_id, ''),
    COALESCE(raw.request_id, ''),
    raw.body,
    raw.received_at,
    COALESCE(review.status, 'pending') AS review_status,
    COALESCE(review.operator, ''),
    COALESCE(review.note, ''),
    review.created_at,
    count(*) OVER() AS total_count
FROM raw_stream_messages raw
LEFT JOIN LATERAL (
    SELECT status, operator, note, created_at
    FROM stream_dlq_reviews
    WHERE stream_key = raw.stream_key
        AND stream_id = raw.stream_id
    ORDER BY review_id DESC
    LIMIT 1
) review ON true
WHERE raw.stream_role = 'dlq'
    AND ($1 = '' OR raw.account_id = $1)
    AND ($2 = '' OR COALESCE(review.status, 'pending') = $2)
ORDER BY raw.received_at DESC, raw.raw_message_pk DESC
LIMIT $3 OFFSET $4
`

const deadLetterStatusCountsSQL = `
SELECT
    COALESCE(review.status, 'pending') AS review_status,
    count(*)::bigint
FROM raw_stream_messages raw
LEFT JOIN LATERAL (
    SELECT status
    FROM stream_dlq_reviews
    WHERE stream_key = raw.stream_key
        AND stream_id = raw.stream_id
    ORDER BY review_id DESC
    LIMIT 1
) review ON true
WHERE raw.stream_role = 'dlq'
GROUP BY COALESCE(review.status, 'pending')
ORDER BY review_status
`

const insertDeadLetterReviewSQL = `
INSERT INTO stream_dlq_reviews (
    stream_key,
    stream_id,
    status,
    operator,
    note
)
SELECT $1, $2, $3, $4, $5
WHERE EXISTS (
    SELECT 1
    FROM raw_stream_messages
    WHERE stream_key = $1
        AND stream_id = $2
        AND stream_role = 'dlq'
)
RETURNING review_id, created_at
`

const deadLetterReviewsSQL = `
SELECT
    review_id,
    stream_key,
    stream_id,
    status,
    operator,
    note,
    created_at
FROM stream_dlq_reviews
WHERE stream_key = $1
    AND stream_id = $2
ORDER BY review_id DESC
`

const latestBrokerNotReadySQL = `
SELECT DISTINCT ON (account_id)
    account_id,
    code,
    COALESCE(body->>'message', ''),
    received_at
FROM raw_stream_messages
WHERE code = 'BROKER_NOT_READY'
    AND account_id IS NOT NULL
    AND account_id <> ''
    AND received_at >= $1
ORDER BY account_id, received_at DESC
`

const jobRunSelectColumns = `
SELECT
    run_id,
    job_name,
    trade_date::text,
    timezone,
    status,
    trigger,
    skipped,
    started_at,
    finished_at,
    duration_ms,
    report_json,
    error_summary,
    created_at,
    updated_at
FROM job_runs
`

const upsertJobRunSQL = `
INSERT INTO job_runs (
    run_id,
    job_name,
    trade_date,
    timezone,
    status,
    trigger,
    skipped,
    started_at,
    finished_at,
    duration_ms,
    report_json,
    error_summary
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12
)
ON CONFLICT (run_id) DO UPDATE SET
    job_name = EXCLUDED.job_name,
    trade_date = EXCLUDED.trade_date,
    timezone = EXCLUDED.timezone,
    status = EXCLUDED.status,
    trigger = EXCLUDED.trigger,
    skipped = EXCLUDED.skipped,
    started_at = COALESCE(EXCLUDED.started_at, job_runs.started_at),
    finished_at = COALESCE(EXCLUDED.finished_at, job_runs.finished_at),
    duration_ms = EXCLUDED.duration_ms,
    report_json = EXCLUDED.report_json,
    error_summary = EXCLUDED.error_summary,
    updated_at = now()
`
