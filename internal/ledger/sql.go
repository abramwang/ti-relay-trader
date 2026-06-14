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
    symbol = EXCLUDED.symbol,
    name = EXCLUDED.name,
    exchange = EXCLUDED.exchange,
    trade_side = EXCLUDED.trade_side,
    business_type = EXCLUDED.business_type,
    offset_type = EXCLUDED.offset_type,
    limit_price = EXCLUDED.limit_price,
    order_qty = EXCLUDED.order_qty,
    submitted_qty = EXCLUDED.submitted_qty,
    cum_filled_qty = EXCLUDED.cum_filled_qty,
    leaves_qty = EXCLUDED.leaves_qty,
    cancelled_qty = EXCLUDED.cancelled_qty,
    invalid_qty = EXCLUDED.invalid_qty,
    avg_fill_price = COALESCE(EXCLUDED.avg_fill_price, orders.avg_fill_price),
    fee = EXCLUDED.fee,
    status = EXCLUDED.status,
    gateway_status = EXCLUDED.gateway_status,
    adapter_status_code = COALESCE(EXCLUDED.adapter_status_code, orders.adapter_status_code),
    adapter_status_name = COALESCE(EXCLUDED.adapter_status_name, orders.adapter_status_name),
    is_terminal = EXCLUDED.is_terminal,
    reject_code = COALESCE(EXCLUDED.reject_code, orders.reject_code),
    reject_message = COALESCE(EXCLUDED.reject_message, orders.reject_message),
    origin_message_id = COALESCE(EXCLUDED.origin_message_id, orders.origin_message_id),
    request_id = COALESCE(EXCLUDED.request_id, orders.request_id),
    idempotency_key = COALESCE(EXCLUDED.idempotency_key, orders.idempotency_key),
    shareholder_id = COALESCE(EXCLUDED.shareholder_id, orders.shareholder_id),
    accepted_at = COALESCE(EXCLUDED.accepted_at, orders.accepted_at),
    inserted_at = COALESCE(EXCLUDED.inserted_at, orders.inserted_at),
    last_updated_at = COALESCE(EXCLUDED.last_updated_at, orders.last_updated_at),
    terminal_at = COALESCE(EXCLUDED.terminal_at, orders.terminal_at),
    raw_payload = EXCLUDED.raw_payload,
    adapter_context = EXCLUDED.adapter_context,
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
    leaves_qty = CASE WHEN $7 > 0 OR $14 = TRUE THEN $7 ELSE leaves_qty END,
    cancelled_qty = GREATEST(cancelled_qty, $8),
    invalid_qty = GREATEST(invalid_qty, $9),
    avg_fill_price = COALESCE($10, avg_fill_price),
    fee = GREATEST(fee, $11),
    status = $12,
    gateway_status = $13,
    is_terminal = $14,
    reject_code = COALESCE($15, reject_code),
    reject_message = COALESCE($16, reject_message),
    last_updated_at = COALESCE($17, now()),
    terminal_at = CASE WHEN $14 = TRUE THEN COALESCE($18, $17, terminal_at, now()) ELSE terminal_at END,
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

const positionSelectColumns = `
SELECT
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
    updated_at
FROM positions
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
