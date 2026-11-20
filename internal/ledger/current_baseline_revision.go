package ledger

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"reflect"
	"strconv"
	"time"
)

var (
	ErrCurrentBaselineStale    = errors.New("current baseline source revision changed")
	ErrCurrentBaselineTracking = errors.New("current baseline revision tracking unavailable")
)

// RevisionEvidence is one local database snapshot, not a source-stream cursor or
// a claim that queries and broker events cover the same economic boundary.
type CurrentBaselineRevisionEvidence struct {
	Epoch            string `json:"epoch"`
	AccountRevision  string `json:"account_revision"`
	UnscopedRevision string `json:"unscoped_revision"`
}

func (r CurrentBaselineRevisionEvidence) valid() bool {
	b, err := hex.DecodeString(r.Epoch)
	if err != nil || len(b) != 16 || hex.EncodeToString(b) != r.Epoch {
		return false
	}
	for _, v := range []string{r.AccountRevision, r.UnscopedRevision} {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || strconv.FormatInt(n, 10) != v {
			return false
		}
	}
	return true
}

// Keep this list explicit: adding a new writer/table requires an audit of the
// observation contract, not an automatic claim of all-account coverage.
var currentBaselineRevisionTables = []string{"accounts", "account_gateway_routes", "orders", "order_events", "fills", "positions", "asset_snapshots", "cash_ledger", "etf_component_transfers", "order_cancel_attempts", "test_legacy_order_resolutions", "raw_stream_messages"}

func readCurrentBaselineRevision(ctx context.Context, q currentBaselineReader, account string) (CurrentBaselineRevisionEvidence, error) {
	var r CurrentBaselineRevisionEvidence
	if _, err := securitiesCommandTracking(ctx, q); err != nil {
		return r, err
	}
	var observed int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM unnest($1::text[]) AS names(name)
JOIN pg_class c ON c.oid=to_regclass(names.name)
JOIN pg_trigger t ON t.tgrelid=c.oid
JOIN pg_proc p ON p.oid=t.tgfoid AND p.pronamespace=c.relnamespace
WHERE ((t.tgname='account_baseline_revision_watch' AND t.tgtype=29)
OR (t.tgname='account_baseline_revision_truncate' AND t.tgtype=32))
AND t.tgenabled IN ('O','A') AND p.proname='observe_account_baseline_revision' AND p.pronargs=0`, currentBaselineRevisionTables).Scan(&observed)
	if err != nil {
		return r, err
	}
	if observed != 2*len(currentBaselineRevisionTables) {
		return r, ErrCurrentBaselineTracking
	}
	err = q.QueryRowContext(ctx, `SELECT epoch,
COALESCE((SELECT revision FROM account_baseline_revisions WHERE account_id=$1),0)::text,
COALESCE((SELECT revision FROM account_baseline_revisions WHERE account_id=''),0)::text
FROM account_baseline_revision_epoch WHERE singleton`, account).Scan(&r.Epoch, &r.AccountRevision, &r.UnscopedRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrCurrentBaselineTracking
	}
	if err != nil {
		return r, err
	}
	if !r.valid() {
		return r, ErrCurrentBaselineTracking
	}
	return r, nil
}

func checkCurrentBaselineRevisionForPublication(ctx context.Context, tx *sql.Tx, account string, expected *CurrentBaselineRevisionEvidence) error {
	if expected == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('relay.account-baseline:',0))`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('relay.account-baseline:' || $1,0))`, account); err != nil {
		return err
	}
	actual, err := readCurrentBaselineRevision(ctx, tx, account)
	if err != nil {
		return err
	}
	if actual != *expected {
		return ErrCurrentBaselineStale
	}
	return nil
}

type CurrentBaselineValidity struct {
	BaselineID            string                           `json:"baseline_id"`
	CheckedAt             time.Time                        `json:"checked_at"`
	Status                string                           `json:"status"`
	CurrentLedgerRevision *string                          `json:"current_ledger_revision"`
	RevisionEvidence      *CurrentBaselineRevisionEvidence `json:"revision_evidence"`
	ValidUntil            *time.Time                       `json:"valid_until"`
	Reasons               []string                         `json:"reasons"`
	SourceHealth          *CurrentBaselineSourceHealth     `json:"source_health,omitempty"`
}

// CheckValidity is GET-safe and never contacts OC or writes invalidation state.
// Matching local revisions cannot promote a partial candidate to "current".
// The scope argument must come from trusted current deployment/session context.
func (s *CurrentBaselineStore) CheckValidity(ctx context.Context, deploymentEnvironment string, scope CurrentBaselineScope, id string) (CurrentBaselineValidity, error) {
	out := CurrentBaselineValidity{BaselineID: id, Status: "unverifiable", Reasons: []string{}}
	if scope.validate() != nil || scope.Environment != deploymentEnvironment || !validBaselineText(id) {
		return out, ErrCurrentBaselineInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT transaction_timestamp()`).Scan(&out.CheckedAt); err != nil {
		return out, err
	}
	m, err := readCurrentBaselineManifestForAccount(ctx, tx, scope.Environment, scope.AccountID, id)
	if err != nil {
		return out, err
	}
	out.ValidUntil = m.ValidUntil
	if !reflect.DeepEqual(m.Scope, scope) {
		out.Status = "invalidated"
		out.Reasons = append(out.Reasons, "scope_changed")
	}
	r, err := readCurrentBaselineRevision(ctx, tx, scope.AccountID)
	if errors.Is(err, ErrCurrentBaselineTracking) {
		out.Reasons = append(out.Reasons, "revision_tracking_unavailable")
	} else if err != nil {
		return out, err
	} else {
		out.RevisionEvidence = &r
		out.CurrentLedgerRevision = &r.AccountRevision
		if m.RevisionEvidence == nil {
			out.Reasons = append(out.Reasons, "baseline_revision_missing")
		} else if *m.RevisionEvidence != r {
			out.Status = "invalidated"
			out.Reasons = append(out.Reasons, "ledger_revision_changed")
		}
	}
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM account_baseline_heads WHERE environment=$1 AND account_id=$2`, scope.Environment, scope.AccountID).Scan(&generation); err != nil {
		return out, err
	}
	published, _ := strconv.ParseInt(m.PublicationGeneration, 10, 64)
	if generation < published {
		return out, ErrCurrentBaselineCorrupt
	}
	if generation > published {
		out.Status = "invalidated"
		out.Reasons = append(out.Reasons, "baseline_superseded")
	}
	if m.SourceHealth != nil {
		out.SourceHealth, err = readCurrentBaselineHealth(ctx, tx, scope, out.CheckedAt)
		if err != nil {
			return out, err
		}
		out.Reasons = append(out.Reasons, out.SourceHealth.Reasons...)
	}
	out.Reasons = append(out.Reasons, "coverage_unverified", "query_effects_unreconciled", "source_health_unverified", "validity_policy_unset")
	if err = tx.Commit(); err != nil {
		return CurrentBaselineValidity{}, err
	}
	return out, nil
}
