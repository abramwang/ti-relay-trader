package futures

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Intentionally no XACK/XGROUP/XADD or command publishing capability.
type StreamReadClient interface {
	ReadStream(context.Context, string, string, int) (any, error)
	XInfoStream(context.Context, string) *redis.XInfoStreamCmd
}

// RedisStreamReader keeps RESP2 field arrays until validation. The standard
// XRead decoder collapses duplicate field names into a map before we see them.
type RedisStreamReader struct{ client *redis.Client }

func NewRedisStreamReader(client *redis.Client) (*RedisStreamReader, error) {
	if client == nil || client.Options().Protocol != 2 {
		return nil, fmt.Errorf("futures reader requires a dedicated RESP2 client")
	}
	return &RedisStreamReader{client: client}, nil
}

func (r *RedisStreamReader) XInfoStream(ctx context.Context, key string) *redis.XInfoStreamCmd {
	return r.client.XInfoStream(ctx, key)
}

func (r *RedisStreamReader) ReadStream(ctx context.Context, key, cursor string, limit int) (any, error) {
	if limit < 1 || limit > 256 {
		return nil, ErrAuditCapacity
	}
	if _, err := streamID(cursor); err != nil {
		return nil, err
	}
	return r.client.Do(ctx, "XREAD", "COUNT", limit, "STREAMS", key, cursor).Result()
}

var ErrStreamHistory = errors.New("futures stream history continuity not established")

func checkStreamHistory(info *redis.XInfoStream, cursor string) error {
	if info == nil || info.Length < 0 || info.EntriesAdded < info.Length {
		return ErrStreamHistory
	}
	cmp, err := compareStreamID(info.LastGeneratedID, cursor)
	if err != nil || cmp < 0 {
		return ErrStreamHistory
	}
	cmp, err = compareStreamID(info.MaxDeletedEntryID, cursor)
	if err != nil || cmp > 0 {
		return ErrStreamHistory
	}
	if info.Length == 0 {
		if info.EntriesAdded > 0 {
			return ErrStreamHistory
		}
		return nil
	}
	cmp, err = compareStreamID(info.FirstEntry.ID, cursor)
	if err != nil {
		return ErrStreamHistory
	}
	// Conservatively stop when trimming may have removed unread history. Redis
	// IDs are not consecutive, so a numeric gap alone proves nothing.
	if cmp > 0 && info.EntriesAdded > info.Length {
		return ErrStreamHistory
	}
	return nil
}

// ReadOnce performs one bounded non-blocking XREAD using a caller-supplied client.
// The caller must bind that client to config.SourceID; no connection/config/env
// discovery, daemon startup, group creation or broker operation happens here.
// XINFO detects observable loss, not all delete/recreate/restore scenarios.
func (s *StreamStore) ReadOnce(ctx context.Context, h StreamHandle, client StreamReadClient, limit int) (StreamCapture, error) {
	if client == nil || limit < 1 || limit > 256 {
		return StreamCapture{}, ErrAuditCapacity
	}
	cursor, err := s.Cursor(ctx, h)
	if err != nil {
		return StreamCapture{}, err
	}
	before, err := client.XInfoStream(ctx, h.stream).Result()
	if err != nil {
		return StreamCapture{}, err
	}
	if err = checkStreamHistory(before, cursor); err != nil {
		return StreamCapture{}, err
	}
	streams, err := client.ReadStream(ctx, h.stream, cursor, limit)
	if err != nil && !errors.Is(err, redis.Nil) {
		return StreamCapture{}, err
	}
	if errors.Is(err, redis.Nil) {
		streams = nil
	}
	inputs, err := decodeStreamRead(h.stream, cursor, limit, streams)
	if err != nil {
		return StreamCapture{}, err
	}
	after, err := client.XInfoStream(ctx, h.stream).Result()
	if err != nil {
		return StreamCapture{}, err
	}
	if err = checkStreamHistory(after, cursor); err != nil {
		return StreamCapture{}, err
	}
	cmp, err := compareStreamID(after.LastGeneratedID, before.LastGeneratedID)
	if err != nil || cmp < 0 {
		return StreamCapture{}, ErrStreamHistory
	}
	if len(inputs) == 0 {
		current, err := s.Cursor(ctx, h)
		if err != nil {
			return StreamCapture{}, err
		}
		if current != cursor {
			return StreamCapture{}, ErrStreamCursor
		}
		return StreamCapture{ReceiptIDs: []int64{}, Cursor: cursor, Replayed: true}, nil
	}
	cmp, err = compareStreamID(inputs[len(inputs)-1].EntryID, after.LastGeneratedID)
	if err != nil || cmp > 0 {
		return StreamCapture{}, ErrStreamHistory
	}
	return s.Capture(ctx, h, cursor, inputs)
}

func decodeStreamRead(key, cursor string, limit int, response any) ([]StreamInput, error) {
	inputs := []StreamInput{}
	if response == nil {
		return inputs, nil
	}
	streams, ok := response.([]any)
	if !ok || len(streams) != 1 {
		return nil, ErrStreamIdentity
	}
	stream, ok := streams[0].([]any)
	if !ok || len(stream) != 2 || stream[0] != key {
		return nil, ErrStreamIdentity
	}
	messages, ok := stream[1].([]any)
	if !ok || len(messages) == 0 || len(messages) > limit {
		return nil, ErrStreamIdentity
	}
	total := 0
	last := cursor
	for _, entry := range messages {
		message, ok := entry.([]any)
		if !ok || len(message) != 2 {
			return nil, ErrStreamIdentity
		}
		id, ok := message[0].(string)
		if !ok {
			return nil, ErrStreamIdentity
		}
		cmp, err := compareStreamID(id, last)
		if err != nil || cmp <= 0 {
			return nil, ErrStreamCursor
		}
		fields, ok := message[1].([]any)
		if !ok || len(fields) != 2 || fields[0] != "body" {
			return nil, fmt.Errorf("expected sole Redis body field; input not committed")
		}
		var raw []byte
		switch v := fields[1].(type) {
		case string:
			raw = []byte(v)
		case []byte:
			raw = bytes.Clone(v)
		default:
			return nil, fmt.Errorf("Redis body must be bytes; input not committed")
		}
		if len(raw) > maxSnapshotBytes-total {
			return nil, ErrAuditCapacity
		}
		total += len(raw)
		inputs = append(inputs, StreamInput{id, raw})
		last = id
	}
	return inputs, nil
}
