package futures

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func reviewFixture(t *testing.T, c QueryContext, target TestQueryTarget, id int64) ([]byte, []byte) {
	t.Helper()
	evidence := []byte(`{"fixture_only":true,"source":"independent synthetic OC request audit"}`)
	sum := sha256.Sum256(evidence)
	r := QueryRequestReview{CommandID: id, BatchID: "reviewed-query", ReviewID: "synthetic-request-review", EvidenceSHA256: hex.EncodeToString(sum[:]), Target: target, Query: c, TimeoutMS: 60000}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b, evidence
}

func TestQueryRequestReviewStrict(t *testing.T) {
	c := batchContext("asset")
	c.NativeUserID = "FIXTURE_USER"
	c.NativeInvestorID = "FIXTURE_INV"
	target := publicationTarget()
	target.CounterSessionID = c.CounterSessionID
	target.TradeDate = c.TradeDate
	raw, evidence := reviewFixture(t, c, target, 1)
	if _, err := DecodeQueryRequestReview(raw, evidence); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["unexpected"] = true },
		func(m map[string]any) { m["timeout_ms"] = 0 },
		func(m map[string]any) { m["timeout_ms"] = 300001 },
		func(m map[string]any) { m["evidence_sha256"] = "wrong" },
		func(m map[string]any) { m["query_context"].(map[string]any)["CounterSessionID"] = "other" },
		func(m map[string]any) { m["query_context"].(map[string]any)["SDKRequestID"] = 0 },
		func(m map[string]any) { m["query_context"].(map[string]any)["Route"].(map[string]any)["env"] = "prod" },
		func(m map[string]any) { m["query_context"].(map[string]any)["unknown"] = true },
	} {
		if _, err := DecodeQueryRequestReview(editBody(t, raw, change), evidence); err == nil {
			t.Fatal("unsafe review accepted")
		}
	}
	if _, err := DecodeQueryRequestReview(append([]byte(`{"command_id":2,`), raw[1:]...), evidence); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := DecodeQueryRequestReview(raw, append(evidence, ' ')); err == nil {
		t.Fatal("changed evidence accepted")
	}
}

func TestConstraintsRequestReviewBindsExactTarget(t *testing.T) {
	c := constraintsContext()
	target := publicationTarget()
	target.CounterSessionID, target.TradeDate = c.CounterSessionID, c.TradeDate
	raw, evidence := reviewFixture(t, c, target, 1)
	r, err := DecodeQueryRequestReview(raw, evidence)
	if err != nil || r.Query.Constraints == nil || *r.Query.Constraints != *c.Constraints {
		t.Fatal("facts target lost", err)
	}
	for _, edit := range []func(map[string]any){
		func(m map[string]any) { delete(m["query_context"].(map[string]any), "Constraints") },
		func(m map[string]any) { m["query_context"].(map[string]any)["Constraints"] = nil },
		func(m map[string]any) {
			m["query_context"].(map[string]any)["Constraints"].(map[string]any)["exchange"] = "DCE "
		},
		func(m map[string]any) {
			m["query_context"].(map[string]any)["Constraints"].(map[string]any)["extra"] = true
		},
		func(m map[string]any) { m["query_context"].(map[string]any)["Action"] = "account.asset.query" },
		func(m map[string]any) { m["query_context"].(map[string]any)["SDKRequestID"] = 3 },
	} {
		if _, err := DecodeQueryRequestReview(editBody(t, raw, edit), evidence); err == nil {
			t.Fatal("invalid facts review accepted")
		}
	}
}
