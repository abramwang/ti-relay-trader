ALTER TABLE orders
    ADD COLUMN trade_date DATE,
    ADD COLUMN strategy_type TEXT,
    ADD COLUMN strategy_id TEXT,
    ADD COLUMN basket_id TEXT,
    ADD COLUMN parent_order_id TEXT,
    ADD COLUMN t0_order_group_id TEXT;

UPDATE orders
SET trade_date = (
    COALESCE(inserted_at, accepted_at, created_at, last_updated_at, terminal_at, updated_at) AT TIME ZONE 'Asia/Shanghai'
)::date
WHERE trade_date IS NULL
    AND COALESCE(inserted_at, accepted_at, created_at, last_updated_at, terminal_at, updated_at) IS NOT NULL;

CREATE UNIQUE INDEX orders_account_trade_date_gateway_order_unique
    ON orders(account_id, trade_date, gateway_order_id)
    WHERE trade_date IS NOT NULL;

CREATE INDEX orders_account_trade_date_idx
    ON orders(account_id, trade_date, COALESCE(last_updated_at, created_at) DESC);

CREATE INDEX orders_strategy_attribution_idx
    ON orders(account_id, trade_date, strategy_type, strategy_id)
    WHERE strategy_type IS NOT NULL AND strategy_type <> '';

CREATE INDEX orders_basket_attribution_idx
    ON orders(account_id, trade_date, basket_id)
    WHERE basket_id IS NOT NULL AND basket_id <> '';

CREATE INDEX orders_t0_group_idx
    ON orders(account_id, trade_date, t0_order_group_id)
    WHERE t0_order_group_id IS NOT NULL AND t0_order_group_id <> '';

ALTER TABLE order_events
    ADD COLUMN trade_date DATE,
    ADD COLUMN strategy_type TEXT,
    ADD COLUMN strategy_id TEXT,
    ADD COLUMN basket_id TEXT,
    ADD COLUMN parent_order_id TEXT,
    ADD COLUMN t0_order_group_id TEXT;

UPDATE order_events
SET trade_date = (
    COALESCE(produced_at, received_at) AT TIME ZONE 'Asia/Shanghai'
)::date
WHERE trade_date IS NULL
    AND COALESCE(produced_at, received_at) IS NOT NULL;

CREATE INDEX order_events_account_trade_date_idx
    ON order_events(account_id, trade_date, received_at);

ALTER TABLE fills
    ADD COLUMN business_type TEXT,
    ADD COLUMN strategy_type TEXT,
    ADD COLUMN strategy_id TEXT,
    ADD COLUMN basket_id TEXT,
    ADD COLUMN parent_order_id TEXT,
    ADD COLUMN t0_order_group_id TEXT;

UPDATE fills
SET
    business_type = COALESCE(NULLIF(adapter_context->>'business_type', ''), business_type),
    strategy_type = COALESCE(NULLIF(adapter_context->>'strategy_type', ''), strategy_type),
    strategy_id = COALESCE(NULLIF(adapter_context->>'strategy_id', ''), strategy_id),
    basket_id = COALESCE(NULLIF(adapter_context->>'basket_id', ''), basket_id),
    parent_order_id = COALESCE(NULLIF(adapter_context->>'parent_order_id', ''), parent_order_id),
    t0_order_group_id = COALESCE(NULLIF(adapter_context->>'t0_order_group_id', ''), t0_order_group_id)
WHERE business_type IS NULL
    OR strategy_type IS NULL
    OR strategy_id IS NULL
    OR basket_id IS NULL
    OR parent_order_id IS NULL
    OR t0_order_group_id IS NULL;

UPDATE fills
SET
    business_type = COALESCE(fills.business_type, orders.business_type::text),
    strategy_type = COALESCE(fills.strategy_type, orders.strategy_type),
    strategy_id = COALESCE(fills.strategy_id, orders.strategy_id),
    basket_id = COALESCE(fills.basket_id, orders.basket_id),
    parent_order_id = COALESCE(fills.parent_order_id, orders.parent_order_id),
    t0_order_group_id = COALESCE(fills.t0_order_group_id, orders.t0_order_group_id)
FROM orders
WHERE fills.account_id = orders.account_id
    AND fills.gateway_order_id = orders.gateway_order_id
    AND (
        fills.business_type IS NULL
        OR fills.strategy_type IS NULL
        OR fills.strategy_id IS NULL
        OR fills.basket_id IS NULL
        OR fills.parent_order_id IS NULL
        OR fills.t0_order_group_id IS NULL
    );

CREATE INDEX fills_strategy_attribution_idx
    ON fills(account_id, trade_date, strategy_type, strategy_id)
    WHERE strategy_type IS NOT NULL AND strategy_type <> '';

CREATE INDEX fills_basket_attribution_idx
    ON fills(account_id, trade_date, basket_id)
    WHERE basket_id IS NOT NULL AND basket_id <> '';

CREATE INDEX fills_t0_group_idx
    ON fills(account_id, trade_date, t0_order_group_id)
    WHERE t0_order_group_id IS NOT NULL AND t0_order_group_id <> '';

CREATE TABLE performance_attribution_links (
    attribution_link_pk BIGSERIAL PRIMARY KEY,
    link_id TEXT NOT NULL UNIQUE,
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE RESTRICT,
    trade_date DATE NOT NULL,
    strategy_type TEXT NOT NULL,
    strategy_id TEXT NOT NULL DEFAULT '',
    basket_id TEXT NOT NULL DEFAULT '',
    parent_order_id TEXT NOT NULL DEFAULT '',
    t0_order_group_id TEXT NOT NULL DEFAULT '',
    security_id TEXT NOT NULL DEFAULT '',
    symbol TEXT NOT NULL DEFAULT '',
    exchange TEXT NOT NULL DEFAULT '',
    gateway_order_id TEXT,
    order_id BIGINT,
    order_stream_id TEXT,
    fill_id TEXT,
    link_type TEXT NOT NULL,
    quantity NUMERIC(20, 6) NOT NULL DEFAULT 0,
    amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    cost_amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    pnl_amount NUMERIC(20, 6) NOT NULL DEFAULT 0,
    estimation_method TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'relay',
    quality_flags JSONB NOT NULL DEFAULT '[]'::jsonb,
    source_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT performance_attribution_links_type_check CHECK (
        link_type IN ('order', 'fill', 'transfer', 'position', 'cash', 'nav_component', 'manual_adjustment')
    )
);

CREATE INDEX performance_attribution_links_strategy_idx
    ON performance_attribution_links(account_id, trade_date, strategy_type, strategy_id);

CREATE INDEX performance_attribution_links_order_idx
    ON performance_attribution_links(account_id, trade_date, gateway_order_id)
    WHERE gateway_order_id IS NOT NULL;

CREATE INDEX performance_attribution_links_fill_idx
    ON performance_attribution_links(account_id, trade_date, gateway_order_id, fill_id)
    WHERE fill_id IS NOT NULL;

CREATE INDEX performance_attribution_links_t0_idx
    ON performance_attribution_links(account_id, trade_date, t0_order_group_id)
    WHERE t0_order_group_id <> '';
