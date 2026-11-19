package futures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const nativeFactsPriceSource = "oc_test_native_facts.v1"

// References are local evidence, not additions to OC command wire. The store
// re-reads the original receipt before reserving any trade publication.
type TestNativeFactsReference struct {
	CommandID           int64  `json:"command_id"`
	ReceiptID           int64  `json:"receipt_id"`
	BodySHA256          string `json:"body_sha256"`
	InstrumentStartedAt string `json:"instrument_started_at"`
	DepthStartedAt      string `json:"depth_started_at"`
}

func (r TestNativeFactsReference) validate() error {
	if r.CommandID <= 0 || r.ReceiptID <= 0 || len(r.BodySHA256) != 64 || strings.Trim(r.BodySHA256, "0123456789abcdef") != "" || !validTime(r.InstrumentStartedAt) || !validTime(r.DepthStartedAt) {
		return ErrStreamIdentity
	}
	return nil
}

type TestNativePricePreparation struct {
	Market              TestTradeMarket       `json:"market"`
	SuggestedLimitPrice Price                 `json:"suggested_limit_price"`
	ValidUntil          time.Time             `json:"valid_until"`
	Facts               InstrumentConstraints `json:"facts"`
	AtomicSnapshot      bool                  `json:"atomic_snapshot"`
	BusinessAuthorized  bool                  `json:"business_authorized"`
}

// PrepareTestNativePrice is read-only: no registration, Redis, ACK, permission
// or automatic price rewrite. Broker quote times remain distinct from sampling.
func (s *CommandStore) PrepareTestNativePrice(ctx context.Context, scope CommandScope, target TestQueryTarget, queryID, receiptID int64, mapping CatalogBinding) (TestNativePricePreparation, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return TestNativePricePreparation{}, err
	}
	defer tx.Rollback()
	return loadTestNativePrice(ctx, tx, scope, target, queryID, receiptID, mapping, time.Now())
}

func loadTestNativePrice(ctx context.Context, tx *sql.Tx, scope CommandScope, target TestQueryTarget, queryID, receiptID int64, mapping CatalogBinding, now time.Time) (TestNativePricePreparation, error) {
	bad := func() (TestNativePricePreparation, error) { return TestNativePricePreparation{}, ErrQueryPublishGate }
	if scope.validate() != nil || scope.Route.Env != "test" || scope.Route.BrokerID != "ctp_4900" || scope.Route.AccountID != "300052141" || scope.NativeBrokerID != "4900" || scope.UserID != "300052141" || scope.InvestorID != "300052141" || target.Validate() != nil || queryID <= 0 || receiptID <= 0 {
		return bad()
	}
	q, err := readRegisteredCommand(ctx, tx, string(commandRouteBytes(scope.Route)), queryID)
	if err != nil || q.Scope != scope || q.Original.Action != "instrument.constraints.query" || q.Original.Constraints == nil {
		return bad()
	}
	targetRaw, _ := json.Marshal(target)
	var same bool
	var state string
	var attempted time.Time
	if err := tx.QueryRowContext(ctx, `SELECT state,target_json=$2::jsonb,attempted_at FROM futures_test_query_publications WHERE command_id=$1`, queryID, targetRaw).Scan(&state, &same, &attempted); err != nil || !same || state != "published" || attempted.After(now) {
		return bad()
	}
	fields, _ := json.Marshal([]IntentField{{"action", "instrument.constraints.query"}, {"symbol", q.Original.Constraints.Symbol}, {"exchange", q.Original.Constraints.Exchange}})
	var superseded bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM futures_test_query_publications p JOIN futures_commands c USING(command_id) WHERE c.route_key=$1 AND c.fields_json @> $2::jsonb AND p.target_json=$3::jsonb AND (p.attempted_at>$4 OR (p.attempted_at=$4 AND p.command_id>$5)))`, string(commandRouteBytes(scope.Route)), fields, targetRaw, attempted, queryID).Scan(&superseded); err != nil || superseded {
		return TestNativePricePreparation{}, fmt.Errorf("%w: native_facts_superseded_or_unverifiable", ErrQueryPublishGate)
	}
	var raw, digest, configRaw []byte
	var stream string
	if err := tx.QueryRowContext(ctx, `SELECT i.body,i.body_sha256,i.stream_key,i.state,r.config_json FROM futures_stream_inbox i JOIN futures_stream_receivers r USING(stream_key) WHERE i.receipt_id=$1`, receiptID).Scan(&raw, &digest, &stream, &state, &configRaw); err != nil {
		return bad()
	}
	var config StreamConfig
	hash := sha256.Sum256(raw)
	if len(raw) > 262144 || state == "quarantined" || json.Unmarshal(configRaw, &config) != nil || config.Scope != scope || config.Role != "reply" || config.SourceID != target.SourceID || stream != queryReplyStream(scope.Route) || !bytes.Equal(hash[:], digest) {
		return bad()
	}
	transport, _ := json.Marshal(struct{ SourceID, RunID, Epoch, Group string }{target.SourceID, target.ServerRunID, target.Epoch, target.Group})
	if err := tx.QueryRowContext(ctx, `SELECT transport_json=$2::jsonb FROM futures_test_reply_sources WHERE stream_key=$1`, stream, transport).Scan(&same); err != nil || !same {
		return bad()
	}
	// A chosen success must not hide a retained conflicting reply for this
	// command. Bound the recent scan; high traffic fails closed rather than skips.
	rows, err := tx.QueryContext(ctx, `SELECT body,body_sha256,state FROM futures_stream_inbox WHERE stream_key=$1 AND received_at >= $2 ORDER BY receipt_id LIMIT 257`, stream, attempted)
	if err != nil {
		return bad()
	}
	count, size := 0, 0
	for rows.Next() {
		var b, h []byte
		var st string
		if err := rows.Scan(&b, &h, &st); err != nil {
			rows.Close()
			return bad()
		}
		count++
		size += len(b)
		if count > 256 || size > 4<<20 {
			rows.Close()
			return bad()
		}
		var envelope struct {
			Origin string `json:"origin_message_id"`
		}
		if json.Unmarshal(b, &envelope) == nil && envelope.Origin == q.Original.MessageID {
			sum := sha256.Sum256(b)
			if st == "quarantined" || !bytes.Equal(sum[:], h) || !bytes.Equal(b, raw) {
				rows.Close()
				return bad()
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return bad()
	}
	// Database reads must not renew a sample or freeze the freshness clock.
	return nativePriceFromReply(q, target, receiptID, raw, mapping, attempted, time.Now())
}

func nativePriceFromReply(q CommandRegistration, target TestQueryTarget, receiptID int64, raw []byte, mapping CatalogBinding, attempted, now time.Time) (TestNativePricePreparation, error) {
	bad := func() (TestNativePricePreparation, error) { return TestNativePricePreparation{}, ErrQueryPublishGate }
	bind := queryBinding(q)
	c := QueryContext{Route: q.Scope.Route, Action: q.Original.Action, OriginMessageID: q.Original.MessageID, CounterSessionID: target.CounterSessionID, ConnectionGeneration: &target.ConnectionGeneration, TradeDate: target.TradeDate, NativeBrokerID: q.Scope.NativeBrokerID, NativeInvestorID: q.Scope.InvestorID, NativeUserID: q.Scope.UserID, Command: &bind, Constraints: q.Original.Constraints}
	page, err := decodeQueryPage(raw, c)
	if err != nil || page.failed || !page.last || len(page.items) != 1 {
		return bad()
	}
	var f InstrumentConstraints
	if json.Unmarshal(page.items[0], &f) != nil || f.Exchange != "SHFE" || mapping.SourceInstrumentID != f.Symbol || mapping.ExchangeID != f.Exchange || !validID(mapping.SecurityID) || !validID(mapping.PublicationSnapshotID) || !validID(mapping.LogicalTableChecksum) || len(mapping.SecurityID) > 128 || len(mapping.PublicationSnapshotID) > 128 || len(mapping.LogicalTableChecksum) > 128 {
		return bad()
	}
	s, d := f.Static, f.Dynamic
	if s.ProductClass != "1" || s.InstLifePhase != "1" || s.IsTrading != 1 || s.OpenDate > target.TradeDate || s.ExpireDate < target.TradeDate || s.PriceTick == nil || s.VolumeMultiple == nil || *s.VolumeMultiple <= 0 || s.MinLimitOrderVolume == nil || *s.MinLimitOrderVolume <= 0 || *s.MinLimitOrderVolume > 1 || s.MaxLimitOrderVolume == nil || *s.MaxLimitOrderVolume < 1 || d.LowerLimitPrice == nil || d.UpperLimitPrice == nil {
		return bad()
	}
	o, _ := parseReply(raw)
	var adapter struct {
		Numeric  map[string]string `json:"numeric_source_binary64"`
		Evidence map[string]struct {
			Started string `json:"started_at"`
		} `json:"query_evidence"`
	}
	if json.Unmarshal(o["adapter_context"], &adapter) != nil {
		return bad()
	}
	// A TEST counter may have an empty bid book. Retain that fact and only
	// allow a lower-limit resting buy when a valid ask is strictly above it.
	if d.BidPrice1 == nil {
		if adapter.Numeric["BidPrice1"] != "7fefffffffffffff" || d.AskPrice1 == nil || validateLimitOrderPrice(*d.AskPrice1, *s.PriceTick, *d.LowerLimitPrice, *d.UpperLimitPrice) != nil {
			return bad()
		}
		ask, _ := checkedPositivePrice(*d.AskPrice1)
		lower, _ := checkedPositivePrice(*d.LowerLimitPrice)
		if ask.Cmp(lower) <= 0 {
			return bad()
		}
	}
	// Check the wire's binary64 provenance without using floating-point
	// arithmetic for tick alignment or price selection.
	for name, price := range map[string]*Price{"PriceTick": s.PriceTick, "LowerLimitPrice": d.LowerLimitPrice, "UpperLimitPrice": d.UpperLimitPrice, "BidPrice1": d.BidPrice1, "AskPrice1": d.AskPrice1} {
		if price == nil {
			continue
		}
		bits, err := hex.DecodeString(adapter.Numeric[name])
		value, parseErr := strconv.ParseFloat(string(*price), 64)
		if err != nil || len(bits) != 8 || parseErr != nil || math.Float64bits(value) != binary.BigEndian.Uint64(bits) {
			return bad()
		}
	}
	ref := &TestNativeFactsReference{q.CommandID, receiptID, commandDigest(raw), adapter.Evidence["instrument"].Started, adapter.Evidence["depth"].Started}
	m := TestTradeMarket{NativeFacts: ref, PriceSource: nativeFactsPriceSource, SecurityID: mapping.SecurityID, SourceInstrumentID: mapping.SourceInstrumentID, ExchangeID: mapping.ExchangeID, TradeDate: target.TradeDate, ObservedAt: d.CapturedAt, PublicationSnapshotID: mapping.PublicationSnapshotID, LogicalTableChecksum: mapping.LogicalTableChecksum, PriceTick: *s.PriceTick, LowerLimit: *d.LowerLimitPrice, UpperLimit: *d.UpperLimitPrice}
	if d.BidPrice1 != nil {
		m.BestBid = *d.BidPrice1
	}
	end, err := nativeFactsDeadline(m, now)
	if err != nil {
		return TestNativePricePreparation{}, err
	}
	start, _ := time.Parse(time.RFC3339Nano, ref.InstrumentStartedAt)
	staticEnd, _ := time.Parse(time.RFC3339Nano, s.CapturedAt)
	depthStart, _ := time.Parse(time.RFC3339Nano, ref.DepthStartedAt)
	produced, _ := time.Parse(time.RFC3339Nano, textField(o, "produced_at"))
	if start.Before(attempted) || staticEnd.Before(start) || staticEnd.After(depthStart) || produced.After(now) {
		return bad()
	}
	price, err := nativeTestBuyPrice(m)
	if err != nil {
		return TestNativePricePreparation{}, err
	}
	if d.AskPrice1 != nil {
		ask, ok := checkedPositivePrice(*d.AskPrice1)
		bid, _ := checkedPositivePrice(m.BestBid)
		if !ok || (bid != nil && ask.Cmp(bid) < 0) || validateLimitOrderPrice(*d.AskPrice1, m.PriceTick, m.LowerLimit, m.UpperLimit) != nil {
			return bad()
		}
	}
	return TestNativePricePreparation{Market: m, SuggestedLimitPrice: price, ValidUntil: end, Facts: f}, nil
}

func nativeFactsDeadline(m TestTradeMarket, now time.Time) (time.Time, error) {
	r := m.NativeFacts
	if m.PriceSource != nativeFactsPriceSource || r == nil || r.validate() != nil || m.Stale {
		return time.Time{}, ErrQueryPublishGate
	}
	static, _ := time.Parse(time.RFC3339Nano, r.InstrumentStartedAt)
	depth, _ := time.Parse(time.RFC3339Nano, r.DepthStartedAt)
	captured, err := time.Parse(time.RFC3339Nano, m.ObservedAt)
	if err != nil || static.After(depth) || depth.After(captured) || captured.After(now) {
		return time.Time{}, fmt.Errorf("%w: native_facts_clock_or_sequence_invalid", ErrQueryPublishGate)
	}
	if !now.Before(static.Add(300*time.Second)) || !now.Before(depth.Add(5*time.Second)) {
		return time.Time{}, fmt.Errorf("%w: native_facts_expired", ErrQueryPublishGate)
	}
	end := depth.Add(5 * time.Second)
	if static.Add(300 * time.Second).Before(end) {
		end = static.Add(300 * time.Second)
	}
	return end, nil
}

func nativeTestBuyPrice(m TestTradeMarket) (Price, error) {
	if m.BestBid == "" && m.PriceSource == nativeFactsPriceSource && m.NativeFacts != nil {
		if err := validateLimitOrderPrice(m.LowerLimit, m.PriceTick, m.LowerLimit, m.UpperLimit); err != nil {
			return "", err
		}
		return m.LowerLimit, nil
	}
	if err := validateLimitOrderPrice(m.BestBid, m.PriceTick, m.LowerLimit, m.UpperLimit); err != nil {
		return "", err
	}
	bid, _ := checkedPositivePrice(m.BestBid)
	tick, _ := checkedPositivePrice(m.PriceTick)
	lower, _ := checkedPositivePrice(m.LowerLimit)
	units := new(big.Rat).Quo(bid, tick).Num()
	units = new(big.Int).Sub(units, big.NewInt(10))
	lowUnits := new(big.Rat).Quo(lower, tick)
	minimum, remainder := new(big.Int), new(big.Int)
	minimum.QuoRem(lowUnits.Num(), lowUnits.Denom(), remainder)
	if remainder.Sign() != 0 {
		minimum.Add(minimum, big.NewInt(1))
	}
	if units.Cmp(minimum) < 0 {
		units.Set(minimum)
	}
	value := new(big.Rat).Mul(new(big.Rat).SetInt(units), tick)
	text := strings.TrimRight(strings.TrimRight(value.FloatString(9), "0"), ".")
	p := Price(text)
	parsed, ok := checkedPositivePrice(p)
	if !ok || parsed.Cmp(value) != 0 || validateLimitOrderPrice(p, m.PriceTick, m.LowerLimit, m.UpperLimit) != nil {
		return "", priceAdmissionError("invalid_native_suggestion")
	}
	return p, nil
}

func verifyTestNativeMarket(ctx context.Context, tx *sql.Tx, c CommandRegistration, target TestQueryTarget, market TestTradeMarket) error {
	if market.NativeFacts == nil || len(c.Original.Orders) != 1 {
		return ErrQueryPublishGate
	}
	r := market.NativeFacts
	p, err := loadTestNativePrice(ctx, tx, c.Scope, target, r.CommandID, r.ReceiptID, c.Evidence.Mappings[c.Original.Orders[0].GatewayOrderID], time.Now())
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p.Market, market) {
		return fmt.Errorf("%w: native_market_differs_from_receipt", ErrQueryPublishGate)
	}
	return nil
}
