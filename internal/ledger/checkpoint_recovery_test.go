package ledger

import (
	"context"
	"encoding/json"
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
