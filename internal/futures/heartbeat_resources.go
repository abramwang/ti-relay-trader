package futures

import "encoding/json"

// These are OC's local journal observations, not reserved execution capacity.
type ExecutionResourceUsage struct {
	Journal               JournalResourceUsage `json:"journal"`
	QueryCommandsReceived int64                `json:"query_commands_received"`
}

type JournalResourceUsage struct {
	Revision  int64                            `json:"revision"`
	Warning   bool                             `json:"warning"`
	Exhausted bool                             `json:"exhausted"`
	Metrics   map[string]JournalResourceMetric `json:"metrics"`
}

type JournalResourceMetric struct {
	Used      int64 `json:"used"`
	Limit     int64 `json:"limit"`
	Remaining int64 `json:"remaining"`
	Warning   bool  `json:"warning"`
	Exhausted bool  `json:"exhausted"`
}

func decodeExecutionResources(raw []byte) (*ExecutionResourceUsage, error) {
	r, err := shape(raw, []string{"journal", "query_commands_received"}, nil)
	if err != nil {
		return nil, ErrQueryPublishGate
	}
	j, err := shape(r["journal"], []string{"revision", "warning", "exhausted", "metrics"}, nil)
	if err != nil {
		return nil, ErrQueryPublishGate
	}
	keys := []string{"file_bytes", "commands", "messages", "deliveries", "conflicts", "callbacks", "events"}
	metrics, err := shape(j["metrics"], keys, nil)
	if err != nil {
		return nil, ErrQueryPublishGate
	}
	var u ExecutionResourceUsage
	if json.Unmarshal(raw, &u) != nil || u.Journal.Revision < 0 || u.QueryCommandsReceived < 0 {
		return nil, ErrQueryPublishGate
	}
	// Reject nulls/fractions explicitly rather than accepting Go zero values.
	for _, value := range []json.RawMessage{r["query_commands_received"], j["revision"]} {
		n, err := nullableInteger(value)
		if err != nil || n == nil || *n < 0 {
			return nil, ErrQueryPublishGate
		}
	}
	warning, exhausted := false, false
	for _, key := range keys {
		fields, err := shape(metrics[key], []string{"used", "limit", "remaining", "warning", "exhausted"}, nil)
		if err != nil {
			return nil, ErrQueryPublishGate
		}
		for _, field := range []string{"used", "limit", "remaining"} {
			n, err := nullableInteger(fields[field])
			if err != nil || n == nil || *n < 0 {
				return nil, ErrQueryPublishGate
			}
		}
		limit := int64(10000)
		if key == "commands" {
			limit = 2000
		}
		if key == "file_bytes" {
			limit = 64 * 1024 * 1024
		}
		m := u.Journal.Metrics[key]
		remaining := int64(0)
		if m.Used < limit {
			remaining = limit - m.Used
		}
		if m.Limit != limit || m.Remaining != remaining || !resourceFlag(fields["warning"], m.Used >= (limit*4+4)/5) || !resourceFlag(fields["exhausted"], m.Used >= limit) {
			return nil, ErrQueryPublishGate
		}
		warning, exhausted = warning || m.Warning, exhausted || m.Exhausted
	}
	if !resourceFlag(j["warning"], warning) || !resourceFlag(j["exhausted"], exhausted) {
		return nil, ErrQueryPublishGate
	}
	return &u, nil
}

func resourceFlag(raw json.RawMessage, expected bool) bool {
	return expected && literal(raw, "true") || !expected && literal(raw, "false")
}

func (a *HeartbeatAssessment) decodeResidentDiagnostics(raw rawObject, h Heartbeat) error {
	if h.Operations.QueryReason == "query_command_limit" {
		return ErrQueryPublishGate
	}
	if b, exists := raw["runtime_blocked_reason"]; exists {
		if literal(b, "null") || json.Unmarshal(b, &a.RuntimeBlockedReason) != nil || len(a.RuntimeBlockedReason) > 4096 {
			return ErrQueryPublishGate
		}
		if a.RuntimeBlockedReason != "" && (!oneOf(h.StateText, "recovery_blocked", "stopping") || h.AcceptingTradeCommands || h.AcceptingCancelCommands || h.Operations.AcceptingQueryCommands) {
			return ErrQueryPublishGate
		}
	}
	if b, exists := raw["resource_usage"]; exists {
		var err error
		a.ResourceUsage, err = decodeExecutionResources(b)
		if err != nil {
			return err
		}
	}
	exhausted := a.ResourceUsage != nil && a.ResourceUsage.Journal.Exhausted
	if exhausted && (h.AcceptingTradeCommands || h.AcceptingCancelCommands || h.Operations.AcceptingQueryCommands) {
		return ErrQueryPublishGate
	}
	for _, reason := range []string{h.Operations.QueryReason, h.Operations.SubmitReason, h.Operations.CancelReason} {
		if reason == "journal_capacity_exhausted" && !exhausted {
			return ErrQueryPublishGate
		}
	}
	return nil
}
