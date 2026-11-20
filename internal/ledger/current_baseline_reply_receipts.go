package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// This links only observed archive pages, not all pages the broker produced.
// OriginalBody is used internally; published evidence contains digests, not owners.
type CurrentBaselineReplyReceipt struct {
	Status         string                            `json:"status"`
	ArchiveMatch   string                            `json:"archive_match"`
	OriginalSHA256 string                            `json:"original_body_sha256"`
	Observations   []CurrentBaselineReceiptReference `json:"observations"`
	OriginalBody   []byte                            `json:"-"`
}

type CurrentBaselineReceiptReference struct {
	AttemptID               string    `json:"attempt_id"`
	BatchSequence           string    `json:"batch_sequence"`
	Ordinal                 int       `json:"ordinal"`
	BodySHA256              string    `json:"body_sha256"`
	BodyBytes               int       `json:"body_bytes"`
	Outcome                 string    `json:"outcome"`
	ErrorCode               string    `json:"error_code"`
	StartCursor             string    `json:"start_cursor"`
	EndCursor               string    `json:"end_cursor"`
	EvidenceSHA256          string    `json:"evidence_sha256"`
	ApplicationReportSHA256 string    `json:"application_report_sha256"`
	ReceivedAt              time.Time `json:"received_at"`
}

func validateBaselineReceiptBatch(r LedgerReadReceipt) error {
	if ValidateCurrentBaselineSourceJSON(r.Evidence) != nil || ValidateCurrentBaselineSourceJSON(r.ApplicationReport) != nil {
		return ErrCurrentBaselineCorrupt
	}
	prior := r.StartCursor
	for _, e := range r.Entries {
		cmp, err := CompareCurrentBaselineStreamIDs(e.EntryID, prior)
		if err != nil || cmp <= 0 {
			return ErrCurrentBaselineCorrupt
		}
		prior = e.EntryID
	}
	switch r.Outcome {
	case "applied":
		if r.ErrorCode != "" || r.EndCursor != prior {
			return ErrCurrentBaselineCorrupt
		}
	case "application_failed", "read_failed":
		if r.ErrorCode == "" || r.EndCursor != r.StartCursor {
			return ErrCurrentBaselineCorrupt
		}
	default:
		return ErrCurrentBaselineCorrupt
	}
	return nil
}

func linkCurrentBaselineReplyReceipts(ctx context.Context, tx *sql.Tx, src *CurrentBaselineSources, budget *int) error {
	stream := currentBaselineStreamPrefix(src.Scope) + ":reply"
	state := "unavailable"
	var receiver *CurrentBaselineReceiptObservation
	if src.SourceHealth != nil && len(src.SourceHealth.Receipts) > 0 {
		state = "receiver_missing"
		for i := range src.SourceHealth.Receipts {
			r := &src.SourceHealth.Receipts[i]
			if r.Role == "reply" && r.Found {
				receiver = r
				break
			}
		}
		if receiver != nil {
			state = "missing"
			if !receiver.BindingMatched || receiver.Stream != stream {
				state = "binding_mismatch"
			}
		}
		if !src.SourceHealth.ReceiptTrackingAvailable {
			state = "protection_unavailable"
		}
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, resource := range []string{"asset", "positions", "orders", "fills"} {
		for _, r := range src.Queries[resource].Replies {
			if r.Stream == stream && r.Role == "reply" && r.Direction == "out" && !seen[r.EntryID] {
				ids = append(ids, r.EntryID)
				seen[r.EntryID] = true
			}
		}
	}
	linked := map[string]*CurrentBaselineReplyReceipt{}
	if state == "missing" && len(ids) > 0 {
		if receiver.BatchCount == nil {
			return ErrCurrentBaselineCorrupt
		}
		batchCount, err := strconv.ParseInt(*receiver.BatchCount, 10, 64)
		if err != nil || batchCount < 0 {
			return ErrCurrentBaselineCorrupt
		}
		if len(ids) > currentBaselineMaxRows {
			return ErrCurrentBaselineLimit
		}
		// Indexed physical identities, never matching by business ID or JSONB.
		rows, err := tx.QueryContext(ctx, `SELECT e.entry_id,e.attempt_id,e.ordinal,b.batch_sequence::text,b.received_at
FROM ledger_stream_read_entries e JOIN ledger_stream_read_batches b USING(stream_key,attempt_id)
WHERE e.stream_key=$1 AND e.entry_id=ANY($2::text[])
ORDER BY b.batch_sequence,e.ordinal LIMIT $3`, stream, ids, currentBaselineMaxRows+1)
		if err != nil {
			return err
		}
		type match struct {
			id  string
			ref CurrentBaselineReceiptReference
		}
		matches := []match{}
		for rows.Next() {
			var m match
			if err = rows.Scan(&m.id, &m.ref.AttemptID, &m.ref.Ordinal, &m.ref.BatchSequence, &m.ref.ReceivedAt); err != nil {
				rows.Close()
				return err
			}
			if len(matches) >= currentBaselineMaxRows {
				rows.Close()
				return ErrCurrentBaselineLimit
			}
			matches = append(matches, m)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		batches := map[string]LedgerReadReceipt{}
		for _, m := range matches {
			ref := m.ref
			sequence, err := strconv.ParseInt(ref.BatchSequence, 10, 64)
			if err != nil || sequence <= 0 || sequence > batchCount || ref.AttemptID == "bootstrap-v1" || ref.ReceivedAt.IsZero() || ref.ReceivedAt.After(src.ReadAt) {
				return ErrCurrentBaselineCorrupt
			}
			r, ok := batches[ref.AttemptID]
			if !ok {
				r, err = readLedgerReceipt(ctx, tx, stream, ref.AttemptID)
				if err != nil {
					return err
				}
				if err = validateBaselineReceiptBatch(r); err != nil {
					return err
				}
				*budget += len(r.Evidence) + len(r.ApplicationReport)
				for _, e := range r.Entries {
					*budget += len(e.Body)
				}
				if *budget > currentBaselineMaxBytes {
					return ErrCurrentBaselineLimit
				}
				batches[ref.AttemptID] = r
			}
			if ref.Ordinal < 0 || ref.Ordinal >= len(r.Entries) || r.Entries[ref.Ordinal].EntryID != m.id {
				return ErrCurrentBaselineCorrupt
			}
			body := r.Entries[ref.Ordinal].Body
			ref.BodySHA256, ref.BodyBytes = baselineHash(body), len(body)
			ref.Outcome, ref.ErrorCode = r.Outcome, r.ErrorCode
			ref.StartCursor, ref.EndCursor = r.StartCursor, r.EndCursor
			ref.EvidenceSHA256, ref.ApplicationReportSHA256 = baselineHash(r.Evidence), baselineHash(r.ApplicationReport)
			if r.Outcome == "applied" {
				cmp, e := CompareCurrentBaselineStreamIDs(receiver.Cursor, m.id)
				if e != nil || cmp < 0 {
					return ErrCurrentBaselineCorrupt
				}
			}
			link := linked[m.id]
			if link == nil {
				link = &CurrentBaselineReplyReceipt{Status: "not_applied", OriginalSHA256: ref.BodySHA256, OriginalBody: body, Observations: []CurrentBaselineReceiptReference{}}
				linked[m.id] = link
			} else if link.OriginalSHA256 != ref.BodySHA256 || string(link.OriginalBody) != string(body) {
				link.Status = "conflict"
			}
			if r.Outcome == "applied" && link.Status != "conflict" {
				if link.Status == "matched" {
					link.Status = "conflict"
				} else {
					link.Status = "matched"
				}
			}
			link.Observations = append(link.Observations, ref)
		}
	}
	for _, resource := range []string{"asset", "positions", "orders", "fills"} {
		q := src.Queries[resource]
		for i := range q.Replies {
			r := &q.Replies[i]
			link := CurrentBaselineReplyReceipt{Status: state, Observations: []CurrentBaselineReceiptReference{}}
			if r.Stream != stream || r.Role != "reply" || r.Direction != "out" {
				link.Status = "scope_mismatch"
			} else if found := linked[r.EntryID]; found != nil {
				link = *found
				if string(link.OriginalBody) == r.Body {
					link.ArchiveMatch = "exact"
				} else if strings.TrimSpace(string(link.OriginalBody)) == r.Body {
					link.ArchiveMatch = "legacy_trimmed"
				} else if link.Status != "conflict" {
					link.Status = "archive_mismatch"
				}
			} else if state == "missing" {
				cmp, err := CompareCurrentBaselineStreamIDs(r.EntryID, receiver.InitialCursor)
				if err != nil {
					return ErrCurrentBaselineInput
				}
				if cmp <= 0 {
					link.Status = "before_capture"
				}
			}
			r.Receipt = &link
			raw, err := json.Marshal(link)
			if err != nil {
				return err
			}
			*budget += len(raw)
			if *budget > currentBaselineMaxBytes {
				return ErrCurrentBaselineLimit
			}
		}
		src.Queries[resource] = q
	}
	return nil
}
