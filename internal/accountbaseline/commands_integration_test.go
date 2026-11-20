package accountbaseline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/ledger"
)

func TestCurrentAccountBaselineIsolatedCommands(t *testing.T) {
	db, ctx := sourceDatabase(t)
	store, _ := ledger.NewCurrentBaselineStore(db)
	scope := fixtureSources(t, 0).Scope
	ids := map[string]string{"asset": "query-asset", "positions": "query-positions", "orders": "query-orders", "fills": "query-fills"}
	start := time.Now().UTC().Add(-time.Minute)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id,origin_message_id,body,body_text,received_at) VALUES($1,$2,'in','cmd.trade',$3,$4,$5::jsonb,$6,$7)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	originals := map[string]string{}
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("command-%d", i)
		payload := commandPayload(id)
		a := archivedCommand(t, id, "order.submit", payload)
		if i == 0 {
			a = archivedCommand(t, id, "order.batch.submit", map[string]any{"account_id": scope.AccountID, "orders": []any{commandPayload("done"), commandPayload("missing-child")}})
		}
		if i == 1 {
			a = archivedCommand(t, id, "order.cancel", map[string]any{"account_id": scope.AccountID, "gateway_order_id": "done", "cancel_id": "cancel-1"})
		}
		if i == 2 {
			a.Archive.Stream = strings.Replace(a.Archive.Stream, ":test:", ":prod:", 1)
		}
		r := a.Archive
		if _, err = stmt.ExecContext(ctx, r.Stream, fmt.Sprintf("%d-0", i+100), scope.AccountID, id, r.Body, r.Body, r.ReceivedAt); err != nil {
			t.Fatal(err)
		}
		originals[fmt.Sprint(i+1)] = r.Body
	}
	a := archivedCommand(t, "other", "order.submit", commandPayload("other"))
	if _, err = stmt.ExecContext(ctx, a.Archive.Stream, "999-0", "other", "other", a.Archive.Body, a.Archive.Body, a.Archive.ReceivedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO orders(account_id,gateway_order_id,origin_message_id,trade_date,symbol,exchange,trade_side,business_type,order_qty,cum_filled_qty,leaves_qty,status,is_terminal,limit_price,updated_at) VALUES
('00030484','done','command-0','2026-11-18','000001','SZ','B','S',100,0,0,'cancelled',true,10.125,now()),
('00030484','unrelated','unrelated-history','2026-11-18','000001','SZ','B','S',100,100,0,'filled',true,10.125,now())`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	src, err := store.ReadSources(ctx, "test", scope, ids)
	if err != nil {
		t.Fatal(err)
	}
	if src.TradeCommands == nil || len(src.TradeCommands.Archives) != 501 || len(src.TradeCommands.Orders) != 1 || len(src.UnresolvedOrders) != 0 {
		t.Fatal("scope/terminal associations", src.TradeCommands)
	}
	if src.TradeCommands.Archives[8].Archive.ArchiveID != "9" || src.TradeCommands.Archives[9].Archive.ArchiveID != "10" {
		t.Fatal("archive ordering was textual")
	}
	c, err := Assemble(src, "commands-pg", start)
	if err != nil {
		t.Fatal(err)
	}
	observations := commandObservations(t, c)
	batch := observations["command-archive:1"]
	if batch.Targets[0].Association != "ledger_intent_observed" || batch.Targets[1].Association != "unrepresented" {
		t.Fatal("batch was collapsed", batch)
	}
	if observations["command-archive:3"].Targets[0].Association != "unverifiable" {
		t.Fatal("wrong environment trusted")
	}
	m, err := store.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if m.Resources["commands"].TotalRows != 501 || m.Resources["orders"].TotalRows != 0 || m.Consistency.CoverageVerified || m.MissingSecurityMeansZero {
		t.Fatal("overstated publication", m)
	}
	first, err := store.Page(ctx, scope, m.BaselineID, "commands", "", 500)
	if err != nil || first.Count != 500 || first.PageComplete {
		t.Fatal(first, err)
	}
	// New responsibility invalidates eligibility, not the fixed publication.
	a = archivedCommand(t, "late", "order.cancel", map[string]any{"account_id": scope.AccountID, "gateway_order_id": "done"})
	if _, err = db.ExecContext(ctx, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id,origin_message_id,body,body_text,received_at) VALUES($1,'1000-0','in','cmd.trade',$2,'late',$3::jsonb,$4,now())`, a.Archive.Stream, scope.AccountID, a.Archive.Body, a.Archive.Body); err != nil {
		t.Fatal(err)
	}
	stale := c
	stale.CollectionID = "stale"
	if _, err = store.PublishCandidate(ctx, stale); !errors.Is(err, ledger.ErrCurrentBaselineStale) {
		t.Fatal("revision did not guard commands", err)
	}
	store, _ = ledger.NewCurrentBaselineStore(db)
	last, err := store.Page(ctx, scope, m.BaselineID, "commands", first.NextCursor, 500)
	if err != nil || last.Count != 1 || !last.PageComplete || last.TotalRows != 501 {
		t.Fatal(last, err)
	}
	for _, doc := range append(first.Items, last.Items...) {
		var o CommandObservation
		if err = json.Unmarshal(doc, &o); err != nil {
			t.Fatal(err)
		}
		if o.OriginalBody != originals[o.Evidence.ArchiveID] || o.Evidence.BodySHA256 != digest([]byte(o.OriginalBody)) {
			t.Fatal("fixed page changed original bytes", o.RecordID)
		}
	}
	// Omission in an older candidate is not a successfully collected empty set.
	src, err = store.ReadSources(ctx, "test", scope, ids)
	if err != nil {
		t.Fatal(err)
	}
	src.TradeCommands = nil
	legacy, err := Assemble(src, "legacy", start)
	if err != nil {
		t.Fatal(err)
	}
	lm, err := store.PublishCandidate(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Page(ctx, scope, lm.BaselineID, "commands", "", 500); !errors.Is(err, ledger.ErrCurrentBaselineUnavailable) {
		t.Fatal("absent commands became empty", err)
	}
}

func TestCurrentAccountBaselineCommandMigrationDowngrade(t *testing.T) {
	db, ctx := sourceDatabase(t)
	store, _ := ledger.NewCurrentBaselineStore(db)
	down, err := os.ReadFile("../../migrations/postgres/000057_account_baseline_commands.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../migrations/postgres/000057_account_baseline_commands.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	src := fixtureSources(t, 0)
	legacy, err := Assemble(src, "legacy-migration", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	lm, err := store.PublishCandidate(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	if again, err := store.PublishCandidate(ctx, legacy); err != nil || again.BaselineID != lm.BaselineID {
		t.Fatal("old candidate identity changed", err)
	}
	src.TradeCommands = &ledger.CurrentBaselineCommandSources{}
	c, err := Assemble(src, "empty-command-collection", fixtureStart)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(down)); err == nil {
		t.Fatal("downgrade hid empty command resource")
	}
	page, err := store.Page(ctx, src.Scope, m.BaselineID, "commands", "", 1)
	if err != nil || page.Count != 0 || m.Resources["commands"].Completeness != "unavailable" {
		t.Fatal(page, err)
	}
}
