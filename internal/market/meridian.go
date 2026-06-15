package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/timeutil"
)

const (
	instrumentsPath    = "/v1/metadata/instruments"
	barsPath           = "/v1/market/bars"
	snapshotsPath      = "/v1/market/snapshots"
	snapshotStreamPath = "/v1/stream/market/snapshots"
	tradingDayPath     = "/v1/metadata/trading-day"
)

const (
	marketBarsCacheTTL   = 2 * time.Second
	marketBarsStaleTTL   = 60 * time.Second
	marketBarsCacheLimit = 256
)

type MeridianClient struct {
	baseURL             string
	snapshotMarketLevel string
	snapshotDataScope   string
	httpClient          *http.Client
	streamClient        *http.Client
	now                 func() time.Time
	barsGroup           singleflight.Group
	cacheMu             sync.Mutex
	cache               map[string]meridianCacheEntry
	barsCacheTTL        time.Duration
	barsStaleTTL        time.Duration
	barsCacheLimit      int
}

type MeridianResponse struct {
	StatusCode int
	URL        string
	Payload    map[string]any
}

type meridianCacheEntry struct {
	response   MeridianResponse
	expiresAt  time.Time
	staleUntil time.Time
}

func NewMeridianClient(cfg config.MarketConfig) (*MeridianClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("market.base_url is required")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	marketLevel := strings.TrimSpace(cfg.SnapshotMarketLevel)
	if marketLevel == "" {
		marketLevel = "level1"
	}
	dataScope := strings.TrimSpace(cfg.SnapshotDataScope)
	if dataScope == "" {
		dataScope = "realtime"
	}
	return &MeridianClient{
		baseURL:             baseURL,
		snapshotMarketLevel: marketLevel,
		snapshotDataScope:   dataScope,
		httpClient:          &http.Client{Timeout: timeout},
		streamClient:        &http.Client{},
		now:                 time.Now,
		cache:               make(map[string]meridianCacheEntry),
		barsCacheTTL:        marketBarsCacheTTL,
		barsStaleTTL:        marketBarsStaleTTL,
		barsCacheLimit:      marketBarsCacheLimit,
	}, nil
}

func (client *MeridianClient) MarketSnapshots(ctx context.Context, values url.Values) (MeridianResponse, error) {
	if client == nil {
		return MeridianResponse{}, errors.New("meridian client is nil")
	}
	query := cloneValues(values)
	today := client.today()
	if strings.TrimSpace(query.Get("market_level")) == "" {
		query.Set("market_level", client.snapshotMarketLevel)
	}
	if strings.TrimSpace(query.Get("data_scope")) == "" {
		query.Set("data_scope", client.snapshotDataScope)
	}
	if strings.TrimSpace(query.Get("limit")) == "" {
		query.Set("limit", "1")
	}

	if shouldUsePreviousTradingDay(query) {
		if tradeDate, err := client.previousOrCurrentTradingDate(ctx, today); err == nil && tradeDate != "" {
			query.Set("trade_date", tradeDate)
			if tradeDate != today {
				query.Set("data_scope", "historical")
			}
		}
	}

	response, err := client.getJSON(ctx, snapshotsPath, query)
	if err != nil {
		return MeridianResponse{}, err
	}
	if shouldFallbackToHistorical(query, response.Payload, today) {
		if tradeDate, err := client.previousOrCurrentTradingDate(ctx, today); err == nil && tradeDate != "" {
			fallback := cloneValues(query)
			fallback.Set("trade_date", tradeDate)
			fallback.Set("data_scope", "historical")
			return client.getJSON(ctx, snapshotsPath, fallback)
		}
	}
	return response, nil
}

func (client *MeridianClient) MarketSnapshotStream(ctx context.Context, values url.Values) (*http.Response, error) {
	if client == nil {
		return nil, errors.New("meridian client is nil")
	}
	query := cloneValues(values)
	if strings.TrimSpace(query.Get("market_level")) == "" {
		query.Set("market_level", client.snapshotMarketLevel)
	}
	if strings.TrimSpace(query.Get("include_existing")) == "" {
		query.Set("include_existing", "true")
	}
	if strings.TrimSpace(query.Get("watch_interval_ms")) == "" {
		query.Set("watch_interval_ms", "1000")
	}
	upstreamURL := client.baseURL + snapshotStreamPath
	if encoded := query.Encode(); encoded != "" {
		upstreamURL += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build meridian stream request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stream meridian snapshots: %w", err)
	}
	return resp, nil
}

func (client *MeridianClient) MetadataInstruments(ctx context.Context, values url.Values) (MeridianResponse, error) {
	if client == nil {
		return MeridianResponse{}, errors.New("meridian client is nil")
	}
	return client.getJSON(ctx, instrumentsPath, cloneValues(values))
}

func (client *MeridianClient) MarketBars(ctx context.Context, values url.Values) (MeridianResponse, error) {
	if client == nil {
		return MeridianResponse{}, errors.New("meridian client is nil")
	}
	query := cloneValues(values)
	today := client.today()
	if shouldUseBarsPreviousTradingDay(query, today) {
		date := strings.TrimSpace(query.Get("trade_date"))
		if date == "" {
			date = today
		}
		if tradeDate, err := client.previousOrCurrentTradingDate(ctx, date); err == nil && tradeDate != "" {
			query.Set("trade_date", tradeDate)
			if strings.TrimSpace(query.Get("data_scope")) == "" {
				if tradeDate == today {
					query.Set("data_scope", "realtime")
				} else {
					query.Set("data_scope", "historical")
				}
			}
		}
	}
	return client.cachedBars(ctx, query)
}

func (client *MeridianClient) cachedBars(ctx context.Context, values url.Values) (MeridianResponse, error) {
	key := cacheKey(barsPath, values)
	if response, ok := client.cachedResponse(key, time.Now(), false); ok {
		return response, nil
	}
	result, err, _ := client.barsGroup.Do(key, func() (any, error) {
		now := time.Now()
		if response, ok := client.cachedResponse(key, now, false); ok {
			return response, nil
		}
		response, err := client.getJSON(context.WithoutCancel(ctx), barsPath, values)
		if err != nil {
			if cached, ok := client.cachedResponse(key, time.Now(), true); ok {
				return cached, nil
			}
			return MeridianResponse{}, err
		}
		if response.StatusCode >= http.StatusInternalServerError {
			if cached, ok := client.cachedResponse(key, time.Now(), true); ok {
				return cached, nil
			}
			return response, nil
		}
		if shouldCacheMeridianResponse(response) {
			client.storeCachedResponse(key, response, time.Now())
		}
		return response, nil
	})
	if err != nil {
		return MeridianResponse{}, err
	}
	response, ok := result.(MeridianResponse)
	if !ok {
		return MeridianResponse{}, errors.New("invalid meridian bars cache result")
	}
	return response, nil
}

func cacheKey(path string, values url.Values) string {
	if encoded := values.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func shouldCacheMeridianResponse(response MeridianResponse) bool {
	if response.StatusCode >= http.StatusInternalServerError {
		return false
	}
	if _, ok := response.Payload["error"]; ok {
		return false
	}
	return true
}

func (client *MeridianClient) cachedResponse(key string, now time.Time, allowStale bool) (MeridianResponse, bool) {
	client.cacheMu.Lock()
	defer client.cacheMu.Unlock()
	entry, ok := client.cache[key]
	if !ok {
		return MeridianResponse{}, false
	}
	if now.Before(entry.expiresAt) || (allowStale && now.Before(entry.staleUntil)) {
		return entry.response, true
	}
	return MeridianResponse{}, false
}

func (client *MeridianClient) storeCachedResponse(key string, response MeridianResponse, now time.Time) {
	ttl := client.barsCacheTTL
	if ttl <= 0 {
		ttl = marketBarsCacheTTL
	}
	staleTTL := client.barsStaleTTL
	if staleTTL < ttl {
		staleTTL = ttl
	}
	limit := client.barsCacheLimit
	if limit <= 0 {
		limit = marketBarsCacheLimit
	}
	client.cacheMu.Lock()
	defer client.cacheMu.Unlock()
	if len(client.cache) >= limit {
		client.pruneCacheLocked(now)
	}
	if len(client.cache) >= limit {
		for existingKey := range client.cache {
			delete(client.cache, existingKey)
			break
		}
	}
	client.cache[key] = meridianCacheEntry{
		response:   response,
		expiresAt:  now.Add(ttl),
		staleUntil: now.Add(staleTTL),
	}
}

func (client *MeridianClient) pruneCacheLocked(now time.Time) {
	for key, entry := range client.cache {
		if !now.Before(entry.staleUntil) {
			delete(client.cache, key)
		}
	}
}

func (client *MeridianClient) getJSON(ctx context.Context, path string, values url.Values) (MeridianResponse, error) {
	upstreamURL := client.baseURL + path
	if encoded := values.Encode(); encoded != "" {
		upstreamURL += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return MeridianResponse{}, fmt.Errorf("build meridian request: %w", err)
	}
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return MeridianResponse{}, fmt.Errorf("request meridian: %w", err)
	}
	defer resp.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return MeridianResponse{}, fmt.Errorf("decode meridian response: %w", err)
	}
	return MeridianResponse{
		StatusCode: resp.StatusCode,
		URL:        upstreamURL,
		Payload:    payload,
	}, nil
}

func (client *MeridianClient) previousOrCurrentTradingDate(ctx context.Context, date string) (string, error) {
	values := url.Values{}
	values.Set("date", date)
	response, err := client.getJSON(ctx, tradingDayPath, values)
	if err != nil {
		return "", err
	}
	data, ok := response.Payload["data"].(map[string]any)
	if !ok {
		return "", errors.New("meridian trading-day response missing data")
	}
	value, ok := data["previous_or_current_trading_date"]
	if !ok {
		return "", errors.New("meridian trading-day response missing previous_or_current_trading_date")
	}
	return meridianDateString(value), nil
}

func (client *MeridianClient) today() string {
	return client.now().In(timeutil.Location()).Format("20060102")
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func shouldUsePreviousTradingDay(values url.Values) bool {
	return strings.EqualFold(strings.TrimSpace(values.Get("data_scope")), "realtime") &&
		strings.TrimSpace(values.Get("trade_date")) == ""
}

func shouldFallbackToHistorical(values url.Values, payload map[string]any, today string) bool {
	if !strings.EqualFold(strings.TrimSpace(values.Get("data_scope")), "realtime") {
		return false
	}
	tradeDate := compactMeridianDate(strings.TrimSpace(values.Get("trade_date")))
	if tradeDate != "" && tradeDate != today {
		return false
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		return false
	}
	code, _ := errorPayload["code"].(string)
	return code == "source_unavailable"
}

func shouldUseBarsPreviousTradingDay(values url.Values, today string) bool {
	tradeDate := compactMeridianDate(strings.TrimSpace(values.Get("trade_date")))
	return tradeDate == "" || tradeDate == today
}

func meridianDateString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}

func compactMeridianDate(value string) string {
	digits := strings.Builder{}
	for _, char := range value {
		if char >= '0' && char <= '9' {
			digits.WriteRune(char)
		}
	}
	if digits.Len() == 8 {
		return digits.String()
	}
	return strings.TrimSpace(value)
}
