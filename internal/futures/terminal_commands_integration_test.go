package futures

import (
	"errors"
	"fmt"
	"testing"
)

func TestCommandHistoryReadOnlyPaginationAndRestart(t *testing.T) {
	db, s, ctx := assessmentDB(t)
	v := intentVectors(t)["submit"]
	reader := &TerminalReader{db: db, manifest: TerminalManifest{Accounts: []TerminalAccountConfig{{Scope: v.scope()}}}}
	var ids []int64
	register := func(label string, vector intentVector) CommandRegistration {
		vector.Raw = string(editBody(t, []byte(vector.Raw), func(m map[string]any) {
			m["message_id"], m["idempotency_key"] = label, label
			p := m["payload"].(map[string]any)
			p["gateway_order_id"], p["client_order_id"] = label+"-order", label+"-client"
		}))
		return registerVector(t, s, ctx, vector, registrationEvidence(t, vector), "registered")
	}
	for i := 0; i < 5; i++ {
		ids = append(ids, register(fmt.Sprintf("history-%d", i), v).CommandID)
	}
	// Query intentions and another configured route must not leak into the list.
	q := intentVectors(t)["account.positions.query"]
	registerVector(t, s, ctx, q, registrationEvidence(t, q), "registered")
	foreign := v
	foreign.Scope = make(map[string]string, len(v.Scope))
	for k, value := range v.Scope {
		foreign.Scope[k] = value
	}
	foreign.Scope["gateway_id"] = "another-history-gateway"
	register("foreign", foreign)
	auditCount := storedCount(t, db, "futures_command_audit")
	page, err := reader.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 2})
	if err != nil || page.Route != v.scope().Route || len(page.Items) != 2 || page.Items[0].CommandID != ids[4] || page.NextBeforeID != ids[3] {
		t.Fatal(page, err)
	}
	register("later-insert", v)
	// A new reader (or another browser) resumes using the original cursor.
	fresh := &TerminalReader{db: db, manifest: reader.manifest}
	second, err := fresh.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 2, BeforeID: page.NextBeforeID})
	if err != nil || len(second.Items) != 2 || second.Items[0].CommandID != ids[2] || second.NextBeforeID != ids[1] {
		t.Fatal(second, err)
	}
	last, err := fresh.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 2, BeforeID: second.NextBeforeID})
	if err != nil || len(last.Items) != 1 || last.Items[0].CommandID != ids[0] || last.NextBeforeID != 0 {
		t.Fatal(last, err)
	}
	for _, item := range page.Items {
		if item.Publication.State != "registered" || item.Publication.Published != nil || item.RetrySafe || item.BrokerStateKnown || item.Publication.Replayed {
			t.Fatal(item)
		}
	}
	if storedCount(t, db, "futures_command_audit") != auditCount+1 || storedCount(t, db, "futures_test_trade_publications") != 0 {
		t.Fatal("history wrote execution state")
	}
	if _, err = reader.Commands(ctx, "999999999", CommandHistoryOptions{Limit: 2}); !errors.Is(err, ErrOrderRequestMissing) {
		t.Fatal(err)
	}
	if _, err = reader.Commands(ctx, "not-an-account", CommandHistoryOptions{Limit: 2}); !errors.Is(err, ErrCommandHistoryRequest) {
		t.Fatal(err)
	}
	if _, err = reader.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 101}); !errors.Is(err, ErrCommandHistoryRequest) {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE futures_commands SET body_sha256='tampered' WHERE command_id=$1`, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 100}); err == nil {
		t.Fatal("tampered intent accepted")
	}
}

func TestCommandHistoryPublicationStates(t *testing.T) {
	for _, state := range []string{"published", "attempted", "not_published"} {
		t.Run(state, func(t *testing.T) {
			db, s, ctx := assessmentDB(t)
			v, e, target, a := assessmentFixture(t)
			reg := registerVector(t, s, ctx, v, e, "registered")
			transport := &fakeQueryPublisher{appendError: state == "attempted", rejected: state == "not_published"}
			_, err := s.publishTestTrade(ctx, v.scope(), reg.CommandID, reg.Original.MessageID, target, a, transport)
			if (err != nil) != (state != "published") {
				t.Fatal(err)
			}
			reader := &TerminalReader{db: db, manifest: TerminalManifest{Accounts: []TerminalAccountConfig{{Scope: v.scope()}}}}
			for i := 0; i < 2; i++ {
				page, err := reader.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 50})
				if err != nil || len(page.Items) != 1 {
					t.Fatal(page, err)
				}
				item := page.Items[0]
				if item.Publication.State != state || item.RetrySafe || item.BrokerStateKnown || item.Publication.BrokerAccepted {
					t.Fatal(item)
				}
				if state == "attempted" {
					if item.Publication.Published != nil {
						t.Fatal(item)
					}
				} else if item.Publication.Published == nil || *item.Publication.Published != (state == "published") {
					t.Fatal(item)
				}
			}
			if transport.appends.Load() != 1 {
				t.Fatal("history repeated publication")
			}
		})
	}
}

func TestCommandHistoryCancelAndEmpty(t *testing.T) {
	db, s, ctx := assessmentDB(t)
	v := intentVectors(t)["cancel_target_day"]
	reader := &TerminalReader{db: db, manifest: TerminalManifest{Accounts: []TerminalAccountConfig{{Scope: v.scope()}}}}
	page, err := reader.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 2})
	if err != nil || page.Route != v.scope().Route || page.Items == nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	r := registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	page, err = reader.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 2})
	if err != nil || len(page.Items) != 1 || page.Items[0].Cancel == nil || page.Items[0].Cancel.CancelID != r.Original.Cancel.CancelID || page.Items[0].Action != "order.cancel" {
		t.Fatal(page, err)
	}
}

func TestCommandHistoryAliasesDoNotDuplicateIntent(t *testing.T) {
	db, s, ctx := assessmentDB(t)
	vs := intentVectors(t)
	v := vs["submit"]
	original := registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	registerVector(t, s, ctx, vs["same_intent_new_transport"], RegistrationEvidence{}, "replayed")
	registerVector(t, s, ctx, v, RegistrationEvidence{}, "replayed")
	reader := &TerminalReader{db: db, manifest: TerminalManifest{Accounts: []TerminalAccountConfig{{Scope: v.scope()}}}}
	page, err := reader.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 100})
	if err != nil || len(page.Items) != 1 || page.Items[0].CommandID != original.CommandID || page.Items[0].MessageID != original.Original.MessageID {
		t.Fatal(page, err)
	}
	if len(page.Items[0].Orders) != 1 || page.Items[0].Orders[0] != original.Original.Orders[0] {
		t.Fatal("original intent not retained", page)
	}
}

func TestCommandHistoryPreservesBatchOrder(t *testing.T) {
	db, s, ctx := assessmentDB(t)
	v := intentVectors(t)["ordered_batch"]
	original := registerVector(t, s, ctx, v, registrationEvidence(t, v), "registered")
	reader := &TerminalReader{db: db, manifest: TerminalManifest{Accounts: []TerminalAccountConfig{{Scope: v.scope()}}}}
	page, err := reader.Commands(ctx, v.scope().Route.AccountID, CommandHistoryOptions{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Action != "order.batch.submit" || len(page.Items[0].Orders) != len(original.Original.Orders) {
		t.Fatal(page, err)
	}
	for i, order := range page.Items[0].Orders {
		if order != original.Original.Orders[i] {
			t.Fatal("batch intent order or decimal changed", i, order)
		}
	}
}
