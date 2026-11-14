package performance

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"ti-relay-trader/internal/ledger"
)

type ResearchPublicationGate struct {
	Code   string `json:"code"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type ResearchCostInventoryDay struct {
	TradeDate string         `json:"trade_date"`
	Rows      int            `json:"rows"`
	Buckets   map[string]int `json:"bucket_rows"`
	Statuses  map[string]int `json:"status_rows"`
	Fees      map[string]int `json:"fee_source_rows"`
	RowFlags  map[string]int `json:"row_flag_counts"`
	DayFlags  []string       `json:"day_flags"`
}

type ResearchCostSemanticOccurrence struct {
	TradeDate  string `json:"trade_date"`
	SecurityID string `json:"security_id,omitempty"`
	CostBucket string `json:"cost_bucket,omitempty"`
	Scope      string `json:"scope"`
}

type ResearchCostSemanticReview struct {
	Code        string                           `json:"code"`
	Detail      string                           `json:"detail"`
	Occurrences []ResearchCostSemanticOccurrence `json:"occurrences"`
}

// This is a read-only inventory, never a publication authorization or a quality
// waiver. Independent query/CSV audits are not implicitly attached by name.
type ResearchCostPublicationReview struct {
	Schema          string                       `json:"schema"`
	AccountID       string                       `json:"account_id"`
	VersionID       string                       `json:"version_id"`
	PayloadSHA256   string                       `json:"payload_sha256"`
	Source          ResearchSourcePreflight      `json:"source_preflight"`
	Structure       CostContinuityReview         `json:"structural_review"`
	FormalTargets   ResearchCostComparisonCounts `json:"formal_target_comparison"`
	Days            []ResearchCostInventoryDay   `json:"days"`
	Semantics       []ResearchCostSemanticReview `json:"semantic_reviews"`
	CostGates       []ResearchPublicationGate    `json:"cost_gates"`
	NAVGates        []ResearchPublicationGate    `json:"economic_nav_gates"`
	CostPublishable bool                         `json:"cost_publishable"`
	NAVPublishable  bool                         `json:"economic_nav_publishable"`
	DatabaseWrites  int                          `json:"database_writes"`
	BrokerCommands  int                          `json:"broker_commands"`
}

func researchPublicationBucket(bucket string) string {
	switch {
	case bucket == "CORE":
		return "CORE"
	case isT0ResidualBucket(bucket):
		return "ETF_T0_RESIDUAL"
	case isComponentResidualBucket(bucket):
		return "ETF_T0_COMPONENT"
	case strings.HasPrefix(bucket, "ETF_T0:"):
		return "ETF_T0"
	default:
		return "UNKNOWN"
	}
}

// These labels describe review work, not an allowlist for publishing money.
func researchPublicationFlag(flag string) (string, string) {
	switch flag {
	case "t0_residual_preview_only":
		return "residual_persistence", "Separate residual ownership, next-day cost carry and attribution must be supported by the formal reader/writer."
	case componentResidualPreviewFlag, "component_marked_basis_not_acquisition_cost":
		return "component_marked_basis", "Component marked basis is not acquisition cost; preserve source redemption, avoid counting source-day appreciation twice, and support next-day readers."
	case closedDayPoolFlag, "per_redemption_buy_allocation_unasserted":
		return "closed_day_pool", "A closed daily pool proves aggregate cost only; do not invent a buy allocation for each redemption."
	case overnightT0ChainFlag, "iopv_release_not_cash":
		return "overnight_cost_release", "Carry the confirmed overnight lot separately; IOPV release is not cash and source-day expense must not be charged again."
	case "historical_t0_order_group_inferred":
		return "historical_group_provenance", "Revalidate inferred grouping against frozen orders, fills and active attribution evidence; it is not a new confirmed audit."
	case "component_sale_unique_inventory_inferred", "historical_component_sale_link_inferred":
		return "component_sale_provenance", "Preserve parent transfer and sale links, temporal ordering and competing inventory exclusions; inferred links are not broker-provided parent identities."
	case "component_transfer_sell_quantity_mismatch", "component_transfer_without_closing_inventory_unclassified":
		return "component_transfer_scope", "Review unmatched transfers separately from proven physical holdings; do not fabricate stock lots or assume a cash substitution without source evidence."
	case "aggregate_cost_bucket_open_reconciliation", "aggregate_cost_bucket_quantity_reconciliation", "aggregate_quantity_reconciled_on_core", "closed_t0_inventory_verified", "etf_t0_cost_separated", "etf_t0_redemption_cost_released", "etf_t0_residual_cost_separated", "component_sales_excluded_from_position_cost", "component_sales_linked_to_transfer", "etf_redemption_from_transfer_ledger", "reverse_repo_excluded_from_position_cost":
		return "", ""
	default:
		return "unreviewed_flag:" + flag, "Unclassified quality flag requires explicit review; never discard it to permit publication."
	}
}

func ReviewResearchCostPublication(v ledger.ResearchCostVersion, c ResearchCostComparison, s ResearchSourcePreflight) (ResearchCostPublicationReview, error) {
	r := ResearchCostPublicationReview{}
	b, err := DecodeResearchCostVersion(v)
	if err != nil {
		return r, err
	}
	hash, err := SourceObservationHash(s.Observation)
	if err != nil || hash != s.ObservationSHA256 || s.Schema != "relay.research_source_preflight.v1" ||
		s.CostPublishable || s.NAVPublishable || s.DatabaseWrites != 0 || s.BrokerCommands != 0 ||
		c.AccountID != v.AccountID || c.VersionID != v.VersionID || c.PayloadSHA256 != v.PayloadSHA256 || !c.ResearchOnly || c.NAVPublishable ||
		s.Observation.AccountID != v.AccountID || s.Observation.VersionID != v.VersionID || s.Observation.PayloadSHA256 != v.PayloadSHA256 ||
		s.Observation.DateFrom != b.AnchorDate || s.Observation.DateTo != v.DateTo || len(c.Days) != len(b.Costs) ||
		s.AnchorAndAllocationsMatch != c.AnchorAndAllocationsMatch || !reflect.DeepEqual(s.EvidenceChanges, c.EvidenceChanges) {
		return r, fmt.Errorf("publication review scope/integrity mismatch")
	}
	// Reconstruct the comparison from its stored side to reject incomplete or
	// mismatched research rows/counts without treating missing formal rows as zero.
	var stored []ledger.PositionCostState
	for _, day := range c.Days {
		for _, row := range day.Rows {
			if row.Stored != nil {
				stored = append(stored, *row.Stored)
			}
		}
	}
	checked, err := CompareResearchCostVersion(v, b.Opening, b.Audits, stored)
	if err != nil || !reflect.DeepEqual(checked.Days, c.Days) || checked.Counts != c.Counts {
		return r, fmt.Errorf("publication review incomplete cost comparison")
	}
	r = ResearchCostPublicationReview{Schema: "relay.research_cost_publication_review.v1", AccountID: b.AccountID, VersionID: b.VersionID, PayloadSHA256: v.PayloadSHA256, Source: s, Structure: b.Review, FormalTargets: c.Counts}
	issues := map[string]*ResearchCostSemanticReview{}
	add := func(code, detail string, at ResearchCostSemanticOccurrence) {
		if code == "" {
			return
		}
		if issues[code] == nil {
			issues[code] = &ResearchCostSemanticReview{Code: code, Detail: detail}
		}
		for _, old := range issues[code].Occurrences {
			if old == at {
				return
			}
		}
		issues[code].Occurrences = append(issues[code].Occurrences, at)
	}
	for _, day := range b.Costs {
		d := ResearchCostInventoryDay{TradeDate: day.TradeDate, Rows: len(day.Positions), Buckets: map[string]int{}, Statuses: map[string]int{}, Fees: map[string]int{}, RowFlags: map[string]int{}, DayFlags: append([]string{}, day.QualityFlags...)}
		sort.Strings(d.DayFlags)
		for _, flag := range d.DayFlags {
			code, detail := researchPublicationFlag(flag)
			add(code, detail, ResearchCostSemanticOccurrence{TradeDate: day.TradeDate, Scope: "day"})
		}
		for _, p := range day.Positions {
			kind := researchPublicationBucket(p.CostBucket)
			d.Buckets[kind]++
			d.Statuses[p.Status]++
			d.Fees[p.FeeSource]++
			at := ResearchCostSemanticOccurrence{day.TradeDate, p.Symbol + "." + p.Exchange, p.CostBucket, "row"}
			if kind == "ETF_T0_RESIDUAL" {
				code, detail := researchPublicationFlag("t0_residual_preview_only")
				add(code, detail, at)
			}
			seen := map[string]bool{}
			for _, flag := range p.QualityFlags {
				if !seen[flag] {
					d.RowFlags[flag]++
					seen[flag] = true
				}
				code, detail := researchPublicationFlag(flag)
				add(code, detail, at)
			}
			if kind == "ETF_T0" || kind == "ETF_T0_COMPONENT" || p.FeeSource == "all_in_t0_friction_less_source_day_expense" {
				add("estimated_friction_not_final_fees", "Preserve estimated all-in T0 friction and its scope; fee_complete does not establish final broker settlement.", at)
			}
		}
		r.Days = append(r.Days, d)
	}
	keys := make([]string, 0, len(issues))
	for code := range issues {
		keys = append(keys, code)
	}
	sort.Strings(keys)
	for _, code := range keys {
		item := issues[code]
		sort.Slice(item.Occurrences, func(i, j int) bool {
			a, b := item.Occurrences[i], item.Occurrences[j]
			return a.TradeDate+"|"+a.SecurityID+"|"+a.CostBucket+"|"+a.Scope < b.TradeDate+"|"+b.SecurityID+"|"+b.CostBucket+"|"+b.Scope
		})
		r.Semantics = append(r.Semantics, *item)
	}
	gate := func(code string, passed bool, detail string) ResearchPublicationGate {
		state := "blocked"
		if passed {
			state = "passed"
		}
		return ResearchPublicationGate{code, state, detail}
	}
	r.CostGates = []ResearchPublicationGate{
		gate("structural_continuity", b.Review.Verified, "Quantity and six-decimal cost identities only; not economic NAV or final settlement."),
		gate("anchor_and_attribution", c.AnchorAndAllocationsMatch, "Current anchor and active allocation payloads compared with the archived research version."),
		gate("source_observation", s.SourceState == "unchanged_since_observation_not_historical_verification" && len(s.Changes) == 0 && s.RecheckFrom == "", "Change watch only, not the original calculation manifest; changes require review from recheck_from."),
		gate("formal_targets_absent", c.Counts.ResearchOnly == b.Review.Rows && c.Counts.Equal+c.Counts.Changed+c.Counts.MetadataOnly+c.Counts.StoredOnly == 0, "Existing/partial targets require a separate revision plan; never overwrite them via research publication."),
		gate("cost_semantics", len(r.Semantics) == 0, "See per-day inventory and individual semantic review occurrences."),
		{Code: "independent_evidence_binding", State: "not_attached", Detail: "Previously completed input replay, query/event, opening and special-cash audits must be bound by identity and digest to a new candidate; this report does not rerun or waive them."},
		{Code: "atomic_publication_and_next_day_reader", State: "not_implemented", Detail: "Require immutable candidate lineage, revision invalidation, transactional conflict checks and compatible next-day readers. Existing preview persistence guards remain."},
	}
	r.NAVGates = []ResearchPublicationGate{
		{Code: "broker_asset_scope", State: "unverified", Detail: "Keep broker total assets as the baseline pending component explanation; do not replace it with a reconstructed sum."},
		{Code: "etf_remaining_settlement_and_final_fees", State: "unverified", Detail: "Cost continuity and explained historical cash records do not prove all outstanding rights released or all fees final."},
	}
	return r, nil
}

func (r ResearchCostPublicationReview) Summary() map[string]any {
	return map[string]any{"account_id": r.AccountID, "version_id": r.VersionID, "days": len(r.Days), "rows": r.Structure.Rows, "semantic_review_groups": len(r.Semantics), "cost_gates": r.CostGates, "economic_nav_gates": r.NAVGates, "cost_publishable": r.CostPublishable, "economic_nav_publishable": r.NAVPublishable, "database_writes": r.DatabaseWrites, "broker_commands": r.BrokerCommands}
}
