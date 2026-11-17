package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These are original fixed-producer audit bytes. No synthetic audit repair is used.
func TestOCAuditGroupReviewBuilder(t *testing.T) {
	if os.Getenv("RELAY_OC_CTP_AUDIT_GROUP_FIX_DIR") == "" {
		t.Skip("explicit fixed OC audit material required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python 3 required", err)
	}
	count := 0
	for _, c := range nextdayCases(t) {
		contexts := nextdayAuditQueries(t, c)
		for _, input := range c.Inputs {
			var command rawObject
			if err := json.Unmarshal([]byte(input.Body), &command); err != nil {
				t.Fatal(err)
			}
			q, ok := contexts[textField(command, "message_id")]
			if !ok {
				continue
			}
			count++
			t.Run(c.Scenario+"/"+q.Action, func(t *testing.T) {
				target := TestQueryTarget{SourceID: "offline-only", ServerRunID: string(bytes.Repeat([]byte("0"), 40)),
					Epoch: "offline-only", Group: "offline-group", CounterSessionID: q.CounterSessionID,
					TradeDate: q.TradeDate, ConnectionGeneration: *q.ConnectionGeneration}
				targetRaw, err := json.Marshal(target)
				if err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				for name, raw := range map[string][]byte{"audit.jsonl": []byte(c.Audit), "command.json": []byte(input.Body), "target.json": targetRaw} {
					if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
				outDir := filepath.Join(dir, "draft")
				args := []string{"../../scripts/build-ctp-query-review.py", "--audit-file", filepath.Join(dir, "audit.jsonl"),
					"--audit-sha256", commandDigest([]byte(c.Audit)), "--command-file", filepath.Join(dir, "command.json"),
					"--target-file", filepath.Join(dir, "target.json"), "--broker", q.Route.BrokerID, "--gateway", q.Route.GatewayID,
					"--account", q.Route.AccountID, "--native-broker", q.NativeBrokerID, "--user", q.NativeUserID,
					"--investor", q.NativeInvestorID, "--command-id", "1", "--batch-id", "offline-only", "--review-id", "offline-only",
					"--output-dir", outDir}
				if output, err := exec.Command(python, args...).CombinedOutput(); err != nil {
					t.Fatalf("original audit builder failed: %v %s", err, output)
				}
				raw, err := os.ReadFile(filepath.Join(outDir, "review.json"))
				if err != nil {
					t.Fatal(err)
				}
				evidence, err := os.ReadFile(filepath.Join(outDir, "evidence.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				for _, line := range bytes.Split(bytes.TrimSuffix(evidence, []byte("\n")), []byte("\n")) {
					if !bytes.Contains([]byte(c.Audit), append(append([]byte{}, line...), '\n')) {
						t.Fatal("evidence is not an original audit line")
					}
				}
				r, err := DecodeQueryRequestReview(raw, evidence)
				if err != nil || r.Target != target || r.Query.Ticket != q.Ticket || *r.Query.SDKRequestID != *q.SDKRequestID {
					t.Fatal("builder/Go independent context mismatch", err)
				}
				batch, err := NewQueryBatch(r.Query, 0, time.Minute, DefaultBatchLimits())
				if err != nil {
					t.Fatal(err)
				}
				pages := 0
				for _, output := range c.Outputs {
					var object rawObject
					if err := json.Unmarshal(output, &object); err != nil {
						t.Fatal(err)
					}
					if textField(object, "origin_message_id") != q.OriginMessageID {
						continue
					}
					if _, err := batch.Accept(output, 0); err != nil {
						t.Fatal(err)
					}
					pages++
				}
				if pages != 1 {
					t.Fatal("unexpected result pages", pages)
				}
				want := BatchReady
				if c.Scenario == "partial_order" {
					want = BatchFailed
				}
				assertState(t, batch, want)
			})
		}
	}
	if count != 9 {
		t.Fatal("query coverage", count)
	}
}
