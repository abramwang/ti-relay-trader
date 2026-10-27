package ledger

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

func TestBrokerCashTimestampReadOnly(t *testing.T) {
	dsn := os.Getenv("RELAY_CASH_TIME_READONLY_DATABASE_URL")
	if dsn == "" {
		t.Skip("opt-in read-only cash timestamp acceptance")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, tc := range []struct {
		name, kind, raw string
		missing         bool
	}{
		{"source before batch", "broker_close", `{"asset":{"updated_at":"2026-10-21T15:01:01+08:00"}}`, false},
		{"legacy broker source", "broker_close", `{"broker_asset":{"updated_at":"2026-10-21T15:01:01+08:00"}}`, false},
		{"missing fails closed", "broker_close", `{}`, true},
		{"derived close unchanged", "close", `{"asset":{"updated_at":"2026-10-21T15:01:01+08:00"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, statement := range []string{assetSnapshotSQL, latestAssetSQL} {
				query := `WITH asset_snapshots AS (SELECT 'acct'::text account_id, $2::date trade_date,
                $3::text snapshot_type, 1000::numeric cash_available, 1000::numeric cash_total, 1000::numeric net_asset,
                0::numeric market_value, 0::numeric stock_value, 0::numeric fund_value, 0::numeric reverse_repo_receivable,
                0::numeric commission, 0::numeric day_profit, 0::numeric position_profit, 0::numeric close_profit, 0::numeric credit,
                '2026-10-21T15:01:20+08:00'::timestamptz captured_at, 1 asset_snapshot_pk, $4::jsonb raw_payload) ` + statement
				asset, err := scanAsset(tx.QueryRowContext(ctx, query, "acct", "2026-10-21", tc.kind, tc.raw))
				if err != nil {
					t.Fatal(err)
				}
				if tc.missing {
					if !asset.UpdatedAt.IsZero() {
						t.Fatalf("missing source fell back to batch: %v", asset.UpdatedAt)
					}
					continue
				}
				want := "2026-10-21T15:01:01+08:00"
				if tc.kind == "close" {
					want = "2026-10-21T15:01:20+08:00"
				}
				expected, _ := time.Parse(time.RFC3339, want)
				if !asset.UpdatedAt.Equal(expected) {
					t.Fatalf("got %v want %v", asset.UpdatedAt, expected)
				}
			}
		})
	}
	for _, sample := range []struct{ account, date string }{{"307000051388", "2026-10-21"}, {"314000045768", "2026-10-26"}} {
		asset, err := NewRepository(tx).GetAssetSnapshot(ctx, sample.account, sample.date, "broker_close")
		if err != nil {
			t.Fatal(err)
		}
		var source, capture time.Time
		if err := tx.QueryRowContext(ctx, `SELECT (raw_payload #>> '{asset,updated_at}')::timestamptz,captured_at FROM asset_snapshots WHERE account_id=$1 AND trade_date=$2 AND snapshot_type='broker_close'`, sample.account, sample.date).Scan(&source, &capture); err != nil {
			t.Fatal(err)
		}
		if !asset.UpdatedAt.Equal(source) || !source.Before(capture) {
			t.Fatalf("original cash time not restored: %v %v %v", asset.UpdatedAt, source, capture)
		}
		t.Logf("%s %s source=%s captured=%s", sample.account, sample.date, source.Format(time.RFC3339Nano), capture.Format(time.RFC3339Nano))
	}
}
