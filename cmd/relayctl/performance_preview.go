package main

import (
	"context"
	"sort"

	"ti-relay-trader/internal/ledger"
)

// Only eligible paired cost/NAV results are carried forward. The underlying
// repository runs in a read-only transaction, including its optional readers.
type performancePreviewStore struct {
	*ledger.Repository
	costs map[string]map[string][]ledger.PositionCostState
	navs  map[string]map[string]ledger.PerformanceNAV
}

func (s *performancePreviewStore) remember(account, date string, costs []ledger.PositionCostState, nav ledger.PerformanceNAV) {
	if s.costs[account] == nil {
		s.costs[account] = make(map[string][]ledger.PositionCostState)
		s.navs[account] = make(map[string]ledger.PerformanceNAV)
	}
	s.costs[account][date] = append([]ledger.PositionCostState(nil), costs...)
	s.navs[account][date] = nav
}

func (s *performancePreviewStore) ListPositionCostStates(ctx context.Context, q ledger.PositionCostStateQuery) ([]ledger.PositionCostState, error) {
	base, err := s.Repository.ListPositionCostStates(ctx, q)
	if err != nil {
		return nil, err
	}
	return previewCostStates(base, s.costs[q.AccountID], q), nil
}

func previewCostStates(base []ledger.PositionCostState, previews map[string][]ledger.PositionCostState, q ledger.PositionCostStateQuery) []ledger.PositionCostState {
	if q.TradeDate != "" {
		if items, ok := previews[q.TradeDate]; ok {
			return append([]ledger.PositionCostState(nil), items...)
		}
		return base
	}
	latest := ""
	for _, item := range base {
		if item.TradeDate > latest {
			latest = item.TradeDate
		}
	}
	selected := ""
	for date := range previews {
		if date < q.BeforeDate && date >= latest && date > selected {
			selected = date
		}
	}
	if selected != "" {
		return append([]ledger.PositionCostState(nil), previews[selected]...)
	}
	return base
}

func (s *performancePreviewStore) ListPerformanceNAVs(ctx context.Context, account, from, to string) ([]ledger.PerformanceNAV, error) {
	base, err := s.Repository.ListPerformanceNAVs(ctx, account, from, to)
	if err != nil {
		return nil, err
	}
	return previewNAVs(base, s.navs[account], from, to), nil
}

func previewNAVs(base []ledger.PerformanceNAV, previews map[string]ledger.PerformanceNAV, from, to string) []ledger.PerformanceNAV {
	byDate := make(map[string]ledger.PerformanceNAV, len(base)+len(previews))
	for _, item := range base {
		byDate[item.TradeDate] = item
	}
	for date, item := range previews {
		if (from == "" || date >= from) && (to == "" || date <= to) {
			byDate[date] = item
		}
	}
	items := make([]ledger.PerformanceNAV, 0, len(byDate))
	for _, item := range byDate {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TradeDate < items[j].TradeDate })
	return items
}
