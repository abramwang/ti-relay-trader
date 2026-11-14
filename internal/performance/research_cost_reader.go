package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

var ErrResearchCostUnavailable = errors.New("research cost storage unavailable")

type ResearchCostVersionInfo struct {
	VersionID     string    `json:"version_id"`
	AnchorDate    string    `json:"anchor_date"`
	DateFrom      string    `json:"date_from"`
	DateTo        string    `json:"date_to"`
	PayloadSHA256 string    `json:"payload_sha256"`
	CreatedAt     time.Time `json:"created_at"`
}

// ResearchCostReader owns only read-only transactions; it has no market,
// command publisher, migration runner, or calculation/persistence dependency.
type ResearchCostReader struct{ DB *sql.DB }

func researchReadError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "42P01" {
		return ErrResearchCostUnavailable
	}
	return err
}

func (reader *ResearchCostReader) ListVersions(ctx context.Context, account string) ([]ResearchCostVersionInfo, error) {
	if reader == nil || reader.DB == nil {
		return nil, ErrResearchCostUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := reader.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT version_id,anchor_date::text,date_from::text,date_to::text,payload_sha256,created_at FROM public.performance_research_cost_versions WHERE account_id=$1 ORDER BY created_at DESC,version_id LIMIT 201`, account)
	if err != nil {
		return nil, researchReadError(err)
	}
	defer rows.Close()
	items := make([]ResearchCostVersionInfo, 0)
	for rows.Next() {
		var v ResearchCostVersionInfo
		if err := rows.Scan(&v.VersionID, &v.AnchorDate, &v.DateFrom, &v.DateTo, &v.PayloadSHA256, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.CreatedAt = v.CreatedAt.In(timeutil.Location())
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) > 200 {
		return nil, fmt.Errorf("research version listing exceeds reviewed limit")
	}
	return items, nil
}

func (reader *ResearchCostReader) Compare(ctx context.Context, account, version string) (ResearchCostComparison, error) {
	if reader == nil || reader.DB == nil {
		return ResearchCostComparison{}, ErrResearchCostUnavailable
	}
	if version == "" || strings.TrimSpace(version) != version || len(version) > 160 {
		return ResearchCostComparison{}, fmt.Errorf("explicit version required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := reader.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ResearchCostComparison{}, err
	}
	defer tx.Rollback()
	r, err := ReadResearchCostComparison(ctx, tx, account, version)
	return r, researchReadError(err)
}

func ReadResearchCostComparison(ctx context.Context, tx *sql.Tx, account, version string) (ResearchCostComparison, error) {
	for _, s := range []string{`SET LOCAL search_path TO public`, `SET LOCAL statement_timeout='30s'`, `SET LOCAL lock_timeout='5s'`} {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return ResearchCostComparison{}, err
		}
	}
	var readOnly, isolation string
	if err := tx.QueryRowContext(ctx, `SELECT current_setting('transaction_read_only'),current_setting('transaction_isolation')`).Scan(&readOnly, &isolation); err != nil || readOnly != "on" || isolation != "repeatable read" {
		return ResearchCostComparison{}, errors.New("comparison requires read-only repeatable-read transaction")
	}
	repo := ledger.NewRepository(tx)
	v, err := repo.GetResearchCostVersion(ctx, account, version)
	if err != nil {
		return ResearchCostComparison{}, err
	}
	b, err := DecodeResearchCostVersion(v)
	if err != nil {
		return ResearchCostComparison{}, err
	}
	opening, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: account, BeforeDate: b.Dates[0]})
	if err != nil {
		return ResearchCostComparison{}, err
	}
	var active []ledger.T0Allocation
	var stored []ledger.PositionCostState
	for _, date := range b.Dates {
		history, err := repo.ListT0AllocationHistory(ctx, account, date)
		if err != nil {
			return ResearchCostComparison{}, err
		}
		items, err := ledger.ResolveT0Allocations(history)
		if err != nil {
			return ResearchCostComparison{}, err
		}
		active = append(active, items...)
		rows, err := repo.ListPositionCostStates(ctx, ledger.PositionCostStateQuery{AccountID: account, TradeDate: date})
		if err != nil {
			return ResearchCostComparison{}, err
		}
		stored = append(stored, rows...)
		if len(stored) > 100000 || len(active) > 10000 {
			return ResearchCostComparison{}, errors.New("comparison read budget exceeded")
		}
	}
	return CompareResearchCostVersion(v, opening, active, stored)
}
