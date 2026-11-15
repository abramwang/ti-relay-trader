package api

import (
	"testing"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
)

func TestProvisionalReconstructionPublicationSource(t *testing.T) {
	n := ledger.PerformanceNAV{FormulaVersion: performance.ProvisionalReconstructionFormula, Status: "provisional", PnLComponents: map[string]any{"continuity_verified": true, "authorization": map[string]any{"approved": true, "scope": "provisional_only"}}}
	if !isOfficialPerformanceNAVFormula(n.FormulaVersion) {
		t.Fatal("published provisional formula absent")
	}
	if source, ok := authoritativePerformanceNAVSource(n, false); !ok || source != "user_authorized_provisional_reconstruction" {
		t.Fatalf("wrong eligibility: %s %v", source, ok)
	}
	n.PnLComponents["authorization"] = map[string]any{"approved": false, "scope": "provisional_only"}
	if _, ok := authoritativePerformanceNAVSource(n, false); ok {
		t.Fatal("unapproved preview eligible")
	}
	n.Status = "blocked"
	if _, ok := authoritativePerformanceNAVSource(n, true); ok {
		t.Fatal("blocked reconstruction eligible")
	}
}
