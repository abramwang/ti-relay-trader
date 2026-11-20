package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Local capture epoch is explicitly not a Redis/OC namespace epoch.
type CurrentBaselineReceiptObservation struct {
	Stream          string               `json:"stream"`
	Role            string               `json:"role"`
	Found           bool                 `json:"found"`
	Binding         *LedgerStreamBinding `json:"binding"`
	BindingMatched  bool                 `json:"binding_matched"`
	CaptureEpoch    string               `json:"local_capture_epoch"`
	InitialCursor   string               `json:"initial_cursor"`
	Cursor          string               `json:"cursor"`
	BlockedReason   string               `json:"blocked_reason"`
	FaultCount      *string              `json:"fault_count"`
	BatchCount      *string              `json:"batch_count"`
	EntryCount      *string              `json:"entry_count"`
	UpdatedAt       *time.Time           `json:"updated_at"`
	LatestOutcome   string               `json:"latest_outcome"`
	LatestErrorCode string               `json:"latest_error_code"`
}

func currentBaselineReceiptTracking(ctx context.Context, q currentBaselineReader) (bool, bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `SELECT to_regclass('ledger_stream_receivers') IS NOT NULL`).Scan(&exists)
	if err != nil || !exists {
		return exists, false, err
	}
	var n int
	err = q.QueryRowContext(ctx, `SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid
JOIN pg_proc p ON p.oid=t.tgfoid AND p.pronamespace=c.relnamespace
WHERE c.oid=to_regclass('ledger_stream_receivers') AND p.proname='observe_account_baseline_receiver' AND p.pronargs=0
AND ((t.tgname='account_baseline_receiver_watch' AND t.tgtype=29) OR (t.tgname='account_baseline_receiver_truncate' AND t.tgtype=32))
AND t.tgenabled IN ('O','A')`).Scan(&n)
	if err != nil || n != 2 {
		return true, false, err
	}
	// A receiver watcher alone cannot make mutable receipt rows trustworthy.
	err = q.QueryRowContext(ctx, `SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid
JOIN pg_proc p ON p.oid=t.tgfoid AND p.pronamespace=c.relnamespace
WHERE p.proname='reject_account_baseline_mutation' AND p.pronargs=0 AND t.tgenabled IN ('O','A')
AND ((c.oid=to_regclass('ledger_stream_read_batches') AND
 ((t.tgname='ledger_read_batches_immutable' AND t.tgtype=27) OR (t.tgname='ledger_read_batches_no_truncate' AND t.tgtype=34)))
 OR (c.oid=to_regclass('ledger_stream_read_entries') AND
 ((t.tgname='ledger_read_entries_immutable' AND t.tgtype=27) OR (t.tgname='ledger_read_entries_no_truncate' AND t.tgtype=34))))`).Scan(&n)
	return true, n == 4, err
}

func readCurrentBaselineReceipts(ctx context.Context, q currentBaselineReader, scope CurrentBaselineScope, h *CurrentBaselineSourceHealth) error {
	h.Receipts = []CurrentBaselineReceiptObservation{}
	exists, tracking, err := currentBaselineReceiptTracking(ctx, q)
	if err != nil {
		return err
	}
	h.ReceiptTrackingAvailable = tracking
	if !exists {
		h.reason("durable_receipts_unavailable")
		return nil
	}
	if !tracking {
		h.reason("receipt_tracking_unavailable")
	}
	for _, role := range []string{"reply", "event", "hb", "dlq"} {
		r := CurrentBaselineReceiptObservation{Stream: currentBaselineStreamPrefix(scope) + ":" + role, Role: role}
		var binding []byte
		var account, faults, batches, entries string
		var at time.Time
		err = q.QueryRowContext(ctx, `SELECT r.account_id,r.binding,r.capture_epoch,r.initial_cursor,r.cursor_id,r.blocked_reason,r.fault_count::text,r.batch_count::text,r.entry_count::text,r.updated_at,
coalesce(b.outcome,''),coalesce(b.error_code,'') FROM ledger_stream_receivers r
LEFT JOIN ledger_stream_read_batches b ON b.stream_key=r.stream_key AND b.batch_sequence=r.batch_count AND b.batch_sequence>0
WHERE r.stream_key=$1`, r.Stream).Scan(&account, &binding, &r.CaptureEpoch, &r.InitialCursor, &r.Cursor, &r.BlockedReason, &faults, &batches, &entries, &at, &r.LatestOutcome, &r.LatestErrorCode)
		if err == sql.ErrNoRows {
			h.reason(role + "_durable_receipts_missing")
		} else if err != nil {
			return err
		} else {
			r.Found = true
			r.FaultCount = &faults
			r.BatchCount = &batches
			r.EntryCount = &entries
			r.UpdatedAt = &at
			var b LedgerStreamBinding
			if json.Unmarshal(binding, &b) != nil {
				return ErrCurrentBaselineCorrupt
			}
			r.Binding = &b
			key, e := b.Stream()
			r.BindingMatched = e == nil && key == r.Stream && account == scope.AccountID && b.AccountID == scope.AccountID && b.Environment == scope.Environment && b.BrokerID == scope.BrokerID && b.GatewayID == scope.GatewayID && b.Role == role
			if !r.BindingMatched {
				h.reason(role + "_receipt_binding_mismatch")
			}
			if r.BlockedReason != "" {
				h.reason(role + "_receipt_application_blocked")
			}
			if r.LatestOutcome == "read_failed" {
				h.reason(role + "_receipt_read_failed")
			}
			if faults != "0" {
				h.reason(role + "_receipt_prior_faults_unreconciled")
			}
			if batches == "0" {
				h.reason(role + "_receipt_observation_missing")
			}
		}
		h.Receipts = append(h.Receipts, r)
	}
	return nil
}
