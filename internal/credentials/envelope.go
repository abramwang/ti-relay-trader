package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	Protocol  = "oc.secret.v1"
	Algorithm = "A256GCM"
)

var (
	accountIDPattern = regexp.MustCompile(`^[0-9]{6,20}$`)
	brokerIDPattern  = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
	keyIDPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

type BrokerCredentials struct {
	BrokerLoginUser string `json:"broker_login_user"`
	BrokerPassword  string `json:"broker_password"`
	DynamicPassword string `json:"dynamic_password"`
}

type KeyMaterial struct {
	ID  string
	Key []byte
}

type Envelope struct {
	Protocol          string `json:"protocol"`
	Algorithm         string `json:"algorithm"`
	Environment       string `json:"env"`
	BrokerID          string `json:"broker_id"`
	AccountID         string `json:"account_id"`
	CredentialVersion int64  `json:"credential_version"`
	KeyID             string `json:"key_id"`
	NonceB64          string `json:"nonce_b64"`
	CiphertextB64     string `json:"ciphertext_b64"`
	TagB64            string `json:"tag_b64"`
	IssuedAt          string `json:"issued_at"`
}

func ParseKeyMaterial(keyID, keyHex string) (KeyMaterial, error) {
	keyID = strings.TrimSpace(keyID)
	if !keyIDPattern.MatchString(keyID) {
		return KeyMaterial{}, errors.New("credential key id must use 1-64 letters, digits, dot, underscore, or hyphen")
	}
	keyHex = strings.TrimSpace(keyHex)
	if len(keyHex) != 64 {
		return KeyMaterial{}, errors.New("credential key must contain exactly 64 hexadecimal characters")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return KeyMaterial{}, errors.New("credential key must be a valid 32-byte hexadecimal value")
	}
	return KeyMaterial{ID: keyID, Key: key}, nil
}

func ValidateBrokerCredentials(value BrokerCredentials) error {
	if strings.TrimSpace(value.BrokerLoginUser) == "" {
		return errors.New("broker_login_user is required")
	}
	if strings.TrimSpace(value.BrokerPassword) == "" {
		return errors.New("broker_password is required")
	}
	return nil
}

func ValidateIdentity(environment, brokerID, accountID string, credentialVersion int64, keyID string) error {
	if environment != "test" && environment != "prod" {
		return fmt.Errorf("credential environment must be test or prod")
	}
	if !brokerIDPattern.MatchString(brokerID) {
		return errors.New("credential broker id must use 1-32 lowercase letters, digits, underscore, or hyphen")
	}
	if !accountIDPattern.MatchString(accountID) {
		return errors.New("credential account id must contain 6-20 digits")
	}
	if credentialVersion < 1 {
		return errors.New("credential version must be positive")
	}
	if !keyIDPattern.MatchString(keyID) {
		return errors.New("credential key id is invalid")
	}
	return nil
}

func AdditionalAuthenticatedData(environment, brokerID, accountID string, credentialVersion int64, keyID string) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d|%s", Protocol, environment, brokerID, accountID, credentialVersion, keyID)
}

func Encrypt(
	environment string,
	brokerID string,
	accountID string,
	credentialVersion int64,
	key KeyMaterial,
	plaintext BrokerCredentials,
	issuedAt time.Time,
	random io.Reader,
) (Envelope, error) {
	if err := ValidateIdentity(environment, brokerID, accountID, credentialVersion, key.ID); err != nil {
		return Envelope{}, err
	}
	if len(key.Key) != 32 {
		return Envelope{}, errors.New("credential key must be 32 bytes")
	}
	if err := ValidateBrokerCredentials(plaintext); err != nil {
		return Envelope{}, err
	}
	if random == nil {
		random = rand.Reader
	}
	plainJSON, err := json.Marshal(plaintext)
	if err != nil {
		return Envelope{}, errors.New("encode credential plaintext")
	}
	block, err := aes.NewCipher(key.Key)
	if err != nil {
		return Envelope{}, errors.New("initialize credential cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, errors.New("initialize credential GCM")
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return Envelope{}, errors.New("generate credential nonce")
	}
	aad := []byte(AdditionalAuthenticatedData(environment, brokerID, accountID, credentialVersion, key.ID))
	sealed := gcm.Seal(nil, nonce, plainJSON, aad)
	if len(sealed) < gcm.Overhead() {
		return Envelope{}, errors.New("credential encryption produced an invalid result")
	}
	ciphertext := sealed[:len(sealed)-gcm.Overhead()]
	tag := sealed[len(sealed)-gcm.Overhead():]
	return Envelope{
		Protocol:          Protocol,
		Algorithm:         Algorithm,
		Environment:       environment,
		BrokerID:          brokerID,
		AccountID:         accountID,
		CredentialVersion: credentialVersion,
		KeyID:             key.ID,
		NonceB64:          base64.StdEncoding.EncodeToString(nonce),
		CiphertextB64:     base64.StdEncoding.EncodeToString(ciphertext),
		TagB64:            base64.StdEncoding.EncodeToString(tag),
		IssuedAt:          issuedAt.Format(time.RFC3339),
	}, nil
}

func (envelope Envelope) Validate(expectedEnvironment, expectedBrokerID, expectedAccountID string, expectedVersion int64, expectedKeyID string) error {
	if envelope.Protocol != Protocol {
		return errors.New("credential envelope protocol mismatch")
	}
	if envelope.Algorithm != Algorithm {
		return errors.New("credential envelope algorithm mismatch")
	}
	if err := ValidateIdentity(envelope.Environment, envelope.BrokerID, envelope.AccountID, envelope.CredentialVersion, envelope.KeyID); err != nil {
		return fmt.Errorf("invalid credential envelope identity: %w", err)
	}
	if envelope.Environment != expectedEnvironment || envelope.BrokerID != expectedBrokerID ||
		envelope.AccountID != expectedAccountID || envelope.CredentialVersion != expectedVersion || envelope.KeyID != expectedKeyID {
		return errors.New("credential envelope identity mismatch")
	}
	if _, err := time.Parse(time.RFC3339, envelope.IssuedAt); err != nil {
		return errors.New("credential envelope issued_at is invalid")
	}
	return nil
}

func Decrypt(envelope Envelope, key KeyMaterial) (BrokerCredentials, error) {
	if err := envelope.Validate(envelope.Environment, envelope.BrokerID, envelope.AccountID, envelope.CredentialVersion, key.ID); err != nil {
		return BrokerCredentials{}, err
	}
	if len(key.Key) != 32 {
		return BrokerCredentials{}, errors.New("credential key must be 32 bytes")
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.NonceB64)
	if err != nil {
		return BrokerCredentials{}, errors.New("credential envelope nonce is not valid base64")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.CiphertextB64)
	if err != nil {
		return BrokerCredentials{}, errors.New("credential envelope ciphertext is not valid base64")
	}
	tag, err := base64.StdEncoding.DecodeString(envelope.TagB64)
	if err != nil {
		return BrokerCredentials{}, errors.New("credential envelope tag is not valid base64")
	}
	block, err := aes.NewCipher(key.Key)
	if err != nil {
		return BrokerCredentials{}, errors.New("initialize credential cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return BrokerCredentials{}, errors.New("initialize credential GCM")
	}
	if len(nonce) != gcm.NonceSize() || len(tag) != gcm.Overhead() {
		return BrokerCredentials{}, errors.New("credential envelope nonce or tag length is invalid")
	}
	aad := []byte(AdditionalAuthenticatedData(envelope.Environment, envelope.BrokerID, envelope.AccountID, envelope.CredentialVersion, envelope.KeyID))
	plainJSON, err := gcm.Open(nil, nonce, append(ciphertext, tag...), aad)
	if err != nil {
		return BrokerCredentials{}, errors.New("credential envelope authentication failed")
	}
	var plaintext BrokerCredentials
	if err := json.Unmarshal(plainJSON, &plaintext); err != nil {
		return BrokerCredentials{}, errors.New("credential plaintext is invalid JSON")
	}
	if err := ValidateBrokerCredentials(plaintext); err != nil {
		return BrokerCredentials{}, fmt.Errorf("credential plaintext is invalid: %w", err)
	}
	return plaintext, nil
}
