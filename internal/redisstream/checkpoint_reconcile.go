package redisstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type checkpointReconcileStore interface {
	checkpointRecoveryStore
	GetStreamCheckpoint(context.Context, string) (ledger.StreamCheckpoint, error)
	CheckpointReconciliationBlocked(context.Context, string, string) (bool, error)
	ResolvedTestOrderKeys(context.Context, string, string) (map[string]ledger.TestOrderResolution, error)
	StructureStreamCheckpointError(context.Context, ledger.StreamCheckpoint, map[string]any) (bool, error)
	RecordCheckpointReconciliationAttempt(context.Context, ledger.StreamCheckpoint, string, any) (bool, error)
	ReadCheckpointReply(context.Context, string, string) (json.RawMessage, error)
	GetCommandStatus(context.Context, string) (ledger.CommandStatus, error)
	GetLatestAsset(context.Context, string) (trading.Asset, error)
	ListPositions(context.Context, trading.PositionQuery) ([]trading.Position, error)
	ListOrders(context.Context, trading.OrderQuery) ([]trading.Order, error)
	ListFills(context.Context, trading.FillQuery) ([]trading.Fill, error)
}

type CheckpointReconcileReport struct {
	AccountID string         `json:"account_id"`
	Status    string         `json:"status"`
	Evidence  map[string]any `json:"evidence"`
}

// Explicit operator action; never called by health polling and never sends trades.
// normalizeOnly permits evidence preparation while the counter is off hours.
func (service *RuntimeObservability) ReconcileReplyCheckpoint(ctx context.Context, account string, normalizeOnly bool, query func(context.Context, string) (string, error)) (report CheckpointReconcileReport, returnErr error) {
	report = CheckpointReconcileReport{AccountID: account, Status: "blocked", Evidence: map[string]any{}}
	if service.cfg.Redis.Env != "test" || service.cfg.Service.Environment != config.EnvironmentTest {
		return report, errors.New("explicit checkpoint reconciliation currently supports TEST only")
	}
	store, ok := service.store.(checkpointReconcileStore)
	if !ok {
		return report, errors.New("checkpoint reconciliation store unavailable")
	}
	route, ok := service.cfg.AccountRoute(account)
	if !ok || !route.Enabled {
		return report, errors.New("enabled account required")
	}
	stream := NewStreams(route.StreamPrefix).Reply
	cp, err := store.GetStreamCheckpoint(ctx, stream)
	if err != nil {
		return report, err
	}
	if active, present := cp.Metadata["active_error"]; present && active == nil {
		report.Status = "already_recovered"
		if evidence, ok := cp.Metadata["last_recovered_error"].(map[string]any); ok {
			report.Evidence = evidence
		}
		return report, nil
	}
	id := checkpointErrorStreamID(cp.Metadata, cp.LastError)
	if id == "" {
		return report, errors.New("original error locator missing")
	}
	body, err := store.ReadCheckpointReply(ctx, stream, id)
	if err != nil {
		return report, err
	}
	envelope, err := DecodeEntry(stream, id, map[string]any{"body": string(body)})
	if err != nil {
		return report, err
	}
	if !eligibleRecoveryError(envelope, cp, route.AccountID, route.BrokerID, route.GatewayID, service.cfg.Redis.Env) {
		return report, errors.New("original reply does not prove a single read-only BROKER_NOT_READY error")
	}
	oldSession, err := service.sessionBefore(ctx, route.StreamPrefix, id)
	if err != nil {
		return report, err
	}
	active := map[string]any{"code": envelope.Code, "action": envelope.Action, "stream_id": id,
		"message": cp.LastError, "observed_at": producedAtFromStreamID(id).In(timeutil.Location()), "counter_session_id": oldSession}
	updated, err := store.StructureStreamCheckpointError(ctx, cp, active)
	if err != nil {
		return report, err
	}
	if !updated {
		return report, errors.New("checkpoint changed during error preparation; retry")
	}
	report.Evidence = map[string]any{"code": envelope.Code, "action": envelope.Action, "stream_id": id,
		"message": cp.LastError, "old_counter_session_id": oldSession, "account_id": account,
		"gateway_id": route.GatewayID, "observed_at": producedAtFromStreamID(id).In(timeutil.Location())}
	report.Status = "structured"
	if normalizeOnly {
		return report, nil
	}
	report.Status = "blocked"
	defer func() {
		if report.Status == "recovered" {
			return
		}
		if returnErr != nil {
			report.Evidence["blocked_reason"] = returnErr.Error()
		}
		report.Evidence["finished_at"] = service.now()
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		latest, err := store.GetStreamCheckpoint(auditCtx, stream)
		if err == nil && (latest.LastError != cp.LastError || checkpointErrorStreamID(latest.Metadata, latest.LastError) != id) {
			err = errors.New("active error changed before recording reconciliation attempt")
		}
		if err == nil {
			var recorded bool
			recorded, err = store.RecordCheckpointReconciliationAttempt(auditCtx, latest, id, report)
			if err == nil && !recorded {
				err = errors.New("checkpoint changed before reconciliation attempt audit")
			}
		}
		if err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("persist reconciliation attempt: %w", err))
		}
	}()
	if query == nil {
		return report, errors.New("read-only query publisher required")
	}
	started := service.now()
	initial, err := service.Snapshot(ctx, true)
	if err != nil {
		return report, err
	}
	gateway, err := reconcileReady(initial, account)
	if err != nil {
		return report, err
	}
	session := gateway.CounterSessionID
	report.Evidence["new_counter_session_id"] = session
	report.Evidence["started_at"] = started
	// Historical working orders must not prevent gathering read-only evidence.
	// They still prevent committing recovery at the final gate below.
	blocked, err := store.CheckpointRecoveryBlocked(ctx, account)
	if err != nil {
		return report, err
	}
	if blocked {
		return report, errors.New("unknown commands or open reconciliation breaks block recovery")
	}
	statuses := make([]ledger.CommandStatus, 0, 4)
	seen := map[string]bool{}
	for _, action := range []string{ActionAccountAsset, ActionAccountPositions, ActionOrderList, ActionFillList} {
		current, err := service.waitReconcileReady(ctx, account, session)
		if err != nil {
			return report, err
		}
		g := recoveryGateway(current, account)
		if g.CounterSessionID != session {
			return report, errors.New("OC session changed before query")
		}
		messageID, err := query(ctx, action)
		if err != nil {
			return report, err
		}
		if messageID == "" || seen[messageID] {
			return report, errors.New("query message IDs must be unique")
		}
		seen[messageID] = true
		status, err := waitRecoveryQuery(ctx, store, messageID, account, action)
		if err != nil {
			return report, err
		}
		statuses = append(statuses, status)
		report.Evidence["queries"] = statuses
	}
	// Wait until archived replies have also been applied to the ledger.
	auditStart, err := service.waitReconcileReady(ctx, account, session)
	if err != nil {
		return report, err
	}
	summary, err := auditRecoveryLedger(ctx, store, statuses, account, started.In(timeutil.Location()).Format("2006-01-02"))
	if err != nil {
		return report, err
	}
	report.Evidence["reconciliation"] = summary
	resolutions, err := store.ResolvedTestOrderKeys(ctx, account, session)
	if err != nil {
		return report, err
	}
	report.Evidence["test_legacy_order_resolutions"] = resolutions
	unresolved, err := unresolvedRecoveryOrders(ctx, store, account, resolutions)
	if err != nil {
		return report, err
	}
	report.Evidence["unresolved_orders"] = unresolved
	report.Evidence["unresolved_order_count"] = len(unresolved)
	final, err := service.Snapshot(ctx, true)
	if err != nil {
		return report, err
	}
	gateway, err = reconcileReady(final, account)
	if err != nil {
		return report, err
	}
	if gateway.CounterSessionID != session {
		return report, errors.New("OC session changed during ledger audit")
	}
	if !recoveryStreamsStable(auditStart, final, account) {
		return report, errors.New("reply or event stream changed during ledger audit; retry")
	}
	for _, previous := range statuses {
		current, err := store.GetCommandStatus(ctx, previous.OriginMessageID)
		if err != nil {
			return report, err
		}
		if err := validateRecoveryQuery(current, account, previous.Action); err != nil {
			return report, err
		}
		if len(current.Replies) != len(previous.Replies) {
			return report, errors.New("query replies changed during ledger audit")
		}
		for i := range current.Replies {
			if current.Replies[i].StreamID != previous.Replies[i].StreamID {
				return report, errors.New("query page identities changed during ledger audit")
			}
		}
	}
	if started.In(timeutil.Location()).Format("20060102") != service.now().In(timeutil.Location()).Format("20060102") {
		return report, errors.New("business date changed during reconciliation")
	}
	if err := service.verifyRecoverySession(ctx, route.StreamPrefix, started, session); err != nil {
		return report, err
	}
	blocked, err = store.CheckpointReconciliationBlocked(ctx, account, session)
	if err != nil {
		return report, err
	}
	if blocked {
		return report, fmt.Errorf("recovery remains blocked: %d unresolved historical/current orders, or unknown commands/open reconciliation breaks", len(unresolved))
	}
	latest, err := store.GetStreamCheckpoint(ctx, stream)
	if err != nil {
		return report, err
	}
	if latest.LastError != cp.LastError || checkpointErrorStreamID(latest.Metadata, latest.LastError) != id {
		return report, errors.New("active checkpoint error changed during reconciliation")
	}
	report.Evidence["recovered_at"] = service.now()
	report.Evidence["recovery_source"] = "cross_session_full_readonly_reconciliation"
	recovered, err := store.RecoverStreamCheckpoint(ctx, stream, latest.LastStreamID, cp.LastError, report.Evidence)
	if err != nil {
		return report, err
	}
	if !recovered {
		return report, errors.New("checkpoint changed before recovery commit; retry")
	}
	report.Status = "recovered"
	return report, nil
}

type unresolvedRecoveryOrder struct {
	GatewayOrderID   string              `json:"gateway_order_id"`
	TradeDate        string              `json:"trade_date"`
	CounterSessionID string              `json:"counter_session_id,omitempty"`
	OriginMessageID  string              `json:"origin_message_id,omitempty"`
	Status           trading.OrderStatus `json:"status"`
	OrderQty         int64               `json:"order_qty"`
	FilledQty        int64               `json:"cum_filled_qty"`
	LeavesQty        int64               `json:"leaves_qty"`
}

func unresolvedRecoveryOrders(ctx context.Context, store checkpointReconcileStore, account string, resolutions map[string]ledger.TestOrderResolution) ([]unresolvedRecoveryOrder, error) {
	unresolved := []unresolvedRecoveryOrder{}
	for offset := 0; ; offset += 500 {
		orders, err := store.ListOrders(ctx, trading.OrderQuery{AccountID: account, Limit: 500, Cursor: strconv.Itoa(offset)})
		if err != nil {
			return nil, err
		}
		for _, o := range orders {
			if o.IsTerminal {
				continue
			}
			if _, confirmed := resolutions[o.TradeDate+"/"+o.GatewayOrderID]; confirmed {
				continue
			}
			unresolved = append(unresolved, unresolvedRecoveryOrder{GatewayOrderID: o.GatewayOrderID, TradeDate: o.TradeDate,
				CounterSessionID: o.CounterSessionID, OriginMessageID: o.OriginMessageID, Status: o.Status, OrderQty: o.OrderQty,
				FilledQty: o.CumFilledQty, LeavesQty: o.LeavesQty})
		}
		if len(orders) < 500 {
			break
		}
	}
	return unresolved, nil
}

func eligibleRecoveryError(e EntryEnvelope, cp ledger.StreamCheckpoint, account, broker, gateway, environment string) bool {
	if active, ok := cp.Metadata["active_error"].(map[string]any); ok {
		code, _ := active["code"].(string)
		if code != "CHECKPOINT_ERROR" && code != "BROKER_NOT_READY" {
			return false
		}
	}
	readonly := e.Action == ActionAccountAsset || e.Action == ActionAccountPositions || e.Action == ActionOrderList || e.Action == ActionFillList
	return readonly && e.Code == "BROKER_NOT_READY" && e.Status == "failed" && e.Routing.AccountID == account &&
		e.Routing.BrokerID == broker && e.Routing.GatewayID == gateway && e.Routing.Env == environment &&
		cp.LastError == e.StreamID+": "+indeterminateCommandSkipReason(e)
}

func (service *RuntimeObservability) sessionBefore(ctx context.Context, prefix, id string) (string, error) {
	stream := NewStreams(prefix).HB
	entries, err := service.client.XRevRangeN(ctx, stream, id, "-", 1).Result()
	if err != nil {
		return "", err
	}
	if len(entries) != 1 {
		return "", errors.New("original heartbeat evidence unavailable")
	}
	e, err := DecodeEntry(stream, entries[0].ID, entries[0].Values)
	var p heartbeatPayload
	if err != nil || json.Unmarshal(e.Payload, &p) != nil || p.CounterSessionID == "" {
		return "", errors.New("original session evidence invalid")
	}
	return p.CounterSessionID, nil
}

func (service *RuntimeObservability) verifyRecoverySession(ctx context.Context, prefix string, start time.Time, session string) error {
	stream := NewStreams(prefix).HB
	start = start.Add(-time.Duration(service.cfg.Operations.HeartbeatStaleSeconds) * time.Second)
	entries, err := service.client.XRangeN(ctx, stream, streamIDAt(start), "+", 1000).Result()
	if err != nil {
		return err
	}
	if len(entries) == 0 || len(entries) >= 1000 {
		return errors.New("session audit missing or exceeds bounded scan")
	}
	for _, entry := range entries {
		e, err := DecodeEntry(stream, entry.ID, entry.Values)
		var p heartbeatPayload
		if err != nil || json.Unmarshal(e.Payload, &p) != nil || p.CounterSessionID != session {
			return errors.New("conflicting OC session observed during reconciliation")
		}
	}
	return nil
}

func recoveryGateway(snapshot RuntimeSnapshot, account string) *GatewayRuntimeStatus {
	for i := range snapshot.Gateways {
		if snapshot.Gateways[i].AccountID == account {
			return &snapshot.Gateways[i]
		}
	}
	return nil
}

// A reply is archived before its worker checkpoint is committed. Wait for this
// bounded handoff (and OC pending heartbeat) without relaxing the final gate.
func (service *RuntimeObservability) waitReconcileReady(ctx context.Context, account, session string) (RuntimeSnapshot, error) {
	for {
		snapshot, err := service.Snapshot(ctx, true)
		if err != nil {
			return snapshot, err
		}
		if _, err := reconcileReadyIgnoringHandoff(snapshot, account, session); err != nil {
			return snapshot, err
		}
		g := recoveryGateway(snapshot, account)
		if snapshot.Summary.TotalLag == 0 && g.PendingTrades == 0 && g.PendingQueries == 0 {
			return snapshot, nil
		}
		if err := recoveryPause(ctx); err != nil {
			return snapshot, err
		}
	}
}

func reconcileReadyIgnoringHandoff(snapshot RuntimeSnapshot, account, session string) (*GatewayRuntimeStatus, error) {
	g := recoveryGateway(snapshot, account)
	if g == nil || g.CounterSessionID != session {
		return nil, errors.New("OC session changed during query handoff")
	}
	copySnapshot := snapshot
	copySnapshot.Gateways = append([]GatewayRuntimeStatus(nil), snapshot.Gateways...)
	copyGateway := recoveryGateway(copySnapshot, account)
	copyGateway.PendingTrades = 0
	copyGateway.PendingQueries = 0
	copySnapshot.Summary.TotalLag = 0
	return reconcileReady(copySnapshot, account)
}

func reconcileReady(snapshot RuntimeSnapshot, account string) (*GatewayRuntimeStatus, error) {
	g := recoveryGateway(snapshot, account)
	if !checkpointGatewayReady(g) || snapshot.Summary.TotalLag != 0 || snapshot.Summary.PendingDeadLetters != 0 || len(snapshot.Errors) != 0 {
		return nil, errors.New("fresh unique ready session with zero pending, lag and DLQ required")
	}
	for _, stream := range snapshot.Streams {
		if stream.ActiveError != nil && !(stream.AccountID == account && stream.Role == SuffixReply && stream.ActiveError["code"] == "BROKER_NOT_READY") {
			return nil, errors.New("another active stream error blocks reconciliation")
		}
	}
	return g, nil
}

func waitRecoveryQuery(ctx context.Context, store checkpointReconcileStore, messageID, account, action string) (ledger.CommandStatus, error) {
	for {
		status, err := store.GetCommandStatus(ctx, messageID)
		if err != nil {
			return status, err
		}
		if status.Terminal {
			if err := validateRecoveryQuery(status, account, action); err != nil {
				return status, err
			}
			return status, nil
		}
		if err := recoveryPause(ctx); err != nil {
			return status, err
		}
	}
}

func validateRecoveryQuery(status ledger.CommandStatus, account, action string) error {
	if status.State != "completed" || !status.Success || status.Contradictory || status.TerminalCount != 1 || len(status.Replies) == 0 {
		return fmt.Errorf("%s did not obtain one complete successful terminal: %s", action, status.State)
	}
	for i, reply := range status.Replies {
		if reply.AccountID != account || reply.Action != action || reply.ResultType != status.ExpectedResultType || (reply.Code != "" && reply.Code != "OK") ||
			(i == len(status.Replies)-1 && (reply.Status != "completed" || !reply.IsLast)) ||
			(i < len(status.Replies)-1 && (reply.Status != "partial" || reply.IsLast)) {
			return fmt.Errorf("%s reply routing, page ordering or terminal mismatch", action)
		}
	}
	return nil
}

func recoveryStreamsStable(before, after RuntimeSnapshot, account string) bool {
	for _, role := range []string{SuffixReply, SuffixEvent} {
		var first, last *StreamRuntimeStatus
		for i := range before.Streams {
			if before.Streams[i].AccountID == account && before.Streams[i].Role == role {
				first = &before.Streams[i]
			}
		}
		for i := range after.Streams {
			if after.Streams[i].AccountID == account && after.Streams[i].Role == role {
				last = &after.Streams[i]
			}
		}
		if first == nil || last == nil || first.LatestStreamID != last.LatestStreamID || first.CheckpointID != last.CheckpointID {
			return false
		}
	}
	return true
}

func recoveryPause(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type recoveryPageAudit struct {
	Source   string `json:"source"`
	Index    int    `json:"index"`
	Count    int    `json:"count"`
	SHA256   string `json:"sha256"`
	StreamID string `json:"stream_id,omitempty"`
}

func recoveryPageDigest(source string, index int, values any, count int, id string) recoveryPageAudit {
	body, _ := json.Marshal(values)
	hash := sha256.Sum256(body)
	return recoveryPageAudit{Source: source, Index: index, Count: count, SHA256: hex.EncodeToString(hash[:]), StreamID: id}
}

func auditRecoveryLedger(ctx context.Context, store checkpointReconcileStore, statuses []ledger.CommandStatus, account, date string) (map[string]any, error) {
	positions := map[string]trading.Position{}
	orders := map[string]trading.Order{}
	fills := map[string]trading.Fill{}
	var asset trading.Asset
	var pages []recoveryPageAudit
	for _, status := range statuses {
		for i, reply := range status.Replies {
			body, err := store.ReadCheckpointReply(ctx, reply.StreamKey, reply.StreamID)
			if err != nil {
				return nil, err
			}
			e, err := DecodeEntry(reply.StreamKey, reply.StreamID, map[string]any{"body": string(body)})
			if err != nil {
				return nil, err
			}
			if e.OriginMessageID != status.OriginMessageID || e.Action != status.Action || e.Routing.AccountID != account {
				return nil, errors.New("raw query association mismatch")
			}
			count := 0
			switch status.Action {
			case ActionAccountAsset:
				asset, _, err = assetFromReplyEnvelope(e)
				count = 1
			case ActionAccountPositions:
				var items []trading.Position
				items, _, err = positionsFromReplyEnvelope(e)
				count = len(items)
				for _, p := range items {
					if p.Quantity <= 0 {
						continue
					}
					key := p.Symbol + "/" + string(p.Exchange)
					if _, found := positions[key]; found {
						return nil, errors.New("duplicate queried position")
					}
					positions[key] = p
				}
			case ActionOrderList:
				var items []trading.Order
				items, err = ordersFromReplyEnvelope(e)
				count = len(items)
				for _, o := range items {
					if _, found := orders[o.GatewayOrderID]; found {
						return nil, errors.New("duplicate queried order")
					}
					orders[o.GatewayOrderID] = o
				}
			case ActionFillList:
				var items []trading.Fill
				var transfers []trading.ComponentTransfer
				items, transfers, err = fillRecordsFromReplyEnvelope(e)
				if len(transfers) > 0 {
					return nil, errors.New("component transfers require separate reconciliation; not auto-cleared")
				}
				count = len(items)
				for _, f := range items {
					key := recoveryFillKey(f)
					if _, found := fills[key]; found {
						return nil, errors.New("duplicate queried fill")
					}
					fills[key] = f
				}
			}
			if err != nil {
				return nil, err
			}
			pages = append(pages, recoveryPageDigest(status.Action, i, body, count, e.StreamID))
		}
	}
	actualAsset, err := store.GetLatestAsset(ctx, account)
	if err != nil {
		return nil, err
	}
	if actualAsset.AccountID != asset.AccountID || !recoveryMoneyEqual(actualAsset.CashAvailable, asset.CashAvailable) || !recoveryMoneyEqual(actualAsset.CashTotal, asset.CashTotal) {
		return nil, errors.New("queried cash and current ledger differ")
	}
	if actualAsset.UpdatedAt.Before(asset.UpdatedAt) {
		return nil, errors.New("latest asset predates fresh query")
	}
	pages = append(pages, recoveryPageDigest("ledger.asset", 0, actualAsset, 1, ""))
	for offset := 0; ; offset += 500 {
		items, err := store.ListPositions(ctx, trading.PositionQuery{AccountID: account, Limit: 500, Cursor: strconv.Itoa(offset)})
		if err != nil {
			return nil, err
		}
		pages = append(pages, recoveryPageDigest("ledger.positions", offset/500, items, len(items), ""))
		for _, p := range items {
			key := p.Symbol + "/" + string(p.Exchange)
			expected, found := positions[key]
			if !found || p.Quantity != expected.Quantity || p.SellableQty != expected.SellableQty || !recoveryMoneyEqual(p.TotalCost, expected.TotalCost) {
				return nil, fmt.Errorf("position mismatch: %s", key)
			}
			delete(positions, key)
		}
		if len(items) < 500 {
			break
		}
	}
	orderCount := 0
	fillTotals := map[string]int64{}
	for offset := 0; ; offset += 500 {
		items, err := store.ListFills(ctx, trading.FillQuery{AccountID: account, DateFrom: date, DateTo: date, Limit: 500, Cursor: strconv.Itoa(offset)})
		if err != nil {
			return nil, err
		}
		pages = append(pages, recoveryPageDigest("ledger.fills", offset/500, items, len(items), ""))
		for _, f := range items {
			key := recoveryFillKey(f)
			expected, found := fills[key]
			if !found || f.Qty != expected.Qty || f.Price != expected.Price || f.Symbol != expected.Symbol || f.TradeSide != expected.TradeSide {
				return nil, fmt.Errorf("fill mismatch: %s", key)
			}
			delete(fills, key)
			fillTotals[f.GatewayOrderID] += f.Qty
		}
		if len(items) < 500 {
			break
		}
	}
	for offset := 0; ; offset += 500 {
		items, err := store.ListOrders(ctx, trading.OrderQuery{AccountID: account, DateFrom: date, DateTo: date, Limit: 500, Cursor: strconv.Itoa(offset)})
		if err != nil {
			return nil, err
		}
		pages = append(pages, recoveryPageDigest("ledger.orders", offset/500, items, len(items), ""))
		for _, o := range items {
			expected, found := orders[o.GatewayOrderID]
			if !found || o.OrderQty != expected.OrderQty || o.CumFilledQty != expected.CumFilledQty || o.LeavesQty != expected.LeavesQty || o.Status != expected.Status || o.Symbol != expected.Symbol || o.TradeSide != expected.TradeSide || o.BusinessType != expected.BusinessType || !o.IsTerminal {
				return nil, fmt.Errorf("order mismatch or unexplained active order: %s", o.GatewayOrderID)
			}
			if o.CumFilledQty != fillTotals[o.GatewayOrderID] {
				return nil, fmt.Errorf("order/fill quantity mismatch: %s", o.GatewayOrderID)
			}
			delete(fillTotals, o.GatewayOrderID)
			delete(orders, o.GatewayOrderID)
			orderCount++
		}
		if len(items) < 500 {
			break
		}
	}
	if len(positions)+len(orders)+len(fills)+len(fillTotals) != 0 {
		return nil, errors.New("query records missing from ledger or orphan fills")
	}
	return map[string]any{"status": "matched", "scope": "current_business_date", "trade_date": date, "order_count": orderCount, "page_audit": pages, "unexplained_active_orders": 0,
		"asset_scope": "OC cash_available and cash_total; market valuation is not part of counter recovery"}, nil
}

func recoveryFillKey(f trading.Fill) string { return f.GatewayOrderID + "/" + f.FillID }
func recoveryMoneyEqual(a, b float64) bool {
	return !math.IsNaN(a) && !math.IsNaN(b) && !math.IsInf(a, 0) && !math.IsInf(b, 0) && math.Abs(a-b) < 0.005
}
