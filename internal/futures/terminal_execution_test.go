package futures

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestOrderServiceTerminalDatabaseAndPolicy(t *testing.T) {
	admin, store, ctx := assessmentDB(t)
	_, market, _ := serviceFixture(t, store, &fakeQueryPublisher{})
	// Only the disposable test DSN is used. The runtime constructor must
	// compare current_database, not just trust a configured name.
	cfg, err := pgx.ParseConfig(os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	name := "execution_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "") + "_test"
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, `DROP DATABASE `+name); err != nil {
			t.Error(err)
		}
	})
	v, _, _, _, _ := executionTradeFixture(t, time.Now())
	r := &TerminalReader{db: db, sources: []*TestStreamSource{nil}, manifest: TerminalManifest{Accounts: []TerminalAccountConfig{{Scope: v.scope()}}}}
	if r.HasOrderPolicy() {
		t.Fatal("default policy grants execution")
	}
	if _, err := r.NewOrderService(ctx, "unused", name, market); err == nil {
		t.Fatal("missing policy accepted")
	}
	r.manifest.Accounts[0].Execution = &TerminalExecutionConfig{SubmitEnabled: false, CommandGroup: "existing-group", AuthorizationExpiresAt: "2026-11-18T15:00:00+08:00"}
	s, err := r.NewOrderService(ctx, "unused", name, market)
	if err != nil || !r.HasOrderPolicy() || s.accounts["300052141"].SubmitEnabled {
		t.Fatal("read-only policy unavailable", err)
	}
	for _, expected := range []string{"", "postgres", "other_test"} {
		if _, err := r.NewOrderService(ctx, "unused", expected, market); err == nil {
			t.Fatal("wrong database identity accepted", expected)
		}
	}
	r.manifest.Accounts[0].Execution.CommandGroup = ""
	if _, err := r.NewOrderService(ctx, "unused", name, market); err == nil {
		t.Fatal("guessed missing group")
	}
	r.manifest.Accounts[0].Execution.CommandGroup = "existing-group"
	r.manifest.Accounts[0].Execution.AuthorizationExpiresAt = "tomorrow"
	if _, err := r.NewOrderService(ctx, "unused", name, market); err == nil {
		t.Fatal("guessed authorization time")
	}
	r.manifest.Accounts[0].Execution.AuthorizationExpiresAt = "2026-11-18T15:00:00+08:00"
	r.manifest.Accounts[0].Scope.Route.Env = "prod"
	if _, err := r.NewOrderService(ctx, "unused", name, market); err == nil {
		t.Fatal("production route accepted")
	}
}
