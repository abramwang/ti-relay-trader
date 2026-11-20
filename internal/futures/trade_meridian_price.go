package futures

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const meridianPriceSource = "meridian_futures_snapshot.v1"
const maxMeridianPriceBody = 32768

type MeridianPricePreparation struct {
	Market             TestTradeMarket `json:"market"`
	ValidUntil         time.Time       `json:"valid_until"`
	BodySHA256         string          `json:"body_sha256"`
	BusinessAuthorized bool            `json:"business_authorized"`
}

// PrepareMeridianPrice consumes an existing HTTP/SSE payload, not an OC query.
// It does not select an order price, grant authority or publish a command.
func PrepareMeridianPrice(raw []byte, mapping CatalogBinding, tick Price, tradeDate string, now time.Time) (MeridianPricePreparation, error) {
	m, err := meridianPriceMarket(raw, mapping, tick, tradeDate)
	if err != nil {
		return MeridianPricePreparation{}, err
	}
	end, err := meridianPriceDeadline(m, now)
	if err != nil {
		return MeridianPricePreparation{}, err
	}
	return MeridianPricePreparation{Market: m, ValidUntil: end, BodySHA256: commandDigest(raw)}, nil
}

func meridianPriceMarket(raw []byte, mapping CatalogBinding, tick Price, tradeDate string) (TestTradeMarket, error) {
	bad := func() (TestTradeMarket, error) {
		return TestTradeMarket{}, priceAdmissionError("invalid_meridian_snapshot")
	}
	if len(raw) == 0 || len(raw) > maxMeridianPriceBody || !validDay(tradeDate) || !validCatalogIdentity(mapping) || !validID(mapping.PublicationSnapshotID) || !validID(mapping.LogicalTableChecksum) {
		return bad()
	}
	if _, ok := checkedPositivePrice(tick); !ok {
		return bad()
	}
	root, err := parseReply(raw)
	if err != nil {
		return bad()
	}
	var rows []rawObject
	var meta rawObject
	if json.Unmarshal(root["data"], &rows) != nil || json.Unmarshal(root["meta"], &meta) != nil || len(rows) == 0 || len(rows) > 16 {
		return bad()
	}
	if textField(meta, "schema_version") != "futures_market_snapshot.v1" || textField(meta, "cache_status") != "ready" || textField(meta, "data_scope") != "realtime" || !literal(meta["process_running"], "true") || !literal(meta["stale_data"], "false") || textField(meta, "trade_date") != tradeDate || !validID(textField(meta, "session_id")) {
		return bad()
	}
	var count int
	if json.Unmarshal(meta["count"], &count) != nil || count != len(rows) {
		return bad()
	}
	var row rawObject
	for _, candidate := range rows {
		if textField(candidate, "security_id") == mapping.SecurityID {
			if row != nil {
				return bad()
			}
			row = candidate
		}
	}
	if row == nil || textField(row, "schema_version") != "futures_market_snapshot.v1" || textField(row, "source") != "ctp" || textField(row, "source_instrument_id") != mapping.SourceInstrumentID || textField(row, "exchange_id") != mapping.ExchangeID || textField(row, "session_id") != textField(meta, "session_id") || !validID(textField(row, "event_identity")) {
		return bad()
	}
	// Meridian's row uses numeric TradingDay; metadata uses a string. Neither
	// is inferred from the calendar date (night sessions may be on a prior day).
	if string(row["trading_day"]) != tradeDate {
		return bad()
	}
	event, e1 := time.Parse(time.RFC3339Nano, textField(row, "exchange_timestamp"))
	received, e2 := time.Parse(time.RFC3339Nano, textField(row, "received_at"))
	if e1 != nil || e2 != nil || received.Sub(event) < -250*time.Millisecond || received.Sub(event) > 5*time.Second {
		return bad()
	}
	observed := event
	if received.Before(observed) {
		observed = received
	}
	low, e1 := meridianDecimal(row["lower_limit_price"])
	high, e2 := meridianDecimal(row["upper_limit_price"])
	if e1 != nil || e2 != nil {
		return bad()
	}
	// Price preparation uses a usable top of book, but does not require an
	// order to be below the bid or within a second, OC-sourced price range.
	var sides [2]Price
	for i, side := range []string{"bids", "asks"} {
		var levels []rawObject
		if json.Unmarshal(row[side], &levels) != nil || len(levels) == 0 || !literal(levels[0]["level"], "1") || !literal(levels[0]["available"], "true") {
			return bad()
		}
		var volume int64
		if json.Unmarshal(levels[0]["volume"], &volume) != nil || volume <= 0 {
			return bad()
		}
		sides[i], err = meridianDecimal(levels[0]["price"])
		if err != nil || validateLimitOrderPrice(sides[i], tick, low, high) != nil {
			return bad()
		}
	}
	bid, _ := checkedPositivePrice(sides[0])
	ask, _ := checkedPositivePrice(sides[1])
	if bid.Cmp(ask) > 0 {
		return bad()
	}
	return TestTradeMarket{PriceSource: meridianPriceSource, MeridianSnapshot: bytes.Clone(raw), SecurityID: mapping.SecurityID, SourceInstrumentID: mapping.SourceInstrumentID, ExchangeID: mapping.ExchangeID, PublicationSnapshotID: mapping.PublicationSnapshotID, LogicalTableChecksum: mapping.LogicalTableChecksum, TradeDate: tradeDate, ObservedAt: observed.Format(time.RFC3339Nano), PriceTick: tick, BestBid: sides[0], LowerLimit: low, UpperLimit: high}, nil
}

// Preserve JSON decimal numbers without first rounding them through float64.
func meridianDecimal(raw []byte) (Price, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 64 {
		return "", priceAdmissionError("invalid_meridian_number")
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil || bytes.HasPrefix(raw, []byte(`"`)) {
		return "", priceAdmissionError("invalid_meridian_number")
	}
	if value, err := strconv.ParseFloat(n.String(), 64); err != nil || value <= 0 {
		return "", priceAdmissionError("invalid_meridian_number")
	}
	r, ok := new(big.Rat).SetString(n.String())
	if !ok || r.Sign() <= 0 || r.Cmp(new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 64))) >= 0 {
		return "", priceAdmissionError("invalid_meridian_number")
	}
	// Bound exponents before formatting; sub-nanotick values are not rounded.
	text := strings.TrimRight(strings.TrimRight(r.FloatString(9), "0"), ".")
	var p Price
	encoded, _ := json.Marshal(text)
	if json.Unmarshal(encoded, &p) != nil {
		return "", priceAdmissionError("invalid_meridian_number")
	}
	checked, _ := checkedPositivePrice(p)
	if checked == nil || checked.Cmp(r) != 0 {
		return "", priceAdmissionError("invalid_meridian_number")
	}
	return p, nil
}

func verifyMeridianMarket(m TestTradeMarket) error {
	b := CatalogBinding{m.SecurityID, m.SourceInstrumentID, m.ExchangeID, m.PublicationSnapshotID, m.LogicalTableChecksum}
	actual, err := meridianPriceMarket(m.MeridianSnapshot, b, m.PriceTick, m.TradeDate)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, m) {
		return priceAdmissionError("meridian_market_differs_from_snapshot")
	}
	return nil
}

func meridianPriceDeadline(m TestTradeMarket, now time.Time) (time.Time, error) {
	if err := verifyMeridianMarket(m); err != nil {
		return time.Time{}, err
	}
	var body struct {
		Data []struct {
			SecurityID string `json:"security_id"`
			Event      string `json:"exchange_timestamp"`
			Received   string `json:"received_at"`
		} `json:"data"`
	}
	if json.Unmarshal(m.MeridianSnapshot, &body) != nil {
		return time.Time{}, ErrQueryPublishGate
	}
	for _, row := range body.Data {
		if row.SecurityID != m.SecurityID {
			continue
		}
		for _, value := range []string{row.Event, row.Received} {
			ts, _ := time.Parse(time.RFC3339Nano, value)
			if ts.After(now.Add(250*time.Millisecond)) || !now.Before(ts.Add(5*time.Second)) {
				return time.Time{}, priceAdmissionError("meridian_snapshot_expired_or_future")
			}
		}
	}
	observed, _ := time.Parse(time.RFC3339Nano, m.ObservedAt)
	return observed.Add(5 * time.Second), nil
}
