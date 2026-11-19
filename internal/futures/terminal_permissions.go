package futures

import (
	"time"

	"ti-relay-trader/internal/timeutil"
)

// Display-only eligibility, never a reusable command permission. Publication
// still checks the current raw heartbeat, source binding and order evidence.
type TerminalExecutionView struct {
	SubmitAuthorized bool      `json:"submit_authorized"`
	CancelAuthorized bool      `json:"cancel_authorized"`
	SubmitEnabled    bool      `json:"submit_enabled"`
	CancelEnabled    bool      `json:"cancel_enabled"`
	ValidUntil       time.Time `json:"valid_until"`
	TradeDate        string    `json:"trade_date"`
	PriceSource      string    `json:"price_source"`
	Reason           string    `json:"reason"`
}

func (s *TestOrderService) TerminalExecution(account TerminalAccount, now time.Time) TerminalExecutionView {
	v := TerminalExecutionView{Reason: "relay_account_read_only", PriceSource: "meridian_futures_snapshot.v1"}
	a, ok := s.accounts[account.AccountID]
	if !ok || a.Source.Scope.Route != account.Route || !now.Before(a.AuthorizationExpiresAt) {
		return v
	}
	v.SubmitAuthorized, v.CancelAuthorized = a.SubmitEnabled, a.CancelEnabled
	if account.Capture == nil || !account.Capture.Ready || !account.Capture.ValidUntil.After(now) {
		v.Reason = "relay_capture_unavailable"
		return v
	}
	h := account.Heartbeat
	v.Reason = "broker_not_ready"
	if account.ConnectionError != "" || account.LedgerError != "" || account.LocalOrderError != "" || account.ConnectionState != "execution" || h == nil || h.AuthorizationOwner != "relay" || h.SessionID == "" || h.TradeDate == nil || h.ConnectionGeneration == nil || *h.ConnectionGeneration < 1 || h.ProducedAt.After(now) || !now.Before(h.ProducedAt.Add(5*time.Second)) || !h.RedisReady || !h.BrokerReady || !h.SettlementConfirmed || h.RecoveryClear == nil || !*h.RecoveryClear || h.RuntimeBlockedReason != "" || h.StateText == "stopping" || h.State == "STOPPED" {
		return v
	}
	v.TradeDate = *h.TradeDate
	v.Reason = "outside_test_session"
	local := now.In(timeutil.Location())
	segment := assessmentDaySegment(local)
	if segment == 0 || local.Format("20060102") != v.TradeDate {
		return v
	}
	endMinute := []int{615, 690, 900}[segment-1]
	end := time.Date(local.Year(), local.Month(), local.Day(), endMinute/60, endMinute%60, 0, 0, local.Location())
	v.ValidUntil = h.ProducedAt.Add(5 * time.Second)
	for _, limit := range []time.Time{a.AuthorizationExpiresAt, end, account.Capture.ValidUntil} {
		if limit.Before(v.ValidUntil) {
			v.ValidUntil = limit
		}
	}
	v.SubmitEnabled = a.SubmitEnabled && h.AcceptingTradeCommands
	v.CancelEnabled = a.CancelEnabled && h.AcceptingCancelCommands
	v.Reason = ""
	if !v.SubmitEnabled && !v.CancelEnabled {
		v.Reason = "relay_or_counter_read_only"
	}
	return v
}
