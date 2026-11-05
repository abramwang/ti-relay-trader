// Package futures contains isolated CTP models. It is not wired into live ingestion.
package futures

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

// Local defensive limits, not negotiated OC message or pagination capacities.
const maxSnapshotBytes = 8 * 1024 * 1024
const maxSnapshotRows = 100000

var moneyPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
var accountPattern = regexp.MustCompile(`^[0-9]{6,20}$`)
var dayPattern = regexp.MustCompile(`^[0-9]{8}$`)
var timestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?\+08:00$`)

// Money preserves the source decimal text, including negative zero. It does not
// certify binary64 origin, accounting scope or a broker's invalid-value sentinel.
type Money string

func (m *Money) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("money must be a decimal string")
	}
	if len(text) > 343 || !moneyPattern.MatchString(text) {
		return fmt.Errorf("invalid money text")
	}
	*m = Money(text)
	return nil
}

// Count requires an integer JSON token; 1.0, 1e0, null and booleans are invalid.
type Count int32

func (c *Count) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("missing count")
	}
	for _, b := range raw {
		if b < '0' || b > '9' {
			return fmt.Errorf("count must be a nonnegative integer token")
		}
	}
	n, err := strconv.ParseUint(string(raw), 10, 31)
	if err != nil {
		return fmt.Errorf("count exceeds int32")
	}
	*c = Count(n)
	return nil
}

// SnapshotScope must come from a validated complete query batch, not its first row.
// These decoders check rows only; callers still own routing and publication gates.
type SnapshotScope struct {
	AccountID      string
	TradeDate      string
	NativeBrokerID string
}

type FundScope struct {
	BrokerID     string `json:"broker_id"`
	AccountID    string `json:"account_id"`
	CurrencyID   string `json:"currency_id"`
	BizType      string `json:"biz_type"`
	TradingDay   string `json:"trading_day"`
	SettlementID Count  `json:"settlement_id"`
}

type Asset struct {
	Scope            FundScope `json:"scope"`
	Balance          Money     `json:"balance"`
	Available        Money     `json:"available"`
	CurrMargin       Money     `json:"curr_margin"`
	FrozenMargin     Money     `json:"frozen_margin"`
	FrozenCash       Money     `json:"frozen_cash"`
	FrozenCommission Money     `json:"frozen_commission"`
	Commission       Money     `json:"commission"`
	PositionProfit   Money     `json:"position_profit"`
	CloseProfit      Money     `json:"close_profit"`
	Deposit          Money     `json:"deposit"`
	Withdraw         Money     `json:"withdraw"`
	ScopeVerified    bool      `json:"scope_verified"`
}

type AssetRow struct {
	AccountID  string `json:"account_id"`
	CapturedAt string `json:"captured_at"`
	Futures    Asset  `json:"futures"`
}

type Position struct {
	PosiDirection string `json:"posi_direction"`
	HedgeFlag     string `json:"hedge_flag"`
	PositionDate  string `json:"position_date"`
	InvestUnitID  string `json:"invest_unit_id"`
	Position      Count  `json:"position"`
	TodayPosition Count  `json:"today_position"`
	YDPosition    Count  `json:"yd_position"`
	LongFrozen    Count  `json:"long_frozen"`
	ShortFrozen   Count  `json:"short_frozen"`
	PositionCost  Money  `json:"position_cost"`
	OpenCost      Money  `json:"open_cost"`
	UseMargin     Money  `json:"use_margin"`
}

type PositionRow struct {
	AccountID  string   `json:"account_id"`
	Symbol     string   `json:"symbol"`
	Exchange   string   `json:"exchange"`
	CapturedAt string   `json:"captured_at"`
	Futures    Position `json:"futures"`
}

func native(text string, max int, empty bool) bool {
	if (!empty && text == "") || len(text) > max {
		return false
	}
	for i := range text {
		if text[i] < '!' || text[i] > '~' {
			return false
		}
	}
	return true
}

func validDay(day string) bool {
	_, err := time.Parse("20060102", day)
	return dayPattern.MatchString(day) && err == nil
}

func validTime(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return timestampPattern.MatchString(value) && err == nil
}

func (s SnapshotScope) validate() error {
	if !accountPattern.MatchString(s.AccountID) || !validDay(s.TradeDate) || !native(s.NativeBrokerID, 10, false) {
		return fmt.Errorf("invalid expected snapshot scope")
	}
	return nil
}

// Check duplicate keys before decoding: encoding/json normally keeps the last
// duplicate value and accepts case-insensitive struct fields. Neither is allowed.
func scanValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	keys := map[string]struct{}{}
	for d.More() {
		if delim == '{' {
			keyToken, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("invalid JSON object key")
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("duplicate JSON member")
			}
			keys[key] = struct{}{}
		}
		if err := scanValue(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func rows(raw []byte, scope SnapshotScope) ([]json.RawMessage, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if len(raw) > maxSnapshotBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("snapshot byte limit or invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := scanValue(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	var result []json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result == nil || len(result) > maxSnapshotRows {
		return nil, fmt.Errorf("snapshot must be a bounded array, not null")
	}
	return result, nil
}

func object(raw json.RawMessage, fields ...string) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if len(result) != len(fields) {
		return nil, fmt.Errorf("missing or unknown object members")
	}
	for _, field := range fields {
		if _, ok := result[field]; !ok {
			return nil, fmt.Errorf("missing required member %s", field)
		}
	}
	return result, nil
}

// DecodeAssets decodes items from an already validated complete query. It returns
// no rows on any error and never sums currencies, account scopes or margin.
func DecodeAssets(raw []byte, scope SnapshotScope) ([]AssetRow, error) {
	items, err := rows(raw, scope)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("empty asset snapshot")
	}
	result := make([]AssetRow, 0, len(items))
	seen := map[FundScope]struct{}{}
	for i, item := range items {
		fields, err := object(item, "account_id", "captured_at", "futures")
		if err != nil {
			return nil, fmt.Errorf("asset row %d: %w", i, err)
		}
		funds, err := object(fields["futures"], "scope", "balance", "available", "curr_margin", "frozen_margin", "frozen_cash", "frozen_commission", "commission", "position_profit", "close_profit", "deposit", "withdraw", "scope_verified")
		if err != nil {
			return nil, fmt.Errorf("asset row %d: %w", i, err)
		}
		if _, err := object(funds["scope"], "broker_id", "account_id", "currency_id", "biz_type", "trading_day", "settlement_id"); err != nil {
			return nil, fmt.Errorf("asset scope %d: %w", i, err)
		}
		if !bytes.Equal(bytes.TrimSpace(funds["scope_verified"]), []byte("false")) {
			return nil, fmt.Errorf("asset scope must remain explicitly unverified")
		}
		var row AssetRow
		if err := json.Unmarshal(item, &row); err != nil {
			return nil, fmt.Errorf("asset row %d: %w", i, err)
		}
		s := row.Futures.Scope
		if row.AccountID != scope.AccountID || !validTime(row.CapturedAt) || s.BrokerID != scope.NativeBrokerID ||
			s.TradingDay != scope.TradeDate || !native(s.AccountID, 12, false) || !native(s.CurrencyID, 3, false) || !native(s.BizType, 1, false) {
			return nil, fmt.Errorf("asset row %d: identity or timestamp mismatch", i)
		}
		if _, duplicate := seen[s]; duplicate {
			return nil, fmt.Errorf("duplicate fund scope")
		}
		seen[s] = struct{}{}
		result = append(result, row)
	}
	return result, nil
}

// DecodePositions keeps native direction, hedge, date and investment-unit buckets.
// An empty complete snapshot is valid; a missing/null field is never a zero value.
func DecodePositions(raw []byte, scope SnapshotScope) ([]PositionRow, error) {
	items, err := rows(raw, scope)
	if err != nil {
		return nil, err
	}
	result := make([]PositionRow, 0, len(items))
	seen := map[[6]string]struct{}{}
	for i, item := range items {
		fields, err := object(item, "account_id", "captured_at", "symbol", "exchange", "futures")
		if err != nil {
			return nil, fmt.Errorf("position row %d: %w", i, err)
		}
		position, err := object(fields["futures"], "posi_direction", "hedge_flag", "position_date", "invest_unit_id", "position", "today_position", "yd_position", "long_frozen", "short_frozen", "position_cost", "open_cost", "use_margin")
		if err != nil {
			return nil, fmt.Errorf("position row %d: %w", i, err)
		}
		if bytes.Equal(bytes.TrimSpace(position["invest_unit_id"]), []byte("null")) {
			return nil, fmt.Errorf("missing investment unit is not an empty unit")
		}
		var row PositionRow
		if err := json.Unmarshal(item, &row); err != nil {
			return nil, fmt.Errorf("position row %d: %w", i, err)
		}
		p := row.Futures
		if row.AccountID != scope.AccountID || !validTime(row.CapturedAt) || !native(row.Symbol, 80, false) || !native(row.Exchange, 8, false) ||
			!oneOf(p.PosiDirection, "1", "2", "3") || !oneOf(p.HedgeFlag, "1", "2", "3", "5", "6", "7") || !oneOf(p.PositionDate, "1", "2") || !native(p.InvestUnitID, 16, true) {
			return nil, fmt.Errorf("position row %d: invalid identity, enum or timestamp", i)
		}
		key := [6]string{row.Symbol, row.Exchange, p.PosiDirection, p.HedgeFlag, p.PositionDate, p.InvestUnitID}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate position bucket")
		}
		seen[key] = struct{}{}
		result = append(result, row)
	}
	return result, nil
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
