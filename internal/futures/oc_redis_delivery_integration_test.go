package futures

import (
	"bytes"
	"testing"
	"time"
)

func TestOCRedisReceivedQueryPagesPersistAndRecover(t *testing.T) {
	for index, file := range ocRedisFiles {
		t.Run(file.name, func(t *testing.T) {
			db, q, ctx := queryTestDB(t)
			c, evidence := ocRedisPages(t, index)
			commands, _ := NewCommandStore(db)
			r, err := commands.Register(ctx, c.commandScope(), "cmd.query", []byte(evidence.Command), RegistrationEvidence{})
			if err != nil || r.Outcome != "registered" {
				t.Fatal(r, err)
			}
			c.Command = nil
			bindQuery(t, q, ctx, "received-redis-pages", r, c)
			for i, output := range evidence.Outputs {
				// Canonical source is a local test precondition. OC evidence does not
				// provide the output key; this does not attest live stream routing.
				source := QuerySource{queryReplyStream(c.Route), output.StreamID}
				result, err := q.Ingest(ctx, "received-redis-pages", source, []byte(output.Body), time.Duration(i))
				if err != nil || result.Replayed != (i == 2) || result.Published != (i == 3) {
					t.Fatal(result, err)
				}
				want := BatchCollecting
				if i == 3 {
					want = BatchReady
				}
				if result.State != want {
					t.Fatal(result)
				}
			}
			if storedCount(t, db, "futures_query_snapshots") != 1 || storedCount(t, db, "futures_query_observations") != 4 {
				t.Fatal("duplicate snapshot or lost transport audit")
			}
			for _, output := range evidence.Outputs {
				var saved []byte
				if err := db.QueryRowContext(ctx, `SELECT body FROM futures_query_observations WHERE entry_id=$1`, output.StreamID).Scan(&saved); err != nil || !bytes.Equal(saved, []byte(output.Body)) {
					t.Fatal("original body changed", err)
				}
			}
			restored, _ := NewQueryStore(db)
			if state, err := restored.Recover(ctx, "received-redis-pages"); err != nil || state != BatchReady {
				t.Fatal(state, err)
			}
			snapshot, err := restored.Snapshot(ctx, "received-redis-pages")
			if err != nil || len(snapshot.Candidate.Positions) != 3 {
				t.Fatal(snapshot, err)
			}
			if storedCount(t, db, "futures_query_snapshots") != 1 {
				t.Fatal("recovery duplicated snapshot")
			}
		})
	}
}
