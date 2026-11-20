package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type CurrentBaselineHeartbeat struct {
	ArchiveID      string     `json:"archive_id"`
	EntryID        string     `json:"entry_id"`
	BodySHA256     string     `json:"body_sha256"`
	ReceivedAt     time.Time  `json:"received_at"`
	ProducedAt     *time.Time `json:"produced_at"`
	ContextMatched bool       `json:"context_matched"`
	BrokerReady    *bool      `json:"broker_ready"`
	RedisReady     *bool      `json:"redis_ready"`
}

type CurrentBaselineCheckpoint struct {
	Stream          string          `json:"stream"`
	Role            string          `json:"role"`
	Found           bool            `json:"found"`
	LastStreamID    string          `json:"last_stream_id"`
	LastProcessedAt *time.Time      `json:"last_processed_at"`
	ErrorCount      *string         `json:"error_count"`
	LastError       string          `json:"last_error"`
	ActiveError     json.RawMessage `json:"active_error"`
}

// These are observations of the local archive/worker, not a Redis source epoch,
// a page-count proof, or a broker sampling/event inclusion watermark.
type CurrentBaselineSourceHealth struct {
	ObservedAt               time.Time                           `json:"observed_at"`
	TrackingAvailable        bool                                `json:"tracking_available"`
	ContinuityVerified       bool                                `json:"continuity_verified"`
	Heartbeat                *CurrentBaselineHeartbeat           `json:"heartbeat"`
	Checkpoints              []CurrentBaselineCheckpoint         `json:"checkpoints"`
	Reasons                  []string                            `json:"reasons"`
	ReceiptTrackingAvailable bool                                `json:"receipt_tracking_available"`
	Receipts                 []CurrentBaselineReceiptObservation `json:"durable_receipts"`
}

func (h *CurrentBaselineSourceHealth) reason(s string) {
	for _, old := range h.Reasons {
		if old == s {
			return
		}
	}
	h.Reasons = append(h.Reasons, s)
}

func currentBaselineStreamPrefix(scope CurrentBaselineScope) string {
	env := scope.Environment
	if env == "production" {
		env = "prod"
	}
	return fmt.Sprintf("relay:%s:v1:%s:%s", env, scope.BrokerID, scope.GatewayID)
}

// CompareCurrentBaselineStreamIDs compares numeric components only. It makes no
// claim that IDs are contiguous or that all entries between them were retained.
func CompareCurrentBaselineStreamIDs(a, b string) (int, error) {
	parse := func(s string) ([2]uint64, error) {
		var out [2]uint64
		if s == "0" {
			return out, nil
		}
		parts := strings.Split(s, "-")
		if len(parts) != 2 {
			return out, ErrCurrentBaselineInput
		}
		for i, p := range parts {
			n, err := strconv.ParseUint(p, 10, 64)
			if err != nil || strconv.FormatUint(n, 10) != p {
				return out, ErrCurrentBaselineInput
			}
			out[i] = n
		}
		return out, nil
	}
	x, err := parse(a)
	if err != nil {
		return 0, err
	}
	y, err := parse(b)
	if err != nil {
		return 0, err
	}
	for i := range x {
		if x[i] < y[i] {
			return -1, nil
		}
		if x[i] > y[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func currentBaselineHealthTracking(ctx context.Context, q currentBaselineReader) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM (VALUES
('raw_stream_messages','account_baseline_heartbeat_watch','observe_account_baseline_heartbeat',29),
('stream_checkpoints','account_baseline_checkpoint_watch','observe_account_baseline_checkpoint',29),
('stream_checkpoints','account_baseline_checkpoint_truncate','observe_account_baseline_checkpoint',32),
('account_baseline_source_contexts','account_baseline_revision_watch','observe_account_baseline_revision',29),
('account_baseline_source_contexts','account_baseline_revision_truncate','observe_account_baseline_revision',32)
) AS required(tbl,trig,fn,kind)
JOIN pg_class c ON c.oid=to_regclass(required.tbl)
JOIN pg_trigger t ON t.tgrelid=c.oid AND t.tgname=required.trig AND t.tgtype=required.kind
JOIN pg_proc p ON p.oid=t.tgfoid AND p.pronamespace=c.relnamespace
WHERE t.tgenabled IN ('O','A') AND p.proname=required.fn AND p.pronargs=0`).Scan(&count)
	return count == 5, err
}

func readCurrentBaselineHealth(ctx context.Context, q currentBaselineReader, scope CurrentBaselineScope, at time.Time) (*CurrentBaselineSourceHealth, error) {
	h := &CurrentBaselineSourceHealth{ObservedAt: at, Checkpoints: []CurrentBaselineCheckpoint{}, Reasons: []string{}}
	var err error
	h.TrackingAvailable, err = currentBaselineHealthTracking(ctx, q)
	if err != nil {
		return nil, err
	}
	if !h.TrackingAvailable {
		h.reason("source_health_tracking_unavailable")
	}
	prefix := currentBaselineStreamPrefix(scope)
	var r CurrentBaselineArchive
	var account string
	var size int
	err = q.QueryRowContext(ctx, `SELECT raw_message_pk::text,stream_id,direction,coalesce(account_id,''),
CASE WHEN octet_length(body_text) <= 1048576 THEN body_text ELSE '' END,
coalesce(parse_error,''),received_at,coalesce(octet_length(body_text),0)
FROM raw_stream_messages WHERE stream_key=$1 AND stream_role='hb'
ORDER BY received_at DESC,raw_stream_messages.raw_message_pk DESC LIMIT 1`, prefix+":hb").Scan(&r.ArchiveID, &r.EntryID, &r.Direction, &account, &r.Body, &r.ParseError, &r.ReceivedAt, &size)
	if errors.Is(err, sql.ErrNoRows) {
		h.reason("heartbeat_missing")
	} else if err != nil {
		return nil, err
	} else if size > 1<<20 {
		return nil, ErrCurrentBaselineLimit
	} else {
		reviewCurrentBaselineHeartbeat(h, scope, r, account)
	}
	for _, role := range []string{"reply", "event", "hb", "dlq"} {
		c := CurrentBaselineCheckpoint{Stream: prefix + ":" + role, Role: role, ActiveError: json.RawMessage("null")}
		var storedRole, count string
		var processed sql.NullTime
		var metadata []byte
		err = q.QueryRowContext(ctx, `SELECT stream_role,last_stream_id,last_processed_at,error_count::text,last_error,
CASE WHEN octet_length(metadata::text) <= 1048576 THEN metadata::text ELSE NULL END
FROM stream_checkpoints WHERE stream_key=$1`, c.Stream).Scan(&storedRole, &c.LastStreamID, &processed, &count, &c.LastError, &metadata)
		if errors.Is(err, sql.ErrNoRows) {
			h.reason(role + "_checkpoint_missing")
		} else if err != nil {
			return nil, err
		} else {
			if metadata == nil {
				return nil, ErrCurrentBaselineLimit
			}
			c.Found, c.ErrorCount = true, &count
			if processed.Valid {
				c.LastProcessedAt = &processed.Time
			}
			if storedRole != role {
				h.reason(role + "_checkpoint_role_mismatch")
			}
			if _, e := CompareCurrentBaselineStreamIDs(c.LastStreamID, "0"); e != nil {
				h.reason(role + "_checkpoint_invalid_cursor")
			}
			if !processed.Valid {
				h.reason(role + "_checkpoint_processing_time_missing")
			} else if processed.Time.After(at) {
				h.reason(role + "_checkpoint_time_in_future")
			}
			var m map[string]json.RawMessage
			if json.Unmarshal(metadata, &m) != nil {
				return nil, ErrCurrentBaselineCorrupt
			}
			if value, ok := m["active_error"]; ok {
				c.ActiveError = value
				if string(value) != "null" {
					h.reason(role + "_checkpoint_active_error")
				}
			} else if c.LastError != "" {
				h.reason(role + "_checkpoint_active_error")
			}
		}
		h.Checkpoints = append(h.Checkpoints, c)
	}
	if h.Heartbeat != nil && h.Checkpoints[2].Found {
		cmp, e := CompareCurrentBaselineStreamIDs(h.Checkpoints[2].LastStreamID, h.Heartbeat.EntryID)
		if e != nil || cmp < 0 {
			h.reason("heartbeat_checkpoint_not_caught_up")
		}
	}
	h.reason("source_continuity_unverified")
	h.reason("heartbeat_freshness_policy_unset")
	if err = readCurrentBaselineReceipts(ctx, q, scope, h); err != nil {
		return nil, err
	}
	return h, nil
}

func reviewCurrentBaselineHeartbeat(h *CurrentBaselineSourceHealth, scope CurrentBaselineScope, r CurrentBaselineArchive, account string) {
	b := &CurrentBaselineHeartbeat{ArchiveID: r.ArchiveID, EntryID: r.EntryID, BodySHA256: baselineHash([]byte(r.Body)), ReceivedAt: r.ReceivedAt}
	h.Heartbeat = b
	var doc map[string]json.RawMessage
	if r.Body == "" || r.ParseError != "" || ValidateCurrentBaselineSourceJSON([]byte(r.Body)) != nil || json.Unmarshal([]byte(r.Body), &doc) != nil {
		h.reason("heartbeat_original_invalid")
		return
	}
	if _, err := CompareCurrentBaselineStreamIDs(r.EntryID, "0"); err != nil {
		h.reason("heartbeat_invalid_entry_id")
	}
	if account != scope.AccountID || r.Direction != "out" {
		h.reason("heartbeat_archive_scope_mismatch")
	}
	str := func(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
	if str(doc["protocol"]) != "relay.stream.v1" || str(doc["message_type"]) != "heartbeat" || str(doc["message_id"]) == "" {
		h.reason("heartbeat_envelope_invalid")
	}
	var routing map[string]string
	_ = json.Unmarshal(doc["routing"], &routing)
	env := scope.Environment
	if env == "production" {
		env = "prod"
	}
	if routing["env"] != env || routing["broker_id"] != scope.BrokerID || routing["gateway_id"] != scope.GatewayID || routing["account_id"] != scope.AccountID {
		h.reason("heartbeat_routing_mismatch")
	}
	produced, e := time.Parse(time.RFC3339Nano, str(doc["produced_at"]))
	if e != nil || produced.IsZero() {
		h.reason("heartbeat_publication_time_missing")
	} else {
		b.ProducedAt = &produced
		if produced.After(h.ObservedAt) {
			h.reason("heartbeat_time_in_future")
		}
	}
	if r.ReceivedAt.IsZero() || r.ReceivedAt.After(h.ObservedAt) {
		h.reason("heartbeat_receive_time_invalid")
	}
	layers := []map[string]json.RawMessage{doc}
	for _, key := range []string{"payload", "adapter_context"} {
		var v map[string]json.RawMessage
		if raw, ok := doc[key]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, &v) != nil || v == nil {
				h.reason("heartbeat_context_invalid")
			} else {
				layers = append(layers, v)
			}
		}
	}
	matched := account == scope.AccountID && r.Direction == "out" && routing["env"] == env &&
		routing["broker_id"] == scope.BrokerID && routing["gateway_id"] == scope.GatewayID && routing["account_id"] == scope.AccountID
	for _, field := range []string{"counter_session_id", "connection_generation", "trade_date"} {
		found := false
		for _, layer := range layers {
			raw, ok := layer[field]
			if !ok {
				continue
			}
			found = true
			valid := false
			switch field {
			case "counter_session_id":
				valid = str(raw) == scope.CounterSessionID
			case "trade_date":
				valid = str(raw) == scope.TradeDate
			case "connection_generation":
				var n int64
				valid = json.Unmarshal(raw, &n) == nil && scope.ConnectionGeneration != nil && n == *scope.ConnectionGeneration
			}
			if !valid {
				matched = false
				h.reason("heartbeat_" + field + "_mismatch")
			}
		}
		if !found {
			matched = false
			h.reason("heartbeat_" + field + "_missing")
		}
	}
	b.ContextMatched = matched
	for _, field := range []string{"broker_ready", "redis_ready"} {
		var value *bool
		conflict := false
		for _, layer := range layers {
			raw, ok := layer[field]
			if !ok {
				continue
			}
			var v *bool
			if json.Unmarshal(raw, &v) != nil || v == nil {
				conflict = true
				continue
			}
			if value != nil && *value != *v {
				conflict = true
			}
			value = v
		}
		if conflict {
			value = nil
			h.reason("heartbeat_" + field + "_invalid")
		}
		if value == nil {
			h.reason("heartbeat_" + field + "_missing")
		} else if !*value {
			h.reason("heartbeat_" + field + "_false")
		}
		if field == "broker_ready" {
			b.BrokerReady = value
		} else {
			b.RedisReady = value
		}
	}
	for _, layer := range layers {
		switch strings.ToLower(str(layer["state"])) {
		case "stopping", "stopped", "degraded", "recovery_blocked", "disconnected":
			h.reason("heartbeat_state_unavailable")
		}
	}
}
