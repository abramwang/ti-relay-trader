package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"ti-relay-trader/internal/ledger"
)

func cyclePublicationFixture(t *testing.T) (CycleReviewSource, ledger.CycleReviewVersion) {
	c, tr := cycleProfitFixture(t)
	p, err := CalculateETFCycleProfits(c, tr)
	if err != nil {
		t.Fatal(err)
	}
	s := CycleReviewSource{AccountID: "a", Cycles: c}
	s.Profit.Profits = p
	v, err := BuildCycleReviewVersion(s, "v1", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return s, v
}
func TestCycleReviewPublication(t *testing.T) {
	_, v := cyclePublicationFixture(t)
	var d CycleReviewDocument
	if err := json.Unmarshal(v.Payload, &d); err != nil {
		t.Fatal(err)
	}
	if d.DateFrom != "2026-10-12" || d.ObservedThrough != "2026-10-13" || len(d.Rows) != 1 || d.Rows[0].Profit.NetCyclePnLCents != 2700 {
		t.Fatal(d)
	}
	for _, kind := range []string{"profit", "account", "final", "nav", "fee", "day", "version", "hash", "duplicate", "status", "flags"} {
		t.Run(kind, func(t *testing.T) {
			var x CycleReviewDocument
			_ = json.Unmarshal(v.Payload, &x)
			switch kind {
			case "profit":
				x.Rows[0].Profit.NetCyclePnLCents++
			case "account":
				x.Rows[0].Profit.Trading.AccountID = "b"
			case "final":
				x.FinalSettlement = true
			case "nav":
				x.Rows[0].Profit.AccountNAVPublishable = true
			case "fee":
				x.Rows[0].Profit.Trading.OrdinaryFeeCents = -1
			case "day":
				x.ObservedThrough = "2026-10-11"
			case "version":
				x.VersionID = " ../v1"
			case "hash":
				x.SourceSHA256 = "a"
			case "duplicate":
				x.Rows = append(x.Rows, x.Rows[0])
			case "status":
				x.Rows[0].Profit.Status = "final"
			case "flags":
				x.Rows[0].Profit.QualityFlags = nil
			}
			if ValidateCycleReview(x) == nil {
				t.Fatal("invalid publication accepted")
			}
		})
	}
	for _, kind := range []string{"calculation", "scope", "publishable"} {
		s, _ := cyclePublicationFixture(t)
		switch kind {
		case "calculation":
			s.Profit.Profits[0].NetCyclePnLCents++
		case "scope":
			s.AccountID = "b"
		case "publishable":
			s.Publishable = true
		}
		if _, err := BuildCycleReviewVersion(s, "v2", strings.Repeat("b", 64)); err == nil {
			t.Fatal(kind)
		}
	}
}
func TestCycleReviewPostgres(t *testing.T) {
	dsn := os.Getenv("RELAY_CYCLE_REVIEW_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable PostgreSQL required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	ddl, err := os.ReadFile("../../migrations/postgres/000043_performance_cycle_reviews.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE accounts(account_id TEXT PRIMARY KEY); INSERT INTO accounts VALUES('a'),('b');`+string(ddl)); err != nil {
		t.Fatal(err)
	}
	_, v := cyclePublicationFixture(t)
	repo := ledger.NewRepository(db)
	if _, err := repo.AppendCycleReviewVersion(ctx, v); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendCycleReviewVersion(ctx, v); err != nil {
		t.Fatal("idempotent replay", err)
	}
	if _, err := repo.GetCycleReviewVersion(ctx, "b", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("account isolation", err)
	}
	if _, err := repo.GetCycleReviewVersion(ctx, "a", "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("explicit missing version fell back")
	}
	s, _ := cyclePublicationFixture(t)
	conflict, err := BuildCycleReviewVersion(s, "v1", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendCycleReviewVersion(ctx, conflict); err == nil {
		t.Fatal("conflict overwrote version")
	}
	v2, err := BuildCycleReviewVersion(s, "v2", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := db.BeginTx(ctx, nil)
	if _, err := ledger.NewRepository(tx).AppendCycleReviewVersion(ctx, v2); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	latest, err := repo.GetCycleReviewVersion(ctx, "a", "")
	if err != nil || latest.VersionID != "v1" {
		t.Fatal("rollback exposed version", err)
	}
	if _, err := repo.AppendCycleReviewVersion(ctx, v2); err != nil {
		t.Fatal(err)
	}
	latest, err = repo.GetCycleReviewVersion(ctx, "a", "")
	if err != nil || latest.VersionID != "v2" {
		t.Fatal("latest", err)
	}
	for _, q := range []string{`UPDATE performance_cycle_reviews SET version_id='v3'`, `DELETE FROM performance_cycle_reviews`} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Fatal("immutable guard failed")
		}
	}
	latest.Payload[1] = 'x'
	if _, err := repo.AppendCycleReviewVersion(ctx, latest); err == nil {
		t.Fatal("corrupt hash accepted")
	}
	daily, err := BuildCycleReviewVersion(dailyCycleFixture(t), "v4", strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AppendCycleReviewVersion(ctx, daily); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.GetCycleReviewVersion(ctx, "a", "v4")
	if err != nil {
		t.Fatal(err)
	}
	var doc CycleReviewDocument
	if err = json.Unmarshal(saved.Payload, &doc); err != nil {
		t.Fatal(err)
	}
	if err = ValidateCycleReview(doc); err != nil || doc.Daily == nil || len(doc.Daily.Days) != 5 {
		t.Fatal("daily round trip", err)
	}
	old, err := repo.GetCycleReviewVersion(ctx, "a", "v1")
	if err != nil || string(old.Payload) != string(v.Payload) {
		t.Fatal("old publication changed", err)
	}
	attribution, err := BuildCycleReviewVersion(attributionFixture(t), "v5", strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AppendCycleReviewVersion(ctx, attribution); err != nil {
		t.Fatal(err)
	}
	saved, err = repo.GetCycleReviewVersion(ctx, "a", "v5")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(saved.Payload, &doc); err != nil {
		t.Fatal(err)
	}
	if err = ValidateCycleReview(doc); err != nil || doc.Attribution == nil || len(doc.Attribution.Days) != 5 {
		t.Fatal("attribution round trip", err)
	}
	old, err = repo.GetCycleReviewVersion(ctx, "a", "v4")
	if err != nil || string(old.Payload) != string(daily.Payload) {
		t.Fatal("daily publication changed", err)
	}
}
