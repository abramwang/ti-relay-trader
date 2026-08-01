package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	relayperformance "ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

const defaultPerformanceGoldSource = "manual_user_confirmed"

type performanceGoldImportReport struct {
	AccountID string                      `json:"account_id"`
	Input     string                      `json:"input"`
	Source    string                      `json:"source"`
	SourceRef string                      `json:"source_ref"`
	Status    string                      `json:"status"`
	Persist   bool                        `json:"persist"`
	Rows      int                         `json:"rows"`
	Saved     []ledger.PerformanceNAVGold `json:"saved,omitempty"`
	Validated []ledger.PerformanceNAVGold `json:"validated,omitempty"`
}

type performanceGoldCompareItem struct {
	Gold                 ledger.PerformanceNAVGold                      `json:"gold"`
	Available            bool                                           `json:"available"`
	Status               string                                         `json:"status,omitempty"`
	FormulaVersion       string                                         `json:"formula_version,omitempty"`
	RelayCloseAsset      float64                                        `json:"relay_close_asset,omitempty"`
	RelayDailyPnL        float64                                        `json:"relay_daily_pnl,omitempty"`
	CloseDiff            float64                                        `json:"close_diff,omitempty"`
	PnLDiff              float64                                        `json:"pnl_diff,omitempty"`
	CloseWithinTolerance bool                                           `json:"close_within_tolerance"`
	PnLWithinTolerance   bool                                           `json:"pnl_within_tolerance"`
	QualityGatePassed    bool                                           `json:"quality_gate_passed"`
	ReverseRepo          relayperformance.EconomicNAVReverseRepoSummary `json:"reverse_repo,omitempty"`
	QualityFlags         []string                                       `json:"quality_flags,omitempty"`
	Error                string                                         `json:"error,omitempty"`
}

type performanceGoldCompareSummary struct {
	Rows                 int `json:"rows"`
	AvailableRows        int `json:"available_rows"`
	UnavailableRows      int `json:"unavailable_rows"`
	BlockedRows          int `json:"blocked_rows"`
	CloseWithinTolerance int `json:"close_within_tolerance"`
	PnLWithinTolerance   int `json:"pnl_within_tolerance"`
	QualityGatePassed    int `json:"quality_gate_passed"`
}

type performanceGoldCompareReport struct {
	AccountID string                        `json:"account_id"`
	DateFrom  string                        `json:"date_from,omitempty"`
	DateTo    string                        `json:"date_to,omitempty"`
	Source    string                        `json:"source"`
	Tolerance float64                       `json:"tolerance"`
	Summary   performanceGoldCompareSummary `json:"summary"`
	Items     []performanceGoldCompareItem  `json:"items"`
}

func runPerformanceGoldImport(args []string) error {
	flags := flag.NewFlagSet("performance-gold-import", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	accountID := flags.String("account", "", "account id")
	input := flags.String("input", "", "manual NAV gold CSV path")
	source := flags.String("source", defaultPerformanceGoldSource, "audited source name")
	sourceRef := flags.String("source-ref", "", "source document reference, defaults to input path")
	status := flags.String("status", "confirmed", "gold status: draft, confirmed, or voided")
	confirmedBy := flags.String("confirmed-by", "", "operator confirming the source")
	persist := flags.Bool("persist", false, "persist the validated rows in one database transaction")
	timeout := flags.Duration("timeout", 30*time.Second, "database operation timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	*accountID = strings.TrimSpace(*accountID)
	*input = strings.TrimSpace(*input)
	*source = strings.TrimSpace(*source)
	*sourceRef = strings.TrimSpace(*sourceRef)
	*status = strings.TrimSpace(*status)
	*confirmedBy = strings.TrimSpace(*confirmedBy)
	if *accountID == "" || *input == "" {
		return fmt.Errorf("-account and -input are required")
	}
	if *source == "" {
		return fmt.Errorf("-source is required")
	}
	if *sourceRef == "" {
		*sourceRef = *input
	}
	if *status == "confirmed" && *confirmedBy == "" {
		return fmt.Errorf("-confirmed-by is required for confirmed gold")
	}
	confirmedAt := time.Time{}
	if *status == "confirmed" {
		confirmedAt = timeutil.Now()
	}
	items, err := readPerformanceGoldCSV(*input, *accountID, *source, *sourceRef, *status, *confirmedBy, confirmedAt)
	if err != nil {
		return err
	}
	report := performanceGoldImportReport{
		AccountID: *accountID,
		Input:     *input,
		Source:    *source,
		SourceRef: *sourceRef,
		Status:    *status,
		Persist:   *persist,
		Rows:      len(items),
	}
	if !*persist {
		report.Validated = items
		return writeJSON(report)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	repo := ledger.NewRepository(tx)
	report.Saved = make([]ledger.PerformanceNAVGold, 0, len(items))
	for _, item := range items {
		saved, err := repo.UpsertPerformanceNAVGold(ctx, item)
		if err != nil {
			return err
		}
		report.Saved = append(report.Saved, saved)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return writeJSON(report)
}

func runPerformanceGoldCompare(args []string) error {
	flags := flag.NewFlagSet("performance-gold-compare", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", os.Getenv(config.EnvPath), "relay YAML config path")
	accountID := flags.String("account", "", "account id")
	dateFrom := flags.String("date-from", "", "first trade date, YYYYMMDD or YYYY-MM-DD")
	dateTo := flags.String("date-to", "", "last trade date, YYYYMMDD or YYYY-MM-DD")
	source := flags.String("source", defaultPerformanceGoldSource, "gold source name")
	tolerance := flags.Float64("tolerance", 0.01, "absolute CNY comparison tolerance")
	timeout := flags.Duration("timeout", 10*time.Minute, "comparison timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	*accountID = strings.TrimSpace(*accountID)
	*source = strings.TrimSpace(*source)
	if *accountID == "" {
		return fmt.Errorf("-account is required")
	}
	if *tolerance < 0 || math.IsNaN(*tolerance) || math.IsInf(*tolerance, 0) {
		return fmt.Errorf("-tolerance must be a finite non-negative number")
	}
	normalizedFrom, normalizedTo, err := normalizePerformanceGoldDateRange(*dateFrom, *dateTo)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	repo := ledger.NewRepository(db)
	gold, err := repo.ListPerformanceNAVGold(ctx, ledger.PerformanceNAVGoldQuery{
		AccountID: *accountID,
		DateFrom:  normalizedFrom,
		DateTo:    normalizedTo,
		Status:    "confirmed",
		Source:    *source,
	})
	if err != nil {
		return err
	}
	if len(gold) == 0 {
		return fmt.Errorf("no current confirmed gold rows found")
	}
	marketClient, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	service, err := relayperformance.New(relayperformance.Options{
		Store:               repo,
		Calendar:            marketClient,
		Market:              marketClient,
		FormulaVersion:      cfg.Performance.FormulaVersion,
		ETFT0FrictionRate:   cfg.Performance.ETFT0FrictionRate,
		AutoToleranceCNY:    cfg.Performance.AutoToleranceCNY,
		AutoToleranceBP:     cfg.Performance.AutoToleranceBP,
		WarningToleranceCNY: cfg.Performance.WarningToleranceCNY,
		WarningToleranceBP:  cfg.Performance.WarningToleranceBP,
	})
	if err != nil {
		return err
	}
	report := performanceGoldCompareReport{
		AccountID: *accountID,
		DateFrom:  normalizedFrom,
		DateTo:    normalizedTo,
		Source:    *source,
		Tolerance: *tolerance,
		Items:     make([]performanceGoldCompareItem, 0, len(gold)),
	}
	for _, goldItem := range gold {
		item := performanceGoldCompareItem{Gold: goldItem}
		preview, previewErr := service.CalculateEconomicNAV(ctx, *accountID, goldItem.TradeDate, relayperformance.EconomicNAVOptions{})
		if previewErr != nil {
			item.Error = previewErr.Error()
			report.Summary.UnavailableRows++
			report.Items = append(report.Items, item)
			continue
		}
		item.Available = true
		item.Status = preview.Status
		item.FormulaVersion = preview.FormulaVersion
		item.RelayCloseAsset = preview.NAV.CloseEconomicNAV
		item.RelayDailyPnL = preview.NAV.AccountDayPnL
		item.CloseDiff = roundComparison(preview.NAV.CloseEconomicNAV - goldItem.CloseAsset)
		item.PnLDiff = roundComparison(preview.NAV.AccountDayPnL - goldItem.DailyPnL)
		item.CloseWithinTolerance = math.Abs(item.CloseDiff) <= *tolerance
		item.PnLWithinTolerance = math.Abs(item.PnLDiff) <= *tolerance
		item.QualityGatePassed = preview.Status != "blocked" && item.CloseWithinTolerance && item.PnLWithinTolerance
		item.ReverseRepo = preview.ReverseRepo
		item.QualityFlags = preview.QualityFlags
		report.Summary.AvailableRows++
		if preview.Status == "blocked" {
			report.Summary.BlockedRows++
		}
		if item.CloseWithinTolerance {
			report.Summary.CloseWithinTolerance++
		}
		if item.PnLWithinTolerance {
			report.Summary.PnLWithinTolerance++
		}
		if item.QualityGatePassed {
			report.Summary.QualityGatePassed++
		}
		report.Items = append(report.Items, item)
	}
	report.Summary.Rows = len(report.Items)
	return writeJSON(report)
}

func readPerformanceGoldCSV(path, accountID, source, sourceRef, status, confirmedBy string, confirmedAt time.Time) ([]ledger.PerformanceNAVGold, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open gold CSV: %w", err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read gold CSV header: %w", err)
	}
	indexes := make(map[string]int, len(header))
	for index, name := range header {
		indexes[strings.TrimSpace(name)] = index
	}
	required := []string{
		"trade_date",
		"open_asset_excluding_fund_occupancy",
		"close_asset_excluding_fund_occupancy",
		"daily_pnl",
	}
	for _, name := range required {
		if _, ok := indexes[name]; !ok {
			return nil, fmt.Errorf("gold CSV missing required column %q", name)
		}
	}
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read gold CSV: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("gold CSV has no data rows")
	}
	seenDates := make(map[string]struct{}, len(rows))
	items := make([]ledger.PerformanceNAVGold, 0, len(rows))
	for rowIndex, row := range rows {
		line := rowIndex + 2
		value := func(name string) (string, error) {
			index := indexes[name]
			if index >= len(row) {
				return "", fmt.Errorf("gold CSV line %d missing value for %s", line, name)
			}
			return strings.TrimSpace(row[index]), nil
		}
		dateValue, err := value("trade_date")
		if err != nil {
			return nil, err
		}
		tradeDate, err := parseCLITradeDate(dateValue)
		if err != nil {
			return nil, fmt.Errorf("gold CSV line %d invalid trade_date: %w", line, err)
		}
		dateText := tradeDate.Format("2006-01-02")
		if _, duplicate := seenDates[dateText]; duplicate {
			return nil, fmt.Errorf("gold CSV line %d duplicates trade_date %s", line, dateText)
		}
		seenDates[dateText] = struct{}{}
		openAsset, openRaw, err := performanceGoldCSVNumber(value, "open_asset_excluding_fund_occupancy")
		if err != nil {
			return nil, fmt.Errorf("gold CSV line %d: %w", line, err)
		}
		closeAsset, closeRaw, err := performanceGoldCSVNumber(value, "close_asset_excluding_fund_occupancy")
		if err != nil {
			return nil, fmt.Errorf("gold CSV line %d: %w", line, err)
		}
		dailyPnL, pnlRaw, err := performanceGoldCSVNumber(value, "daily_pnl")
		if err != nil {
			return nil, fmt.Errorf("gold CSV line %d: %w", line, err)
		}
		observedOpen := closeAsset - dailyPnL
		if openAsset < 0 || closeAsset < 0 || observedOpen < 0 {
			return nil, fmt.Errorf("gold CSV line %d has a negative asset value", line)
		}
		item, err := ledger.PreparePerformanceNAVGold(ledger.PerformanceNAVGold{
			AccountID:           accountID,
			TradeDate:           dateText,
			Status:              status,
			CarriedOpenAsset:    openAsset,
			ObservedOpenAsset:   observedOpen,
			OvernightAdjustment: observedOpen - openAsset,
			CloseAsset:          closeAsset,
			DailyPnL:            dailyPnL,
			AssetScope:          "excluding_fund_occupancy",
			Source:              source,
			SourceRef:           sourceRef,
			ConfirmedBy:         confirmedBy,
			ConfirmedAt:         confirmedAt,
			RawPayload: map[string]any{
				"input":      path,
				"row_number": line,
				"source_fields": map[string]string{
					"trade_date":                           dateValue,
					"open_asset_excluding_fund_occupancy":  openRaw,
					"close_asset_excluding_fund_occupancy": closeRaw,
					"daily_pnl":                            pnlRaw,
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("gold CSV line %d: %w", line, err)
		}
		items = append(items, item)
	}
	return items, nil
}

func performanceGoldCSVNumber(value func(string) (string, error), name string) (float64, string, error) {
	raw, err := value(name)
	if err != nil {
		return 0, "", err
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, raw, fmt.Errorf("invalid %s value %q", name, raw)
	}
	return parsed, raw, nil
}

func normalizePerformanceGoldDateRange(dateFrom, dateTo string) (string, string, error) {
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)
	var from, to time.Time
	var err error
	if dateFrom != "" {
		from, err = parseCLITradeDate(dateFrom)
		if err != nil {
			return "", "", fmt.Errorf("invalid date-from: %w", err)
		}
	}
	if dateTo != "" {
		to, err = parseCLITradeDate(dateTo)
		if err != nil {
			return "", "", fmt.Errorf("invalid date-to: %w", err)
		}
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		return "", "", fmt.Errorf("date-to must not be before date-from")
	}
	fromText, toText := "", ""
	if !from.IsZero() {
		fromText = from.Format("2006-01-02")
	}
	if !to.IsZero() {
		toText = to.Format("2006-01-02")
	}
	return fromText, toText, nil
}

func openPerformanceDatabase(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func roundComparison(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}
