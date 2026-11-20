package accountbaseline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
)

// Synthetic transport only; the actual receiver/parser/ledger/PG path is used.
type queryReceiptTransport struct {
	binding ledger.LedgerStreamBinding
	inputs  []ledger.LedgerStreamInput
}

func (r queryReceiptTransport) Observe(context.Context, string, string) (redisstream.LedgerSourceObservation, error) {
	return redisstream.LedgerSourceObservation{SourceID: r.binding.SourceID, ServerRunID: r.binding.ServerRunID, ObservedAt: time.Now().UTC(), Length: int64(len(r.inputs)), EntriesAdded: int64(len(r.inputs)), FirstID: r.inputs[0].EntryID, LastGeneratedID: r.inputs[len(r.inputs)-1].EntryID, MaxDeletedID: "0-0"}, nil
}
func (r queryReceiptTransport) Read(_ context.Context, key, _ string, _ int) (any, error) {
	rows := []any{}
	for _, in := range r.inputs {
		rows = append(rows, []any{in.EntryID, []any{"body", string(in.Body)}})
	}
	return []any{[]any{key, rows}}, nil
}

type receiptSourceFixture struct {
	db      *sql.DB
	ctx     context.Context
	store   *ledger.CurrentBaselineStore
	capture *ledger.LedgerReceiptStore
	handle  ledger.LedgerStreamHandle
	scope   ledger.CurrentBaselineScope
	ids     map[string]string
	inputs  []ledger.LedgerStreamInput
	start   time.Time
}

func newReceiptSourceFixture(t *testing.T, mode string) receiptSourceFixture {
	t.Helper()
	db, ctx := sourceSchemaDatabase(t, true)
	s := fixtureSources(t, 501)
	start := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	delta := start.Sub(fixtureStart)
	repo := ledger.NewRepository(db)
	f := receiptSourceFixture{db: db, ctx: ctx, scope: s.Scope, ids: map[string]string{}, start: start}
	f.store, _ = ledger.NewCurrentBaselineStore(db)
	f.capture, _ = ledger.NewLedgerReceiptStore(db)
	b := ledger.LedgerStreamBinding{Environment: "test", AccountID: s.Scope.AccountID, BrokerID: "huaxin", GatewayID: s.Scope.GatewayID, Role: "reply", SourceID: "synthetic-transport", ServerRunID: strings.Repeat("a", 40)}
	var err error
	f.handle, err = f.capture.Open(ctx, b, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	ordinal := 0
	for _, kind := range []string{"asset", "positions", "orders", "fills"} {
		q := s.Queries[kind]
		f.ids[kind] = q.MessageID
		for _, list := range [][]ledger.CurrentBaselineArchive{q.Commands, q.Replies} {
			for _, row := range list {
				mutate(t, &row, func(w map[string]any) {
					for _, key := range []string{"sent_at", "produced_at"} {
						if value, ok := w[key].(string); ok {
							at, _ := time.Parse(time.RFC3339Nano, value)
							w[key] = at.Add(delta)
						}
					}
					if row.Role == "reply" && kind == "asset" {
						w["payload"] = map[string]any{"account": map[string]any{"account_id": s.Scope.AccountID, "cash_available": json.Number("1000.01"), "cash_total": json.Number("1100.01"), "net_asset": json.Number("1100.01")}}
					}
				})
				if row.Role == "cmd.query" {
					err = repo.ArchiveRawStreamMessage(ctx, ledger.RawStreamMessage{StreamRef: ledger.StreamRef{Key: row.Stream, ID: fmt.Sprintf("%d-0", ordinal+1)}, SourceRef: ledger.SourceRef{OriginMessageID: q.MessageID}, Direction: "in", Role: "cmd.query", AccountID: s.Scope.AccountID, Body: json.RawMessage(row.Body), BodyText: row.Body, ReceivedAt: row.ReceivedAt.Add(delta)})
					if err != nil {
						t.Fatal(err)
					}
					continue
				}
				ordinal++
				f.inputs = append(f.inputs, ledger.LedgerStreamInput{EntryID: fmt.Sprintf("%d-0", ordinal), Body: []byte(" \n" + row.Body + "\n")})
			}
		}
	}
	if mode != "success" {
		code := "source_after_failed"
		apply := ledger.LedgerReadApply(nil)
		if mode == "application_failed" {
			code = ""
			apply = func(context.Context, *ledger.Repository, []ledger.LedgerStreamInput) (json.RawMessage, error) {
				return json.RawMessage(`{}`), errors.New("synthetic application failure")
			}
		}
		if _, err = f.capture.Record(ctx, f.handle, "synthetic-failure", "0-0", json.RawMessage(`{"synthetic_only":true}`), f.inputs, code, apply); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "conflict" {
		changed := append([]ledger.LedgerStreamInput{}, f.inputs...)
		changed[0].Body = []byte(strings.Replace(string(changed[0].Body), "1000.01", "999.01", 1))
		if _, err = f.capture.Record(ctx, f.handle, "synthetic-conflict", "0-0", json.RawMessage(`{"synthetic_only":true}`), changed, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "success" || mode == "retry" {
		if _, err = redisstream.ReceiveLedgerStreamOnce(ctx, f.capture, f.handle, queryReceiptTransport{b, f.inputs}, 256); err != nil {
			t.Fatal(err)
		}
	} else {
		// A mutable legacy success must not override the failed durable receipt.
		for _, in := range f.inputs {
			var w wire
			if err = json.Unmarshal(in.Body, &w); err != nil {
				t.Fatal(err)
			}
			if err = repo.ArchiveRawStreamMessage(ctx, ledger.RawStreamMessage{StreamRef: ledger.StreamRef{Key: f.handle.Stream(), ID: in.EntryID}, SourceRef: ledger.SourceRef{OriginMessageID: w.OriginMessageID}, Direction: "out", Role: "reply", AccountID: s.Scope.AccountID, Body: json.RawMessage(in.Body), BodyText: string(in.Body)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return f
}

func (f receiptSourceFixture) assemble(t *testing.T, id string) (ledger.CurrentBaselineCandidate, ledger.CurrentBaselineSources) {
	t.Helper()
	s, err := f.store.ReadSources(f.ctx, "test", f.scope, f.ids)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Assemble(s, id, f.start)
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestCurrentAccountBaselineReceiptLinkedAssembly(t *testing.T) {
	f := newReceiptSourceFixture(t, "retry")
	c, src := f.assemble(t, "linked")
	for _, p := range reports(t, c) {
		if !p.ProjectionValid || !p.RetainedPagesReceipted || p.ArchiveComplete || !hasFlag(p, "reply_receipt_prior_failure") {
			t.Fatal(p)
		}
		for _, page := range p.Pages {
			if page.BodySource != "durable_receipt" || page.BodySHA256 == page.ArchiveBodySHA256 || page.Receipt.ArchiveMatch != "legacy_trimmed" || len(page.Receipt.Observations) != 2 || page.Receipt.Observations[0].Outcome != "read_failed" || page.Receipt.Observations[1].Outcome != "applied" {
				t.Fatal(page)
			}
		}
	}
	if string(src.Queries["asset"].Replies[0].Receipt.OriginalBody) != string(f.inputs[0].Body) || len(c.Resources["positions"]) != 501 {
		t.Fatal("original bytes or rows lost")
	}
	if strings.Contains(string(c.Queries), f.handle.Owner()) || strings.Contains(string(c.Queries), `"OriginalBody"`) {
		t.Fatal("private capture fields exposed")
	}
	m, err := f.store.PublishCandidate(f.ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if m.Completeness != "partial" || m.Consistency.CoverageVerified || m.MissingSecurityMeansZero || m.SourceHealth.ContinuityVerified {
		t.Fatal("link promoted to complete", m)
	}
	page, err := f.store.Page(f.ctx, f.scope, m.BaselineID, "positions", "", 500)
	if err != nil || page.Count != 500 {
		t.Fatal(page, err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE raw_stream_messages SET body_text=replace(body_text,'1000.01','999.01') WHERE origin_message_id=$1 AND stream_role='reply'`, f.ids["asset"]); err != nil {
		t.Fatal(err)
	}
	changed, _ := f.assemble(t, "changed")
	p := reports(t, changed)["asset"]
	if p.ProjectionValid || !hasFlag(p, "reply_receipt_archive_mismatch") || len(changed.Cash) != 0 {
		t.Fatal("archive substituted for receipt", p)
	}
	old, err := f.store.Manifest(f.ctx, f.scope, m.BaselineID)
	if err != nil || string(old.Queries) != string(m.Queries) {
		t.Fatal("fixed evidence changed", err)
	}
	last, err := f.store.Page(f.ctx, f.scope, m.BaselineID, "positions", page.NextCursor, 500)
	if err != nil || last.Count != 1 {
		t.Fatal(last, err)
	}
}

func TestCurrentAccountBaselineReceiptFailureEvidence(t *testing.T) {
	for _, mode := range []string{"read_failed", "application_failed", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			f := newReceiptSourceFixture(t, mode)
			c, _ := f.assemble(t, mode)
			p := reports(t, c)["asset"]
			flag := "reply_receipt_not_applied"
			if mode == "conflict" {
				flag = "reply_receipt_conflict"
			}
			if p.ProjectionValid || p.RetainedPagesReceipted || !hasFlag(p, flag) {
				t.Fatal(p)
			}
			st, err := f.capture.Status(f.ctx, f.handle)
			if err != nil || st.Cursor != "0-0" || st.FaultCount == 0 {
				t.Fatal("failure responsibility changed", st, err)
			}
		})
	}
}

func TestCurrentAccountBaselineReceiptProtectionAndCorruption(t *testing.T) {
	f := newReceiptSourceFixture(t, "success")
	c, _ := f.assemble(t, "before-disabled")
	if _, err := f.db.ExecContext(f.ctx, `ALTER TABLE ledger_stream_read_entries DISABLE TRIGGER ledger_read_entries_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PublishCandidate(f.ctx, c); !errors.Is(err, ledger.ErrCurrentBaselineTracking) {
		t.Fatal("published without immutable protection", err)
	}
	blocked, _ := f.assemble(t, "disabled")
	if p := reports(t, blocked)["asset"]; p.ProjectionValid || !hasFlag(p, "reply_receipt_protection_unavailable") {
		t.Fatal(p)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE ledger_stream_read_entries SET body=convert_to('corrupt','UTF8') WHERE ordinal=0;
ALTER TABLE ledger_stream_read_entries ENABLE TRIGGER ledger_read_entries_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReadSources(f.ctx, "test", f.scope, f.ids); !errors.Is(err, ledger.ErrCurrentBaselineCorrupt) {
		t.Fatal("corrupt original accepted", err)
	}
}

func TestCurrentAccountBaselineReceiptScopeAndMissing(t *testing.T) {
	for _, fault := range []string{"entry", "stream", "binding"} {
		t.Run(fault, func(t *testing.T) {
			f := newReceiptSourceFixture(t, "success")
			query := `UPDATE raw_stream_messages SET stream_id='1000-0' WHERE stream_role='reply' AND origin_message_id='query-asset'`
			flag := "reply_receipt_missing"
			if fault == "stream" {
				query = `UPDATE raw_stream_messages SET stream_key='relay:test:v1:huaxin:other:reply' WHERE stream_role='reply' AND origin_message_id='query-asset'`
				flag = "reply_receipt_scope_mismatch"
			}
			if fault == "binding" {
				query = `UPDATE ledger_stream_receivers SET binding=jsonb_set(binding,'{account_id}','"other"')`
				flag = "reply_receipt_binding_mismatch"
			}
			if _, err := f.db.ExecContext(f.ctx, query); err != nil {
				t.Fatal(err)
			}
			c, _ := f.assemble(t, fault)
			p := reports(t, c)["asset"]
			if p.ProjectionValid || p.RetainedPagesReceipted || !hasFlag(p, flag) {
				t.Fatal(p)
			}
		})
	}
}

func TestCurrentAccountBaselineReceiptLegacyBoundary(t *testing.T) {
	db, ctx := sourceSchemaDatabase(t, true)
	s := fixtureSources(t, 0)
	repo := ledger.NewRepository(db)
	ids := map[string]string{}
	for kind, q := range s.Queries {
		ids[kind] = q.MessageID
	}
	q := s.Queries["asset"]
	for _, r := range append(q.Commands, q.Replies...) {
		if err := repo.ArchiveRawStreamMessage(ctx, ledger.RawStreamMessage{
			StreamRef: ledger.StreamRef{Key: r.Stream, ID: r.EntryID}, SourceRef: ledger.SourceRef{OriginMessageID: q.MessageID},
			Direction: r.Direction, Role: r.Role, AccountID: s.Scope.AccountID,
			Body: json.RawMessage(r.Body), BodyText: r.Body, ReceivedAt: r.ReceivedAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := ledger.NewCurrentBaselineStore(db)
	read, err := store.ReadSources(ctx, "test", s.Scope, ids)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Queries["asset"].Replies[0].Receipt.Status; got != "receiver_missing" {
		t.Fatal(got)
	}
	b := ledger.LedgerStreamBinding{Environment: "test", AccountID: s.Scope.AccountID, BrokerID: "huaxin", GatewayID: s.Scope.GatewayID, Role: "reply", SourceID: "synthetic-legacy", ServerRunID: strings.Repeat("a", 40)}
	stream, _ := b.Stream()
	if err = repo.UpsertStreamCheckpoint(ctx, ledger.StreamCheckpoint{StreamKey: stream, Role: "reply", LastStreamID: "2-0"}); err != nil {
		t.Fatal(err)
	}
	capture, _ := ledger.NewLedgerReceiptStore(db)
	if _, err = capture.Open(ctx, b, "2-0"); err != nil {
		t.Fatal(err)
	}
	read, err = store.ReadSources(ctx, "test", s.Scope, ids)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Assemble(read, "legacy", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	p := reports(t, c)["asset"]
	if !p.ProjectionValid || p.RetainedPagesReceipted || !hasFlag(p, "reply_receipt_before_capture") || p.Pages[0].BodySource != "legacy_archive" || len(p.Pages[0].Receipt.Observations) != 0 {
		t.Fatal("legacy history retroactively receipted", p)
	}
}
