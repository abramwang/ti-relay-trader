package performance

import (
	"errors"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/trading"
)

// Quantities describe the existing linker's evidence, not a new allocation or
// security classification. A matching account position is not ownership proof.
type CycleComponentGap struct {
	ParentID       string              `json:"redemption_order_id"`
	SecurityID     string              `json:"security_id"`
	TransferQty    int64               `json:"transfer_quantity"`
	LinkedQty      int64               `json:"linked_sale_quantity"`
	RemainingQty   int64               `json:"unlinked_quantity"`
	OpenQty        int64               `json:"account_open_quantity"`
	CloseQty       int64               `json:"account_close_quantity"`
	LaterSaleDate  string              `json:"later_sale_date,omitempty"`
	LaterSaleQty   int64               `json:"later_sale_quantity,omitempty"`
	LaterSaleCents int64               `json:"later_sale_cents,omitempty"`
	Cash           *CycleComponentCash `json:"cash_explanation,omitempty"`
}

// An explanation of cash already included in the cycle, never an additive leg.
type CycleComponentCash struct {
	TransferFillID  string `json:"transfer_fill_id"`
	FlowID          string `json:"broker_flow_id"`
	AmountCents     int64  `json:"amount_cents"`
	PostedAt        string `json:"posted_at"`
	EvidenceSHA256  string `json:"evidence_sha256"`
	AlreadyIncluded bool   `json:"already_included"`
}

type CycleComponentReview struct {
	CycleID     string              `json:"cycle_id"`
	Groups      int                 `json:"reviewed_groups"`
	InputSHA256 string              `json:"input_sha256"`
	CostSHA256  string              `json:"cost_sha256"`
	Gaps        []CycleComponentGap `json:"same_day_gaps"`
}

func componentQuantityGaps(redemptions []trading.Fill, links componentSaleLinks, open, close []trading.Position) ([]CycleComponentGap, error) {
	positions := func(rows []trading.Position) map[string]int64 {
		m := map[string]int64{}
		for _, p := range rows {
			m[contributionSecurityID(p.Symbol, p.Exchange)] += p.Quantity
		}
		return m
	}
	o, c := positions(open), positions(close)
	seen := map[string]bool{}
	result := []CycleComponentGap{}
	for _, r := range redemptions {
		if seen[r.GatewayOrderID] {
			continue
		}
		seen[r.GatewayOrderID] = true
		g := links.groups[r.GatewayOrderID]
		if g == nil || len(g.expectedBySecurity) == 0 {
			return nil, errors.New("component review missing transfer group")
		}
		for security, expected := range g.expectedBySecurity {
			linked := g.linkedBySecurity[security]
			if expected <= 0 || linked < 0 || linked > expected {
				return nil, errors.New("invalid component quantity evidence")
			}
			if linked != expected {
				result = append(result, CycleComponentGap{ParentID: r.GatewayOrderID, SecurityID: security, TransferQty: expected, LinkedQty: linked, RemainingQty: expected - linked, OpenQty: o[security], CloseQty: c[security]})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ParentID+"/"+result[i].SecurityID < result[j].ParentID+"/"+result[j].SecurityID
	})
	return result, nil
}

func validateCycleComponentReview(c CycleComponentReview, row CycleReviewRow, through string) error {
	bad := func() error { return errors.New("invalid cycle component review") }
	if c.CycleID != row.Profit.CycleID || c.Groups <= 0 || c.Groups > 1000 || len(c.Gaps) > 10000 || !cycleEvidenceHash.MatchString(c.InputSHA256) || !cycleEvidenceHash.MatchString(c.CostSHA256) {
		return bad()
	}
	previous := ""
	var later int64
	for _, g := range c.Gaps {
		key := g.ParentID + "/" + g.SecurityID
		if strings.TrimSpace(g.ParentID) == "" || key <= previous || !cashCycleSecurity.MatchString(g.SecurityID) || g.TransferQty <= 0 || g.TransferQty > 1000000000000 || g.LinkedQty < 0 || g.LinkedQty >= g.TransferQty || g.RemainingQty != g.TransferQty-g.LinkedQty || g.OpenQty < 0 || g.CloseQty < 0 || g.LaterSaleQty < 0 || g.LaterSaleCents < 0 || g.LaterSaleCents > 100000000000000 {
			return bad()
		}
		if g.LaterSaleDate == "" {
			if g.LaterSaleQty != 0 || g.LaterSaleCents != 0 {
				return bad()
			}
		} else {
			date, err := time.Parse("2006-01-02", g.LaterSaleDate)
			if err != nil || date.Format("2006-01-02") != g.LaterSaleDate || g.LaterSaleDate <= row.SourceDate || g.LaterSaleDate > through || g.LaterSaleQty != g.RemainingQty || g.LaterSaleCents <= 0 || g.OpenQty != 0 || g.CloseQty != g.RemainingQty {
				return bad()
			}
		}
		later += g.LaterSaleCents
		if x := g.Cash; x != nil {
			posted, err := time.Parse(time.RFC3339, x.PostedAt)
			_, offset := posted.Zone()
			if err != nil || offset != 8*3600 || posted.Format("2006-01-02") < row.SourceDate || posted.Format("2006-01-02") > through || strings.TrimSpace(x.FlowID) == "" || strings.TrimSpace(x.TransferFillID) == "" || !cycleEvidenceHash.MatchString(x.EvidenceSHA256) || x.AmountCents <= 0 || x.AmountCents > 100000000000000 || !x.AlreadyIncluded || g.LaterSaleDate != "" || g.LinkedQty != 0 || g.OpenQty != 0 || g.CloseQty != 0 {
				return bad()
			}
		}
		previous = key
	}
	if later != row.Profit.Trading.LaterComponentSales || (len(c.Gaps) > 0) != containsStringValue(row.Profit.Trading.QualityFlags, "component_transfer_sell_quantity_mismatch") {
		return bad()
	}
	return nil
}
