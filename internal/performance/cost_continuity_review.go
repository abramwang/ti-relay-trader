package performance

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"ti-relay-trader/internal/ledger"
)

type CostBasisMovement struct {
	TradeDate   string `json:"trade_date"`
	SecurityID  string `json:"security_id"`
	CostBucket  string `json:"cost_bucket"`
	Kind        string `json:"kind"`
	Quantity    int64  `json:"quantity"`
	BasisMicros int64  `json:"basis_micros"`
}

type CostContinuityReview struct {
	AccountID           string              `json:"account_id"`
	AnchorDate          string              `json:"anchor_date"`
	Days                int                 `json:"days"`
	Rows                int                 `json:"rows"`
	CarriedLots         int                 `json:"carried_lots"`
	Movements           []CostBasisMovement `json:"basis_movements"`
	RoundingDifferences []CostBasisMovement `json:"rounding_differences"`
	ExactCostIdentities bool                `json:"exact_cost_identities"`
	Verified            bool                `json:"structural_continuity_verified"`
	Publishable         bool                `json:"economic_nav_publishable"`
}

// Convert existing six-decimal ledger values without introducing a cent-level
// tolerance. Decimal precision/range failures require review, not rounding.
func costReviewMicros(v float64) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e11 {
		return 0, fmt.Errorf("invalid cost review amount")
	}
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	if !ok {
		return 0, fmt.Errorf("invalid decimal cost")
	}
	r.Mul(r, big.NewRat(1000000, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("cost amount exceeds six-decimal ledger precision")
	}
	return r.Num().Int64(), nil
}

// ReviewCostContinuity checks a caller-supplied complete trading-day sequence.
// It does not infer holidays, approve economic scope, or authorize persistence.
// Corporate actions need a separate evidence-aware review and fail closed here.
func ReviewCostContinuity(account, anchorDate string, opening []ledger.PositionCostState, dates []string, days []CostLedgerResult) (CostContinuityReview, error) {
	r := CostContinuityReview{AccountID: account, AnchorDate: anchorDate, ExactCostIdentities: true}
	fail := func(detail string) (CostContinuityReview, error) {
		return CostContinuityReview{}, fmt.Errorf("cost continuity: %s", detail)
	}
	validDate := func(s string) bool { d, _, e := parseTradeDate(s); return e == nil && d == s }
	if strings.TrimSpace(account) != account || account == "" || !validDate(anchorDate) || len(opening) == 0 || len(opening) > 10000 || len(days) == 0 || len(days) > 1000 || len(days) != len(dates) {
		return fail("missing or oversized scope/anchor/calendar")
	}
	key := func(p ledger.PositionCostState) string { return p.Symbol + "." + p.Exchange + "|" + p.CostBucket }
	previous := map[string]ledger.PositionCostState{}
	for _, p := range opening {
		_, duplicate := previous[key(p)]
		m, e := costReviewMicros(p.CloseTotalCost)
		if duplicate || p.AccountID != account || p.TradeDate != anchorDate || p.Symbol == "" || p.CostBucket == "" ||
			(p.Status != "calculated" && p.Status != "estimated") || p.QuantityResidual != 0 || e != nil || m < 0 ||
			p.CloseQuantity < 0 || p.CloseQuantity > 1e12 || (p.CloseQuantity == 0 && m != 0) {
			return fail("invalid opening anchor row")
		}
		previous[key(p)] = p
	}
	lastDate := anchorDate
	for i, day := range days {
		if !validDate(dates[i]) || dates[i] <= lastDate || day.AccountID != account || day.TradeDate != dates[i] ||
			(day.Status != "calculated" && day.Status != "estimated") || day.Persisted || day.OpeningSource != "relay_previous_close_cost" ||
			len(day.Positions) == 0 || len(day.Positions) > 10000 || day.Summary.BlockedItems != 0 || day.Summary.QuantityBreaks != 0 || day.Summary.T0BlockedBuckets != 0 {
			return fail("day scope, status or explicit sequence mismatch")
		}
		current := map[string]ledger.PositionCostState{}
		quantityBySecurity := map[string]int64{}
		coreQuantity := map[string]int64{}
		for _, p := range day.Positions {
			k, security := key(p), p.Symbol+"."+p.Exchange
			if _, duplicate := current[k]; duplicate {
				return fail("duplicate bucket " + k)
			}
			if p.AccountID != account || p.TradeDate != day.TradeDate || len(p.Symbol) != 6 || (p.Exchange != "SH" && p.Exchange != "SZ") ||
				(p.Status != "calculated" && p.Status != "estimated") || p.QuantityResidual != 0 ||
				(p.CorporateActionType != "" && p.CorporateActionType != "none") || p.CorporateActionQuantityDelta != 0 {
				return fail("unsupported row scope/status/action " + k)
			}
			for _, c := range p.Symbol {
				if c < '0' || c > '9' {
					return fail("invalid security symbol")
				}
			}
			for _, q := range []int64{p.OpenQuantity, p.BuyQuantity, p.SellQuantity, p.CloseQuantity, p.BrokerCloseQuantity} {
				if q < 0 || q > 1e12 {
					return fail("quantity range " + k)
				}
			}
			values := []float64{p.OpenTotalCost, p.BuyAmount, p.BuyFee, p.SellAmount, p.SellFee, p.RealizedPnL, p.CloseTotalCost}
			m := make([]int64, len(values))
			for j, value := range values {
				var err error
				m[j], err = costReviewMicros(value)
				if err != nil || (j != 5 && m[j] < 0) {
					return fail("invalid money " + k)
				}
			}
			old, exists := previous[k]
			oldCost, err := costReviewMicros(old.CloseTotalCost)
			if err != nil || p.OpenQuantity != old.CloseQuantity || m[0] != oldCost {
				return fail("opening differs from preceding close " + day.TradeDate + "/" + k)
			}
			if exists && old.CloseQuantity > 0 {
				r.CarriedLots++
			}
			var incoming, outgoing, incomingQty int64
			switch {
			case p.CostBucket == "CORE":
				coreQuantity[security] = p.BrokerCloseQuantity
			case strings.HasPrefix(p.CostBucket, "ETF_T0:"):
				if p.CostBucket == "ETF_T0:" || p.OpenQuantity != 0 || m[0] != 0 || p.BuyQuantity == 0 || p.BuyQuantity != p.SellQuantity || p.CloseQuantity != 0 || m[1] <= 0 ||
					m[2] != 0 || m[3] != 0 || m[4] != 0 || m[5] != 0 || m[6] != 0 ||
					p.FeeSource != "included_in_etf_t0_friction" || !containsStringValue(p.QualityFlags, "etf_t0_redemption_cost_released") {
					return fail("unsupported redemption cost release " + k)
				}
				outgoing = m[1]
				r.Movements = append(r.Movements, CostBasisMovement{day.TradeDate, security, p.CostBucket, "redemption_cost_out_not_loss", p.SellQuantity, outgoing})
			case isT0ResidualBucket(p.CostBucket):
				if len(p.CostBucket) == len(t0ResidualPrefix) {
					return fail("empty residual group")
				}
			case isComponentResidualBucket(p.CostBucket):
				if !containsStringValue(p.QualityFlags, "component_marked_basis_not_acquisition_cost") {
					return fail("unmarked component basis")
				}
				if p.OpeningSource == "meridian_source_close_component_mark" {
					parts := strings.SplitN(p.CostBucket, ":", 3)
					price, e := costReviewMicros(p.ClosePrice)
					if len(parts) != 3 || parts[1] != day.TradeDate || parts[2] == "" || p.OpenQuantity != 0 || p.BuyQuantity != 0 || p.SellQuantity != 0 || p.CloseQuantity <= 0 || e != nil || price <= 0 || price > 100000000000000000/p.CloseQuantity ||
						m[0] != 0 || m[1] != 0 || m[2] != 0 || m[3] != 0 || m[4] != 0 || m[5] != 0 || m[6] != price*p.CloseQuantity {
						return fail("component mark evidence mismatch " + k)
					}
					incoming, incomingQty = m[6], p.CloseQuantity
					r.Movements = append(r.Movements, CostBasisMovement{day.TradeDate, security, p.CostBucket, "component_marked_basis_in_not_purchase", incomingQty, incoming})
				} else if !exists || p.OpeningSource != "relay_previous_close_cost" {
					return fail("component carry without source")
				}
			default:
				return fail("unknown bucket " + k)
			}
			delta := m[6] - (m[0] + m[1] + m[2] - m[3] + m[4] + m[5] + incoming - outgoing)
			if p.CloseQuantity != p.OpenQuantity+p.BuyQuantity-p.SellQuantity+incomingQty ||
				delta < -1 || delta > 1 || (p.CloseQuantity == 0 && m[6] != 0) {
				return fail("daily quantity/cost identity " + day.TradeDate + "/" + k)
			}
			// Close cost and realized PnL are rounded separately by the existing
			// engine. Keep a single-micro difference visible, never alter either.
			if delta != 0 {
				r.ExactCostIdentities = false
				r.RoundingDifferences = append(r.RoundingDifferences, CostBasisMovement{day.TradeDate, security, p.CostBucket, "stored_six_decimal_identity_difference", 0, delta})
			}
			quantityBySecurity[security] += p.CloseQuantity
			current[k] = p
			r.Rows++
		}
		for security, qty := range quantityBySecurity {
			broker, exists := coreQuantity[security]
			if !exists || broker != qty {
				return fail("aggregate broker quantity " + security)
			}
		}
		for k, old := range previous {
			if _, exists := current[k]; !exists && (old.CloseQuantity != 0 || old.CloseTotalCost != 0) {
				return fail("nonempty bucket disappeared " + k)
			}
		}
		previous, lastDate = current, day.TradeDate
		r.Days++
	}
	r.Verified = true
	return r, nil
}
