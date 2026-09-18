package credentials

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"
)

func TestEnvelopeRoundTripAndIdentityAuthentication(t *testing.T) {
	key, err := ParseKeyMaterial("hx-test-202609", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := BrokerCredentials{
		BrokerLoginUser: "broker-user",
		BrokerPassword:  "broker-password",
		DynamicPassword: "123456",
	}
	envelope, err := Encrypt(
		"test",
		"huaxin",
		"00030484",
		3,
		key,
		plaintext,
		time.Date(2026, 9, 18, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
		bytes.NewReader([]byte("0123456789ab")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Protocol != Protocol || envelope.Algorithm != Algorithm ||
		envelope.Environment != "test" || envelope.CredentialVersion != 3 ||
		envelope.NonceB64 != base64.StdEncoding.EncodeToString([]byte("0123456789ab")) ||
		envelope.IssuedAt != "2026-09-18T10:00:00+08:00" {
		t.Fatalf("envelope = %#v", envelope)
	}
	decoded, err := Decrypt(envelope, key)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != plaintext {
		t.Fatalf("decoded = %#v, want %#v", decoded, plaintext)
	}

	tampered := envelope
	tampered.AccountID = "00030485"
	if _, err := Decrypt(tampered, key); err == nil {
		t.Fatal("tampered identity decrypted successfully")
	}
	tampered = envelope
	tampered.CiphertextB64 = base64.StdEncoding.EncodeToString([]byte("tampered"))
	if _, err := Decrypt(tampered, key); err == nil {
		t.Fatal("tampered ciphertext decrypted successfully")
	}
}

func TestAESGCMCompatibilityVector(t *testing.T) {
	key, err := ParseKeyMaterial("hx-test-vector-1", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Encrypt("test", "huaxin", "00030484", 7, key, BrokerCredentials{
		BrokerLoginUser: "demo-user", BrokerPassword: "demo-password", DynamicPassword: "123456",
	}, time.Date(2026, 9, 18, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60)), bytes.NewReader([]byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.NonceB64 != "AAECAwQFBgcICQoL" ||
		envelope.CiphertextB64 != "PCC0aaqOp2nSLfjs2IcnGPCz9RbKWTsZVQjI8G4McpAtMsyOwKp36ivUHp778EdKintarz6zzrUS50tqa5SanJQe6li3qEgAcT3JMZ/ufZtP5bsGVFlxWc/OqvlO0sQ=" ||
		envelope.TagB64 != "6+hjilk8OXUKgZiV/1RLAw==" {
		t.Fatalf("compatibility vector mismatch: %#v", envelope)
	}
}

func TestCredentialValidationRejectsUnsafeIdentityAndMissingPlaintext(t *testing.T) {
	if _, err := ParseKeyMaterial("bad key", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"); err == nil {
		t.Fatal("unsafe key id accepted")
	}
	if err := ValidateIdentity("prod", "huaxin", "50100011407701", 1, "hx-prod-202609"); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}
	if err := ValidateIdentity("production", "huaxin", "501000114077", 1, "hx-prod-202609"); err == nil {
		t.Fatal("non-protocol environment accepted")
	}
	if err := ValidateBrokerCredentials(BrokerCredentials{BrokerLoginUser: "user"}); err == nil {
		t.Fatal("missing broker password accepted")
	}
}
