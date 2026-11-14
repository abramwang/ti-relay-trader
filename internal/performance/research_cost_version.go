package performance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"ti-relay-trader/internal/ledger"
)

const researchCostSchema = "relay.research_cost_version.v1"

// EvidenceHashes identify frozen inputs, not proof of final economic scope.
// Audits are the active revision snapshot at review time, not inferred ownership.
type ResearchCostBundle struct {
	Schema         string                     `json:"schema"`
	AccountID      string                     `json:"account_id"`
	VersionID      string                     `json:"version_id"`
	AnchorDate     string                     `json:"anchor_date"`
	Dates          []string                   `json:"trade_dates"`
	Opening        []ledger.PositionCostState `json:"opening"`
	Costs          []CostLedgerResult         `json:"costs"`
	Audits         []ledger.T0Allocation      `json:"active_attribution_audits"`
	EvidenceHashes map[string]string          `json:"evidence_sha256"`
	Review         CostContinuityReview       `json:"review"`
	ResearchOnly   bool                       `json:"research_only"`
}

func reviewResearchCostBundle(b ResearchCostBundle) (CostContinuityReview, error) {
	if b.Schema != researchCostSchema || !b.ResearchOnly || b.VersionID == "" || len(b.EvidenceHashes) == 0 || len(b.EvidenceHashes) > 100 || len(b.Audits) > 10000 {
		return CostContinuityReview{}, fmt.Errorf("invalid research bundle scope/evidence")
	}
	for name, h := range b.EvidenceHashes {
		decoded, err := hex.DecodeString(h)
		if name == "" || strings.TrimSpace(name) != name || len(name) > 512 || err != nil || len(decoded) != 32 || strings.ToLower(h) != h {
			return CostContinuityReview{}, fmt.Errorf("invalid research evidence fingerprint")
		}
	}
	r, err := ReviewCostContinuity(b.AccountID, b.AnchorDate, b.Opening, b.Dates, b.Costs)
	if err != nil {
		return r, err
	}
	byID := map[string]ledger.T0Allocation{}
	byOrder := map[string]bool{}
	dateSet := map[string]bool{}
	for _, date := range b.Dates {
		dateSet[date] = true
	}
	for _, a := range b.Audits {
		n, err := ledger.NormalizeT0Allocation(a)
		key := a.TradeDate + "|" + a.GatewayOrderID
		_, duplicate := byID[a.LinkID]
		if err != nil || n.TradeDate != a.TradeDate || a.AccountID != b.AccountID || a.Status != "active" || !dateSet[a.TradeDate] || duplicate || byOrder[key] {
			return r, fmt.Errorf("invalid or duplicate active attribution snapshot")
		}
		byID[a.LinkID], byOrder[key] = a, true
	}
	for _, day := range b.Costs {
		seen := map[string]bool{}
		for _, id := range day.AttributionLinkIDs {
			a, ok := byID[id]
			if !ok || a.TradeDate != day.TradeDate || seen[id] {
				return r, fmt.Errorf("missing or wrong-day attribution audit: %s", id)
			}
			seen[id] = true
		}
	}
	return r, nil
}

// PrepareResearchCostVersion recalculates the structural review rather than
// trusting a caller's summary. It never grants cost or economic NAV publication.
func PrepareResearchCostVersion(b ResearchCostBundle) (ledger.ResearchCostVersion, error) {
	b.Schema, b.ResearchOnly = researchCostSchema, true
	r, err := reviewResearchCostBundle(b)
	if err != nil {
		return ledger.ResearchCostVersion{}, err
	}
	b.Review = r
	payload, err := json.Marshal(b)
	if err != nil {
		return ledger.ResearchCostVersion{}, err
	}
	if len(payload) > ledger.MaxResearchCostVersionBytes {
		return ledger.ResearchCostVersion{}, fmt.Errorf("research bundle too large")
	}
	h := sha256.Sum256(payload)
	return ledger.ResearchCostVersion{AccountID: b.AccountID, VersionID: b.VersionID, AnchorDate: b.AnchorDate, DateFrom: b.Dates[0], DateTo: b.Dates[len(b.Dates)-1], Payload: payload, PayloadSHA256: hex.EncodeToString(h[:])}, nil
}

func DecodeResearchCostVersion(v ledger.ResearchCostVersion) (ResearchCostBundle, error) {
	var b ResearchCostBundle
	if len(v.Payload) > ledger.MaxResearchCostVersionBytes {
		return b, fmt.Errorf("research bundle too large")
	}
	h := sha256.Sum256(v.Payload)
	if hex.EncodeToString(h[:]) != v.PayloadSHA256 {
		return b, fmt.Errorf("research bundle digest mismatch")
	}
	d := json.NewDecoder(bytes.NewReader(v.Payload))
	d.DisallowUnknownFields()
	if err := d.Decode(&b); err != nil {
		return b, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return b, fmt.Errorf("trailing research bundle data")
	}
	r, err := reviewResearchCostBundle(b)
	if err != nil {
		return b, err
	}
	if b.AccountID != v.AccountID || b.VersionID != v.VersionID || b.AnchorDate != v.AnchorDate || b.Dates[0] != v.DateFrom || b.Dates[len(b.Dates)-1] != v.DateTo || !reflect.DeepEqual(b.Review, r) {
		return b, fmt.Errorf("research bundle envelope/review mismatch")
	}
	return b, nil
}

func SaveResearchCostVersion(ctx context.Context, repo *ledger.Repository, b ResearchCostBundle) (ledger.ResearchCostVersion, error) {
	v, err := PrepareResearchCostVersion(b)
	if err != nil {
		return v, err
	}
	return repo.AppendResearchCostVersion(ctx, v)
}

func LoadResearchCostVersion(ctx context.Context, repo *ledger.Repository, accountID, versionID string) (ResearchCostBundle, error) {
	v, err := repo.GetResearchCostVersion(ctx, accountID, versionID)
	if err != nil {
		return ResearchCostBundle{}, err
	}
	return DecodeResearchCostVersion(v)
}
