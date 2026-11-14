package performance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"ti-relay-trader/internal/ledger"
)

func TestResearchCostVersionPostgres(t *testing.T) {
	dsn := os.Getenv("RELAY_RESEARCH_COST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable research cost database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ddl, err := os.ReadFile("../../migrations/postgres/000042_research_cost_versions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE accounts(account_id TEXT PRIMARY KEY); INSERT INTO accounts VALUES ('account'),('other'),('314000045768');`+string(ddl)); err != nil {
		t.Fatal(err)
	}
	repo := ledger.NewRepository(db)
	b := researchCostFixture()
	v, err := SaveResearchCostVersion(ctx, repo, b)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := SaveResearchCostVersion(ctx, repo, b)
	if err != nil || !reflect.DeepEqual(v, replay) {
		t.Fatal("replay", err)
	}
	if _, err := LoadResearchCostVersion(ctx, repo, "other", b.VersionID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("account isolation", err)
	}
	if _, err := LoadResearchCostVersion(ctx, repo, b.AccountID, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("implicit latest", err)
	}
	conflict := researchCostFixture()
	conflict.EvidenceHashes["new-source"] = conflict.EvidenceHashes["synthetic-test"]
	if _, err := SaveResearchCostVersion(ctx, repo, conflict); err == nil {
		t.Fatal("overwrote same version")
	}
	// Two concurrent different payloads with the same identity must have one winner.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, candidate := range []ResearchCostBundle{b, conflict} {
		wg.Add(1)
		go func(c ResearchCostBundle) {
			defer wg.Done()
			c.VersionID = "concurrent"
			_, err := SaveResearchCostVersion(ctx, repo, c)
			results <- err
		}(candidate)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent successes=%d", success)
	}
	// A transaction abort cannot expose a subset of dates or any new version.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.VersionID = "aborted"
	if _, err := SaveResearchCostVersion(ctx, ledger.NewRepository(tx), b); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := LoadResearchCostVersion(ctx, repo, b.AccountID, b.VersionID); !errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		t.Fatal("uncommitted version visible", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResearchCostVersion(ctx, repo, b.AccountID, b.VersionID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("rollback failed", err)
	}
	b.VersionID = "review-2"
	if _, err := SaveResearchCostVersion(ctx, repo, b); err != nil {
		t.Fatal(err)
	}
	// Reconnect and explicitly pin the old version: no deletion or active pointer.
	db2, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	old, err := ledger.NewRepository(db2).GetResearchCostVersion(ctx, "account", "review-1")
	if err != nil || string(old.Payload) != string(v.Payload) || old.PayloadSHA256 != v.PayloadSHA256 {
		t.Fatal("old version readback", err)
	}
	for _, statement := range []string{`UPDATE performance_research_cost_versions SET payload_text='{}'`, `DELETE FROM performance_research_cost_versions`} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Fatal("mutable archive")
		}
	}
	// A privileged or legacy writer can bypass the typed API. Readback must
	// detect incorrect digests and structurally invalid but well-hashed payloads.
	for _, id := range []string{"bad-digest", "bad-schema"} {
		payload := []byte(`{}`)
		h := sha256.Sum256(payload)
		digest := hex.EncodeToString(h[:])
		if id == "bad-digest" {
			digest = v.PayloadSHA256
		}
		_, err := db.ExecContext(ctx, `INSERT INTO performance_research_cost_versions
		(account_id,version_id,anchor_date,date_from,date_to,payload_sha256,payload_text)
		VALUES('account',$1,'2026-10-09','2026-10-12','2026-10-13',$2,$3)`, id, digest, string(payload))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := LoadResearchCostVersion(ctx, repo, "account", id); err == nil {
			t.Fatal("accepted corrupt stored version", id)
		}
	}
	t.Run("frozen 22 day actual chain", func(t *testing.T) {
		if os.Getenv("RELAY_RESEARCH_COST_FROZEN_REPLAY") != "1" {
			t.Skip("local private frozen evidence replay not requested")
		}
		read := func(path, hash string, target any) {
			t.Helper()
			raw, err := os.ReadFile("../../" + path)
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.Sum256(raw)
			if hex.EncodeToString(h[:]) != hash {
				t.Fatal("frozen evidence changed", path)
			}
			if err := json.Unmarshal(raw, target); err != nil {
				t.Fatal(err)
			}
		}
		const sourceHash = "aacd65f5560e0ca503d8410d06497494901b7f0d44e5dd6f5179978218dcf2ad"
		const reviewHash = "56a8d0826101100302a6c3b74463e15ce39cd9d2902071094c886f378bda8f9d"
		var frozen struct {
			Costs []CostLedgerResult `json:"costs"`
		}
		var audit struct {
			Opening []ledger.PositionCostState `json:"stored_opening_anchor"`
			Audits  []ledger.T0Allocation      `json:"active_attribution_audits"`
			Review  CostContinuityReview       `json:"review"`
			Missing []string                   `json:"unpersisted_attribution_link_ids"`
		}
		read("outputs/recovery/fuying13_settlement_20261110/cost_chain_cash_v12.json", sourceHash, &frozen)
		read("outputs/recovery/fuying13_remaining_20261113/cost_continuity.json", reviewHash, &audit)
		if len(audit.Missing) != 0 {
			t.Fatal("missing audits")
		}
		b := ResearchCostBundle{AccountID: "314000045768", VersionID: "fuying13-cost-research-20261114-v1", AnchorDate: "2026-10-09", Opening: audit.Opening, Costs: frozen.Costs, Audits: audit.Audits, EvidenceHashes: map[string]string{"cost_chain": sourceHash, "continuity_review": reviewHash}}
		for _, d := range b.Costs {
			b.Dates = append(b.Dates, d.TradeDate)
		}
		v, err := SaveResearchCostVersion(ctx, repo, b)
		if err != nil {
			t.Fatal(err)
		}
		got, err := LoadResearchCostVersion(ctx, ledger.NewRepository(db2), b.AccountID, b.VersionID)
		if err != nil || !reflect.DeepEqual(got.Review, audit.Review) || !reflect.DeepEqual(got.Costs, b.Costs) || got.Review.Rows != 972 || got.Review.Days != 22 || len(got.Review.RoundingDifferences) != 1 || got.Review.Publishable {
			t.Fatal("frozen chain differs", err)
		}
		if _, err := SaveResearchCostVersion(ctx, repo, b); err != nil {
			t.Fatal("actual replay", err)
		}
		t.Logf("readback: days=%d rows=%d carried=%d movements=%d rounding=%d bytes=%d sha256=%s", got.Review.Days, got.Review.Rows, got.Review.CarriedLots, len(got.Review.Movements), len(got.Review.RoundingDifferences), len(v.Payload), v.PayloadSHA256)
	})
	// All tests work without any official cost, NAV, asset, or cash table.
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&tables); err != nil || tables != 2 {
		t.Fatal("unexpected financial tables", tables, err)
	}
	down, err := os.ReadFile("../../migrations/postgres/000042_research_cost_versions.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(down)); err != nil {
		t.Fatal("down migration", err)
	}
}
