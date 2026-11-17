package futures

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Counts are retained callback observations, not orders, fills, ACKs or releases.
type ReadOnlyLegacyCounts struct {
	Retained         int64 `json:"retained_count"`
	Pending          int64 `json:"pending_count"`
	AuditOnly        int64 `json:"audit_only_count"`
	Linked           int64 `json:"linked_pending_count"`
	Waiting          int64 `json:"waiting_pending_count"`
	BlockingConflict bool  `json:"blocking_conflict"`
}

type ReadOnlyCurrentCounts struct {
	Retained        int64 `json:"retained_count"`
	Pending         int64 `json:"pending_count"`
	AuditOnly       int64 `json:"audit_only_count"`
	PreExchange     int64 `json:"pre_exchange_count"`
	Linked          int64 `json:"linked_pending_count"`
	Waiting         int64 `json:"waiting_pending_count"`
	BlockingPending int64 `json:"blocking_pending_count"`
	BlockingErrors  int64 `json:"blocking_error_count"`
}

type ReadOnlySourceScope struct {
	Env            string `json:"env"`
	BrokerID       string `json:"broker_id"`
	AccountID      string `json:"account_id"`
	GatewayID      string `json:"gateway_id"`
	NativeBrokerID string `json:"native_broker_id"`
	InvestorID     string `json:"investor_id"`
	UserID         string `json:"user_id"`
}

type ReadOnlyReconciliation struct {
	Policy                 string                `json:"policy"`
	SourceManifestSHA256   string                `json:"source_manifest_sha256"`
	SourceScope            ReadOnlySourceScope   `json:"source_scope"`
	ExternalEventsEnabled  bool                  `json:"external_events_enabled"`
	QueryEvidenceGateClear bool                  `json:"query_evidence_gate_clear"`
	Legacy                 ReadOnlyLegacyCounts  `json:"legacy"`
	Current                ReadOnlyCurrentCounts `json:"current"`
}

func decodeReadOnlyReconciliation(raw json.RawMessage, route QueryRoute, queryReady bool) (*ReadOnlyReconciliation, error) {
	bad := func() (*ReadOnlyReconciliation, error) {
		return nil, fmt.Errorf("invalid futures heartbeat: readonly reconciliation")
	}
	obj, err := shape(raw, []string{"policy", "source_manifest_sha256", "source_scope", "external_events_enabled", "query_evidence_gate_clear", "legacy", "current"}, nil)
	if err != nil {
		return bad()
	}
	if textField(obj, "policy") != "readonly_query_evidence.v1" || !literal(obj["external_events_enabled"], "false") ||
		(!literal(obj["query_evidence_gate_clear"], "true") && !literal(obj["query_evidence_gate_clear"], "false")) {
		return bad()
	}
	hash := textField(obj, "source_manifest_sha256")
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return bad()
	}
	if _, err := shape(obj["source_scope"], []string{"env", "broker_id", "account_id", "gateway_id", "native_broker_id", "investor_id", "user_id"}, nil); err != nil {
		return bad()
	}
	legacy, err := shape(obj["legacy"], []string{"retained_count", "pending_count", "audit_only_count", "linked_pending_count", "waiting_pending_count", "blocking_conflict"}, nil)
	if err != nil || (!literal(legacy["blocking_conflict"], "true") && !literal(legacy["blocking_conflict"], "false")) {
		return bad()
	}
	current, err := shape(obj["current"], []string{"retained_count", "pending_count", "audit_only_count", "pre_exchange_count", "linked_pending_count", "waiting_pending_count", "blocking_pending_count", "blocking_error_count"}, nil)
	if err != nil {
		return bad()
	}
	for _, fields := range []rawObject{legacy, current} {
		for key, value := range fields {
			if key == "blocking_conflict" {
				continue
			}
			n, err := nullableInteger(value)
			if err != nil || n == nil || *n < 0 || *n > 10000 {
				return bad()
			}
		}
	}
	var r ReadOnlyReconciliation
	if err := json.Unmarshal(raw, &r); err != nil {
		return bad()
	}
	s := r.SourceScope
	c := CommandScope{QueryRoute{s.Env, s.BrokerID, s.GatewayID, s.AccountID}, s.NativeBrokerID, s.InvestorID, s.UserID}
	if c.validate() != nil || s.Env != "test" || s.Env != route.Env || s.BrokerID != route.BrokerID || s.AccountID != route.AccountID || s.GatewayID == route.GatewayID {
		return bad()
	}
	l, n := r.Legacy, r.Current
	if l.Retained != l.Pending+l.AuditOnly || l.Pending != l.Linked+l.Waiting ||
		n.Retained != n.Pending+n.AuditOnly || n.Pending != n.Linked+n.Waiting+n.BlockingPending || n.PreExchange > n.Retained {
		return bad()
	}
	if r.QueryEvidenceGateClear && (l.BlockingConflict || n.BlockingPending != 0 || n.BlockingErrors != 0) || queryReady && !r.QueryEvidenceGateClear {
		return bad()
	}
	return &r, nil
}

func (h Heartbeat) readOnlyNativeScopeMatches(s CommandScope) bool {
	if h.ReadOnly == nil || h.ReadOnly.Reconciliation == nil {
		return true
	}
	r := h.ReadOnly.Reconciliation.SourceScope
	return r.NativeBrokerID == s.NativeBrokerID && r.InvestorID == s.InvestorID && r.UserID == s.UserID
}
