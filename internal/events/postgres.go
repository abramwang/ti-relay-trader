package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"ti-relay-trader/internal/timeutil"
)

const (
	PostgresChannel          = "relay_ledger_events_v1"
	PostgresEventSchema      = "relay.ledger_event.v1"
	postgresNotifyMaxPayload = 7900
)

type postgresEventEnvelope struct {
	Schema string `json:"schema"`
	Event  Event  `json:"event"`
}

type SQLExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type PostgresNotifier struct {
	exec SQLExecutor
}

func NewPostgresNotifier(exec SQLExecutor) *PostgresNotifier {
	return &PostgresNotifier{exec: exec}
}

func (notifier *PostgresNotifier) Notify(ctx context.Context, event Event) error {
	if notifier == nil || notifier.exec == nil {
		return errors.New("postgres event notifier is unavailable")
	}
	payload, err := encodePostgresEvent(event)
	if err != nil {
		return err
	}
	_, err = notifier.exec.ExecContext(ctx, "SELECT pg_notify($1, $2)", PostgresChannel, payload)
	return err
}

type PostgresListener struct {
	dsn    string
	hub    *Hub
	logger *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.RWMutex
	ready   bool
	lastErr error
}

func StartPostgresListener(parent context.Context, dsn string, hub *Hub, logger *slog.Logger) *PostgresListener {
	ctx, cancel := context.WithCancel(parent)
	listener := &PostgresListener{
		dsn:    strings.TrimSpace(dsn),
		hub:    hub,
		logger: logger,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if listener.logger == nil {
		listener.logger = slog.Default()
	}
	go listener.run(ctx)
	return listener
}

func (listener *PostgresListener) Close() {
	if listener == nil {
		return
	}
	listener.cancel()
	<-listener.done
}

func (listener *PostgresListener) Health(context.Context) error {
	if listener == nil {
		return errors.New("postgres event listener is unavailable")
	}
	listener.mu.RLock()
	defer listener.mu.RUnlock()
	if listener.ready {
		return nil
	}
	if listener.lastErr != nil {
		return fmt.Errorf("postgres event listener is not ready: %w", listener.lastErr)
	}
	return errors.New("postgres event listener is starting")
}

func (listener *PostgresListener) run(ctx context.Context) {
	defer close(listener.done)
	if listener.dsn == "" {
		listener.setState(false, errors.New("database DSN is empty"))
		return
	}
	for ctx.Err() == nil {
		if err := listener.listen(ctx); err != nil && ctx.Err() == nil {
			listener.setState(false, err)
			listener.logger.Warn("relay_postgres_event_listener_reconnecting", "error", err)
		}
		if !waitForRetry(ctx, time.Second) {
			break
		}
	}
	listener.setState(false, ctx.Err())
}

func (listener *PostgresListener) listen(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, listener.dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN "+PostgresChannel); err != nil {
		return err
	}
	listener.setState(true, nil)
	listener.logger.Info("relay_postgres_event_listener_ready", "channel", PostgresChannel)
	for ctx.Err() == nil {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		event, err := decodePostgresEvent(notification.Payload)
		if err != nil {
			listener.logger.Warn("relay_postgres_event_invalid", "error", err)
			continue
		}
		listener.hub.Publish(event)
	}
	return ctx.Err()
}

func (listener *PostgresListener) setState(ready bool, err error) {
	listener.mu.Lock()
	listener.ready = ready
	listener.lastErr = err
	listener.mu.Unlock()
}

func encodePostgresEvent(event Event) (string, error) {
	if strings.TrimSpace(event.Type) == "" {
		return "", errors.New("event type is required")
	}
	if event.Time.IsZero() {
		event.Time = timeutil.Now()
	} else {
		event.Time = timeutil.InBusinessLocation(event.Time)
	}
	payload, err := json.Marshal(postgresEventEnvelope{
		Schema: PostgresEventSchema,
		Event:  event,
	})
	if err != nil {
		return "", err
	}
	if len(payload) > postgresNotifyMaxPayload {
		return "", fmt.Errorf("event payload is %d bytes, limit is %d", len(payload), postgresNotifyMaxPayload)
	}
	return string(payload), nil
}

func decodePostgresEvent(payload string) (Event, error) {
	var envelope postgresEventEnvelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return Event{}, err
	}
	if envelope.Schema == "" {
		// Accept the pre-envelope payload during independent API/worker rollouts.
		var legacy Event
		if err := json.Unmarshal([]byte(payload), &legacy); err != nil {
			return Event{}, err
		}
		envelope.Event = legacy
	} else if envelope.Schema != PostgresEventSchema {
		return Event{}, fmt.Errorf("unsupported postgres event schema %q", envelope.Schema)
	}
	event := envelope.Event
	if strings.TrimSpace(event.Type) == "" {
		return Event{}, errors.New("event type is required")
	}
	return event, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
