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

// Reference expectations over durable facts, not an implementation of persistence.
func recoveryDecision(f durableFacts) (string, bool, error) {
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
			decision, ack, err := recoveryDecision(c)
			if err != nil || decision != c.Expected || ack != c.AckSafe {
				t.Fatalf("decision=%s ack=%v error=%v", decision, ack, err)
			}
			// No recovery branch grants permission to invoke the SDK again.
			if decision == "resubmit" || decision == "recancel" {
				t.Fatal("automatic write replay")
			}
		})
	}
	for _, bad := range []durableFacts{{Dispatch: true}, {Result: true}, {Intent: true, Outbox: true}, {Intent: true, Result: true, Published: true}, {Intent: true, Acked: true}} {
		if _, _, err := recoveryDecision(bad); err == nil {
			t.Fatal("impossible ACK/output evidence accepted")
		}
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
