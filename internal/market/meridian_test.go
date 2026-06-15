package market

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
)

func TestMarketSnapshotsUsesPreviousTradingDayForNonTradingDay(t *testing.T) {
	var snapshotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tradingDayPath:
			if r.URL.Query().Get("date") != "20260614" {
				t.Fatalf("trading-day date = %q", r.URL.Query().Get("date"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"date":                             20260614,
					"is_trading_day":                   false,
					"previous_or_current_trading_date": 20260612,
				},
				"meta": map[string]any{"schema_version": "metadata_trading_day_status.v1"},
			})
		case snapshotsPath:
			snapshotQuery = r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"security_id":  "600000.SH",
					"trade_date":   20260612,
					"market_level": "level1",
					"last":         9.67,
				}},
				"meta": map[string]any{"schema_version": "market_snapshot.v1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewMeridianClient(config.MarketConfig{
		BaseURL:             server.URL,
		TimeoutSeconds:      1,
		SnapshotMarketLevel: "level1",
		SnapshotDataScope:   "realtime",
	})
	if err != nil {
		t.Fatalf("NewMeridianClient: %v", err)
	}
	client.now = func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	}

	response, err := client.MarketSnapshots(context.Background(), url.Values{
		"security_id": {"600000.SH"},
	})
	if err != nil {
		t.Fatalf("MarketSnapshots: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if snapshotQuery.Get("trade_date") != "20260612" || snapshotQuery.Get("data_scope") != "historical" {
		t.Fatalf("snapshot query = %s", snapshotQuery.Encode())
	}
}

func TestMarketSnapshotsFallsBackWhenRealtimeUnavailable(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tradingDayPath:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"date":                             20260612,
					"is_trading_day":                   true,
					"previous_or_current_trading_date": 20260612,
				},
			})
		case snapshotsPath:
			requests++
			if requests == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{"code": "source_unavailable", "message": "cache unavailable"},
				})
				return
			}
			if r.URL.Query().Get("trade_date") != "20260612" || r.URL.Query().Get("data_scope") != "historical" {
				t.Fatalf("fallback query = %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"security_id": "600000.SH", "last": 9.67}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewMeridianClient(config.MarketConfig{
		BaseURL:             server.URL,
		TimeoutSeconds:      1,
		SnapshotMarketLevel: "level1",
		SnapshotDataScope:   "realtime",
	})
	if err != nil {
		t.Fatalf("NewMeridianClient: %v", err)
	}
	client.now = func() time.Time {
		return time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	}

	if _, err := client.MarketSnapshots(context.Background(), url.Values{"security_id": {"600000.SH"}}); err != nil {
		t.Fatalf("MarketSnapshots: %v", err)
	}
	if requests != 2 {
		t.Fatalf("snapshot requests = %d, want 2", requests)
	}
}

func TestMarketSnapshotsScopesRealtimeToCurrentTradingDay(t *testing.T) {
	var snapshotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tradingDayPath:
			if r.URL.Query().Get("date") != "20260615" {
				t.Fatalf("trading-day date = %q", r.URL.Query().Get("date"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"date":                             20260615,
					"is_trading_day":                   true,
					"previous_or_current_trading_date": 20260615,
				},
			})
		case snapshotsPath:
			snapshotQuery = r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"security_id": "600000.SH",
					"trade_date":  20260615,
					"last":        9.72,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewMeridianClient(config.MarketConfig{
		BaseURL:             server.URL,
		TimeoutSeconds:      1,
		SnapshotMarketLevel: "level1",
		SnapshotDataScope:   "realtime",
	})
	if err != nil {
		t.Fatalf("NewMeridianClient: %v", err)
	}
	client.now = func() time.Time {
		return time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC)
	}

	if _, err := client.MarketSnapshots(context.Background(), url.Values{"security_id": {"600000.SH"}}); err != nil {
		t.Fatalf("MarketSnapshots: %v", err)
	}
	if snapshotQuery.Get("trade_date") != "20260615" || snapshotQuery.Get("data_scope") != "realtime" {
		t.Fatalf("snapshot query = %s", snapshotQuery.Encode())
	}
}

func TestMarketBarsPassesThroughQuery(t *testing.T) {
	var barsQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != barsPath {
			http.NotFound(w, r)
			return
		}
		barsQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"security_id":     "600000.SH",
				"instrument_type": "stock",
				"trade_date":      20260612,
				"datetime":        "2026-06-12T15:00:00+08:00",
				"frequency":       "1m",
				"adjustment":      "none",
				"close":           9.67,
				"schema_version":  "market_bar.v1",
			}},
			"meta": map[string]any{"schema_version": "market_bar.v1"},
		})
	}))
	defer server.Close()

	client, err := NewMeridianClient(config.MarketConfig{
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("NewMeridianClient: %v", err)
	}
	response, err := client.MarketBars(context.Background(), url.Values{
		"security_id": {"600000.SH"},
		"trade_date":  {"20260612"},
		"frequency":   {"1m"},
		"adjustment":  {"none"},
		"limit":       {"5"},
	})
	if err != nil {
		t.Fatalf("MarketBars: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if barsQuery.Get("security_id") != "600000.SH" || barsQuery.Get("frequency") != "1m" || barsQuery.Get("adjustment") != "none" {
		t.Fatalf("bars query = %s", barsQuery.Encode())
	}
}

func TestMarketBarsUsesPreviousTradingDayForToday(t *testing.T) {
	var barsQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tradingDayPath:
			if r.URL.Query().Get("date") != "20260614" {
				t.Fatalf("trading-day date = %q", r.URL.Query().Get("date"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"date":                             20260614,
					"is_trading_day":                   false,
					"previous_or_current_trading_date": 20260612,
				},
			})
		case barsPath:
			barsQuery = r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"security_id": "600000.SH",
					"trade_date":  20260612,
					"datetime":    "2026-06-12T15:00:00+08:00",
					"frequency":   "1m",
					"close":       9.67,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewMeridianClient(config.MarketConfig{
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("NewMeridianClient: %v", err)
	}
	client.now = func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	}
	response, err := client.MarketBars(context.Background(), url.Values{
		"security_id": {"600000.SH"},
		"trade_date":  {"20260614"},
		"frequency":   {"1m"},
		"limit":       {"300"},
	})
	if err != nil {
		t.Fatalf("MarketBars: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if barsQuery.Get("trade_date") != "20260612" || barsQuery.Get("data_scope") != "historical" || barsQuery.Get("limit") != "300" {
		t.Fatalf("bars query = %s", barsQuery.Encode())
	}
}

func TestMarketBarsUsesRealtimeForCurrentTradingDay(t *testing.T) {
	var barsQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tradingDayPath:
			if r.URL.Query().Get("date") != "20260615" {
				t.Fatalf("trading-day date = %q", r.URL.Query().Get("date"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"date":                             20260615,
					"is_trading_day":                   true,
					"previous_or_current_trading_date": 20260615,
				},
			})
		case barsPath:
			barsQuery = r.URL.Query()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"security_id": "600000.SH",
					"trade_date":  20260615,
					"datetime":    "2026-06-15T09:31:00+08:00",
					"frequency":   "1m",
					"close":       9.72,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewMeridianClient(config.MarketConfig{
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("NewMeridianClient: %v", err)
	}
	client.now = func() time.Time {
		return time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC)
	}
	response, err := client.MarketBars(context.Background(), url.Values{
		"security_id": {"600000.SH"},
		"trade_date":  {"20260615"},
		"frequency":   {"1m"},
		"limit":       {"300"},
	})
	if err != nil {
		t.Fatalf("MarketBars: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if barsQuery.Get("trade_date") != "20260615" || barsQuery.Get("data_scope") != "realtime" || barsQuery.Get("limit") != "300" {
		t.Fatalf("bars query = %s", barsQuery.Encode())
	}
}

func TestMarketBarsCoalescesConcurrentRequests(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != barsPath {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		requests++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"security_id": "600000.SH",
				"trade_date":  20260612,
				"datetime":    "2026-06-12T09:31:00+08:00",
				"frequency":   "1m",
				"close":       9.67,
			}},
			"meta": map[string]any{"schema_version": "market_bar.v1"},
		})
	}))
	defer server.Close()

	client, err := NewMeridianClient(config.MarketConfig{
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("NewMeridianClient: %v", err)
	}
	values := url.Values{
		"security_id": {"600000.SH"},
		"trade_date":  {"20260612"},
		"frequency":   {"1m"},
		"adjustment":  {"none"},
		"limit":       {"20"},
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := client.MarketBars(context.Background(), values)
			if err != nil {
				errCh <- err
				return
			}
			if response.StatusCode != http.StatusOK {
				errCh <- http.ErrAbortHandler
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("MarketBars concurrent call: %v", err)
		}
	}
	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 1 {
		t.Fatalf("upstream bars requests = %d, want 1", got)
	}
}

func TestMarketBarsReturnsStaleCacheOnUpstreamFailure(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != barsPath {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		requests++
		current := requests
		mu.Unlock()
		if current > 1 {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": "upstream_reset", "message": "connection reset"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"security_id": "600000.SH",
				"trade_date":  20260612,
				"datetime":    "2026-06-12T09:31:00+08:00",
				"frequency":   "1m",
				"close":       9.67,
			}},
			"meta": map[string]any{"schema_version": "market_bar.v1"},
		})
	}))
	defer server.Close()

	client, err := NewMeridianClient(config.MarketConfig{
		BaseURL:        server.URL,
		TimeoutSeconds: 1,
	})
	if err != nil {
		t.Fatalf("NewMeridianClient: %v", err)
	}
	client.barsCacheTTL = 10 * time.Millisecond
	client.barsStaleTTL = time.Minute
	values := url.Values{
		"security_id": {"600000.SH"},
		"trade_date":  {"20260612"},
		"frequency":   {"1m"},
		"adjustment":  {"none"},
		"limit":       {"20"},
	}
	if response, err := client.MarketBars(context.Background(), values); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("initial MarketBars status=%d err=%v", response.StatusCode, err)
	}
	time.Sleep(20 * time.Millisecond)
	response, err := client.MarketBars(context.Background(), values)
	if err != nil {
		t.Fatalf("stale MarketBars: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stale status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if closePrice := firstBarsClose(t, response); closePrice != 9.67 {
		t.Fatalf("stale close = %v, want 9.67", closePrice)
	}
	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 2 {
		t.Fatalf("upstream bars requests = %d, want 2", got)
	}
}

func firstBarsClose(t *testing.T, response MeridianResponse) float64 {
	t.Helper()
	rows, ok := response.Payload["data"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("missing bars data: %#v", response.Payload)
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("invalid bars row: %#v", rows[0])
	}
	closePrice, ok := row["close"].(float64)
	if !ok {
		t.Fatalf("invalid close: %#v", row["close"])
	}
	return closePrice
}
