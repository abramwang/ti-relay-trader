package futures

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Historical live OC evidence is never published back into Redis by this test.
func TestOCJointLiveHeartbeatEvidence(t *testing.T) {
	const dir = "../../testdata/joint_heartbeat_20261116/"
	manifest, err := os.ReadFile(dir + "SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(manifest)), "\n")
	if len(lines) != 7 {
		t.Fatal("incomplete manifest")
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.ContainsAny(fields[1], "/\\") {
			t.Fatal("invalid manifest entry")
		}
		body, err := os.ReadFile(dir + fields[1])
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != fields[0] {
			t.Fatal("artifact changed", fields[1])
		}
	}
	var evidence struct {
		Records []struct {
			Stream, Body string
			ID           string `json:"entry_id"`
			Hash         string `json:"body_sha256"`
			Size         int    `json:"body_bytes"`
		}
	}
	raw, err := os.ReadFile(dir + "heartbeat_evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &evidence); err != nil || len(evidence.Records) != 17 {
		t.Fatal("missing heartbeats", err)
	}
	route := QueryRoute{"test", "ctp_joint_test", "ctp-transport-20261116", "990000000116"}
	start, _ := time.Parse(time.RFC3339Nano, "2026-11-16T21:28:56.037+08:00")
	inside, size := 0, 0
	for i, r := range evidence.Records {
		digest := sha256.Sum256([]byte(r.Body))
		h, err := DecodeHeartbeat([]byte(r.Body), route)
		if err != nil || h.Recovery == nil || h.ReadOnly != nil || h.BrokerReady || h.AcceptingTradeCommands || h.AcceptingCancelCommands || h.TradeDate != "" || h.CounterSessionID != "ctpproc-cd57a6a1810d671fbffaa838936f100e" {
			t.Fatal("historical transport evidence promoted or rejected", i, err)
		}
		if r.Hash != hex.EncodeToString(digest[:]) || r.Size != len(r.Body) || r.Stream != strings.TrimSuffix(queryReplyStream(route), "reply")+"hb" {
			t.Fatal("body/physical source mismatch", i)
		}
		if i > 0 {
			cmp, err := compareStreamID(evidence.Records[i-1].ID, r.ID)
			if err != nil || cmp >= 0 {
				t.Fatal("entry ordering", err)
			}
		}
		if !h.ProducedAt.Before(start) && !h.ProducedAt.After(start.Add(15*time.Second)) {
			inside++
		}
		if i == 16 && (r.ID != "1794835752035-0" || h.StateText != "stopping") {
			t.Fatal("final stopping evidence lost")
		}
		size += len(r.Body)
	}
	if inside != 15 || size != 18745 || evidence.Records[0].ID != "1794835736035-0" {
		t.Fatal("window or raw byte count", inside, size)
	}
	f, err := os.Open(dir + "runtime.audit.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	matched := 0
	for s.Scan() {
		var row struct {
			Entry struct {
				Kind, Body, Stream string
				ID                 string `json:"stream_id"`
			}
		}
		if err := json.Unmarshal(s.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		if row.Entry.Kind != "heartbeat_published" {
			continue
		}
		if matched >= len(evidence.Records) {
			t.Fatal("extra local audit heartbeat")
		}
		r := evidence.Records[matched]
		if row.Entry.Body != r.Body || row.Entry.ID != r.ID || row.Entry.Stream != r.Stream {
			t.Fatal("OC audit differs from retained evidence", matched)
		}
		matched++
	}
	if err := s.Err(); err != nil || matched != 17 {
		t.Fatal("incomplete local audit", err)
	}
}
