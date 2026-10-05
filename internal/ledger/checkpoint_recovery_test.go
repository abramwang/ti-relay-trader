package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStructureCheckpointErrorPreservesCursorAndHistory(t *testing.T) {
	exec := &recordingExecutor{result: rowsAffectedResult(1)}
	repo := NewRepository(exec)
	cp := StreamCheckpoint{StreamKey: "reply", LastStreamID: "999-0", LastError: "123-0: original error"}
	ok, err := repo.StructureStreamCheckpointError(context.Background(), cp, map[string]any{"code": "BROKER_NOT_READY", "stream_id": "123-0"})
	if err != nil || !ok {
		t.Fatalf("structure: %v %v", ok, err)
	}
	requireQueryContains(t, exec.query, "last_stream_id = $2 AND last_error = $3")
	requireQueryContains(t, exec.query, "SET metadata = metadata ||")
	if exec.args[1] != "999-0" || exec.args[2] != cp.LastError {
		t.Fatal("wrong compare-and-set identity")
	}
	var metadata map[string]any
	if err := json.Unmarshal(exec.args[3].([]byte), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["last_error_stream_id"] != "123-0" || metadata["active_error"] == nil {
		t.Fatal("error identity lost")
	}
}

func TestLegacyDispositionRequiresMatchingSessionAndUnchangedOrder(t *testing.T) {
	exec := &recordingQueryExecutor{err: errors.New("read-only query stub")}
	repo := NewRepository(exec)
	_, _ = repo.ResolvedTestOrderKeys(context.Background(), "a", "current-session")
	requireQueryContains(t, exec.query, "r.environment = 'test'")
	requireQueryContains(t, exec.query, "r.counter_session_id = $2")
	requireQueryContains(t, exec.query, "r.order_snapshot = jsonb_build_object")
	requireQueryContains(t, exec.query, "'cum_filled_qty', o.cum_filled_qty")
	if exec.args[0] != "a" || exec.args[1] != "current-session" {
		t.Fatal("resolution scope not bound")
	}
	_, _ = repo.CheckpointReconciliationBlocked(context.Background(), "a", "current-session")
	requireQueryContains(t, exec.query, "AND NOT o.is_terminal AND NOT EXISTS")
	requireQueryContains(t, exec.query, "r.order_snapshot = jsonb_build_object")
	_, _ = repo.CheckpointRecoveryBlocked(context.Background(), "a")
	if strings.Contains(exec.query, "test_legacy_order_resolutions") {
		t.Fatal("normal/production recovery depends on TEST resolution table")
	}
}

func TestRecoveryCheckpointCASRefusesConcurrentChange(t *testing.T) {
	exec := &recordingExecutor{result: rowsAffectedResult(0)}
	repo := NewRepository(exec)
	ok, err := repo.RecoverStreamCheckpoint(context.Background(), "reply", "999-0", "123-0: error", map[string]any{"stream_id": "123-0"})
	if err != nil || ok {
		t.Fatalf("CAS conflict accepted: %v %v", ok, err)
	}
	requireQueryContains(t, exec.query, "last_stream_id = $2 AND last_error = $3")
	var metadata map[string]any
	if err := json.Unmarshal(exec.args[3].([]byte), &metadata); err != nil {
		t.Fatal(err)
	}
	if value, present := metadata["active_error"]; !present || value != nil || metadata["last_recovered_error"] == nil {
		t.Fatal("recovery audit missing")
	}
}

func TestBlockedReconciliationAttemptKeepsActiveError(t *testing.T) {
	exec := &recordingExecutor{result: rowsAffectedResult(1)}
	repo := NewRepository(exec)
	cp := StreamCheckpoint{StreamKey: "reply", LastStreamID: "999-0", LastError: "123-0: original"}
	ok, err := repo.RecordCheckpointReconciliationAttempt(context.Background(), cp, "123-0", map[string]any{"status": "blocked", "unresolved_order_count": 32})
	if err != nil || !ok {
		t.Fatalf("record audit: %v %v", ok, err)
	}
	requireQueryContains(t, exec.query, "metadata->'active_error'->>'stream_id' = $4")
	var metadata map[string]any
	if err := json.Unmarshal(exec.args[4].([]byte), &metadata); err != nil {
		t.Fatal(err)
	}
	if _, present := metadata["active_error"]; present {
		t.Fatal("blocked audit replaced active error")
	}
	if metadata["last_reconciliation_attempt"] == nil {
		t.Fatal("blocked audit not saved")
	}
}

func TestSuccessfulReconciliationReplacesLatestBlockedAttempt(t *testing.T) {
	exec := &recordingExecutor{result: rowsAffectedResult(1)}
	repo := NewRepository(exec)
	ok, err := repo.RecoverStreamCheckpoint(context.Background(), "reply", "999-0", "123-0: original", map[string]any{"account_id": "a", "recovery_source": "cross_session_full_readonly_reconciliation"})
	if err != nil || !ok {
		t.Fatalf("recover: %v %v", ok, err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(exec.args[3].([]byte), &metadata); err != nil {
		t.Fatal(err)
	}
	attempt, _ := metadata["last_reconciliation_attempt"].(map[string]any)
	if attempt["status"] != "recovered" || attempt["evidence"] == nil {
		t.Fatal("successful recovery left latest attempt blocked")
	}
}
