package performance

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
)

// This is reviewed broker EOD evidence, not an OC intraday cash-flow inference.
type sameDayETFCashInclusion struct {
	Schema               string            `json:"schema"`
	AccountID            string            `json:"account_id"`
	TradeDate            string            `json:"trade_date"`
	Scope                string            `json:"scope"`
	SourceSHA256         map[string]string `json:"source_sha256"`
	CashChainVerified    bool              `json:"cash_chain_verified"`
	CashMultisetVerified bool              `json:"cash_multiset_verified"`
	IncludedCashTotal    string            `json:"included_cash_total"`
	Receipts             []struct {
		AccountID       string `json:"account_id"`
		TradeDate       string `json:"trade_date"`
		SourceTradeDate string `json:"source_trade_date"`
		BrokerFlowID    string `json:"broker_flow_id"`
		BrokerOrderID   string `json:"broker_order_id"`
		SecurityID      string `json:"security_id"`
		Amount          string `json:"amount"`
	} `json:"receipts"`
}

func confirmedSameDayETFCash(observation ledger.AssetPositionObservation, carry float64) (float64, error) {
	raw, exists := observation.RawPayload["same_day_etf_cash_inclusion"]
	if !exists {
		return 0, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return 0, err
	}
	var proof sameDayETFCashInclusion
	if err = json.Unmarshal(data, &proof); err != nil {
		return 0, err
	}
	invalid := func() (float64, error) { return 0, fmt.Errorf("invalid same-day ETF cash inclusion evidence") }
	day, err := time.Parse("2006-01-02", proof.TradeDate)
	if err != nil || day.Format("2006-01-02") != proof.TradeDate || proof.TradeDate != observation.TradeDate ||
		proof.AccountID == "" || proof.AccountID != observation.AccountID ||
		proof.Schema != "broker_same_day_etf_receipts_audit.v1" ||
		proof.Scope != "broker_end_of_day_cash_not_OC_intraday_atomic_snapshot" ||
		!proof.CashChainVerified || !proof.CashMultisetVerified || len(proof.Receipts) == 0 || len(proof.Receipts) > 10000 {
		return invalid()
	}
	for _, source := range []string{"funds", "cash_flows", "settlements", "executions"} {
		hash, err := hex.DecodeString(proof.SourceSHA256[source])
		if err != nil || len(hash) != 32 {
			return invalid()
		}
	}
	if proof.SourceSHA256["funds"] != observation.RawPayload["statement_sha256"] {
		return invalid()
	}
	parse := func(value string) (float64, bool) {
		parts := strings.Split(value, ".")
		if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) != 2 {
			return 0, false
		}
		for _, digit := range parts[0] + parts[1] {
			if digit < '0' || digit > '9' {
				return 0, false
			}
		}
		n, err := strconv.ParseFloat(value, 64)
		return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n > 0 && n < 1e12
	}
	seen := map[string]bool{}
	total := 0.0
	for _, row := range proof.Receipts {
		amount, ok := parse(row.Amount)
		if !ok || row.AccountID != proof.AccountID || row.TradeDate != proof.TradeDate || row.SourceTradeDate != proof.TradeDate ||
			strings.TrimSpace(row.BrokerFlowID) == "" || seen[row.BrokerFlowID] || strings.TrimSpace(row.BrokerOrderID) == "" ||
			strings.TrimSpace(row.SecurityID) == "" {
			return invalid()
		}
		seen[row.BrokerFlowID] = true
		total = roundMoney(total + amount)
	}
	claimed, ok := parse(proof.IncludedCashTotal)
	if !ok || math.Abs(total-claimed) > 0.000001 || math.IsNaN(carry) || math.IsInf(carry, 0) || total > carry+0.000001 {
		return invalid()
	}
	return total, nil
}
