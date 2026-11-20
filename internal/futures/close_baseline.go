package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// CloseBaseline is historical coverage, not current capacity or send permission.
// Absorbed contains only published, terminal closes observed before this query.
type CloseBaseline struct {
	PositionCommandID         int64           `json:"position_command_id"`
	BaselineSHA256            string          `json:"baseline_sha256"`
	PreviousPositionCommandID int64           `json:"previous_position_command_id"`
	PreviousEvidenceSHA256    string          `json:"previous_evidence_sha256,omitempty"`
	Absorbed                  map[int64]int64 `json:"absorbed"`
}

func writeCloseBaseline(ctx context.Context, tx *sql.Tx, key string, v CloseBaseline) error {
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > 262144 {
		return ErrCloseReservation
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO futures_close_baselines(bucket_key,position_command_id,evidence,evidence_sha256) VALUES($1,$2,$3,$4)`, key, v.PositionCommandID, raw, commandDigest(raw))
	return err
}

func readCloseBaseline(ctx context.Context, tx *sql.Tx, key string, id int64) (CloseBaseline, error) {
	var raw []byte
	var hash string
	var v CloseBaseline
	err := tx.QueryRowContext(ctx, `SELECT evidence,evidence_sha256 FROM futures_close_baselines WHERE bucket_key=$1 AND position_command_id=$2`, key, id).Scan(&raw, &hash)
	if err != nil || commandDigest(raw) != hash || json.Unmarshal(raw, &v) != nil || v.PositionCommandID != id || id <= 0 || len(v.BaselineSHA256) != 64 || v.Absorbed == nil || len(v.Absorbed) > 4096 {
		return v, fmt.Errorf("%w: invalid baseline history", ErrCloseReservation)
	}
	if v.PreviousPositionCommandID == 0 {
		if len(v.Absorbed) != 0 || v.PreviousEvidenceSHA256 != "" {
			return v, ErrCloseReservation
		}
	} else {
		if v.PreviousPositionCommandID >= id || v.PreviousPositionCommandID < 0 {
			return v, ErrCloseReservation
		}
		var previous []byte
		if err := tx.QueryRowContext(ctx, `SELECT evidence FROM futures_close_baselines WHERE bucket_key=$1 AND position_command_id=$2`, key, v.PreviousPositionCommandID).Scan(&previous); err != nil || commandDigest(previous) != v.PreviousEvidenceSHA256 {
			return v, ErrCloseReservation
		}
	}
	for command, filled := range v.Absorbed {
		if command <= 0 || filled < 0 {
			return v, ErrCloseReservation
		}
	}
	return v, nil
}

func closeBaselineHead(ctx context.Context, tx *sql.Tx, scope CommandScope, target TestQueryTarget, key string) (CloseBaseline, error) {
	var id int64
	var digest string
	var same bool
	scopeRaw, _ := json.Marshal(scope)
	targetRaw, _ := json.Marshal(target)
	err := tx.QueryRowContext(ctx, `SELECT position_command_id,baseline_sha256,scope_json=$2::jsonb AND target_json=$3::jsonb AND route_key=$4
		FROM futures_close_buckets WHERE bucket_key=$1 FOR UPDATE`, key, scopeRaw, targetRaw, string(commandRouteBytes(scope.Route))).Scan(&id, &digest, &same)
	if err != nil || !same {
		return CloseBaseline{}, ErrCloseReservation
	}
	v, err := readCloseBaseline(ctx, tx, key, id)
	if err != nil || v.BaselineSHA256 != digest {
		return CloseBaseline{}, ErrCloseReservation
	}
	return v, nil
}

// RebaseCFFEXClose explicitly advances a TEST bucket only after every known close
// has a published command and terminal facts preceding the new query. Active,
// unpublished, unknown, changed-freeze and cross-session cases are NOT inferred.
// Old reservations and reply bytes remain intact; no network or trading occurs.
func (s *CommandStore) RebaseCFFEXClose(ctx context.Context, scope CommandScope, target TestQueryTarget, symbol, side string, positionID int64) (CloseBaseline, error) {
	if scope.validate() != nil || scope.Route.Env != "test" || target.Validate() != nil || !native(symbol, 80, false) || !oneOf(side, "B", "S") || positionID <= 0 {
		return CloseBaseline{}, ErrCloseReservation
	}
	tx, err := beginFuturesTransaction(ctx, s.db)
	if err != nil {
		return CloseBaseline{}, err
	}
	defer tx.Rollback()
	// Registration takes route -> native bucket -> inbox -> trade journal locks.
	// Do not lock individual command rows here (publication owns that lock first).
	var route string
	if err := tx.QueryRowContext(ctx, `SELECT route_key FROM futures_command_routes WHERE route_key=$1 FOR UPDATE`, string(commandRouteBytes(scope.Route))).Scan(&route); err != nil {
		return CloseBaseline{}, err
	}
	o := SubmitIntent{TradeDate: target.TradeDate, Exchange: "CFFEX", Symbol: symbol, TradeSide: side, OffsetType: "C", HedgeType: "speculation"}
	key := closeBucketKey(scope, o)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ctp.close.bucket/"+key); err != nil {
		return CloseBaseline{}, err
	}
	head, err := closeBaselineHead(ctx, tx, scope, target, key)
	if err != nil {
		return CloseBaseline{}, err
	}
	if head.PositionCommandID == positionID {
		// Historical replay only, not a claim that this is still send-ready.
		return head, nil
	}
	if positionID < head.PositionCommandID {
		return CloseBaseline{}, ErrCloseReservation
	}
	old, err := loadCloseBaseline(ctx, tx, scope, target, head.PositionCommandID)
	if err != nil {
		return CloseBaseline{}, err
	}
	oldRaw, _ := json.Marshal(old)
	if commandDigest(oldRaw) != head.BaselineSHA256 {
		return CloseBaseline{}, ErrCloseReservation
	}
	next, err := loadCloseBaseline(ctx, tx, scope, target, positionID)
	if err != nil {
		return CloseBaseline{}, err
	}
	if err := checkLatestCloseBaseline(ctx, tx, scope, target, positionID); err != nil {
		return CloseBaseline{}, err
	}
	anchor := CommandRegistration{Scope: scope, Original: Command{SubmitCommand: SubmitCommand{Orders: []SubmitIntent{o}}}}
	commands, err := closeResponsibilities(ctx, tx, anchor, key)
	if err != nil {
		return CloseBaseline{}, err
	}
	report, err := lockedCloseReport(ctx, tx, scope, target, o, commands)
	if err != nil {
		return CloseBaseline{}, err
	}
	before, err := calculateCFFEXCloseCapacity(scope, target, old, symbol, side, commands, report, head.Absorbed)
	if err != nil || before.LocalReserved != 0 {
		return CloseBaseline{}, fmt.Errorf("%w: old capacity unresolved", ErrCloseReservation)
	}
	// Only absorb a snapshot requested AFTER all locally observed facts. This
	// deliberately refuses overlapping sampling; equal freeze totals are no proof.
	var ordered bool
	if err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM futures_trade_observations t WHERE t.scope_key=$1 AND t.received_at>=p.attempted_at)
		FROM futures_test_query_publications p WHERE p.command_id=$2`, tradeScopeKey(report.Scope), positionID).Scan(&ordered); err != nil || !ordered {
		return CloseBaseline{}, fmt.Errorf("%w: query overlaps trade observations", ErrCloseReservation)
	}
	query, err := readRegisteredCommand(ctx, tx, route, positionID)
	if err != nil {
		return CloseBaseline{}, err
	}
	envelope, err := parseReply(query.Original.Raw)
	if err != nil {
		return CloseBaseline{}, err
	}
	sent, err := time.Parse(time.RFC3339Nano, textField(envelope, "sent_at"))
	completed, _ := time.Parse(time.RFC3339Nano, next.ProducedAt)
	previous, _ := time.Parse(time.RFC3339Nano, old.ProducedAt)
	if err != nil || !sent.After(previous) || !completed.After(sent) {
		return CloseBaseline{}, ErrCloseReservation
	}
	for _, p := range next.Snapshot.Positions {
		captured, err := time.Parse(time.RFC3339Nano, p.CapturedAt)
		if err != nil || captured.Before(sent) {
			return CloseBaseline{}, ErrCloseReservation
		}
	}
	absorbed := make(map[int64]int64, len(commands))
	targetRaw, _ := json.Marshal(target)
	for _, c := range commands {
		filled, terminal, err := closeOrderProgress(c.Original.Orders[0], report)
		if err != nil || !terminal || report.Orders[c.Original.Orders[0].GatewayOrderID].Order.CounterSessionID != target.CounterSessionID {
			return CloseBaseline{}, fmt.Errorf("%w: nonterminal or foreign-session close", ErrCloseReservation)
		}
		var published bool
		var entry string
		if err := tx.QueryRowContext(ctx, `SELECT p.state='published' AND p.action='order.submit' AND p.target_json->'target'=$2::jsonb AND p.finished_at<q.attempted_at,COALESCE(p.entry_id,'')
			FROM futures_test_trade_publications p CROSS JOIN futures_test_query_publications q WHERE p.command_id=$1 AND q.command_id=$3`, c.CommandID, targetRaw, positionID).Scan(&published, &entry); err != nil || !published {
			return CloseBaseline{}, fmt.Errorf("%w: close publication unresolved", ErrCloseReservation)
		}
		if _, err := streamID(entry); err != nil || entry == "0-0" {
			return CloseBaseline{}, ErrCloseReservation
		}
		absorbed[c.CommandID] = filled
	}
	after, err := calculateCFFEXCloseCapacity(scope, target, next, symbol, side, commands, report, absorbed)
	if err != nil || after.Position != before.Position-before.ClosedSinceQuery || after.BrokerFrozen != before.BrokerFrozen || after.Today > before.Today || after.YesterdayRemaining > before.YesterdayRemaining {
		return CloseBaseline{}, fmt.Errorf("%w: position or freeze coverage mismatch", ErrCloseReservation)
	}
	nextRaw, _ := json.Marshal(next)
	headRaw, _ := json.Marshal(head)
	v := CloseBaseline{positionID, commandDigest(nextRaw), head.PositionCommandID, commandDigest(headRaw), absorbed}
	if err := writeCloseBaseline(ctx, tx, key, v); err != nil {
		return CloseBaseline{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE futures_close_buckets SET position_command_id=$2,baseline_sha256=$3 WHERE bucket_key=$1`, key, positionID, v.BaselineSHA256); err != nil {
		return CloseBaseline{}, err
	}
	if err := tx.Commit(); err != nil {
		return CloseBaseline{}, err
	}
	return v, nil
}

func lockedCloseReport(ctx context.Context, tx *sql.Tx, scope CommandScope, target TestQueryTarget, o SubmitIntent, commands []CommandRegistration) (TradeReport, error) {
	tradeScope := TradeScope{scope.Route, scope.NativeBrokerID, scope.InvestorID, target.TradeDate}
	if _, err := observeTradesTx(ctx, tx, tradeScope, nil); err != nil {
		return TradeReport{}, err
	}
	report, err := readTradeReport(ctx, tx, tradeScope)
	if err != nil {
		return report, err
	}
	return report, checkCloseFactCoverage(o, commands, report)
}

func checkLatestCloseBaseline(ctx context.Context, tx *sql.Tx, scope CommandScope, target TestQueryTarget, id int64) error {
	scopeRaw, _ := json.Marshal(scope)
	targetRaw, _ := json.Marshal(target)
	var newer bool
	// A later returned position query must be reconciled, even if it failed.
	// Inbox locks held by the caller serialize this check with reply projection.
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM futures_reply_results r JOIN futures_commands c USING(command_id)
		JOIN futures_test_query_publications p USING(command_id)
		CROSS JOIN futures_test_query_publications baseline
		WHERE baseline.command_id=$1 AND p.command_id<>$1 AND p.attempted_at>=baseline.attempted_at
		AND c.scope_json=$2::jsonb AND r.target_json=$3::jsonb
		AND convert_from(c.body,'UTF8')::jsonb->>'action'='account.positions.query')`, id, scopeRaw, targetRaw).Scan(&newer)
	if err != nil {
		return err
	}
	if newer {
		return fmt.Errorf("%w: newer position response requires reconciliation", ErrCloseReservation)
	}
	return nil
}

// Called in the SAME transaction as publication-attempt insertion. A standalone
// capacity read must not become a reusable permission or release an unknown send.
func recheckClosePublication(ctx context.Context, tx *sql.Tx, c CommandRegistration, target TestQueryTarget) error {
	if c.Original.Action != "order.submit" || len(c.Original.Orders) != 1 {
		for _, o := range c.Original.Orders {
			if o.Exchange == "CFFEX" && o.OffsetType != "O" {
				return ErrCloseReservation
			}
		}
		return nil
	}
	o := c.Original.Orders[0]
	if o.Exchange != "CFFEX" || o.OffsetType == "O" {
		return nil
	}
	if o.OffsetType != "C" {
		return ErrCloseReservation
	}
	key := closeBucketKey(c.Scope, o)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ctp.close.bucket/"+key); err != nil {
		return err
	}
	r, err := readCloseReservation(ctx, tx, c)
	if err != nil {
		return err
	}
	head, err := closeBaselineHead(ctx, tx, c.Scope, target, key)
	if err != nil || r.PositionCommandID != head.PositionCommandID || r.Target != target {
		return ErrCloseReservation
	}
	reply, err := loadCloseBaseline(ctx, tx, c.Scope, target, head.PositionCommandID)
	if err != nil {
		return err
	}
	if err := checkLatestCloseBaseline(ctx, tx, c.Scope, target, head.PositionCommandID); err != nil {
		return err
	}
	raw, _ := json.Marshal(reply)
	if commandDigest(raw) != head.BaselineSHA256 {
		return ErrCloseReservation
	}
	commands, err := closeResponsibilities(ctx, tx, c, key)
	if err != nil {
		return err
	}
	commands = append(commands, c)
	report, err := lockedCloseReport(ctx, tx, c.Scope, target, o, commands)
	if err != nil {
		return err
	}
	if report.Orders[o.GatewayOrderID] != nil {
		return fmt.Errorf("%w: execution already observed before first publication", ErrCloseReservation)
	}
	_, err = calculateCFFEXCloseCapacity(c.Scope, target, reply, o.Symbol, o.TradeSide, commands, report, head.Absorbed)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCloseReservation, err)
	}
	return nil
}
