package futures

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func evidenceFixture(t *testing.T, bodies ...[]byte) (*sql.DB, *StreamStore, StreamConfig, StreamHandle, HeartbeatCheckpoint, context.Context) {
	t.Helper()
	db, _, ctx := queryTestDB(t)
	s, _ := NewStreamStore(db)
	c := captureConfig()
	h, err := s.OpenHeartbeatCapture(ctx, c, HeartbeatCheckpoint{Cursor: "0-0"})
	if err != nil {
		t.Fatal(err)
	}
	for i, body := range bodies {
		cursor, _ := s.Cursor(ctx, h)
		id := strconv.Itoa(i+1) + "-0"
		captureStream(t, s, ctx, h, cursor, StreamInput{id, body})
	}
	cp, err := s.HeartbeatCheckpoint(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return db, s, c, h, cp, ctx
}

func TestHeartbeatEvidenceRawPaginationIsReadOnly(t *testing.T) {
	sample := runtimeHeartbeats(t)[0]
	// The fixture route is deliberately independent of the body source.
	bodies := [][]byte{[]byte(sample.Body), {0xff, 0}, {}, []byte(sample.Body)}
	db, s, c, _, cp, ctx := evidenceFixture(t, bodies...)
	var after int64
	for i := range bodies {
		page, err := s.HeartbeatEvidence(ctx, c, cp, after, 1, false)
		if err != nil || len(page.Records) != 1 || page.Records[0].BodyBase64 != nil || page.BusinessAuthorized || page.Checkpoint != cp || page.More != (i < len(bodies)-1) {
			t.Fatal(page, err)
		}
		rawPage, err := s.HeartbeatEvidence(ctx, c, cp, after, 1, true)
		if err != nil || rawPage.Records[0].BodyBase64 == nil {
			t.Fatal(err)
		}
		body, err := base64.StdEncoding.DecodeString(*rawPage.Records[0].BodyBase64)
		if err != nil || !bytes.Equal(body, bodies[i]) || rawPage.Records[0].BodySHA256 != page.Records[0].BodySHA256 {
			t.Fatal("original bytes changed", err)
		}
		after = page.NextReceipt
		encoded, _ := json.Marshal(page)
		if !strings.Contains(string(encoded), "+08:00") {
			t.Fatal("time is not +08:00")
		}
	}
	page, err := s.HeartbeatEvidence(ctx, c, cp, after, 1, true)
	if err != nil || len(page.Records) != 0 || page.More || page.NextReceipt != after {
		t.Fatal(page, err)
	}
	current, _ := s.HeartbeatCheckpoint(ctx, c)
	if current != cp || storedCount(t, db, "futures_heartbeat_windows") != 0 || storedCount(t, db, "futures_query_batches") != 0 {
		t.Fatal("audit altered state")
	}
}

func TestHeartbeatEvidenceRequiresSameAnchorAndRoute(t *testing.T) {
	_, s, c, h, cp, ctx := evidenceFixture(t, []byte("one"))
	page, err := s.HeartbeatEvidence(ctx, c, cp, 0, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	captureStream(t, s, ctx, h, cp.Cursor, StreamInput{"2-0", []byte("two")})
	if out, err := s.HeartbeatEvidence(ctx, c, cp, page.NextReceipt, 1, false); !errors.Is(err, ErrStreamCursor) || out.Records != nil {
		t.Fatal("mixed checkpoints", err)
	}
	cp, _ = s.HeartbeatCheckpoint(ctx, c)
	if _, err := s.OpenHeartbeatCapture(ctx, c, cp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HeartbeatEvidence(ctx, c, cp, 0, 1, false); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("old owner accepted", err)
	}
	cp, _ = s.HeartbeatCheckpoint(ctx, c)
	other := c
	other.SourceID += "-other"
	if _, err := s.HeartbeatEvidence(ctx, other, cp, 0, 1, false); !errors.Is(err, ErrStreamIdentity) {
		t.Fatal("foreign source accepted", err)
	}
	for _, pair := range [][2]int64{{-1, 1}, {0, 0}, {0, 257}, {9999999, 1}} {
		if _, err := s.HeartbeatEvidence(ctx, c, cp, pair[0], int(pair[1]), false); err == nil {
			t.Fatal("unsafe bounds accepted")
		}
	}
	other = c
	other.Scope.Route.GatewayID += "-other"
	oh, err := s.OpenHeartbeatCapture(ctx, other, HeartbeatCheckpoint{Cursor: "0-0"})
	if err != nil {
		t.Fatal(err)
	}
	r := captureStream(t, s, ctx, oh, "0-0", StreamInput{"1-0", []byte("foreign")})
	if _, err := s.HeartbeatEvidence(ctx, c, cp, r.ReceiptIDs[0], 1, false); !errors.Is(err, ErrStreamCursor) {
		t.Fatal("foreign receipt skipped history", err)
	}
}

func TestHeartbeatEvidenceCorruptionFailsWholePage(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE futures_stream_inbox SET body='bad'::bytea WHERE entry_id='2-0'`,
		`UPDATE futures_stream_inbox SET body_sha256='bad'::bytea WHERE entry_id='2-0'`,
		`UPDATE futures_stream_receivers SET audit_count=audit_count+1`,
		`UPDATE futures_stream_receivers SET cursor_id='3-0'`,
	} {
		t.Run(mutation, func(t *testing.T) {
			db, s, c, _, cp, ctx := evidenceFixture(t, []byte("one"), []byte("two"))
			// Use a length-preserving bad digest to respect the SQL column contract.
			mutation = strings.ReplaceAll(mutation, "body_sha256='bad'::bytea", "body_sha256=decode(repeat('00',32),'hex')")
			if _, err := db.ExecContext(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			if page, err := s.HeartbeatEvidence(ctx, c, cp, 0, 2, true); err == nil || page.Records != nil || page.NextReceipt != 0 {
				t.Fatal("partial corrupt evidence returned", page, err)
			}
		})
	}
}

func TestHeartbeatEvidenceEmptyAndStage1(t *testing.T) {
	_, s, c, _, cp, ctx := evidenceFixture(t)
	page, err := s.HeartbeatEvidence(ctx, c, cp, 0, 1, false)
	if err != nil || page.Records == nil || len(page.Records) != 0 || page.More {
		t.Fatal(page, err)
	}
	output := runtimeHeartbeats(t)[0]
	route := runtimeRoute(t, output)
	c.Scope.Route = route
	h, err := s.OpenHeartbeatCapture(ctx, c, HeartbeatCheckpoint{Cursor: "0-0"})
	if err != nil {
		t.Fatal(err)
	}
	captureStream(t, s, ctx, h, "0-0", StreamInput{"1-0", []byte(output.Body)})
	cp, _ = s.HeartbeatCheckpoint(ctx, c)
	page, err = s.HeartbeatEvidence(ctx, c, cp, 0, 1, false)
	if err != nil || page.Records[0].Classification != "transport_recovery_only" || page.BusinessAuthorized {
		t.Fatal(page, err)
	}
}

func TestHeartbeatEvidenceByteBudgetAndEntryOrder(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		body := bytes.Repeat([]byte("x"), 5*1024*1024)
		_, s, c, _, cp, ctx := evidenceFixture(t, body, body)
		if page, err := s.HeartbeatEvidence(ctx, c, cp, 0, 2, true); !errors.Is(err, ErrAuditCapacity) || page.Records != nil {
			t.Fatal("unbounded page", err)
		}
		if page, err := s.HeartbeatEvidence(ctx, c, cp, 0, 1, false); err != nil || !page.More || len(page.Records) != 1 {
			t.Fatal("bounded retry failed", err)
		}
	})
	t.Run("order", func(t *testing.T) {
		db, s, c, _, cp, ctx := evidenceFixture(t, []byte("one"), []byte("two"), []byte("three"))
		if _, err := db.ExecContext(ctx, `UPDATE futures_stream_inbox SET entry_id='2-1' WHERE entry_id='1-0'`); err != nil {
			t.Fatal(err)
		}
		if page, err := s.HeartbeatEvidence(ctx, c, cp, 0, 3, false); !errors.Is(err, ErrStreamCursor) || page.Records != nil {
			t.Fatal("entry order corruption accepted", err)
		}
	})
}
