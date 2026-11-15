package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"math"
	"os"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

const fuyingTransferDate = "2026-11-13"

func fuyingConfirmedTransfer() ledger.CashLedgerEntry {
	return ledger.CashLedgerEntry{
		EntryID: "user-confirmed-768-stock-to-futures-20261113", AccountID: fuying13Account,
		TradeDate: fuyingTransferDate, LedgerType: "withdrawal", FlowClass: "external_flow",
		Currency: "CNY", Amount: -5000000, Status: "confirmed", Source: "user_confirmation_20261115",
		Description: "Account-boundary outflow only; product internal transfer, destination credit unverified",
		RawPayload: map[string]any{"effective_time_precision": "date", "user_time_description": "approximately midday Asia/Shanghai",
			"product_flow_class": "internal_transfer", "destination_credit_verified": false, "preview_only": true},
	}
}

// The account-scoped engine needs an outflow; this is not a product redemption.
// Deliberately no EffectiveAt: the existing engine marks its date-only weighting
// as estimated. Neither that estimate nor the scenario NAV is publishable.
func addFuyingTransfer(q ledger.CashLedgerQuery, rows []ledger.CashLedgerEntry) ([]ledger.CashLedgerEntry, error) {
	if q.AccountID != fuying13Account || (q.TradeDate != fuyingTransferDate && q.TradeDate != "20261113") {
		return rows, nil
	}
	if q.FlowClass != "external_flow" || q.Status != "confirmed" {
		return rows, nil
	}
	if len(rows) != 0 {
		return nil, errors.New("existing day cash flow requires renewed duplicate review")
	}
	return []ledger.CashLedgerEntry{fuyingConfirmedTransfer()}, nil
}

type fuyingTransferPreviewStore struct{ *ledger.Repository }

func validateFuyingTransferComparison(before, after performance.EconomicNAVResult) error {
	for _, r := range []performance.EconomicNAVResult{before, after} {
		if r.AccountID != fuying13Account || r.TradeDate != fuyingTransferDate || r.Persisted {
			return errors.New("unexpected scenario identity or persistence")
		}
	}
	if before.CashFlows.ExternalNetFlow != 0 || before.CashFlows.ExternalFlowCount != 0 || after.CashFlows.ExternalNetFlow != -5000000 || after.CashFlows.ExternalFlowCount != 1 {
		return errors.New("account flow injection was not consumed exactly once")
	}
	if !reflect.DeepEqual(before.Valuation, after.Valuation) || !reflect.DeepEqual(before.AssetBasis, after.AssetBasis) ||
		before.NAV.OpenEconomicNAV != after.NAV.OpenEconomicNAV || before.NAV.CloseEconomicNAV != after.NAV.CloseEconomicNAV ||
		math.IsNaN(after.NAV.AccountDayPnL-before.NAV.AccountDayPnL) || math.Abs(after.NAV.AccountDayPnL-before.NAV.AccountDayPnL-5000000) > 0.000001 {
		return errors.New("valuation or other economic inputs changed; cannot attribute difference solely to transfer")
	}
	return nil
}

func (s *fuyingTransferPreviewStore) ListCashLedgerEntries(ctx context.Context, q ledger.CashLedgerQuery) ([]ledger.CashLedgerEntry, error) {
	rows, err := s.Repository.ListCashLedgerEntries(ctx, q)
	if err != nil {
		return nil, err
	}
	return addFuyingTransfer(q, rows)
}

func runFuyingTransferPreview(args []string) error {
	f := flag.NewFlagSet("performance-fuying13-transfer-preview-20261115", flag.ContinueOnError)
	configPath := f.String("config", "", "production config, no environment switching")
	output := f.String("output", "", "new private evidence JSON; no apply option")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *output == "" {
		return errors.New("explicit config and new output required")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	parsed, err := pgconn.ParseConfig(cfg.Database.DSN)
	if err != nil || string(cfg.Service.Environment) != "production" || cfg.Database.ExpectedName == "" || parsed.Database != cfg.Database.ExpectedName {
		return errors.New("review requires explicit production database identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name, readOnly string
	if err := tx.QueryRowContext(ctx, `SELECT current_database(), current_setting('transaction_read_only')`).Scan(&name, &readOnly); err != nil || name != cfg.Database.ExpectedName || readOnly != "on" {
		return errors.New("connected database identity/read-only guard failed")
	}
	repo := ledger.NewRepository(tx)
	entries, err := repo.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{AccountID: fuying13Account, DateFrom: "2026-11-12", DateTo: "2026-11-15", Limit: 1000})
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("cash ledger changed; review all statuses/classes before adding any scenario flow")
	}
	observations := make(map[string]ledger.AssetPositionObservation)
	for _, kind := range []string{"open", "close"} {
		row, err := repo.GetAssetPositionObservation(ctx, fuying13Account, fuyingTransferDate, kind)
		if err != nil {
			return err
		}
		observations[kind] = row
	}
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	results := make(map[string]performance.EconomicNAVResult)
	for _, scenario := range []string{"unchanged", "confirmed_account_outflow"} {
		var store performance.Store = repo
		if scenario == "confirmed_account_outflow" {
			store = &fuyingTransferPreviewStore{Repository: repo}
		}
		service, err := settlement20261110Service(cfg, store, client)
		if err != nil {
			return err
		}
		result, err := service.CalculateEconomicNAV(ctx, fuying13Account, fuyingTransferDate, performance.EconomicNAVOptions{Persist: false})
		if err != nil {
			return err
		}
		if result.Persisted {
			return errors.New("unexpected persisted result")
		}
		results[scenario] = result
	}
	if err := validateFuyingTransferComparison(results["unchanged"], results["confirmed_account_outflow"]); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	report := map[string]any{
		"schema": "fuying13_confirmed_transfer_preview.v1", "generated_at": timeutil.Now(),
		"account_id": fuying13Account, "trade_date": fuyingTransferDate, "environment": cfg.Service.Environment, "database": name,
		"database_read_only": true, "database_writes": 0, "broker_commands": 0, "economic_nav_publishable": false,
		"confirmed_source_transfer": fuyingConfirmedTransfer(), "existing_cash_entries": entries,
		"observations": observations, "scenarios": results,
		"limitations": []string{"account-boundary comparison, not product redemption", "destination account and credit unverified",
			"midday is approximate; engine date-only half-weight is not an observed time or approved return",
			"broker total asset remains the chosen basis; October ETF scope and prior NAV gaps unchanged",
			"small residual is not a fee or proof of complete attribution", "market reads are not an atomic snapshot"},
	}
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	summary := make(map[string]any)
	for name, result := range results {
		summary[name] = map[string]any{"status": result.Status, "account_day_pnl": result.NAV.AccountDayPnL,
			"external_net_flow": result.CashFlows.ExternalNetFlow, "reverse_repo": result.ReverseRepo, "quality_flags": result.QualityFlags}
	}
	return writeJSON(map[string]any{"database_writes": 0, "economic_nav_publishable": false, "scenarios": summary})
}
