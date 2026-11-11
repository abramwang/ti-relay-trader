package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

// Cost continuity is reviewed separately from NAV eligibility. No NAV is carried
// across a blocked date and this entry point has no write mode.
func runFuying13Review20261110(args []string) error {
	flags := flag.NewFlagSet("performance-fuying13-review-20261110", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config")
	output := flags.String("output", "", "new private read-only report")
	allocationReview := flags.Bool("confirmed-split-preview", false, "overlay the user's reviewed October 15 split-buy attribution in memory")
	componentPreview := flags.Bool("component-residual-preview", false, "research component carry and daily mark attribution; never publish")
	dayPoolPreview := flags.Bool("closed-day-pool-preview", false, "review October 16 closed T0 strategy-day cost without per-redemption buy allocation")
	inventoryPreview := flags.Bool("oct23-inventory-preview", false, "review October 23 split buys after the core inventory was fully sold; memory only")
	overnightPreview := flags.Bool("nov04-overnight-preview", false, "review confirmed November 3/4 overnight redemption separately; no target-day carry or publication")
	overnightChain := flags.Bool("overnight-chain-preview", false, "release the reviewed November 4 overnight lot in the memory cost chain; never publish")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" {
		return errors.New("new output required")
	}
	if *dayPoolPreview && (!*componentPreview || !*allocationReview) {
		return errors.New("closed-day pool review requires confirmed-split-preview and component-residual-preview")
	}
	if *inventoryPreview && (!*dayPoolPreview || !*componentPreview || !*allocationReview) {
		return errors.New("October 23 inventory review requires all earlier read-only preview stages")
	}
	if *overnightPreview && !*inventoryPreview {
		return errors.New("overnight review requires October 23 and all earlier read-only preview stages")
	}
	if *overnightChain && !*overnightPreview {
		return errors.New("overnight chain requires the November 4 diagnostic and all prior preview stages")
	}
	if _, err := os.Stat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("review is restricted to the production ledger")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("unexpected database")
	}
	repo := ledger.NewRepository(tx)
	store := &performancePreviewStore{Repository: repo, costs: map[string]map[string][]ledger.PositionCostState{fuying13Account: {}}, navs: map[string]map[string]ledger.PerformanceNAV{}}
	m, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	var serviceStore performance.Store = store
	var allocationHash string
	if *allocationReview {
		raw, err := os.ReadFile("testdata/fixtures/fuying13_t0_split_buy_review_20261110.json")
		if err != nil {
			return err
		}
		var links []ledger.T0Allocation
		if err := json.Unmarshal(raw, &links); err != nil {
			return err
		}
		if err := validateFuying13SplitReview(links); err != nil {
			return err
		}
		allocationHash = fmt.Sprintf("%x", sha256.Sum256(raw))
		serviceStore = &fuying13SplitPreview{performancePreviewStore: store, links: links}
	}
	var inventoryEvidence *fuying23InventoryEvidence
	var overnightHash string
	var overnightEvidence *fuyingOvernightEvidence
	if *inventoryPreview {
		evidence, err := loadFuying23InventoryReview(ctx, repo)
		if err != nil {
			return err
		}
		inventoryEvidence = &evidence
		serviceStore.(*fuying13SplitPreview).inventoryLinks = evidence.Links
	}
	if *overnightPreview {
		link, hash, err := loadFuyingOvernightConfirmation()
		if err != nil {
			return err
		}
		overnightHash = hash
		serviceStore.(*fuying13SplitPreview).overnightLinks = []ledger.T0Allocation{link}
	}
	service, err := settlement20261110Service(cfg, serviceStore, m)
	if *componentPreview {
		var overnightAllocations []performance.OvernightT0Allocation
		if *overnightChain {
			overnightAllocations = []performance.OvernightT0Allocation{{AccountID: fuying13Account, SourceDate: "2026-11-03", RedemptionDate: "2026-11-04", SecurityID: "159915.SZ", GroupID: fuyingOvernightGroup, SourceBuyOrderID: fuyingOvernightBuy, RedemptionOrderID: fuyingOvernightRedemption, Quantity: 1000000, EvidenceRef: "testdata/fixtures/fuying13_overnight_review_20261111.json"}}
		}
		poolDate := ""
		if *dayPoolPreview {
			poolDate = "2026-10-16"
		}
		service, err = performance.New(performance.Options{Store: serviceStore, Calendar: m, Market: m, FormulaVersion: cfg.Performance.FormulaVersion,
			ETFT0FrictionRate: cfg.Performance.ETFT0FrictionRate, AutoToleranceCNY: cfg.Performance.AutoToleranceCNY, AutoToleranceBP: cfg.Performance.AutoToleranceBP,
			WarningToleranceCNY: cfg.Performance.WarningToleranceCNY, WarningToleranceBP: cfg.Performance.WarningToleranceBP, ComponentResidualPreview: true, ClosedDayT0PoolPreviewDate: poolDate, OvernightT0Allocations: overnightAllocations})
	}
	if err != nil {
		return err
	}
	pcf, err := m.MarketETFCashComponents(ctx, url.Values{"security_id": {"510310.SH"}, "trade_date": {"20261013"}})
	if err != nil || pcf.StatusCode != 200 {
		return fmt.Errorf("cash PCF unavailable: status %d error %v", pcf.StatusCode, err)
	}
	var costs []performance.CostLedgerResult
	var contributions []performance.ContributionResult
	var blockedDate, failure string
	start, _ := time.ParseInLocation("20060102", "20261012", timeutil.Location())
	end, _ := time.ParseInLocation("20060102", "20261110", timeutil.Location())
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		calendar, e := m.TradingDayStatus(ctx, d.Format("20060102"))
		if e != nil || !calendar.IsTradingDayKnown {
			return fmt.Errorf("calendar unavailable %s: %v", day, e)
		}
		if !calendar.IsTradingDay {
			continue
		}
		if *overnightPreview && day == "2026-11-04" {
			evidence, e := reviewFuyingOvernight(ctx, repo, service, store.costs[fuying13Account]["2026-11-03"])
			if e != nil {
				return e
			}
			overnightEvidence = &evidence
		}
		cost, e := service.CalculateCostLedger(ctx, fuying13Account, day, performance.CostLedgerOptions{})
		costs = append(costs, cost)
		if e != nil || !fuying13CostCarryEligible(cost, day) {
			blockedDate = day
			failure = fmt.Sprintf("cost chain stopped: status=%s flags=%v error=%v", cost.Status, cost.QualityFlags, e)
			break
		}
		if day <= "2026-10-14" || *componentPreview {
			c, e := service.CalculateContributions(ctx, fuying13Account, day)
			if e != nil {
				return fmt.Errorf("contribution %s: %w", day, e)
			}
			contributions = append(contributions, c)
		}
		store.costs[fuying13Account][day] = append([]ledger.PositionCostState(nil), cost.Positions...)
		fmt.Fprintf(os.Stderr, "cost preview %s: %s, quantity breaks=%d\n", day, cost.Status, cost.Summary.QuantityBreaks)
	}
	report := map[string]any{"account_id": fuying13Account, "generated_at": timeutil.Now(), "database_read_only": true, "database_writes": 0, "broker_commands": 0,
		"date_from": "2026-10-12", "date_to": "2026-11-10", "cost_chain_complete": blockedDate == "", "first_cost_blocked_date": blockedDate, "error": failure,
		"costs": costs, "contributions": contributions, "cash_component_pcf": pcf.Payload, "economic_nav_publishable": false,
		"split_allocation_fixture_sha256": allocationHash,
		"component_residual_preview":      *componentPreview,
		"closed_day_pool_preview":         *dayPoolPreview,
		"oct23_inventory_evidence":        inventoryEvidence,
		"overnight_confirmation_sha256":   overnightHash,
		"overnight_diagnostic":            overnightEvidence,
		"overnight_chain_preview":         *overnightChain,
		"scope":                           "cost-only memory chain; no NAV propagation or removal of preview-only gates"}
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	return writeJSON(map[string]any{"database_writes": 0, "cost_days": len(costs), "cost_chain_complete": blockedDate == "", "first_cost_blocked_date": blockedDate, "economic_nav_publishable": false})
}

type fuying13SplitPreview struct {
	*performancePreviewStore
	links          []ledger.T0Allocation
	inventoryLinks []ledger.T0Allocation
	overnightLinks []ledger.T0Allocation
}

func (s *fuying13SplitPreview) ListT0AllocationHistory(ctx context.Context, account, date string) ([]ledger.T0Allocation, error) {
	old, err := s.Repository.ListT0AllocationHistory(ctx, account, date)
	if err != nil || account != fuying13Account {
		return old, err
	}
	switch date {
	case "2026-10-15":
		return mergeFuying13SplitReview(old, s.links)
	case fuying23Date:
		return mergeFuying13SplitReview(old, s.inventoryLinks)
	case "2026-11-03":
		return mergeFuying13SplitReview(old, s.overnightLinks)
	default:
		return old, nil
	}
}

func mergeFuying13SplitReview(old, links []ledger.T0Allocation) ([]ledger.T0Allocation, error) {
	result := append([]ledger.T0Allocation(nil), old...)
	for _, link := range links {
		found := false
		for _, prior := range old {
			if prior.GatewayOrderID == link.GatewayOrderID {
				if !reflect.DeepEqual(prior, link) {
					return nil, errors.New("split-buy audit already exists with a different revision or scope")
				}
				found = true
			}
		}
		if !found {
			result = append(result, link)
		}
	}
	_, err := ledger.ResolveT0Allocations(result)
	return result, err
}

func validateFuying13SplitReview(links []ledger.T0Allocation) error {
	if len(links) != 3 {
		return errors.New("three reviewed split-buy links required")
	}
	if _, err := ledger.ResolveT0Allocations(links); err != nil {
		return err
	}
	want := map[string]int64{"external-huaxin-31400004576801-12002A620003971": 20200, "external-huaxin-31400004576801-12002A620003974": 979800, "external-huaxin-31400004576801-12002A620003985": 1000000}
	for _, link := range links {
		side := "B"
		if link.Quantity == 1000000 {
			side = "R"
		}
		if link.AccountID != fuying13Account || link.TradeDate != "2026-10-15" || link.SecurityID != "159869.SZ" || link.Role != "redemption" || link.GroupID != "user-confirmed-768-20261015-redemption-split" || link.EvidenceType != "user_confirmation" || link.Status != "active" || link.Revision != 1 || link.TradeSide != side || want[link.GatewayOrderID] != link.Quantity {
			return errors.New("split-buy fixture outside confirmed scope")
		}
	}
	return nil
}

func fuying13CostCarryEligible(c performance.CostLedgerResult, day string) bool {
	return c.AccountID == fuying13Account && c.TradeDate == day && (c.Status == "calculated" || c.Status == "estimated") &&
		!c.Persisted && len(c.Positions) > 0 && c.Summary.BlockedItems == 0 && c.Summary.QuantityBreaks == 0 && c.Summary.T0BlockedBuckets == 0
}
