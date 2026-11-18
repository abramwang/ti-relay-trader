package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// TestTradeApproval is local operator evidence, not an extension of OC wire.
// Legacy assessments reserve one submit/cancel; explicit day mode reserves each
// original order independently. Both retain unknown attempts without resending.
type TestTradeApproval struct {
	PermissionProfile   string                   `json:"permission_profile,omitempty"`
	ExecutionPermission *TestExecutionPermission `json:"execution_permission,omitempty"`
	AssessmentID        string                   `json:"assessment_id"`
	CommandSHA256       string                   `json:"command_sha256"`
	ExpiresAt           string                   `json:"expires_at"`
	CancelReceiptID     int64                    `json:"cancel_receipt_id"`
	Market              *TestTradeMarket         `json:"market"`
}

// TestExecutionPermission is local, command-bound permission, never OC wire.
type TestExecutionPermission struct {
	Submit bool `json:"submit"`
	Cancel bool `json:"cancel"`
}

const relayTestExecutionProfile = "relay_test_execution.v1"

func (a TestTradeApproval) relayExecution() bool {
	return a.PermissionProfile == relayTestExecutionProfile
}

type TestTradeMarket struct {
	MeridianSnapshot      []byte                    `json:"meridian_snapshot,omitempty"`
	NativeFacts           *TestNativeFactsReference `json:"native_facts,omitempty"`
	PriceSource           string                    `json:"price_source,omitempty"`
	SecurityID            string                    `json:"security_id"`
	SourceInstrumentID    string                    `json:"source_instrument_id"`
	ExchangeID            string                    `json:"exchange_id"`
	TradeDate             string                    `json:"trade_date"`
	ObservedAt            string                    `json:"observed_at"`
	PublicationSnapshotID string                    `json:"publication_snapshot_id"`
	LogicalTableChecksum  string                    `json:"logical_table_checksum"`
	Stale                 bool                      `json:"stale"`
	PriceTick             Price                     `json:"price_tick"`
	BestBid               Price                     `json:"best_bid,omitempty"`
	LowerLimit            Price                     `json:"lower_limit"`
	UpperLimit            Price                     `json:"upper_limit"`
}

func DecodeTestTradeApproval(raw []byte) (TestTradeApproval, error) {
	var a TestTradeApproval
	// A bounded 32KiB original Meridian body is base64 encoded inside this
	// local evidence object. Keep the envelope bounded without rejecting an
	// otherwise valid snapshot solely because of encoding expansion.
	if len(raw) > 65536 {
		return a, ErrStreamIdentity
	}
	if _, err := parseReply(raw); err != nil {
		return a, err
	}
	fields, err := shape(raw, []string{"assessment_id", "command_sha256", "expires_at", "cancel_receipt_id", "market"}, []string{"permission_profile", "execution_permission"})
	if err != nil {
		return a, err
	}
	if !literal(fields["market"], "null") {
		market, err := shape(fields["market"], []string{"security_id", "source_instrument_id", "exchange_id", "trade_date", "observed_at", "publication_snapshot_id", "logical_table_checksum", "stale", "price_tick", "lower_limit", "upper_limit"}, []string{"price_source", "best_bid", "native_facts", "meridian_snapshot"})
		if err != nil {
			return a, err
		}
		if !literal(market["stale"], "true") && !literal(market["stale"], "false") {
			return a, ErrStreamIdentity
		}
		if ref, ok := market["native_facts"]; ok {
			if _, err := shape(ref, []string{"command_id", "receipt_id", "body_sha256", "instrument_started_at", "depth_started_at"}, nil); err != nil {
				return a, err
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return a, err
	}
	if p, present := fields["permission_profile"]; present && (!oneOf(a.PermissionProfile, "test_day_relay.v1", relayTestExecutionProfile) || literal(p, "null")) {
		return a, ErrStreamIdentity
	}
	permission, present := fields["execution_permission"]
	if a.relayExecution() {
		p, err := shape(permission, []string{"submit", "cancel"}, nil)
		if err != nil || a.ExecutionPermission == nil {
			return a, ErrStreamIdentity
		}
		for _, key := range []string{"submit", "cancel"} {
			if !literal(p[key], "true") && !literal(p[key], "false") {
				return a, ErrStreamIdentity
			}
		}
	} else if present {
		return a, ErrStreamIdentity
	}
	if !validID(a.AssessmentID) || len(a.AssessmentID) > 128 || len(a.CommandSHA256) != 64 || strings.Trim(a.CommandSHA256, "0123456789abcdef") != "" || !validTime(a.ExpiresAt) || a.CancelReceiptID < 0 {
		return a, ErrStreamIdentity
	}
	if a.Market != nil {
		if a.Market.PriceSource == meridianPriceSource {
			if !a.relayExecution() || verifyMeridianMarket(*a.Market) != nil {
				return a, ErrStreamIdentity
			}
		} else if a.Market.MeridianSnapshot != nil {
			return a, ErrStreamIdentity
		}
		if a.Market.PriceSource == nativeFactsPriceSource {
			if !a.relayExecution() || a.Market.NativeFacts == nil || a.Market.NativeFacts.validate() != nil {
				return a, ErrStreamIdentity
			}
		} else if a.Market.NativeFacts != nil {
			return a, ErrStreamIdentity
		}
	}
	return a, nil
}

// ValidateTestTradePreparation performs no I/O and does not authorize sending.
func ValidateTestTradePreparation(raw []byte, scope CommandScope, target TestQueryTarget, e RegistrationEvidence, a TestTradeApproval) (Command, error) {
	c, err := DecodeCommand(raw, scope, "cmd.trade")
	if err != nil {
		return c, err
	}
	if target.Validate() != nil || checkRegistrationEvidence(c, scope, e) != "" {
		return c, ErrStreamIdentity
	}
	approvalRaw, _ := json.Marshal(a)
	if _, err := DecodeTestTradeApproval(approvalRaw); err != nil {
		return c, err
	}
	return c, validateTestTrade(CommandRegistration{Scope: scope, Original: c, Evidence: e}, target, a)
}

func validateTestTrade(c CommandRegistration, target TestQueryTarget, a TestTradeApproval) error {
	if c.Scope.Route.Env != "test" || commandDigest(c.Original.Raw) != a.CommandSHA256 {
		return ErrStreamIdentity
	}
	if a.PermissionProfile != "" && (c.Scope.Route.BrokerID != "ctp_4900" || c.Scope.Route.AccountID != "300052141" || c.Scope.NativeBrokerID != "4900" || c.Scope.InvestorID != "300052141" || c.Scope.UserID != "300052141") {
		return ErrStreamIdentity
	}
	if a.relayExecution() && !a.executionActionAllowed(c.Original.Action) {
		return ErrQueryPublishGate
	}
	if c.Original.Action == "order.cancel" {
		if c.Original.Cancel == nil || c.Original.Cancel.TradeDate != target.TradeDate || a.CancelReceiptID <= 0 || a.Market != nil {
			return ErrStreamIdentity
		}
		return nil
	}
	if c.Original.Action != "order.submit" || len(c.Original.Orders) != 1 || a.CancelReceiptID != 0 || a.Market == nil {
		return ErrStreamIdentity
	}
	o, m := c.Original.Orders[0], a.Market
	if o.OrderQty != 1 || o.TradeSide != "B" || o.OffsetType != "O" || o.HedgeType != "speculation" || o.Exchange != "SHFE" || o.TradeDate != target.TradeDate || m.TradeDate != target.TradeDate || m.Stale || !validTime(m.ObservedAt) {
		return ErrStreamIdentity
	}
	b := c.Evidence.Mappings[o.GatewayOrderID]
	if b.SecurityID != m.SecurityID || b.SourceInstrumentID != m.SourceInstrumentID || b.ExchangeID != m.ExchangeID || b.PublicationSnapshotID != m.PublicationSnapshotID || b.LogicalTableChecksum != m.LogicalTableChecksum {
		return ErrStreamIdentity
	}
	nativeLower := m.PriceSource == "oc_test_native_lower_limit.v1"
	nativeFacts := m.PriceSource == nativeFactsPriceSource
	meridian := m.PriceSource == meridianPriceSource
	if m.PriceSource != "" && !nativeLower && !nativeFacts && !meridian {
		return ErrStreamIdentity
	}
	if meridian {
		if !a.relayExecution() {
			return ErrStreamIdentity
		}
		if err := verifyMeridianMarket(*m); err != nil {
			return err
		}
		// The strategy owns the price; final trading limits are broker decisions.
		// Do not inherit the assessment's bid-minus-ten preference here.
		return validatePriceTick(o.LimitPrice, m.PriceTick)
	}
	if nativeFacts {
		if !a.relayExecution() || m.NativeFacts == nil || m.NativeFacts.validate() != nil {
			return ErrStreamIdentity
		}
		ceiling, err := nativeTestBuyPrice(*m)
		if err != nil {
			return err
		}
		if err := validateLimitOrderPrice(o.LimitPrice, m.PriceTick, m.LowerLimit, m.UpperLimit); err != nil {
			return err
		}
		p, _ := checkedPositivePrice(o.LimitPrice)
		cap, _ := checkedPositivePrice(ceiling)
		if p.Cmp(cap) > 0 {
			return priceAdmissionError("test_resting_price_required")
		}
		return nil
	}
	if nativeLower {
		if a.PermissionProfile != "test_day_relay.v1" || o.Symbol != "rb2701" || m.SourceInstrumentID != "rb2701" || m.BestBid != "" {
			return ErrStreamIdentity
		}
	}
	if err := validateLimitOrderPrice(o.LimitPrice, m.PriceTick, m.LowerLimit, m.UpperLimit); err != nil {
		return err
	}
	price, _ := checkedPositivePrice(o.LimitPrice)
	tick, _ := checkedPositivePrice(m.PriceTick)
	low, _ := checkedPositivePrice(m.LowerLimit)
	high, _ := checkedPositivePrice(m.UpperLimit)
	if nativeLower {
		// No bid is implied: the TEST-only lower-limit preference is not
		// a guarantee against execution.
		if price.Cmp(low) != 0 {
			return priceAdmissionError("test_lower_limit_required")
		}
		return nil
	}
	bid, ok := checkedPositivePrice(m.BestBid)
	if !ok || bid.Cmp(low) < 0 || bid.Cmp(high) > 0 {
		return priceAdmissionError("invalid_reference_bid")
	}
	// A resting-price preference is not a guarantee against execution.
	ceiling := new(big.Rat).Sub(bid, new(big.Rat).Mul(tick, big.NewRat(10, 1)))
	if price.Cmp(ceiling) > 0 {
		return priceAdmissionError("test_resting_price_required")
	}
	return nil
}

func (s *CommandStore) PublishTestTrade(ctx context.Context, id int64, messageID string, p *TestTradePublisher) (QueryPublication, error) {
	if p == nil {
		return QueryPublication{}, ErrStreamIdentity
	}
	return s.publishTestTrade(ctx, p.base.scope, id, messageID, p.base.target, p.approval, p)
}

// The transport validates heartbeat and market age. This method owns the
// persistent budget and verifies a cancel against an actual captured OC event.
func (s *CommandStore) publishTestTrade(ctx context.Context, scope CommandScope, id int64, messageID string, target TestQueryTarget, a TestTradeApproval, transport queryPublicationTransport) (QueryPublication, error) {
	approvalRaw, _ := json.Marshal(a)
	if _, err := DecodeTestTradeApproval(approvalRaw); err != nil {
		return QueryPublication{}, err
	}
	binding, _ := json.Marshal(struct {
		Target   TestQueryTarget   `json:"target"`
		Approval TestTradeApproval `json:"approval"`
	}{target, a})
	assessment := string(framed("ctp.test.assessment.v1/", string(commandRouteBytes(scope.Route)), a.AssessmentID))
	check := func(c CommandRegistration) error { return validateTestTrade(c, target, a) }
	insert := func(ctx context.Context, tx *sql.Tx, c CommandRegistration, binding, proof []byte) error {
		if a.Market != nil && a.Market.PriceSource == nativeFactsPriceSource {
			if err := verifyTestNativeMarket(ctx, tx, c, target, *a.Market); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, assessment); err != nil {
			return err
		}
		var mixed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_test_trade_publications p JOIN futures_commands c USING(command_id) WHERE c.route_key=$1 AND p.target_json->'approval'->>'assessment_id'=$2 AND COALESCE(p.target_json->'approval'->>'permission_profile','')<>$3)`, string(commandRouteBytes(scope.Route)), a.AssessmentID, a.PermissionProfile).Scan(&mixed); err != nil {
			return err
		}
		if mixed {
			return ErrStreamIdentity
		}
		key := assessment
		if a.PermissionProfile == "test_day_relay.v1" || a.relayExecution() {
			// Day mode reserves per original order, not per process. Existing
			// command idempotency and unknown-attempt records remain unchanged.
			var legacy bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_test_trade_publications WHERE assessment_key=$1)`, assessment).Scan(&legacy); err != nil {
				return err
			}
			if legacy {
				return ErrStreamIdentity
			}
			gateway, day := "", ""
			if c.Original.Cancel != nil {
				gateway, day = c.Original.Cancel.GatewayOrderID, c.Original.Cancel.TradeDate
			} else {
				gateway, day = c.Original.Orders[0].GatewayOrderID, c.Original.Orders[0].TradeDate
			}
			key = string(framed("ctp.test.day-order.v1/", string(commandRouteBytes(scope.Route)), a.AssessmentID, day, gateway))
		}
		if c.Original.Action == "order.cancel" {
			if err := verifyAssessmentCancel(ctx, tx, key, c, target, a); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO futures_test_trade_publications(command_id,assessment_key,action,target_json,proof_json,state) VALUES($1,$2,$3,$4,$5,'attempted')`, c.CommandID, key, c.Original.Action, binding, proof)
		return err
	}
	return s.publishTestRegistered(ctx, scope, id, messageID, target, transport, "futures_test_trade_publications", binding, check, insert)
}

func verifyAssessmentCancel(ctx context.Context, tx *sql.Tx, assessment string, c CommandRegistration, target TestQueryTarget, a TestTradeApproval) error {
	var parentID int64
	targetRaw, _ := json.Marshal(target)
	if err := tx.QueryRowContext(ctx, `SELECT command_id FROM futures_test_trade_publications WHERE assessment_key=$1 AND action='order.submit' AND state IN ('attempted','published') AND target_json->'target'=$2::jsonb`, assessment, targetRaw).Scan(&parentID); err != nil {
		return fmt.Errorf("cancel requires this assessment's original submit: %w", err)
	}
	parent, err := readRegisteredCommand(ctx, tx, string(commandRouteBytes(c.Scope.Route)), parentID)
	if err != nil || len(parent.Original.Orders) != 1 {
		return ErrStreamIdentity
	}
	o := parent.Original.Orders[0]
	if c.Original.Cancel.GatewayOrderID != o.GatewayOrderID || c.Original.Cancel.TradeDate != o.TradeDate {
		return ErrStreamIdentity
	}
	var raw, digest, configRaw []byte
	var stream, state string
	if err := tx.QueryRowContext(ctx, `SELECT i.body,i.body_sha256,i.stream_key,i.state,r.config_json FROM futures_stream_inbox i JOIN futures_stream_receivers r USING(stream_key) WHERE i.receipt_id=$1`, a.CancelReceiptID).Scan(&raw, &digest, &stream, &state, &configRaw); err != nil {
		return err
	}
	h := sha256.Sum256(raw)
	var config StreamConfig
	expected := strings.TrimSuffix(queryReplyStream(c.Scope.Route), ":reply") + ":event"
	if json.Unmarshal(configRaw, &config) != nil || config.Scope != c.Scope || config.Role != "event" || config.SourceID != target.SourceID || stream != expected || state == "quarantined" || !bytes.Equal(h[:], digest) {
		return ErrStreamIdentity
	}
	event, err := DecodeTradeEvent(raw, EventContext{Scope: TradeScope{Route: c.Scope.Route, NativeBrokerID: c.Scope.NativeBrokerID, InvestorID: c.Scope.InvestorID, TradeDate: target.TradeDate}, ExpectedOrigin: &parent.Original.MessageID})
	if err != nil || event.Order == nil || !o.matches(event, parent.Original.SubmitCommand) || event.Order.CounterSessionID != target.CounterSessionID || (event.Order.NativeIDs.UserID != "" && event.Order.NativeIDs.UserID != c.Scope.UserID) || !oneOf(event.Order.GatewayStatus, "accepted", "working") || event.Order.LeavesQty <= 0 || strings.TrimSpace(event.Order.NativeIDs.OrderSysID) == "" {
		return ErrStreamIdentity
	}
	produced, err := time.Parse(time.RFC3339Nano, event.ProducedAt)
	if err != nil || produced.After(time.Now().Add(2*time.Second)) || time.Since(produced) > time.Minute {
		return ErrStreamIdentity
	}
	return nil
}
