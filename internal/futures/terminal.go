package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// TerminalManifest is operator-controlled observation configuration. Reviews
// contain independent request evidence, not context inferred from a reply.
type TerminalManifest struct {
	Accounts []TerminalAccountConfig `json:"accounts"`
	Reviews  []TerminalReview        `json:"reviews"`
}

type TerminalAccountConfig struct {
	Alias           string                   `json:"alias"`
	Scope           CommandScope             `json:"scope"`
	HistoryGateways []string                 `json:"history_gateways"`
	DeploymentID    string                   `json:"deployment_id"`
	SourceID        string                   `json:"source_id"`
	ServerRunID     string                   `json:"server_run_id"`
	Epoch           string                   `json:"epoch"`
	Execution       *TerminalExecutionConfig `json:"execution,omitempty"`
}

// Optional server-side policy. Merely adding this to an observation manifest
// does not enable an API or change the terminal's read-only controls.
type TerminalExecutionConfig struct {
	SubmitEnabled          bool                  `json:"submit_enabled"`
	CancelEnabled          bool                  `json:"cancel_enabled"`
	AuthorizationExpiresAt string                `json:"authorization_expires_at"`
	CommandGroup           string                `json:"command_group"`
	CFFEX                  *CFFEXExecutionPolicy `json:"cffex,omitempty"`
}

type TerminalReview struct {
	Review       json.RawMessage `json:"review"`
	EvidenceFile string          `json:"evidence_file"`
}

type TerminalReader struct {
	db       *sql.DB
	manifest TerminalManifest
	sources  []*TestStreamSource
	reviews  map[int64]QueryRequestReview
}

type TerminalAccount struct {
	AccountID       string                 `json:"account_id"`
	Alias           string                 `json:"alias"`
	Route           QueryRoute             `json:"route"`
	TradingEnabled  bool                   `json:"trading_enabled"`
	CancelEnabled   bool                   `json:"cancel_enabled"`
	Heartbeat       *AdvertisedHeartbeat   `json:"heartbeat"`
	ConnectionState string                 `json:"connection_state"`
	ConnectionError string                 `json:"connection_error,omitempty"`
	Queries         []TerminalQuery        `json:"queries"`
	LedgerError     string                 `json:"ledger_error,omitempty"`
	LocalOrders     []OrderRow             `json:"local_orders"`
	LocalOrderError string                 `json:"local_order_error,omitempty"`
	Execution       *TerminalExecutionView `json:"execution,omitempty"`
	Capture         *CaptureHealthView     `json:"capture,omitempty"`
}

type TerminalQuery struct {
	Action            string        `json:"action"`
	MessageID         string        `json:"message_id"`
	GatewayID         string        `json:"gateway_id"`
	SessionID         string        `json:"counter_session_id"`
	TradeDate         string        `json:"trade_date"`
	RequestedAt       time.Time     `json:"requested_at"`
	ProducedAt        string        `json:"produced_at,omitempty"`
	State             string        `json:"state"`
	Code              string        `json:"code,omitempty"`
	Message           string        `json:"message,omitempty"`
	Reviewed          bool          `json:"reviewed"`
	ResponseValidated bool          `json:"response_validated"`
	ResponsePersisted bool          `json:"response_persisted"`
	BusinessPublished bool          `json:"business_published"`
	Assets            []AssetRow    `json:"assets,omitempty"`
	Positions         []PositionRow `json:"positions,omitempty"`
	Orders            []OrderRow    `json:"orders,omitempty"`
	Fills             []FillRow     `json:"fills,omitempty"`
	RowCount          *int          `json:"row_count"`
}

func readTerminalFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("terminal evidence size/read failure")
	}
	return b, nil
}

func NewTerminalReader(db *sql.DB, redisURL, manifestFile string) (*TerminalReader, error) {
	raw, err := readTerminalFile(manifestFile, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("terminal manifest unavailable")
	}
	r := &TerminalReader{db: db, reviews: map[int64]QueryRequestReview{}}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&r.manifest); err != nil {
		return nil, fmt.Errorf("terminal manifest invalid")
	}
	if d.Decode(new(any)) != io.EOF || db == nil || len(r.manifest.Accounts) == 0 || len(r.manifest.Accounts) > 16 || len(r.manifest.Reviews) > 256 {
		return nil, ErrStreamIdentity
	}
	seen := map[string]bool{}
	for _, a := range r.manifest.Accounts {
		if seen[a.Scope.Route.AccountID] || a.Scope.validate() != nil || a.Scope.Route.Env != "test" || len(a.HistoryGateways) > 16 || a.ServerRunID == "" || a.Epoch == "" {
			r.Close()
			return nil, ErrStreamIdentity
		}
		seen[a.Scope.Route.AccountID] = true
		for _, gateway := range a.HistoryGateways {
			if !validID(gateway) || len(gateway) > 128 {
				r.Close()
				return nil, ErrStreamIdentity
			}
		}
		source, err := NewTestStreamSource(TestSourceConfig{Scope: a.Scope, DeploymentID: a.DeploymentID, RedisURL: redisURL})
		if err != nil {
			r.Close()
			return nil, err
		}
		r.sources = append(r.sources, source)
		if source.String() != a.SourceID {
			r.Close()
			return nil, ErrStreamIdentity
		}
	}
	for _, item := range r.manifest.Reviews {
		evidence, err := readTerminalFile(item.EvidenceFile, 262144)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("terminal review evidence unavailable")
		}
		review, err := DecodeQueryRequestReview(item.Review, evidence)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("terminal request review invalid")
		}
		if _, exists := r.reviews[review.CommandID]; exists {
			r.Close()
			return nil, ErrBatchIdentity
		}
		r.reviews[review.CommandID] = review
	}
	return r, nil
}

func (r *TerminalReader) Close() {
	for _, source := range r.sources {
		_ = source.Close()
	}
}

func (r *TerminalReader) Snapshot(ctx context.Context) []TerminalAccount {
	return r.snapshot(ctx, true)
}

// Heartbeats observes the same bound source without loading historical query ledgers.
func (r *TerminalReader) Heartbeats(ctx context.Context) []TerminalAccount {
	return r.snapshot(ctx, false)
}

func (r *TerminalReader) snapshot(ctx context.Context, includeQueries bool) []TerminalAccount {
	out := make([]TerminalAccount, 0, len(r.manifest.Accounts))
	for i, a := range r.manifest.Accounts {
		item := TerminalAccount{AccountID: a.Scope.Route.AccountID, Alias: a.Alias, Route: a.Scope.Route, Queries: []TerminalQuery{}}
		hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		item.Heartbeat, item.ConnectionState, item.ConnectionError = r.connection(hctx, i)
		cancel()
		if a.Execution != nil {
			cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
			t := TestQueryTarget{SourceID: a.SourceID, ServerRunID: a.ServerRunID, Epoch: a.Epoch, Group: a.Execution.CommandGroup}
			if h := item.Heartbeat; h != nil && h.TradeDate != nil && h.ConnectionGeneration != nil {
				t.CounterSessionID = h.SessionID
				t.TradeDate = *h.TradeDate
				t.ConnectionGeneration = *h.ConnectionGeneration
			}
			v, _, _ := readCaptureHealth(cctx, r.db, a.Scope, t)
			item.Capture = &v
			ccancel()
		}
		if !includeQueries {
			out = append(out, item)
			continue
		}
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		queries, err := r.queries(qctx, a)
		cancel()
		if err != nil {
			item.LedgerError = "observation_store_unavailable"
		} else {
			item.Queries = queries
		}
		ectx, ecancel := context.WithTimeout(ctx, 3*time.Second)
		item.LocalOrders, err = r.localOrders(ectx, a)
		ecancel()
		if err != nil {
			item.LocalOrders = nil
			item.LocalOrderError = "local_order_observation_invalid"
		}
		out = append(out, item)
	}
	return out
}

func (r *TerminalReader) connection(ctx context.Context, index int) (*AdvertisedHeartbeat, string, string) {
	a := r.manifest.Accounts[index]
	client := r.sources[index].reader.client
	prefix := strings.TrimSuffix(queryReplyStream(a.Scope.Route), "reply")
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		return nil, "unavailable", "redis_unavailable"
	}
	if !strings.Contains("\n"+info, "\nrun_id:"+a.ServerRunID+"\r\n") {
		return nil, "unavailable", "source_changed"
	}
	epoch, err := client.Get(ctx, prefix+"cmd.trade:transport-epoch").Result()
	if err != nil || epoch != a.Epoch {
		return nil, "unavailable", "epoch_missing_or_changed"
	}
	rows, err := client.XRevRangeN(ctx, prefix+"hb", "+", "-", 1).Result()
	if err != nil || len(rows) != 1 {
		return nil, "unavailable", "heartbeat_missing"
	}
	raw, ok := rows[0].Values["body"].(string)
	if !ok {
		return nil, "unavailable", "heartbeat_invalid"
	}
	h, err := DecodeHeartbeat([]byte(raw), a.Scope.Route)
	if err != nil || !h.readOnlyNativeScopeMatches(a.Scope) || !h.assessmentNativeScopeMatches(a.Scope) {
		return nil, "unavailable", "heartbeat_invalid"
	}
	// This is a display status only. No code path publishes commands or grants readiness.
	state := "not_ready"
	age := time.Since(h.ProducedAt)
	if h.StateText == "stopping" || h.State == "STOPPED" {
		state = "stopped"
	} else if age < 0 || age > 30*time.Second {
		state = "stale"
	} else if h.BrokerReady && h.ReadOnly != nil && h.ReadOnly.QueryCollectionReady {
		state = "read_only"
	} else if h.Assessment != nil {
		if l := h.Assessment.Lifecycle; l != nil && l.Phase != "active" {
			state = l.Phase
		}
		_, ready := h.queryCollection()
		if ready && (h.Assessment.relayExecution() || time.Now().Before(h.Assessment.ExpiresAt)) {
			state = "assessment"
			if h.Assessment.relayExecution() {
				state = "execution"
			}
		}
	}
	var day *string
	if h.TradeDate != "" {
		day = &h.TradeDate
	}
	view := &AdvertisedHeartbeat{SessionID: h.CounterSessionID, TradeDate: day, ProducedAt: h.ProducedAt, AgeMilliseconds: age.Milliseconds(), State: h.State, StateText: h.StateText, RedisReady: h.RedisReady, BrokerReady: h.BrokerReady, Operations: h.Operations}
	if h.ReadOnly != nil {
		view.ReadOnlyReconciliation = h.ReadOnly.Reconciliation
		view.ConnectionGeneration = h.ReadOnly.ConnectionGeneration
		view.QueryCollectionReady = &h.ReadOnly.QueryCollectionReady
		view.RecoveryClear = &h.ReadOnly.TransportRecoveryClear
		view.RuntimeMode = "test_broker_read_only"
	} else if h.Assessment != nil {
		view.ProcessLifecycle, view.PermissionProfile = h.Assessment.Lifecycle, h.Assessment.PermissionProfile
		view.AuthorizationOwner = h.Assessment.AuthorizationOwner
		if h.Assessment.relayExecution() {
			view.ResidentDurationSeconds = &h.Assessment.ResidentDurationSeconds
			view.ResourceUsage, view.RuntimeBlockedReason = h.Assessment.ResourceUsage, h.Assessment.RuntimeBlockedReason
		}
		if h.Assessment.OrderSnapshotScope != "" {
			view.OrderSnapshotScope = h.Assessment.OrderSnapshotScope
			view.NativeOrderSnapshotReady, view.OrderProjectionReady = &h.Assessment.NativeOrderSnapshotReady, &h.Assessment.OrderProjectionReady
		}
		g, ready := h.queryCollection()
		view.AssessmentLegacySource = h.Assessment.LegacySource
		view.ConnectionGeneration, view.QueryCollectionReady = g, &ready
		view.RecoveryClear = &h.Assessment.TransportRecoveryClear
		view.RuntimeMode = "test_broker_assessment"
		view.SettlementConfirmed, view.OrderSnapshotReady = h.SettlementConfirmed, h.OrderSnapshotReady
		view.AcceptingTradeCommands, view.AcceptingCancelCommands = h.AcceptingTradeCommands, h.AcceptingCancelCommands
	}
	return view, state, ""
}

type terminalCommand struct {
	publicationState, publicationReason string
	id                                  int64
	scope                               CommandScope
	command                             Command
	target                              TestQueryTarget
	at                                  time.Time
}

func (r *TerminalReader) queries(ctx context.Context, a TerminalAccountConfig) ([]TerminalQuery, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	commands := []terminalCommand{}
	for _, gateway := range append([]string{a.Scope.Route.GatewayID}, a.HistoryGateways...) {
		scope := a.Scope
		scope.Route.GatewayID = gateway
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT ON (convert_from(c.body,'UTF8')::jsonb->>'action') c.command_id,c.body,c.body_sha256,p.target_json,p.attempted_at,c.scope_json,p.state,p.reason FROM futures_commands c JOIN futures_test_query_publications p USING(command_id) WHERE c.route_key=$1 ORDER BY convert_from(c.body,'UTF8')::jsonb->>'action',c.command_id DESC`, string(commandRouteBytes(scope.Route)))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			c := terminalCommand{scope: scope}
			var body, target, registeredScope []byte
			var digest string
			if err = rows.Scan(&c.id, &body, &digest, &target, &c.at, &registeredScope, &c.publicationState, &c.publicationReason); err != nil {
				rows.Close()
				return nil, err
			}
			var actualScope CommandScope
			if json.Unmarshal(registeredScope, &actualScope) != nil || actualScope != scope {
				rows.Close()
				return nil, ErrBatchIdentity
			}
			if commandDigest(body) != digest {
				rows.Close()
				return nil, ErrBatchIdentity
			}
			c.command, err = DecodeCommand(body, scope, "cmd.query")
			if err != nil {
				rows.Close()
				return nil, err
			}
			c.target, err = DecodeTestQueryTarget(target)
			if err != nil {
				rows.Close()
				return nil, err
			}
			commands = append(commands, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	latest := map[string]terminalCommand{}
	for _, c := range commands {
		if old, ok := latest[c.command.Action]; !ok || c.at.After(old.at) {
			latest[c.command.Action] = c
		}
	}
	result := []TerminalQuery{}
	for _, action := range []string{"account.asset.query", "account.positions.query", "order.list.query", "fill.list.query"} {
		c, ok := latest[action]
		if !ok {
			result = append(result, TerminalQuery{Action: action, State: "not_queried"})
			continue
		}
		view := TerminalQuery{Action: action, MessageID: c.command.MessageID, GatewayID: c.scope.Route.GatewayID, SessionID: c.target.CounterSessionID, TradeDate: c.target.TradeDate, RequestedAt: c.at, State: "awaiting_reply"}
		if c.publicationState != "published" {
			view.State = "publication_unknown"
			if c.publicationState == "not_published" {
				view.State = "not_published"
			}
			view.Message = c.publicationReason
			result = append(result, view)
			continue
		}
		stored, _, _, err := readReplyResult(ctx, tx, c.id, c.target)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			if stored.Action != c.command.Action {
				return nil, ErrBatchIdentity
			}
			view.State, view.Code, view.Message, view.ProducedAt = stored.State, stored.Code, stored.Message, stored.ProducedAt
			view.ResponseValidated, view.ResponsePersisted = stored.ResponseValidated, true
			if stored.State == "ready" && stored.Snapshot != nil {
				s := stored.Snapshot
				view.State = "response_ready"
				view.Assets, view.Positions, view.Orders, view.Fills = s.Assets, s.Positions, s.Orders, s.Fills
				n := len(s.Assets) + len(s.Positions) + len(s.Orders) + len(s.Fills)
				view.RowCount = &n
			}
			result = append(result, view)
			continue
		}
		pages := [][]byte{}
		rows, err := tx.QueryContext(ctx, `SELECT i.body,i.body_sha256,i.state,r.config_json FROM futures_stream_inbox i JOIN futures_stream_receivers r USING(stream_key) WHERE i.stream_key=$1 AND convert_from(i.body,'UTF8')::jsonb->>'origin_message_id'=$2 ORDER BY i.receipt_id LIMIT 257`, queryReplyStream(c.scope.Route), c.command.MessageID)
		if err != nil {
			return nil, err
		}
		count, total := 0, 0
		for rows.Next() {
			count++
			var body, digest, receiverJSON []byte
			var disposition string
			if err = rows.Scan(&body, &digest, &disposition, &receiverJSON); err != nil {
				rows.Close()
				return nil, err
			}
			var receiver StreamConfig
			if json.Unmarshal(receiverJSON, &receiver) != nil || receiver.Scope != c.scope || receiver.Role != "reply" || receiver.SourceID != c.target.SourceID {
				rows.Close()
				return nil, ErrStreamIdentity
			}
			total += len(body)
			if count > 256 || total > 8<<20 {
				rows.Close()
				return nil, ErrAuditCapacity
			}
			if disposition == "quarantined" {
				rows.Close()
				return nil, ErrBatchIdentity
			}
			sum := sha256.Sum256(body)
			if !bytes.Equal(sum[:], digest) {
				rows.Close()
				return nil, ErrBatchIdentity
			}
			obj, err := parseReply(body)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if textField(obj, "origin_message_id") == c.command.MessageID {
				pages = append(pages, body)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if err := r.renderObservation(&view, c, pages); err != nil {
			view.State = "invalid"
			view.ResponseValidated = false
			view.Message = err.Error()
			view.RowCount = nil
			view.Assets = nil
			view.Positions = nil
			view.Orders = nil
			view.Fills = nil
		}
		result = append(result, view)
	}
	return result, tx.Commit()
}

func (r *TerminalReader) renderObservation(v *TerminalQuery, c terminalCommand, pages [][]byte) error {
	review, reviewed := r.reviews[c.id]
	var batch *QueryBatch
	if reviewed {
		qc := review.Query
		if review.Target != c.target || qc.commandScope() != c.scope || qc.Action != c.command.Action || qc.OriginMessageID != c.command.MessageID {
			return ErrBatchIdentity
		}
		qc.Command = &QueryCommandBinding{c.id, c.command.RequestID, c.command.CorrelationID, c.command.IdempotencyKey}
		var err error
		// Offline presentation of retained evidence. Never register/publish/ACK it
		// or reopen the expired live receive window to manufacture readiness.
		batch, err = NewQueryBatch(qc, 0, time.Second, DefaultBatchLimits())
		if err != nil {
			return err
		}
		v.Reviewed = true
	} else if c.command.Action == "account.positions.query" && len(pages) > 0 {
		// Display a complete, correlated response without claiming independent
		// SDK audit or publishing a business snapshot. Session/route/day come
		// from our persisted command, never from the returned rows.
		if c.publicationState != "published" {
			return ErrBatchIdentity
		}
		var err error
		batch, err = terminalPositionBatch(c, pages[0])
		if err != nil {
			return err
		}
	}
	for _, body := range pages {
		obj, err := parseReply(body)
		if err != nil {
			return err
		}
		var route QueryRoute
		if json.Unmarshal(obj["routing"], &route) != nil || route != c.scope.Route || textField(obj, "action") != c.command.Action || textField(obj, "request_id") != c.command.RequestID || textField(obj, "correlation_id") != c.command.CorrelationID || textField(obj, "idempotency_key") != c.command.IdempotencyKey {
			return ErrBatchIdentity
		}
		if textField(obj, "protocol") != "relay.stream.v1" || textField(obj, "message_type") != "reply" {
			return ErrBatchIdentity
		}
		var payload, adapter, evidence rawObject
		if json.Unmarshal(obj["payload"], &payload) != nil || json.Unmarshal(obj["adapter_context"], &adapter) != nil || json.Unmarshal(adapter["query_evidence"], &evidence) != nil {
			return ErrBatchIdentity
		}
		generation, err := nullableInteger(evidence["connection_generation"])
		if err != nil || generation == nil || *generation != c.target.ConnectionGeneration || textField(evidence, "counter_session_id") != c.target.CounterSessionID || textField(payload, "trade_date") != c.target.TradeDate || textField(payload, "product_type") != "futures" || textField(payload, "schema_version") != "ctp.f0.draft.2" || textField(obj, "result_type") != queryResultType(c.command.Action) || !validTime(textField(obj, "produced_at")) || !oneOf(textField(obj, "status"), "partial", "completed", "failed") {
			return ErrBatchIdentity
		}
		v.ProducedAt = textField(obj, "produced_at")
		v.Code = textField(obj, "code")
		v.Message = textField(obj, "message")
		if batch == nil {
			v.State = "pending_review"
			if textField(obj, "status") == "failed" {
				v.State = "failed"
			}
			continue
		}
		if _, err = batch.Accept(body, 0); err != nil {
			return err
		}
	}
	if batch == nil {
		return nil
	}
	state, reason := batch.State()
	v.State = string(state)
	if state == BatchFailed {
		if v.Message == "" {
			v.Message = reason
		}
		return nil
	}
	if state != BatchReady {
		return nil
	}
	candidate, err := batch.Candidate()
	if err != nil {
		return err
	}
	v.Assets = candidate.Assets
	v.Positions = candidate.Positions
	v.Orders = candidate.Orders
	v.Fills = candidate.Fills
	n := len(v.Assets) + len(v.Positions) + len(v.Orders) + len(v.Fills)
	v.RowCount = &n
	if !reviewed {
		v.State, v.ResponseValidated = "response_ready", true
	}
	return nil
}

func terminalPositionBatch(c terminalCommand, raw []byte) (*QueryBatch, error) {
	obj, err := parseReply(raw)
	if err != nil {
		return nil, err
	}
	var adapter, evidence rawObject
	if json.Unmarshal(obj["adapter_context"], &adapter) != nil || json.Unmarshal(adapter["query_evidence"], &evidence) != nil {
		return nil, ErrBatchIdentity
	}
	requestID, err := nullableInteger(evidence["sdk_request_id"])
	if err != nil {
		return nil, err
	}
	qc := QueryContext{
		Route: c.scope.Route, Action: c.command.Action, OriginMessageID: c.command.MessageID,
		CounterSessionID: c.target.CounterSessionID, ConnectionGeneration: &c.target.ConnectionGeneration,
		TradeDate: c.target.TradeDate, NativeBrokerID: c.scope.NativeBrokerID,
		NativeInvestorID: c.scope.InvestorID, NativeUserID: c.scope.UserID,
		Command: &QueryCommandBinding{c.id, c.command.RequestID, c.command.CorrelationID, c.command.IdempotencyKey},
		Ticket:  textField(evidence, "ticket"), SDKRequestID: requestID,
	}
	// Ticket/SDK ID here only enforce consistency across response pages. This
	// ephemeral batch must never be registered as independently reviewed input.
	return NewQueryBatch(qc, 0, time.Second, DefaultBatchLimits())
}
