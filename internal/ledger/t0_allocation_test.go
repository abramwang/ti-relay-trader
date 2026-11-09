package ledger

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestFuying13ReviewedAllocationFixture(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/fixtures/fuying13_t0_allocation_review_20261109.json")
	if err != nil {
		t.Fatal(err)
	}
	var items []T0Allocation
	if err = json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	current, err := ResolveT0Allocations(items)
	if err != nil || len(current) != 4 {
		t.Fatalf("%+v %v", current, err)
	}
	for _, item := range current {
		if item.AccountID != "314000045768" || item.SecurityID != "510310.SH" || item.Revision != 1 {
			t.Fatalf("unexpected scope %+v", item)
		}
	}
}

func allocationFixture() T0Allocation {
	return T0Allocation{LinkID: "audit-1", AccountID: "audit-account", TradeDate: "2026-10-12", GatewayOrderID: "buy", SecurityID: "510310.SH", TradeSide: "B", Quantity: 5900, GroupID: "residual", Role: "residual", Revision: 1, Status: "active", EvidenceType: "user_confirmation", EvidenceRef: "confirmation-1", ConfirmedBy: "operator", ConfirmedAt: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)}
}

func TestT0AllocationRevisionHistory(t *testing.T) {
	first := allocationFixture()
	second := first
	second.LinkID = "audit-2"
	second.Revision = 2
	second.Supersedes = first.LinkID
	second.GroupID = "corrected"
	items, err := ResolveT0Allocations([]T0Allocation{second, first})
	if err != nil || len(items) != 1 || items[0].GroupID != "corrected" {
		t.Fatalf("%+v %v", items, err)
	}
	third := second
	third.LinkID = "audit-3"
	third.Revision = 3
	third.Supersedes = second.LinkID
	third.Status = "revoked"
	items, err = ResolveT0Allocations([]T0Allocation{first, second, third})
	if err != nil || len(items) != 0 {
		t.Fatalf("%+v %v", items, err)
	}
	for _, history := range [][]T0Allocation{{second}, {first, first}, {first, second, second}, {first, third}} {
		if _, err := ResolveT0Allocations(history); err == nil {
			t.Fatal("accepted invalid revision chain")
		}
	}
	wrongScope := second
	wrongScope.AccountID = "other"
	if _, err := ResolveT0Allocations([]T0Allocation{first, wrongScope}); err == nil {
		t.Fatal("cross-account supersession")
	}
}

func TestT0AllocationRejectsMissingEvidence(t *testing.T) {
	for _, change := range []func(*T0Allocation){
		func(x *T0Allocation) { x.EvidenceRef = "" }, func(x *T0Allocation) { x.ConfirmedAt = time.Time{} },
		func(x *T0Allocation) { x.Quantity = 0 }, func(x *T0Allocation) { x.TradeSide = "R" },
		func(x *T0Allocation) { x.SecurityID = "510310.SSE" }, func(x *T0Allocation) { x.EvidenceType = "balance_inference" },
	} {
		item := allocationFixture()
		change(&item)
		if _, err := NormalizeT0Allocation(item); err == nil {
			t.Fatalf("accepted %+v", item)
		}
	}
}
