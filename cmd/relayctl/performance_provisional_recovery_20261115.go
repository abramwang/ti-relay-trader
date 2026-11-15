package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type provisionalRecoveryPlan struct {
	Schema        string                               `json:"schema"`
	Account       string                               `json:"account_id"`
	Dates         []string                             `json:"dates"`
	Anchor        ledger.PerformanceNAV                `json:"anchor"`
	Before        []ledger.PerformanceNAV              `json:"before"`
	Assets        []performance.ReconstructionAssetDay `json:"assets"`
	NAVs          []ledger.PerformanceNAV              `json:"navs"`
	Authorization map[string]any                       `json:"authorization"`
	InputSHA      map[string]string                    `json:"input_sha256"`
}

func reconstructionSHA(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "invalid-json"
	}
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&canonical) != nil {
		return "invalid-json"
	}
	b, err = json.Marshal(canonical)
	if err != nil {
		return "invalid-json"
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func readProvisionalPlan(path, digest string, target *provisionalRecoveryPlan) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		return errors.New("reviewed plan fingerprint mismatch")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(target)
}

func sameProvisionalNAV(left, right ledger.PerformanceNAV) bool {
	if reconstructionSHA(left.PnLComponents) != reconstructionSHA(right.PnLComponents) || !reflect.DeepEqual(left.QualityFlags, right.QualityFlags) {
		return false
	}
	left.PnLComponents, right.PnLComponents = nil, nil
	return sameContinuousNAV(left, right)
}

// The broker's missing source date stays unverified. Only an unambiguous,
// same-security prior redemption corroborated by the pinned PCF may be inferred.
func validateProvisionalCashSources(ctx context.Context, repo interface {
	ListOrders(context.Context, trading.OrderQuery) ([]trading.Order, error)
}, matrix fuyingCashMatrix) error {
	for _, group := range matrix.PCF.Candidates {
		if group.Verified || group.Included || len(group.Matches) != 1 {
			return errors.New("cash source is not a unique provisional candidate")
		}
		match := group.Matches[0]
		parts := strings.Split(match.SecurityID, ".")
		if len(parts) != 2 || len(match.OrderIDs) == 0 {
			return errors.New("candidate security/order evidence missing")
		}
		posted := ""
		for _, id := range group.FlowIDs {
			found := false
			for _, row := range matrix.Unresolved {
				if row.FlowID != id {
					continue
				}
				if row.Raw["股票代码"] != parts[0] {
					return errors.New("cash security differs from redemption")
				}
				d := row.Raw["交易日期"]
				if posted != "" && posted != d {
					return errors.New("cash group spans posting days")
				}
				posted, found = d, true
			}
			if !found {
				return errors.New("candidate cash missing")
			}
		}
		posting, err := time.Parse("20060102", posted)
		if err != nil {
			return err
		}
		orders, err := repo.ListOrders(ctx, trading.OrderQuery{AccountID: fuying13Account, Symbol: parts[0], Exchange: trading.Exchange(parts[1]), DateFrom: "2026-10-09", DateTo: posting.AddDate(0, 0, -1).Format("2006-01-02"), Limit: 500, History: true})
		if err != nil {
			return err
		}
		if len(orders) >= 500 {
			return errors.New("candidate order evidence truncated")
		}
		latest := ""
		matched := map[string]bool{}
		for _, o := range orders {
			if o.TradeSide != "R" || o.CumFilledQty <= 0 {
				continue
			}
			if o.TradeDate > latest {
				latest = o.TradeDate
			}
			if o.TradeDate == compactToISODate(match.SourceDate) {
				matched[o.GatewayOrderID] = true
			}
		}
		if latest < compactToISODate(match.SourceDate) || len(matched) != len(match.OrderIDs) {
			return fmt.Errorf("prior redemption differs from PCF source: security=%s posted=%s latest=%s source=%s matched=%d expected=%d", match.SecurityID, posted, latest, match.SourceDate, len(matched), len(match.OrderIDs))
		}
		for _, id := range match.OrderIDs {
			if !matched[id] {
				return errors.New("candidate redemption not found")
			}
		}
	}
	return nil
}

func readProvisionalRecovery(ctx context.Context, tx *sql.Tx, cfg *config.Config) (provisionalRecoveryPlan, error) {
	p := provisionalRecoveryPlan{Schema: "relay.provisional_reconstruction_review.v1", Account: fuying13Account,
		Authorization: map[string]any{"approved": true, "scope": "provisional_only", "confirmed_date": "2026-11-15", "evidence_type": "user_confirmation", "quote": "按CSV重建暂定净值，保留未核验项"},
		InputSHA:      map[string]string{}}
	repo := ledger.NewRepository(tx)
	previous, err := repo.ListPerformanceNAVs(ctx, fuying13Account, "2026-10-09", "2026-11-12")
	if err != nil {
		return p, err
	}
	if len(previous) != 7 || previous[0].TradeDate != "2026-10-09" || previous[6].TradeDate != "2026-10-19" {
		return p, errors.New("reviewed seven-row published baseline changed; no overwrite")
	}
	for _, n := range previous {
		if n.FormulaVersion != fuyingContinuousFormula || n.Status != "provisional" {
			return p, errors.New("unexpected published NAV scope")
		}
	}
	p.Anchor, p.Before = previous[0], previous
	input, err := loadFuyingNAVInputs(ctx, tx)
	if err != nil {
		return p, err
	}
	var matrix fuyingCashMatrix
	if err := readPinnedJSON(fuyingCashMatrixPath, fuyingCashMatrixHash, &matrix); err != nil {
		return p, err
	}
	p.InputSHA["cash_matrix"] = fuyingCashMatrixHash
	p.InputSHA["funds"] = brokerBasis20261110Specs[0].FundsHash
	p.InputSHA["flows"] = brokerBasis20261110Specs[0].FlowsHash
	if err := validateProvisionalCashSources(ctx, repo, matrix); err != nil {
		return p, err
	}
	p.Authorization["cash_source_policy"] = "same_etf_prior_redemption_and_unique_pcf_match"
	p.Authorization["cash_source_user_instruction"] = "找最近有申购赎回交易的交易日对上"
	client, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return p, err
	}
	start, _ := time.ParseInLocation("2006-01-02", "2026-10-12", timeutil.Location())
	for d := start; d.Format("2006-01-02") <= "2026-11-12"; d = d.AddDate(0, 0, 1) {
		cal, e := client.TradingDayStatus(ctx, d.Format("20060102"))
		if e != nil || !cal.IsTradingDayKnown {
			return p, fmt.Errorf("calendar unavailable: %v", e)
		}
		if cal.IsTradingDay {
			p.Dates = append(p.Dates, d.Format("2006-01-02"))
		}
	}
	if len(p.Dates) != 24 || len(input.Bases) != 21 {
		return p, errors.New("calendar/broker coverage mismatch")
	}
	bridge, err := reviewFuyingObservedCash(matrix, p.Dates[:21])
	if err != nil {
		return p, err
	}
	for i, b := range input.Bases {
		if b.Date != p.Dates[i] {
			return p, errors.New("broker date coverage mismatch")
		}
		open, e1 := fuyingBasisCents(b, "reported_open_total_asset")
		other, e2 := fuyingBasisCents(b, "broker_unclassified_asset_component")
		deposit, e3 := fuyingBasisCents(b, "reported_deposit")
		withdrawal, e4 := fuyingBasisCents(b, "reported_withdrawal")
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || deposit != 0 || withdrawal != 0 {
			return p, errors.New("external flows require separate weighting review")
		}
		var unresolved []string
		for _, r := range matrix.Unresolved {
			if compactToISODate(r.Raw["交易日期"]) == b.Date {
				unresolved = append(unresolved, r.FlowID)
			}
		}
		t := bridge.Days[i]
		var inferredReceivable, inferredPayable int64
		for _, l := range input.Candidates {
			if l.Source <= b.Date && b.Date < l.Posted {
				if l.Cents > 0 {
					inferredReceivable += l.Cents
				} else {
					inferredPayable -= l.Cents
				}
			}
		}
		evidence := map[string]any{"broker_basis": b, "cash_timing": t, "cash_matrix_sha256": fuyingCashMatrixHash, "provisional_cash_source_matches": matrix.PCF.Candidates, "inferred_cash_legs": input.Candidates, "private_source_verified": false}
		p.Assets = append(p.Assets, performance.ReconstructionAssetDay{Date: b.Date, ReportedClose: b.Asset.NetAsset, ObservedOpen: float64(open) / 100,
			ReceivableCents: t.CloseReceivableCents, PayableCents: t.ClosePayableCents, InferredReceivableCents: inferredReceivable, InferredPayableCents: inferredPayable, OtherCents: other, UnresolvedFlows: unresolved, Source: "broker_statement_with_observed_cash_timing", InputSHA: reconstructionSHA(evidence), Evidence: evidence})
	}
	if bridge.Days[20].CloseReceivableCents != 0 || bridge.Days[20].ClosePayableCents != 0 {
		return p, errors.New("observed historical cash does not release before OC continuation")
	}
	service, err := settlement20261110Service(cfg, repo, client)
	if err != nil {
		return p, err
	}
	for _, date := range p.Dates[21:] {
		native, e := service.CalculateEconomicNAV(ctx, fuying13Account, date, performance.EconomicNAVOptions{})
		if e != nil {
			return p, e
		}
		allowed := map[string]bool{"performance_inception_baseline": true, "research_position_valuation": true, "broker_position_cost_excluded": true, "reverse_repo_accrual_preview": true, "reverse_repo_principal_treatment_inferred": true, "reverse_repo_principal_separate_from_cash": true, "reverse_repo_estimated_interest_excluded": true, "previous_economic_nav_gap": true, "attribution_residual_within_tolerance": true}
		for _, flag := range native.QualityFlags {
			if !allowed[flag] {
				return p, fmt.Errorf("unreviewed OC continuation flag %s on %s", flag, date)
			}
		}
		cash, e := repo.ListCashLedgerEntries(ctx, ledger.CashLedgerQuery{AccountID: fuying13Account, TradeDate: date})
		if e != nil {
			return p, e
		}
		if len(cash) != 0 || native.NAV.ExternalNetFlow != 0 || native.Valuation.ETFSettlementEstimate != 0 {
			return p, errors.New("OC continuation cash/ETF scope changed")
		}
		evidence := map[string]any{"native_nav": native.NAV, "source_quality_flags": native.QualityFlags, "valuation": native.Valuation, "reverse_repo": native.ReverseRepo, "cash_entries": cash}
		p.Assets = append(p.Assets, performance.ReconstructionAssetDay{Date: date, ReportedClose: native.NAV.CloseEconomicNAV, ObservedOpen: native.NAV.OpenEconomicNAV,
			Source: "oc_saved_close_and_meridian", InputSHA: reconstructionSHA(evidence), Evidence: evidence})
	}
	p.NAVs, err = performance.BuildProvisionalReconstruction(p.Anchor, p.Dates, p.Assets, p.Authorization)
	return p, err
}

func insertProvisionalRecovery(ctx context.Context, tx *sql.Tx, p provisionalRecoveryPlan, planSHA string) (int, error) {
	if len(planSHA) != 64 || p.Account != fuying13Account || len(p.NAVs) != 24 {
		return 0, errors.New("invalid recovery scope")
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE performance_nav_versions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	repo := ledger.NewRepository(tx)
	current, err := repo.ListPerformanceNAVs(ctx, p.Account, "2026-10-09", "2026-11-12")
	if err != nil {
		return 0, err
	}
	if reconstructionSHA(current) != reconstructionSHA(p.Before) {
		return 0, errors.New("NAV changed before publication")
	}
	rebuilt, err := performance.BuildProvisionalReconstruction(p.Anchor, p.Dates, p.Assets, p.Authorization)
	if err != nil {
		return 0, err
	}
	if reconstructionSHA(rebuilt) != reconstructionSHA(p.NAVs) {
		return 0, errors.New("NAV plan differs from independent arithmetic")
	}
	for _, n := range p.NAVs {
		n.Source = "relay.provisional_reconstruction:" + planSHA
		if _, err := repo.UpsertPerformanceNAV(ctx, n); err != nil {
			return 0, err
		}
	}
	return len(p.NAVs), nil
}

func runProvisionalRecovery(args []string) error {
	f := flag.NewFlagSet("performance-provisional-recovery-20261115", flag.ContinueOnError)
	configPath := f.String("config", "", "production config")
	output := f.String("output", "", "new private report")
	apply := f.Bool("apply", false, "publish the user-authorized provisional chain, never final clearing")
	planPath := f.String("plan", "", "reviewed plan")
	planSHA := f.String("plan-sha256", "", "reviewed SHA256")
	backup := f.String("backup", "", "fresh full backup")
	backupSHA := f.String("backup-sha256", "", "backup digest")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *output == "" || (*apply && (*planPath == "" || len(*planSHA) != 64 || *backup == "" || len(*backupSHA) != 64)) {
		return errors.New("config/output and pinned plan/backup for apply required")
	}
	for _, path := range []string{*output, *output + ".commit.json"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("output already exists")
		}
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("production database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if *apply {
		if err := validateResearchBackup(ctx, *backup, *backupSHA); err != nil {
			return err
		}
	}
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !*apply, Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("database identity mismatch")
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
		return err
	}
	p, err := readProvisionalRecovery(ctx, tx, cfg)
	if err != nil {
		return err
	}
	written := 0
	if *apply {
		var reviewed provisionalRecoveryPlan
		if err := readProvisionalPlan(*planPath, *planSHA, &reviewed); err != nil {
			return err
		}
		if reconstructionSHA(reviewed) != reconstructionSHA(p) {
			return errors.New("current inputs differ from reviewed provisional plan")
		}
		written, err = insertProvisionalRecovery(ctx, tx, p, *planSHA)
		if err != nil {
			return err
		}
	}
	if err := writeETFTransferReviewFile(*output, p); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if *apply {
		rows, e := ledger.NewRepository(db).ListPerformanceNAVs(ctx, p.Account, "2026-10-12", "2026-11-12")
		if e != nil || len(rows) != 24 {
			return errors.New("commit succeeded; readback failed")
		}
		for i, n := range rows {
			expected := p.NAVs[i]
			expected.Source = "relay.provisional_reconstruction:" + *planSHA
			if !sameProvisionalNAV(n, expected) {
				return errors.New("commit succeeded; readback differs")
			}
		}
	}
	r := map[string]any{"committed": true, "applied": *apply, "nav_rows_written": written, "cash_rows_written": 0, "cost_rows_written": 0, "broker_commands": 0, "status": "provisional", "final_settlement_verified": false, "date_to": "2026-11-12", "account_id": p.Account, "plan_sha256": *planSHA, "backup_sha256": *backupSHA, "completed_at": timeutil.Now()}
	if err := writeETFTransferReviewFile(*output+".commit.json", r); err != nil {
		return fmt.Errorf("commit succeeded: %w", err)
	}
	return writeJSON(r)
}
