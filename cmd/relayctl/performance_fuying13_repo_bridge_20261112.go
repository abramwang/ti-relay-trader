package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"regexp"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/market"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
)

const fuyingResidualHash = "2f83c667ddcbe944554611d2e559e5f18311012741fe3a733ca6f6537e0dcf5d"
const fuyingCashNAVHash = "4455044be0b1e3f8d113fcabab497951cf2dbe07989bc13acb019b3fd5e6ecf9"

var fuyingRepoCashNumber = regexp.MustCompile(`^[0-9]{1,12}(?:\.[0-9]{1,2})?$`)

func fuyingRepoCashCents(value string) (int64, error) {
	if !fuyingRepoCashNumber.MatchString(value) {
		return 0, errors.New("invalid repo receipt decimal")
	}
	return parseBrokerMoneyCents(value)
}

func fuyingRepoScaled(value float64, scale int64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) >= 1e12 {
		return 0, errors.New("invalid repo model money")
	}
	n := int64(math.Round(value * float64(scale)))
	if float64(n)/float64(scale) != value {
		return 0, errors.New("repo model money precision mismatch")
	}
	return n, nil
}

type fuyingRepoAdjustedDay struct {
	Date         string  `json:"trade_date"`
	Open         float64 `json:"conditional_open_asset"`
	Close        float64 `json:"conditional_close_asset"`
	PnL          float64 `json:"conditional_day_pnl"`
	Index        float64 `json:"normalized_reference_index"`
	OriginalPnL  float64 `json:"original_cash_scenario_pnl"`
	Unclassified float64 `json:"broker_unclassified_asset_preserved"`
}

func fuyingRepoAdjustedCurve(scenario fuyingNAVScenario, bridge performance.RepoInterestBridge) ([]fuyingRepoAdjustedDay, error) {
	if scenario.Publishable || bridge.EconomicNAVPublishable || len(scenario.Days) == 0 || len(scenario.Days) != len(bridge.Days) {
		return nil, errors.New("invalid repo conditional curve scope")
	}
	var days []fuyingRepoAdjustedDay
	var previous int64
	index := 1.0
	for i, d := range scenario.Days {
		b := bridge.Days[i]
		if b.TradeDate != d.Date {
			return nil, errors.New("repo curve date mismatch")
		}
		op, err := fuyingRepoScaled(d.ConditionalOpen, 1000000)
		if err != nil {
			return nil, err
		}
		cl, err := fuyingRepoScaled(d.ConditionalClose, 1000000)
		if err != nil {
			return nil, err
		}
		pnl, err := fuyingRepoScaled(d.PnL, 1000000)
		if err != nil {
			return nil, err
		}
		if cl-op != pnl {
			return nil, errors.New("original cash curve identity failed")
		}
		op += b.OpenReceivableMicros
		cl += b.CloseReceivableMicros
		if op <= 0 || cl <= 0 || (i > 0 && previous != op) || cl-op-pnl != b.AssetAdjustmentMicros {
			return nil, errors.New("repo adjusted curve continuity failed")
		}
		index *= float64(cl) / float64(op)
		days = append(days, fuyingRepoAdjustedDay{d.Date, float64(op) / 1e6, float64(cl) / 1e6,
			float64(cl-op) / 1e6, index, d.PnL, d.Unclassified})
		previous = cl
	}
	return days, nil
}

func runFuyingRepoBridge(args []string) error {
	flags := flag.NewFlagSet("performance-fuying13-repo-bridge-20261112", flag.ContinueOnError)
	configPath := flags.String("config", "", "production config for read-only accrual calculation")
	output := flags.String("output", "", "new private evidence report")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" {
		return errors.New("new output required")
	}
	if _, err := os.Stat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	var evidence struct {
		Account  string `json:"account_id"`
		Receipts []struct {
			Date      string `json:"trade_date"`
			Source    string `json:"source_trade_date"`
			Order     string `json:"gateway_order_id"`
			ID        string `json:"broker_flow_id"`
			Principal string `json:"principal"`
			Interest  string `json:"received_interest"`
		} `json:"repo_receipt_evidence"`
	}
	if err := readPinnedJSON("outputs/recovery/fuying13_cash_matrix_20261112/residual_verified_v2.json", fuyingResidualHash, &evidence); err != nil {
		return err
	}
	var nav struct {
		Account       string                           `json:"account_id"`
		Scenarios     []fuyingNAVScenario              `json:"cash_nav_scenarios"`
		Contributions []performance.ContributionResult `json:"contributions"`
	}
	if err := readPinnedJSON("outputs/recovery/fuying13_cash_matrix_20261112/nav_scenarios_v1.json", fuyingCashNAVHash, &nav); err != nil {
		return err
	}
	if evidence.Account != fuying13Account || nav.Account != fuying13Account || len(evidence.Receipts) != 22 || len(nav.Scenarios) != 2 || len(nav.Scenarios[0].Days) != 21 {
		return errors.New("frozen repo evidence scope changed")
	}
	var dates []string
	for _, day := range nav.Scenarios[0].Days {
		dates = append(dates, day.Date)
	}
	var receipts []performance.RepoInterestReceipt
	for _, r := range evidence.Receipts {
		principal, err := fuyingRepoCashCents(r.Principal)
		if err != nil {
			return err
		}
		interest, err := fuyingRepoCashCents(r.Interest)
		if err != nil {
			return err
		}
		receipts = append(receipts, performance.RepoInterestReceipt{AccountID: fuying13Account, SourceTradeDate: r.Source,
			GatewayOrderID: r.Order, PostedDate: r.Date, ReceiptID: r.ID, PrincipalCents: principal,
			ReceivedAmountCents: principal + interest, EvidenceRef: "sha256:" + fuyingResidualHash + "#" + r.ID})
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("unexpected ledger environment")
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
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("unexpected database")
	}
	m, err := market.NewMeridianClient(cfg.Market)
	if err != nil {
		return err
	}
	service, err := settlement20261110Service(cfg, ledger.NewRepository(tx), m)
	if err != nil {
		return err
	}
	var lots []performance.RepoInterestLot
	var calculations []performance.ReverseRepoResult
	for _, day := range append([]string{"2026-10-09"}, dates...) {
		result, err := service.CalculateReverseRepo(ctx, fuying13Account, day, false)
		if err != nil {
			return err
		}
		if len(result.Accruals) == 0 {
			return fmt.Errorf("missing repo model %s", day)
		}
		var gross int64
		for _, a := range result.Accruals {
			if a.AccountID != fuying13Account || a.TradeDate != day || a.Status == "voided" {
				return errors.New("invalid repo accrual")
			}
			principal, err := fuyingRepoScaled(a.Principal, 100)
			if err != nil {
				return err
			}
			interest, err := fuyingRepoScaled(a.GrossInterest, 1000000)
			if err != nil {
				return err
			}
			gross += interest
			lots = append(lots, performance.RepoInterestLot{AccountID: a.AccountID, TradeDate: day, GatewayOrderID: a.GatewayOrderID,
				SecurityID: a.SecurityID, PrincipalCents: principal, GrossInterestMicros: interest, EvidenceRef: "read_only_model:" + day + ":" + a.GatewayOrderID})
		}
		if day != "2026-10-09" {
			matched := false
			for _, c := range nav.Contributions {
				if c.TradeDate != day {
					continue
				}
				for _, item := range c.Contributions {
					if item.StrategyType != performance.StrategyCashManagement {
						continue
					}
					if matched || item.GrossContribution == nil {
						return errors.New("ambiguous frozen repo contribution")
					}
					v, err := fuyingRepoScaled(*item.GrossContribution, 1000000)
					if err != nil || v != gross {
						return fmt.Errorf("repo model differs from frozen contribution %s", day)
					}
					matched = true
				}
			}
			if !matched {
				return fmt.Errorf("missing frozen contribution %s", day)
			}
		}
		calculations = append(calculations, result)
		fmt.Fprintf(os.Stderr, "repo bridge model %s: orders=%d\n", day, len(result.Accruals))
	}
	bridge, err := performance.PreviewRepoInterestBridge(fuying13Account, "2026-10-09", dates, lots, receipts)
	if err != nil {
		return err
	}
	curves := map[string][]fuyingRepoAdjustedDay{}
	for _, scenario := range nav.Scenarios {
		curve, err := fuyingRepoAdjustedCurve(scenario, bridge)
		if err != nil {
			return err
		}
		curves[scenario.Name] = curve
	}
	report := map[string]any{"account_id": fuying13Account, "generated_at": timeutil.Now(), "database_read_only": true,
		"database_writes": 0, "broker_commands": 0, "economic_nav_publishable": false,
		"source_hashes":      map[string]string{"cash_nav": fuyingCashNAVHash, "residual": fuyingResidualHash},
		"model_calculations": calculations, "lots": lots, "receipts": receipts, "interest_bridge": bridge,
		"conditional_curves": curves, "scope": "full-term source-date accrual research; receipt releases prior gross accrual; principal and fees not added; estimated interest assumed outside broker assets, unverified; ETF gates unchanged"}
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	return writeJSON(map[string]any{"days": len(bridge.Days), "lots": len(lots), "receipts": len(receipts), "pending_lots": len(bridge.PendingLots), "database_writes": 0, "economic_nav_publishable": false})
}
