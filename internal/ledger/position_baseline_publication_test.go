package ledger

import (
	"encoding/json"
	"testing"
	"time"

	"ti-relay-trader/internal/trading"
)

func cashTimeBaselineFixture(t *testing.T) baselineFixture {
	t.Helper()
	f := newBaselineFixture(t, 2)
	at := f.broker.CapturedAt.Add(-2 * time.Second)
	asset := trading.Asset{AccountID: "a", UpdatedAt: at, CashTotal: 1019824.081435, CashAvailable: 1019824.081435, NetAsset: 1019824.081435, Commission: 971.598565}
	f.close.CapturedAt = at
	for i := range f.close.Rows {
		f.close.Rows[i].CapturedAt = at
	}
	f.close.Raw.Asset = &asset
	broker, source := asset, asset
	f.close.Raw.BrokerAsset = &source
	f.broker.Raw.Asset = &broker
	f.close.Raw.ReverseRepo.CashObservedAt = at
	var reply map[string]any
	if err := json.Unmarshal(f.assetReplies[0].Body, &reply); err != nil {
		t.Fatal(err)
	}
	// The ingress contract uses routing for logical identity and produced_at for
	// observation time; the payload may retain the broker account and no time.
	reply["payload"] = map[string]any{"account": map[string]any{"account_id": "broker-a", "cash_total": 1019824.0814349862, "cash_available": 1019824.0814349862, "net_asset": 1019824.0814349862, "commission": 971.5985649999996}}
	f.assetReplies[0].Body, _ = json.Marshal(reply)
	return f
}

func TestPositionBaselineSeparateCashAndBatchClocks(t *testing.T) {
	f := cashTimeBaselineFixture(t)
	p, err := f.assemble()
	if err != nil || p.Baseline.Completeness != "complete" || !p.Baseline.Proof["cash_observation_time_linked"] || p.Baseline.CashObservedAt != baselineTime(f.close.CapturedAt) || p.Baseline.BusinessAsOf != baselineTime(f.broker.CapturedAt) {
		t.Fatal(p.Baseline, err)
	}
	for name, mutate := range map[string]func(*baselineFixture){
		"source account":      func(f *baselineFixture) { f.close.Raw.BrokerAsset.AccountID = "other" },
		"source micro amount": func(f *baselineFixture) { f.close.Raw.BrokerAsset.CashTotal += 0.000001 },
		"missing cash time":   func(f *baselineFixture) { f.close.Raw.ReverseRepo.CashObservedAt = time.Time{} },
		"missing source":      func(f *baselineFixture) { f.close.Raw.BrokerAsset = nil },
		"changed time":        func(f *baselineFixture) { f.close.CapturedAt = f.close.CapturedAt.Add(time.Second) },
		"partial archive":     func(f *baselineFixture) { f.assetReplies = nil },
		"old source": func(f *baselineFixture) {
			f.broker.Raw.Asset.UpdatedAt = f.broker.Raw.Asset.UpdatedAt.AddDate(0, 0, -1)
		},
		"missing publication": func(f *baselineFixture) { f.closeJob = baselineJobReport{} },
		"row generation":      func(f *baselineFixture) { f.close.Rows[0].Raw.RunID = "other" },
		"row quantity":        func(f *baselineFixture) { f.close.Rows[0].Quantity++ },
	} {
		t.Run(name, func(t *testing.T) {
			f := cashTimeBaselineFixture(t)
			mutate(&f)
			p, err := f.assemble()
			if err != nil || p.Baseline.Completeness == "complete" || p.Baseline.MissingSecurityMeansZero {
				t.Fatal(p.Baseline, err)
			}
		})
	}
}

func TestPositionBaselineCashClockSkewUsesReceiveWindow(t *testing.T) {
	f := cashTimeBaselineFixture(t)
	// OC's clock can precede the local request start slightly. Reply origin and
	// local receipt window prove the request, not clock synchronization.
	started := f.close.CapturedAt.Add(2 * time.Millisecond)
	f.captureJob.Accounts[0].Freshness.StartedAt = baselineTime(started)
	for i := range f.replies {
		if f.replies[i].ReceivedAt.Before(started) {
			f.replies[i].ReceivedAt = started.Add(time.Millisecond)
		}
	}
	f.assetReplies[0].ReceivedAt = started.Add(time.Millisecond)
	p, err := f.assemble()
	if err != nil || p.Baseline.Completeness != "complete" {
		t.Fatal(p.Baseline, err)
	}
	f.assetReplies[0].ReceivedAt = started.Add(-time.Millisecond)
	p, err = f.assemble()
	if err != nil || p.Baseline.Completeness == "complete" {
		t.Fatal("local receive window bypass", p.Baseline, err)
	}
}

func recoveryBaselineFixture(t *testing.T) baselineFixture {
	f := cashTimeBaselineFixture(t)
	f.close.Raw.Source = "historical-repair"
	r := f.closeJob.Settlement.Result
	r.TradeDate = f.query.TradeDate
	r.Source = f.close.Raw.Source
	r.Status = "completed"
	no := false
	zero := 0
	r.DryRun = &no
	r.SnapshotOnly = &no
	r.AccountErrors = &zero
	r.Accounts[0].Asset = f.close.Raw.Asset
	f.closeJob = baselineJobReport{RecoveryPublication: &r}
	return f
}

func TestPositionBaselineRecoveryPublication(t *testing.T) {
	f := recoveryBaselineFixture(t)
	p, err := f.assemble()
	if err != nil || p.Baseline.Completeness != "complete" || p.Baseline.ClosePublicationSource != "reconciliation_run" {
		t.Fatal(p.Baseline, err)
	}
	for name, mutate := range map[string]func(*baselineFixture){
		"failed":                func(f *baselineFixture) { f.closeJob.RecoveryPublication.Status = "failed" },
		"wrong run":             func(f *baselineFixture) { f.closeJob.RecoveryPublication.RunID = "other" },
		"wrong day":             func(f *baselineFixture) { f.closeJob.RecoveryPublication.TradeDate = "2026-10-08" },
		"wrong account":         func(f *baselineFixture) { f.closeJob.RecoveryPublication.Accounts[0].AccountID = "other" },
		"dry run":               func(f *baselineFixture) { yes := true; f.closeJob.RecoveryPublication.DryRun = &yes },
		"missing dry-run field": func(f *baselineFixture) { f.closeJob.RecoveryPublication.DryRun = nil },
		"source":                func(f *baselineFixture) { f.closeJob.RecoveryPublication.Source = "other" },
		"summary error":         func(f *baselineFixture) { f.closeJob.RecoveryPublication.Errors = []string{"failed"} },
		"account error":         func(f *baselineFixture) { f.closeJob.RecoveryPublication.Accounts[0].Errors = []string{"failed"} },
		"missing count":         func(f *baselineFixture) { f.closeJob.RecoveryPublication.Accounts[0].Count = nil },
		"missing asset":         func(f *baselineFixture) { f.closeJob.RecoveryPublication.Accounts[0].Asset = nil },
		"wrong asset account": func(f *baselineFixture) {
			f.close.Raw.Asset.AccountID = "other"
		},
		"snapshot only":       func(f *baselineFixture) { yes := true; f.closeJob.RecoveryPublication.SnapshotOnly = &yes },
		"missing error count": func(f *baselineFixture) { f.closeJob.RecoveryPublication.AccountErrors = nil },
		"publication too early": func(f *baselineFixture) {
			f.closeJob.RecoveryPublication.CompletedAt = baselineTime(f.close.CapturedAt.Add(-time.Second))
		},
		"duplicate account": func(f *baselineFixture) {
			r := f.closeJob.RecoveryPublication
			r.Accounts = append(r.Accounts, r.Accounts[0])
		},
		"invalid job not overridden": func(f *baselineFixture) { f.closeJob.Settlement.Result.RunID = f.close.Raw.RunID },
	} {
		t.Run(name, func(t *testing.T) {
			f := recoveryBaselineFixture(t)
			mutate(&f)
			p, err := f.assemble()
			if err != nil || p.Baseline.Completeness == "complete" {
				t.Fatal(p.Baseline, err)
			}
		})
	}
}
