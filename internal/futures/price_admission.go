package futures

import (
	"encoding/json"
	"math/big"
)

// Local admission reasons are not OC wire error codes. Rejection happens
// before publication and never changes the broker connection or order state.
type priceAdmissionError string

func (e priceAdmissionError) Error() string { return "futures price admission: " + string(e) }
func (e priceAdmissionError) Unwrap() error { return ErrStreamIdentity }

func checkedPositivePrice(p Price) (*big.Rat, bool) {
	raw, _ := json.Marshal(p)
	var checked Price
	if json.Unmarshal(raw, &checked) != nil {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(string(checked))
	return r, ok && r.Sign() > 0
}

// The caller supplies already scoped evidence. This checks economics only;
// it does not establish freshness, provenance, margin sufficiency or authority.
func validateLimitOrderPrice(price, tick, lower, upper Price) error {
	p, ok := checkedPositivePrice(price)
	if !ok {
		return priceAdmissionError("invalid_limit_price")
	}
	t, ok := checkedPositivePrice(tick)
	if !ok {
		return priceAdmissionError("invalid_price_tick")
	}
	l, ok := checkedPositivePrice(lower)
	if !ok {
		return priceAdmissionError("invalid_lower_limit")
	}
	u, ok := checkedPositivePrice(upper)
	if !ok || l.Cmp(u) >= 0 {
		return priceAdmissionError("invalid_price_range")
	}
	if p.Cmp(l) < 0 || p.Cmp(u) > 0 {
		return priceAdmissionError("price_outside_limits")
	}
	if !new(big.Rat).Quo(p, t).IsInt() {
		return priceAdmissionError("price_off_tick")
	}
	return nil
}
