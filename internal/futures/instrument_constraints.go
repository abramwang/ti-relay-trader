package futures

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// These are collected broker facts, not current trading permission or Meridian
// instrument master data. Raw replies retain both independent SDK observations.
type InstrumentConstraints struct {
	Symbol                   string            `json:"symbol"`
	Exchange                 string            `json:"exchange"`
	TradeDate                string            `json:"trade_date"`
	Source                   string            `json:"source"`
	SourceEnvironment        string            `json:"source_environment"`
	TradeDateSource          string            `json:"trade_date_source"`
	Static                   ConstraintStatic  `json:"static"`
	Dynamic                  ConstraintDynamic `json:"dynamic"`
	UnavailableNumericFields []string          `json:"unavailable_numeric_fields"`
}

type ConstraintStatic struct {
	PriceTick           *Price `json:"price_tick"`
	VolumeMultiple      *int64 `json:"volume_multiple"`
	MinLimitOrderVolume *int64 `json:"min_limit_order_volume"`
	MaxLimitOrderVolume *int64 `json:"max_limit_order_volume"`
	ProductClass        string `json:"product_class"`
	IsTrading           int64  `json:"is_trading"`
	InstLifePhase       string `json:"inst_life_phase"`
	OpenDate            string `json:"open_date"`
	ExpireDate          string `json:"expire_date"`
	CapturedAt          string `json:"captured_at"`
}

type ConstraintDynamic struct {
	LowerLimitPrice *Price  `json:"lower_limit_price"`
	UpperLimitPrice *Price  `json:"upper_limit_price"`
	BidPrice1       *Price  `json:"bid_price1"`
	AskPrice1       *Price  `json:"ask_price1"`
	ActionDay       *string `json:"action_day"`
	BrokerTradeDate *string `json:"broker_trade_date"`
	UpdateTime      *string `json:"update_time"`
	UpdateMillisec  *int64  `json:"update_millisec"`
	CapturedAt      string  `json:"captured_at"`
}

func cloneInstrumentConstraints(v InstrumentConstraints) InstrumentConstraints {
	v.Static.PriceTick = clonePtr(v.Static.PriceTick)
	v.Static.VolumeMultiple = clonePtr(v.Static.VolumeMultiple)
	v.Static.MinLimitOrderVolume = clonePtr(v.Static.MinLimitOrderVolume)
	v.Static.MaxLimitOrderVolume = clonePtr(v.Static.MaxLimitOrderVolume)
	v.Dynamic.LowerLimitPrice = clonePtr(v.Dynamic.LowerLimitPrice)
	v.Dynamic.UpperLimitPrice = clonePtr(v.Dynamic.UpperLimitPrice)
	v.Dynamic.BidPrice1 = clonePtr(v.Dynamic.BidPrice1)
	v.Dynamic.AskPrice1 = clonePtr(v.Dynamic.AskPrice1)
	v.Dynamic.ActionDay = clonePtr(v.Dynamic.ActionDay)
	v.Dynamic.BrokerTradeDate = clonePtr(v.Dynamic.BrokerTradeDate)
	v.Dynamic.UpdateTime = clonePtr(v.Dynamic.UpdateTime)
	v.Dynamic.UpdateMillisec = clonePtr(v.Dynamic.UpdateMillisec)
	v.UnavailableNumericFields = append([]string{}, v.UnavailableNumericFields...)
	return v
}

func decodeInstrumentConstraints(raw []byte, c QueryContext) ([]InstrumentConstraints, error) {
	bad := func() ([]InstrumentConstraints, error) { return nil, fmt.Errorf("invalid instrument constraint facts") }
	var rows []json.RawMessage
	if c.Constraints == nil || json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
		return bad()
	}
	o, err := shape(rows[0], []string{"symbol", "exchange", "trade_date", "source", "source_environment", "trade_date_source", "static", "dynamic", "unavailable_numeric_fields"}, nil)
	if err != nil || textField(o, "symbol") != c.Constraints.Symbol || textField(o, "exchange") != c.Constraints.Exchange || textField(o, "trade_date") != c.TradeDate || textField(o, "source") != "ctp_trader_api" || textField(o, "source_environment") != c.Route.Env || textField(o, "trade_date_source") != "login_query_context" {
		return bad()
	}
	s, err := shape(o["static"], []string{"price_tick", "volume_multiple", "min_limit_order_volume", "max_limit_order_volume", "product_class", "is_trading", "inst_life_phase", "open_date", "expire_date", "captured_at"}, nil)
	if err != nil {
		return bad()
	}
	d, err := shape(o["dynamic"], []string{"lower_limit_price", "upper_limit_price", "bid_price1", "ask_price1", "action_day", "broker_trade_date", "update_time", "update_millisec", "captured_at"}, nil)
	if err != nil {
		return bad()
	}
	var v InstrumentConstraints
	if json.Unmarshal(rows[0], &v) != nil || !validTime(v.Static.CapturedAt) || !validTime(v.Dynamic.CapturedAt) || !validDay(v.Static.OpenDate) || !validDay(v.Static.ExpireDate) || !validID(v.Static.ProductClass) || !validID(v.Static.InstLifePhase) || !oneOf(string(s["is_trading"]), "0", "1") || v.UnavailableNumericFields == nil {
		return bad()
	}
	missing := map[string]bool{}
	for _, field := range v.UnavailableNumericFields {
		if missing[field] {
			return bad()
		}
		missing[field] = true
	}
	for _, f := range []struct {
		name  string
		raw   json.RawMessage
		price bool
	}{
		{"PriceTick", s["price_tick"], true}, {"VolumeMultiple", s["volume_multiple"], false}, {"MinLimitOrderVolume", s["min_limit_order_volume"], false}, {"MaxLimitOrderVolume", s["max_limit_order_volume"], false},
		{"LowerLimitPrice", d["lower_limit_price"], true}, {"UpperLimitPrice", d["upper_limit_price"], true}, {"BidPrice1", d["bid_price1"], true}, {"AskPrice1", d["ask_price1"], true},
	} {
		if literal(f.raw, "null") != missing[f.name] {
			return bad()
		}
		delete(missing, f.name)
		if literal(f.raw, "null") {
			continue
		}
		if f.price {
			var p Price
			if p.UnmarshalJSON(f.raw) != nil || !positivePrice(p) {
				return bad()
			}
		} else {
			n, err := nullableInteger(f.raw)
			if err != nil || n == nil || *n < 0 {
				return bad()
			}
		}
	}
	if len(missing) != 0 {
		return bad()
	}
	for _, day := range []*string{v.Dynamic.ActionDay, v.Dynamic.BrokerTradeDate} {
		if day != nil && !validDay(*day) {
			return bad()
		}
	}
	if v.Dynamic.UpdateTime != nil {
		if _, err := time.Parse("15:04:05", *v.Dynamic.UpdateTime); err != nil {
			return bad()
		}
	}
	if v.Dynamic.UpdateMillisec != nil && (*v.Dynamic.UpdateMillisec < 0 || *v.Dynamic.UpdateMillisec > 999) {
		return bad()
	}
	return []InstrumentConstraints{v}, nil
}

func decodeConstraintsPage(o rawObject, c QueryContext) (queryPage, error) {
	bad := func() (queryPage, error) { return queryPage{}, fmt.Errorf("invalid instrument constraint reply") }
	p, err := shape(o["payload"], []string{"account_id", "product_type", "schema_version", "counter_session_id", "trade_date"}, []string{"items"})
	if err != nil || textField(p, "account_id") != c.Route.AccountID || textField(p, "product_type") != "futures" || textField(p, "schema_version") != "ctp.f0.draft.2" || textField(p, "counter_session_id") != c.CounterSessionID || textField(p, "trade_date") != c.TradeDate {
		return bad()
	}
	var a rawObject
	if json.Unmarshal(o["adapter_context"], &a) != nil || !literal(a["atomic_snapshot"], "false") {
		return bad()
	}
	g, err := nullableInteger(a["connection_generation"])
	if err != nil || !sameInteger(g, c.ConnectionGeneration) {
		return bad()
	}
	evidence, err := shape(a["query_evidence"], []string{"instrument", "depth"}, nil)
	if err != nil {
		return bad()
	}
	status := textField(o, "status")
	failed := status == "failed"
	if !failed && status != "completed" {
		return bad()
	}
	if literal(evidence["instrument"], "null") && !literal(evidence["depth"], "null") {
		return bad()
	}
	completedTimes := map[string]string{}
	requestIDs := map[int64]bool{}
	for _, name := range []string{"instrument", "depth"} {
		// An unexecuted SDK leg has no request identity or completion evidence.
		// Only failed replies may omit it; never synthesize a successful leg.
		if failed && literal(evidence[name], "null") {
			continue
		}
		e, err := shape(evidence[name], []string{"sdk_request_id", "sdk_return_code", "callback_error_id", "is_last", "row_count", "excluded_prefix_count", "state", "started_at", "completed_at"}, []string{"response_error_id"})
		if err != nil || (!literal(e["is_last"], "true") && !literal(e["is_last"], "false")) || !validID(textField(e, "state")) {
			return bad()
		}
		id, e1 := nullableInteger(e["sdk_request_id"])
		sync, e2 := nullableInteger(e["sdk_return_code"])
		callback, e3 := nullableInteger(e["callback_error_id"])
		count, e4 := nullableInteger(e["row_count"])
		excluded, e5 := nullableInteger(e["excluded_prefix_count"])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || (id != nil && *id <= 0) || count == nil || *count < 0 || excluded == nil || *excluded < 0 {
			return bad()
		}
		var response *int64
		if b, ok := e["response_error_id"]; ok {
			response, err = nullableInteger(b)
			if err != nil {
				return bad()
			}
		}
		for _, field := range []string{"started_at", "completed_at"} {
			if !validTime(textField(e, field)) && !(failed && literal(e[field], "null")) {
				return bad()
			}
		}
		if !failed {
			if id == nil || requestIDs[*id] || sync == nil || *sync != 0 || (callback != nil && *callback != 0) || (response != nil && *response != 0) || *count != 1 || !literal(e["is_last"], "true") || textField(e, "state") != "completed" {
				return bad()
			}
			requestIDs[*id] = true
			start, _ := time.Parse(time.RFC3339Nano, textField(e, "started_at"))
			end, _ := time.Parse(time.RFC3339Nano, textField(e, "completed_at"))
			produced, _ := time.Parse(time.RFC3339Nano, textField(o, "produced_at"))
			if end.Before(start) || end.After(produced) {
				return bad()
			}
			completedTimes[name] = textField(e, "completed_at")
		}
	}
	page := queryPage{messageID: textField(o, "message_id"), code: textField(o, "code"), failed: failed}
	if failed {
		if _, exists := p["items"]; exists {
			return bad()
		}
		if _, exists := o["chunk"]; exists {
			return bad()
		}
		if page.code == "OK" {
			return bad()
		}
		return page, nil
	}
	chunk, err := shape(o["chunk"], []string{"index", "is_last"}, nil)
	if err != nil || page.code != "OK" || !literal(chunk["index"], "0") || !literal(chunk["is_last"], "true") {
		return bad()
	}
	items, err := decodeInstrumentConstraints(p["items"], c)
	if err != nil || items[0].Static.CapturedAt != completedTimes["instrument"] || items[0].Dynamic.CapturedAt != completedTimes["depth"] {
		return bad()
	}
	var numbers map[string]string
	if json.Unmarshal(a["numeric_source_binary64"], &numbers) != nil {
		return bad()
	}
	for _, field := range []string{"PriceTick", "LowerLimitPrice", "UpperLimitPrice", "BidPrice1", "AskPrice1"} {
		b, err := hex.DecodeString(numbers[field])
		if err != nil || len(b) != 8 {
			return bad()
		}
	}
	if json.Unmarshal(p["items"], &page.items) != nil {
		return bad()
	}
	page.last = true
	// Copy items independently; the raw reply remains the authoritative audit.
	for i := range page.items {
		page.items[i] = bytes.Clone(page.items[i])
	}
	return page, nil
}
