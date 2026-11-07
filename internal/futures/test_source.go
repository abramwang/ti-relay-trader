package futures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestSourceConfig is explicit operator configuration, never active-config
// discovery. DeploymentID must change for an intentional restore/replacement.
type TestSourceConfig struct {
	Scope        CommandScope
	DeploymentID string
	RedisURL     string `json:"-"`
}

// TestStreamSource owns a dedicated RESP2 client and a fixed source/route pair.
// Its fingerprint binds configuration, not the server's physical identity or OC.
type TestStreamSource struct {
	scope    CommandScope
	sourceID string
	reader   *RedisStreamReader
}

func NewTestStreamSource(c TestSourceConfig) (*TestStreamSource, error) {
	if c.Scope.validate() != nil || c.Scope.Route.Env != "test" ||
		!(c.Scope.Route.BrokerID == "ctp" || strings.HasPrefix(c.Scope.Route.BrokerID, "ctp_")) ||
		!validID(c.DeploymentID) || len(c.DeploymentID) > 128 {
		return nil, ErrStreamIdentity
	}
	return newTestSource(c)
}

func emptyNativeScope(s CommandScope) bool {
	return s.NativeBrokerID == "" && s.InvestorID == "" && s.UserID == ""
}

// Transport-only hb collection must not invent native login credentials.
func NewTestHeartbeatSource(c TestSourceConfig) (*TestStreamSource, error) {
	if !emptyNativeScope(c.Scope) || !validID(c.DeploymentID) || len(c.DeploymentID) > 128 {
		return nil, ErrStreamIdentity
	}
	if _, err := (HeartbeatInspectionRequest{Route: c.Scope.Route, After: "0-0", Limit: 1}).Stream(); err != nil {
		return nil, err
	}
	return newTestSource(c)
}

func newTestSource(c TestSourceConfig) (*TestStreamSource, error) {
	options, err := redis.ParseURL(c.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid TEST Redis URL")
	}
	if options.TLSConfig != nil && options.TLSConfig.InsecureSkipVerify {
		return nil, fmt.Errorf("TEST source cannot disable TLS verification")
	}
	identity, _ := json.Marshal(struct {
		Deployment, Network, Address string
		Database                     int
		TLS                          bool
	}{c.DeploymentID, options.Network, options.Addr, options.DB, options.TLSConfig != nil})
	digest := sha256.Sum256(identity)
	options.Protocol = 2
	options.MaxRetries = -1
	if emptyNativeScope(c.Scope) {
		options.DialerRetries = 1
		options.PoolSize, options.MaxActiveConns, options.MinIdleConns, options.MaxIdleConns = 1, 1, 0, 1
	}
	options.DialTimeout, options.ReadTimeout, options.WriteTimeout = 2*time.Second, 2*time.Second, 2*time.Second
	options.ContextTimeoutEnabled = true
	options.ClientName = "relay-ctp-test-consumer"
	client := redis.NewClient(options)
	reader, err := NewRedisStreamReader(client)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &TestStreamSource{c.Scope, "ctp-test-source-v1:" + hex.EncodeToString(digest[:]), reader}, nil
}

func (s *TestStreamSource) String() string { return s.sourceID }
func (s *TestStreamSource) Close() error   { return s.reader.client.Close() }

func (s *TestStreamSource) StreamConfig(role string) StreamConfig {
	return StreamConfig{s.scope, role, s.sourceID}
}

func (s *TestStreamSource) matches(h StreamHandle) bool {
	return s != nil && (!emptyNativeScope(s.scope) || h.config.Role == "hb") && h.config.Scope == s.scope && h.config.SourceID == s.sourceID && oneOf(h.config.Role, "event", "reply", "hb")
}

func (s *TestStreamSource) ReadOnce(ctx context.Context, store *StreamStore, h StreamHandle, limit int) (StreamCapture, error) {
	if !s.matches(h) || store == nil {
		return StreamCapture{}, ErrStreamIdentity
	}
	return store.ReadOnce(ctx, h, s.reader, limit)
}
