package futures

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withReadOnlyReconciliation(t *testing.T, raw []byte, s CommandScope) []byte {
	t.Helper()
	return editBody(t, raw, func(m map[string]any) {
		m["adapter_context"].(map[string]any)["readonly_reconciliation"] = ReadOnlyReconciliation{
			Policy: "readonly_query_evidence.v1", SourceManifestSHA256: strings.Repeat("a", 64), QueryEvidenceGateClear: true,
			SourceScope: ReadOnlySourceScope{s.Route.Env, s.Route.BrokerID, s.Route.AccountID, "previous-fixture-gateway", s.NativeBrokerID, s.InvestorID, s.UserID},
			Legacy:      ReadOnlyLegacyCounts{Retained: 50, Pending: 32, AuditOnly: 18, Waiting: 32},
			Current:     ReadOnlyCurrentCounts{Retained: 3, Pending: 3, PreExchange: 1, Waiting: 3},
		}
	})
}

func TestReadOnlyReconciliationStrict(t *testing.T) {
	g, raw, at := readOnlyGuard(t)
	raw = withReadOnlyReconciliation(t, raw, g.scope)
	h, err := DecodeHeartbeat(raw, g.scope.Route)
	if err != nil || h.ReadOnly.Reconciliation.Legacy.Pending != 32 || !bytes.Equal(h.Raw, raw) {
		t.Fatal(err)
	}
	if _, ready := h.queryCollection(); !ready {
		t.Fatal("waiting callbacks deadlocked query")
	}
	reader, req := inspectFixture(t, g.scope.Route, raw)
	out, err := InspectTestHeartbeats(inspectContext(t), reader, req)
	if err != nil || out.BusinessAuthorized || out.Records[0].Advertised.ReadOnlyReconciliation == nil {
		t.Fatal("missing diagnostic", err)
	}
	if err := g.observeAt(guardSource(g, "1-0"), raw, at); err != nil {
		t.Fatal(err)
	}

	for name, change := range map[string]func(map[string]any){
		"null":            func(m map[string]any) { m["current"] = nil },
		"missing":         func(m map[string]any) { delete(m, "legacy") },
		"policy":          func(m map[string]any) { m["policy"] = "local_assessment_only.v1" },
		"hash":            func(m map[string]any) { m["source_manifest_sha256"] = "invalid" },
		"events":          func(m map[string]any) { m["external_events_enabled"] = true },
		"boolean_count":   func(m map[string]any) { m["current"].(map[string]any)["pending_count"] = true },
		"null_count":      func(m map[string]any) { m["current"].(map[string]any)["pending_count"] = nil },
		"string_count":    func(m map[string]any) { m["current"].(map[string]any)["pending_count"] = "3" },
		"fraction":        func(m map[string]any) { m["current"].(map[string]any)["pending_count"] = 3.5 },
		"overflow":        func(m map[string]any) { m["legacy"].(map[string]any)["pending_count"] = 10001 },
		"sum":             func(m map[string]any) { m["current"].(map[string]any)["waiting_pending_count"] = 2 },
		"legacy_sum":      func(m map[string]any) { m["legacy"].(map[string]any)["linked_pending_count"] = 1 },
		"blocked":         func(m map[string]any) { m["current"].(map[string]any)["blocking_error_count"] = 1 },
		"legacy_conflict": func(m map[string]any) { m["legacy"].(map[string]any)["blocking_conflict"] = true },
		"gate":            func(m map[string]any) { m["query_evidence_gate_clear"] = false },
		"overlap_count":   func(m map[string]any) { m["current"].(map[string]any)["pre_exchange_count"] = 4 },
		"foreign_account": func(m map[string]any) { m["source_scope"].(map[string]any)["account_id"] = "990000000019" },
		"prod":            func(m map[string]any) { m["source_scope"].(map[string]any)["env"] = "prod" },
		"same_gateway":    func(m map[string]any) { m["source_scope"].(map[string]any)["gateway_id"] = g.scope.Route.GatewayID },
		"extra":           func(m map[string]any) { m["business_processed"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			bad := editBody(t, raw, func(m map[string]any) {
				change(m["adapter_context"].(map[string]any)["readonly_reconciliation"].(map[string]any))
			})
			if _, err := DecodeHeartbeat(bad, g.scope.Route); err == nil {
				t.Fatal("invalid reconciliation accepted")
			}
		})
	}
}

func TestReadOnlyReconciliationBindingPinned(t *testing.T) {
	for _, change := range []string{"manifest", "scope", "counts", "removed", "added", "native"} {
		t.Run(change, func(t *testing.T) {
			g, raw, at := readOnlyGuard(t)
			with := withReadOnlyReconciliation(t, raw, g.scope)
			first := with
			if change == "added" {
				first = raw
			}
			if err := g.observeAt(guardSource(g, "1-0"), first, at); err != nil {
				t.Fatal(err)
			}
			next := editBody(t, with, func(m map[string]any) {
				m["message_id"], m["produced_at"] = "next", at.Add(time.Second).Format(time.RFC3339Nano)
				a := m["adapter_context"].(map[string]any)
				r := a["readonly_reconciliation"].(map[string]any)
				switch change {
				case "manifest":
					r["source_manifest_sha256"] = strings.Repeat("b", 64)
				case "scope":
					r["source_scope"].(map[string]any)["gateway_id"] = "another-old-gateway"
				case "counts":
					r["legacy"].(map[string]any)["audit_only_count"], r["legacy"].(map[string]any)["retained_count"] = 19, 51
				case "removed":
					delete(a, "readonly_reconciliation")
				case "native":
					r["source_scope"].(map[string]any)["user_id"] = "foreign"
				}
			})
			if err := g.observeAt(guardSource(g, "2-0"), next, at.Add(time.Second)); err == nil {
				t.Fatal("binding changed")
			}
		})
	}
}

// Original OC materials stay private and byte-for-byte unchanged. These are
// fake-SPI outputs, not new live broker evidence or permission to publish.
func ocReadOnlyReconciliationBytes(t *testing.T) []byte {
	t.Helper()
	dir := os.Getenv("RELAY_OC_CTP_READONLY_RUNTIME_REVIEW_DIR")
	if dir == "" {
		t.Skip("explicit private OC review directory required")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "runtime-reconciliation.v1.json"))
	if err != nil || commandDigest(raw) != "70877acf508a300730a22ce1c87ef6076fa5debe24174add3eb9f72893f2884b" {
		t.Fatal("OC original digest", err)
	}
	return raw
}

func TestOCReadOnlyReconciliationDelivery(t *testing.T) {
	raw := ocReadOnlyReconciliationBytes(t)
	var doc struct {
		Synthetic bool `json:"synthetic_only"`
		SDK       bool `json:"real_sdk_calls"`
		Redis     bool `json:"redis_connected"`
		Scenarios []struct {
			Name       string            `json:"scenario"`
			Heartbeats []json.RawMessage `json:"heartbeats"`
			Blocked    []json.RawMessage `json:"blocked_heartbeats"`
			Outputs    []ocRuntimeOutput `json:"outputs"`
			Rejected   []struct {
				Body json.RawMessage `json:"body"`
			} `json:"rejected_heartbeat_examples"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || !doc.Synthetic || doc.SDK || doc.Redis || len(doc.Scenarios) != 21 {
		t.Fatal("evidence boundary", err)
	}
	count, badCount, pages := 0, 0, 0
	for _, c := range doc.Scenarios {
		for _, raw := range append(c.Heartbeats, c.Blocked...) {
			h, err := DecodeHeartbeat(raw, readOnlyRoute())
			if err != nil || h.ReadOnly == nil || h.AcceptingTradeCommands || h.AcceptingCancelCommands || h.SettlementConfirmed || !bytes.Equal(raw, h.Raw) {
				t.Fatal(c.Name, err)
			}
			if strings.HasPrefix(c.Name, "readonly_") && h.ReadOnly.Reconciliation == nil {
				t.Fatal("new profile missing", c.Name)
			}
			count++
		}
		for _, bad := range c.Rejected {
			if _, err := DecodeHeartbeat(bad.Body, readOnlyRoute()); err == nil {
				t.Fatal("OC negative accepted", c.Name)
			}
			badCount++
		}
		for _, out := range c.Outputs {
			var obj rawObject
			if err := json.Unmarshal([]byte(out.Body), &obj); err != nil || textField(obj, "message_type") != "reply" || out.Stream != queryReplyStream(readOnlyRoute()) {
				t.Fatal("readonly emitted non-reply")
			}
			var sample struct {
				Payload struct {
					TradeDate string `json:"trade_date"`
				} `json:"payload"`
				Adapter struct {
					Evidence struct {
						Ticket     string `json:"ticket"`
						Session    string `json:"counter_session_id"`
						Generation *int64 `json:"connection_generation"`
						Request    *int64 `json:"sdk_request_id"`
					} `json:"query_evidence"`
				} `json:"adapter_context"`
			}
			if err := json.Unmarshal([]byte(out.Body), &sample); err != nil {
				t.Fatal(err)
			}
			// Schema-only replay of pinned synthetic output; not live request attestation.
			qc := QueryContext{Route: readOnlyRoute(), Action: textField(obj, "action"), OriginMessageID: textField(obj, "origin_message_id"), TradeDate: sample.Payload.TradeDate,
				Ticket: sample.Adapter.Evidence.Ticket, CounterSessionID: sample.Adapter.Evidence.Session, ConnectionGeneration: sample.Adapter.Evidence.Generation, SDKRequestID: sample.Adapter.Evidence.Request,
				NativeBrokerID: "4900", NativeInvestorID: "offlineinv", NativeUserID: "offlineuser"}
			batch, err := NewQueryBatch(qc, 0, time.Minute, DefaultBatchLimits())
			if err != nil {
				t.Fatal(c.Name, err)
			}
			if _, err := batch.Accept([]byte(out.Body), 0); err != nil {
				t.Fatal(c.Name, err)
			}
			want := BatchReady
			if textField(obj, "status") == "failed" {
				want = BatchFailed
			}
			assertState(t, batch, want)
			pages++
		}
	}
	if count != 58 || badCount != 9 || pages != 13 {
		t.Fatal("coverage", count, badCount, pages)
	}
}

func TestOCReadOnlyReconciliationPostgres(t *testing.T) {
	raw := ocReadOnlyReconciliationBytes(t)
	var doc struct {
		Scenarios []readOnlyScenario `json:"scenarios"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var outputs []ocRuntimeOutput
	for _, s := range doc.Scenarios {
		if s.Scenario == "readonly_reconcile" {
			outputs = s.Outputs
		}
	}
	if len(outputs) != 2 {
		t.Fatal("expected nonempty order and fill pages")
	}
	db, q, ctx := queryTestDB(t)
	gen := int64(1)
	for i, out := range outputs {
		kind := []string{"order", "fill"}[i]
		sdk := int64(3 + i)
		// Fixed synthetic scenario identities, never register live evidence from reply fields.
		c := QueryContext{Route: readOnlyRoute(), Action: kind + ".list.query", OriginMessageID: "msg-reconcile-" + kind,
			Ticket: fmt.Sprint(i + 1), CounterSessionID: "ctpproc-bc40dc7467b7721c675839cf7b980440", ConnectionGeneration: &gen,
			SDKRequestID: &sdk, TradeDate: "20261117", NativeBrokerID: "4900", NativeInvestorID: "offlineinv", NativeUserID: "offlineuser"}
		registerQuery(t, q, ctx, kind, c)
		r, err := q.Ingest(ctx, kind, QuerySource{out.Stream, out.StreamID}, []byte(out.Body), 0)
		if err != nil || r.State != BatchReady {
			t.Fatal(kind, r, err)
		}
		applied, err := q.ApplyTradeQuery(ctx, kind)
		if err != nil || !applied.Applied || applied.RowCount != 1 {
			t.Fatal(kind, applied, err)
		}
		if _, err := q.Ingest(ctx, kind, QuerySource{out.Stream, out.StreamID}, []byte(out.Body), 0); err != nil {
			t.Fatal("duplicate page", err)
		}
		q, _ = NewQueryStore(db)
		if _, err := q.Recover(ctx, kind); err != nil {
			t.Fatal("explicit old receiver retirement", err)
		}
		applied, err = q.ApplyTradeQuery(ctx, kind)
		if err != nil || !applied.Replayed {
			t.Fatal("reopen replay", applied, err)
		}
		if _, err := q.Ingest(ctx, kind, QuerySource{out.Stream, out.StreamID}, []byte(out.Body), 0); !errors.Is(err, ErrBatchOwner) {
			t.Fatal("retired receiver resumed", err)
		}
	}
	store, _ := NewTradeStore(db)
	r, err := store.Read(ctx, TradeScope{readOnlyRoute(), "4900", "offlineinv", "20261117"})
	if err != nil || r.Blocked || r.PendingCount != 0 || len(r.Orders) != 1 || len(r.Executions) != 1 || storedCount(t, db, "futures_trade_observations") != 2 {
		t.Fatal("nonempty v2 association/replay", r, err)
	}
}
