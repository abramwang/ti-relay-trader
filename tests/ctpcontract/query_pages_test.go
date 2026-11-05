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

func TestQueryFailureEdgeEnvelopes(t *testing.T) {
	var fixtures struct {
		messages
		MaterialVersion string          `json:"material_version"`
		Recovery        json.RawMessage `json:"recovery"`
	}
	readFixture(t, "query_edge_cases.json", &fixtures)
	if !fixtures.SyntheticOnly || fixtures.ContractVersion != "ctp.f0.draft.2" || fixtures.MaterialVersion != "relay.ctp.query.edges.v1" || len(fixtures.Cases) != 3 {
		t.Fatal("unexpected failure fixture catalog")
	}
	for _, c := range fixtures.Cases {
		t.Run(c.Name, func(t *testing.T) {
			env, err := redisstream.DecodeEntry(prefix+":"+c.Suffix, "1793710980000-0", map[string]any{"body": string(c.Body)})
			if err != nil || env.Status != "failed" || env.Routing.AccountID != account {
				t.Fatalf("failure envelope: %#v, error=%v", env, err)
			}
			var body map[string]any
			if err := json.Unmarshal(c.Body, &body); err != nil {
				t.Fatal(err)
			}
			p := object(t, body["payload"])
			if _, exists := p["items"]; exists {
				t.Fatal("failure fabricated rows")
			}
			if _, exists := body["chunk"]; exists {
				t.Fatal("failure fabricated a successful chunk")
			}
			evidence := object(t, object(t, body["adapter_context"])["query_evidence"])
			if evidence["snapshot_applied"] != false {
				t.Fatal("failure claims an applied standard candidate")
			}
			if evidence["failure_stage"] == "before_enqueue" {
				for _, key := range []string{"ticket", "sdk_request_id", "sdk_return_code", "callback_error_id"} {
					value, exists := evidence[key]
					if !exists || value != nil {
						t.Fatalf("missing/null distinction lost for %s", key)
					}
				}
			}
		})
	}
}
