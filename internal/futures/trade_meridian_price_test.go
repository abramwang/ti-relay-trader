package futures

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func meridianPriceFixture(t *testing.T, now time.Time) ([]byte, CatalogBinding) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/fixtures/meridian_futures_price.synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = editBody(t, raw, func(m map[string]any) {
		r := m["data"].([]any)[0].(map[string]any)
		r["exchange_timestamp"] = now.Add(-100 * time.Millisecond).Format(time.RFC3339Nano)
		r["received_at"] = now.Add(-50 * time.Millisecond).Format(time.RFC3339Nano)
	})
	return raw, CatalogBinding{"RB2701.SHFE", "rb2701", "SHFE", "synthetic-version", "synthetic-checksum"}
}

func TestMeridianPricePreparation(t *testing.T) {
	now, _ := time.Parse(time.RFC3339Nano, "2026-11-16T21:18:47.700+08:00")
	raw, b := meridianPriceFixture(t, now)
	p, err := PrepareMeridianPrice(raw, b, "1", "20261117", now)
	if err != nil || p.BusinessAuthorized || p.BodySHA256 != commandDigest(raw) || !p.ValidUntil.Equal(now.Add(4900*time.Millisecond)) {
		t.Fatal(p, err)
	}
	if p.Market.NativeFacts != nil || p.Market.PriceSource != meridianPriceSource || p.Market.BestBid != "2999" {
		t.Fatal(p)
	}
	for _, price := range []Price{"3000", "3173", "2870"} {
		if err := validateLimitOrderPrice(price, p.Market.PriceTick, p.Market.LowerLimit, p.Market.UpperLimit); err != nil {
			t.Fatal(err)
		}
	}
	raw[0] = 'x'
	if verifyMeridianMarket(p.Market) != nil {
		t.Fatal("caller mutated retained original")
	}
	for _, at := range []time.Time{now.Add(4900 * time.Millisecond), now.Add(-time.Second)} {
		if _, err := meridianPriceDeadline(p.Market, at); err == nil {
			t.Fatal("stale/future data accepted")
		}
	}
	if _, err := meridianPriceDeadline(p.Market, now.UTC()); err != nil {
		t.Fatal("host timezone changed freshness", err)
	}
}

func TestMeridianPriceRejectsInvalidEvidence(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	raw, b := meridianPriceFixture(t, now)
	for name, mutate := range map[string]func(map[string]any, map[string]any, map[string]any){
		"stale":           func(m, r, meta map[string]any) { meta["stale_data"] = true },
		"missing_flag":    func(m, r, meta map[string]any) { delete(meta, "stale_data") },
		"process_stopped": func(m, r, meta map[string]any) { meta["process_running"] = false },
		"idle":            func(m, r, meta map[string]any) { meta["cache_status"] = "session_idle" },
		"wrong_source":    func(m, r, meta map[string]any) { r["source"] = "oc" },
		"wrong_day":       func(m, r, meta map[string]any) { r["trading_day"] = 20261118 },
		"wrong_case":      func(m, r, meta map[string]any) { r["source_instrument_id"] = "RB2701" },
		"wrong_exchange":  func(m, r, meta map[string]any) { r["exchange_id"] = "CFFEX" },
		"wrong_session":   func(m, r, meta map[string]any) { r["session_id"] = "old" },
		"missing_target":  func(m, r, meta map[string]any) { r["security_id"] = "AU2612.SHFE" },
		"duplicate":       func(m, r, meta map[string]any) { m["data"] = []any{r, r}; meta["count"] = 2 },
		"wrong_count":     func(m, r, meta map[string]any) { meta["count"] = 2 },
		"fresh_global_stale_contract": func(m, r, meta map[string]any) {
			r["exchange_timestamp"] = now.Add(-6 * time.Second).Format(time.RFC3339Nano)
		},
		"future":      func(m, r, meta map[string]any) { r["received_at"] = now.Add(time.Second).Format(time.RFC3339Nano) },
		"no_timezone": func(m, r, meta map[string]any) { r["exchange_timestamp"] = "2026-11-17T13:40:00" },
		"null_limit":  func(m, r, meta map[string]any) { r["lower_limit_price"] = nil },
		"no_bid":      func(m, r, meta map[string]any) { r["bids"] = []any{} },
		"unavailable": func(m, r, meta map[string]any) { r["bids"].([]any)[0].(map[string]any)["available"] = false },
		"zero_volume": func(m, r, meta map[string]any) { r["asks"].([]any)[0].(map[string]any)["volume"] = 0 },
		"crossed":     func(m, r, meta map[string]any) { r["asks"].([]any)[0].(map[string]any)["price"] = 2998 },
		"off_tick":    func(m, r, meta map[string]any) { r["bids"].([]any)[0].(map[string]any)["price"] = 2999.5 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := editBody(t, raw, func(m map[string]any) { mutate(m, m["data"].([]any)[0].(map[string]any), m["meta"].(map[string]any)) })
			if _, err := PrepareMeridianPrice(changed, b, "1", "20261117", now); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	for _, value := range []string{"null", `"1"`, `  "1"  `, "0", "-1", "true", "1e999999999", "1e-999999999", "0.0000000001"} {
		if _, err := meridianDecimal([]byte(value)); err == nil {
			t.Fatal("invalid number", value)
		}
	}
	for input, want := range map[string]Price{"2e-1": "0.2", "3.000e3": "3000", "0.000000001": "0.000000001"} {
		if p, err := meridianDecimal([]byte(input)); err != nil || p != want {
			t.Fatal(p, err)
		}
	}
	for _, invalid := range [][]byte{append(raw, raw...), []byte(`{"data":[],"data":[],"meta":{}}`), []byte(strings.Repeat(" ", maxMeridianPriceBody+1))} {
		if _, err := PrepareMeridianPrice(invalid, b, "1", "20261117", now); err == nil {
			t.Fatal("malformed body accepted")
		}
	}
}

func TestMeridianPriceExecutionSeparation(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-17T13:40:00+08:00")
	v, e, target, a, hb := executionTradeFixture(t, now)
	raw, b := meridianPriceFixture(t, now)
	p, err := PrepareMeridianPrice(raw, b, "1", target.TradeDate, now)
	if err != nil {
		t.Fatal(err)
	}
	a.Market = &p.Market
	for key := range e.Mappings {
		e.Mappings[key] = b
	}
	v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) { m["payload"].(map[string]any)["limit_price"] = "3000" }))
	a.CommandSHA256 = commandDigest([]byte(v.Raw))
	if _, err := ValidateTestTradePreparation([]byte(v.Raw), v.scope(), target, e, a); err != nil {
		t.Fatal("strategy-selected ask price rejected", err)
	}
	for _, price := range []string{"2870", "3173", "4000", "3000.5", "0"} {
		body := editBody(t, []byte(v.Raw), func(m map[string]any) { m["payload"].(map[string]any)["limit_price"] = price })
		changed := a
		changed.CommandSHA256 = commandDigest(body)
		_, err := ValidateTestTradePreparation(body, v.scope(), target, e, changed)
		if (price == "3000.5" || price == "0") != (err != nil) {
			t.Fatal("tick checks or broker-owned limits changed", price, err)
		}
	}
	proof, err := testTradeHeartbeatProof(hb, v.scope().Route, target, a, now)
	if err != nil || !proof.ExpiresAt.Equal(p.ValidUntil) {
		t.Fatal(proof, err)
	}
	// Original bytes survive approval persistence, with no native query reference.
	encoded, _ := json.Marshal(a)
	decoded, err := DecodeTestTradeApproval(encoded)
	if err != nil || string(decoded.Market.MeridianSnapshot) != string(raw) {
		t.Fatal(err)
	}
	for _, mutate := range []func(*TestTradeApproval){
		func(a *TestTradeApproval) { a.PermissionProfile = "test_day_relay.v1"; a.ExecutionPermission = nil },
		func(a *TestTradeApproval) { a.Market.PriceSource = nativeFactsPriceSource },
		func(a *TestTradeApproval) { a.Market.PriceSource = "" },
		func(a *TestTradeApproval) { a.Market.BestBid = "5218" },
		func(a *TestTradeApproval) { a.Market.ObservedAt = assessmentTime(now) },
		func(a *TestTradeApproval) { a.Market.MeridianSnapshot = nil },
	} {
		changed, _ := DecodeTestTradeApproval(encoded)
		mutate(&changed)
		data, _ := json.Marshal(changed)
		if _, err := DecodeTestTradeApproval(data); err == nil {
			t.Fatal("source mixing/tamper accepted")
		}
	}
	prod := v.scope()
	prod.Route.Env = "production"
	if _, err := ValidateTestTradePreparation([]byte(v.Raw), prod, target, e, a); err == nil {
		t.Fatal("TEST feature opened production")
	}
	a.ExecutionPermission.Submit = false
	if _, err := testTradeHeartbeatProof(hb, v.scope().Route, target, a, now); !errors.Is(err, ErrQueryPublishGate) {
		t.Fatal("quote granted trading", err)
	}
}

func TestMeridianPriceCFFEXMapping(t *testing.T) {
	now := time.Now()
	raw, b := meridianPriceFixture(t, now)
	b.SecurityID, b.SourceInstrumentID, b.ExchangeID = "IF2612.CFFEX", "IF2612", "CFFEX"
	raw = editBody(t, raw, func(m map[string]any) {
		r := m["data"].([]any)[0].(map[string]any)
		r["security_id"] = b.SecurityID
		r["source_instrument_id"] = b.SourceInstrumentID
		r["exchange_id"] = b.ExchangeID
	})
	if _, err := PrepareMeridianPrice(raw, b, "0.2", "20261117", now); err != nil {
		t.Fatal(err)
	}
	wrong := b
	wrong.SourceInstrumentID = "if2612"
	if _, err := PrepareMeridianPrice(raw, wrong, "0.2", "20261117", now); err == nil {
		t.Fatal("native ID case changed")
	}
}

func TestMeridianPriceDurablePublication(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		db, store, ctx := assessmentDB(t)
		v, e, target, a, _ := executionTradeFixture(t, time.Now())
		raw, b := meridianPriceFixture(t, time.Now())
		p, err := PrepareMeridianPrice(raw, b, "1", target.TradeDate, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		a.Market = &p.Market
		for key := range e.Mappings {
			e.Mappings[key] = b
		}
		v.Raw = string(editBody(t, []byte(v.Raw), func(m map[string]any) { m["payload"].(map[string]any)["limit_price"] = "3000" }))
		a.CommandSHA256 = commandDigest([]byte(v.Raw))
		r := registerVector(t, store, ctx, v, e, "registered")
		transport := &fakeQueryPublisher{appendError: unknown}
		result, err := store.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, transport)
		if unknown != errors.Is(err, ErrQueryPublishUnknown) || !unknown && (err != nil || result.State != "published") {
			t.Fatal(result, err)
		}
		var storedJSON []byte
		if err := db.QueryRowContext(ctx, `SELECT target_json->'approval'->'market'->'meridian_snapshot' FROM futures_test_trade_publications WHERE command_id=$1`, r.CommandID).Scan(&storedJSON); err != nil {
			t.Fatal(err)
		}
		var storedBody []byte
		if json.Unmarshal(storedJSON, &storedBody) != nil || string(storedBody) != string(raw) {
			t.Fatal("original snapshot lost in publication audit")
		}
		if n := storedCount(t, db, "futures_test_query_publications"); n != 0 {
			t.Fatal("Meridian pricing introduced OC queries", n)
		}
		fresh, _ := NewCommandStore(db)
		again, err := fresh.publishTestTrade(ctx, v.scope(), r.CommandID, r.Original.MessageID, target, a, transport)
		if !again.Replayed || transport.appends.Load() != 1 || unknown != errors.Is(err, ErrQueryPublishUnknown) {
			t.Fatal(again, err)
		}
	}
}
