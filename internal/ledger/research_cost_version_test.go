package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestResearchCostVersionEnvelope(t *testing.T) {
	payload := []byte(`{"research_only":true}`)
	h := sha256.Sum256(payload)
	v := ResearchCostVersion{AccountID: "account", VersionID: "version", AnchorDate: "2026-10-09", DateFrom: "2026-10-12", DateTo: "2026-10-13", Payload: payload, PayloadSHA256: hex.EncodeToString(h[:])}
	if err := validateResearchCostVersion(v); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ResearchCostVersion){
		"empty account":     func(v *ResearchCostVersion) { v.AccountID = "" },
		"whitespace":        func(v *ResearchCostVersion) { v.VersionID = " version" },
		"nul":               func(v *ResearchCostVersion) { v.VersionID = "version\x00" },
		"long version":      func(v *ResearchCostVersion) { v.VersionID = strings.Repeat("x", 161) },
		"bad calendar date": func(v *ResearchCostVersion) { v.DateTo = "2026-02-30" },
		"anchor overlap":    func(v *ResearchCostVersion) { v.AnchorDate = v.DateFrom },
		"reversed range":    func(v *ResearchCostVersion) { v.DateFrom = "2026-10-14" },
		"hash":              func(v *ResearchCostVersion) { v.PayloadSHA256 = strings.Repeat("0", 64) },
		"not object":        func(v *ResearchCostVersion) { v.Payload = []byte(`[]`) },
		"invalid json":      func(v *ResearchCostVersion) { v.Payload = []byte(`{`) },
		"oversized":         func(v *ResearchCostVersion) { v.Payload = make([]byte, MaxResearchCostVersionBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			copy := v
			mutate(&copy)
			if err := validateResearchCostVersion(copy); err == nil {
				t.Fatal("accepted invalid envelope")
			}
		})
	}
}
