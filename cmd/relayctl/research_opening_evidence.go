package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type researchOpeningLot struct {
	Security string `json:"security_id"`
	Bucket   string `json:"cost_bucket"`
	Quantity int64  `json:"quantity"`
	Cost     string `json:"research_open_total_cost"`
}

type researchOpeningReview struct {
	Date               string                       `json:"trade_date"`
	PreviousDate       string                       `json:"previous_trade_date"`
	State              string                       `json:"state"`
	Flags              []string                     `json:"flags"`
	Baseline           ledger.PositionBaseline      `json:"previous_close_baseline"`
	PreviousPositions  []ledger.PositionBaselineRow `json:"previous_close_positions"`
	Query              *researchQueryProof          `json:"pre_open_query,omitempty"`
	Requests           []json.RawMessage            `json:"archived_requests"`
	RequestSHA         string                       `json:"request_sha256,omitempty"`
	Replies            []json.RawMessage            `json:"archived_replies"`
	Positions          []trading.Position           `json:"pre_open_positions"`
	Lots               []researchOpeningLot         `json:"research_open_lots"`
	PreviousComparison *researchBookComparison      `json:"previous_close_vs_pre_open,omitempty"`
	CostComparison     *researchBookComparison      `json:"research_open_vs_pre_open,omitempty"`
}

func researchOpeningQuantities(account, date string, positions []trading.Position) ([]researchBookRow, error) {
	seen := map[string]bool{}
	var rows []researchBookRow
	for _, p := range positions {
		security := p.Symbol + "." + string(p.Exchange)
		if p.AccountID != account || p.Symbol == "" || (p.Exchange != trading.ExchangeSH && p.Exchange != trading.ExchangeSZ) || p.Quantity < 0 || p.SellableQty < 0 || p.SellableQty > p.Quantity || seen[security] {
			return nil, errors.New("position scope, quantity or identity invalid")
		}
		seen[security] = true
		// Full-set proof is checked by the caller; explicit zero and absence then
		// have the same economic quantity. Sellability is deliberately not compared.
		if p.Quantity > 0 {
			rows = append(rows, researchBookRow{Key: researchBookKey(account, date, security, ""), Fields: map[string]any{"quantity": p.Quantity}})
		}
	}
	return rows, nil
}

func researchOpeningCostRows(account, date string, costs []ledger.PositionCostState) ([]researchBookRow, []researchOpeningLot, error) {
	quantities := map[string]int64{}
	seen := map[string]bool{}
	var lots []researchOpeningLot
	for _, p := range costs {
		security := p.Symbol + "." + p.Exchange
		key := security + "|" + p.CostBucket
		if p.AccountID != account || p.TradeDate != date || p.Symbol == "" || (p.Exchange != "SH" && p.Exchange != "SZ") || p.CostBucket == "" || seen[key] || p.OpenQuantity < 0 || p.OpenTotalCost < 0 || math.IsNaN(p.OpenTotalCost) || math.IsInf(p.OpenTotalCost, 0) || (p.CorporateActionType != "" && p.CorporateActionType != "none") || p.CorporateActionQuantityDelta != 0 || math.MaxInt64-quantities[security] < p.OpenQuantity {
			return nil, nil, errors.New("cost opening scope, duplicates or corporate actions require separate review")
		}
		seen[key] = true
		quantities[security] += p.OpenQuantity
		if p.OpenQuantity > 0 {
			lots = append(lots, researchOpeningLot{security, p.CostBucket, p.OpenQuantity, fmt.Sprintf("%.6f", p.OpenTotalCost)})
		}
	}
	sort.Slice(lots, func(i, j int) bool { return lots[i].Security+"|"+lots[i].Bucket < lots[j].Security+"|"+lots[j].Bucket })
	var rows []researchBookRow
	for security, qty := range quantities {
		if qty > 0 {
			rows = append(rows, researchBookRow{Key: researchBookKey(account, date, security, ""), Fields: map[string]any{"quantity": qty}})
		}
	}
	return rows, lots, nil
}

func reviewResearchOpeningPages(account, date string, proof researchQueryProof, replies []researchArchivedReply) ([]trading.Position, error) {
	if proof.State != "terminal_archive_consistent" || proof.MessageID == "" || proof.Finals != 1 || len(replies) == 0 || proof.ArchivedReplies != len(replies) {
		return nil, errors.New("pre-open archive incomplete")
	}
	cutoff, err := time.ParseInLocation("2006-01-02 15:04", date+" 09:15", timeutil.Location())
	if err != nil {
		return nil, err
	}
	var positions []trading.Position
	seen, finals := map[string]bool{}, 0
	for i, raw := range replies {
		var original struct {
			Produced time.Time `json:"produced_at"`
			Chunk    struct {
				Last bool `json:"is_last"`
			} `json:"chunk"`
			Payload struct {
				Items *[]json.RawMessage `json:"items"`
			} `json:"payload"`
		}
		if json.Unmarshal(raw.Body, &original) != nil || original.Produced.IsZero() || original.Payload.Items == nil {
			return nil, errors.New("original position page time or items missing")
		}
		for _, item := range *original.Payload.Items {
			var fields struct {
				Quantity *int64 `json:"quantity"`
				Sellable *int64 `json:"sellable_qty"`
			}
			if json.Unmarshal(item, &fields) != nil || fields.Quantity == nil || fields.Sellable == nil {
				return nil, errors.New("explicit position quantities missing")
			}
		}
		e, err := redisstream.DecodeEntry(raw.Stream, "", map[string]any{"body": raw.Body})
		if err != nil {
			return nil, err
		}
		if raw.ParseError != "" || e.ProducedAt.IsZero() || e.ProducedAt.In(timeutil.Location()).Format("2006-01-02") != date || !e.ProducedAt.Before(cutoff) || raw.ReceivedAt.In(timeutil.Location()).Format("2006-01-02") != date || !raw.ReceivedAt.Before(cutoff) {
			return nil, errors.New("position query is not before production opening auction")
		}
		if e.MessageID == "" || seen[e.MessageID] || e.OriginMessageID != proof.MessageID || e.Routing.AccountID != account {
			return nil, errors.New("position page identity mismatch")
		}
		seen[e.MessageID] = true
		if e.Status == "completed" {
			finals++
			if !original.Chunk.Last || i != len(replies)-1 {
				return nil, errors.New("position final page boundary mismatch")
			}
		} else if original.Chunk.Last {
			return nil, errors.New("nonfinal position page marked last")
		}
		ps, err := redisstream.DecodePositionQueryRecords(e)
		if err != nil {
			return nil, err
		}
		positions = append(positions, ps...)
		if len(positions) > researchBookMaxRows {
			return nil, errors.New("pre-open position budget exceeded")
		}
	}
	if finals != 1 || len(positions) != proof.Items {
		return nil, errors.New("position count differs from archive")
	}
	if _, err := researchOpeningQuantities(account, date, positions); err != nil {
		return nil, err
	}
	return positions, nil
}

func readResearchOpeningBaseline(ctx context.Context, tx *sql.Tx, env string, route config.AccountRouteConfig, date string) (ledger.PositionBaseline, []ledger.PositionBaselineRow, error) {
	q := ledger.PositionBaselineQuery{Environment: env, AccountID: route.AccountID, BrokerID: route.BrokerID, GatewayID: route.GatewayID, TradeDate: date, SnapshotType: "close", Limit: 500}
	var baseline ledger.PositionBaseline
	var positions []ledger.PositionBaselineRow
	seen := map[string]bool{}
	for {
		p, err := ledger.NewRepository(tx).GetPositionBaseline(ctx, q)
		if err != nil {
			return baseline, nil, err
		}
		if q.Cursor == "" {
			baseline = p.Baseline
		} else if p.Baseline.BaselineID != baseline.BaselineID {
			return baseline, nil, errors.New("close baseline changed during read")
		}
		positions = append(positions, p.Positions...)
		if len(positions) > researchBookMaxRows {
			return baseline, nil, errors.New("close baseline budget exceeded")
		}
		if p.NextCursor == "" {
			break
		}
		if seen[p.NextCursor] {
			return baseline, nil, errors.New("repeated baseline cursor")
		}
		seen[p.NextCursor] = true
		q.Cursor, q.BaselineID = p.NextCursor, baseline.BaselineID
	}
	if len(positions) != baseline.TotalRows {
		return baseline, nil, errors.New("baseline pagination incomplete")
	}
	return baseline, positions, nil
}

func readResearchOpeningEvidence(ctx context.Context, tx *sql.Tx, env string, route config.AccountRouteConfig, b performance.ResearchCostBundle, days []researchQueryDay) ([]researchOpeningReview, error) {
	if env != "production" || route.BrokerID != "huaxin" || route.AccountID != b.AccountID || len(days) != len(b.Dates)+1 || len(b.Costs) != len(b.Dates) || !b.Review.Verified || days[0].Date != b.AnchorDate {
		return nil, errors.New("opening review requires verified production Huaxin research chain")
	}
	var result []researchOpeningReview
	for i, date := range b.Dates {
		d := days[i+1]
		previous := days[i].Date
		if d.Date != date || b.Costs[i].TradeDate != date {
			return nil, errors.New("opening review dates differ from research chain")
		}
		r := researchOpeningReview{Date: date, PreviousDate: previous, State: "unproven", Flags: []string{}}
		base, ps, err := readResearchOpeningBaseline(ctx, tx, env, route, previous)
		if err != nil {
			return nil, err
		}
		r.Baseline, r.PreviousPositions = base, ps
		if base.Completeness != "complete" || !base.MissingSecurityMeansZero {
			r.Flags = append(r.Flags, "previous_close_incomplete")
		}
		var queries []researchQueryProof
		for _, a := range d.Attempts {
			if a.Job == "pre_open_init" {
				for _, q := range a.Queries {
					if q.Step == "positions" {
						queries = append(queries, q)
					}
				}
			}
		}
		if len(queries) != 1 {
			r.Flags = append(r.Flags, "pre_open_attempt_missing_or_ambiguous")
			result = append(result, r)
			continue
		}
		q := queries[0]
		r.Query = &q
		requests, err := readResearchArchiveRole(ctx, tx, route.AccountID, q.MessageID, "cmd.query")
		if err != nil {
			return nil, err
		}
		var state string
		state, r.Requests, r.RequestSHA = reviewResearchRequest(requests, route, env, date, redisstream.ActionAccountPositions, q.MessageID)
		if state != "account_day_unfiltered_request" {
			r.Flags = append(r.Flags, "pre_open_request_scope_unproven")
		} else {
			var command struct {
				SentAt time.Time `json:"sent_at"`
			}
			cutoff, err := time.ParseInLocation("2006-01-02 15:04", date+" 09:15", timeutil.Location())
			if err != nil || json.Unmarshal(requests[0].Body, &command) != nil || command.SentAt.IsZero() || !command.SentAt.Before(cutoff) {
				r.Flags = append(r.Flags, "pre_open_command_time_unproven")
			}
		}
		replies, err := readResearchReplies(ctx, tx, route.AccountID, q.MessageID)
		if err != nil {
			return nil, err
		}
		for _, raw := range replies {
			r.Replies = append(r.Replies, json.RawMessage(raw.Body))
		}
		r.Positions, err = reviewResearchOpeningPages(route.AccountID, date, q, replies)
		if err != nil {
			r.Flags = append(r.Flags, err.Error())
			result = append(result, r)
			continue
		}
		costRows, lots, err := researchOpeningCostRows(route.AccountID, date, b.Costs[i].Positions)
		if err != nil {
			r.Flags = append(r.Flags, err.Error())
			result = append(result, r)
			continue
		}
		r.Lots = lots
		var prior []trading.Position
		for _, p := range ps {
			prior = append(prior, trading.Position{AccountID: route.AccountID, Symbol: p.Symbol, Exchange: trading.Exchange(p.Exchange), Quantity: p.Quantity, SellableQty: p.SellableQty})
		}
		previousRows, err := researchOpeningQuantities(route.AccountID, date, prior)
		if err != nil {
			r.Flags = append(r.Flags, err.Error())
			result = append(result, r)
			continue
		}
		openRows, err := researchOpeningQuantities(route.AccountID, date, r.Positions)
		if err != nil {
			return nil, err
		}
		if len(r.Flags) == 0 {
			pc := compareResearchBooks(previousRows, openRows)
			cc := compareResearchBooks(costRows, openRows)
			r.PreviousComparison, r.CostComparison = &pc, &cc
			if pc.State == "exact_projection_match" && cc.State == "exact_projection_match" {
				r.State = "opening_quantities_verified"
			} else {
				r.State = "opening_quantity_differences"
			}
		}
		result = append(result, r)
	}
	return result, nil
}
