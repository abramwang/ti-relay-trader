package trading

import (
	"errors"
	"strings"
	"testing"
)

func TestSubmitOrderValidate(t *testing.T) {
	req := SubmitOrderRequest{
		AccountID:    "00030484",
		Symbol:       "600000",
		Exchange:     ExchangeSH,
		TradeSide:    TradeSideBuy,
		BusinessType: BusinessTypeStock,
		OffsetType:   OffsetTypeClose,
		Price:        9.54,
		Qty:          100,
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestSubmitOrderRejectsBadQty(t *testing.T) {
	req := SubmitOrderRequest{
		AccountID:    "00030484",
		Symbol:       "600000",
		Exchange:     ExchangeSH,
		TradeSide:    TradeSideBuy,
		BusinessType: BusinessTypeStock,
		Price:        9.54,
	}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("error does not wrap ErrInvalidSchema: %v", err)
	}
	if !strings.Contains(err.Error(), "qty") {
		t.Fatalf("error does not mention qty: %v", err)
	}
}

func TestBatchSubmitValidatesAccountConsistency(t *testing.T) {
	req := BatchSubmitOrderRequest{
		AccountID: "acct-1",
		Orders: []SubmitOrderRequest{
			{
				AccountID:    "acct-2",
				Symbol:       "600000",
				Exchange:     ExchangeSH,
				TradeSide:    TradeSideBuy,
				BusinessType: BusinessTypeStock,
				Price:        9.54,
				Qty:          100,
			},
		},
	}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "must match batch account_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBatchSubmitRejectsDuplicateGatewayOrderID(t *testing.T) {
	order := SubmitOrderRequest{
		AccountID:      "acct-1",
		GatewayOrderID: "gw-1",
		Symbol:         "600000",
		Exchange:       ExchangeSH,
		TradeSide:      TradeSideBuy,
		BusinessType:   BusinessTypeStock,
		Price:          9.54,
		Qty:            100,
	}
	req := BatchSubmitOrderRequest{
		AccountID: "acct-1",
		Orders:    []SubmitOrderRequest{order, order},
	}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "gateway_order_id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCancelOrderValidate(t *testing.T) {
	req := CancelOrderRequest{
		AccountID:      "acct-1",
		GatewayOrderID: "gw-1",
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestNormalizeTradeSide(t *testing.T) {
	side, ok := NormalizeTradeSide("buy")
	if !ok || side != TradeSideBuy {
		t.Fatalf("side = %q, ok=%v", side, ok)
	}
}

func TestOrderStatusTerminal(t *testing.T) {
	if !OrderStatusFilled.Terminal() {
		t.Fatal("filled should be terminal")
	}
	if OrderStatusWorking.Terminal() {
		t.Fatal("working should not be terminal")
	}
}
