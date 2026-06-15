package redisstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/trading"
)

type LedgerWriter interface {
	UpsertAccount(ctx context.Context, account trading.Account) error
	UpsertOrder(ctx context.Context, order trading.Order) error
	UpdateOrderStatus(ctx context.Context, event trading.OrderEvent) error
	AppendOrderEvent(ctx context.Context, event trading.OrderEvent, stream ledger.StreamRef, source ledger.SourceRef) error
	InsertFill(ctx context.Context, fill trading.Fill, stream ledger.StreamRef, source ledger.SourceRef) error
	ListFills(ctx context.Context, query trading.FillQuery) ([]trading.Fill, error)
	UpsertAssetSnapshot(ctx context.Context, asset trading.Asset, snapshotType string, source string, rawPayload any, capturedAt time.Time) error
	UpsertPosition(ctx context.Context, position trading.Position, source string, rawPayload any, updatedAt time.Time) error
	ArchiveRawStreamMessage(ctx context.Context, message ledger.RawStreamMessage) error
}

type LedgerSyncOptions struct {
	Prefixes []string
	StartID  string
	Count    int64
	Block    time.Duration
	Roles    []string
}

type LedgerSyncReport struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Protocol    string               `json:"protocol"`
	RedisAddr   string               `json:"redis_addr"`
	Prefixes    []string             `json:"prefixes"`
	StartID     string               `json:"start_id"`
	Count       int64                `json:"count"`
	Block       string               `json:"block,omitempty"`
	Totals      LedgerProcessResult  `json:"totals"`
	Streams     []LedgerStreamReport `json:"streams"`
}

type LedgerStreamReport struct {
	Name    string              `json:"name"`
	Role    string              `json:"role"`
	StartID string              `json:"start_id"`
	Count   int                 `json:"count"`
	Totals  LedgerProcessResult `json:"totals"`
	Errors  []LedgerEntryError  `json:"errors,omitempty"`
}

type LedgerEntryError struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error"`
}

type LedgerProcessResult struct {
	Seen           int      `json:"seen"`
	Archived       int      `json:"archived"`
	Accounts       int      `json:"accounts"`
	Orders         int      `json:"orders"`
	OrderEvents    int      `json:"order_events"`
	Fills          int      `json:"fills"`
	Assets         int      `json:"assets"`
	Positions      int      `json:"positions"`
	Replies        int      `json:"replies"`
	Skipped        int      `json:"skipped"`
	SkipReasons    []string `json:"skip_reasons,omitempty"`
	ParseErrors    int      `json:"parse_errors"`
	LedgerErrors   int      `json:"ledger_errors"`
	Unsupported    int      `json:"unsupported"`
	LastStreamID   string   `json:"last_stream_id,omitempty"`
	LastMessageID  string   `json:"last_message_id,omitempty"`
	LastEventType  string   `json:"last_event_type,omitempty"`
	LastAction     string   `json:"last_action,omitempty"`
	LastAccountID  string   `json:"last_account_id,omitempty"`
	AccountIDs     []string `json:"account_ids,omitempty"`
	LastGatewayOID string   `json:"last_gateway_order_id,omitempty"`
}

func SyncLedger(ctx context.Context, cfg config.Config, writer LedgerWriter, opts LedgerSyncOptions) (LedgerSyncReport, error) {
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		return LedgerSyncReport{}, errors.New("redis.url is required for ledger sync")
	}
	if writer == nil {
		return LedgerSyncReport{}, errors.New("ledger writer is required")
	}

	redisOptions, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return LedgerSyncReport{}, err
	}
	client := redis.NewClient(redisOptions)
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		return LedgerSyncReport{}, err
	}

	prefixes := opts.Prefixes
	if len(prefixes) == 0 {
		prefixes = PrefixesFromConfig(cfg)
	}
	if len(prefixes) == 0 {
		return LedgerSyncReport{}, errors.New("no stream prefixes configured")
	}

	startID := strings.TrimSpace(opts.StartID)
	if startID == "" {
		startID = "0"
	}
	count := opts.Count
	if count <= 0 {
		count = 100
	}
	roles := outputRoles(opts.Roles)

	report := LedgerSyncReport{
		GeneratedAt: timeutil.Now(),
		Protocol:    Protocol,
		RedisAddr:   maskRedisAddr(cfg.Redis.URL),
		Prefixes:    prefixes,
		StartID:     startID,
		Count:       count,
	}
	if opts.Block > 0 {
		report.Block = opts.Block.String()
	}

	for _, prefix := range prefixes {
		streams := NewStreams(prefix)
		for _, role := range roles {
			streamName := streamNameForRole(streams, role)
			if streamName == "" {
				continue
			}
			streamReport := readAndProcessStream(ctx, client, writer, streamName, role, startID, count, opts.Block)
			report.Streams = append(report.Streams, streamReport)
			report.Totals.add(streamReport.Totals)
		}
	}

	return report, nil
}

func ProcessLedgerEntry(ctx context.Context, writer LedgerWriter, stream, streamID string, values map[string]any) LedgerProcessResult {
	result := LedgerProcessResult{
		Seen:         1,
		LastStreamID: streamID,
	}

	envelope, err := DecodeEntry(stream, streamID, values)
	if err != nil {
		result.ParseErrors++
		result.Skipped++
		result.SkipReasons = append(result.SkipReasons, err.Error())
		if archiveErr := archiveParseError(ctx, writer, stream, streamID, values, err); archiveErr != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, archiveErr.Error())
		} else {
			result.Archived++
		}
		return result
	}

	result.LastMessageID = envelope.MessageID
	result.LastEventType = envelope.EventType
	result.LastAction = envelope.Action
	result.LastAccountID = envelope.Routing.AccountID
	result.noteAccount(envelope.Routing.AccountID)
	result.LastGatewayOID = firstNonEmpty(envelope.GatewayOrderID, gatewayOrderIDFromPayload(envelope.Payload))

	if err := writer.ArchiveRawStreamMessage(ctx, rawMessageFromEnvelope(envelope)); err != nil {
		result.LedgerErrors++
		result.SkipReasons = append(result.SkipReasons, err.Error())
		return result
	}
	result.Archived++

	switch envelope.MessageType {
	case "reply":
		result.Replies++
		return processReplyEnvelope(ctx, writer, envelope, result)
	case "event":
		return processEventEnvelope(ctx, writer, envelope, result)
	default:
		if envelope.EventType == "order.event" || envelope.EventType == "fill.event" {
			return processEventEnvelope(ctx, writer, envelope, result)
		}
		result.Unsupported++
		result.Skipped++
		result.SkipReasons = append(result.SkipReasons, "unsupported message_type "+envelope.MessageType)
		return result
	}
}

func processReplyEnvelope(ctx context.Context, writer LedgerWriter, envelope EntryEnvelope, result LedgerProcessResult) LedgerProcessResult {
	if envelope.Status == string(trading.ReplyStatusRejected) || envelope.Status == string(trading.ReplyStatusFailed) {
		return processRejectedOrderReply(ctx, writer, envelope, result)
	}

	switch {
	case envelope.Action == ActionAccountAsset || envelope.ResultType == "asset_page":
		asset, raw, err := assetFromReplyEnvelope(envelope)
		if err != nil {
			result.Skipped++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.noteAccount(asset.AccountID)
		if err := writer.UpsertAccount(ctx, accountFromEnvelope(envelope, asset.AccountID)); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.Accounts++
		capturedAt := envelope.ProducedAt
		if err := writer.UpsertAssetSnapshot(ctx, asset, "intraday", "query", raw, capturedAt); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.Assets++
		return result
	case envelope.Action == ActionAccountPositions || envelope.ResultType == "position_page":
		positions, raws, err := positionsFromReplyEnvelope(envelope)
		if err != nil {
			result.Skipped++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		accountID := envelope.Routing.AccountID
		if len(positions) > 0 {
			accountID = positions[0].AccountID
		}
		if accountID != "" {
			if err := writer.UpsertAccount(ctx, accountFromEnvelope(envelope, accountID)); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Accounts++
		}
		updatedAt := envelope.ProducedAt
		for i, position := range positions {
			result.noteAccount(position.AccountID)
			if err := writer.UpsertPosition(ctx, position, "query", raws[i], updatedAt); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Positions++
		}
		return result
	case envelope.Action == ActionOrderList || envelope.ResultType == "order_page":
		orders, err := ordersFromReplyEnvelope(envelope)
		if err != nil {
			result.Skipped++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		accountID := envelope.Routing.AccountID
		if len(orders) > 0 {
			accountID = orders[0].AccountID
		}
		if accountID != "" {
			if err := writer.UpsertAccount(ctx, accountFromEnvelope(envelope, accountID)); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Accounts++
		}
		for _, order := range orders {
			result.noteAccount(order.AccountID)
			if err := writer.UpsertOrder(ctx, order); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Orders++
			if err := synthesizeOrderSummaryFill(ctx, writer, envelope, order, "order_page", &result); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
		}
		return result
	case envelope.Action == ActionFillList || envelope.ResultType == "fill_page":
		fills, err := fillsFromReplyEnvelope(envelope)
		if err != nil {
			result.Skipped++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		accountID := envelope.Routing.AccountID
		if len(fills) > 0 {
			accountID = fills[0].AccountID
		}
		if accountID != "" {
			if err := writer.UpsertAccount(ctx, accountFromEnvelope(envelope, accountID)); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Accounts++
		}
		for _, fill := range fills {
			result.noteAccount(fill.AccountID)
			if err := writer.InsertFill(ctx, fill, streamRef(envelope), sourceRef(envelope)); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Fills++
		}
		return result
	default:
		return result
	}
}

func processRejectedOrderReply(ctx context.Context, writer LedgerWriter, envelope EntryEnvelope, result LedgerProcessResult) LedgerProcessResult {
	if envelope.Action != ActionOrderSubmit && envelope.Action != ActionOrderBatchSubmit {
		return result
	}
	accountID := envelope.Routing.AccountID
	gatewayOrderID := firstNonEmpty(envelope.GatewayOrderID, gatewayOrderIDFromPayload(envelope.Payload))
	if accountID == "" || gatewayOrderID == "" {
		result.Skipped++
		result.SkipReasons = append(result.SkipReasons, "rejected order reply missing account_id or gateway_order_id")
		return result
	}
	rejectCode, rejectMessage := orderErrorInfo(envelope)
	event := trading.OrderEvent{
		EventID:        envelope.MessageID,
		EventType:      trading.EventTypeOrder,
		AccountID:      accountID,
		GatewayOrderID: gatewayOrderID,
		Status:         trading.OrderStatusRejected,
		GatewayStatus:  trading.GatewayStatusRejected,
		IsTerminal:     true,
		Order: trading.Order{
			AccountID:       accountID,
			GatewayOrderID:  gatewayOrderID,
			Status:          trading.OrderStatusRejected,
			GatewayStatus:   trading.GatewayStatusRejected,
			IsTerminal:      true,
			RejectCode:      trading.ErrorCode(rejectCode),
			RejectMessage:   rejectMessage,
			OriginMessageID: envelope.OriginMessageID,
			RequestID:       envelope.RequestID,
			IdempotencyKey:  envelope.IdempotencyKey,
			LastUpdatedAt:   envelope.ProducedAt,
			TerminalAt:      envelope.ProducedAt,
			AdapterContext:  orderDebugContext(envelope, rejectCode, rejectMessage),
		},
		ProducedAt:     envelope.ProducedAt,
		AdapterContext: orderDebugContext(envelope, rejectCode, rejectMessage),
	}
	if err := writer.UpdateOrderStatus(ctx, event); err != nil {
		result.LedgerErrors++
		result.SkipReasons = append(result.SkipReasons, err.Error())
		return result
	}
	result.Orders++
	if err := writer.AppendOrderEvent(ctx, event, streamRef(envelope), sourceRef(envelope)); err != nil {
		result.LedgerErrors++
		result.SkipReasons = append(result.SkipReasons, err.Error())
		return result
	}
	result.OrderEvents++
	return result
}

func processEventEnvelope(ctx context.Context, writer LedgerWriter, envelope EntryEnvelope, result LedgerProcessResult) LedgerProcessResult {
	switch envelope.EventType {
	case "order.event":
		event, complete, err := orderEventFromEnvelope(envelope)
		if err != nil {
			result.Skipped++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		if complete {
			result.noteAccount(event.AccountID)
			if err := writer.UpsertAccount(ctx, accountFromEnvelope(envelope, event.AccountID)); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Accounts++
			if err := writer.UpsertOrder(ctx, event.Order); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Orders++
			if err := synthesizeOrderSummaryFill(ctx, writer, envelope, event.Order, "order.event", &result); err != nil {
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
		} else {
			result.noteAccount(event.AccountID)
			if err := writer.UpdateOrderStatus(ctx, event); err != nil {
				if errors.Is(err, ledger.ErrOrderNotFound) {
					result.Skipped++
					result.SkipReasons = append(result.SkipReasons, err.Error())
					return result
				}
				result.LedgerErrors++
				result.SkipReasons = append(result.SkipReasons, err.Error())
				return result
			}
			result.Orders++
		}
		if err := writer.AppendOrderEvent(ctx, event, streamRef(envelope), sourceRef(envelope)); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.OrderEvents++
		return result
	case "fill.event":
		fill, err := fillFromEnvelope(envelope)
		if err != nil {
			result.Skipped++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.noteAccount(fill.AccountID)
		if err := writer.UpsertAccount(ctx, accountFromEnvelope(envelope, fill.AccountID)); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.Accounts++
		if err := writer.InsertFill(ctx, fill, streamRef(envelope), sourceRef(envelope)); err != nil {
			result.LedgerErrors++
			result.SkipReasons = append(result.SkipReasons, err.Error())
			return result
		}
		result.Fills++
		return result
	default:
		result.Unsupported++
		result.Skipped++
		result.SkipReasons = append(result.SkipReasons, "unsupported event_type "+envelope.EventType)
		return result
	}
}

func readAndProcessStream(ctx context.Context, client *redis.Client, writer LedgerWriter, streamName, role, startID string, count int64, block time.Duration) LedgerStreamReport {
	report := LedgerStreamReport{
		Name:    streamName,
		Role:    role,
		StartID: startID,
	}

	result, err := client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{streamName, startID},
		Count:   count,
		Block:   block,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return report
		}
		report.Errors = append(report.Errors, LedgerEntryError{Error: err.Error()})
		report.Totals.LedgerErrors++
		return report
	}

	for _, stream := range result {
		for _, message := range stream.Messages {
			entryResult := ProcessLedgerEntry(ctx, writer, stream.Stream, message.ID, message.Values)
			report.Count++
			report.Totals.add(entryResult)
			if len(entryResult.SkipReasons) > 0 {
				report.Errors = append(report.Errors, LedgerEntryError{
					StreamID: message.ID,
					Error:    strings.Join(entryResult.SkipReasons, "; "),
				})
			}
		}
	}
	return report
}

func archiveParseError(ctx context.Context, writer LedgerWriter, stream, streamID string, values map[string]any, parseErr error) error {
	body, _ := bodyString(values["body"])
	return writer.ArchiveRawStreamMessage(ctx, ledger.RawStreamMessage{
		StreamRef:  ledger.StreamRef{Key: stream, ID: streamID},
		Direction:  directionFromRole(roleFromStream(stream)),
		Role:       roleFromStream(stream),
		BodyText:   body,
		ParseError: parseErr.Error(),
		ReceivedAt: time.Now().UTC(),
	})
}

func rawMessageFromEnvelope(envelope EntryEnvelope) ledger.RawStreamMessage {
	return ledger.RawStreamMessage{
		StreamRef: ledger.StreamRef{Key: envelope.Stream, ID: envelope.StreamID},
		SourceRef: ledger.SourceRef{
			OriginMessageID: firstNonEmpty(envelope.OriginMessageID, envelope.MessageID),
			RequestID:       envelope.RequestID,
			CorrelationID:   firstNonEmpty(envelope.CorrelationID, envelope.RequestCorrelationID),
			IdempotencyKey:  envelope.IdempotencyKey,
		},
		Direction:      directionFromRole(roleFromStream(envelope.Stream)),
		Role:           roleFromStream(envelope.Stream),
		MessageType:    envelope.MessageType,
		Action:         envelope.Action,
		EventType:      envelope.EventType,
		Status:         envelope.Status,
		Code:           envelope.Code,
		AccountID:      envelope.Routing.AccountID,
		GatewayOrderID: firstNonEmpty(envelope.GatewayOrderID, gatewayOrderIDFromPayload(envelope.Payload)),
		Body:           json.RawMessage(envelope.BodyText),
		BodyText:       envelope.BodyText,
		ReceivedAt:     time.Now().UTC(),
	}
}

func orderEventFromEnvelope(envelope EntryEnvelope) (trading.OrderEvent, bool, error) {
	var payload orderPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return trading.OrderEvent{}, false, fmt.Errorf("decode order.event payload: %w", err)
	}
	mergeEnvelopeFields(&payload, envelope)
	rawAccountID := normalizeAccountIDFromRouting(&payload.AccountID, envelope)
	if err := payload.validateOrderEventFields(); err != nil {
		return trading.OrderEvent{}, false, err
	}

	order := payload.toOrder(envelope)
	order.AdapterContext = withRawAccountID(order.AdapterContext, rawAccountID)
	complete := payload.completeOrderLedgerFields()
	event := trading.OrderEvent{
		EventID:        envelope.MessageID,
		EventType:      trading.EventTypeOrder,
		AccountID:      order.AccountID,
		GatewayOrderID: order.GatewayOrderID,
		Status:         order.Status,
		GatewayStatus:  order.GatewayStatus,
		IsTerminal:     order.IsTerminal,
		Order:          order,
		ProducedAt:     envelope.ProducedAt,
		AdapterContext: order.AdapterContext,
	}
	return event, complete, nil
}

func fillFromEnvelope(envelope EntryEnvelope) (trading.Fill, error) {
	var payload fillPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return trading.Fill{}, fmt.Errorf("decode fill.event payload: %w", err)
	}
	return fillFromPayload(payload, envelope, "fill.event")
}

func fillFromPayload(payload fillPayload, envelope EntryEnvelope, source string) (trading.Fill, error) {
	if payload.AccountID == "" {
		payload.AccountID = envelope.Routing.AccountID
	}
	rawAccountID := normalizeAccountIDFromRouting(&payload.AccountID, envelope)
	if payload.GatewayOrderID == "" {
		payload.GatewayOrderID = envelope.GatewayOrderID
	}
	if payload.FillID == "" {
		payload.FillID = stringFromMap(envelope.AdapterContext, "match_stream_id")
	}
	if payload.Fee == 0 {
		payload.Fee = floatFromMap(envelope.AdapterContext, "fee")
	}
	if err := payload.validate(); err != nil {
		if source != "fill.event" {
			return trading.Fill{}, errors.New(strings.NewReplacer("fill.event", source).Replace(err.Error()))
		}
		return trading.Fill{}, err
	}
	return trading.Fill{
		FillID:         payload.FillID,
		AccountID:      payload.AccountID,
		GatewayOrderID: payload.GatewayOrderID,
		OrderID:        payload.OrderID,
		OrderStreamID:  payload.OrderStreamID,
		Symbol:         payload.Symbol,
		Name:           payload.Name,
		Exchange:       trading.Exchange(payload.Exchange),
		TradeSide:      trading.TradeSide(payload.TradeSide),
		Price:          payload.Price,
		Qty:            payload.Qty,
		Fee:            payload.Fee,
		TradeDate:      payload.TradeDate,
		MatchTimestamp: payload.MatchTimestamp,
		MatchedAt:      parseTime(payload.MatchedAt),
		ShareholderID:  payload.ShareholderID,
		AdapterContext: withRawAccountID(envelope.AdapterContext, rawAccountID),
	}, nil
}

func assetFromReplyEnvelope(envelope EntryEnvelope) (trading.Asset, any, error) {
	var payload assetPagePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return trading.Asset{}, nil, fmt.Errorf("decode asset_page payload: %w", err)
	}
	if payload.Account.AccountID == "" {
		payload.Account.AccountID = envelope.Routing.AccountID
	}
	rawAccountID := normalizeAccountIDFromRouting(&payload.Account.AccountID, envelope)
	if strings.TrimSpace(payload.Account.AccountID) == "" {
		return trading.Asset{}, nil, fmt.Errorf("asset_page payload incomplete for ledger write: missing account_id")
	}
	asset := trading.Asset{
		AccountID:      payload.Account.AccountID,
		CashAvailable:  payload.Account.CashAvailable,
		CashTotal:      payload.Account.CashTotal,
		NetAsset:       payload.Account.NetAsset,
		MarketValue:    payload.Account.MarketValue,
		StockValue:     payload.Account.StockValue,
		FundValue:      payload.Account.FundValue,
		Commission:     payload.Account.Commission,
		DayProfit:      payload.Account.DayProfit,
		PositionProfit: payload.Account.PositionProfit,
		CloseProfit:    payload.Account.CloseProfit,
		Credit:         payload.Account.Credit,
		UpdatedAt:      envelope.ProducedAt,
	}
	if rawAccountID != "" {
		return asset, map[string]any{
			"account":        payload.Account,
			"raw_account_id": rawAccountID,
		}, nil
	}
	return asset, payload.Account, nil
}

func positionsFromReplyEnvelope(envelope EntryEnvelope) ([]trading.Position, []any, error) {
	var payload positionPagePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, nil, fmt.Errorf("decode position_page payload: %w", err)
	}
	positions := make([]trading.Position, 0, len(payload.Items))
	raws := make([]any, 0, len(payload.Items))
	for _, item := range payload.Items {
		if item.AccountID == "" {
			item.AccountID = envelope.Routing.AccountID
		}
		rawAccountID := normalizeAccountIDFromRouting(&item.AccountID, envelope)
		if strings.TrimSpace(item.AccountID) == "" {
			return nil, nil, fmt.Errorf("position_page payload incomplete for ledger write: missing account_id")
		}
		if strings.TrimSpace(item.Symbol) == "" {
			return nil, nil, fmt.Errorf("position_page payload incomplete for ledger write: missing symbol")
		}
		if strings.TrimSpace(item.Exchange) == "" {
			return nil, nil, fmt.Errorf("position_page payload incomplete for ledger write: missing exchange")
		}
		positions = append(positions, trading.Position{
			AccountID:     item.AccountID,
			Symbol:        item.Symbol,
			Name:          item.Name,
			Exchange:      trading.Exchange(item.Exchange),
			Quantity:      item.Quantity,
			SellableQty:   item.SellableQty,
			InitialQty:    item.InitialQty,
			TodayQty:      item.TodayQty,
			AvgCost:       item.AvgCost,
			LastPrice:     item.LastPrice,
			MarketValue:   item.MarketValue,
			UnrealizedPnL: item.UnrealizedPnL,
			SettledProfit: item.SettledProfit,
			ShareholderID: item.ShareholderID,
			UpdatedAt:     envelope.ProducedAt,
		})
		if rawAccountID != "" {
			raws = append(raws, map[string]any{
				"position":       item,
				"raw_account_id": rawAccountID,
			})
			continue
		}
		raws = append(raws, item)
	}
	return positions, raws, nil
}

func ordersFromReplyEnvelope(envelope EntryEnvelope) ([]trading.Order, error) {
	var payload orderPagePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode order_page payload: %w", err)
	}
	orders := make([]trading.Order, 0, len(payload.Items))
	for _, item := range payload.Items {
		if item.AccountID == "" {
			item.AccountID = envelope.Routing.AccountID
		}
		mergeEnvelopeFields(&item, envelope)
		rawAccountID := normalizeAccountIDFromRouting(&item.AccountID, envelope)
		if err := item.validateOrderPageFields(); err != nil {
			return nil, err
		}
		order := item.toOrder(envelope)
		order.AdapterContext = withRawAccountID(order.AdapterContext, rawAccountID)
		if order.LastUpdatedAt.IsZero() && !envelope.ProducedAt.IsZero() {
			order.LastUpdatedAt = envelope.ProducedAt
		}
		orders = append(orders, order)
	}
	return orders, nil
}

func fillsFromReplyEnvelope(envelope EntryEnvelope) ([]trading.Fill, error) {
	var payload fillPagePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode fill_page payload: %w", err)
	}
	fills := make([]trading.Fill, 0, len(payload.Items))
	for _, item := range payload.Items {
		fill, err := fillFromPayload(item, envelope, "fill_page")
		if err != nil {
			return nil, err
		}
		fills = append(fills, fill)
	}
	return fills, nil
}

func synthesizeOrderSummaryFill(ctx context.Context, writer LedgerWriter, envelope EntryEnvelope, order trading.Order, source string, result *LedgerProcessResult) error {
	targetQty := orderSummaryFilledQty(order)
	if targetQty <= 0 {
		return nil
	}
	existingQty, err := existingOrderFillQty(ctx, writer, order.AccountID, order.GatewayOrderID)
	if err != nil {
		return err
	}
	missingQty := targetQty - existingQty
	if missingQty <= 0 {
		return nil
	}
	price, priceSource := orderSummaryFillPrice(order)
	if price <= 0 {
		return fmt.Errorf("synthesize fill %s/%s: missing executable price", order.AccountID, order.GatewayOrderID)
	}
	matchedAt, matchedAtSource := summaryFillMatchedAt(order, source, envelope)
	context := map[string]any{
		"relay_synthesized":             true,
		"relay_synthesis_source":        source,
		"relay_synthesis_reason":        "order_filled_without_complete_fill_ledger",
		"relay_synthesis_target_qty":    targetQty,
		"relay_synthesis_existing_qty":  existingQty,
		"relay_synthesis_missing_qty":   missingQty,
		"relay_synthesis_price_source":  priceSource,
		"relay_synthesis_gateway_order": order.GatewayOrderID,
		"relay_synthesis_time_source":   matchedAtSource,
	}
	for key, value := range order.AdapterContext {
		if _, exists := context[key]; !exists {
			context[key] = value
		}
	}
	fill := trading.Fill{
		FillID:         "relay-summary:" + order.GatewayOrderID,
		AccountID:      order.AccountID,
		GatewayOrderID: order.GatewayOrderID,
		OrderID:        order.OrderID,
		OrderStreamID:  order.OrderStreamID,
		Symbol:         order.Symbol,
		Name:           order.Name,
		Exchange:       order.Exchange,
		TradeSide:      order.TradeSide,
		Price:          price,
		Qty:            missingQty,
		Fee:            0,
		TradeDate:      tradeDateFromTime(matchedAt),
		MatchTimestamp: unixMilliOrZero(matchedAt),
		MatchedAt:      matchedAt,
		ShareholderID:  order.ShareholderID,
		AdapterContext: context,
	}
	if err := writer.InsertFill(ctx, fill, summaryFillStreamRef(envelope, order.GatewayOrderID), sourceRef(envelope)); err != nil {
		return err
	}
	result.Fills++
	return nil
}

func summaryFillMatchedAt(order trading.Order, source string, envelope EntryEnvelope) (time.Time, string) {
	candidates := []struct {
		name  string
		value time.Time
	}{
		{name: "terminal_at", value: order.TerminalAt},
		{name: "last_updated_at", value: order.LastUpdatedAt},
		{name: "created_at", value: order.CreatedAt},
		{name: "inserted_at", value: order.InsertedAt},
		{name: "accepted_at", value: order.AcceptedAt},
		{name: "produced_at", value: envelope.ProducedAt},
	}
	if source == "order_page" {
		candidates = []struct {
			name  string
			value time.Time
		}{
			{name: "created_at", value: order.CreatedAt},
			{name: "inserted_at", value: order.InsertedAt},
			{name: "accepted_at", value: order.AcceptedAt},
			{name: "terminal_at", value: order.TerminalAt},
			{name: "last_updated_at", value: order.LastUpdatedAt},
			{name: "produced_at", value: envelope.ProducedAt},
		}
	}
	for _, candidate := range candidates {
		if !candidate.value.IsZero() {
			return candidate.value, candidate.name
		}
	}
	return time.Time{}, ""
}

func summaryFillStreamRef(envelope EntryEnvelope, gatewayOrderID string) ledger.StreamRef {
	stream := streamRef(envelope)
	if stream.ID != "" && gatewayOrderID != "" {
		stream.ID = stream.ID + ":summary:" + gatewayOrderID
	}
	return stream
}

func orderSummaryFilledQty(order trading.Order) int64 {
	isFilled := order.Status == trading.OrderStatusFilled || order.GatewayStatus == trading.GatewayStatusFilled
	if !isFilled {
		return 0
	}
	targetQty := firstPositiveInt(
		order.CumFilledQty,
		int64FromMap(order.AdapterContext, "dealt_vol"),
		int64FromMap(order.AdapterContext, "nDealtVol"),
	)
	if targetQty <= 0 && order.OrderQty > 0 {
		targetQty = order.OrderQty
	}
	if order.OrderQty > 0 && targetQty > order.OrderQty {
		targetQty = order.OrderQty
	}
	return targetQty
}

func existingOrderFillQty(ctx context.Context, writer LedgerWriter, accountID string, gatewayOrderID string) (int64, error) {
	const limit = 500
	var total int64
	for page := 0; page < 20; page++ {
		query := trading.FillQuery{
			AccountID:      accountID,
			GatewayOrderID: gatewayOrderID,
			Limit:          limit,
		}
		if page > 0 {
			query.Cursor = fmt.Sprintf("%d", page*limit)
		}
		fills, err := writer.ListFills(ctx, query)
		if err != nil {
			return 0, err
		}
		for _, fill := range fills {
			total += fill.Qty
		}
		if len(fills) < limit {
			break
		}
	}
	return total, nil
}

func orderSummaryFillPrice(order trading.Order) (float64, string) {
	if order.AvgFillPrice > 0 {
		return order.AvgFillPrice, "avg_fill_price"
	}
	if value := floatFromMap(order.AdapterContext, "avg_fill_price"); value > 0 {
		return value, "adapter_context.avg_fill_price"
	}
	if value := floatFromMap(order.AdapterContext, "avg_price"); value > 0 {
		return value, "adapter_context.avg_price"
	}
	if order.LimitPrice > 0 {
		return order.LimitPrice, "limit_price"
	}
	return 0, ""
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func tradeDateFromTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.In(timeutil.Location()).Format("2006-01-02")
}

func unixMilliOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

type orderPayload struct {
	AccountID         string  `json:"account_id"`
	ClientOrderID     string  `json:"client_order_id"`
	GatewayOrderID    string  `json:"gateway_order_id"`
	OrderID           int64   `json:"order_id"`
	OrderStreamID     string  `json:"order_stream_id"`
	Symbol            string  `json:"symbol"`
	Name              string  `json:"name"`
	Exchange          string  `json:"exchange"`
	TradeSide         string  `json:"trade_side"`
	BusinessType      string  `json:"business_type"`
	OffsetType        string  `json:"offset_type"`
	LimitPrice        float64 `json:"limit_price"`
	Price             float64 `json:"price"`
	OrderQty          int64   `json:"order_qty"`
	Qty               int64   `json:"qty"`
	SubmittedQty      int64   `json:"submitted_qty"`
	CumFilledQty      int64   `json:"cum_filled_qty"`
	LeavesQty         int64   `json:"leaves_qty"`
	CancelledQty      int64   `json:"cancelled_qty"`
	InvalidQty        int64   `json:"invalid_qty"`
	AvgFillPrice      float64 `json:"avg_fill_price"`
	Fee               float64 `json:"fee"`
	Status            string  `json:"status"`
	GatewayStatus     string  `json:"gateway_status"`
	AdapterStatusCode int     `json:"adapter_status_code"`
	AdapterStatusName string  `json:"adapter_status_name"`
	AdapterStatus     string  `json:"adapter_status"`
	IsTerminal        bool    `json:"is_terminal"`
	RejectCode        string  `json:"reject_code"`
	RejectMessage     string  `json:"reject_message"`
	ShareholderID     string  `json:"shareholder_id"`
	CreatedAt         string  `json:"created_at"`
	AcceptedAt        string  `json:"accepted_at"`
	InsertedAt        string  `json:"inserted_at"`
	LastUpdatedAt     string  `json:"last_updated_at"`
	UpdateTime        string  `json:"update_time"`
	TerminalAt        string  `json:"terminal_at"`
}

type fillPayload struct {
	FillID         string  `json:"fill_id"`
	AccountID      string  `json:"account_id"`
	GatewayOrderID string  `json:"gateway_order_id"`
	OrderID        int64   `json:"order_id"`
	OrderStreamID  string  `json:"order_stream_id"`
	Symbol         string  `json:"symbol"`
	Name           string  `json:"name"`
	Exchange       string  `json:"exchange"`
	TradeSide      string  `json:"trade_side"`
	Price          float64 `json:"price"`
	Qty            int64   `json:"qty"`
	Fee            float64 `json:"fee"`
	TradeDate      string  `json:"trade_date"`
	MatchTimestamp int64   `json:"match_timestamp"`
	MatchedAt      string  `json:"matched_at"`
	ShareholderID  string  `json:"shareholder_id"`
}

type assetPagePayload struct {
	Account assetPayload `json:"account"`
}

type assetPayload struct {
	AccountID      string  `json:"account_id"`
	CashAvailable  float64 `json:"cash_available"`
	CashTotal      float64 `json:"cash_total"`
	NetAsset       float64 `json:"net_asset"`
	MarketValue    float64 `json:"market_value"`
	StockValue     float64 `json:"stock_value"`
	FundValue      float64 `json:"fund_value"`
	Commission     float64 `json:"commission"`
	DayProfit      float64 `json:"day_profit"`
	PositionProfit float64 `json:"position_profit"`
	CloseProfit    float64 `json:"close_profit"`
	Credit         float64 `json:"credit"`
}

type positionPagePayload struct {
	Items []positionPayload `json:"items"`
}

type orderPagePayload struct {
	Items []orderPayload `json:"items"`
}

type fillPagePayload struct {
	Items []fillPayload `json:"items"`
}

type positionPayload struct {
	AccountID     string  `json:"account_id"`
	Symbol        string  `json:"symbol"`
	Name          string  `json:"name"`
	Exchange      string  `json:"exchange"`
	Quantity      int64   `json:"quantity"`
	SellableQty   int64   `json:"sellable_qty"`
	InitialQty    int64   `json:"initial_qty"`
	TodayQty      int64   `json:"today_qty"`
	AvgCost       float64 `json:"avg_cost"`
	LastPrice     float64 `json:"last_price"`
	MarketValue   float64 `json:"market_value"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	SettledProfit float64 `json:"settled_profit"`
	ShareholderID string  `json:"shareholder_id"`
}

func mergeEnvelopeFields(payload *orderPayload, envelope EntryEnvelope) {
	if payload.AccountID == "" {
		payload.AccountID = envelope.Routing.AccountID
	}
	if payload.GatewayOrderID == "" {
		payload.GatewayOrderID = envelope.GatewayOrderID
	}
	if payload.OrderID == 0 {
		payload.OrderID = int64FromMap(envelope.AdapterContext, "order_id")
	}
	if payload.OrderStreamID == "" {
		payload.OrderStreamID = stringFromMap(envelope.AdapterContext, "order_stream_id")
	}
	if payload.Fee == 0 {
		payload.Fee = floatFromMap(envelope.AdapterContext, "fee")
	}
	if payload.AdapterStatusCode == 0 {
		payload.AdapterStatusCode = int(int64FromMap(envelope.AdapterContext, "order_status_code"))
	}
	if payload.AdapterStatusName == "" {
		payload.AdapterStatusName = stringFromMap(envelope.AdapterContext, "order_status_name")
	}
}

func (payload orderPayload) validateOrderEventFields() error {
	missing := make([]string, 0)
	if strings.TrimSpace(payload.AccountID) == "" {
		missing = append(missing, "account_id")
	}
	if strings.TrimSpace(payload.GatewayOrderID) == "" {
		missing = append(missing, "gateway_order_id")
	}
	if len(missing) > 0 {
		return fmt.Errorf("order.event payload incomplete for ledger write: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func (payload orderPayload) validateOrderPageFields() error {
	if err := payload.validateOrderEventFields(); err != nil {
		return errors.New(strings.NewReplacer("order.event", "order_page").Replace(err.Error()))
	}
	missing := make([]string, 0)
	if strings.TrimSpace(payload.Symbol) == "" {
		missing = append(missing, "symbol")
	}
	if strings.TrimSpace(payload.Exchange) == "" {
		missing = append(missing, "exchange")
	}
	if strings.TrimSpace(payload.TradeSide) == "" {
		missing = append(missing, "trade_side")
	}
	if strings.TrimSpace(payload.BusinessType) == "" {
		missing = append(missing, "business_type")
	}
	if firstPositive(payload.LimitPrice, payload.Price) <= 0 {
		missing = append(missing, "limit_price")
	}
	if firstPositiveInt(payload.OrderQty, payload.Qty) <= 0 {
		missing = append(missing, "order_qty")
	}
	if len(missing) > 0 {
		return fmt.Errorf("order_page payload incomplete for ledger write: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func (payload orderPayload) completeOrderLedgerFields() bool {
	if strings.TrimSpace(payload.Symbol) == "" {
		return false
	}
	if strings.TrimSpace(payload.Exchange) == "" {
		return false
	}
	if strings.TrimSpace(payload.TradeSide) == "" {
		return false
	}
	if strings.TrimSpace(payload.BusinessType) == "" {
		return false
	}
	if firstPositive(payload.LimitPrice, payload.Price) <= 0 {
		return false
	}
	if firstPositiveInt(payload.OrderQty, payload.Qty) <= 0 {
		return false
	}
	return true
}

func (payload orderPayload) toOrder(envelope EntryEnvelope) trading.Order {
	limitPrice := firstPositive(payload.LimitPrice, payload.Price)
	orderQty := firstPositiveInt(payload.OrderQty, payload.Qty)
	status, gatewayStatus, inferredTerminal := trading.NormalizeOrderExecutionState(
		trading.OrderStatus(strings.TrimSpace(payload.Status)),
		trading.GatewayStatus(strings.TrimSpace(payload.GatewayStatus)),
		orderQty,
		payload.CumFilledQty,
		payload.LeavesQty,
	)
	isTerminal := payload.IsTerminal || inferredTerminal || status.Terminal() || gatewayStatus.Terminal()
	adapterStatusName := firstNonEmpty(payload.AdapterStatusName, payload.AdapterStatus)
	rejectCode, rejectMessage := orderPayloadRejectInfo(envelope, payload, status, gatewayStatus)
	adapterContext := orderDebugContext(envelope, rejectCode, rejectMessage)
	return trading.Order{
		AccountID:         payload.AccountID,
		ClientOrderID:     payload.ClientOrderID,
		GatewayOrderID:    payload.GatewayOrderID,
		OrderID:           payload.OrderID,
		OrderStreamID:     payload.OrderStreamID,
		Symbol:            payload.Symbol,
		Name:              payload.Name,
		Exchange:          trading.Exchange(payload.Exchange),
		TradeSide:         trading.TradeSide(payload.TradeSide),
		BusinessType:      trading.BusinessType(payload.BusinessType),
		OffsetType:        trading.OffsetType(payload.OffsetType),
		LimitPrice:        limitPrice,
		OrderQty:          orderQty,
		SubmittedQty:      payload.SubmittedQty,
		CumFilledQty:      payload.CumFilledQty,
		LeavesQty:         payload.LeavesQty,
		CancelledQty:      payload.CancelledQty,
		InvalidQty:        payload.InvalidQty,
		AvgFillPrice:      payload.AvgFillPrice,
		Fee:               payload.Fee,
		Status:            status,
		GatewayStatus:     gatewayStatus,
		AdapterStatusCode: payload.AdapterStatusCode,
		AdapterStatusName: adapterStatusName,
		IsTerminal:        isTerminal,
		RejectCode:        trading.ErrorCode(rejectCode),
		RejectMessage:     rejectMessage,
		OriginMessageID:   envelope.OriginMessageID,
		RequestID:         envelope.RequestID,
		IdempotencyKey:    envelope.IdempotencyKey,
		ShareholderID:     payload.ShareholderID,
		CreatedAt:         parseTime(payload.CreatedAt),
		AcceptedAt:        parseTime(payload.AcceptedAt),
		InsertedAt:        parseTime(payload.InsertedAt),
		LastUpdatedAt:     parseTime(firstNonEmpty(payload.LastUpdatedAt, payload.UpdateTime)),
		TerminalAt:        parseTime(payload.TerminalAt),
		AdapterContext:    adapterContext,
	}
}

func (payload fillPayload) validate() error {
	missing := make([]string, 0)
	if strings.TrimSpace(payload.AccountID) == "" {
		missing = append(missing, "account_id")
	}
	if strings.TrimSpace(payload.GatewayOrderID) == "" {
		missing = append(missing, "gateway_order_id")
	}
	if strings.TrimSpace(payload.Symbol) == "" {
		missing = append(missing, "symbol")
	}
	if strings.TrimSpace(payload.Exchange) == "" {
		missing = append(missing, "exchange")
	}
	if strings.TrimSpace(payload.TradeSide) == "" {
		missing = append(missing, "trade_side")
	}
	if payload.Price <= 0 {
		missing = append(missing, "price")
	}
	if payload.Qty <= 0 {
		missing = append(missing, "qty")
	}
	if len(missing) > 0 {
		return fmt.Errorf("fill.event payload incomplete for ledger write: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func accountFromEnvelope(envelope EntryEnvelope, accountID string) trading.Account {
	brokerID := firstNonEmpty(envelope.Routing.BrokerID, "unknown")
	return trading.Account{
		AccountID:      accountID,
		BrokerID:       brokerID,
		Status:         trading.AccountStatusEnabled,
		Enabled:        true,
		TradingEnabled: false,
		Simulated:      false,
		Tags: map[string]string{
			"source":     "redis-stream",
			"env":        envelope.Routing.Env,
			"gateway_id": envelope.Routing.GatewayID,
		},
		UpdatedAt: time.Now().UTC(),
	}
}

func streamRef(envelope EntryEnvelope) ledger.StreamRef {
	return ledger.StreamRef{Key: envelope.Stream, ID: envelope.StreamID}
}

func sourceRef(envelope EntryEnvelope) ledger.SourceRef {
	return ledger.SourceRef{
		OriginMessageID: envelope.OriginMessageID,
		RequestID:       envelope.RequestID,
		CorrelationID:   firstNonEmpty(envelope.CorrelationID, envelope.RequestCorrelationID),
		IdempotencyKey:  envelope.IdempotencyKey,
	}
}

func outputRoles(roles []string) []string {
	if len(roles) == 0 {
		return []string{SuffixReply, SuffixEvent}
	}
	normalized := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role != "" {
			normalized = append(normalized, role)
		}
	}
	return normalized
}

func streamNameForRole(streams Streams, role string) string {
	switch role {
	case SuffixReply:
		return streams.Reply
	case SuffixEvent:
		return streams.Event
	case SuffixHB:
		return streams.HB
	case SuffixDLQ:
		return streams.DLQ
	default:
		return ""
	}
}

func roleFromStream(stream string) string {
	if idx := strings.LastIndex(stream, ":"); idx >= 0 && idx+1 < len(stream) {
		return stream[idx+1:]
	}
	return ""
}

func directionFromRole(role string) string {
	switch role {
	case SuffixCmdTrade, SuffixCmdQuery:
		return "in"
	default:
		return "out"
	}
}

func gatewayOrderIDFromPayload(raw json.RawMessage) string {
	var payload struct {
		GatewayOrderID string `json:"gateway_order_id"`
	}
	_ = json.Unmarshal(raw, &payload)
	return payload.GatewayOrderID
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveInt(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func orderPayloadRejectInfo(envelope EntryEnvelope, payload orderPayload, status trading.OrderStatus, gatewayStatus trading.GatewayStatus) (string, string) {
	code, message := orderErrorInfo(envelope)
	code = firstNonEmpty(payload.RejectCode, code)
	message = firstNonEmpty(payload.RejectMessage, message)
	isRejected := status == trading.OrderStatusRejected ||
		gatewayStatus == trading.GatewayStatusRejected ||
		envelope.Status == string(trading.ReplyStatusRejected) ||
		envelope.Status == string(trading.ReplyStatusFailed) ||
		payload.RejectMessage != "" ||
		payload.RejectCode != "" ||
		stringFromMap(envelope.AdapterContext, "error_text") != ""
	if message == "" {
		if isRejected {
			return code, ""
		}
		return "", ""
	}
	if isRejected {
		return code, message
	}
	return "", ""
}

func normalizeAccountIDFromRouting(accountID *string, envelope EntryEnvelope) string {
	if accountID == nil {
		return ""
	}
	payloadAccountID := strings.TrimSpace(*accountID)
	routingAccountID := strings.TrimSpace(envelope.Routing.AccountID)
	if payloadAccountID == "" {
		*accountID = routingAccountID
		return ""
	}
	if routingAccountID == "" || payloadAccountID == routingAccountID {
		*accountID = payloadAccountID
		return ""
	}
	if strings.HasPrefix(payloadAccountID, routingAccountID) || strings.HasPrefix(routingAccountID, payloadAccountID) {
		*accountID = routingAccountID
		return payloadAccountID
	}
	*accountID = payloadAccountID
	return ""
}

func withRawAccountID(context map[string]any, rawAccountID string) map[string]any {
	if rawAccountID == "" {
		return context
	}
	out := make(map[string]any, len(context)+1)
	for key, value := range context {
		out[key] = value
	}
	out["relay_raw_account_id"] = rawAccountID
	return out
}

func orderErrorInfo(envelope EntryEnvelope) (string, string) {
	var payload map[string]any
	_ = json.Unmarshal(envelope.Payload, &payload)
	code := firstNonEmpty(
		stringFromMap(payload, "reject_code"),
		stringFromMap(payload, "error_code"),
		stringFromMap(payload, "err_code"),
		stringFromMap(payload, "code"),
		envelope.Code,
	)
	message := firstNonEmpty(
		stringFromMap(payload, "reject_message"),
		stringFromMap(payload, "error_message"),
		stringFromMap(payload, "error_msg"),
		stringFromMap(payload, "err_msg"),
		stringFromMap(payload, "error_text"),
		stringFromMap(payload, "status_msg"),
		stringFromMap(payload, "status_message"),
		stringFromMap(payload, "message"),
		stringFromMap(envelope.AdapterContext, "error_message"),
		stringFromMap(envelope.AdapterContext, "error_msg"),
		stringFromMap(envelope.AdapterContext, "err_msg"),
		stringFromMap(envelope.AdapterContext, "error_text"),
		stringFromMap(envelope.AdapterContext, "status_msg"),
		stringFromMap(envelope.AdapterContext, "status_message"),
		stringFromMap(envelope.AdapterContext, "broker_status_text"),
		envelope.Message,
	)
	return code, message
}

func orderDebugContext(envelope EntryEnvelope, rejectCode string, rejectMessage string) map[string]any {
	context := make(map[string]any, len(envelope.AdapterContext)+6)
	for key, value := range envelope.AdapterContext {
		context[key] = value
	}
	if envelope.Code != "" {
		context["relay_reply_code"] = envelope.Code
	}
	if envelope.Message != "" {
		context["relay_reply_message"] = envelope.Message
	}
	if envelope.Status != "" {
		context["relay_reply_status"] = envelope.Status
	}
	if rejectCode != "" {
		context["relay_error_code"] = rejectCode
	}
	if rejectMessage != "" {
		context["relay_error_message"] = rejectMessage
	}
	if len(context) == 0 {
		return nil
	}
	return context
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func floatFromMap(values map[string]any, key string) float64 {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		f, _ := value.Float64()
		return f
	default:
		return 0
	}
}

func int64FromMap(values map[string]any, key string) int64 {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case float64:
		return int64(value)
	case int:
		return int64(value)
	case int64:
		return value
	case json.Number:
		i, _ := value.Int64()
		return i
	default:
		return 0
	}
}

func (result *LedgerProcessResult) add(other LedgerProcessResult) {
	result.Seen += other.Seen
	result.Archived += other.Archived
	result.Accounts += other.Accounts
	result.Orders += other.Orders
	result.OrderEvents += other.OrderEvents
	result.Fills += other.Fills
	result.Assets += other.Assets
	result.Positions += other.Positions
	result.Replies += other.Replies
	result.Skipped += other.Skipped
	result.ParseErrors += other.ParseErrors
	result.LedgerErrors += other.LedgerErrors
	result.Unsupported += other.Unsupported
	result.SkipReasons = append(result.SkipReasons, other.SkipReasons...)
	if other.LastStreamID != "" {
		result.LastStreamID = other.LastStreamID
	}
	if other.LastMessageID != "" {
		result.LastMessageID = other.LastMessageID
	}
	if other.LastEventType != "" {
		result.LastEventType = other.LastEventType
	}
	if other.LastAction != "" {
		result.LastAction = other.LastAction
	}
	if other.LastAccountID != "" {
		result.LastAccountID = other.LastAccountID
	}
	for _, accountID := range other.AccountIDs {
		result.noteAccount(accountID)
	}
	if other.LastGatewayOID != "" {
		result.LastGatewayOID = other.LastGatewayOID
	}
}

func (result *LedgerProcessResult) noteAccount(accountID string) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return
	}
	for _, existing := range result.AccountIDs {
		if existing == accountID {
			return
		}
	}
	result.AccountIDs = append(result.AccountIDs, accountID)
}

var _ LedgerWriter = (*ledger.Repository)(nil)
