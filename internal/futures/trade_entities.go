package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
)

// The header is small on healthy days. Conflict reasons remain in the header;
// orders, executions (including fees), and alias indexes live in keyed rows.
type tradeEntityCheckpoint struct {
	Version, AuditCount, AuditBytes int
	ReportSHA256                    string
	Orders, Executions, Aliases     int
}

type tradeAliasRecord struct {
	Owner                               string
	Ambiguous                           bool
	Total                               int64
	Overfilled, HasTotal, HasOverfilled bool
}

func decodeTradeEntityHeader(scope TradeScope, count, size int, report, digest, state, stateHash []byte) (TradeReport, tradeEntityCheckpoint, error) {
	var header TradeReport
	var cp tradeEntityCheckpoint
	rh, sh := sha256.Sum256(report), sha256.Sum256(state)
	d := json.NewDecoder(bytes.NewReader(state))
	d.DisallowUnknownFields()
	if !bytes.Equal(rh[:], digest) || !bytes.Equal(sh[:], stateHash) ||
		d.Decode(&cp) != nil || d.Decode(new(any)) != io.EOF || json.Unmarshal(report, &header) != nil ||
		cp.Version != 2 || cp.AuditCount != count || cp.AuditBytes != size || cp.ReportSHA256 != hex.EncodeToString(rh[:]) ||
		header.Scope != scope || header.Orders == nil || header.Executions == nil || len(header.Orders)+len(header.Executions) != 0 ||
		header.Conflicts == nil || header.PendingCount < 0 || header.PendingCount > cp.Executions ||
		header.Blocked != (header.PendingCount > 0 || len(header.Conflicts) > 0) ||
		cp.Orders < 0 || cp.Executions < 0 || cp.Aliases < 0 || cp.Orders > count || cp.Executions > count || cp.Orders+cp.Executions > count || cp.Aliases > 2*count ||
		tradeCapacity(count, size, 0, 0) != nil {
		return header, cp, ErrTradeCheckpoint
	}
	return header, cp, nil
}

func entityAliases(p *tradeProjection) map[string]tradeAliasRecord {
	keys := map[string]bool{}
	for k := range p.aliases {
		keys[k] = true
	}
	for k := range p.ambiguous {
		keys[k] = true
	}
	for k := range p.totals {
		keys[k] = true
	}
	for k := range p.overfilled {
		keys[k] = true
	}
	out := map[string]tradeAliasRecord{}
	for k := range keys {
		_, total := p.totals[k]
		_, over := p.overfilled[k]
		out[k] = tradeAliasRecord{p.aliases[k], p.ambiguous[k], p.totals[k], p.overfilled[k], total, over}
	}
	return out
}

// Include both incoming and previously established aliases. A conflicting new
// SysID must not strand the old group, and a sparse order may acquire an alias.
func affectedTradeAliases(ctx context.Context, tx *sql.Tx, scope TradeScope, items []tradeObservation) ([]string, []string, []string, error) {
	aliases, orders, fills := map[string]bool{}, map[string]bool{}, map[string]bool{}
	p := newTradeProjection(scope)
	for _, item := range items {
		if item.kind == "order" {
			if row, err := DecodeOrder(item.raw, scope); err == nil {
				orders[row.GatewayOrderID] = true
				aliases[p.alias(row.TradeRow)] = true
			}
		} else if row, err := DecodeFill(item.raw, scope); err == nil {
			fills[row.FillID] = true
			aliases[p.alias(row.TradeRow)] = true
		}
	}
	o, f := tradeKeys(orders), tradeKeys(fills)
	rows, err := tx.QueryContext(ctx, `SELECT native_alias FROM futures_order_states WHERE scope_key=$1 AND gateway_order_id=ANY($2::text[])
        UNION SELECT native_alias FROM futures_execution_facts WHERE scope_key=$1 AND fill_id=ANY($3::text[])`, tradeScopeKey(scope), o, f)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, nil, nil, err
		}
		aliases[alias] = true
	}
	return tradeKeys(aliases), o, f, rows.Err()
}

func tradeKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// all is used only for explicit report reads/verification. Normal ingestion
// loads complete affected native-order groups, not unrelated daily facts.
func loadTradeEntities(ctx context.Context, tx *sql.Tx, header TradeReport, aliases, orders, fills []string, all bool) (*tradeProjection, error) {
	p := newTradeProjection(header.Scope)
	p.report.Conflicts = append([]string{}, header.Conflicts...)
	p.report.FillIdentityVersion = header.FillIdentityVersion
	key := tradeScopeKey(header.Scope)
	query := `SELECT native_alias,record::text,record_sha256 FROM futures_trade_aliases WHERE scope_key=$1`
	args := []any{key}
	if !all {
		query += ` AND native_alias=ANY($2::text[])`
		args = append(args, aliases)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var alias string
		var raw, digest []byte
		var r tradeAliasRecord
		if err := rows.Scan(&alias, &raw, &digest); err != nil {
			rows.Close()
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if !bytes.Equal(sum[:], digest) || json.Unmarshal(raw, &r) != nil || (r.Owner != "" && alias == "") {
			rows.Close()
			return nil, ErrTradeCheckpoint
		}
		if r.Owner != "" {
			p.aliases[alias] = r.Owner
		}
		if r.Ambiguous {
			p.ambiguous[alias] = true
		}
		if r.HasTotal {
			p.totals[alias] = r.Total
		}
		if r.HasOverfilled {
			p.overfilled[alias] = r.Overfilled
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	query = `SELECT gateway_order_id,native_alias,record::text,record_sha256,blocked FROM futures_order_states WHERE scope_key=$1`
	args = []any{key}
	if !all {
		query += ` AND (native_alias=ANY($2::text[]) OR gateway_order_id=ANY($3::text[]))`
		args = append(args, aliases, orders)
	}
	rows, err = tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, alias string
		var raw, digest []byte
		var blocked bool
		var r OrderRecord
		if err := rows.Scan(&id, &alias, &raw, &digest, &blocked); err != nil {
			rows.Close()
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if !bytes.Equal(sum[:], digest) || json.Unmarshal(raw, &r) != nil || r.Order.GatewayOrderID != id || p.alias(r.Order.TradeRow) != alias || r.Blocked != blocked ||
			(alias != "" && p.aliases[alias] == "") {
			rows.Close()
			return nil, ErrTradeCheckpoint
		}
		p.report.Orders[id] = &r
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	query = `SELECT fill_id,native_alias,record::text,record_sha256,link_status,COALESCE(linked_gateway,''),eligible_gateway FROM futures_execution_facts WHERE scope_key=$1`
	args = []any{key}
	if !all {
		query += ` AND (native_alias=ANY($2::text[]) OR fill_id=ANY($3::text[]))`
		args = append(args, aliases, fills)
	}
	rows, err = tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, alias, status, linked, eligible string
		var raw, digest []byte
		var r ExecutionRecord
		if err := rows.Scan(&id, &alias, &raw, &digest, &status, &linked, &eligible); err != nil {
			rows.Close()
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if !bytes.Equal(sum[:], digest) || json.Unmarshal(raw, &r) != nil || r.Fill.FillID != id || p.alias(r.Fill.TradeRow) != alias || r.State != status || r.LinkedGateway != linked {
			rows.Close()
			return nil, ErrTradeCheckpoint
		}
		p.report.Executions[id] = &r
		if eligible != "" {
			p.eligible[id] = eligible
		}
		if r.State == "pending" {
			p.report.PendingCount++
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	p.report.Blocked = p.report.PendingCount > 0 || len(p.report.Conflicts) > 0
	if err := validateTradeIndexes(p); err != nil {
		return nil, err
	}
	return p, nil
}

func storeTradeEntities(ctx context.Context, tx *sql.Tx, key string, p *tradeProjection) error {
	orders, fills := map[string]any{}, map[string]any{}
	for id := range p.changedOrders {
		r := p.report.Orders[id]
		orders[id] = struct {
			Record *OrderRecord
			Alias  string
		}{r, p.alias(r.Order.TradeRow)}
	}
	for id := range p.changedFills {
		r := p.report.Executions[id]
		fills[id] = struct {
			Record          *ExecutionRecord
			Alias, Eligible string
		}{r, p.alias(r.Fill.TradeRow), p.eligible[id]}
	}
	o, _ := json.Marshal(orders)
	f, _ := json.Marshal(fills)
	a, _ := json.Marshal(entityAliases(p))
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_order_states(scope_key,gateway_order_id,record,blocked,native_alias,record_sha256)
        SELECT $1,key,value->'Record',(value->'Record'->>'Blocked')::boolean,value->>'Alias',sha256(convert_to((value->'Record')::text,'UTF8')) FROM jsonb_each($2::jsonb)
        ON CONFLICT(scope_key,gateway_order_id) DO UPDATE SET record=EXCLUDED.record,blocked=EXCLUDED.blocked,native_alias=EXCLUDED.native_alias,record_sha256=EXCLUDED.record_sha256`, key, o); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO futures_execution_facts(scope_key,fill_id,record,link_status,linked_gateway,native_alias,record_sha256,eligible_gateway)
        SELECT $1,key,value->'Record',value->'Record'->>'State',NULLIF(value->'Record'->>'LinkedGateway',''),value->>'Alias',sha256(convert_to((value->'Record')::text,'UTF8')),value->>'Eligible' FROM jsonb_each($2::jsonb)
        ON CONFLICT(scope_key,fill_id) DO UPDATE SET record=EXCLUDED.record,link_status=EXCLUDED.link_status,linked_gateway=EXCLUDED.linked_gateway,native_alias=EXCLUDED.native_alias,record_sha256=EXCLUDED.record_sha256,eligible_gateway=EXCLUDED.eligible_gateway`, key, f); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO futures_trade_aliases(scope_key,native_alias,record,record_sha256)
        SELECT $1,key,value,sha256(convert_to(value::text,'UTF8')) FROM jsonb_each($2::jsonb)
        ON CONFLICT(scope_key,native_alias) DO UPDATE SET record=EXCLUDED.record,record_sha256=EXCLUDED.record_sha256
        WHERE futures_trade_aliases.record IS DISTINCT FROM EXCLUDED.record`, key, a)
	return err
}
