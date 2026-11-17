package futures

import (
	"context"
	"encoding/json"
	"time"
)

// TestTradePublisher is never constructed by the public stock API or worker.
// It publishes only an explicitly approved single TEST assessment command.
type TestTradePublisher struct {
	base     *TestQueryPublisher
	approval TestTradeApproval
}

func NewTestTradePublisher(c TestSourceConfig, t TestQueryTarget, a TestTradeApproval) (*TestTradePublisher, error) {
	raw, _ := json.Marshal(a)
	if _, err := DecodeTestTradeApproval(raw); err != nil {
		return nil, err
	}
	p, err := NewTestQueryPublisher(c, t)
	if err != nil {
		return nil, err
	}
	return &TestTradePublisher{p, a}, nil
}
func (p *TestTradePublisher) Close() error { return p.base.Close() }

func (p *TestTradePublisher) inspect(ctx context.Context) (queryPublishProof, error) {
	bad := func() (queryPublishProof, error) { return queryPublishProof{}, ErrQueryPublishGate }
	if err := p.base.verifyTransportStream(ctx, "cmd.trade"); err != nil {
		return bad()
	}
	key := p.base.prefix() + ":hb"
	raw, err := p.base.client.Do(ctx, "XREVRANGE", key, "+", "-", "COUNT", 1).Result()
	if err != nil {
		return bad()
	}
	rows, err := decodeStreamRead(key, "0-0", 1, []any{[]any{key, raw}})
	if err != nil || len(rows) != 1 {
		return bad()
	}
	proof, err := testTradeHeartbeatProof(rows[0], p.base.scope.Route, p.base.target, p.approval, time.Now())
	if err != nil {
		return bad()
	}
	return proof, nil
}

func testTradeHeartbeatProof(row StreamInput, route QueryRoute, target TestQueryTarget, a TestTradeApproval, now time.Time) (queryPublishProof, error) {
	h, err := DecodeHeartbeat(row.Raw, route)
	if err != nil || h.ReadOnly != nil || h.Recovery != nil || !h.RedisReady || !h.BrokerReady || !h.OrderSnapshotReady || h.CounterSessionID != target.CounterSessionID || h.TradeDate != target.TradeDate {
		return queryPublishProof{}, ErrQueryPublishGate
	}
	var envelope struct {
		Adapter map[string]json.RawMessage `json:"adapter_context"`
	}
	if json.Unmarshal(row.Raw, &envelope) != nil {
		return queryPublishProof{}, ErrQueryPublishGate
	}
	g, err := nullableInteger(envelope.Adapter["connection_generation"])
	if err != nil || g == nil || *g != target.ConnectionGeneration || !literal(envelope.Adapter["transport_recovery_clear"], "true") {
		return queryPublishProof{}, ErrQueryPublishGate
	}
	if a.CancelReceiptID > 0 {
		if !h.AcceptingCancelCommands {
			return queryPublishProof{}, ErrQueryPublishGate
		}
	} else if !h.SettlementConfirmed || !h.AcceptingTradeCommands || a.Market == nil {
		return queryPublishProof{}, ErrQueryPublishGate
	}
	expires, err := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	if err != nil || !now.Before(expires) || expires.After(now.Add(time.Minute)) || h.ProducedAt.After(now.Add(2*time.Second)) || !now.Before(h.ProducedAt.Add(5*time.Second)) {
		return queryPublishProof{}, ErrQueryPublishGate
	}
	if h.ProducedAt.Add(5 * time.Second).Before(expires) {
		expires = h.ProducedAt.Add(5 * time.Second)
	}
	if a.CancelReceiptID == 0 {
		observed, err := time.Parse(time.RFC3339Nano, a.Market.ObservedAt)
		if err != nil || observed.After(now.Add(2*time.Second)) || !now.Before(observed.Add(5*time.Second)) {
			return queryPublishProof{}, ErrQueryPublishGate
		}
		if observed.Add(5 * time.Second).Before(expires) {
			expires = observed.Add(5 * time.Second)
		}
	}
	return queryPublishProof{EntryID: row.EntryID, Body: row.Raw, CheckedAt: now, ExpiresAt: expires, ProducedAt: h.ProducedAt}, nil
}

// appendQuery implements the shared one-attempt transport interface; the
// physical destination here is always cmd.trade, never cmd.query.
func (p *TestTradePublisher) appendQuery(ctx context.Context, proof queryPublishProof, raw []byte) (string, bool, error) {
	if commandDigest(raw) != p.approval.CommandSHA256 {
		return "", true, nil
	}
	b := p.base
	result, err := b.client.Eval(ctx, testQueryAppendScript, []string{b.prefix() + ":cmd.trade:transport-epoch", b.prefix() + ":hb", b.prefix() + ":cmd.trade"}, b.target.ServerRunID, b.target.Epoch, b.target.Group, proof.EntryID, string(proof.Body), proof.ExpiresAt.UnixMilli(), proof.ProducedAt.UnixMilli(), string(raw)).Result()
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
		return "", false, ErrQueryPublishUnknown
	}
	return value, false, nil
}
