package api

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

func TestSettlementUsesCashTimeNotBatchCompletion(t *testing.T) {
	batch := time.Date(2026, 10, 21, 15, 1, 20, 0, timeutil.Location())
	for _, tc := range []struct {
		name          string
		cashTime      time.Time
		wantPrincipal float64
		wantError     bool
	}{
		{"cash before repo", batch.Add(-19 * time.Second), 0, false},
		{"cash after repo", batch.Add(-time.Second), 1000, false},
		{"missing source time fails closed", time.Time{}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeSettlementStore{assetSnapshotResult: trading.Asset{AccountID: "acct", CashTotal: 2000, NetAsset: 2000, UpdatedAt: tc.cashTime}}
			server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), settles: store,
				orders: &fakeOrderSubmitter{listFillsResult: orderflow.ListFillsResult{Fills: []trading.Fill{{
					FillID: "repo", GatewayOrderID: "repo", AccountID: "acct", Symbol: "204001", Exchange: trading.ExchangeSH,
					TradeSide: trading.TradeSideSell, Qty: 10, Price: 1.4, MatchedAt: batch.Add(-4 * time.Second),
				}}}},
			}
			result := server.buildAccountSettlementSnapshot(context.Background(), "acct", "2026-10-21", "close", "broker_close", "test", "test", false, false, batch, batch.Add(-time.Hour))
			if tc.wantError {
				if !strings.Contains(strings.Join(result.Errors, ";"), "observation time is missing") || len(store.assetSnapshots) != 0 || len(store.positionSnapshots) != 0 {
					t.Fatalf("missing time wrote snapshot: %#v", result)
				}
				return
			}
			if len(result.Errors) != 0 || len(store.assetSnapshots) != 1 {
				t.Fatalf("snapshot result: %#v", result)
			}
			asset := store.assetSnapshots[0].asset
			if asset.ReverseRepoReceivable != tc.wantPrincipal || asset.NetAsset != 2000+tc.wantPrincipal || !asset.UpdatedAt.Equal(tc.cashTime) {
				t.Fatalf("incorrect cutoff: %#v", asset)
			}
		})
	}
}
