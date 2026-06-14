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
	"time"

	"ti-relay-trader/internal/config"
)

const (
	instrumentsPath = "/v1/metadata/instruments"
	snapshotsPath   = "/v1/market/snapshots"
	tradingDayPath  = "/v1/metadata/trading-day"
)

type MeridianClient struct {
	baseURL             string
	snapshotMarketLevel string
	snapshotDataScope   string
	httpClient          *http.Client
	now                 func() time.Time
}

type MeridianResponse struct {
	StatusCode int
	URL        string
	Payload    map[string]any
}

func NewMeridianClient(cfg config.MarketConfig) (*MeridianClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("market.base_url is required")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
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
		now:                 time.Now,
	}, nil
}

func (client *MeridianClient) MarketSnapshots(ctx context.Context, values url.Values) (MeridianResponse, error) {
	if client == nil {
		return MeridianResponse{}, errors.New("meridian client is nil")
	}
	query := cloneValues(values)
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
		if tradeDate, err := client.previousOrCurrentTradingDate(ctx, client.today()); err == nil && tradeDate != "" && tradeDate != client.today() {
			query.Set("trade_date", tradeDate)
			query.Set("data_scope", "historical")
		}
	}

	response, err := client.getJSON(ctx, snapshotsPath, query)
	if err != nil {
		return MeridianResponse{}, err
	}
	if shouldFallbackToHistorical(query, response.Payload) {
		if tradeDate, err := client.previousOrCurrentTradingDate(ctx, client.today()); err == nil && tradeDate != "" {
			fallback := cloneValues(query)
			fallback.Set("trade_date", tradeDate)
			fallback.Set("data_scope", "historical")
			return client.getJSON(ctx, snapshotsPath, fallback)
		}
	}
	return response, nil
}

func (client *MeridianClient) MetadataInstruments(ctx context.Context, values url.Values) (MeridianResponse, error) {
	if client == nil {
		return MeridianResponse{}, errors.New("meridian client is nil")
	}
	return client.getJSON(ctx, instrumentsPath, cloneValues(values))
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
	now := client.now()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("CST", 8*60*60)
	}
	return now.In(location).Format("20060102")
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

func shouldFallbackToHistorical(values url.Values, payload map[string]any) bool {
	if strings.TrimSpace(values.Get("trade_date")) != "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(values.Get("data_scope")), "realtime") {
		return false
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		return false
	}
	code, _ := errorPayload["code"].(string)
	return code == "source_unavailable"
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
