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
