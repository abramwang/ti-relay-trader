package redisstream

import (
	"context"
	"encoding/json"

	"ti-relay-trader/internal/ledger"
)

// This is a point-in-time source check, not an XREAD or continuity proof.
func (r *RedisLedgerReceiptReader) ProbeHandoff(ctx context.Context, b ledger.LedgerStreamBinding, cursor string) (ledger.LedgerHandoffWitness, error) {
	key, err := b.Stream()
	if err != nil {
		return ledger.LedgerHandoffWitness{}, err
	}
	observation, err := r.Observe(ctx, key, cursor)
	if err != nil {
		return ledger.LedgerHandoffWitness{}, err
	}
	if err = checkLedgerSource(b, cursor, observation); err != nil {
		return ledger.LedgerHandoffWitness{}, err
	}
	raw, err := json.Marshal(struct {
		Observation         LedgerSourceObservation `json:"observation"`
		ContinuityVerified  bool                    `json:"continuity_verified"`
		StreamEpochVerified bool                    `json:"stream_epoch_verified"`
	}{Observation: observation})
	return ledger.LedgerHandoffWitness{AnchorSHA256: observation.AnchorSHA256, Evidence: raw}, err
}
