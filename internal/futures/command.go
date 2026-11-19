package futures

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// CommandScope comes from trusted configuration, never from a command payload.
// UserID is the login user, not an alias for InvestorID.
type CommandScope struct {
	Route                              QueryRoute
	NativeBrokerID, InvestorID, UserID string
}

func (s CommandScope) validate() error {
	r := s.Route
	if !oneOf(r.Env, "test", "prod") || !routeBrokerPattern.MatchString(r.BrokerID) ||
		!accountPattern.MatchString(r.AccountID) || !validID(r.GatewayID) || len(r.GatewayID) > 128 ||
		!native(s.NativeBrokerID, 10, false) || !native(s.InvestorID, 12, false) || !native(s.UserID, 15, false) {
		return fmt.Errorf("invalid configured command scope")
	}
	return nil
}

type CancelIntent struct {
	GatewayOrderID, TradeDate, CancelID string
}

// ConstraintsTarget uses the broker's native symbol, not a second instrument catalog.
type ConstraintsTarget struct {
	Symbol   string `json:"symbol"`
	Exchange string `json:"exchange"`
}

func (t ConstraintsTarget) valid() bool {
	return native(t.Symbol, 80, false) && oneOf(t.Exchange, "SHFE", "CFFEX")
}

type IntentField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Command is a syntax-checked intention, not an execution/readiness permit.
type Command struct {
	SubmitCommand
	Cancel                                   *CancelIntent
	Constraints                              *ConstraintsTarget
	Fields                                   []IntentField
	Intent, IdempotencyLookup, MessageLookup []byte
}

func framed(prefix string, values ...string) []byte {
	var b strings.Builder
	b.WriteString(prefix)
	for _, v := range values {
		b.WriteString(strconv.Itoa(len(v)))
		b.WriteByte(':')
		b.WriteString(v)
	}
	return []byte(b.String())
}

func commandDigest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func commandRouteBytes(r QueryRoute) []byte {
	return framed("ctp.command.route.v1/", r.Env, r.BrokerID, r.AccountID, r.GatewayID)
}

// DecodeCommand implements the existing actions and the TEST constraints
// candidate using ctp.intent.v1 encoding. Syntax does not enable publication.
// streamSuffix is selected by the trusted caller. TradingDay/target checks are
// deliberately deferred until AFTER durable lookup in CommandStore.Register.
func DecodeCommand(raw []byte, scope CommandScope, streamSuffix string) (Command, error) {
	bad := func() (Command, error) { return Command{}, fmt.Errorf("invalid command syntax or stream") }
	if err := scope.validate(); err != nil {
		return Command{}, err
	}
	if len(raw) > maxSubmitCommandBytes {
		return Command{}, ErrAuditCapacity
	}
	if _, err := parseReply(raw); err != nil {
		return Command{}, err
	}
	obj, err := shape(raw, []string{"protocol", "message_type", "message_id", "request_id", "correlation_id", "idempotency_key", "action", "sent_at", "payload"}, nil)
	if err != nil {
		return Command{}, err
	}
	if textField(obj, "protocol") != "relay.stream.v1" || textField(obj, "message_type") != "command" || !validTime(textField(obj, "sent_at")) {
		return bad()
	}
	for _, k := range []string{"message_id", "request_id", "correlation_id", "idempotency_key"} {
		if !validID(textField(obj, k)) || len(textField(obj, k)) > maxSubmitIDBytes {
			return bad()
		}
	}
	action := textField(obj, "action")
	query := queryResultType(action) != ""
	if (query && streamSuffix != "cmd.query") || (!query && streamSuffix != "cmd.trade") {
		return bad()
	}
	c := Command{SubmitCommand: SubmitCommand{MessageID: textField(obj, "message_id"), RequestID: textField(obj, "request_id"), CorrelationID: textField(obj, "correlation_id"), IdempotencyKey: textField(obj, "idempotency_key"), Action: action, Raw: bytes.Clone(raw)}}
	if oneOf(action, "order.submit", "order.batch.submit") {
		c.SubmitCommand, err = decodeSubmitSyntax(raw, scope.Route.AccountID, "")
		if err != nil {
			return Command{}, err
		}
	} else {
		required := []string{"account_id", "product_type", "schema_version"}
		if action == "instrument.constraints.query" {
			if scope.Route.Env != "test" {
				return bad()
			}
			required = append(required, "symbol", "exchange")
		} else if action == "order.cancel" {
			required = append(required, "gateway_order_id", "trade_date", "cancel_id")
		} else if !query {
			return bad()
		}
		p, err := shape(obj["payload"], required, nil)
		if err != nil {
			return Command{}, err
		}
		if textField(p, "account_id") != scope.Route.AccountID || textField(p, "product_type") != "futures" || textField(p, "schema_version") != "ctp.f0.draft.2" {
			return bad()
		}
		if action == "instrument.constraints.query" {
			c.Constraints = &ConstraintsTarget{textField(p, "symbol"), textField(p, "exchange")}
			if !c.Constraints.valid() {
				return bad()
			}
		}
		if action == "order.cancel" {
			for _, k := range []string{"gateway_order_id", "cancel_id"} {
				if !validID(textField(p, k)) || len(textField(p, k)) > maxSubmitIDBytes {
					return bad()
				}
			}
			if !validDay(textField(p, "trade_date")) {
				return bad()
			}
			c.Cancel = &CancelIntent{textField(p, "gateway_order_id"), textField(p, "trade_date"), textField(p, "cancel_id")}
		}
	}
	add := func(name, value string) { c.Fields = append(c.Fields, IntentField{name, value}) }
	r := scope.Route
	for _, f := range []IntentField{{"env", r.Env}, {"broker_id", r.BrokerID}, {"account_id", r.AccountID}, {"gateway_id", r.GatewayID}, {"native_broker_id", scope.NativeBrokerID}, {"investor_id", scope.InvestorID}, {"user_id", scope.UserID}, {"protocol", "relay.stream.v1"}, {"product_type", "futures"}, {"schema_version", "ctp.f0.draft.2"}, {"action", action}} {
		add(f.Name, f.Value)
	}
	if len(c.Orders) > 0 {
		add("order_count", strconv.Itoa(len(c.Orders)))
		for i, row := range c.Orders {
			price := string(row.LimitPrice)
			if strings.Contains(price, ".") {
				price = strings.TrimRight(strings.TrimRight(price, "0"), ".")
			}
			for _, f := range []IntentField{{"account_id", row.AccountID}, {"gateway_order_id", row.GatewayOrderID}, {"client_order_id", row.ClientOrderID}, {"trade_date", row.TradeDate}, {"symbol", row.Symbol}, {"exchange", row.Exchange}, {"business_type", row.BusinessType}, {"trade_side", row.TradeSide}, {"offset_type", row.OffsetType}, {"hedge_type", row.HedgeType}, {"order_qty", strconv.FormatInt(int64(row.OrderQty), 10)}, {"limit_price", price}} {
				add(fmt.Sprintf("orders[%d].%s", i, f.Name), f.Value)
			}
		}
	} else if c.Cancel != nil {
		add("gateway_order_id", c.Cancel.GatewayOrderID)
		add("trade_date", c.Cancel.TradeDate)
		add("cancel_id", c.Cancel.CancelID)
	} else if c.Constraints != nil {
		add("symbol", c.Constraints.Symbol)
		add("exchange", c.Constraints.Exchange)
	}
	values := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		values[i] = f.Value
	}
	c.Intent = framed("ctp.intent.v1/", values...)
	c.IdempotencyLookup = framed("ctp.idempotency.v1/", r.Env, r.BrokerID, r.AccountID, r.GatewayID, c.IdempotencyKey)
	c.MessageLookup = framed("ctp.message.v1/", r.Env, r.BrokerID, r.AccountID, r.GatewayID, c.MessageID)
	return c, nil
}
