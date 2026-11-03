package ctpcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
)

// This is a fixture contract check, not the application's CTP command parser.
func validateDraftPayload(p map[string]any, action, event string) error {
	if p["product_type"] != "futures" || p["schema_version"] != "ctp.f0.draft.2" {
		return fmt.Errorf("missing or conflicting payload discriminator")
	}
	if action == "order.batch.submit" {
		parent, ok := p["account_id"].(string)
		if !ok || parent == "" {
			return fmt.Errorf("missing batch account")
		}
		children, ok := p["orders"].([]any)
		if !ok || len(children) == 0 {
			return fmt.Errorf("missing batch children")
		}
		for _, child := range children {
			c, ok := child.(map[string]any)
			if !ok || c["account_id"] != parent || c["business_type"] != "F" {
				return fmt.Errorf("batch child account/product mismatch")
			}
			for _, key := range []string{"product_type", "schema_version"} {
				if value, exists := c[key]; exists && value != p[key] {
					return fmt.Errorf("batch child %s conflicts; explicit null is not inheritance", key)
				}
			}
		}
	}
	if event == "fill.event" {
		ids, ok := p["native_ids"].(map[string]any)
		if !ok {
			return fmt.Errorf("missing native IDs")
		}
		trade, ok := ids["trade_id"].(string)
		if !ok || trade == "" {
			return fmt.Errorf("missing native trade ID")
		}
		day, dayOK := p["trade_date"].(string)
		exchange, exchangeOK := p["exchange"].(string)
		if !dayOK || !exchangeOK || p["account_id"] != account {
			return fmt.Errorf("missing fixture fill scope")
		}
		digest := sha256.Sum256(identityBytes("fill", "test", "ctp_4900", account, account, day, exchange, trade))
		if p["fill_id"] != "ctp-fill-v1-"+hex.EncodeToString(digest[:]) {
			return fmt.Errorf("missing or conflicting standard fill ID")
		}
	}
	return nil
}

func fixturePayload(t *testing.T, name string) map[string]any {
	t.Helper()
	var f messages
	readFixture(t, "messages.json", &f)
	for _, c := range f.Cases {
		if c.Name == name {
			var body struct {
				Payload map[string]any `json:"payload"`
			}
			if err := json.Unmarshal(c.Body, &body); err != nil {
				t.Fatal(err)
			}
			return body.Payload
		}
	}
	t.Fatalf("fixture not found: %s", name)
	return nil
}

func TestDraft2RegressionNegatives(t *testing.T) {
	for _, name := range []string{"accepted", "queue_expired"} {
		for _, field := range []string{"product_type", "schema_version"} {
			t.Run(name+"_missing_"+field, func(t *testing.T) {
				p := fixturePayload(t, name)
				delete(p, field)
				if validateDraftPayload(p, "", "") == nil {
					t.Fatal("draft.1 missing marker accepted")
				}
			})
		}
	}
	for _, mutate := range []func(map[string]any){
		func(p map[string]any) { delete(p, "fill_id") },
		func(p map[string]any) { p["fill_id"] = "ctp-fill-v1-wrong" },
		func(p map[string]any) { p["fill_id"] = nil },
		func(p map[string]any) { p["trade_date"] = "20261103" },
	} {
		p := fixturePayload(t, "fill")
		mutate(p)
		if validateDraftPayload(p, "", "fill.event") == nil {
			t.Fatal("invalid fill identity accepted")
		}
	}
	for _, field := range []string{"product_type", "schema_version", "account_id", "business_type"} {
		for _, value := range []any{nil, "conflict"} {
			p := fixturePayload(t, "batch")
			child := p["orders"].([]any)[0].(map[string]any)
			child[field] = value
			if validateDraftPayload(p, "order.batch.submit", "") == nil {
				t.Fatalf("conflicting batch %s accepted", field)
			}
		}
	}
	p := fixturePayload(t, "batch")
	child := p["orders"].([]any)[0].(map[string]any)
	child["product_type"] = p["product_type"]
	child["schema_version"] = p["schema_version"]
	if err := validateDraftPayload(p, "order.batch.submit", ""); err != nil {
		t.Fatal(err)
	}
}

type instrumentMapping struct {
	SchemaVersion      string      `json:"schema_version"`
	SecurityID         string      `json:"security_id"`
	SourceInstrumentID string      `json:"source_instrument_id"`
	ExchangeID         string      `json:"exchange_id"`
	ProductID          string      `json:"product_id"`
	VolumeMultiple     json.Number `json:"volume_multiple"`
	PriceTick          json.Number `json:"price_tick"`
	TradingStatus      string      `json:"trading_status"`
	SourceSnapshotID   string      `json:"source_snapshot_id"`
}

func TestMeridianExplicitInstrumentMapping(t *testing.T) {
	var f struct {
		ContractVersion     string              `json:"contract_version"`
		SyntheticOnly       bool                `json:"synthetic_only"`
		SourceContractID    string              `json:"source_contract_id"`
		SourceFixtureSHA256 string              `json:"source_fixture_sha256"`
		SourceFixture       string              `json:"source_fixture"`
		Instruments         []instrumentMapping `json:"instruments"`
		Cases               []struct {
			Name               string `json:"name"`
			SecurityID         string `json:"security_id"`
			SourceInstrumentID string `json:"source_instrument_id"`
			ExchangeID         string `json:"exchange_id"`
			ExpectedMatch      bool   `json:"expected_match"`
		} `json:"cases"`
	}
	readFixture(t, "meridian_mapping.json", &f)
	if !f.SyntheticOnly || f.ContractVersion != "ctp.f0.draft.2" || f.SourceContractID != "meridian.ctp_futures_level1.v1" || len(f.Cases) != 8 {
		t.Fatal("invalid mapping catalogue")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			matches := 0
			for _, row := range f.Instruments {
				if row.SchemaVersion != "futures_instrument.v1" || row.SourceSnapshotID != "fixture-instruments-v1" {
					t.Fatal("changed Meridian contract/source")
				}
				if row.SecurityID == c.SecurityID && row.SourceInstrumentID == c.SourceInstrumentID && row.ExchangeID == c.ExchangeID {
					matches++
				}
			}
			if (matches == 1) != c.ExpectedMatch {
				t.Fatal("mapping inferred instead of exact directory lookup")
			}
		})
	}
}
