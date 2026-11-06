package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

const maxTradeQueryRows = 10000
const maxTradeAuditCount = 50000
const maxTradeAuditBytes = 64 * 1024 * 1024
const tradeInsertChunk = 256

func tradeCapacity(count, size, addedCount, addedSize int) error {
	if count < 0 || size < 0 || addedCount < 0 || addedSize < 0 || count > maxTradeAuditCount || size > maxTradeAuditBytes || addedCount > maxTradeAuditCount-count || addedSize > maxTradeAuditBytes-size {
		return ErrAuditCapacity
	}
	return nil
}

// TradeStore stores caller-verified ROW evidence, not arbitrary envelopes/pages.
// SourceID must identify the original stream/entry/row, not a gateway or TradeID.
// This isolated implementation has bounded day journals; no Redis/API/ACK path.
type TradeStore struct{ db *sql.DB }

func NewTradeStore(db *sql.DB) (*TradeStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database required")
	}
	return &TradeStore{db: db}, nil
}

func tradeScopeKey(scope TradeScope) string {
	raw, _ := json.Marshal(struct {
		Route QueryRoute
		Day   string
	}{scope.Route, scope.TradeDate})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

type TradeIngestResult struct {
	Replayed     bool
	Blocked      bool
	PendingCount int
}

// Observe atomically commits original evidence, execution identities, order
// projections, links and quality. nil error means durable, NOT conflict-free.
// Unknown gateway ownership is never inferred from a failed local lookup.
func (s *TradeStore) Observe(ctx context.Context, scope TradeScope, sourceID, kind string, raw []byte) (TradeIngestResult, error) {
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return TradeIngestResult{}, err
	}
	defer tx.Rollback()
	result, err := observeTradesTx(ctx, tx, scope, []tradeObservation{{sourceID, kind, raw}})
	if err != nil {
		return TradeIngestResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TradeIngestResult{}, err
	}
	return result, nil
}

type tradeObservation struct {
	sourceID, kind string
	raw            []byte
}

// One replay and one projection per complete batch, in the caller's transaction.
func observeTradesTx(ctx context.Context, tx *sql.Tx, scope TradeScope, observations []tradeObservation) (TradeIngestResult, error) {
	if err := scope.validate(); err != nil {
		return TradeIngestResult{}, err
	}
	if len(observations) > maxTradeAuditCount {
		return TradeIngestResult{}, ErrAuditCapacity
	}
	for i, observation := range observations {
		if !validID(observation.sourceID) || len(observation.sourceID) > 512 || !oneOf(observation.kind, "order", "fill") {
			return TradeIngestResult{}, fmt.Errorf("invalid observation source/kind")
		}
		if len(observation.raw) > maxSnapshotBytes {
			return TradeIngestResult{}, ErrAuditCapacity
		}
		if observation.raw == nil {
			observations[i].raw = []byte{}
		}
	}
	contextJSON, _ := json.Marshal(scope)
	if len(contextJSON) > 64*1024 {
		return TradeIngestResult{}, ErrAuditCapacity
	}
	key := tradeScopeKey(scope)
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_trade_scopes(scope_key,context_json) VALUES($1,$2) ON CONFLICT DO NOTHING`, key, contextJSON); err != nil {
		return TradeIngestResult{}, err
	}
	var count, size int
	var authorized bool
	err := tx.QueryRowContext(ctx, `SELECT context_json=$2::jsonb,audit_count,audit_bytes FROM futures_trade_scopes WHERE scope_key=$1 FOR UPDATE`, key, contextJSON).Scan(&authorized, &count, &size)
	if err != nil {
		return TradeIngestResult{}, err
	}
	if !authorized {
		return TradeIngestResult{}, fmt.Errorf("immutable native authorization scope mismatch")
	}
	p := newTradeProjection(scope)
	rows, err := tx.QueryContext(ctx, `SELECT observation_id,source_id,kind,body,body_sha256,source_conflict FROM futures_trade_observations WHERE scope_key=$1 ORDER BY observation_id`, key)
	if err != nil {
		return TradeIngestResult{}, err
	}
	seenCount, seenSize := 0, 0
	priorBySource := map[string][]tradeObservation{}
	for rows.Next() {
		var id int64
		var priorSource, priorKind string
		var prior, digest []byte
		var conflict bool
		if err := rows.Scan(&id, &priorSource, &priorKind, &prior, &digest, &conflict); err != nil {
			rows.Close()
			return TradeIngestResult{}, err
		}
		sum := sha256.Sum256(prior)
		if !bytes.Equal(sum[:], digest) {
			rows.Close()
			return TradeIngestResult{}, fmt.Errorf("trade evidence checksum mismatch")
		}
		seenCount++
		seenSize += len(prior)
		if tradeCapacity(seenCount, seenSize, 0, 0) != nil {
			rows.Close()
			return TradeIngestResult{}, ErrAuditCapacity
		}
		if conflict {
			p.conflict("source identity conflict: " + priorSource)
		} else {
			p.observe(strconv.FormatInt(id, 10), priorKind, prior)
		}
		priorBySource[priorSource] = append(priorBySource[priorSource], tradeObservation{priorSource, priorKind, prior})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return TradeIngestResult{}, err
	}
	rows.Close()
	if count != seenCount || size != seenSize {
		return TradeIngestResult{}, fmt.Errorf("trade evidence count mismatch")
	}
	allReplayed := len(observations) > 0
	newRows := make([]pendingTradeObservation, 0, len(observations))
	for _, observation := range observations {
		sourceID, kind, raw := observation.sourceID, observation.kind, observation.raw
		replayed, sourceConflict := false, false
		for _, prior := range priorBySource[sourceID] {
			if prior.kind == kind && bytes.Equal(prior.raw, raw) {
				replayed = true
			} else {
				sourceConflict = true
			}
		}
		if replayed {
			continue
		}
		allReplayed = false
		if tradeCapacity(count, size, 1, len(raw)) != nil {
			return TradeIngestResult{}, ErrAuditCapacity
		}
		count++
		size += len(raw)
		newRows = append(newRows, pendingTradeObservation{tradeObservation: observation, conflict: sourceConflict})
		priorBySource[sourceID] = append(priorBySource[sourceID], observation)
	}
	for start := 0; start < len(newRows); start += tradeInsertChunk {
		end := min(start+tradeInsertChunk, len(newRows))
		if err := insertTradeObservations(ctx, tx, key, newRows[start:end], p); err != nil {
			return TradeIngestResult{}, err
		}
	}
	p.reconcile()
	orders, _ := json.Marshal(p.report.Orders)
	executions, _ := json.Marshal(p.report.Executions)
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_order_states(scope_key,gateway_order_id,record,blocked)
        SELECT $1,key,value,(value->>'Blocked')::boolean FROM jsonb_each($2::jsonb)
        ON CONFLICT(scope_key,gateway_order_id) DO UPDATE SET record=EXCLUDED.record,blocked=EXCLUDED.blocked`, key, orders); err != nil {
		return TradeIngestResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_execution_facts(scope_key,fill_id,record,link_status,linked_gateway)
        SELECT $1,key,value,value->>'State',NULLIF(value->>'LinkedGateway','') FROM jsonb_each($2::jsonb)
        ON CONFLICT(scope_key,fill_id) DO UPDATE SET record=EXCLUDED.record,link_status=EXCLUDED.link_status,linked_gateway=EXCLUDED.linked_gateway`, key, executions); err != nil {
		return TradeIngestResult{}, err
	}
	report, err := json.Marshal(p.report)
	if err != nil {
		return TradeIngestResult{}, err
	}
	digest := sha256.Sum256(report)
	if _, err := tx.ExecContext(ctx, `UPDATE futures_trade_scopes SET report=$2,report_sha256=$3,audit_count=$4,audit_bytes=$5,updated_at=clock_timestamp() WHERE scope_key=$1`, key, report, digest[:], count, size); err != nil {
		return TradeIngestResult{}, err
	}
	var invalidApplication bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_query_trade_applications a
        JOIN futures_query_batches b USING(batch_id) WHERE a.scope_key=$1 AND b.state<>'ready')
        OR EXISTS(SELECT 1 FROM futures_event_inbox WHERE scope_key=$1 AND state<>'applied')`, key).Scan(&invalidApplication); err != nil {
		return TradeIngestResult{}, err
	}
	return TradeIngestResult{Replayed: allReplayed, Blocked: p.report.Blocked || invalidApplication, PendingCount: p.report.PendingCount}, nil
}

type pendingTradeObservation struct {
	tradeObservation
	conflict bool
}

// Bound parameter batches avoid one network round trip per row. Ordinality
// preserves the original journal order; generated IDs remain the audit identity.
func insertTradeObservations(ctx context.Context, tx *sql.Tx, key string, items []pendingTradeObservation, p *tradeProjection) error {
	sources, kinds := []string{}, []string{}
	bodies, hashes := [][]byte{}, [][]byte{}
	conflicts := []bool{}
	for _, item := range items {
		sum := sha256.Sum256(item.raw)
		sources = append(sources, item.sourceID)
		kinds = append(kinds, item.kind)
		bodies = append(bodies, item.raw)
		hashes = append(hashes, sum[:])
		conflicts = append(conflicts, item.conflict)
	}
	rows, err := tx.QueryContext(ctx, `INSERT INTO futures_trade_observations(scope_key,source_id,kind,body,body_sha256,source_conflict)
        SELECT $1,s,k,b,h,c FROM unnest($2::text[],$3::text[],$4::bytea[],$5::bytea[],$6::boolean[]) WITH ORDINALITY AS x(s,k,b,h,c,n) ORDER BY n
        RETURNING observation_id,source_id,kind,body,source_conflict`, key, sources, kinds, bodies, hashes, conflicts)
	if err != nil {
		return err
	}
	defer rows.Close()
	type inserted struct {
		id           int64
		source, kind string
		raw          []byte
		conflict     bool
	}
	insertedRows := []inserted{}
	for rows.Next() {
		var row inserted
		if err = rows.Scan(&row.id, &row.source, &row.kind, &row.raw, &row.conflict); err != nil {
			return err
		}
		insertedRows = append(insertedRows, row)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(insertedRows) != len(items) {
		return fmt.Errorf("incomplete trade audit insert")
	}
	sort.Slice(insertedRows, func(i, j int) bool { return insertedRows[i].id < insertedRows[j].id })
	for _, row := range insertedRows {
		if row.conflict {
			p.conflict("source identity conflict: " + row.source)
		} else {
			p.observe(strconv.FormatInt(row.id, 10), row.kind, row.raw)
		}
	}
	return nil
}

// Read exposes quality alongside facts. Blocked reports must never be presented
// as a complete trading ledger or used to enable trading/accounting readiness.
func (s *TradeStore) Read(ctx context.Context, scope TradeScope) (TradeReport, error) {
	if err := scope.validate(); err != nil {
		return TradeReport{}, err
	}
	contextJSON, _ := json.Marshal(scope)
	var raw, digest, invalid, events []byte
	err := s.db.QueryRowContext(ctx, `SELECT report,report_sha256,
        COALESCE((SELECT jsonb_agg(a.batch_id ORDER BY a.batch_id) FROM futures_query_trade_applications a
        JOIN futures_query_batches b USING(batch_id) WHERE a.scope_key=s.scope_key AND b.state<>'ready'),'[]'::jsonb),
        COALESCE((SELECT jsonb_agg('event '||event_id||': '||state||' '||reason ORDER BY event_id)
        FROM futures_event_inbox e WHERE e.scope_key=s.scope_key AND e.state<>'applied'),'[]'::jsonb)
        FROM futures_trade_scopes s WHERE scope_key=$1 AND context_json=$2::jsonb`, tradeScopeKey(scope), contextJSON).Scan(&raw, &digest, &invalid, &events)
	if errors.Is(err, sql.ErrNoRows) {
		return TradeReport{}, ErrNoSnapshot
	}
	if err != nil {
		return TradeReport{}, err
	}
	sum := sha256.Sum256(raw)
	if !bytes.Equal(sum[:], digest) {
		return TradeReport{}, fmt.Errorf("trade projection checksum mismatch")
	}
	var report TradeReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return TradeReport{}, err
	}
	var invalidBatches []string
	if err := json.Unmarshal(invalid, &invalidBatches); err != nil {
		return TradeReport{}, err
	}
	for _, id := range invalidBatches {
		report.Blocked = true
		report.Conflicts = append(report.Conflicts, "applied query invalidated: "+id)
	}
	var eventIssues []string
	if err := json.Unmarshal(events, &eventIssues); err != nil {
		return TradeReport{}, err
	}
	if len(eventIssues) > 0 {
		report.Blocked = true
		report.Conflicts = append(report.Conflicts, eventIssues...)
	}
	return report, nil
}
