package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func healthDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	db, ctx := baselineRevisionDatabase(t)
	raw, err := os.ReadFile("../../migrations/postgres/000054_account_baseline_source_health.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	execRevision(t, ctx, db, string(raw))
	return db, ctx
}

func healthBody(t *testing.T, scope CurrentBaselineScope, at time.Time, modify func(map[string]any)) string {
	t.Helper()
	env := scope.Environment
	if env == "production" {
		env = "prod"
	}
	w := map[string]any{
		"protocol": "relay.stream.v1", "message_type": "heartbeat", "message_id": "hb-" + at.Format(time.RFC3339Nano),
		"produced_at": at.Format(time.RFC3339Nano),
		"routing":     map[string]any{"env": env, "broker_id": scope.BrokerID, "gateway_id": scope.GatewayID, "account_id": scope.AccountID},
		"payload":     map[string]any{"counter_session_id": scope.CounterSessionID, "connection_generation": *scope.ConnectionGeneration, "trade_date": scope.TradeDate, "broker_ready": true, "redis_ready": true, "state": "RUNNING"},
	}
	if modify != nil {
		modify(w)
	}
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func hasHealthReason(h *CurrentBaselineSourceHealth, reason string) bool {
	for _, v := range h.Reasons {
		if v == reason {
			return true
		}
	}
	return false
}

func TestCurrentAccountBaselineHealthStreamIDs(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		cmp  int
	}{{"9-999", "10-0", -1}, {"10-2", "10-10", -1}, {"18446744073709551615-0", "9999999999999999999-0", 1}, {"0", "0-0", 0}} {
		x, e := CompareCurrentBaselineStreamIDs(tc.a, tc.b)
		if e != nil || x != tc.cmp {
			t.Fatal(tc, x, e)
		}
	}
	for _, bad := range []string{"", "01-0", "-1-0", "1-", "1-1-1", "1e2-0", "18446744073709551616-0"} {
		if _, e := CompareCurrentBaselineStreamIDs(bad, "0"); e == nil {
			t.Fatal("invalid ID", bad)
		}
	}
}

func TestCurrentAccountBaselineHealthHeartbeatValidation(t *testing.T) {
	scope := currentBaselineFixture(0).Scope
	at := time.Now().UTC()
	for _, tc := range []struct {
		name, reason string
		modify       func(map[string]any)
	}{
		{"complete", "", nil},
		{"missing generation", "heartbeat_connection_generation_missing", func(w map[string]any) { delete(w["payload"].(map[string]any), "connection_generation") }},
		{"wrong session", "heartbeat_counter_session_id_mismatch", func(w map[string]any) { w["counter_session_id"] = "other" }},
		{"null generation", "heartbeat_connection_generation_mismatch", func(w map[string]any) { w["connection_generation"] = nil }},
		{"false login", "heartbeat_broker_ready_false", func(w map[string]any) { w["payload"].(map[string]any)["broker_ready"] = false }},
		{"null login", "heartbeat_broker_ready_missing", func(w map[string]any) { w["payload"].(map[string]any)["broker_ready"] = nil }},
		{"conflicting login", "heartbeat_broker_ready_invalid", func(w map[string]any) { w["broker_ready"] = false }},
		{"wrong route", "heartbeat_routing_mismatch", func(w map[string]any) { w["routing"].(map[string]any)["gateway_id"] = "old" }},
		{"future publication", "heartbeat_time_in_future", func(w map[string]any) { w["produced_at"] = at.Add(time.Hour).Format(time.RFC3339Nano) }},
		{"stopping", "heartbeat_state_unavailable", func(w map[string]any) { w["payload"].(map[string]any)["state"] = "STOPPING" }},
		{"bad context", "heartbeat_context_invalid", func(w map[string]any) { w["adapter_context"] = []any{1} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &CurrentBaselineSourceHealth{ObservedAt: at.Add(time.Second)}
			r := CurrentBaselineArchive{ArchiveID: "1", EntryID: "100-0", Direction: "out", ReceivedAt: at, Body: healthBody(t, scope, at, tc.modify)}
			reviewCurrentBaselineHeartbeat(h, scope, r, scope.AccountID)
			if tc.reason != "" && !hasHealthReason(h, tc.reason) {
				t.Fatal(h.Reasons)
			}
			if tc.reason == "" && (len(h.Reasons) != 0 || !h.Heartbeat.ContextMatched || h.ContinuityVerified) {
				t.Fatal(h)
			}
			if h.Heartbeat.BodySHA256 != baselineHash([]byte(r.Body)) {
				t.Fatal("not original bytes")
			}
		})
	}
	h := &CurrentBaselineSourceHealth{ObservedAt: at}
	reviewCurrentBaselineHeartbeat(h, scope, CurrentBaselineArchive{Body: `{"message_id":"a","message_id":"b"}`}, scope.AccountID)
	if !hasHealthReason(h, "heartbeat_original_invalid") {
		t.Fatal("duplicate key accepted")
	}
}

func TestCurrentAccountBaselineHealthTransitions(t *testing.T) {
	db, ctx := healthDatabase(t)
	scope := currentBaselineFixture(0).Scope
	prefix := currentBaselineStreamPrefix(scope)
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	put := func(id string, when time.Time, modify func(map[string]any)) {
		t.Helper()
		body := healthBody(t, scope, when, modify)
		execRevision(t, ctx, db, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id,body,body_text,received_at)
VALUES($1,$2,'out','hb',$3,$4::jsonb,$4,$5)`, prefix+":hb", id, scope.AccountID, body, when)
	}
	put("100-0", at, nil)
	r := revisionNow(t, ctx, db, scope.AccountID)
	put("101-0", at.Add(time.Second), func(w map[string]any) { w["payload"].(map[string]any)["pending_query_count"] = 2 })
	if got := revisionNow(t, ctx, db, scope.AccountID); got != r {
		t.Fatal("normal heartbeat advanced revision", r, got)
	}
	for _, role := range []string{"reply", "event", "hb", "dlq"} {
		execRevision(t, ctx, db, `INSERT INTO stream_checkpoints(stream_key,stream_role,last_stream_id,last_processed_at) VALUES($1,$2,'101-0',$3)`, prefix+":"+role, role, at.Add(2*time.Second))
	}
	r = revisionNow(t, ctx, db, scope.AccountID)
	h, err := readCurrentBaselineHealth(ctx, db, scope, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !h.TrackingAvailable || !h.Heartbeat.ContextMatched || h.ContinuityVerified || len(h.Reasons) != 3 || !hasHealthReason(h, "durable_receipts_unavailable") {
		t.Fatal(h)
	}
	store, _ := NewCurrentBaselineStore(db)
	c := currentBaselineFixture(501)
	c.CollectionID = "health-transition"
	c.CompletedAt = h.ObservedAt
	c.StartedAt = at
	c.RevisionEvidence = &r
	c.SourceHealth = h
	m, err := store.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := store.CheckValidity(ctx, "test", scope, m.BaselineID)
	if err != nil || valid.Status != "unverifiable" || valid.SourceHealth == nil {
		t.Fatal(valid, err)
	}
	// A complete heartbeat arrival and worker progress do not bump the token.
	put("102-0", at.Add(3*time.Second), nil)
	execRevision(t, ctx, db, `UPDATE stream_checkpoints SET last_stream_id='102-0',last_processed_at=$2,processed_count=processed_count+1 WHERE stream_key=$1`, prefix+":hb", at.Add(4*time.Second))
	if got := revisionNow(t, ctx, db, scope.AccountID); got != r {
		t.Fatal("normal forward progress invalidates", r, got)
	}
	// Rolling back both the source observation and its health transition is atomic.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := healthBody(t, scope, at.Add(5*time.Second), func(w map[string]any) { w["payload"].(map[string]any)["broker_ready"] = false })
	_, err = tx.ExecContext(ctx, `INSERT INTO raw_stream_messages(stream_key,stream_id,direction,stream_role,account_id,body,body_text,received_at) VALUES($1,'103-0','out','hb',$2,$3::jsonb,$3,$4)`, prefix+":hb", scope.AccountID, bad, at.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if revisionNow(t, ctx, tx, scope.AccountID) == r {
		t.Fatal("health transition missing inside transaction")
	}
	if revisionNow(t, ctx, db, scope.AccountID) != r {
		t.Fatal("uncommitted transition visible")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if revisionNow(t, ctx, db, scope.AccountID) != r {
		t.Fatal("rollback left invalidation")
	}
	// A temporary different session must invalidate even after the old one returns.
	put("103-0", at.Add(5*time.Second), func(w map[string]any) { w["payload"].(map[string]any)["counter_session_id"] = "other-session" })
	put("104-0", at.Add(6*time.Second), nil)
	valid, err = store.CheckValidity(ctx, "test", scope, m.BaselineID)
	if err != nil || valid.Status != "invalidated" || !valid.SourceHealth.Heartbeat.ContextMatched {
		t.Fatal(valid, err)
	}
	page, err := store.Page(ctx, scope, m.BaselineID, "positions", "", 500)
	if err != nil || page.Count != 500 || *page.LedgerRevision != r.AccountRevision {
		t.Fatal(page, err)
	}
	// Same collection retry remains idempotent, not a fresh validity assertion.
	if retry, e := store.PublishCandidate(ctx, c); e != nil || retry.BaselineID != m.BaselineID {
		t.Fatal(retry, e)
	}
	c.CollectionID = "stale-health"
	if _, err = store.PublishCandidate(ctx, c); !errors.Is(err, ErrCurrentBaselineStale) {
		t.Fatal(err)
	}
	// Captured metadata must be an independent immutable copy.
	h.Reasons[0] = "mutated"
	original, err := store.Manifest(ctx, scope, m.BaselineID)
	if err != nil || original.SourceHealth.Reasons[0] == "mutated" {
		t.Fatal(original, err)
	}
}

func TestCurrentAccountBaselineHealthCheckpoints(t *testing.T) {
	db, ctx := healthDatabase(t)
	scope := currentBaselineFixture(0).Scope
	key := currentBaselineStreamPrefix(scope) + ":reply"
	execRevision(t, ctx, db, `INSERT INTO stream_checkpoints(stream_key,stream_role,last_stream_id,last_processed_at) VALUES($1,'reply','9-0',now())`, key)
	r := revisionNow(t, ctx, db, scope.AccountID)
	execRevision(t, ctx, db, `UPDATE stream_checkpoints SET last_stream_id='10-0',processed_count=1 WHERE stream_key=$1`, key)
	if revisionNow(t, ctx, db, scope.AccountID) != r {
		t.Fatal("lexical cursor comparison")
	}
	execRevision(t, ctx, db, `UPDATE stream_checkpoints SET last_stream_id='9-0' WHERE stream_key=$1`, key)
	if revisionNow(t, ctx, db, scope.AccountID) == r {
		t.Fatal("cursor regression invisible")
	}
	r = revisionNow(t, ctx, db, scope.AccountID)
	execRevision(t, ctx, db, `UPDATE stream_checkpoints SET last_error='parse failed',error_count=1,metadata='{"active_error":{"code":"PARSE_ERROR"}}' WHERE stream_key=$1`, key)
	h, err := readCurrentBaselineHealth(ctx, db, scope, time.Now().UTC())
	if err != nil || !hasHealthReason(h, "reply_checkpoint_active_error") {
		t.Fatal(h, err)
	}
	execRevision(t, ctx, db, `UPDATE stream_checkpoints SET metadata='{"active_error":null,"last_recovered_error":{"code":"PARSE_ERROR"}}' WHERE stream_key=$1`, key)
	h, err = readCurrentBaselineHealth(ctx, db, scope, time.Now().UTC())
	if err != nil || hasHealthReason(h, "reply_checkpoint_active_error") || h.Checkpoints[0].LastError != "parse failed" || *h.Checkpoints[0].ErrorCount != "1" {
		t.Fatal(h, err)
	}
	if revisionNow(t, ctx, db, scope.AccountID) == r {
		t.Fatal("recovered error revived old revision")
	}
	r = revisionNow(t, ctx, db, scope.AccountID)
	execRevision(t, ctx, db, `TRUNCATE stream_checkpoints`)
	if revisionNow(t, ctx, db, scope.AccountID) == r {
		t.Fatal("truncate invisible")
	}
	if got := revisionNow(t, ctx, db, "other"); got.UnscopedRevision != revisionNow(t, ctx, db, scope.AccountID).UnscopedRevision {
		t.Fatal("unscoped stream fault not conservative")
	}
}

func TestCurrentAccountBaselineHealthTrackingRequired(t *testing.T) {
	db, ctx := healthDatabase(t)
	c := currentBaselineFixture(0)
	h, err := readCurrentBaselineHealth(ctx, db, c.Scope, time.Now().UTC())
	if err != nil || !h.TrackingAvailable {
		t.Fatal(h, err)
	}
	c.CompletedAt = h.ObservedAt
	c.SourceHealth = h
	r := revisionNow(t, ctx, db, c.Scope.AccountID)
	c.RevisionEvidence = &r
	store, _ := NewCurrentBaselineStore(db)
	m, err := store.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	execRevision(t, ctx, db, `ALTER TABLE raw_stream_messages DISABLE TRIGGER account_baseline_heartbeat_watch`)
	v, err := store.CheckValidity(ctx, "test", c.Scope, m.BaselineID)
	if err != nil || v.Status != "unverifiable" || !hasHealthReason(v.SourceHealth, "source_health_tracking_unavailable") {
		t.Fatal(v, err)
	}
	c.CollectionID = "no-health-tracker"
	if _, err = store.PublishCandidate(ctx, c); !errors.Is(err, ErrCurrentBaselineTracking) {
		t.Fatal(err)
	}
	execRevision(t, ctx, db, `ALTER TABLE raw_stream_messages ENABLE TRIGGER account_baseline_heartbeat_watch`)
	for _, direction := range []string{"down", "up"} {
		raw, e := os.ReadFile("../../migrations/postgres/000054_account_baseline_source_health." + direction + ".sql")
		if e != nil {
			t.Fatal(e)
		}
		execRevision(t, ctx, db, string(raw))
	}
	v, err = store.CheckValidity(ctx, "test", c.Scope, m.BaselineID)
	if err != nil || v.Status != "invalidated" || !v.SourceHealth.TrackingAvailable {
		t.Fatal(v, err)
	}
}

func TestCurrentAccountBaselineHealthCandidateCopy(t *testing.T) {
	c := currentBaselineFixture(0)
	c.SourceHealth = &CurrentBaselineSourceHealth{ObservedAt: c.CompletedAt, Reasons: []string{"unverified"}}
	copy, _, err := prepareCurrentBaseline(c)
	if err != nil {
		t.Fatal(err)
	}
	c.SourceHealth.Reasons[0] = "different"
	if reflect.DeepEqual(copy.SourceHealth, c.SourceHealth) {
		t.Fatal("source health aliased")
	}
	c.SourceHealth.ContinuityVerified = true
	if _, _, err = prepareCurrentBaseline(c); err == nil {
		t.Fatal("caller fabricated continuity")
	}
	c.SourceHealth.ContinuityVerified = false
	c.SourceHealth.ObservedAt = c.CompletedAt.Add(time.Second)
	if _, _, err = prepareCurrentBaseline(c); err == nil {
		t.Fatal("different source transaction time accepted")
	}
	c.SourceHealth.ObservedAt = c.CompletedAt
	c.SourceHealth.Reasons = []string{strings.Repeat("x", 1<<20)}
	if _, _, err = prepareCurrentBaseline(c); !errors.Is(err, ErrCurrentBaselineLimit) {
		t.Fatal(fmt.Sprint(err))
	}
}
