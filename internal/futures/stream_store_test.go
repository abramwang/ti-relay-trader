package futures

import (
	"bytes"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestStreamIDNumericAndCanonical(t *testing.T) {
	for _, id := range []string{"", "0", "-1-0", "+1-0", "01-0", "1-00", "1-1-1", "18446744073709551616-0", "1-18446744073709551616"} {
		if _, err := streamID(id); err == nil {
			t.Fatal("invalid ID accepted", id)
		}
	}
	for _, pair := range [][2]string{{"9-99", "10-0"}, {"10-9", "10-10"}, {"0-0", "0-1"}, {"18446744073709551615-0", "18446744073709551615-18446744073709551615"}} {
		if n, err := compareStreamID(pair[0], pair[1]); err != nil || n != -1 {
			t.Fatal(pair, n, err)
		}
		if n, err := compareStreamID(pair[1], pair[0]); err != nil || n != 1 {
			t.Fatal(pair, n, err)
		}
	}
}

func TestStreamReadEnvelopeIsLosslessAndBounded(t *testing.T) {
	raw := []byte{0xff, 0, '{'}
	streams := []redis.XStream{{Stream: "configured", Messages: []redis.XMessage{{ID: "10-0", Values: map[string]any{"body": raw}}, {ID: "10-1", Values: map[string]any{"body": "not JSON"}}}}}
	out, err := decodeStreamRead("configured", "9-99", 2, streams)
	if err != nil || len(out) != 2 || !bytes.Equal(out[0].Raw, raw) {
		t.Fatal(out, err)
	}
	raw[0] = 'x'
	if out[0].Raw[0] != 0xff {
		t.Fatal("shared mutable body")
	}
	for _, bad := range []struct {
		key, cursor string
		limit       int
		rows        []redis.XStream
	}{
		{"wrong", "0-0", 2, streams}, {"configured", "10-0", 2, streams}, {"configured", "0-0", 1, streams},
		{"configured", "0-0", 2, append(streams, streams...)},
		{"configured", "0-0", 2, []redis.XStream{{Stream: "configured", Messages: []redis.XMessage{{ID: "1-0", Values: map[string]any{"other": "data"}}}}}},
		{"configured", "0-0", 2, []redis.XStream{{Stream: "configured", Messages: []redis.XMessage{{ID: "1-0", Values: map[string]any{"body": 42}}}}}},
		{"configured", "0-0", 2, []redis.XStream{{Stream: "configured", Messages: []redis.XMessage{{ID: "1-0", Values: map[string]any{"body": "", "extra": "data"}}}}}},
		{"configured", "0-0", 2, []redis.XStream{{Stream: "configured", Messages: []redis.XMessage{{ID: "1-0", Values: map[string]any{"body": strings.Repeat("x", maxSnapshotBytes+1)}}}}}},
	} {
		if _, err := decodeStreamRead(bad.key, bad.cursor, bad.limit, bad.rows); err == nil {
			t.Fatal("invalid read accepted")
		}
	}
}

func TestStreamHistoryLossFailsClosed(t *testing.T) {
	base := redis.XInfoStream{Length: 2, EntriesAdded: 2, FirstEntry: redis.XMessage{ID: "9-0"}, LastGeneratedID: "10-0", MaxDeletedEntryID: "0-0"}
	if err := checkStreamHistory(&base, "0-0"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*redis.XInfoStream){
		func(i *redis.XInfoStream) { i.LastGeneratedID = "8-0" },
		func(i *redis.XInfoStream) { i.MaxDeletedEntryID = "10-0" },
		func(i *redis.XInfoStream) { i.MaxDeletedEntryID = "" },
		func(i *redis.XInfoStream) { i.FirstEntry.ID = "10-0"; i.EntriesAdded = 3 },
		func(i *redis.XInfoStream) { i.Length = 0 },
		func(i *redis.XInfoStream) { i.EntriesAdded = 1 },
	} {
		info := base
		change(&info)
		if err := checkStreamHistory(&info, "9-0"); err == nil {
			t.Fatal("loss not blocked", info)
		}
	}
	base.EntriesAdded = 5
	if err := checkStreamHistory(&base, "9-0"); err != nil {
		t.Fatal("old trimmed history at/before cursor rejected", err)
	}
}
