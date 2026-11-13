package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const fuyingSplitFixture = "testdata/fixtures/fuying13_t0_split_buy_review_20261110.json"
const fuyingSplitHash = "fd473ff6b2207e6d14672ec758bcce20837a90f5b344a32bf474d2eaad00d37f"

func loadFuyingSplitAudit(path string) ([]ledger.T0Allocation, error) {
	var links []ledger.T0Allocation
	if err := readPinnedJSON(path, fuyingSplitHash, &links); err != nil {
		return nil, err
	}
	if err := validateFuying13SplitReview(links); err != nil {
		return nil, err
	}
	for i := range links {
		item, err := ledger.NormalizeT0Allocation(links[i])
		if err != nil {
			return nil, err
		}
		links[i] = item
	}
	return links, nil
}

func validateFuyingSplitOrder(link ledger.T0Allocation, order trading.Order) error {
	qty, business, status := link.Quantity, "S", "filled"
	if link.Quantity == 20200 {
		qty, status = 1000000, "cancelled"
	}
	if link.TradeSide == "R" {
		business = "E"
	}
	if order.AccountID != link.AccountID || order.GatewayOrderID != link.GatewayOrderID || order.TradeDate != link.TradeDate ||
		order.Symbol != "159869" || string(order.Exchange) != "SZ" || string(order.TradeSide) != link.TradeSide ||
		string(order.BusinessType) != business || order.CumFilledQty != link.Quantity || order.OrderQty != qty ||
		!order.IsTerminal || string(order.Status) != status || order.LeavesQty != 0 ||
		(order.T0OrderGroupID != "" && order.T0OrderGroupID != link.GroupID) {
		return fmt.Errorf("confirmed split order evidence changed: %s", link.GatewayOrderID)
	}
	return nil
}

func validateFuyingSplitFills(link ledger.T0Allocation, fills []trading.Fill) error {
	if len(fills) >= 500 {
		return errors.New("split fill evidence exceeds bounded review")
	}
	var qty int64
	seen := map[string]bool{}
	for _, fill := range fills {
		if fill.AccountID != link.AccountID || fill.GatewayOrderID != link.GatewayOrderID || fill.TradeDate != link.TradeDate ||
			fill.Symbol != "159869" || string(fill.Exchange) != "SZ" || string(fill.TradeSide) != link.TradeSide ||
			fill.FillID == "" || seen[fill.FillID] || fill.Qty <= 0 || fill.Qty > link.Quantity-qty ||
			(fill.T0OrderGroupID != "" && fill.T0OrderGroupID != link.GroupID) {
			return fmt.Errorf("split fill evidence conflict: %s", link.GatewayOrderID)
		}
		seen[fill.FillID] = true
		qty += fill.Qty
	}
	// A redemption may be represented by transfers rather than ordinary fills.
	if qty != link.Quantity && !(link.TradeSide == "R" && qty == 0) {
		return fmt.Errorf("split fill quantity mismatch: %s", link.GatewayOrderID)
	}
	return nil
}

func fuyingSplitFinancialFingerprints(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	result := map[string]string{}
	for _, table := range []string{"performance_nav_versions", "performance_position_cost_states", "asset_snapshots", "cash_ledger"} {
		var digest string
		// Table names are fixed locally, not command-line input. Preserve duplicate
		// multiplicity while comparing the complete historical rows, not just counts.
		err := tx.QueryRowContext(ctx, `SELECT md5(COALESCE(string_agg(h, '' ORDER BY h), '')) FROM
(SELECT md5(to_jsonb(t)::text) h FROM `+table+` t WHERE account_id=$1 AND trade_date BETWEEN '2026-10-12'::date AND '2026-11-10'::date) s`, fuying13Account).Scan(&digest)
		if err != nil {
			return nil, err
		}
		result[table] = digest
	}
	return result, nil
}

// This command only appends three confirmed attribution links. It cannot publish
// costs or NAV, modify broker assets, or send commands to a gateway.
func runFuyingSplitAudit(args []string) error {
	f := flag.NewFlagSet("performance-fuying13-split-audit-20261113", flag.ContinueOnError)
	configPath := f.String("config", "", "production config")
	output := f.String("output", "", "new private evidence report")
	apply := f.Bool("apply", false, "append the three confirmed allocation links only")
	backup := f.String("backup", "", "fresh custom PostgreSQL backup required for apply")
	backupHash := f.String("backup-sha256", "", "verified backup digest")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *output == "" {
		return errors.New("new output required")
	}
	if _, err := os.Stat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if *apply {
		stat, err := os.Stat(*backup)
		if err != nil || !stat.Mode().IsRegular() || stat.Size() < 1024 || time.Since(stat.ModTime()) > 2*time.Hour || stat.ModTime().After(time.Now().Add(time.Minute)) {
			return errors.New("fresh backup required")
		}
		hash, err := recovery20261027FileHash(*backup)
		if err != nil || len(*backupHash) != 64 || hash != *backupHash {
			return errors.New("backup digest mismatch")
		}
		if err := exec.CommandContext(ctx, "pg_restore", "--list", *backup).Run(); err != nil {
			return errors.New("invalid backup archive")
		}
	}
	links, err := loadFuyingSplitAudit(fuyingSplitFixture)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Service.Environment != config.EnvironmentProduction || cfg.Database.ExpectedName != "relay_trader" {
		return errors.New("reviewed production database required")
	}
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	isolation := sql.LevelRepeatableRead
	if *apply {
		isolation = sql.LevelReadCommitted
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !*apply, Isolation: isolation})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "relay_trader" {
		return errors.New("unexpected database")
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = '30s'"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return err
	}
	if *apply {
		// Match the repository's per-order lock, in fixture order, before reading
		// history so concurrent revisions cannot race the batch preflight.
		for _, link := range links {
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "relay.t0_allocation.v1|"+link.AccountID+"|"+link.TradeDate+"|"+link.GatewayOrderID); err != nil {
				return err
			}
			var pk int64
			if err := tx.QueryRowContext(ctx, `SELECT order_pk FROM orders WHERE account_id=$1 AND trade_date=$2::date AND gateway_order_id=$3 FOR SHARE`, link.AccountID, link.TradeDate, link.GatewayOrderID).Scan(&pk); err != nil {
				return err
			}
		}
	}
	repo := ledger.NewRepository(tx)
	financialBefore, err := fuyingSplitFinancialFingerprints(ctx, tx)
	if err != nil {
		return err
	}
	old, err := repo.ListT0AllocationHistory(ctx, fuying13Account, "2026-10-15")
	if err != nil {
		return err
	}
	merged, err := mergeFuying13SplitReview(old, links)
	if err != nil {
		return err
	}
	var orders []trading.Order
	var fills []trading.Fill
	for _, link := range links {
		order, err := repo.GetOrder(ctx, link.AccountID, link.GatewayOrderID)
		if err != nil {
			return err
		}
		if err := validateFuyingSplitOrder(link, order); err != nil {
			return err
		}
		orders = append(orders, order)
		items, err := repo.ListFills(ctx, trading.FillQuery{AccountID: link.AccountID, GatewayOrderID: link.GatewayOrderID, TradeDate: link.TradeDate, Limit: 500})
		if err != nil {
			return err
		}
		if err := validateFuyingSplitFills(link, items); err != nil {
			return err
		}
		fills = append(fills, items...)
	}
	planned, written := len(merged)-len(old), 0
	if *apply {
		for _, link := range links {
			if _, err := repo.AppendT0Allocation(ctx, link); err != nil {
				return err
			}
		}
		after, err := repo.ListT0AllocationHistory(ctx, fuying13Account, "2026-10-15")
		if err != nil {
			return err
		}
		check, err := mergeFuying13SplitReview(after, links)
		if err != nil || len(check) != len(after) || len(after) != len(merged) {
			return errors.New("allocation readback mismatch")
		}
		written = planned
	}
	financialAfter, err := fuyingSplitFinancialFingerprints(ctx, tx)
	if err != nil || !reflect.DeepEqual(financialBefore, financialAfter) {
		return fmt.Errorf("historical financial rows changed during attribution audit: %v", err)
	}
	phase := "read_only_preflight"
	if *apply {
		phase = "prepared_not_committed"
	}
	if err := writeETFTransferReviewFile(*output, map[string]any{
		"phase": phase, "generated_at": timeutil.Now(), "account_id": fuying13Account, "trade_date": "2026-10-15",
		"fixture_sha256": fuyingSplitHash, "backup_sha256": *backupHash, "apply_requested": *apply,
		"financial_fingerprints": financialAfter,
		"links":                  links, "orders": orders, "fills": fills, "prior_history": old, "planned_new_links": planned, "prepared_new_links": written,
		"identical_links_skipped": 3 - planned, "nav_writes": 0, "cost_writes": 0, "asset_writes": 0, "original_order_writes": 0,
		"broker_commands": 0, "economic_nav_publishable": false,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return writeJSON(map[string]any{"phase": "completed", "apply_requested": *apply, "new_links": written, "planned_new_links": planned,
		"identical_links_skipped": 3 - planned, "economic_nav_publishable": false, "nav_writes": 0, "cost_writes": 0, "asset_writes": 0})
}
