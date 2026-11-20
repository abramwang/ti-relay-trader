package futures

import (
	"context"
	"strings"
	"time"
)

func (r *TerminalReader) HasOrderPolicy() bool {
	for _, a := range r.manifest.Accounts {
		if a.Execution != nil {
			return true
		}
	}
	return false
}

// NewOrderService only reads configuration/database identity. It neither
// creates tables nor initializes Redis bindings, queries OC or grants a day.
func (r *TerminalReader) NewOrderService(ctx context.Context, redisURL, expectedDatabase string, market OrderMarketEvidence) (*TestOrderService, error) {
	if r == nil || r.db == nil || !strings.HasSuffix(expectedDatabase, "_test") || len(r.manifest.Accounts) != len(r.sources) {
		return nil, ErrOrderExecutionOff
	}
	var actual string
	if err := r.db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&actual); err != nil || actual != expectedDatabase {
		return nil, ErrOrderExecutionOff
	}
	accounts := make([]TestOrderAccount, 0, len(r.manifest.Accounts))
	for _, a := range r.manifest.Accounts {
		if a.Execution == nil {
			continue
		}
		end, err := time.Parse(time.RFC3339Nano, a.Execution.AuthorizationExpiresAt)
		if err != nil || !validID(a.Execution.CommandGroup) || len(a.Execution.CommandGroup) > 128 {
			return nil, ErrOrderExecutionOff
		}
		accounts = append(accounts, TestOrderAccount{
			Source:        TestSourceConfig{Scope: a.Scope, DeploymentID: a.DeploymentID, RedisURL: redisURL},
			SubmitEnabled: a.Execution.SubmitEnabled, CancelEnabled: a.Execution.CancelEnabled, AuthorizationExpiresAt: end,
			CFFEX: cloneCFFEXPolicy(a.Execution.CFFEX),
		})
	}
	store, err := NewCommandStore(r.db)
	if err != nil {
		return nil, err
	}
	service, err := NewTestOrderService(store, accounts, market, r.orderTarget)
	if err == nil {
		service.cancelTarget = func(ctx context.Context, scope CommandScope) (TestQueryTarget, error) {
			return r.executionTarget(ctx, scope, true)
		}
	}
	return service, err
}

func (r *TerminalReader) orderTarget(ctx context.Context, scope CommandScope) (TestQueryTarget, error) {
	return r.executionTarget(ctx, scope, false)
}

func (r *TerminalReader) executionTarget(ctx context.Context, scope CommandScope, cancel bool) (TestQueryTarget, error) {
	for i, a := range r.manifest.Accounts {
		if a.Scope != scope || a.Execution == nil {
			continue
		}
		h, _, issue := r.connection(ctx, i)
		if issue != "" || h == nil || h.SessionID == "" || h.TradeDate == nil || h.ConnectionGeneration == nil || *h.ConnectionGeneration < 1 || time.Since(h.ProducedAt) < 0 || time.Since(h.ProducedAt) >= 5*time.Second || !h.BrokerReady || !h.SettlementConfirmed || h.StateText == "stopping" || h.State == "STOPPED" || h.RuntimeBlockedReason != "" {
			return TestQueryTarget{}, ErrQueryPublishGate
		}
		if (cancel && !h.AcceptingCancelCommands) || (!cancel && !h.AcceptingTradeCommands) {
			return TestQueryTarget{}, ErrQueryPublishGate
		}
		// This identifies the current target only. The existing publisher
		// independently verifies the full raw heartbeat, recovery, native
		// snapshot and execution mode before its atomic guarded XADD.
		target := TestQueryTarget{a.SourceID, a.ServerRunID, a.Epoch, a.Execution.CommandGroup, h.SessionID, *h.TradeDate, *h.ConnectionGeneration}
		return target, target.Validate()
	}
	return TestQueryTarget{}, ErrOrderExecutionOff
}
