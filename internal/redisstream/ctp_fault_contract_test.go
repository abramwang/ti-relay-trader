package redisstream

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Exercises shared reply handling only; it does not enable CTP order/fill ingestion.
func TestCTPFaultFixturesUseSharedReplySemantics(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ctp/fault_messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Version   string `json:"contract_version"`
		Synthetic bool   `json:"synthetic_only"`
		Cases     []struct {
			Name   string          `json:"name"`
			Suffix string          `json:"stream_suffix"`
			Body   json.RawMessage `json:"body"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if !f.Synthetic || f.Version != "ctp.f0.draft.2" || len(f.Cases) != 4 {
		t.Fatal("invalid fixture catalog")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			writer := &fakeLedgerWriter{}
			result := ProcessLedgerEntry(context.Background(), writer, "relay:test:v1:ctp_4900:990000000001:"+c.Suffix, "1793710921000-0", map[string]any{"body": string(c.Body)})
			if result.Archived != 1 || len(writer.raw) != 1 {
				t.Fatalf("raw evidence lost: %#v", result)
			}
			if result.ParseErrors != 0 || result.LedgerErrors != 0 || result.DeadLetters != 0 {
				t.Fatalf("fixture processing failed: %#v", result)
			}
			if len(writer.fills) != 0 {
				t.Fatal("command outcome created an execution")
			}
			if c.Name == "batch_partial" {
				if len(writer.orderUpdates) != 1 || len(writer.orderEvents) != 1 || writer.orderUpdates[0].GatewayOrderID != "fixture-batch-child-2" {
					t.Fatal("batch rejected more than the identified failed child")
				}
				if string(writer.orderUpdates[0].Order.RejectCode) != "ORDER_SUBMIT_REJECTED" {
					t.Fatal("explicit rejection lost")
				}
				if len(writer.cancelAttempts) != 0 {
					t.Fatal("batch submit became cancel attempt")
				}
				return
			}
			if len(writer.orderUpdates) != 0 || len(writer.orderEvents) != 0 || len(writer.fills) != 0 {
				t.Fatal("cancel/unknown result changed order or created execution")
			}
			if c.Name == "submit_unknown" {
				if len(writer.cancelAttempts) != 0 {
					t.Fatal("unknown submit became cancel attempt")
				}
				return
			}
			if len(writer.cancelAttempts) != 1 {
				t.Fatal("cancel audit missing")
			}
			a := writer.cancelAttempts[0]
			if a.GatewayOrderID != "fixture-order-1" || a.RetrySafe == nil || *a.RetrySafe || !a.ReconciliationRequired {
				t.Fatalf("unsafe cancel projection: %#v", a)
			}
			if c.Name == "cancel_rejected" && a.Status != "rejected" {
				t.Fatal("cancel rejection lost")
			}
		})
	}
}

func TestCTPLocalCommandFailuresDoNotCreateOrderTerminalStates(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/oc_query_runtime_review_20261118/command_rejections.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Synthetic bool `json:"synthetic_only"`
		RealSDK   bool `json:"real_sdk_calls"`
		Cases     []struct {
			Name   string `json:"name"`
			Output struct {
				Body   string `json:"body"`
				SHA256 string `json:"sha256"`
			} `json:"output"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &d) != nil || !d.Synthetic || d.RealSDK || len(d.Cases) != 6 {
		t.Fatal("invalid evidence scope")
	}
	for _, c := range d.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if fmt.Sprintf("%x", sha256.Sum256([]byte(c.Output.Body))) != c.Output.SHA256 {
				t.Fatal("body changed")
			}
			w := &fakeLedgerWriter{}
			r := ProcessLedgerEntry(context.Background(), w, "relay:test:v1:ctp_4900:ctp-dongwu-assessment-pm-20261117:reply", "1-0", map[string]any{"body": c.Output.Body})
			if r.Archived != 1 || r.Replies != 1 || r.ParseErrors != 0 || r.LedgerErrors != 0 || r.DeadLetters != 0 {
				t.Fatal(r)
			}
			if len(w.orderUpdates) != 0 || len(w.orderEvents) != 0 || len(w.fills) != 0 {
				t.Fatal("local command failure changed order state")
			}
		})
	}
}
