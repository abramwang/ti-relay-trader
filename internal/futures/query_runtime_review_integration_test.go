package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestRuntimeFactsPublishedReviewedAndStoredPostgres(t *testing.T) {
	for _, v := range runtimeFacts(t) {
		t.Run(v.Name, func(t *testing.T) {
			db, q, ctx := queryTestDB(t)
			s, _ := NewCommandStore(db)
			raw := []byte(v.Outputs[0].Body)
			c := runtimeFactsContext(t, raw)
			// A local synthetic command with this sample's correlation. The OC
			// output below is retained byte-for-byte, never sent to Redis.
			body := editBody(t, constraintsCases(t)[0].Request, func(m map[string]any) {
				m["message_id"], m["request_id"] = c.OriginMessageID, "req-one"
				m["correlation_id"], m["idempotency_key"] = "fixture-correlation", "idem-one"
			})
			r, err := s.Register(ctx, c.commandScope(), "cmd.query", body, RegistrationEvidence{})
			if err != nil {
				t.Fatal(err)
			}
			target := publicationTarget()
			target.CounterSessionID, target.TradeDate = c.CounterSessionID, c.TradeDate
			f := &fakeQueryPublisher{}
			if _, err = s.publishTestQuery(ctx, c.commandScope(), r.CommandID, c.OriginMessageID, target, f); err != nil {
				t.Fatal(err)
			}
			review, evidence := reviewFixture(t, c, target, r.CommandID)
			wrong := editBody(t, review, func(m map[string]any) {
				m["query_context"].(map[string]any)["Constraints"].(map[string]any)["symbol"] = "rb2702"
			})
			if _, _, err := q.RegisterReviewedQuery(ctx, wrong, evidence); err == nil {
				t.Fatal("foreign contract bound")
			}
			accepted, _, err := q.RegisterReviewedQuery(ctx, review, evidence)
			if err != nil {
				t.Fatal(err)
			}
			out, err := q.Ingest(ctx, accepted.BatchID, QuerySource{queryReplyStream(c.Route), "3-0"}, raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			want := BatchFailed
			if v.Name == "complete" || v.Name == "null_price" || v.Name == "prefix_option" {
				want = BatchReady
			}
			if out.State != want || out.Published != (want == BatchReady) {
				t.Fatal(out)
			}
			if want == BatchFailed && storedCount(t, db, "futures_query_snapshots") != 0 {
				t.Fatal("failed facts promoted")
			}
			var stored []byte
			if err := db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations WHERE batch_id=$1`, accepted.BatchID).Scan(&stored); err != nil || !bytes.Equal(stored, raw) {
				t.Fatal("raw lost", err)
			}
			if again, err := q.Ingest(ctx, accepted.BatchID, QuerySource{queryReplyStream(c.Route), "3-0"}, raw, time.Second); err != nil || !again.Replayed {
				t.Fatal("replay", err)
			}
			fresh, _ := NewQueryStore(db)
			if _, err := fresh.Current(ctx, c); !errors.Is(err, ErrNoSnapshot) {
				t.Fatal("restart revived facts", err)
			}
			if replay, err := s.publishTestQuery(ctx, c.commandScope(), r.CommandID, c.OriginMessageID, target, f); err != nil || !replay.Replayed || f.appends.Load() != 1 {
				t.Fatal("query repeated", err)
			}
		})
	}
}

func TestConstraintsUnknownPublicationNotRetriedPostgres(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	s, _ := NewCommandStore(db)
	c := constraintsContext()
	r, err := s.Register(ctx, c.commandScope(), "cmd.query", constraintsCases(t)[0].Request, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	target := publicationTarget()
	target.CounterSessionID, target.TradeDate = c.CounterSessionID, c.TradeDate
	f := &fakeQueryPublisher{appendError: true}
	for i := 0; i < 2; i++ {
		if _, err := s.publishTestQuery(ctx, c.commandScope(), r.CommandID, c.OriginMessageID, target, f); !errors.Is(err, ErrQueryPublishUnknown) {
			t.Fatal(err)
		}
	}
	if f.appends.Load() != 1 {
		t.Fatal("unknown publication retried")
	}
	review, evidence := reviewFixture(t, c, target, r.CommandID)
	if _, _, err := q.RegisterReviewedQuery(ctx, review, evidence); err == nil {
		t.Fatal("unconfirmed publication applied")
	}
	if storedCount(t, db, "futures_query_batches") != 0 {
		t.Fatal("unknown request bound")
	}
}

func TestConstraintsUnexecutedFailureAuditedPostgres(t *testing.T) {
	db, q, ctx := queryTestDB(t)
	s, _ := NewCommandStore(db)
	c := constraintsContext()
	r, err := s.Register(ctx, c.commandScope(), "cmd.query", constraintsCases(t)[0].Request, RegistrationEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	bindQuery(t, q, ctx, "not-executed", r, c)
	raw := editBody(t, constraintsCases(t)[0].Reply, func(m map[string]any) {
		m["status"], m["code"] = "failed", "QUERY_FAILED"
		delete(m, "chunk")
		delete(m["payload"].(map[string]any), "items")
		m["adapter_context"].(map[string]any)["query_evidence"] = map[string]any{"instrument": nil, "depth": nil}
	})
	out, err := q.Ingest(ctx, "not-executed", QuerySource{queryReplyStream(c.Route), "1-0"}, raw, 0)
	if err != nil || out.State != BatchFailed || out.Published {
		t.Fatal(out, err)
	}
	var stored []byte
	if err := db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations`).Scan(&stored); err != nil || !bytes.Equal(stored, raw) {
		t.Fatal(err)
	}
	var m map[string]any
	if json.Unmarshal(stored, &m) != nil || m["adapter_context"].(map[string]any)["query_evidence"].(map[string]any)["depth"] != nil {
		t.Fatal("unexecuted leg fabricated")
	}
}
