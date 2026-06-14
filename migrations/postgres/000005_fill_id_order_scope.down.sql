DROP INDEX IF EXISTS fills_fill_id_order_unique;

CREATE UNIQUE INDEX fills_fill_id_unique
    ON fills(account_id, fill_id)
    WHERE fill_id IS NOT NULL;
