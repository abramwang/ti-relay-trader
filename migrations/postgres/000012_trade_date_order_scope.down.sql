DO $$
BEGIN
    RAISE EXCEPTION
        'migration 000012 is intentionally irreversible because restoring account-scoped gateway_order_id uniqueness would discard valid cross-day order history';
END
$$;
