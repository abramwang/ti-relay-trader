package redisstream

import (
	"fmt"
	"sort"
	"strings"

	"ti-relay-trader/internal/config"
)

const Protocol = "relay.stream.v1"

const (
	SuffixCmdTrade = "cmd.trade"
	SuffixCmdQuery = "cmd.query"
	SuffixReply    = "reply"
	SuffixEvent    = "event"
	SuffixHB       = "hb"
	SuffixDLQ      = "dlq"
)

type Streams struct {
	Prefix   string `json:"prefix"`
	CmdTrade string `json:"cmd_trade"`
	CmdQuery string `json:"cmd_query"`
	Reply    string `json:"reply"`
	Event    string `json:"event"`
	HB       string `json:"hb"`
	DLQ      string `json:"dlq"`
}

func NewStreams(prefix string) Streams {
	prefix = strings.TrimRight(strings.TrimSpace(prefix), ":")
	return Streams{
		Prefix:   prefix,
		CmdTrade: prefix + ":" + SuffixCmdTrade,
		CmdQuery: prefix + ":" + SuffixCmdQuery,
		Reply:    prefix + ":" + SuffixReply,
		Event:    prefix + ":" + SuffixEvent,
		HB:       prefix + ":" + SuffixHB,
		DLQ:      prefix + ":" + SuffixDLQ,
	}
}

func (streams Streams) All() []string {
	return []string{
		streams.CmdTrade,
		streams.CmdQuery,
		streams.Reply,
		streams.Event,
		streams.HB,
		streams.DLQ,
	}
}

func PrefixFromRedisConfig(cfg config.RedisConfig) string {
	if strings.TrimSpace(cfg.Env) == "" || strings.TrimSpace(cfg.BrokerID) == "" || strings.TrimSpace(cfg.GatewayID) == "" {
		return ""
	}
	return fmt.Sprintf("relay:%s:v1:%s:%s", cfg.Env, cfg.BrokerID, cfg.GatewayID)
}

func PrefixesFromConfig(cfg config.Config) []string {
	seen := map[string]struct{}{}
	add := func(prefix string) {
		prefix = strings.TrimRight(strings.TrimSpace(prefix), ":")
		if prefix == "" {
			return
		}
		seen[prefix] = struct{}{}
	}

	for _, account := range cfg.Accounts {
		add(account.StreamPrefix)
	}
	add(PrefixFromRedisConfig(cfg.Redis))

	prefixes := make([]string, 0, len(seen))
	for prefix := range seen {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	return prefixes
}
