package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

var ErrForeignQuery = errors.New("reply belongs to another query")

var routeBrokerPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
var integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

type QueryRoute struct {
	Env       string `json:"env"`
	BrokerID  string `json:"broker_id"`
	GatewayID string `json:"gateway_id"`
	AccountID string `json:"account_id"`
}

// QueryContext is caller-verified request evidence, never learned from a reply.
// Empty Ticket/TradeDate and nil integers mean genuinely unavailable evidence.
// NativeBrokerID is the configured CTP BrokerID, not the routing broker label.
type QueryContext struct {
	Route                QueryRoute
	Action               string
	OriginMessageID      string
	Ticket               string
	CounterSessionID     string
	ConnectionGeneration *int64
	SDKRequestID         *int64
	TradeDate            string
	NativeBrokerID       string
	NativeInvestorID     string               `json:",omitempty"`
	NativeUserID         string               `json:",omitempty"`
	Command              *QueryCommandBinding `json:",omitempty"`
}

type BatchState string

const (
	BatchCollecting  BatchState = "collecting"
	BatchReady       BatchState = "ready"
	BatchFailed      BatchState = "failed"
	BatchInvalid     BatchState = "invalid"
	BatchExpired     BatchState = "expired"
	BatchInterrupted BatchState = "interrupted"
)

// BatchLimits are local memory guards, not negotiated OC wire capacities.
type BatchLimits struct{ Pages, Rows, Bytes int }

func DefaultBatchLimits() BatchLimits {
	return BatchLimits{Pages: 256, Rows: maxTradeQueryRows, Bytes: maxSnapshotBytes}
}

// SnapshotCandidate is a complete query result, NOT a publication/ACK permit.
// Current-session readiness, durable audit and atomic publication are separate.
type SnapshotCandidate struct {
	Context   QueryContext
	Assets    []AssetRow
	Positions []PositionRow
	Orders    []OrderRow `json:",omitempty"`
	Fills     []FillRow  `json:",omitempty"`
}

type queryPage struct {
	messageID string
	failed    bool
	code      string
	index     int
	last      bool
	items     []json.RawMessage
}

// QueryBatch is a single-owner, in-memory assembler for four query categories.
// Callers must durably audit ALL input (including rejected bodies) before ACK.
// now/deadline are elapsed durations from one monotonic clock, not business time.
// A transport disconnect must call Interrupt; no reconnection auto-resume exists.
type QueryBatch struct {
	context      QueryContext
	limits       BatchLimits
	lastNow      time.Duration
	deadline     time.Duration
	state        BatchState
	reason       string
	pages        map[int]queryPage
	messages     map[string][]byte
	messageOrder []string
	finalIndex   int
	rowCount     int
	byteCount    int
	candidate    *SnapshotCandidate
}

func validID(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r <= 32 || r == 127 {
			return false
		}
	}
	return true
}

func copyContext(c QueryContext) QueryContext {
	c.Command = clonePtr(c.Command)
	if c.SDKRequestID != nil {
		v := *c.SDKRequestID
		c.SDKRequestID = &v
	}
	if c.ConnectionGeneration != nil {
		v := *c.ConnectionGeneration
		c.ConnectionGeneration = &v
	}
	return c
}

func NewQueryBatch(c QueryContext, now, deadline time.Duration, limits BatchLimits) (*QueryBatch, error) {
	if c.Command != nil && !validQueryCommandContext(c) {
		return nil, fmt.Errorf("invalid query command binding")
	}
	r := c.Route
	if !oneOf(r.Env, "test", "prod") || !routeBrokerPattern.MatchString(r.BrokerID) || !validID(r.GatewayID) ||
		!accountPattern.MatchString(r.AccountID) || queryResultType(c.Action) == "" ||
		!validID(c.OriginMessageID) || !validID(c.CounterSessionID) || !native(c.NativeBrokerID, 10, false) ||
		(c.Ticket != "" && !validID(c.Ticket)) || (c.TradeDate != "" && !validDay(c.TradeDate)) {
		return nil, fmt.Errorf("invalid or unsupported query context")
	}
	if isTradeQuery(c.Action) && !native(c.NativeInvestorID, 12, false) {
		return nil, fmt.Errorf("trade query requires configured native investor")
	}
	if (c.ConnectionGeneration != nil && *c.ConnectionGeneration <= 0) ||
		(c.SDKRequestID != nil && *c.SDKRequestID <= 0) ||
		(c.Ticket == "" && c.SDKRequestID != nil) || (c.Ticket != "" && c.ConnectionGeneration == nil) {
		return nil, fmt.Errorf("inconsistent request evidence")
	}
	max := DefaultBatchLimits()
	if now < 0 || deadline <= now || limits.Pages <= 0 || limits.Pages > max.Pages ||
		limits.Rows <= 0 || limits.Rows > max.Rows || limits.Bytes <= 0 || limits.Bytes > max.Bytes {
		return nil, fmt.Errorf("invalid monotonic deadline or local limits")
	}
	return &QueryBatch{context: copyContext(c), limits: limits, lastNow: now, deadline: deadline,
		state: BatchCollecting, finalIndex: -1, pages: map[int]queryPage{}, messages: map[string][]byte{}}, nil
}

func (b *QueryBatch) State() (BatchState, string) { return b.state, b.reason }

func (b *QueryBatch) invalidate(err error) error {
	b.state, b.reason, b.candidate = BatchInvalid, err.Error(), nil
	return err
}

func (b *QueryBatch) Advance(now time.Duration) error {
	if now < b.lastNow {
		return b.invalidate(fmt.Errorf("monotonic clock regressed"))
	}
	b.lastNow = now
	if b.state == BatchCollecting && now >= b.deadline {
		b.state, b.reason = BatchExpired, "query deadline reached"
	}
	if oneOf(string(b.state), string(BatchInvalid), string(BatchExpired), string(BatchInterrupted)) {
		return fmt.Errorf("query %s: %s", b.state, b.reason)
	}
	return nil
}

func (b *QueryBatch) Interrupt(reason string) {
	b.state, b.reason, b.candidate = BatchInterrupted, reason, nil
}

// Accept permits out-of-order pages. Only byte-identical message redelivery is
// replayed. Late contradictions invalidate a ready candidate; this object is not
// a transaction manager and cannot retract an already published snapshot.
func (b *QueryBatch) Accept(raw []byte, now time.Duration) (replayed bool, err error) {
	if err := b.Advance(now); err != nil {
		return false, err
	}
	if len(raw) > b.limits.Bytes {
		return false, b.invalidate(fmt.Errorf("single reply exceeds byte limit"))
	}
	p, err := decodeQueryPage(raw, b.context)
	if errors.Is(err, ErrForeignQuery) {
		return false, err
	}
	if err != nil {
		return false, b.invalidate(err)
	}
	if old, ok := b.messages[p.messageID]; ok {
		if !bytes.Equal(old, raw) {
			return false, b.invalidate(fmt.Errorf("message identity changed bytes"))
		}
		return true, nil
	}
	if b.state != BatchCollecting {
		return false, b.invalidate(fmt.Errorf("new message after query terminal"))
	}
	if len(raw) > b.limits.Bytes-b.byteCount {
		return false, b.invalidate(fmt.Errorf("query byte limit"))
	}
	if p.failed {
		if b.finalIndex >= 0 {
			return false, b.invalidate(fmt.Errorf("failure conflicts with successful terminal"))
		}
		b.remember(p.messageID, raw)
		b.state, b.reason = BatchFailed, p.code
		return false, nil
	}
	if _, duplicate := b.pages[p.index]; duplicate || p.index >= b.limits.Pages {
		return false, b.invalidate(fmt.Errorf("duplicate page identity or page limit"))
	}
	if b.finalIndex >= 0 && p.index > b.finalIndex {
		return false, b.invalidate(fmt.Errorf("page beyond terminal index"))
	}
	if p.last {
		if b.finalIndex >= 0 {
			return false, b.invalidate(fmt.Errorf("multiple terminal pages"))
		}
		for index := range b.pages {
			if index > p.index {
				return false, b.invalidate(fmt.Errorf("terminal excludes received page"))
			}
		}
	}
	if len(p.items) > b.limits.Rows-b.rowCount {
		return false, b.invalidate(fmt.Errorf("query row limit"))
	}
	b.remember(p.messageID, raw)
	b.pages[p.index] = p
	b.rowCount += len(p.items)
	if p.last {
		b.finalIndex = p.index
	}
	if b.finalIndex < 0 || len(b.pages) != b.finalIndex+1 {
		return false, nil
	}
	items := make([]json.RawMessage, 0, b.rowCount)
	for i := 0; i <= b.finalIndex; i++ {
		items = append(items, b.pages[i].items...)
	}
	joined, err := json.Marshal(items)
	if err != nil {
		return false, b.invalidate(err)
	}
	candidate := &SnapshotCandidate{Context: copyContext(b.context)}
	scope := b.scope()
	switch b.context.Action {
	case "account.asset.query":
		candidate.Assets, err = DecodeAssets(joined, scope)
	case "account.positions.query":
		candidate.Positions, err = DecodePositions(joined, scope)
	case "order.list.query":
		candidate.Orders, err = DecodeOrders(joined, b.context.tradeScope())
	case "fill.list.query":
		candidate.Fills, err = DecodeFills(joined, b.context.tradeScope())
	}
	if err != nil {
		return false, b.invalidate(fmt.Errorf("invalid complete snapshot: %w", err))
	}
	b.candidate, b.state = candidate, BatchReady
	return false, nil
}

func (b *QueryBatch) scope() SnapshotScope {
	return SnapshotScope{AccountID: b.context.Route.AccountID, TradeDate: b.context.TradeDate, NativeBrokerID: b.context.NativeBrokerID}
}

func (b *QueryBatch) remember(id string, raw []byte) {
	b.messages[id] = bytes.Clone(raw)
	b.messageOrder = append(b.messageOrder, id)
	b.byteCount += len(raw)
}

func (b *QueryBatch) Candidate() (SnapshotCandidate, error) {
	if b.state != BatchReady || b.candidate == nil {
		return SnapshotCandidate{}, fmt.Errorf("no complete valid snapshot")
	}
	c := *b.candidate
	c.Context = copyContext(c.Context)
	if c.Assets != nil {
		c.Assets = append(make([]AssetRow, 0, len(c.Assets)), c.Assets...)
	}
	if c.Positions != nil {
		c.Positions = append(make([]PositionRow, 0, len(c.Positions)), c.Positions...)
	}
	if c.Orders != nil {
		c.Orders = append(make([]OrderRow, 0, len(c.Orders)), c.Orders...)
		for i := range c.Orders {
			c.Orders[i] = cloneOrder(c.Orders[i])
		}
	}
	if c.Fills != nil {
		c.Fills = append(make([]FillRow, 0, len(c.Fills)), c.Fills...)
		for i := range c.Fills {
			c.Fills[i] = cloneFill(c.Fills[i])
		}
	}
	return c, nil
}

// AcceptedMessages returns copies in arrival order, not a durable audit log.
// Invalid/unroutable input is deliberately the caller's audit responsibility.
func (b *QueryBatch) AcceptedMessages() [][]byte {
	result := make([][]byte, 0, len(b.messageOrder))
	for _, id := range b.messageOrder {
		result = append(result, bytes.Clone(b.messages[id]))
	}
	return result
}

type rawObject map[string]json.RawMessage

func parseReply(raw []byte) (rawObject, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid reply UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := scanValue(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing reply data")
	}
	var obj rawObject
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("reply must be an object")
	}
	return obj, nil
}

func shape(raw []byte, required, optional []string) (rawObject, error) {
	var obj rawObject
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("expected object")
	}
	for _, k := range required {
		if _, ok := obj[k]; !ok {
			return nil, fmt.Errorf("missing %s", k)
		}
	}
	for k := range obj {
		if !oneOf(k, required...) && !oneOf(k, optional...) {
			return nil, fmt.Errorf("unknown member %s", k)
		}
	}
	return obj, nil
}

func textField(obj rawObject, key string) string {
	var s string
	_ = json.Unmarshal(obj[key], &s)
	return s
}

func literal(raw []byte, value string) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte(value)) }

func nullableInteger(raw []byte) (*int64, error) {
	if literal(raw, "null") {
		return nil, nil
	}
	s := string(bytes.TrimSpace(raw))
	if !integerPattern.MatchString(s) {
		return nil, fmt.Errorf("expected integer token or explicit null")
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return &v, err
}

func sameInteger(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func decodeQueryPage(raw []byte, c QueryContext) (queryPage, error) {
	bad := func(s string) (queryPage, error) { return queryPage{}, fmt.Errorf("invalid query page: %s", s) }
	obj, err := parseReply(raw)
	if err != nil {
		return queryPage{}, err
	}
	route, err := shape(obj["routing"], []string{"env", "broker_id", "gateway_id", "account_id"}, nil)
	if err != nil {
		return bad("routing shape")
	}
	r := QueryRoute{Env: textField(route, "env"), BrokerID: textField(route, "broker_id"), GatewayID: textField(route, "gateway_id"), AccountID: textField(route, "account_id")}
	if !validID(textField(obj, "origin_message_id")) {
		return bad("missing or invalid origin message")
	}
	if r != c.Route || textField(obj, "origin_message_id") != c.OriginMessageID {
		return queryPage{}, ErrForeignQuery
	}
	obj, err = shape(raw, []string{"protocol", "message_type", "message_id", "origin_message_id", "produced_at", "routing", "action", "status", "code", "result_type", "payload", "adapter_context"},
		[]string{"request_id", "correlation_id", "idempotency_key", "message", "chunk"})
	if err != nil {
		return queryPage{}, err
	}
	for _, key := range []string{"message_id", "origin_message_id", "code", "request_id", "correlation_id", "idempotency_key"} {
		if value, present := obj[key]; present && (!validID(textField(obj, key)) || literal(value, "null")) {
			return bad("message identifier")
		}
	}
	if c.Command != nil {
		for key, want := range map[string]string{"request_id": c.Command.RequestID, "correlation_id": c.Command.CorrelationID, "idempotency_key": c.Command.IdempotencyKey} {
			if textField(obj, key) != want {
				return bad("original command correlation differs")
			}
		}
	}
	if msg, ok := obj["message"]; ok && (len(msg) == 0 || bytes.TrimSpace(msg)[0] != '"') {
		return bad("message must be a string")
	}
	resultType := queryResultType(c.Action)
	if textField(obj, "protocol") != "relay.stream.v1" || textField(obj, "message_type") != "reply" ||
		textField(obj, "action") != c.Action || textField(obj, "result_type") != resultType || !validTime(textField(obj, "produced_at")) {
		return bad("envelope context")
	}
	payload, err := shape(obj["payload"], []string{"product_type", "schema_version"}, []string{"account_id", "trade_date", "items"})
	if err != nil {
		return queryPage{}, err
	}
	if textField(payload, "product_type") != "futures" || textField(payload, "schema_version") != "ctp.f0.draft.2" {
		return bad("product or schema")
	}
	if _, ok := payload["account_id"]; ok && textField(payload, "account_id") != c.Route.AccountID {
		return bad("payload account")
	}
	if _, ok := payload["trade_date"]; ok && (!validDay(textField(payload, "trade_date")) || textField(payload, "trade_date") != c.TradeDate) {
		return bad("TradingDay")
	}
	var adapter rawObject
	if json.Unmarshal(obj["adapter_context"], &adapter) != nil || adapter == nil {
		return bad("adapter context")
	}
	e, err := shape(adapter["query_evidence"], []string{"ticket", "sdk_request_id", "sdk_return_code", "callback_error_id", "connection_generation", "counter_session_id", "snapshot_applied"}, []string{"failure_stage"})
	if err != nil {
		return queryPage{}, err
	}
	ticket := textField(e, "ticket")
	if (ticket == "" && !literal(e["ticket"], "null")) || ticket != c.Ticket || textField(e, "counter_session_id") != c.CounterSessionID {
		return bad("ticket or session")
	}
	request, err1 := nullableInteger(e["sdk_request_id"])
	generation, err2 := nullableInteger(e["connection_generation"])
	syncCode, err3 := nullableInteger(e["sdk_return_code"])
	callback, err4 := nullableInteger(e["callback_error_id"])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || !sameInteger(request, c.SDKRequestID) || !sameInteger(generation, c.ConnectionGeneration) {
		return bad("request evidence integers or context")
	}
	stage := textField(e, "failure_stage")
	if _, present := e["failure_stage"]; present && !validID(stage) {
		return bad("failure stage")
	}
	status := textField(obj, "status")
	p := queryPage{messageID: textField(obj, "message_id"), code: textField(obj, "code"), failed: status == "failed"}
	if p.failed {
		_, hasItems := payload["items"]
		_, hasChunk := obj["chunk"]
		if p.code == "OK" || hasItems || hasChunk || !literal(e["snapshot_applied"], "false") {
			return bad("failed reply must not publish items")
		}
		if (ticket == "" || stage == "before_enqueue") && (ticket != "" || stage != "before_enqueue" || request != nil || syncCode != nil || callback != nil) {
			return bad("before enqueue evidence")
		}
		if stage == "queue_expired" && (request != nil || syncCode != nil || callback != nil) {
			return bad("queue expiry fabricated SDK facts")
		}
		return p, nil
	}
	if !oneOf(status, "partial", "completed") || p.code != "OK" || ticket == "" || request == nil || generation == nil ||
		syncCode == nil || *syncCode != 0 || (callback != nil && *callback != 0) || !literal(e["snapshot_applied"], "true") ||
		!validDay(textField(payload, "trade_date")) || stage == "before_enqueue" || stage == "queue_expired" {
		return bad("success without complete evidence")
	}
	chunk, err := shape(obj["chunk"], []string{"index", "is_last"}, nil)
	if err != nil {
		return queryPage{}, err
	}
	var index Count
	if err := index.UnmarshalJSON(chunk["index"]); err != nil || !literal(chunk["is_last"], strconv.FormatBool(status == "completed")) {
		return bad("chunk index or terminal flag")
	}
	p.index, p.last = int(index), status == "completed"
	scope := SnapshotScope{AccountID: c.Route.AccountID, TradeDate: c.TradeDate, NativeBrokerID: c.NativeBrokerID}
	switch c.Action {
	case "account.asset.query":
		_, err = DecodeAssets(payload["items"], scope)
	case "account.positions.query":
		_, err = DecodePositions(payload["items"], scope)
	case "order.list.query":
		_, err = DecodeOrders(payload["items"], c.tradeScope())
	case "fill.list.query":
		_, err = DecodeFills(payload["items"], c.tradeScope())
	}
	if err != nil {
		return queryPage{}, err
	}
	if err := json.Unmarshal(payload["items"], &p.items); err != nil {
		return queryPage{}, err
	}
	return p, nil
}

func isTradeQuery(action string) bool { return oneOf(action, "order.list.query", "fill.list.query") }

func queryResultType(action string) string {
	switch action {
	case "account.asset.query":
		return "asset_page"
	case "account.positions.query":
		return "position_page"
	case "order.list.query":
		return "order_page"
	case "fill.list.query":
		return "fill_page"
	default:
		return ""
	}
}

func (c QueryContext) tradeScope() TradeScope {
	return TradeScope{Route: c.Route, NativeBrokerID: c.NativeBrokerID, InvestorID: c.NativeInvestorID, TradeDate: c.TradeDate}
}
