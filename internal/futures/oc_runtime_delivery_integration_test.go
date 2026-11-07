package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestOCRuntimeHeartbeatAuditPersistAndRecover(t *testing.T) {
	db, _, ctx := queryTestDB(t)
	outputs := runtimeHeartbeats(t)[:3]
	cfg := streamTestConfig("hb")
	cfg.Scope.Route = runtimeRoute(t, outputs[0])
	s, old := openStream(t, db, ctx, cfg)
	inputs := make([]StreamInput, len(outputs))
	for i, out := range outputs {
		inputs[i] = StreamInput{out.StreamID, []byte(out.Body)}
	}
	captureStream(t, s, ctx, old, "0-0", inputs...)
	h, err := s.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.HeartbeatAudit(ctx, h, 0, 10)
	if err != nil || len(page) != 3 {
		t.Fatal(err)
	}
	for i, record := range page {
		if !bytes.Equal(record.Receipt.Raw, []byte(outputs[i].Body)) || record.DecodeError != "" || record.Decoded == nil || record.Decoded.Recovery == nil || record.Decoded.TradeDate != "" || record.Decoded.BrokerReady {
			t.Fatal("stage1 audit rewritten, rejected or promoted", i, record.DecodeError)
		}
	}
	status, err := s.Status(ctx, h)
	if err != nil || status.Pending != 3 || status.Delivered != 0 || status.Cursor != outputs[2].StreamID {
		t.Fatal(status, err)
	}
}

func TestOCRuntimeQueryPagesUseActualPhysicalRoute(t *testing.T) {
	for _, name := range []string{"shared_redis_20261107.json", "sanitizer_private_20261107.json"} {
		t.Run(name, func(t *testing.T) {
			db, q, ctx := queryTestDB(t)
			raw, err := os.ReadFile("../../testdata/runtime_review/" + name)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Cases []struct {
					Command string            `json:"raw_command"`
					Outputs []ocRuntimeOutput `json:"outputs_after"`
				} `json:"extra_cases"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Cases) != 2 || len(doc.Cases[0].Outputs) != 4 {
				t.Fatal("incomplete query evidence", err)
			}
			evidence := doc.Cases[0]
			var envelope struct{ Routing QueryRoute }
			if err := json.Unmarshal([]byte(evidence.Outputs[0].Body), &envelope); err != nil {
				t.Fatal(err)
			}
			c := ocReplyContext(t, false, "positions")
			c.Route = envelope.Routing // Synthetic fixture binding, not runtime discovery.
			commands, _ := NewCommandStore(db)
			r, err := commands.Register(ctx, c.commandScope(), "cmd.query", []byte(evidence.Command), RegistrationEvidence{})
			if err != nil || r.Outcome != "registered" {
				t.Fatal(r, err)
			}
			c.Command = nil
			bindQuery(t, q, ctx, "stage1-pages", r, c)
			for i, output := range evidence.Outputs {
				if output.Stream != queryReplyStream(c.Route) {
					t.Fatal("physical source mismatch", output.Stream)
				}
				result, err := q.Ingest(ctx, "stage1-pages", QuerySource{output.Stream, output.StreamID}, []byte(output.Body), time.Duration(i))
				if err != nil || result.Replayed != (i == 2) || result.Published != (i == 3) {
					t.Fatal(result, err)
				}
				var saved []byte
				if err := db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations WHERE entry_id=$1`, output.StreamID).Scan(&saved); err != nil || !bytes.Equal(saved, []byte(output.Body)) {
					t.Fatal("source body changed", err)
				}
			}
			restored, _ := NewQueryStore(db)
			if state, err := restored.Recover(ctx, "stage1-pages"); err != nil || state != BatchReady {
				t.Fatal(state, err)
			}
			snapshot, err := restored.Snapshot(ctx, "stage1-pages")
			if err != nil || len(snapshot.Candidate.Positions) != 3 || storedCount(t, db, "futures_query_snapshots") != 1 || storedCount(t, db, "futures_query_observations") != 4 {
				t.Fatal("incomplete or duplicated snapshot", err)
			}
		})
	}
}
