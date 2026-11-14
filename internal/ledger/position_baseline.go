package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

const PositionBaselineMaxRows = 100000
const positionBaselineMaxReplies = 250000

var ErrPositionBaselineChanged = errors.New("position baseline changed; restart from the first page")
var ErrPositionBaselineTooLarge = errors.New("position baseline exceeds the explicit size limit")

type PositionBaselineQuery struct {
	Environment, AccountID, BrokerID, GatewayID, TradeDate, SnapshotType, BaselineID, Cursor string
	Limit                                                                                    int
}

type PositionBaselineRow struct {
	SecurityID      string `json:"security_id"`
	Symbol          string `json:"symbol"`
	Exchange        string `json:"exchange"`
	Quantity        int64  `json:"quantity"`
	SellableQty     int64  `json:"sellable_qty"`
	SourceUpdatedAt string `json:"source_updated_at"`
}

type PositionBaseline struct {
	SchemaVersion            string          `json:"schema_version"`
	BaselineID               string          `json:"baseline_id"`
	Environment              string          `json:"environment"`
	AccountID                string          `json:"account_id"`
	BrokerID                 string          `json:"broker_id"`
	GatewayID                string          `json:"gateway_id"`
	TradeDate                string          `json:"trade_date"`
	SnapshotType             string          `json:"snapshot_type"`
	Completeness             string          `json:"completeness"`
	MissingSecurityMeansZero bool            `json:"missing_security_means_zero"`
	ExecutionReady           bool            `json:"execution_ready"`
	TotalRows                int             `json:"total_rows"`
	MaxRows                  int             `json:"max_rows"`
	ContentSHA256            string          `json:"content_sha256"`
	DigestFormat             string          `json:"digest_format"`
	SourceRunID              string          `json:"source_run_id,omitempty"`
	ClosePublicationSource   string          `json:"close_publication_source,omitempty"`
	CaptureRunID             string          `json:"capture_run_id,omitempty"`
	PositionQueryMessageID   string          `json:"position_query_message_id,omitempty"`
	AssetQueryMessageID      string          `json:"asset_query_message_id,omitempty"`
	CounterSessionID         string          `json:"counter_session_id,omitempty"`
	SourceBrokerAccountID    string          `json:"source_broker_account_id,omitempty"`
	CollectionStartedAt      string          `json:"collection_started_at,omitempty"`
	CollectionCompletedAt    string          `json:"collection_completed_at,omitempty"`
	BusinessAsOf             string          `json:"business_as_of,omitempty"`
	CashObservedAt           string          `json:"cash_observed_at,omitempty"`
	GeneratedAt              string          `json:"generated_at,omitempty"`
	Proof                    map[string]bool `json:"proof"`
	QualityFlags             []string        `json:"quality_flags"`
}

type PositionBaselinePage struct {
	Query        map[string]any        `json:"query"`
	Baseline     PositionBaseline      `json:"baseline"`
	Positions    []PositionBaselineRow `json:"positions"`
	Count        int                   `json:"count"`
	Offset       int                   `json:"offset"`
	NextCursor   string                `json:"next_cursor"`
	PageComplete bool                  `json:"page_complete"`
}

type baselineSnapshotRow struct {
	Symbol, Exchange      string
	Quantity, SellableQty int64
	CapturedAt            time.Time
	Raw                   struct {
		RunID             string `json:"run_id"`
		InputSnapshotType string `json:"input_snapshot_type"`
	}
}

type baselineSnapshot struct {
	Exists     bool
	CapturedAt time.Time
	Raw        struct {
		RunID             string         `json:"run_id"`
		InputSnapshotType string         `json:"input_snapshot_type"`
		Source            string         `json:"source"`
		Asset             *trading.Asset `json:"asset"`
		BrokerAsset       *trading.Asset `json:"broker_asset"`
		ReverseRepo       struct {
			CashObservedAt time.Time `json:"cash_observed_at"`
		} `json:"reverse_repo"`
	}
	Rows []baselineSnapshotRow
}

type baselineSnapshotResult struct {
	RunID             string   `json:"run_id"`
	SnapshotType      string   `json:"snapshot_type"`
	InputSnapshotType string   `json:"input_snapshot_type"`
	CompletedAt       string   `json:"completed_at"`
	TradeDate         string   `json:"trade_date"`
	Source            string   `json:"source"`
	Status            string   `json:"status"`
	DryRun            *bool    `json:"dry_run"`
	SnapshotOnly      *bool    `json:"snapshot_only"`
	Errors            []string `json:"errors"`
	AccountErrors     *int     `json:"account_error_count"`
	Accounts          []struct {
		AccountID    string         `json:"account_id"`
		Asset        *trading.Asset `json:"asset"`
		AssetWritten bool           `json:"asset_snapshot_written"`
		Count        *int           `json:"positions_count"`
		Written      *int           `json:"position_snapshots_written"`
		Errors       []string       `json:"errors"`
	} `json:"accounts"`
}

type baselineQueryTerminal struct {
	AccountID     string `json:"account_id"`
	Action        string `json:"action"`
	Success       bool   `json:"success"`
	Terminal      bool   `json:"terminal"`
	Contradictory bool   `json:"contradictory"`
	TerminalCount int    `json:"terminal_count"`
	ReplyCount    int    `json:"reply_count"`
	MessageID     string `json:"origin_message_id"`
}

type baselineJobReport struct {
	RecoveryPublication *baselineSnapshotResult `json:"-"`
	Settlement          struct {
		OK     bool                   `json:"ok"`
		Result baselineSnapshotResult `json:"result"`
	} `json:"settlement_snapshot"`
	Capture struct {
		OK     bool                   `json:"ok"`
		Result baselineSnapshotResult `json:"result"`
	} `json:"broker_close_snapshot"`
	Accounts []struct {
		AccountID string   `json:"account_id"`
		Errors    []string `json:"errors"`
		Freshness struct {
			OK             bool                                             `json:"ok"`
			AssetFresh     bool                                             `json:"asset_fresh"`
			PositionsFresh bool                                             `json:"positions_fresh"`
			StartedAt      string                                           `json:"refresh_started_at"`
			Terminals      struct{ Asset, Positions baselineQueryTerminal } `json:"query_terminals"`
		} `json:"refresh_freshness"`
	} `json:"accounts"`
}

type baselineRawReply struct {
	Body       []byte
	ParseError string
	ReceivedAt time.Time
}

type baselineReplyEnvelope struct {
	Protocol         string    `json:"protocol"`
	MessageID        string    `json:"message_id"`
	OriginMessageID  string    `json:"origin_message_id"`
	MessageType      string    `json:"message_type"`
	Action           string    `json:"action"`
	ResultType       string    `json:"result_type"`
	Status           string    `json:"status"`
	ProducedAt       time.Time `json:"produced_at"`
	CounterSessionID string    `json:"counter_session_id"`
	Route            struct {
		Env       string `json:"env"`
		AccountID string `json:"account_id"`
		BrokerID  string `json:"broker_id"`
		GatewayID string `json:"gateway_id"`
	} `json:"routing"`
	Chunk struct {
		IsLast bool `json:"is_last"`
	} `json:"chunk"`
	Payload struct {
		Items            *[]trading.Position `json:"items"`
		Account          *trading.Asset      `json:"account"`
		CounterSessionID string              `json:"counter_session_id"`
	} `json:"payload"`
	AdapterContext struct {
		CounterSessionID string `json:"counter_session_id"`
	} `json:"adapter_context"`
}

func (repo *Repository) GetPositionBaseline(ctx context.Context, query PositionBaselineQuery) (PositionBaselinePage, error) {
	if repo == nil || repo.exec == nil {
		return PositionBaselinePage{}, fmt.Errorf("%w: baseline repository unavailable", ErrInvalidLedgerInput)
	}
	if query.AccountID == "" || query.BrokerID == "" || query.GatewayID == "" || (query.Environment != "production" && query.Environment != "test") {
		return PositionBaselinePage{}, fmt.Errorf("%w: explicit baseline scope required", ErrInvalidLedgerInput)
	}
	date, err := normalizeTradeDate(query.TradeDate)
	if err != nil || date == "" || query.SnapshotType != "close" || query.Limit < 1 || query.Limit > 500 {
		return PositionBaselinePage{}, fmt.Errorf("%w: close trade_date and limit 1..500 required", ErrInvalidLedgerInput)
	}
	query.TradeDate = date
	if starter, ok := repo.exec.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	}); ok {
		tx, err := starter.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return PositionBaselinePage{}, err
		}
		defer tx.Rollback()
		return NewRepository(tx).readPositionBaseline(ctx, query)
	}
	if _, ok := repo.exec.(*sql.Tx); !ok {
		return PositionBaselinePage{}, fmt.Errorf("baseline requires a database transaction")
	}
	return repo.readPositionBaseline(ctx, query)
}

func (repo *Repository) readPositionBaseline(ctx context.Context, query PositionBaselineQuery) (PositionBaselinePage, error) {
	q, err := repo.queryer()
	if err != nil {
		return PositionBaselinePage{}, err
	}
	closeSnapshot, err := loadBaselineSnapshot(ctx, q, query, "close")
	if err != nil {
		return PositionBaselinePage{}, err
	}
	brokerSnapshot, err := loadBaselineSnapshot(ctx, q, query, "broker_close")
	if err != nil {
		return PositionBaselinePage{}, err
	}
	closeJob, err := loadBaselineJob(ctx, q, query.TradeDate, "settlement_snapshot", closeSnapshot.Raw.RunID)
	if err != nil {
		return PositionBaselinePage{}, err
	}
	if closeJob.Settlement.Result.RunID == "" && closeSnapshot.Raw.RunID != "" {
		closeJob.RecoveryPublication, err = loadBaselineRecoveryPublication(ctx, q, query, closeSnapshot)
		if err != nil {
			return PositionBaselinePage{}, err
		}
	}
	captureJob, err := loadBaselineJob(ctx, q, query.TradeDate, "broker_close_snapshot", brokerSnapshot.Raw.RunID)
	if err != nil {
		return PositionBaselinePage{}, err
	}
	var terminal baselineQueryTerminal
	var assetTerminal baselineQueryTerminal
	for _, account := range captureJob.Accounts {
		if account.AccountID == query.AccountID {
			terminal = account.Freshness.Terminals.Positions
			assetTerminal = account.Freshness.Terminals.Asset
		}
	}
	var replies []baselineRawReply
	var assetReplies []baselineRawReply
	if terminal.MessageID != "" {
		replies, err = loadBaselineReplies(ctx, q, query.AccountID, terminal.MessageID)
		if err != nil {
			return PositionBaselinePage{}, err
		}
	}
	if assetTerminal.MessageID != "" {
		assetReplies, err = loadBaselineReplies(ctx, q, query.AccountID, assetTerminal.MessageID)
		if err != nil {
			return PositionBaselinePage{}, err
		}
	}
	page, err := assemblePositionBaseline(query, closeSnapshot, brokerSnapshot, closeJob, captureJob, replies, assetReplies)
	if err != nil {
		return PositionBaselinePage{}, err
	}
	return paginatePositionBaseline(page, query)
}

func loadBaselineSnapshot(ctx context.Context, q Queryer, query PositionBaselineQuery, kind string) (baselineSnapshot, error) {
	out := baselineSnapshot{Rows: []baselineSnapshotRow{}}
	rows, err := q.QueryContext(ctx, `SELECT captured_at, raw_payload FROM asset_snapshots WHERE account_id=$1 AND trade_date=$2::date AND snapshot_type=$3`, query.AccountID, query.TradeDate, kind)
	if err != nil {
		return out, err
	}
	if rows.Next() {
		var raw []byte
		if err = rows.Scan(&out.CapturedAt, &raw); err == nil {
			err = json.Unmarshal(raw, &out.Raw)
		}
		out.Exists = err == nil
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = q.QueryContext(ctx, `SELECT symbol,exchange,quantity,sellable_qty,captured_at,raw_payload FROM position_snapshots WHERE account_id=$1 AND trade_date=$2::date AND snapshot_type=$3 ORDER BY symbol,exchange LIMIT $4`, query.AccountID, query.TradeDate, kind, PositionBaselineMaxRows+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var row baselineSnapshotRow
		var raw []byte
		if err := rows.Scan(&row.Symbol, &row.Exchange, &row.Quantity, &row.SellableQty, &row.CapturedAt, &raw); err != nil {
			return out, err
		}
		if err := json.Unmarshal(raw, &row.Raw); err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, row)
	}
	if len(out.Rows) > PositionBaselineMaxRows {
		return out, ErrPositionBaselineTooLarge
	}
	return out, rows.Err()
}

func loadBaselineJob(ctx context.Context, q Queryer, date, key, runID string) (baselineJobReport, error) {
	var out baselineJobReport
	if runID == "" {
		return out, nil
	}
	job := "post_close_capture"
	if key == "settlement_snapshot" {
		job = "post_close_settlement"
	}
	rows, err := q.QueryContext(ctx, `SELECT report_json FROM job_runs WHERE trade_date=$1::date AND finished_at IS NOT NULL AND report_json->$2->'result'->>'run_id'=$3 AND job_name=$4 ORDER BY updated_at DESC LIMIT 1`, date, key, runID, job)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	if rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return out, err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return out, err
		}
	}
	return out, rows.Err()
}

func loadBaselineReplies(ctx context.Context, q Queryer, accountID, messageID string) ([]baselineRawReply, error) {
	rows, err := q.QueryContext(ctx, `SELECT body, COALESCE(parse_error,''),received_at FROM raw_stream_messages WHERE account_id=$1 AND origin_message_id=$2 AND stream_role='reply' ORDER BY received_at,raw_message_pk LIMIT $3`, accountID, messageID, positionBaselineMaxReplies+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []baselineRawReply
	for rows.Next() {
		var row baselineRawReply
		if err := rows.Scan(&row.Body, &row.ParseError, &row.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if len(out) > positionBaselineMaxReplies {
		return nil, ErrPositionBaselineTooLarge
	}
	return out, rows.Err()
}

// Snapshot publication also occurs through the settlement API outside cron.
// Reuse its persisted completion record, never synthesize a successful job.
func loadBaselineRecoveryPublication(ctx context.Context, q Queryer, query PositionBaselineQuery, snapshot baselineSnapshot) (*baselineSnapshotResult, error) {
	rows, err := q.QueryContext(ctx, `SELECT summary,source,completed_at FROM reconciliation_runs WHERE run_id=$1 AND trade_date=$2::date AND status='completed' AND completed_at IS NOT NULL AND COALESCE(error_message,'')=''`, snapshot.Raw.RunID, query.TradeDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result *baselineSnapshotResult
	for rows.Next() {
		if result != nil {
			return nil, fmt.Errorf("duplicate snapshot publication")
		}
		var raw []byte
		var source string
		var completed time.Time
		if err := rows.Scan(&raw, &source, &completed); err != nil {
			return nil, err
		}
		var candidate baselineSnapshotResult
		if err := json.Unmarshal(raw, &candidate); err != nil {
			return nil, err
		}
		if !validBaselineRecoveryPublication(query, snapshot, candidate, source, completed) {
			return nil, nil
		}
		candidate.CompletedAt = baselineTime(completed)
		result = &candidate
	}
	return result, rows.Err()
}

func validBaselineRecoveryPublication(query PositionBaselineQuery, snapshot baselineSnapshot, r baselineSnapshotResult, source string, completed time.Time) bool {
	if !snapshot.Exists || r.RunID == "" || r.RunID != snapshot.Raw.RunID || r.TradeDate != query.TradeDate || source == "" || source != snapshot.Raw.Source || r.Source != source || r.Status != "completed" || r.SnapshotType != "close" || r.InputSnapshotType != "broker_close" || r.DryRun == nil || *r.DryRun || r.SnapshotOnly == nil || *r.SnapshotOnly || r.AccountErrors == nil || *r.AccountErrors != 0 || len(r.Errors) != 0 || completed.IsZero() || completed.Before(snapshot.CapturedAt) {
		return false
	}
	matched := 0
	for _, a := range r.Accounts {
		if a.AccountID != query.AccountID {
			continue
		}
		matched++
		if !a.AssetWritten || len(a.Errors) != 0 || a.Count == nil || a.Written == nil || *a.Count != len(snapshot.Rows) || *a.Written != len(snapshot.Rows) || a.Asset == nil || a.Asset.AccountID != query.AccountID || !baselineAssetFactEqual(a.Asset, snapshot.Raw.Asset) {
			return false
		}
	}
	return matched == 1
}

// Compare the original cash projection, not the derived close valuation. The
// archive projection uses routing.account_id and produced_at just as ingestion.
func baselineAssetFactEqual(a, b *trading.Asset) bool {
	return a != nil && b != nil && a.AccountID != "" && a.AccountID == b.AccountID && !a.UpdatedAt.IsZero() && a.UpdatedAt.Equal(b.UpdatedAt) &&
		a.CashAvailable == b.CashAvailable && a.CashTotal == b.CashTotal && a.NetAsset == b.NetAsset && a.MarketValue == b.MarketValue && a.StockValue == b.StockValue && a.FundValue == b.FundValue && a.ReverseRepoReceivable == b.ReverseRepoReceivable && a.Commission == b.Commission && a.Credit == b.Credit
}

func baselineCashObservationLinked(query PositionBaselineQuery, close, broker baselineSnapshot, archived *trading.Asset, started time.Time) bool {
	source := broker.Raw.Asset
	if source == nil {
		source = broker.Raw.BrokerAsset
	}
	if !close.Exists || !broker.Exists || close.Raw.InputSnapshotType != "broker_close" || source == nil || source.AccountID != query.AccountID || !baselineAssetFactEqual(source, close.Raw.BrokerAsset) || !baselineAssetFactEqual(source, archived) || close.Raw.Asset == nil || close.Raw.Asset.AccountID != query.AccountID || !close.Raw.Asset.UpdatedAt.Equal(source.UpdatedAt) || !close.CapturedAt.Equal(source.UpdatedAt) || !close.Raw.ReverseRepo.CashObservedAt.Equal(source.UpdatedAt) || started.IsZero() || source.UpdatedAt.After(broker.CapturedAt) {
		return false
	}
	at := source.UpdatedAt.In(timeutil.Location())
	return at.Format("2006-01-02") == query.TradeDate && (query.Environment != "production" || at.Hour() >= 15)
}

func assemblePositionBaseline(query PositionBaselineQuery, closeSnapshot, broker baselineSnapshot, closeJob, captureJob baselineJobReport, replies, assetReplies []baselineRawReply) (PositionBaselinePage, error) {
	if len(closeSnapshot.Rows) > PositionBaselineMaxRows || len(broker.Rows) > PositionBaselineMaxRows || len(replies) > positionBaselineMaxReplies {
		return PositionBaselinePage{}, ErrPositionBaselineTooLarge
	}
	meta := PositionBaseline{SchemaVersion: "position_baseline.v1", Environment: query.Environment, AccountID: query.AccountID, BrokerID: query.BrokerID, GatewayID: query.GatewayID, TradeDate: query.TradeDate, SnapshotType: "close", Completeness: "partial", MaxRows: PositionBaselineMaxRows, DigestFormat: "position_baseline_rows.v1", Proof: map[string]bool{}, QualityFlags: []string{}, SourceRunID: closeSnapshot.Raw.RunID, CaptureRunID: broker.Raw.RunID}
	out := PositionBaselinePage{Positions: []PositionBaselineRow{}}
	fail := func(flag string) { meta.QualityFlags = append(meta.QualityFlags, flag) }
	meta.BusinessAsOf = baselineTime(broker.CapturedAt)
	if !closeSnapshot.Exists {
		fail("close_asset_missing")
		if len(closeSnapshot.Rows) == 0 {
			meta.Completeness = "unavailable"
		}
	}
	if !broker.Exists {
		fail("broker_capture_missing")
	}
	if broker.Exists {
		asOf := broker.CapturedAt.In(timeutil.Location())
		if asOf.Format("2006-01-02") != query.TradeDate || (query.Environment == "production" && asOf.Hour() < 15) {
			fail("historical_close_business_time_unproven")
		}
	}
	checkPublished := func(result baselineSnapshotResult, snapshot baselineSnapshot, kind string) bool {
		completedAt, timeErr := time.Parse(time.RFC3339Nano, result.CompletedAt)
		if result.RunID == "" || result.RunID != snapshot.Raw.RunID || result.SnapshotType != kind || timeErr != nil || completedAt.Before(snapshot.CapturedAt) {
			return false
		}
		for _, a := range result.Accounts {
			if a.AccountID == query.AccountID {
				return a.AssetWritten && len(a.Errors) == 0 && a.Count != nil && a.Written != nil && *a.Count == len(snapshot.Rows) && *a.Written == len(snapshot.Rows)
			}
		}
		return false
	}
	meta.Proof["close_snapshot_published"] = checkPublished(closeJob.Settlement.Result, closeSnapshot, "close") && closeJob.Settlement.Result.InputSnapshotType == "broker_close"
	publication := closeJob.Settlement.Result
	if meta.Proof["close_snapshot_published"] {
		meta.ClosePublicationSource = "job_run"
	}
	if closeJob.Settlement.Result.RunID == "" && closeJob.RecoveryPublication != nil {
		r := *closeJob.RecoveryPublication
		completed, _ := time.Parse(time.RFC3339Nano, r.CompletedAt)
		if validBaselineRecoveryPublication(query, closeSnapshot, r, closeSnapshot.Raw.Source, completed) && checkPublished(r, closeSnapshot, "close") {
			meta.Proof["close_snapshot_published"] = true
			meta.ClosePublicationSource = "reconciliation_run"
			publication = r
		}
	}
	meta.Proof["broker_snapshot_published"] = checkPublished(captureJob.Capture.Result, broker, "broker_close")
	if !meta.Proof["close_snapshot_published"] || !meta.Proof["broker_snapshot_published"] {
		fail("snapshot_publication_unproven")
	}
	meta.GeneratedAt = publication.CompletedAt
	var positionsTerminal baselineQueryTerminal
	var assetTerminal baselineQueryTerminal
	for _, account := range captureJob.Accounts {
		if account.AccountID != query.AccountID {
			continue
		}
		f := account.Freshness
		positionsTerminal = f.Terminals.Positions
		assetTerminal = f.Terminals.Asset
		meta.CollectionStartedAt = f.StartedAt
		meta.PositionQueryMessageID, meta.AssetQueryMessageID = f.Terminals.Positions.MessageID, f.Terminals.Asset.MessageID
		validTerminal := func(t baselineQueryTerminal, action string) bool {
			return t.AccountID == query.AccountID && t.Action == action && t.Success && t.Terminal && !t.Contradictory && t.TerminalCount == 1 && t.MessageID != "" && t.ReplyCount > 0
		}
		meta.Proof["asset_query_completed"] = f.OK && f.AssetFresh && len(account.Errors) == 0 && validTerminal(f.Terminals.Asset, "account.asset.query")
		meta.Proof["positions_query_completed"] = f.OK && f.PositionsFresh && len(account.Errors) == 0 && validTerminal(f.Terminals.Positions, "account.positions.query")
	}
	if !meta.Proof["asset_query_completed"] || !meta.Proof["positions_query_completed"] {
		fail("account_query_incomplete")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, meta.CollectionStartedAt)
	if err != nil || startedAt.After(broker.CapturedAt) {
		fail("collection_time_unproven")
	}
	expected := make(map[string]PositionBaselineRow)
	seen := make(map[string]bool)
	seenMessages := make(map[string]bool)
	finals := 0
	missingSessions := 0
	archiveValid := len(replies) > 0 && len(replies) == positionsTerminal.ReplyCount
	wireEnv := query.Environment
	if wireEnv == "production" {
		wireEnv = "prod"
	}
	for index, raw := range replies {
		var reply baselineReplyEnvelope
		if raw.ParseError != "" || json.Unmarshal(raw.Body, &reply) != nil {
			archiveValid = false
			continue
		}
		if reply.Protocol != "relay.stream.v1" || reply.MessageType != "reply" || reply.Action != "account.positions.query" || reply.ResultType != "position_page" || reply.OriginMessageID != meta.PositionQueryMessageID || reply.Route.AccountID != query.AccountID || reply.Route.BrokerID != query.BrokerID || reply.Route.GatewayID != query.GatewayID || reply.Route.Env != wireEnv || reply.MessageID == "" || seenMessages[reply.MessageID] {
			archiveValid = false
		}
		seenMessages[reply.MessageID] = true
		if raw.ReceivedAt.Before(startedAt) || raw.ReceivedAt.After(broker.CapturedAt) || reply.ProducedAt.IsZero() {
			archiveValid = false
		}
		if reply.Status == "completed" && reply.Chunk.IsLast {
			finals++
			meta.CollectionCompletedAt = baselineTime(raw.ReceivedAt)
			if index != len(replies)-1 {
				archiveValid = false
			}
		} else if reply.Status != "partial" || reply.Chunk.IsLast {
			archiveValid = false
		}
		frameSession := false
		for _, sid := range []string{reply.CounterSessionID, reply.Payload.CounterSessionID, reply.AdapterContext.CounterSessionID} {
			if sid == "" {
				continue
			}
			frameSession = true
			if meta.CounterSessionID != "" && meta.CounterSessionID != sid {
				archiveValid = false
			}
			meta.CounterSessionID = sid
		}
		if !frameSession {
			missingSessions++
		}
		if reply.Payload.Items == nil {
			archiveValid = false
			continue
		}
		for _, position := range *reply.Payload.Items {
			key := position.Symbol + "." + string(position.Exchange)
			if position.AccountID != query.AccountID || position.Symbol == "" || !position.Exchange.Valid() || position.Quantity < 0 || position.SellableQty < 0 || seen[key] {
				archiveValid = false
			}
			seen[key] = true
			if position.Quantity > 0 {
				expected[key] = PositionBaselineRow{SecurityID: key, Symbol: position.Symbol, Exchange: string(position.Exchange), Quantity: position.Quantity, SellableQty: position.SellableQty, SourceUpdatedAt: baselineTime(reply.ProducedAt)}
			}
			if len(expected) > PositionBaselineMaxRows {
				return out, ErrPositionBaselineTooLarge
			}
		}
	}
	meta.Proof["archive_pages_closed"] = archiveValid && finals == 1
	if !meta.Proof["archive_pages_closed"] {
		fail("query_archive_incomplete_or_inconsistent")
	}
	assetArchiveValid := len(assetReplies) == 1 && assetTerminal.ReplyCount == 1
	var archivedAsset *trading.Asset
	for _, raw := range assetReplies {
		var reply baselineReplyEnvelope
		if raw.ParseError != "" || json.Unmarshal(raw.Body, &reply) != nil {
			assetArchiveValid = false
			continue
		}
		if reply.Protocol != "relay.stream.v1" || reply.MessageType != "reply" || reply.Action != "account.asset.query" || reply.ResultType != "asset_page" || reply.OriginMessageID != meta.AssetQueryMessageID || reply.Route.AccountID != query.AccountID || reply.Route.BrokerID != query.BrokerID || reply.Route.GatewayID != query.GatewayID || reply.Route.Env != wireEnv || reply.MessageID == "" || reply.Status != "completed" || !reply.Chunk.IsLast || reply.Payload.Account == nil || raw.ReceivedAt.Before(startedAt) || raw.ReceivedAt.After(broker.CapturedAt) || reply.ProducedAt.IsZero() {
			assetArchiveValid = false
		}
		if reply.Payload.Account != nil {
			meta.SourceBrokerAccountID = reply.Payload.Account.AccountID
			projection := *reply.Payload.Account
			projection.AccountID = reply.Route.AccountID
			projection.UpdatedAt = reply.ProducedAt
			projection.ReverseRepoReceivable = 0
			// Ledger monetary columns are NUMERIC(20,6). OC's binary-double
			// serialization can carry extra tail digits; compare at stored precision.
			for _, value := range []*float64{&projection.CashAvailable, &projection.CashTotal, &projection.NetAsset, &projection.MarketValue, &projection.StockValue, &projection.FundValue, &projection.Commission, &projection.Credit} {
				*value = math.Round(*value*1_000_000) / 1_000_000
			}
			archivedAsset = &projection
		}
		if assetArchiveValid {
			completed, _ := time.Parse(time.RFC3339Nano, meta.CollectionCompletedAt)
			if raw.ReceivedAt.After(completed) {
				meta.CollectionCompletedAt = baselineTime(raw.ReceivedAt)
			}
		}
		frameSession := false
		for _, sid := range []string{reply.CounterSessionID, reply.Payload.CounterSessionID, reply.AdapterContext.CounterSessionID} {
			frameSession = frameSession || sid != ""
			if sid != "" && meta.CounterSessionID != "" && sid != meta.CounterSessionID {
				assetArchiveValid = false
			}
			if sid != "" && meta.CounterSessionID == "" {
				meta.CounterSessionID = sid
			}
		}
		if !frameSession {
			missingSessions++
		}
	}
	if meta.CounterSessionID != "" && missingSessions > 0 {
		fail("counter_session_coverage_incomplete")
	}
	meta.Proof["asset_archive_closed"] = assetArchiveValid
	if !assetArchiveValid {
		fail("asset_query_archive_incomplete_or_inconsistent")
	}
	cashLinked := assetArchiveValid && baselineCashObservationLinked(query, closeSnapshot, broker, archivedAsset, startedAt)
	meta.Proof["cash_observation_time_linked"] = cashLinked
	meta.Proof["source_generation_verified"] = closeSnapshot.Raw.InputSnapshotType == "broker_close" && (closeSnapshot.CapturedAt.Equal(broker.CapturedAt) || cashLinked)
	if cashLinked {
		meta.CashObservedAt = baselineTime(closeSnapshot.CapturedAt)
	}
	if !meta.Proof["source_generation_verified"] {
		fail("snapshot_source_generation_mismatch")
	}
	matchSnapshot := func(snapshot baselineSnapshot) bool {
		if len(snapshot.Rows) != len(expected) {
			return false
		}
		identities := map[string]bool{}
		for _, row := range snapshot.Rows {
			identity := row.Symbol + "." + row.Exchange
			if identities[identity] {
				return false
			}
			identities[identity] = true
			p, ok := expected[row.Symbol+"."+row.Exchange]
			if !ok || row.Quantity != p.Quantity || row.SellableQty != p.SellableQty || row.Raw.RunID != snapshot.Raw.RunID || !row.CapturedAt.Equal(snapshot.CapturedAt) {
				return false
			}
			if snapshot.Raw.InputSnapshotType != row.Raw.InputSnapshotType {
				return false
			}
		}
		return true
	}
	meta.Proof["snapshot_matches_full_query"] = matchSnapshot(closeSnapshot) && matchSnapshot(broker)
	meta.Proof["stale_rows_excluded"] = meta.Proof["snapshot_matches_full_query"] && meta.Proof["archive_pages_closed"]
	if !meta.Proof["snapshot_matches_full_query"] {
		fail("snapshot_rows_differ_from_full_query")
	}
	for _, row := range closeSnapshot.Rows {
		p := PositionBaselineRow{SecurityID: row.Symbol + "." + row.Exchange, Symbol: row.Symbol, Exchange: row.Exchange, Quantity: row.Quantity, SellableQty: row.SellableQty}
		if source, ok := expected[p.SecurityID]; ok {
			p.SourceUpdatedAt = source.SourceUpdatedAt
		}
		out.Positions = append(out.Positions, p)
	}
	sort.Slice(out.Positions, func(i, j int) bool { return out.Positions[i].SecurityID < out.Positions[j].SecurityID })
	meta.TotalRows = len(out.Positions)
	meta.ContentSHA256 = PositionBaselineRowsDigest(out.Positions)
	meta.Proof["counter_session_provided"] = meta.CounterSessionID != ""
	if len(meta.QualityFlags) == 0 {
		meta.Completeness = "complete"
		meta.MissingSecurityMeansZero = true
	}
	// The identifier binds evidence and quantities; a changed generation cannot reuse its cursor.
	body, _ := json.Marshal(meta)
	meta.BaselineID = fmt.Sprintf("pb-%x", sha256.Sum256(body))
	out.Baseline = meta
	return out, nil
}

func PositionBaselineRowsDigest(rows []PositionBaselineRow) string {
	values := make([][]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, []any{row.SecurityID, row.Symbol, row.Exchange, row.Quantity, row.SellableQty, row.SourceUpdatedAt})
	}
	body, _ := json.Marshal(values)
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func baselineTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return timeutil.FormatRFC3339Nano(t)
}

func paginatePositionBaseline(page PositionBaselinePage, query PositionBaselineQuery) (PositionBaselinePage, error) {
	page.Query = map[string]any{"environment": query.Environment, "account_id": query.AccountID, "broker_id": query.BrokerID, "gateway_id": query.GatewayID, "trade_date": query.TradeDate, "snapshot_type": query.SnapshotType, "baseline_id": query.BaselineID, "cursor": query.Cursor, "limit": query.Limit}
	type cursor struct {
		BaselineID string `json:"baseline_id"`
		Offset     int    `json:"offset"`
	}
	position := cursor{BaselineID: query.BaselineID}
	if query.Cursor != "" {
		body, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		if err != nil || json.Unmarshal(body, &position) != nil || position.BaselineID == "" || position.Offset <= 0 {
			return PositionBaselinePage{}, fmt.Errorf("%w: invalid baseline cursor", ErrInvalidLedgerInput)
		}
		if query.BaselineID != "" && query.BaselineID != position.BaselineID {
			return PositionBaselinePage{}, fmt.Errorf("%w: cursor/baseline mismatch", ErrInvalidLedgerInput)
		}
	}
	if position.BaselineID != "" && position.BaselineID != page.Baseline.BaselineID {
		return PositionBaselinePage{}, ErrPositionBaselineChanged
	}
	if position.Offset > len(page.Positions) {
		return PositionBaselinePage{}, fmt.Errorf("%w: cursor exceeds baseline", ErrInvalidLedgerInput)
	}
	end := position.Offset + query.Limit
	if end > len(page.Positions) {
		end = len(page.Positions)
	}
	page.Offset = position.Offset
	page.Positions = page.Positions[position.Offset:end]
	page.Count = len(page.Positions)
	page.PageComplete = end == page.Baseline.TotalRows
	if !page.PageComplete {
		body, _ := json.Marshal(cursor{page.Baseline.BaselineID, end})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(body)
	}
	return page, nil
}
