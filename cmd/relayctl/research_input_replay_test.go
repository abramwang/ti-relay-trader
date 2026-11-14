package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ti-relay-trader/internal/ledger"
)

func replayTestRecord(n int, method string, request, response any, err string) researchInputRecord {
	q, _ := json.Marshal(request)
	v, _ := json.Marshal(response)
	return researchInputRecord{Sequence: n, Method: method, Request: q, Response: v, Error: err}
}

func TestResearchReplayStrictRequests(t *testing.T) {
	r := &researchInputReplay{records: []researchInputRecord{replayTestRecord(1, "store.test", 1, "ok", "")}}
	var v string
	if err := r.read("store.test", 2, &v); err == nil {
		t.Fatal("wrong request accepted")
	}
	if err := r.read("store.test", 1, &v); err == nil {
		t.Fatal("mismatch not sticky")
	}
	r = &researchInputReplay{records: []researchInputRecord{replayTestRecord(1, "store.EffectiveRepoFeeRule", nil, ledger.FeeRule{}, sql.ErrNoRows.Error())}}
	var rule ledger.FeeRule
	if err := r.read("store.EffectiveRepoFeeRule", nil, &rule); !errors.Is(err, sql.ErrNoRows) || r.failure != nil {
		t.Fatal("missing-rule identity lost", err)
	}
	if err := r.read("store.EffectiveRepoFeeRule", nil, &rule); err == nil {
		t.Fatal("exhausted input accepted")
	}
	r = &researchInputReplay{records: []researchInputRecord{replayTestRecord(1, "store.test", nil, nil, "connection lost")}}
	if err := r.read("store.test", nil, &v); err == nil || r.failure == nil {
		t.Fatal("unknown error not fatal")
	}
}

func TestResearchReplayConcurrentSnapshots(t *testing.T) {
	r := &researchInputReplay{}
	for i := 0; i < 30; i++ {
		r.records = append(r.records, replayTestRecord(i+1, "market.MarketSnapshots", i, i, ""))
	}
	var wg sync.WaitGroup
	for i := 29; i >= 0; i-- {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var value int
			if err := r.read("market.MarketSnapshots", i, &value); err != nil || value != i {
				t.Errorf("response mismatch %d: %d %v", i, value, err)
			}
		}(i)
	}
	wg.Wait()
	if r.next != 30 || r.failure != nil {
		t.Fatal("incomplete batch")
	}
	r = &researchInputReplay{records: []researchInputRecord{
		replayTestRecord(1, "market.MarketSnapshots", "A", 1, ""),
		replayTestRecord(2, "market.MarketSnapshots", "A", 2, ""),
		replayTestRecord(3, "market.MarketSnapshots", "B", 3, ""),
	}}
	for i, request := range []string{"B", "A", "A"} {
		var value int
		if err := r.read("market.MarketSnapshots", request, &value); err != nil || value != []int{3, 1, 2}[i] {
			t.Fatal("same-key occurrence order changed", value, err)
		}
	}
	r = &researchInputReplay{records: []researchInputRecord{
		replayTestRecord(1, "market.MarketSnapshots", 1, 1, ""),
		replayTestRecord(2, "store.boundary", nil, nil, ""),
		replayTestRecord(3, "market.MarketSnapshots", 2, 2, ""),
	}}
	var value int
	if r.read("market.MarketSnapshots", 2, &value) == nil {
		t.Fatal("cross-boundary match")
	}
}

func TestResearchReplaySecuritySetAndPrecision(t *testing.T) {
	q := func(ids, date string) []byte {
		b, _ := json.Marshal(url.Values{"security_ids": {ids}, "trade_date": {date}, "limit": {"2"}})
		return b
	}
	if !sameResearchRequest("market.MarketBars", q("B,A", "20261012"), q("A,B", "20261012")) {
		t.Fatal("security ordering")
	}
	for _, b := range [][]byte{q("A,A", "20261012"), q("A,B", "20261013"), q("A", "20261012")} {
		if sameResearchRequest("market.MarketBars", q("A,B", "20261012"), b) {
			t.Fatal("request difference hidden")
		}
	}
	if sameResearchJSON([]byte(`9007199254740992`), []byte(`9007199254740993`)) {
		t.Fatal("integer precision lost")
	}
	if sameResearchJSON([]byte(`1 2`), []byte(`1`)) {
		t.Fatal("trailing JSON ignored")
	}
	if equalResearchOutput("micro", 0.000001, 0.000002) == nil {
		t.Fatal("micro difference ignored")
	}
}

func TestResearchReplayMemoryAndNoFallback(t *testing.T) {
	q := ledger.PositionCostStateQuery{AccountID: fuying13Account, BeforeDate: "2026-10-13"}
	rows := []ledger.PositionCostState{{AccountID: fuying13Account, TradeDate: "2026-10-12", CloseTotalCost: 123.000001}}
	r := &researchInputReplay{records: []researchInputRecord{replayTestRecord(1, "store.ListPositionCostStates", q, rows, "")}, costs: map[string][]ledger.PositionCostState{"2026-10-12": rows}}
	s := &capturedResearchStore{capture: &researchInputCapture{replay: r}}
	got, err := s.ListPositionCostStates(context.Background(), q)
	if err != nil || len(got) != 1 || r.carryChecks != 1 {
		t.Fatal(got, err)
	}
	if _, err := s.UpsertPositionCostState(context.Background(), rows[0]); !errors.Is(err, errResearchWrite) {
		t.Fatal("write escaped")
	}
	r.next = 0
	r.costs["2026-10-12"] = []ledger.PositionCostState{{AccountID: fuying13Account, TradeDate: "2026-10-12", CloseTotalCost: 123.000002}}
	if _, err := s.ListPositionCostStates(context.Background(), q); err == nil {
		t.Fatal("recorded costs masked new difference")
	}
	c := &researchInputCapture{replay: &researchInputReplay{}}
	if _, err := captureCall(c, "missing", nil, func() (int, error) { t.Fatal("live fallback executed"); return 1, nil }); err == nil {
		t.Fatal("missing replay accepted")
	}
}

func TestResearchReplaySealedLoader(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	c, err := newResearchInputCapture(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if err := c.record("test", i, i, nil); err != nil {
			t.Fatal(err)
		}
	}
	c.complete = true
	if err := c.close(nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "inputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sha := fmt.Sprintf("%x", sha256.Sum256(raw))
	if _, err := loadResearchInputReplay(dir, sha); err != nil {
		t.Fatal(err)
	}
	if _, err := loadResearchInputReplay(dir, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong fingerprint")
	}
	if err := os.WriteFile(filepath.Join(dir, "inputs.jsonl"), append(raw, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadResearchInputReplay(dir, sha); err == nil {
		t.Fatal("tampered input accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "inputs.jsonl"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	mraw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(mraw, &m); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"complete", "calls", "account_id"} {
		old := m[field]
		m[field] = map[string]any{"complete": false, "calls": 8, "account_id": "other"}[field]
		b, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadResearchInputReplay(dir, sha); err == nil {
			t.Fatal("invalid manifest", field)
		}
		m[field] = old
	}
}

func TestResearchReplayFrozenEngine(t *testing.T) {
	if os.Getenv("RELAY_RESEARCH_INPUT_REPLAY") != "1" {
		t.Skip("requires private sealed inputs")
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	r, err := loadResearchInputReplay("outputs/recovery/fuying13_engine_capture_20261114/run-1", "18915395b9340868c63f6259d838c025ceeb7ca58bb7d0cf0d914e0920d053f1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := replayResearchCostEngine(r)
	if err != nil {
		t.Fatal(err)
	}
	if result["rows"] != 972 || result["records_consumed"] != 1234 || result["exact_engine_outputs_match"] != true {
		t.Fatal(result)
	}
}
