package performance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/timeutil"
)

var researchSourceTables = []string{"orders", "fills", "position_snapshots", "asset_snapshots", "etf_component_transfers", "order_fee_records"}

type ResearchSourceDay struct {
	Table  string `json:"table"`
	Date   string `json:"trade_date"`
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

// This observation starts a change watch. It is not a reconstructed snapshot
// of the inputs originally used by an older calculation.
type ResearchSourceObservation struct {
	Schema        string              `json:"schema"`
	Environment   string              `json:"environment"`
	Database      string              `json:"database"`
	AccountID     string              `json:"account_id"`
	VersionID     string              `json:"version_id"`
	PayloadSHA256 string              `json:"payload_sha256"`
	DateFrom      string              `json:"date_from"`
	DateTo        string              `json:"date_to"`
	ObservedAt    time.Time           `json:"observed_at"`
	Days          []ResearchSourceDay `json:"days"`
}

type ResearchSourceChange struct {
	Table        string `json:"table"`
	Date         string `json:"trade_date"`
	BeforeRows   int    `json:"before_rows"`
	AfterRows    int    `json:"after_rows"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
}

type ResearchSourcePreflight struct {
	Schema                    string                    `json:"schema"`
	Observation               ResearchSourceObservation `json:"observation"`
	ObservationSHA256         string                    `json:"observation_sha256"`
	BaselineSHA256            string                    `json:"baseline_sha256,omitempty"`
	SourceState               string                    `json:"source_state"`
	Changes                   []ResearchSourceChange    `json:"changes"`
	RecheckFrom               string                    `json:"recheck_from,omitempty"`
	AnchorAndAllocationsMatch bool                      `json:"anchor_and_allocations_match"`
	EvidenceChanges           []ResearchEvidenceChange  `json:"evidence_changes"`
	Unverified                []string                  `json:"unverified"`
	CostPublishable           bool                      `json:"cost_publishable"`
	NAVPublishable            bool                      `json:"economic_nav_publishable"`
	DatabaseWrites            int                       `json:"database_writes"`
	BrokerCommands            int                       `json:"broker_commands"`
}

func sourceDays(from, to string) ([]string, error) {
	a, e1 := time.Parse("2006-01-02", from)
	b, e2 := time.Parse("2006-01-02", to)
	if e1 != nil || e2 != nil || a.Format("2006-01-02") != from || b.Format("2006-01-02") != to || b.Before(a) || b.Sub(a) > 365*24*time.Hour {
		return nil, errors.New("invalid source observation dates")
	}
	var dates []string
	for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("2006-01-02"))
	}
	return dates, nil
}

func sourceHash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func validSourceHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}

func validateSourceObservation(o ResearchSourceObservation) error {
	dates, err := sourceDays(o.DateFrom, o.DateTo)
	if err != nil {
		return err
	}
	if o.Schema != "relay.research_source_observation.v1" || (o.Environment != "production" && o.Environment != "test") || o.Database == "" || o.AccountID == "" || o.VersionID == "" || !validSourceHash(o.PayloadSHA256) || o.ObservedAt.IsZero() || len(o.Days) != len(dates)*len(researchSourceTables) {
		return errors.New("invalid source observation scope")
	}
	i, total := 0, 0
	for _, table := range researchSourceTables {
		for _, date := range dates {
			d := o.Days[i]
			i++
			if d.Table != table || d.Date != date || d.Rows < 0 || !validSourceHash(d.SHA256) || (d.Rows == 0 && d.SHA256 != sourceHash(nil)) {
				return errors.New("invalid or incomplete source day coverage")
			}
			total += d.Rows
			if total > 100000 {
				return errors.New("source row budget exceeded")
			}
		}
	}
	return nil
}

func SourceObservationHash(o ResearchSourceObservation) (string, error) {
	if err := validateSourceObservation(o); err != nil {
		return "", err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	return sourceHash(raw), nil
}

// All tables are static allowlisted names. Raw row digests are a conservative
// change signal, including audit metadata; they do not prove economic changes.
func ReadResearchSourceObservation(ctx context.Context, tx *sql.Tx, scope ResearchSourceObservation) (ResearchSourceObservation, error) {
	dates, err := sourceDays(scope.DateFrom, scope.DateTo)
	if err != nil {
		return scope, err
	}
	var ro, isolation, database string
	if err = tx.QueryRowContext(ctx, `SELECT current_setting('transaction_read_only'),current_setting('transaction_isolation'),current_database()`).Scan(&ro, &isolation, &database); err != nil {
		return scope, err
	}
	if ro != "on" || isolation != "repeatable read" || database != scope.Database {
		return scope, errors.New("source watch requires verified read-only repeatable-read database")
	}
	for _, statement := range []string{`SET LOCAL search_path TO public`, `SET LOCAL TIME ZONE 'UTC'`, `SET LOCAL DateStyle TO 'ISO, YMD'`, `SET LOCAL statement_timeout='30s'`, `SET LOCAL lock_timeout='5s'`} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return scope, err
		}
	}
	scope.Schema = "relay.research_source_observation.v1"
	if err := tx.QueryRowContext(ctx, `SELECT transaction_timestamp()`).Scan(&scope.ObservedAt); err != nil {
		return scope, err
	}
	scope.ObservedAt = scope.ObservedAt.In(timeutil.Location())
	scope.Days = nil
	totalRows, totalBytes := 0, 0
	for _, table := range researchSourceTables {
		byDay := map[string][]string{}
		rows, err := tx.QueryContext(ctx, `SELECT trade_date::text,to_jsonb(t)::text FROM public.`+table+` t WHERE account_id=$1 AND trade_date BETWEEN $2::date AND $3::date ORDER BY trade_date LIMIT 100001`, scope.AccountID, scope.DateFrom, scope.DateTo)
		if err != nil {
			return scope, err
		}
		for rows.Next() {
			var date, raw string
			if err := rows.Scan(&date, &raw); err != nil {
				rows.Close()
				return scope, err
			}
			totalRows++
			totalBytes += len(raw)
			if totalRows > 100000 || len(raw) > 1<<20 || totalBytes > 256<<20 {
				rows.Close()
				return scope, errors.New("source observation read budget exceeded")
			}
			byDay[date] = append(byDay[date], sourceHash([]byte(raw)))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return scope, err
		}
		for _, date := range dates {
			hashes := byDay[date]
			sort.Strings(hashes)
			scope.Days = append(scope.Days, ResearchSourceDay{Table: table, Date: date, Rows: len(hashes), SHA256: sourceHash([]byte(strings.Join(hashes, "")))})
		}
	}
	return scope, validateSourceObservation(scope)
}

func ReviewResearchSources(current ResearchSourceObservation, previous *ResearchSourcePreflight, comparison ResearchCostComparison) (ResearchSourcePreflight, error) {
	current.Days = append([]ResearchSourceDay(nil), current.Days...)
	hash, err := SourceObservationHash(current)
	if err != nil {
		return ResearchSourcePreflight{}, err
	}
	if comparison.AccountID != current.AccountID || comparison.VersionID != current.VersionID || comparison.PayloadSHA256 != current.PayloadSHA256 || !comparison.ResearchOnly || comparison.NAVPublishable || len(comparison.Days) == 0 || comparison.Days[0].TradeDate <= current.DateFrom || comparison.Days[len(comparison.Days)-1].TradeDate != current.DateTo {
		return ResearchSourcePreflight{}, errors.New("source/comparison scope mismatch")
	}
	r := ResearchSourcePreflight{Schema: "relay.research_source_preflight.v1", Observation: current, ObservationSHA256: hash, SourceState: "observation_started_not_historical_verification", Changes: []ResearchSourceChange{}, AnchorAndAllocationsMatch: comparison.AnchorAndAllocationsMatch, EvidenceChanges: comparison.EvidenceChanges,
		Unverified: []string{"original_calculation_source_manifest_missing", "meridian_original_inputs_and_current_revisions_unverified", "source_coverage_not_proof_of_query_completion", "preview_only_cost_semantics_require_publication_review", "broker_asset_components_and_economic_nav_scope_unverified"}}
	if previous != nil {
		prior := previous.Observation
		ph, err := SourceObservationHash(prior)
		if err != nil || previous.Schema != r.Schema || ph != previous.ObservationSHA256 {
			return r, errors.New("baseline observation integrity failure")
		}
		if prior.Environment != current.Environment || prior.Database != current.Database || prior.AccountID != current.AccountID || prior.VersionID != current.VersionID || prior.PayloadSHA256 != current.PayloadSHA256 || prior.DateFrom != current.DateFrom || prior.DateTo != current.DateTo || prior.ObservedAt.After(current.ObservedAt) {
			return r, errors.New("baseline observation scope/time mismatch")
		}
		r.BaselineSHA256 = ph
		r.SourceState = "unchanged_since_observation_not_historical_verification"
		for i, now := range current.Days {
			old := prior.Days[i]
			if reflect.DeepEqual(old, now) {
				continue
			}
			r.Changes = append(r.Changes, ResearchSourceChange{Table: now.Table, Date: now.Date, BeforeRows: old.Rows, AfterRows: now.Rows, BeforeSHA256: old.SHA256, AfterSHA256: now.SHA256})
			if r.RecheckFrom == "" || now.Date < r.RecheckFrom {
				r.RecheckFrom = now.Date
			}
		}
		if len(r.Changes) > 0 {
			r.SourceState = "source_rows_changed_requires_review"
		}
	}
	if !r.AnchorAndAllocationsMatch {
		// A changed opening anchor or attribution can propagate through every
		// later cost. Until dependency mapping exists, review the full window.
		r.RecheckFrom = current.DateFrom
		r.Unverified = append(r.Unverified, "opening_or_active_attribution_changed")
	}
	if len(r.Changes) > 0 {
		r.Unverified = append(r.Unverified, "source_rows_changed_since_observation")
	}
	return r, nil
}

func (r ResearchSourcePreflight) Summary() map[string]any {
	counts := map[string]int{}
	for _, d := range r.Observation.Days {
		counts[d.Table] += d.Rows
	}
	return map[string]any{"account_id": r.Observation.AccountID, "version_id": r.Observation.VersionID, "source_state": r.SourceState, "source_rows": counts, "changed_table_days": len(r.Changes), "recheck_from": r.RecheckFrom, "unverified": r.Unverified, "cost_publishable": false, "economic_nav_publishable": false, "database_writes": 0, "broker_commands": 0, "observation_sha256": r.ObservationSHA256}
}
