package redisstream

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/timeutil"
)

type StreamScanOptions struct {
	Pattern string
	Count   int64
}

type StreamScanReport struct {
	GeneratedAt time.Time           `json:"generated_at"`
	Protocol    string              `json:"protocol"`
	RedisAddr   string              `json:"redis_addr"`
	Pattern     string              `json:"pattern"`
	Accounts    []StreamScanAccount `json:"accounts"`
	Streams     []StreamScanEntry   `json:"streams"`
}

type StreamScanAccount struct {
	Prefix    string   `json:"prefix"`
	BrokerID  string   `json:"broker_id,omitempty"`
	GatewayID string   `json:"gateway_id,omitempty"`
	Roles     []string `json:"roles"`
}

type StreamScanEntry struct {
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	BrokerID  string `json:"broker_id,omitempty"`
	GatewayID string `json:"gateway_id,omitempty"`
	Role      string `json:"role,omitempty"`
	Type      string `json:"type"`
	Length    int64  `json:"length,omitempty"`
}

func ScanStreams(ctx context.Context, cfg config.Config, opts StreamScanOptions) (StreamScanReport, error) {
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		return StreamScanReport{}, errors.New("redis.url is required for stream scan")
	}
	redisOptions, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return StreamScanReport{}, err
	}
	client := redis.NewClient(redisOptions)
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		return StreamScanReport{}, err
	}

	pattern := strings.TrimSpace(opts.Pattern)
	if pattern == "" {
		env := strings.TrimSpace(cfg.Redis.Env)
		if env == "" {
			env = "*"
		}
		pattern = "relay:" + env + ":v1:*:*"
	}
	count := opts.Count
	if count <= 0 {
		count = 200
	}

	report := StreamScanReport{
		GeneratedAt: timeutil.Now(),
		Protocol:    Protocol,
		RedisAddr:   maskRedisAddr(cfg.Redis.URL),
		Pattern:     pattern,
	}

	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, pattern, count).Result()
		if err != nil {
			return StreamScanReport{}, err
		}
		for _, key := range keys {
			entry, ok := scanStreamEntry(ctx, client, key)
			if ok {
				report.Streams = append(report.Streams, entry)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	sort.Slice(report.Streams, func(i, j int) bool {
		return report.Streams[i].Name < report.Streams[j].Name
	})
	report.Accounts = summarizeScanAccounts(report.Streams)
	return report, nil
}

func scanStreamEntry(ctx context.Context, client *redis.Client, key string) (StreamScanEntry, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return StreamScanEntry{}, false
	}
	typ, err := client.Type(ctx, key).Result()
	if err != nil || typ != "stream" {
		return StreamScanEntry{}, false
	}
	length, err := client.XLen(ctx, key).Result()
	if err != nil {
		return StreamScanEntry{}, false
	}
	entry := StreamScanEntry{Name: key, Type: typ, Length: length}
	parts := strings.Split(key, ":")
	if len(parts) >= 6 {
		entry.BrokerID = parts[3]
		entry.GatewayID = parts[4]
		entry.Role = parts[5]
		entry.Prefix = strings.Join(parts[:5], ":")
	} else if len(parts) > 1 {
		entry.Role = parts[len(parts)-1]
		entry.Prefix = strings.Join(parts[:len(parts)-1], ":")
	}
	return entry, true
}

func summarizeScanAccounts(streams []StreamScanEntry) []StreamScanAccount {
	type accountState struct {
		account StreamScanAccount
		roles   map[string]struct{}
	}
	byPrefix := make(map[string]*accountState)
	for _, stream := range streams {
		prefix := strings.TrimSpace(stream.Prefix)
		if prefix == "" {
			continue
		}
		state := byPrefix[prefix]
		if state == nil {
			state = &accountState{
				account: StreamScanAccount{
					Prefix:    prefix,
					BrokerID:  stream.BrokerID,
					GatewayID: stream.GatewayID,
				},
				roles: map[string]struct{}{},
			}
			byPrefix[prefix] = state
		}
		if stream.Role != "" {
			state.roles[stream.Role] = struct{}{}
		}
	}

	accounts := make([]StreamScanAccount, 0, len(byPrefix))
	for _, state := range byPrefix {
		for role := range state.roles {
			state.account.Roles = append(state.account.Roles, role)
		}
		sort.Strings(state.account.Roles)
		accounts = append(accounts, state.account)
	}
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].Prefix < accounts[j].Prefix
	})
	return accounts
}
