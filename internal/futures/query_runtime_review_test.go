package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const queryRuntimeFixtureDir = "../../testdata/oc_query_runtime_review_20261118"

func queryRuntimeFixture(t *testing.T, name string, dst any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(queryRuntimeFixtureDir, name))
	if err != nil || json.Unmarshal(raw, dst) != nil {
		t.Fatal("cannot read OC runtime evidence", name, err)
	}
}

func TestQueryRuntimeOriginalDigests(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(queryRuntimeFixtureDir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 10 {
		t.Fatal("unexpected inventory")
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 2 || filepath.Base(f[1]) != f[1] {
			t.Fatal("invalid manifest")
		}
		body, err := os.ReadFile(filepath.Join(queryRuntimeFixtureDir, f[1]))
		if err != nil || commandDigest(body) != f[0] {
			t.Fatal("evidence changed", f[1], err)
		}
	}
}

type runtimeFactsCase struct {
	Name    string          `json:"name"`
	Reply   json.RawMessage `json:"reply"`
	Outputs []struct {
		Body   string `json:"body"`
		SHA256 string `json:"sha256"`
		Stream string `json:"stream_suffix"`
	} `json:"outputs"`
	ReplayCalls int `json:"replay_sdk_calls"`
	TradeCalls  int `json:"trade_calls"`
}

func runtimeFacts(t *testing.T) []runtimeFactsCase {
	t.Helper()
	var doc struct {
		Synthetic bool               `json:"synthetic_only"`
		RealSDK   bool               `json:"real_sdk_calls"`
		Cases     []runtimeFactsCase `json:"cases"`
	}
	queryRuntimeFixture(t, "native_facts_runtime.json", &doc)
	if !doc.Synthetic || doc.RealSDK || len(doc.Cases) != 9 {
		t.Fatal("wrong evidence scope")
	}
	return doc.Cases
}

// Only fixture context is read from the synthetic output here. Live query
// context must instead be bound to the independently registered publication.
func runtimeFactsContext(t *testing.T, raw []byte) QueryContext {
	t.Helper()
	o, err := parseReply(raw)
	if err != nil {
		t.Fatal(err)
	}
	var p rawObject
	if json.Unmarshal(o["payload"], &p) != nil {
		t.Fatal("payload")
	}
	c := constraintsContext()
	c.OriginMessageID = textField(o, "origin_message_id")
	c.TradeDate, c.CounterSessionID = textField(p, "trade_date"), textField(p, "counter_session_id")
	return c
}

func TestQueryRuntimeNineOriginalFactOutputs(t *testing.T) {
	for _, v := range runtimeFacts(t) {
		t.Run(v.Name, func(t *testing.T) {
			if len(v.Outputs) != 1 || v.TradeCalls != 0 || v.ReplayCalls != 0 {
				t.Fatal("unexpected output/call count")
			}
			x := v.Outputs[0]
			raw := []byte(x.Body)
			if x.Stream != "reply" || commandDigest(raw) != x.SHA256 {
				t.Fatal("raw digest mismatch")
			}
			c := runtimeFactsContext(t, raw)
			b, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = b.Accept(raw, 0); err != nil {
				t.Fatal(err)
			}
			want := BatchFailed
			if v.Name == "complete" || v.Name == "null_price" || v.Name == "prefix_option" {
				want = BatchReady
			}
			assertState(t, b, want)
			candidate, err := b.Candidate()
			if want == BatchFailed {
				if err == nil {
					t.Fatal("failure became facts")
				}
			} else if err != nil || len(candidate.Constraints) != 1 || candidate.Constraints[0].Symbol != "rb2701" {
				t.Fatal(candidate, err)
			}
			if v.Name == "null_price" && candidate.Constraints[0].Dynamic.UpperLimitPrice != nil {
				t.Fatal("null filled")
			}
			if !bytes.Equal(b.AcceptedMessages()[0], raw) {
				t.Fatal("raw rewritten")
			}
			if replay, err := b.Accept(raw, time.Second); err != nil || !replay {
				t.Fatal("replay", err)
			}
		})
	}
}

func TestConstraintsNullLegOnlyOnFailure(t *testing.T) {
	raw := constraintsCases(t)[0].Reply
	for _, failed := range []bool{false, true} {
		for _, nulls := range [][]string{{"instrument"}, {"depth"}, {"instrument", "depth"}} {
			body := editBody(t, raw, func(m map[string]any) {
				if failed {
					m["status"], m["code"] = "failed", "QUERY_FAILED"
					delete(m, "chunk")
					delete(m["payload"].(map[string]any), "items")
				}
				e := m["adapter_context"].(map[string]any)["query_evidence"].(map[string]any)
				for _, name := range nulls {
					e[name] = nil
				}
			})
			b, _ := NewQueryBatch(constraintsContext(), 0, time.Minute, DefaultBatchLimits())
			_, err := b.Accept(body, 0)
			if failed && !(len(nulls) == 1 && nulls[0] == "instrument") {
				if err != nil {
					t.Fatal(err)
				}
				assertState(t, b, BatchFailed)
			} else if err == nil {
				t.Fatal("success with unexecuted leg or depth executed without instrument")
			}
		}
	}
}

func queryRuntimeHeartbeats(t *testing.T) []json.RawMessage {
	t.Helper()
	var d struct {
		Synthetic  bool              `json:"synthetic_only"`
		Deployed   bool              `json:"deployed"`
		Heartbeats []json.RawMessage `json:"heartbeats"`
	}
	queryRuntimeFixture(t, "heartbeat_examples.json", &d)
	if !d.Synthetic || d.Deployed || len(d.Heartbeats) != 3 {
		t.Fatal("invalid heartbeat inventory")
	}
	return d.Heartbeats
}

func TestQueryRuntimeHeartbeatOutputs(t *testing.T) {
	for i, raw := range queryRuntimeHeartbeats(t) {
		h, err := DecodeHeartbeat(raw, responsibilityRoute())
		if err != nil {
			t.Fatal(i, err)
		}
		_, ready := h.queryCollection()
		if ready != (i == 1) || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
			t.Fatal("capabilities", i)
		}
	}
}

func TestResidentQueriesOutsideTradingWindow(t *testing.T) {
	for _, v := range []struct{ at, phase string }{
		{"08:55:00", "waiting"}, {"10:20:00", "active"}, {"12:00:00", "active"}, {"15:01:00", "expired"},
	} {
		t.Run(v.at, func(t *testing.T) {
			raw := editBody(t, queryRuntimeHeartbeats(t)[1], func(m map[string]any) {
				m["produced_at"] = "2026-11-17T" + v.at + "+08:00"
				a := m["adapter_context"].(map[string]any)["admission_evidence"].(map[string]any)
				a["expires_at"] = "2026-11-17T15:00:00+08:00"
				a["process_lifecycle"] = map[string]any{"mode": "scheduled_assessment.v1", "starts_at": "2026-11-17T09:00:00+08:00", "expires_at": a["expires_at"], "phase": v.phase, "process_duration_seconds": 32400}
			})
			h, err := DecodeHeartbeat(raw, responsibilityRoute())
			if err != nil {
				t.Fatal(err)
			}
			scope := CommandScope{Route: responsibilityRoute(), NativeBrokerID: "4900", UserID: "300052141", InvestorID: "300052141"}
			target := publicationTarget()
			target.CounterSessionID, target.TradeDate = h.CounterSessionID, h.TradeDate
			row := StreamInput{"1-0", raw}
			proof, err := testQueryHeartbeatProof(row, scope, target, h.ProducedAt)
			if err != nil || !proof.ExpiresAt.Equal(h.ProducedAt.Add(5*time.Second)) {
				t.Fatal("query incorrectly tied to trade window", proof, err)
			}
			if h.Assessment.windowOpen(h.ProducedAt) || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
				t.Fatal("trading window widened")
			}
			if _, err := testQueryHeartbeatProof(row, scope, target, h.ProducedAt.Add(5*time.Second)); err == nil {
				t.Fatal("stale query proof")
			}
			legacy := editBody(t, raw, func(m map[string]any) {
				delete(m, "accepting_query_commands")
				delete(m["payload"].(map[string]any), "accepting_query_commands")
				a := m["adapter_context"].(map[string]any)
				delete(a, "operation_reasons")
				delete(a["admission_evidence"].(map[string]any), "query_execution")
			})
			if _, err := testQueryHeartbeatProof(StreamInput{"1-0", legacy}, scope, target, h.ProducedAt); err == nil {
				t.Fatal("legacy readiness inferred outside window")
			}
		})
	}
}
