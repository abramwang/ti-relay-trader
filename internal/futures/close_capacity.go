package futures

import (
	"encoding/json"
	"fmt"
	"time"
)

// CloseCapacity is a read-only calculation for one combined speculative bucket.
// It is not a reservation, authorization or evidence of an atomic broker snapshot.
// Today/YesterdayRemaining describe the baseline, not CT/CY admission limits.
type CloseCapacity struct {
	Position           int64 `json:"position"`
	Today              int64 `json:"today"`
	YesterdayRemaining int64 `json:"yesterday_remaining"`
	BrokerFrozen       int64 `json:"broker_frozen"`
	LocalReserved      int64 `json:"local_reserved"`
	ClosedSinceQuery   int64 `json:"closed_since_query"`
	Available          int64 `json:"available"`
}

// CalculateCFFEXCloseCapacity consumes a complete, command-correlated position
// reply and explicitly selected post-query close intentions. The caller must
// supply ALL such intentions, including unpublished/unknown ones, and exclude
// concurrent external activity before using the result for admission. Registration
// and reservation must ultimately share a transaction; this function sends nothing.
// Baseline broker freezes remain deducted until a new reconciled snapshot, so
// local terminal events cannot release an unrelated broker reservation.
func CalculateCFFEXCloseCapacity(scope CommandScope, target TestQueryTarget, reply ReplyResult, symbol, closeSide string, commands []CommandRegistration, report TradeReport) (CloseCapacity, error) {
	return calculateCFFEXCloseCapacity(scope, target, reply, symbol, closeSide, commands, report, nil)
}

// absorbed is private, persisted coverage established by a quiescent rebase.
// It is never accepted from an HTTP caller or inferred from a frozen total.
func calculateCFFEXCloseCapacity(scope CommandScope, target TestQueryTarget, reply ReplyResult, symbol, closeSide string, commands []CommandRegistration, report TradeReport, absorbed map[int64]int64) (CloseCapacity, error) {
	return calculateCombinedCloseCapacity(scope, target, reply, "CFFEX", symbol, closeSide, commands, report, absorbed)
}

// Combined means a single directional PositionDate=1 row whose Position includes
// both remaining dates. Split-date rows are not silently summed into this model.
func calculateCombinedCloseCapacity(scope CommandScope, target TestQueryTarget, reply ReplyResult, exchange, symbol, closeSide string, commands []CommandRegistration, report TradeReport, absorbed map[int64]int64) (CloseCapacity, error) {
	bad := func(reason string) (CloseCapacity, error) {
		return CloseCapacity{}, fmt.Errorf("combined close capacity: %s", reason)
	}
	if scope.validate() != nil || target.Validate() != nil || !validExchange(exchange) || !native(symbol, 80, false) || !oneOf(closeSide, "B", "S") {
		return bad("invalid scope or close direction")
	}
	if reply.CommandID <= 0 || reply.Action != "account.positions.query" || reply.State != "ready" || !reply.ResponseValidated || reply.Snapshot == nil || !validTime(reply.ProducedAt) {
		return bad("complete position query required")
	}
	snapshot := reply.Snapshot
	q := snapshot.Context
	if !validQueryCommandContext(q) || q.Command.CommandID != reply.CommandID || q.commandScope() != scope || q.Action != reply.Action || q.CounterSessionID != target.CounterSessionID || q.ConnectionGeneration == nil || *q.ConnectionGeneration != target.ConnectionGeneration || q.TradeDate != target.TradeDate || len(snapshot.Assets)+len(snapshot.Orders)+len(snapshot.Fills)+len(snapshot.Constraints) != 0 {
		return bad("position query context mismatch")
	}
	if _, err := NewQueryBatch(q, 0, time.Second, DefaultBatchLimits()); err != nil {
		return bad("invalid position query identity")
	}
	tradeScope := TradeScope{Route: scope.Route, NativeBrokerID: scope.NativeBrokerID, InvestorID: scope.InvestorID, TradeDate: target.TradeDate}
	if report.Scope != tradeScope || report.Blocked || report.PendingCount != 0 || len(report.Conflicts) != 0 {
		return bad("unresolved trade facts")
	}
	// Revalidate typed inputs too: negative Count values are possible in Go.
	raw, err := json.Marshal(snapshot.Positions)
	if err != nil {
		return bad("invalid positions")
	}
	positions, err := DecodePositions(raw, SnapshotScope{scope.Route.AccountID, target.TradeDate, scope.NativeBrokerID})
	if err != nil {
		return bad("invalid positions")
	}
	completed, _ := time.Parse(time.RFC3339Nano, reply.ProducedAt)
	direction := "2" // Sell closes a long; buy closes a short.
	if closeSide == "B" {
		direction = "3"
	}
	var result CloseCapacity
	found := false
	for _, row := range positions {
		captured, _ := time.Parse(time.RFC3339Nano, row.CapturedAt)
		if captured.After(completed) {
			return bad("row captured after query completion")
		}
		p := row.Futures
		if row.Exchange != exchange || row.Symbol != symbol {
			continue
		}
		if p.PosiDirection == "1" {
			return bad("net position is not a directional bucket")
		}
		if p.PosiDirection != direction || p.HedgeFlag != "1" || p.InvestUnitID != "" {
			continue
		}
		if found || p.PositionDate != "1" || p.TodayPosition > p.Position {
			return bad("ambiguous position date or quantity")
		}
		found = true
		result.Position, result.Today = int64(p.Position), int64(p.TodayPosition)
		// YDPosition is the opening-day historical quantity, not remaining lots.
		result.YesterdayRemaining = result.Position - result.Today
		result.BrokerFrozen = int64(p.ShortFrozen)
		if closeSide == "B" {
			result.BrokerFrozen = int64(p.LongFrozen)
		}
		if result.BrokerFrozen > result.Position {
			return bad("broker freeze exceeds position")
		}
	}
	seenCommands, seenOrders := map[int64]bool{}, map[string]bool{}
	covered := 0
	for _, reg := range commands {
		if reg.CommandID <= 0 || seenCommands[reg.CommandID] || reg.Scope != scope || !oneOf(reg.Outcome, "registered", "replayed") || reg.Original.Action != "order.submit" || len(reg.Original.Orders) != 1 {
			return bad("invalid or duplicate close registration")
		}
		o := reg.Original.Orders[0]
		if seenOrders[o.GatewayOrderID] || !validID(o.GatewayOrderID) || o.AccountID != scope.Route.AccountID || o.TradeDate != target.TradeDate || o.Symbol != symbol || o.Exchange != exchange || o.BusinessType != "F" || o.TradeSide != closeSide || o.HedgeType != "speculation" || o.OffsetType != "C" || o.OrderQty <= 0 {
			return bad("close intention mismatch or unsupported offset")
		}
		// Commands already represented by the snapshot cannot be deducted twice.
		decoded, err := DecodeCommand(reg.Original.Raw, scope, "cmd.trade")
		if err != nil || decoded.Action != "order.submit" || len(decoded.Orders) != 1 || decoded.Orders[0] != o || decoded.MessageID != reg.Original.MessageID {
			return bad("close registration differs from original command")
		}
		envelope, err := parseReply(reg.Original.Raw)
		if err != nil {
			return bad("invalid command envelope")
		}
		sent, err := time.Parse(time.RFC3339Nano, textField(envelope, "sent_at"))
		absorbedFilled, included := absorbed[reg.CommandID]
		if err != nil || (!included && !sent.After(completed)) || (included && !sent.Before(completed)) {
			return bad("intention is not strictly after the position baseline")
		}
		seenCommands[reg.CommandID], seenOrders[o.GatewayOrderID] = true, true
		filled, terminal, err := closeOrderProgress(o, report)
		if err != nil {
			return bad(err.Error())
		}
		if included {
			if !terminal || filled != absorbedFilled || absorbedFilled < 0 {
				return bad("absorbed terminal facts changed")
			}
			covered++
			continue
		}
		result.ClosedSinceQuery += filled
		if !terminal {
			result.LocalReserved += int64(o.OrderQty) - filled
		}
		if result.ClosedSinceQuery+result.LocalReserved > result.Position-result.BrokerFrozen {
			return bad("close intentions exceed unreserved position")
		}
	}
	if covered != len(absorbed) {
		return bad("absorbed responsibility missing")
	}
	result.Available = result.Position - result.BrokerFrozen - result.LocalReserved - result.ClosedSinceQuery
	return result, nil
}

// An accepted receipt, cancel request or failed cancel does not release quantity.
// CumFilledQty and distinct executions are overlapping evidence, not additive.
func closeOrderProgress(intent SubmitIntent, report TradeReport) (int64, bool, error) {
	invalid := func() (int64, bool, error) { return 0, false, fmt.Errorf("inconsistent close order/fill facts") }
	record := report.Orders[intent.GatewayOrderID]
	var filled int64
	for id, execution := range report.Executions {
		if execution == nil {
			return invalid()
		}
		f := execution.Fill
		if execution.LinkedGateway != intent.GatewayOrderID && f.GatewayOrderID != intent.GatewayOrderID {
			continue
		}
		if execution.State != "linked" || execution.FactConflict || execution.LinkedGateway != intent.GatewayOrderID || f.FillID != id || !validID(id) || f.Qty <= 0 || !closeFactsMatch(intent, f.TradeRow) || record == nil || f.NativeIDs.OrderSysID == "" || f.NativeIDs.OrderSysID != record.Order.NativeIDs.OrderSysID {
			return invalid()
		}
		if f.GatewayOrderID != intent.GatewayOrderID && f.GatewayOrderID != nativeIdentity("order", report.Scope, f.Exchange, f.NativeIDs.OrderSysID) {
			return invalid()
		}
		filled += int64(f.Qty)
		if filled > int64(intent.OrderQty) {
			return invalid()
		}
	}
	if record == nil {
		if filled != 0 {
			return invalid()
		}
		return 0, false, nil
	}
	o := record.Order
	if record.Blocked || o.GatewayOrderID != intent.GatewayOrderID || !closeFactsMatch(intent, o.TradeRow) || o.OrderQty != intent.OrderQty || o.CumFilledQty < 0 || o.CumFilledQty > o.OrderQty || o.LeavesQty < 0 || !equalDecimal(string(o.LimitPrice), string(intent.LimitPrice)) {
		return invalid()
	}
	if terminal(o.GatewayStatus) {
		if o.LeavesQty != 0 || filled > int64(o.CumFilledQty) || (o.GatewayStatus == "filled" && o.CumFilledQty != o.OrderQty) || (o.GatewayStatus == "rejected" && o.CumFilledQty != 0) {
			return invalid()
		}
		return int64(o.CumFilledQty), true, nil
	}
	if !oneOf(o.GatewayStatus, "accepted", "working") || int64(o.LeavesQty)+int64(o.CumFilledQty) != int64(o.OrderQty) {
		return invalid()
	}
	if int64(o.CumFilledQty) > filled {
		filled = int64(o.CumFilledQty)
	}
	return filled, false, nil
}

func closeFactsMatch(o SubmitIntent, r TradeRow) bool {
	return r.AccountID == o.AccountID && r.TradeDate == o.TradeDate && r.Symbol == o.Symbol && r.Exchange == o.Exchange && r.BusinessType == o.BusinessType && r.TradeSide == o.TradeSide && r.OffsetType == o.OffsetType && r.HedgeType == o.HedgeType
}
