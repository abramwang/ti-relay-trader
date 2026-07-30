package redisstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/timeutil"
)

type ObservabilityStore interface {
	ListStreamCheckpoints(ctx context.Context) ([]ledger.StreamCheckpoint, error)
	DeadLetterStatusCounts(ctx context.Context) (map[string]int64, error)
	ListDeadLetters(ctx context.Context, query ledger.DeadLetterQuery) (ledger.DeadLetterPage, error)
	AddDeadLetterReview(ctx context.Context, review ledger.DeadLetterReview) (ledger.DeadLetterReview, error)
	ListDeadLetterReviews(ctx context.Context, streamKey, streamID string) ([]ledger.DeadLetterReview, error)
	LatestBrokerNotReady(ctx context.Context, since time.Time) (map[string]ledger.GatewayIssue, error)
}

type TradingDayCalendar interface {
	TradingDayStatus(ctx context.Context, date string) (market.TradingDayStatus, error)
}

type RuntimeObservability struct {
	cfg      config.Config
	store    ObservabilityStore
	calendar TradingDayCalendar
	client   *redis.Client
	now      func() time.Time

	mu       sync.Mutex
	cached   RuntimeSnapshot
	cacheEnd time.Time
}

type RuntimeSnapshot struct {
	GeneratedAt                  time.Time              `json:"generated_at"`
	Environment                  string                 `json:"environment"`
	MonitoringActive             bool                   `json:"monitoring_active"`
	MonitoringReason             string                 `json:"monitoring_reason"`
	TradingDate                  string                 `json:"trading_date"`
	TradingDayKnown              bool                   `json:"trading_day_known"`
	IsTradingDay                 bool                   `json:"is_trading_day"`
	PreviousOrCurrentTradingDate string                 `json:"previous_or_current_trading_date,omitempty"`
	ActionsWriteEnabled          bool                   `json:"actions_write_enabled"`
	Summary                      RuntimeSummary         `json:"summary"`
	Gateways                     []GatewayRuntimeStatus `json:"gateways"`
	Streams                      []StreamRuntimeStatus  `json:"streams"`
	DeadLetters                  map[string]int64       `json:"dead_letters"`
	Errors                       []string               `json:"errors,omitempty"`
}

type RuntimeSummary struct {
	GatewaysOnline     int   `json:"gateways_online"`
	GatewaysAttention  int   `json:"gateways_attention"`
	GatewaysOffHours   int   `json:"gateways_off_hours"`
	StreamsHealthy     int   `json:"streams_healthy"`
	StreamsAttention   int   `json:"streams_attention"`
	TotalLag           int64 `json:"total_lag"`
	PendingDeadLetters int64 `json:"pending_dead_letters"`
}

type GatewayRuntimeStatus struct {
	AccountID               string     `json:"account_id"`
	Alias                   string     `json:"alias,omitempty"`
	BrokerID                string     `json:"broker_id"`
	GatewayID               string     `json:"gateway_id"`
	Status                  string     `json:"status"`
	State                   string     `json:"state,omitempty"`
	StateText               string     `json:"state_text,omitempty"`
	ComponentID             string     `json:"component_id,omitempty"`
	ComponentRole           string     `json:"component_role,omitempty"`
	RedisReady              *bool      `json:"redis_ready,omitempty"`
	BrokerReady             *bool      `json:"broker_ready,omitempty"`
	OrderSnapshotReady      *bool      `json:"order_snapshot_ready,omitempty"`
	AcceptingTradeCommands  *bool      `json:"accepting_trade_commands,omitempty"`
	AcceptingCancelCommands *bool      `json:"accepting_cancel_commands,omitempty"`
	LastHeartbeatAt         *time.Time `json:"last_heartbeat_at,omitempty"`
	HeartbeatAgeSecs        int64      `json:"heartbeat_age_seconds,omitempty"`
	PendingTrades           int64      `json:"pending_trade_count"`
	PendingQueries          int64      `json:"pending_query_count"`
	BrokerNotReady          bool       `json:"broker_not_ready"`
	LastIssueCode           string     `json:"last_issue_code,omitempty"`
	LastIssueMessage        string     `json:"last_issue_message,omitempty"`
	LastIssueAt             *time.Time `json:"last_issue_at,omitempty"`
}

type StreamRuntimeStatus struct {
	AccountID      string         `json:"account_id"`
	BrokerID       string         `json:"broker_id"`
	GatewayID      string         `json:"gateway_id"`
	Role           string         `json:"role"`
	StreamKey      string         `json:"stream_key"`
	Exists         bool           `json:"exists"`
	Length         int64          `json:"length"`
	LatestStreamID string         `json:"latest_stream_id,omitempty"`
	LatestAt       *time.Time     `json:"latest_at,omitempty"`
	CheckpointID   string         `json:"checkpoint_id,omitempty"`
	LastConsumedAt *time.Time     `json:"last_consumed_at,omitempty"`
	Lag            int64          `json:"lag"`
	LagCapped      bool           `json:"lag_capped,omitempty"`
	Status         string         `json:"status"`
	LastError      string         `json:"last_error,omitempty"`
	ProcessedCount int64          `json:"processed_count"`
	ErrorCount     int64          `json:"error_count"`
	CheckpointMeta map[string]any `json:"checkpoint_metadata,omitempty"`
}

type heartbeatPayload struct {
	ComponentID             string `json:"component_id"`
	ComponentRole           string `json:"component_role"`
	State                   string `json:"state"`
	StateText               string `json:"state_text"`
	RedisReady              *bool  `json:"redis_ready"`
	BrokerReady             *bool  `json:"broker_ready"`
	OrderSnapshotReady      *bool  `json:"order_snapshot_ready"`
	AcceptingTradeCommands  *bool  `json:"accepting_trade_commands"`
	AcceptingCancelCommands *bool  `json:"accepting_cancel_commands"`
	PendingTradeCount       int64  `json:"pending_trade_count"`
	PendingQueryCount       int64  `json:"pending_query_count"`
}

type streamProbeCommands struct {
	account    config.AccountRouteConfig
	role       string
	streamKey  string
	checkpoint ledger.StreamCheckpoint
	info       *redis.XInfoStreamCmd
	lag        *redis.Cmd
}

type heartbeatProbeCommand struct {
	account config.AccountRouteConfig
	stream  string
	latest  *redis.XMessageSliceCmd
}

const streamLagLua = `
local cursor = ARGV[1]
local limit = tonumber(ARGV[2])
local count = 0
local batch_size = 250
while count < limit do
    local take = math.min(batch_size + 1, limit - count + 1)
    local entries = redis.call('XRANGE', KEYS[1], cursor, '+', 'COUNT', take)
    if #entries == 0 then
        break
    end
    local advanced = false
    for index = 1, #entries do
        local id = entries[index][1]
        if id ~= cursor then
            count = count + 1
            advanced = true
            if count >= limit then
                return count
            end
        end
    end
    local last_id = entries[#entries][1]
    if last_id == cursor or not advanced then
        break
    end
    cursor = last_id
end
return count
`

func NewRuntimeObservability(cfg config.Config, store ObservabilityStore, calendar TradingDayCalendar) (*RuntimeObservability, error) {
	if store == nil {
		return nil, errors.New("observability store is required")
	}
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		return nil, errors.New("redis.url is required for runtime observability")
	}
	options, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return nil, fmt.Errorf("parse observability redis url: %w", err)
	}
	return &RuntimeObservability{
		cfg:      cfg,
		store:    store,
		calendar: calendar,
		client:   redis.NewClient(options),
		now:      timeutil.Now,
	}, nil
}

func (service *RuntimeObservability) Close() error {
	if service == nil || service.client == nil {
		return nil
	}
	return service.client.Close()
}

func (service *RuntimeObservability) ActionsWriteEnabled() bool {
	return service != nil && service.cfg.Operations.ActionsWriteEnabled
}

func (service *RuntimeObservability) Snapshot(ctx context.Context, force bool) (RuntimeSnapshot, error) {
	if service == nil || service.client == nil || service.store == nil {
		return RuntimeSnapshot{}, errors.New("runtime observability is unavailable")
	}
	now := service.now()
	service.mu.Lock()
	defer service.mu.Unlock()
	if !force && !service.cached.GeneratedAt.IsZero() && now.Before(service.cacheEnd) {
		return service.cached, nil
	}

	snapshot := service.buildSnapshot(ctx, now)
	service.cached = snapshot
	service.cacheEnd = now.Add(time.Duration(service.cfg.Operations.SnapshotCacheSeconds) * time.Second)
	return snapshot, nil
}

func (service *RuntimeObservability) ListDeadLetters(ctx context.Context, query ledger.DeadLetterQuery) (ledger.DeadLetterPage, error) {
	return service.store.ListDeadLetters(ctx, query)
}

func (service *RuntimeObservability) AddDeadLetterReview(ctx context.Context, review ledger.DeadLetterReview) (ledger.DeadLetterReview, error) {
	return service.store.AddDeadLetterReview(ctx, review)
}

func (service *RuntimeObservability) ListDeadLetterReviews(ctx context.Context, streamKey, streamID string) ([]ledger.DeadLetterReview, error) {
	return service.store.ListDeadLetterReviews(ctx, streamKey, streamID)
}

func (service *RuntimeObservability) buildSnapshot(ctx context.Context, now time.Time) RuntimeSnapshot {
	snapshot := RuntimeSnapshot{
		GeneratedAt:         now,
		Environment:         string(service.cfg.Service.Environment),
		TradingDate:         now.Format("2006-01-02"),
		ActionsWriteEnabled: service.cfg.Operations.ActionsWriteEnabled,
		Gateways:            make([]GatewayRuntimeStatus, 0, len(service.cfg.Accounts)),
		Streams:             make([]StreamRuntimeStatus, 0, len(service.cfg.Accounts)*4),
		DeadLetters: map[string]int64{
			"pending":      0,
			"acknowledged": 0,
			"ignored":      0,
			"replayed":     0,
		},
	}
	var tradingDay market.TradingDayStatus
	snapshot.MonitoringActive, snapshot.MonitoringReason, tradingDay = service.monitoringWindow(ctx, now)
	snapshot.TradingDayKnown = tradingDay.IsTradingDayKnown
	snapshot.IsTradingDay = tradingDay.IsTradingDay
	snapshot.PreviousOrCurrentTradingDate = tradingDay.PreviousOrCurrentTradingDate

	checkpoints, err := service.store.ListStreamCheckpoints(ctx)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "checkpoint query failed")
	}
	checkpointByStream := make(map[string]ledger.StreamCheckpoint, len(checkpoints))
	for _, checkpoint := range checkpoints {
		checkpointByStream[checkpoint.StreamKey] = checkpoint
	}

	issues, err := service.store.LatestBrokerNotReady(ctx, now.Add(-24*time.Hour))
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "broker issue query failed")
		issues = map[string]ledger.GatewayIssue{}
	}
	counts, err := service.store.DeadLetterStatusCounts(ctx)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "dead letter count failed")
	} else {
		for status, count := range counts {
			snapshot.DeadLetters[status] = count
		}
	}

	streamCommands, heartbeatCommands := service.queueRedisProbes(ctx, checkpointByStream)
	_, _ = service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index := range streamCommands {
			streamCommands[index].info = pipe.XInfoStream(ctx, streamCommands[index].streamKey)
		}
		for index := range heartbeatCommands {
			heartbeatCommands[index].latest = pipe.XRevRangeN(ctx, heartbeatCommands[index].stream, "+", "-", 1)
		}
		return nil
	})
	service.queueLagProbes(ctx, streamCommands)

	for _, command := range streamCommands {
		status := service.streamStatus(snapshot.MonitoringActive, command)
		snapshot.Streams = append(snapshot.Streams, status)
		if status.Status == "ok" || status.Status == "idle" || status.Status == "off_hours" {
			snapshot.Summary.StreamsHealthy++
		} else {
			snapshot.Summary.StreamsAttention++
		}
		if status.Lag > 0 {
			snapshot.Summary.TotalLag += status.Lag
		}
	}
	for _, command := range heartbeatCommands {
		status := service.gatewayStatus(now, snapshot.MonitoringActive, command, issues[command.account.AccountID])
		snapshot.Gateways = append(snapshot.Gateways, status)
		switch status.Status {
		case "online":
			snapshot.Summary.GatewaysOnline++
		case "off_hours":
			snapshot.Summary.GatewaysOffHours++
		default:
			snapshot.Summary.GatewaysAttention++
		}
	}
	snapshot.Summary.PendingDeadLetters = snapshot.DeadLetters["pending"]
	return snapshot
}

func (service *RuntimeObservability) queueLagProbes(ctx context.Context, commands []streamProbeCommands) {
	limit := service.cfg.Operations.LagCriticalEntries + 1
	if limit < 1 {
		limit = 1
	}
	_, _ = service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index := range commands {
			if commands[index].role == SuffixHB {
				continue
			}
			checkpointID := strings.TrimSpace(commands[index].checkpoint.LastStreamID)
			if checkpointID == "" || commands[index].info == nil {
				continue
			}
			info, err := commands[index].info.Result()
			if err != nil || info.LastGeneratedID == checkpointID {
				continue
			}
			commands[index].lag = pipe.Eval(ctx, streamLagLua, []string{commands[index].streamKey}, checkpointID, limit)
		}
		return nil
	})
}

func (service *RuntimeObservability) queueRedisProbes(
	_ context.Context,
	checkpoints map[string]ledger.StreamCheckpoint,
) ([]streamProbeCommands, []heartbeatProbeCommand) {
	streamCommands := make([]streamProbeCommands, 0, len(service.cfg.Accounts)*4)
	heartbeatCommands := make([]heartbeatProbeCommand, 0, len(service.cfg.Accounts))
	for _, account := range service.cfg.Accounts {
		if !account.Enabled {
			continue
		}
		streams := NewStreams(account.StreamPrefix)
		for _, role := range []string{SuffixReply, SuffixEvent, SuffixHB, SuffixDLQ} {
			streamKey := streamNameForRole(streams, role)
			streamCommands = append(streamCommands, streamProbeCommands{
				account:    account,
				role:       role,
				streamKey:  streamKey,
				checkpoint: checkpoints[streamKey],
			})
		}
		heartbeatCommands = append(heartbeatCommands, heartbeatProbeCommand{
			account: account,
			stream:  streams.HB,
		})
	}
	return streamCommands, heartbeatCommands
}

func (service *RuntimeObservability) monitoringWindow(ctx context.Context, now time.Time) (bool, string, market.TradingDayStatus) {
	if service.calendar == nil {
		return false, "calendar_unavailable", market.TradingDayStatus{}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	status, err := service.calendar.TradingDayStatus(checkCtx, now.Format("2006-01-02"))
	if err != nil || !status.IsTradingDayKnown {
		return false, "calendar_unavailable", status
	}
	if !status.IsTradingDay {
		return false, "non_trading_day", status
	}
	hour, minute, _ := now.Clock()
	minutes := hour*60 + minute
	if minutes < 8*60+55 || minutes > 15*60+15 {
		return false, "off_hours", status
	}
	return true, "trading_session", status
}

func (service *RuntimeObservability) streamStatus(monitoringActive bool, command streamProbeCommands) StreamRuntimeStatus {
	checkpoint := command.checkpoint
	status := StreamRuntimeStatus{
		AccountID:      command.account.AccountID,
		BrokerID:       command.account.BrokerID,
		GatewayID:      command.account.GatewayID,
		Role:           command.role,
		StreamKey:      command.streamKey,
		CheckpointID:   checkpoint.LastStreamID,
		Lag:            -1,
		Status:         "unknown",
		LastError:      checkpoint.LastError,
		ProcessedCount: checkpoint.ProcessedCount,
		ErrorCount:     checkpoint.ErrorCount,
		CheckpointMeta: checkpoint.Metadata,
	}
	if !checkpoint.LastProcessedAt.IsZero() {
		lastConsumedAt := checkpoint.LastProcessedAt
		status.LastConsumedAt = &lastConsumedAt
	}
	if command.info == nil {
		status.LastError = firstNonEmpty(status.LastError, "redis probe unavailable")
		return status
	}
	info, err := command.info.Result()
	if err != nil {
		if isMissingStream(err) {
			status.Lag = 0
			status.Status = "idle"
			return status
		}
		status.LastError = firstNonEmpty(status.LastError, "redis stream info failed")
		return status
	}
	status.Exists = true
	status.Length = info.Length
	status.LatestStreamID = info.LastGeneratedID
	if latestAt := producedAtFromStreamID(info.LastGeneratedID); !latestAt.IsZero() {
		status.LatestAt = &latestAt
	}
	checkpointID := strings.TrimSpace(checkpoint.LastStreamID)
	lagLimit := service.cfg.Operations.LagCriticalEntries + 1
	switch {
	case command.role == SuffixHB:
		status.Lag = 0
	case checkpointID == "":
		status.Lag = info.Length
		if lagLimit > 0 && status.Lag >= lagLimit {
			status.Lag = lagLimit
			status.LagCapped = true
		}
	case info.LastGeneratedID == checkpointID:
		status.Lag = 0
	case command.lag != nil:
		if lag, err := command.lag.Int64(); err == nil {
			status.Lag = lag
			if lagLimit > 0 && status.Lag >= lagLimit {
				status.LagCapped = true
			}
		}
	case info.Length == 0:
		status.Lag = 0
	}

	switch {
	case status.LastError != "":
		status.Status = "error"
	case status.Lag < 0:
		status.Status = "unknown"
	case status.Lag >= service.cfg.Operations.LagCriticalEntries:
		status.Status = "critical"
	case status.Lag >= service.cfg.Operations.LagWarningEntries:
		status.Status = "warning"
	case status.Lag == 0 && status.LatestAt == nil:
		status.Status = "idle"
	default:
		status.Status = "ok"
	}
	if !monitoringActive {
		status.Status = "off_hours"
	}
	return status
}

func (service *RuntimeObservability) gatewayStatus(
	now time.Time,
	monitoringActive bool,
	command heartbeatProbeCommand,
	issue ledger.GatewayIssue,
) GatewayRuntimeStatus {
	status := GatewayRuntimeStatus{
		AccountID: command.account.AccountID,
		Alias:     command.account.Alias,
		BrokerID:  command.account.BrokerID,
		GatewayID: command.account.GatewayID,
		Status:    "unknown",
	}
	if command.latest != nil {
		messages, err := command.latest.Result()
		if err == nil && len(messages) > 0 {
			envelope, decodeErr := DecodeEntry(command.stream, messages[0].ID, messages[0].Values)
			if decodeErr == nil {
				var payload heartbeatPayload
				_ = json.Unmarshal(envelope.Payload, &payload)
				status.ComponentID = payload.ComponentID
				status.ComponentRole = payload.ComponentRole
				status.State = payload.State
				status.StateText = payload.StateText
				status.RedisReady = payload.RedisReady
				status.BrokerReady = payload.BrokerReady
				status.OrderSnapshotReady = payload.OrderSnapshotReady
				status.AcceptingTradeCommands = payload.AcceptingTradeCommands
				status.AcceptingCancelCommands = payload.AcceptingCancelCommands
				status.PendingTrades = payload.PendingTradeCount
				status.PendingQueries = payload.PendingQueryCount
				if !envelope.ProducedAt.IsZero() {
					lastHeartbeatAt := envelope.ProducedAt
					status.LastHeartbeatAt = &lastHeartbeatAt
					status.HeartbeatAgeSecs = maxInt64(0, int64(now.Sub(lastHeartbeatAt).Seconds()))
				}
			}
		}
	}
	if !issue.ReceivedAt.IsZero() {
		status.LastIssueCode = issue.Code
		status.LastIssueMessage = issue.Message
		lastIssueAt := issue.ReceivedAt
		status.LastIssueAt = &lastIssueAt
		status.BrokerNotReady = monitoringActive && now.Sub(issue.ReceivedAt) <= 5*time.Minute
	}
	if !monitoringActive {
		status.Status = "off_hours"
		return status
	}
	state := strings.ToUpper(strings.TrimSpace(status.State))
	stateText := strings.ToLower(strings.TrimSpace(status.StateText))
	switch {
	case status.BrokerNotReady || boolIsFalse(status.BrokerReady) || stateText == "broker_not_ready":
		status.Status = "broker_not_ready"
	case strings.Contains(stateText, "reconnect") || strings.Contains(state, "RECONNECT"):
		status.Status = "reconnecting"
	case status.LastHeartbeatAt == nil:
		status.Status = "missing"
	case status.HeartbeatAgeSecs > int64(service.cfg.Operations.HeartbeatStaleSeconds):
		status.Status = "stale"
	case state == "UP" &&
		!boolIsFalse(status.RedisReady) &&
		!boolIsFalse(status.OrderSnapshotReady) &&
		!boolIsFalse(status.AcceptingTradeCommands) &&
		!boolIsFalse(status.AcceptingCancelCommands):
		status.Status = "online"
	default:
		status.Status = "degraded"
	}
	return status
}

func boolIsFalse(value *bool) bool {
	return value != nil && !*value
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
