package ledger

import (
	"encoding/json"
	"time"

	"ti-relay-trader/internal/timeutil"
)

func optionalBusinessTime(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := timeutil.FormatRFC3339Nano(value)
	return &formatted
}

func (run JobRun) MarshalJSON() ([]byte, error) {
	type jobRunAlias JobRun
	return json.Marshal(struct {
		jobRunAlias
		StartedAt  *string `json:"started_at,omitempty"`
		FinishedAt *string `json:"finished_at,omitempty"`
		CreatedAt  *string `json:"created_at,omitempty"`
		UpdatedAt  *string `json:"updated_at,omitempty"`
	}{
		jobRunAlias: jobRunAlias(run),
		StartedAt:   optionalBusinessTime(run.StartedAt),
		FinishedAt:  optionalBusinessTime(run.FinishedAt),
		CreatedAt:   optionalBusinessTime(run.CreatedAt),
		UpdatedAt:   optionalBusinessTime(run.UpdatedAt),
	})
}

func (run ReconciliationRun) MarshalJSON() ([]byte, error) {
	type reconciliationRunAlias ReconciliationRun
	return json.Marshal(struct {
		reconciliationRunAlias
		StartedAt   *string `json:"started_at,omitempty"`
		CompletedAt *string `json:"completed_at,omitempty"`
	}{
		reconciliationRunAlias: reconciliationRunAlias(run),
		StartedAt:              optionalBusinessTime(run.StartedAt),
		CompletedAt:            optionalBusinessTime(run.CompletedAt),
	})
}

func (item ReconciliationBreak) MarshalJSON() ([]byte, error) {
	type reconciliationBreakAlias ReconciliationBreak
	return json.Marshal(struct {
		reconciliationBreakAlias
		CreatedAt  *string `json:"created_at,omitempty"`
		ResolvedAt *string `json:"resolved_at,omitempty"`
	}{
		reconciliationBreakAlias: reconciliationBreakAlias(item),
		CreatedAt:                optionalBusinessTime(item.CreatedAt),
		ResolvedAt:               optionalBusinessTime(item.ResolvedAt),
	})
}

func (bucket RawStreamSummaryBucket) MarshalJSON() ([]byte, error) {
	type rawStreamSummaryBucketAlias RawStreamSummaryBucket
	return json.Marshal(struct {
		rawStreamSummaryBucketAlias
		LastReceivedAt *string `json:"last_received_at,omitempty"`
	}{
		rawStreamSummaryBucketAlias: rawStreamSummaryBucketAlias(bucket),
		LastReceivedAt:              optionalBusinessTime(bucket.LastReceivedAt),
	})
}

func (performance DailyPerformance) MarshalJSON() ([]byte, error) {
	type dailyPerformanceAlias DailyPerformance
	return json.Marshal(struct {
		dailyPerformanceAlias
		OpenCapturedAt *string `json:"open_captured_at,omitempty"`
		CapturedAt     *string `json:"captured_at,omitempty"`
	}{
		dailyPerformanceAlias: dailyPerformanceAlias(performance),
		OpenCapturedAt:        optionalBusinessTime(performance.OpenCapturedAt),
		CapturedAt:            optionalBusinessTime(performance.CapturedAt),
	})
}

func (rule FeeRule) MarshalJSON() ([]byte, error) {
	type feeRuleAlias FeeRule
	return json.Marshal(struct {
		feeRuleAlias
		ActivatedAt *string `json:"activated_at,omitempty"`
		CreatedAt   *string `json:"created_at,omitempty"`
		UpdatedAt   *string `json:"updated_at,omitempty"`
	}{
		feeRuleAlias: feeRuleAlias(rule),
		ActivatedAt:  optionalBusinessTime(rule.ActivatedAt),
		CreatedAt:    optionalBusinessTime(rule.CreatedAt),
		UpdatedAt:    optionalBusinessTime(rule.UpdatedAt),
	})
}

func (entry CashLedgerEntry) MarshalJSON() ([]byte, error) {
	type cashLedgerEntryAlias CashLedgerEntry
	return json.Marshal(struct {
		cashLedgerEntryAlias
		EffectiveAt *string `json:"effective_at,omitempty"`
		ConfirmedAt *string `json:"confirmed_at,omitempty"`
		VoidedAt    *string `json:"voided_at,omitempty"`
		CreatedAt   *string `json:"created_at,omitempty"`
	}{
		cashLedgerEntryAlias: cashLedgerEntryAlias(entry),
		EffectiveAt:          optionalBusinessTime(entry.EffectiveAt),
		ConfirmedAt:          optionalBusinessTime(entry.ConfirmedAt),
		VoidedAt:             optionalBusinessTime(entry.VoidedAt),
		CreatedAt:            optionalBusinessTime(entry.CreatedAt),
	})
}

func (baseline NavBaseline) MarshalJSON() ([]byte, error) {
	type navBaselineAlias NavBaseline
	return json.Marshal(struct {
		navBaselineAlias
		ConfirmedAt *string `json:"confirmed_at,omitempty"`
		CreatedAt   *string `json:"created_at,omitempty"`
		UpdatedAt   *string `json:"updated_at,omitempty"`
	}{
		navBaselineAlias: navBaselineAlias(baseline),
		ConfirmedAt:      optionalBusinessTime(baseline.ConfirmedAt),
		CreatedAt:        optionalBusinessTime(baseline.CreatedAt),
		UpdatedAt:        optionalBusinessTime(baseline.UpdatedAt),
	})
}

func (nav PerformanceNAV) MarshalJSON() ([]byte, error) {
	type performanceNAVAlias PerformanceNAV
	return json.Marshal(struct {
		performanceNAVAlias
		FinalizedAt *string `json:"finalized_at,omitempty"`
		CreatedAt   *string `json:"created_at,omitempty"`
		UpdatedAt   *string `json:"updated_at,omitempty"`
	}{
		performanceNAVAlias: performanceNAVAlias(nav),
		FinalizedAt:         optionalBusinessTime(nav.FinalizedAt),
		CreatedAt:           optionalBusinessTime(nav.CreatedAt),
		UpdatedAt:           optionalBusinessTime(nav.UpdatedAt),
	})
}

func (item NAVReconciliation) MarshalJSON() ([]byte, error) {
	type navReconciliationAlias NAVReconciliation
	return json.Marshal(struct {
		navReconciliationAlias
		ReviewedAt *string `json:"reviewed_at,omitempty"`
		CreatedAt  *string `json:"created_at,omitempty"`
		UpdatedAt  *string `json:"updated_at,omitempty"`
	}{
		navReconciliationAlias: navReconciliationAlias(item),
		ReviewedAt:             optionalBusinessTime(item.ReviewedAt),
		CreatedAt:              optionalBusinessTime(item.CreatedAt),
		UpdatedAt:              optionalBusinessTime(item.UpdatedAt),
	})
}

func (accrual ReverseRepoAccrual) MarshalJSON() ([]byte, error) {
	type reverseRepoAccrualAlias ReverseRepoAccrual
	return json.Marshal(struct {
		reverseRepoAccrualAlias
		CalculatedAt *string `json:"calculated_at,omitempty"`
		SettledAt    *string `json:"settled_at,omitempty"`
	}{
		reverseRepoAccrualAlias: reverseRepoAccrualAlias(accrual),
		CalculatedAt:            optionalBusinessTime(accrual.CalculatedAt),
		SettledAt:               optionalBusinessTime(accrual.SettledAt),
	})
}
