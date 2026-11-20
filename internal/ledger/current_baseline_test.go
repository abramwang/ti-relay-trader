package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func currentBaselineFixture(n int) CurrentBaselineCandidate {
	gen := int64(1)
	c := CurrentBaselineCandidate{
		Scope:        CurrentBaselineScope{"test", "acct-1", "huaxin", "gateway-1", "20261119", "session-1", &gen},
		CollectionID: "collection-1", StartedAt: time.Date(2026, 11, 19, 1, 30, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, 11, 19, 1, 30, 4, 0, time.UTC),
		Cash:        json.RawMessage(`{"cash_available":"123.45","frozen_cash":null}`),
		Queries:     json.RawMessage(`[{"action":"account.positions.query","status":"completed","last_page_seen":true}]`),
		Resources:   map[string][]CurrentBaselineRecord{},
	}
	for i := n - 1; i >= 0; i-- {
		key := fmt.Sprintf("%06d.SH", i)
		c.Resources["positions"] = append(c.Resources["positions"], CurrentBaselineRecord{key, json.RawMessage(fmt.Sprintf(`{"security_id":%q,"quantity":100,"sellable_qty":null,"frozen_qty":null}`, key))})
	}
	return c
}

func TestCurrentAccountBaselineCanonicalJSON(t *testing.T) {
	raw := []byte(`{"z":null,"arr":[-0,-2,true,false,{"b":"1.2300","a":"\ud83d\ude00"}],"a":"\u4e2d<&/\u2028\u2029\n\t\u0000\\u2028"}`)
	got, err := currentBaselineJSON(raw)
	// Independent Python json.dumps(..., ensure_ascii=False, sort_keys=True,
	// separators=(",", ":"), allow_nan=False) digest, including literal U+2028/9.
	if err != nil || baselineHash(got) != "3f48a6270a936e8f8547f3301aa28c5e7e3606e0898dcc42c26587a8497c4d6d" {
		t.Fatalf("%s %v", got, err)
	}
	again, err := currentBaselineJSON(got)
	if err != nil || string(again) != string(got) {
		t.Fatal("non-idempotent canonical encoding", err)
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":1.2}`, `[1e3]`, `[9223372036854775808]`, `{"a":"\ud800"}`, `{"a":"\udc00"}`, `{"a":"\ud800x"}`, `{} {}`, string([]byte{'"', 0xff, '"'}), strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)} {
		if _, err := currentBaselineJSON([]byte(raw)); !errors.Is(err, ErrCurrentBaselineInput) {
			t.Fatalf("accepted %q: %v", raw, err)
		}
	}
}

func TestCurrentAccountBaselineCandidateValidation(t *testing.T) {
	in := currentBaselineFixture(501)
	before, _ := json.Marshal(in)
	c, hash, err := prepareCurrentBaseline(in)
	if err != nil || len(hash) != 64 || c.Resources["positions"][0].Key != "000000.SH" {
		t.Fatal(hash, err)
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("mutated caller evidence")
	}
	_, again, err := prepareCurrentBaseline(c)
	if err != nil || hash != again {
		t.Fatal("input order/normalization changed identity", err)
	}
	for name, mutate := range map[string]func(*CurrentBaselineCandidate){
		"wrong env":             func(c *CurrentBaselineCandidate) { c.Scope.Environment = "prod" },
		"bad date":              func(c *CurrentBaselineCandidate) { c.Scope.TradeDate = "20260230" },
		"empty account":         func(c *CurrentBaselineCandidate) { c.Scope.AccountID = "" },
		"invalid utf8 identity": func(c *CurrentBaselineCandidate) { c.Scope.AccountID = string([]byte{'a', 0xff}) },
		"missing session":       func(c *CurrentBaselineCandidate) { c.Scope.CounterSessionID = "" },
		"missing generation":    func(c *CurrentBaselineCandidate) { c.Scope.ConnectionGeneration = nil },
		"missing collection":    func(c *CurrentBaselineCandidate) { c.CollectionID = "" },
		"bad time":              func(c *CurrentBaselineCandidate) { c.CompletedAt = c.StartedAt.Add(-time.Second) },
		"number as cash":        func(c *CurrentBaselineCandidate) { c.Cash = json.RawMessage(`1`) },
		"float amount":          func(c *CurrentBaselineCandidate) { c.Cash = json.RawMessage(`{"cash_available":1.2}`) },
		"queries object":        func(c *CurrentBaselineCandidate) { c.Queries = json.RawMessage(`{}`) },
		"unknown resource":      func(c *CurrentBaselineCandidate) { c.Resources["unknown"] = nil },
		"duplicate key": func(c *CurrentBaselineCandidate) {
			c.Resources["positions"] = append(c.Resources["positions"], c.Resources["positions"][0])
		},
		"row not object": func(c *CurrentBaselineCandidate) { c.Resources["positions"][0].Document = json.RawMessage(`[]`) },
		"too many rows": func(c *CurrentBaselineCandidate) {
			c.Resources["positions"] = make([]CurrentBaselineRecord, currentBaselineMaxRows+1)
		},
		"metadata too large": func(c *CurrentBaselineCandidate) { c.Queries = json.RawMessage(strings.Repeat(" ", 4<<20) + "[]") },
	} {
		t.Run(name, func(t *testing.T) {
			c := currentBaselineFixture(1)
			mutate(&c)
			if _, _, err := prepareCurrentBaseline(c); err == nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
	if _, err := NewCurrentBaselineStore(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

func assertCurrentBaselineUnverified(t *testing.T, m CurrentBaselineManifest) {
	t.Helper()
	if m.Completeness == "complete" || m.MissingSecurityMeansZero || m.Consistency.CoverageVerified || m.Consistency.QueryEffectsReconciled || m.Consistency.BrokerAtomic || m.LedgerRevision != nil || m.ValidUntil != nil || !reflect.DeepEqual(m.Consistency, CurrentBaselineConsistency{Level: "relay_published_view"}) || len(m.Limitations) == 0 {
		t.Fatal("candidate upgraded to proof", m)
	}
}
