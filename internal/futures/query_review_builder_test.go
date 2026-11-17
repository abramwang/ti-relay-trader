package futures

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestQueryReviewBuilderOfflineContract(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 required for the offline review builder")
	}
	for _, kind := range []string{"asset", "positions"} {
		t.Run(kind, func(t *testing.T) {
			c := batchContext(kind)
			c.NativeUserID, c.NativeInvestorID = "FIXTURE_USER", "FIXTURE_INV"
			c.Ticket = "1"
			target := publicationTarget()
			target.CounterSessionID, target.TradeDate = c.CounterSessionID, c.TradeDate
			marshal := func(value any) []byte {
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			command := marshal(map[string]any{
				"protocol": "relay.stream.v1", "message_type": "command", "message_id": c.OriginMessageID,
				"request_id": "req", "action": c.Action,
				"payload": map[string]any{"account_id": c.Route.AccountID, "product_type": "futures", "schema_version": "ctp.f0.draft.2"},
			})
			scope := string(marshal([]string{"test", c.Route.BrokerID, c.Route.AccountID, c.Route.GatewayID, c.NativeBrokerID, c.NativeUserID, c.NativeInvestorID}))
			prefix := "relay:test:v1:" + c.Route.BrokerID + ":" + c.Route.GatewayID
			var audit []byte
			previous := ""
			for i, entry := range []map[string]any{
				{"kind": "command_received", "body": string(command), "stream": prefix + ":cmd.query", "group": target.Group},
				{"kind": "query_result", "action": c.Action, "request_id": "req", "origin_message_id": c.OriginMessageID,
					"counter_session_id": c.CounterSessionID, "connection_generation": c.ConnectionGeneration, "trade_date": c.TradeDate,
					"ticket": 1, "sdk_request_id": c.SDKRequestID, "raw_application": "missing_biz_type",
					"raw": map[string]any{"sdk_request_id": c.SDKRequestID, "state": "completed", "last_seen": true, "callback_error": nil}},
			} {
				record := map[string]any{"scope": scope, "entry": entry, "sequence": i + 1, "previous": previous, "counter_session_id": c.CounterSessionID}
				hash := sha256.Sum256(marshal(record))
				previous = hex.EncodeToString(hash[:])
				audit = append(audit, marshal(map[string]any{"record": record, "sha256": previous})...)
				audit = append(audit, '\n')
			}
			dir := t.TempDir()
			for name, body := range map[string][]byte{"audit.jsonl": audit, "command.json": command, "target.json": marshal(target)} {
				if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			hash := sha256.Sum256(audit)
			output := filepath.Join(dir, "draft")
			args := []string{"../../scripts/build-ctp-query-review.py", "--audit-file", filepath.Join(dir, "audit.jsonl"),
				"--audit-sha256", hex.EncodeToString(hash[:]), "--command-file", filepath.Join(dir, "command.json"),
				"--target-file", filepath.Join(dir, "target.json"), "--broker", c.Route.BrokerID, "--gateway", c.Route.GatewayID,
				"--account", c.Route.AccountID, "--native-broker", c.NativeBrokerID, "--user", c.NativeUserID,
				"--investor", c.NativeInvestorID, "--command-id", "1", "--batch-id", "batch", "--review-id", "review", "--output-dir", output}
			if out, err := exec.Command(python, args...).CombinedOutput(); err != nil {
				t.Fatalf("builder: %s %v", out, err)
			}
			raw, err := os.ReadFile(filepath.Join(output, "review.json"))
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := os.ReadFile(filepath.Join(output, "evidence.jsonl"))
			if err != nil || !bytes.Equal(evidence, audit) {
				t.Fatal("original audit bytes changed", err)
			}
			review, err := DecodeQueryRequestReview(raw, evidence)
			if err != nil || review.Query.Ticket != "1" || review.Query.OriginMessageID != c.OriginMessageID || review.Target != target {
				t.Fatal("builder/receiver contract mismatch", err)
			}
			if out, err := exec.Command(python, args...).CombinedOutput(); err == nil {
				t.Fatalf("existing output overwritten: %s", out)
			}
			retained, _ := os.ReadFile(filepath.Join(output, "review.json"))
			if !bytes.Equal(retained, raw) {
				t.Fatal("existing review modified")
			}
		})
	}
}
