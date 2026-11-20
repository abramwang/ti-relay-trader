package accountbaseline

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
)

// decimal preserves the source decimal value without a float64 round trip.
// Bound both the token and exponent before allocating arbitrary-precision data.
func decimal(raw json.RawMessage) (string, error) {
	s := string(raw)
	if strings.HasPrefix(s, `"`) {
		s = text(raw)
	}
	if len(s) == 0 || len(s) > 256 || !json.Valid([]byte(s)) || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) {
		return "", errors.New("invalid decimal")
	}
	mantissa, exponent := s, 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa = s[:i]
		n, err := strconv.Atoi(s[i+1:])
		if err != nil || n < -100 || n > 100 {
			return "", errors.New("decimal exponent outside supported range")
		}
		exponent = n
	}
	scale := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		scale = len(mantissa) - i - 1
	}
	scale -= exponent
	if scale < 0 {
		scale = 0
	}
	if scale > 100 {
		return "", errors.New("decimal precision outside supported range")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "", errors.New("invalid decimal")
	}
	out := r.FloatString(scale)
	if strings.Contains(out, ".") {
		out = strings.TrimRight(strings.TrimRight(out, "0"), ".")
	}
	if out == "-0" {
		out = "0"
	}
	return out, nil
}

func optionalInteger(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return integer(raw)
}

func sourceFields(e redisstream.EntryEnvelope) map[string]any {
	return map[string]any{"source": "broker_query", "account_id": e.Routing.AccountID,
		"source_query_message_id": e.OriginMessageID, "source_reply_message_id": e.MessageID,
		"source_stream_id": e.StreamID, "source_observed_at": nil, "source_published_at": e.ProducedAt,
		"cash_freeze_inclusion": "unknown", "sellable_freeze_inclusion": "unknown"}
}

func sourceDate(raw json.RawMessage) (string, error) {
	s := strings.ReplaceAll(text(raw), "-", "")
	t, err := time.Parse("20060102", s)
	if err != nil || t.Format("20060102") != s {
		return "", errors.New("missing source trade date")
	}
	return s, nil
}

func sourceItem(m map[string]json.RawMessage, account string) error {
	if m == nil {
		return errors.New("null item")
	}
	if a, ok := m["account_id"]; ok && text(a) != account {
		return errors.New("item account mismatch")
	}
	exchange := text(m["exchange"])
	if exchange != "SH" && exchange != "SZ" {
		return errors.New("not a supported securities exchange")
	}
	if s := text(m["symbol"]); s == "" || strings.TrimSpace(s) != s {
		return errors.New("missing security")
	}
	return nil
}

func project(resource string, pages []redisstream.EntryEnvelope) ([]ledger.CurrentBaselineRecord, json.RawMessage, error) {
	var rows []ledger.CurrentBaselineRecord
	var cash json.RawMessage
	seen := map[string]bool{}
	for _, e := range pages {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(e.Payload, &payload); err != nil || payload == nil {
			return nil, nil, errors.New("missing payload")
		}
		if a, ok := payload["account_id"]; ok && text(a) != e.Routing.AccountID {
			return nil, nil, errors.New("payload account mismatch")
		}
		if resource == "asset" {
			if _, ok := payload["account"]; !ok {
				return nil, nil, errors.New("missing asset account")
			}
			if cash != nil {
				return nil, nil, errors.New("ambiguous multiple cash observations")
			}
			var err error
			cash, err = projectCash(e, payload["account"])
			if err != nil {
				return nil, nil, err
			}
			continue
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(payload["items"], &items); err != nil || items == nil {
			return nil, nil, errors.New("missing explicit items array")
		}
		if n, ok := payload["item_count"]; ok {
			count, err := integer(n)
			if err != nil || count != int64(len(items)) {
				return nil, nil, errors.New("item count mismatch")
			}
		}
		for _, m := range items {
			if err := sourceItem(m, e.Routing.AccountID); err != nil {
				return nil, nil, err
			}
		}
		var projected []ledger.CurrentBaselineRecord
		var err error
		switch resource {
		case "positions":
			projected, err = projectPositions(e, items)
		case "orders":
			projected, err = projectOrders(e, items)
		case "fills":
			// Component transfers need their own coverage and event reconciliation;
			// never silently discard them and certify the ordinary fill subset.
			if v, ok := payload["component_transfers"]; ok {
				var transfers []json.RawMessage
				if json.Unmarshal(v, &transfers) != nil || len(transfers) > 0 {
					return nil, nil, errors.New("component transfer coverage unresolved")
				}
			}
			projected, err = projectFills(e, items)
		default:
			return nil, nil, errors.New("unknown resource")
		}
		if err != nil {
			return nil, nil, err
		}
		for _, r := range projected {
			if seen[r.Key] {
				return nil, nil, errors.New("duplicate resource identity")
			}
			seen[r.Key] = true
			rows = append(rows, r)
		}
		if len(rows) > 100000 {
			return nil, nil, ledger.ErrCurrentBaselineLimit
		}
	}
	return rows, cash, nil
}

func record(key string, m map[string]any) ledger.CurrentBaselineRecord {
	m["record_id"] = key
	raw, _ := json.Marshal(m)
	return ledger.CurrentBaselineRecord{Key: key, Document: raw}
}

func projectCash(e redisstream.EntryEnvelope, raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil || text(m["account_id"]) != e.Routing.AccountID {
		return nil, errors.New("missing cash scope")
	}
	out := sourceFields(e)
	for _, key := range []string{"cash_available", "cash_total"} {
		v, err := decimal(m[key])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		out[key] = v
	}
	out["frozen_cash"] = nil
	out["frozen_fee"] = nil
	out["available_includes_frozen_deduction"] = nil
	out["semantics_evidence"] = []string{}
	if v, ok := m["frozen_cash"]; ok && string(v) != "null" {
		s, err := decimal(v)
		if err != nil {
			return nil, err
		}
		out["frozen_cash"] = s
	}
	out["cash_scope"] = "broker_query_projection_unverified"
	out["currency"] = nil
	if v, ok := m["currency"]; ok {
		if text(v) == "" {
			return nil, errors.New("invalid currency")
		}
		out["currency"] = text(v)
	}
	return json.Marshal(out)
}

func projectPositions(e redisstream.EntryEnvelope, items []map[string]json.RawMessage) ([]ledger.CurrentBaselineRecord, error) {
	decoded, err := redisstream.DecodePositionQueryRecords(e)
	if err != nil {
		return nil, err
	}
	if len(decoded) != len(items) {
		return nil, errors.New("position row loss")
	}
	var rows []ledger.CurrentBaselineRecord
	for i, p := range decoded {
		m := items[i]
		qty, err := integer(m["quantity"])
		if err != nil || qty != p.Quantity {
			return nil, errors.New("missing position quantity")
		}
		sellable, err := optionalInteger(m["sellable_qty"])
		if err != nil {
			return nil, err
		}
		if n, ok := sellable.(int64); ok && n > qty {
			return nil, errors.New("sellable exceeds quantity")
		}
		frozen, err := optionalInteger(m["frozen_qty"])
		if err != nil {
			return nil, err
		}
		if n, ok := frozen.(int64); ok && n > qty {
			return nil, errors.New("frozen exceeds quantity")
		}
		out := sourceFields(e)
		out["symbol"] = p.Symbol
		out["exchange"] = p.Exchange
		out["quantity"] = qty
		out["sellable_qty"] = sellable
		out["frozen_qty"] = frozen
		out["sellable_includes_frozen_deduction"] = nil
		out["shareholder_id"] = nil
		if v := text(m["shareholder_id"]); v != "" {
			out["shareholder_id"] = v
		}
		rows = append(rows, record(rowKey("position", p.AccountID, p.Symbol, string(p.Exchange)), out))
	}
	return rows, nil
}

func projectOrders(e redisstream.EntryEnvelope, items []map[string]json.RawMessage) ([]ledger.CurrentBaselineRecord, error) {
	decoded, _, _, err := redisstream.DecodeTradeQueryRecords(e)
	if err != nil {
		return nil, err
	}
	if len(decoded) != len(items) {
		return nil, errors.New("order row loss")
	}
	var rows []ledger.CurrentBaselineRecord
	for i, o := range decoded {
		m := items[i]
		date, err := sourceDate(m["trade_date"])
		if err != nil {
			return nil, err
		}
		qty, e1 := integer(m["order_qty"])
		cum, e2 := integer(m["cum_filled_qty"])
		leaves, e3 := integer(m["leaves_qty"])
		if e1 != nil || e2 != nil || e3 != nil || qty == 0 || cum > qty || leaves > qty-cum || qty != o.OrderQty || cum != o.CumFilledQty || leaves != o.LeavesQty {
			return nil, errors.New("missing or inferred order quantities")
		}
		price, err := decimal(m["limit_price"])
		if err != nil {
			return nil, err
		}
		var terminal bool
		if json.Unmarshal(m["is_terminal"], &terminal) != nil || string(m["is_terminal"]) == "null" || terminal != o.IsTerminal || text(m["status"]) != string(o.Status) {
			return nil, errors.New("missing or inferred order state")
		}
		if text(m["gateway_order_id"]) == "" || text(m["trade_side"]) == "" || text(m["business_type"]) == "" {
			return nil, errors.New("missing order intent")
		}
		out := sourceFields(e)
		out["gateway_order_id"] = o.GatewayOrderID
		out["order_stream_id"] = o.OrderStreamID
		out["trade_date"] = date
		out["symbol"] = o.Symbol
		out["exchange"] = o.Exchange
		out["trade_side"] = o.TradeSide
		out["business_type"] = o.BusinessType
		out["order_qty"] = qty
		out["cum_filled_qty"] = cum
		out["leaves_qty"] = leaves
		out["limit_price"] = price
		out["status"] = o.Status
		out["is_terminal"] = terminal
		out["origin_message_id"] = nil
		// Envelope origin identifies this query, not the original order command.
		if v := text(m["origin_message_id"]); v != "" {
			out["origin_message_id"] = v
		}
		rows = append(rows, record(rowKey("order", o.AccountID, date, o.GatewayOrderID), out))
	}
	return rows, nil
}

func projectFills(e redisstream.EntryEnvelope, items []map[string]json.RawMessage) ([]ledger.CurrentBaselineRecord, error) {
	_, decoded, transfers, err := redisstream.DecodeTradeQueryRecords(e)
	if err != nil {
		return nil, err
	}
	if len(decoded) != len(items) || len(transfers) > 0 {
		return nil, errors.New("fill row loss or transfers")
	}
	var rows []ledger.CurrentBaselineRecord
	for i, f := range decoded {
		m := items[i]
		date, err := sourceDate(m["trade_date"])
		if err != nil {
			return nil, err
		}
		qty, err := integer(m["qty"])
		if err != nil || qty == 0 || qty != f.Qty {
			return nil, errors.New("missing fill quantity")
		}
		price, err := decimal(m["price"])
		if err != nil || price == "0" || strings.HasPrefix(price, "-") {
			return nil, errors.New("invalid ordinary fill price")
		}
		if text(m["fill_id"]) == "" || text(m["gateway_order_id"]) == "" || text(m["trade_side"]) == "" {
			return nil, errors.New("missing fill identity")
		}
		out := sourceFields(e)
		out["fill_id"] = f.FillID
		out["gateway_order_id"] = f.GatewayOrderID
		out["trade_date"] = date
		out["symbol"] = f.Symbol
		out["exchange"] = f.Exchange
		out["trade_side"] = f.TradeSide
		out["qty"] = qty
		out["price"] = price
		out["matched_at"] = nil
		out["fee"] = nil
		if v := text(m["matched_at"]); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return nil, err
			}
			out["matched_at"] = t
		}
		if v, ok := m["fee"]; ok && string(v) != "null" {
			fee, err := decimal(v)
			if err != nil {
				return nil, err
			}
			out["fee"] = fee
		}
		rows = append(rows, record(rowKey("fill", f.AccountID, date, f.GatewayOrderID, f.FillID), out))
	}
	return rows, nil
}
