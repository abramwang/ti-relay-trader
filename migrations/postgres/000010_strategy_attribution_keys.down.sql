DROP TABLE IF EXISTS performance_attribution_links;

DROP INDEX IF EXISTS fills_t0_group_idx;
DROP INDEX IF EXISTS fills_basket_attribution_idx;
DROP INDEX IF EXISTS fills_strategy_attribution_idx;

ALTER TABLE fills
    DROP COLUMN IF EXISTS t0_order_group_id,
    DROP COLUMN IF EXISTS parent_order_id,
    DROP COLUMN IF EXISTS basket_id,
    DROP COLUMN IF EXISTS strategy_id,
    DROP COLUMN IF EXISTS strategy_type,
    DROP COLUMN IF EXISTS business_type;

DROP INDEX IF EXISTS order_events_account_trade_date_idx;

ALTER TABLE order_events
    DROP COLUMN IF EXISTS t0_order_group_id,
    DROP COLUMN IF EXISTS parent_order_id,
    DROP COLUMN IF EXISTS basket_id,
    DROP COLUMN IF EXISTS strategy_id,
    DROP COLUMN IF EXISTS strategy_type,
    DROP COLUMN IF EXISTS trade_date;

DROP INDEX IF EXISTS orders_t0_group_idx;
DROP INDEX IF EXISTS orders_basket_attribution_idx;
DROP INDEX IF EXISTS orders_strategy_attribution_idx;
DROP INDEX IF EXISTS orders_account_trade_date_idx;
DROP INDEX IF EXISTS orders_account_trade_date_gateway_order_unique;

ALTER TABLE orders
    DROP COLUMN IF EXISTS t0_order_group_id,
    DROP COLUMN IF EXISTS parent_order_id,
    DROP COLUMN IF EXISTS basket_id,
    DROP COLUMN IF EXISTS strategy_id,
    DROP COLUMN IF EXISTS strategy_type,
    DROP COLUMN IF EXISTS trade_date;
