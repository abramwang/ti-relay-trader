package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
)

func feePublicationFixture(t *testing.T) (feeRulePublication, *config.Config) {
	t.Helper()
	data, err := os.ReadFile("../../testdata/performance/huaxin_fee_budget_20261011.json")
	if err != nil {
		t.Fatal(err)
	}
	var p feeRulePublication
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Service.Environment = config.EnvironmentProduction
	cfg.Accounts = nil
	for _, id := range p.AccountIDs {
		cfg.Accounts = append(cfg.Accounts, config.AccountRouteConfig{AccountID: id, Enabled: true, BrokerID: "huaxin"})
	}
	return p, &cfg
}

func TestFeePublicationUsesIsolatedPostgresTransaction(t *testing.T) {
	dsn := os.Getenv("RELAY_LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RELAY_LEDGER_TEST_DATABASE_URL for temporary-table integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Shadow real rules and their sequence, with no account foreign key or persistent writes.
	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE performance_fee_rules (LIKE public.performance_fee_rules INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES) ON COMMIT DROP;
CREATE TEMP SEQUENCE isolated_fee_publication_seq;
ALTER TABLE performance_fee_rules ALTER COLUMN fee_rule_pk SET DEFAULT nextval('pg_temp.isolated_fee_publication_seq');`)
	if err != nil {
		t.Fatal(err)
	}
	p, cfg := feePublicationFixture(t)
	rules, _ := buildFeeRulePublication(p, cfg, "production", "digest")
	items, err := publishFeeRules(ctx, tx, rules, false)
	if err != nil || len(items) != 32 {
		t.Fatalf("preview: items=%d err=%v", len(items), err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM performance_fee_rules").Scan(&count); err != nil || count != 0 {
		t.Fatalf("preview wrote count=%d err=%v", count, err)
	}
	items, err = publishFeeRules(ctx, tx, rules, true)
	if err != nil || len(items) != 32 {
		t.Fatalf("publish: items=%d err=%v", len(items), err)
	}
	for _, item := range items {
		if item.Outcome != "published" || item.Version < 1 || item.Version > 8 {
			t.Fatalf("invalid publication: %#v", item)
		}
	}
	items, err = publishFeeRules(ctx, tx, rules, true)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	for _, item := range items {
		if item.Outcome != "replayed" {
			t.Fatalf("replay inserted: %#v", item)
		}
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM performance_fee_rules").Scan(&count); err != nil || count != 32 {
		t.Fatalf("duplicate rows=%d err=%v", count, err)
	}
	rules[0].CommissionRate = 0.0002
	if _, err := publishFeeRules(ctx, tx, rules, true); err == nil {
		t.Fatal("changed identity must be rejected")
	}
	rules[0].RuleID += "-new"
	if _, err := publishFeeRules(ctx, tx, rules, true); err == nil {
		t.Fatal("overlapping active rule must be rejected")
	}
}

func TestFeePublicationCoverageAndAudit(t *testing.T) {
	p, cfg := feePublicationFixture(t)
	rules, err := buildFeeRulePublication(p, cfg, "production", "digest")
	if err != nil || len(rules) != 32 {
		t.Fatalf("rules=%d err=%v", len(rules), err)
	}
	seen := make(map[string]bool)
	for i, rule := range rules {
		if seen[rule.RuleID] || rule.Status != "active" || rule.EffectiveFrom != "2026-10-11" || rule.RawPayload["authority"] != "sample_verified_budget" || rule.RawPayload["publication_sha256"] != "digest" {
			t.Fatalf("invalid audit/scope: %#v", rule)
		}
		seen[rule.RuleID] = true
		for _, other := range rules[i+1:] {
			if feeRuleScopesOverlap(rule, other) {
				t.Fatalf("publication overlaps: %s / %s", rule.RuleID, other.RuleID)
			}
		}
	}
}

func TestFeePublicationGuards(t *testing.T) {
	for _, name := range []string{"environment", "disabled", "wrong_broker", "duplicate_account", "backdated", "missing_evidence", "duplicate_scope", "unrelated_business", "negative_fee"} {
		t.Run(name, func(t *testing.T) {
			p, cfg := feePublicationFixture(t)
			switch name {
			case "environment":
				cfg.Service.Environment = config.EnvironmentTest
			case "disabled":
				cfg.Accounts[0].Enabled = false
			case "wrong_broker":
				cfg.Accounts[0].BrokerID = "other"
			case "duplicate_account":
				p.AccountIDs = append(p.AccountIDs, p.AccountIDs[0])
			case "backdated":
				p.EffectiveFrom = "2026-10-10"
			case "missing_evidence":
				delete(p.Evidence, "authority")
			case "duplicate_scope":
				p.Templates[1] = p.Templates[0]
			case "unrelated_business":
				p.Templates[0].BusinessType = "E"
			case "negative_fee":
				p.Templates[0].FixedFee = -1
			}
			if _, err := buildFeeRulePublication(p, cfg, "production", "digest"); err == nil {
				t.Fatal("unsafe publication accepted")
			}
		})
	}
}

func TestFeePublicationOverlapAndReplay(t *testing.T) {
	p, cfg := feePublicationFixture(t)
	rules, _ := buildFeeRulePublication(p, cfg, "production", "digest")
	a, b := rules[0], rules[0]
	a.Version, a.ActivatedBy, a.ActivatedAt = 5, "operator", time.Now()
	if !samePublishedFeeRule(a, b) {
		t.Fatal("server activation metadata must not invalidate replay")
	}
	b.CommissionRate += 0.001
	if samePublishedFeeRule(a, b) {
		t.Fatal("changed payload must not replay")
	}
	b = rules[0]
	b.Market = "*"
	if !feeRuleScopesOverlap(a, b) {
		t.Fatal("wildcard must overlap")
	}
	b.EffectiveTo = "2026-10-10"
	if feeRuleScopesOverlap(a, b) {
		t.Fatal("nonoverlapping periods must pass")
	}
	if feeRuleScopesOverlap(a, ledger.FeeRule{AccountID: "other"}) {
		t.Fatal("other account must be isolated")
	}
}
