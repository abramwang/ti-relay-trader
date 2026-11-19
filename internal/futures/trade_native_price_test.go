package futures

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func nativePriceFixture(t *testing.T, target TestQueryTarget, scope CommandScope, now time.Time) (CommandRegistration, []byte, CatalogBinding) {
	t.Helper()
	raw := editBody(t, constraintsCases(t)[0].Request, func(m map[string]any) {
		m["message_id"], m["idempotency_key"] = "native-facts-query", "native-facts-query"
		m["payload"].(map[string]any)["account_id"] = scope.Route.AccountID
		m["sent_at"] = assessmentTime(now.Add(-time.Second))
	})
	c, err := DecodeCommand(raw, scope, "cmd.query")
	if err != nil {
		t.Fatal(err)
	}
	q := CommandRegistration{CommandID: 1, Scope: scope, Original: c}
	body := editBody(t, constraintsCases(t)[0].Reply, func(m map[string]any) {
		m["routing"], m["produced_at"] = scope.Route, assessmentTime(now)
		m["origin_message_id"], m["request_id"], m["correlation_id"], m["idempotency_key"] = c.MessageID, c.RequestID, c.CorrelationID, c.IdempotencyKey
		p := m["payload"].(map[string]any)
		p["account_id"], p["trade_date"], p["counter_session_id"] = scope.Route.AccountID, target.TradeDate, target.CounterSessionID
		m["adapter_context"].(map[string]any)["connection_generation"] = target.ConnectionGeneration
		f := constraintsItem(m)
		f["trade_date"] = target.TradeDate
		f["static"].(map[string]any)["captured_at"] = assessmentTime(now.Add(-400 * time.Millisecond))
		f["dynamic"].(map[string]any)["captured_at"] = assessmentTime(now.Add(-100 * time.Millisecond))
		f["dynamic"].(map[string]any)["bid_price1"] = "5218"
		f["dynamic"].(map[string]any)["ask_price1"] = "5219"
		for name, delta := range map[string]time.Duration{"instrument": -500 * time.Millisecond, "depth": -200 * time.Millisecond} {
			e := constraintsEvidence(m, name)
			e["started_at"] = assessmentTime(now.Add(delta))
			e["completed_at"] = assessmentTime(now.Add(delta + 100*time.Millisecond))
		}
	})
	return q, body, CatalogBinding{"RB2701.SHFE", "rb2701", "SHFE", "synthetic-directory", "synthetic-checksum"}
}

func TestNativePriceFactsAndAdmission(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	v, _, target, a, hb := executionTradeFixture(t, now)
	q, raw, mapping := nativePriceFixture(t, target, v.scope(), now)
	p, err := nativePriceFromReply(q, target, 5, raw, mapping, now.Add(-time.Second), now)
	if err != nil || p.SuggestedLimitPrice != "5218" || p.BusinessAuthorized || p.AtomicSnapshot || p.Facts.Dynamic.BrokerTradeDate == nil {
		t.Fatal(p, err)
	}
	if !p.ValidUntil.Equal(now.Add(4800*time.Millisecond)) || p.Market.NativeFacts.BodySHA256 != commandDigest(raw) {
		t.Fatal(p)
	}
	v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) {
		p := m["payload"].(map[string]any)
		p["symbol"], p["limit_price"] = "rb2701", "5218"
	}))
	e := registrationEvidence(t, v)
	for key := range e.Mappings {
		e.Mappings[key] = mapping
	}
	a.Market, a.CommandSHA256 = &p.Market, commandDigest([]byte(v.Raw))
	if _, err := ValidateTestTradePreparation([]byte(v.Raw), v.scope(), target, e, a); err != nil {
		t.Fatal(err)
	}
	proof, err := testTradeHeartbeatProof(hb, v.scope().Route, target, a, now)
	if err != nil || !proof.ExpiresAt.Equal(p.ValidUntil) {
		t.Fatal(proof, err)
	}
	for _, price := range []string{"5217", "5218.5", "5219", "2968"} {
		changed := editBody(t, []byte(v.Raw), func(m map[string]any) { m["payload"].(map[string]any)["limit_price"] = price })
		a.CommandSHA256 = commandDigest(changed)
		if _, err := ValidateTestTradePreparation(changed, v.scope(), target, e, a); err == nil {
			t.Fatal("unsafe price", price)
		}
	}
	for name, mutate := range map[string]func(map[string]any){
		"failed": func(m map[string]any) {
			m["status"], m["code"] = "failed", "QUERY_FAILED"
			delete(m, "chunk")
			delete(m["payload"].(map[string]any), "items")
		},
		"partial":        func(m map[string]any) { constraintsEvidence(m, "depth")["is_last"] = false },
		"old_session":    func(m map[string]any) { m["payload"].(map[string]any)["counter_session_id"] = "old" },
		"old_generation": func(m map[string]any) { m["adapter_context"].(map[string]any)["connection_generation"] = 2 },
		"wrong_day":      func(m map[string]any) { constraintsItem(m)["trade_date"] = "20261118" },
		"wrong_account":  func(m map[string]any) { m["routing"].(map[string]any)["account_id"] = "00030484" },
		"wrong_origin":   func(m map[string]any) { m["origin_message_id"] = "other" },
		"wrong_request":  func(m map[string]any) { m["request_id"] = "other" },
		"wrong_contract": func(m map[string]any) { constraintsItem(m)["symbol"] = "rb2701C4350" },
		"missing_bid": func(m map[string]any) {
			constraintsItem(m)["dynamic"].(map[string]any)["bid_price1"] = nil
			constraintsItem(m)["unavailable_numeric_fields"] = []any{"BidPrice1"}
		},
		"missing_limit": func(m map[string]any) {
			constraintsItem(m)["dynamic"].(map[string]any)["upper_limit_price"] = nil
			constraintsItem(m)["unavailable_numeric_fields"] = []any{"UpperLimitPrice"}
		},
		"closed_contract":  func(m map[string]any) { constraintsItem(m)["static"].(map[string]any)["is_trading"] = 0 },
		"option":           func(m map[string]any) { constraintsItem(m)["static"].(map[string]any)["product_class"] = "2" },
		"expired_contract": func(m map[string]any) { constraintsItem(m)["static"].(map[string]any)["expire_date"] = "20261116" },
		"min_lots":         func(m map[string]any) { constraintsItem(m)["static"].(map[string]any)["min_limit_order_volume"] = 2 },
		"zero_multiplier":  func(m map[string]any) { constraintsItem(m)["static"].(map[string]any)["volume_multiple"] = 0 },
		"crossed_book":     func(m map[string]any) { constraintsItem(m)["dynamic"].(map[string]any)["ask_price1"] = "5217" },
		"late_query": func(m map[string]any) {
			constraintsEvidence(m, "depth")["started_at"] = assessmentTime(now.Add(-5 * time.Second))
			constraintsEvidence(m, "instrument")["started_at"] = assessmentTime(now.Add(-6 * time.Second))
			constraintsEvidence(m, "instrument")["completed_at"] = assessmentTime(now.Add(-5500 * time.Millisecond))
			constraintsItem(m)["static"].(map[string]any)["captured_at"] = assessmentTime(now.Add(-5500 * time.Millisecond))
		},
		"future": func(m map[string]any) { m["produced_at"] = assessmentTime(now.Add(time.Second)) },
		"numeric_provenance": func(m map[string]any) {
			m["adapter_context"].(map[string]any)["numeric_source_binary64"].(map[string]any)["PriceTick"] = "4000000000000000"
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := nativePriceFromReply(q, target, 5, editBody(t, raw, mutate), mapping, now.Add(-10*time.Second), now); err == nil {
				t.Fatal("invalid facts authorized price")
			}
		})
	}
	if _, err := nativePriceFromReply(q, target, 5, raw, mapping, now, now); err == nil {
		t.Fatal("query predates publication")
	}
	if _, err := nativePriceFromReply(q, target, 5, raw, mapping, now.Add(-time.Second), now.Add(4800*time.Millisecond)); err == nil {
		t.Fatal("expired facts")
	}
	if _, err := nativeFactsDeadline(p.Market, now.Add(-time.Second)); err == nil {
		t.Fatal("future facts")
	}
}

func TestNativePriceExactTicks(t *testing.T) {
	for _, c := range []struct{ tick, bid, low, high, want string }{
		{"1", "5218", "5218", "5653", "5218"},
		{"1", "5430", "5218", "5653", "5420"},
		{"0.2", "10.6", "10.1", "20", "10.2"},
		{"0.000000001", "1.000000020", "1", "2", "1.00000001"},
		{"1", "5218.1", "5000", "6000", ""},
		{"0", "5218", "5000", "6000", ""},
	} {
		p, err := nativeTestBuyPrice(TestTradeMarket{PriceTick: Price(c.tick), BestBid: Price(c.bid), LowerLimit: Price(c.low), UpperLimit: Price(c.high)})
		if (err == nil) != (c.want != "") || string(p) != c.want {
			t.Fatal(c, p, err)
		}
	}
}

func TestNativePriceEmptyBidRetainsFact(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	v, _, target, a, _ := executionTradeFixture(t, now)
	q, raw, mapping := nativePriceFixture(t, target, v.scope(), now)
	raw = editBody(t, raw, func(m map[string]any) {
		constraintsItem(m)["dynamic"].(map[string]any)["bid_price1"] = nil
		constraintsItem(m)["unavailable_numeric_fields"] = []any{"BidPrice1"}
		m["adapter_context"].(map[string]any)["numeric_source_binary64"].(map[string]any)["BidPrice1"] = "7fefffffffffffff"
	})
	p, err := nativePriceFromReply(q, target, 5, raw, mapping, now.Add(-time.Second), now)
	if err != nil || p.SuggestedLimitPrice != "5218" || p.Market.BestBid != "" || p.Facts.Dynamic.BidPrice1 != nil || p.BusinessAuthorized {
		t.Fatal(p, err)
	}
	e := registrationEvidence(t, v)
	for key := range e.Mappings {
		e.Mappings[key] = mapping
	}
	for _, price := range []string{"5218", "5219", "5217", "5218.5"} {
		body := editBody(t, []byte(v.Raw), func(m map[string]any) {
			fields := m["payload"].(map[string]any)
			fields["symbol"] = "rb2701"
			fields["limit_price"] = price
		})
		a.Market, a.CommandSHA256 = &p.Market, commandDigest(body)
		_, err := ValidateTestTradePreparation(body, v.scope(), target, e, a)
		if (err == nil) != (price == "5218") {
			t.Fatal(price, err)
		}
	}
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) {
			constraintsItem(m)["dynamic"].(map[string]any)["ask_price1"] = nil
			constraintsItem(m)["unavailable_numeric_fields"] = []any{"BidPrice1", "AskPrice1"}
		},
		func(m map[string]any) { constraintsItem(m)["dynamic"].(map[string]any)["ask_price1"] = "5218" },
		func(m map[string]any) {
			m["adapter_context"].(map[string]any)["numeric_source_binary64"].(map[string]any)["BidPrice1"] = "0000000000000000"
		},
	} {
		if _, err := nativePriceFromReply(q, target, 5, editBody(t, raw, mutate), mapping, now.Add(-time.Second), now); err == nil {
			t.Fatal("unsafe empty bid accepted")
		}
	}
	if _, err := nativePriceFromReply(q, target, 5, raw, mapping, now.Add(-time.Second), now.Add(5*time.Second)); err == nil {
		t.Fatal("expired empty bid accepted")
	}
}

func TestNativePriceCaptureWithinRequest(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	v, _, target, _, _ := executionTradeFixture(t, now)
	q, raw, mapping := nativePriceFixture(t, target, v.scope(), now)
	for _, field := range []string{"static", "dynamic"} {
		end := now.Add(-400 * time.Millisecond)
		if field == "dynamic" {
			end = now.Add(-100 * time.Millisecond)
		}
		for _, delta := range []time.Duration{-time.Millisecond, -101 * time.Millisecond, time.Millisecond} {
			b := editBody(t, raw, func(m map[string]any) {
				constraintsItem(m)[field].(map[string]any)["captured_at"] = assessmentTime(end.Add(delta))
			})
			_, err := nativePriceFromReply(q, target, 5, b, mapping, now.Add(-time.Second), now)
			if (err == nil) != (delta == -time.Millisecond) {
				t.Fatal(field, delta, err)
			}
		}
	}
}

func TestNativePriceReferenceStrictAndCopied(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	v, _, target, a, _ := executionTradeFixture(t, now)
	q, raw, mapping := nativePriceFixture(t, target, v.scope(), now)
	p, err := nativePriceFromReply(q, target, 5, raw, mapping, now.Add(-time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	a.Market = &p.Market
	b, _ := json.Marshal(a)
	for _, change := range []func(map[string]any){
		func(m map[string]any) { delete(m["market"].(map[string]any), "native_facts") },
		func(m map[string]any) { m["market"].(map[string]any)["native_facts"] = nil },
		func(m map[string]any) {
			m["permission_profile"] = "test_day_relay.v1"
			delete(m, "execution_permission")
		},
		func(m map[string]any) { m["market"].(map[string]any)["price_source"] = "" },
		func(m map[string]any) {
			m["market"].(map[string]any)["native_facts"].(map[string]any)["receipt_id"] = 1.5
		},
		func(m map[string]any) {
			m["market"].(map[string]any)["native_facts"].(map[string]any)["body_sha256"] = strings.Repeat("z", 64)
		},
	} {
		if _, err := DecodeTestTradeApproval(editBody(t, b, change)); err == nil {
			t.Fatal("malformed native reference")
		}
	}
}
