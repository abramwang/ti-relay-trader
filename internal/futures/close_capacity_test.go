package futures

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

type closeCapacityFixture struct {
	scope    CommandScope
	target   TestQueryTarget
	reply    ReplyResult
	commands []CommandRegistration
	report   TradeReport
}

func newCloseCapacityFixture(t *testing.T) *closeCapacityFixture {
	t.Helper()
	scope := CommandScope{QueryRoute{"test", "ctp_4900", "close-fixture", "990000000001"}, "4900", "FIXTURE_INV", "FIXTURE_USER"}
	target := publicationTarget()
	target.TradeDate = "20261119"
	request := int64(12)
	q := QueryContext{Route: scope.Route, NativeBrokerID: scope.NativeBrokerID, NativeInvestorID: scope.InvestorID, NativeUserID: scope.UserID,
		Action: "account.positions.query", OriginMessageID: "positions-fixture", CounterSessionID: target.CounterSessionID,
		ConnectionGeneration: &target.ConnectionGeneration, TradeDate: target.TradeDate, Ticket: "ticket-1", SDKRequestID: &request,
		Command: &QueryCommandBinding{1, "positions-request", "positions-correlation", "positions-idem"}}
	position := PositionRow{AccountID: scope.Route.AccountID, Symbol: "IF2701", Exchange: "CFFEX", CapturedAt: "2026-11-19T09:30:00+08:00",
		Futures: Position{PosiDirection: "2", HedgeFlag: "1", PositionDate: "1", Position: 10, TodayPosition: 3, YDPosition: 12,
			LongFrozen: 9, ShortFrozen: 2, PositionCost: "0", OpenCost: "0", UseMargin: "0"}}
	return &closeCapacityFixture{scope: scope, target: target, reply: ReplyResult{CommandID: 1, Action: q.Action, State: "ready", ResponseValidated: true,
		ProducedAt: "2026-11-19T09:31:00+08:00", Snapshot: &SnapshotCandidate{Context: q, Positions: []PositionRow{position}}},
		report: newTradeProjection(TradeScope{scope.Route, scope.NativeBrokerID, scope.InvestorID, target.TradeDate}).report}
}

func (f *closeCapacityFixture) add(t *testing.T, quantity Count, side string) CommandRegistration {
	t.Helper()
	now, _ := time.Parse(time.RFC3339Nano, "2026-11-19T09:32:00+08:00")
	r := OrderExecutionRequest{IdempotencyKey: fmt.Sprintf("close-%d", len(f.commands)),
		Order: OrderPreviewRequest{f.scope.Route.AccountID, "IF2701.CFFEX", f.target.TradeDate, side, "C", "speculation", quantity, "3500.2"}}
	raw, e := buildServiceOrder(f.scope, r, CatalogBinding{"IF2701.CFFEX", "IF2701", "CFFEX", "fixture-catalog", "fixture-checksum"}, now)
	c, err := DecodeCommand(raw, f.scope, "cmd.trade")
	if err != nil {
		t.Fatal(err)
	}
	reg := CommandRegistration{Outcome: "registered", CommandID: int64(len(f.commands) + 2), Scope: f.scope, Original: c, Evidence: e}
	f.commands = append(f.commands, reg)
	return reg
}

func (f *closeCapacityFixture) calculate(side string) (CloseCapacity, error) {
	return CalculateCFFEXCloseCapacity(f.scope, f.target, f.reply, "IF2701", side, f.commands, f.report)
}

func closeCapacityOrder(reg CommandRegistration, status string, filled Count) OrderRow {
	o := reg.Original.Orders[0]
	leaves := o.OrderQty - filled
	if terminal(status) {
		leaves = 0
	}
	return OrderRow{TradeRow: TradeRow{AccountID: o.AccountID, GatewayOrderID: o.GatewayOrderID, ClientOrderID: o.ClientOrderID,
		CounterSessionID: "synthetic-session", TradeDate: o.TradeDate, Symbol: o.Symbol, Exchange: o.Exchange,
		BusinessType: o.BusinessType, TradeSide: o.TradeSide, OffsetType: o.OffsetType, HedgeType: o.HedgeType,
		NativeIDs: NativeIDs{BrokerID: reg.Scope.NativeBrokerID, InvestorID: reg.Scope.InvestorID, OrderSysID: fmt.Sprintf("%d", reg.CommandID)}},
		OrderQty: o.OrderQty, LimitPrice: o.LimitPrice, GatewayStatus: status, CumFilledQty: filled, LeavesQty: leaves}
}

func closeCapacityFill(scope TradeScope, order OrderRow, id string, qty Count) FillRow {
	row := FillRow{TradeRow: order.TradeRow, Qty: qty, Price: order.LimitPrice}
	row.NativeIDs.TradeID = id
	row.FillID = nativeFillIdentityV2(scope, order.Exchange, id, row.NativeIDs.OrderSysID, order.TradeSide)
	return row
}

func TestCFFEXCloseCapacityBucketsAndHistoricalQuantity(t *testing.T) {
	f := newCloseCapacityFixture(t)
	r, err := f.calculate("S")
	want := CloseCapacity{Position: 10, Today: 3, YesterdayRemaining: 7, BrokerFrozen: 2, Available: 8}
	if err != nil || r != want {
		t.Fatal(r, err)
	}
	// An opposite side, another hedge bucket, investment unit or exchange cannot
	// supply this speculative account's close inventory.
	for _, change := range []func(*PositionRow){
		func(p *PositionRow) { p.Futures.PosiDirection = "3" },
		func(p *PositionRow) { p.Futures.HedgeFlag = "2" },
		func(p *PositionRow) { p.Futures.InvestUnitID = "unit-2" },
		func(p *PositionRow) { p.Exchange = "SHFE" },
		func(p *PositionRow) { p.Symbol = "IH2701" },
	} {
		other := f.reply.Snapshot.Positions[0]
		change(&other)
		f.reply.Snapshot.Positions = append(f.reply.Snapshot.Positions, other)
	}
	r, err = f.calculate("S")
	if err != nil || r != want {
		t.Fatal("cross-bucket aggregation", r, err)
	}
	short, err := f.calculate("B")
	if err != nil || short.BrokerFrozen != 9 || short.Available != 1 {
		t.Fatal("buy-close must use short position and long frozen", short, err)
	}
	f.reply.Snapshot.Positions = []PositionRow{}
	if r, err := f.calculate("S"); err != nil || r != (CloseCapacity{}) {
		t.Fatal("complete empty snapshot", r, err)
	}
	f.reply.Snapshot.Positions = nil
	if _, err := f.calculate("S"); err == nil {
		t.Fatal("missing rows became empty position")
	}
}

func TestCFFEXCloseCapacityRetainsUnknownAndReleasesOnlyRemainder(t *testing.T) {
	f := newCloseCapacityFixture(t)
	reg := f.add(t, 5, "S")
	assert := func(held, filled, available int64) {
		t.Helper()
		r, err := f.calculate("S")
		if err != nil || r.LocalReserved != held || r.ClosedSinceQuery != filled || r.Available != available || r.BrokerFrozen != 2 {
			t.Fatal(r, err)
		}
	}
	assert(5, 0, 3) // Registered, including publication unknown: keep all lots.
	for _, status := range []string{"accepted", "working"} {
		o := closeCapacityOrder(reg, status, 0)
		f.report.Orders[o.GatewayOrderID] = &OrderRecord{Order: o}
		assert(5, 0, 3)
	}
	o := closeCapacityOrder(reg, "working", 2)
	f.report.Orders[o.GatewayOrderID].Order = o
	assert(3, 2, 3) // Cumulative evidence before the fill events.
	fill := closeCapacityFill(f.report.Scope, o, "f1", 2)
	f.report.Executions[fill.FillID] = &ExecutionRecord{Fill: fill, State: "linked", LinkedGateway: o.GatewayOrderID}
	assert(3, 2, 3) // Do not add cumulative and individual fills.
	fill2 := closeCapacityFill(f.report.Scope, o, "f2", 1)
	f.report.Executions[fill2.FillID] = &ExecutionRecord{Fill: fill2, State: "linked", LinkedGateway: o.GatewayOrderID}
	assert(2, 3, 3) // Distinct fills can lead the latest cumulative event.
	o = closeCapacityOrder(reg, "cancelled", 3)
	f.report.Orders[o.GatewayOrderID].Order = o
	assert(0, 3, 5) // Only unfilled remainder is returned; broker freeze stays.
	f.add(t, 5, "S")
	assert(5, 3, 0)
	f.add(t, 1, "S")
	if result, err := f.calculate("S"); err == nil || result != (CloseCapacity{}) {
		t.Fatal("over-close produced usable budget", result, err)
	}
}

func TestCFFEXCloseCapacityTerminalOutcomes(t *testing.T) {
	for _, c := range []struct {
		status string
		filled Count
		want   int64
	}{{"rejected", 0, 8}, {"cancelled", 0, 8}, {"cancelled", 2, 6}, {"filled", 5, 3}} {
		t.Run(c.status+fmt.Sprint(c.filled), func(t *testing.T) {
			f := newCloseCapacityFixture(t)
			reg := f.add(t, 5, "S")
			o := closeCapacityOrder(reg, c.status, c.filled)
			f.report.Orders[o.GatewayOrderID] = &OrderRecord{Order: o}
			r, err := f.calculate("S")
			if err != nil || r.Available != c.want || r.LocalReserved != 0 {
				t.Fatal(r, err)
			}
		})
	}
}

func TestCFFEXCloseCapacityScopeAndEvidenceGates(t *testing.T) {
	cases := map[string]func(*closeCapacityFixture){
		"failed_query":     func(f *closeCapacityFixture) { f.reply.State = "failed" },
		"partial_query":    func(f *closeCapacityFixture) { f.reply.State = "collecting" },
		"unvalidated":      func(f *closeCapacityFixture) { f.reply.ResponseValidated = false },
		"missing_snapshot": func(f *closeCapacityFixture) { f.reply.Snapshot = nil },
		"missing_command":  func(f *closeCapacityFixture) { f.reply.Snapshot.Context.Command = nil },
		"wrong_command":    func(f *closeCapacityFixture) { f.reply.Snapshot.Context.Command.CommandID++ },
		"wrong_action":     func(f *closeCapacityFixture) { f.reply.Action = "order.list.query" },
		"old_session":      func(f *closeCapacityFixture) { f.target.CounterSessionID = "new-session" },
		"next_day":         func(f *closeCapacityFixture) { f.target.TradeDate = "20261120" },
		"new_generation": func(f *closeCapacityFixture) {
			v := int64(2)
			f.reply.Snapshot.Context.ConnectionGeneration = &v
		},
		"row_account":          func(f *closeCapacityFixture) { f.reply.Snapshot.Positions[0].AccountID = "990000000002" },
		"row_future":           func(f *closeCapacityFixture) { f.reply.Snapshot.Positions[0].CapturedAt = "2026-11-19T09:32:00+08:00" },
		"negative_count":       func(f *closeCapacityFixture) { f.reply.Snapshot.Positions[0].Futures.Position = -1 },
		"today_exceeds_total":  func(f *closeCapacityFixture) { f.reply.Snapshot.Positions[0].Futures.TodayPosition = 11 },
		"freeze_exceeds_total": func(f *closeCapacityFixture) { f.reply.Snapshot.Positions[0].Futures.ShortFrozen = 11 },
		"net_direction":        func(f *closeCapacityFixture) { f.reply.Snapshot.Positions[0].Futures.PosiDirection = "1" },
		"shfe_date_split":      func(f *closeCapacityFixture) { f.reply.Snapshot.Positions[0].Futures.PositionDate = "2" },
		"duplicate_bucket": func(f *closeCapacityFixture) {
			f.reply.Snapshot.Positions = append(f.reply.Snapshot.Positions, f.reply.Snapshot.Positions[0])
		},
		"report_scope":           func(f *closeCapacityFixture) { f.report.Scope.Route.GatewayID = "other" },
		"unresolved_report":      func(f *closeCapacityFixture) { f.report.Blocked = true },
		"pending_fills":          func(f *closeCapacityFixture) { f.report.PendingCount = 1 },
		"report_conflicts":       func(f *closeCapacityFixture) { f.report.Conflicts = []string{"conflict"} },
		"duplicate_command":      func(f *closeCapacityFixture) { f.commands = append(f.commands, f.commands[0]) },
		"same_order_new_command": func(f *closeCapacityFixture) { r := f.commands[0]; r.CommandID++; f.commands = append(f.commands, r) },
		"command_scope":          func(f *closeCapacityFixture) { f.commands[0].Scope.Route.Env = "prod" },
		"order_side":             func(f *closeCapacityFixture) { f.commands[0].Original.Orders[0].TradeSide = "B" },
		"order_offset":           func(f *closeCapacityFixture) { f.commands[0].Original.Orders[0].OffsetType = "CT" },
		"order_yesterday":        func(f *closeCapacityFixture) { f.commands[0].Original.Orders[0].OffsetType = "CY" },
		"order_open":             func(f *closeCapacityFixture) { f.commands[0].Original.Orders[0].OffsetType = "O" },
		"zero_quantity":          func(f *closeCapacityFixture) { f.commands[0].Original.Orders[0].OrderQty = 0 },
		"order_hedge":            func(f *closeCapacityFixture) { f.commands[0].Original.Orders[0].HedgeType = "hedging" },
		"original_changed":       func(f *closeCapacityFixture) { f.commands[0].Original.Orders[0].OrderQty++ },
		"before_baseline": func(f *closeCapacityFixture) {
			f.commands[0].Original.Raw = editBody(t, f.commands[0].Original.Raw, func(m map[string]any) { m["sent_at"] = "2026-11-19T09:30:00+08:00" })
		},
		"equal_to_baseline": func(f *closeCapacityFixture) {
			f.commands[0].Original.Raw = editBody(t, f.commands[0].Original.Raw, func(m map[string]any) { m["sent_at"] = f.reply.ProducedAt })
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCloseCapacityFixture(t)
			f.add(t, 2, "S")
			mutate(f)
			if result, err := f.calculate("S"); err == nil || result != (CloseCapacity{}) {
				t.Fatal("unsafe capacity", result, err)
			}
		})
	}
}

func TestCFFEXCloseCapacityProjectionReplay(t *testing.T) {
	f := newCloseCapacityFixture(t)
	reg := f.add(t, 5, "S")
	working, cancelled := closeCapacityOrder(reg, "working", 2), closeCapacityOrder(reg, "cancelled", 2)
	fill := closeCapacityFill(f.report.Scope, working, "f1", 2)
	orderRaw, _ := json.Marshal(working)
	finalRaw, _ := json.Marshal(cancelled)
	fillRaw, _ := json.Marshal(fill)
	inputs := []struct {
		kind string
		raw  []byte
	}{{"order", orderRaw}, {"fill", fillRaw}, {"order", finalRaw}}
	var first CloseCapacity
	for _, indices := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 1, 2, 0, 2}} {
		p := newTradeProjection(f.report.Scope)
		for i, index := range indices {
			p.observe(fmt.Sprint(i), inputs[index].kind, inputs[index].raw)
		}
		// Rebuild from serialized durable facts, not mutable in-process counters.
		raw, _ := json.Marshal(p.report)
		if err := json.Unmarshal(raw, &f.report); err != nil {
			t.Fatal(err)
		}
		result, err := f.calculate("S")
		if err != nil || result.Available != 6 || result.ClosedSinceQuery != 2 || result.LocalReserved != 0 {
			t.Fatal(result, err, f.report.Conflicts)
		}
		if first == (CloseCapacity{}) {
			first = result
		} else if first != result {
			t.Fatal("order-dependent quantity")
		}
	}
	before, _ := json.Marshal([]any{f.reply, f.commands, f.report})
	_, _ = f.calculate("S")
	after, _ := json.Marshal([]any{f.reply, f.commands, f.report})
	if !reflect.DeepEqual(before, after) {
		t.Fatal("calculation mutated evidence")
	}
}

func TestCFFEXCloseCapacityRejectsInconsistentProgress(t *testing.T) {
	for name, change := range map[string]func(*OrderRecord, *ExecutionRecord){
		"blocked_order":         func(o *OrderRecord, f *ExecutionRecord) { o.Blocked = true },
		"order_qty":             func(o *OrderRecord, f *ExecutionRecord) { o.Order.OrderQty++ },
		"order_price":           func(o *OrderRecord, f *ExecutionRecord) { o.Order.LimitPrice = "3500.4" },
		"order_side":            func(o *OrderRecord, f *ExecutionRecord) { o.Order.TradeSide = "B" },
		"negative_cumulative":   func(o *OrderRecord, f *ExecutionRecord) { o.Order.CumFilledQty = -1 },
		"negative_leaves":       func(o *OrderRecord, f *ExecutionRecord) { o.Order.LeavesQty = -1 },
		"terminal_leaves":       func(o *OrderRecord, f *ExecutionRecord) { o.Order.LeavesQty = 3 },
		"overfilled":            func(o *OrderRecord, f *ExecutionRecord) { o.Order.CumFilledQty = 6 },
		"filled_qty_missing":    func(o *OrderRecord, f *ExecutionRecord) { o.Order.GatewayStatus = "filled" },
		"rejected_with_fill":    func(o *OrderRecord, f *ExecutionRecord) { o.Order.GatewayStatus = "rejected" },
		"bad_status":            func(o *OrderRecord, f *ExecutionRecord) { o.Order.GatewayStatus = "unknown" },
		"fill_exceeds_terminal": func(o *OrderRecord, f *ExecutionRecord) { f.Fill.Qty = 3 },
		"unlinked_fill":         func(o *OrderRecord, f *ExecutionRecord) { f.State = "pending" },
		"conflicting_fill":      func(o *OrderRecord, f *ExecutionRecord) { f.FactConflict = true },
		"fill_owner":            func(o *OrderRecord, f *ExecutionRecord) { f.LinkedGateway = "other" },
		"fill_identity":         func(o *OrderRecord, f *ExecutionRecord) { f.Fill.FillID = "other" },
		"negative_fill":         func(o *OrderRecord, f *ExecutionRecord) { f.Fill.Qty = -1 },
		"zero_fill":             func(o *OrderRecord, f *ExecutionRecord) { f.Fill.Qty = 0 },
		"fill_day":              func(o *OrderRecord, f *ExecutionRecord) { f.Fill.TradeDate = "20261118" },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newCloseCapacityFixture(t)
			reg := fixture.add(t, 5, "S")
			order := closeCapacityOrder(reg, "cancelled", 2)
			fill := closeCapacityFill(fixture.report.Scope, order, "f1", 2)
			record := &OrderRecord{Order: order}
			execution := &ExecutionRecord{Fill: fill, State: "linked", LinkedGateway: order.GatewayOrderID}
			fixture.report.Orders[order.GatewayOrderID] = record
			fixture.report.Executions[fill.FillID] = execution
			change(record, execution)
			if result, err := fixture.calculate("S"); err == nil || result != (CloseCapacity{}) {
				t.Fatal("inconsistent evidence released position", result, err)
			}
		})
	}
}

func TestCFFEXCloseCapacityQuantityOverflow(t *testing.T) {
	f := newCloseCapacityFixture(t)
	p := &f.reply.Snapshot.Positions[0].Futures
	p.Position, p.TodayPosition, p.ShortFrozen = math.MaxInt32, 0, 0
	f.add(t, math.MaxInt32, "S")
	if r, err := f.calculate("S"); err != nil || r.Available != 0 || r.LocalReserved != math.MaxInt32 {
		t.Fatal(r, err)
	}
	f.add(t, math.MaxInt32, "S")
	if _, err := f.calculate("S"); err == nil {
		t.Fatal("int32 overflow created capacity")
	}
}

func TestCFFEXCloseCapacityVerifiedExternalFillAlias(t *testing.T) {
	f := newCloseCapacityFixture(t)
	reg := f.add(t, 5, "S")
	order := closeCapacityOrder(reg, "cancelled", 2)
	fill := closeCapacityFill(f.report.Scope, order, "f1", 2)
	fill.GatewayOrderID = nativeIdentity("order", f.report.Scope, order.Exchange, order.NativeIDs.OrderSysID)
	o, _ := json.Marshal(order)
	x, _ := json.Marshal(fill)
	p := newTradeProjection(f.report.Scope)
	p.observe("fill", "fill", x)
	p.observe("order", "order", o)
	f.report = p.report
	result, err := f.calculate("S")
	if err != nil || result.Available != 6 || result.ClosedSinceQuery != 2 {
		t.Fatal("verified alias rejected or deducted twice", result, err)
	}
	for _, record := range f.report.Executions {
		record.Fill.GatewayOrderID = "unverified-alias"
	}
	if _, err := f.calculate("S"); err == nil {
		t.Fatal("unverified alias accepted")
	}
}
