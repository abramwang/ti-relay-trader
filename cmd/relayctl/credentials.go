package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/credentials"
)

const maxCredentialInputBytes = 8 * 1024

func runCredentials(args []string) error {
	if len(args) == 0 {
		return errors.New("missing credentials action: rotate, status, or disable")
	}
	switch args[0] {
	case "rotate":
		return runCredentialRotate(args[1:])
	case "status":
		return runCredentialStatus(args[1:])
	case "disable":
		return runCredentialDisable(args[1:])
	default:
		return fmt.Errorf("unknown credentials action %q", args[0])
	}
}

func runCredentialRotate(args []string) error {
	flags := flag.NewFlagSet("credentials rotate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	accountID := flags.String("account", "", "Relay standard account id")
	operator := flags.String("operator", "", "operator identity recorded in PostgreSQL audit")
	inputPath := flags.String("input", "-", "credential JSON path with mode 0600, or - for stdin")
	timeout := flags.Duration("timeout", 15*time.Second, "credential operation timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, environment, brokerID, err := credentialCommandConfig(*configPath, *accountID)
	if err != nil {
		return err
	}
	plaintext, err := readCredentialInput(*inputPath, os.Stdin)
	if err != nil {
		return err
	}
	key, err := credentials.LoadKeyMaterial(environment)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	store, err := credentials.OpenRedisSecretStore(cfg.Redis.URL)
	if err != nil {
		return err
	}
	defer store.Close()
	db, err := openCredentialAuditDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	manager, err := credentials.NewManager(credentials.ManagerOptions{
		Store: store, Audit: credentials.NewSQLAuditStore(db), Environment: environment,
		BrokerID: brokerID, Key: key,
	})
	if err != nil {
		return err
	}
	result, err := manager.Rotate(ctx, strings.TrimSpace(*accountID), strings.TrimSpace(*operator), plaintext)
	if err != nil {
		return err
	}
	return writeJSON(result)
}

func runCredentialStatus(args []string) error {
	flags := flag.NewFlagSet("credentials status", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	accountID := flags.String("account", "", "Relay standard account id")
	verify := flags.Bool("verify", false, "authenticate and decrypt without displaying plaintext")
	timeout := flags.Duration("timeout", 10*time.Second, "credential status timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, environment, brokerID, err := credentialCommandConfig(*configPath, *accountID)
	if err != nil {
		return err
	}
	var key credentials.KeyMaterial
	if *verify {
		key, err = credentials.LoadKeyMaterial(environment)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	store, err := credentials.OpenRedisSecretStore(cfg.Redis.URL)
	if err != nil {
		return err
	}
	defer store.Close()
	manager, err := credentials.NewManager(credentials.ManagerOptions{
		Store: store, Environment: environment, BrokerID: brokerID, Key: key,
	})
	if err != nil {
		return err
	}
	status, err := manager.Status(ctx, strings.TrimSpace(*accountID), *verify)
	if err != nil {
		return err
	}
	return writeJSON(status)
}

func runCredentialDisable(args []string) error {
	flags := flag.NewFlagSet("credentials disable", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	accountID := flags.String("account", "", "Relay standard account id")
	operator := flags.String("operator", "", "operator identity recorded in PostgreSQL audit")
	confirmAccount := flags.String("confirm-account", "", "must exactly match -account")
	timeout := flags.Duration("timeout", 15*time.Second, "credential operation timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*accountID) == "" || strings.TrimSpace(*confirmAccount) != strings.TrimSpace(*accountID) {
		return errors.New("-confirm-account must exactly match -account")
	}
	cfg, environment, brokerID, err := credentialCommandConfig(*configPath, *accountID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	store, err := credentials.OpenRedisSecretStore(cfg.Redis.URL)
	if err != nil {
		return err
	}
	defer store.Close()
	db, err := openCredentialAuditDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	manager, err := credentials.NewManager(credentials.ManagerOptions{
		Store: store, Audit: credentials.NewSQLAuditStore(db), Environment: environment, BrokerID: brokerID,
	})
	if err != nil {
		return err
	}
	result, err := manager.Disable(ctx, strings.TrimSpace(*accountID), strings.TrimSpace(*operator))
	if err != nil {
		return err
	}
	return writeJSON(result)
}

func credentialCommandConfig(path, accountID string) (*config.Config, string, string, error) {
	cfg, err := loadConfig(path)
	if err != nil {
		return nil, "", "", err
	}
	environment, err := credentials.ProtocolEnvironment(cfg.Service.Environment)
	if err != nil {
		return nil, "", "", err
	}
	accountID = strings.TrimSpace(accountID)
	account, ok := cfg.AccountRoute(accountID)
	if !ok {
		return nil, "", "", fmt.Errorf("account %q is not configured in the selected Relay environment", accountID)
	}
	brokerID := strings.TrimSpace(account.BrokerID)
	if brokerID != "huaxin" {
		return nil, "", "", fmt.Errorf("credential management currently supports huaxin accounts only")
	}
	if configuredBroker := strings.TrimSpace(cfg.Redis.BrokerID); configuredBroker != "" && configuredBroker != brokerID {
		return nil, "", "", errors.New("account broker does not match Redis broker configuration")
	}
	return cfg, environment, brokerID, nil
}

func readCredentialInput(path string, stdin io.Reader) (credentials.BrokerCredentials, error) {
	path = strings.TrimSpace(path)
	var reader io.Reader
	var file *os.File
	if path == "" || path == "-" {
		if stdin == nil {
			return credentials.BrokerCredentials{}, errors.New("credential stdin is unavailable")
		}
		reader = stdin
	} else {
		info, err := os.Stat(path)
		if err != nil {
			return credentials.BrokerCredentials{}, errors.New("stat credential input file")
		}
		if !info.Mode().IsRegular() {
			return credentials.BrokerCredentials{}, errors.New("credential input must be a regular file")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return credentials.BrokerCredentials{}, errors.New("credential input file permissions must not exceed 0600")
		}
		file, err = os.Open(path)
		if err != nil {
			return credentials.BrokerCredentials{}, errors.New("open credential input file")
		}
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxCredentialInputBytes+1))
	if err != nil {
		return credentials.BrokerCredentials{}, errors.New("read credential input")
	}
	if len(data) > maxCredentialInputBytes {
		return credentials.BrokerCredentials{}, errors.New("credential input exceeds 8 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var plaintext credentials.BrokerCredentials
	if err := decoder.Decode(&plaintext); err != nil {
		return credentials.BrokerCredentials{}, errors.New("credential input must be one valid JSON object")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return credentials.BrokerCredentials{}, errors.New("credential input contains trailing data")
	}
	if err := credentials.ValidateBrokerCredentials(plaintext); err != nil {
		return credentials.BrokerCredentials{}, err
	}
	return plaintext, nil
}

func openCredentialAuditDB(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	if cfg == nil || strings.TrimSpace(cfg.Database.DSN) == "" {
		return nil, errors.New("PostgreSQL DSN is required for credential audit")
	}
	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return nil, errors.New("open credential audit database")
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("connect credential audit database")
	}
	return db, nil
}
