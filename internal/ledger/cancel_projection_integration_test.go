package ledger

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

func TestCancelProjectionPostgres(t *testing.T) {
	dsn := os.Getenv("RELAY_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RELAY_LEDGER_TEST_DATABASE_URL to run isolated PostgreSQL cancel projection test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Shadow ledger tables inside this rollback-only connection; never write broker facts.
	for _, statement := range []string{
		`CREATE TEMP SEQUENCE cancel_projection_test_seq`,
		`CREATE TEMP TABLE order_cancel_attempts (LIKE public.order_cancel_attempts INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES) ON COMMIT DROP`,
		`ALTER TABLE order_cancel_attempts ALTER COLUMN cancel_attempt_pk SET DEFAULT nextval('pg_temp.cancel_projection_test_seq')`,
		`CREATE TEMP TABLE raw_stream_messages (
			raw_message_pk BIGINT GENERATED ALWAYS AS IDENTITY, body JSONB, account_id TEXT,
			action TEXT, status TEXT, code TEXT, request_id TEXT, stream_key TEXT, stream_id TEXT,
			message_type TEXT, origin_message_id TEXT, received_at TIMESTAMPTZ DEFAULT now()
		) ON COMMIT DROP`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewRepository(tx)
	yes, no := true, false
	accepted := OrderCancelAttempt{
		AttemptID: "cancel-test", OriginMessageID: "cancel-test", AccountID: "test-account",
		GatewayOrderID: "gw-test", TradeDate: "2026-10-06", Status: "accepted", Code: "OK",
		RetrySafe: &no, OrderStateChanged: &no, OccurredAt: time.Now(), StreamKey: "relay:test:v1:hx:acct:reply",
	}
	checkWrite := func(attempt OrderCancelAttempt, want bool) {
		t.Helper()
		changed, err := repo.UpsertOrderCancelAttemptChanged(ctx, attempt)
		if err != nil || changed != want {
			t.Fatalf("write %s/%s changed=%v want=%v err=%v", attempt.Status, attempt.StreamKey, changed, want, err)
		}
	}
	checkWrite(accepted, true)
	checkWrite(accepted, false)
	rejected := accepted
	rejected.Status, rejected.Code, rejected.Message = "rejected", "BROKER_CANCEL_REJECTED", "controlled rejection"
	rejected.RetrySafe = &yes
	rejected.StreamKey = "relay:test:v1:hx:acct:event"
	checkWrite(rejected, true)
	checkWrite(accepted, false)
	rejected.StreamID = "different-raw-id"
	rejected.OccurredAt = time.Now().Add(time.Second)
	checkWrite(rejected, false)
	lateReply := rejected
	lateReply.Status, lateReply.StreamKey = "failed", accepted.StreamKey
	checkWrite(lateReply, false)
	attempts, err := repo.ListOrderCancelAttempts(ctx, OrderCancelAttemptQuery{AccountID: "test-account"})
	if err != nil || len(attempts) != 1 || attempts[0].Status != "rejected" || attempts[0].ReconciliationRequired {
		t.Fatalf("rejected read=%#v err=%v", attempts, err)
	}

	timeout := rejected
	timeout.Status, timeout.Code = "timeout", "CANCEL_RESPONSE_TIMEOUT"
	timeout.StreamKey = "relay:test:v1:hx:acct:dlq"
	timeout.ReconciliationRequired, timeout.RetrySafe = true, &no
	timeout.RawPayload = map[string]any{
		"controlled_test_fault": true, "fault_plan_id": "plan-test",
		"original_body": map[string]any{"message_id": "cancel-test", "idempotency_key": "idem-test"},
	}
	checkWrite(timeout, true)
	checkWrite(timeout, false)
	checkWrite(accepted, false)
	checkWrite(rejected, false)
	wrongOrder := timeout
	wrongOrder.GatewayOrderID = "other-order"
	checkWrite(wrongOrder, false)
	newAttempt := accepted
	newAttempt.AttemptID, newAttempt.OriginMessageID = "manual-cancel-new", "manual-cancel-new"
	checkWrite(newAttempt, true)

	_, err = tx.ExecContext(ctx, `INSERT INTO raw_stream_messages
		(body, account_id, action, status, code, message_type, origin_message_id, stream_key, stream_id)
		VALUES ('{"message_id":"reply-test","result_type":"order_action_receipt"}',
		'test-account','order.cancel','accepted','OK','reply','cancel-test','test:reply','1-0')`)
	if err != nil {
		t.Fatal(err)
	}
	status, err := repo.GetCommandStatus(ctx, "cancel-test")
	if err != nil || status.State != "accepted" || !status.Success || len(status.CancelAttempts) != 1 {
		t.Fatalf("command status=%#v err=%v", status, err)
	}
	outcome := status.CancelAttempts[0]
	if outcome.Status != "timeout" || !outcome.ReconciliationRequired || outcome.RetrySafe == nil || *outcome.RetrySafe {
		t.Fatalf("unknown outcome=%#v", outcome)
	}
	if raw := outcome.RawPayload.(map[string]any); raw["fault_plan_id"] != "plan-test" || raw["original_body"] == nil {
		t.Fatalf("raw fault evidence=%#v", raw)
	}
	withoutReply, err := repo.GetCommandStatus(ctx, "manual-cancel-new")
	if err != nil || withoutReply.State != "pending" || withoutReply.Success || len(withoutReply.CancelAttempts) != 1 {
		t.Fatalf("outcome without reply=%#v err=%v", withoutReply, err)
	}
}
