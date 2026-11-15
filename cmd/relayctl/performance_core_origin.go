package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/trading"
)

type coreOriginLot struct {
	Security string                     `json:"security_id"`
	From     string                     `json:"zero_open_date"`
	Days     []ledger.PositionCostState `json:"replayed_days"`
}

type coreOriginReview struct {
	Account     string                       `json:"account_id"`
	Date        string                       `json:"anchor_date"`
	Lots        []coreOriginLot              `json:"lots"`
	Openings    []researchOpeningReview      `json:"opening_evidence"`
	Queries     []researchQueryDay           `json:"query_evidence"`
	Verified    bool                         `json:"zero_origin_costs_verified"`
	Publishable bool                         `json:"cost_publishable"`
	Scope       string                       `json:"scope"`
	SourceSHA   map[string]string            `json:"source_sha256"`
	Rebuilt     performance.CostLedgerResult `json:"rebuilt_anchor"`
}

func selectCoreOrigins(b performance.ResearchCostBundle) ([]coreOriginLot, int, error) {
	if len(b.Costs) == 0 || len(b.Costs) != len(b.Dates) {
		return nil, 0, errors.New("missing cost chain")
	}
	last := b.Costs[len(b.Costs)-1]
	if err := cleanContinuationCost(last, b.AccountID, b.Dates[len(b.Dates)-1]); err != nil {
		return nil, 0, err
	}
	var lots []coreOriginLot
	start := len(b.Costs)
	for _, target := range last.Positions {
		found := -1
		for i, day := range b.Costs {
			for _, row := range day.Positions {
				if row.Symbol == target.Symbol && row.Exchange == target.Exchange && row.CostBucket == "CORE" && row.OpenQuantity == 0 && row.OpenTotalCost == 0 {
					found = i
				}
			}
		}
		if found <= 0 {
			return nil, 0, fmt.Errorf("no independently checkable zero opening for %s.%s", target.Symbol, target.Exchange)
		}
		if found < start {
			start = found
		}
		lots = append(lots, coreOriginLot{Security: target.Symbol + "." + target.Exchange, From: b.Dates[found]})
	}
	sort.Slice(lots, func(i, j int) bool { return lots[i].Security < lots[j].Security })
	return lots, start, nil
}

func coreCostFacts(p ledger.PositionCostState) []any {
	return []any{p.OpenQuantity, p.OpenTotalCost, p.BuyQuantity, p.BuyAmount, p.BuyFee, p.SellQuantity, p.SellAmount, p.SellFee, p.RealizedPnL, p.CloseQuantity, p.CloseTotalCost, p.AverageCost}
}

func verifyCoreOriginBooks(day researchQueryDay) error {
	for _, step := range []string{"orders", "fills"} {
		matched := 0
		for _, a := range day.Attempts {
			if !a.LinkedToClose {
				continue
			}
			for _, q := range a.Queries {
				if q.Step != step {
					continue
				}
				c := q.BookCoverage
				if q.State != "terminal_archive_consistent" || c == nil || c.RequestState != "account_day_unfiltered_request" || len(c.DecodeIssues) != 0 {
					return fmt.Errorf("unproven %s query on %s", step, day.Date)
				}
				for _, d := range c.Rows.Differences {
					if d.Kind != "book_only_not_in_this_query" || d.EventEvidence == nil || d.EventEvidence.State != "matching_full_event_found" {
						return fmt.Errorf("unresolved %s book difference on %s", step, day.Date)
					}
				}
				if c.Rows.State != "exact_projection_match" && c.Rows.State != "differences_require_review" {
					return errors.New("unknown query comparison state")
				}
				matched++
			}
		}
		if matched != 1 {
			return fmt.Errorf("ambiguous/missing %s close query on %s", step, day.Date)
		}
	}
	return nil
}

func readCoreOriginReview(ctx context.Context, tx *sql.Tx, cfg *config.Config, b performance.ResearchCostBundle) (*coreOriginReview, error) {
	lots, first, err := selectCoreOrigins(b)
	if err != nil {
		return nil, err
	}
	route, ok := cfg.AccountRoute(b.AccountID)
	if !ok || cfg.Service.Environment != config.EnvironmentProduction || route.BrokerID != "huaxin" {
		return nil, errors.New("zero-origin evidence currently requires a production Huaxin route")
	}
	r := &coreOriginReview{Account: b.AccountID, Date: b.Dates[len(b.Dates)-1], Lots: lots,
		SourceSHA: map[string]string{},
		Scope:     "independent ordinary acquisition-cost replay; no NAV or publication authorization; query scope, source freshness and price valuation remain explicit"}
	sub := b
	sub.AnchorDate, sub.Opening = b.Dates[first-1], b.Costs[first-1].Positions
	sub.Dates, sub.Costs = b.Dates[first:], b.Costs[first:]
	sub.Review, err = performance.ReviewCostContinuity(b.AccountID, sub.AnchorDate, sub.Opening, sub.Dates, sub.Costs)
	if err != nil {
		return nil, err
	}
	for _, date := range append([]string{sub.AnchorDate}, sub.Dates...) {
		d, err := readResearchQueryDay(ctx, tx, string(cfg.Service.Environment), route, date, true, true)
		if err != nil {
			return nil, err
		}
		r.Queries = append(r.Queries, d)
	}
	r.Openings, err = readResearchOpeningEvidence(ctx, tx, string(cfg.Service.Environment), route, sub, r.Queries)
	if err != nil {
		return nil, err
	}
	repo := ledger.NewRepository(tx)
	for index, date := range sub.Dates {
		if err := verifyCoreOriginBooks(r.Queries[index+1]); err != nil {
			return nil, err
		}
		opening := r.Openings[index]
		if opening.State != "opening_quantities_verified" {
			return nil, fmt.Errorf("opening set not proved on %s: %v", date, opening.Flags)
		}
		baseline, closePositions, err := readResearchOpeningBaseline(ctx, tx, string(cfg.Service.Environment), route, date)
		if err != nil || baseline.Completeness != "complete" || !baseline.MissingSecurityMeansZero {
			return nil, fmt.Errorf("close set not proved on %s: %v", date, err)
		}
		openQ, closeQ := map[string]int64{}, map[string]int64{}
		for _, p := range opening.Positions {
			openQ[p.Symbol+"."+string(p.Exchange)] = p.Quantity
		}
		for _, p := range closePositions {
			closeQ[p.SecurityID] = p.Quantity
		}
		var fills []trading.Fill
		for offset := 0; ; offset += 500 {
			if offset >= 20000 {
				return nil, errors.New("fill evidence exceeds budget")
			}
			page, err := repo.ListFills(ctx, trading.FillQuery{AccountID: b.AccountID, TradeDate: date, History: true, Limit: 500, Cursor: strconv.Itoa(offset)})
			if err != nil {
				return nil, err
			}
			fills = append(fills, page...)
			if len(page) < 500 {
				break
			}
		}
		fees, err := repo.ListOrderFeeRecords(ctx, ledger.OrderFeeRecordQuery{AccountID: b.AccountID, TradeDate: date, Limit: 5000})
		if err != nil || len(fees) >= 5000 {
			return nil, fmt.Errorf("fee evidence unavailable/truncated: %v", err)
		}
		raw, err := json.Marshal(map[string]any{"fills": fills, "fees": fees, "opening": opening, "close": closePositions, "close_sha": baseline.ContentSHA256})
		if err != nil {
			return nil, err
		}
		r.SourceSHA[date] = fmt.Sprintf("%x", sha256.Sum256(raw))
		rows := map[string]ledger.PositionCostState{}
		for _, p := range sub.Costs[index].Positions {
			if p.CostBucket == "CORE" {
				rows[p.Symbol+"."+p.Exchange] = p
			}
		}
		for n := range r.Lots {
			lot := &r.Lots[n]
			if date < lot.From {
				continue
			}
			expected, exists := rows[lot.Security]
			if !exists || expected.CorporateActionQuantityDelta != 0 || (expected.CorporateActionType != "none" && expected.CorporateActionType != "") || expected.Status != "calculated" || len(expected.QualityFlags) != 0 {
				return nil, fmt.Errorf("ordinary row missing or requires review: %s %s", date, lot.Security)
			}
			var qty int64
			var cost float64
			if len(lot.Days) > 0 {
				previous := lot.Days[len(lot.Days)-1]
				qty, cost = previous.CloseQuantity, previous.CloseTotalCost
			}
			if openQ[lot.Security] != qty {
				return nil, fmt.Errorf("zero origin/carry differs from complete open query: %s %s", date, lot.Security)
			}
			var selected []trading.Fill
			for _, fill := range fills {
				if fill.Symbol+"."+string(fill.Exchange) == lot.Security {
					selected = append(selected, fill)
				}
			}
			actual, err := performance.ReplayCoreCostDay(b.AccountID, date, expected.Symbol, expected.Exchange, qty, cost, selected, fees)
			if err != nil {
				return nil, err
			}
			if actual.CloseQuantity != closeQ[lot.Security] || !reflect.DeepEqual(coreCostFacts(actual), coreCostFacts(expected)) {
				return nil, fmt.Errorf("raw replay differs from archived cost/close: %s %s actual=%v expected=%v close=%d", date, lot.Security, coreCostFacts(actual), coreCostFacts(expected), closeQ[lot.Security])
			}
			lot.Days = append(lot.Days, actual)
		}
	}
	previous := b.Dates[len(b.Dates)-2]
	var seed []ledger.PositionCostState
	for _, lot := range r.Lots {
		for _, p := range lot.Days {
			if p.TradeDate == previous {
				seed = append(seed, p)
			}
		}
	}
	base, ps, err := readResearchOpeningBaseline(ctx, tx, string(cfg.Service.Environment), route, previous)
	if err != nil || base.Completeness != "complete" || !base.MissingSecurityMeansZero {
		return nil, errors.New("replay seed closing coverage missing")
	}
	actualQuantities := map[string]int64{}
	for _, p := range seed {
		if p.CloseQuantity > 0 {
			actualQuantities[p.Symbol+"."+p.Exchange] = p.CloseQuantity
		}
	}
	expectedQuantities := map[string]int64{}
	for _, p := range ps {
		if p.Quantity > 0 {
			expectedQuantities[p.SecurityID] = p.Quantity
		}
	}
	if !reflect.DeepEqual(actualQuantities, expectedQuantities) {
		return nil, errors.New("independent replay does not cover full seed holdings")
	}
	store := &performancePreviewStore{Repository: repo, costs: map[string]map[string][]ledger.PositionCostState{b.AccountID: {previous: seed}}, navs: map[string]map[string]ledger.PerformanceNAV{}}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return nil, err
	}
	service, err := settlement20261110Service(cfg, store, client)
	if err != nil {
		return nil, err
	}
	r.Rebuilt, err = service.CalculateCostLedger(ctx, b.AccountID, r.Date, performance.CostLedgerOptions{})
	if err != nil {
		return nil, err
	}
	if err := cleanContinuationCost(r.Rebuilt, b.AccountID, r.Date); err != nil {
		return nil, err
	}
	old := map[string][]any{}
	for _, p := range b.Costs[len(b.Costs)-1].Positions {
		old[p.Symbol+"."+p.Exchange] = coreCostFacts(p)
	}
	fresh := map[string][]any{}
	for _, p := range r.Rebuilt.Positions {
		fresh[p.Symbol+"."+p.Exchange] = coreCostFacts(p)
	}
	if !reflect.DeepEqual(old, fresh) {
		return nil, errors.New("normal engine anchor differs from raw zero-origin replay")
	}
	r.Verified = true
	return r, nil
}
