package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"ti-relay-trader/internal/timeutil"
)

const EnvPath = "RELAY_CONFIG_PATH"

type Mode string

const (
	ModeDocs   Mode = "docs"
	ModeAPI    Mode = "api"
	ModeWorker Mode = "worker"
)

type Config struct {
	Service     ServiceConfig        `yaml:"service"`
	Database    DatabaseConfig       `yaml:"database"`
	Redis       RedisConfig          `yaml:"redis"`
	Market      MarketConfig         `yaml:"market"`
	AutoRefresh AutoRefreshConfig    `yaml:"auto_refresh"`
	Accounts    []AccountRouteConfig `yaml:"accounts"`
	Jobs        map[string]JobConfig `yaml:"jobs"`
}

type ServiceConfig struct {
	PublicURL   string      `yaml:"public_url"`
	DocsAddr    string      `yaml:"docs_addr"`
	APIAddr     string      `yaml:"api_addr"`
	Mode        Mode        `yaml:"mode"`
	Environment Environment `yaml:"environment"`
	Timezone    string      `yaml:"timezone"`
	LogLevel    string      `yaml:"log_level"`
	LogFormat   string      `yaml:"log_format"`
}

type Environment string

const (
	EnvironmentTest       Environment = "test"
	EnvironmentProduction Environment = "production"
)

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

type MarketConfig struct {
	BaseURL             string `yaml:"base_url"`
	TimeoutSeconds      int    `yaml:"timeout_seconds"`
	SnapshotMarketLevel string `yaml:"snapshot_market_level"`
	SnapshotDataScope   string `yaml:"snapshot_data_scope"`
}

type AutoRefreshConfig struct {
	Enabled         *bool `yaml:"enabled"`
	DebounceSeconds int   `yaml:"debounce_seconds"`
	CooldownSeconds int   `yaml:"cooldown_seconds"`
	TimeoutSeconds  int   `yaml:"timeout_seconds"`
}

type AccountRouteConfig struct {
	AccountID      string `yaml:"account_id"`
	Alias          string `yaml:"alias"`
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
	if cfg.Service.Environment == "" {
		cfg.Service.Environment = EnvironmentTest
	}
	cfg.Service.Timezone = strings.TrimSpace(cfg.Service.Timezone)
	if cfg.Service.Timezone == "" {
		cfg.Service.Timezone = timeutil.LocationName
	}
	if cfg.Service.LogLevel == "" {
		cfg.Service.LogLevel = "info"
	}
	if cfg.Service.LogFormat == "" {
		cfg.Service.LogFormat = "json"
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
	if cfg.Market.BaseURL == "" {
		cfg.Market.BaseURL = "http://meridian-data.quantstage.com"
	}
	if cfg.Market.TimeoutSeconds == 0 {
		cfg.Market.TimeoutSeconds = 15
	}
	if cfg.Market.SnapshotMarketLevel == "" {
		cfg.Market.SnapshotMarketLevel = "level1"
	}
	if cfg.Market.SnapshotDataScope == "" {
		cfg.Market.SnapshotDataScope = "realtime"
	}
	if cfg.AutoRefresh.DebounceSeconds == 0 {
		cfg.AutoRefresh.DebounceSeconds = 2
	}
	if cfg.AutoRefresh.CooldownSeconds == 0 {
		cfg.AutoRefresh.CooldownSeconds = 20
	}
	if cfg.AutoRefresh.TimeoutSeconds == 0 {
		cfg.AutoRefresh.TimeoutSeconds = 10
	}
	if cfg.Jobs == nil {
		cfg.Jobs = map[string]JobConfig{}
	}
}

func (cfg Config) Validate() error {
	if !cfg.Service.Mode.Valid() {
		return fmt.Errorf("invalid service.mode %q", cfg.Service.Mode)
	}
	if !cfg.Service.Environment.Valid() {
		return fmt.Errorf("invalid service.environment %q", cfg.Service.Environment)
	}
	if _, err := time.LoadLocation(cfg.Service.Timezone); err != nil {
		return fmt.Errorf("invalid service.timezone %q: %w", cfg.Service.Timezone, err)
	}
	if !validLogLevel(cfg.Service.LogLevel) {
		return fmt.Errorf("invalid service.log_level %q", cfg.Service.LogLevel)
	}
	if !validLogFormat(cfg.Service.LogFormat) {
		return fmt.Errorf("invalid service.log_format %q", cfg.Service.LogFormat)
	}
	if cfg.Database.MaxIdleConns > cfg.Database.MaxOpenConns {
		return fmt.Errorf("database.max_idle_conns must be <= max_open_conns")
	}
	if strings.TrimSpace(cfg.Market.BaseURL) == "" {
		return fmt.Errorf("market.base_url is required")
	}
	if cfg.Market.TimeoutSeconds < 0 {
		return fmt.Errorf("market.timeout_seconds must be non-negative")
	}
	if cfg.AutoRefresh.DebounceSeconds < 0 {
		return fmt.Errorf("auto_refresh.debounce_seconds must be non-negative")
	}
	if cfg.AutoRefresh.CooldownSeconds < 0 {
		return fmt.Errorf("auto_refresh.cooldown_seconds must be non-negative")
	}
	if cfg.AutoRefresh.TimeoutSeconds < 0 {
		return fmt.Errorf("auto_refresh.timeout_seconds must be non-negative")
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
		if account.TradingEnabled && !account.Enabled {
			return fmt.Errorf("accounts[%d].trading_enabled requires enabled=true", i)
		}
		if cfg.Service.Environment == EnvironmentProduction && account.TradingEnabled && account.Simulated {
			return fmt.Errorf("accounts[%d].simulated must be false when production trading is enabled", i)
		}
		if expected := streamPrefixForAccount(cfg.Redis.Env, account); expected != "" && account.StreamPrefix != expected {
			return fmt.Errorf("accounts[%d].stream_prefix %q does not match expected %q", i, account.StreamPrefix, expected)
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

func (environment Environment) Valid() bool {
	switch environment {
	case EnvironmentTest, EnvironmentProduction:
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

func (cfg Config) AutoRefreshEnabled() bool {
	return cfg.AutoRefresh.Enabled == nil || *cfg.AutoRefresh.Enabled
}

func validLogLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug", "info", "warn", "warning", "error":
		return true
	default:
		return false
	}
}

func validLogFormat(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json", "text":
		return true
	default:
		return false
	}
}

func streamPrefixForAccount(redisEnv string, account AccountRouteConfig) string {
	redisEnv = strings.TrimSpace(redisEnv)
	brokerID := strings.TrimSpace(account.BrokerID)
	gatewayID := strings.TrimSpace(account.GatewayID)
	if redisEnv == "" || brokerID == "" || gatewayID == "" {
		return ""
	}
	return fmt.Sprintf("relay:%s:v1:%s:%s", redisEnv, brokerID, gatewayID)
}
