package performance

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func repoBridgeFixture() ([]RepoInterestLot, []RepoInterestReceipt) {
	lots := []RepoInterestLot{
		{AccountID: "a", TradeDate: "2026-10-09", GatewayOrderID: "old", SecurityID: "204001.SH", PrincipalCents: 100000, GrossInterestMicros: 1000001, EvidenceRef: "model-old"},
		{AccountID: "a", TradeDate: "2026-10-12", GatewayOrderID: "new", SecurityID: "204001.SH", PrincipalCents: 200000, GrossInterestMicros: 2000000, EvidenceRef: "model-new"},
	}
	receipts := []RepoInterestReceipt{{AccountID: "a", SourceTradeDate: "2026-10-09", GatewayOrderID: "old", PostedDate: "2026-10-12", ReceiptID: "cash", PrincipalCents: 100000, ReceivedAmountCents: 100101, EvidenceRef: "broker-cash"}}
	return lots, receipts
}

func TestRepoInterestBridgeReleasesGrossNotPrincipalOrFees(t *testing.T) {
	lots, receipts := repoBridgeFixture()
	before, _ := json.Marshal([]any{lots, receipts})
	r, err := PreviewRepoInterestBridge("a", "2026-10-09", []string{"2026-10-12", "2026-10-13"}, lots, receipts)
	if err != nil {
		t.Fatal(err)
	}
	d := r.Days[0]
	if d.OpenReceivableMicros != 1000001 || d.CloseReceivableMicros != 2000000 || d.ReceivedInterestMicros != 1010000 || d.ReleasedReceivableMicros != 1000001 || d.SettlementVarianceMicros != 9999 || d.RecognizedInterestMicros != 2009999 {
		t.Fatalf("wrong gross interest bridge: %#v", d)
	}
	if r.Days[1].OpenReceivableMicros != 2000000 || r.Days[1].CloseReceivableMicros != 2000000 || r.Days[1].RecognizedInterestMicros != 0 || len(r.PendingLots) != 1 || r.PendingLots[0].GatewayOrderID != "new" || r.EconomicNAVPublishable {
		t.Fatal("unobserved receipt treated as paid or interest duplicated")
	}
	after, _ := json.Marshal([]any{lots, receipts})
	if string(before) != string(after) {
		t.Fatal("source mutated")
	}
	for _, day := range r.Days {
		if day.ReceivedInterestMicros+day.CloseReceivableMicros-day.OpenReceivableMicros != day.RecognizedInterestMicros {
			t.Fatal("lost conservation")
		}
	}
}

func TestRepoInterestBridgeActualZeroAndBeforeWindow(t *testing.T) {
	lots, receipts := repoBridgeFixture()
	receipts[0].ReceivedAmountCents = receipts[0].PrincipalCents
	r, err := PreviewRepoInterestBridge("a", "2026-10-09", []string{"2026-10-12"}, lots, receipts)
	if err != nil || r.Days[0].SettlementVarianceMicros != -1000001 {
		t.Fatalf("zero actual interest: %v %#v", err, r)
	}
	lots[0].TradeDate, receipts[0].SourceTradeDate, receipts[0].PostedDate = "2026-10-08", "2026-10-08", "2026-10-09"
	r, err = PreviewRepoInterestBridge("a", "2026-10-09", []string{"2026-10-12"}, lots, receipts)
	if err != nil || r.Days[0].OpenReceivableMicros != 0 || r.Days[0].ReceivedInterestMicros != 0 {
		t.Fatal("reused pre-window receipt")
	}
}

func TestRepoInterestBridgeRejectsAmbiguousEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*[]RepoInterestLot, *[]RepoInterestReceipt){
		"duplicate lot":         func(l *[]RepoInterestLot, _ *[]RepoInterestReceipt) { *l = append(*l, (*l)[0]) },
		"wrong account":         func(l *[]RepoInterestLot, _ *[]RepoInterestReceipt) { (*l)[0].AccountID = "other" },
		"unsupported product":   func(l *[]RepoInterestLot, _ *[]RepoInterestReceipt) { (*l)[0].SecurityID = "131810.SZ" },
		"no accrual proof":      func(l *[]RepoInterestLot, _ *[]RepoInterestReceipt) { (*l)[0].EvidenceRef = "" },
		"bad date":              func(l *[]RepoInterestLot, _ *[]RepoInterestReceipt) { (*l)[0].TradeDate = "2026-02-30" },
		"negative interest":     func(l *[]RepoInterestLot, _ *[]RepoInterestReceipt) { (*l)[0].GrossInterestMicros = -1 },
		"overflow":              func(l *[]RepoInterestLot, _ *[]RepoInterestReceipt) { (*l)[0].GrossInterestMicros = math.MaxInt64 },
		"missing accrual":       func(l *[]RepoInterestLot, _ *[]RepoInterestReceipt) { *l = (*l)[1:] },
		"duplicate receipt":     func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { *r = append(*r, (*r)[0]) },
		"wrong receipt account": func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { (*r)[0].AccountID = "other" },
		"partial principal":     func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { (*r)[0].PrincipalCents-- },
		"short payment":         func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { (*r)[0].ReceivedAmountCents = 99999 },
		"excess payment":        func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { (*r)[0].ReceivedAmountCents = math.MaxInt64 },
		"no receipt proof":      func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { (*r)[0].EvidenceRef = "" },
		"future receipt":        func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { (*r)[0].PostedDate = "2026-10-14" },
		"uncovered date":        func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { (*r)[0].PostedDate = "2026-10-11" },
		"before source":         func(_ *[]RepoInterestLot, r *[]RepoInterestReceipt) { (*r)[0].PostedDate = "2026-10-08" },
	} {
		t.Run(name, func(t *testing.T) {
			lots, receipts := repoBridgeFixture()
			mutate(&lots, &receipts)
			if _, err := PreviewRepoInterestBridge("a", "2026-10-09", []string{"2026-10-12", "2026-10-13"}, lots, receipts); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestRepoInterestBridgeDateIdentityAndOrderIndependence(t *testing.T) {
	lots, receipts := repoBridgeFixture()
	lots[1].GatewayOrderID = lots[0].GatewayOrderID
	receipts = append(receipts, RepoInterestReceipt{AccountID: "a", SourceTradeDate: "2026-10-12", GatewayOrderID: "old", PostedDate: "2026-10-13", ReceiptID: "cash2", PrincipalCents: 200000, ReceivedAmountCents: 200200, EvidenceRef: "cash2"})
	dates := []string{"2026-10-12", "2026-10-13"}
	a, err := PreviewRepoInterestBridge("a", "2026-10-09", dates, lots, receipts)
	if err != nil {
		t.Fatal(err)
	}
	lots[0], lots[1] = lots[1], lots[0]
	receipts[0], receipts[1] = receipts[1], receipts[0]
	b, err := PreviewRepoInterestBridge("a", "2026-10-09", dates, lots, receipts)
	if err != nil || !reflect.DeepEqual(a, b) || len(a.PendingLots) != 0 || a.Days[1].CloseReceivableMicros != 0 {
		t.Fatal("date identity or ordering lost")
	}
	receipts[1].ReceiptID = receipts[0].ReceiptID
	if _, err := PreviewRepoInterestBridge("a", "2026-10-09", dates, lots, receipts); err == nil {
		t.Fatal("receipt reused across sources")
	}
}

func TestRepoInterestBridgeScopeValidation(t *testing.T) {
	for _, dates := range [][]string{nil, {"2026-10-09"}, {"2026-10-13", "2026-10-12"}, {"2026-10-12", "2026-10-12"}, {"2026-10-99"}} {
		if _, err := PreviewRepoInterestBridge("a", "2026-10-09", dates, nil, nil); err == nil {
			t.Fatal("invalid dates")
		}
	}
	if _, err := PreviewRepoInterestBridge("a", "bad", []string{"2026-10-12"}, nil, nil); err == nil {
		t.Fatal("invalid opening")
	}
}
