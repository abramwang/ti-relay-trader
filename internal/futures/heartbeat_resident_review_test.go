package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const residentHeartbeatReviewDir = "../../testdata/oc_resident_heartbeat_review_20261118"

func TestResidentHeartbeatReviewOriginalBodies(t *testing.T) {
	for _, manifest := range []string{"SHA256SUMS", "BODY_SHA256SUMS"} {
		raw, err := os.ReadFile(filepath.Join(residentHeartbeatReviewDir, manifest))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 3 {
			t.Fatal("unexpected inventory", manifest)
		}
		for _, line := range lines {
			f := strings.Fields(line)
			if len(f) != 2 || filepath.Base(f[1]) != f[1] {
				t.Fatal("invalid manifest")
			}
			body, err := os.ReadFile(filepath.Join(residentHeartbeatReviewDir, f[1]))
			if err != nil || commandDigest(body) != f[0] {
				t.Fatal("evidence changed", f[1], err)
			}
		}
	}
	var doc struct {
		Synthetic bool `json:"synthetic_only"`
		RealSDK   bool `json:"real_sdk_calls"`
		Cases     []struct {
			Scenario string `json:"scenario"`
			Body     string `json:"body"`
			Bytes    int    `json:"body_bytes"`
			SHA256   string `json:"body_sha256"`
			Double   bool   `json:"sdk_is_test_double"`
			Submits  int    `json:"sdk_submit_calls"`
			Cancels  int    `json:"sdk_cancel_calls"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join(residentHeartbeatReviewDir, "heartbeat_bodies.json"))
	if err != nil || json.Unmarshal(raw, &doc) != nil || !doc.Synthetic || doc.RealSDK || len(doc.Cases) != 3 {
		t.Fatal("invalid review envelope", err)
	}
	seen := map[string]bool{}
	for _, c := range doc.Cases {
		if !oneOf(c.Scenario, "normal_resident", "journal_exhausted", "runtime_latched") || seen[c.Scenario] {
			t.Fatal("unexpected/duplicate scenario")
		}
		seen[c.Scenario] = true
		body, err := os.ReadFile(filepath.Join(residentHeartbeatReviewDir, c.Scenario+".body.json"))
		if err != nil || !bytes.Equal(body, []byte(c.Body)) || len(body) != c.Bytes || commandDigest(body) != c.SHA256 || !c.Double || c.Submits != 0 || c.Cancels != 0 {
			t.Fatal("body provenance differs", c.Scenario, err)
		}
		t.Run(c.Scenario, func(t *testing.T) {
			h, err := DecodeHeartbeat(body, responsibilityRoute())
			if err != nil {
				t.Fatal("OC original rejected", err)
			}
			if !bytes.Equal(h.Raw, body) || h.Assessment == nil || !h.Assessment.relayExecution() || h.Assessment.ResidentDurationSeconds != 0 || h.Assessment.Lifecycle != nil || h.Assessment.ResourceUsage == nil {
				t.Fatal("changed original or invented lifecycle")
			}
			normal := c.Scenario == "normal_resident"
			_, query := h.queryCollection()
			if query != normal || h.AcceptingTradeCommands != normal || h.AcceptingCancelCommands != normal {
				t.Fatal("incorrect capabilities")
			}
			switch c.Scenario {
			case "normal_resident":
				if h.State != "UP" || h.Assessment.ResourceUsage.Journal.Exhausted || h.Assessment.ResourceUsage.QueryCommandsReceived != 2 {
					t.Fatal("normal evidence")
				}
			case "journal_exhausted":
				u := h.Assessment.ResourceUsage.Journal
				if !u.Exhausted || !u.Warning || u.Metrics["callbacks"].Used != 10000 || u.Metrics["callbacks"].Remaining != 0 || h.Operations.QueryReason != "journal_capacity_exhausted" {
					t.Fatal("capacity evidence")
				}
			case "runtime_latched":
				if h.StateText != "recovery_blocked" || h.Assessment.TransportRecoveryClear || h.Assessment.RuntimeBlockedReason != "assessment_unexpected_pending_or_wrong_delivery" {
					t.Fatal("latched evidence")
				}
			}
			// Evaluate only at the fixture's synthetic timestamp; these historical
			// bodies are never current session evidence and never go to Redis.
			v, _, target, approval, _ := executionTradeFixture(t, h.ProducedAt)
			target.CounterSessionID, target.TradeDate = h.CounterSessionID, h.TradeDate
			row := StreamInput{"1-0", body}
			_, err = testQueryHeartbeatProof(row, v.scope(), target, h.ProducedAt)
			if (err == nil) != normal {
				t.Fatal("query gate differs", err)
			}
			_, err = testTradeHeartbeatProof(row, v.scope().Route, target, approval, h.ProducedAt)
			if (err == nil) != normal {
				t.Fatal("explicit local permission gate differs", err)
			}
			if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, TestTradeApproval{}, h.ProducedAt); err == nil {
				t.Fatal("capability authorized trade")
			}
			if _, err := testQueryHeartbeatProof(row, v.scope(), target, h.ProducedAt.Add(5*time.Second)); err == nil {
				t.Fatal("stale resident heartbeat")
			}
			if _, err := testTradeHeartbeatProof(row, v.scope().Route, target, approval, h.ProducedAt.Add(5*time.Second)); err == nil {
				t.Fatal("stale execution heartbeat")
			}
		})
	}
}
