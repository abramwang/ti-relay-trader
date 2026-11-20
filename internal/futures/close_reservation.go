package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

var ErrCloseReservation = errors.New("futures close reservation unavailable")

// CloseReservation records capacity at registration, NOT current available
// quantity or permission to publish. Terminal facts are applied on the next
// calculation; the original reservation/evidence is never erased or rewritten.
type CloseReservation struct {
	CommandID         int64           `json:"command_id"`
	PositionCommandID int64           `json:"position_command_id"`
	Scope             CommandScope    `json:"scope"`
	Target            TestQueryTarget `json:"target"`
	BaselineSHA256    string          `json:"baseline_sha256"`
	Quantity          int64           `json:"quantity"`
	Before            CloseCapacity   `json:"before"`
}

type CloseRegistration struct {
	Command     CommandRegistration
	Reservation *CloseReservation
}

func closeBucketKey(scope CommandScope, o SubmitIntent) string {
	// Gateway/login aliases must not obtain independent capacity for the same
	// native investor. The first reservation pins the exact route and session.
	return commandDigest(framed("ctp.close.bucket.v1/", scope.Route.Env, scope.NativeBrokerID,
		scope.InvestorID, o.TradeDate, o.Exchange, o.Symbol, o.TradeSide, o.HedgeType))
}

// RegisterCFFEXClose atomically registers one ordinary C intent and its quantity
// reservation. It reads all relevant persisted intentions and trade facts, not
// caller-supplied counts. No Redis, broker I/O or publication occurs here.
//
// Initial scope is TEST, one pinned position reply/session/day and speculative
// CFFEX positions without investment units. Unmanaged closes/external activity
// fail closed. Snapshot replacement requires RebaseCFFEXClose; cross-session adoption, fresh market and
// permission checks belong to the eventual send path; this is not that path.
func (s *CommandStore) RegisterCFFEXClose(ctx context.Context, scope CommandScope, raw []byte, evidence RegistrationEvidence, target TestQueryTarget, positionID int64) (CloseRegistration, error) {
	c, err := DecodeCommand(raw, scope, "cmd.trade")
	if err != nil {
		return CloseRegistration{}, err
	}
	if scope.Route.Env != "test" || c.Action != "order.submit" || len(c.Orders) != 1 {
		return CloseRegistration{}, ErrCloseReservation
	}
	o := c.Orders[0]
	if o.Exchange != "CFFEX" || o.OffsetType != "C" || o.HedgeType != "speculation" {
		return CloseRegistration{}, ErrCloseReservation
	}
	var reservation *CloseReservation
	reg, err := s.registerAtomic(ctx, scope, "cmd.trade", raw, evidence, nil, func(tx *sql.Tx, reg CommandRegistration) error {
		if reg.Outcome == "replayed" {
			// Resolve an old responsibility before looking at today's snapshot,
			// target or permissions. This never acquires another send attempt.
			reservation, err = readCloseReservation(ctx, tx, reg)
			return err
		}
		if reg.Outcome != "registered" {
			return nil
		}
		if target.Validate() != nil || target.TradeDate != o.TradeDate || positionID <= 0 {
			return ErrCloseReservation
		}
		reservation, err = reserveCFFEXClose(ctx, tx, reg, target, positionID)
		return err
	})
	if err != nil {
		return CloseRegistration{}, err
	}
	return CloseRegistration{reg, reservation}, nil
}

func reserveCFFEXClose(ctx context.Context, tx *sql.Tx, reg CommandRegistration, target TestQueryTarget, positionID int64) (*CloseReservation, error) {
	scope, o := reg.Scope, reg.Original.Orders[0]
	key, route := closeBucketKey(scope, o), string(commandRouteBytes(scope.Route))
	// Cross-route contenders lock the native bucket before pinning a baseline.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ctp.close.bucket/"+key); err != nil {
		return nil, err
	}
	reply, err := loadCloseBaseline(ctx, tx, scope, target, positionID)
	if err != nil {
		return nil, err
	}
	if err := checkLatestCloseBaseline(ctx, tx, scope, target, positionID); err != nil {
		return nil, err
	}
	baseline, _ := json.Marshal(reply)
	digest := commandDigest(baseline)
	scopeRaw, _ := json.Marshal(scope)
	targetRaw, _ := json.Marshal(target)
	inserted, err := tx.ExecContext(ctx, `INSERT INTO futures_close_buckets(bucket_key,route_key,scope_json,target_json,position_command_id,baseline_sha256)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, key, route, scopeRaw, targetRaw, positionID, digest)
	if err != nil {
		return nil, err
	}
	if n, err := inserted.RowsAffected(); err != nil {
		return nil, err
	} else if n == 1 {
		if err := writeCloseBaseline(ctx, tx, key, CloseBaseline{PositionCommandID: positionID, BaselineSHA256: digest, Absorbed: map[int64]int64{}}); err != nil {
			return nil, err
		}
	}
	var same bool
	if err := tx.QueryRowContext(ctx, `SELECT route_key=$2 AND scope_json=$3::jsonb AND target_json=$4::jsonb AND position_command_id=$5 AND baseline_sha256=$6
		FROM futures_close_buckets WHERE bucket_key=$1 FOR UPDATE`, key, route, scopeRaw, targetRaw, positionID, digest).Scan(&same); err != nil {
		return nil, err
	}
	if !same {
		return nil, fmt.Errorf("%w: baseline or route changed; reconcile coverage first", ErrCloseReservation)
	}
	version, err := readCloseBaseline(ctx, tx, key, positionID)
	if err != nil || version.BaselineSHA256 != digest {
		return nil, fmt.Errorf("%w: baseline history mismatch", ErrCloseReservation)
	}
	commands, err := closeResponsibilities(ctx, tx, reg, key)
	if err != nil {
		return nil, err
	}
	// Replay and lock the existing bounded trade journal. No observations are
	// fabricated; an absent local journal alone does not prove a broker empty.
	report, err := lockedCloseReport(ctx, tx, scope, target, o, commands)
	if err != nil {
		return nil, err
	}
	before, err := calculateCFFEXCloseCapacity(scope, target, reply, o.Symbol, o.TradeSide, commands, report, version.Absorbed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCloseReservation, err)
	}
	if _, err := calculateCFFEXCloseCapacity(scope, target, reply, o.Symbol, o.TradeSide, append(commands, reg), report, version.Absorbed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCloseReservation, err)
	}
	out := &CloseReservation{reg.CommandID, positionID, scope, target, digest, int64(o.OrderQty), before}
	raw, _ := json.Marshal(out)
	_, err = tx.ExecContext(ctx, `INSERT INTO futures_close_reservations(command_id,bucket_key,evidence,evidence_sha256) VALUES($1,$2,$3,$4)`, reg.CommandID, key, raw, commandDigest(raw))
	return out, err
}

func readCloseReservation(ctx context.Context, tx *sql.Tx, reg CommandRegistration) (*CloseReservation, error) {
	var raw []byte
	var hash, key string
	var same bool
	scopeRaw, _ := json.Marshal(reg.Scope)
	err := tx.QueryRowContext(ctx, `SELECT r.evidence,r.evidence_sha256,r.bucket_key,
		b.scope_json=$2::jsonb AND b.route_key=$3
		AND b.target_json=convert_from(r.evidence,'UTF8')::jsonb->'target'
		FROM futures_close_reservations r JOIN futures_close_buckets b USING(bucket_key) WHERE r.command_id=$1`, reg.CommandID, scopeRaw, string(commandRouteBytes(reg.Scope.Route))).Scan(&raw, &hash, &key, &same)
	if err != nil {
		return nil, fmt.Errorf("%w: missing or unreadable reservation: %v", ErrCloseReservation, err)
	}
	var out CloseReservation
	if len(reg.Original.Orders) != 1 || len(raw) > 65536 || commandDigest(raw) != hash || json.Unmarshal(raw, &out) != nil || !same {
		return nil, ErrCloseReservation
	}
	o := reg.Original.Orders[0]
	if key != closeBucketKey(reg.Scope, o) || out.CommandID != reg.CommandID || out.Scope != reg.Scope || out.Target.Validate() != nil || out.Target.TradeDate != o.TradeDate || out.Quantity != int64(o.OrderQty) || out.Quantity <= 0 || out.Before.Available < out.Quantity {
		return nil, ErrCloseReservation
	}
	version, err := readCloseBaseline(ctx, tx, key, out.PositionCommandID)
	if err != nil || version.BaselineSHA256 != out.BaselineSHA256 {
		return nil, ErrCloseReservation
	}
	return &out, nil
}

func loadCloseBaseline(ctx context.Context, tx *sql.Tx, scope CommandScope, target TestQueryTarget, id int64) (ReplyResult, error) {
	bad := func() (ReplyResult, error) { return ReplyResult{}, ErrCloseReservation }
	q, err := readRegisteredCommand(ctx, tx, string(commandRouteBytes(scope.Route)), id)
	if err != nil || q.Scope != scope || q.Original.Action != "account.positions.query" {
		return bad()
	}
	// Lock inbox writers/projectors while selecting facts. Unprocessed input
	// cannot be silently omitted. These locks do not claim real-time broker
	// coverage or remove the need for fresh capture checks at publication.
	streams := []string{queryReplyStream(scope.Route), eventStreamForRoute(scope.Route)}
	rows, err := tx.QueryContext(ctx, `SELECT r.stream_key,r.config_json,r.audit_count,r.audit_bytes,
		(SELECT count(*) FROM futures_stream_inbox i WHERE i.stream_key=r.stream_key),
		(SELECT COALESCE(sum(octet_length(body)),0) FROM futures_stream_inbox i WHERE i.stream_key=r.stream_key)
		FROM futures_stream_receivers r WHERE r.stream_key=ANY($1::text[]) ORDER BY r.stream_key FOR UPDATE`, streams)
	if err != nil {
		return ReplyResult{}, err
	}
	count := 0
	for rows.Next() {
		var key string
		var raw []byte
		var expectedCount, size, actualCount, actualSize int64
		var config StreamConfig
		if err := rows.Scan(&key, &raw, &expectedCount, &size, &actualCount, &actualSize); err != nil {
			rows.Close()
			return ReplyResult{}, err
		}
		if expectedCount != actualCount || size != actualSize || json.Unmarshal(raw, &config) != nil || config.Scope != scope || config.SourceID != target.SourceID || (key == streams[0] && config.Role != "reply") || (key == streams[1] && config.Role != "event") {
			rows.Close()
			return bad()
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ReplyResult{}, err
	}
	if count != 2 {
		return bad()
	}
	var pending, same bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_stream_inbox WHERE stream_key=ANY($1::text[]) AND state<>'delivered')`, streams).Scan(&pending); err != nil || pending {
		return bad()
	}
	transport, _ := json.Marshal(struct{ SourceID, RunID, Epoch, Group string }{target.SourceID, target.ServerRunID, target.Epoch, target.Group})
	if err := tx.QueryRowContext(ctx, `SELECT transport_json=$2::jsonb FROM futures_test_reply_sources WHERE stream_key=$1`, streams[0], transport).Scan(&same); err != nil || !same {
		return bad()
	}
	targetRaw, _ := json.Marshal(target)
	if err := tx.QueryRowContext(ctx, `SELECT state='published' AND target_json=$2::jsonb FROM futures_test_query_publications WHERE command_id=$1`, id, targetRaw).Scan(&same); err != nil || !same {
		return bad()
	}
	result, count, size, err := readReplyResult(ctx, tx, id, target)
	if err != nil || result == nil || result.State != "ready" || result.Snapshot == nil || !result.ResponseValidated {
		return bad()
	}
	pages, err := replyResultInputs(ctx, tx, streams[0], id, count, size)
	if err != nil {
		return ReplyResult{}, err
	}
	derived := assembleReplyResult(q, target, pages)
	if !reflect.DeepEqual(*result, derived) {
		return bad()
	}
	return derived, nil
}

func closeResponsibilities(ctx context.Context, tx *sql.Tx, current CommandRegistration, key string) ([]CommandRegistration, error) {
	// Reuse the bounded command registry, not a second mutable quantity counter.
	// Include other gateways for the same investor to detect unmanaged history.
	scopeFilter, _ := json.Marshal(map[string]any{"NativeBrokerID": current.Scope.NativeBrokerID, "InvestorID": current.Scope.InvestorID, "Route": map[string]string{"env": current.Scope.Route.Env}})
	rows, err := tx.QueryContext(ctx, `SELECT route_key,command_id FROM futures_commands WHERE stream_suffix='cmd.trade' AND scope_json @> $1::jsonb ORDER BY command_id LIMIT 4097`, scopeFilter)
	if err != nil {
		return nil, err
	}
	type ref struct {
		route string
		id    int64
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.route, &r.id); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(refs) > 4096 {
		return nil, ErrAuditCapacity
	}
	var out []CommandRegistration
	o := current.Original.Orders[0]
	for _, r := range refs {
		if r.id == current.CommandID {
			continue
		}
		c, err := readRegisteredCommand(ctx, tx, r.route, r.id)
		if err != nil {
			return nil, err
		}
		for _, other := range c.Original.Orders {
			if !sameCloseBucket(o, other.TradeDate, other.Symbol, other.Exchange, other.TradeSide, other.HedgeType, other.OffsetType) {
				continue
			}
			if c.Scope != current.Scope || c.Original.Action != "order.submit" || len(c.Original.Orders) != 1 || other.OffsetType != "C" {
				return nil, fmt.Errorf("%w: unmanaged close intention", ErrCloseReservation)
			}
			if _, err := readCloseReservation(ctx, tx, c); err != nil {
				return nil, err
			}
			if closeBucketKey(c.Scope, other) != key {
				return nil, ErrCloseReservation
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func sameCloseBucket(o SubmitIntent, day, symbol, exchange, side, hedge, offset string) bool {
	return day == o.TradeDate && symbol == o.Symbol && exchange == o.Exchange && side == o.TradeSide && hedge == o.HedgeType && offset != "O"
}

func checkCloseFactCoverage(o SubmitIntent, commands []CommandRegistration, report TradeReport) error {
	known := map[string]bool{}
	for _, c := range commands {
		known[c.Original.Orders[0].GatewayOrderID] = true
	}
	matches := func(r TradeRow) bool {
		return sameCloseBucket(o, r.TradeDate, r.Symbol, r.Exchange, r.TradeSide, r.HedgeType, r.OffsetType)
	}
	for id, r := range report.Orders {
		if r == nil || (matches(r.Order.TradeRow) && !known[id]) {
			return fmt.Errorf("%w: unmanaged close order facts", ErrCloseReservation)
		}
	}
	for _, r := range report.Executions {
		if r == nil || (matches(r.Fill.TradeRow) && !known[r.LinkedGateway]) {
			return fmt.Errorf("%w: unmanaged close execution facts", ErrCloseReservation)
		}
	}
	return nil
}
