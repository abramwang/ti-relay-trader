package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type researchEventProof struct {
	State      string                   `json:"state"`
	Candidates int                      `json:"candidates"`
	Matches    []researchEventReference `json:"matching_events"`
	Rejected   []researchEventReference `json:"unmatched_or_invalid_events,omitempty"`
}

type researchEventReference struct {
	Stream     string    `json:"stream"`
	StreamID   string    `json:"stream_id"`
	MessageID  string    `json:"message_id"`
	EventType  string    `json:"event_type"`
	ReceivedAt time.Time `json:"received_at"`
	ProducedAt time.Time `json:"produced_at"`
	SHA        string    `json:"body_sha256"`
	Reason     string    `json:"reason,omitempty"`
}

type researchArchivedEvent struct {
	researchArchivedReply
	StreamID, GatewayOrderID string
}

func decodeResearchEvent(raw researchArchivedEvent, env, date string, route config.AccountRouteConfig) (researchBookRow, *trading.Fill, researchEventReference, error) {
	ref := researchEventReference{Stream: raw.Stream, StreamID: raw.StreamID, ReceivedAt: raw.ReceivedAt.In(timeutil.Location()), SHA: fmt.Sprintf("%x", sha256.Sum256(raw.Body))}
	var wire struct {
		Protocol   string              `json:"protocol"`
		Type       string              `json:"message_type"`
		MessageID  string              `json:"message_id"`
		ProducedAt time.Time           `json:"produced_at"`
		Routing    redisstream.Routing `json:"routing"`
		Payload    struct {
			MatchedAt      string `json:"matched_at"`
			MatchTimestamp int64  `json:"match_timestamp"`
		} `json:"payload"`
	}
	if env == "production" {
		env = "prod"
	}
	prefix := route.StreamPrefix
	if prefix == "" {
		prefix = fmt.Sprintf("relay:%s:v1:%s:%s", env, route.BrokerID, route.GatewayID)
	}
	if raw.ParseError != "" || json.Unmarshal(raw.Body, &wire) != nil {
		return researchBookRow{}, nil, ref, errors.New("archive_parse_error")
	}
	ref.MessageID, ref.ProducedAt = wire.MessageID, wire.ProducedAt.In(timeutil.Location())
	if raw.Stream != prefix+":event" || raw.StreamID == "" || wire.MessageID == "" || wire.Protocol != "relay.stream.v1" || wire.Type != "event" || wire.Routing.Env != env || wire.Routing.BrokerID != route.BrokerID || wire.Routing.GatewayID != route.GatewayID || wire.Routing.AccountID != route.AccountID || wire.ProducedAt.IsZero() || wire.ProducedAt.In(timeutil.Location()).Format("2006-01-02") != date || raw.ReceivedAt.In(timeutil.Location()).Format("2006-01-02") != date {
		return researchBookRow{}, nil, ref, errors.New("archive_scope_or_time_mismatch")
	}
	e, err := redisstream.DecodeEntry(raw.Stream, raw.StreamID, map[string]any{"body": raw.Body})
	if err != nil {
		return researchBookRow{}, nil, ref, err
	}
	ref.EventType = e.EventType
	if e.EventType == "fill.event" {
		matched, err := time.Parse(time.RFC3339Nano, wire.Payload.MatchedAt)
		if (err != nil || matched.IsZero()) && wire.Payload.MatchTimestamp <= 0 {
			return researchBookRow{}, nil, ref, errors.New("original_match_time_missing")
		}
	}
	o, f, err := redisstream.DecodeTradeEventRecord(e)
	if err != nil {
		return researchBookRow{}, nil, ref, err
	}
	var row researchBookRow
	if o != nil {
		row = researchOrderRow(*o)
	} else if f != nil {
		row = researchFillRow(*f)
	} else {
		return row, nil, ref, errors.New("empty event")
	}
	var key []string
	if json.Unmarshal([]byte(row.Key), &key) != nil || len(key) != 4 || key[0] != route.AccountID || key[1] != date || key[2] != raw.GatewayOrderID || key[2] == "" {
		return row, nil, ref, errors.New("event_business_identity_mismatch")
	}
	return row, f, ref, nil
}

func reviewResearchEvents(difference researchBookDifference, kind string, events []researchArchivedEvent, env, date string, route config.AccountRouteConfig) (*researchEventProof, *trading.Fill) {
	p := &researchEventProof{State: "matching_event_not_found", Matches: []researchEventReference{}}
	var matchedFill *trading.Fill
	seen := map[string]bool{}
	var key []string
	if json.Unmarshal([]byte(difference.Key), &key) != nil || len(key) != 4 || key[0] != route.AccountID || key[1] != date || key[2] == "" || (kind != "orders" && kind != "fills") {
		p.State = "invalid_book_identity"
		return p, nil
	}
	for _, raw := range events {
		if raw.GatewayOrderID != key[2] {
			continue
		}
		row, fill, ref, err := decodeResearchEvent(raw, env, date, route)
		if ref.EventType != "" && ((kind == "orders" && ref.EventType != "order.event") || (kind == "fills" && ref.EventType != "fill.event")) {
			continue
		}
		if err == nil && row.Key != difference.Key {
			continue
		}
		p.Candidates++
		if err != nil {
			ref.Reason = err.Error()
			p.Rejected = append(p.Rejected, ref)
			continue
		}
		if seen[ref.MessageID] {
			ref.Reason = "duplicate_event_message_id"
			p.Rejected = append(p.Rejected, ref)
			continue
		}
		seen[ref.MessageID] = true
		check := compareResearchBooks([]researchBookRow{row}, []researchBookRow{{Key: difference.Key, Fields: difference.Book}})
		if check.Matched != 1 {
			ref.Reason = "event_core_projection_differs"
			p.Rejected = append(p.Rejected, ref)
			continue
		}
		p.Matches = append(p.Matches, ref)
		if fill != nil {
			matchedFill = fill
		}
	}
	if len(p.Matches) > 0 {
		p.State = "matching_full_event_found"
		for _, ref := range p.Rejected {
			if ref.Reason != "event_core_projection_differs" {
				p.State = "matching_event_with_archive_warnings"
				break
			}
		}
	}
	return p, matchedFill
}

func attachResearchEventEvidence(ctx context.Context, tx *sql.Tx, env string, route config.AccountRouteConfig, d *researchQueryDay) error {
	targets := map[string]bool{}
	for _, a := range d.Attempts {
		for _, q := range a.Queries {
			if q.BookCoverage != nil {
				for _, diff := range q.BookCoverage.Rows.Differences {
					if diff.Kind == "book_only_not_in_this_query" {
						var key []string
						if json.Unmarshal([]byte(diff.Key), &key) != nil || len(key) != 4 || key[0] != route.AccountID || key[1] != d.Date {
							return errors.New("event evidence target scope invalid")
						}
						targets[key[2]] = true
					}
				}
			}
		}
	}
	if len(targets) == 0 {
		return nil
	}
	gateways := make([]string, 0, len(targets))
	for k := range targets {
		gateways = append(gateways, k)
	}
	sort.Strings(gateways)
	start, err := time.ParseInLocation("2006-01-02", d.Date, timeutil.Location())
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT body,stream_key,stream_id,gateway_order_id,COALESCE(parse_error,''),received_at FROM raw_stream_messages WHERE account_id=$1 AND stream_role='event' AND gateway_order_id=ANY($2) AND received_at >= $3 AND received_at < $4 ORDER BY received_at,raw_message_pk LIMIT 100001`, route.AccountID, gateways, start, start.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	var events []researchArchivedEvent
	bytes := 0
	for rows.Next() {
		var raw researchArchivedEvent
		if err := rows.Scan(&raw.Body, &raw.Stream, &raw.StreamID, &raw.GatewayOrderID, &raw.ParseError, &raw.ReceivedAt); err != nil {
			rows.Close()
			return err
		}
		events = append(events, raw)
		bytes += len(raw.Body)
		if len(events) > researchBookMaxRows || bytes > 64<<20 {
			rows.Close()
			return errors.New("event evidence budget exceeded")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	matchedFills := map[string]trading.Fill{}
	for ai := range d.Attempts {
		for qi := range d.Attempts[ai].Queries {
			q := &d.Attempts[ai].Queries[qi]
			if q.BookCoverage == nil {
				continue
			}
			for i := range q.BookCoverage.Rows.Differences {
				diff := &q.BookCoverage.Rows.Differences[i]
				if diff.Kind != "book_only_not_in_this_query" {
					continue
				}
				proof, fill := reviewResearchEvents(*diff, q.Step, events, env, d.Date, route)
				diff.EventEvidence = proof
				if fill != nil && proof.State == "matching_full_event_found" {
					matchedFills[diff.Key] = *fill
				}
			}
		}
	}
	var fills []trading.Fill
	keys := make([]string, 0, len(matchedFills))
	for key := range matchedFills {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fills = append(fills, matchedFills[key])
	}
	if len(fills) > 0 {
		d.RepoTiming = reviewResearchRepoCashTiming(*d, route.AccountID, fills)
	}
	return nil
}

// Only independently matched, query-external fills are reviewed here. This is
// not a reconstruction of all-day cash, settlement or repo receivables.
func reviewResearchRepoCashTiming(d researchQueryDay, account string, fills []trading.Fill) map[string]any {
	type snapshot struct {
		Kind     string         `json:"snapshot_type"`
		Captured time.Time      `json:"captured_at"`
		Asset    *trading.Asset `json:"asset"`
		Repo     struct {
			Principal *float64 `json:"principal_receivable"`
		} `json:"reverse_repo"`
	}
	r := map[string]any{"state": "cash_archive_link_unproven", "scope": "only query-external fills with independently matching events; no final cash reconstruction or NAV approval"}
	var broker, close snapshot
	brokerCount, closeCount := 0, 0
	for _, raw := range d.SnapshotTimes {
		var s snapshot
		if json.Unmarshal(raw, &s) != nil {
			return r
		}
		if s.Kind == "broker_close" {
			broker = s
			brokerCount++
		}
		if s.Kind == "close" {
			close = s
			closeCount++
		}
	}
	if brokerCount != 1 || closeCount != 1 || broker.Asset == nil || close.Asset == nil || broker.Asset.AccountID != account || close.Asset.AccountID != account || broker.Asset.UpdatedAt.IsZero() || close.Repo.Principal == nil || broker.Captured.Before(broker.Asset.UpdatedAt) || broker.Captured.In(timeutil.Location()).Format("2006-01-02") != d.Date || broker.Asset.UpdatedAt.In(timeutil.Location()).Format("2006-01-02") != d.Date {
		return r
	}
	seenFills := map[string]bool{}
	for _, f := range fills {
		var key []string
		row := researchFillRow(f)
		if json.Unmarshal([]byte(row.Key), &key) != nil || len(key) != 4 || key[0] != account || key[1] != d.Date || key[2] == "" || key[3] == "" || seenFills[row.Key] || f.MatchedAt.IsZero() || f.MatchedAt.In(timeutil.Location()).Format("2006-01-02") != d.Date {
			r["state"] = "reviewed_fill_scope_unproven"
			return r
		}
		seenFills[row.Key] = true
	}
	linked := 0
	for _, a := range d.Attempts {
		if !a.LinkedToClose {
			continue
		}
		for _, q := range a.Queries {
			if q.Step != "asset" || q.State != "terminal_archive_consistent" {
				continue
			}
			var e struct {
				Produced time.Time           `json:"produced_at"`
				Routing  redisstream.Routing `json:"routing"`
				Payload  struct {
					Account *trading.Asset `json:"account"`
				} `json:"payload"`
			}
			if json.Unmarshal(q.AssetReply, &e) != nil || e.Payload.Account == nil {
				continue
			}
			x := e.Payload.Account
			y := broker.Asset
			if e.Routing.AccountID == account && e.Produced.Equal(y.UpdatedAt) && fmt.Sprintf("%.6f", x.CashTotal) == fmt.Sprintf("%.6f", y.CashTotal) && fmt.Sprintf("%.6f", x.CashAvailable) == fmt.Sprintf("%.6f", y.CashAvailable) && fmt.Sprintf("%.6f", x.Commission) == fmt.Sprintf("%.6f", y.Commission) && y.CashTotal == close.Asset.CashTotal {
				linked++
			}
		}
	}
	if linked != 1 {
		return r
	}
	total := performance.ReverseRepoPrincipalFromFills(fills)
	if total <= 0 {
		r["state"] = "no_reviewed_reverse_repo"
		return r
	}
	asof := performance.ReverseRepoPrincipalFromFillsAsOf(fills, broker.Asset.UpdatedAt)
	batch := performance.ReverseRepoPrincipalFromFillsAsOf(fills, broker.Captured)
	r["state"] = "reviewed_repo_cash_timing_proven"
	r["cash_observed_at"], r["broker_batch_at"] = broker.Asset.UpdatedAt.In(timeutil.Location()), broker.Captured.In(timeutil.Location())
	r["cash_total"], r["recorded_close_principal"] = broker.Asset.CashTotal, *close.Repo.Principal
	r["reviewed_principal"], r["reviewed_principal_as_of_cash"], r["reviewed_principal_after_cash"] = total, asof, total-asof
	r["reviewed_principal_as_of_batch"], r["batch_clock_would_include_later_principal"] = batch, batch > asof
	r["fills"] = fills
	return r
}
