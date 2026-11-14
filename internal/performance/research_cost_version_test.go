package performance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"ti-relay-trader/internal/ledger"
)

func researchCostFixture() ResearchCostBundle {
	a, dates, days := continuityFixture()
	return ResearchCostBundle{AccountID: "account", VersionID: "review-1", AnchorDate: "2026-10-09", Opening: a, Dates: dates, Costs: days, EvidenceHashes: map[string]string{"synthetic-test": strings.Repeat("a", 64)}}
}

func TestResearchCostVersionRoundTrip(t *testing.T) {
	b := researchCostFixture()
	v, err := PrepareResearchCostVersion(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeResearchCostVersion(v)
	if err != nil || !got.ResearchOnly || got.Review.Publishable || !got.Review.Verified || !reflect.DeepEqual(got.Costs, b.Costs) || !reflect.DeepEqual(got.Opening, b.Opening) {
		t.Fatal(got, err)
	}
	again, err := PrepareResearchCostVersion(got)
	if err != nil || string(again.Payload) != string(v.Payload) || again.PayloadSHA256 != v.PayloadSHA256 {
		t.Fatal("unstable round trip", err)
	}
	if b.Schema != "" || b.ResearchOnly || b.Review.Verified {
		t.Fatal("mutated input")
	}
}

func TestResearchCostVersionAuditPrecisionAndMissingLinks(t *testing.T) {
	b := researchCostFixture()
	a := ledger.T0Allocation{LinkID: "link", AccountID: "account", TradeDate: b.Dates[0], GatewayOrderID: "order", SecurityID: "510310.SH", TradeSide: "B", Quantity: 10, GroupID: "remaining", Role: "residual", Revision: 1, Status: "active", EvidenceType: "user_confirmation", EvidenceRef: "test", ConfirmedBy: "test", ConfirmedDate: "2026-11-11"}
	b.Costs[0].AttributionLinkIDs = []string{"link"}
	if _, err := PrepareResearchCostVersion(b); err == nil {
		t.Fatal("missing audit accepted")
	}
	b.Audits = []ledger.T0Allocation{a}
	v, err := PrepareResearchCostVersion(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeResearchCostVersion(v)
	if err != nil || !got.Audits[0].ConfirmedAt.IsZero() || got.Audits[0].ConfirmedDate != "2026-11-11" {
		t.Fatal("date precision", err)
	}
	auditJSON, err := json.Marshal(got.Audits)
	if err != nil || strings.Contains(string(auditJSON), "confirmed_at") {
		t.Fatal("date-only audit acquired a timestamp", err)
	}
	for name, mutate := range map[string]func(*ResearchCostBundle){
		"wrong day":       func(b *ResearchCostBundle) { b.Audits[0].TradeDate = b.Dates[1] },
		"wrong account":   func(b *ResearchCostBundle) { b.Audits[0].AccountID = "other" },
		"revoked":         func(b *ResearchCostBundle) { b.Audits[0].Status = "revoked" },
		"duplicate":       func(b *ResearchCostBundle) { b.Audits = append(b.Audits, b.Audits[0]) },
		"duplicate order": func(b *ResearchCostBundle) { x := b.Audits[0]; x.LinkID = "other"; b.Audits = append(b.Audits, x) },
		"bad hash":        func(b *ResearchCostBundle) { b.EvidenceHashes = map[string]string{"source": "bad"} },
		"missing anchor":  func(b *ResearchCostBundle) { b.Opening = nil },
		"broken cost":     func(b *ResearchCostBundle) { b.Costs[0].Positions[0].CloseTotalCost++ },
	} {
		t.Run(name, func(t *testing.T) {
			copy, err := DecodeResearchCostVersion(v)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&copy)
			if _, err = PrepareResearchCostVersion(copy); err == nil {
				t.Fatal("accepted invalid bundle")
			}
		})
	}
}

func TestResearchCostVersionRejectsCorruptReadback(t *testing.T) {
	v, err := PrepareResearchCostVersion(researchCostFixture())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ledger.ResearchCostVersion){
		"digest":  func(v *ledger.ResearchCostVersion) { v.PayloadSHA256 = strings.Repeat("0", 64) },
		"account": func(v *ledger.ResearchCostVersion) { v.AccountID = "other" },
		"version": func(v *ledger.ResearchCostVersion) { v.VersionID = "other" },
		"dates":   func(v *ledger.ResearchCostVersion) { v.DateTo = "2026-10-14" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := v
			mutate(&copy)
			if _, err := DecodeResearchCostVersion(copy); err == nil {
				t.Fatal("accepted corrupt envelope")
			}
		})
	}
	for _, field := range []string{"research_only", "review", "schema", "unknown"} {
		t.Run(field, func(t *testing.T) {
			copy := v
			var body map[string]any
			if err := json.Unmarshal(v.Payload, &body); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "research_only":
				body[field] = false
			case "review":
				body[field].(map[string]any)["economic_nav_publishable"] = true
			case "schema":
				body[field] = "other"
			default:
				body[field] = true
			}
			copy.Payload, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.Sum256(copy.Payload)
			copy.PayloadSHA256 = hex.EncodeToString(h[:])
			if _, err := DecodeResearchCostVersion(copy); err == nil {
				t.Fatal("accepted recomputed hash with invalid content")
			}
		})
	}
}
