DROP VIEW IF EXISTS research_account_daily_performance_v1;

DELETE FROM position_snapshots
WHERE snapshot_type <> 'close';

ALTER TABLE position_snapshots
    DROP CONSTRAINT IF EXISTS position_snapshots_unique;

ALTER TABLE position_snapshots
    DROP CONSTRAINT IF EXISTS position_snapshots_type_check;

DROP INDEX IF EXISTS position_snapshots_account_date_type_idx;

ALTER TABLE position_snapshots
    DROP COLUMN IF EXISTS snapshot_type;

ALTER TABLE position_snapshots
    ADD CONSTRAINT position_snapshots_unique
    UNIQUE (trade_date, account_id, symbol, exchange);

CREATE INDEX IF NOT EXISTS position_snapshots_account_date_idx
    ON position_snapshots(account_id, trade_date);

CREATE VIEW research_account_daily_performance_v1 AS
WITH asset_ranked AS (
    SELECT
        account_id,
        trade_date,
        cash_available,
        cash_total,
        net_asset,
        market_value,
        stock_value,
        fund_value,
        day_profit,
        position_profit,
        close_profit,
        credit,
        captured_at,
        row_number() OVER (
            PARTITION BY account_id, trade_date
            ORDER BY captured_at DESC, asset_snapshot_pk DESC
        ) AS rn
    FROM asset_snapshots
    WHERE snapshot_type = 'close'
),
asset AS (
    SELECT
        account_id,
        trade_date,
        cash_available,
        cash_total,
        net_asset,
        COALESCE(lag(net_asset) OVER (PARTITION BY account_id ORDER BY trade_date), 0) AS previous_net_asset,
        market_value,
        stock_value,
        fund_value,
        day_profit,
        position_profit,
        close_profit,
        credit,
        captured_at
    FROM asset_ranked
    WHERE rn = 1
),
positions AS (
    SELECT
        account_id,
        trade_date,
        count(*)::bigint AS positions_count,
        COALESCE(sum(market_value), 0) AS position_market_value,
        COALESCE(sum(unrealized_pnl), 0) AS unrealized_pnl,
        COALESCE(sum(day_unrealized_pnl), 0) AS day_unrealized_pnl,
        COALESCE(sum(settled_profit), 0) AS settled_profit
    FROM position_snapshots
    GROUP BY account_id, trade_date
),
fills_by_date AS (
    SELECT
        account_id,
        CASE
            WHEN trade_date IS NOT NULL THEN trade_date
            ELSE (COALESCE(matched_at, created_at) AT TIME ZONE 'Asia/Shanghai')::date
        END AS trade_date,
        count(*)::bigint AS fills_count,
        COALESCE(sum(CASE WHEN trade_side IN ('B', 'P') THEN price * qty ELSE 0 END), 0) AS buy_amount,
        COALESCE(sum(CASE WHEN trade_side IN ('S', 'R') THEN price * qty ELSE 0 END), 0) AS sell_amount,
        COALESCE(sum(fee), 0) AS fee_total
    FROM fills
    GROUP BY account_id, CASE
        WHEN trade_date IS NOT NULL THEN trade_date
        ELSE (COALESCE(matched_at, created_at) AT TIME ZONE 'Asia/Shanghai')::date
    END
)
SELECT
    asset.account_id,
    asset.trade_date,
    asset.cash_available,
    asset.cash_total,
    asset.net_asset,
    asset.previous_net_asset,
    CASE WHEN asset.previous_net_asset > 0 THEN asset.net_asset - asset.previous_net_asset ELSE 0 END AS daily_pnl,
    CASE WHEN asset.previous_net_asset > 0 THEN (asset.net_asset - asset.previous_net_asset) / asset.previous_net_asset ELSE 0 END AS return_rate,
    asset.market_value,
    asset.stock_value,
    asset.fund_value,
    asset.day_profit,
    asset.position_profit,
    asset.close_profit,
    asset.credit,
    COALESCE(positions.positions_count, 0) AS positions_count,
    COALESCE(positions.position_market_value, 0) AS position_market_value,
    COALESCE(positions.unrealized_pnl, 0) AS unrealized_pnl,
    COALESCE(positions.day_unrealized_pnl, 0) AS day_unrealized_pnl,
    COALESCE(positions.settled_profit, 0) AS settled_profit,
    COALESCE(positions.settled_profit, 0) AS realized_pnl,
    COALESCE(positions.settled_profit, 0) + COALESCE(positions.day_unrealized_pnl, 0) AS gross_pnl,
    COALESCE(positions.settled_profit, 0) + COALESCE(positions.day_unrealized_pnl, 0) - COALESCE(fills_by_date.fee_total, 0) AS net_pnl,
    COALESCE(fills_by_date.fills_count, 0) AS fills_count,
    COALESCE(fills_by_date.buy_amount, 0) AS buy_amount,
    COALESCE(fills_by_date.sell_amount, 0) AS sell_amount,
    COALESCE(fills_by_date.buy_amount, 0) + COALESCE(fills_by_date.sell_amount, 0) AS turnover,
    COALESCE(fills_by_date.fee_total, 0) AS fee_total,
    asset.captured_at
FROM asset
LEFT JOIN positions ON positions.account_id = asset.account_id AND positions.trade_date = asset.trade_date
LEFT JOIN fills_by_date ON fills_by_date.account_id = asset.account_id AND fills_by_date.trade_date = asset.trade_date;
