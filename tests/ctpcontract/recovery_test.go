package ctpcontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

type durableFacts struct {
	Name      string `json:"name"`
	Intent    bool   `json:"intent"`
	Dispatch  bool   `json:"dispatch_started"`
	Result    bool   `json:"result"`
	Outbox    bool   `json:"outbox"`
	Published bool   `json:"published"`
	Acked     bool   `json:"acked"`
	Expected  string `json:"expected"`
	AckSafe   bool   `json:"ack_safe"`
}

func (f *durableFacts) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"intent", "dispatch_started", "result", "outbox", "published", "acked"} {
		value := string(bytes.TrimSpace(fields[key]))
		if value != "true" && value != "false" {
			return fmt.Errorf("%s requires an explicit JSON boolean", key)
		}
	}
	type plain durableFacts
	return json.Unmarshal(data, (*plain)(f))
}

// Reference expectations over durable facts, not an implementation of persistence.
func recoveryDecision(f durableFacts, writeAheadProven bool) (string, bool, error) {
	if (f.Dispatch && !f.Intent) || (f.Result && !f.Intent) || (f.Outbox && !f.Result) || (f.Published && !f.Outbox) || (f.Acked && !f.Published) {
		return "", false, fmt.Errorf("inconsistent durable evidence")
	}
	switch {
	case f.Acked:
		return "already_complete", true, nil
	case f.Published:
		return "ack_original", true, nil
	case f.Outbox:
		return "republish_same_output", false, nil
	case f.Result:
		return "restore_output", false, nil
	case f.Dispatch:
		return "outcome_unknown", false, nil
	case f.Intent:
		if !writeAheadProven {
			return "outcome_unknown", false, nil
		}
		return "proven_not_submitted", false, nil
	default:
		return "not_started", false, nil
	}
}

func TestRecoveryExpectationVectors(t *testing.T) {
	var f struct {
		Version   string         `json:"scenario_version"`
		Synthetic bool           `json:"synthetic_only"`
		Cases     []durableFacts `json:"cases"`
	}
	readFixture(t, "recovery_cases.json", &f)
	if !f.Synthetic || f.Version != "ctp.recovery.expectations.v1" || len(f.Cases) != 9 {
		t.Fatal("invalid recovery catalogue")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			// The unchanged shared vectors explicitly assume a proven write-ahead order.
			decision, ack, err := recoveryDecision(c, true)
			if err != nil || decision != c.Expected || ack != c.AckSafe {
				t.Fatalf("decision=%s ack=%v error=%v", decision, ack, err)
			}
		})
	}
	for _, bad := range []durableFacts{{Dispatch: true}, {Result: true}, {Intent: true, Outbox: true}, {Intent: true, Result: true, Published: true}, {Intent: true, Acked: true}} {
		if _, _, err := recoveryDecision(bad, false); err == nil {
			t.Fatal("impossible ACK/output evidence accepted")
		}
	}
}

func TestRecoveryAllFactCombinations(t *testing.T) {
	// Bit order: intent, dispatch, result, outbox, published, acked.
	valid := map[int]string{
		0: "not_started", 1: "outcome_unknown", 3: "outcome_unknown",
		5: "restore_output", 7: "restore_output",
		13: "republish_same_output", 15: "republish_same_output",
		29: "ack_original", 31: "ack_original",
		61: "already_complete", 63: "already_complete",
	}
	for _, proven := range []bool{false, true} {
		rejected := 0
		for mask := 0; mask < 64; mask++ {
			f := durableFacts{Intent: mask&1 != 0, Dispatch: mask&2 != 0,
				Result: mask&4 != 0, Outbox: mask&8 != 0,
				Published: mask&16 != 0, Acked: mask&32 != 0}
			decision, ack, err := recoveryDecision(f, proven)
			want, ok := valid[mask]
			if !ok {
				if err == nil {
					t.Fatalf("inconsistent mask %d accepted", mask)
				}
				rejected++
				continue
			}
			if proven && mask == 1 {
				want = "proven_not_submitted"
			}
			wantAck := mask == 29 || mask == 31 || mask == 61 || mask == 63
			if err != nil || decision != want || ack != wantAck {
				t.Fatalf("mask=%d proven=%v decision=%s ack=%v error=%v", mask, proven, decision, ack, err)
			}
		}
		if rejected != 53 {
			t.Fatalf("rejected=%d, want 53", rejected)
		}
	}
}

func TestRecoveryFactsRequireExplicitBooleans(t *testing.T) {
	fields := []string{"intent", "dispatch_started", "result", "outbox", "published", "acked"}
	for _, key := range fields {
		for _, value := range []string{"", "null", "0", "1", `"false"`, "[]", "{}"} {
			data := map[string]json.RawMessage{}
			for _, field := range fields {
				data[field] = json.RawMessage("false")
			}
			if value == "" {
				delete(data, key)
			} else {
				data[key] = json.RawMessage(value)
			}
			encoded, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			var f durableFacts
			if err := json.Unmarshal(encoded, &f); err == nil {
				t.Fatalf("%s=%q treated as a boolean", key, value)
			}
		}
	}
	data, err := json.Marshal(durableFacts{})
	if err != nil {
		t.Fatal(err)
	}
	var f durableFacts
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("explicit false rejected: %v", err)
	}
}

func TestFaultMessagesCarryDraftDiscriminators(t *testing.T) {
	var f messages
	readFixture(t, "fault_messages.json", &f)
	if !f.SyntheticOnly || f.ContractVersion != "ctp.f0.draft.2" || len(f.Cases) != 4 {
		t.Fatal("invalid fault catalogue")
	}
	for _, c := range f.Cases {
		var b struct {
			Action  string          `json:"action"`
			Event   string          `json:"event_type"`
			Payload map[string]any  `json:"payload"`
			Chunk   json.RawMessage `json:"chunk"`
		}
		d := json.NewDecoder(bytes.NewReader(c.Body))
		d.UseNumber()
		if err := d.Decode(&b); err != nil {
			t.Fatal(err)
		}
		// A batch reply carries counts, not the command's orders array.
		action := b.Action
		if action == "order.batch.submit" {
			action = ""
		}
		if err := validateDraftPayload(b.Payload, action, b.Event); err != nil {
			t.Fatal(err)
		}
		if len(b.Chunk) != 0 {
			t.Fatal("trade receipt has query chunk")
		}
	}
}
