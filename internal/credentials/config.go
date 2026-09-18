package credentials

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ti-relay-trader/internal/config"
)

const (
	AdminTokenEnv = "RELAY_CREDENTIAL_ADMIN_TOKEN"
	TestKeyIDEnv  = "RELAY_OC_HUAXIN_TEST_CREDENTIAL_KEY_ID"
	TestKeyHexEnv = "RELAY_OC_HUAXIN_TEST_CREDENTIAL_KEY_HEX"
	ProdKeyIDEnv  = "RELAY_OC_HUAXIN_PROD_CREDENTIAL_KEY_ID"
	ProdKeyHexEnv = "RELAY_OC_HUAXIN_PROD_CREDENTIAL_KEY_HEX"
)

func ProtocolEnvironment(environment config.Environment) (string, error) {
	switch environment {
	case config.EnvironmentTest:
		return "test", nil
	case config.EnvironmentProduction:
		return "prod", nil
	default:
		return "", fmt.Errorf("unsupported Relay environment %q", environment)
	}
}

func LoadKeyMaterial(environment string) (KeyMaterial, error) {
	return LoadKeyMaterialWithLookup(environment, os.Getenv)
}

func LoadKeyMaterialWithLookup(environment string, lookup func(string) string) (KeyMaterial, error) {
	if lookup == nil {
		return KeyMaterial{}, errors.New("credential key environment lookup is nil")
	}
	var keyIDName, keyHexName string
	switch strings.TrimSpace(environment) {
	case "test":
		keyIDName, keyHexName = TestKeyIDEnv, TestKeyHexEnv
	case "prod":
		keyIDName, keyHexName = ProdKeyIDEnv, ProdKeyHexEnv
	default:
		return KeyMaterial{}, errors.New("credential environment must be test or prod")
	}
	keyID := strings.TrimSpace(lookup(keyIDName))
	keyHex := strings.TrimSpace(lookup(keyHexName))
	if keyID == "" || keyHex == "" {
		return KeyMaterial{}, fmt.Errorf("credential key material is missing; set %s and %s through the secure process environment", keyIDName, keyHexName)
	}
	return ParseKeyMaterial(keyID, keyHex)
}

func Prefix(environment, brokerID, accountID string) (string, error) {
	if err := ValidateIdentity(environment, brokerID, accountID, 1, "validation"); err != nil {
		return "", err
	}
	return fmt.Sprintf("relay:%s:v1:%s:%s", environment, brokerID, accountID), nil
}

func CurrentKey(environment, brokerID, accountID string) (string, error) {
	prefix, err := Prefix(environment, brokerID, accountID)
	if err != nil {
		return "", err
	}
	return prefix + ":secret:credentials:current", nil
}

func VersionKey(environment, brokerID, accountID string, version int64) (string, error) {
	if version < 1 {
		return "", errors.New("credential version must be positive")
	}
	prefix, err := Prefix(environment, brokerID, accountID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:secret:credentials:v%d", prefix, version), nil
}

func LockKey(environment, brokerID, accountID string) (string, error) {
	prefix, err := Prefix(environment, brokerID, accountID)
	if err != nil {
		return "", err
	}
	return prefix + ":secret:credentials:lock", nil
}
