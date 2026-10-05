package redisstream

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/trading"
)

func TestRecoveryQueryRequiresUniqueCompleteMatchingPages(t *testing.T) {
	good := ledger.CommandStatus{State: "completed", Success: true, TerminalCount: 1, ExpectedResultType: "position_page",
		Replies: []ledger.CommandReplyStatus{
			{AccountID: "a", Action: ActionAccountPositions, ResultType: "position_page", Status: "partial", Code: "OK"},
			{AccountID: "a", Action: ActionAccountPositions, ResultType: "position_page", Status: "completed", IsLast: true, Code: "OK"},
		}}
	if err := validateRecoveryQuery(good, "a", ActionAccountPositions); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ledger.CommandStatus){
		"duplicate terminal": func(s *ledger.CommandStatus) { s.TerminalCount = 2 },
		"missing final":      func(s *ledger.CommandStatus) { s.Replies[1].IsLast = false },
		"foreign account":    func(s *ledger.CommandStatus) { s.Replies[0].AccountID = "b" },
		"foreign action":     func(s *ledger.CommandStatus) { s.Replies[0].Action = ActionFillList },
		"wrong result":       func(s *ledger.CommandStatus) { s.Replies[0].ResultType = "fill_page" },
		"failed page":        func(s *ledger.CommandStatus) { s.Replies[0].Status = "failed" },
		"error code":         func(s *ledger.CommandStatus) { s.Replies[1].Code = "BROKER_NOT_READY" },
		"premature final":    func(s *ledger.CommandStatus) { s.Replies[0].IsLast = true },
		"contradiction":      func(s *ledger.CommandStatus) { s.Contradictory = true },
		"no pages":           func(s *ledger.CommandStatus) { s.Replies = nil },
	} {
		t.Run(name, func(t *testing.T) {
			s := good
			s.Replies = append([]ledger.CommandReplyStatus(nil), good.Replies...)
			mutate(&s)
			if validateRecoveryQuery(s, "a", ActionAccountPositions) == nil {
				t.Fatal("unsafe recovery query accepted")
			}
		})
	}
}

func TestRecoveryErrorMustBeProvenByRawReply(t *testing.T) {
	e := EntryEnvelope{StreamID: "123-0", Action: ActionAccountPositions, Status: "failed", Code: "BROKER_NOT_READY", Routing: Routing{AccountID: "a", BrokerID: "b", GatewayID: "g", Env: "test"}}
	cp := ledger.StreamCheckpoint{LastStreamID: "999-0", LastError: "123-0: " + indeterminateCommandSkipReason(e)}
	if !eligibleRecoveryError(e, cp, "a", "b", "g", "test") {
		t.Fatal("valid raw error rejected after cursor moved")
	}
	for _, mutate := range []func(*EntryEnvelope){
		func(e *EntryEnvelope) { e.Action = ActionOrderSubmit },
		func(e *EntryEnvelope) { e.Code = "COMMAND_OUTCOME_UNKNOWN" },
		func(e *EntryEnvelope) { e.Status = "completed" },
		func(e *EntryEnvelope) { e.Routing.Env = "prod" },
		func(e *EntryEnvelope) { e.Routing.GatewayID = "foreign" },
	} {
		bad := e
		mutate(&bad)
		if eligibleRecoveryError(bad, cp, "a", "b", "g", "test") {
			t.Fatal("unproven raw error accepted")
		}
	}
	cp.Metadata = map[string]any{"active_error": map[string]any{"code": "MULTIPLE_CHECKPOINT_ERRORS"}}
	if eligibleRecoveryError(e, cp, "a", "b", "g", "test") {
		t.Fatal("aggregate error accepted")
	}
}

func TestExplicitRecoveryRejectsProductionWithoutQueries(t *testing.T) {
	service := &RuntimeObservability{cfg: config.Config{Redis: config.RedisConfig{Env: "prod"}}}
	called := false
	_, err := service.ReconcileReplyCheckpoint(context.Background(), "a", false, func(context.Context, string) (string, error) { called = true; return "id", nil })
	if err == nil || called {
		t.Fatal("production was not rejected before publication")
	}
}

type recoveryAuditStore struct {
	checkpointReconcileStore
	raw             map[string]json.RawMessage
	positions       []trading.Position
	orders          []trading.Order
	fills           []trading.Fill
	asset           trading.Asset
	positionOffsets []int
}

func (s *recoveryAuditStore) ReadCheckpointReply(_ context.Context, _, id string) (json.RawMessage, error) {
	return s.raw[id], nil
}
func (s *recoveryAuditStore) GetLatestAsset(context.Context, string) (trading.Asset, error) {
	return s.asset, nil
}
func (s *recoveryAuditStore) ListPositions(_ context.Context, q trading.PositionQuery) ([]trading.Position, error) {
	offset, _ := strconv.Atoi(q.Cursor)
	s.positionOffsets = append(s.positionOffsets, offset)
	if offset >= len(s.positions) {
		return nil, nil
	}
	end := offset + q.Limit
	if end > len(s.positions) {
		end = len(s.positions)
	}
	return s.positions[offset:end], nil
}
func (s *recoveryAuditStore) ListOrders(context.Context, trading.OrderQuery) ([]trading.Order, error) {
	return s.orders, nil
}
func (s *recoveryAuditStore) ListFills(context.Context, trading.FillQuery) ([]trading.Fill, error) {
	return s.fills, nil
}

func recoveryAuditFixture(t *testing.T, count int) (*recoveryAuditStore, []ledger.CommandStatus) {
	t.Helper()
	s := &recoveryAuditStore{raw: map[string]json.RawMessage{}, asset: trading.Asset{AccountID: "a", CashAvailable: 10, CashTotal: 20, UpdatedAt: time.UnixMilli(100)}}
	var positions []map[string]any
	for i := 0; i < count; i++ {
		symbol := fmt.Sprintf("%06d", i)
		positions = append(positions, map[string]any{"account_id": "a", "symbol": symbol, "exchange": "SH", "quantity": 100, "sellable_qty": 0, "total_cost": 10})
		s.positions = append(s.positions, trading.Position{AccountID: "a", Symbol: symbol, Exchange: "SH", Quantity: 100, TotalCost: 10})
	}
	var statuses []ledger.CommandStatus
	for i, action := range []string{ActionAccountAsset, ActionAccountPositions, ActionOrderList, ActionFillList} {
		id := fmt.Sprintf("100-%d", i)
		payload := map[string]any{"items": []any{}}
		if action == ActionAccountAsset {
			payload = map[string]any{"account": s.asset}
		}
		if action == ActionAccountPositions {
			payload["items"] = positions
		}
		body, err := json.Marshal(map[string]any{"protocol": Protocol, "message_type": "reply", "action": action, "origin_message_id": action,
			"routing": map[string]any{"env": "test", "account_id": "a", "broker_id": "b", "gateway_id": "g"}, "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		s.raw[id] = body
		statuses = append(statuses, ledger.CommandStatus{Action: action, OriginMessageID: action, Replies: []ledger.CommandReplyStatus{{StreamKey: "reply", StreamID: id}}})
	}
	return s, statuses
}

func TestRecoveryLedgerReadsAllPagesAndRejectsQuantityMismatch(t *testing.T) {
	s, statuses := recoveryAuditFixture(t, 1001)
	summary, err := auditRecoveryLedger(context.Background(), s, statuses, "a", "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.positionOffsets) != 3 || s.positionOffsets[2] != 1000 || summary["status"] != "matched" {
		t.Fatalf("incomplete pagination: %v", s.positionOffsets)
	}
	s.positions[1000].SellableQty = 100
	if _, err := auditRecoveryLedger(context.Background(), s, statuses, "a", "2026-10-05"); err == nil {
		t.Fatal("last page mismatch ignored")
	}
}

func TestRecoveryLedgerRejectsMissingOrExtraRowsAndBadCash(t *testing.T) {
	for _, name := range []string{"missing", "extra", "cash", "nan", "orphan fill", "active order"} {
		t.Run(name, func(t *testing.T) {
			s, statuses := recoveryAuditFixture(t, 1)
			switch name {
			case "missing":
				s.positions = nil
			case "extra":
				s.positions = append(s.positions, trading.Position{Symbol: "foreign", Exchange: "SH"})
			case "cash":
				s.asset.CashAvailable++
			case "nan":
				s.asset.CashAvailable = math.NaN()
			case "orphan fill":
				s.fills = []trading.Fill{{FillID: "unknown", GatewayOrderID: "o"}}
			case "active order":
				s.orders = []trading.Order{{GatewayOrderID: "o", IsTerminal: false}}
			}
			if _, err := auditRecoveryLedger(context.Background(), s, statuses, "a", "2026-10-05"); err == nil {
				t.Fatal("unsafe ledger accepted")
			}
		})
	}
}

func TestRecoveryStreamsMustNotMoveDuringLedgerAudit(t *testing.T) {
	streams := []StreamRuntimeStatus{{AccountID: "a", Role: SuffixReply, LatestStreamID: "1-0", CheckpointID: "1-0"}, {AccountID: "a", Role: SuffixEvent, LatestStreamID: "2-0", CheckpointID: "2-0"}}
	before := RuntimeSnapshot{Streams: streams}
	after := RuntimeSnapshot{Streams: append([]StreamRuntimeStatus(nil), streams...)}
	if !recoveryStreamsStable(before, after, "a") {
		t.Fatal("stable stream rejected")
	}
	after.Streams[1].LatestStreamID = "3-0"
	if recoveryStreamsStable(before, after, "a") {
		t.Fatal("concurrent event accepted")
	}
}

func TestRecoveryReadinessRejectsOffHoursAndOtherErrors(t *testing.T) {
	yes := true
	now := time.Now()
	snapshot := RuntimeSnapshot{Gateways: []GatewayRuntimeStatus{{AccountID: "a", Status: "online", OrderEntryReady: true, CounterSessionID: "s", LastHeartbeatAt: &now,
		RedisReady: &yes, BrokerReady: &yes, OrderSnapshotReady: &yes, AcceptingTradeCommands: &yes}}}
	if _, err := reconcileReady(snapshot, "a"); err != nil {
		t.Fatal(err)
	}
	snapshot.Streams = []StreamRuntimeStatus{{AccountID: "a", Role: SuffixEvent, ActiveError: map[string]any{"code": "BROKER_NOT_READY"}}}
	if _, err := reconcileReady(snapshot, "a"); err == nil {
		t.Fatal("other stream failure ignored")
	}
	snapshot.Streams = nil
	snapshot.Gateways[0].Status = "off_hours"
	if _, err := reconcileReady(snapshot, "a"); err == nil {
		t.Fatal("off hours treated as recovered")
	}
}
