// Package accountbaseline builds reference-only publications from archived OC
// queries and unresolved ledger observations. It has no network or trading API.
package accountbaseline

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/redisstream"
)

type Evidence struct {
	ArchiveID         string                              `json:"archive_id"`
	Stream            string                              `json:"stream"`
	EntryID           string                              `json:"entry_id"`
	BodySHA256        string                              `json:"body_sha256"`
	MessageID         string                              `json:"message_id"`
	Status            string                              `json:"status"`
	IsLast            *bool                               `json:"is_last"`
	ReceivedAt        time.Time                           `json:"received_at"`
	BodySource        string                              `json:"body_source"`
	ArchiveBodySHA256 string                              `json:"archive_body_sha256"`
	Receipt           *ledger.CurrentBaselineReplyReceipt `json:"receipt,omitempty"`
}

type QueryReport struct {
	Resource               string     `json:"resource"`
	Action                 string     `json:"action"`
	OriginMessageID        string     `json:"origin_message_id"`
	RequestMatched         bool       `json:"request_matched"`
	TerminalSequenceValid  bool       `json:"terminal_sequence_valid"`
	ContextMatched         bool       `json:"context_matched"`
	ArchiveComplete        bool       `json:"archive_complete"`
	ProjectionValid        bool       `json:"projection_valid"`
	RetainedPagesReceipted bool       `json:"retained_pages_receipted"`
	ProjectionError        string     `json:"projection_error,omitempty"`
	ObservedRows           *int       `json:"observed_rows"`
	Commands               []Evidence `json:"commands"`
	Pages                  []Evidence `json:"pages"`
	Flags                  []string   `json:"flags"`
}

type wire struct {
	Protocol             string                     `json:"protocol"`
	MessageType          string                     `json:"message_type"`
	MessageID            string                     `json:"message_id"`
	OriginMessageID      string                     `json:"origin_message_id"`
	Action               string                     `json:"action"`
	ResultType           string                     `json:"result_type"`
	Status               string                     `json:"status"`
	SentAt               time.Time                  `json:"sent_at"`
	ProducedAt           time.Time                  `json:"produced_at"`
	Routing              redisstream.Routing        `json:"routing"`
	CounterSessionID     string                     `json:"counter_session_id"`
	ConnectionGeneration *int64                     `json:"connection_generation"`
	TradeDate            string                     `json:"trade_date"`
	Payload              map[string]json.RawMessage `json:"payload"`
	Adapter              map[string]json.RawMessage `json:"adapter_context"`
	Chunk                struct {
		IsLast *bool `json:"is_last"`
	} `json:"chunk"`
}

func digest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func (p *QueryReport) flag(code string) {
	for _, old := range p.Flags {
		if old == code {
			return
		}
	}
	p.Flags = append(p.Flags, code)
}

// Assemble never promotes archive closure into economic consistency. Even an
// empty successful query cannot grant missing_security_means_zero in this stage.
func Assemble(src ledger.CurrentBaselineSources, collectionID string, started time.Time) (ledger.CurrentBaselineCandidate, error) {
	c := ledger.CurrentBaselineCandidate{Scope: src.Scope, CollectionID: collectionID, StartedAt: started, CompletedAt: src.ReadAt, Resources: map[string][]ledger.CurrentBaselineRecord{}}
	if src.RevisionEvidence != nil {
		r := *src.RevisionEvidence
		c.RevisionEvidence = &r
	}
	c.SourceHealth = src.SourceHealth
	if started.IsZero() || src.ReadAt.Before(started) || src.Scope.ConnectionGeneration == nil || *src.Scope.ConnectionGeneration <= 0 || src.Scope.CounterSessionID == "" || len(src.Queries) != 4 {
		return c, ledger.ErrCurrentBaselineInput
	}
	if src.Scope.BrokerID != "huaxin" {
		return c, ledger.ErrCurrentBaselineScope
	}
	if src.Scope.Environment != "test" && src.Scope.Environment != "production" {
		return c, ledger.ErrCurrentBaselineInput
	}
	budget := 0
	for _, q := range src.Queries {
		for _, list := range [][]ledger.CurrentBaselineArchive{q.Commands, q.Replies} {
			if len(list) > 100000 {
				return c, ledger.ErrCurrentBaselineLimit
			}
			for _, r := range list {
				budget += len(r.Body)
				if r.Receipt != nil {
					raw, err := json.Marshal(r.Receipt)
					if err != nil {
						return c, err
					}
					budget += len(raw) + len(r.Receipt.OriginalBody)
				}
				if budget > 64<<20 {
					return c, ledger.ErrCurrentBaselineLimit
				}
			}
		}
	}
	for _, r := range src.UnresolvedOrders {
		budget += len(r.Document)
	}
	if budget > 64<<20 || len(src.UnresolvedOrders) > 100000 {
		return c, ledger.ErrCurrentBaselineLimit
	}
	var reports []QueryReport
	for _, resource := range []string{"asset", "positions", "orders", "fills"} {
		q, ok := src.Queries[resource]
		if !ok || q.MessageID == "" {
			return c, ledger.ErrCurrentBaselineInput
		}
		p, rows, cash := review(src, started, resource, q)
		if src.SourceHealth != nil && len(p.Pages) > 0 {
			var checkpoint string
			for _, cp := range src.SourceHealth.Checkpoints {
				if cp.Role == "reply" && cp.Found {
					checkpoint = cp.LastStreamID
				}
			}
			for _, page := range p.Pages {
				cmp, err := ledger.CompareCurrentBaselineStreamIDs(checkpoint, page.EntryID)
				if err != nil || cmp < 0 {
					p.flag("reply_checkpoint_not_caught_up")
				}
			}
		}
		if resource == "asset" {
			c.Cash = cash
		} else {
			c.Resources[resource] = rows
		}
		for _, code := range p.Flags {
			c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: code, Resource: resource, RecordIDs: []string{q.MessageID}, Detail: "See archived query evidence; this is not a complete account or execution proof."})
		}
		reports = append(reports, p)
	}
	if err := mergeUnresolved(&c, src.UnresolvedOrders); err != nil {
		return c, err
	}
	if err := appendCommandObservations(&c, src.TradeCommands, &budget); err != nil {
		return c, err
	}
	if src.SourceHealth != nil {
		for _, reason := range src.SourceHealth.Reasons {
			c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: reason, Resource: "account", RecordIDs: []string{}, Detail: "See local source observations; retained checkpoints are not a continuity or sampling-boundary proof."})
		}
	}
	c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: "command_and_event_coverage_unverified", Resource: "account", RecordIDs: []string{}, Detail: "The local orders read and revision are not proof of all commands, external activity, or query/event inclusion."})
	raw, err := json.Marshal(reports)
	if err != nil {
		return c, err
	}
	c.Queries = raw
	return c, nil
}

func decode(r ledger.CurrentBaselineArchive) (wire, error) {
	var w wire
	body := querySourceBody(r)
	if r.ParseError != "" || len(body) == 0 || ledger.ValidateCurrentBaselineSourceJSON(body) != nil {
		return w, errors.New("invalid original archive")
	}
	err := json.Unmarshal(body, &w)
	return w, err
}

func evidence(r ledger.CurrentBaselineArchive, w wire) Evidence {
	source := "legacy_archive"
	if r.Receipt != nil && r.Receipt.Status == "matched" {
		source = "durable_receipt"
	}
	return Evidence{ArchiveID: r.ArchiveID, Stream: r.Stream, EntryID: r.EntryID, BodySHA256: digest(querySourceBody(r)), MessageID: w.MessageID, Status: w.Status, IsLast: w.Chunk.IsLast, ReceivedAt: r.ReceivedAt,
		BodySource: source, ArchiveBodySHA256: digest([]byte(r.Body)), Receipt: r.Receipt}
}

func physicalID(id string) (uint64, uint64, bool) {
	a, b, ok := strings.Cut(id, "-")
	if !ok {
		return 0, 0, false
	}
	x, e1 := strconv.ParseUint(a, 10, 64)
	y, e2 := strconv.ParseUint(b, 10, 64)
	return x, y, e1 == nil && e2 == nil && strconv.FormatUint(x, 10) == a && strconv.FormatUint(y, 10) == b
}

func review(src ledger.CurrentBaselineSources, started time.Time, resource string, q ledger.CurrentBaselineQuerySources) (QueryReport, []ledger.CurrentBaselineRecord, json.RawMessage) {
	p := QueryReport{Resource: resource, Action: ledger.CurrentBaselineQueryActions[resource], OriginMessageID: q.MessageID, Commands: []Evidence{}, Pages: []Evidence{}, Flags: []string{}}
	wireEnv := src.Scope.Environment
	if wireEnv == "production" {
		wireEnv = "prod"
	}
	prefix := fmt.Sprintf("relay:%s:v1:%s:%s", wireEnv, src.Scope.BrokerID, src.Scope.GatewayID)
	for _, r := range q.Commands {
		w, e := decode(r)
		p.Commands = append(p.Commands, evidence(r, w))
		_, _, entryOK := physicalID(r.EntryID)
		if e != nil || !entryOK || len(q.Commands) != 1 || r.Direction != "in" || r.Role != "cmd.query" || r.Stream != prefix+":cmd.query" || w.Protocol != "relay.stream.v1" || w.MessageType != "command" || w.Action != p.Action || w.MessageID != q.MessageID || w.SentAt.IsZero() || w.SentAt.Before(started) || w.SentAt.After(src.ReadAt) || r.ReceivedAt.Before(started) || r.ReceivedAt.After(src.ReadAt) {
			p.flag("request_scope_unproven")
			continue
		}
		if (w.Routing.Env != "" && w.Routing.Env != wireEnv) || (w.Routing.BrokerID != "" && w.Routing.BrokerID != src.Scope.BrokerID) || (w.Routing.GatewayID != "" && w.Routing.GatewayID != src.Scope.GatewayID) || (w.Routing.AccountID != "" && w.Routing.AccountID != src.Scope.AccountID) {
			p.flag("request_scope_unproven")
			continue
		}
		bad := text(w.Payload["account_id"]) != src.Scope.AccountID
		for k, v := range w.Payload {
			switch k {
			case "account_id":
			case "trade_date":
				bad = bad || strings.ReplaceAll(text(v), "-", "") != src.Scope.TradeDate
			default:
				bad = true
			}
		}
		if bad {
			p.flag("filtered_or_invalid_request")
			continue
		}
		p.RequestMatched = true
	}
	if !p.RequestMatched {
		p.flag("request_scope_unproven")
	}
	valid := p.RequestMatched && len(q.Replies) > 0
	contextOK := true
	finals := 0
	seen := map[string]string{}
	var priorMS, priorSeq uint64
	var envelopes []redisstream.EntryEnvelope
	p.RetainedPagesReceipted = len(q.Replies) > 0
	for i, r := range q.Replies {
		linked, usable := reviewQueryReceipt(&p, r)
		p.RetainedPagesReceipted = p.RetainedPagesReceipted && linked
		valid = valid && usable
		w, e := decode(r)
		p.Pages = append(p.Pages, evidence(r, w))
		ms, seq, ok := physicalID(r.EntryID)
		if !ok || (i > 0 && (ms < priorMS || (ms == priorMS && seq <= priorSeq))) {
			valid = false
			p.flag("reply_order_or_identity_invalid")
		}
		priorMS, priorSeq = ms, seq
		if e != nil || r.Direction != "out" || r.Role != "reply" || r.Stream != prefix+":reply" || w.Protocol != "relay.stream.v1" || w.MessageType != "reply" || w.Action != p.Action || w.OriginMessageID != q.MessageID || w.ResultType != map[string]string{"asset": "asset_page", "positions": "position_page", "orders": "order_page", "fills": "fill_page"}[resource] || w.Routing != (redisstream.Routing{Env: wireEnv, BrokerID: src.Scope.BrokerID, GatewayID: src.Scope.GatewayID, AccountID: src.Scope.AccountID}) || w.ProducedAt.IsZero() || r.ReceivedAt.Before(started) || r.ReceivedAt.After(src.ReadAt) {
			valid = false
			p.flag("reply_scope_or_archive_invalid")
			continue
		}
		if w.MessageID == "" {
			valid = false
			p.flag("reply_order_or_identity_invalid")
		}
		if old, exists := seen[w.MessageID]; exists {
			valid = false
			if old != r.Body {
				p.flag("reply_identity_conflict")
			} else {
				p.flag("duplicate_reply")
			}
		}
		seen[w.MessageID] = r.Body
		if w.Chunk.IsLast == nil {
			valid = false
			p.flag("missing_final_flag")
		} else if w.Status == "completed" && *w.Chunk.IsLast {
			finals++
			if i != len(q.Replies)-1 {
				valid = false
				p.flag("final_not_last")
			}
		} else if w.Status != "partial" || *w.Chunk.IsLast {
			valid = false
			p.flag("failed_or_invalid_page")
		}
		matched, conflict := queryContext(w, src.Scope)
		if !matched {
			contextOK = false
			p.flag("query_context_unverified")
		}
		if conflict {
			valid = false
			p.flag("query_context_conflict")
		}
		env, e := redisstream.DecodeEntry(r.Stream, r.EntryID, map[string]any{"body": string(querySourceBody(r))})
		if e != nil {
			valid = false
			p.flag("reply_decode_failed")
			continue
		}
		envelopes = append(envelopes, env)
	}
	if finals != 1 {
		valid = false
		p.flag("single_final_missing")
	}
	p.TerminalSequenceValid = valid
	p.ContextMatched = contextOK && len(q.Replies) > 0
	// The current Huaxin wire has no guaranteed continuous page ordinal/count.
	// Redis entry IDs are not page sequence numbers. Do not invent that proof.
	p.flag("page_coverage_unverified")
	if !valid {
		return p, nil, nil
	}
	rows, cash, err := project(resource, envelopes)
	if err != nil {
		p.flag("resource_projection_failed")
		p.ProjectionError = err.Error()
		return p, nil, nil
	}
	p.ProjectionValid = true
	count := len(rows)
	if resource == "asset" {
		count = 1
	}
	p.ObservedRows = &count
	p.flag("sampling_boundary_unverified")
	if resource == "asset" || resource == "positions" {
		p.flag("freeze_semantics_unverified")
	}
	return p, rows, cash
}

func queryContext(w wire, s ledger.CurrentBaselineScope) (bool, bool) {
	session, generation, date := false, false, false
	conflict := false
	sets := []map[string]json.RawMessage{w.Payload, w.Adapter}
	if w.CounterSessionID != "" {
		session = true
		conflict = w.CounterSessionID != s.CounterSessionID
	}
	if w.ConnectionGeneration != nil {
		generation = true
		conflict = conflict || *w.ConnectionGeneration != *s.ConnectionGeneration
	}
	if w.TradeDate != "" {
		date = true
		conflict = conflict || strings.ReplaceAll(w.TradeDate, "-", "") != s.TradeDate
	}
	for _, m := range sets {
		if v, ok := m["counter_session_id"]; ok && string(v) != "null" {
			session = true
			conflict = conflict || text(v) != s.CounterSessionID
		}
		if v, ok := m["connection_generation"]; ok && string(v) != "null" {
			generation = true
			n, e := integer(v)
			conflict = conflict || e != nil || n != *s.ConnectionGeneration
		}
		if v, ok := m["trade_date"]; ok && string(v) != "null" {
			date = true
			conflict = conflict || strings.ReplaceAll(text(v), "-", "") != s.TradeDate
		}
	}
	return session && generation && date && !conflict, conflict
}

func text(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
func integer(raw json.RawMessage) (int64, error) {
	n, e := strconv.ParseInt(string(raw), 10, 64)
	if e != nil || n < 0 {
		return 0, errors.New("missing or invalid integer")
	}
	return n, nil
}
func rowKey(kind string, parts ...string) string {
	raw, _ := json.Marshal(parts)
	return kind + ":" + digest(raw)
}

// Keep unresolved observations even if a newer query shows a terminal order.
// A matching immutable intent is one resource row with both observations, not
// two quantities for the consumer to add. Disagreements remain separate.
func mergeUnresolved(c *ledger.CurrentBaselineCandidate, local []ledger.CurrentBaselineRecord) error {
	index := map[string]int{}
	identities := map[string]string{}
	for i, r := range c.Resources["orders"] {
		var m map[string]json.RawMessage
		if json.Unmarshal(r.Document, &m) != nil {
			return ledger.ErrCurrentBaselineInput
		}
		index[orderMatchKey(m)] = i
		identities[orderIdentity(m)] = r.Key
	}
	seen := map[string]bool{}
	for _, r := range local {
		if seen[r.Key] || ledger.ValidateCurrentBaselineSourceJSON(r.Document) != nil {
			return ledger.ErrCurrentBaselineInput
		}
		seen[r.Key] = true
		var m map[string]json.RawMessage
		if json.Unmarshal(r.Document, &m) != nil || text(m["account_id"]) != c.Scope.AccountID {
			return ledger.ErrCurrentBaselineScope
		}
		key := orderMatchKey(m)
		if i, ok := index[key]; ok && key != "" {
			// Re-marshal through RawMessage fields so integer quantities never
			// pass through float64 while attaching the ledger observations.
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(c.Resources["orders"][i].Document, &raw)
			var observations []json.RawMessage
			if old := raw["ledger_observations"]; len(old) > 0 {
				_ = json.Unmarshal(old, &observations)
			}
			observations = append(observations, r.Document)
			raw["ledger_observations"], _ = json.Marshal(observations)
			raw["has_unresolved_ledger_observation"] = json.RawMessage(`true`)
			raw["execution_state"] = json.RawMessage(`"reconciliation_required"`)
			c.Resources["orders"][i].Document, _ = json.Marshal(raw)
		} else {
			if other, ok := identities[orderIdentity(m)]; ok && orderIdentity(m) != "" {
				c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: "order_observation_conflict", Resource: "orders", RecordIDs: []string{other, r.Key}, Detail: "Same order identity has different or missing immutable intent fields; observations are not additive and require reconciliation."})
			}
			c.Resources["orders"] = append(c.Resources["orders"], r)
		}
	}
	if len(local) > 0 {
		c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: "unresolved_ledger_orders", Resource: "orders", RecordIDs: []string{}, Detail: "Cross-day and local unknown responsibilities retained; broker absence or terminal observations do not resolve them."})
	}
	sort.Slice(c.Resources["orders"], func(i, j int) bool { return c.Resources["orders"][i].Key < c.Resources["orders"][j].Key })
	return nil
}

func orderIdentity(m map[string]json.RawMessage) string {
	if text(m["gateway_order_id"]) == "" || text(m["trade_date"]) == "" {
		return ""
	}
	return rowKey("order", text(m["account_id"]), text(m["trade_date"]), text(m["gateway_order_id"]))
}

func orderMatchKey(m map[string]json.RawMessage) string {
	if text(m["gateway_order_id"]) == "" || text(m["trade_date"]) == "" {
		return ""
	}
	qty, e := integer(m["order_qty"])
	if e != nil {
		return ""
	}
	price, e := decimal(m["limit_price"])
	if e != nil {
		return ""
	}
	return rowKey("match", text(m["account_id"]), text(m["trade_date"]), text(m["gateway_order_id"]), text(m["symbol"]), text(m["exchange"]), text(m["trade_side"]), text(m["business_type"]), strconv.FormatInt(qty, 10), price)
}
