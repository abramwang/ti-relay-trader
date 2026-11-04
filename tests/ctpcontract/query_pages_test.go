package ctpcontract

import (
	"bytes"
	"encoding/json"
	"testing"

	"ti-relay-trader/internal/redisstream"
)

func TestQueryPageEnvelopes(t *testing.T) {
	var fixtures messages
	readFixture(t, "query_pages.json", &fixtures)
	if !fixtures.SyntheticOnly || fixtures.ContractVersion != "ctp.f0.draft.2" || len(fixtures.Cases) != 2 {
		t.Fatal("unexpected query fixture catalog")
	}
	for _, c := range fixtures.Cases {
		t.Run(c.Name, func(t *testing.T) {
			env, err := redisstream.DecodeEntry(prefix+":"+c.Suffix, "1793710980000-0", map[string]any{"body": string(c.Body)})
			if err != nil || env.Status != "completed" || env.Routing.AccountID != account {
				t.Fatalf("query envelope: %#v, error=%v", env, err)
			}
			var body map[string]any
			d := json.NewDecoder(bytes.NewReader(c.Body))
			d.UseNumber()
			if err := d.Decode(&body); err != nil {
				t.Fatal(err)
			}
			p := object(t, body["payload"])
			if err := validateDraftPayload(p, env.Action, ""); err != nil {
				t.Fatal(err)
			}
			row := object(t, p["items"].([]any)[0])
			if row["trade_date"] != p["trade_date"] || row["account_id"] != env.Routing.AccountID {
				t.Fatal("query row scope lost")
			}
			ids := object(t, row["native_ids"])
			if ids["order_sys_id"] != "00001234" || row["gateway_order_id"] != "fixture-order-1" {
				t.Fatal("query identity differs from event fixture")
			}
			if c.Name == "orders" {
				if env.Action != "order.list.query" || env.ResultType != "order_page" || ids["sdk_request_id"] != json.Number("3") {
					t.Fatal("order query replaced original order RequestID")
				}
			} else if c.Name == "fills" {
				if env.Action != "fill.list.query" || env.ResultType != "fill_page" || row["fill_id"] != fixturePayload(t, "fill")["fill_id"] {
					t.Fatal("fill query identity changed")
				}
				if _, exists := ids["sdk_request_id"]; exists {
					t.Fatal("TradeField fabricated a query RequestID")
				}
			} else {
				t.Fatal("unknown query fixture")
			}
		})
	}
}
