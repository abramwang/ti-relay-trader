package performance

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/trading"
)

func transferReviewFixture() ([]trading.Order, []trading.Fill, []trading.ComponentTransfer) {
	when := time.Date(2026, 10, 16, 9, 58, 0, 0, time.UTC)
	orders := []trading.Order{{GatewayOrderID: "redeem", Symbol: "159869", Exchange: trading.ExchangeSZ,
		TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, CumFilledQty: 1000000}}
	fills := []trading.Fill{{FillID: "sale", GatewayOrderID: "sell", Symbol: "300001", Exchange: trading.ExchangeSZ,
		TradeSide: trading.TradeSideSell, Qty: 100, Price: 10, MatchedAt: when.Add(time.Second)}}
	transfers := []trading.ComponentTransfer{
		{FillID: "parent", GatewayOrderID: "redeem", Symbol: "159869", Exchange: trading.ExchangeSZ, ComponentQty: 1000000, MatchedAt: when},
		{FillID: "stock", GatewayOrderID: "redeem", Symbol: "300001", Exchange: trading.ExchangeSZ, ComponentQty: 100, MatchedAt: when},
		{FillID: "cash-like", GatewayOrderID: "redeem", Symbol: "159900", Exchange: trading.ExchangeSZ,
			Qty: 1, ComponentQty: 1, Price: 116964, CashSubstitution: false, MatchedAt: when},
	}
	return orders, fills, transfers
}

func TestTransferReviewPreservesUnclassifiedRecordAndDuplicateIdentity(t *testing.T) {
	orders, fills, transfers := transferReviewFixture()
	transfers = append(transfers, transfers[2])
	result := reviewETFComponentTransfers("acct-1", "2026-10-16", orders, fills, transfers, time.Now())
	if result.UnmatchedGroups != 1 || len(result.Groups) != 1 || result.Groups[0].LinkedComponentSales != 1000 {
		t.Fatalf("unexpected review: %+v", result)
	}
	unlinked := result.Groups[0].UnlinkedComponents
	if len(unlinked) != 1 || unlinked[0].SecurityID != "159900.SZ" || unlinked[0].UnlinkedQty != 1 || len(unlinked[0].SourceTransfers) != 1 {
		t.Fatalf("unlinked evidence: %+v", unlinked)
	}
	source := unlinked[0].SourceTransfers[0]
	if source.Price != 116964 || source.ComponentValue != nil || source.CashSubstitution || source.MatchedAt == nil || source.MatchedAt.Format("Z07:00") != "+08:00" {
		t.Fatalf("source evidence was reinterpreted: %+v", source)
	}
	if transfers[2].CashSubstitution || fills[0].Qty != 100 {
		t.Fatal("audit changed source records")
	}
}

func TestTransferReviewNeverInventsMissingTimestamp(t *testing.T) {
	orders, fills, transfers := transferReviewFixture()
	transfers[2].MatchedAt = time.Time{}
	result := reviewETFComponentTransfers("acct-1", "2026-10-16", orders, fills, transfers, time.Now())
	if result.Groups[0].UnlinkedComponents[0].SourceTransfers[0].MatchedAt != nil {
		t.Fatal("missing source timestamp was replaced with a synthetic clock value")
	}
}

func TestTransferReviewMatchedLinksNeverProveCashOrEntitlementCoverage(t *testing.T) {
	orders, fills, transfers := transferReviewFixture()
	transfers[2].CashSubstitution = true
	result := reviewETFComponentTransfers("acct-1", "2026-10-16", orders, fills, transfers, time.Now())
	if result.UnmatchedGroups != 0 || result.Groups[0].LinkStatus != "matched" || len(result.Groups[0].UnlinkedComponents) != 0 {
		t.Fatalf("cash substitution was treated as a stock: %+v", result)
	}
	if result.CashSettlementVerified || result.EconomicCoverageVerified || result.FinancialPublicationAllowed {
		t.Fatal("quantity matching was promoted to financial verification")
	}
}

func TestTransferReviewDistinguishesMissingEvidencePartialSalesAndOrphans(t *testing.T) {
	orders, fills, transfers := transferReviewFixture()
	fills[0].Qty = 40
	orders = append(orders, trading.Order{GatewayOrderID: "missing", Symbol: "510310", Exchange: trading.ExchangeSH,
		TradeSide: trading.TradeSideRedemption, BusinessType: trading.BusinessTypeETF, CumFilledQty: 100})
	transfers = append(transfers, trading.ComponentTransfer{GatewayOrderID: "unknown", Symbol: "600000"})
	result := reviewETFComponentTransfers("acct-1", "2026-10-16", orders, fills, transfers, time.Now())
	if result.UnmatchedGroups != 2 || result.Groups[0].LinkStatus != "missing_component_evidence" ||
		len(result.OrphanTransferOrderIDs) != 1 || result.OrphanTransferOrderIDs[0] != "unknown" {
		t.Fatalf("missing evidence hidden: %+v", result)
	}
	unlinked := result.Groups[1].UnlinkedComponents
	if len(unlinked) != 2 || unlinked[1].SecurityID != "300001.SZ" || unlinked[1].UnlinkedQty != 60 {
		t.Fatalf("partial linkage: %+v", unlinked)
	}
}

type pagedTransferReviewStore struct {
	*fakePerformanceStore
	failTransferPage bool
}

func reviewPageRange(cursor string, limit, length int) (int, int) {
	start, _ := strconv.Atoi(cursor)
	if start > length {
		start = length
	}
	end := start + limit
	if end > length {
		end = length
	}
	return start, end
}

func (store *pagedTransferReviewStore) ListOrders(_ context.Context, q trading.OrderQuery) ([]trading.Order, error) {
	store.orderQueries = append(store.orderQueries, q)
	a, b := reviewPageRange(q.Cursor, q.Limit, len(store.orders))
	return store.orders[a:b], nil
}

func (store *pagedTransferReviewStore) ListFills(_ context.Context, q trading.FillQuery) ([]trading.Fill, error) {
	store.fillQueries = append(store.fillQueries, q)
	a, b := reviewPageRange(q.Cursor, q.Limit, len(store.fills))
	return store.fills[a:b], nil
}

func (store *pagedTransferReviewStore) ListComponentTransfers(_ context.Context, q trading.ComponentTransferQuery) ([]trading.ComponentTransfer, error) {
	if store.failTransferPage && q.Cursor == "500" {
		return nil, errors.New("transfer page unavailable")
	}
	a, b := reviewPageRange(q.Cursor, q.Limit, len(store.transfers))
	return store.transfers[a:b], nil
}

func TestTransferReviewReadsEveryPageWithoutMarketOrWrites(t *testing.T) {
	store := &pagedTransferReviewStore{fakePerformanceStore: &fakePerformanceStore{}}
	for i := 0; i < 501; i++ {
		store.orders = append(store.orders, trading.Order{GatewayOrderID: strconv.Itoa(i)})
		store.fills = append(store.fills, trading.Fill{FillID: strconv.Itoa(i), GatewayOrderID: strconv.Itoa(i), Qty: 1})
		store.transfers = append(store.transfers, trading.ComponentTransfer{GatewayOrderID: strconv.Itoa(i)})
	}
	service, err := New(Options{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ReviewETFComponentTransfers(context.Background(), "acct-1", "20261016")
	if err != nil || result.Orders != 501 || result.Fills != 501 || result.Transfers != 501 {
		t.Fatalf("pagination: %+v / %v", result, err)
	}
	if len(store.orderQueries) != 2 || len(store.fillQueries) != 2 || result.TradeDate != "2026-10-16" {
		t.Fatalf("pagination queries: %+v / %+v", store.orderQueries, store.fillQueries)
	}
	if len(store.navUpserts)+len(store.navStatusUpdates)+len(store.reconciliationUpserts)+len(store.costUpserts)+len(store.upserts) != 0 {
		t.Fatal("review wrote financial state")
	}
	store.failTransferPage = true
	partial, err := service.ReviewETFComponentTransfers(context.Background(), "acct-1", "20261016")
	if err == nil || partial.AccountID != "" {
		t.Fatalf("partial evidence returned as successful: %+v / %v", partial, err)
	}
}

func TestTransferReviewRejectsInvalidAndOverLimitEvidence(t *testing.T) {
	store := &pagedTransferReviewStore{fakePerformanceStore: &fakePerformanceStore{}}
	service, _ := New(Options{Store: store})
	for _, args := range [][2]string{{"", "20261016"}, {"acct-1", ""}, {"acct-1", "20260230"}} {
		if _, err := service.ReviewETFComponentTransfers(context.Background(), args[0], args[1]); err == nil {
			t.Fatal("invalid audit scope accepted")
		}
	}
	store.transfers = make([]trading.ComponentTransfer, contributionPageLimit*maxContributionPages+1)
	if _, err := service.ReviewETFComponentTransfers(context.Background(), "acct-1", "20261016"); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("truncated transfer evidence accepted: %v", err)
	}
	unsupported, _ := New(Options{Store: struct{ Store }{store}})
	if _, err := unsupported.ReviewETFComponentTransfers(context.Background(), "acct-1", "20261016"); err == nil {
		t.Fatal("missing transfer store accepted as complete empty evidence")
	}
}
