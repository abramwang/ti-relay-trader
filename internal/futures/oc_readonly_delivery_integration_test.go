package futures

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOCReadOnlyEvidenceRetainsOriginalBytes(t *testing.T) {
	_, store, config, _, _, ctx := evidenceFixture(t)
	config.Scope.Route = readOnlyRoute()
	handle, err := store.OpenHeartbeatCapture(ctx, config, HeartbeatCheckpoint{Cursor: "0-0"})
	if err != nil {
		t.Fatal(err)
	}
	raw := readOnlyScenarios(t)[0].Heartbeats[1]
	captureStream(t, store, ctx, handle, "0-0", StreamInput{"1-0", raw})
	cp, err := store.HeartbeatCheckpoint(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.HeartbeatEvidence(ctx, config, cp, 0, 10, true)
	if err != nil || len(page.Records) != 1 || page.BusinessAuthorized {
		t.Fatal(page, err)
	}
	row := page.Records[0]
	if row.Classification != "test_broker_read_only" || row.BodyBase64 == nil || *row.BodyBase64 != base64.StdEncoding.EncodeToString(raw) {
		t.Fatal("raw audit lost")
	}
}

func TestOCReadOnlyWindowCarriesModeAndStopsOnBudget(t *testing.T) {
	w, db, ctx := heartbeatWindowFixture(t)
	body := func(id string, offset time.Duration, ready bool) []byte {
		return editBody(t, readOnlyScenarios(t)[0].Heartbeats[1], func(m map[string]any) {
			m["routing"] = w.guard.scope.Route
			m["message_id"] = id
			m["produced_at"] = w.startedAt.Add(time.Millisecond + offset).Format(time.RFC3339Nano)
			m["counter_session_id"] = w.guard.expected.CounterSessionID
			p := m["payload"].(map[string]any)
			p["counter_session_id"], p["trade_date"] = w.guard.expected.CounterSessionID, w.guard.expected.Scope.TradeDate
			m["adapter_context"].(map[string]any)["query_collection_ready"] = ready
		})
	}
	captureStream(t, w.store, ctx, w.handle, "0-0", StreamInput{"1-0", body("ready", 0, true)})
	if _, err := observeWindow(w, ctx, 10); err != nil || !w.guard.readOnlyMode {
		t.Fatal("read-only mode lost", err)
	}
	captureStream(t, w.store, ctx, w.handle, "1-0", StreamInput{"2-0", body("budget", time.Millisecond, false)}, StreamInput{"3-0", body("later", 2*time.Millisecond, true)})
	if _, err := observeWindow(w, ctx, 10); !errors.Is(err, ErrTestSessionStopped) || !strings.Contains(err.Error(), "query_collection_not_ready") {
		t.Fatal("budget did not stop window", err)
	}
	if storedCount(t, db, "futures_heartbeat_observations") != 2 || storedCount(t, db, "futures_stream_inbox") != 3 {
		t.Fatal("budget skipped or raw lost")
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
