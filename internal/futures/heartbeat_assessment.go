package futures

import (
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

// Assessment is a bounded TEST runtime profile, not production authorization
// or evidence of complete asset scope / a local sufficient-margin calculation.
type HeartbeatAssessment struct {
	ConnectionGeneration                                                                          *int64
	TransportRecoveryClear                                                                        bool
	AssessmentID, Symbol, Exchange                                                                string
	ExpiresAt                                                                                     time.Time
	AssetScopeVerified, PositionSnapshotReady, InstrumentUsable, OrderRefReady, NativeLimitsFresh bool
	SubmitAttempts, CancelAttempts, PendingCallbacks                                              int64
	CallbackPolicy                                                                                string
	DeferredCallbacks, BlockingCallbacks                                                          int64
	CallbackReconciliationComplete                                                                bool
	NativeDepthRequestID                                                                          *int64
	Static                                                                                        *AssessmentStatic
	Limits                                                                                        *AssessmentLimits
	LegacySource                                                                                  *AssessmentLegacySource
}
type AssessmentStatic struct {
	PriceTick      Price  `json:"price_tick"`
	VolumeMultiple int64  `json:"volume_multiple"`
	MinLimitLots   int64  `json:"min_limit_lots"`
	MaxLimitLots   int64  `json:"max_limit_lots"`
	SDKRequestID   int64  `json:"sdk_request_id"`
	CapturedAt     string `json:"captured_at"`
}
type AssessmentLimits struct {
	Lower       Price  `json:"lower_limit"`
	Upper       Price  `json:"upper_limit"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
}

func positivePrice(p Price) bool {
	r, ok := new(big.Rat).SetString(string(p))
	return ok && r.Sign() > 0
}

func decodeAssessmentHeartbeat(fields rawObject, h Heartbeat, route QueryRoute) (HeartbeatAssessment, error) {
	bad := func() (HeartbeatAssessment, error) {
		return HeartbeatAssessment{}, fmt.Errorf("invalid futures heartbeat: assessment evidence or readiness")
	}
	g, err := nullableInteger(fields["connection_generation"])
	if err != nil || (g != nil && *g < 1) || (!literal(fields["transport_recovery_clear"], "true") && !literal(fields["transport_recovery_clear"], "false")) {
		return bad()
	}
	var raw rawObject
	if json.Unmarshal(fields["admission_evidence"], &raw) != nil || raw == nil {
		return bad()
	}
	a := HeartbeatAssessment{ConnectionGeneration: g, TransportRecoveryClear: literal(fields["transport_recovery_clear"], "true"), AssessmentID: textField(raw, "assessment_id"), Symbol: textField(raw, "symbol"), Exchange: textField(raw, "exchange")}
	if !validID(a.AssessmentID) || len(a.AssessmentID) > 128 || !native(a.Symbol, 80, false) || a.Exchange != "SHFE" || !validTime(textField(raw, "expires_at")) {
		return bad()
	}
	a.ExpiresAt, _ = time.Parse(time.RFC3339Nano, textField(raw, "expires_at"))
	for key, dst := range map[string]*bool{"asset_scope_verified": &a.AssetScopeVerified, "position_snapshot_ready": &a.PositionSnapshotReady, "instrument_usable": &a.InstrumentUsable, "order_ref_ready": &a.OrderRefReady, "native_limits_fresh": &a.NativeLimitsFresh} {
		if !literal(raw[key], "true") && !literal(raw[key], "false") {
			return bad()
		}
		*dst = literal(raw[key], "true")
	}
	// This delivery explicitly does not establish asset scope. A different
	// financial claim needs its own contract/evidence, not a silent upgrade.
	if a.AssetScopeVerified {
		return bad()
	}
	for key, dst := range map[string]*int64{"submit_attempts": &a.SubmitAttempts, "cancel_attempts": &a.CancelAttempts, "pending_callback_count": &a.PendingCallbacks} {
		n, e := nullableInteger(raw[key])
		if e != nil || n == nil || *n < 0 {
			return bad()
		}
		*dst = *n
	}
	if a.SubmitAttempts > 1 || a.CancelAttempts > 1 {
		return bad()
	}
	policyKeys := []string{"callback_policy", "deferred_callback_count", "blocking_callback_count", "callback_reconciliation_complete"}
	policyFields := 0
	for _, key := range policyKeys {
		if _, present := raw[key]; present {
			policyFields++
		}
	}
	a.CallbackReconciliationComplete = a.PendingCallbacks == 0
	if policyFields != 0 {
		if policyFields != len(policyKeys) || textField(raw, "callback_policy") != "local_assessment_only.v1" {
			return bad()
		}
		a.CallbackPolicy = "local_assessment_only.v1"
		for key, dst := range map[string]*int64{"deferred_callback_count": &a.DeferredCallbacks, "blocking_callback_count": &a.BlockingCallbacks} {
			n, e := nullableInteger(raw[key])
			if e != nil || n == nil || *n < 0 || *n > a.PendingCallbacks {
				return bad()
			}
			*dst = *n
		}
		if a.PendingCallbacks-a.DeferredCallbacks != a.BlockingCallbacks ||
			(!literal(raw["callback_reconciliation_complete"], "true") && !literal(raw["callback_reconciliation_complete"], "false")) ||
			literal(raw["callback_reconciliation_complete"], "true") != a.CallbackReconciliationComplete {
			return bad()
		}
	}
	if b, ok := raw["legacy_source"]; ok {
		a.LegacySource, err = decodeAssessmentLegacy(b, route)
		if err != nil || a.CallbackPolicy != "local_assessment_only.v1" {
			return bad()
		}
	} else if assessmentRequiresLegacy(route) {
		return bad()
	}
	a.NativeDepthRequestID, err = nullableInteger(raw["native_depth_request_id"])
	if err != nil || (a.NativeDepthRequestID != nil && *a.NativeDepthRequestID < 1) {
		return bad()
	}
	if b, ok := raw["static_constraints"]; ok {
		if json.Unmarshal(b, &a.Static) != nil || a.Static == nil {
			return bad()
		}
		s := a.Static
		if !positivePrice(s.PriceTick) || s.VolumeMultiple <= 0 || s.MinLimitLots < 1 || s.MaxLimitLots < s.MinLimitLots || s.SDKRequestID < 1 || !validTime(s.CapturedAt) {
			return bad()
		}
	}
	if b, ok := raw["native_limits"]; ok {
		if json.Unmarshal(b, &a.Limits) != nil || a.Limits == nil {
			return bad()
		}
		l := a.Limits
		if !positivePrice(l.Lower) || !positivePrice(l.Upper) || !validTime(l.StartedAt) || !validTime(l.CompletedAt) {
			return bad()
		}
		low, _ := new(big.Rat).SetString(string(l.Lower))
		high, _ := new(big.Rat).SetString(string(l.Upper))
		start, _ := time.Parse(time.RFC3339Nano, l.StartedAt)
		end, _ := time.Parse(time.RFC3339Nano, l.CompletedAt)
		if low.Cmp(high) >= 0 || end.Before(start) {
			return bad()
		}
	}
	if (h.BrokerReady && (g == nil || !validDay(h.TradeDate))) || !oneOf(h.StateText, "broker_not_ready", "settlement_not_confirmed", "order_snapshot_not_ready", "trade_constraints_not_ready", "recovery_blocked", "running", "stopping") {
		return bad()
	}
	common := h.RedisReady && h.BrokerReady && h.SettlementConfirmed && h.OrderSnapshotReady && a.PositionSnapshotReady && a.OrderRefReady && a.TransportRecoveryClear && a.callbacksAllowAssessment()
	if h.AcceptingTradeCommands && (!common || !a.InstrumentUsable || !a.NativeLimitsFresh || a.SubmitAttempts != 0 || a.Static == nil || a.Limits == nil || a.NativeDepthRequestID == nil) {
		return bad()
	}
	if h.AcceptingCancelCommands && (!common || a.CancelAttempts != 0 || a.SubmitAttempts != 1) {
		return bad()
	}
	if h.StateText == "running" {
		if h.State != "UP" || (!h.AcceptingTradeCommands && !h.AcceptingCancelCommands) {
			return bad()
		}
	} else if h.State != "DEGRADED" || h.AcceptingTradeCommands || h.AcceptingCancelCommands {
		return bad()
	}
	if (h.StateText == "broker_not_ready" && h.BrokerReady) || (h.StateText == "settlement_not_confirmed" && h.SettlementConfirmed) || (h.StateText == "order_snapshot_not_ready" && h.OrderSnapshotReady) || (h.StateText == "recovery_blocked" && a.TransportRecoveryClear) || (h.StateText == "stopping" && (h.BrokerReady || h.SettlementConfirmed || h.OrderSnapshotReady)) {
		return bad()
	}
	return a, nil
}

// Only the explicit TEST assessment profile can defer historical callbacks.
// Transport/ACK safety remains an independent gate; deferred is not delivered.
func (a HeartbeatAssessment) callbacksAllowAssessment() bool {
	if a.LegacySource != nil && a.LegacySource.Diagnostics.BlockingConflict {
		return false
	}
	return a.PendingCallbacks == 0 || (a.CallbackPolicy == "local_assessment_only.v1" && a.BlockingCallbacks == 0 && a.DeferredCallbacks == a.PendingCallbacks)
}

// QueryCollection is independent of trading readiness so initialization cannot
// deadlock on the snapshots that these queries are meant to obtain.
func (h Heartbeat) queryCollection() (*int64, bool) {
	if h.ReadOnly != nil {
		return h.ReadOnly.ConnectionGeneration, h.ReadOnly.QueryCollectionReady && h.ReadOnly.TransportRecoveryClear
	}
	if a := h.Assessment; a != nil {
		return a.ConnectionGeneration, h.RedisReady && h.BrokerReady && a.TransportRecoveryClear && a.callbacksAllowAssessment() && !oneOf(h.StateText, "stopping", "recovery_blocked") && validDay(h.TradeDate)
	}
	return nil, false
}
