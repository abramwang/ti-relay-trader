package futures

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixtureItems(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/ctp/messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Name string `json:"name"`
			Body struct {
				Payload struct {
					Items json.RawMessage `json:"items"`
				} `json:"payload"`
			} `json:"body"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixtures.Cases {
		if c.Name == name {
			return c.Body.Payload.Items
		}
	}
	t.Fatal("missing fixture")
	return nil
}

func testScope() SnapshotScope {
	return SnapshotScope{AccountID: "990000000001", TradeDate: "20261104", NativeBrokerID: "4900"}
}

func mutate(t *testing.T, name string, change func([]map[string]any) []map[string]any) []byte {
	t.Helper()
	var items []map[string]any
	d := json.NewDecoder(bytes.NewReader(fixtureItems(t, name)))
	d.UseNumber()
	if err := d.Decode(&items); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(change(items))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSnapshotFixturesPreserveNativeValues(t *testing.T) {
	assets, err := DecodeAssets(fixtureItems(t, "asset"), testScope())
	if err != nil || len(assets) != 1 {
		t.Fatalf("assets=%v err=%v", assets, err)
	}
	if assets[0].Futures.Balance != "100000" || assets[0].Futures.Scope.AccountID != "FIXTURE_FUND" || assets[0].Futures.ScopeVerified {
		t.Fatal("asset scope or value changed")
	}
	positions, err := DecodePositions(fixtureItems(t, "positions"), testScope())
	if err != nil || len(positions) != 1 {
		t.Fatalf("positions=%v err=%v", positions, err)
	}
	p := positions[0]
	if p.Symbol != "rb2612" || p.Futures.Position != 1 || p.Futures.PositionDate != "2" || p.Futures.InvestUnitID != "" {
		t.Fatal("native position changed")
	}
	if p.CapturedAt != "2026-11-03T21:00:59+08:00" {
		t.Fatal("night capture rewritten to TradingDay")
	}
}

func TestMoneyProtocolTextsPreserved(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/ctp/oc_money_review_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Required struct {
				Value *string `json:"value"`
			} `json:"required"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, c := range fixture.Cases {
		if c.Required.Value == nil {
			continue
		}
		input, _ := json.Marshal(*c.Required.Value)
		var amount Money
		if err := json.Unmarshal(input, &amount); err != nil || string(amount) != *c.Required.Value {
			t.Fatalf("money text altered: %v", err)
		}
		output, err := json.Marshal(amount)
		if err != nil || !bytes.Equal(input, output) {
			t.Fatal("money roundtrip changed")
		}
		checked++
	}
	if checked != 20 {
		t.Fatalf("expected 20 valid bit-pattern text cases, got %d", checked)
	}
	for _, input := range []string{`null`, `0`, `true`, `"1e3"`, `"01"`, `"+1"`, `"NaN"`, `"1\n"`, `"` + strings.Repeat("1", 344) + `"`} {
		var amount Money
		if json.Unmarshal([]byte(input), &amount) == nil {
			t.Fatalf("bad money accepted: %s", input)
		}
	}
}

func TestStrictCountsNeverCoerce(t *testing.T) {
	for _, value := range []string{"1.0", "1e0", "true", "null", `"1"`, "-1", "2147483648"} {
		raw := mutate(t, "positions", func(items []map[string]any) []map[string]any {
			items[0]["futures"].(map[string]any)["position"] = json.RawMessage(value)
			return items
		})
		if result, err := DecodePositions(raw, testScope()); err == nil || result != nil {
			t.Fatalf("invalid count %s yielded partial/success", value)
		}
	}
	for _, input := range []string{"0", "2147483647"} {
		var count Count
		if err := json.Unmarshal([]byte(input), &count); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequiredFieldsAndExplicitNull(t *testing.T) {
	for _, name := range []string{"asset", "positions"} {
		base := fixtureItems(t, name)
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(base, &items); err != nil {
			t.Fatal(err)
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(items[0]["futures"], &nested); err != nil {
			t.Fatal(err)
		}
		for key := range nested {
			for _, absent := range []bool{true, false} {
				raw := mutate(t, name, func(rows []map[string]any) []map[string]any {
					f := rows[0]["futures"].(map[string]any)
					if absent {
						delete(f, key)
					} else {
						f[key] = nil
					}
					return rows
				})
				if name == "asset" {
					if result, err := DecodeAssets(raw, testScope()); err == nil || result != nil {
						t.Fatalf("asset %s absent/null accepted", key)
					}
				} else if result, err := DecodePositions(raw, testScope()); err == nil || result != nil {
					t.Fatalf("position %s absent/null accepted", key)
				}
			}
		}
	}
}

func TestExactMembersAndDuplicateJSONRejected(t *testing.T) {
	base := string(fixtureItems(t, "positions"))
	inputs := []string{
		strings.Replace(base, `"position": 1`, `"position": 1, "position": 2`, 1),
		strings.Replace(base, `"position": 1`, `"position": 1, "\u0070osition": 2`, 1),
		strings.Replace(base, `"position": 1`, `"Position": 1`, 1),
		strings.Replace(base, `"position": 1`, `"position": 1, "unexpected": 0`, 1),
		base + " null", "null", "{}", "[null]", "[{}]", "[NaN]",
	}
	for _, input := range inputs {
		if input == base {
			t.Fatal("mutation failed")
		}
		if result, err := DecodePositions([]byte(input), testScope()); err == nil || result != nil {
			t.Fatalf("invalid JSON accepted: %s", input)
		}
	}
}

func TestEmptyAndLateInvalidRowsNeverProducePartialResult(t *testing.T) {
	if result, err := DecodePositions([]byte("[]"), testScope()); err != nil || result == nil || len(result) != 0 {
		t.Fatal("complete empty positions must be a non-null empty slice")
	}
	if result, err := DecodeAssets([]byte("[]"), testScope()); err == nil || result != nil {
		t.Fatal("empty funds must fail")
	}
	for _, name := range []string{"asset", "positions"} {
		raw := mutate(t, name, func(items []map[string]any) []map[string]any {
			return append(items, map[string]any{"account_id": "wrong"})
		})
		if name == "asset" {
			if result, err := DecodeAssets(raw, testScope()); err == nil || result != nil {
				t.Fatal("partial assets escaped")
			}
		} else if result, err := DecodePositions(raw, testScope()); err == nil || result != nil {
			t.Fatal("partial positions escaped")
		}
	}
}

func TestAssetScopesRetainedWithoutSumming(t *testing.T) {
	for _, key := range []string{"account_id", "currency_id", "biz_type", "settlement_id"} {
		raw := mutate(t, "asset", func(items []map[string]any) []map[string]any {
			clone, _ := json.Marshal(items[0])
			var other map[string]any
			json.Unmarshal(clone, &other)
			f := other["futures"].(map[string]any)
			values := map[string]any{"account_id": "OTHER_FUND", "currency_id": "USD", "biz_type": "2", "settlement_id": 2}
			f["scope"].(map[string]any)[key] = values[key]
			f["balance"] = "-0.00000000000000001"
			return append(items, other)
		})
		result, err := DecodeAssets(raw, testScope())
		if err != nil || len(result) != 2 || result[1].Futures.Balance != "-0.00000000000000001" {
			t.Fatalf("scope %s lost: %v", key, err)
		}
	}
}

func TestAssetBizTypeMissingOrNULNeverDefaultsOrPublishesPartial(t *testing.T) {
	cases := []struct {
		name    string
		value   any
		missing bool
	}{
		{name: "missing", missing: true},
		{name: "null", value: nil},
		{name: "empty", value: ""},
		{name: "nul", value: "\x00"},
		{name: "numeric_zero", value: 0},
		{name: "numeric_one", value: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mutate(t, "asset", func(items []map[string]any) []map[string]any {
				encoded, err := json.Marshal(items[0])
				if err != nil {
					t.Fatal(err)
				}
				var other map[string]any
				if err := json.Unmarshal(encoded, &other); err != nil {
					t.Fatal(err)
				}
				scope := other["futures"].(map[string]any)["scope"].(map[string]any)
				scope["account_id"] = "OTHER_FUND"
				if tc.missing {
					delete(scope, "biz_type")
				} else {
					scope["biz_type"] = tc.value
				}
				return append(items, other)
			})
			original := append([]byte(nil), raw...)
			if result, err := DecodeAssets(raw, testScope()); err == nil || result != nil {
				t.Fatal("unknown BizType defaulted or preceding valid row escaped")
			}
			if !bytes.Equal(raw, original) {
				t.Fatal("original evidence mutated")
			}
		})
	}
}

func TestAllNativePositionDimensions(t *testing.T) {
	for _, direction := range []string{"1", "2", "3"} {
		for _, hedge := range []string{"1", "2", "3", "5", "6", "7"} {
			for _, date := range []string{"1", "2"} {
				raw := mutate(t, "positions", func(items []map[string]any) []map[string]any {
					p := items[0]["futures"].(map[string]any)
					p["posi_direction"], p["hedge_flag"], p["position_date"] = direction, hedge, date
					p["today_position"], p["yd_position"] = 5, 9 // Do not infer Position = today + yesterday.
					return items
				})
				result, err := DecodePositions(raw, testScope())
				if err != nil || result[0].Futures.HedgeFlag != hedge || result[0].Futures.Position != 1 {
					t.Fatalf("native bucket lost: %v", err)
				}
			}
		}
	}
	for key, value := range map[string]any{"posi_direction": "4", "hedge_flag": "4", "position_date": "3", "invest_unit_id": " unit-1 "} {
		raw := mutate(t, "positions", func(items []map[string]any) []map[string]any {
			items[0]["futures"].(map[string]any)[key] = value
			return items
		})
		if _, err := DecodePositions(raw, testScope()); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
	}
}

func TestDuplicateBusinessKeysRejected(t *testing.T) {
	for _, name := range []string{"asset", "positions"} {
		raw := mutate(t, name, func(items []map[string]any) []map[string]any { return append(items, items[0]) })
		if name == "asset" {
			if _, err := DecodeAssets(raw, testScope()); err == nil {
				t.Fatal("duplicate fund scope accepted")
			}
		} else if _, err := DecodePositions(raw, testScope()); err == nil {
			t.Fatal("duplicate position bucket accepted")
		}
	}
}

func TestScopeTimeAndSourceCodesValidated(t *testing.T) {
	for _, scope := range []SnapshotScope{
		{AccountID: "990000000002", TradeDate: "20261104", NativeBrokerID: "4900"},
		{AccountID: "990000000001", TradeDate: "20261103", NativeBrokerID: "4900"},
		{AccountID: "990000000001", TradeDate: "20261104", NativeBrokerID: "4901"},
		{AccountID: "990000000001", TradeDate: "20260230", NativeBrokerID: "4900"},
	} {
		if _, err := DecodeAssets(fixtureItems(t, "asset"), scope); err == nil {
			t.Fatal("mismatched asset context accepted")
		}
	}
	for key, value := range map[string]any{"captured_at": "2026-11-03T13:00:00Z", "account_id": "990000000002", "symbol": strings.Repeat("x", 81), "exchange": " SHFE"} {
		raw := mutate(t, "positions", func(items []map[string]any) []map[string]any { items[0][key] = value; return items })
		if _, err := DecodePositions(raw, testScope()); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
	}
	if validTime("2026-02-30T09:00:00+08:00") || validTime("2026-11-03T21:00:00.0000000001+08:00") {
		t.Fatal("invalid time accepted")
	}
	if !validTime("2026-11-03T21:00:00.000000001+08:00") {
		t.Fatal("nanosecond source time rejected")
	}
}

func TestInputLimitsAndInvalidUTF8(t *testing.T) {
	for _, raw := range [][]byte{
		bytes.Repeat([]byte(" "), maxSnapshotBytes+1),
		[]byte(strings.Repeat("[", 66) + strings.Repeat("]", 66)),
		[]byte{'[', '"', 0xff, '"', ']'},
		[]byte("[" + strings.Repeat("{},", maxSnapshotRows) + "{}]"),
	} {
		if result, err := DecodePositions(raw, testScope()); err == nil || result != nil {
			t.Fatal("unbounded/malformed snapshot accepted")
		}
	}
}

func TestDecodedSnapshotsAreIndependent(t *testing.T) {
	raw := fixtureItems(t, "positions")
	before := bytes.Clone(raw)
	a, err := DecodePositions(raw, testScope())
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodePositions(raw, testScope())
	if err != nil {
		t.Fatal(err)
	}
	a[0].Futures.Position = 77
	if b[0].Futures.Position != 1 || !bytes.Equal(raw, before) {
		t.Fatal("decoder shared mutable data or changed raw input")
	}
}

func FuzzSnapshotDecoders(f *testing.F) {
	f.Add(fixtureItems(f, "asset"))
	f.Add(fixtureItems(f, "positions"))
	for _, raw := range []string{"[]", "null", "[null]", "[{}]", `[{"a":1,"a":2}]`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if result, err := DecodeAssets(raw, testScope()); err != nil && result != nil {
			t.Fatal("invalid asset input yielded partial rows")
		}
		if result, err := DecodePositions(raw, testScope()); err != nil && result != nil {
			t.Fatal("invalid position input yielded partial rows")
		}
	})
}
