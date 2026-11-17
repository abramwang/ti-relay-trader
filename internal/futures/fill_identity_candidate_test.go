package futures

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Independent reference encoder for the original, byte-preserved shared vectors.
func candidateFillIdentity(parts []string) (string, string) {
	var b strings.Builder
	b.WriteString("ctp.identity.v2/fill/")
	for _, p := range parts {
		fmt.Fprintf(&b, "%d:%s", len([]byte(p)), p)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return b.String(), "ctp-fill-v2-" + hex.EncodeToString(sum[:])
}

func TestOCReportedFillCollisionAndV2Candidate(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/fixtures/ctp_fill_identity_v2_candidate_20261117.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Status  string            `json:"status"`
		Legacy  string            `json:"legacy_collision_id"`
		Mapping map[string]string `json:"direction_mapping"`
		Cases   []struct {
			Name      string   `json:"name"`
			Parts     []string `json:"parts"`
			Direction string   `json:"native_direction"`
			Encoded   string   `json:"encoded"`
			ID        string   `json:"fill_id"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &f); err != nil || f.Status != "candidate_not_runtime_enabled" || len(f.Cases) != 3 {
		t.Fatal("candidate scope", err)
	}
	seen := map[string]bool{}
	for i, c := range f.Cases {
		if len(c.Parts) != 9 || f.Mapping[c.Direction] != c.Parts[8] {
			t.Fatal("tuple or direction", c.Name)
		}
		encoded, id := candidateFillIdentity(c.Parts)
		if encoded != c.Encoded || id != c.ID || seen[id] {
			t.Fatal("candidate bytes/hash/separation", c.Name)
		}
		seen[id] = true
		s := TradeScope{Route: QueryRoute{Env: c.Parts[0], BrokerID: c.Parts[1], AccountID: c.Parts[2], GatewayID: c.Parts[3]}, TradeDate: c.Parts[4]}
		if nativeFillIdentityV2(s, c.Parts[5], c.Parts[6], c.Parts[7], c.Parts[8]) != id {
			t.Fatal("runtime encoder differs from agreed vector")
		}
		if i < 2 && nativeIdentity("fill", s, c.Parts[5], c.Parts[6]) != f.Legacy {
			t.Fatal("reported v1 collision did not reproduce")
		}
		// Every declared identity component must participate in the hash.
		for k := range c.Parts {
			changed := append([]string(nil), c.Parts...)
			changed[k] += "x"
			if _, other := candidateFillIdentity(changed); other == id {
				t.Fatal("missing dimension", k)
			}
		}
	}
	// OC has confirmed all nine parts. Original vectors retain their historical
	// candidate label; enabling reception does not migrate any existing ledger.
	scope := tradeScope()
	row := tradeFixture(t, "fill")
	changed := editBody(t, row, func(m map[string]any) {
		ids := m["native_ids"].(map[string]any)
		_, m["fill_id"] = candidateFillIdentity([]string{scope.Route.Env, scope.Route.BrokerID, scope.Route.AccountID, scope.Route.GatewayID, scope.TradeDate, m["exchange"].(string), ids["trade_id"].(string), ids["order_sys_id"].(string), m["trade_side"].(string)})
	})
	if _, err := DecodeFill(changed, scope); err != nil {
		t.Fatal("agreed SHFE v2 row rejected", err)
	}
}
