package futures

import "encoding/json"

// OrderPreviewRequest is an intention, not a registered or authorized command.
// It deliberately has no route, raw market data, permission or command IDs.
type OrderPreviewRequest struct {
	AccountID  string `json:"account_id"`
	SecurityID string `json:"security_id"`
	TradeDate  string `json:"trade_date"`
	TradeSide  string `json:"trade_side"`
	OffsetType string `json:"offset_type"`
	HedgeType  string `json:"hedge_type"`
	Qty        Count  `json:"qty"`
	Price      Price  `json:"price"`
}

func DecodeOrderPreview(raw []byte) (OrderPreviewRequest, error) {
	var r OrderPreviewRequest
	if len(raw) > 4096 {
		return r, ErrAuditCapacity
	}
	if _, err := parseReply(raw); err != nil {
		return r, err
	}
	if _, err := shape(raw, []string{"account_id", "security_id", "trade_date", "trade_side", "offset_type", "hedge_type", "qty", "price"}, nil); err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if !accountPattern.MatchString(r.AccountID) || !validDay(r.TradeDate) || !oneOf(r.TradeSide, "B", "S") || !oneOf(r.OffsetType, "O", "C", "CT", "CY") || r.HedgeType != "speculation" || r.Qty <= 0 {
		return r, ErrStreamIdentity
	}
	if _, ok := checkedPositivePrice(r.Price); !ok {
		return r, ErrStreamIdentity
	}
	return r, nil
}

// ValidatePreviewPrice leaves the user's price unchanged. Broker limits are
// not an extra local order gate in the Meridian path.
func ValidatePreviewPrice(r OrderPreviewRequest, tick Price) error {
	return validatePriceTick(r.Price, tick)
}
