-- One-time user-confirmed TEST-only disposition. Does not update orders or fills.
BEGIN;
DO $$
DECLARE
    candidates INTEGER;
BEGIN
    IF current_database() <> 'relay_trader_test' THEN
        RAISE EXCEPTION 'This disposition is restricted to relay_trader_test';
    END IF;
    LOCK TABLE orders IN SHARE ROW EXCLUSIVE MODE;
    SELECT count(*) INTO candidates FROM orders
    WHERE account_id = '00030484' AND NOT is_terminal
      AND trade_date IN ('2026-09-06', '2026-09-15', '2026-09-16');
    IF candidates <> 32 THEN
        RAISE EXCEPTION 'Expected exactly 32 confirmed legacy orders, got %', candidates;
    END IF;
    IF EXISTS (SELECT 1 FROM orders WHERE account_id = '00030484' AND NOT is_terminal
        AND trade_date IN ('2026-09-06', '2026-09-15', '2026-09-16')
        AND (status <> 'working' OR cum_filled_qty <> 0 OR leaves_qty <> order_qty)) THEN
        RAISE EXCEPTION 'A confirmed legacy order changed; re-confirm before disposition';
    END IF;

    INSERT INTO test_legacy_order_resolutions
        (environment, order_pk, account_id, trade_date, gateway_order_id,
         counter_session_id, confirmed_by, reason, order_snapshot, evidence)
    SELECT 'test', o.order_pk, o.account_id, o.trade_date, o.gateway_order_id,
        'hxproc-fe8bf34c4f301af7', 'user',
        'User confirmed these historical TEST orders cannot execute in the current counter; retain broker working facts',
        jsonb_build_object(
            'trade_date', o.trade_date, 'gateway_order_id', o.gateway_order_id,
            'status', o.status, 'gateway_status', o.gateway_status, 'is_terminal', o.is_terminal,
            'order_qty', o.order_qty, 'cum_filled_qty', o.cum_filled_qty, 'leaves_qty', o.leaves_qty,
            'order_id', o.order_id, 'order_stream_id', o.order_stream_id,
            'origin_message_id', o.origin_message_id, 'counter_session_id', o.adapter_context->>'counter_session_id'),
        jsonb_build_object('source', 'explicit_user_confirmation_20261005',
            'disposition', 'test_legacy_order_excluded_from_current_session_risk',
            'broker_terminal_inferred', false, 'missing_original_session_not_backfilled', true,
            'cancel_attempt_count', (SELECT count(*) FROM order_cancel_attempts a
                WHERE a.account_id = o.account_id AND a.gateway_order_id = o.gateway_order_id))
    FROM orders o
    WHERE o.account_id = '00030484' AND NOT o.is_terminal
      AND o.trade_date IN ('2026-09-06', '2026-09-15', '2026-09-16')
    ON CONFLICT (order_pk, counter_session_id) DO NOTHING;

    IF (SELECT count(*) FROM test_legacy_order_resolutions
        WHERE account_id = '00030484' AND counter_session_id = 'hxproc-fe8bf34c4f301af7') <> 32 THEN
        RAISE EXCEPTION 'Legacy disposition coverage is not exactly 32';
    END IF;
END $$;
COMMIT;
