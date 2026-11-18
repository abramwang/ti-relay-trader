package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ti-relay-trader/internal/futures"
)

func preparationArgs(t *testing.T) []string {
	t.Helper()
	doc, err := os.ReadFile("../../docs/OC_CTP_SINGLE_ORDER_EXECUTION_20261117.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllSubmatch(doc, -1)
	if len(blocks) != 2 {
		t.Fatal("missing wire examples")
	}
	raw := blocks[0][1]
	hash := sha256.Sum256(raw)
	target := futures.TestQueryTarget{SourceID: "synthetic-source", ServerRunID: strings.Repeat("a", 40), Epoch: "synthetic-epoch", Group: "synthetic-group", CounterSessionID: "synthetic-session", TradeDate: "20261117", ConnectionGeneration: 1}
	binding := futures.CatalogBinding{SecurityID: "RB2701.SHFE", SourceInstrumentID: "rb2701", ExchangeID: "SHFE", PublicationSnapshotID: "synthetic-version", LogicalTableChecksum: "synthetic-checksum"}
	evidence := futures.RegistrationEvidence{CurrentTradeDate: "20261117", Mappings: map[string]futures.CatalogBinding{"review-only-order-20261117": binding}}
	approval := futures.TestTradeApproval{AssessmentID: "synthetic-assessment", CommandSHA256: hex.EncodeToString(hash[:]), ExpiresAt: "2026-11-17T11:07:49+08:00", Market: &futures.TestTradeMarket{SecurityID: binding.SecurityID, SourceInstrumentID: binding.SourceInstrumentID, ExchangeID: binding.ExchangeID, PublicationSnapshotID: binding.PublicationSnapshotID, LogicalTableChecksum: binding.LogicalTableChecksum, TradeDate: "20261117", ObservedAt: "2026-11-17T11:06:48.500+08:00", PriceTick: "1", BestBid: "2974", LowerLimit: "2870", UpperLimit: "3173"}}
	files := map[string]any{"command": json.RawMessage(raw), "target": target, "evidence": evidence, "approval": approval}
	dir := t.TempDir()
	args := []string{"--env", "test", "--broker", "ctp_4900", "--gateway", "ctp-dongwu-asset-retest-20261117", "--account", "300052141", "--native-broker", "4900", "--user", "300052141", "--investor", "300052141", "--database", "relay_assessment_test"}
	for name, value := range files {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		// Preserve the exact approved command bytes, including original key order.
		if name == "command" {
			b = raw
		}
		p := filepath.Join(dir, name+".json")
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--"+name+"-file", p)
	}
	return args
}

func TestTradeCLIIsOfflineByDefault(t *testing.T) {
	var out, diagnostics bytes.Buffer
	err := run(context.Background(), preparationArgs(t), func(string) string { t.Fatal("offline check accessed credentials"); return "" }, &out, &diagnostics)
	if err != nil {
		t.Fatal(err, diagnostics.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["mode"] != "check_only_no_connections" || result["command_published"] != false || result["broker_accepted"] != false {
		t.Fatal(result, err)
	}
}

func TestTradeCLIRejectsProductionAndUnpairedPermissions(t *testing.T) {
	for _, extra := range [][]string{{"--env", "prod"}, {"--register"}, {"--publish-trade"}, {"--allow-test-trade-write"}, {"--register", "--persist-test-intent", "--publish-trade", "--allow-test-trade-write"}} {
		var out, diagnostics bytes.Buffer
		args := append(preparationArgs(t), extra...)
		if err := run(context.Background(), args, func(string) string { t.Fatal("invalid invocation accessed credentials"); return "" }, &out, &diagnostics); err == nil {
			t.Fatal("unsafe invocation accepted", extra)
		}
	}
}

func TestTradeCLIExecutionPermissionOffline(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		args := preparationArgs(t)
		for i, arg := range args {
			if arg != "--approval-file" {
				continue
			}
			raw, err := os.ReadFile(args[i+1])
			var a futures.TestTradeApproval
			if err != nil || json.Unmarshal(raw, &a) != nil {
				t.Fatal(err)
			}
			a.PermissionProfile = "relay_test_execution.v1"
			a.ExecutionPermission = &futures.TestExecutionPermission{Submit: enabled, Cancel: true}
			raw, err = json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(args[i+1], raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		var out, diagnostics bytes.Buffer
		err := run(context.Background(), args, func(string) string { t.Fatal("offline check accessed credentials"); return "" }, &out, &diagnostics)
		if !enabled {
			if err == nil {
				t.Fatal("closed submit permission accepted")
			}
			continue
		}
		var result map[string]any
		if err != nil || json.Unmarshal(out.Bytes(), &result) != nil || result["permission_profile"] != "relay_test_execution.v1" || result["business_authorized"] != false || result["command_published"] != false {
			t.Fatal(result, err)
		}
	}
}
