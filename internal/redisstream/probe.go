package redisstream

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/timeutil"
)

type ProbeOptions struct {
	SamplesPerStream int
	Prefixes         []string
}

type ProbeReport struct {
	GeneratedAt      time.Time     `json:"generated_at"`
	Protocol         string        `json:"protocol"`
	RedisAddr        string        `json:"redis_addr"`
	Prefixes         []string      `json:"prefixes"`
	SamplesPerStream int           `json:"samples_per_stream"`
	Streams          []StreamProbe `json:"streams"`
}

type StreamProbe struct {
	Name                string               `json:"name"`
	Exists              bool                 `json:"exists"`
	Length              int64                `json:"length,omitempty"`
	LastGeneratedID     string               `json:"last_generated_id,omitempty"`
	ConsumerGroups      []ConsumerGroupProbe `json:"consumer_groups,omitempty"`
	ConsumerGroupsError string               `json:"consumer_groups_error,omitempty"`
	Latest              []MessageSummary     `json:"latest,omitempty"`
	Error               string               `json:"error,omitempty"`
}

type ConsumerGroupProbe struct {
	Name            string              `json:"name"`
	Consumers       int64               `json:"consumers"`
	Pending         int64               `json:"pending"`
	LastDeliveredID string              `json:"last_delivered_id"`
	EntriesRead     int64               `json:"entries_read"`
	Lag             int64               `json:"lag"`
	PendingEntries  []PendingEntryProbe `json:"pending_entries,omitempty"`
	PendingError    string              `json:"pending_error,omitempty"`
}

type PendingEntryProbe struct {
	ID               string `json:"id"`
	Consumer         string `json:"consumer"`
	IdleMilliseconds int64  `json:"idle_ms"`
	DeliveryCount    int64  `json:"delivery_count"`
}

func Probe(ctx context.Context, cfg config.Config, opts ProbeOptions) (ProbeReport, error) {
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		return ProbeReport{}, errors.New("redis.url is required for stream probe")
	}

	redisOptions, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return ProbeReport{}, err
	}
	client := redis.NewClient(redisOptions)
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		return ProbeReport{}, err
	}

	prefixes := opts.Prefixes
	if len(prefixes) == 0 {
		prefixes = PrefixesFromConfig(cfg)
	}
	if len(prefixes) == 0 {
		return ProbeReport{}, errors.New("no stream prefixes configured")
	}

	samples := opts.SamplesPerStream
	if samples <= 0 {
		samples = 3
	}

	report := ProbeReport{
		GeneratedAt:      timeutil.Now(),
		Protocol:         Protocol,
		RedisAddr:        maskRedisAddr(cfg.Redis.URL),
		Prefixes:         prefixes,
		SamplesPerStream: samples,
	}

	for _, prefix := range prefixes {
		for _, stream := range NewStreams(prefix).All() {
			report.Streams = append(report.Streams, probeStream(ctx, client, stream, samples))
		}
	}

	return report, nil
}

func probeStream(ctx context.Context, client *redis.Client, stream string, samples int) StreamProbe {
	probe := StreamProbe{Name: stream}

	info, err := client.XInfoStream(ctx, stream).Result()
	if err != nil {
		if isMissingStream(err) {
			return probe
		}
		probe.Error = err.Error()
		return probe
	}

	probe.Exists = true
	probe.Length = info.Length
	probe.LastGeneratedID = info.LastGeneratedID

	groups, err := client.XInfoGroups(ctx, stream).Result()
	if err != nil {
		probe.ConsumerGroupsError = err.Error()
	} else {
		probe.ConsumerGroups = consumerGroupProbes(groups)
		for index := range probe.ConsumerGroups {
			if probe.ConsumerGroups[index].Pending <= 0 {
				continue
			}
			entries, pendingErr := client.XPendingExt(ctx, &redis.XPendingExtArgs{
				Stream: stream,
				Group:  probe.ConsumerGroups[index].Name,
				Start:  "-",
				End:    "+",
				Count:  int64(samples),
			}).Result()
			if pendingErr != nil {
				probe.ConsumerGroups[index].PendingError = pendingErr.Error()
				continue
			}
			probe.ConsumerGroups[index].PendingEntries = pendingEntryProbes(entries)
		}
	}

	messages, err := client.XRevRangeN(ctx, stream, "+", "-", int64(samples)).Result()
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	probe.Latest = make([]MessageSummary, 0, len(messages))
	for _, message := range messages {
		probe.Latest = append(probe.Latest, SummarizeEntry(stream, message.ID, message.Values))
	}

	return probe
}

func consumerGroupProbes(groups []redis.XInfoGroup) []ConsumerGroupProbe {
	if len(groups) == 0 {
		return nil
	}
	probes := make([]ConsumerGroupProbe, 0, len(groups))
	for _, group := range groups {
		probes = append(probes, ConsumerGroupProbe{
			Name:            group.Name,
			Consumers:       group.Consumers,
			Pending:         group.Pending,
			LastDeliveredID: group.LastDeliveredID,
			EntriesRead:     group.EntriesRead,
			Lag:             group.Lag,
		})
	}
	return probes
}

func pendingEntryProbes(entries []redis.XPendingExt) []PendingEntryProbe {
	if len(entries) == 0 {
		return nil
	}
	probes := make([]PendingEntryProbe, 0, len(entries))
	for _, entry := range entries {
		probes = append(probes, PendingEntryProbe{
			ID:               entry.ID,
			Consumer:         entry.Consumer,
			IdleMilliseconds: entry.Idle.Milliseconds(),
			DeliveryCount:    entry.RetryCount,
		})
	}
	return probes
}

func isMissingStream(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such key") || strings.Contains(text, "no such stream")
}

func maskRedisAddr(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "invalid-url"
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if username == "" {
			parsed.User = url.UserPassword("", "***")
		} else {
			parsed.User = url.UserPassword(username, "***")
		}
	}
	return parsed.String()
}
