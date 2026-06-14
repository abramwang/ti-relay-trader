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
