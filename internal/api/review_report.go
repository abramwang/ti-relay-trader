package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

const dailyReviewBreakLimit = 1000

type DailyReviewReport struct {
	RequestedDate              string                        `json:"requested_date"`
	TradeDate                  string                        `json:"trade_date"`
	Timezone                   string                        `json:"timezone"`
	Status                     string                        `json:"status"`
	GeneratedAt                string                        `json:"generated_at"`
	CalendarSource             string                        `json:"calendar_source,omitempty"`
	IsTradingDay               *bool                         `json:"is_trading_day,omitempty"`
	PreviousOrCurrentTradeDate string                        `json:"previous_or_current_trade_date,omitempty"`
	Summary                    DailyReviewSummary            `json:"summary"`
	Jobs                       map[string]DailyReviewJobView `json:"jobs"`
	Accounts                   []DailyReviewAccount          `json:"accounts"`
	Warnings                   []string                      `json:"warnings,omitempty"`
}

type DailyReviewSummary struct {
	ConfiguredAccounts int `json:"configured_accounts"`
	ReviewedAccounts   int `json:"reviewed_accounts"`
	PassedAccounts     int `json:"passed_accounts"`
	AttentionAccounts  int `json:"attention_accounts"`
	BlockedAccounts    int `json:"blocked_accounts"`
	PendingAccounts    int `json:"pending_accounts"`
	OpenBreaks         int `json:"open_breaks"`
	WarningBreaks      int `json:"warning_breaks"`
	CriticalBreaks     int `json:"critical_breaks"`
}

type DailyReviewJobView struct {
	RunID             string `json:"run_id,omitempty"`
	Status            string `json:"status"`
	Skipped           bool   `json:"skipped,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	FinishedAt        string `json:"finished_at,omitempty"`
	DurationMS        int64  `json:"duration_ms,omitempty"`
	AccountErrorCount int    `json:"account_error_count,omitempty"`
	ErrorSummary      string `json:"error_summary,omitempty"`
}

type DailyReviewAccount struct {
	AccountID string                       `json:"account_id"`
	Alias     string                       `json:"alias,omitempty"`
	Status    string                       `json:"status"`
	Open      *DailyReviewSnapshot         `json:"open,omitempty"`
	Close     *DailyReviewSnapshot         `json:"close,omitempty"`
	Breaks    []ledger.ReconciliationBreak `json:"breaks,omitempty"`
	Issues    []DailyReviewIssue           `json:"issues,omitempty"`
}

type DailyReviewSnapshot struct {
	Persisted                bool           `json:"persisted"`
	Blocked                  bool           `json:"blocked,omitempty"`
	Asset                    map[string]any `json:"asset,omitempty"`
	AssetUpdatedAt           string         `json:"asset_updated_at,omitempty"`
	PositionsLatestUpdatedAt string         `json:"positions_latest_updated_at,omitempty"`
	PositionSnapshots        int            `json:"position_snapshots"`
	PositionsCount           int            `json:"positions_count"`
	OrdersCount              int            `json:"orders_count"`
	FillsCount               int            `json:"fills_count"`
	NonTerminalOrders        int            `json:"non_terminal_orders"`
	Errors                   []string       `json:"errors,omitempty"`
}

type DailyReviewIssue struct {
	Source     string `json:"source"`
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	ObjectType string `json:"object_type,omitempty"`
	ObjectID   string `json:"object_id,omitempty"`
}

func (s *Server) handleDailyReviewReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteMethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if s.jobs == nil || s.settles == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeUnavailable, "daily review stores are unavailable", nil)
		return
	}
	report, err := s.buildDailyReviewReport(r.Context(), r.URL.Query().Get("trade_date"))
	if err != nil {
		if errors.Is(err, ledger.ErrInvalidLedgerInput) || strings.Contains(err.Error(), "trade_date") {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid daily review query", err.Error())
			return
		}
		s.logger.Warn("daily_review_report_failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "daily review report failed", nil)
		return
	}
	httpx.WriteOK(w, r, http.StatusOK, report)
}

func (s *Server) buildDailyReviewReport(ctx context.Context, requested string) (DailyReviewReport, error) {
	explicitDate := strings.TrimSpace(requested) != ""
	if !explicitDate {
		requested = timeutil.Now().Format("2006-01-02")
	}
	requestedDate, err := normalizeAPIDate(requested)
	if err != nil {
		return DailyReviewReport{}, err
	}
	tradeDate := requestedDate
	report := DailyReviewReport{
		RequestedDate: requestedDate,
		TradeDate:     tradeDate,
		Timezone:      timeutil.LocationName,
		Status:        "pending",
		GeneratedAt:   timeutil.FormatRFC3339Nano(timeutil.Now()),
		Jobs:          map[string]DailyReviewJobView{},
		Accounts:      []DailyReviewAccount{},
	}

	if s.market != nil {
		calendarCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		calendar, calendarErr := s.market.TradingDayStatus(calendarCtx, requestedDate)
		cancel()
		if calendarErr != nil {
			report.Warnings = append(report.Warnings, "Meridian trading-day status is unavailable")
		} else {
			report.CalendarSource = "meridian"
			report.PreviousOrCurrentTradeDate = calendar.PreviousOrCurrentTradingDate
			if calendar.IsTradingDayKnown {
				isTradingDay := calendar.IsTradingDay
				report.IsTradingDay = &isTradingDay
				if !isTradingDay && !explicitDate && calendar.PreviousOrCurrentTradingDate != "" {
					previous, normalizeErr := normalizeAPIDate(calendar.PreviousOrCurrentTradingDate)
					if normalizeErr == nil {
						tradeDate = previous
						report.TradeDate = previous
					}
				}
			}
		}
	}

	runs, err := s.jobs.ListJobRuns(ctx, ledger.JobRunQuery{
		JobNames:  []string{"pre_open_init", "post_close_settlement"},
		TradeDate: tradeDate,
		Limit:     20,
	})
	if err != nil {
		return DailyReviewReport{}, err
	}
	latest := latestDailyJobRuns(runs)
	for name, run := range latest {
		report.Jobs[name] = dailyReviewJobView(run)
	}
	if _, ok := report.Jobs["pre_open_init"]; !ok {
		report.Jobs["pre_open_init"] = DailyReviewJobView{Status: "missing"}
	}
	if _, ok := report.Jobs["post_close_settlement"]; !ok {
		report.Jobs["post_close_settlement"] = DailyReviewJobView{Status: "missing"}
	}

	var breaks []ledger.ReconciliationBreak
	if post := latest["post_close_settlement"]; post != nil {
		runID := stringFromMap(post.Report, "settlement_run_id")
		if runID == "" {
			runID = "post_close_settlement-" + strings.ReplaceAll(tradeDate, "-", "")
		}
		breaks, err = s.settles.ListReconciliationBreaks(ctx, ledger.ReconciliationBreakQuery{
			RunID: runID,
			Limit: dailyReviewBreakLimit,
		})
		if err != nil {
			return DailyReviewReport{}, err
		}
	}

	aliases, _ := s.accountAliasOverrides(ctx)
	accountIDs := dailyReviewAccountIDs(s.cfg.Accounts, latest, breaks)
	report.Summary.ConfiguredAccounts = len(accountIDs)
	for _, accountID := range accountIDs {
		account := DailyReviewAccount{
			AccountID: accountID,
			Status:    "pending",
			Breaks:    []ledger.ReconciliationBreak{},
			Issues:    []DailyReviewIssue{},
		}
		if route, ok := s.cfg.AccountRoute(accountID); ok {
			account.Alias = effectiveAccountAlias(route, aliases)
		}
		account.Open = dailyReviewSnapshot(latest["pre_open_init"], "open_snapshot", accountID)
		account.Close = dailyReviewSnapshot(latest["post_close_settlement"], "settlement_snapshot", accountID)
		for _, item := range breaks {
			if item.AccountID != accountID {
				continue
			}
			account.Breaks = append(account.Breaks, item)
			if item.Status != "open" {
				continue
			}
			account.Issues = append(account.Issues, DailyReviewIssue{
				Source:     "reconciliation",
				Code:       item.BreakType,
				Severity:   item.Severity,
				Message:    item.Description,
				ObjectType: item.ObjectType,
				ObjectID:   item.ObjectID,
			})
			report.Summary.OpenBreaks++
			switch item.Severity {
			case "critical":
				report.Summary.CriticalBreaks++
			case "warning":
				report.Summary.WarningBreaks++
			}
		}
		account.Issues = append(account.Issues, snapshotIssues("pre_open", account.Open)...)
		account.Issues = append(account.Issues, snapshotIssues("post_close", account.Close)...)
		account.Status = dailyReviewAccountStatus(account, latest["post_close_settlement"] != nil)
		switch account.Status {
		case "passed":
			report.Summary.PassedAccounts++
		case "attention":
			report.Summary.AttentionAccounts++
		case "blocked":
			report.Summary.BlockedAccounts++
		default:
			report.Summary.PendingAccounts++
		}
		report.Accounts = append(report.Accounts, account)
	}
	report.Summary.ReviewedAccounts = report.Summary.PassedAccounts + report.Summary.AttentionAccounts + report.Summary.BlockedAccounts
	report.Warnings = append(report.Warnings, dailyJobWarnings(latest)...)
	report.Warnings = uniqueStrings(report.Warnings)

	if explicitDate && report.IsTradingDay != nil && !*report.IsTradingDay && tradeDate == requestedDate {
		report.Status = "non_trading"
	} else if latest["post_close_settlement"] == nil {
		report.Status = "pending"
	} else if report.Summary.BlockedAccounts > 0 || report.Summary.CriticalBreaks > 0 {
		report.Status = "blocked"
	} else if report.Summary.AttentionAccounts > 0 || report.Summary.OpenBreaks > 0 || len(report.Warnings) > 0 {
		report.Status = "attention"
	} else {
		report.Status = "passed"
	}
	return report, nil
}

func latestDailyJobRuns(runs []ledger.JobRun) map[string]*ledger.JobRun {
	out := map[string]*ledger.JobRun{}
	for index := range runs {
		run := runs[index]
		if _, exists := out[run.JobName]; exists {
			continue
		}
		copy := run
		out[run.JobName] = &copy
	}
	return out
}

func dailyReviewJobView(run *ledger.JobRun) DailyReviewJobView {
	if run == nil {
		return DailyReviewJobView{Status: "missing"}
	}
	return DailyReviewJobView{
		RunID:             run.RunID,
		Status:            run.Status,
		Skipped:           run.Skipped,
		StartedAt:         optionalBusinessTime(run.StartedAt),
		FinishedAt:        optionalBusinessTime(run.FinishedAt),
		DurationMS:        run.DurationMS,
		AccountErrorCount: intFromReviewAny(run.Report["account_error_count"]),
		ErrorSummary:      run.ErrorSummary,
	}
}

func dailyReviewAccountIDs(configured []config.AccountRouteConfig, runs map[string]*ledger.JobRun, breaks []ledger.ReconciliationBreak) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(configured))
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		ids = append(ids, value)
	}
	for _, jobName := range []string{"pre_open_init", "post_close_settlement"} {
		if run := runs[jobName]; run != nil {
			for _, account := range reviewMapSlice(run.Report["accounts"]) {
				add(stringFromAny(account["account_id"]))
			}
		}
	}
	for _, item := range breaks {
		add(item.AccountID)
	}
	if len(ids) == 0 {
		for _, account := range configured {
			if account.Enabled {
				add(account.AccountID)
			}
		}
	}
	return ids
}

func dailyReviewSnapshot(run *ledger.JobRun, wrapperKey string, accountID string) *DailyReviewSnapshot {
	if run == nil {
		return nil
	}
	flow := reviewAccountMap(run.Report["accounts"], accountID)
	wrapper := reviewMap(run.Report[wrapperKey])
	result := reviewMap(wrapper["result"])
	settled := reviewAccountMap(result["accounts"], accountID)
	if flow == nil && settled == nil {
		return nil
	}
	observed := reviewMap(flow["snapshot"])
	snapshot := &DailyReviewSnapshot{
		Persisted:                boolFromReviewAny(settled["asset_snapshot_written"]),
		Blocked:                  boolFromReviewAny(flow["snapshot_blocked"]),
		Asset:                    reviewMap(observed["asset"]),
		AssetUpdatedAt:           stringFromAny(observed["asset_updated_at"]),
		PositionsLatestUpdatedAt: stringFromAny(observed["positions_latest_updated_at"]),
		PositionSnapshots:        intFromReviewAny(settled["position_snapshots_written"]),
		PositionsCount:           intFromReviewAny(observed["positions_count"]),
		OrdersCount:              intFromReviewAny(observed["orders_count"]),
		FillsCount:               intFromReviewAny(observed["fills_count"]),
		NonTerminalOrders:        intFromReviewAny(observed["non_terminal_orders"]),
		Errors:                   reviewStrings(flow["errors"]),
	}
	if settled != nil {
		snapshot.PositionsCount = intFromReviewAny(settled["positions_count"])
		snapshot.OrdersCount = intFromReviewAny(settled["orders_count"])
		snapshot.FillsCount = intFromReviewAny(settled["fills_count"])
		snapshot.NonTerminalOrders = intFromReviewAny(settled["non_terminal_orders"])
		snapshot.Errors = append(snapshot.Errors, reviewStrings(settled["errors"])...)
	}
	snapshot.Errors = uniqueStrings(snapshot.Errors)
	return snapshot
}

func snapshotIssues(source string, snapshot *DailyReviewSnapshot) []DailyReviewIssue {
	if snapshot == nil {
		return []DailyReviewIssue{{Source: source, Code: "snapshot_missing", Severity: "critical", Message: source + " snapshot is missing"}}
	}
	issues := make([]DailyReviewIssue, 0, len(snapshot.Errors)+1)
	if snapshot.Blocked || !snapshot.Persisted {
		issues = append(issues, DailyReviewIssue{Source: source, Code: "snapshot_blocked", Severity: "critical", Message: source + " snapshot was not persisted from a confirmed fresh broker query"})
	}
	for _, message := range snapshot.Errors {
		issues = append(issues, DailyReviewIssue{Source: source, Code: "account_query_error", Severity: "warning", Message: message})
	}
	return issues
}

func dailyReviewAccountStatus(account DailyReviewAccount, postCloseExists bool) string {
	if !postCloseExists {
		return "pending"
	}
	for _, issue := range account.Issues {
		if issue.Severity == "critical" {
			return "blocked"
		}
	}
	if len(account.Issues) > 0 {
		return "attention"
	}
	return "passed"
}

func dailyJobWarnings(runs map[string]*ledger.JobRun) []string {
	warnings := []string{}
	for _, name := range []string{"pre_open_init", "post_close_settlement"} {
		run := runs[name]
		if run == nil {
			continue
		}
		if run.Status == "failed" || run.ErrorSummary != "" {
			warnings = append(warnings, name+": "+firstNonEmpty(run.ErrorSummary, "job failed"))
		}
		warnings = append(warnings, reviewStrings(run.Report["warnings"])...)
		warnings = append(warnings, reviewStrings(run.Report["errors"])...)
	}
	return warnings
}

func reviewMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	result, _ := value.(map[string]any)
	return result
}

func reviewMapSlice(value any) []map[string]any {
	switch items := value.(type) {
	case []any:
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if mapped := reviewMap(item); mapped != nil {
				result = append(result, mapped)
			}
		}
		return result
	case []map[string]any:
		return items
	default:
		return nil
	}
}

func reviewAccountMap(value any, accountID string) map[string]any {
	for _, account := range reviewMapSlice(value) {
		if stringFromAny(account["account_id"]) == accountID {
			return account
		}
	}
	return nil
}

func reviewStrings(value any) []string {
	switch items := value.(type) {
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				result = append(result, text)
			}
		}
		return result
	case []string:
		return append([]string(nil), items...)
	default:
		return nil
	}
}

func intFromReviewAny(value any) int {
	number, ok := floatFromAny(value)
	if !ok {
		return 0
	}
	return int(number)
}

func boolFromReviewAny(value any) bool {
	if result, ok := value.(bool); ok {
		return result
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
