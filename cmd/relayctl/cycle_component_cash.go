package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"ti-relay-trader/internal/performance"
)

const cycleComponentCashPath = "outputs/recovery/fuying13_query_evidence_20261114/component-cash-final.json"
const cycleComponentCashSHA = "4eb2c1073c6662199e4cdb007261f4ca9720618da8b935e8f616d920672ab1ee"

type componentCashRow struct {
	fuyingMatrixCash
	PostedAt string `json:"cash_posted_at"`
}

type componentCashProof struct {
	Schema      string `json:"schema"`
	Account     string `json:"account_id"`
	Writes      int    `json:"database_writes"`
	Commands    int    `json:"broker_commands"`
	Publishable bool   `json:"economic_nav_publishable"`
	Summary     struct {
		Days   int    `json:"days"`
		Legs   int    `json:"cash_legs"`
		Amount string `json:"amount"`
		Breaks int    `json:"inventory_breaks"`
	} `json:"summary"`
	Days []struct {
		Date      string `json:"trade_date"`
		Inventory string `json:"inventory_state"`
		Legs      []struct {
			Date       string `json:"trade_date"`
			Parent     string `json:"gateway_order_id"`
			Fill       string `json:"fill_id"`
			Amount     string `json:"amount"`
			State      string `json:"state"`
			Physical   int64  `json:"physical_quantity"`
			Additional string `json:"additional_broker_eod_receivable"`
			Intraday   bool   `json:"intraday_cash_inclusion_verified"`
			Final      bool   `json:"final_settlement_verified"`
			Transfer   struct {
				Account  string      `json:"account_id"`
				Date     string      `json:"trade_date"`
				Parent   string      `json:"gateway_order_id"`
				Fill     string      `json:"fill_id"`
				OrderID  int64       `json:"order_id"`
				StreamID string      `json:"order_stream_id"`
				Symbol   string      `json:"component_symbol"`
				Exchange string      `json:"component_exchange"`
				Quantity int64       `json:"component_qty"`
				Price    json.Number `json:"price"`
			} `json:"original_transfer"`
			Order struct {
				Account  string `json:"account_id"`
				Date     string `json:"trade_date"`
				Parent   string `json:"gateway_order_id"`
				OrderID  int64  `json:"order_id"`
				StreamID string `json:"order_stream_id"`
				Symbol   string `json:"symbol"`
				Exchange string `json:"exchange"`
				Side     string `json:"trade_side"`
				Business string `json:"business_type"`
				Terminal bool   `json:"is_terminal"`
			} `json:"parent_order"`
			Cash componentCashRow `json:"cash_row"`
		} `json:"cash_legs"`
	} `json:"days"`
}

func loadCycleComponentCash(reviews []performance.CycleComponentReview, cash performance.ETFCashCycleReview) error {
	var proof componentCashProof
	if err := readPinnedJSON(cycleComponentCashPath, cycleComponentCashSHA, &proof); err != nil {
		return err
	}
	var matrix struct {
		Rows []componentCashRow `json:"resolved_cash"`
	}
	if err := readPinnedJSON(fuyingCashMatrixPath, fuyingCashMatrixHash, &matrix); err != nil {
		return err
	}
	return attachCycleComponentCash(reviews, cash, proof, matrix.Rows)
}

// Only the sealed per-parent evidence is used; the security code is not a rule.
// Match against the cash already consumed by the cycle before attaching metadata.
func attachCycleComponentCash(reviews []performance.CycleComponentReview, cash performance.ETFCashCycleReview, proof componentCashProof, matrix []componentCashRow) error {
	bad := func() error { return errors.New("component cash explanation evidence mismatch") }
	if proof.Schema != "fuying13_component_cash_audit.v1" || proof.Account != cash.AccountID || proof.Writes != 0 || proof.Commands != 0 || proof.Publishable || proof.Summary.Breaks != 0 || proof.Summary.Days != len(proof.Days) || len(proof.Days) == 0 || len(proof.Days) > 1000 {
		return bad()
	}
	flows := map[string]componentCashRow{}
	for _, r := range matrix {
		if r.FlowID == "" || flows[r.FlowID].FlowID != "" {
			return bad()
		}
		flows[r.FlowID] = r
	}
	flowCycles := map[string]string{}
	for _, cycle := range cash.Cycles {
		for _, id := range cycle.FlowIDs {
			if flowCycles[id] != "" {
				return bad()
			}
			flowCycles[id] = cycle.CycleID
		}
	}
	seenDays, seenFlows := map[string]bool{}, map[string]bool{}
	total, count := int64(0), 0
	type attachment struct {
		gap  *performance.CycleComponentGap
		cash performance.CycleComponentCash
	}
	pending := []attachment{}
	for _, day := range proof.Days {
		if seenDays[day.Date] || day.Inventory != "aggregate_quantity_matched" || len(day.Legs) == 0 {
			return bad()
		}
		seenDays[day.Date] = true
		for _, leg := range day.Legs {
			x, o, r := leg.Transfer, leg.Order, leg.Cash
			amount, e1 := parseBrokerMoneyCents(leg.Amount)
			value, e2 := parseBrokerMoneyCents(x.Price.String())
			cashAmount, e3 := parseBrokerMoneyCents(r.Amount)
			posted, e4 := time.Parse(time.RFC3339, r.PostedAt)
			_, offset := posted.Zone()
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || offset != 8*3600 || amount <= 0 || amount > 100000000000000 || amount != value || amount != cashAmount || leg.Additional != "0.00" || leg.Physical != 0 || leg.Final || leg.Intraday || leg.State != "specific_historical_cash_leg_proven" {
				return bad()
			}
			if leg.Date != day.Date || x.Date != day.Date || o.Date != day.Date || posted.Format("2006-01-02") != day.Date || x.Account != proof.Account || o.Account != proof.Account || r.AccountID != proof.Account || leg.Parent == "" || x.Parent != leg.Parent || o.Parent != leg.Parent || leg.Fill == "" || x.Fill != leg.Fill || o.StreamID == "" || o.StreamID != x.StreamID || o.OrderID != x.OrderID || o.Side != "R" || o.Business != "E" || !o.Terminal || x.Quantity != 1 {
				return bad()
			}
			cycle := day.Date + "/" + o.Symbol + "." + o.Exchange
			m, ok := flows[r.FlowID]
			if !ok || seenFlows[r.FlowID] || flowCycles[r.FlowID] != cycle || m.AccountID != proof.Account || compactToISODate(m.SourceDate) != day.Date || m.SecurityID != o.Symbol+"."+o.Exchange || m.Amount != r.Amount || m.PostedAt != r.PostedAt || !reflect.DeepEqual(m.Raw, r.Raw) {
				return bad()
			}
			seenFlows[r.FlowID] = true
			matches := 0
			for i := range reviews {
				if reviews[i].CycleID != cycle {
					continue
				}
				for j := range reviews[i].Gaps {
					g := &reviews[i].Gaps[j]
					if g.ParentID != leg.Parent || g.SecurityID != x.Symbol+"."+x.Exchange {
						continue
					}
					if g.Cash != nil || g.LaterSaleDate != "" || g.OpenQty != 0 || g.CloseQty != 0 || g.LinkedQty != 0 || g.TransferQty != x.Quantity || g.RemainingQty != x.Quantity {
						return bad()
					}
					pending = append(pending, attachment{g, performance.CycleComponentCash{TransferFillID: leg.Fill, FlowID: r.FlowID, AmountCents: amount, PostedAt: r.PostedAt, EvidenceSHA256: cycleComponentCashSHA, AlreadyIncluded: true}})
					matches++
				}
			}
			if matches != 1 {
				return bad()
			}
			total += amount
			count++
		}
	}
	want, err := parseBrokerMoneyCents(proof.Summary.Amount)
	if err != nil || total != want || count != proof.Summary.Legs || strings.TrimSpace(proof.Account) == "" {
		return bad()
	}
	used := map[*performance.CycleComponentGap]bool{}
	for _, p := range pending {
		if used[p.gap] {
			return bad()
		}
		used[p.gap] = true
	}
	for _, p := range pending {
		x := p.cash
		p.gap.Cash = &x
	}
	return nil
}
