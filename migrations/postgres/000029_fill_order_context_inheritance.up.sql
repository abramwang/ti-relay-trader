UPDATE fills AS fill
SET
    business_type = COALESCE(NULLIF(fill.business_type, ''), orders.business_type::text),
    strategy_type = COALESCE(NULLIF(fill.strategy_type, ''), orders.strategy_type),
    strategy_id = COALESCE(NULLIF(fill.strategy_id, ''), orders.strategy_id),
    basket_id = COALESCE(NULLIF(fill.basket_id, ''), orders.basket_id),
    parent_order_id = COALESCE(NULLIF(fill.parent_order_id, ''), orders.parent_order_id),
    t0_order_group_id = COALESCE(NULLIF(fill.t0_order_group_id, ''), orders.t0_order_group_id)
FROM orders
WHERE fill.account_id = orders.account_id
    AND fill.trade_date = orders.trade_date
    AND fill.gateway_order_id = orders.gateway_order_id
    AND (
        (NULLIF(fill.business_type, '') IS NULL AND orders.business_type IS NOT NULL)
        OR (NULLIF(fill.strategy_type, '') IS NULL AND NULLIF(orders.strategy_type, '') IS NOT NULL)
        OR (NULLIF(fill.strategy_id, '') IS NULL AND NULLIF(orders.strategy_id, '') IS NOT NULL)
        OR (NULLIF(fill.basket_id, '') IS NULL AND NULLIF(orders.basket_id, '') IS NOT NULL)
        OR (NULLIF(fill.parent_order_id, '') IS NULL AND NULLIF(orders.parent_order_id, '') IS NOT NULL)
        OR (NULLIF(fill.t0_order_group_id, '') IS NULL AND NULLIF(orders.t0_order_group_id, '') IS NOT NULL)
    );
