package futures

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeQueryPublisher struct {
	inspections atomic.Int32
	appends     atomic.Int32
	gateError   bool
	rejected    bool
	appendError bool
	appendHook  func()
}

func (f *fakeQueryPublisher) inspect(context.Context) (queryPublishProof, error) {
	f.inspections.Add(1)
	if f.gateError {
		return queryPublishProof{}, ErrQueryPublishGate
	}
	return queryPublishProof{EntryID: "1-0", Body: []byte("synthetic proof"), CheckedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (f *fakeQueryPublisher) appendQuery(context.Context, queryPublishProof, []byte) (string, bool, error) {
	f.appends.Add(1)
	if f.appendHook != nil {
		f.appendHook()
	}
	if f.appendError {
		return "", false, errors.New("synthetic lost response")
	}
	if f.rejected {
		return "", true, nil
	}
	return "2-0", false, nil
}

func TestQueryPublicationDurabilityAndNoResend(t *testing.T) {
	for _, scenario := range []string{"published", "response_lost", "atomic_gate", "preflight", "insert_failed", "completion_failed"} {
		t.Run(scenario, func(t *testing.T) {
			db, s, ctx := commandTestDB(t)
			v := intentVectors(t)["account.asset.query"]
			r := registerVector(t, s, ctx, v, RegistrationEvidence{}, "registered")
			f := &fakeQueryPublisher{appendError: scenario == "response_lost", rejected: scenario == "atomic_gate", gateError: scenario == "preflight"}
			if scenario == "insert_failed" || scenario == "completion_failed" {
				_, err := db.ExecContext(ctx, `CREATE FUNCTION reject_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$`)
				if err != nil {
					t.Fatal(err)
				}
				event := "INSERT"
				if scenario == "completion_failed" {
					event = "UPDATE"
				}
				_, err = db.ExecContext(ctx, "CREATE TRIGGER fail_publication BEFORE "+event+" ON futures_test_query_publications FOR EACH ROW EXECUTE FUNCTION reject_publication()")
				if err != nil {
					t.Fatal(err)
				}
			}
			f.appendHook = func() {
				var state string
				if err := db.QueryRowContext(ctx, `SELECT state FROM futures_test_query_publications WHERE command_id=$1`, r.CommandID).Scan(&state); err != nil || state != "attempted" {
					t.Error("network before durable attempt", err)
				}
			}
			result, err := s.publishTestQuery(ctx, v.scope(), r.CommandID, r.Original.MessageID, publicationTarget(), f)
			switch scenario {
			case "published":
				if err != nil || result.Published == nil || !*result.Published || result.StreamID != "2-0" {
					t.Fatal(result, err)
				}
			case "response_lost", "completion_failed":
				if !errors.Is(err, ErrQueryPublishUnknown) || result.Published != nil {
					t.Fatal(result, err)
				}
			default:
				if err == nil {
					t.Fatal("failure accepted")
				}
			}
			if scenario == "preflight" || scenario == "insert_failed" {
				if f.appends.Load() != 0 || storedCount(t, db, "futures_test_query_publications") != 0 {
					t.Fatal("failed preparation sent")
				}
				return
			}
			// A new store simulates a new process, not an in-memory retry cache.
			fresh, _ := NewCommandStore(db)
			again, againErr := fresh.publishTestQuery(ctx, v.scope(), r.CommandID, r.Original.MessageID, publicationTarget(), f)
			if !again.Replayed || f.appends.Load() != 1 || f.inspections.Load() != 1 {
				t.Fatal("replayed query resent", again, againErr)
			}
			if scenario == "response_lost" || scenario == "completion_failed" {
				if !errors.Is(againErr, ErrQueryPublishUnknown) {
					t.Fatal(againErr)
				}
			}
			if scenario == "published" && againErr != nil {
				t.Fatal(againErr)
			}
			wrong := publicationTarget()
			wrong.Epoch = "other"
			if _, e := fresh.publishTestQuery(ctx, v.scope(), r.CommandID, r.Original.MessageID, wrong, f); e == nil {
				t.Fatal("retargeted attempt")
			}
			if _, e := fresh.publishTestQuery(ctx, v.scope(), r.CommandID, "alias", publicationTarget(), f); e == nil {
				t.Fatal("alias sent")
			}
		})
	}
}

func TestQueryPublicationConcurrentSingleAttempt(t *testing.T) {
	db, s, ctx := commandTestDB(t)
	v := intentVectors(t)["order.list.query"]
	r := registerVector(t, s, ctx, v, RegistrationEvidence{}, "registered")
	f := &fakeQueryPublisher{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.publishTestQuery(ctx, v.scope(), r.CommandID, r.Original.MessageID, publicationTarget(), f)
			if err != nil && !errors.Is(err, ErrQueryPublishUnknown) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.appends.Load() != 1 || storedCount(t, db, "futures_test_query_publications") != 1 {
		t.Fatal("multiple attempts", f.appends.Load())
	}
}

func TestQueryPublicationRejectsTrade(t *testing.T) {
	_, s, ctx := commandTestDB(t)
	v := intentVectors(t)["submit"]
	r := registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	f := &fakeQueryPublisher{}
	if _, err := s.publishTestQuery(ctx, v.scope(), r.CommandID, r.Original.MessageID, publicationTarget(), f); err == nil || f.appends.Load() != 0 || f.inspections.Load() != 0 {
		t.Fatal("trade reached publisher", err)
	}
}
