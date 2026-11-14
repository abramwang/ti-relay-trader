package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type researchInputRecord struct {
	Sequence int             `json:"sequence"`
	Method   string          `json:"method"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
	Error    string          `json:"error"`
}

type researchInputReplay struct {
	mu          sync.Mutex
	records     []researchInputRecord
	next        int
	failure     error
	costs       map[string][]ledger.PositionCostState
	carryChecks int
}

func sameResearchJSON(a, b []byte) bool {
	decode := func(raw []byte) (any, error) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		err := d.Decode(&v)
		if err == nil && d.Decode(new(any)) != io.EOF {
			return nil, errors.New("trailing JSON data")
		}
		return v, err
	}
	x, e1 := decode(a)
	y, e2 := decode(b)
	return e1 == nil && e2 == nil && reflect.DeepEqual(x, y)
}

func sameResearchRequest(method string, a, b []byte) bool {
	switch method {
	case "market.MetadataInstruments", "market.MarketBars", "market.MetadataAdjustFactors", "market.MarketETFCashComponents":
		// These endpoints treat the comma-separated securities as a set. Preserve
		// multiplicity, pagination, dates and every other parameter verbatim.
		normalize := func(raw []byte) []byte {
			var q url.Values
			if json.Unmarshal(raw, &q) != nil {
				return raw
			}
			for i, value := range q["security_ids"] {
				ids := strings.Split(value, ",")
				sort.Strings(ids)
				q["security_ids"][i] = strings.Join(ids, ",")
			}
			out, err := json.Marshal(q)
			if err != nil {
				return raw
			}
			return out
		}
		return sameResearchJSON(normalize(a), normalize(b))
	default:
		return sameResearchJSON(a, b)
	}
}

func (r *researchInputReplay) fail(err error) error {
	if r.failure == nil {
		r.failure = err
	}
	return r.failure
}

func (r *researchInputReplay) read(method string, request, target any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil {
		return r.failure
	}
	if r.next >= len(r.records) {
		return r.fail(fmt.Errorf("input exhausted at %s", method))
	}
	entry := r.records[r.next]
	want, err := json.Marshal(request)
	if err != nil {
		return r.fail(err)
	}
	// T0 groups request snapshots concurrently. Only this contiguous batch may
	// arrive in a different order; exact parameters and occurrence counts remain
	// mandatory. Never search across a store/calendar boundary for a response.
	if method == "market.MarketSnapshots" && entry.Method == method && !sameResearchJSON(entry.Request, want) {
		for i := r.next + 1; i < len(r.records) && r.records[i].Method == method; i++ {
			if sameResearchJSON(r.records[i].Request, want) {
				entry = r.records[i]
				copy(r.records[r.next+1:i+1], r.records[r.next:i])
				r.records[r.next] = entry
				break
			}
		}
	}
	if entry.Method != method || !sameResearchRequest(method, entry.Request, want) {
		return r.fail(fmt.Errorf("input %d request mismatch: got %s %s; recorded %s %s", entry.Sequence, method, want, entry.Method, entry.Request))
	}
	if err := json.Unmarshal(entry.Response, target); err != nil {
		return r.fail(err)
	}
	r.next++
	if entry.Error != "" {
		if method == "store.EffectiveRepoFeeRule" && entry.Error == sql.ErrNoRows.Error() {
			return sql.ErrNoRows
		}
		return r.fail(fmt.Errorf("unsupported recorded error at %d: %s", entry.Sequence, entry.Error))
	}
	// The next day consumes newly calculated memory costs, not a recorded answer.
	if method == "store.ListPositionCostStates" {
		q, ok := request.(ledger.PositionCostStateQuery)
		out, okOut := target.(*[]ledger.PositionCostState)
		if !ok || !okOut {
			return r.fail(errors.New("cost replay type mismatch"))
		}
		fresh := previewCostStates(*out, r.costs, q)
		a, e := json.Marshal(fresh)
		if e != nil || !sameResearchJSON(a, entry.Response) {
			return r.fail(fmt.Errorf("memory cost carry differs at input %d", entry.Sequence))
		}
		for _, row := range fresh {
			if _, exists := r.costs[row.TradeDate]; exists {
				r.carryChecks++
				break
			}
		}
		*out = fresh
	}
	return nil
}

func readResearchPrivateFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errors.New("research input file exceeds budget")
	}
	return b, err
}

func loadResearchInputReplay(directory, expectedSHA string) (*researchInputReplay, error) {
	if len(expectedSHA) != 64 {
		return nil, errors.New("explicit sealed input SHA256 required")
	}
	mraw, err := readResearchPrivateFile(filepath.Join(directory, "manifest.json"), 1<<20)
	if err != nil {
		return nil, err
	}
	var m struct {
		Schema      string         `json:"schema"`
		Account     string         `json:"account_id"`
		Version     string         `json:"baseline_version_id"`
		BaselineSHA string         `json:"baseline_payload_sha256"`
		Complete    bool           `json:"complete"`
		Calls       int            `json:"calls"`
		Bytes       int            `json:"bytes"`
		SHA         string         `json:"sha256"`
		Methods     map[string]int `json:"methods"`
		RunError    string         `json:"run_error"`
	}
	if err := json.Unmarshal(mraw, &m); err != nil {
		return nil, err
	}
	if m.Schema != "relay.research_engine_inputs.v1" || m.Account != fuying13Account || m.Version != fuyingResearchVersion || m.BaselineSHA != fuyingResearchPayloadHash || !m.Complete || m.RunError != "" || m.SHA != expectedSHA || m.Calls < 7 || m.Calls > 10000 {
		return nil, errors.New("incomplete or incompatible research manifest")
	}
	raw, err := readResearchPrivateFile(filepath.Join(directory, "inputs.jsonl"), 256<<20)
	if err != nil {
		return nil, err
	}
	if len(raw) != m.Bytes || fmt.Sprintf("%x", sha256.Sum256(raw)) != expectedSHA {
		return nil, errors.New("sealed input hash/size mismatch")
	}
	r := &researchInputReplay{costs: map[string][]ledger.PositionCostState{}}
	counts := map[string]int{}
	s := bufio.NewScanner(bytes.NewReader(raw))
	s.Buffer(make([]byte, 64<<10), (16<<20)+1)
	for s.Scan() {
		var rec researchInputRecord
		if err := json.Unmarshal(s.Bytes(), &rec); err != nil {
			return nil, err
		}
		if rec.Sequence != len(r.records)+1 || !json.Valid(rec.Request) || !json.Valid(rec.Response) {
			return nil, errors.New("invalid input sequence or payload")
		}
		r.records = append(r.records, rec)
		counts[rec.Method]++
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(r.records) != m.Calls || !reflect.DeepEqual(counts, m.Methods) {
		return nil, errors.New("input count mismatch")
	}
	return r, nil
}

func runResearchInputReplay(args []string) error {
	f := flag.NewFlagSet("performance-research-input-replay", flag.ContinueOnError)
	directory := f.String("capture", "", "sealed private input directory")
	sha := f.String("sha256", "", "independently recorded inputs.jsonl fingerprint")
	output := f.String("output", "", "new private result file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *directory == "" || *output == "" || f.NArg() != 0 {
		return errors.New("capture, sha256 and new output required")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	r, err := loadResearchInputReplay(*directory, *sha)
	if err != nil {
		return err
	}
	result, err := replayResearchCostEngine(r)
	if err != nil {
		return err
	}
	result["inputs_sha256"] = *sha
	if err := writeETFTransferReviewFile(*output, result); err != nil {
		return err
	}
	return writeJSON(result)
}

func replayResearchCostEngine(r *researchInputReplay) (map[string]any, error) {
	var scope struct {
		Baseline    performance.ResearchCostBundle `json:"baseline"`
		Config      config.PerformanceConfig       `json:"performance_config"`
		Now         time.Time                      `json:"fixed_calculation_time"`
		Component   bool                           `json:"component_residual_preview"`
		Pool        string                         `json:"closed_day_pool"`
		Overnight   bool                           `json:"overnight_chain"`
		BrokerCash  bool                           `json:"broker_cash_overlay"`
		Attribution string                         `json:"attribution_source"`
	}
	if err := r.read("scope", nil, &scope); err != nil {
		return nil, err
	}
	if scope.Now.IsZero() || !scope.Component || scope.Pool != "2026-10-16" || !scope.Overnight || !scope.BrokerCash || scope.Attribution != "persisted_audits_not_fixture_overlay" || len(scope.Baseline.Dates) != 22 {
		return nil, errors.New("unsupported replay scope")
	}
	var hashes map[string]string
	if err := r.read("code_fingerprints", nil, &hashes); err != nil {
		return nil, err
	}
	verified := 0
	for path, want := range hashes {
		if !strings.HasPrefix(path, "internal/") && path != "go.mod" && path != "go.sum" {
			continue
		}
		clean := filepath.Clean(path)
		if clean != path || filepath.IsAbs(path) {
			return nil, errors.New("invalid source path")
		}
		raw, err := readResearchPrivateFile(path, 16<<20)
		if err != nil {
			return nil, err
		}
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != want {
			return nil, fmt.Errorf("engine source differs: %s", path)
		}
		verified++
	}
	if verified == 0 {
		return nil, errors.New("missing engine fingerprints")
	}
	var observation json.RawMessage
	if err := r.read("source_observation", nil, &observation); err != nil {
		return nil, err
	}
	// These are verified helper artifacts, not independently rerun broker/SQL checks.
	var ignored json.RawMessage
	if err := r.read("market.MarketETFComponents", url.Values{"limit": {"1000"}, "security_id": {"159915.SZ"}, "trade_date": {"20261104"}}, &ignored); err != nil {
		return nil, err
	}
	if err := r.read("market.MarketETFCashComponents", url.Values{"limit": {"5"}, "security_id": {"159915.SZ"}, "trade_date": {"20261105"}}, &ignored); err != nil {
		return nil, err
	}
	if err := r.read("verified_helper_results", nil, &ignored); err != nil {
		return nil, err
	}
	if r.records[len(r.records)-1].Method != "cost_chain_output" {
		return nil, errors.New("missing sealed output")
	}
	var expected struct {
		Complete      bool                             `json:"cost_chain_complete"`
		Costs         []performance.CostLedgerResult   `json:"costs"`
		Contributions []performance.ContributionResult `json:"contributions"`
		Overnight     fuyingOvernightEvidence          `json:"overnight_diagnostic"`
	}
	if err := json.Unmarshal(r.records[len(r.records)-1].Response, &expected); err != nil {
		return nil, err
	}
	if !expected.Complete || len(expected.Costs) != 22 || len(expected.Contributions) != 22 {
		return nil, errors.New("incomplete recorded engine output")
	}
	capture := &researchInputCapture{replay: r}
	store := &capturedResearchStore{capture: capture}
	m := &capturedResearchMarket{capture: capture}
	cfg := scope.Config
	service, err := performance.New(performance.Options{Store: store, Calendar: m, Market: m, Now: func() time.Time { return scope.Now }, FormulaVersion: cfg.FormulaVersion,
		ETFT0FrictionRate: cfg.ETFT0FrictionRate, AutoToleranceCNY: cfg.AutoToleranceCNY, AutoToleranceBP: cfg.AutoToleranceBP, WarningToleranceCNY: cfg.WarningToleranceCNY, WarningToleranceBP: cfg.WarningToleranceBP,
		ComponentResidualPreview: true, ClosedDayT0PoolPreviewDate: scope.Pool,
		OvernightT0Allocations: []performance.OvernightT0Allocation{{AccountID: fuying13Account, SourceDate: "2026-11-03", RedemptionDate: "2026-11-04", SecurityID: "159915.SZ", GroupID: fuyingOvernightGroup, SourceBuyOrderID: fuyingOvernightBuy, RedemptionOrderID: fuyingOvernightRedemption, Quantity: 1000000, EvidenceRef: "testdata/fixtures/fuying13_overnight_review_20261111.json"}}})
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if _, err := m.MarketETFCashComponents(ctx, url.Values{"security_id": {"510310.SH"}, "trade_date": {"20261013"}}); err != nil {
		return nil, err
	}
	start, _ := time.ParseInLocation("2006-01-02", "2026-10-12", timeutil.Location())
	end, _ := time.ParseInLocation("2006-01-02", "2026-11-10", timeutil.Location())
	days, rows := 0, 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		calendar, err := m.TradingDayStatus(ctx, d.Format("20060102"))
		if err != nil {
			return nil, err
		}
		if !calendar.IsTradingDayKnown {
			return nil, errors.New("unknown replay calendar")
		}
		if !calendar.IsTradingDay {
			continue
		}
		if day == "2026-11-04" {
			e := expected.Overnight
			if err := validateFuyingOvernightEvidence(e); err != nil {
				return nil, err
			}
			var source ledger.PositionCostState
			for _, p := range r.costs["2026-11-03"] {
				if p.CostBucket == e.Source.CostBucket {
					source = p
				}
			}
			if err := equalResearchOutput("overnight source", source, e.Source); err != nil {
				return nil, err
			}
			var redemption trading.Order
			for _, o := range e.Orders {
				if o.GatewayOrderID == fuyingOvernightRedemption {
					redemption = o
				}
			}
			preview, err := service.PreviewOvernightT0(ctx, source, redemption, e.Transfers[0])
			if err != nil {
				return nil, err
			}
			if err := equalResearchOutput("overnight preview", preview, e.Preview); err != nil {
				return nil, err
			}
		}
		cost, err := service.CalculateCostLedger(ctx, fuying13Account, day, performance.CostLedgerOptions{})
		if err != nil {
			return nil, err
		}
		if !fuying13CostCarryEligible(cost, day) || days >= len(expected.Costs) {
			return nil, fmt.Errorf("replay cost not eligible %s", day)
		}
		if err := equalResearchOutput("cost "+day, cost, expected.Costs[days]); err != nil {
			return nil, err
		}
		contribution, err := service.CalculateContributions(ctx, fuying13Account, day)
		if err != nil {
			return nil, err
		}
		if err := equalResearchOutput("contribution "+day, contribution, expected.Contributions[days]); err != nil {
			return nil, err
		}
		r.costs[day] = append([]ledger.PositionCostState(nil), cost.Positions...)
		days++
		rows += len(cost.Positions)
	}
	if err := r.read("cost_chain_output", nil, &ignored); err != nil {
		return nil, err
	}
	if r.failure != nil {
		return nil, r.failure
	}
	if r.next != len(r.records) || days != 22 || r.carryChecks == 0 {
		return nil, errors.New("unconsumed inputs or incomplete memory chain")
	}
	return map[string]any{"schema": "relay.research_engine_replay.v1", "account_id": fuying13Account, "days": days, "rows": rows, "records_consumed": r.next, "memory_carry_checks": r.carryChecks, "engine_source_files_verified": verified, "exact_engine_outputs_match": true, "network_calls": 0, "database_reads": 0, "database_writes": 0, "broker_commands": 0, "cost_publishable": false, "economic_nav_publishable": false, "scope": "cost and contribution engine replay; helper evidence reused, historical query completeness and asset scope not certified"}, nil
}

func equalResearchOutput(label string, got, want any) error {
	a, e1 := json.Marshal(got)
	b, e2 := json.Marshal(want)
	if e1 != nil || e2 != nil {
		return errors.Join(e1, e2)
	}
	if !sameResearchJSON(a, b) {
		return fmt.Errorf("%s output differs", label)
	}
	return nil
}
