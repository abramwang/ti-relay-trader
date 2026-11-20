package accountbaseline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/trading"
)

func registrationIntent(t *testing.T, id string) ledger.SecuritiesCommandIntent {
	t.Helper()
	a := archivedCommand(t, id, "order.submit", commandPayload("registered-gw"))
	var w map[string]json.RawMessage
	if err := json.Unmarshal([]byte(a.Archive.Body), &w); err != nil {
		t.Fatal(err)
	}
	w["idempotency_key"] = json.RawMessage(`"shared-idempotency"`)
	body, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.SecuritiesCommandIntent{Environment: "test", AccountID: "00030484", StreamKey: a.Archive.Stream, Action: "order.submit", MessageID: id, IdempotencyKey: "shared-idempotency", Body: " \n" + string(body) + "\n"}
}

func TestCurrentAccountBaselineRegisteredCommandLifecycle(t *testing.T) {
	db, ctx := sourceSchemaDatabase(t, true)
	journal, err := ledger.NewRepository(db).SecuritiesCommandStore("postgres")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var created atomic.Int32
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		in := registrationIntent(t, fmt.Sprintf("msg-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, fresh, e := journal.Register(ctx, in)
			if fresh {
				created.Add(1)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if created.Load() != 1 {
		t.Fatal("multiple dispatch owners", created.Load())
	}
	r, err := journal.Get(ctx, "00030484", "order.submit", "shared-idempotency")
	if err != nil || r.State != "registered_not_dispatched" {
		t.Fatal(r, err)
	}
	changed := r.SecuritiesCommandIntent
	changed.Body = strings.Replace(changed.Body, `"qty":100`, `"qty":200`, 1)
	if _, _, err = journal.Register(ctx, changed); !errors.Is(err, ledger.ErrSecuritiesCommandConflict) {
		t.Fatal("changed payload replayed", err)
	}
	store, _ := ledger.NewCurrentBaselineStore(db)
	scope := fixtureSources(t, 0).Scope
	ids := map[string]string{"asset": "q1", "positions": "q2", "orders": "q3", "fills": "q4"}
	src, err := store.ReadSources(ctx, "test", scope, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.TradeCommands.Registrations) != 1 || len(src.TradeCommands.Archives) != 0 {
		t.Fatal("unarchived command disappeared")
	}
	c, err := Assemble(src, "registered", time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.PublishCandidate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if m.Resources["commands"].TotalRows != 1 {
		t.Fatal("missing registered obligation")
	}
	claims := atomic.Int32{}
	errs = make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := journal.Claim(ctx, r)
			if e == nil {
				claims.Add(1)
			} else if !errors.Is(e, ledger.ErrSecuritiesCommandUncertain) {
				errs <- e
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if claims.Load() != 1 {
		t.Fatal("duplicate claims", claims.Load())
	}
	stale := c
	stale.CollectionID = "after-dispatch"
	if _, err = store.PublishCandidate(ctx, stale); !errors.Is(err, ledger.ErrCurrentBaselineStale) {
		t.Fatal("dispatch not observed by revision", err)
	}
	r, err = journal.Get(ctx, r.AccountID, r.Action, r.IdempotencyKey)
	if err != nil || r.State != "dispatch_unknown" {
		t.Fatal(r, err)
	}
	// An archive write failure must roll back its publication acknowledgement.
	if _, err = db.ExecContext(ctx, `CREATE FUNCTION fail_command_archive() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic archive failure'; END $$;
CREATE TRIGGER fail_command_archive BEFORE INSERT ON raw_stream_messages FOR EACH ROW EXECUTE FUNCTION fail_command_archive()`); err != nil {
		t.Fatal(err)
	}
	if err = journal.Complete(ctx, r, "800-0"); err == nil {
		t.Fatal("archive failure accepted")
	}
	unknown, err := journal.Get(ctx, r.AccountID, r.Action, r.IdempotencyKey)
	if err != nil || unknown.State != "dispatch_unknown" {
		t.Fatal("partial completion", unknown, err)
	}
	if _, err = db.ExecContext(ctx, `DROP TRIGGER fail_command_archive ON raw_stream_messages`); err != nil {
		t.Fatal(err)
	}
	if err = journal.Complete(ctx, r, "800-0"); err != nil {
		t.Fatal(err)
	}
	if err = journal.Complete(ctx, r, "800-0"); err != nil {
		t.Fatal("same acknowledgement not idempotent", err)
	}
	if err = journal.Complete(ctx, r, "801-0"); !errors.Is(err, ledger.ErrSecuritiesCommandConflict) {
		t.Fatal("different publication accepted", err)
	}
	var original string
	if err = db.QueryRowContext(ctx, `SELECT body_text FROM raw_stream_messages WHERE stream_key=$1 AND stream_id='800-0'`, r.StreamKey).Scan(&original); err != nil || original != r.Body {
		t.Fatal("original bytes changed", err)
	}
	published, err := journal.Get(ctx, r.AccountID, r.Action, r.IdempotencyKey)
	if err != nil || published.State != "published" {
		t.Fatal(published, err)
	}
	page, err := store.Page(ctx, scope, m.BaselineID, "commands", "", 10)
	if err != nil || !strings.Contains(string(page.Items[0]), "registered_not_dispatched") {
		t.Fatal("historical publication mutated", err)
	}
	for _, table := range []string{"securities_command_intents", "securities_command_dispatches", "securities_command_publications"} {
		if _, err = db.ExecContext(ctx, "DELETE FROM "+table); err == nil {
			t.Fatal("evidence deleted", table)
		}
		if _, err = db.ExecContext(ctx, "TRUNCATE "+table+" CASCADE"); err == nil {
			t.Fatal("evidence truncated", table)
		}
	}
	down, err := os.ReadFile("../../migrations/postgres/000058_securities_command_registration.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(down)); err == nil {
		t.Fatal("destructive downgrade allowed")
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE securities_command_dispatches DISABLE TRIGGER account_baseline_revision_watch`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadSources(ctx, "test", scope, ids); !errors.Is(err, ledger.ErrCurrentBaselineTracking) {
		t.Fatal("disabled tracking ignored", err)
	}
	if _, err = journal.Get(ctx, r.AccountID, r.Action, r.IdempotencyKey); !errors.Is(err, ledger.ErrCurrentBaselineTracking) {
		t.Fatal("sender ignored disabled protection", err)
	}
}

type registrationPublisher struct {
	calls       int
	legacyCalls int
	journal     *ledger.SecuritiesCommandStore
	fail        bool
	body        string
	action      string
}

func (p *registrationPublisher) PublishCommand(context.Context, string, redisstream.CommandEnvelope) (redisstream.CommandPublishResult, error) {
	p.legacyCalls++
	return redisstream.CommandPublishResult{}, errors.New("legacy publisher must not be used")
}
func (p *registrationPublisher) PublishCommandBodyOnce(ctx context.Context, stream string, body []byte) (redisstream.CommandPublishResult, error) {
	p.calls++
	p.body = string(body)
	var w redisstream.CommandEnvelope
	if err := json.Unmarshal(body, &w); err != nil {
		return redisstream.CommandPublishResult{}, err
	}
	p.action = w.Action
	r, err := p.journal.Get(ctx, "00030484", w.Action, w.IdempotencyKey)
	if err != nil || r.State != "dispatch_unknown" || r.Body != string(body) {
		return redisstream.CommandPublishResult{}, errors.New("publish preceded durable claim")
	}
	if p.fail {
		return redisstream.CommandPublishResult{}, errors.New("synthetic lost Redis response")
	}
	return redisstream.CommandPublishResult{StreamKey: stream, StreamID: fmt.Sprintf("%d-0", 900+p.calls), BodyBytes: len(body)}, nil
}

func registeredServiceConfig() config.Config {
	return config.Config{Service: config.ServiceConfig{Environment: config.EnvironmentTest, SecuritiesCommandRegistration: true}, Database: config.DatabaseConfig{ExpectedName: "isolated_test"}, Redis: config.RedisConfig{Env: "test"}, Accounts: []config.AccountRouteConfig{{AccountID: "00030484", BrokerID: "huaxin", GatewayID: "gw-test", StreamPrefix: "relay:test:v1:huaxin:gw-test", Enabled: true, TradingEnabled: true, Simulated: true}}}
}

func registeredSubmit(id string) trading.SubmitOrderRequest {
	return trading.SubmitOrderRequest{AccountID: "00030484", GatewayOrderID: id, ClientOrderID: id, IdempotencyKey: "idem-" + id, Symbol: "000001", Exchange: trading.ExchangeSZ, TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, Qty: 100, Price: 10.125, TradeDate: "2026-11-19"}
}

func TestCurrentAccountBaselineRegisteredOrderflow(t *testing.T) {
	for _, action := range []string{"submit", "batch", "cancel"} {
		for _, lost := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/lost=%t", action, lost), func(t *testing.T) {
				db, ctx := sourceSchemaDatabase(t, true)
				repo := ledger.NewRepository(db)
				journal, err := repo.SecuritiesCommandStore("postgres")
				if err != nil {
					t.Fatal(err)
				}
				p := &registrationPublisher{journal: journal, fail: lost}
				cfg := registeredServiceConfig()
				if action == "cancel" {
					if err = repo.UpsertAccount(ctx, trading.Account{AccountID: "00030484", BrokerID: "huaxin", GatewayID: "gw-test", Enabled: true}); err != nil {
						t.Fatal(err)
					}
					if err = repo.CreateOrder(ctx, trading.Order{AccountID: "00030484", GatewayOrderID: "target", Symbol: "000001", Exchange: trading.ExchangeSZ, TradeSide: trading.TradeSideBuy, BusinessType: trading.BusinessTypeStock, OrderQty: 100, LeavesQty: 100, LimitPrice: 10, Status: trading.OrderStatusWorking, TradeDate: "2026-11-19"}); err != nil {
						t.Fatal(err)
					}
				}
				makeService := func() *orderflow.Service {
					s, e := orderflow.New(orderflow.Options{Config: cfg, Ledger: repo, Publisher: p, CommandJournal: journal})
					if e != nil {
						t.Fatal(e)
					}
					return s
				}
				call := func(s *orderflow.Service) (string, bool, error) {
					switch action {
					case "submit":
						r, e := s.SubmitOrder(ctx, registeredSubmit("single"), orderflow.SubmitOptions{})
						return r.MessageID, r.Replayed, e
					case "batch":
						r, e := s.BatchSubmitOrders(ctx, trading.BatchSubmitOrderRequest{AccountID: "00030484", IdempotencyKey: "batch-idem", Orders: []trading.SubmitOrderRequest{registeredSubmit("child-a"), registeredSubmit("child-b")}}, orderflow.BatchSubmitOptions{})
						return r.MessageID, r.Replayed, e
					default:
						r, e := s.CancelOrder(ctx, trading.CancelOrderRequest{AccountID: "00030484", GatewayOrderID: "target", CancelID: "cancel-one", IdempotencyKey: "cancel-idem"}, orderflow.CancelOptions{})
						return r.MessageID, r.Replayed, e
					}
				}
				id, replayed, err := call(makeService())
				if replayed || (lost && !errors.Is(err, ledger.ErrSecuritiesCommandUncertain)) || (!lost && err != nil) {
					t.Fatal(id, replayed, err)
				}
				again, replayed, err := call(makeService())
				if lost {
					if !errors.Is(err, ledger.ErrSecuritiesCommandUncertain) {
						t.Fatal("unknown retried", err)
					}
				} else if err != nil || !replayed || again != id {
					t.Fatal("original receipt lost", again, replayed, err)
				}
				if p.calls != 1 {
					t.Fatal("duplicate network sends", p.calls)
				}
				switch action {
				case "submit":
					changed := registeredSubmit("single")
					changed.Price++
					_, err = makeService().SubmitOrder(ctx, changed, orderflow.SubmitOptions{})
				case "batch":
					_, err = makeService().BatchSubmitOrders(ctx, trading.BatchSubmitOrderRequest{AccountID: "00030484", IdempotencyKey: "batch-idem", Orders: []trading.SubmitOrderRequest{registeredSubmit("child-a")}}, orderflow.BatchSubmitOptions{})
				default:
					_, err = makeService().CancelOrder(ctx, trading.CancelOrderRequest{AccountID: "00030484", GatewayOrderID: "target", CancelID: "changed-cancel", IdempotencyKey: "cancel-idem"}, orderflow.CancelOptions{})
				}
				if !errors.Is(err, orderflow.ErrIdempotencyConflict) || p.calls != 1 {
					t.Fatal("changed intent replayed or published", err, p.calls)
				}
				cfg.Service.SecuritiesCommandRegistration = false
				legacy, err := orderflow.New(orderflow.Options{Config: cfg, Ledger: repo, Publisher: p})
				if err != nil {
					t.Fatal(err)
				}
				_, err = legacy.SubmitOrder(ctx, registeredSubmit("disabled-mode"), orderflow.SubmitOptions{})
				if !errors.Is(err, ledger.ErrSecuritiesCommandUncertain) || p.legacyCalls != 0 {
					t.Fatal("registered account fell back to legacy publication", err, p.legacyCalls)
				}
				var count int
				if err = db.QueryRowContext(ctx, `SELECT count(*) FROM securities_command_dispatches`).Scan(&count); err != nil || count != 1 {
					t.Fatal(count, err)
				}
				if action == "cancel" {
					o, e := repo.GetOrder(ctx, "00030484", "target")
					if e != nil || o.Status != trading.OrderStatusWorking {
						t.Fatal("cancel publication changed order", o, e)
					}
				}
			})
		}
	}
}

func TestCurrentAccountBaselineRegisteredNoSendWithoutSchema(t *testing.T) {
	db, ctx := sourceSchemaDatabase(t, true)
	repo := ledger.NewRepository(db)
	journal, err := repo.SecuritiesCommandStore("postgres")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/postgres/000058_securities_command_registration.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	p := &registrationPublisher{journal: journal}
	s, err := orderflow.New(orderflow.Options{Config: registeredServiceConfig(), Ledger: repo, Publisher: p, CommandJournal: journal})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitOrder(ctx, registeredSubmit("missing-schema"), orderflow.SubmitOptions{})
	if !errors.Is(err, ledger.ErrCurrentBaselineTracking) || p.calls != 0 || p.legacyCalls != 0 {
		t.Fatal("missing schema allowed network publication", err, p.calls, p.legacyCalls)
	}
}
