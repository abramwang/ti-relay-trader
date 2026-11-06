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
	XRead(context.Context, *redis.XReadArgs) *redis.XStreamSliceCmd
	XInfoStream(context.Context, string) *redis.XInfoStreamCmd
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
	streams, err := client.XRead(ctx, &redis.XReadArgs{Streams: []string{h.stream, cursor}, Count: int64(limit), Block: -1}).Result()
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

func decodeStreamRead(key, cursor string, limit int, streams []redis.XStream) ([]StreamInput, error) {
	inputs := []StreamInput{}
	if len(streams) == 0 {
		return inputs, nil
	}
	if len(streams) != 1 || streams[0].Stream != key || len(streams[0].Messages) > limit {
		return nil, ErrStreamIdentity
	}
	total := 0
	last := cursor
	for _, message := range streams[0].Messages {
		cmp, err := compareStreamID(message.ID, last)
		if err != nil || cmp <= 0 {
			return nil, ErrStreamCursor
		}
		if len(message.Values) != 1 {
			return nil, fmt.Errorf("expected sole Redis body field; input not committed")
		}
		var raw []byte
		switch v := message.Values["body"].(type) {
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
		inputs = append(inputs, StreamInput{message.ID, raw})
		last = message.ID
	}
	return inputs, nil
}
