package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCurrentAccountBaselineWriterTakeover(t *testing.T) {
	db, ctx := receiptDatabase(t)
	repo := NewRepository(db)
	store, _ := NewLedgerReceiptStore(db)
	b := receiptBinding()
	key, _ := b.Stream()
	legacy, err := repo.AcquireStreamWriter(ctx, []string{key}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(legacy.Close)
	if _, err = repo.AcquireStreamWriter(ctx, []string{key}, false); !errors.Is(err, ErrLedgerWriterBusy) {
		t.Fatal(err)
	}
	if _, err = store.Open(ctx, b, "0-0"); !errors.Is(err, ErrLedgerWriterBusy) {
		t.Fatal("active legacy takeover", err)
	}
	if err = legacy.Repository().UpsertStreamCheckpoint(ctx, StreamCheckpoint{StreamKey: key, Role: b.Role, LastStreamID: "12-0"}); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	if _, err = store.Open(ctx, b, "0-0"); !errors.Is(err, ErrLedgerCaptureCursor) {
		t.Fatal("rewind", err)
	}
	h, err := store.Open(ctx, b, "12-0")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := store.Receipt(ctx, h, "bootstrap-v1")
	if err != nil {
		t.Fatal(err)
	}
	var boundary struct {
		CheckpointFound    bool   `json:"checkpoint_found"`
		InitialCursor      string `json:"initial_cursor"`
		ContinuityVerified bool   `json:"continuity_verified"`
	}
	if err = json.Unmarshal(bootstrap.Evidence, &boundary); err != nil || !boundary.CheckpointFound || boundary.InitialCursor != "12-0" || boundary.ContinuityVerified {
		t.Fatal(boundary, err)
	}
	st, err := store.Status(ctx, h)
	if err != nil || st.BatchCount != 0 || st.EntryCount != 0 || st.EvidenceBytes <= 0 {
		t.Fatal(st, err)
	}
	if _, err = repo.AcquireStreamWriter(ctx, []string{key}, false); !errors.Is(err, ErrLedgerCaptureBinding) {
		t.Fatal("downgrade allowed", err)
	}
	durable, err := repo.AcquireStreamWriter(ctx, []string{key}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(durable.Close)
	attached, err := durable.Receipts().Attach(ctx, b, h.Owner())
	if err != nil || attached.Owner() != h.Owner() {
		t.Fatal(err)
	}
	if _, err = store.Resume(ctx, b, h.Owner(), "12-0"); !errors.Is(err, ErrLedgerWriterBusy) {
		t.Fatal("active durable takeover", err)
	}
	if _, err = repo.AcquireStreamWriter(ctx, []string{key}, true); !errors.Is(err, ErrLedgerWriterBusy) {
		t.Fatal("two durable writers", err)
	}
	durable.Close()
	next, err := store.Resume(ctx, b, h.Owner(), "12-0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Attach(ctx, b, h.Owner()); !errors.Is(err, ErrLedgerCaptureOwner) {
		t.Fatal("old manifest regained owner", err)
	}
	if _, err = store.Attach(ctx, b, next.Owner()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Record(ctx, next, "bootstrap-v1", "12-0", json.RawMessage(`{}`), nil, "", nil); !errors.Is(err, ErrInvalidLedgerInput) {
		t.Fatal("reserved receipt identity", err)
	}
}

func TestCurrentAccountBaselineWriterPartialLocksAndDisconnect(t *testing.T) {
	db, ctx := receiptDatabase(t)
	repo := NewRepository(db)
	key, _ := receiptBinding().Stream()
	first := key + ":a"
	second := key + ":b"
	blocked, err := repo.AcquireStreamWriter(ctx, []string{second}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(blocked.Close)
	if _, err = repo.AcquireStreamWriter(ctx, []string{first, second}, false); !errors.Is(err, ErrLedgerWriterBusy) {
		t.Fatal(err)
	}
	lease, err := repo.AcquireStreamWriter(ctx, []string{first}, false)
	if err != nil {
		t.Fatal("partial acquire leaked first lock", err)
	}
	t.Cleanup(lease.Close)
	bound := lease.Repository()
	var pid int
	if err = lease.conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var killed bool
	if err = db.QueryRowContext(ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&killed); err != nil || !killed {
		t.Fatal(err)
	}
	if _, err = bound.exec.ExecContext(ctx, `INSERT INTO receipt_effects(n) VALUES(9)`); err == nil {
		t.Fatal("lost lease wrote through pool")
	}
	lease.Close()
	next, err := repo.AcquireStreamWriter(ctx, []string{first}, false)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
	var n int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM receipt_effects").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestCurrentAccountBaselineBootstrapBoundary(t *testing.T) {
	db, ctx := receiptDatabase(t)
	repo := NewRepository(db)
	store, _ := NewLedgerReceiptStore(db)
	for _, tc := range []struct {
		name, checkpoint, initial, tail, role, lastError string
		errors                                           int64
		valid                                            bool
	}{
		{name: "empty", initial: "0-0", valid: true},
		{name: "unregistered_jump", initial: "9-0"},
		{name: "exact", checkpoint: "9-0", initial: "9-0", tail: "9-0", valid: true},
		{name: "uncheckpointed", initial: "0-0", tail: "1-0"},
		{name: "archive_ahead", checkpoint: "9-0", initial: "9-0", tail: "10-0"},
		{name: "cursor_ahead", checkpoint: "9-0", initial: "10-0"},
		{name: "cursor_behind", checkpoint: "9-0", initial: "8-0"},
		{name: "old_errors", checkpoint: "9-0", initial: "9-0", errors: 1},
		{name: "last_error", checkpoint: "9-0", initial: "9-0", lastError: "incomplete"},
		{name: "wrong_role", checkpoint: "9-0", initial: "9-0", role: "hb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := receiptBinding()
			b.GatewayID = tc.name
			key, _ := b.Stream()
			if tc.checkpoint != "" {
				role := tc.role
				if role == "" {
					role = b.Role
				}
				if err := repo.UpsertStreamCheckpoint(ctx, StreamCheckpoint{StreamKey: key, Role: role, LastStreamID: tc.checkpoint, ErrorCount: tc.errors, LastError: tc.lastError}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.tail != "" {
				if err := repo.ArchiveRawStreamMessage(ctx, RawStreamMessage{StreamRef: StreamRef{Key: key, ID: tc.tail}, Role: b.Role, Direction: "in", BodyText: `{}`}); err != nil {
					t.Fatal(err)
				}
			}
			h, err := store.Open(ctx, b, tc.initial)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.Attach(ctx, b, h.Owner()); err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrLedgerCaptureCursor) {
					t.Fatal(err)
				}
				var n int
				if err = db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_stream_receivers WHERE stream_key=$1`, key).Scan(&n); err != nil || n != 0 {
					t.Fatal("partial takeover", n, err)
				}
			}
		})
	}
}

func TestCurrentAccountBaselineBootstrapEvidenceLimit(t *testing.T) {
	db, ctx := receiptDatabase(t)
	repo := NewRepository(db)
	b := receiptBinding()
	key, _ := b.Stream()
	if err := repo.UpsertStreamCheckpoint(ctx, StreamCheckpoint{StreamKey: key, Role: b.Role, LastStreamID: "1-0", Metadata: map[string]any{"oversized": strings.Repeat("x", 1<<20)}}); err != nil {
		t.Fatal(err)
	}
	store, _ := NewLedgerReceiptStore(db)
	if _, err := store.Open(ctx, b, "1-0"); !errors.Is(err, ErrCurrentBaselineLimit) {
		t.Fatal("unreadable bootstrap committed", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ledger_stream_receivers`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
