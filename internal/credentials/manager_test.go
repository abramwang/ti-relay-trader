package credentials

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeSecretStore struct {
	values map[string]string
	locks  map[string]string
	writes []string
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{values: map[string]string{}, locks: map[string]string{}}
}

func (store *fakeSecretStore) Get(_ context.Context, key string) (string, error) {
	value, ok := store.values[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return value, nil
}

func (store *fakeSecretStore) SetIfAbsent(_ context.Context, key, value string) (bool, error) {
	if _, ok := store.values[key]; ok {
		return false, nil
	}
	store.values[key] = value
	store.writes = append(store.writes, key)
	return true, nil
}

func (store *fakeSecretStore) AcquireLock(_ context.Context, key, token string, _ time.Duration) (bool, error) {
	if _, ok := store.locks[key]; ok {
		return false, nil
	}
	store.locks[key] = token
	return true, nil
}

func (store *fakeSecretStore) SetIfLockOwner(_ context.Context, lockKey, token, key, value string) (bool, error) {
	if store.locks[lockKey] != token {
		return false, nil
	}
	store.values[key] = value
	store.writes = append(store.writes, key)
	return true, nil
}

func (store *fakeSecretStore) DeleteIfLockOwner(_ context.Context, lockKey, token, key string) (bool, error) {
	if store.locks[lockKey] != token {
		return false, nil
	}
	delete(store.values, key)
	store.writes = append(store.writes, "delete:"+key)
	return true, nil
}

func (store *fakeSecretStore) ReleaseLock(_ context.Context, key, token string) error {
	if store.locks[key] == token {
		delete(store.locks, key)
	}
	return nil
}

func (store *fakeSecretStore) Close() error { return nil }

type fakeAuditStore struct {
	events []AuditEvent
	err    error
}

func (store *fakeAuditStore) AppendCredentialAudit(_ context.Context, event AuditEvent) error {
	if store.err != nil {
		return store.err
	}
	store.events = append(store.events, event)
	return nil
}

func TestManagerRotatesVersionBeforeCurrentAndAudits(t *testing.T) {
	secretStore := newFakeSecretStore()
	auditStore := &fakeAuditStore{}
	key, _ := ParseKeyMaterial("hx-test-202609", strings.Repeat("01", 32))
	random := strings.NewReader(strings.Repeat("abcdefghijklmnop", 8))
	manager, err := NewManager(ManagerOptions{
		Store: secretStore, Audit: auditStore, Environment: "test", BrokerID: "huaxin", Key: key,
		Now: func() time.Time {
			return time.Date(2026, 9, 18, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
		},
		Random: random,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Rotate(context.Background(), "00030484", "relay-admin", BrokerCredentials{
		BrokerLoginUser: "user", BrokerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CredentialVersion != 1 || !result.RestartRequired || len(result.EnvelopeSHA256) != 64 {
		t.Fatalf("rotation result = %#v", result)
	}
	versionKey, _ := VersionKey("test", "huaxin", "00030484", 1)
	currentKey, _ := CurrentKey("test", "huaxin", "00030484")
	if len(secretStore.writes) != 2 || secretStore.writes[0] != versionKey || secretStore.writes[1] != currentKey {
		t.Fatalf("writes = %#v", secretStore.writes)
	}
	if secretStore.values[currentKey] != "1" {
		t.Fatalf("current = %q", secretStore.values[currentKey])
	}
	if strings.Contains(secretStore.values[versionKey], "password") || strings.Contains(secretStore.values[versionKey], "user") {
		t.Fatal("credential envelope contains plaintext")
	}
	if len(auditStore.events) != 2 || auditStore.events[0].Status != "started" || auditStore.events[1].Status != "succeeded" {
		t.Fatalf("audit events = %#v", auditStore.events)
	}
	status, err := manager.Status(context.Background(), "00030484", true)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || !status.DecryptionVerified || status.CredentialVersion != 1 {
		t.Fatalf("status = %#v", status)
	}
}

func TestManagerSkipsOrphanVersionAndDisablesOnlyCurrentPointer(t *testing.T) {
	secretStore := newFakeSecretStore()
	auditStore := &fakeAuditStore{}
	versionOne, _ := VersionKey("prod", "huaxin", "501000114077", 1)
	secretStore.values[versionOne] = `{"orphan":true}`
	key, _ := ParseKeyMaterial("hx-prod-202609", strings.Repeat("02", 32))
	manager, err := NewManager(ManagerOptions{
		Store: secretStore, Audit: auditStore, Environment: "prod", BrokerID: "huaxin", Key: key,
		Now:    func() time.Time { return time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC) },
		Random: strings.NewReader(strings.Repeat("qrstuvwxyzABCDEF", 8)),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Rotate(context.Background(), "501000114077", "relay-admin", BrokerCredentials{
		BrokerLoginUser: "user", BrokerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CredentialVersion != 2 {
		t.Fatalf("version = %d, want 2", result.CredentialVersion)
	}
	disabled, err := manager.Disable(context.Background(), "501000114077", "relay-admin")
	if err != nil {
		t.Fatal(err)
	}
	if !disabled.Disabled || !disabled.OCStopRequired || disabled.CredentialVersion != 2 {
		t.Fatalf("disabled = %#v", disabled)
	}
	currentKey, _ := CurrentKey("prod", "huaxin", "501000114077")
	if _, ok := secretStore.values[currentKey]; ok {
		t.Fatal("current pointer still exists")
	}
	versionTwo, _ := VersionKey("prod", "huaxin", "501000114077", 2)
	if _, ok := secretStore.values[versionOne]; !ok {
		t.Fatal("orphan version was deleted")
	}
	if _, ok := secretStore.values[versionTwo]; !ok {
		t.Fatal("active version was deleted")
	}
}

func TestManagerRequiresAuditBeforeWriting(t *testing.T) {
	secretStore := newFakeSecretStore()
	key, _ := ParseKeyMaterial("hx-test-202609", strings.Repeat("03", 32))
	manager, err := NewManager(ManagerOptions{
		Store: secretStore, Audit: &fakeAuditStore{err: errors.New("audit unavailable")},
		Environment: "test", BrokerID: "huaxin", Key: key,
		Random: strings.NewReader(strings.Repeat("0123456789abcdef", 4)),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Rotate(context.Background(), "00030484", "relay-admin", BrokerCredentials{
		BrokerLoginUser: "user", BrokerPassword: "password",
	})
	if err == nil {
		t.Fatal("rotation succeeded without audit")
	}
	if len(secretStore.values) != 0 {
		t.Fatalf("credential store changed despite audit failure: %#v", secretStore.values)
	}
}

func TestLoadKeyMaterialUsesEnvironmentSpecificVariables(t *testing.T) {
	values := map[string]string{
		TestKeyIDEnv:  "hx-test-202609",
		TestKeyHexEnv: strings.Repeat("04", 32),
	}
	key, err := LoadKeyMaterialWithLookup("test", func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != "hx-test-202609" || len(key.Key) != 32 {
		t.Fatalf("key = %#v", key)
	}
	if _, err := LoadKeyMaterialWithLookup("prod", func(name string) string { return values[name] }); err == nil {
		t.Fatal("missing production key variables accepted")
	}
}
