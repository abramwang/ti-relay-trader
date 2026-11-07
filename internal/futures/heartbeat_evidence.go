package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type HeartbeatEvidenceRecord struct {
	ReceiptID      int64   `json:"receipt_id"`
	EntryID        string  `json:"entry_id"`
	BodySHA256     string  `json:"body_sha256"`
	BodyBytes      int     `json:"body_bytes"`
	Classification string  `json:"classification"`
	BodyBase64     *string `json:"body_base64,omitempty"`
}

type HeartbeatEvidencePage struct {
	Mode               string                    `json:"mode"`
	Stream             string                    `json:"stream"`
	SourceID           string                    `json:"source_id"`
	Route              QueryRoute                `json:"route"`
	Checkpoint         HeartbeatCheckpoint       `json:"checkpoint"`
	AfterReceipt       int64                     `json:"after_receipt"`
	NextReceipt        int64                     `json:"next_receipt"`
	More               bool                      `json:"more"`
	At                 time.Time                 `json:"at"`
	BusinessAuthorized bool                      `json:"business_authorized"`
	Records            []HeartbeatEvidenceRecord `json:"records"`
}

// HeartbeatEvidence never opens/claims a receiver. Each page is a read-only
// repeatable-read snapshot; callers must retain the same owner/cursor anchor
// across pages. State changes outside the raw ledger are not part of this view.
func (s *StreamStore) HeartbeatEvidence(ctx context.Context, c StreamConfig, expected HeartbeatCheckpoint, after int64, limit int, includeRaw bool) (HeartbeatEvidencePage, error) {
	if err := validateHeartbeatCapture(c); err != nil {
		return HeartbeatEvidencePage{}, err
	}
	if len(expected.Owner) != 64 || !validID(expected.Owner) {
		return HeartbeatEvidencePage{}, ErrStreamOwner
	}
	if _, err := streamID(expected.Cursor); err != nil {
		return HeartbeatEvidencePage{}, err
	}
	if after < 0 || limit < 1 || limit > 256 {
		return HeartbeatEvidencePage{}, ErrAuditCapacity
	}
	key, _ := c.stream()
	binding, _ := json.Marshal(c)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return HeartbeatEvidencePage{}, err
	}
	defer tx.Rollback()
	cp := HeartbeatCheckpoint{Registered: true}
	var same bool
	if err := tx.QueryRowContext(ctx, `SELECT owner_id,cursor_id,audit_count,audit_bytes,config_json=$2::jsonb FROM futures_stream_receivers WHERE stream_key=$1`, key, binding).Scan(&cp.Owner, &cp.Cursor, &cp.Records, &cp.Bytes, &same); err != nil {
		return HeartbeatEvidencePage{}, err
	}
	if !same {
		return HeartbeatEvidencePage{}, ErrStreamIdentity
	}
	if cp.Owner != expected.Owner {
		return HeartbeatEvidencePage{}, ErrStreamOwner
	}
	if cp.Cursor != expected.Cursor {
		return HeartbeatEvidencePage{}, ErrStreamCursor
	}
	var count int
	var size int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(octet_length(body)),0) FROM futures_stream_inbox WHERE stream_key=$1`, key).Scan(&count, &size); err != nil {
		return HeartbeatEvidencePage{}, err
	}
	if count != cp.Records || size != cp.Bytes {
		return HeartbeatEvidencePage{}, fmt.Errorf("heartbeat evidence counters inconsistent")
	}
	var tail string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT entry_id FROM futures_stream_inbox WHERE stream_key=$1 ORDER BY receipt_id DESC LIMIT 1),'0-0')`, key).Scan(&tail); err != nil {
		return HeartbeatEvidencePage{}, err
	}
	if tail != cp.Cursor {
		return HeartbeatEvidencePage{}, ErrStreamCursor
	}
	previous := "0-0"
	if after != 0 {
		if err := tx.QueryRowContext(ctx, `SELECT entry_id FROM futures_stream_inbox WHERE stream_key=$1 AND receipt_id=$2`, key, after).Scan(&previous); err != nil {
			if err == sql.ErrNoRows {
				return HeartbeatEvidencePage{}, ErrStreamCursor
			}
			return HeartbeatEvidencePage{}, err
		}
		if cmp, err := compareStreamID(previous, cp.Cursor); err != nil || cmp > 0 {
			return HeartbeatEvidencePage{}, ErrStreamCursor
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT receipt_id,entry_id,body,body_sha256 FROM futures_stream_inbox WHERE stream_key=$1 AND receipt_id>$2 ORDER BY receipt_id LIMIT $3`, key, after, limit+1)
	if err != nil {
		return HeartbeatEvidencePage{}, err
	}
	defer rows.Close()
	out := HeartbeatEvidencePage{Mode: "test_heartbeat_evidence_only", Stream: key, SourceID: c.SourceID, Route: c.Scope.Route, Checkpoint: cp, AfterReceipt: after, NextReceipt: after, Records: []HeartbeatEvidenceRecord{}}
	pageBytes := 0
	for rows.Next() {
		if len(out.Records) == limit {
			out.More = true
			break
		}
		var item HeartbeatEvidenceRecord
		var body, digest []byte
		if err := rows.Scan(&item.ReceiptID, &item.EntryID, &body, &digest); err != nil {
			return HeartbeatEvidencePage{}, err
		}
		if len(body) > maxSnapshotBytes-pageBytes {
			return HeartbeatEvidencePage{}, ErrAuditCapacity
		}
		pageBytes += len(body)
		sum := sha256.Sum256(body)
		if !bytes.Equal(sum[:], digest) {
			return HeartbeatEvidencePage{}, fmt.Errorf("heartbeat evidence checksum mismatch")
		}
		if cmp, err := compareStreamID(item.EntryID, cp.Cursor); err != nil || cmp > 0 {
			return HeartbeatEvidencePage{}, ErrStreamCursor
		}
		if cmp, err := compareStreamID(item.EntryID, previous); err != nil || cmp <= 0 {
			return HeartbeatEvidencePage{}, ErrStreamCursor
		}
		previous = item.EntryID
		item.BodySHA256, item.BodyBytes = hex.EncodeToString(sum[:]), len(body)
		item.Classification = "invalid_heartbeat"
		if h, err := DecodeHeartbeat(body, c.Scope.Route); err == nil {
			item.Classification = "decoded_unverified"
			if h.Recovery != nil {
				item.Classification = "transport_recovery_only"
			}
		}
		if includeRaw {
			encoded := base64.StdEncoding.EncodeToString(body)
			item.BodyBase64 = &encoded
		}
		out.NextReceipt = item.ReceiptID
		out.Records = append(out.Records, item)
	}
	if err := rows.Err(); err != nil {
		return HeartbeatEvidencePage{}, err
	}
	if err := rows.Close(); err != nil {
		return HeartbeatEvidencePage{}, err
	}
	if err := tx.Commit(); err != nil {
		return HeartbeatEvidencePage{}, err
	}
	out.At = time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600))
	return out, nil
}
