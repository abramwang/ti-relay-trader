package performance

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type corporateActionFactor struct {
	securityID string
	exDate     string
	exFactor   float64
	source     string
	raw        map[string]any
}

func (service *Service) loadCorporateActionFactors(
	ctx context.Context,
	tradeDate string,
	securityIDs []string,
) (map[string]corporateActionFactor, bool, []string) {
	factors := make(map[string]corporateActionFactor)
	if len(securityIDs) == 0 {
		return factors, true, nil
	}
	if service.market == nil {
		return factors, false, []string{"meridian_adjust_factors_unavailable"}
	}

	unique := make(map[string]struct{}, len(securityIDs))
	for _, securityID := range securityIDs {
		securityID = strings.ToUpper(strings.TrimSpace(securityID))
		if securityID != "" {
			unique[securityID] = struct{}{}
		}
	}
	securityIDs = securityIDs[:0]
	for securityID := range unique {
		securityIDs = append(securityIDs, securityID)
	}
	sort.Strings(securityIDs)

	available := true
	normalizedDate, parsedDate, err := parseTradeDate(tradeDate)
	if err != nil {
		return factors, false, []string{"invalid_corporate_action_trade_date"}
	}
	compactDate := parsedDate.Format("20060102")
	for start := 0; start < len(securityIDs); start += contributionMarketBatch {
		end := start + contributionMarketBatch
		if end > len(securityIDs) {
			end = len(securityIDs)
		}
		batch := securityIDs[start:end]
		response, queryErr := service.market.MetadataAdjustFactors(ctx, url.Values{
			"security_ids": {strings.Join(batch, ",")},
			"start_date":   {compactDate},
			"end_date":     {compactDate},
			"limit":        {strconv.Itoa(max(10, len(batch)*2))},
		})
		if queryErr != nil || response.StatusCode >= http.StatusBadRequest || response.Payload["error"] != nil {
			available = false
			continue
		}
		for _, row := range contributionRows(response.Payload) {
			securityID := strings.ToUpper(contributionString(row["security_id"]))
			if _, expected := unique[securityID]; !expected {
				continue
			}
			exDate := corporateActionDate(row["ex_date"])
			if exDate != normalizedDate {
				continue
			}
			exFactor, ok := contributionFloat(row["ex_factor"])
			if !ok || exFactor <= 0 || math.IsNaN(exFactor) || math.IsInf(exFactor, 0) {
				available = false
				continue
			}
			if _, duplicate := factors[securityID]; duplicate {
				available = false
				continue
			}
			factors[securityID] = corporateActionFactor{
				securityID: securityID,
				exDate:     exDate,
				exFactor:   exFactor,
				source:     firstContributionString(row, "source", "source_dataset"),
				raw:        cloneCorporateActionContext(row),
			}
		}
	}
	if !available {
		return factors, false, []string{"meridian_adjust_factors_unavailable"}
	}
	return factors, true, nil
}

func applyCorporateActionOpening(
	state *costWorkingState,
	brokerOpenQuantity int64,
	factor corporateActionFactor,
	hasFactor bool,
	factorsAvailable bool,
) {
	previousCloseQuantity := state.quantity
	state.item.PreviousCloseQuantity = previousCloseQuantity
	state.item.BrokerOpenQuantity = brokerOpenQuantity
	state.item.CorporateActionQuantityDelta = brokerOpenQuantity - previousCloseQuantity

	if hasFactor {
		state.item.CorporateActionFactor = factor.exFactor
		state.item.CorporateActionSource = factor.source
		state.item.CorporateActionContext = factor.raw
	}
	if previousCloseQuantity == 0 && brokerOpenQuantity == 0 {
		state.item.CorporateActionFactor = 0
		state.item.CorporateActionSource = ""
		state.item.CorporateActionContext = nil
		return
	}
	if brokerOpenQuantity == previousCloseQuantity {
		if hasFactor {
			state.item.CorporateActionType = "price_adjustment"
			state.flags = appendUnique(state.flags, "corporate_action_price_adjustment")
		}
		if !factorsAvailable {
			markCorporateActionCheckUnavailable(state)
		}
		return
	}

	if !factorsAvailable {
		markCorporateActionCheckUnavailable(state)
		state.flags = appendUnique(state.flags, "unexplained_open_quantity_change")
		state.item.CorporateActionType = "mismatch"
		state.item.Status = "blocked"
		return
	}
	if !hasFactor {
		state.flags = appendUnique(state.flags, "unexplained_open_quantity_change")
		state.item.CorporateActionType = "mismatch"
		state.item.Status = "blocked"
		return
	}

	expectedQuantity := float64(previousCloseQuantity) * factor.exFactor
	tolerance := math.Max(1, math.Abs(expectedQuantity)*0.000001)
	if math.Abs(float64(brokerOpenQuantity)-expectedQuantity) > tolerance {
		state.flags = appendUnique(state.flags, "corporate_action_mismatch")
		state.item.CorporateActionType = "mismatch"
		state.item.Status = "blocked"
		return
	}

	state.quantity = brokerOpenQuantity
	state.item.OpenQuantity = brokerOpenQuantity
	state.item.CorporateActionType = "quantity_adjustment"
	state.flags = appendUnique(state.flags, "corporate_action_quantity_adjusted")
}

func markCorporateActionCheckUnavailable(state *costWorkingState) {
	state.flags = appendUnique(state.flags, "corporate_action_check_unavailable")
	if state.item.Status == "calculated" {
		state.item.Status = "estimated"
	}
}

func corporateActionDate(value any) string {
	if number, ok := contributionFloat(value); ok && number > 0 {
		compact := fmt.Sprintf("%08d", int64(math.Round(number)))
		if normalized, _, err := parseTradeDate(compact); err == nil {
			return normalized
		}
	}
	if normalized, _, err := parseTradeDate(contributionString(value)); err == nil {
		return normalized
	}
	return ""
}

func cloneCorporateActionContext(row map[string]any) map[string]any {
	result := make(map[string]any, len(row))
	for key, value := range row {
		result[key] = value
	}
	return result
}
