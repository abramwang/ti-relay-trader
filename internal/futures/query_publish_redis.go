package futures

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestQueryPublisher owns a dedicated no-retry client. Construction is offline.
type TestQueryPublisher struct {
	scope  CommandScope
	target TestQueryTarget
	client *redis.Client
}

func NewTestQueryPublisher(c TestSourceConfig, t TestQueryTarget) (*TestQueryPublisher, error) {
	if t.Validate() != nil {
		return nil, ErrStreamIdentity
	}
	s, err := NewTestStreamSource(c)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if s.String() != t.SourceID {
		return nil, ErrStreamIdentity
	}
	opts := *s.reader.client.Options()
	opts.Protocol, opts.MaxRetries, opts.DialerRetries = 2, -1, 1
	opts.PoolSize, opts.MaxActiveConns, opts.MinIdleConns, opts.MaxIdleConns = 1, 1, 0, 1
	opts.ContextTimeoutEnabled = true
	opts.ClientName = "relay-ctp-test-query-publisher"
	return &TestQueryPublisher{c.Scope, t, redis.NewClient(&opts)}, nil
}

func (p *TestQueryPublisher) Close() error { return p.client.Close() }
func (p *TestQueryPublisher) prefix() string {
	return strings.TrimSuffix(queryReplyStream(p.scope.Route), ":reply")
}

func (p *TestQueryPublisher) inspect(ctx context.Context) (queryPublishProof, error) {
	bad := func() (queryPublishProof, error) { return queryPublishProof{}, ErrQueryPublishGate }
	if err := p.verifyTransport(ctx); err != nil {
		return bad()
	}
	key := p.prefix() + ":hb"
	raw, err := p.client.Do(ctx, "XREVRANGE", key, "+", "-", "COUNT", 1).Result()
	if err != nil {
		return bad()
	}
	rows, err := decodeStreamRead(key, "0-0", 1, []any{[]any{key, raw}})
	if err != nil || len(rows) != 1 {
		return bad()
	}
	return testQueryHeartbeatProof(rows[0], p.scope, p.target, time.Now())
}

func testQueryHeartbeatProof(row StreamInput, scope CommandScope, target TestQueryTarget, now time.Time) (queryPublishProof, error) {
	bad := func() (queryPublishProof, error) { return queryPublishProof{}, ErrQueryPublishGate }
	h, err := DecodeHeartbeat(row.Raw, scope.Route)
	g, ready := h.queryCollection()
	if err != nil || !h.readOnlyNativeScopeMatches(scope) || !h.assessmentNativeScopeMatches(scope) || !ready || g == nil || *g != target.ConnectionGeneration || h.CounterSessionID != target.CounterSessionID || h.TradeDate != target.TradeDate || h.ProducedAt.After(now.Add(2*time.Second)) || !now.Before(h.ProducedAt.Add(30*time.Second)) {
		return bad()
	}
	expires := h.ProducedAt.Add(30 * time.Second)
	if expires.After(now.Add(30 * time.Second)) {
		expires = now.Add(30 * time.Second)
	}
	if h.Assessment != nil {
		// Query collection does not consume a trade budget or require a ready
		// order snapshot: these queries are how that snapshot is obtained.
		if !h.Assessment.windowOpen(now) {
			return bad()
		}
		if h.Assessment.ExpiresAt.Before(expires) {
			expires = h.Assessment.ExpiresAt
		}
		if h.ProducedAt.Add(5 * time.Second).Before(expires) {
			expires = h.ProducedAt.Add(5 * time.Second)
		}
		if !now.Before(expires) {
			return bad()
		}
	}
	return queryPublishProof{EntryID: row.EntryID, Body: row.Raw, CheckedAt: now, ExpiresAt: expires, ProducedAt: h.ProducedAt}, nil
}

func (p *TestQueryPublisher) verifyTransport(ctx context.Context) error {
	return p.verifyTransportStream(ctx, "cmd.query")
}

func (p *TestQueryPublisher) verifyTransportStream(ctx context.Context, suffix string) error {
	if !oneOf(suffix, "cmd.query", "cmd.trade") {
		return ErrStreamIdentity
	}
	info, err := p.client.Info(ctx, "server").Result()
	if err != nil || !strings.Contains(info, "\r\nrun_id:"+p.target.ServerRunID+"\r\n") {
		return ErrQueryPublishGate
	}
	epoch, err := p.client.Get(ctx, p.prefix()+":cmd.trade:transport-epoch").Result()
	if err != nil || epoch != p.target.Epoch {
		return ErrQueryPublishGate
	}
	groups, err := p.client.XInfoGroups(ctx, p.prefix()+":"+suffix).Result()
	if err != nil || len(groups) != 1 || groups[0].Name != p.target.Group {
		return ErrQueryPublishGate
	}
	return nil
}

// Recheck the exact latest heartbeat, epoch, run_id, group and age atomically
// with XADD. A changing gate returns before any append. Never creates a group.
const testQueryAppendScript = `
local info=redis.call('INFO','server')
if not string.find(info,'\r\nrun_id:'..ARGV[1]..'\r\n',1,true) then return {0,'gate_changed'} end
if redis.call('GET',KEYS[1]) ~= ARGV[2] then return {0,'gate_changed'} end
local groups=redis.call('XINFO','GROUPS',KEYS[3])
if #groups ~= 1 then return {0,'gate_changed'} end
local name=nil
for i=1,#groups[1],2 do if groups[1][i]=='name' then name=groups[1][i+1] end end
if name ~= ARGV[3] then return {0,'gate_changed'} end
local tail=redis.call('XREVRANGE',KEYS[2],'+','-','COUNT',1)
if #tail~=1 or tail[1][1]~=ARGV[4] or #tail[1][2]~=2 or tail[1][2][1]~='body' or tail[1][2][2]~=ARGV[5] then return {0,'gate_changed'} end
local now=redis.call('TIME')
local ms=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000)
if ms>=tonumber(ARGV[6]) or ms<tonumber(ARGV[7])-2000 then return {0,'gate_changed'} end
return {1,redis.call('XADD',KEYS[3],'*','body',ARGV[8])}
`

func (p *TestQueryPublisher) appendQuery(ctx context.Context, proof queryPublishProof, raw []byte) (string, bool, error) {
	// EVAL directly: no script-cache fallback, no XADD retry on uncertain result.
	result, err := p.client.Eval(ctx, testQueryAppendScript, []string{p.prefix() + ":cmd.trade:transport-epoch", p.prefix() + ":hb", p.prefix() + ":cmd.query"}, p.target.ServerRunID, p.target.Epoch, p.target.Group, proof.EntryID, string(proof.Body), proof.ExpiresAt.UnixMilli(), proof.ProducedAt.UnixMilli(), string(raw)).Result()
	if err != nil {
		return "", false, ErrQueryPublishUnknown
	}
	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return "", false, ErrQueryPublishUnknown
	}
	code, ok := values[0].(int64)
	if !ok {
		return "", false, ErrQueryPublishUnknown
	}
	value, ok := values[1].(string)
	if !ok {
		return "", false, ErrQueryPublishUnknown
	}
	if code == 0 && value == "gate_changed" {
		return "", true, nil
	}
	if code != 1 {
		return "", false, errors.New("invalid query append result")
	}
	return value, false, nil
}
