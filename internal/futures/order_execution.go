package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"ti-relay-trader/internal/timeutil"
)

var (
	ErrOrderRequestConflict = errors.New("futures order idempotency conflict")
	ErrOrderRequestMissing  = errors.New("futures order request not found")
	ErrOrderExecutionOff    = errors.New("futures TEST order execution unavailable")
	orderRequestKey         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	orderSecurityID         = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,29}\.(SHFE|CFFEX)$`)
)

type OrderExecutionRequest struct {
	IdempotencyKey string              `json:"idempotency_key"`
	Order          OrderPreviewRequest `json:"order"`
}

func DecodeOrderExecutionRequest(raw []byte) (OrderExecutionRequest, error) {
	var r OrderExecutionRequest
	if len(raw) > 4096 {
		return r, ErrAuditCapacity
	}
	if _, err := parseReply(raw); err != nil {
		return r, err
	}
	fields, err := shape(raw, []string{"idempotency_key", "order"}, nil)
	if err != nil || json.Unmarshal(fields["idempotency_key"], &r.IdempotencyKey) != nil || !orderRequestKey.MatchString(r.IdempotencyKey) {
		return r, ErrStreamIdentity
	}
	r.Order, err = DecodeOrderPreview(fields["order"])
	if err != nil || !orderSecurityID.MatchString(r.Order.SecurityID) {
		return r, ErrStreamIdentity
	}
	return r, nil
}

// This is a command publication observation, never an order acknowledgement.
// The order/event ledger remains authoritative for actual broker state.
type OrderExecutionResult struct {
	IdempotencyKey   string              `json:"idempotency_key"`
	Order            OrderPreviewRequest `json:"order"`
	MessageID        string              `json:"message_id"`
	GatewayOrderID   string              `json:"gateway_order_id"`
	ClientOrderID    string              `json:"client_order_id"`
	Publication      QueryPublication    `json:"publication"`
	BrokerStateKnown bool                `json:"broker_state_known"`
	RetrySafe        bool                `json:"retry_safe"`
}

type OrderMarketEvidence interface {
	FuturesPriceCatalog(context.Context, string) ([]byte, error)
	FuturesPriceSnapshot(context.Context, string) ([]byte, error)
}

// Operator-owned bindings only. Neither this policy nor a session/approval is
// accepted in the public order intention. A nil service leaves HTTP disabled.
type TestOrderAccount struct {
	Source                 TestSourceConfig
	SubmitEnabled          bool
	CancelEnabled          bool
	AuthorizationExpiresAt time.Time
}

type TestOrderService struct {
	store        *CommandStore
	accounts     map[string]TestOrderAccount
	market       OrderMarketEvidence
	target       func(context.Context, CommandScope) (TestQueryTarget, error)
	cancelTarget func(context.Context, CommandScope) (TestQueryTarget, error)
	transport    func(TestSourceConfig, TestQueryTarget, TestTradeApproval) (queryPublicationTransport, func(), error)
}

func NewTestOrderService(store *CommandStore, accounts []TestOrderAccount, market OrderMarketEvidence, target func(context.Context, CommandScope) (TestQueryTarget, error)) (*TestOrderService, error) {
	if store == nil || store.db == nil || market == nil || target == nil || len(accounts) == 0 || len(accounts) > 16 {
		return nil, ErrOrderExecutionOff
	}
	s := &TestOrderService{store: store, accounts: map[string]TestOrderAccount{}, market: market, target: target, cancelTarget: target}
	for _, a := range accounts {
		scope := a.Source.Scope
		if scope.validate() != nil || scope.Route.Env != "test" || scope.Route.BrokerID != "ctp_4900" || scope.Route.AccountID != "300052141" || scope.NativeBrokerID != "4900" || scope.UserID != "300052141" || scope.InvestorID != "300052141" {
			return nil, ErrOrderExecutionOff
		}
		if _, exists := s.accounts[scope.Route.AccountID]; exists {
			return nil, ErrOrderExecutionOff
		}
		s.accounts[scope.Route.AccountID] = a
	}
	s.transport = func(c TestSourceConfig, t TestQueryTarget, a TestTradeApproval) (queryPublicationTransport, func(), error) {
		p, err := NewTestTradePublisher(c, t, a)
		if err != nil {
			return nil, nil, err
		}
		return p, func() { _ = p.Close() }, nil
	}
	return s, nil
}

func orderRequestIdentity(scope CommandScope, key string) string {
	raw, _ := json.Marshal(struct {
		Scope CommandScope
		Key   string
	}{scope, key})
	return commandDigest(raw)
}

func (s *TestOrderService) Submit(ctx context.Context, request OrderExecutionRequest) (OrderExecutionResult, error) {
	// Validate direct Go callers just as strictly as HTTP callers.
	rawRequest, err := json.Marshal(request)
	if err != nil {
		return OrderExecutionResult{}, ErrStreamIdentity
	}
	r, err := DecodeOrderExecutionRequest(rawRequest)
	if err != nil {
		return OrderExecutionResult{}, err
	}
	a, ok := s.accounts[r.Order.AccountID]
	if !ok {
		return OrderExecutionResult{}, ErrOrderExecutionOff
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Resolve persisted responsibility BEFORE checking current permissions,
	// quotes, day or session. A timeout/restart is not a new trading intention.
	old, err := s.lookup(ctx, a.Source.Scope, r.IdempotencyKey, &r.Order)
	if err == nil {
		return old, nil
	}
	if !errors.Is(err, ErrOrderRequestMissing) {
		return OrderExecutionResult{}, err
	}
	if !a.SubmitEnabled || !time.Now().Before(a.AuthorizationExpiresAt) || r.Order.TradeSide != "B" || r.Order.OffsetType != "O" || r.Order.Qty != 1 {
		return OrderExecutionResult{}, ErrOrderExecutionOff
	}
	target, err := s.target(ctx, a.Source.Scope)
	if err != nil || target.Validate() != nil || target.TradeDate != r.Order.TradeDate {
		return OrderExecutionResult{}, ErrQueryPublishGate
	}
	catalog, err := s.market.FuturesPriceCatalog(ctx, r.Order.SecurityID)
	if err != nil {
		return OrderExecutionResult{}, err
	}
	binding, tick, err := ResolveMeridianPriceCatalog(catalog, r.Order.SecurityID)
	if err != nil || binding.ExchangeID != "SHFE" || ValidatePreviewPrice(r.Order, tick) != nil {
		return OrderExecutionResult{}, ErrStreamIdentity
	}
	quote, err := s.market.FuturesPriceSnapshot(ctx, r.Order.SecurityID)
	if err != nil {
		return OrderExecutionResult{}, err
	}
	prepared, err := PrepareMeridianPrice(quote, binding, tick, target.TradeDate, time.Now())
	if err != nil {
		return OrderExecutionResult{}, err
	}
	raw, evidence := buildServiceOrder(a.Source.Scope, r, binding, time.Now())
	expires := prepared.ValidUntil
	if a.AuthorizationExpiresAt.Before(expires) {
		expires = a.AuthorizationExpiresAt
	}
	approval := TestTradeApproval{PermissionProfile: relayTestExecutionProfile,
		ExecutionPermission: &TestExecutionPermission{Submit: true},
		AssessmentID:        "relay-order-service-v1", CommandSHA256: commandDigest(raw),
		ExpiresAt: timeutil.FormatRFC3339Nano(expires), Market: &prepared.Market}
	if _, err := ValidateTestTradePreparation(raw, a.Source.Scope, target, evidence, approval); err != nil {
		return OrderExecutionResult{}, err
	}
	transport, closeTransport, err := s.transport(a.Source, target, approval)
	if err != nil {
		return OrderExecutionResult{}, err
	}
	defer closeTransport()
	// No network I/O under the registration lock. Competing requests can
	// prepare independently but only the winner is allowed to attempt publish.
	reg, err := s.store.register(ctx, a.Source.Scope, "cmd.trade", raw, evidence, func(tx *sql.Tx) (*CommandRegistration, error) {
		return serviceOrderRegistration(ctx, tx, a.Source.Scope, r.IdempotencyKey, &r.Order)
	})
	if err != nil {
		return OrderExecutionResult{}, err
	}
	if reg.Outcome == "replayed" {
		return s.lookup(ctx, a.Source.Scope, r.IdempotencyKey, &r.Order)
	}
	if reg.Outcome != "registered" {
		return OrderExecutionResult{}, ErrOrderRequestConflict
	}
	result := serviceOrderResult(reg, r.IdempotencyKey)
	pub, err := s.store.publishTestTrade(ctx, reg.Scope, reg.CommandID, reg.Original.MessageID, target, approval, transport)
	if pub.CommandID != 0 {
		result.Publication = pub
	}
	// Even a preflight failure is now a registered intention. Returning its
	// identity lets a caller reconcile it instead of inventing a new order.
	return result, err
}

func buildServiceOrder(scope CommandScope, r OrderExecutionRequest, b CatalogBinding, now time.Time) ([]byte, RegistrationEvidence) {
	id := orderRequestIdentity(scope, r.IdempotencyKey)
	orderID, clientID := "relay-web-order-"+id, "relay-web-client-"+id
	payload := struct {
		SubmitIntent
		ProductType   string `json:"product_type"`
		SchemaVersion string `json:"schema_version"`
	}{SubmitIntent{r.Order.AccountID, orderID, clientID, r.Order.TradeDate, b.SourceInstrumentID, b.ExchangeID, "F", r.Order.TradeSide, r.Order.OffsetType, r.Order.HedgeType, r.Order.Qty, r.Order.Price}, "futures", "ctp.f0.draft.2"}
	raw, _ := json.Marshal(map[string]any{
		"protocol": "relay.stream.v1", "message_type": "command", "action": "order.submit",
		"message_id": "relay-web-submit-" + id, "request_id": "relay-web-request-" + id,
		"correlation_id": "relay-web-correlation-" + id, "idempotency_key": "relay-web-idem-" + id,
		"sent_at": timeutil.FormatRFC3339Nano(now), "payload": payload,
	})
	return raw, RegistrationEvidence{CurrentTradeDate: r.Order.TradeDate, Mappings: map[string]CatalogBinding{orderID: b}}
}

func (s *TestOrderService) Lookup(ctx context.Context, account, key string) (OrderExecutionResult, error) {
	if !accountPattern.MatchString(account) || !orderRequestKey.MatchString(key) {
		return OrderExecutionResult{}, ErrStreamIdentity
	}
	a, ok := s.accounts[account]
	if !ok {
		return OrderExecutionResult{}, ErrOrderRequestMissing
	}
	return s.lookup(ctx, a.Source.Scope, key, nil)
}

func serviceOrderRegistration(ctx context.Context, tx *sql.Tx, scope CommandScope, requestKey string, expected *OrderPreviewRequest) (*CommandRegistration, error) {
	reg, err := readServiceRegistration(ctx, tx, scope, requestKey)
	if err != nil || reg == nil {
		return nil, err
	}
	id := orderRequestIdentity(scope, requestKey)
	if reg.Original.Action != "order.submit" || reg.Original.MessageID != "relay-web-submit-"+id || len(reg.Original.Orders) != 1 || reg.Original.Orders[0].GatewayOrderID != "relay-web-order-"+id || reg.Original.Orders[0].ClientOrderID != "relay-web-client-"+id {
		return nil, ErrOrderRequestConflict
	}
	if expected != nil {
		original := serviceOrderResult(*reg, requestKey).Order
		price := original.Price
		original.Price = expected.Price
		if original != *expected || !equalDecimal(string(price), string(expected.Price)) {
			return nil, ErrOrderRequestConflict
		}
	}
	return reg, nil
}

func readServiceRegistration(ctx context.Context, tx *sql.Tx, scope CommandScope, requestKey string) (*CommandRegistration, error) {
	key := string(commandRouteBytes(scope.Route))
	id := orderRequestIdentity(scope, requestKey)
	var commandID int64
	err := tx.QueryRowContext(ctx, `SELECT command_id FROM futures_commands WHERE route_key=$1 AND idempotency_key=$2`, key, "relay-web-idem-"+id).Scan(&commandID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	reg, err := readRegisteredCommand(ctx, tx, key, commandID)
	if err != nil {
		return nil, err
	}
	if reg.Scope != scope {
		return nil, ErrOrderRequestConflict
	}
	reg.Outcome = "replayed"
	return &reg, nil
}

func serviceOrderResult(reg CommandRegistration, key string) OrderExecutionResult {
	o := reg.Original.Orders[0]
	b := reg.Evidence.Mappings[o.GatewayOrderID]
	return OrderExecutionResult{IdempotencyKey: key,
		Order:     OrderPreviewRequest{o.AccountID, b.SecurityID, o.TradeDate, o.TradeSide, o.OffsetType, o.HedgeType, o.OrderQty, o.LimitPrice},
		MessageID: reg.Original.MessageID, GatewayOrderID: o.GatewayOrderID, ClientOrderID: o.ClientOrderID,
		Publication: QueryPublication{CommandID: reg.CommandID, State: "registered", Replayed: reg.Outcome == "replayed"},
	}
}

func (s *TestOrderService) lookup(ctx context.Context, scope CommandScope, key string, expected *OrderPreviewRequest) (OrderExecutionResult, error) {
	tx, err := beginFuturesTransaction(ctx, s.store.db)
	if err != nil {
		return OrderExecutionResult{}, err
	}
	defer tx.Rollback()
	reg, err := serviceOrderRegistration(ctx, tx, scope, key, expected)
	if err != nil {
		return OrderExecutionResult{}, err
	}
	if reg == nil {
		return OrderExecutionResult{}, ErrOrderRequestMissing
	}
	result := serviceOrderResult(*reg, key)
	result.Publication, err = readServicePublication(ctx, tx, *reg)
	return result, err
}

func readServicePublication(ctx context.Context, tx *sql.Tx, reg CommandRegistration) (QueryPublication, error) {
	result := QueryPublication{CommandID: reg.CommandID, State: "registered", Replayed: reg.Outcome == "replayed"}
	var state, reason string
	var entry sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT state,entry_id,reason FROM futures_test_trade_publications WHERE command_id=$1`, reg.CommandID).Scan(&state, &entry, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return QueryPublication{}, err
	}
	if !oneOf(state, "published", "not_published", "attempted") {
		return QueryPublication{}, ErrStreamIdentity
	}
	if state == "published" {
		if _, err := streamID(entry.String); err != nil || entry.String == "0-0" {
			return QueryPublication{}, ErrStreamCursor
		}
	}
	return publicationResult(reg.CommandID, state, entry.String, reason, true), nil
}
