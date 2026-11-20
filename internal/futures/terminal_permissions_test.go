package futures

import (
	"testing"
	"time"

	"ti-relay-trader/internal/timeutil"
)

func TestTerminalExecutionPolicy(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 5, 0, 0, timeutil.Location())
	for _, scenario := range []string{"ready", "cancel_only", "no_cancel", "capture_missing", "capture_stale", "expired", "stale", "future", "wrong_route", "production", "unavailable", "ledger", "blocked", "legacy", "missing_native", "recovery", "afternoon_break", "wrong_day"} {
		t.Run(scenario, func(t *testing.T) {
			b, g, day := true, int64(1), "20261119"
			scope := CommandScope{Route: QueryRoute{Env: "test", BrokerID: "ctp_4900", GatewayID: "synthetic", AccountID: "300052141"}}
			policy := TestOrderAccount{Source: TestSourceConfig{Scope: scope}, SubmitEnabled: true, CancelEnabled: true, AuthorizationExpiresAt: now.Add(time.Hour)}
			h := &AdvertisedHeartbeat{ProducedAt: now, SessionID: "test-session", TradeDate: &day, ConnectionGeneration: &g, AuthorizationOwner: "relay", RedisReady: true, BrokerReady: true, SettlementConfirmed: true, RecoveryClear: &b, NativeOrderSnapshotReady: &b, OrderSnapshotScope: "full_native_current_session", AcceptingTradeCommands: true, AcceptingCancelCommands: true}
			a := TerminalAccount{AccountID: "300052141", Route: scope.Route, Heartbeat: h, ConnectionState: "execution"}
			a.Capture = &CaptureHealthView{Ready: true, State: "running", ValidUntil: now.Add(time.Minute)}
			n := now
			wantSubmit, wantCancel := false, false
			switch scenario {
			case "capture_missing":
				a.Capture = nil
			case "capture_stale":
				a.Capture.ValidUntil = now
			case "ready":
				wantSubmit, wantCancel = true, true
			case "cancel_only":
				policy.SubmitEnabled = false
				h.AcceptingTradeCommands = false
				wantCancel = true
			case "no_cancel":
				policy.CancelEnabled = false
				wantSubmit = true
			case "expired":
				policy.AuthorizationExpiresAt = now
			case "stale":
				h.ProducedAt = now.Add(-5 * time.Second)
			case "future":
				h.ProducedAt = now.Add(time.Second)
			case "wrong_route":
				a.Route.GatewayID = "other"
			case "production":
				a.Route.Env = "prod"
			case "unavailable":
				a.ConnectionError = "source_changed"
			case "ledger":
				a.LocalOrderError = "invalid"
			case "blocked":
				h.RuntimeBlockedReason = "blocked"
			case "legacy":
				h.AuthorizationOwner = ""
			case "missing_native":
				h.NativeOrderSnapshotReady = nil
				h.OrderSnapshotScope = ""
				wantSubmit, wantCancel = true, true
			case "recovery":
				b = false
			case "afternoon_break":
				n = time.Date(2026, 11, 19, 12, 0, 0, 0, timeutil.Location())
				h.ProducedAt = n
				policy.AuthorizationExpiresAt = n.Add(time.Hour)
			case "wrong_day":
				day = "20261118"
			}
			s := &TestOrderService{accounts: map[string]TestOrderAccount{"300052141": policy}}
			v := s.TerminalExecution(a, n)
			if v.SubmitEnabled != wantSubmit || v.CancelEnabled != wantCancel {
				t.Fatal(v)
			}
			if wantSubmit || wantCancel {
				if !v.ValidUntil.Equal(now.Add(5*time.Second)) || v.TradeDate != "20261119" {
					t.Fatal(v)
				}
			}
		})
	}
}

func TestCFFEXTerminalScopeSeparateFromSHFE(t *testing.T) {
	for clock, enabled := range map[string]bool{"09:05:00": false, "09:30:00": true, "10:15:00": true, "13:00:00": true, "15:00:00": false} {
		now, _ := time.Parse(time.RFC3339, "2026-11-19T"+clock+"+08:00")
		b, generation, day := true, int64(1), "20261119"
		scope := CommandScope{Route: QueryRoute{"test", "ctp_4900", "synthetic", "300052141"}}
		p := TestOrderAccount{Source: TestSourceConfig{Scope: scope}, SubmitEnabled: true, CancelEnabled: true,
			AuthorizationExpiresAt: now.Add(time.Hour), CFFEX: &CFFEXExecutionPolicy{[]string{"IF", "IM"}, 10}}
		h := &AdvertisedHeartbeat{ProducedAt: now, SessionID: "test", TradeDate: &day, ConnectionGeneration: &generation,
			AuthorizationOwner: "relay", RedisReady: true, BrokerReady: true, SettlementConfirmed: true, RecoveryClear: &b,
			AcceptingTradeCommands: true, AcceptingCancelCommands: true}
		a := TerminalAccount{AccountID: scope.Route.AccountID, Route: scope.Route, Heartbeat: h, ConnectionState: "execution",
			Capture: &CaptureHealthView{Ready: true, ValidUntil: now.Add(time.Second)}}
		s := &TestOrderService{accounts: map[string]TestOrderAccount{a.AccountID: p}}
		v := s.TerminalExecution(a, now)
		if v.CFFEX == nil || v.CFFEX.MaxOrderQty != 10 || v.CFFEX.SubmitEnabled != enabled || v.CFFEX.CancelEnabled != enabled {
			t.Fatal(clock, v)
		}
		if enabled && !v.CFFEX.ValidUntil.Equal(a.Capture.ValidUntil) {
			t.Fatal("scope outlived capture", v)
		}
		if clock == "09:05:00" && !v.SubmitEnabled || clock == "13:00:00" && v.SubmitEnabled {
			t.Fatal("legacy SHFE sessions changed", v)
		}
		v.CFFEX.Products[0] = "T"
		if !p.CFFEX.allows("IF2701", 1) {
			t.Fatal("view mutates operator policy")
		}
		a.Capture.Ready = false
		if got := s.TerminalExecution(a, now); got.CFFEX.SubmitEnabled || got.CFFEX.CancelEnabled {
			t.Fatal("capture unavailable scope still ready", got)
		}
	}
}
