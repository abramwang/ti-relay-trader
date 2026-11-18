package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"strings"
)

// Local event observations do not certify the incomplete full-account query.
func (r *TerminalReader) localOrders(ctx context.Context, a TerminalAccountConfig) ([]OrderRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT i.body,i.body_sha256,c.body,c.body_sha256,c.scope_json,p.target_json,s.config_json FROM futures_stream_inbox i JOIN futures_stream_receivers s USING(stream_key) JOIN futures_commands c ON convert_from(c.body,'UTF8')::jsonb->>'message_id'=convert_from(i.body,'UTF8')::jsonb->>'origin_message_id' JOIN futures_test_trade_publications p USING(command_id) WHERE i.stream_key=$1 AND c.route_key=$2 AND p.action='order.submit' AND p.state='published' AND i.state<>'quarantined' ORDER BY i.receipt_id LIMIT 10001`, strings.TrimSuffix(queryReplyStream(a.Scope.Route), "reply")+"event", string(commandRouteBytes(a.Scope.Route)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]OrderRow{}
	count := 0
	for rows.Next() {
		count++
		if count > 10000 {
			return nil, ErrAuditCapacity
		}
		var raw, digest, cmdRaw, scopeRaw, targetRaw, sourceRaw []byte
		var cmdDigest string
		if err = rows.Scan(&raw, &digest, &cmdRaw, &cmdDigest, &scopeRaw, &targetRaw, &sourceRaw); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if !bytes.Equal(sum[:], digest) || commandDigest(cmdRaw) != cmdDigest {
			return nil, ErrStreamIdentity
		}
		var scope CommandScope
		var binding struct {
			Target TestQueryTarget `json:"target"`
		}
		if json.Unmarshal(scopeRaw, &scope) != nil || scope != a.Scope || json.Unmarshal(targetRaw, &binding) != nil || binding.Target.Validate() != nil {
			return nil, ErrStreamIdentity
		}
		var source StreamConfig
		if json.Unmarshal(sourceRaw, &source) != nil || source.Scope != scope || source.Role != "event" || source.SourceID != binding.Target.SourceID || (a.SourceID != "" && source.SourceID != a.SourceID) {
			return nil, ErrStreamIdentity
		}
		c, err := DecodeCommand(cmdRaw, scope, "cmd.trade")
		if err != nil {
			return nil, err
		}
		event, err := DecodeTradeEvent(raw, EventContext{Scope: TradeScope{Route: scope.Route, NativeBrokerID: scope.NativeBrokerID, InvestorID: scope.InvestorID, TradeDate: binding.Target.TradeDate}, ExpectedOrigin: &c.MessageID})
		if err != nil {
			return nil, err
		}
		if event.Order == nil {
			continue
		}
		o := *event.Order
		if len(c.Orders) != 1 || o.CounterSessionID != binding.Target.CounterSessionID {
			return nil, ErrStreamIdentity
		}
		original := c.Orders[0]
		if o.GatewayOrderID != original.GatewayOrderID || o.Symbol != original.Symbol || o.Exchange != original.Exchange || o.TradeSide != original.TradeSide || o.OffsetType != original.OffsetType || o.OrderQty != original.OrderQty || !equalDecimal(string(o.LimitPrice), string(original.LimitPrice)) {
			return nil, ErrStreamIdentity
		}
		key := o.TradeDate + ":" + o.GatewayOrderID
		old, ok := latest[key]
		if ok && (old.GatewayStatus == "cancelled" || old.GatewayStatus == "filled" || old.GatewayStatus == "rejected") && o.GatewayStatus != old.GatewayStatus {
			continue
		}
		latest[key] = o
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]OrderRow, 0, len(latest))
	for _, o := range latest {
		result = append(result, o)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GatewayOrderID > result[j].GatewayOrderID })
	return result, nil
}
