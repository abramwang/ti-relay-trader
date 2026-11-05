package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func fixtureBody(t testing.TB, file, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/ctp/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name string          `json:"name"`
			Body json.RawMessage `json:"body"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Cases {
		if c.Name == name {
			return c.Body
		}
	}
	t.Fatal("missing fixture " + name)
	return nil
}

func editBody(t testing.TB, raw []byte, edit func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	result, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func evidence(m map[string]any) map[string]any {
	return m["adapter_context"].(map[string]any)["query_evidence"].(map[string]any)
}

func batchContext(name string) QueryContext {
	generation, request := int64(1), int64(12)
	c := QueryContext{Route: QueryRoute{"test", "ctp_4900", "990000000001", "990000000001"},
		Action: "account.positions.query", OriginMessageID: "fixture-query-position", Ticket: "fixture-ticket-2",
		CounterSessionID: "ctpproc-fixture-1", ConnectionGeneration: &generation, SDKRequestID: &request,
		TradeDate: "20261104", NativeBrokerID: "4900"}
	if name == "asset" {
		c.Action, c.OriginMessageID, c.Ticket, request = "account.asset.query", "fixture-query-asset", "fixture-ticket-1", 11
	}
	return c
}

func newBatch(t testing.TB, name string) *QueryBatch {
	t.Helper()
	b, err := NewQueryBatch(batchContext(name), 0, time.Minute, DefaultBatchLimits())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertState(t testing.TB, b *QueryBatch, want BatchState) {
	t.Helper()
	if state, reason := b.State(); state != want {
		t.Fatalf("state=%s want=%s reason=%s", state, want, reason)
	}
	if want != BatchReady {
		if _, err := b.Candidate(); err == nil {
			t.Fatal("non-ready batch published candidate")
		}
	}
}

func page(t testing.TB, name, id string, index int, last bool, symbol string) []byte {
	return editBody(t, fixtureBody(t, "messages.json", name), func(m map[string]any) {
		m["message_id"] = id
		m["status"] = "partial"
		if last {
			m["status"] = "completed"
		}
		m["chunk"] = map[string]any{"index": index, "is_last": last}
		p := m["payload"].(map[string]any)
		row := p["items"].([]any)[0].(map[string]any)
		if name == "positions" {
			row["symbol"] = symbol
		} else {
			row["futures"].(map[string]any)["scope"].(map[string]any)["account_id"] = symbol
		}
	})
}

func TestBatchSharedFixturesAndRawReplay(t *testing.T) {
	for _, name := range []string{"asset", "positions"} {
		b := newBatch(t, name)
		raw := fixtureBody(t, "messages.json", name)
		if replay, err := b.Accept(raw, time.Second); replay || err != nil {
			t.Fatalf("first delivery replay=%v err=%v", replay, err)
		}
		assertState(t, b, BatchReady)
		if replay, err := b.Accept(raw, time.Hour); !replay || err != nil {
			t.Fatalf("complete exact replay rejected: %v", err)
		}
		if len(b.AcceptedMessages()) != 1 || !bytes.Equal(b.AcceptedMessages()[0], raw) {
			t.Fatal("raw bytes not preserved or replay counted")
		}
	}
}

func TestBatchOutOfOrderAndCrossPageIdentity(t *testing.T) {
	for _, name := range []string{"asset", "positions"} {
		for _, duplicate := range []bool{false, true} {
			b := newBatch(t, name)
			lastSymbol := "OTHER"
			if duplicate {
				lastSymbol = "FIRST"
			}
			last := page(t, name, "last", 1, true, lastSymbol)
			if _, err := b.Accept(last, 0); err != nil {
				t.Fatal(err)
			}
			assertState(t, b, BatchCollecting)
			if replay, err := b.Accept(last, 0); !replay || err != nil {
				t.Fatal("partial collection replay failed")
			}
			_, err := b.Accept(page(t, name, "first", 0, false, "FIRST"), 0)
			if duplicate {
				if err == nil {
					t.Fatal("cross-page duplicate accepted")
				}
				assertState(t, b, BatchInvalid)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			c, err := b.Candidate()
			if err != nil || len(c.Assets)+len(c.Positions) != 2 {
				t.Fatal("complete rows not published", err)
			}
			if name == "positions" && c.Positions[0].Symbol != "FIRST" {
				t.Fatal("arrival order used instead of page order")
			}
		}
	}
}

func TestBatchPageGapsDeadlineAndNoRevival(t *testing.T) {
	b := newBatch(t, "positions")
	last := page(t, "positions", "last", 2, true, "THIRD")
	b.Accept(last, 0)
	b.Accept(page(t, "positions", "first", 0, false, "FIRST"), 0)
	assertState(t, b, BatchCollecting)
	if err := b.Advance(time.Minute); err == nil {
		t.Fatal("deadline boundary not expired")
	}
	assertState(t, b, BatchExpired)
	if _, err := b.Accept(page(t, "positions", "middle", 1, false, "SECOND"), time.Minute); err == nil {
		t.Fatal("expired query revived")
	}
	assertState(t, b, BatchExpired)
}

func TestBatchAllThreePageArrivalOrders(t *testing.T) {
	pages := [][]byte{
		page(t, "positions", "first", 0, false, "FIRST"),
		page(t, "positions", "second", 1, false, "SECOND"),
		page(t, "positions", "third", 2, true, "THIRD"),
	}
	for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		b := newBatch(t, "positions")
		for n, index := range order {
			if _, err := b.Accept(pages[index], time.Duration(n)); err != nil {
				t.Fatal(order, err)
			}
			if n < 2 {
				assertState(t, b, BatchCollecting)
			}
		}
		c, err := b.Candidate()
		if err != nil || len(c.Positions) != 3 || c.Positions[0].Symbol != "FIRST" || c.Positions[1].Symbol != "SECOND" || c.Positions[2].Symbol != "THIRD" {
			t.Fatal("permuted pages lost order or completeness", order, err)
		}
	}
}

func TestBatchConflictsInvalidateCandidate(t *testing.T) {
	base := page(t, "positions", "first", 0, false, "FIRST")
	final := page(t, "positions", "last", 1, true, "SECOND")
	cases := []struct{ first, second []byte }{
		{base, append(bytes.Clone(base), ' ')},
		{base, page(t, "positions", "other-id", 0, false, "FIRST")},
		{final, page(t, "positions", "second-terminal", 2, true, "THIRD")},
		{final, page(t, "positions", "beyond", 2, false, "THIRD")},
		{page(t, "positions", "beyond", 2, false, "THIRD"), final},
		{page(t, "positions", "ready", 0, true, "FIRST"), base},
	}
	for _, tc := range cases {
		b := newBatch(t, "positions")
		if _, err := b.Accept(tc.first, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Accept(tc.second, 0); err == nil {
			t.Fatal("conflict accepted")
		}
		assertState(t, b, BatchInvalid)
	}
}

func TestBatchForeignQueriesDoNotPoison(t *testing.T) {
	for _, key := range []string{"env", "broker_id", "gateway_id", "account_id", "origin_message_id"} {
		b := newBatch(t, "positions")
		raw := fixtureBody(t, "messages.json", "positions")
		foreign := editBody(t, raw, func(m map[string]any) {
			if key == "origin_message_id" {
				m[key] = "other-command"
			} else {
				m["routing"].(map[string]any)[key] = "other-route"
			}
		})
		if _, err := b.Accept(foreign, 0); !errors.Is(err, ErrForeignQuery) {
			t.Fatal("foreign query not identified", err)
		}
		assertState(t, b, BatchCollecting)
		if len(b.AcceptedMessages()) != 0 {
			t.Fatal("foreign body retained as own evidence")
		}
		if _, err := b.Accept(raw, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBatchStrictEnvelopeAndEvidence(t *testing.T) {
	base := fixtureBody(t, "messages.json", "positions")
	changes := map[string]func(map[string]any){
		"missing origin":      func(m map[string]any) { delete(m, "origin_message_id") },
		"null origin":         func(m map[string]any) { m["origin_message_id"] = nil },
		"unknown":             func(m map[string]any) { m["extra"] = true },
		"case":                func(m map[string]any) { m["Status"] = m["status"]; delete(m, "status") },
		"null id":             func(m map[string]any) { m["message_id"] = nil },
		"invalid optional id": func(m map[string]any) { m["request_id"] = " has space" },
		"message null":        func(m map[string]any) { m["message"] = nil },
		"protocol":            func(m map[string]any) { m["protocol"] = "other" },
		"event":               func(m map[string]any) { m["message_type"] = "event" },
		"action":              func(m map[string]any) { m["action"] = "account.asset.query" },
		"result type":         func(m map[string]any) { m["result_type"] = "asset_page" },
		"time":                func(m map[string]any) { m["produced_at"] = "2026-11-03T13:01:00Z" },
		"code":                func(m map[string]any) { m["code"] = "ERROR" },
		"status":              func(m map[string]any) { m["status"] = "accepted" },
		"ticket":              func(m map[string]any) { evidence(m)["ticket"] = "old-ticket" },
		"session":             func(m map[string]any) { evidence(m)["counter_session_id"] = "old-session" },
		"generation":          func(m map[string]any) { evidence(m)["connection_generation"] = 2 },
		"request":             func(m map[string]any) { evidence(m)["sdk_request_id"] = 13 },
		"sync code":           func(m map[string]any) { evidence(m)["sdk_return_code"] = -2 },
		"callback":            func(m map[string]any) { evidence(m)["callback_error_id"] = 7 },
		"applied":             func(m map[string]any) { evidence(m)["snapshot_applied"] = false },
		"missing evidence":    func(m map[string]any) { delete(evidence(m), "callback_error_id") },
		"extra evidence":      func(m map[string]any) { evidence(m)["other"] = 1 },
		"bool integer":        func(m map[string]any) { evidence(m)["sdk_return_code"] = false },
		"float integer":       func(m map[string]any) { evidence(m)["connection_generation"] = json.RawMessage("1.0") },
		"exponent integer":    func(m map[string]any) { evidence(m)["sdk_request_id"] = json.RawMessage("12e0") },
		"overflow":            func(m map[string]any) { evidence(m)["sdk_request_id"] = json.RawMessage("9223372036854775808") },
		"chunk null":          func(m map[string]any) { m["chunk"] = nil },
		"chunk negative":      func(m map[string]any) { m["chunk"].(map[string]any)["index"] = -1 },
		"chunk float":         func(m map[string]any) { m["chunk"].(map[string]any)["index"] = json.RawMessage("0.0") },
		"chunk bool":          func(m map[string]any) { m["chunk"].(map[string]any)["is_last"] = 1 },
		"chunk terminal":      func(m map[string]any) { m["chunk"].(map[string]any)["is_last"] = false },
		"wrong day":           func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = "20261103" },
		"null day":            func(m map[string]any) { m["payload"].(map[string]any)["trade_date"] = nil },
		"missing day":         func(m map[string]any) { delete(m["payload"].(map[string]any), "trade_date") },
		"wrong account":       func(m map[string]any) { m["payload"].(map[string]any)["account_id"] = "990000000002" },
		"wrong schema":        func(m map[string]any) { m["payload"].(map[string]any)["schema_version"] = "old" },
		"wrong product":       func(m map[string]any) { m["payload"].(map[string]any)["product_type"] = "stock" },
		"null items":          func(m map[string]any) { m["payload"].(map[string]any)["items"] = nil },
		"missing items":       func(m map[string]any) { delete(m["payload"].(map[string]any), "items") },
		"before enqueue":      func(m map[string]any) { evidence(m)["failure_stage"] = "before_enqueue" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			b := newBatch(t, "positions")
			if _, err := b.Accept(editBody(t, base, change), 0); err == nil {
				t.Fatal("invalid page accepted")
			}
			assertState(t, b, BatchInvalid)
		})
	}
	for _, raw := range [][]byte{
		[]byte("null"), []byte("[]"), append(bytes.Clone(base), []byte("{}")...),
		[]byte(strings.Replace(string(base), `"status":`, `"status":"partial","status":`, 1)),
		[]byte(strings.Replace(string(base), `"status":`, `"\u0073tatus":"partial","status":`, 1)),
		{0xff},
	} {
		b := newBatch(t, "positions")
		if _, err := b.Accept(raw, 0); err == nil {
			t.Fatal("malformed raw accepted")
		}
		assertState(t, b, BatchInvalid)
	}
}

func failureBody(t testing.TB, raw []byte) []byte {
	return editBody(t, raw, func(m map[string]any) {
		m["message_id"], m["status"], m["code"] = "failure", "failed", "QUERY_FAILED"
		delete(m, "chunk")
		delete(m["payload"].(map[string]any), "items")
		evidence(m)["snapshot_applied"] = false
		evidence(m)["failure_stage"] = "standard_projection"
	})
}

func TestBatchFailuresNeverBecomeEmptySnapshots(t *testing.T) {
	for _, name := range []string{"before_login", "queue_full", "unsupported_order_state"} {
		raw := editBody(t, fixtureBody(t, "query_edge_cases.json", name), func(m map[string]any) {
			m["action"], m["result_type"] = "account.positions.query", "position_page"
		})
		var m struct {
			Origin  string `json:"origin_message_id"`
			Payload struct {
				Day string `json:"trade_date"`
			} `json:"payload"`
			Adapter struct {
				Evidence struct {
					Ticket     string `json:"ticket"`
					Request    *int64 `json:"sdk_request_id"`
					Generation *int64 `json:"connection_generation"`
				} `json:"query_evidence"`
			} `json:"adapter_context"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		c := batchContext("positions")
		c.OriginMessageID, c.TradeDate, c.Ticket = m.Origin, m.Payload.Day, m.Adapter.Evidence.Ticket
		c.SDKRequestID, c.ConnectionGeneration = m.Adapter.Evidence.Request, m.Adapter.Evidence.Generation
		b, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Accept(raw, 0); err != nil {
			t.Fatal(err)
		}
		assertState(t, b, BatchFailed)
		if replay, err := b.Accept(raw, 0); !replay || err != nil {
			t.Fatal("failed replay rejected", err)
		}
	}
	queue := fixtureBody(t, "messages.json", "queue_expired")
	c := batchContext("asset")
	c.OriginMessageID, c.Ticket, c.SDKRequestID = "fixture-query-expired", "fixture-ticket-3", nil
	b, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accept(queue, 0); err != nil {
		t.Fatal(err)
	}
	assertState(t, b, BatchFailed)

	base := fixtureBody(t, "messages.json", "positions")
	failure := failureBody(t, base)
	b = newBatch(t, "positions")
	b.Accept(page(t, "positions", "partial", 0, false, "FIRST"), 0)
	if _, err := b.Accept(failure, 0); err != nil {
		t.Fatal(err)
	}
	assertState(t, b, BatchFailed)
	if _, err := b.Accept(base, 0); err == nil {
		t.Fatal("success after failure accepted")
	}
	assertState(t, b, BatchInvalid)

	b = newBatch(t, "positions")
	b.Accept(page(t, "positions", "last", 1, true, "FIRST"), 0)
	if _, err := b.Accept(failure, 0); err == nil {
		t.Fatal("terminal contradiction accepted")
	}
	assertState(t, b, BatchInvalid)
}

func TestBatchFailureShapeAndEvidence(t *testing.T) {
	base := failureBody(t, fixtureBody(t, "messages.json", "positions"))
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["payload"].(map[string]any)["items"] = []any{} },
		func(m map[string]any) { m["chunk"] = nil },
		func(m map[string]any) { m["code"] = "OK" },
		func(m map[string]any) { evidence(m)["snapshot_applied"] = true },
		func(m map[string]any) { evidence(m)["snapshot_applied"] = nil },
		func(m map[string]any) { evidence(m)["failure_stage"] = "queue_expired" },
		func(m map[string]any) { evidence(m)["failure_stage"] = "before_enqueue" },
		func(m map[string]any) { evidence(m)["failure_stage"] = nil },
	} {
		b := newBatch(t, "positions")
		if _, err := b.Accept(editBody(t, base, change), 0); err == nil {
			t.Fatal("invalid failure accepted")
		}
		assertState(t, b, BatchInvalid)
	}
}

func TestBatchEmptyPositionVsEmptyAsset(t *testing.T) {
	for _, name := range []string{"asset", "positions"} {
		b := newBatch(t, name)
		raw := editBody(t, fixtureBody(t, "messages.json", name), func(m map[string]any) { m["payload"].(map[string]any)["items"] = []any{} })
		_, err := b.Accept(raw, 0)
		if name == "asset" {
			if err == nil {
				t.Fatal("empty funds accepted")
			}
			assertState(t, b, BatchInvalid)
		} else {
			if err != nil {
				t.Fatal(err)
			}
			c, err := b.Candidate()
			if err != nil || c.Positions == nil || len(c.Positions) != 0 {
				t.Fatal("empty positions lost", err)
			}
		}
	}
}

func TestBatchLimits(t *testing.T) {
	first, second := page(t, "positions", "first", 0, false, "FIRST"), page(t, "positions", "last", 1, true, "SECOND")
	for _, limits := range []BatchLimits{
		{1, 10, maxSnapshotBytes}, {10, 1, maxSnapshotBytes}, {10, 10, len(first) + len(second) - 1}, {10, 10, 1},
	} {
		b, err := NewQueryBatch(batchContext("positions"), 0, time.Minute, limits)
		if err != nil {
			t.Fatal(err)
		}
		_, err = b.Accept(first, 0)
		if err == nil {
			if replay, err := b.Accept(first, 0); !replay || err != nil {
				t.Fatal("replay consumed budget", err)
			}
			_, err = b.Accept(second, 0)
		}
		if err == nil {
			t.Fatal("limit bypassed")
		}
		assertState(t, b, BatchInvalid)
	}
	b, err := NewQueryBatch(batchContext("positions"), 0, time.Minute, BatchLimits{2, 2, len(first) + len(second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accept(first, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accept(second, 0); err != nil {
		t.Fatal("exact limits rejected", err)
	}
	assertState(t, b, BatchReady)
}

func TestBatchContextValidation(t *testing.T) {
	for _, change := range []func(*QueryContext){
		func(c *QueryContext) { c.Action = "order.list.query" },
		func(c *QueryContext) { c.Action = "fill.list.query" },
		func(c *QueryContext) { c.TradeDate = "20260230" },
		func(c *QueryContext) { c.Route.Env = "other" },
		func(c *QueryContext) { c.Route.BrokerID = "CTP" },
		func(c *QueryContext) { c.NativeBrokerID = " 4900" },
		func(c *QueryContext) { c.OriginMessageID = "" },
		func(c *QueryContext) { c.Ticket = "" },
		func(c *QueryContext) { c.CounterSessionID = "" },
		func(c *QueryContext) { c.ConnectionGeneration = nil },
		func(c *QueryContext) { *c.SDKRequestID = 0 },
	} {
		c := batchContext("positions")
		change(&c)
		if _, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits()); err == nil {
			t.Fatal("invalid context accepted")
		}
	}
	for _, limits := range []BatchLimits{{}, {257, 1, 1}, {1, maxSnapshotRows + 1, 1}, {1, 1, maxSnapshotBytes + 1}} {
		if _, err := NewQueryBatch(batchContext("positions"), 0, time.Minute, limits); err == nil {
			t.Fatal("invalid limits accepted")
		}
	}
	if _, err := NewQueryBatch(batchContext("positions"), time.Minute, time.Minute, DefaultBatchLimits()); err == nil {
		t.Fatal("elapsed deadline accepted")
	}
}

func TestBatchOwnershipAndInterrupt(t *testing.T) {
	c := batchContext("positions")
	b, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits())
	if err != nil {
		t.Fatal(err)
	}
	*c.SDKRequestID, *c.ConnectionGeneration = 99, 99
	raw := fixtureBody(t, "messages.json", "positions")
	if _, err := b.Accept(raw, time.Second); err != nil {
		t.Fatal("caller mutated context", err)
	}
	raw[0] = '!'
	out, _ := b.Candidate()
	out.Positions[0].Symbol = "changed"
	*out.Context.SDKRequestID = 98
	evidence := b.AcceptedMessages()
	evidence[0][0] = '!'
	next, _ := b.Candidate()
	if next.Positions[0].Symbol != "rb2612" || *next.Context.SDKRequestID != 12 || b.AcceptedMessages()[0][0] != '{' {
		t.Fatal("mutable data shared")
	}
	b.Interrupt("new connection generation")
	assertState(t, b, BatchInterrupted)
	if _, err := b.Accept(fixtureBody(t, "messages.json", "positions"), time.Second); err == nil {
		t.Fatal("interrupted query resumed")
	}
	b = newBatch(t, "positions")
	b.Advance(time.Second)
	if b.Advance(0) == nil {
		t.Fatal("clock regression accepted")
	}
	assertState(t, b, BatchInvalid)
}

func TestBatchAdapterAuditAndNanoseconds(t *testing.T) {
	b := newBatch(t, "positions")
	raw := editBody(t, fixtureBody(t, "messages.json", "positions"), func(m map[string]any) {
		m["produced_at"] = "2026-11-03T21:01:00.000000001+08:00"
		m["adapter_context"].(map[string]any)["native_audit"] = map[string]any{"raw": "preserved", "unknown": 9}
		m["payload"].(map[string]any)["account_id"] = "990000000001"
		m["request_id"] = "optional-request"
		m["message"] = ""
	})
	if _, err := b.Accept(raw, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, b.AcceptedMessages()[0]) {
		t.Fatal("audit/time rewritten")
	}
}

func FuzzQueryBatch(f *testing.F) {
	f.Add(fixtureBody(f, "messages.json", "positions"))
	f.Add(failureBody(f, fixtureBody(f, "messages.json", "positions")))
	f.Add([]byte("null"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		b := newBatch(t, "positions")
		_, err := b.Accept(raw, 0)
		if err != nil {
			if _, e := b.Candidate(); e == nil {
				t.Fatal("rejected input produced candidate")
			}
		}
	})
}
