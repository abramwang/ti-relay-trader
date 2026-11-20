package orderflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ti-relay-trader/internal/config"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/trading"
)

type SecuritiesCommandJournal interface {
	Get(context.Context, string, string, string) (ledger.SecuritiesCommandRecord, error)
	GetByMessage(context.Context, string, string) (ledger.SecuritiesCommandRecord, error)
	Register(context.Context, ledger.SecuritiesCommandIntent) (ledger.SecuritiesCommandRecord, bool, error)
	Claim(context.Context, ledger.SecuritiesCommandRecord) error
	Complete(context.Context, ledger.SecuritiesCommandRecord, string) error
}

type singleAttemptPublisher interface {
	PublishCommandBodyOnce(context.Context, string, []byte) (redisstream.CommandPublishResult, error)
}

func configuredCommandJournal(opts Options) (SecuritiesCommandJournal, error) {
	if !opts.Config.Service.SecuritiesCommandRegistration {
		if opts.CommandJournal != nil {
			return nil, errors.New("command journal supplied without explicit enablement")
		}
		return nil, nil
	}
	if opts.Config.Service.Environment != config.EnvironmentTest || opts.Config.Redis.Env != "test" || !strings.HasSuffix(opts.Config.Database.ExpectedName, "_test") {
		return nil, errors.New("registered securities publication requires TEST")
	}
	if _, ok := opts.Publisher.(singleAttemptPublisher); !ok {
		return nil, errors.New("single-attempt original-body publisher required")
	}
	if opts.CommandJournal != nil {
		return opts.CommandJournal, nil
	}
	provider, ok := opts.Ledger.(interface {
		SecuritiesCommandStore(string) (*ledger.SecuritiesCommandStore, error)
	})
	if !ok {
		return nil, errors.New("durable securities command store required")
	}
	return provider.SecuritiesCommandStore(opts.Config.Database.ExpectedName)
}

func (s *Service) publishTradeCommand(ctx context.Context, stream string, envelope redisstream.CommandEnvelope, account, gatewayOrder string) (redisstream.CommandPublishResult, error) {
	if s.commandJournal == nil {
		if guard, ok := s.ledger.(interface {
			CheckLegacySecuritiesPublication(context.Context, string) error
		}); ok {
			if err := guard.CheckLegacySecuritiesPublication(ctx, account); err != nil {
				return redisstream.CommandPublishResult{}, err
			}
		}
		p, err := s.publisher.PublishCommand(ctx, stream, envelope)
		if err != nil {
			return p, err
		}
		return p, s.archiveCommand(ctx, p, envelope, redisstream.SuffixCmdTrade, account, gatewayOrder)
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return redisstream.CommandPublishResult{}, err
	}
	in := ledger.SecuritiesCommandIntent{Environment: "test", AccountID: account, StreamKey: stream, Action: envelope.Action, MessageID: envelope.MessageID, IdempotencyKey: envelope.IdempotencyKey, Body: string(body)}
	r, created, err := s.commandJournal.Register(ctx, in)
	if errors.Is(err, ledger.ErrSecuritiesCommandConflict) {
		return redisstream.CommandPublishResult{}, fmt.Errorf("%w: registered command differs", ErrIdempotencyConflict)
	}
	if err != nil {
		return redisstream.CommandPublishResult{}, err
	}
	if !created {
		return redisstream.CommandPublishResult{}, fmt.Errorf("%w: original message_id=%s", ledger.ErrSecuritiesCommandUncertain, r.MessageID)
	}
	if err = s.commandJournal.Claim(ctx, r); err != nil {
		return redisstream.CommandPublishResult{}, fmt.Errorf("%w: dispatch claim not confirmed for message_id=%s: %v", ledger.ErrSecuritiesCommandUncertain, r.MessageID, err)
	}
	p, err := s.publisher.(singleAttemptPublisher).PublishCommandBodyOnce(ctx, stream, []byte(r.Body))
	if err != nil {
		return p, fmt.Errorf("%w: message_id=%s", ledger.ErrSecuritiesCommandUncertain, r.MessageID)
	}
	if p.StreamKey != stream || p.BodyBytes != len(r.Body) {
		return p, ledger.ErrSecuritiesCommandUncertain
	}
	if err = s.commandJournal.Complete(ctx, r, p.StreamID); err != nil {
		return p, fmt.Errorf("%w: publication receipt not committed for message_id=%s: %v", ledger.ErrSecuritiesCommandUncertain, r.MessageID, err)
	}
	return p, nil
}

func registeredRaw(r ledger.SecuritiesCommandRecord) ledger.RawStreamMessage {
	var w redisstream.CommandEnvelope
	_ = json.Unmarshal([]byte(r.Body), &w)
	id := ""
	if r.StreamID != nil {
		id = *r.StreamID
	}
	return ledger.RawStreamMessage{StreamRef: ledger.StreamRef{Key: r.StreamKey, ID: id}, SourceRef: ledger.SourceRef{OriginMessageID: r.MessageID, RequestID: w.RequestID, CorrelationID: w.CorrelationID, IdempotencyKey: r.IdempotencyKey}, Action: r.Action, AccountID: r.AccountID, BodyText: r.Body, Body: json.RawMessage(r.Body)}
}

func registeredCommandMetadata(r ledger.SecuritiesCommandRecord) (commandReceiptMetadata, error) {
	if r.State != "published" || r.StreamID == nil {
		return commandReceiptMetadata{}, fmt.Errorf("%w: message_id=%s state=%s", ledger.ErrSecuritiesCommandUncertain, r.MessageID, r.State)
	}
	return metadataFromArchivedCommand(registeredRaw(r), r.IdempotencyKey, ""), nil
}

func (s *Service) registeredCancelReplay(ctx context.Context, req trading.CancelOrderRequest) (CancelOrderResult, bool, error) {
	if s.commandJournal == nil {
		return CancelOrderResult{}, false, nil
	}
	r, err := s.commandJournal.Get(ctx, req.AccountID, redisstream.ActionOrderCancel, req.IdempotencyKey)
	if errors.Is(err, ledger.ErrSecuritiesCommandNotFound) {
		return CancelOrderResult{}, false, nil
	}
	if err != nil {
		return CancelOrderResult{}, true, err
	}
	old, err := archivedCancelRequest(registeredRaw(r))
	if err != nil {
		return CancelOrderResult{}, true, err
	}
	if !sameCancelOrder(old, req) {
		return CancelOrderResult{}, true, ErrIdempotencyConflict
	}
	m, err := registeredCommandMetadata(r)
	if err != nil {
		return CancelOrderResult{}, true, err
	}
	order, err := s.ledger.GetOrder(ctx, req.AccountID, req.GatewayOrderID)
	if err != nil {
		return CancelOrderResult{}, true, err
	}
	return CancelOrderResult{AccountID: req.AccountID, Action: redisstream.ActionOrderCancel, Order: order, CancelID: old.CancelID, MessageID: m.MessageID, StreamKey: m.StreamKey, StreamID: m.StreamID, IdempotencyKey: m.IdempotencyKey, RequestID: m.RequestID, Published: m.Published, Replayed: true}, true, nil
}

func (s *Service) checkRegisteredBatch(ctx context.Context, req trading.BatchSubmitOrderRequest) error {
	if s.commandJournal == nil {
		return nil
	}
	r, err := s.commandJournal.Get(ctx, req.AccountID, redisstream.ActionOrderBatchSubmit, req.IdempotencyKey)
	if errors.Is(err, ledger.ErrSecuritiesCommandNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	payload, err := archivedCommandPayload(registeredRaw(r))
	if err != nil {
		return err
	}
	var old trading.BatchSubmitOrderRequest
	if json.Unmarshal(payload, &old) != nil || len(old.Orders) != len(req.Orders) {
		return ErrIdempotencyConflict
	}
	for i := range old.Orders {
		if !sameSubmitOrderRequests(old.Orders[i], req.Orders[i]) {
			return ErrIdempotencyConflict
		}
	}
	return nil
}
