UPDATE fills
SET trade_date = (
    COALESCE(
        matched_at,
        CASE
            WHEN match_timestamp IS NOT NULL AND match_timestamp > 0
                THEN to_timestamp(match_timestamp / 1000.0)
        END,
        created_at
    ) AT TIME ZONE 'Asia/Shanghai'
)::date
WHERE trade_date IS NULL;

ALTER TABLE orders
    ALTER COLUMN trade_date SET NOT NULL;

ALTER TABLE order_events
    ALTER COLUMN trade_date SET NOT NULL;

ALTER TABLE fills
    ALTER COLUMN trade_date SET NOT NULL;

ALTER TABLE order_events
    DROP CONSTRAINT order_events_order_fk;

ALTER TABLE fills
    DROP CONSTRAINT fills_order_fk;

ALTER TABLE orders
    DROP CONSTRAINT orders_gateway_order_unique;

DROP INDEX IF EXISTS orders_account_trade_date_gateway_order_unique;

ALTER TABLE orders
    ADD CONSTRAINT orders_account_trade_date_gateway_order_unique
    UNIQUE (account_id, trade_date, gateway_order_id);

DROP INDEX IF EXISTS order_events_order_idx;
CREATE INDEX order_events_order_idx
    ON order_events(account_id, trade_date, gateway_order_id, received_at);

DROP INDEX IF EXISTS fills_order_idx;
CREATE INDEX fills_order_idx
    ON fills(account_id, trade_date, gateway_order_id);

DROP INDEX IF EXISTS fills_fill_id_order_unique;
CREATE UNIQUE INDEX fills_fill_id_order_unique
    ON fills(account_id, trade_date, gateway_order_id, fill_id)
    WHERE fill_id IS NOT NULL;

DROP INDEX IF EXISTS fills_fallback_unique;
CREATE UNIQUE INDEX fills_fallback_unique
    ON fills(account_id, trade_date, order_stream_id, match_timestamp, qty, price)
    WHERE fill_id IS NULL
        AND order_stream_id IS NOT NULL
        AND match_timestamp IS NOT NULL;

ALTER TABLE order_events
    ADD CONSTRAINT order_events_order_fk
    FOREIGN KEY (account_id, trade_date, gateway_order_id)
    REFERENCES orders(account_id, trade_date, gateway_order_id)
    ON DELETE CASCADE
    NOT VALID;

ALTER TABLE fills
    ADD CONSTRAINT fills_order_fk
    FOREIGN KEY (account_id, trade_date, gateway_order_id)
    REFERENCES orders(account_id, trade_date, gateway_order_id)
    ON DELETE RESTRICT
    NOT VALID;

CREATE OR REPLACE VIEW research_order_fill_export_v1 AS
SELECT
    orders.account_id,
    orders.trade_date,
    orders.gateway_order_id,
    orders.client_order_id,
    orders.order_id,
    orders.order_stream_id,
    orders.symbol,
    orders.name,
    orders.exchange,
    orders.trade_side,
    orders.business_type,
    orders.limit_price,
    orders.order_qty,
    orders.cum_filled_qty,
    orders.avg_fill_price,
    orders.fee AS order_fee,
    orders.status,
    orders.gateway_status,
    orders.is_terminal,
    orders.reject_code,
    orders.reject_message,
    orders.created_at,
    orders.accepted_at,
    orders.inserted_at,
    orders.last_updated_at,
    orders.terminal_at,
    fills.fill_id,
    fills.order_id AS fill_order_id,
    fills.order_stream_id AS fill_order_stream_id,
    fills.price AS fill_price,
    fills.qty AS fill_qty,
    fills.fee AS fill_fee,
    fills.matched_at,
    fills.match_timestamp
FROM orders
LEFT JOIN fills
    ON fills.account_id = orders.account_id
    AND fills.trade_date = orders.trade_date
    AND fills.gateway_order_id = orders.gateway_order_id;
