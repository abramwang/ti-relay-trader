package futures

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func tradeQueryContext(kind string) QueryContext {
	c := batchContext("positions")
	c.NativeInvestorID = "FIXTURE_INV"
	if kind == "order" {
		c.Action = "order.list.query"
		c.OriginMessageID = "fixture-query-orders"
		c.Ticket = "fixture-ticket-4"
		*c.SDKRequestID = 13
	} else {
		c.Action = "fill.list.query"
		c.OriginMessageID = "fixture-query-fills"
		c.Ticket = "fixture-ticket-5"
		*c.SDKRequestID = 14
	}
	return c
}

func tradePage(t testing.TB, kind, id string, index int, last bool, items ...[]byte) []byte {
	name := "orders"
	if kind == "fill" {
		name = "fills"
	}
	return editBody(t, fixtureBody(t, "query_pages.json", name), func(m map[string]any) {
		m["message_id"] = id
		m["status"] = "partial"
		if last {
			m["status"] = "completed"
		}
		m["chunk"] = map[string]any{"index": index, "is_last": last}
		rows := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			rows = append(rows, json.RawMessage(item))
		}
		m["payload"].(map[string]any)["items"] = rows
	})
}

func TestTradeQuerySharedPagesAndCopies(t *testing.T) {
	for _, kind := range []string{"order", "fill"} {
		b, err := NewQueryBatch(tradeQueryContext(kind), 0, time.Minute, DefaultBatchLimits())
		if err != nil {
			t.Fatal(err)
		}
		raw := tradePage(t, kind, "one", 0, true, tradeFixture(t, kind))
		if _, err := b.Accept(raw, 0); err != nil {
			t.Fatal(err)
		}
		c, err := b.Candidate()
		if err != nil {
			t.Fatal(err)
		}
		if kind == "order" {
			*c.Orders[0].SubmittedAt = "changed"
			*c.Orders[0].NativeIDs.FrontID = 77
			next, _ := b.Candidate()
			if *next.Orders[0].SubmittedAt == "changed" || *next.Orders[0].NativeIDs.FrontID == 77 {
				t.Fatal("aliased order candidate")
			}
		} else {
			*c.Fills[0].MatchedAt = "changed"
			next, _ := b.Candidate()
			if *next.Fills[0].MatchedAt == "changed" {
				t.Fatal("aliased fill candidate")
			}
		}
		if replay, err := b.Accept(raw, time.Hour); err != nil || !replay {
			t.Fatal(replay, err)
		}
	}
}

func TestTradeQueryCrossPageDuplicateAndEmpty(t *testing.T) {
	for _, kind := range []string{"order", "fill"} {
		for _, duplicate := range []bool{false, true} {
			b, _ := NewQueryBatch(tradeQueryContext(kind), 0, time.Minute, DefaultBatchLimits())
			first := tradeFixture(t, kind)
			if _, err := b.Accept(tradePage(t, kind, "last", 1, true, first), 0); err != nil {
				t.Fatal(err)
			}
			assertState(t, b, BatchCollecting)
			var rows [][]byte
			if duplicate {
				rows = append(rows, first)
			}
			_, err := b.Accept(tradePage(t, kind, "first", 0, false, rows...), 0)
			if duplicate {
				if err == nil {
					t.Fatal("cross-page duplicate accepted")
				}
				assertState(t, b, BatchInvalid)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertState(t, b, BatchReady)
			}
		}
		b, _ := NewQueryBatch(tradeQueryContext(kind), 0, time.Minute, DefaultBatchLimits())
		if _, err := b.Accept(tradePage(t, kind, "empty", 0, true), 0); err != nil {
			t.Fatal(err)
		}
		c, _ := b.Candidate()
		if kind == "order" && c.Orders == nil || kind == "fill" && c.Fills == nil {
			t.Fatal("successful empty result lost")
		}
	}
}

func TestTradeQueryNativeScopeAndMalformedRows(t *testing.T) {
	for _, kind := range []string{"order", "fill"} {
		c := tradeQueryContext(kind)
		c.NativeInvestorID = ""
		if _, err := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits()); err == nil {
			t.Fatal("missing authorization accepted")
		}
		c = tradeQueryContext(kind)
		c.NativeInvestorID = "OTHER"
		b, _ := NewQueryBatch(c, 0, time.Minute, DefaultBatchLimits())
		if _, err := b.Accept(tradePage(t, kind, "wrong", 0, true, tradeFixture(t, kind)), 0); err == nil {
			t.Fatal("foreign native investor accepted")
		}
		assertState(t, b, BatchInvalid)
		bad := editBody(t, tradeFixture(t, kind), func(m map[string]any) { m["business_type"] = "S" })
		b, _ = NewQueryBatch(tradeQueryContext(kind), 0, time.Minute, DefaultBatchLimits())
		if _, err := b.Accept(tradePage(t, kind, "bad", 0, true, bad), 0); err == nil {
			t.Fatal("invalid row accepted")
		}
	}
}

func TestTradeEventSharedFixturesAndRaw(t *testing.T) {
	origin := "fixture-submit-1"
	for _, kind := range []string{"order", "fill"} {
		raw := fixtureBody(t, "messages.json", kind)
		got, err := DecodeTradeEvent(raw, EventContext{tradeScope(), &origin})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Raw, raw) || kind == "order" && got.Order == nil || kind == "fill" && got.Fill == nil {
			t.Fatal("lost event/raw")
		}
		if kind == "order" && got.Order.TradeDate != "20261104" {
			t.Fatal("night session day changed")
		}
		if kind == "fill" && *got.Fill.MatchedAt != "2026-11-03T21:01:01+08:00" {
			t.Fatal("source time changed")
		}
		raw[0] = '!'
		if got.Raw[0] == '!' {
			t.Fatal("aliased audit")
		}
		if len(got.RawPayload) == 0 {
			t.Fatal("missing exact payload")
		}
	}
}

func TestTradeEventRejectsEnvelopeAndOriginMismatch(t *testing.T) {
	origin := "fixture-submit-1"
	for _, kind := range []string{"order", "fill"} {
		raw := fixtureBody(t, "messages.json", kind)
		changes := []func(map[string]any){
			func(m map[string]any) { m["routing"].(map[string]any)["env"] = "prod" },
			func(m map[string]any) { m["protocol"] = "other" },
			func(m map[string]any) { m["message_type"] = "reply" },
			func(m map[string]any) { m["event_type"] = "asset.event" },
			func(m map[string]any) { m["produced_at"] = "2026-11-03T13:01:01Z" },
			func(m map[string]any) { m["message_id"] = nil },
			func(m map[string]any) { m["origin_message_id"] = "wrong" },
			func(m map[string]any) { delete(m, "origin_message_id") },
			func(m map[string]any) { m["adapter_context"] = nil },
			func(m map[string]any) { m["unexpected"] = true },
			func(m map[string]any) { delete(m["payload"].(map[string]any), "product_type") },
			func(m map[string]any) { m["payload"].(map[string]any)["schema_version"] = "other" },
			func(m map[string]any) {
				m["payload"].(map[string]any)["native_ids"].(map[string]any)["investor_id"] = "OTHER"
			},
		}
		for i, edit := range changes {
			if _, err := DecodeTradeEvent(editBody(t, raw, edit), EventContext{tradeScope(), &origin}); err == nil {
				t.Fatalf("%s mutation %d accepted", kind, i)
			}
		}
		duplicate := []byte(strings.Replace(string(raw), `"message_id":`, `"message_id":"duplicate","message_id":`, 1))
		for _, bad := range [][]byte{duplicate, append(bytes.Clone(raw), '0'), []byte("null"), {0xff}} {
			if _, err := DecodeTradeEvent(bad, EventContext{tradeScope(), &origin}); err == nil {
				t.Fatal("invalid JSON accepted")
			}
		}
	}
}

func TestTradeEventExternalOriginAndHistoricalSession(t *testing.T) {
	raw := editBody(t, fixtureBody(t, "messages.json", "order"), func(m map[string]any) {
		delete(m, "origin_message_id")
		p := m["payload"].(map[string]any)
		p["gateway_order_id"] = nativeIdentity("order", tradeScope(), "SHFE", "00001234")
		p["counter_session_id"] = "historical-order-session"
		p["submitted_at"] = nil
	})
	got, err := DecodeTradeEvent(raw, EventContext{Scope: tradeScope()})
	if err != nil || got.Order.SubmittedAt != nil || got.Order.CounterSessionID != "historical-order-session" {
		t.Fatal(got, err)
	}
}
