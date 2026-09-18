package credentials

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/timeutil"
)

const credentialLockTTL = 30 * time.Second

type ManagerOptions struct {
	Store       SecretStore
	Audit       AuditStore
	Environment string
	BrokerID    string
	Key         KeyMaterial
	Now         func() time.Time
	Random      io.Reader
}

type Manager struct {
	store       SecretStore
	audit       AuditStore
	environment string
	brokerID    string
	key         KeyMaterial
	now         func() time.Time
	random      io.Reader
}

type RotationResult struct {
	OperationID       string `json:"operation_id"`
	Environment       string `json:"environment"`
	BrokerID          string `json:"broker_id"`
	AccountID         string `json:"account_id"`
	CredentialVersion int64  `json:"credential_version"`
	KeyID             string `json:"key_id"`
	EnvelopeSHA256    string `json:"envelope_sha256"`
	IssuedAt          string `json:"issued_at"`
	CredentialSource  string `json:"credential_source"`
	RestartRequired   bool   `json:"restart_required"`
}

type CredentialStatus struct {
	Environment        string `json:"environment"`
	BrokerID           string `json:"broker_id"`
	AccountID          string `json:"account_id"`
	Configured         bool   `json:"configured"`
	CredentialVersion  int64  `json:"credential_version,omitempty"`
	KeyID              string `json:"key_id,omitempty"`
	IssuedAt           string `json:"issued_at,omitempty"`
	Protocol           string `json:"protocol,omitempty"`
	Algorithm          string `json:"algorithm,omitempty"`
	EnvelopeSHA256     string `json:"envelope_sha256,omitempty"`
	CredentialSource   string `json:"credential_source,omitempty"`
	DecryptionVerified bool   `json:"decryption_verified"`
}

type DisableResult struct {
	OperationID       string `json:"operation_id"`
	Environment       string `json:"environment"`
	BrokerID          string `json:"broker_id"`
	AccountID         string `json:"account_id"`
	CredentialVersion int64  `json:"credential_version,omitempty"`
	Disabled          bool   `json:"disabled"`
	OCStopRequired    bool   `json:"oc_stop_required"`
}

func NewManager(options ManagerOptions) (*Manager, error) {
	if options.Store == nil {
		return nil, errors.New("credential secret store is required")
	}
	if options.Environment != "test" && options.Environment != "prod" {
		return nil, errors.New("credential environment must be test or prod")
	}
	if strings.TrimSpace(options.BrokerID) == "" {
		return nil, errors.New("credential broker id is required")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().In(timeutil.Location()) }
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Manager{
		store:       options.Store,
		audit:       options.Audit,
		environment: options.Environment,
		brokerID:    strings.TrimSpace(options.BrokerID),
		key:         options.Key,
		now:         options.Now,
		random:      options.Random,
	}, nil
}

func (manager *Manager) Rotate(ctx context.Context, accountID, operator string, plaintext BrokerCredentials) (RotationResult, error) {
	accountID = strings.TrimSpace(accountID)
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return RotationResult{}, errors.New("credential operator is required")
	}
	if err := ValidateIdentity(manager.environment, manager.brokerID, accountID, 1, manager.key.ID); err != nil {
		return RotationResult{}, err
	}
	if len(manager.key.Key) != 32 {
		return RotationResult{}, errors.New("credential encryption key is not configured")
	}
	if err := ValidateBrokerCredentials(plaintext); err != nil {
		return RotationResult{}, err
	}
	operationID, err := randomToken(manager.random, 16)
	if err != nil {
		return RotationResult{}, err
	}
	now := manager.now()
	if err := manager.appendAudit(ctx, AuditEvent{
		OperationID: operationID, Environment: manager.environment, BrokerID: manager.brokerID,
		AccountID: accountID, Action: "rotate", Status: "started", KeyID: manager.key.ID,
		Operator: operator, CreatedAt: now,
	}); err != nil {
		return RotationResult{}, err
	}

	lockKey, _ := LockKey(manager.environment, manager.brokerID, accountID)
	lockToken, err := randomToken(manager.random, 16)
	if err != nil {
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "RANDOM_FAILED", nil, err)
	}
	acquired, err := manager.store.AcquireLock(ctx, lockKey, lockToken, credentialLockTTL)
	if err != nil {
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "LOCK_FAILED", nil, err)
	}
	if !acquired {
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "ROTATION_IN_PROGRESS", nil, errors.New("another credential operation is in progress"))
	}
	defer func() { _ = manager.store.ReleaseLock(context.Background(), lockKey, lockToken) }()

	currentVersion, err := manager.currentVersion(ctx, accountID)
	if err != nil {
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "CURRENT_READ_FAILED", nil, err)
	}

	var envelope Envelope
	var envelopeJSON []byte
	var version int64
	for candidate := currentVersion + 1; candidate <= currentVersion+1000; candidate++ {
		envelope, err = Encrypt(manager.environment, manager.brokerID, accountID, candidate, manager.key, plaintext, now, manager.random)
		if err != nil {
			return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "ENCRYPT_FAILED", nil, err)
		}
		envelopeJSON, err = json.Marshal(envelope)
		if err != nil {
			return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "ENVELOPE_ENCODE_FAILED", nil, errors.New("encode credential envelope"))
		}
		versionKey, _ := VersionKey(manager.environment, manager.brokerID, accountID, candidate)
		created, createErr := manager.store.SetIfAbsent(ctx, versionKey, string(envelopeJSON))
		if createErr != nil {
			return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "VERSION_WRITE_FAILED", nil, createErr)
		}
		if created {
			version = candidate
			break
		}
	}
	if version == 0 {
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "VERSION_EXHAUSTED", nil, errors.New("could not allocate a credential version"))
	}

	versionKey, _ := VersionKey(manager.environment, manager.brokerID, accountID, version)
	stored, err := manager.store.Get(ctx, versionKey)
	versionRef := &version
	if err != nil || !bytes.Equal([]byte(stored), envelopeJSON) {
		if err == nil {
			err = errors.New("credential version read-back mismatch")
		}
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "VERSION_VERIFY_FAILED", versionRef, err)
	}
	verifiedEnvelope, err := decodeEnvelope([]byte(stored))
	if err != nil {
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "ENVELOPE_INVALID", versionRef, err)
	}
	if err := verifiedEnvelope.Validate(manager.environment, manager.brokerID, accountID, version, manager.key.ID); err != nil {
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "ENVELOPE_IDENTITY_MISMATCH", versionRef, err)
	}
	if _, err := Decrypt(verifiedEnvelope, manager.key); err != nil {
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "ENVELOPE_DECRYPT_VERIFY_FAILED", versionRef, err)
	}
	currentKey, _ := CurrentKey(manager.environment, manager.brokerID, accountID)
	activated, err := manager.store.SetIfLockOwner(ctx, lockKey, lockToken, currentKey, strconv.FormatInt(version, 10))
	if err != nil || !activated {
		if err == nil {
			err = errors.New("credential operation lock expired before activation")
		}
		return RotationResult{}, manager.fail(ctx, operationID, accountID, operator, "rotate", "ACTIVATION_FAILED", versionRef, err)
	}

	digest := sha256.Sum256(envelopeJSON)
	result := RotationResult{
		OperationID: operationID, Environment: manager.environment, BrokerID: manager.brokerID,
		AccountID: accountID, CredentialVersion: version, KeyID: manager.key.ID,
		EnvelopeSHA256: hex.EncodeToString(digest[:]), IssuedAt: envelope.IssuedAt,
		CredentialSource: "relay_redis_encrypted", RestartRequired: true,
	}
	if err := manager.appendAudit(ctx, AuditEvent{
		OperationID: operationID, Environment: manager.environment, BrokerID: manager.brokerID,
		AccountID: accountID, Action: "rotate", Status: "succeeded", CredentialVersion: versionRef,
		KeyID: manager.key.ID, EnvelopeSHA256: result.EnvelopeSHA256, Operator: operator,
		Metadata: map[string]any{"credential_source": result.CredentialSource, "restart_required": true}, CreatedAt: manager.now(),
	}); err != nil {
		return result, errors.New("credential activated but success audit could not be written")
	}
	return result, nil
}

func (manager *Manager) Status(ctx context.Context, accountID string, verifyDecryption bool) (CredentialStatus, error) {
	accountID = strings.TrimSpace(accountID)
	if err := ValidateIdentity(manager.environment, manager.brokerID, accountID, 1, "status"); err != nil {
		return CredentialStatus{}, err
	}
	result := CredentialStatus{Environment: manager.environment, BrokerID: manager.brokerID, AccountID: accountID}
	version, err := manager.currentVersion(ctx, accountID)
	if err != nil {
		return result, err
	}
	if version == 0 {
		return result, nil
	}
	versionKey, _ := VersionKey(manager.environment, manager.brokerID, accountID, version)
	stored, err := manager.store.Get(ctx, versionKey)
	if err != nil {
		return result, errors.New("current credential version envelope is missing")
	}
	envelope, err := decodeEnvelope([]byte(stored))
	if err != nil {
		return result, err
	}
	if err := envelope.Validate(manager.environment, manager.brokerID, accountID, version, envelope.KeyID); err != nil {
		return result, err
	}
	digest := sha256.Sum256([]byte(stored))
	result.Configured = true
	result.CredentialVersion = version
	result.KeyID = envelope.KeyID
	result.IssuedAt = envelope.IssuedAt
	result.Protocol = envelope.Protocol
	result.Algorithm = envelope.Algorithm
	result.EnvelopeSHA256 = hex.EncodeToString(digest[:])
	result.CredentialSource = "relay_redis_encrypted"
	if verifyDecryption {
		if manager.key.ID == "" || len(manager.key.Key) != 32 {
			return result, errors.New("credential verification key is not configured")
		}
		if envelope.KeyID != manager.key.ID {
			return result, errors.New("configured credential key id does not match the active envelope")
		}
		if _, err := Decrypt(envelope, manager.key); err != nil {
			return result, err
		}
		result.DecryptionVerified = true
	}
	return result, nil
}

func (manager *Manager) Disable(ctx context.Context, accountID, operator string) (DisableResult, error) {
	accountID = strings.TrimSpace(accountID)
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return DisableResult{}, errors.New("credential operator is required")
	}
	if err := ValidateIdentity(manager.environment, manager.brokerID, accountID, 1, "disable"); err != nil {
		return DisableResult{}, err
	}
	operationID, err := randomToken(manager.random, 16)
	if err != nil {
		return DisableResult{}, err
	}
	if err := manager.appendAudit(ctx, AuditEvent{
		OperationID: operationID, Environment: manager.environment, BrokerID: manager.brokerID,
		AccountID: accountID, Action: "disable", Status: "started",
		Operator: operator, CreatedAt: manager.now(),
	}); err != nil {
		return DisableResult{}, err
	}
	lockKey, _ := LockKey(manager.environment, manager.brokerID, accountID)
	lockToken, err := randomToken(manager.random, 16)
	if err != nil {
		return DisableResult{}, manager.fail(ctx, operationID, accountID, operator, "disable", "RANDOM_FAILED", nil, err)
	}
	acquired, err := manager.store.AcquireLock(ctx, lockKey, lockToken, credentialLockTTL)
	if err != nil || !acquired {
		if err == nil {
			err = errors.New("another credential operation is in progress")
		}
		return DisableResult{}, manager.fail(ctx, operationID, accountID, operator, "disable", "LOCK_FAILED", nil, err)
	}
	defer func() { _ = manager.store.ReleaseLock(context.Background(), lockKey, lockToken) }()
	version, err := manager.currentVersion(ctx, accountID)
	if err != nil {
		return DisableResult{}, manager.fail(ctx, operationID, accountID, operator, "disable", "CURRENT_READ_FAILED", nil, err)
	}
	var versionRef *int64
	if version > 0 {
		versionRef = &version
	}
	currentKey, _ := CurrentKey(manager.environment, manager.brokerID, accountID)
	deleted, err := manager.store.DeleteIfLockOwner(ctx, lockKey, lockToken, currentKey)
	if err != nil || !deleted {
		if err == nil {
			err = errors.New("credential operation lock expired before disable")
		}
		return DisableResult{}, manager.fail(ctx, operationID, accountID, operator, "disable", "DISABLE_FAILED", versionRef, err)
	}
	result := DisableResult{
		OperationID: operationID, Environment: manager.environment, BrokerID: manager.brokerID,
		AccountID: accountID, CredentialVersion: version, Disabled: true, OCStopRequired: true,
	}
	if err := manager.appendAudit(ctx, AuditEvent{
		OperationID: operationID, Environment: manager.environment, BrokerID: manager.brokerID,
		AccountID: accountID, Action: "disable", Status: "succeeded", CredentialVersion: versionRef,
		Operator: operator, Metadata: map[string]any{"oc_stop_required": true}, CreatedAt: manager.now(),
	}); err != nil {
		return result, errors.New("credential pointer deleted but success audit could not be written")
	}
	return result, nil
}

func (manager *Manager) currentVersion(ctx context.Context, accountID string) (int64, error) {
	currentKey, _ := CurrentKey(manager.environment, manager.brokerID, accountID)
	value, err := manager.store.Get(ctx, currentKey)
	if errors.Is(err, ErrSecretNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	version, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || version < 1 {
		return 0, errors.New("current credential version is invalid")
	}
	return version, nil
}

func (manager *Manager) appendAudit(ctx context.Context, event AuditEvent) error {
	if manager.audit == nil {
		return errors.New("credential audit store is required for write operations")
	}
	return manager.audit.AppendCredentialAudit(ctx, event)
}

func (manager *Manager) fail(ctx context.Context, operationID, accountID, operator, action, code string, version *int64, cause error) error {
	auditErr := manager.appendAudit(ctx, AuditEvent{
		OperationID: operationID, Environment: manager.environment, BrokerID: manager.brokerID,
		AccountID: accountID, Action: action, Status: "failed", CredentialVersion: version,
		KeyID: manager.key.ID, Operator: operator, ErrorCode: code, CreatedAt: manager.now(),
	})
	if auditErr != nil {
		return fmt.Errorf("credential operation failed (%s); failure audit could not be written", code)
	}
	if cause == nil {
		cause = errors.New("credential operation failed")
	}
	return fmt.Errorf("credential operation failed (%s): %w", code, cause)
}

func decodeEnvelope(data []byte) (Envelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, errors.New("credential envelope is invalid JSON")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Envelope{}, errors.New("credential envelope contains trailing data")
	}
	return envelope, nil
}

func randomToken(random io.Reader, size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", errors.New("generate credential operation token")
	}
	return hex.EncodeToString(value), nil
}
