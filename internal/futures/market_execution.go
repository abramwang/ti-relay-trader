package futures

import (
	"time"

	"ti-relay-trader/internal/timeutil"
)

// Explicit Relay policy, not OC wire or a declaration of exchange availability.
// No default sessions: a new venue must not inherit another venue's calendar.
type MarketExecutionPolicy struct {
	Exchange    string       `json:"exchange"`
	MaxOrderQty Count        `json:"max_order_qty"`
	CloseModel  string       `json:"close_model"`
	DaySessions []DaySession `json:"day_sessions"`
}

type DaySession struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func dayMinute(v string) (int, bool) {
	t, err := time.Parse("15:04", v)
	return t.Hour()*60 + t.Minute(), err == nil && t.Format("15:04") == v
}

func (p *MarketExecutionPolicy) validate() error {
	if p == nil || !validExchange(p.Exchange) || p.MaxOrderQty <= 0 || !oneOf(p.CloseModel, "none", "combined") || len(p.DaySessions) == 0 || len(p.DaySessions) > 8 {
		return ErrOrderExecutionOff
	}
	previous := 0
	for _, s := range p.DaySessions {
		start, ok := dayMinute(s.Start)
		end, endOK := dayMinute(s.End)
		if !ok || !endOK || start < 8*60 || end > 16*60 || start >= end || start < previous {
			return ErrOrderExecutionOff
		}
		previous = end
	}
	return nil
}

func cloneMarketPolicy(p *MarketExecutionPolicy) *MarketExecutionPolicy {
	if p == nil {
		return nil
	}
	out := *p
	out.DaySessions = append([]DaySession(nil), p.DaySessions...)
	return &out
}

func cloneMarketPolicies(p []MarketExecutionPolicy) []MarketExecutionPolicy {
	if p == nil {
		return nil
	}
	out := make([]MarketExecutionPolicy, len(p))
	for i := range p {
		out[i] = *cloneMarketPolicy(&p[i])
	}
	return out
}

func (a TestOrderAccount) marketPolicy(exchange string) *MarketExecutionPolicy {
	for _, p := range a.Markets {
		if p.Exchange == exchange {
			return cloneMarketPolicy(&p)
		}
	}
	return nil
}

// Preview only. Publication rechecks source, session, permission and risk facts.
func (s *TestOrderService) AllowsOrderIntent(o OrderPreviewRequest, market TestTradeMarket) bool {
	a, ok := s.accounts[o.AccountID]
	return ok && serviceSubmitAllowed(a, o, CatalogBinding{SecurityID: market.SecurityID, SourceInstrumentID: market.SourceInstrumentID, ExchangeID: market.ExchangeID})
}

func (p *MarketExecutionPolicy) allows(exchange, offset, side, hedge string, qty Count) bool {
	return p.validate() == nil && exchange == p.Exchange && qty > 0 && qty <= p.MaxOrderQty &&
		oneOf(side, "B", "S") && hedge == "speculation" && (offset == "O" || offset == "C" && p.CloseModel == "combined")
}

func (p *MarketExecutionPolicy) sessionEnd(now time.Time) (time.Time, bool) {
	if p.validate() != nil {
		return time.Time{}, false
	}
	t := now.In(timeutil.Location())
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		return time.Time{}, false
	}
	minute := t.Hour()*60 + t.Minute()
	for _, s := range p.DaySessions {
		start, _ := dayMinute(s.Start)
		end, _ := dayMinute(s.End)
		if minute >= start && minute < end {
			return time.Date(t.Year(), t.Month(), t.Day(), end/60, end%60, 0, 0, t.Location()), true
		}
	}
	return time.Time{}, false
}
