package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestFuyingResearchCaptureRecordsImmutableInputs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	c, err := newResearchInputCapture(dir)
	if err != nil {
		t.Fatal(err)
	}
	q := map[string]string{"security_id": "159915.SZ"}
	response := map[string]any{"price": 0.000001, "nil": nil}
	_, err = captureCall(c, "market.test", q, func() (map[string]any, error) { q["security_id"] = "changed"; return response, nil })
	if err != nil {
		t.Fatal(err)
	}
	response["price"] = 999
	if _, err := captureCall(c, "source.error", nil, func() (any, error) { return nil, errors.New("explicit source failure") }); err == nil {
		t.Fatal("source error swallowed")
	}
	c.complete = true
	if err := c.close(nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "inputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(data))
	if !scanner.Scan() {
		t.Fatal("missing record")
	}
	if err := json.Unmarshal(scanner.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	var request map[string]string
	json.Unmarshal(first["request"], &request)
	var result map[string]any
	json.Unmarshal(first["response"], &result)
	if request["security_id"] != "159915.SZ" || result["price"] != 0.000001 || result["nil"] != nil {
		t.Fatal(request, result)
	}
	var manifest struct {
		Complete bool   `json:"complete"`
		Calls    int    `json:"calls"`
		SHA      string `json:"sha256"`
		Bytes    int    `json:"bytes"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	if !manifest.Complete || manifest.Calls != 2 || manifest.Bytes != len(data) || manifest.SHA != hex.EncodeToString(h[:]) {
		t.Fatal(manifest)
	}
	for _, name := range []string{"inputs.jsonl", "manifest.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private capture", err)
		}
	}
	if _, err := newResearchInputCapture(dir); err == nil {
		t.Fatal("overwrote directory")
	}
	if err := c.record("closed", nil, nil, nil); err == nil {
		t.Fatal("write after close")
	}
}

func TestFuyingResearchCaptureConcurrentAndFailure(t *testing.T) {
	c, err := newResearchInputCapture(filepath.Join(t.TempDir(), "capture"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.record("parallel", nil, 1, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	c.bytes = 256 << 20
	if err := c.record("overflow", nil, 1, nil); err == nil {
		t.Fatal("budget bypass")
	}
	c.complete = true
	if err := c.close(nil); err == nil {
		t.Fatal("capture failure lost")
	}
	raw, err := os.ReadFile(filepath.Join(c.directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	if m["complete"] != false || m["calls"] != float64(50) {
		t.Fatal(m)
	}
	store := &capturedResearchStore{}
	if _, err := store.UpsertPositionCostState(context.Background(), ledger.PositionCostState{}); !errors.Is(err, errResearchWrite) {
		t.Fatal("write escaped guard")
	}
	if _, err := store.UpsertPerformanceNAV(context.Background(), ledger.PerformanceNAV{}); !errors.Is(err, errResearchWrite) {
		t.Fatal("NAV escaped guard")
	}
}

func TestFuyingResearchCaptureArgumentGuard(t *testing.T) {
	if err := runFuying13Review20261110([]string{"-capture-inputs", filepath.Join(t.TempDir(), "capture"), "-output", "unused", "-config", "must-not-read"}); err == nil {
		t.Fatal("incomplete stages accepted")
	}
}

func TestFuyingResearchCaptureFrozenComparison(t *testing.T) {
	if os.Getenv("RELAY_RESEARCH_COST_FROZEN_REPLAY") != "1" {
		t.Skip("requires private frozen evidence")
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	b, v, err := loadFuyingResearchBundle()
	if err != nil {
		t.Fatal(err)
	}
	diff, err := compareCapturedCosts(v, b, b.Costs)
	if err != nil || diff["business_differences"] != 0 || diff["rows"] != 972 || diff["economic_nav_publishable"] != false {
		t.Fatal(diff, err)
	}
	if _, err := compareCapturedCosts(v, b, b.Costs[:1]); err == nil {
		t.Fatal("partial replay compared as complete")
	}
	costs := append([]performance.CostLedgerResult(nil), b.Costs...)
	costs[0].Positions = append([]ledger.PositionCostState(nil), costs[0].Positions...)
	costs[0].Positions[0].CloseTotalCost += 0.000001
	diff, err = compareCapturedCosts(v, b, costs)
	if err != nil || diff["business_differences"] != 1 {
		t.Fatal("micro difference lost", err)
	}
}
