package futures

import (
	"encoding/json"
	"time"

	"ti-relay-trader/internal/timeutil"
)

func (a TestTradeApproval) executionActionAllowed(action string) bool {
	if !a.relayExecution() || a.ExecutionPermission == nil {
		return false
	}
	return action == "order.submit" && a.ExecutionPermission.Submit || action == "order.cancel" && a.ExecutionPermission.Cancel
}

// This is Relay's deliberately limited Dongwu TEST daytime policy, not a
// general SHFE calendar, night-session policy or an OC authorization window.
func relayExecutionTradeGate(route QueryRoute, target TestQueryTarget, a TestTradeApproval, h Heartbeat, now time.Time) (time.Time, error) {
	bad := func() (time.Time, error) { return time.Time{}, ErrQueryPublishGate }
	s := h.Assessment
	if route.Env != "test" || route.BrokerID != "ctp_4900" || route.AccountID != "300052141" || s == nil || !s.relayExecution() {
		return bad()
	}
	action := "order.submit"
	if a.CancelReceiptID > 0 {
		action = "order.cancel"
	}
	if !a.executionActionAllowed(action) {
		return bad()
	}
	local := now.In(timeutil.Location())
	segment := assessmentDaySegment(local)
	if local.Format("20060102") != target.TradeDate || segment == 0 {
		return bad()
	}
	endMinute := []int{615, 690, 900}[segment-1]
	return time.Date(local.Year(), local.Month(), local.Day(), endMinute/60, endMinute%60, 0, 0, local.Location()), nil
}

func relayExecutionCommandDeadline(raw []byte, a TestTradeApproval, now time.Time) (time.Time, error) {
	var command struct {
		Action string `json:"action"`
		SentAt string `json:"sent_at"`
	}
	if json.Unmarshal(raw, &command) != nil || commandDigest(raw) != a.CommandSHA256 || !a.executionActionAllowed(command.Action) {
		return time.Time{}, ErrQueryPublishGate
	}
	sent, err := time.Parse(time.RFC3339Nano, command.SentAt)
	if err != nil || sent.After(now) || !now.Before(sent.Add(5*time.Second)) {
		return time.Time{}, ErrQueryPublishGate
	}
	return sent.Add(5 * time.Second), nil
}
