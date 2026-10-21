package performance

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type ETFTransferReview struct {
	AccountID                   string                   `json:"account_id"`
	TradeDate                   string                   `json:"trade_date"`
	Scope                       string                   `json:"scope"`
	Orders                      int                      `json:"orders"`
	Fills                       int                      `json:"fills"`
	Transfers                   int                      `json:"transfers"`
	Groups                      []ETFTransferGroupReview `json:"groups"`
	UnmatchedGroups             int                      `json:"unmatched_groups"`
	OrphanTransferOrderIDs      []string                 `json:"orphan_transfer_order_ids,omitempty"`
	CashSettlementVerified      bool                     `json:"cash_settlement_verified"`
	EconomicCoverageVerified    bool                     `json:"economic_coverage_verified"`
	FinancialPublicationAllowed bool                     `json:"financial_publication_allowed"`
	QualityFlags                []string                 `json:"quality_flags,omitempty"`
	GeneratedAt                 time.Time                `json:"generated_at"`
}

type ETFTransferGroupReview struct {
	GatewayOrderID       string                       `json:"gateway_order_id"`
	SecurityID           string                       `json:"security_id"`
	OrderStreamID        string                       `json:"order_stream_id,omitempty"`
	LinkStatus           string                       `json:"link_status"`
	ExpectedSecurities   int                          `json:"expected_securities"`
	LinkedComponentSales float64                      `json:"linked_component_sales"`
	UnlinkedComponents   []ETFUnlinkedComponentReview `json:"unlinked_components"`
}

type ETFUnlinkedComponentReview struct {
	SecurityID      string                      `json:"security_id"`
	ExpectedQty     int64                       `json:"expected_qty"`
	LinkedQty       int64                       `json:"linked_qty"`
	UnlinkedQty     int64                       `json:"unlinked_qty"`
	SourceTransfers []ETFTransferSourceEvidence `json:"source_transfers"`
}

type ETFTransferSourceEvidence struct {
	FillID           string     `json:"fill_id"`
	OrderStreamID    string     `json:"order_stream_id,omitempty"`
	Qty              int64      `json:"qty"`
	ComponentQty     int64      `json:"component_qty"`
	Price            float64    `json:"price"`
	ComponentValue   *float64   `json:"component_value"`
	CashSubstitution bool       `json:"cash_substitution"`
	RecordType       string     `json:"record_type"`
	TransferType     string     `json:"transfer_type,omitempty"`
	MatchedAt        *time.Time `json:"matched_at"`
}

// ReviewETFComponentTransfers audits stored linkage, without market requests or settlement inference.
func (service *Service) ReviewETFComponentTransfers(ctx context.Context, accountID, tradeDate string) (ETFTransferReview, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ETFTransferReview{}, errors.New("account_id is required")
	}
	date, _, err := parseTradeDate(tradeDate)
	if err != nil {
		return ETFTransferReview{}, err
	}
	if _, ok := service.store.(interface {
		ListComponentTransfers(context.Context, trading.ComponentTransferQuery) ([]trading.ComponentTransfer, error)
	}); !ok {
		return ETFTransferReview{}, errors.New("component transfer evidence store is required")
	}
	orders, err := service.listContributionOrders(ctx, accountID, date)
	if err != nil {
		return ETFTransferReview{}, err
	}
	fills, err := service.listTradeDateFills(ctx, accountID, date)
	if err != nil {
		return ETFTransferReview{}, err
	}
	transfers, err := service.listContributionComponentTransfers(ctx, accountID, date)
	if err != nil {
		return ETFTransferReview{}, err
	}
	return reviewETFComponentTransfers(accountID, date, orders, dedupeContributionFills(fills), transfers, service.now()), nil
}

func reviewETFComponentTransfers(accountID, date string, orders []trading.Order, fills []trading.Fill, transfers []trading.ComponentTransfer, now time.Time) ETFTransferReview {
	links := buildComponentSaleLinks(orders, fills, transfers)
	result := ETFTransferReview{
		AccountID: accountID, TradeDate: date, Scope: "stored_component_sale_linkage_only",
		Orders: len(orders), Fills: len(fills), Transfers: len(transfers),
		Groups: make([]ETFTransferGroupReview, 0), QualityFlags: append([]string(nil), links.flags...),
		GeneratedAt: now.In(timeutil.Location()),
	}
	knownOrders := make(map[string]bool, len(orders))
	for _, order := range orders {
		knownOrders[order.GatewayOrderID] = true
	}
	for _, transfer := range transfers {
		if !knownOrders[transfer.GatewayOrderID] {
			result.OrphanTransferOrderIDs = appendUnique(result.OrphanTransferOrderIDs, transfer.GatewayOrderID)
		}
	}
	if len(result.OrphanTransferOrderIDs) > 0 {
		sort.Strings(result.OrphanTransferOrderIDs)
		result.QualityFlags = appendUnique(result.QualityFlags, "component_transfer_order_missing")
	}
	seen := make(map[string]bool)
	for _, order := range orders {
		if !isETFBusinessRedemption(order.TradeSide, order.BusinessType) || seen[order.GatewayOrderID] {
			continue
		}
		group := links.groups[order.GatewayOrderID]
		if group == nil && order.CumFilledQty <= 0 {
			continue
		}
		seen[order.GatewayOrderID] = true
		item := ETFTransferGroupReview{
			GatewayOrderID: order.GatewayOrderID, SecurityID: contributionSecurityID(order.Symbol, order.Exchange),
			OrderStreamID: order.OrderStreamID, LinkStatus: "missing_component_evidence",
			UnlinkedComponents: make([]ETFUnlinkedComponentReview, 0),
		}
		if group != nil {
			item.ExpectedSecurities = len(group.expectedBySecurity)
			for _, fill := range group.linkedFills {
				item.LinkedComponentSales += contributionFillAmount(fill)
			}
			item.LinkedComponentSales = roundMoney(item.LinkedComponentSales)
			if componentTransferGroupComplete(group) {
				item.LinkStatus = "matched"
			} else if len(group.expectedBySecurity) > 0 {
				item.LinkStatus = "unmatched"
			}
			for securityID, expected := range group.expectedBySecurity {
				linked := group.linkedBySecurity[securityID]
				if expected == linked {
					continue
				}
				component := ETFUnlinkedComponentReview{
					SecurityID: securityID, ExpectedQty: expected, LinkedQty: linked, UnlinkedQty: expected - linked,
					SourceTransfers: make([]ETFTransferSourceEvidence, 0),
				}
				for _, transfer := range group.sourcesBySecurity[securityID] {
					var matchedAt *time.Time
					if !transfer.MatchedAt.IsZero() {
						value := transfer.MatchedAt.In(timeutil.Location())
						matchedAt = &value
					}
					component.SourceTransfers = append(component.SourceTransfers, ETFTransferSourceEvidence{
						FillID: transfer.FillID, OrderStreamID: transfer.OrderStreamID,
						Qty: transfer.Qty, ComponentQty: transfer.ComponentQty, Price: transfer.Price,
						ComponentValue: transfer.ComponentValue, CashSubstitution: transfer.CashSubstitution,
						RecordType: transfer.RecordType, TransferType: transfer.TransferType,
						MatchedAt: matchedAt,
					})
				}
				sort.Slice(component.SourceTransfers, func(i, j int) bool {
					return component.SourceTransfers[i].FillID < component.SourceTransfers[j].FillID
				})
				item.UnlinkedComponents = append(item.UnlinkedComponents, component)
			}
			sort.Slice(item.UnlinkedComponents, func(i, j int) bool {
				return item.UnlinkedComponents[i].SecurityID < item.UnlinkedComponents[j].SecurityID
			})
		}
		if item.LinkStatus != "matched" {
			result.UnmatchedGroups++
		}
		result.Groups = append(result.Groups, item)
	}
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i].GatewayOrderID < result.Groups[j].GatewayOrderID })
	return result
}
