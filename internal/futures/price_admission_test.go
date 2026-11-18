package futures

import (
	"errors"
	"testing"
)

func TestLimitOrderPriceAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		price, tick, lower, upper Price
		reason                    string
	}{
		{"valid", "2964", "1", "2870", "3173", ""},
		{"lower inclusive", "5218", "1", "5218", "5653", ""},
		{"upper inclusive", "5653", "1", "5218", "5653", ""},
		{"decimal exact", "0.3", "0.1", "0.1", "1", ""},
		{"nine decimals", "1.000000001", "0.000000001", "1", "2", ""},
		{"no float rounding", "1.000000001", "0.1", "1", "2", "price_off_tick"},
		{"counter differs from live market", "2958", "1", "5218", "5653", "price_outside_limits"},
		{"above upper", "5654", "1", "5218", "5653", "price_outside_limits"},
		{"off tick", "2964.5", "1", "2870", "3173", "price_off_tick"},
		{"zero price", "0", "1", "1", "2", "invalid_limit_price"},
		{"exponent", "1e3", "1", "1", "2000", "invalid_limit_price"},
		{"fraction", "3/2", "0.1", "1", "2", "invalid_limit_price"},
		{"over precision", "1.0000000001", "1", "1", "2", "invalid_limit_price"},
		{"overflow", "18446744073709551616", "1", "1", "2", "invalid_limit_price"},
		{"missing tick", "1", "", "1", "2", "invalid_price_tick"},
		{"zero tick", "1", "0", "1", "2", "invalid_price_tick"},
		{"missing lower", "1", "1", "", "2", "invalid_lower_limit"},
		{"missing upper", "1", "1", "1", "", "invalid_price_range"},
		{"inverted range", "1", "1", "2", "1", "invalid_price_range"},
		{"equal limits unsupported", "1", "1", "1", "1", "invalid_price_range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLimitOrderPrice(tc.price, tc.tick, tc.lower, tc.upper)
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var rejection priceAdmissionError
			if !errors.Is(err, ErrStreamIdentity) || !errors.As(err, &rejection) || string(rejection) != tc.reason {
				t.Fatalf("got %v, want %s", err, tc.reason)
			}
		})
	}
}

func TestPriceAdmissionRejectionDoesNotPublishPostgres(t *testing.T) {
	db, store, ctx := assessmentDB(t)
	v, evidence, target, approval := assessmentFixture(t)
	registered := registerVector(t, store, ctx, v, evidence, "registered")
	transport := &fakeQueryPublisher{}
	approval.Market.LowerLimit = "3000"
	_, err := store.publishTestTrade(ctx, v.scope(), registered.CommandID, registered.Original.MessageID, target, approval, transport)
	var rejection priceAdmissionError
	if !errors.As(err, &rejection) || rejection != "price_outside_limits" {
		t.Fatal("expected local price rejection", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM futures_test_trade_publications WHERE command_id=$1`, registered.CommandID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if transport.appends.Load() != 0 || count != 0 {
		t.Fatal("rejected preparation published or consumed a budget", count)
	}
	// No attempt was made. A later valid observation may be evaluated without
	// changing the command identity or resetting any execution record.
	approval.Market.LowerLimit = "2870"
	if _, err := store.publishTestTrade(ctx, v.scope(), registered.CommandID, registered.Original.MessageID, target, approval, transport); err != nil {
		t.Fatal("local rejection blocked later valid admission", err)
	}
	if transport.appends.Load() != 1 {
		t.Fatal("expected exactly one publication")
	}
}
