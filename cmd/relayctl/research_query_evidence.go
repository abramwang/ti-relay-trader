package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/performance"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

var researchQueryKinds = []struct{ Step, Action, Result string }{
	{"asset", "account.asset.query", "asset_page"},
	{"positions", "account.positions.query", "position_page"},
	{"orders", "order.list.query", "order_page"},
	{"fills", "fill.list.query", "fill_page"},
	{"fees", "fee.list.query", "fee_page"},
}

type researchQueryAccount struct {
	AccountID string   `json:"account_id"`
	Errors    []string `json:"errors"`
	Refresh   []struct {
		Step string `json:"step"`
	} `json:"refresh"`
	Freshness struct {
		StartedAt time.Time                       `json:"refresh_started_at"`
		Terminals map[string]ledger.CommandStatus `json:"query_terminals"`
	} `json:"refresh_freshness"`
}

type researchQueryProof struct {
	Step            string                `json:"step"`
	MessageID       string                `json:"message_id"`
	ReportedReplies int                   `json:"reported_replies"`
	ArchivedReplies int                   `json:"archived_replies"`
	Items           int                   `json:"items"`
	Finals          int                   `json:"finals"`
	ArchiveSHA      string                `json:"archive_sha256,omitempty"`
	AssetReply      json.RawMessage       `json:"asset_reply,omitempty"`
	State           string                `json:"state"`
	Flags           []string              `json:"flags"`
	FirstReply      *time.Time            `json:"first_reply,omitempty"`
	LastReply       *time.Time            `json:"last_reply,omitempty"`
	BookCoverage    *researchBookCoverage `json:"book_coverage,omitempty"`
}

type researchQueryAttempt struct {
	RunID         string               `json:"run_id"`
	Job           string               `json:"job"`
	Status        string               `json:"status"`
	SnapshotRunID string               `json:"snapshot_run_id,omitempty"`
	LinkedToClose bool                 `json:"linked_to_close_capture"`
	AccountErrors []string             `json:"account_errors"`
	Queries       []researchQueryProof `json:"queries"`
}

type researchQueryDay struct {
	Date          string                  `json:"trade_date"`
	Baseline      ledger.PositionBaseline `json:"close_position_baseline"`
	Attempts      []researchQueryAttempt  `json:"attempts"`
	MissingJobs   []string                `json:"missing_jobs"`
	SnapshotTimes []json.RawMessage       `json:"snapshot_times"`
	TimingReview  map[string]any          `json:"timing_review"`
	RepoTiming    map[string]any          `json:"reviewed_repo_cash_timing,omitempty"`
}

type researchArchivedReply struct {
	Body       []byte
	Stream     string
	ParseError string
	ReceivedAt time.Time
}

// This validates transport/archive evidence only. It does not infer economic
// cash scope, fill completeness relative to the broker, or book-row equality.
func reviewResearchQuery(step, action, result, environment, date string, route config.AccountRouteConfig, terminal ledger.CommandStatus, started, finished time.Time, replies []researchArchivedReply) researchQueryProof {
	p := researchQueryProof{Step: step, MessageID: terminal.OriginMessageID, ReportedReplies: terminal.ReplyCount, ArchivedReplies: len(replies), State: "not_proven", Flags: []string{}}
	flag := func(s string) {
		for _, old := range p.Flags {
			if old == s {
				return
			}
		}
		p.Flags = append(p.Flags, s)
	}
	if terminal.OriginMessageID == "" {
		flag("query_identity_missing")
		return p
	}
	if terminal.AccountID != route.AccountID || terminal.Action != action || terminal.ExpectedResultType != result || !terminal.Success || !terminal.Terminal || terminal.Contradictory || terminal.TerminalCount != 1 || terminal.State != "completed" {
		flag("reported_terminal_not_single_success")
	}
	if terminal.ReplyCount < 1 || terminal.ReplyCount != len(replies) {
		flag("archive_count_differs_from_report")
	}
	if started.IsZero() || finished.IsZero() || finished.Before(started) || started.In(timeutil.Location()).Format("2006-01-02") != date || finished.In(timeutil.Location()).Format("2006-01-02") != date {
		flag("collection_window_unproven")
	}
	if environment == "production" {
		environment = "prod"
	}
	wantStream := fmt.Sprintf("relay:%s:v1:%s:%s:reply", environment, route.BrokerID, route.GatewayID)
	if route.StreamPrefix != "" {
		wantStream = route.StreamPrefix + ":reply"
	}
	seen := map[string]bool{}
	h := sha256.New()
	var previous time.Time
	for i, raw := range replies {
		framed, _ := json.Marshal(struct {
			Stream     string
			Body       []byte
			ParseError string
			At         time.Time
		}{raw.Stream, raw.Body, raw.ParseError, raw.ReceivedAt})
		fmt.Fprintf(h, "%d:", len(framed))
		h.Write(framed)
		var e struct {
			Protocol  string    `json:"protocol"`
			MessageID string    `json:"message_id"`
			Origin    string    `json:"origin_message_id"`
			Type      string    `json:"message_type"`
			Action    string    `json:"action"`
			Result    string    `json:"result_type"`
			Status    string    `json:"status"`
			Produced  time.Time `json:"produced_at"`
			Route     struct {
				Env     string `json:"env"`
				Account string `json:"account_id"`
				Broker  string `json:"broker_id"`
				Gateway string `json:"gateway_id"`
			} `json:"routing"`
			Chunk struct {
				Last bool `json:"is_last"`
			} `json:"chunk"`
			Payload struct {
				Items   *[]json.RawMessage `json:"items"`
				Account json.RawMessage    `json:"account"`
			} `json:"payload"`
		}
		if raw.ParseError != "" || json.Unmarshal(raw.Body, &e) != nil {
			flag("archive_parse_error")
			continue
		}
		if e.Protocol != "relay.stream.v1" || e.Type != "reply" || e.Action != action || e.Result != result || e.Origin != terminal.OriginMessageID || e.Route.Account != route.AccountID || e.Route.Env != environment || e.Route.Broker != route.BrokerID || e.Route.Gateway != route.GatewayID || raw.Stream != wantStream {
			flag("archive_identity_mismatch")
		}
		if e.MessageID == "" || seen[e.MessageID] {
			flag("duplicate_or_missing_reply_identity")
		}
		seen[e.MessageID] = true
		if raw.ReceivedAt.Before(started) || raw.ReceivedAt.After(finished) || (!previous.IsZero() && raw.ReceivedAt.Before(previous)) || e.Produced.IsZero() || e.Produced.In(timeutil.Location()).Format("2006-01-02") != date {
			flag("archive_time_outside_collection")
		}
		previous = raw.ReceivedAt
		at := raw.ReceivedAt.In(timeutil.Location())
		if p.FirstReply == nil {
			p.FirstReply = &at
		}
		p.LastReply = &at
		if e.Status == "completed" && e.Chunk.Last {
			p.Finals++
			if i != len(replies)-1 {
				flag("final_not_last")
			}
		} else if e.Status != "partial" || e.Chunk.Last {
			flag("failed_or_invalid_page")
		}
		if action == "account.asset.query" {
			p.AssetReply = append(json.RawMessage(nil), raw.Body...)
			var account map[string]json.RawMessage
			if len(e.Payload.Account) == 0 || json.Unmarshal(e.Payload.Account, &account) != nil || len(account) == 0 {
				flag("asset_payload_missing")
			} else {
				p.Items++
			}
		} else if e.Payload.Items == nil {
			flag("items_payload_missing")
		} else {
			p.Items += len(*e.Payload.Items)
		}
	}
	p.ArchiveSHA = fmt.Sprintf("%x", h.Sum(nil))
	if p.Finals != 1 {
		flag("single_archive_final_missing")
	}
	if len(p.Flags) == 0 {
		p.State = "terminal_archive_consistent"
	}
	return p
}

func readResearchReplies(ctx context.Context, tx *sql.Tx, account, message string) ([]researchArchivedReply, error) {
	return readResearchArchiveRole(ctx, tx, account, message, "reply")
}

func readResearchArchiveRole(ctx context.Context, tx *sql.Tx, account, message, role string) ([]researchArchivedReply, error) {
	if message == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT body,stream_key,COALESCE(parse_error,''),received_at FROM raw_stream_messages WHERE account_id=$1 AND origin_message_id=$2 AND stream_role=$3 ORDER BY received_at,raw_message_pk LIMIT 25001`, account, message, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []researchArchivedReply
	bytes := 0
	for rows.Next() {
		var row researchArchivedReply
		if err := rows.Scan(&row.Body, &row.Stream, &row.ParseError, &row.ReceivedAt); err != nil {
			return nil, err
		}
		bytes += len(row.Body)
		result = append(result, row)
		if bytes > 64<<20 || len(result) > 25000 {
			return nil, errors.New("query evidence archive budget exceeded")
		}
	}
	return result, rows.Err()
}

func readResearchQueryDay(ctx context.Context, tx *sql.Tx, env string, route config.AccountRouteConfig, date string, bookCoverage, eventCoverage bool) (researchQueryDay, error) {
	d := researchQueryDay{Date: date, Attempts: []researchQueryAttempt{}, MissingJobs: []string{}}
	page, err := ledger.NewRepository(tx).GetPositionBaseline(ctx, ledger.PositionBaselineQuery{Environment: env, AccountID: route.AccountID, BrokerID: route.BrokerID, GatewayID: route.GatewayID, TradeDate: date, SnapshotType: "close", Limit: 1})
	if err != nil {
		return d, err
	}
	d.Baseline = page.Baseline
	times, err := tx.QueryContext(ctx, `SELECT jsonb_build_object('snapshot_type',snapshot_type,'captured_at',captured_at,'run_id',raw_payload->'run_id','input_snapshot_type',raw_payload->'input_snapshot_type','asset',raw_payload->'asset','broker_asset',raw_payload->'broker_asset','reverse_repo',raw_payload->'reverse_repo') FROM asset_snapshots WHERE account_id=$1 AND trade_date=$2::date AND snapshot_type IN ('broker_close','close') ORDER BY snapshot_type`, route.AccountID, date)
	if err != nil {
		return d, err
	}
	for times.Next() {
		var raw []byte
		if err := times.Scan(&raw); err != nil {
			times.Close()
			return d, err
		}
		d.SnapshotTimes = append(d.SnapshotTimes, json.RawMessage(raw))
	}
	err = times.Err()
	times.Close()
	if err != nil {
		return d, err
	}
	d.TimingReview = reviewResearchSnapshotTimes(d.SnapshotTimes)
	rows, err := tx.QueryContext(ctx, `SELECT run_id,job_name,status,finished_at,report_json->'accounts',COALESCE(report_json->'broker_close_snapshot'->'result'->>'run_id','') FROM job_runs WHERE trade_date=$1::date AND job_name IN ('pre_open_init','post_close_capture') ORDER BY job_name,updated_at,run_id LIMIT 101`, date)
	if err != nil {
		return d, err
	}
	type job struct {
		run, name, status, snapshot string
		finished                    sql.NullTime
		accounts                    []byte
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.run, &j.name, &j.status, &j.finished, &j.accounts, &j.snapshot); err != nil {
			rows.Close()
			return d, err
		}
		jobs = append(jobs, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	if len(jobs) > 100 {
		return d, errors.New("too many daily task attempts")
	}
	seenJobs := map[string]bool{}
	var books researchDailyBooks
	if bookCoverage {
		books, err = readResearchDailyBooks(ctx, tx, route.AccountID, date)
		if err != nil {
			return d, err
		}
	}
	for _, j := range jobs {
		var accounts []researchQueryAccount
		if len(j.accounts) > 0 {
			if err := json.Unmarshal(j.accounts, &accounts); err != nil {
				return d, err
			}
		}
		for _, a := range accounts {
			if a.AccountID != route.AccountID {
				continue
			}
			seenJobs[j.name] = true
			attempt := researchQueryAttempt{RunID: j.run, Job: j.name, Status: j.status, SnapshotRunID: j.snapshot, LinkedToClose: j.snapshot != "" && j.snapshot == d.Baseline.CaptureRunID, AccountErrors: a.Errors, Queries: []researchQueryProof{}}
			for _, kind := range researchQueryKinds {
				terminal := a.Freshness.Terminals[kind.Step]
				if preOpenFeeNotScheduled(j.name, a, kind.Step) {
					attempt.Queries = append(attempt.Queries, researchQueryProof{Step: kind.Step, State: "not_scheduled", Flags: []string{"pre_open_does_not_request_fees"}})
					continue
				}
				raw, err := readResearchReplies(ctx, tx, route.AccountID, terminal.OriginMessageID)
				if err != nil {
					return d, err
				}
				proof := reviewResearchQuery(kind.Step, kind.Action, kind.Result, env, date, route, terminal, a.Freshness.StartedAt, j.finished.Time, raw)
				if bookCoverage && j.name == "post_close_capture" && (kind.Step == "orders" || kind.Step == "fills") {
					proof.BookCoverage, err = readResearchBookCoverage(ctx, tx, env, date, kind.Action, route, proof, raw, books)
					if err != nil {
						return d, err
					}
				}
				attempt.Queries = append(attempt.Queries, proof)
			}
			d.Attempts = append(d.Attempts, attempt)
		}
	}
	for _, name := range []string{"pre_open_init", "post_close_capture"} {
		if !seenJobs[name] {
			d.MissingJobs = append(d.MissingJobs, name)
		}
	}
	if eventCoverage {
		if err := attachResearchEventEvidence(ctx, tx, env, route, &d); err != nil {
			return d, err
		}
	}
	return d, nil
}

func preOpenFeeNotScheduled(job string, a researchQueryAccount, step string) bool {
	if job != "pre_open_init" || step != "fees" {
		return false
	}
	if _, exists := a.Freshness.Terminals[step]; exists {
		return false
	}
	seen := map[string]bool{}
	for _, r := range a.Refresh {
		seen[r.Step] = true
	}
	return !seen["fees"] && seen["asset"] && seen["positions"] && seen["orders"] && seen["fills"]
}

func reviewResearchSnapshotTimes(raw []json.RawMessage) map[string]any {
	type snapshot struct {
		Kind        string        `json:"snapshot_type"`
		Captured    time.Time     `json:"captured_at"`
		Input       string        `json:"input_snapshot_type"`
		Asset       trading.Asset `json:"asset"`
		BrokerAsset trading.Asset `json:"broker_asset"`
		Repo        struct {
			CashAt time.Time `json:"cash_observed_at"`
		} `json:"reverse_repo"`
	}
	var close, broker snapshot
	for _, b := range raw {
		var s snapshot
		if json.Unmarshal(b, &s) != nil {
			return map[string]any{"state": "unparseable"}
		}
		if s.Kind == "close" {
			close = s
		}
		if s.Kind == "broker_close" {
			broker = s
		}
	}
	state := "unexplained_time_difference"
	a, b := close.BrokerAsset, broker.Asset
	if close.Captured.IsZero() || broker.Captured.IsZero() {
		state = "snapshot_missing"
	} else if close.Captured.Equal(broker.Captured) {
		state = "same_batch_time"
	} else if close.Input == "broker_close" && a.AccountID != "" && a.AccountID == b.AccountID && !a.UpdatedAt.IsZero() && a.UpdatedAt.Equal(b.UpdatedAt) && close.Captured.Equal(a.UpdatedAt) && close.Repo.CashAt.Equal(a.UpdatedAt) && a.CashTotal == b.CashTotal && a.CashAvailable == b.CashAvailable && a.NetAsset == b.NetAsset && a.Commission == b.Commission && !a.UpdatedAt.After(broker.Captured) {
		state = "cash_observation_time_not_batch_time"
	}
	return map[string]any{"state": state, "close_time": close.Captured.In(timeutil.Location()), "broker_batch_time": broker.Captured.In(timeutil.Location()), "cash_observed_at": b.UpdatedAt.In(timeutil.Location()), "baseline_override": false, "scope": "stored raw source timing explanation only; not publication or economic cash scope approval"}
}

func runResearchQueryEvidence(args []string) error {
	f := flag.NewFlagSet("performance-research-query-evidence", flag.ContinueOnError)
	configPath := f.String("config", "", "explicit config; database read-only")
	account := f.String("account", "", "explicit account")
	version := f.String("version", "", "archived research version")
	output := f.String("output", "", "new private evidence report")
	bookCoverage := f.Bool("book-coverage", false, "also compare archived close query scope and core book rows; read-only")
	eventCoverage := f.Bool("event-coverage", false, "link book-only records to independent event archives and inspect repo cash timing; requires book-coverage")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" || *output == "" || strings.TrimSpace(*account) == "" || strings.TrimSpace(*version) == "" {
		return errors.New("config, account, version and output required")
	}
	if *eventCoverage && !*bookCoverage {
		return errors.New("event-coverage requires book-coverage")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	route, ok := cfg.AccountRoute(*account)
	if !ok {
		return errors.New("account route not configured")
	}
	parsed, err := pgconn.ParseConfig(cfg.Database.DSN)
	if err != nil || cfg.Database.ExpectedName == "" || parsed.Database != cfg.Database.ExpectedName {
		return errors.New("configured database mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := openPerformanceDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout='30s'`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL TIME ZONE 'Asia/Shanghai'`); err != nil {
		return err
	}
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return err
	}
	if name != cfg.Database.ExpectedName {
		return errors.New("connected database mismatch")
	}
	v, err := ledger.NewRepository(tx).GetResearchCostVersion(ctx, *account, *version)
	if err != nil {
		return err
	}
	b, err := performance.DecodeResearchCostVersion(v)
	if err != nil {
		return err
	}
	dates := append([]string{b.AnchorDate}, b.Dates...)
	if len(dates) > 64 {
		return errors.New("query evidence window exceeds 64 days")
	}
	var days []researchQueryDay
	counts := map[string]int{}
	for _, date := range dates {
		d, err := readResearchQueryDay(ctx, tx, string(cfg.Service.Environment), route, date, *bookCoverage, *eventCoverage)
		if err != nil {
			return err
		}
		days = append(days, d)
		counts["baseline:"+d.Baseline.Completeness]++
		counts["timing:"+fmt.Sprint(d.TimingReview["state"])]++
		if d.RepoTiming != nil {
			counts["repo_timing:"+fmt.Sprint(d.RepoTiming["state"])]++
		}
		for _, a := range d.Attempts {
			for _, q := range a.Queries {
				counts[a.Job+":"+q.State]++
				if q.BookCoverage != nil {
					counts["request:"+q.BookCoverage.RequestState]++
					counts["book:"+q.Step+":"+q.BookCoverage.Rows.State]++
					counts["book:"+q.Step+":parsed_rows"] += q.BookCoverage.Rows.QueryRows
					counts["book:"+q.Step+":ledger_rows"] += q.BookCoverage.Rows.BookRows
					counts["book:"+q.Step+":matched_rows"] += q.BookCoverage.Rows.Matched
					counts["book:"+q.Step+":invalid_records"] += len(q.BookCoverage.DecodeIssues)
					for _, diff := range q.BookCoverage.Rows.Differences {
						counts["book:"+q.Step+":"+diff.Kind]++
						if diff.EventEvidence != nil {
							counts["event:"+q.Step+":"+diff.EventEvidence.State]++
						}
					}
					if q.BookCoverage.Transfers != nil {
						counts["book:transfers:"+q.BookCoverage.Transfers.State]++
						counts["book:transfers:parsed_rows"] += q.BookCoverage.Transfers.QueryRows
						counts["book:transfers:ledger_rows"] += q.BookCoverage.Transfers.BookRows
						counts["book:transfers:matched_rows"] += q.BookCoverage.Transfers.Matched
						for _, diff := range q.BookCoverage.Transfers.Differences {
							counts["book:transfers:"+diff.Kind]++
						}
					}
				}
			}
		}
		for _, missing := range d.MissingJobs {
			counts["missing:"+missing]++
		}
		fmt.Fprintf(os.Stderr, "query evidence %s: baseline=%s attempts=%d\n", date, d.Baseline.Completeness, len(d.Attempts))
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	report := map[string]any{"schema": "relay.research_query_evidence.v1", "account_id": *account, "version_id": *version, "payload_sha256": v.PayloadSHA256, "environment": string(cfg.Service.Environment), "database": name, "observed_at": timeutil.Now(), "days": days, "summary": counts, "database_writes": 0, "broker_commands": 0, "cost_publishable": false, "economic_nav_publishable": false, "scope": "anchor and cost dates; task terminal/archive consistency and existing close position baseline; not order/fill book-row equality, next-day opening dependencies, or economic asset scope"}
	if *bookCoverage {
		report["book_coverage_requested"] = true
		report["scope"] = "anchor and cost dates; archive completion and close-query versus current book core projections, including component transfers; not proof of broker-wide completeness, historical fees, next-day dependencies or economic asset scope"
	}
	if *eventCoverage {
		report["event_coverage_requested"] = true
		report["scope"] = "historical query/core book projections; independent event evidence for book-only orders/fills and cash timing for matched query-external repos; no economic cash scope or cost/NAV publication approval"
	}
	if err := writeETFTransferReviewFile(*output, report); err != nil {
		return err
	}
	return writeJSON(counts)
}
