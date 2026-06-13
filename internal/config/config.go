package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const EnvPath = "RELAY_CONFIG_PATH"

type Mode string

const (
	ModeDocs   Mode = "docs"
	ModeAPI    Mode = "api"
	ModeWorker Mode = "worker"
)

type Config struct {
	Service  ServiceConfig        `yaml:"service"`
	Database DatabaseConfig       `yaml:"database"`
	Redis    RedisConfig          `yaml:"redis"`
	Accounts []AccountRouteConfig `yaml:"accounts"`
	Jobs     map[string]JobConfig `yaml:"jobs"`
}

type ServiceConfig struct {
	PublicURL string `yaml:"public_url"`
	DocsAddr  string `yaml:"docs_addr"`
	APIAddr   string `yaml:"api_addr"`
	Mode      Mode   `yaml:"mode"`
}

type DatabaseConfig struct {
	Driver       string `yaml:"driver"`
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type RedisConfig struct {
	URL       string `yaml:"url"`
	Env       string `yaml:"env"`
	BrokerID  string `yaml:"broker_id"`
	GatewayID string `yaml:"gateway_id"`
}

type AccountRouteConfig struct {
	AccountID      string `yaml:"account_id"`
	BrokerID       string `yaml:"broker_id"`
	GatewayID      string `yaml:"gateway_id"`
	StreamPrefix   string `yaml:"stream_prefix"`
	Enabled        bool   `yaml:"enabled"`
	TradingEnabled bool   `yaml:"trading_enabled"`
	Simulated      bool   `yaml:"simulated"`
}

type JobConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Schedule string `yaml:"schedule"`
}

func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("config path is empty")
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	cfg, err := Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	return cfg, nil
}

func LoadFromEnv() (*Config, bool, error) {
	path := strings.TrimSpace(os.Getenv(EnvPath))
	if path == "" {
		cfg := Default()
		return &cfg, false, nil
	}
	cfg, err := Load(path)
	return cfg, true, err
}

func Decode(reader io.Reader) (*Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Default() Config {
	cfg := Config{}
	cfg.ApplyDefaults()
	return cfg
}

func (cfg *Config) ApplyDefaults() {
	if cfg.Service.PublicURL == "" {
		cfg.Service.PublicURL = "http://relay-trader.quantstage.com"
	}
	if cfg.Service.DocsAddr == "" {
		cfg.Service.DocsAddr = "0.0.0.0:9092"
	}
	if cfg.Service.APIAddr == "" {
		cfg.Service.APIAddr = "0.0.0.0:9092"
	}
	if cfg.Service.Mode == "" {
		cfg.Service.Mode = ModeDocs
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "postgres"
	}
	if cfg.Database.MaxOpenConns == 0 {
		cfg.Database.MaxOpenConns = 16
	}
	if cfg.Database.MaxIdleConns == 0 {
		cfg.Database.MaxIdleConns = 4
	}
	if cfg.Jobs == nil {
		cfg.Jobs = map[string]JobConfig{}
	}
}

func (cfg Config) Validate() error {
	if !cfg.Service.Mode.Valid() {
		return fmt.Errorf("invalid service.mode %q", cfg.Service.Mode)
	}
	if cfg.Database.MaxIdleConns > cfg.Database.MaxOpenConns {
		return fmt.Errorf("database.max_idle_conns must be <= max_open_conns")
	}

	seenAccounts := make(map[string]struct{}, len(cfg.Accounts))
	for i, account := range cfg.Accounts {
		if strings.TrimSpace(account.AccountID) == "" {
			return fmt.Errorf("accounts[%d].account_id is required", i)
		}
		if _, ok := seenAccounts[account.AccountID]; ok {
			return fmt.Errorf("duplicate account route for account_id %q", account.AccountID)
		}
		seenAccounts[account.AccountID] = struct{}{}
		if strings.TrimSpace(account.BrokerID) == "" {
			return fmt.Errorf("accounts[%d].broker_id is required", i)
		}
		if strings.TrimSpace(account.GatewayID) == "" {
			return fmt.Errorf("accounts[%d].gateway_id is required", i)
		}
		if strings.TrimSpace(account.StreamPrefix) == "" {
			return fmt.Errorf("accounts[%d].stream_prefix is required", i)
		}
	}

	return nil
}

func (mode Mode) Valid() bool {
	switch mode {
	case ModeDocs, ModeAPI, ModeWorker:
		return true
	default:
		return false
	}
}

func (cfg Config) AccountRoute(accountID string) (AccountRouteConfig, bool) {
	for _, account := range cfg.Accounts {
		if account.AccountID == accountID {
			return account, true
		}
	}
	return AccountRouteConfig{}, false
}
