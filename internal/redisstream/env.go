package redisstream

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"ti-relay-trader/internal/config"
)

func ApplyProbeEnv(cfg config.Config) config.Config {
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		cfg.Redis.URL = redisURLFromEnv()
	}
	if strings.TrimSpace(cfg.Redis.Env) == "" {
		cfg.Redis.Env = strings.TrimSpace(os.Getenv("HX_RELAY_ENV"))
	}
	if strings.TrimSpace(cfg.Redis.BrokerID) == "" {
		cfg.Redis.BrokerID = strings.TrimSpace(os.Getenv("HX_RELAY_BROKER_ID"))
	}
	if strings.TrimSpace(cfg.Redis.GatewayID) == "" {
		cfg.Redis.GatewayID = strings.TrimSpace(os.Getenv("HX_RELAY_GATEWAY_ID"))
	}

	accountID := strings.TrimSpace(os.Getenv("HX_ACCOUNT_ID"))
	prefix := PrefixFromRedisConfig(cfg.Redis)
	if accountID != "" && prefix != "" && !hasAccount(cfg.Accounts, accountID) {
		cfg.Accounts = append(cfg.Accounts, config.AccountRouteConfig{
			AccountID:    accountID,
			BrokerID:     cfg.Redis.BrokerID,
			GatewayID:    cfg.Redis.GatewayID,
			StreamPrefix: prefix,
			Enabled:      true,
		})
	}
	return cfg
}

func redisURLFromEnv() string {
	if value := strings.TrimSpace(os.Getenv("REDIS_URL")); value != "" {
		return value
	}

	host := strings.TrimSpace(os.Getenv("HX_REDIS_HOST"))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(os.Getenv("HX_REDIS_PORT"))
	if port == "" {
		port = "6379"
	}
	db := strings.TrimSpace(os.Getenv("HX_REDIS_DB"))
	if db == "" {
		db = "0"
	}

	redisURL := url.URL{
		Scheme: "redis",
		Host:   net.JoinHostPort(host, port),
		Path:   fmt.Sprintf("/%s", db),
	}
	if password := os.Getenv("HX_REDIS_PASSWORD"); password != "" {
		redisURL.User = url.UserPassword("", password)
	}
	return redisURL.String()
}

func hasAccount(accounts []config.AccountRouteConfig, accountID string) bool {
	for _, account := range accounts {
		if account.AccountID == accountID {
			return true
		}
	}
	return false
}
