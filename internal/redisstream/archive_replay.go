package redisstream

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
)

type ArchiveReplayOptions struct {
	AccountID string
	DateFrom  string
	DateTo    string
	Stages    []string
}

type ArchiveReplayReport struct {
	StartedAt   time.Time                  `json:"started_at"`
	CompletedAt time.Time                  `json:"completed_at"`
	AccountID   string                     `json:"account_id,omitempty"`
	DateFrom    string                     `json:"date_from,omitempty"`
	DateTo      string                     `json:"date_to,omitempty"`
	Stages      []ArchiveReplayStageReport `json:"stages"`
	Totals      LedgerProcessResult        `json:"totals"`
}

type ArchiveReplayStageReport struct {
	Name       string              `json:"name"`
	Rows       int                 `json:"rows"`
	Totals     LedgerProcessResult `json:"totals"`
	ErrorCount int                 `json:"error_count"`
	Errors     []LedgerEntryError  `json:"errors,omitempty"`
}

type archiveReplayWriter struct {
	LedgerWriter
}

func (archiveReplayWriter) ArchiveRawStreamMessage(context.Context, ledger.RawStreamMessage) error {
	return nil
}

func (archiveReplayWriter) AllowSummaryFillSynthesis() bool {
	return false
}

func ReplayArchivedLedger(ctx context.Context, db *sql.DB, writer LedgerWriter, options ArchiveReplayOptions) (ArchiveReplayReport, error) {
	if db == nil {
		return ArchiveReplayReport{}, fmt.Errorf("database is required")
	}
	if writer == nil {
		return ArchiveReplayReport{}, fmt.Errorf("ledger writer is required")
	}
	normalized, err := normalizeArchiveReplayOptions(options)
	if err != nil {
		return ArchiveReplayReport{}, err
	}

	report := ArchiveReplayReport{
		StartedAt: timeutil.Now(),
		AccountID: normalized.AccountID,
		DateFrom:  normalized.DateFrom,
		DateTo:    normalized.DateTo,
	}
	replayWriter := archiveReplayWriter{LedgerWriter: writer}
	availableStages := map[string]struct {
		name      string
		predicate string
	}{
		"orders": {
			name:      "orders",
			predicate: "((stream_role = 'event' AND event_type = 'order.event') OR (stream_role = 'reply' AND action = 'order.list.query'))",
		},
		"fills": {
			name:      "fills",
			predicate: "((stream_role = 'event' AND event_type = 'fill.event') OR (stream_role = 'reply' AND action = 'fill.list.query'))",
		},
		"transfers": {
			name:      "transfers",
			predicate: "(stream_role = 'event' AND event_type IN ('transfer.event', 'etf_component_transfer.event'))",
		},
	}
	for _, stageName := range normalized.Stages {
		stage := availableStages[stageName]
		stageReport, err := replayArchiveStage(ctx, db, replayWriter, normalized, stage.name, stage.predicate)
		if err != nil {
			return report, err
		}
		report.Stages = append(report.Stages, stageReport)
		report.Totals.add(stageReport.Totals)
	}
	report.CompletedAt = timeutil.Now()
	return report, nil
}

func normalizeArchiveReplayOptions(options ArchiveReplayOptions) (ArchiveReplayOptions, error) {
	options.AccountID = strings.TrimSpace(options.AccountID)
	options.DateFrom = strings.TrimSpace(options.DateFrom)
	options.DateTo = strings.TrimSpace(options.DateTo)
	if len(options.Stages) == 0 {
		options.Stages = []string{"orders", "fills", "transfers"}
	} else {
		seen := make(map[string]struct{}, len(options.Stages))
		normalizedStages := make([]string, 0, len(options.Stages))
		for _, stage := range options.Stages {
			stage = strings.ToLower(strings.TrimSpace(stage))
			if stage != "orders" && stage != "fills" && stage != "transfers" {
				return options, fmt.Errorf("unsupported replay stage %q; expected orders, fills, or transfers", stage)
			}
			if _, exists := seen[stage]; exists {
				continue
			}
			seen[stage] = struct{}{}
			normalizedStages = append(normalizedStages, stage)
		}
		if len(normalizedStages) == 0 {
			return options, fmt.Errorf("at least one replay stage is required")
		}
		options.Stages = normalizedStages
	}
	for label, value := range map[string]string{
		"date_from": options.DateFrom,
		"date_to":   options.DateTo,
	} {
		if value == "" {
			continue
		}
		if _, err := time.ParseInLocation("2006-01-02", value, timeutil.Location()); err != nil {
			return options, fmt.Errorf("%s must be YYYY-MM-DD: %w", label, err)
		}
	}
	if options.DateFrom != "" && options.DateTo != "" && options.DateFrom > options.DateTo {
		return options, fmt.Errorf("date_from must be on or before date_to")
	}
	return options, nil
}

func replayArchiveStage(
	ctx context.Context,
	db *sql.DB,
	writer LedgerWriter,
	options ArchiveReplayOptions,
	stageName string,
	predicate string,
) (ArchiveReplayStageReport, error) {
	query, args := buildArchiveReplayQuery(options, predicate)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return ArchiveReplayStageReport{}, fmt.Errorf("query archived %s messages: %w", stageName, err)
	}
	defer rows.Close()

	report := ArchiveReplayStageReport{Name: stageName}
	for rows.Next() {
		var streamKey string
		var streamID string
		var bodyText string
		if err := rows.Scan(&streamKey, &streamID, &bodyText); err != nil {
			return report, fmt.Errorf("scan archived %s message: %w", stageName, err)
		}
		report.Rows++
		result := ProcessLedgerEntry(ctx, writer, streamKey, streamID, map[string]any{"body": bodyText})
		report.Totals.add(result)
		if result.ParseErrors > 0 || result.LedgerErrors > 0 {
			report.ErrorCount++
			if len(report.Errors) < 50 {
				report.Errors = append(report.Errors, LedgerEntryError{
					StreamID: streamID,
					Error:    strings.Join(result.SkipReasons, "; "),
				})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("iterate archived %s messages: %w", stageName, err)
	}
	return report, nil
}

func buildArchiveReplayQuery(options ArchiveReplayOptions, predicate string) (string, []any) {
	var builder strings.Builder
	builder.WriteString(`
SELECT
    stream_key,
    stream_id,
    COALESCE(NULLIF(body_text, ''), body::text)
FROM raw_stream_messages
WHERE parse_error IS NULL
    AND COALESCE(NULLIF(body_text, ''), body::text) <> ''
    AND `)
	builder.WriteString(predicate)

	args := make([]any, 0, 3)
	appendCondition := func(sqlText string, value any) {
		args = append(args, value)
		builder.WriteString("\n    AND ")
		builder.WriteString(fmt.Sprintf(sqlText, len(args)))
	}
	if options.AccountID != "" {
		appendCondition("account_id = $%d", options.AccountID)
	}
	if options.DateFrom != "" {
		appendCondition("(received_at AT TIME ZONE 'Asia/Shanghai')::date >= $%d::date", options.DateFrom)
	}
	if options.DateTo != "" {
		appendCondition("(received_at AT TIME ZONE 'Asia/Shanghai')::date <= $%d::date", options.DateTo)
	}
	builder.WriteString("\nORDER BY received_at, raw_message_pk")
	return builder.String(), args
}
