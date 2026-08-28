package ledger

const etfSettlementFinalizationColumns = `
    etf_settlement_pk,
    account_id,
    source_trade_date::text,
    security_id,
    version,
    is_current,
    status,
    settlement_complete,
    redemption_quantity,
    redemption_unit,
    buy_gross_amount,
    component_sale_gross_amount,
    actual_cash_component,
    actual_cash_substitution,
    actual_total_fee,
    source_close_settlement_carry,
    gross_contribution,
    net_contribution,
    pcf_trade_date::text,
    pcf_schema_version,
    source,
    confirmed_by,
    confirmed_at,
    raw_payload,
    created_at,
    updated_at
`

const listETFSettlementFinalizationsSQL = `
SELECT ` + etfSettlementFinalizationColumns + `
FROM performance_etf_settlement_versions
WHERE account_id = $1
  AND source_trade_date = $2::date
  AND is_current
ORDER BY security_id
`

const lockETFSettlementFinalizationSQL = `
SELECT pg_advisory_xact_lock(hashtextextended(concat_ws('|', $1::text, $2::text, $3::text), 0))
`

const upsertETFSettlementFinalizationSQL = `
WITH retired AS (
    UPDATE performance_etf_settlement_versions
    SET is_current = FALSE, updated_at = now()
    WHERE account_id = $1 AND source_trade_date = $2::date AND security_id = $3 AND is_current
    RETURNING version
), next_version AS (
    SELECT COALESCE(max(version), 0) + 1 AS version
    FROM performance_etf_settlement_versions
    CROSS JOIN (SELECT count(*) FROM retired) AS retirement_barrier
    WHERE account_id = $1 AND source_trade_date = $2::date AND security_id = $3
), inserted AS (
    INSERT INTO performance_etf_settlement_versions (
        account_id, source_trade_date, security_id, version, is_current, status,
        settlement_complete, redemption_quantity, redemption_unit, buy_gross_amount,
        component_sale_gross_amount, actual_cash_component, actual_cash_substitution,
        actual_total_fee, source_close_settlement_carry, gross_contribution,
        net_contribution, pcf_trade_date, pcf_schema_version, source, confirmed_by,
        confirmed_at, raw_payload
    )
    SELECT $1, $2::date, $3, next_version.version, TRUE, $4, $5, $6, $7, $8,
           $9, $10, $11, $12, $13, $14, $15, $16::date, $17, $18, $19, $20, $21::jsonb
    FROM next_version
    RETURNING *
)
SELECT ` + etfSettlementFinalizationColumns + ` FROM inserted
`
