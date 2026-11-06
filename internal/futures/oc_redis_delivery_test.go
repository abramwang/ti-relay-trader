package futures

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
)

var ocRedisFiles = []struct{ name, digest string }{
	{"shared_test_redis_recovery.final.v1.json", "62353b228392d3184a47783e0d571ec2afba4d37d17c50120c4328578d4986ff"},
	{"private_redis_recovery.final.v1.json", "c00119c6d802aba4f603b4c79c8cfca958dcf07bccf3c93b1e9939d5471c0acc"},
	{"asan_private_redis_recovery.v1.json", "8bfd9b59cdcf5ffb86bb7e7248ab23c81971e0aa3c638316183b6fd996b24aed"},
}

type ocRedisOutput struct {
	Body     string
	SHA256   string
	StreamID string `json:"stream_id"`
}

type ocRedisQuery struct {
	Name    string
	Command string          `json:"raw_command"`
	Outputs []ocRedisOutput `json:"outputs_after"`
}

func ocRedisPages(t *testing.T, index int) (QueryContext, ocRedisQuery) {
	t.Helper()
	f := ocRedisFiles[index]
	raw, err := os.ReadFile("../../testdata/redis_review/" + f.name)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != f.digest {
		t.Fatal("OC evidence changed")
	}
	var doc struct {
		BrokerID string         `json:"broker_id"`
		Cases    []ocRedisQuery `json:"extra_cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Cases) != 2 {
		t.Fatal(err)
	}
	q := doc.Cases[0]
	if q.Name != "query_second_publication_confirmation_lost" || len(q.Outputs) != 4 {
		t.Fatal("missing original query pages")
	}
	// Fixed synthetic scenario preconditions, not authenticated live session evidence.
	c := ocReplyContext(t, false, "positions")
	c.Route.BrokerID, c.Route.AccountID = doc.BrokerID, "990000000036"
	for _, output := range q.Outputs {
		hash := sha256.Sum256([]byte(output.Body))
		if hex.EncodeToString(hash[:]) != output.SHA256 {
			t.Fatal("body checksum mismatch")
		}
		if _, err := streamID(output.StreamID); err != nil {
			t.Fatal(err)
		}
	}
	if q.Outputs[1].Body != q.Outputs[2].Body || q.Outputs[1].StreamID == q.Outputs[2].StreamID {
		t.Fatal("expected identical page at distinct stream IDs")
	}
	return c, q
}

func TestOCRedisReceivedQueryPages(t *testing.T) {
	for index, file := range ocRedisFiles {
		t.Run(file.name, func(t *testing.T) {
			c, q := ocRedisPages(t, index)
			for _, sequence := range [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}} {
				batch, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits())
				if err != nil {
					t.Fatal(err)
				}
				for i, n := range sequence {
					replayed, err := batch.Accept([]byte(q.Outputs[n].Body), time.Duration(i))
					if err != nil || replayed != (i == 2) {
						t.Fatal(replayed, err)
					}
					if i < 3 {
						assertState(t, batch, BatchCollecting)
					}
				}
				assertState(t, batch, BatchReady)
				candidate, err := batch.Candidate()
				if err != nil || len(candidate.Positions) != 3 || len(batch.AcceptedMessages()) != 3 {
					t.Fatal(candidate, err)
				}
				for i, p := range candidate.Positions {
					if p.Symbol != []string{"rb0", "rb1", "rb2"}[i] {
						t.Fatal("page order or deduplication failed")
					}
				}
			}
		})
	}
}
