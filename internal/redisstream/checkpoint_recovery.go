package redisstream

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type checkpointRecoveryStore interface {
	RecoverStreamCheckpoint(context.Context, string, string, string, map[string]any) (bool, error)
	CheckpointRecoveryBlocked(context.Context, string) (bool, error)
}

func (service *RuntimeObservability) recoverCheckpointErrors(ctx context.Context, snapshot *RuntimeSnapshot) {
	store, ok := service.store.(checkpointRecoveryStore)
	if !ok || snapshot.Summary.PendingDeadLetters != 0 || len(snapshot.Errors) != 0 || snapshot.Summary.TotalLag != 0 {
		return
	}
	for i := range snapshot.Streams {
		stream := &snapshot.Streams[i]
		if stream.Role != SuffixReply || stream.Status != "error" || stream.ActiveError == nil || stream.Lag != 0 {
			continue
		}
		if code, _ := stream.ActiveError["code"].(string); code != "BROKER_NOT_READY" && code != "CHECKPOINT_ERROR" {
			continue
		}
		var gateway *GatewayRuntimeStatus
		for j := range snapshot.Gateways {
			g := &snapshot.Gateways[j]
			if g.AccountID == stream.AccountID && g.GatewayID == stream.GatewayID && g.BrokerID == stream.BrokerID {
				gateway = g
			}
		}
		if !checkpointGatewayReady(gateway) {
			continue
		}
		blocked, err := store.CheckpointRecoveryBlocked(ctx, stream.AccountID)
		if err != nil || blocked {
			continue
		}
		errorID := checkpointErrorStreamID(map[string]any{"active_error": stream.ActiveError}, stream.LastError)
		if errorID == "" {
			continue
		}
		messages, err := service.client.XRangeN(ctx, stream.StreamKey, errorID, errorID, 1).Result()
		if err != nil || len(messages) != 1 {
			continue
		}
		envelope, err := DecodeEntry(stream.StreamKey, messages[0].ID, messages[0].Values)
		if err != nil || envelope.Code != "BROKER_NOT_READY" || !strings.HasSuffix(envelope.Action, ".query") || envelope.Routing.AccountID != stream.AccountID || envelope.Routing.GatewayID != stream.GatewayID {
			continue
		}
		// Legacy checkpoints are eligible only when their entire recorded error
		// is explained by this one authoritative reply.
		if stream.LastError != envelope.StreamID+": "+indeterminateCommandSkipReason(envelope) {
			continue
		}
		errorAt := producedAtFromStreamID(envelope.StreamID)
		if !gateway.LastHeartbeatAt.After(errorAt) {
			continue
		}
		hbStream := strings.TrimSuffix(stream.StreamKey, SuffixReply) + SuffixHB
		prior, err := service.client.XRevRangeN(ctx, hbStream, envelope.StreamID, "-", 1).Result()
		if err != nil || len(prior) != 1 {
			continue
		}
		hb, err := DecodeEntry(hbStream, prior[0].ID, prior[0].Values)
		if err != nil {
			continue
		}
		var payload heartbeatPayload
		if json.Unmarshal(hb.Payload, &payload) != nil || payload.CounterSessionID != gateway.CounterSessionID {
			continue
		}
		// Require continuous session identity since the error, including the
		// freshness window before it, to reject competing or restarted processes.
		start := errorAt.Add(-time.Duration(service.cfg.Operations.HeartbeatStaleSeconds) * time.Second)
		recent, err := service.client.XRangeN(ctx, hbStream, streamIDAt(start), "+", 1000).Result()
		if err != nil || len(recent) >= 1000 {
			continue
		}
		conflict := false
		for _, message := range recent {
			hb, decodeErr := DecodeEntry(hbStream, message.ID, message.Values)
			var p heartbeatPayload
			if decodeErr != nil || json.Unmarshal(hb.Payload, &p) != nil || p.CounterSessionID != gateway.CounterSessionID {
				conflict = true
			}
		}
		if conflict {
			continue
		}
		evidence := map[string]any{
			"code": envelope.Code, "message": stream.LastError, "stream_id": envelope.StreamID,
			"account_id": stream.AccountID, "gateway_id": stream.GatewayID,
			"counter_session_id": gateway.CounterSessionID, "observed_at": errorAt,
			"first_observed_at": errorAt, "last_observed_at": errorAt,
			"recovered_at":    *gateway.LastHeartbeatAt,
			"recovery_source": "account_readiness_same_counter_session",
		}
		recovered, err := store.RecoverStreamCheckpoint(ctx, stream.StreamKey, stream.CheckpointID, stream.LastError, evidence)
		if err != nil || !recovered {
			continue
		}
		stream.ActiveError = nil
		stream.LastRecoveredError = evidence
		stream.Status = "ok"
		snapshot.Summary.StreamsAttention--
		snapshot.Summary.StreamsHealthy++
	}
}

// Legacy text supplies only an entry locator; the raw reply must still prove
// the error code, action and routing before any recovery is allowed.
func checkpointErrorStreamID(metadata map[string]any, lastError string) string {
	if active, ok := metadata["active_error"].(map[string]any); ok {
		if id, ok := active["stream_id"].(string); ok && validCheckpointStreamID(id) {
			return id
		}
	}
	if id, ok := metadata["last_error_stream_id"].(string); ok && validCheckpointStreamID(id) {
		return id
	}
	id, _, found := strings.Cut(lastError, ": ")
	if found && validCheckpointStreamID(id) {
		return id
	}
	return ""
}

func validCheckpointStreamID(id string) bool {
	ms, seq, found := strings.Cut(id, "-")
	if !found || ms == "" || seq == "" {
		return false
	}
	for _, part := range []string{ms, seq} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}

func boolReady(value *bool) bool { return value != nil && *value }

func checkpointGatewayReady(g *GatewayRuntimeStatus) bool {
	return g != nil && g.OrderEntryReady && g.Status == "online" && g.CounterSessionID != "" &&
		g.LastHeartbeatAt != nil && g.PendingTrades == 0 && g.PendingQueries == 0 &&
		boolReady(g.RedisReady) && boolReady(g.BrokerReady) && boolReady(g.OrderSnapshotReady) && boolReady(g.AcceptingTradeCommands)
}

func streamIDAt(value time.Time) string {
	return strconv.FormatInt(value.UnixMilli(), 10) + "-0"
}
