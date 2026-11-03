// Package ctpcontract checks offline draft fixtures, not a deployed CTP adapter.
package ctpcontract

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/credentials"
	"ti-relay-trader/internal/redisstream"
)

var update = flag.Bool("update-ctp-vectors", false, "regenerate public synthetic CTP credential vectors only")

const fixtureRoot = "../../testdata/ctp"
const account = "990000000001"
const prefix = "relay:test:v1:ctp_4900:" + account

type credentialVector struct {
	Warning    string               `json:"warning"`
	KeyHex     string               `json:"key_hex"`
	AAD        string               `json:"aad_utf8"`
	AADHex     string               `json:"aad_hex"`
	Plaintext  string               `json:"plaintext_utf8"`
	VersionKey string               `json:"version_key"`
	CurrentKey string               `json:"current_key"`
	Envelope   credentials.Envelope `json:"envelope"`
}

func readFixture(t *testing.T, name string, dst any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureRoot, name))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		t.Fatal(err)
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCredentialVector(t *testing.T) {
	// Deliberately public deterministic test inputs. Never use this key/nonce in a service.
	key, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	env := credentials.Envelope{Protocol: credentials.Protocol, Algorithm: credentials.Algorithm,
		Environment: "test", BrokerID: "ctp_4900", AccountID: account, CredentialVersion: 1, KeyID: "ctp-public-fixture-only",
		IssuedAt: "2026-11-03T12:00:00+08:00"}
	aad := credentials.AdditionalAuthenticatedData(env.Environment, env.BrokerID, env.AccountID, env.CredentialVersion, env.KeyID)
	plain := `{"credential_type":"ctp","credential_schema":"ctp.login.v1","broker_id":"4900","investor_id":"FIXTURE_INV","user_id":"FIXTURE_USER","password":"NOT-A-REAL-PASSWORD","app_id":"FIXTURE_APP","auth_code":"NOT-A-REAL-AUTH"}`
	nonce := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	sealed := gcm.Seal(nil, nonce, []byte(plain), []byte(aad))
	env.NonceB64 = base64.StdEncoding.EncodeToString(nonce)
	env.CiphertextB64 = base64.StdEncoding.EncodeToString(sealed[:len(sealed)-gcm.Overhead()])
	env.TagB64 = base64.StdEncoding.EncodeToString(sealed[len(sealed)-gcm.Overhead():])
	versionKey, err := credentials.VersionKey("test", "ctp_4900", account, 1)
	if err != nil {
		t.Fatal(err)
	}
	currentKey, err := credentials.CurrentKey("test", "ctp_4900", account)
	if err != nil {
		t.Fatal(err)
	}
	expected := credentialVector{"PUBLIC SYNTHETIC VECTOR; NEVER DEPLOY KEY OR NONCE", hex.EncodeToString(key), aad, hex.EncodeToString([]byte(aad)), plain, versionKey, currentKey, env}
	if *update {
		b, err := json.MarshalIndent(expected, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixtureRoot, "credentials.json"), append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var v credentialVector
	readFixture(t, "credentials.json", &v)
	if v != expected {
		t.Fatal("credential golden differs; inspect contract changes before regenerating")
	}
	if err := v.Envelope.Validate("test", "ctp_4900", account, 1, env.KeyID); err != nil {
		t.Fatal(err)
	}
	ciphertext := append(mustB64(t, v.Envelope.CiphertextB64), mustB64(t, v.Envelope.TagB64)...)
	decoded, err := gcm.Open(nil, mustB64(t, v.Envelope.NonceB64), ciphertext, []byte(v.AAD))
	if err != nil || string(decoded) != plain {
		t.Fatalf("positive vector did not decrypt: %v", err)
	}
	for name, wrongAAD := range map[string]string{
		"environment": strings.Replace(aad, "|test|", "|prod|", 1),
		"broker":      strings.Replace(aad, "|ctp_4900|", "|ctp_other|", 1),
		"account":     strings.Replace(aad, account, "990000000002", 1),
		"version":     strings.Replace(aad, "|1|", "|2|", 1),
		"key_id":      aad + "-wrong",
		"newline":     aad + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := gcm.Open(nil, nonce, ciphertext, []byte(wrongAAD)); err == nil {
				t.Fatal("changed AAD accepted")
			}
		})
	}
	for _, index := range []int{0, len(ciphertext) - 1} {
		bad := append([]byte(nil), ciphertext...)
		bad[index] ^= 1
		if _, err := gcm.Open(nil, nonce, bad, []byte(aad)); err == nil {
			t.Fatal("tampered ciphertext/tag accepted")
		}
	}
	wrongNonce := append([]byte(nil), nonce...)
	wrongNonce[0] ^= 1
	if _, err := gcm.Open(nil, wrongNonce, ciphertext, []byte(aad)); err == nil {
		t.Fatal("wrong nonce accepted")
	}
	wrongKey := append([]byte(nil), key...)
	wrongKey[0] ^= 1
	wb, _ := aes.NewCipher(wrongKey)
	wg, _ := cipher.NewGCM(wb)
	if _, err := wg.Open(nil, nonce, ciphertext, []byte(aad)); err == nil {
		t.Fatal("wrong key accepted")
	}
	// This is an envelope vector, not an implementation of the CTP credential manager.
	if _, err := credentials.Decrypt(env, credentials.KeyMaterial{ID: env.KeyID, Key: key}); err == nil {
		t.Fatal("CTP plaintext unexpectedly accepted by Huaxin decoder")
	}
}

type messages struct {
	ContractVersion string `json:"contract_version"`
	SyntheticOnly   bool   `json:"synthetic_only"`
	Cases           []struct {
		Name   string          `json:"name"`
		Suffix string          `json:"stream_suffix"`
		Body   json.RawMessage `json:"body"`
	} `json:"cases"`
}

func object(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", v)
	}
	return m
}

func TestMessageFixtures(t *testing.T) {
	var fixtures messages
	readFixture(t, "messages.json", &fixtures)
	if !fixtures.SyntheticOnly || fixtures.ContractVersion != "ctp.f0.draft.1" || len(fixtures.Cases) != 10 {
		t.Fatal("unexpected fixture catalogue")
	}
	names := map[string]bool{}
	for _, c := range fixtures.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if names[c.Name] {
				t.Fatal("duplicate fixture")
			}
			names[c.Name] = true
			env, err := redisstream.DecodeEntry(prefix+":"+c.Suffix, "1793710860000-0", map[string]any{"body": string(c.Body)})
			if err != nil {
				t.Fatal(err)
			}
			if env.Protocol != redisstream.Protocol || env.Routing.Env != "test" || env.Routing.BrokerID != "ctp_4900" || env.Routing.GatewayID != account {
				t.Fatal("route/protocol mismatch")
			}
			var body map[string]any
			d := json.NewDecoder(bytes.NewReader(c.Body))
			d.UseNumber()
			if err := d.Decode(&body); err != nil {
				t.Fatal(err)
			}
			p := object(t, body["payload"])
			if c.Suffix == "cmd.trade" {
				if _, ok := body["routing"]; ok {
					t.Fatal("commands route by stream, not invented routing field")
				}
				if p["account_id"] != account {
					t.Fatal("wrong command account")
				}
				stream, err := redisstream.CommandStreamForAction(redisstream.NewStreams(prefix), env.Action)
				if err != nil || stream != prefix+":cmd.trade" {
					t.Fatal("wrong action stream")
				}
				if c.Name == "submit" {
					assertOrder(t, p)
				}
				if c.Name == "batch" {
					orders := p["orders"].([]any)
					if len(orders) != 2 {
						t.Fatal("missing children")
					}
					ids := map[any]bool{}
					for _, o := range orders {
						v := object(t, o)
						assertOrder(t, v)
						if ids[v["gateway_order_id"]] {
							t.Fatal("child collision")
						}
						ids[v["gateway_order_id"]] = true
					}
				}
			} else if env.Routing.AccountID != account {
				t.Fatal("missing response account")
			}
			if c.Name == "accepted" {
				if env.Status != "accepted" || env.ResultType != "order_action_receipt" {
					t.Fatal("receipt changed")
				}
				if _, ok := body["chunk"]; ok {
					t.Fatal("trading command has query final chunk")
				}
			}
			if c.Name == "order" || c.Name == "fill" {
				if _, ok := p["order_id"]; ok {
					t.Fatal("CTP must not invent numeric order_id")
				}
				ids := object(t, p["native_ids"])
				if ids["order_ref"] != "000000000001" || ids["order_sys_id"] != "00001234" {
					t.Fatal("string identity lost")
				}
				if p["trade_date"] != "20261104" {
					t.Fatal("night session misdated")
				}
				field := "submitted_at"
				if c.Name == "fill" {
					field = "matched_at"
					if p["fee"] != nil || p["fee_complete"] != false {
						t.Fatal("unknown fee fabricated")
					}
				}
				at, err := time.Parse(time.RFC3339, p[field].(string))
				if err != nil || at.Format("20060102") != "20261103" {
					t.Fatal("source date replaced by TradingDay")
				}
			}
			if c.Name == "asset" || c.Name == "positions" {
				if p["product_type"] != "futures" {
					t.Fatal("missing product discriminator")
				}
				chunk := object(t, body["chunk"])
				if chunk["is_last"] != true || env.Status != "completed" {
					t.Fatal("incomplete success page")
				}
				if _, ok := p["account"]; ok {
					t.Fatal("futures must not enter equity asset projection")
				}
				row := object(t, p["items"].([]any)[0])
				f := object(t, row["futures"])
				if c.Name == "positions" {
					v, ok := f["invest_unit_id"]
					if !ok || v != "" {
						t.Fatal("explicit empty investment unit lost")
					}
				}
				if c.Name == "asset" {
					scope := object(t, f["scope"])
					if scope["biz_type"] != "1" || scope["account_id"] == account {
						t.Fatal("fund scope lost")
					}
				}
			}
			if c.Name == "queue_expired" {
				evidence := object(t, object(t, body["adapter_context"])["query_evidence"])
				for _, key := range []string{"sdk_request_id", "sdk_return_code", "callback_error_id"} {
					if v, ok := evidence[key]; !ok || v != nil {
						t.Fatal("unsent task fabricated SDK evidence")
					}
				}
				if _, ok := body["chunk"]; ok {
					t.Fatal("failed task fabricated last page")
				}
			}
			if c.Name == "heartbeat_read_only" && (p["accepting_trade_commands"] != false || p["accepting_cancel_commands"] != false) {
				t.Fatal("read-only permits trade")
			}
		})
	}
}

var decimalPrice = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,9})?$`)

func TestDraftPriceLexemes(t *testing.T) {
	for _, value := range []string{"1", "3600", "3600.000000000", "0.000000001"} {
		if !decimalPrice.MatchString(value) || strings.Trim(value, "0.") == "" {
			t.Fatalf("valid decimal rejected: %q", value)
		}
	}
	for _, value := range []string{"", "0", "0.000", "1e3", "1E3", "01", "+1", "-1", " 1", "1 ", ".1", "1.", "0.0000000001", "NaN", "Inf"} {
		if decimalPrice.MatchString(value) && strings.Trim(value, "0.") != "" {
			t.Fatalf("invalid decimal accepted: %q", value)
		}
	}
	// SDK range/tick constraints are separate from this lexical candidate contract.
	for _, value := range []json.Number{"0", "-1", "1.5", "1e2", "9223372036854775808"} {
		if n, err := value.Int64(); err == nil && n > 0 {
			t.Fatalf("invalid lot quantity accepted: %q", value)
		}
	}
}

func assertOrder(t *testing.T, p map[string]any) {
	t.Helper()
	if p["business_type"] != "F" || p["exchange"] != "SHFE" || p["hedge_type"] != "speculation" {
		t.Fatal("wrong product")
	}
	if p["trade_side"] != "B" && p["trade_side"] != "S" {
		t.Fatal("wrong side")
	}
	if p["offset_type"] != "O" && p["offset_type"] != "C" && p["offset_type"] != "CT" && p["offset_type"] != "CY" {
		t.Fatal("missing offset")
	}
	n, ok := p["order_qty"].(json.Number)
	if !ok {
		t.Fatal("qty is not integer number")
	}
	q, err := n.Int64()
	if err != nil || q <= 0 {
		t.Fatal("qty is not positive lots")
	}
	price, ok := p["limit_price"].(string)
	if !ok || !decimalPrice.MatchString(price) || strings.Trim(price, "0.") == "" {
		t.Fatal("not positive plain decimal string")
	}
}

// Length prefixes frame UTF-8 bytes without concatenation or separator ambiguity.
func identityBytes(kind string, parts ...string) []byte {
	var b bytes.Buffer
	b.WriteString("ctp.identity.v1/")
	b.WriteString(kind)
	b.WriteByte('/')
	for _, p := range parts {
		raw := []byte(p)
		n, _ := json.Marshal(len(raw))
		b.Write(n)
		b.WriteByte(':')
		b.Write(raw)
	}
	return b.Bytes()
}
func TestIdentityRules(t *testing.T) {
	// Normalize only ASCII edge space for the three padded order/trade identifiers.
	normalize := func(s string) string { return strings.Trim(s, " ") }
	if normalize("  00001234  ") != "00001234" || normalize("\t0001") != "\t0001" {
		t.Fatal("padding rule drift")
	}
	parts := []string{"test", "ctp_4900", account, account, "20261104", "SHFE", "00001234"}
	raw := identityBytes("order", parts...)
	const expected = "ctp.identity.v1/order/4:test8:ctp_490012:99000000000112:9900000000018:202611044:SHFE8:00001234"
	if string(raw) != expected {
		t.Fatal("identity byte contract drift")
	}
	base := sha256.Sum256(raw)
	for i := range parts {
		changed := append([]string(nil), parts...)
		changed[i] += "x"
		if sha256.Sum256(identityBytes("order", changed...)) == base {
			t.Fatal("namespace collision")
		}
	}
	if bytes.Equal(identityBytes("fill", "a|b", "c"), identityBytes("fill", "a", "b|c")) {
		t.Fatal("ambiguous parts")
	}
	if bytes.Equal(identityBytes("fill", "001"), identityBytes("fill", "1")) {
		t.Fatal("leading zero collapsed")
	}
	if bytes.Equal(identityBytes("fill", "rb2612"), identityBytes("fill", "RB2612")) {
		t.Fatal("case collapsed")
	}
	long := strings.Repeat("x", 80)
	if !bytes.Contains(identityBytes("order", long), []byte(long)) {
		t.Fatal("long ID truncated")
	}
}

func TestQueryApplicationTruthTable(t *testing.T) {
	var f struct {
		ContractVersion string `json:"contract_version"`
		SyntheticOnly   bool   `json:"synthetic_only"`
		Cases           []struct {
			Name      string `json:"name"`
			Ticket    bool   `json:"ticket_matches"`
			Context   bool   `json:"context_matches"`
			Submitted bool   `json:"sdk_submitted"`
			Raw       bool   `json:"raw_completed"`
			Applied   bool   `json:"typed_applied"`
			Pages     bool   `json:"pages_complete"`
			Terminals int    `json:"terminal_count"`
			Replace   bool   `json:"expected_replace"`
		} `json:"cases"`
	}
	readFixture(t, "query_cases.json", &f)
	if !f.SyntheticOnly || f.ContractVersion != "ctp.f0.draft.1" || len(f.Cases) != 10 {
		t.Fatal("wrong query catalogue")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			replace := c.Ticket && c.Context && c.Submitted && c.Raw && c.Applied && c.Pages && c.Terminals == 1
			if replace != c.Replace {
				t.Fatal("unsafe snapshot truth table")
			}
		})
	}
}
