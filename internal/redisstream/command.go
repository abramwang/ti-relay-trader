package redisstream

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/timeutil"
)

const (
	ActionOrderSubmit      = "order.submit"
	ActionOrderBatchSubmit = "order.batch.submit"
	ActionOrderCancel      = "order.cancel"
	ActionAccountAsset     = "account.asset.query"
	ActionAccountPositions = "account.positions.query"
	ActionOrderList        = "order.list.query"
	ActionFillList         = "fill.list.query"
)

type CommandEnvelope struct {
	Protocol       string `json:"protocol,omitempty"`
	MessageType    string `json:"message_type"`
	MessageID      string `json:"message_id"`
	RequestID      string `json:"request_id,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Action         string `json:"action"`
	Payload        any    `json:"payload"`
	SentAt         string `json:"sent_at,omitempty"`
}

type CommandPublishResult struct {
	StreamKey string `json:"stream_key"`
	StreamID  string `json:"stream_id"`
	BodyBytes int    `json:"body_bytes"`
}

type RedisCommandPublisher struct {
	client *redis.Client
}

func NewRedisCommandPublisher(client *redis.Client) *RedisCommandPublisher {
	return &RedisCommandPublisher{client: client}
}

func OpenRedisCommandPublisher(cfg config.RedisConfig) (*RedisCommandPublisher, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("redis.url is required")
	}
	options, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	return NewRedisCommandPublisher(redis.NewClient(options)), nil
}

func NewCommandEnvelope(action, messageID, requestID, correlationID, idempotencyKey string, payload any, sentAt time.Time) CommandEnvelope {
	if sentAt.IsZero() {
		sentAt = timeutil.Now()
	}
	sentAt = timeutil.InBusinessLocation(sentAt)
	return CommandEnvelope{
		Protocol:       Protocol,
		MessageType:    "command",
		MessageID:      messageID,
		RequestID:      requestID,
		CorrelationID:  correlationID,
		IdempotencyKey: idempotencyKey,
		Action:         action,
		Payload:        payload,
		SentAt:         timeutil.FormatRFC3339Nano(sentAt),
	}
}

func (publisher *RedisCommandPublisher) PublishCommand(ctx context.Context, streamKey string, envelope CommandEnvelope) (CommandPublishResult, error) {
	if publisher == nil || publisher.client == nil {
		return CommandPublishResult{}, fmt.Errorf("redis command publisher is nil")
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return CommandPublishResult{}, fmt.Errorf("marshal redis command: %w", err)
	}
	streamID, err := publisher.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: map[string]any{
			"body": string(body),
		},
	}).Result()
	if err != nil {
		return CommandPublishResult{}, err
	}
	return CommandPublishResult{
		StreamKey: streamKey,
		StreamID:  streamID,
		BodyBytes: len(body),
	}, nil
}

func (publisher *RedisCommandPublisher) Close() error {
	if publisher == nil || publisher.client == nil {
		return nil
	}
	return publisher.client.Close()
}

func (publisher *RedisCommandPublisher) Ping(ctx context.Context) error {
	if publisher == nil || publisher.client == nil {
		return fmt.Errorf("redis command publisher is nil")
	}
	return publisher.client.Ping(ctx).Err()
}

func CommandStreamForAction(streams Streams, action string) (string, error) {
	switch action {
	case ActionOrderSubmit, ActionOrderBatchSubmit, ActionOrderCancel:
		return streams.CmdTrade, nil
	case ActionAccountAsset, ActionAccountPositions, ActionOrderList, ActionFillList:
		return streams.CmdQuery, nil
	default:
		return "", fmt.Errorf("unsupported redis command action %q", action)
	}
}
