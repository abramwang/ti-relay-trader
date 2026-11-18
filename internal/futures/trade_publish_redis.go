package futures

import (
	"context"
	"encoding/json"
	"math/big"
	"time"
)

// TestTradePublisher is never constructed by the public stock API or worker.
// Each publication is one explicitly approved TEST command; day mode persists
// independent order reservations while legacy mode retains its single budget.
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
	h, err := DecodeHeartbeat(rows[0].Raw, p.base.scope.Route)
	if err != nil || !h.assessmentNativeScopeMatches(p.base.scope) {
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
		maxAge := 5 * time.Second
		if a.Market.PriceSource == "oc_test_native_lower_limit.v1" {
			s := h.Assessment
			if route.Env != "test" || route.BrokerID != "ctp_4900" || route.AccountID != "300052141" || a.PermissionProfile != "test_day_relay.v1" || s == nil || s.Limits == nil || s.Static == nil || s.ConstraintSymbol != "rb2701" || s.Exchange != "SHFE" || s.Limits.CompletedAt != a.Market.ObservedAt || !equalDecimal(string(s.Limits.Lower), string(a.Market.LowerLimit)) || !equalDecimal(string(s.Limits.Upper), string(a.Market.UpperLimit)) || !equalDecimal(string(s.Static.PriceTick), string(a.Market.PriceTick)) {
				return queryPublishProof{}, ErrQueryPublishGate
			}
			maxAge = 300 * time.Second
		}
		if err != nil || observed.After(now.Add(2*time.Second)) || !now.Before(observed.Add(maxAge)) {
			return queryPublishProof{}, ErrQueryPublishGate
		}
		if observed.Add(maxAge).Before(expires) {
			expires = observed.Add(maxAge)
		}
	}
	if assessment := h.Assessment; assessment != nil {
		if a.AssessmentID != assessment.AssessmentID || !assessment.windowOpen(now) || assessment.PermissionProfile != a.PermissionProfile {
			return queryPublishProof{}, ErrQueryPublishGate
		}
		if a.PermissionProfile == "test_day_relay.v1" && (assessment.OrderSnapshotScope != "full_native_current_session" || !assessment.NativeOrderSnapshotReady || !assessment.LocalOrdersOnly) {
			return queryPublishProof{}, ErrQueryPublishGate
		}
		if assessment.ExpiresAt.Before(expires) {
			expires = assessment.ExpiresAt
		}
		if a.CancelReceiptID == 0 && a.PermissionProfile == "" {
			if a.Market.SourceInstrumentID != assessment.Symbol || a.Market.ExchangeID != assessment.Exchange || assessment.Static == nil || assessment.Limits == nil || assessment.Static.MinLimitLots > 1 || assessment.Static.MaxLimitLots < 1 || !equalDecimal(string(assessment.Static.PriceTick), string(a.Market.PriceTick)) {
				return queryPublishProof{}, ErrQueryPublishGate
			}
			captured, _ := time.Parse(time.RFC3339Nano, assessment.Static.CapturedAt)
			start, _ := time.Parse(time.RFC3339Nano, assessment.Limits.StartedAt)
			end, _ := time.Parse(time.RFC3339Nano, assessment.Limits.CompletedAt)
			if captured.After(now.Add(2*time.Second)) || !now.Before(captured.Add(300*time.Second)) || start.After(now.Add(2*time.Second)) || end.After(now.Add(2*time.Second)) || !now.Before(start.Add(5*time.Second)) {
				return queryPublishProof{}, ErrQueryPublishGate
			}
			if start.Add(5 * time.Second).Before(expires) {
				expires = start.Add(5 * time.Second)
			}
			if captured.Add(300 * time.Second).Before(expires) {
				expires = captured.Add(300 * time.Second)
			}
		}
	} else if a.PermissionProfile != "" {
		return queryPublishProof{}, ErrQueryPublishGate
	}
	return queryPublishProof{EntryID: row.EntryID, Body: row.Raw, CheckedAt: now, ExpiresAt: expires, ProducedAt: h.ProducedAt}, nil
}

func assessmentNativePrice(raw []byte, scope CommandScope, h Heartbeat) error {
	if !h.assessmentNativeScopeMatches(scope) {
		return ErrQueryPublishGate
	}
	a := h.Assessment
	if a == nil {
		return nil
	}
	c, err := DecodeCommand(raw, scope, "cmd.trade")
	if err != nil {
		return err
	}
	if c.Action == "order.cancel" {
		return nil
	}
	// OC day mode validates this command's contract/limits before its SDK call.
	// A cached constraint_symbol may describe a previous command, so it must
	// not be substituted for the current command's authoritative constraints.
	if a.PermissionProfile == "test_day_relay.v1" {
		if len(c.Orders) != 1 || c.Orders[0].Exchange != "SHFE" {
			return ErrQueryPublishGate
		}
		return nil
	}
	if len(c.Orders) != 1 || a.Limits == nil || a.Static == nil {
		return ErrQueryPublishGate
	}
	o := c.Orders[0]
	if o.Symbol != a.Symbol || o.Exchange != a.Exchange || int64(o.OrderQty) < a.Static.MinLimitLots || int64(o.OrderQty) > a.Static.MaxLimitLots {
		return ErrQueryPublishGate
	}
	p, _ := new(big.Rat).SetString(string(o.LimitPrice))
	tick, _ := new(big.Rat).SetString(string(a.Static.PriceTick))
	low, _ := new(big.Rat).SetString(string(a.Limits.Lower))
	high, _ := new(big.Rat).SetString(string(a.Limits.Upper))
	if p == nil || tick == nil || low == nil || high == nil || p.Cmp(low) < 0 || p.Cmp(high) > 0 || !new(big.Rat).Quo(p, tick).IsInt() {
		return ErrQueryPublishGate
	}
	return nil
}

// appendQuery implements the shared one-attempt transport interface; the
// physical destination here is always cmd.trade, never cmd.query.
func (p *TestTradePublisher) appendQuery(ctx context.Context, proof queryPublishProof, raw []byte) (string, bool, error) {
	if commandDigest(raw) != p.approval.CommandSHA256 {
		return "", true, nil
	}
	b := p.base
	h, err := DecodeHeartbeat(proof.Body, b.scope.Route)
	if err != nil || assessmentNativePrice(raw, b.scope, h) != nil {
		return "", true, nil
	}
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
