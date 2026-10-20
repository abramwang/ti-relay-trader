package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
)

func TestBenchmarkDailyRangesAreBounded(t *testing.T) {
	var starts, ends []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		starts = append(starts, r.URL.Query().Get("start_date"))
		ends = append(ends, r.URL.Query().Get("end_date"))
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Market.BaseURL = upstream.URL
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{market: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	series := []ledger.DailyPerformance{{TradeDate: "2025-01-01"}, {TradeDate: "2026-10-19"}}
	if _, err := s.benchmarkCloses(context.Background(), "000001.SH", series); err != nil {
		t.Fatal(err)
	}
	if len(starts) != 2 || starts[0] != "20250101" || ends[0] != "20251231" || starts[1] != "20260101" || ends[1] != "20261019" {
		t.Fatalf("incorrect bounded requests: %v/%v", starts, ends)
	}
}

func TestBenchmarkDailyBarsRejectBadIdentityOrTruncation(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []any
	}{
		{"wrong security", []any{map[string]any{"security_id": "600000.SH", "trade_date": "20261019", "close": 1}}},
		{"outside range", []any{map[string]any{"security_id": "000001.SH", "trade_date": "20261018", "close": 1}}},
		{"duplicate date", []any{map[string]any{"security_id": "000001.SH", "trade_date": "20261019", "close": 1}, map[string]any{"security_id": "000001.SH", "trade_date": "20261019", "close": 2}}},
		{"truncated", make([]any, 1000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": tc.rows})
			}))
			defer upstream.Close()
			cfg := config.Default()
			cfg.Market.BaseURL = upstream.URL
			client, err := market.NewMeridianClient(cfg.Market)
			if err != nil {
				t.Fatal(err)
			}
			s := &Server{market: client}
			if _, err := s.benchmarkCloses(context.Background(), "000001.SH", []ledger.DailyPerformance{{TradeDate: "2026-10-19"}}); err == nil {
				t.Fatal("invalid benchmark bars accepted")
			}
		})
	}
}
