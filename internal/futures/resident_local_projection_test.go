package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// Explicitly synthetic local intent/publication, never sent to shared Redis.
func residentProjectionCommand(t *testing.T, ctx context.Context, db *sql.DB, scope CommandScope, target TestQueryTarget) CommandRegistration {
	t.Helper()
	migrateFuturesTest(t, db, ctx, "000046_futures_test_trade_publication", "up")
	v, _, _, _ := assessmentFixture(t)
	v.Scope = map[string]string{"env": scope.Route.Env, "broker_id": scope.Route.BrokerID, "gateway_id": scope.Route.GatewayID, "account_id": scope.Route.AccountID, "native_broker_id": scope.NativeBrokerID, "investor_id": scope.InvestorID, "user_id": scope.UserID}
	v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
		m["payload"].(map[string]any)["account_id"] = scope.Route.AccountID
		m["payload"].(map[string]any)["trade_date"] = target.TradeDate
	}))
	s, _ := NewCommandStore(db)
	r := registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	binding, _ := json.Marshal(map[string]any{"target": target})
	if _, err := db.ExecContext(ctx, `INSERT INTO futures_test_trade_publications(command_id,assessment_key,action,target_json,proof_json,state,entry_id,finished_at) VALUES($1,'synthetic-resident-projection','order.submit',$2,'{}','published','1-0',clock_timestamp())`, r.CommandID, binding); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResidentLocalProjectionAndHealth(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "applied", true: "rollback"}[fail], func(t *testing.T) {
			ctx, source, store, p, o := residentFixture(t)
			r := residentProjectionCommand(t, ctx, store.db, source.scope, p.target)
			raw := assessmentOrderBody(t, r, p.target, nil)
			key, _ := source.StreamConfig("event").stream()
			residentAppend(t, ctx, source, key, "1-0", string(raw))
			if fail {
				if _, err := store.db.ExecContext(ctx, `CREATE FUNCTION fail_resident_projection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$; CREATE TRIGGER fail_resident_projection BEFORE INSERT ON futures_trade_observations FOR EACH ROW EXECUTE FUNCTION fail_resident_projection()`); err != nil {
					t.Fatal(err)
				}
			}
			cp := ResidentCheckpoints{Reply: HeartbeatCheckpoint{Cursor: "0-0"}, Event: HeartbeatCheckpoint{Cursor: "0-0"}}
			run, cancel := context.WithCancel(ctx)
			defer cancel()
			saw := false
			err := FollowTestLocalEventPair(run, source, store, p, o, cp, func(v ResidentCaptureProgress) error {
				if v.Mode != "test_resident_local_event_projection" || v.BusinessAuthorized {
					t.Error("mode grants unexpected authority", v)
				}
				if v.LocalProjection != nil && v.LocalProjection.Applied > 0 {
					saw = true
					cancel()
				}
				return nil
			})
			if !fail && (err != context.Canceled || !saw) {
				t.Fatal(saw, err)
			}
			if fail && (err == nil || errors.Is(err, ErrResidentCaptureBudget)) {
				t.Fatal(err)
			}
			health, _, e := readCaptureHealth(ctx, store.db, source.scope, p.target)
			if e == nil || health.Ready {
				t.Fatal(health, e)
			}
			var healthState string
			if err := store.db.QueryRowContext(ctx, `SELECT state FROM futures_capture_health WHERE route_key=$1`, string(commandRouteBytes(source.scope.Route))).Scan(&healthState); err != nil || healthState != "stopped" {
				t.Fatal(healthState, err)
			}
			want := 1
			if fail {
				want = 0
			}
			if storedCount(t, store.db, "futures_order_states") != want {
				t.Fatal("wrong projection count")
			}
			var state string
			if err := store.db.QueryRowContext(ctx, `SELECT state FROM futures_stream_inbox WHERE stream_key=$1`, key).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != map[bool]string{false: "delivered", true: "pending"}[fail] {
				t.Fatal(state)
			}
			for _, suffix := range []string{"cmd.trade", "cmd.query"} {
				if n, err := source.reader.client.XLen(ctx, p.prefix()+":"+suffix).Result(); err != nil || n != 0 {
					t.Fatal("receiver sent a command", n, err)
				}
			}
		})
	}
}
