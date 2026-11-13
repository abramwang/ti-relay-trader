package performance

import (
	"math"
	"reflect"
	"testing"

	"ti-relay-trader/internal/ledger"
)

func continuityFixture() ([]ledger.PositionCostState, []string, []CostLedgerResult) {
	row := func(date, symbol, bucket string) ledger.PositionCostState {
		return ledger.PositionCostState{AccountID: "account", TradeDate: date, Symbol: symbol, Exchange: "SH", CostBucket: bucket, Status: "calculated", CorporateActionType: "none"}
	}
	seed := row("2026-10-09", "515100", "CORE")
	seed.CloseQuantity, seed.CloseTotalCost = 100, 200
	core := row("2026-10-12", "515100", "CORE")
	core.OpenQuantity, core.OpenTotalCost, core.CloseQuantity, core.CloseTotalCost, core.BrokerCloseQuantity = 100, 200, 100, 200, 100
	etf := row(core.TradeDate, "510310", "CORE")
	etf.BrokerCloseQuantity = 10
	closed := row(core.TradeDate, "510310", "ETF_T0:cycle")
	closed.BuyQuantity, closed.SellQuantity, closed.BuyAmount = 100, 100, 400
	closed.FeeSource, closed.QualityFlags = "included_in_etf_t0_friction", []string{"etf_t0_redemption_cost_released"}
	residual := row(core.TradeDate, "510310", "ETF_T0_RESIDUAL:remaining")
	residual.BuyQuantity, residual.BuyAmount, residual.CloseQuantity, residual.CloseTotalCost = 10, 40, 10, 40
	stock := row(core.TradeDate, "600026", "CORE")
	stock.BrokerCloseQuantity = 300
	component := row(core.TradeDate, "600026", "ETF_T0_COMPONENT:2026-10-12:redemption")
	component.CloseQuantity, component.CloseTotalCost, component.ClosePrice = 300, 600, 2
	component.OpeningSource, component.QualityFlags = "meridian_source_close_component_mark", []string{"component_marked_basis_not_acquisition_cost"}
	first := CostLedgerResult{AccountID: "account", TradeDate: core.TradeDate, Status: "estimated", OpeningSource: "relay_previous_close_cost", Positions: []ledger.PositionCostState{core, etf, closed, residual, stock, component}}
	core.TradeDate, core.SellQuantity, core.SellAmount, core.SellFee, core.RealizedPnL = "2026-10-13", 50, 150, 1, 49
	core.CloseQuantity, core.CloseTotalCost, core.BrokerCloseQuantity = 50, 100, 50
	etf.TradeDate, etf.BrokerCloseQuantity = core.TradeDate, 0
	stock.TradeDate, stock.BrokerCloseQuantity = core.TradeDate, 0
	residual.TradeDate, residual.OpenQuantity, residual.OpenTotalCost = core.TradeDate, 10, 40
	residual.BuyQuantity, residual.BuyAmount, residual.CloseQuantity, residual.CloseTotalCost = 0, 0, 0, 0
	residual.SellQuantity, residual.SellAmount, residual.SellFee, residual.RealizedPnL = 10, 45, 1, 4
	component.TradeDate, component.OpeningSource = core.TradeDate, "relay_previous_close_cost"
	component.OpenQuantity, component.OpenTotalCost, component.CloseQuantity, component.CloseTotalCost = 300, 600, 0, 0
	component.SellQuantity, component.SellAmount, component.SellFee, component.RealizedPnL = 300, 660, 1, 59
	second := CostLedgerResult{AccountID: "account", TradeDate: core.TradeDate, Status: "estimated", OpeningSource: "relay_previous_close_cost", Positions: []ledger.PositionCostState{core, etf, residual, stock, component}}
	return []ledger.PositionCostState{seed}, []string{first.TradeDate, second.TradeDate}, []CostLedgerResult{first, second}
}

func TestCostContinuitySeparatesBasisMovements(t *testing.T) {
	a, dates, days := continuityFixture()
	r, err := ReviewCostContinuity("account", "2026-10-09", a, dates, days)
	if err != nil || !r.Verified || r.Publishable || !r.ExactCostIdentities || r.Days != 2 || r.Rows != 11 || r.CarriedLots != 4 || len(r.Movements) != 2 {
		t.Fatal(r, err)
	}
	if r.Movements[0].Kind != "redemption_cost_out_not_loss" || r.Movements[0].BasisMicros != 400000000 || r.Movements[1].Kind != "component_marked_basis_in_not_purchase" || r.Movements[1].BasisMicros != 600000000 {
		t.Fatal(r.Movements)
	}
	r2, err := ReviewCostContinuity("account", "2026-10-09", a, dates, days)
	if err != nil || !reflect.DeepEqual(r, r2) {
		t.Fatal("non-deterministic review", err)
	}
	a2, dates2, days2 := continuityFixture()
	if !reflect.DeepEqual(a, a2) || !reflect.DeepEqual(dates, dates2) || !reflect.DeepEqual(days, days2) {
		t.Fatal("mutated inputs")
	}
}

func TestCostContinuityRejectsBrokenEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*[]ledger.PositionCostState, *[]string, *[]CostLedgerResult){
		"missing anchor":     func(a *[]ledger.PositionCostState, _ *[]string, _ *[]CostLedgerResult) { *a = nil },
		"duplicate anchor":   func(a *[]ledger.PositionCostState, _ *[]string, _ *[]CostLedgerResult) { *a = append(*a, (*a)[0]) },
		"wrong anchor":       func(a *[]ledger.PositionCostState, _ *[]string, _ *[]CostLedgerResult) { (*a)[0].CloseTotalCost++ },
		"missing day":        func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) { *d = (*d)[:1] },
		"calendar duplicate": func(_ *[]ledger.PositionCostState, c *[]string, _ *[]CostLedgerResult) { (*c)[1] = (*c)[0] },
		"wrong account": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[1].Positions[0].AccountID = "other"
		},
		"duplicate bucket": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions = append((*d)[0].Positions, (*d)[0].Positions[0])
		},
		"missing carry": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[1].Positions = (*d)[1].Positions[1:]
		},
		"cost carry": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[1].Positions[0].OpenTotalCost++
		},
		"quantity carry": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[1].Positions[0].OpenQuantity++
		},
		"money NaN": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[0].BuyAmount = math.NaN()
		},
		"money overflow": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[0].BuyAmount = 1e14
		},
		"negative quantity": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[0].BuyQuantity = -1
		},
		"corporate action": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[0].CorporateActionType = "split"
		},
		"broken identity": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[1].Positions[0].RealizedPnL += .01
		},
		"aggregate quantity": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[1].BrokerCloseQuantity++
		},
		"unknown bucket": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[2].CostBucket = "unknown"
		},
		"fake redemption proceeds": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[2].SellAmount = 400
		},
		"missing release label": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[2].QualityFlags = nil
		},
		"wrong mark": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[5].ClosePrice++
		},
		"wrong mark date": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[5].CostBucket = "ETF_T0_COMPONENT:2026-10-09:redemption"
		},
		"unmarked basis": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].Positions[5].QualityFlags = nil
		},
		"persisted preview": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) { (*d)[0].Persisted = true },
		"broker reanchor": func(_ *[]ledger.PositionCostState, _ *[]string, d *[]CostLedgerResult) {
			(*d)[0].OpeningSource = "broker_open_snapshot_cost_reanchor"
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, c, d := continuityFixture()
			mutate(&a, &c, &d)
			if _, err := ReviewCostContinuity("account", "2026-10-09", a, c, d); err == nil {
				t.Fatal("unsafe evidence accepted")
			}
		})
	}
}

func TestCostContinuityKeepsRoundingVisible(t *testing.T) {
	a, c, d := continuityFixture()
	d[1].Positions[0].RealizedPnL = 48.999999
	r, err := ReviewCostContinuity("account", "2026-10-09", a, c, d)
	if err != nil || r.ExactCostIdentities || len(r.RoundingDifferences) != 1 || r.RoundingDifferences[0].BasisMicros != 1 || r.Publishable {
		t.Fatal(r, err)
	}
	d[1].Positions[0].RealizedPnL = 48.999998
	if _, err := ReviewCostContinuity("account", "2026-10-09", a, c, d); err == nil {
		t.Fatal("larger difference hidden")
	}
}

func TestCostReviewMicros(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), 1e12, .0000001} {
		if _, err := costReviewMicros(v); err == nil {
			t.Fatal("invalid precision", v)
		}
	}
	for v, want := range map[float64]int64{453391.418438: 453391418438, -.000001: -1, 1e11: 100000000000000000} {
		if got, err := costReviewMicros(v); err != nil || got != want {
			t.Fatal(v, got, err)
		}
	}
}
