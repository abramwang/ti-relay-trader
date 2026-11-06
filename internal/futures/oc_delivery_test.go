package futures

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

const ocReviewRoot = "../../testdata/oc-trade-review-20261106/"

type ocReplyCase struct {
	Name       string
	Successful bool
	Bodies     []string        `json:"raw_bodies"`
	Command    json.RawMessage `json:"command"`
}

func ocReplyCases(t *testing.T, trade bool) []ocReplyCase {
	t.Helper()
	path, fingerprint := "command_review/query_reply_examples.v1.json", "b60073574d767acad27866d7727ea1f28c7ed77902984757d129c7a5ec7a640b"
	if trade {
		path, fingerprint = "trade_review/order_fill_query_examples.v1.json", "2357304ca1be22e8132c02c6b6815f572596c09c1b17afc37e98adb54faa4fa4"
	}
	raw, err := os.ReadFile(ocReviewRoot + path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != fingerprint {
		t.Fatal("OC delivery bytes changed")
	}
	var doc struct {
		Synthetic bool `json:"synthetic_only"`
		Cases     []ocReplyCase
	}
	if err := json.Unmarshal(raw, &doc); err != nil || !doc.Synthetic || len(doc.Cases) != 12 {
		t.Fatal("invalid synthetic delivery", err)
	}
	return doc.Cases
}

// Fixed offline scenario preconditions, deliberately not learned from a reply.
// They are NOT evidence of a live OC ticket or authenticated broker session.
func ocReplyContext(t *testing.T, trade bool, name string) QueryContext {
	t.Helper()
	generation, request := int64(1), int64(10)
	c := QueryContext{Route: QueryRoute{Env: "test", BrokerID: "ctp_fixture", AccountID: "990000000001", GatewayID: "fixture-gw"},
		Action: "account.positions.query", OriginMessageID: "fixture-submit", Ticket: "110",
		CounterSessionID: "ctpproc-fixture", ConnectionGeneration: &generation, SDKRequestID: &request,
		TradeDate: "20261105", NativeBrokerID: "9999", NativeInvestorID: "FIXTURE_INV", NativeUserID: "FIXTURE_USER",
		Command: &QueryCommandBinding{1, "fixture-request", "fixture-correlation", "fixture-idem"}}
	if trade {
		names := []string{"fill_first_pending", "empty_query_cannot_hide_pending", "order_after_restart", "fill_after_restart", "duplicate_fill_rows_rejected", "unknown_order_state_retained", "cancelled_remainder", "same_fill_conflict", "unknown_is_not_external", "verified_external_order", "verified_external_fill", "missing_id_raw_evidence_retained"}
		requests := []int64{10, 11, 12, 13, 14, 15, 16, 19, 20, 21, 22, 23}
		found := false
		for i, n := range names {
			if n != name {
				continue
			}
			found = true
			request = requests[i]
			c.Ticket = fmt.Sprint(100 + request)
			c.OriginMessageID = "query-" + c.Ticket
			c.Command.RequestID = "request-" + c.Ticket
			if i >= 2 {
				generation, c.CounterSessionID = 2, "ctpproc-restart-fixture"
			}
		}
		if !found {
			t.Fatal("unknown OC scenario", name)
		}
		c.Action = "fill.list.query"
		if oneOf(name, "order_after_restart", "unknown_order_state_retained", "cancelled_remainder", "unknown_is_not_external", "verified_external_order") {
			c.Action = "order.list.query"
		}
	} else {
		switch name {
		case "asset_scopes_two_pages", "empty_asset_failure":
			c.Action = "account.asset.query"
		case "order.list.query", "fill.list.query":
			c.Action = name
		case "before_enqueue":
			c.Ticket, c.TradeDate, c.ConnectionGeneration, c.SDKRequestID = "", "", nil, nil
		case "queue_expired":
			c.SDKRequestID = nil
		}
	}
	return c
}

func ocAssemble(t *testing.T, trade bool, example ocReplyCase, reverse bool) *QueryBatch {
	t.Helper()
	b, err := NewQueryBatch(ocReplyContext(t, trade, example.Name), 0, time.Minute, DefaultBatchLimits())
	if err != nil {
		t.Fatal(err)
	}
	for i := range example.Bodies {
		index := i
		if reverse {
			index = len(example.Bodies) - 1 - i
		}
		if _, err := b.Accept([]byte(example.Bodies[index]), time.Duration(i)); err != nil {
			t.Fatal(err)
		}
	}
	want := BatchFailed
	if example.Successful {
		want = BatchReady
	}
	assertState(t, b, want)
	return b
}

func TestOCReceivedReplyBodies(t *testing.T) {
	count := 0
	for _, trade := range []bool{false, true} {
		for _, example := range ocReplyCases(t, trade) {
			count += len(example.Bodies)
			t.Run(example.Name, func(t *testing.T) {
				for _, reverse := range []bool{false, true} {
					b := ocAssemble(t, trade, example, reverse)
					for _, raw := range example.Bodies {
						if replay, err := b.Accept([]byte(raw), time.Hour); !replay || err != nil {
							t.Fatal("original replay", replay, err)
						}
					}
					if len(b.AcceptedMessages()) != len(example.Bodies) {
						t.Fatal("lost/duplicated original envelopes")
					}
					for i, raw := range b.AcceptedMessages() {
						index := i
						if reverse {
							index = len(example.Bodies) - 1 - i
						}
						if !bytes.Equal(raw, []byte(example.Bodies[index])) {
							t.Fatal("rewritten audit")
						}
					}
				}
			})
		}
	}
	if count != 27 {
		t.Fatal("unexpected reply count", count)
	}
}

func TestOCReceivedTradeSemantics(t *testing.T) {
	candidates := map[string]SnapshotCandidate{}
	for _, example := range ocReplyCases(t, true) {
		b := ocAssemble(t, true, example, false)
		if example.Successful {
			candidate, err := b.Candidate()
			if err != nil {
				t.Fatal(err)
			}
			candidates[example.Name] = candidate
		}
	}
	o := candidates["order_after_restart"].Orders[0]
	f := candidates["fill_after_restart"].Fills[0]
	c := candidates["cancelled_remainder"].Orders[0]
	if o.GatewayOrderID != "fixture-order" || f.GatewayOrderID != o.GatewayOrderID || o.NativeIDs.OrderSysID != "00001234" || f.NativeIDs.TradeID != "000005678" || o.NativeIDs.SDKRequestID == nil || *o.NativeIDs.SDKRequestID != 11 {
		t.Fatal("native identity/query request conflated")
	}
	if o.SubmittedAt == nil || *o.SubmittedAt != "2026-11-04T21:01:00+08:00" || f.MatchedAt == nil || *f.MatchedAt != "2026-11-04T21:01:02+08:00" || o.TradeDate != "20261105" || f.Fee != nil || f.FeeComplete {
		t.Fatal("source time/fee altered")
	}
	if c.GatewayStatus != "cancelled" || c.OrderQty != 10 || c.CumFilledQty != 4 || c.LeavesQty != 6 {
		t.Fatal("cancelled remaining quantity lost")
	}
	eo, ef := candidates["verified_external_order"].Orders[0], candidates["verified_external_fill"].Fills[0]
	if eo.GatewayOrderID != ef.GatewayOrderID || eo.GatewayOrderID == o.GatewayOrderID || ef.FillID != f.FillID {
		t.Fatal("external/native execution identity changed")
	}
	// Local and verified-external fixtures describe separate synthetic stores;
	// never merge them into one account to fabricate an external reclassification.
	for _, external := range []bool{false, true} {
		order, fill := o, f
		if external {
			order, fill = eo, ef
		}
		p := newTradeProjection(candidates["order_after_restart"].Context.tradeScope())
		p.fill("fill", fill)
		p.reconcile()
		if p.report.PendingCount != 1 {
			t.Fatal("fill first fabricated an order")
		}
		p.order(order)
		p.reconcile()
		if p.report.Blocked || p.report.Executions[fill.FillID].LinkedGateway != order.GatewayOrderID {
			t.Fatal("OC rows failed association")
		}
	}
}
