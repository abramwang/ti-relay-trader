package futures

import (
	"encoding/json"
	"time"

	"ti-relay-trader/internal/timeutil"
)

// Lifecycle describes OC's process window, not a Relay order authorization.
type AssessmentLifecycle struct {
	Mode                   string    `json:"mode"`
	StartsAt               time.Time `json:"starts_at"`
	ExpiresAt              time.Time `json:"expires_at"`
	Phase                  string    `json:"phase"`
	ProcessDurationSeconds int64     `json:"process_duration_seconds"`
}

func assessmentDaySegment(t time.Time) int {
	t = t.In(timeutil.Location())
	m := t.Hour()*60 + t.Minute()
	for i, span := range [][2]int{{540, 615}, {630, 690}, {810, 900}} {
		if m >= span[0] && m < span[1] {
			return i + 1
		}
	}
	return 0
}

func (a *HeartbeatAssessment) decodeLifecycle(raw rawObject, h Heartbeat, route QueryRoute) error {
	if profile, exists := raw["permission_profile"]; exists {
		if json.Unmarshal(profile, &a.PermissionProfile) != nil || a.PermissionProfile != "test_day_relay.v1" || route.Env != "test" || route.BrokerID != "ctp_4900" || route.AccountID != "300052141" {
			return ErrQueryPublishGate
		}
		if !literal(raw["per_command_native_validation"], "true") || !native(textField(raw, "constraint_symbol"), 80, false) {
			return ErrQueryPublishGate
		}
		a.ConstraintSymbol = textField(raw, "constraint_symbol")
		for key, dst := range map[string]*bool{"trade_preparation_pending": &a.TradePreparationPending, "trade_response_pending": &a.TradeResponsePending} {
			if !literal(raw[key], "true") && !literal(raw[key], "false") {
				return ErrQueryPublishGate
			}
			*dst = literal(raw[key], "true")
		}
	} else {
		for _, key := range []string{"constraint_symbol", "per_command_native_validation", "trade_preparation_pending", "trade_response_pending"} {
			if _, exists := raw[key]; exists {
				return ErrQueryPublishGate
			}
		}
	}
	b, exists := raw["process_lifecycle"]
	if !exists {
		if a.PermissionProfile != "" {
			return ErrQueryPublishGate
		}
		return nil
	}
	if _, err := shape(b, []string{"mode", "starts_at", "expires_at", "phase", "process_duration_seconds"}, nil); err != nil {
		return err
	}
	var l AssessmentLifecycle
	if json.Unmarshal(b, &l) != nil || l.Mode != "scheduled_assessment.v1" || !oneOf(l.Phase, "waiting", "active", "expired") || l.ProcessDurationSeconds < 1 || l.ProcessDurationSeconds > 86400 || !l.ExpiresAt.Equal(a.ExpiresAt) || !l.StartsAt.Before(l.ExpiresAt) {
		return ErrQueryPublishGate
	}
	_, startOffset := l.StartsAt.Zone()
	_, endOffset := l.ExpiresAt.Zone()
	start, end := l.StartsAt.In(timeutil.Location()), l.ExpiresAt.In(timeutil.Location())
	if startOffset != 8*3600 || endOffset != 8*3600 || start.Nanosecond() != 0 || end.Nanosecond() != 0 || start.Format("20060102") != end.Format("20060102") || (h.TradeDate != "" && h.TradeDate != start.Format("20060102")) {
		return ErrQueryPublishGate
	}
	if a.PermissionProfile == "" {
		if end.Sub(start) > 300*time.Second || assessmentDaySegment(start) == 0 || assessmentDaySegment(start) != assessmentDaySegment(end.Add(-time.Nanosecond)) {
			return ErrQueryPublishGate
		}
	} else if start.Hour()*60+start.Minute() < 540 || end.Hour()*60+end.Minute() > 900 || (end.Hour() == 15 && end.Second() != 0) {
		return ErrQueryPublishGate
	}
	a.Lifecycle = &l
	active := a.windowOpen(h.ProducedAt)
	if a.PermissionProfile == "test_day_relay.v1" {
		// The process can remain active at lunch; only trading uses the exchange
		// segments. Explicit query readiness is validated separately.
		active = !h.ProducedAt.Before(l.StartsAt) && h.ProducedAt.Before(l.ExpiresAt)
	}
	if (l.Phase == "active" && !active) || ((h.AcceptingTradeCommands || h.AcceptingCancelCommands) && !a.windowOpen(h.ProducedAt)) {
		return ErrQueryPublishGate
	}
	return nil
}

func (a HeartbeatAssessment) windowOpen(now time.Time) bool {
	if a.Lifecycle == nil {
		return now.Before(a.ExpiresAt)
	}
	l := a.Lifecycle
	return l.Phase == "active" && !now.Before(l.StartsAt) && now.Before(l.ExpiresAt) && assessmentDaySegment(now) != 0
}
