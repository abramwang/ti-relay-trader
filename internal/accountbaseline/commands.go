package accountbaseline

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ti-relay-trader/internal/ledger"
)

// These are archive observations, not additional orders, freezes or a count of
// outstanding commands. A ledger association does not prove execution closure.
type CommandObservation struct {
	RecordID            string          `json:"record_id"`
	Source              string          `json:"source"`
	Evidence            Evidence        `json:"evidence"`
	IndexedMessageID    string          `json:"indexed_message_id"`
	LogicalCommandKey   string          `json:"logical_command_key"`
	Action              string          `json:"action"`
	OriginalBody        string          `json:"original_body"`
	ArchiveParseError   string          `json:"archive_parse_error"`
	ObservationOnly     bool            `json:"observation_only"`
	QuantityAdditive    bool            `json:"quantity_additive"`
	ExecutionVerified   bool            `json:"execution_verified"`
	ReplyCoverageProven bool            `json:"reply_coverage_proven"`
	Targets             []CommandTarget `json:"targets"`
	Flags               []string        `json:"flags"`
}

type CommandTarget struct {
	ChildIndex      *int              `json:"child_index"`
	GatewayOrderID  string            `json:"gateway_order_id"`
	OriginalPayload string            `json:"original_payload"`
	Association     string            `json:"association"`
	LedgerRecords   []json.RawMessage `json:"ledger_records"`
}

type commandLedgerRow struct {
	doc    json.RawMessage
	fields map[string]json.RawMessage
}

func appendCommandObservations(c *ledger.CurrentBaselineCandidate, src *ledger.CurrentBaselineCommandSources, budget *int) error {
	if src == nil {
		c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: "command_archives_not_collected", Resource: "commands", RecordIDs: []string{}, Detail: "No command collection was supplied; absence is not an empty command set."})
		return nil
	}
	if len(src.Archives) > 100000 || len(src.Orders) > 100000 {
		return ledger.ErrCurrentBaselineLimit
	}
	index := map[string][]commandLedgerRow{}
	for _, r := range src.Orders {
		*budget += len(r.Document)
		if *budget > 64<<20 {
			return ledger.ErrCurrentBaselineLimit
		}
		var m map[string]json.RawMessage
		if ledger.ValidateCurrentBaselineSourceJSON(r.Document) != nil || json.Unmarshal(r.Document, &m) != nil || text(m["account_id"]) != c.Scope.AccountID {
			return ledger.ErrCurrentBaselineInput
		}
		key := rowKey("command-target", text(m["origin_message_id"]), text(m["gateway_order_id"]))
		index[key] = append(index[key], commandLedgerRow{r.Document, m})
	}
	// Inspect all observations before linking: a later conflicting body must not
	// leave an earlier observation with the same message ID apparently trusted.
	type identity struct {
		hash     string
		count    int
		conflict bool
	}
	identities := map[string]identity{}
	seenArchives := map[string]bool{}
	for _, item := range src.Archives {
		r := item.Archive
		*budget += len(r.Body)
		if *budget > 64<<20 {
			return ledger.ErrCurrentBaselineLimit
		}
		if r.ArchiveID == "" || seenArchives[r.ArchiveID] || !utf8.ValidString(r.Body) {
			return ledger.ErrCurrentBaselineInput
		}
		seenArchives[r.ArchiveID] = true
		w, decodeErr := decode(r)
		claims := []string{item.IndexedMessageID}
		if w.MessageID != item.IndexedMessageID {
			claims = append(claims, w.MessageID)
		}
		for _, id := range claims {
			if id == "" {
				continue
			}
			x := identities[id]
			hash := digest([]byte(r.Body))
			x.conflict = x.conflict || decodeErr != nil || w.MessageID != item.IndexedMessageID || (x.count > 0 && x.hash != hash)
			x.hash, x.count = hash, x.count+1
			identities[id] = x
		}
	}
	c.Resources["commands"] = []ledger.CurrentBaselineRecord{}
	targetCount := 0
	for _, item := range src.Archives {
		r := item.Archive
		w, err := decode(r)
		o := CommandObservation{
			RecordID: "command-archive:" + r.ArchiveID, Source: "ledger_command_archive", Evidence: evidence(r, w),
			IndexedMessageID: item.IndexedMessageID, Action: w.Action, OriginalBody: r.Body, ArchiveParseError: r.ParseError,
			ObservationOnly: true, Targets: []CommandTarget{}, Flags: []string{},
		}
		trusted := err == nil && commandScopeValid(r, w, c.Scope, c.CompletedAt) && w.MessageID != "" && item.IndexedMessageID == w.MessageID
		if !trusted {
			o.Flags = append(o.Flags, "command_scope_or_archive_unproven")
		}
		if w.MessageID != "" {
			o.LogicalCommandKey = rowKey("command", c.Scope.Environment, c.Scope.AccountID, w.MessageID)
			if x := identities[w.MessageID]; x.conflict {
				o.Flags = append(o.Flags, "command_identity_conflict")
				trusted = false
			} else if x.count > 1 {
				o.Flags = append(o.Flags, "repeated_command_observation")
			}
		}
		if err == nil {
			var targets []json.RawMessage
			switch w.Action {
			case "order.submit", "order.cancel":
				var original struct {
					Payload json.RawMessage `json:"payload"`
				}
				_ = json.Unmarshal([]byte(r.Body), &original)
				targets = []json.RawMessage{original.Payload}
			case "order.batch.submit":
				if json.Unmarshal(w.Payload["orders"], &targets) != nil || len(targets) == 0 {
					o.Flags = append(o.Flags, "batch_children_invalid")
				}
			default:
				o.Flags = append(o.Flags, "command_action_unsupported")
			}
			targetCount += len(targets)
			if targetCount > 100000 {
				return ledger.ErrCurrentBaselineLimit
			}
			children := map[string]int{}
			for _, target := range targets {
				var m map[string]json.RawMessage
				_ = json.Unmarshal(target, &m)
				children[text(m["gateway_order_id"])]++
			}
			for i, target := range targets {
				var m map[string]json.RawMessage
				valid := json.Unmarshal(target, &m) == nil && m != nil
				t := CommandTarget{OriginalPayload: string(target), Association: "unrepresented", LedgerRecords: []json.RawMessage{}}
				if w.Action == "order.batch.submit" {
					n := i
					t.ChildIndex = &n
				}
				t.GatewayOrderID = text(m["gateway_order_id"])
				switch {
				case !trusted || !valid || text(m["account_id"]) != c.Scope.AccountID || t.GatewayOrderID == "":
					t.Association = "unverifiable"
				case children[t.GatewayOrderID] > 1:
					t.Association = "duplicate_child_identity"
				case w.Action == "order.cancel":
					t.Association = "cancel_outcome_unverified"
				default:
					key := commandIntentKey(m)
					if key == "" {
						t.Association = "unverifiable"
						break
					}
					for _, local := range index[rowKey("command-target", w.MessageID, t.GatewayOrderID)] {
						*budget += len(local.doc)
						if *budget > 64<<20 {
							return ledger.ErrCurrentBaselineLimit
						}
						t.LedgerRecords = append(t.LedgerRecords, local.doc)
						if orderMatchKey(local.fields) == key && t.Association != "ledger_intent_conflict" {
							t.Association = "ledger_intent_observed"
						} else {
							t.Association = "ledger_intent_conflict"
						}
					}
				}
				o.Targets = append(o.Targets, t)
			}
		}
		doc, err := json.Marshal(o)
		if err != nil {
			return err
		}
		*budget += len(doc)
		if *budget > 64<<20 {
			return ledger.ErrCurrentBaselineLimit
		}
		c.Resources["commands"] = append(c.Resources["commands"], ledger.CurrentBaselineRecord{Key: o.RecordID, Document: doc})
	}
	if src.Registrations == nil {
		c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: "command_registration_unavailable", Resource: "commands", RecordIDs: []string{}, Detail: "Write-ahead command registration is not installed; absence of archives cannot prove absence of publication."})
	} else {
		if len(src.Registrations)+len(c.Resources["commands"]) > 100000 {
			return ledger.ErrCurrentBaselineLimit
		}
		seen := map[string]bool{}
		for _, r := range src.Registrations {
			if r.AccountID != c.Scope.AccountID || r.Environment != c.Scope.Environment || r.ID == "" || seen[r.ID] {
				return ledger.ErrCurrentBaselineInput
			}
			seen[r.ID] = true
			key := "command-registration:" + r.ID
			doc, err := json.Marshal(struct {
				RecordID          string                         `json:"record_id"`
				Source            string                         `json:"source"`
				ObservationOnly   bool                           `json:"observation_only"`
				QuantityAdditive  bool                           `json:"quantity_additive"`
				ExecutionVerified bool                           `json:"execution_verified"`
				Registration      ledger.SecuritiesCommandRecord `json:"registration"`
			}{key, "durable_command_registration", true, false, false, r})
			if err != nil {
				return err
			}
			*budget += len(r.Body) + len(doc)
			if *budget > 64<<20 {
				return ledger.ErrCurrentBaselineLimit
			}
			c.Resources["commands"] = append(c.Resources["commands"], ledger.CurrentBaselineRecord{Key: key, Document: doc})
		}
		c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: "registered_command_execution_unverified", Resource: "commands", RecordIDs: []string{}, Detail: "Registration and Redis publication evidence only; no broker acceptance, execution closure, history backfill or all-writer coverage proof. Archive and registration observations are non-additive."})
	}
	sort.Slice(c.Resources["commands"], func(i, j int) bool { return c.Resources["commands"][i].Key < c.Resources["commands"][j].Key })
	c.Limitations = append(c.Limitations, ledger.CurrentBaselineLimitation{Code: "command_archive_coverage_unverified", Resource: "commands", RecordIDs: []string{}, Detail: "Bounded account cmd.trade archive observations only, not outstanding quantity. Legacy securities publication precedes archival; unarchived or mis-scoped commands, replies, execution and cancel closure remain unverified. Ledger intent links are not execution proof."})
	return nil
}

func commandScopeValid(r ledger.CurrentBaselineArchive, w wire, s ledger.CurrentBaselineScope, end time.Time) bool {
	env := s.Environment
	if env == "production" {
		env = "prod"
	}
	parts := strings.Split(r.Stream, ":")
	_, _, physical := physicalID(r.EntryID)
	if len(parts) != 6 || parts[0] != "relay" || parts[1] != env || parts[2] != "v1" || parts[3] != s.BrokerID || parts[4] == "" || parts[5] != "cmd.trade" || !physical || r.Direction != "in" || r.Role != "cmd.trade" || r.Receipt != nil || w.Protocol != "relay.stream.v1" || w.MessageType != "command" || text(w.Payload["account_id"]) != s.AccountID || w.SentAt.IsZero() || w.SentAt.After(end) || r.ReceivedAt.IsZero() || r.ReceivedAt.After(end) {
		return false
	}
	return (w.Routing.Env == "" || w.Routing.Env == env) && (w.Routing.BrokerID == "" || w.Routing.BrokerID == s.BrokerID) && (w.Routing.AccountID == "" || w.Routing.AccountID == s.AccountID) && (w.Routing.GatewayID == "" || w.Routing.GatewayID == parts[4])
}

func commandIntentKey(m map[string]json.RawMessage) string {
	for _, k := range []string{"account_id", "gateway_order_id", "trade_date", "symbol", "exchange", "trade_side", "business_type"} {
		if text(m[k]) == "" {
			return ""
		}
	}
	day := strings.ReplaceAll(text(m["trade_date"]), "-", "")
	if t, err := time.Parse("20060102", day); err != nil || t.Format("20060102") != day {
		return ""
	}
	qty, err := integer(m["qty"])
	if err != nil || qty == 0 {
		return ""
	}
	normalized := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		normalized[k] = v
	}
	normalized["trade_date"], _ = json.Marshal(day)
	normalized["order_qty"], normalized["limit_price"] = m["qty"], m["price"]
	return orderMatchKey(normalized)
}
