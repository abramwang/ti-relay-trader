package futures

import (
	"encoding/json"
	"fmt"
	"strings"
)

type AssessmentLegacyDiagnostics struct {
	Retained                      int64 `json:"source_callback_count"`
	AuditOnly                     int64 `json:"original_audit_only_count"`
	Pending                       int64 `json:"original_pending_count"`
	Linked                        int64 `json:"linked_callback_count"`
	QueryObservations             int64 `json:"query_observation_count"`
	BlockingConflict              bool  `json:"blocking_conflict"`
	BusinessProcessed             int64 `json:"business_processed_count"`
	OldOutcomesMutated            bool  `json:"old_outcomes_mutated"`
	BusinessPublicationAuthorized bool  `json:"business_publication_authorized"`
}

type AssessmentLegacySource struct {
	ManifestSHA256 string                      `json:"manifest_sha256"`
	SourceScope    ReadOnlySourceScope         `json:"source_scope"`
	Diagnostics    AssessmentLegacyDiagnostics `json:"diagnostics"`
}

// This explicitly negotiated recovery binding must not silently fall back to
// the pre-reconciliation TEST profile when its source evidence disappears.
func assessmentRequiresLegacy(route QueryRoute) bool {
	return route.Env == "test" && route.BrokerID == "ctp_4900" && route.GatewayID == "ctp-dongwu-readonly-recovery-20261117"
}

func decodeAssessmentLegacy(raw json.RawMessage, route QueryRoute) (*AssessmentLegacySource, error) {
	bad := func() (*AssessmentLegacySource, error) {
		return nil, fmt.Errorf("invalid futures heartbeat: assessment legacy source")
	}
	o, err := shape(raw, []string{"manifest_sha256", "source_scope", "diagnostics"}, nil)
	if err != nil {
		return bad()
	}
	hash := textField(o, "manifest_sha256")
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return bad()
	}
	if _, err := shape(o["source_scope"], []string{"env", "broker_id", "account_id", "gateway_id", "native_broker_id", "investor_id", "user_id"}, nil); err != nil {
		return bad()
	}
	d, err := shape(o["diagnostics"], []string{"source_callback_count", "original_audit_only_count", "original_pending_count", "linked_callback_count", "query_observation_count", "blocking_conflict", "business_processed_count", "old_outcomes_mutated", "business_publication_authorized"}, nil)
	if err != nil {
		return bad()
	}
	for _, key := range []string{"source_callback_count", "original_audit_only_count", "original_pending_count", "linked_callback_count", "query_observation_count", "business_processed_count"} {
		n, err := nullableInteger(d[key])
		if err != nil || n == nil || *n < 0 || *n > 10000 {
			return bad()
		}
	}
	if (!literal(d["blocking_conflict"], "true") && !literal(d["blocking_conflict"], "false")) ||
		!literal(d["old_outcomes_mutated"], "false") || !literal(d["business_publication_authorized"], "false") {
		return bad()
	}
	var r AssessmentLegacySource
	if json.Unmarshal(raw, &r) != nil {
		return bad()
	}
	s := r.SourceScope
	c := CommandScope{QueryRoute{s.Env, s.BrokerID, s.GatewayID, s.AccountID}, s.NativeBrokerID, s.InvestorID, s.UserID}
	if c.validate() != nil || s.Env != "test" || s.Env != route.Env || s.BrokerID != route.BrokerID || s.AccountID != route.AccountID || s.GatewayID == route.GatewayID {
		return bad()
	}
	n := r.Diagnostics
	if n.Retained != n.Pending+n.AuditOnly || n.Linked > n.Pending || n.BusinessProcessed != 0 {
		return bad()
	}
	return &r, nil
}

func sameAssessmentLegacyBinding(a, b *AssessmentLegacySource) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ManifestSHA256 == b.ManifestSHA256 && a.SourceScope == b.SourceScope &&
		a.Diagnostics.Retained == b.Diagnostics.Retained && a.Diagnostics.Pending == b.Diagnostics.Pending && a.Diagnostics.AuditOnly == b.Diagnostics.AuditOnly
}

func (h Heartbeat) assessmentNativeScopeMatches(s CommandScope) bool {
	if h.Assessment == nil || h.Assessment.LegacySource == nil {
		return true
	}
	r := h.Assessment.LegacySource.SourceScope
	return r.NativeBrokerID == s.NativeBrokerID && r.InvestorID == s.InvestorID && r.UserID == s.UserID
}
