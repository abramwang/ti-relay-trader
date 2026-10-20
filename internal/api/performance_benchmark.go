package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

// One security and at most 366 calendar days cannot exceed the 1000-row page.
func (s *Server) benchmarkCloses(ctx context.Context, securityID string, series []ledger.DailyPerformance) (map[string]float64, error) {
	closes := make(map[string]float64, len(series))
	var first, last time.Time
	for _, item := range series {
		normalized, err := normalizeAPIDate(item.TradeDate)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid series date", errBenchmarkBarsUnavailable)
		}
		date, _ := time.ParseInLocation("2006-01-02", normalized, timeutil.Location())
		if first.IsZero() || date.Before(first) {
			first = date
		}
		if last.IsZero() || date.After(last) {
			last = date
		}
	}
	for from := first; !from.IsZero() && !from.After(last); {
		to := from.AddDate(1, 0, 0).AddDate(0, 0, -1)
		if to.After(last) {
			to = last
		}
		response, err := s.market.MarketBars(ctx, url.Values{
			"security_id": {securityID}, "frequency": {"1d"}, "adjustment": {"none"},
			"start_date": {from.Format("20060102")}, "end_date": {to.Format("20060102")}, "limit": {"1000"},
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errBenchmarkBarsUnavailable, err)
		}
		if response.StatusCode >= http.StatusBadRequest {
			return nil, fmt.Errorf("%w: status %d", errBenchmarkBarsUnavailable, response.StatusCode)
		}
		if payload, ok := response.Payload["error"].(map[string]any); ok {
			return nil, fmt.Errorf("%w: %s", errBenchmarkBarsUnavailable, meridianErrorMessage(payload))
		}
		rows, ok := response.Payload["data"].([]any)
		if !ok || len(rows) >= 1000 {
			return nil, fmt.Errorf("%w: malformed or truncated daily bars", errBenchmarkBarsUnavailable)
		}
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok || stringFromAny(row["security_id"]) != securityID {
				return nil, fmt.Errorf("%w: daily bar security mismatch", errBenchmarkBarsUnavailable)
			}
			date, err := normalizeAPIDate(stringFromAny(row["trade_date"]))
			if err != nil || date < from.Format("2006-01-02") || date > to.Format("2006-01-02") {
				return nil, fmt.Errorf("%w: daily bar outside requested dates", errBenchmarkBarsUnavailable)
			}
			close, ok := floatFromAny(row["close"])
			if !ok || close <= 0 || math.IsNaN(close) || math.IsInf(close, 0) {
				continue
			}
			if _, duplicate := closes[date]; duplicate {
				return nil, fmt.Errorf("%w: duplicate daily bar", errBenchmarkBarsUnavailable)
			}
			closes[date] = close
		}
		from = to.AddDate(0, 0, 1)
	}
	return closes, nil
}
