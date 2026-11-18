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
	"time"

	"ti-relay-trader/internal/futures"
)

func TestMeridianPriceCLIIsOffline(t *testing.T) {
	args := preparationArgs(t)
	var clean []string
	var targetPath string
	for i := 0; i < len(args); i += 2 {
		if args[i] == "--target-file" {
			targetPath = args[i+1]
		}
		if args[i] != "--command-file" && args[i] != "--approval-file" && args[i] != "--evidence-file" {
			clean = append(clean, args[i:i+2]...)
		}
	}
	if targetPath == "" {
		t.Fatal("target missing")
	}
	raw, err := os.ReadFile("../../testdata/fixtures/meridian_futures_price.synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	row := payload["data"].([]any)[0].(map[string]any)
	row["exchange_timestamp"] = now.Add(-100 * time.Millisecond).Format(time.RFC3339Nano)
	row["received_at"] = now.Format(time.RFC3339Nano)
	raw, _ = json.Marshal(payload)
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snapshot.json")
	mapping := filepath.Join(dir, "mapping.json")
	if err := os.WriteFile(snapshot, raw, 0600); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(futures.CatalogBinding{SecurityID: "RB2701.SHFE", SourceInstrumentID: "rb2701", ExchangeID: "SHFE", PublicationSnapshotID: "synthetic-version", LogicalTableChecksum: "synthetic-checksum"})
	if err := os.WriteFile(mapping, b, 0600); err != nil {
		t.Fatal(err)
	}
	clean = append(clean, "--prepare-meridian-price", "--meridian-snapshot-file", snapshot, "--meridian-mapping-file", mapping, "--meridian-price-tick", "1")
	for _, extra := range [][]string{nil, {"--prepare-native-price"}, {"--register", "--persist-test-intent"}, {"--env", "production"}, {"--native-query-id", "1"}} {
		var out, diagnostics bytes.Buffer
		err := run(context.Background(), append(append([]string{}, clean...), extra...), func(string) string { t.Fatal("Meridian preparation accessed credentials"); return "" }, &out, &diagnostics)
		if len(extra) > 0 {
			if err == nil {
				t.Fatal("mixed/production invocation accepted", extra)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if json.Unmarshal(out.Bytes(), &result) != nil || result["mode"] != "check_meridian_price_no_connections" || result["business_authorized"] != false || result["command_published"] != false {
			t.Fatal(out.String())
		}
	}
}

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

func TestNativePriceCLIExplicitReadOnly(t *testing.T) {
	for _, extra := range [][]string{
		{"--native-query-id", "1"},
		{"--prepare-native-price"},
		{"--prepare-native-price", "--native-query-id", "1", "--native-receipt-id", "1", "--native-mapping-file", "/not-read"},
	} {
		args := append(preparationArgs(t), extra...)
		var out, diagnostics bytes.Buffer
		if err := run(context.Background(), args, func(string) string { t.Fatal("unsafe price mode accessed credentials"); return "" }, &out, &diagnostics); err == nil {
			t.Fatal("mixed modes accepted")
		}
	}
}
