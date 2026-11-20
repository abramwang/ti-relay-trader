package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"time"

	"ti-relay-trader/internal/timeutil"
)

// Operator-owned TEST opt-in. A nil policy retains the old SHFE assessment.
// This is a per-order limit, not proof of account margin or portfolio risk.
type CFFEXExecutionPolicy struct {
	Products    []string `json:"products"`
	MaxOrderQty Count    `json:"max_order_qty"`
}

var cffexIndexSymbol = regexp.MustCompile(`^(IF|IH|IC|IM)[0-9]{2}(0[1-9]|1[0-2])$`)

func (p *CFFEXExecutionPolicy) validate() error {
	if p == nil || p.MaxOrderQty <= 0 || len(p.Products) == 0 || len(p.Products) > 4 {
		return ErrOrderExecutionOff
	}
	seen := map[string]bool{}
	for _, product := range p.Products {
		if !oneOf(product, "IF", "IH", "IC", "IM") || seen[product] {
			return ErrOrderExecutionOff
		}
		seen[product] = true
	}
	return nil
}

func (p *CFFEXExecutionPolicy) allows(symbol string, qty Count) bool {
	return p.validate() == nil && cffexIndexSymbol.MatchString(symbol) && oneOf(symbol[:2], p.Products...) && qty > 0 && qty <= p.MaxOrderQty
}

func cloneCFFEXPolicy(p *CFFEXExecutionPolicy) *CFFEXExecutionPolicy {
	if p == nil {
		return nil
	}
	return &CFFEXExecutionPolicy{append([]string(nil), p.Products...), p.MaxOrderQty}
}

// Only continuous day sessions. This is not a holiday calendar or a claim that
// a simulated broker is open; actual TradingDay and fresh capability still apply.
func cffexIndexSessionEnd(now time.Time) (time.Time, bool) {
	t := now.In(timeutil.Location())
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		return time.Time{}, false
	}
	minute, end := t.Hour()*60+t.Minute(), 0
	switch {
	case minute >= 570 && minute < 690:
		end = 690
	case minute >= 780 && minute < 900:
		end = 900
	default:
		return time.Time{}, false
	}
	return time.Date(t.Year(), t.Month(), t.Day(), end/60, end%60, 0, 0, t.Location()), true
}

func serviceSubmitAllowed(a TestOrderAccount, o OrderPreviewRequest, b CatalogBinding) bool {
	if a.Markets != nil {
		return a.marketPolicy(b.ExchangeID).allows(b.ExchangeID, o.OffsetType, o.TradeSide, o.HedgeType, o.Qty)
	}
	if b.ExchangeID == "CFFEX" {
		return a.CFFEX.allows(b.SourceInstrumentID, o.Qty) && oneOf(o.OffsetType, "O", "C") && oneOf(o.TradeSide, "B", "S") && o.HedgeType == "speculation"
	}
	return b.ExchangeID == "SHFE" && o.TradeSide == "B" && o.OffsetType == "O" && o.Qty == 1
}

func (s *TestOrderService) registerOrder(ctx context.Context, scope CommandScope, r OrderExecutionRequest, raw []byte, evidence RegistrationEvidence, target TestQueryTarget, close bool) (CommandRegistration, error) {
	var reserve func(*sql.Tx, CommandRegistration) error
	if close {
		reserve = func(tx *sql.Tx, c CommandRegistration) error {
			if c.Outcome == "replayed" {
				_, err := readCloseReservation(ctx, tx, c)
				return err
			}
			if c.Outcome != "registered" {
				return nil
			}
			// Select the latest attempted query, not the last successful one.
			// Unknown/failed/incomplete newer queries must not expose stale lots.
			scopeRaw, _ := json.Marshal(scope)
			targetRaw, _ := json.Marshal(target)
			var positionID int64
			err := tx.QueryRowContext(ctx, `SELECT p.command_id FROM futures_test_query_publications p JOIN futures_commands c USING(command_id)
				WHERE c.scope_json=$1::jsonb AND p.target_json=$2::jsonb AND p.state<>'not_published'
				AND convert_from(c.body,'UTF8')::jsonb->>'action'='account.positions.query'
				ORDER BY p.attempted_at DESC,p.command_id DESC LIMIT 1`, scopeRaw, targetRaw).Scan(&positionID)
			if err != nil {
				return ErrCloseReservation
			}
			_, err = reserveCombinedClose(ctx, tx, c, target, positionID)
			return err
		}
	}
	return s.store.registerAtomic(ctx, scope, "cmd.trade", raw, evidence, func(tx *sql.Tx) (*CommandRegistration, error) {
		return serviceOrderRegistration(ctx, tx, scope, r.IdempotencyKey, &r.Order)
	}, reserve)
}
