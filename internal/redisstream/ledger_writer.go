package redisstream

import (
	"context"
	"errors"

	"ti-relay-trader/internal/ledger"
)

func acquireLegacyLedgerWriter(ctx context.Context, writer LedgerWriter, prefixes, roles []string) (*ledger.LedgerStreamWriter, error) {
	repo, ok := writer.(*ledger.Repository)
	if !ok {
		return nil, errors.New("ledger stream writer requires a PostgreSQL repository")
	}
	keys := []string{}
	for _, prefix := range prefixes {
		for _, role := range outputRoles(roles) {
			if key := streamNameForRole(NewStreams(prefix), role); key != "" {
				keys = append(keys, key)
			}
		}
	}
	return repo.AcquireStreamWriter(ctx, keys, false)
}
