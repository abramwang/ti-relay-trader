package redisstream

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"ti-relay-trader/internal/ledger"
)

type LedgerSourceObservation struct {
	SourceID        string    `json:"source_id"`
	ServerRunID     string    `json:"server_run_id"`
	RedisDB         int       `json:"redis_db"`
	ObservedAt      time.Time `json:"observed_at"`
	Length          int64     `json:"length"`
	EntriesAdded    int64     `json:"entries_added"`
	FirstID         string    `json:"first_id"`
	LastGeneratedID string    `json:"last_generated_id"`
	MaxDeletedID    string    `json:"max_deleted_id"`
	AnchorSHA256    string    `json:"anchor_sha256"`
}

// Read-only transport surface, intentionally without publishing or ACK methods.
type LedgerReceiptReader interface {
	Observe(context.Context, string, string) (LedgerSourceObservation, error)
	Read(context.Context, string, string, int) (any, error)
}

type RedisLedgerReceiptReader struct {
	client   *redis.Client
	sourceID string
}

func NewRedisLedgerReceiptReader(client *redis.Client, sourceID string) (*RedisLedgerReceiptReader, error) {
	if client == nil || client.Options().Protocol != 2 || sourceID == "" {
		return nil, errors.New("dedicated RESP2 Redis client and configured source identity required")
	}
	return &RedisLedgerReceiptReader{client, sourceID}, nil
}

func (r *RedisLedgerReceiptReader) Observe(ctx context.Context, key, cursor string) (LedgerSourceObservation, error) {
	out := LedgerSourceObservation{SourceID: r.sourceID, RedisDB: r.client.Options().DB, ObservedAt: time.Now().UTC()}
	info, err := r.client.Info(ctx, "server").Result()
	if err != nil {
		return out, err
	}
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "run_id:") {
			out.ServerRunID = strings.TrimPrefix(line, "run_id:")
		}
	}
	x, err := r.client.XInfoStream(ctx, key).Result()
	if err != nil {
		return out, err
	}
	out.Length = x.Length
	out.EntriesAdded = x.EntriesAdded
	out.FirstID = x.FirstEntry.ID
	out.LastGeneratedID = x.LastGeneratedID
	out.MaxDeletedID = x.MaxDeletedEntryID
	if cursor != "0-0" {
		response, err := r.client.Do(ctx, "XRANGE", key, cursor, cursor, "COUNT", 1).Result()
		if err != nil {
			return out, err
		}
		rows, ok := response.([]any)
		if !ok || len(rows) != 1 {
			return out, errors.New("retained cursor entry missing")
		}
		entry, ok := rows[0].([]any)
		if !ok || len(entry) != 2 || entry[0] != cursor {
			return out, errors.New("retained cursor entry invalid")
		}
		body, err := ledgerReceiptBody(entry[1])
		if err != nil {
			return out, err
		}
		sum := sha256.Sum256(body)
		out.AnchorSHA256 = hex.EncodeToString(sum[:])
	}
	return out, nil
}

func (r *RedisLedgerReceiptReader) Read(ctx context.Context, key, cursor string, limit int) (any, error) {
	if limit < 1 || limit > 256 {
		return nil, ledger.ErrCurrentBaselineLimit
	}
	return r.client.Do(ctx, "XREAD", "COUNT", limit, "STREAMS", key, cursor).Result()
}

func ledgerReceiptBody(value any) ([]byte, error) {
	fields, ok := value.([]any)
	if !ok || len(fields) != 2 || fields[0] != "body" {
		return nil, errors.New("expected a sole Redis body field")
	}
	switch b := fields[1].(type) {
	case string:
		return []byte(b), nil
	case []byte:
		return append([]byte(nil), b...), nil
	default:
		return nil, errors.New("Redis body is not bytes")
	}
}

func decodeLedgerRead(key, cursor string, limit int, response any) ([]ledger.LedgerStreamInput, error) {
	out := []ledger.LedgerStreamInput{}
	if response == nil {
		return out, nil
	}
	streams, ok := response.([]any)
	if !ok || len(streams) != 1 {
		return nil, errors.New("unexpected Redis stream response")
	}
	stream, ok := streams[0].([]any)
	if !ok || len(stream) != 2 || stream[0] != key {
		return nil, ledger.ErrLedgerCaptureBinding
	}
	rows, ok := stream[1].([]any)
	if !ok || len(rows) == 0 || len(rows) > limit {
		return nil, ledger.ErrCurrentBaselineLimit
	}
	last := cursor
	size := 0
	for _, v := range rows {
		row, ok := v.([]any)
		if !ok || len(row) != 2 {
			return nil, errors.New("invalid Redis entry")
		}
		id, ok := row[0].(string)
		if !ok {
			return nil, ledger.ErrLedgerCaptureCursor
		}
		cmp, err := ledger.CompareCurrentBaselineStreamIDs(id, last)
		if err != nil || cmp <= 0 {
			return nil, ledger.ErrLedgerCaptureCursor
		}
		body, err := ledgerReceiptBody(row[1])
		if err != nil {
			return nil, err
		}
		size += len(body)
		if size > 4<<20 {
			return nil, ledger.ErrCurrentBaselineLimit
		}
		out = append(out, ledger.LedgerStreamInput{EntryID: id, Body: body})
		last = id
	}
	return out, nil
}

func checkLedgerSource(b ledger.LedgerStreamBinding, cursor string, o LedgerSourceObservation) error {
	if o.SourceID != b.SourceID || o.ServerRunID != b.ServerRunID || o.RedisDB != b.RedisDB {
		return ledger.ErrLedgerCaptureBinding
	}
	if o.Length < 0 || o.EntriesAdded < o.Length || o.ObservedAt.IsZero() {
		return errors.New("invalid source observation")
	}
	cmp, e := ledger.CompareCurrentBaselineStreamIDs(o.LastGeneratedID, cursor)
	if e != nil || cmp < 0 {
		return errors.New("source cursor regressed")
	}
	cmp, e = ledger.CompareCurrentBaselineStreamIDs(o.MaxDeletedID, cursor)
	if e != nil || cmp > 0 {
		return errors.New("unread entries may have been deleted")
	}
	if o.Length == 0 {
		if o.EntriesAdded > 0 {
			return errors.New("stream history removed")
		}
		return nil
	}
	cmp, e = ledger.CompareCurrentBaselineStreamIDs(o.FirstID, cursor)
	if e != nil || (cmp > 0 && o.EntriesAdded > o.Length) {
		return errors.New("stream history may have been trimmed")
	}
	if cursor != "0-0" && len(o.AnchorSHA256) != 64 {
		return errors.New("cursor witness unavailable")
	}
	return nil
}

type ledgerReadEvidence struct {
	RequestedAt         time.Time                `json:"requested_at"`
	CompletedAt         time.Time                `json:"completed_at"`
	RequestedCursor     string                   `json:"requested_cursor"`
	Limit               int                      `json:"limit"`
	Before              *LedgerSourceObservation `json:"before"`
	After               *LedgerSourceObservation `json:"after"`
	ErrorDetail         string                   `json:"error_detail,omitempty"`
	ContinuityVerified  bool                     `json:"continuity_verified"`
	StreamEpochVerified bool                     `json:"stream_epoch_verified"`
	ResponseDecoded     bool                     `json:"response_decoded"`
}

// ReceiveLedgerStreamOnce is an opt-in extension of the existing ledger decoder,
// not a command sender. No worker/CLI enables it implicitly. It does not replace
// unknown broker sampling boundaries with local transport observations.
func ReceiveLedgerStreamOnce(ctx context.Context, store *ledger.LedgerReceiptStore, h ledger.LedgerStreamHandle, reader LedgerReceiptReader, limit int) (ledger.LedgerReadReceipt, error) {
	if store == nil || reader == nil || limit < 1 || limit > 256 {
		return ledger.LedgerReadReceipt{}, ledger.ErrInvalidLedgerInput
	}
	st, err := store.Status(ctx, h)
	if err != nil {
		return ledger.LedgerReadReceipt{}, err
	}
	if st.BlockedReason != "" {
		return ledger.LedgerReadReceipt{}, ledger.ErrLedgerCaptureBlocked
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return ledger.LedgerReadReceipt{}, err
	}
	attempt := "read-" + hex.EncodeToString(nonce[:])
	evidence := ledgerReadEvidence{RequestedAt: time.Now().UTC(), RequestedCursor: st.Cursor, Limit: limit}
	inputs := []ledger.LedgerStreamInput{}
	finish := func(code string, cause error) (ledger.LedgerReadReceipt, error) {
		evidence.CompletedAt = time.Now().UTC()
		if cause != nil {
			evidence.ErrorDetail = cause.Error()
			if len(evidence.ErrorDetail) > 1024 {
				evidence.ErrorDetail = evidence.ErrorDetail[:1024]
			}
		}
		raw, e := json.Marshal(evidence)
		if e != nil {
			return ledger.LedgerReadReceipt{}, e
		}
		r, e := store.Record(ctx, h, attempt, st.Cursor, raw, inputs, code, ledgerReceiptApplication(h.Binding(), h.Stream(), st.Cursor))
		if e != nil {
			return r, e
		}
		if r.Outcome != "applied" {
			return r, fmt.Errorf("ledger receipt %s: %s", r.Outcome, r.ErrorCode)
		}
		return r, nil
	}
	before, err := reader.Observe(ctx, h.Stream(), st.Cursor)
	evidence.Before = &before
	if err != nil {
		return finish("source_before_failed", err)
	}
	if err = checkLedgerSource(h.Binding(), st.Cursor, before); err != nil {
		return finish("source_before_invalid", err)
	}
	if st.AnchorSHA256 != "" && st.AnchorSHA256 != before.AnchorSHA256 {
		return finish("source_cursor_body_changed", errors.New("retained cursor bytes no longer match durable receipt"))
	}
	response, err := reader.Read(ctx, h.Stream(), st.Cursor, limit)
	if errors.Is(err, redis.Nil) {
		response = nil
		err = nil
	}
	if err != nil {
		return finish("stream_read_failed", err)
	}
	inputs, err = decodeLedgerRead(h.Stream(), st.Cursor, limit, response)
	if err != nil {
		return finish("stream_response_invalid", err)
	}
	evidence.ResponseDecoded = true
	after, err := reader.Observe(ctx, h.Stream(), st.Cursor)
	evidence.After = &after
	if err != nil {
		return finish("source_after_failed", err)
	}
	if err = checkLedgerSource(h.Binding(), st.Cursor, after); err != nil {
		return finish("source_after_invalid", err)
	}
	cmp, e := ledger.CompareCurrentBaselineStreamIDs(after.LastGeneratedID, before.LastGeneratedID)
	if e != nil || cmp < 0 || after.EntriesAdded < before.EntriesAdded || after.AnchorSHA256 != before.AnchorSHA256 {
		return finish("source_changed_during_read", errors.New("source counters or cursor witness changed"))
	}
	if len(inputs) > 0 {
		cmp, e = ledger.CompareCurrentBaselineStreamIDs(inputs[len(inputs)-1].EntryID, after.LastGeneratedID)
		if e != nil || cmp > 0 {
			return finish("source_after_invalid", errors.New("response exceeds observed stream tail"))
		}
	}
	return finish("", nil)
}

func ledgerReceiptApplication(b ledger.LedgerStreamBinding, key, start string) ledger.LedgerReadApply {
	return func(ctx context.Context, repo *ledger.Repository, inputs []ledger.LedgerStreamInput) (json.RawMessage, error) {
		messages := make([]redis.XMessage, 0, len(inputs))
		env := b.Environment
		if env == "production" {
			env = "prod"
		}
		for _, r := range inputs {
			var raw struct {
				Protocol    string          `json:"protocol"`
				MessageType string          `json:"message_type"`
				MessageID   string          `json:"message_id"`
				Action      string          `json:"action"`
				ResultType  string          `json:"result_type"`
				Status      string          `json:"status"`
				Routing     Routing         `json:"routing"`
				Payload     json.RawMessage `json:"payload"`
			}
			err := ledger.ValidateCurrentBaselineSourceJSON(r.Body)
			if err == nil {
				err = json.Unmarshal(r.Body, &raw)
			}
			if err == nil && (raw.Protocol != Protocol || raw.MessageID == "" || raw.MessageType != map[string]string{"reply": "reply", "event": "event", "hb": "heartbeat", "dlq": "deadletter"}[b.Role]) {
				err = errors.New("invalid message envelope for captured stream")
			}
			if err == nil && (raw.Routing.Env != env || raw.Routing.BrokerID != b.BrokerID || raw.Routing.GatewayID != b.GatewayID || raw.Routing.AccountID != b.AccountID) {
				err = ledger.ErrLedgerCaptureBinding
			}
			if err == nil {
				err = checkReceiptPayloadAccount(raw.Payload, b.AccountID)
			}
			if err == nil && b.Role == "reply" && raw.Status != "failed" && raw.Status != "rejected" && (raw.Action == ActionAccountAsset || raw.ResultType == "asset_page") {
				err = checkReceiptAssetFields(raw.Payload)
			}
			if err != nil {
				report, _ := json.Marshal(map[string]string{"entry_id": r.EntryID, "validation_error": err.Error()})
				return report, err
			}
			messages = append(messages, redis.XMessage{ID: r.EntryID, Values: map[string]any{"body": string(r.Body)}})
		}
		report := processLedgerStreamMessages(ctx, repo, key, b.Role, start, messages)
		raw, err := json.Marshal(report)
		if err != nil {
			return nil, err
		}
		if report.Totals.Archived != len(inputs) || report.Totals.ParseErrors > 0 || report.Totals.LedgerErrors > 0 || report.Totals.Unsupported > 0 || len(report.Errors) > 0 {
			return raw, errors.New("ledger projection incomplete; batch business writes rolled back")
		}
		if err = saveLedgerCheckpoint(ctx, repo, ledgerStreamCursor{name: key, role: b.Role, lastID: report.Totals.LastStreamID}, report, slog.Default()); err != nil {
			return raw, err
		}
		return raw, nil
	}
}

// The legacy decoder falls back to routing.account_id and zero-valued amounts.
// Durable capture must not turn a missing funds payload into a zero balance.
func checkReceiptAssetFields(raw json.RawMessage) error {
	var payload struct {
		Account map[string]json.RawMessage `json:"account"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	for _, key := range []string{"account_id", "cash_available", "cash_total", "net_asset"} {
		v := payload.Account[key]
		if len(v) == 0 || string(v) == "null" {
			return fmt.Errorf("asset_page missing required field: %s", key)
		}
	}
	return nil
}

func checkReceiptPayloadAccount(raw json.RawMessage, account string) error {
	var p map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if v, ok := p["account_id"]; ok {
		var a string
		if json.Unmarshal(v, &a) != nil || a != account {
			return ledger.ErrLedgerCaptureBinding
		}
	}
	if nested, ok := p["account"]; ok {
		if err := checkReceiptPayloadAccount(nested, account); err != nil {
			return err
		}
	}
	if rawItems, ok := p["items"]; ok {
		var items []json.RawMessage
		if err := json.Unmarshal(rawItems, &items); err != nil {
			return err
		}
		for _, v := range items {
			if err := checkReceiptPayloadAccount(v, account); err != nil {
				return err
			}
		}
	}
	return nil
}
