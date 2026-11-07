package futures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Inspection does not require native login identity: it never activates a guard,
// registers a receiver, or interprets a heartbeat as permission to use the SDK.
type HeartbeatInspectionRequest struct {
	Route QueryRoute `json:"route"`
	After string     `json:"after"`
	Limit int        `json:"limit"`
}

func (r HeartbeatInspectionRequest) Stream() (string, error) {
	if r.Route.Env != "test" || !routeBrokerPattern.MatchString(r.Route.BrokerID) ||
		!(r.Route.BrokerID == "ctp" || strings.HasPrefix(r.Route.BrokerID, "ctp_")) ||
		!accountPattern.MatchString(r.Route.AccountID) || !validID(r.Route.GatewayID) || len(r.Route.GatewayID) > 128 {
		return "", ErrStreamIdentity
	}
	if _, err := streamID(r.After); err != nil {
		return "", err
	}
	if r.Limit < 1 || r.Limit > 256 {
		return "", ErrAuditCapacity
	}
	return strings.TrimSuffix(queryReplyStream(r.Route), "reply") + "hb", nil
}

type AdvertisedHeartbeat struct {
	SessionID               string    `json:"counter_session_id"`
	TradeDate               *string   `json:"trade_date"`
	ProducedAt              time.Time `json:"produced_at"`
	AgeMilliseconds         int64     `json:"age_ms"`
	State                   string    `json:"state,omitempty"`
	StateText               string    `json:"state_text,omitempty"`
	RedisReady              bool      `json:"redis_ready"`
	BrokerReady             bool      `json:"broker_ready"`
	SettlementConfirmed     bool      `json:"settlement_confirmed"`
	OrderSnapshotReady      bool      `json:"order_snapshot_ready"`
	AcceptingTradeCommands  bool      `json:"accepting_trade_commands"`
	AcceptingCancelCommands bool      `json:"accepting_cancel_commands"`
	RecoveryClear           *bool     `json:"transport_recovery_clear,omitempty"`
	RecoveryIssues          *int64    `json:"recovery_issue_count,omitempty"`
}

type InspectedHeartbeat struct {
	EntryID        string               `json:"entry_id"`
	BodySHA256     string               `json:"body_sha256"`
	BodyBytes      int                  `json:"body_bytes"`
	Classification string               `json:"classification"`
	Advertised     *AdvertisedHeartbeat `json:"advertised,omitempty"`
}

type HeartbeatInspection struct {
	Mode               string               `json:"mode"`
	Stream             string               `json:"stream"`
	Route              QueryRoute           `json:"route"`
	StartedAt          time.Time            `json:"started_at"`
	FinishedAt         time.Time            `json:"finished_at"`
	After              string               `json:"after"`
	NextAfter          string               `json:"next_after"`
	ObservedTail       string               `json:"observed_tail"`
	MoreAtObservedTail bool                 `json:"more_at_observed_tail"`
	BusinessAuthorized bool                 `json:"business_authorized"`
	Records            []InspectedHeartbeat `json:"records"`
}

func inspectionReadError(stage string, err error) error {
	code := "redis_unavailable"
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		code = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		code = "timeout"
	case redis.HasErrorPrefix(err, "no such key"):
		code = "stream_missing"
	case redis.HasErrorPrefix(err, "WRONGTYPE"):
		code = "wrong_stream_type"
	case redis.HasErrorPrefix(err, "NOAUTH"), redis.HasErrorPrefix(err, "WRONGPASS"):
		code = "authentication_failed"
	case redis.HasErrorPrefix(err, "NOPERM"):
		code = "permission_denied"
	case errors.As(err, &network) && network.Timeout():
		code = "timeout"
	}
	return fmt.Errorf("heartbeat inspection %s: %s", stage, code)
}

// InspectTestHeartbeats reads one bounded page, including malformed/disconnected
// bodies. NextAfter is a diagnostic pagination token, never a durable checkpoint.
// XINFO checks observable loss only, not Redis restore/recreation or OC identity.
func InspectTestHeartbeats(ctx context.Context, reader StreamReadClient, req HeartbeatInspectionRequest) (HeartbeatInspection, error) {
	key, err := req.Stream()
	if err != nil {
		return HeartbeatInspection{}, err
	}
	deadline, ok := ctx.Deadline()
	if reader == nil || !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
		return HeartbeatInspection{}, ErrStreamIdentity
	}
	if err := ctx.Err(); err != nil {
		return HeartbeatInspection{}, err
	}
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	started := time.Now().In(zone)
	before, err := reader.XInfoStream(ctx, key).Result()
	if err != nil {
		return HeartbeatInspection{}, inspectionReadError("metadata", err)
	}
	if err = checkStreamHistory(before, req.After); err != nil {
		return HeartbeatInspection{}, err
	}
	raw, err := reader.ReadStream(ctx, key, req.After, req.Limit)
	if err != nil && !errors.Is(err, redis.Nil) {
		return HeartbeatInspection{}, inspectionReadError("read", err)
	}
	if errors.Is(err, redis.Nil) {
		raw = nil
	}
	inputs, err := decodeStreamRead(key, req.After, req.Limit, raw)
	if err != nil {
		return HeartbeatInspection{}, err
	}
	after, err := reader.XInfoStream(ctx, key).Result()
	if err != nil {
		return HeartbeatInspection{}, inspectionReadError("final_metadata", err)
	}
	if err = checkStreamHistory(after, req.After); err != nil {
		return HeartbeatInspection{}, err
	}
	cmp, err := compareStreamID(after.LastGeneratedID, before.LastGeneratedID)
	if err != nil || cmp < 0 {
		return HeartbeatInspection{}, ErrStreamHistory
	}
	out := HeartbeatInspection{Mode: "test_heartbeat_inspection_only", Stream: key, Route: req.Route,
		StartedAt: started, FinishedAt: time.Now().In(zone), After: req.After, NextAfter: req.After,
		ObservedTail: after.LastGeneratedID, Records: make([]InspectedHeartbeat, 0, len(inputs))}
	for _, input := range inputs {
		digest := sha256.Sum256(input.Raw)
		item := InspectedHeartbeat{EntryID: input.EntryID, BodySHA256: hex.EncodeToString(digest[:]), BodyBytes: len(input.Raw), Classification: "invalid_heartbeat"}
		h, err := DecodeHeartbeat(input.Raw, req.Route)
		if err == nil {
			item.Classification = "decoded_unverified"
			a := &AdvertisedHeartbeat{SessionID: h.CounterSessionID, ProducedAt: h.ProducedAt.In(zone), AgeMilliseconds: out.FinishedAt.Sub(h.ProducedAt).Milliseconds(),
				State: h.State, StateText: h.StateText, RedisReady: h.RedisReady, BrokerReady: h.BrokerReady,
				SettlementConfirmed: h.SettlementConfirmed, OrderSnapshotReady: h.OrderSnapshotReady,
				AcceptingTradeCommands: h.AcceptingTradeCommands, AcceptingCancelCommands: h.AcceptingCancelCommands}
			if h.Recovery == nil {
				a.TradeDate = &h.TradeDate
			} else {
				item.Classification = "transport_recovery_only"
				a.RecoveryClear, a.RecoveryIssues = &h.Recovery.TransportRecoveryClear, &h.Recovery.RecoveryIssueCount
			}
			item.Advertised = a
		}
		out.Records = append(out.Records, item)
		out.NextAfter = input.EntryID
	}
	cmp, err = compareStreamID(out.NextAfter, after.LastGeneratedID)
	if err != nil || cmp > 0 {
		return HeartbeatInspection{}, ErrStreamHistory
	}
	out.MoreAtObservedTail = cmp < 0
	if err := ctx.Err(); err != nil {
		return HeartbeatInspection{}, err
	}
	return out, nil
}
