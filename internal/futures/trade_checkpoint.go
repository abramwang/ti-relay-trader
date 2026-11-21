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
	"io"
	"math"
	"reflect"
	"strconv"
)

var ErrTradeCheckpoint = errors.New("trade checkpoint requires integrity review")

// Legacy v1 is retained for verified activation and the full-replay oracle.
// Active v2 writers use keyed entities and a compact day header instead.
type tradeCheckpoint struct {
	Version      int
	AuditCount   int
	AuditBytes   int
	ReportSHA256 string
	Aliases      map[string]string
	Ambiguous    map[string]bool
	Eligible     map[string]string
	Totals       map[string]int64
	Overfilled   map[string]bool
}

func (p *tradeProjection) checkpoint(count, size int, report []byte) ([]byte, error) {
	sum := sha256.Sum256(report)
	return json.Marshal(tradeCheckpoint{1, count, size, hex.EncodeToString(sum[:]), p.aliases, p.ambiguous, p.eligible, p.totals, p.overfilled})
}

func restoreTradeProjection(scope TradeScope, count, size int, report, reportHash, state, stateHash []byte) (*tradeProjection, error) {
	rh, sh := sha256.Sum256(report), sha256.Sum256(state)
	if !bytes.Equal(rh[:], reportHash) || !bytes.Equal(sh[:], stateHash) {
		return nil, ErrTradeCheckpoint
	}
	var cp tradeCheckpoint
	p := newTradeProjection(scope)
	d := json.NewDecoder(bytes.NewReader(state))
	d.DisallowUnknownFields()
	if d.Decode(&cp) != nil || d.Decode(new(any)) != io.EOF || json.Unmarshal(report, &p.report) != nil || cp.Version != 1 ||
		cp.AuditCount != count || cp.AuditBytes != size || cp.ReportSHA256 != hex.EncodeToString(rh[:]) || p.report.Scope != scope ||
		p.report.Orders == nil || p.report.Executions == nil || p.report.Conflicts == nil || cp.Aliases == nil || cp.Ambiguous == nil ||
		cp.Eligible == nil || cp.Totals == nil || cp.Overfilled == nil || tradeCapacity(count, size, 0, 0) != nil {
		return nil, ErrTradeCheckpoint
	}
	p.aliases, p.ambiguous, p.eligible, p.totals, p.overfilled = cp.Aliases, cp.Ambiguous, cp.Eligible, cp.Totals, cp.Overfilled
	if err := validateTradeIndexes(p); err != nil {
		return nil, err
	}
	return p, nil
}

func validateTradeIndexes(p *tradeProjection) error {
	for _, reason := range p.report.Conflicts {
		p.conflicts[reason] = true
	}
	for key, order := range p.report.Orders {
		if order == nil || order.Order.GatewayOrderID != key {
			return ErrTradeCheckpoint
		}
	}
	for alias, owner := range p.aliases {
		order := p.report.Orders[owner]
		if order == nil || alias == "" || p.alias(order.Order.TradeRow) != alias {
			return ErrTradeCheckpoint
		}
	}
	pending := 0
	for id, fill := range p.report.Executions {
		if fill == nil || fill.Fill.FillID != id || !oneOf(fill.State, "pending", "linked", "conflict") {
			return ErrTradeCheckpoint
		}
		alias := p.alias(fill.Fill.TradeRow)
		if p.groups[alias] == nil {
			p.groups[alias] = map[string]*ExecutionRecord{}
		}
		p.groups[alias][id] = fill
		if fill.State == "pending" {
			pending++
		}
	}
	if pending != p.report.PendingCount || p.report.Blocked != (pending > 0 || len(p.report.Conflicts) > 0) {
		return ErrTradeCheckpoint
	}
	expectedEligible := map[string]string{}
	expectedTotals := map[string]int64{}
	for id, fill := range p.report.Executions {
		alias := p.alias(fill.Fill.TradeRow)
		owner := p.aliases[alias]
		order := p.report.Orders[owner]
		valid := !fill.FactConflict && !p.ambiguous[alias] && order != nil && !order.Blocked && economicsEqual(order.Order.TradeRow, fill.Fill.TradeRow)
		for _, hint := range fill.GatewayHints {
			valid = valid && (hint == owner || hint == alias)
		}
		if valid {
			qty := int64(fill.Fill.Qty)
			if qty <= 0 || qty > math.MaxInt64-expectedTotals[alias] {
				return ErrTradeCheckpoint
			}
			expectedEligible[id] = owner
			expectedTotals[alias] += qty
		}
	}
	if !reflect.DeepEqual(expectedEligible, p.eligible) {
		return ErrTradeCheckpoint
	}
	for alias, total := range p.totals {
		if total < 0 || total != expectedTotals[alias] {
			return ErrTradeCheckpoint
		}
	}
	for alias, total := range expectedTotals {
		if p.totals[alias] != total {
			return ErrTradeCheckpoint
		}
	}
	for alias, over := range p.overfilled {
		order := p.report.Orders[p.aliases[alias]]
		if over != (order != nil && expectedTotals[alias] > int64(order.Order.OrderQty)) {
			return ErrTradeCheckpoint
		}
	}
	for alias, total := range expectedTotals {
		if total > int64(p.report.Orders[p.aliases[alias]].Order.OrderQty) && !p.overfilled[alias] {
			return ErrTradeCheckpoint
		}
	}
	return nil
}

// Used only for legacy activation and explicit verification, not normal append.
func replayTradeEvidence(ctx context.Context, tx *sql.Tx, scope TradeScope, count, size int) (*tradeProjection, error) {
	rows, err := tx.QueryContext(ctx, `SELECT observation_id,source_id,kind,body,body_sha256,source_conflict
		FROM futures_trade_observations WHERE scope_key=$1 ORDER BY observation_id`, tradeScopeKey(scope))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p := newTradeProjection(scope)
	seenCount, seenSize := 0, 0
	sources := map[string]bool{}
	for rows.Next() {
		var id int64
		var source, kind string
		var raw, digest []byte
		var conflict bool
		if err := rows.Scan(&id, &source, &kind, &raw, &digest, &conflict); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if !bytes.Equal(sum[:], digest) || conflict != sources[source] {
			return nil, ErrTradeCheckpoint
		}
		sources[source] = true
		seenCount++
		seenSize += len(raw)
		if tradeCapacity(seenCount, seenSize, 0, 0) != nil {
			return nil, ErrAuditCapacity
		}
		if conflict {
			p.conflict("source identity conflict: " + source)
		} else {
			p.observe(strconv.FormatInt(id, 10), kind, raw)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if seenCount != count || seenSize != size {
		return nil, ErrTradeCheckpoint
	}
	p.reconcile()
	return p, nil
}

// Verify rebuilds from retained originals in one read-only snapshot, comparing
// both the public report and private indexes. It never repairs or releases work.
func (s *TradeStore) Verify(ctx context.Context, scope TradeScope) (TradeReport, error) {
	if scope.validate() != nil {
		return TradeReport{}, ErrTradeCheckpoint
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return TradeReport{}, err
	}
	defer tx.Rollback()
	var count, size, version int
	var dirty bool
	var report, digest, state, stateHash []byte
	contextJSON, _ := json.Marshal(scope)
	err = tx.QueryRowContext(ctx, `SELECT audit_count,audit_bytes,projection_version,projection_dirty,
        CASE WHEN projection_version=2 THEN summary ELSE report END,
        CASE WHEN projection_version=2 THEN summary_sha256 ELSE report_sha256 END,projection_state,projection_sha256
		FROM futures_trade_scopes WHERE scope_key=$1 AND context_json=$2::jsonb`, tradeScopeKey(scope), contextJSON).
		Scan(&count, &size, &version, &dirty, &report, &digest, &state, &stateHash)
	if errors.Is(err, sql.ErrNoRows) {
		return TradeReport{}, ErrNoSnapshot
	}
	if err != nil {
		return TradeReport{}, err
	}
	if dirty {
		return TradeReport{}, ErrTradeCheckpoint
	}
	p, err := replayTradeEvidence(ctx, tx, scope, count, size)
	if err != nil {
		return TradeReport{}, err
	}
	if version == 2 {
		header, cp, err := decodeTradeEntityHeader(scope, count, size, report, digest, state, stateHash)
		if err != nil {
			return TradeReport{}, err
		}
		stored, err := loadTradeEntities(ctx, tx, header, nil, nil, nil, true)
		if err != nil {
			return TradeReport{}, err
		}
		want, _ := json.Marshal(p.report)
		got, _ := json.Marshal(stored.report)
		wi, _ := p.checkpoint(count, size, want)
		gi, _ := stored.checkpoint(count, size, got)
		if !bytes.Equal(want, got) || !bytes.Equal(wi, gi) || cp.Orders != len(p.report.Orders) || cp.Executions != len(p.report.Executions) || cp.Aliases != len(entityAliases(p)) || header.PendingCount != p.report.PendingCount {
			return TradeReport{}, ErrTradeCheckpoint
		}
	} else {
		expected, _ := json.Marshal(p.report)
		hash := sha256.Sum256(report)
		if !bytes.Equal(hash[:], digest) || !bytes.Equal(expected, report) {
			return TradeReport{}, ErrTradeCheckpoint
		}
	}
	if version == 1 {
		restored, err := restoreTradeProjection(scope, count, size, report, digest, state, stateHash)
		if err != nil {
			return TradeReport{}, err
		}
		a, _ := p.checkpoint(count, size, report)
		b, _ := restored.checkpoint(count, size, report)
		if !bytes.Equal(a, b) {
			return TradeReport{}, ErrTradeCheckpoint
		}
	}
	if err := verifyTradeFactRows(ctx, tx, scope, p); err != nil {
		return TradeReport{}, err
	}
	out, err := readTradeReportSnapshot(ctx, tx, scope)
	if err != nil {
		return TradeReport{}, err
	}
	return out, tx.Commit()
}

func verifyTradeFactRows(ctx context.Context, tx *sql.Tx, scope TradeScope, p *tradeProjection) error {
	for _, item := range []struct {
		table string
		want  any
	}{{"futures_order_states", p.report.Orders}, {"futures_execution_facts", p.report.Executions}} {
		column := "gateway_order_id"
		consistent := "blocked IS NOT DISTINCT FROM (record->>'Blocked')::boolean"
		if item.table == "futures_execution_facts" {
			column = "fill_id"
			consistent = "link_status IS NOT DISTINCT FROM (record->>'State') AND linked_gateway IS NOT DISTINCT FROM NULLIF(record->>'LinkedGateway','')"
		}
		var raw []byte
		var same bool
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_object_agg(`+column+`,record),'{}'::jsonb),COALESCE(bool_and(`+consistent+`),true) FROM `+item.table+` WHERE scope_key=$1`, tradeScopeKey(scope)).Scan(&raw, &same); err != nil {
			return err
		}
		var got, want any
		encoded, _ := json.Marshal(item.want)
		decode := func(raw []byte, value *any) error {
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			return d.Decode(value)
		}
		if !same || decode(raw, &got) != nil || decode(encoded, &want) != nil || !reflect.DeepEqual(got, want) {
			return fmt.Errorf("%w: fact rows differ from replay: %s", ErrTradeCheckpoint, item.table)
		}
	}
	return nil
}
