package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"ti-relay-trader/internal/timeutil"
)

type CancelExecutionRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	AccountID      string `json:"account_id"`
	GatewayOrderID string `json:"gateway_order_id"`
	TradeDate      string `json:"trade_date"`
}

type CancelExecutionResult struct {
	CancelExecutionRequest
	MessageID        string           `json:"message_id"`
	CancelID         string           `json:"cancel_id"`
	Publication      QueryPublication `json:"publication"`
	BrokerStateKnown bool             `json:"broker_state_known"`
	RetrySafe        bool             `json:"retry_safe"`
}

func DecodeCancelExecutionRequest(raw []byte) (CancelExecutionRequest, error) {
	var r CancelExecutionRequest
	if len(raw) > 4096 {
		return r, ErrAuditCapacity
	}
	if _, err := parseReply(raw); err != nil {
		return r, err
	}
	if _, err := shape(raw, []string{"idempotency_key", "account_id", "gateway_order_id", "trade_date"}, nil); err != nil {
		return r, err
	}
	if json.Unmarshal(raw, &r) != nil || !orderRequestKey.MatchString(r.IdempotencyKey) || !accountPattern.MatchString(r.AccountID) || !validID(r.GatewayOrderID) || len(r.GatewayOrderID) > 128 || !validDay(r.TradeDate) {
		return r, ErrStreamIdentity
	}
	return r, nil
}

func (s *TestOrderService) Cancel(ctx context.Context, request CancelExecutionRequest) (CancelExecutionResult, error) {
	rawRequest, _ := json.Marshal(request)
	r, err := DecodeCancelExecutionRequest(rawRequest)
	if err != nil {
		return CancelExecutionResult{}, err
	}
	a, ok := s.accounts[r.AccountID]
	if !ok {
		return CancelExecutionResult{}, ErrOrderExecutionOff
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	old, err := s.lookupCancel(ctx, a.Source.Scope, r.IdempotencyKey, &r)
	if err == nil {
		return old, nil
	}
	if !errors.Is(err, ErrOrderRequestMissing) {
		return CancelExecutionResult{}, err
	}
	if !a.CancelEnabled || !time.Now().Before(a.AuthorizationExpiresAt) {
		return CancelExecutionResult{}, ErrOrderExecutionOff
	}
	target, err := s.cancelTarget(ctx, a.Source.Scope)
	if err != nil || target.Validate() != nil || target.TradeDate != r.TradeDate {
		return CancelExecutionResult{}, ErrQueryPublishGate
	}
	if _, _, err := readCaptureHealth(ctx, s.store.db, a.Source.Scope, target); err != nil {
		return CancelExecutionResult{}, err
	}
	// The browser supplies neither a receipt nor a native order identity.
	// Resolve both from the published local command and retained OC evidence.
	tx, err := beginFuturesTransaction(ctx, s.store.db)
	if err != nil {
		return CancelExecutionResult{}, err
	}
	parent, err := serviceCancelParent(ctx, tx, a.Source.Scope, target, r)
	var receipt int64
	if err == nil {
		receipt, err = latestServiceCancelReceipt(ctx, tx, parent, target)
	}
	tx.Rollback()
	if err != nil {
		// A concurrent identical request may have published its cancellation
		// after our first lookup. Its durable record wins over the new-budget
		// or latest-order check; no second publication is attempted.
		if existing, lookupErr := s.lookupCancel(ctx, a.Source.Scope, r.IdempotencyKey, &r); lookupErr == nil {
			return existing, nil
		}
		return CancelExecutionResult{}, err
	}
	raw, evidence := buildServiceCancel(a.Source.Scope, r, time.Now())
	expires := time.Now().Add(5 * time.Second)
	if a.AuthorizationExpiresAt.Before(expires) {
		expires = a.AuthorizationExpiresAt
	}
	approval := TestTradeApproval{PermissionProfile: relayTestExecutionProfile,
		ExecutionPermission: &TestExecutionPermission{Cancel: true}, AssessmentID: "relay-order-service-v1",
		CommandSHA256: commandDigest(raw), ExpiresAt: timeutil.FormatRFC3339Nano(expires), CancelReceiptID: receipt}
	if o := parent.Original.Orders[0]; a.Markets != nil {
		approval.MarketPolicy = a.marketPolicy(o.Exchange)
		if approval.MarketPolicy == nil {
			return CancelExecutionResult{}, ErrOrderExecutionOff
		}
		// Cancel is independent of new-order quantity and close-model policy.
	} else if o.Exchange == "CFFEX" {
		if !cffexIndexSymbol.MatchString(o.Symbol) {
			return CancelExecutionResult{}, ErrStreamIdentity
		}
		// Revoking new-order product/quantity permission must not disable an
		// independently authorized cancel of an existing published local order.
		approval.CFFEX = &CFFEXExecutionPolicy{[]string{o.Symbol[:2]}, o.OrderQty}
	}
	if _, err := ValidateTestTradePreparation(raw, a.Source.Scope, target, evidence, approval); err != nil {
		return CancelExecutionResult{}, err
	}
	transport, closeTransport, err := s.transport(a.Source, target, approval)
	if err != nil {
		return CancelExecutionResult{}, err
	}
	defer closeTransport()
	transport = &captureGuardTransport{inner: transport, db: s.store.db, scope: a.Source.Scope, target: target}
	reg, err := s.store.register(ctx, a.Source.Scope, "cmd.trade", raw, evidence, func(tx *sql.Tx) (*CommandRegistration, error) {
		return serviceCancelRegistration(ctx, tx, a.Source.Scope, r.IdempotencyKey, &r)
	})
	if err != nil {
		return CancelExecutionResult{}, err
	}
	if reg.Outcome == "replayed" {
		return s.lookupCancel(ctx, a.Source.Scope, r.IdempotencyKey, &r)
	}
	if reg.Outcome != "registered" {
		return CancelExecutionResult{}, ErrOrderRequestConflict
	}
	result := serviceCancelResult(reg, r.IdempotencyKey)
	pub, err := s.store.publishTestTrade(ctx, reg.Scope, reg.CommandID, reg.Original.MessageID, target, approval, transport)
	if pub.CommandID > 0 {
		result.Publication = pub
	}
	return result, err
}

func buildServiceCancel(scope CommandScope, r CancelExecutionRequest, now time.Time) ([]byte, RegistrationEvidence) {
	id := orderRequestIdentity(scope, r.IdempotencyKey)
	raw, _ := json.Marshal(map[string]any{
		"protocol": "relay.stream.v1", "message_type": "command", "action": "order.cancel",
		"message_id": "relay-web-cancel-" + id, "request_id": "relay-web-request-" + id,
		"correlation_id": "relay-web-correlation-" + id, "idempotency_key": "relay-web-idem-" + id,
		"sent_at": timeutil.FormatRFC3339Nano(now),
		"payload": map[string]any{"account_id": r.AccountID, "product_type": "futures", "schema_version": "ctp.f0.draft.2",
			"gateway_order_id": r.GatewayOrderID, "trade_date": r.TradeDate, "cancel_id": "relay-web-cancel-id-" + id},
	})
	return raw, RegistrationEvidence{CancelTarget: &CancelTarget{scope, r.GatewayOrderID, r.TradeDate}}
}

func serviceCancelRegistration(ctx context.Context, tx *sql.Tx, scope CommandScope, key string, expected *CancelExecutionRequest) (*CommandRegistration, error) {
	reg, err := readServiceRegistration(ctx, tx, scope, key)
	if err != nil || reg == nil {
		return nil, err
	}
	id := orderRequestIdentity(scope, key)
	if reg.Original.Action != "order.cancel" || reg.Original.MessageID != "relay-web-cancel-"+id || reg.Original.Cancel == nil || reg.Original.Cancel.CancelID != "relay-web-cancel-id-"+id {
		return nil, ErrOrderRequestConflict
	}
	if expected != nil && serviceCancelResult(*reg, key).CancelExecutionRequest != *expected {
		return nil, ErrOrderRequestConflict
	}
	return reg, nil
}

func serviceCancelResult(reg CommandRegistration, key string) CancelExecutionResult {
	c := reg.Original.Cancel
	return CancelExecutionResult{CancelExecutionRequest: CancelExecutionRequest{key, reg.Scope.Route.AccountID, c.GatewayOrderID, c.TradeDate},
		MessageID: reg.Original.MessageID, CancelID: c.CancelID,
		Publication: QueryPublication{CommandID: reg.CommandID, State: "registered", Replayed: reg.Outcome == "replayed"}}
}

func (s *TestOrderService) LookupCancel(ctx context.Context, account, key string) (CancelExecutionResult, error) {
	if !accountPattern.MatchString(account) || !orderRequestKey.MatchString(key) {
		return CancelExecutionResult{}, ErrStreamIdentity
	}
	a, ok := s.accounts[account]
	if !ok {
		return CancelExecutionResult{}, ErrOrderRequestMissing
	}
	return s.lookupCancel(ctx, a.Source.Scope, key, nil)
}

func (s *TestOrderService) lookupCancel(ctx context.Context, scope CommandScope, key string, expected *CancelExecutionRequest) (CancelExecutionResult, error) {
	tx, err := beginFuturesTransaction(ctx, s.store.db)
	if err != nil {
		return CancelExecutionResult{}, err
	}
	defer tx.Rollback()
	reg, err := serviceCancelRegistration(ctx, tx, scope, key, expected)
	if err != nil {
		return CancelExecutionResult{}, err
	}
	if reg == nil {
		return CancelExecutionResult{}, ErrOrderRequestMissing
	}
	result := serviceCancelResult(*reg, key)
	result.Publication, err = readServicePublication(ctx, tx, *reg)
	return result, err
}

func serviceCancelParent(ctx context.Context, tx *sql.Tx, scope CommandScope, target TestQueryTarget, r CancelExecutionRequest) (CommandRegistration, error) {
	key := string(commandRouteBytes(scope.Route))
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT command_id FROM futures_command_reservations WHERE route_key=$1 AND kind='gateway' AND trade_date=$2 AND identity=$3`, key, r.TradeDate, r.GatewayOrderID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandRegistration{}, ErrOrderRequestMissing
	}
	if err != nil {
		return CommandRegistration{}, err
	}
	parent, err := readRegisteredCommand(ctx, tx, key, id)
	if err != nil {
		return parent, err
	}
	if parent.Scope != scope || parent.Original.Action != "order.submit" || len(parent.Original.Orders) != 1 || parent.Original.Orders[0].GatewayOrderID != r.GatewayOrderID || parent.Original.Orders[0].TradeDate != r.TradeDate {
		return CommandRegistration{}, ErrStreamIdentity
	}
	targetRaw, _ := json.Marshal(target)
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT state='published' AND target_json->'target'=$2::jsonb AND target_json->'approval'->>'assessment_id'='relay-order-service-v1' AND target_json->'approval'->>'permission_profile'='relay_test_execution.v1' FROM futures_test_trade_publications WHERE command_id=$1 AND action='order.submit'`, id, targetRaw).Scan(&valid)
	if err != nil || !valid {
		return CommandRegistration{}, ErrQueryPublishGate
	}
	// Keep the existing one-cancel-attempt budget. A rejected/unknown cancel
	// does not grant a replacement identity or automatic retry.
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_test_trade_publications c JOIN futures_test_trade_publications p ON c.assessment_key=p.assessment_key WHERE p.command_id=$1 AND c.action='order.cancel')`, id).Scan(&exists)
	if err != nil {
		return CommandRegistration{}, err
	}
	if exists {
		return CommandRegistration{}, ErrOrderRequestConflict
	}
	return parent, nil
}
