package trading

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidSchema = errors.New("invalid trading schema")

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (err ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

func (exchange Exchange) Valid() bool {
	switch exchange {
	case ExchangeSH, ExchangeSZ, ExchangeBJ:
		return true
	default:
		return false
	}
}

func (side TradeSide) Valid() bool {
	switch side {
	case TradeSideBuy, TradeSideSell, TradeSidePurchase, TradeSideRedemption:
		return true
	default:
		return false
	}
}

func (typ BusinessType) Valid() bool {
	switch typ {
	case BusinessTypeStock, BusinessTypeETF:
		return true
	default:
		return false
	}
}

func (typ OffsetType) Valid() bool {
	if typ == "" {
		return true
	}
	switch typ {
	case OffsetTypeOpen, OffsetTypeClose:
		return true
	default:
		return false
	}
}

func (status OrderStatus) Terminal() bool {
	switch status {
	case OrderStatusFilled, OrderStatusCancelled, OrderStatusRejected:
		return true
	default:
		return false
	}
}

func (status GatewayStatus) Terminal() bool {
	switch status {
	case GatewayStatusFilled, GatewayStatusCancelled, GatewayStatusRejected:
		return true
	default:
		return false
	}
}

func NormalizeTradeSide(value string) (TradeSide, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "b", "buy":
		return TradeSideBuy, true
	case "s", "sell":
		return TradeSideSell, true
	case "p", "purchase":
		return TradeSidePurchase, true
	case "r", "redemption", "redeem":
		return TradeSideRedemption, true
	default:
		return TradeSide(""), false
	}
}

func NormalizeBusinessType(value string) (BusinessType, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "s", "stock":
		return BusinessTypeStock, true
	case "e", "etf":
		return BusinessTypeETF, true
	default:
		return BusinessType(""), false
	}
}

func (req SubmitOrderRequest) Validate() error {
	if strings.TrimSpace(req.AccountID) == "" {
		return validationError("account_id", "is required")
	}
	if strings.TrimSpace(req.Symbol) == "" {
		return validationError("symbol", "is required")
	}
	if !req.Exchange.Valid() {
		return validationError("exchange", "must be SH, SZ, or BJ")
	}
	if !req.TradeSide.Valid() {
		return validationError("trade_side", "must be B, S, P, or R")
	}
	if !req.BusinessType.Valid() {
		return validationError("business_type", "must be S or E")
	}
	if !req.OffsetType.Valid() {
		return validationError("offset_type", "must be O, C, or empty")
	}
	if req.Price <= 0 {
		return validationError("price", "must be positive")
	}
	if req.Qty <= 0 {
		return validationError("qty", "must be positive")
	}
	return nil
}

func (req BatchSubmitOrderRequest) Validate() error {
	if strings.TrimSpace(req.AccountID) == "" {
		return validationError("account_id", "is required")
	}
	if len(req.Orders) == 0 {
		return validationError("orders", "must contain at least one order")
	}

	seenGatewayOrderID := make(map[string]struct{}, len(req.Orders))
	for i, order := range req.Orders {
		if strings.TrimSpace(order.AccountID) == "" {
			order.AccountID = req.AccountID
		}
		if order.AccountID != req.AccountID {
			return validationError(fmt.Sprintf("orders[%d].account_id", i), "must match batch account_id")
		}
		if err := order.Validate(); err != nil {
			return fmt.Errorf("orders[%d]: %w", i, err)
		}
		if order.GatewayOrderID == "" {
			continue
		}
		if _, ok := seenGatewayOrderID[order.GatewayOrderID]; ok {
			return validationError(fmt.Sprintf("orders[%d].gateway_order_id", i), "must be unique in the batch")
		}
		seenGatewayOrderID[order.GatewayOrderID] = struct{}{}
	}
	return nil
}

func (req CancelOrderRequest) Validate() error {
	if strings.TrimSpace(req.AccountID) == "" {
		return validationError("account_id", "is required")
	}
	if strings.TrimSpace(req.GatewayOrderID) == "" {
		return validationError("gateway_order_id", "is required")
	}
	return nil
}

func validationError(field, message string) error {
	return fmt.Errorf("%w: %w", ErrInvalidSchema, ValidationError{Field: field, Message: message})
}
