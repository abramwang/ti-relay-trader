package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

type feeRulePublication struct {
	PublicationID string           `json:"publication_id"`
	Environment   string           `json:"environment"`
	EffectiveFrom string           `json:"effective_from"`
	ConfirmedBy   string           `json:"confirmed_by"`
	AccountIDs    []string         `json:"account_ids"`
	Evidence      map[string]any   `json:"evidence"`
	Templates     []ledger.FeeRule `json:"templates"`
}

type feeRulePublicationItem struct {
	RuleID    string `json:"rule_id"`
	AccountID string `json:"account_id"`
	Version   int    `json:"version,omitempty"`
	Outcome   string `json:"outcome"`
}

func runFeeRulesPublish(args []string) error {
	flags := flag.NewFlagSet("performance-fee-rules-publish", flag.ContinueOnError)
	configPath := flags.String("config", "", "explicit relay YAML config path")
	input := flags.String("input", "", "approved budget profile JSON")
	expectedEnv := flags.String("expected-env", "", "required target environment")
	persist := flags.Bool("persist", false, "atomically publish approved rules; default is preview")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *input == "" || *expectedEnv == "" || flags.NArg() != 0 {
		return fmt.Errorf("-config, -input and -expected-env are required")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	var publication feeRulePublication
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&publication); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("publication must contain exactly one JSON object")
	}
	rules, err := buildFeeRulePublication(publication, cfg, *expectedEnv, fmt.Sprintf("%x", sha256.Sum256(data)))
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !*persist})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	items, err := publishFeeRules(ctx, tx, rules, *persist)
	if err != nil {
		return err
	}
	if *persist {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return writeJSON(map[string]any{
		"publication_id": publication.PublicationID, "environment": publication.Environment,
		"effective_from": publication.EffectiveFrom, "persist": *persist,
		"authority": "sample_verified_budget", "items": items,
		"warning": "Budget only; actual OC fees take precedence. Broker minimums and rounding remain unverified.",
	})
}

func buildFeeRulePublication(p feeRulePublication, cfg *config.Config, expectedEnv, digest string) ([]ledger.FeeRule, error) {
	if p.Environment != expectedEnv || string(cfg.Service.Environment) != expectedEnv {
		return nil, fmt.Errorf("publication/config/expected environment mismatch")
	}
	date, err := time.ParseInLocation("2006-01-02", p.EffectiveFrom, timeutil.Location())
	if err != nil || p.PublicationID == "" || p.ConfirmedBy == "" || len(p.AccountIDs) == 0 {
		return nil, fmt.Errorf("publication id, confirmation, accounts and canonical effective_from are required")
	}
	approvedOn, _ := p.Evidence["approved_on"].(string)
	approval, approvalErr := time.ParseInLocation("2006-01-02", approvedOn, timeutil.Location())
	if approvalErr != nil || date.Before(approval) {
		return nil, fmt.Errorf("sample budget publication cannot be backdated before approval")
	}
	if p.Evidence["authority"] != "sample_verified_budget" || p.Evidence["charging_basis"] != "order" || p.Evidence["not_broker_settlement_rule"] != true {
		return nil, fmt.Errorf("sample budget authority, order granularity and settlement disclaimer are required")
	}
	if len(p.Templates) != 8 {
		return nil, fmt.Errorf("require eight explicit SH/SZ stock/ETF buy/sell templates")
	}
	scopes := make(map[string]bool)
	for _, rule := range p.Templates {
		if (rule.Market != "SH" && rule.Market != "SZ") || (rule.InstrumentType != "stock" && rule.InstrumentType != "etf") || rule.BusinessType != "S" || (rule.TradeSide != "B" && rule.TradeSide != "S") {
			return nil, fmt.Errorf("unsupported template scope")
		}
		if rule.RuleID != "" || rule.AccountID != "" || rule.Version != 0 || rule.Status != "" || rule.EffectiveFrom != "" || rule.EffectiveTo != "" || rule.RawPayload != nil || rule.CreatedBy != "" || rule.ActivatedBy != "" || !rule.ActivatedAt.IsZero() || !rule.CreatedAt.IsZero() || !rule.UpdatedAt.IsZero() || rule.RepoFeeRate != 0 || rule.EstimatedFrictionRate != 0 {
			return nil, fmt.Errorf("templates cannot override publication identity, audit fields or unrelated fees")
		}
		for _, value := range []float64{rule.CommissionRate, rule.MinimumCommission, rule.StampDutyRate, rule.TransferFeeRate, rule.HandlingFeeRate, rule.OtherRate, rule.FixedFee} {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("fee values must be finite and non-negative")
			}
		}
		scope := strings.Join([]string{rule.Market, rule.InstrumentType, rule.BusinessType, rule.TradeSide}, ":")
		if scopes[scope] {
			return nil, fmt.Errorf("duplicate template scope %s", scope)
		}
		scopes[scope] = true
	}
	seen := make(map[string]bool)
	var rules []ledger.FeeRule
	for _, accountID := range p.AccountIDs {
		eligible := false
		for _, account := range cfg.Accounts {
			if account.AccountID == accountID && account.Enabled && account.BrokerID == "huaxin" {
				eligible = true
			}
		}
		if !eligible || seen[accountID] {
			return nil, fmt.Errorf("account %s must be unique, enabled and assigned to huaxin", accountID)
		}
		seen[accountID] = true
		for _, template := range p.Templates {
			rule := template
			rule.RuleID = strings.Join([]string{p.PublicationID, accountID, rule.Market, rule.InstrumentType, rule.TradeSide}, "-")
			rule.AccountID, rule.Status, rule.EffectiveFrom = accountID, "active", p.EffectiveFrom
			rule.CreatedBy = p.ConfirmedBy
			rule.RawPayload = make(map[string]any, len(p.Evidence)+4)
			for key, value := range p.Evidence {
				rule.RawPayload[key] = value
			}
			rule.RawPayload["publication_id"] = p.PublicationID
			rule.RawPayload["publication_sha256"] = digest
			rule.RawPayload["confirmed_by"] = p.ConfirmedBy
			rule.RawPayload["minimum_commission_verified"] = rule.InstrumentType == "etf"
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

func feeRuleScopesOverlap(a, b ledger.FeeRule) bool {
	if a.AccountID != b.AccountID || (a.EffectiveTo != "" && a.EffectiveTo < b.EffectiveFrom) || (b.EffectiveTo != "" && b.EffectiveTo < a.EffectiveFrom) {
		return false
	}
	for _, pair := range [][2]string{{a.Market, b.Market}, {a.InstrumentType, b.InstrumentType}, {a.BusinessType, b.BusinessType}, {a.TradeSide, b.TradeSide}} {
		x, y := strings.ToLower(pair[0]), strings.ToLower(pair[1])
		if x != "" && x != "*" && y != "" && y != "*" && x != y {
			return false
		}
	}
	return true
}

func publishFeeRules(ctx context.Context, tx *sql.Tx, rules []ledger.FeeRule, persist bool) ([]feeRulePublicationItem, error) {
	if persist {
		if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '5s'; LOCK TABLE performance_fee_rules IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return nil, err
		}
	}
	repo := ledger.NewRepository(tx)
	existingByAccount := make(map[string][]ledger.FeeRule)
	for _, rule := range rules {
		if _, loaded := existingByAccount[rule.AccountID]; loaded {
			continue
		}
		existing, err := repo.ListFeeRules(ctx, ledger.FeeRuleQuery{AccountID: rule.AccountID, Limit: 500, FetchExtra: true})
		if err != nil {
			return nil, err
		}
		if len(existing) > 500 {
			return nil, fmt.Errorf("account %s rule audit is truncated", rule.AccountID)
		}
		existingByAccount[rule.AccountID] = existing
	}
	var items []feeRulePublicationItem
	for _, rule := range rules {
		item := feeRulePublicationItem{RuleID: rule.RuleID, AccountID: rule.AccountID, Outcome: "planned"}
		for _, old := range existingByAccount[rule.AccountID] {
			if old.RuleID == rule.RuleID {
				if !samePublishedFeeRule(old, rule) {
					return nil, fmt.Errorf("rule %s already exists with different content", rule.RuleID)
				}
				item.Outcome, item.Version = "replayed", old.Version
				continue
			}
			if old.Status == "active" && feeRuleScopesOverlap(old, rule) {
				return nil, fmt.Errorf("rule %s overlaps active rule %s; retire or end the old effective period first", rule.RuleID, old.RuleID)
			}
		}
		if persist && item.Outcome != "replayed" {
			created, err := repo.CreateFeeRule(ctx, rule)
			if err != nil {
				return nil, err
			}
			item.Outcome, item.Version = "published", created.Version
		}
		items = append(items, item)
	}
	return items, nil
}

func samePublishedFeeRule(a, b ledger.FeeRule) bool {
	a.Version, b.Version = 0, 0
	a.ActivatedBy, b.ActivatedBy = "", ""
	a.ActivatedAt, b.ActivatedAt = time.Time{}, time.Time{}
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(x) == string(y)
}
