package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const researchBookMaxRows = 100000

type researchBookRow struct {
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
}

type researchBookDifference struct {
	Key          string          `json:"key"`
	Kind         string          `json:"kind"`
	Query        map[string]any  `json:"query,omitempty"`
	Book         map[string]any  `json:"book,omitempty"`
	Fields       []string        `json:"fields,omitempty"`
	BookEvidence json.RawMessage `json:"stored_book_evidence,omitempty"`
}

type researchBookComparison struct {
	State       string                   `json:"state"`
	QueryRows   int                      `json:"query_rows"`
	BookRows    int                      `json:"book_rows"`
	Matched     int                      `json:"matched"`
	QuerySHA    string                   `json:"query_projection_sha256"`
	BookSHA     string                   `json:"book_projection_sha256"`
	Differences []researchBookDifference `json:"differences"`
}

type researchBookCoverage struct {
	RequestState string                  `json:"request_state"`
	Requests     []json.RawMessage       `json:"archived_requests"`
	RequestSHA   string                  `json:"request_sha256"`
	Flags        []string                `json:"flags"`
	DecodeIssues []researchDecodeIssue   `json:"decode_issues,omitempty"`
	Rows         researchBookComparison  `json:"rows"`
	Transfers    *researchBookComparison `json:"component_transfers,omitempty"`
	Scope        string                  `json:"scope"`
}

type researchDecodeIssue struct {
	MessageID  string          `json:"message_id"`
	Collection string          `json:"collection"`
	Index      int             `json:"index"`
	Error      string          `json:"error"`
	Raw        json.RawMessage `json:"raw"`
}

type researchDailyBooks struct {
	Orders, Fills, Transfers []researchBookRow
}

func researchBookKey(account, date, gateway, id string) string {
	if len(date) == 8 {
		if d, err := time.Parse("20060102", date); err == nil {
			date = d.Format("2006-01-02")
		}
	}
	key, _ := json.Marshal([]string{account, date, gateway, id})
	return string(key)
}

func researchOrderRow(o trading.Order) researchBookRow {
	return researchBookRow{researchBookKey(o.AccountID, o.TradeDate, o.GatewayOrderID, ""), map[string]any{
		"symbol": o.Symbol, "exchange": o.Exchange, "trade_side": o.TradeSide, "business_type": o.BusinessType,
		"order_id": o.OrderID, "order_stream_id": o.OrderStreamID, "order_qty": o.OrderQty,
		"limit_price": fmt.Sprintf("%.6f", o.LimitPrice), "cum_filled_qty": o.CumFilledQty,
		"leaves_qty": o.LeavesQty, "cancelled_qty": o.CancelledQty, "invalid_qty": o.InvalidQty,
		"avg_fill_price": fmt.Sprintf("%.6f", o.AvgFillPrice), "status": o.Status, "is_terminal": o.IsTerminal,
	}}
}

func researchFillRow(f trading.Fill) researchBookRow {
	return researchBookRow{researchBookKey(f.AccountID, f.TradeDate, f.GatewayOrderID, f.FillID), map[string]any{
		"symbol": f.Symbol, "exchange": f.Exchange, "trade_side": f.TradeSide, "business_type": f.BusinessType,
		"order_id": f.OrderID, "order_stream_id": f.OrderStreamID, "qty": f.Qty,
		"price": fmt.Sprintf("%.6f", f.Price), "matched_at": f.MatchedAt.In(timeutil.Location()).Truncate(time.Microsecond).Format(time.RFC3339Nano),
	}}
}

func researchTransferRow(t trading.ComponentTransfer) researchBookRow {
	r := researchFillRow(trading.Fill{AccountID: t.AccountID, TradeDate: t.TradeDate, GatewayOrderID: t.GatewayOrderID, FillID: t.FillID,
		Symbol: t.Symbol, Exchange: t.Exchange, TradeSide: t.TradeSide, BusinessType: t.BusinessType, OrderID: t.OrderID,
		OrderStreamID: t.OrderStreamID, Qty: t.Qty, Price: t.Price, MatchedAt: t.MatchedAt})
	r.Fields["component_symbol"], r.Fields["component_exchange"], r.Fields["component_qty"] = t.ComponentSymbol, t.ComponentExchange, t.ComponentQty
	r.Fields["transfer_type"], r.Fields["cash_substitution"] = t.TransferType, t.CashSubstitution
	r.Fields["component_value"] = nil
	if t.ComponentValue != nil {
		r.Fields["component_value"] = fmt.Sprintf("%.6f", *t.ComponentValue)
	}
	return r
}

func researchRowsSHA(rows []researchBookRow) string {
	encoded := make([]string, len(rows))
	for i, r := range rows {
		b, _ := json.Marshal(r)
		encoded[i] = string(b)
	}
	sort.Strings(encoded)
	b, _ := json.Marshal(encoded)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// Compare multisets, never deduplicate evidence silently. Book-only rows can be
// later callbacks or repairs; report them, do not call them invalid or delete them.
func compareResearchBooks(query, book []researchBookRow) researchBookComparison {
	r := researchBookComparison{State: "exact_projection_match", QueryRows: len(query), BookRows: len(book), QuerySHA: researchRowsSHA(query), BookSHA: researchRowsSHA(book), Differences: []researchBookDifference{}}
	q, b := map[string][]researchBookRow{}, map[string][]researchBookRow{}
	keys := map[string]bool{}
	for _, x := range query {
		q[x.Key] = append(q[x.Key], x)
		keys[x.Key] = true
	}
	for _, x := range book {
		b[x.Key] = append(b[x.Key], x)
		keys[x.Key] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, k := range ordered {
		x, y := q[k], b[k]
		d := researchBookDifference{Key: k}
		switch {
		case len(x) > 1 || len(y) > 1:
			d.Kind = fmt.Sprintf("duplicate_identity_query_%d_book_%d", len(x), len(y))
		case len(y) == 0:
			d.Kind, d.Query = "missing_in_book", x[0].Fields
		case len(x) == 0:
			d.Kind, d.Book = "book_only_not_in_this_query", y[0].Fields
		default:
			for field, v := range x[0].Fields {
				a, _ := json.Marshal(v)
				z, _ := json.Marshal(y[0].Fields[field])
				if string(a) != string(z) {
					d.Fields = append(d.Fields, field)
				}
			}
			if len(d.Fields) == 0 {
				r.Matched++
				continue
			}
			sort.Strings(d.Fields)
			d.Kind, d.Query, d.Book = "current_book_fields_differ", x[0].Fields, y[0].Fields
		}
		r.Differences = append(r.Differences, d)
	}
	if len(r.Differences) != 0 {
		r.State = "differences_require_review"
	}
	return r
}

func readResearchDailyBooks(ctx context.Context, tx *sql.Tx, account, date string) (researchDailyBooks, error) {
	var out researchDailyBooks
	repo := ledger.NewRepository(tx)
	for offset := 0; ; offset += 500 {
		rows, err := repo.ListOrders(ctx, trading.OrderQuery{AccountID: account, TradeDate: date, Limit: 500, Cursor: strconv.Itoa(offset)})
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			out.Orders = append(out.Orders, researchOrderRow(row))
		}
		if len(out.Orders) > researchBookMaxRows {
			return out, errors.New("order coverage row budget exceeded")
		}
		if len(rows) < 500 {
			break
		}
	}
	for offset := 0; ; offset += 500 {
		rows, err := repo.ListFills(ctx, trading.FillQuery{AccountID: account, TradeDate: date, Limit: 500, Cursor: strconv.Itoa(offset)})
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			if row.FillID == "" {
				return out, errors.New("book fill without fill_id needs explicit fallback-identity review")
			}
			out.Fills = append(out.Fills, researchFillRow(row))
		}
		if len(out.Fills) > researchBookMaxRows {
			return out, errors.New("fill coverage row budget exceeded")
		}
		if len(rows) < 500 {
			break
		}
	}
	for offset := 0; ; offset += 500 {
		rows, err := repo.ListComponentTransfers(ctx, trading.ComponentTransferQuery{AccountID: account, TradeDate: date, Limit: 500, Cursor: strconv.Itoa(offset)})
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			if row.FillID == "" {
				return out, errors.New("book transfer without fill_id needs explicit fallback-identity review")
			}
			out.Transfers = append(out.Transfers, researchTransferRow(row))
		}
		if len(out.Transfers) > researchBookMaxRows {
			return out, errors.New("transfer coverage row budget exceeded")
		}
		if len(rows) < 500 {
			break
		}
	}
	return out, nil
}

func reviewResearchRequest(raw []researchArchivedReply, route config.AccountRouteConfig, env, date, action, message string) (string, []json.RawMessage, string) {
	state := "request_scope_unproven"
	var requests []json.RawMessage
	for _, r := range raw {
		requests = append(requests, json.RawMessage(r.Body))
	}
	b, err := json.Marshal(requests)
	if err != nil {
		return state, nil, ""
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(b))
	if len(raw) != 1 || raw[0].ParseError != "" {
		return state, requests, hash
	}
	e, err := redisstream.DecodeEntry(raw[0].Stream, "", map[string]any{"body": raw[0].Body})
	var command struct {
		SentAt time.Time `json:"sent_at"`
	}
	if json.Unmarshal(raw[0].Body, &command) != nil {
		return state, requests, hash
	}
	if env == "production" {
		env = "prod"
	}
	prefix := route.StreamPrefix
	if prefix == "" {
		prefix = fmt.Sprintf("relay:%s:v1:%s:%s", env, route.BrokerID, route.GatewayID)
	}
	if err != nil || raw[0].Stream != prefix+":cmd.query" || e.Protocol != "relay.stream.v1" || e.MessageType != "command" || e.Action != action || e.MessageID != message || message == "" || command.SentAt.IsZero() || command.SentAt.In(timeutil.Location()).Format("2006-01-02") != date || raw[0].ReceivedAt.In(timeutil.Location()).Format("2006-01-02") != date || e.Routing.Env != env || e.Routing.BrokerID != route.BrokerID || e.Routing.GatewayID != route.GatewayID || (e.Routing.AccountID != "" && e.Routing.AccountID != route.AccountID) {
		return state, requests, hash
	}
	var payload map[string]string
	if json.Unmarshal(e.Payload, &payload) != nil || payload["account_id"] != route.AccountID {
		return state, requests, hash
	}
	for key, value := range payload {
		if key == "account_id" {
			continue
		}
		if key != "trade_date" || (value != "" && value != date && value != strings.ReplaceAll(date, "-", "")) {
			return state, requests, hash
		}
	}
	return "account_day_unfiltered_request", requests, hash
}

// Individual-row fallback is diagnostic only. A failed original page remains
// failed, even when some rows can independently be compared with the ledger.
func inspectResearchTradePage(e redisstream.EntryEnvelope) ([]trading.Order, []trading.Fill, []trading.ComponentTransfer, []researchDecodeIssue, error) {
	orders, fills, transfers, pageErr := redisstream.DecodeTradeQueryRecords(e)
	if pageErr == nil {
		return orders, fills, transfers, nil, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		return nil, nil, nil, nil, pageErr
	}
	var issues []researchDecodeIssue
	for _, collection := range []string{"items", "component_transfers"} {
		var items []json.RawMessage
		if err := json.Unmarshal(payload[collection], &items); err != nil {
			continue
		}
		for index, item := range items {
			copy := e
			copy.Payload, _ = json.Marshal(map[string]any{"items": []json.RawMessage{}, "component_transfers": []json.RawMessage{}, collection: []json.RawMessage{item}})
			o, f, t, err := redisstream.DecodeTradeQueryRecords(copy)
			if err != nil {
				issues = append(issues, researchDecodeIssue{e.MessageID, collection, index, err.Error(), item})
				continue
			}
			orders, fills, transfers = append(orders, o...), append(fills, f...), append(transfers, t...)
		}
	}
	return orders, fills, transfers, issues, pageErr
}

func readResearchBookCoverage(ctx context.Context, tx *sql.Tx, env, date, action string, route config.AccountRouteConfig, proof researchQueryProof, replies []researchArchivedReply, books researchDailyBooks) (*researchBookCoverage, error) {
	c := &researchBookCoverage{Flags: []string{}, Scope: "current ledger versus this historical query; core fields only, fees/strategy enrichment excluded; no broker-wide completeness or cost/NAV publication assertion"}
	requests, err := readResearchArchiveRole(ctx, tx, route.AccountID, proof.MessageID, "cmd.query")
	if err != nil {
		return nil, err
	}
	c.RequestState, c.Requests, c.RequestSHA = reviewResearchRequest(requests, route, env, date, action, proof.MessageID)
	if c.RequestState != "account_day_unfiltered_request" {
		c.Flags = append(c.Flags, "request_scope_unproven")
	}
	var rows, transfers []researchBookRow
	if proof.State != "terminal_archive_consistent" {
		c.Flags = append(c.Flags, "query_archive_unproven")
	} else {
		for _, raw := range replies {
			e, err := redisstream.DecodeEntry(raw.Stream, "", map[string]any{"body": raw.Body})
			if err != nil {
				return nil, err
			}
			orders, fills, ts, issues, err := inspectResearchTradePage(e)
			c.DecodeIssues = append(c.DecodeIssues, issues...)
			if err != nil {
				c.Flags = append(c.Flags, "normalization_failed: "+e.MessageID+": "+err.Error())
			}
			for _, o := range orders {
				rows = append(rows, researchOrderRow(o))
			}
			for _, f := range fills {
				if f.FillID == "" {
					c.Flags = append(c.Flags, "fill_identity_unproven")
				}
				rows = append(rows, researchFillRow(f))
			}
			for _, t := range ts {
				if t.FillID == "" {
					c.Flags = append(c.Flags, "transfer_identity_unproven")
				}
				transfers = append(transfers, researchTransferRow(t))
			}
			if len(rows)+len(transfers)+len(c.DecodeIssues) > researchBookMaxRows {
				return nil, errors.New("query record budget exceeded")
			}
		}
	}
	for _, row := range append(append([]researchBookRow{}, rows...), transfers...) {
		var key []string
		if json.Unmarshal([]byte(row.Key), &key) != nil || len(key) != 4 || key[0] != route.AccountID || key[1] != date || key[2] == "" {
			c.Flags = append(c.Flags, "record_scope_unproven")
		}
	}
	if action == redisstream.ActionOrderList {
		c.Rows = compareResearchBooks(rows, books.Orders)
		if err := attachResearchBookEvidence(ctx, tx, route.AccountID, date, "orders", &c.Rows); err != nil {
			return nil, err
		}
	} else {
		c.Rows = compareResearchBooks(rows, books.Fills)
		if err := attachResearchBookEvidence(ctx, tx, route.AccountID, date, "fills", &c.Rows); err != nil {
			return nil, err
		}
		t := compareResearchBooks(transfers, books.Transfers)
		if err := attachResearchBookEvidence(ctx, tx, route.AccountID, date, "etf_component_transfers", &t); err != nil {
			return nil, err
		}
		c.Transfers = &t
	}
	if len(c.Flags) != 0 {
		c.Rows.State = "not_proven"
		if c.Transfers != nil {
			c.Transfers.State = "not_proven"
		}
	}
	return c, nil
}

// Stored provenance helps distinguish later events and synthetic shells from
// missing query results. It is not independent validation of those events.
func attachResearchBookEvidence(ctx context.Context, tx *sql.Tx, account, date, table string, comparison *researchBookComparison) error {
	if table != "orders" && table != "fills" && table != "etf_component_transfers" {
		return errors.New("unsupported evidence table")
	}
	var gateways []string
	indices := map[string]int{}
	for i, d := range comparison.Differences {
		if d.Kind != "book_only_not_in_this_query" && d.Kind != "current_book_fields_differ" {
			continue
		}
		var key []string
		if json.Unmarshal([]byte(d.Key), &key) != nil || len(key) != 4 || key[0] != account || key[1] != date {
			return errors.New("difference scope invalid")
		}
		gateways = append(gateways, key[2])
		indices[d.Key] = i
	}
	if len(gateways) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT gateway_order_id,COALESCE(to_jsonb(b)->>'fill_id',''),jsonb_build_object('created_at',to_jsonb(b)->'created_at','updated_at',to_jsonb(b)->'updated_at','last_updated_at',to_jsonb(b)->'last_updated_at','stream_key',to_jsonb(b)->'stream_key','stream_id',to_jsonb(b)->'stream_id','origin_message_id',to_jsonb(b)->'origin_message_id','raw_payload',to_jsonb(b)->'raw_payload','adapter_context',to_jsonb(b)->'adapter_context') FROM `+table+` b WHERE account_id=$1 AND trade_date=$2::date AND gateway_order_id=ANY($3) LIMIT 100001`, account, date, gateways)
	if err != nil {
		return err
	}
	defer rows.Close()
	bytes, count := 0, 0
	for rows.Next() {
		var gateway, fill string
		var raw []byte
		if err := rows.Scan(&gateway, &fill, &raw); err != nil {
			return err
		}
		bytes += len(raw)
		count++
		if bytes > 64<<20 || count > researchBookMaxRows {
			return errors.New("book evidence budget exceeded")
		}
		if i, ok := indices[researchBookKey(account, date, gateway, fill)]; ok {
			comparison.Differences[i].BookEvidence = append(json.RawMessage(nil), raw...)
		}
	}
	return rows.Err()
}
