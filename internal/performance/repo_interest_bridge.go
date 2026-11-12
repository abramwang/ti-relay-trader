package performance

import (
	"errors"
	"fmt"
	"time"
)

// Interest uses micro-CNY to retain the existing six-decimal model precision.
// Principal and actual gross receipts use cents. Fees are already in cash and
// are deliberately excluded from this bridge.
type RepoInterestLot struct {
	AccountID           string `json:"account_id"`
	TradeDate           string `json:"trade_date"`
	GatewayOrderID      string `json:"gateway_order_id"`
	SecurityID          string `json:"security_id"`
	PrincipalCents      int64  `json:"principal_cents"`
	GrossInterestMicros int64  `json:"gross_interest_micros"`
	EvidenceRef         string `json:"evidence_ref"`
}

type RepoInterestReceipt struct {
	AccountID           string `json:"account_id"`
	SourceTradeDate     string `json:"source_trade_date"`
	GatewayOrderID      string `json:"gateway_order_id"`
	PostedDate          string `json:"posted_date"`
	ReceiptID           string `json:"receipt_id"`
	PrincipalCents      int64  `json:"principal_cents"`
	ReceivedAmountCents int64  `json:"received_amount_cents"`
	EvidenceRef         string `json:"evidence_ref"`
}

type RepoInterestBridgeDay struct {
	TradeDate                string `json:"trade_date"`
	OpenReceivableMicros     int64  `json:"open_receivable_micros"`
	AccruedInterestMicros    int64  `json:"accrued_interest_micros"`
	ReceivedInterestMicros   int64  `json:"received_interest_micros"`
	ReleasedReceivableMicros int64  `json:"released_receivable_micros"`
	SettlementVarianceMicros int64  `json:"settlement_variance_micros"`
	CloseReceivableMicros    int64  `json:"close_receivable_micros"`
	AssetAdjustmentMicros    int64  `json:"cash_to_accrual_adjustment_micros"`
	RecognizedInterestMicros int64  `json:"recognized_interest_micros"`
}

type RepoInterestBridge struct {
	AccountID              string                  `json:"account_id"`
	OpeningDate            string                  `json:"opening_date"`
	Days                   []RepoInterestBridgeDay `json:"days"`
	PendingLots            []RepoInterestLot       `json:"pending_lots"`
	EconomicNAVPublishable bool                    `json:"economic_nav_publishable"`
	QualityFlags           []string                `json:"quality_flags"`
}

// PreviewRepoInterestBridge is a pure audit calculation. It does not infer cash
// arrival from a calendar/maturity date or modify the native NAV calculation.
func PreviewRepoInterestBridge(accountID, openingDate string, dates []string, lots []RepoInterestLot, receipts []RepoInterestReceipt) (RepoInterestBridge, error) {
	result := RepoInterestBridge{AccountID: accountID, OpeningDate: openingDate,
		QualityFlags: []string{"repo_interest_bridge_preview_only", "interest_asset_inclusion_unverified", "receipt_coverage_unverified"}}
	fail := func(message string) (RepoInterestBridge, error) { return RepoInterestBridge{}, errors.New(message) }
	validDate := func(date string) bool {
		parsed, err := time.Parse("2006-01-02", date)
		return err == nil && parsed.Format("2006-01-02") == date
	}
	if accountID == "" || !validDate(openingDate) || len(dates) == 0 || len(dates) > 1000 || len(lots) > 10000 || len(receipts) > 10000 {
		return fail("invalid repo bridge scope")
	}
	days := map[string]bool{}
	previous := openingDate
	for _, date := range dates {
		if !validDate(date) || date <= previous {
			return fail("invalid repo bridge dates")
		}
		days[date], previous = true, date
	}
	end := dates[len(dates)-1]
	type key struct{ date, order string }
	index := map[key]RepoInterestLot{}
	// Bounds keep all sums below int64 even at maximum input cardinality.
	const maxInterest = int64(100000000000000)
	const maxPrincipal = int64(100000000000000)
	for _, lot := range lots {
		k := key{lot.TradeDate, lot.GatewayOrderID}
		_, duplicate := index[k]
		if lot.AccountID != accountID || lot.GatewayOrderID == "" || lot.SecurityID != "204001.SH" || lot.EvidenceRef == "" ||
			!validDate(lot.TradeDate) || lot.TradeDate > end || (lot.TradeDate > openingDate && !days[lot.TradeDate]) ||
			lot.PrincipalCents <= 0 || lot.PrincipalCents > maxPrincipal || lot.GrossInterestMicros < 0 || lot.GrossInterestMicros > maxInterest || duplicate {
			return fail("invalid or duplicate repo accrual")
		}
		index[k] = lot
	}
	paid := map[key]RepoInterestReceipt{}
	ids := map[string]bool{}
	for _, receipt := range receipts {
		k := key{receipt.SourceTradeDate, receipt.GatewayOrderID}
		lot, ok := index[k]
		_, duplicate := paid[k]
		if !ok || receipt.AccountID != accountID || receipt.ReceiptID == "" || ids[receipt.ReceiptID] || receipt.EvidenceRef == "" || duplicate ||
			!validDate(receipt.PostedDate) || receipt.PostedDate <= lot.TradeDate || receipt.PostedDate > end ||
			(receipt.PostedDate > openingDate && !days[receipt.PostedDate]) || receipt.PrincipalCents != lot.PrincipalCents ||
			receipt.ReceivedAmountCents < receipt.PrincipalCents || receipt.ReceivedAmountCents > receipt.PrincipalCents+maxInterest/10000 {
			return fail("invalid, future, partial or duplicate repo receipt")
		}
		paid[k], ids[receipt.ReceiptID] = receipt, true
	}
	var balance int64
	for k, lot := range index {
		r, exists := paid[k]
		if lot.TradeDate <= openingDate && (!exists || r.PostedDate > openingDate) {
			balance += lot.GrossInterestMicros
		}
	}
	for _, date := range dates {
		day := RepoInterestBridgeDay{TradeDate: date, OpenReceivableMicros: balance}
		for k, lot := range index {
			if lot.TradeDate == date {
				day.AccruedInterestMicros += lot.GrossInterestMicros
			}
			if receipt, ok := paid[k]; ok && receipt.PostedDate == date {
				actual := (receipt.ReceivedAmountCents - receipt.PrincipalCents) * 10000
				day.ReceivedInterestMicros += actual
				day.ReleasedReceivableMicros += lot.GrossInterestMicros
				day.SettlementVarianceMicros += actual - lot.GrossInterestMicros
			}
		}
		balance += day.AccruedInterestMicros - day.ReleasedReceivableMicros
		day.CloseReceivableMicros = balance
		day.AssetAdjustmentMicros = balance - day.OpenReceivableMicros
		day.RecognizedInterestMicros = day.AccruedInterestMicros + day.SettlementVarianceMicros
		if balance < 0 || day.ReceivedInterestMicros+day.AssetAdjustmentMicros != day.RecognizedInterestMicros {
			return fail(fmt.Sprintf("repo bridge conservation failed %s", date))
		}
		result.Days = append(result.Days, day)
	}
	for _, lot := range lots {
		if _, settled := paid[key{lot.TradeDate, lot.GatewayOrderID}]; !settled {
			result.PendingLots = append(result.PendingLots, lot)
		}
	}
	if len(result.PendingLots) > 0 {
		result.QualityFlags = append(result.QualityFlags, "repo_receipt_not_observed")
	}
	return result, nil
}
