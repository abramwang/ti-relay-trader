package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

// Capture the typed values actually consumed by the engine, not reconstructed
// HTTP bodies. No headers, database credentials or production writes are used.
type researchInputCapture struct {
	mu        sync.Mutex
	file      *os.File
	directory string
	digest    hash.Hash
	calls     int
	bytes     int
	failure   error
	complete  bool
	counts    map[string]int
	started   time.Time
}

func newResearchInputCapture(directory string) (*researchInputCapture, error) {
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("capture directory must not exist")
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(directory, "inputs.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	return &researchInputCapture{file: f, directory: directory, digest: sha256.New(), counts: map[string]int{}, started: timeutil.Now()}, nil
}

func captureResearchCode(c *researchInputCapture) error {
	hashes := map[string]string{}
	for _, root := range []string{"cmd/relayctl", "internal/performance", "internal/ledger", "internal/market", "internal/trading", "internal/config", "internal/timeutil"} {
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			hashes[path] = hex.EncodeToString(sum[:])
			return nil
		}); err != nil {
			return err
		}
	}
	for _, path := range []string{"go.mod", "go.sum"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		hashes[path] = hex.EncodeToString(sum[:])
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	file, err := os.Open(executable)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, readErr := io.Copy(h, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return err
	}
	hashes["running_executable_sha256"] = hex.EncodeToString(h.Sum(nil))
	return c.record("code_fingerprints", nil, hashes, nil)
}

func (c *researchInputCapture) record(method string, request, response any, sourceErr error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil {
		return c.failure
	}
	if c.file == nil {
		return errors.New("capture closed")
	}
	errorText := ""
	if sourceErr != nil {
		errorText = sourceErr.Error()
	}
	raw, err := json.Marshal(map[string]any{"sequence": c.calls + 1, "captured_at": timeutil.Now(), "method": method, "request": request, "response": response, "error": errorText})
	if err == nil && (c.calls >= 10000 || len(raw) > 16<<20 || c.bytes+len(raw)+1 > 256<<20) {
		err = errors.New("capture budget exceeded")
	}
	if err != nil {
		c.failure = err
		return err
	}
	raw = append(raw, '\n')
	n, err := c.file.Write(raw)
	if err == nil && n != len(raw) {
		err = errors.New("short capture write")
	}
	if err != nil {
		c.failure = err
		return err
	}
	c.digest.Write(raw)
	c.calls++
	c.bytes += len(raw)
	c.counts[method]++
	return nil
}

func (c *researchInputCapture) close(runErr error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == nil {
		return errors.New("capture already closed")
	}
	err := errors.Join(c.failure, c.file.Sync(), c.file.Close())
	c.file = nil
	errorText := ""
	if runErr != nil {
		errorText = runErr.Error()
	}
	manifest := map[string]any{"schema": "relay.research_engine_inputs.v1", "account_id": fuying13Account, "baseline_version_id": fuyingResearchVersion, "baseline_payload_sha256": fuyingResearchPayloadHash, "started_at": c.started, "finished_at": timeutil.Now(), "complete": c.complete && runErr == nil && err == nil, "calls": c.calls, "bytes": c.bytes, "sha256": hex.EncodeToString(c.digest.Sum(nil)), "methods": c.counts, "run_error": errorText, "database_writes": 0, "broker_commands": 0, "economic_nav_publishable": false, "scope": "typed engine inputs plus helper results; not raw HTTP, not offline replay certification or historical query completeness"}
	return errors.Join(err, writeETFTransferReviewFile(filepath.Join(c.directory, "manifest.json"), manifest))
}

func captureCall[T any](c *researchInputCapture, method string, request any, call func() (T, error)) (T, error) {
	raw, encodeErr := json.Marshal(request)
	if encodeErr != nil {
		var zero T
		return zero, encodeErr
	}
	value, err := call()
	if recordErr := c.record(method, json.RawMessage(raw), value, err); recordErr != nil {
		return value, errors.Join(err, recordErr)
	}
	return value, err
}

type researchMarket interface {
	performance.ContributionMarket
	performance.TradingCalendar
	MarketETFComponents(context.Context, url.Values) (market.MeridianResponse, error)
}

type capturedResearchMarket struct {
	base    researchMarket
	capture *researchInputCapture
}

func (m *capturedResearchMarket) TradingDayStatus(ctx context.Context, date string) (market.TradingDayStatus, error) {
	return captureCall(m.capture, "market.TradingDayStatus", date, func() (market.TradingDayStatus, error) { return m.base.TradingDayStatus(ctx, date) })
}
func (m *capturedResearchMarket) MetadataInstruments(ctx context.Context, q url.Values) (market.MeridianResponse, error) {
	return captureCall(m.capture, "market.MetadataInstruments", q, func() (market.MeridianResponse, error) { return m.base.MetadataInstruments(ctx, q) })
}
func (m *capturedResearchMarket) MetadataAdjustFactors(ctx context.Context, q url.Values) (market.MeridianResponse, error) {
	return captureCall(m.capture, "market.MetadataAdjustFactors", q, func() (market.MeridianResponse, error) { return m.base.MetadataAdjustFactors(ctx, q) })
}
func (m *capturedResearchMarket) MarketBars(ctx context.Context, q url.Values) (market.MeridianResponse, error) {
	return captureCall(m.capture, "market.MarketBars", q, func() (market.MeridianResponse, error) { return m.base.MarketBars(ctx, q) })
}
func (m *capturedResearchMarket) MarketSnapshots(ctx context.Context, q url.Values) (market.MeridianResponse, error) {
	return captureCall(m.capture, "market.MarketSnapshots", q, func() (market.MeridianResponse, error) { return m.base.MarketSnapshots(ctx, q) })
}
func (m *capturedResearchMarket) MarketETFCashComponents(ctx context.Context, q url.Values) (market.MeridianResponse, error) {
	return captureCall(m.capture, "market.MarketETFCashComponents", q, func() (market.MeridianResponse, error) { return m.base.MarketETFCashComponents(ctx, q) })
}
func (m *capturedResearchMarket) MarketETFComponents(ctx context.Context, q url.Values) (market.MeridianResponse, error) {
	return captureCall(m.capture, "market.MarketETFComponents", q, func() (market.MeridianResponse, error) { return m.base.MarketETFComponents(ctx, q) })
}

type capturedResearchStore struct {
	base    performance.Store
	capture *researchInputCapture
}

var _ performance.Store = (*capturedResearchStore)(nil)

func (s *capturedResearchStore) ListOrders(ctx context.Context, q trading.OrderQuery) ([]trading.Order, error) {
	return captureCall(s.capture, "store.ListOrders", q, func() ([]trading.Order, error) { return s.base.ListOrders(ctx, q) })
}
func (s *capturedResearchStore) ListFills(ctx context.Context, q trading.FillQuery) ([]trading.Fill, error) {
	return captureCall(s.capture, "store.ListFills", q, func() ([]trading.Fill, error) { return s.base.ListFills(ctx, q) })
}
func (s *capturedResearchStore) ListPositionSnapshots(ctx context.Context, q trading.PositionQuery) ([]trading.Position, error) {
	return captureCall(s.capture, "store.ListPositionSnapshots", q, func() ([]trading.Position, error) { return s.base.ListPositionSnapshots(ctx, q) })
}
func (s *capturedResearchStore) ListOrderFeeRecords(ctx context.Context, q ledger.OrderFeeRecordQuery) ([]ledger.OrderFeeRecord, error) {
	return captureCall(s.capture, "store.ListOrderFeeRecords", q, func() ([]ledger.OrderFeeRecord, error) { return s.base.ListOrderFeeRecords(ctx, q) })
}
func (s *capturedResearchStore) ListPositionCostStates(ctx context.Context, q ledger.PositionCostStateQuery) ([]ledger.PositionCostState, error) {
	return captureCall(s.capture, "store.ListPositionCostStates", q, func() ([]ledger.PositionCostState, error) { return s.base.ListPositionCostStates(ctx, q) })
}
func (s *capturedResearchStore) GetPerformanceInception(ctx context.Context, a string) (ledger.PerformanceInception, error) {
	return captureCall(s.capture, "store.GetPerformanceInception", a, func() (ledger.PerformanceInception, error) { return s.base.GetPerformanceInception(ctx, a) })
}
func (s *capturedResearchStore) GetDailyPerformance(ctx context.Context, a, d string) (ledger.DailyPerformance, error) {
	return captureCall(s.capture, "store.GetDailyPerformance", []string{a, d}, func() (ledger.DailyPerformance, error) { return s.base.GetDailyPerformance(ctx, a, d) })
}
func (s *capturedResearchStore) GetAssetPositionObservation(ctx context.Context, a, d, k string) (ledger.AssetPositionObservation, error) {
	return captureCall(s.capture, "store.GetAssetPositionObservation", []string{a, d, k}, func() (ledger.AssetPositionObservation, error) {
		return s.base.GetAssetPositionObservation(ctx, a, d, k)
	})
}
func (s *capturedResearchStore) ListETFSettlementFinalizations(ctx context.Context, a, d string) ([]ledger.ETFSettlementFinalization, error) {
	return captureCall(s.capture, "store.ListETFSettlementFinalizations", []string{a, d}, func() ([]ledger.ETFSettlementFinalization, error) {
		return s.base.ListETFSettlementFinalizations(ctx, a, d)
	})
}
func (s *capturedResearchStore) ListFeeRules(ctx context.Context, q ledger.FeeRuleQuery) ([]ledger.FeeRule, error) {
	return captureCall(s.capture, "store.ListFeeRules", q, func() ([]ledger.FeeRule, error) { return s.base.ListFeeRules(ctx, q) })
}
func (s *capturedResearchStore) EffectiveRepoFeeRule(ctx context.Context, a, d string) (ledger.FeeRule, error) {
	return captureCall(s.capture, "store.EffectiveRepoFeeRule", []string{a, d}, func() (ledger.FeeRule, error) { return s.base.EffectiveRepoFeeRule(ctx, a, d) })
}
func (s *capturedResearchStore) ListCashLedgerEntries(ctx context.Context, q ledger.CashLedgerQuery) ([]ledger.CashLedgerEntry, error) {
	return captureCall(s.capture, "store.ListCashLedgerEntries", q, func() ([]ledger.CashLedgerEntry, error) { return s.base.ListCashLedgerEntries(ctx, q) })
}
func (s *capturedResearchStore) ListNavBaselines(ctx context.Context, a string) ([]ledger.NavBaseline, error) {
	return captureCall(s.capture, "store.ListNavBaselines", a, func() ([]ledger.NavBaseline, error) { return s.base.ListNavBaselines(ctx, a) })
}
func (s *capturedResearchStore) ListReverseRepoAccruals(ctx context.Context, a, d string) ([]ledger.ReverseRepoAccrual, error) {
	return captureCall(s.capture, "store.ListReverseRepoAccruals", []string{a, d}, func() ([]ledger.ReverseRepoAccrual, error) { return s.base.ListReverseRepoAccruals(ctx, a, d) })
}
func (s *capturedResearchStore) ListPerformanceNAVs(ctx context.Context, a, f, t string) ([]ledger.PerformanceNAV, error) {
	return captureCall(s.capture, "store.ListPerformanceNAVs", []string{a, f, t}, func() ([]ledger.PerformanceNAV, error) { return s.base.ListPerformanceNAVs(ctx, a, f, t) })
}
func (s *capturedResearchStore) ListNAVReconciliations(ctx context.Context, a, f, t string) ([]ledger.NAVReconciliation, error) {
	return captureCall(s.capture, "store.ListNAVReconciliations", []string{a, f, t}, func() ([]ledger.NAVReconciliation, error) { return s.base.ListNAVReconciliations(ctx, a, f, t) })
}
func (s *capturedResearchStore) ListT0AllocationHistory(ctx context.Context, a, d string) ([]ledger.T0Allocation, error) {
	return captureCall(s.capture, "store.ListT0AllocationHistory", []string{a, d}, func() ([]ledger.T0Allocation, error) {
		base, ok := s.base.(interface {
			ListT0AllocationHistory(context.Context, string, string) ([]ledger.T0Allocation, error)
		})
		if !ok {
			return nil, errors.New("attribution reader unavailable")
		}
		return base.ListT0AllocationHistory(ctx, a, d)
	})
}
func (s *capturedResearchStore) ListComponentTransfers(ctx context.Context, q trading.ComponentTransferQuery) ([]trading.ComponentTransfer, error) {
	return captureCall(s.capture, "store.ListComponentTransfers", q, func() ([]trading.ComponentTransfer, error) {
		base, ok := s.base.(interface {
			ListComponentTransfers(context.Context, trading.ComponentTransferQuery) ([]trading.ComponentTransfer, error)
		})
		if !ok {
			return nil, errors.New("transfer reader unavailable")
		}
		return base.ListComponentTransfers(ctx, q)
	})
}

var errResearchWrite = errors.New("research input capture rejects financial writes")

func (s *capturedResearchStore) CreateFeeRule(context.Context, ledger.FeeRule) (ledger.FeeRule, error) {
	return ledger.FeeRule{}, errResearchWrite
}
func (s *capturedResearchStore) CreateCashLedgerEntry(context.Context, ledger.CashLedgerEntry) (ledger.CashLedgerEntry, error) {
	return ledger.CashLedgerEntry{}, errResearchWrite
}
func (s *capturedResearchStore) ConfirmCashLedgerEntry(context.Context, string, string, string, time.Time) (ledger.CashLedgerEntry, error) {
	return ledger.CashLedgerEntry{}, errResearchWrite
}
func (s *capturedResearchStore) VoidCashLedgerEntry(context.Context, string, string, string, time.Time) (ledger.CashLedgerEntry, error) {
	return ledger.CashLedgerEntry{}, errResearchWrite
}
func (s *capturedResearchStore) CreateNavBaseline(context.Context, ledger.NavBaseline) (ledger.NavBaseline, error) {
	return ledger.NavBaseline{}, errResearchWrite
}
func (s *capturedResearchStore) UpsertReverseRepoAccrual(context.Context, ledger.ReverseRepoAccrual) error {
	return errResearchWrite
}
func (s *capturedResearchStore) UpsertPerformanceNAV(context.Context, ledger.PerformanceNAV) (ledger.PerformanceNAV, error) {
	return ledger.PerformanceNAV{}, errResearchWrite
}
func (s *capturedResearchStore) UpdatePerformanceNAVStatus(context.Context, ledger.PerformanceNAV) (ledger.PerformanceNAV, error) {
	return ledger.PerformanceNAV{}, errResearchWrite
}
func (s *capturedResearchStore) UpsertNAVReconciliation(context.Context, ledger.NAVReconciliation) (ledger.NAVReconciliation, error) {
	return ledger.NAVReconciliation{}, errResearchWrite
}
func (s *capturedResearchStore) UpsertPerformanceInception(context.Context, ledger.PerformanceInception) (ledger.PerformanceInception, error) {
	return ledger.PerformanceInception{}, errResearchWrite
}
func (s *capturedResearchStore) UpsertPositionCostState(context.Context, ledger.PositionCostState) (ledger.PositionCostState, error) {
	return ledger.PositionCostState{}, errResearchWrite
}

func compareCapturedCosts(v ledger.ResearchCostVersion, b performance.ResearchCostBundle, costs []performance.CostLedgerResult) (map[string]any, error) {
	if len(costs) != len(b.Dates) {
		return nil, errors.New("incomplete captured cost chain")
	}
	var positions []ledger.PositionCostState
	for i, cost := range costs {
		if !fuying13CostCarryEligible(cost, b.Dates[i]) {
			return nil, fmt.Errorf("ineligible replay date %s", b.Dates[i])
		}
		positions = append(positions, cost.Positions...)
	}
	r, err := performance.CompareResearchCostVersion(v, b.Opening, b.Audits, positions)
	if err != nil {
		return nil, err
	}
	var differences []map[string]any
	for _, day := range r.Days {
		for _, row := range day.Rows {
			if row.Outcome == "equal" || row.Outcome == "metadata_only" {
				continue
			}
			differences = append(differences, map[string]any{"trade_date": day.TradeDate, "security_id": row.SecurityID, "cost_bucket": row.CostBucket, "baseline": row.Research, "replay": row.Stored, "changed_fields": row.ChangedFields, "baseline_minus_replay": row.Delta, "delta_unavailable_reason": row.DeltaUnavailable})
		}
	}
	return map[string]any{"baseline_version_id": v.VersionID, "baseline_sha256": v.PayloadSHA256, "days": len(costs), "rows": len(positions), "business_differences": len(differences), "metadata_only": r.Counts.MetadataOnly, "equal": r.Counts.Equal, "differences": differences, "comparison_scope": "archived research vs freshly calculated memory costs; neither side is formal ledger", "cost_publishable": false, "economic_nav_publishable": false}, nil
}
