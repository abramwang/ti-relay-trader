package performance

import (
	"context"
	"fmt"
	"sort"

	"ti-relay-trader/internal/trading"
)

// T0FeeMembership exposes selected executions for a non-publishing fee audit.
// A linked fill may be a partial allocation. It is not a second broker fill.
type T0FeeMembership struct {
	SecurityID     string
	GroupID        string
	BuyFills       []trading.Fill
	ComponentFills []trading.Fill
	ComponentGaps  []CycleComponentGap
}

// ReviewT0FeeMembership reuses the contribution engine's ownership rules. The
// caller must verify selected quantities/amounts against its sealed calculation;
// membership alone does not prove fee coverage or final settlement.
func (s *Service) ReviewT0FeeMembership(ctx context.Context, account, date string) ([]T0FeeMembership, error) {
	parsed, _, err := parseTradeDate(date)
	if account == "" || err != nil || parsed != date {
		return nil, fmt.Errorf("invalid fee membership account/date")
	}
	orders, err := s.listContributionOrders(ctx, account, date)
	if err != nil {
		return nil, err
	}
	fills, err := s.listTradeDateFills(ctx, account, date)
	if err != nil {
		return nil, err
	}
	fills = dedupeContributionFills(fills)
	allocations, err := s.loadT0Allocations(ctx, account, date)
	if err != nil {
		return nil, err
	}
	orders, fills, _, err = applyT0Allocations(account, date, orders, fills, allocations)
	if err != nil {
		return nil, err
	}
	transfers, err := s.listContributionComponentTransfers(ctx, account, date)
	if err != nil {
		return nil, err
	}
	links := buildComponentSaleLinks(orders, fills, transfers)
	fills = dedupeContributionFills(append(fills, redemptionFillsFromComponentTransfers(orders, fills, transfers)...))
	open, err := s.listContributionPositions(ctx, account, date, "open")
	if err != nil {
		return nil, err
	}
	if len(open) == 0 {
		open, _ = s.previousClosePositionFallback(ctx, account, date)
	}
	if s.componentResidualPreview {
		completeUniqueComponentSales(&links, fills, open)
	}
	close, err := s.listContributionPositions(ctx, account, date, "close")
	if err != nil {
		return nil, err
	}
	close, _ = s.supplementClosePositionsFromNextOpen(ctx, account, date, close)
	buckets := buildContributionBuckets(orders, fills, open, close)
	ids := make([]string, 0, len(buckets))
	for id := range buckets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	instruments, _ := s.loadContributionInstruments(ctx, date, ids)
	units, _ := s.loadPCFRedemptionUnits(ctx, date, fills)
	groups, _, _ := s.buildT0Groups(orders, fills, instruments, units)
	groups, err = s.attachOvernightT0(ctx, account, date, groups, orders, fills, transfers)
	if err != nil {
		return nil, err
	}
	if s.closedDayT0PoolPreviewDate == date {
		groups, _, _, err = s.previewClosedDayPools(ctx, account, date, groups, orders, fills, open, close)
		if err != nil {
			return nil, err
		}
	}
	result := make([]T0FeeMembership, 0, len(groups))
	for _, g := range groups {
		if len(g.redemptions) == 0 {
			return nil, fmt.Errorf("fee group missing redemption")
		}
		component, _ := links.linkedFills(g.redemptions[0].GatewayOrderID)
		if containsStringValue(g.flags, closedDayPoolFlag) {
			component, _ = pooledComponentFills(g, links)
		}
		buys := append([]trading.Fill(nil), g.buyFills...)
		if g.overnight != nil {
			source, err := s.listTradeDateFills(ctx, account, g.overnight.link.SourceDate)
			if err != nil {
				return nil, err
			}
			for _, f := range dedupeContributionFills(source) {
				if f.GatewayOrderID == g.overnight.link.SourceBuyOrderID {
					buys = append(buys, f)
				}
			}
		}
		gaps, err := componentQuantityGaps(g.redemptions, links, open, close)
		if err != nil {
			return nil, err
		}
		result = append(result, T0FeeMembership{SecurityID: g.securityID, GroupID: g.groupID, BuyFills: buys, ComponentFills: component, ComponentGaps: gaps})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].SecurityID+"/"+result[i].GroupID < result[j].SecurityID+"/"+result[j].GroupID
	})
	return result, nil
}
