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

const upsertOrderSQL = `
INSERT INTO orders (
    account_id,
    client_order_id,
    gateway_order_id,
    order_id,
    order_stream_id,
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
    $31, COALESCE($32, now()), $33, $34, $35, $36, $37, $38
)
ON CONFLICT (account_id, gateway_order_id) DO UPDATE SET
    client_order_id = COALESCE(EXCLUDED.client_order_id, orders.client_order_id),
    order_id = COALESCE(EXCLUDED.order_id, orders.order_id),
    order_stream_id = COALESCE(EXCLUDED.order_stream_id, orders.order_stream_id),
    created_at = CASE WHEN EXCLUDED.raw_payload ? 'created_at' THEN EXCLUDED.created_at ELSE orders.created_at END,
    symbol = EXCLUDED.symbol,
    name = EXCLUDED.name,
    exchange = EXCLUDED.exchange,
    trade_side = EXCLUDED.trade_side,
    business_type = EXCLUDED.business_type,
    offset_type = EXCLUDED.offset_type,
    limit_price = EXCLUDED.limit_price,
    order_qty = EXCLUDED.order_qty,
    submitted_qty = GREATEST(orders.submitted_qty, EXCLUDED.submitted_qty),
    cum_filled_qty = GREATEST(orders.cum_filled_qty, EXCLUDED.cum_filled_qty),
    leaves_qty = CASE WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.leaves_qty ELSE EXCLUDED.leaves_qty END,
    cancelled_qty = GREATEST(orders.cancelled_qty, EXCLUDED.cancelled_qty),
    invalid_qty = GREATEST(orders.invalid_qty, EXCLUDED.invalid_qty),
    avg_fill_price = COALESCE(EXCLUDED.avg_fill_price, orders.avg_fill_price),
    fee = EXCLUDED.fee,
    status = CASE WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.status ELSE EXCLUDED.status END,
    gateway_status = CASE WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.gateway_status ELSE EXCLUDED.gateway_status END,
    adapter_status_code = COALESCE(EXCLUDED.adapter_status_code, orders.adapter_status_code),
    adapter_status_name = COALESCE(EXCLUDED.adapter_status_name, orders.adapter_status_name),
    is_terminal = orders.is_terminal OR EXCLUDED.is_terminal,
    reject_code = COALESCE(EXCLUDED.reject_code, orders.reject_code),
    reject_message = COALESCE(EXCLUDED.reject_message, orders.reject_message),
    origin_message_id = COALESCE(EXCLUDED.origin_message_id, orders.origin_message_id),
    request_id = COALESCE(EXCLUDED.request_id, orders.request_id),
    idempotency_key = COALESCE(EXCLUDED.idempotency_key, orders.idempotency_key),
    shareholder_id = COALESCE(EXCLUDED.shareholder_id, orders.shareholder_id),
    accepted_at = COALESCE(EXCLUDED.accepted_at, orders.accepted_at),
    inserted_at = COALESCE(EXCLUDED.inserted_at, orders.inserted_at),
    last_updated_at = COALESCE(EXCLUDED.last_updated_at, orders.last_updated_at),
    terminal_at = CASE
        WHEN orders.is_terminal = TRUE AND EXCLUDED.is_terminal = FALSE THEN orders.terminal_at
        WHEN EXCLUDED.is_terminal = TRUE THEN COALESCE(EXCLUDED.terminal_at, orders.terminal_at, EXCLUDED.last_updated_at, now())
        ELSE orders.terminal_at
    END,
    raw_payload = EXCLUDED.raw_payload,
    adapter_context = orders.adapter_context || EXCLUDED.adapter_context,
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
    $11, $12, $13, $14, $15
)
ON CONFLICT DO NOTHING
`

const updateOrderStatusSQL = `
UPDATE orders SET
    order_id = COALESCE($3, order_id),
    order_stream_id = COALESCE($4, order_stream_id),
    submitted_qty = GREATEST(submitted_qty, $5),
    cum_filled_qty = GREATEST(cum_filled_qty, $6),
    leaves_qty = CASE WHEN is_terminal = TRUE AND $14 = FALSE THEN leaves_qty WHEN $7 > 0 OR $14 = TRUE THEN $7 ELSE leaves_qty END,
    cancelled_qty = GREATEST(cancelled_qty, $8),
    invalid_qty = GREATEST(invalid_qty, $9),
    avg_fill_price = COALESCE($10, avg_fill_price),
    fee = GREATEST(fee, $11),
    status = CASE WHEN is_terminal = TRUE AND $14 = FALSE THEN status ELSE $12 END,
    gateway_status = CASE WHEN is_terminal = TRUE AND $14 = FALSE THEN gateway_status ELSE $13 END,
    is_terminal = is_terminal OR $14,
    reject_code = COALESCE($15, reject_code),
    reject_message = COALESCE($16, reject_message),
    last_updated_at = COALESCE($17, now()),
    terminal_at = CASE WHEN is_terminal = TRUE AND $14 = FALSE THEN terminal_at WHEN $14 = TRUE THEN COALESCE($18, $17, terminal_at, now()) ELSE terminal_at END,
    adapter_context = adapter_context || $19::jsonb,
    updated_at = now()
WHERE account_id = $1 AND gateway_order_id = $2
`

const orderSelectColumns = `
SELECT
    account_id,
    client_order_id,
    gateway_order_id,
    order_id,
    order_stream_id,
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
    stream_key,
    stream_id,
    origin_message_id,
    request_id,
    raw_payload,
    adapter_context
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
    $21, $22
)
ON CONFLICT DO NOTHING
`

const fillSelectColumns = `
SELECT
    fill_id,
    account_id,
    gateway_order_id,
    order_id,
    order_stream_id,
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
    adapter_context
FROM fills
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
positions AS (
    SELECT
        count(*)::bigint AS positions_count,
        COALESCE(sum(market_value), 0) AS position_market_value,
        COALESCE(sum(unrealized_pnl), 0) AS unrealized_pnl,
        COALESCE(sum(settled_profit), 0) AS settled_profit
    FROM position_snapshots
    WHERE account_id = $1
        AND trade_date = $2::date
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
    positions.settled_profit,
    fills.fills_count,
    fills.buy_amount,
    fills.sell_amount,
    fills.buy_amount + fills.sell_amount AS turnover,
    fills.fee_total,
    asset.captured_at
FROM asset
CROSS JOIN positions
CROSS JOIN fills
LEFT JOIN previous_asset ON TRUE
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
positions AS (
    SELECT
        trade_date,
        count(*)::bigint AS positions_count,
        COALESCE(sum(market_value), 0) AS position_market_value,
        COALESCE(sum(unrealized_pnl), 0) AS unrealized_pnl,
        COALESCE(sum(settled_profit), 0) AS settled_profit
    FROM position_snapshots
    WHERE account_id = $1
        AND trade_date >= $2::date
        AND trade_date <= $3::date
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
    COALESCE(positions.settled_profit, 0) AS settled_profit,
    COALESCE(fills.fills_count, 0) AS fills_count,
    COALESCE(fills.buy_amount, 0) AS buy_amount,
    COALESCE(fills.sell_amount, 0) AS sell_amount,
    COALESCE(fills.buy_amount, 0) + COALESCE(fills.sell_amount, 0) AS turnover,
    COALESCE(fills.fee_total, 0) AS fee_total,
    asset.captured_at
FROM asset
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
    settled_profit,
    shareholder_id,
    updated_at
FROM positions
`

const positionSnapshotSelectColumns = `
SELECT
    account_id,
    trade_date::text,
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
    settled_profit,
    shareholder_id,
    source,
    raw_payload,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17
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
    settled_profit = EXCLUDED.settled_profit,
    shareholder_id = EXCLUDED.shareholder_id,
    source = EXCLUDED.source,
    raw_payload = EXCLUDED.raw_payload,
    updated_at = EXCLUDED.updated_at
`

const upsertPositionSnapshotSQL = `
INSERT INTO position_snapshots (
    trade_date,
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
    settled_profit,
    shareholder_id,
    source,
    raw_payload,
    captured_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18
)
ON CONFLICT (trade_date, account_id, symbol, exchange) DO UPDATE SET
    name = EXCLUDED.name,
    quantity = EXCLUDED.quantity,
    sellable_qty = EXCLUDED.sellable_qty,
    initial_qty = EXCLUDED.initial_qty,
    today_qty = EXCLUDED.today_qty,
    avg_cost = EXCLUDED.avg_cost,
    last_price = EXCLUDED.last_price,
    market_value = EXCLUDED.market_value,
    unrealized_pnl = EXCLUDED.unrealized_pnl,
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
