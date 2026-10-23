package api

import (
	"context"
	"math"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

// Reuse a published close only when the visible asset snapshot still matches it.
// This avoids estimating account returns from mutable broker position costs.
func (s *Server) enrichAssetWithPublishedDayPnL(ctx context.Context, asset trading.Asset) trading.Asset {
	if s.cfg.Service.Environment != config.EnvironmentProduction || asset.AccountID == "" || asset.UpdatedAt.IsZero() {
		return asset
	}
	at := timeutil.InBusinessLocation(asset.UpdatedAt)
	closeAt := time.Date(at.Year(), at.Month(), at.Day(), 15, 0, 0, 0, timeutil.Location())
	if at.Before(closeAt) {
		return asset
	}
	return s.enrichCloseAssetWithPublishedDayPnL(ctx, asset, at.Format("2006-01-02"))
}

func (s *Server) enrichCloseAssetWithPublishedDayPnL(ctx context.Context, asset trading.Asset, date string) trading.Asset {
	if s.cfg.Service.Environment != config.EnvironmentProduction || asset.AccountID == "" {
		return asset
	}
	asset.AccountDayPnL = nil
	asset.AccountDayPnLSource = ""
	asset.AccountDayPnLTradeDate = date
	asset.AccountDayPnLStatus = "pending"
	asset.AccountDayPnLReason = "published_nav_unavailable"
	if s.perf == nil {
		return asset
	}
	navs, err := s.perf.ListPerformanceNAVs(ctx, asset.AccountID, date, date)
	if err != nil {
		s.logger.Warn("asset_published_day_pnl_unavailable", "account_id", asset.AccountID, "error", err)
		asset.AccountDayPnLReason = "published_nav_query_failed"
		return asset
	}
	for _, nav := range navs {
		if nav.AccountID != asset.AccountID || nav.TradeDate != date || !nav.IsCurrent {
			continue
		}
		if nav.Status == "blocked" {
			asset.AccountDayPnLStatus = "blocked"
			asset.AccountDayPnLReason = "published_nav_blocked"
			return asset
		}
		if (nav.Status != "provisional" && nav.Status != "finalized") ||
			!strings.HasPrefix(nav.FormulaVersion, "performance_economic_nav.v2.") ||
			math.IsNaN(nav.AccountDayPnL) || math.IsInf(nav.AccountDayPnL, 0) {
			asset.AccountDayPnLReason = "published_nav_not_eligible"
			continue
		}
		valuation, ok := nav.PnLComponents["market_valuation"].(map[string]any)
		if !ok {
			asset.AccountDayPnLReason = "published_nav_valuation_missing"
			continue
		}
		cash, cashOK := floatFromAny(valuation["close_visible_cash"])
		marketValue, marketOK := floatFromAny(valuation["close_position_value"])
		matches := func(left, right float64) bool {
			return !math.IsNaN(left) && !math.IsInf(left, 0) && !math.IsNaN(right) && !math.IsInf(right, 0) && math.Abs(left-right) <= 0.01
		}
		if !cashOK || !marketOK || !matches(cash, asset.CashTotal) || !matches(marketValue, asset.MarketValue) || !matches(nav.CloseEconomicNAV, asset.NetAsset) {
			asset.AccountDayPnLStatus = "snapshot_mismatch"
			asset.AccountDayPnLReason = "published_nav_snapshot_mismatch"
			continue
		}
		pnl := nav.AccountDayPnL
		asset.DayProfit = pnl
		asset.AccountDayPnL = &pnl
		asset.AccountDayPnLSource = nav.FormulaVersion
		asset.AccountDayPnLTradeDate = date
		asset.AccountDayPnLStatus = "available"
		asset.AccountDayPnLReason = ""
		return asset
	}
	return asset
}
