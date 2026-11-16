package futures

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// Relay-side transport rehearsal, not an OC runtime or broker acceptance test.
// Synthetic derivatives use random development routes; originals stay unchanged.
func TestOCReadOnlyRedisHeartbeatCaptureAndRestart(t *testing.T) {
	_, store, old, client, reader, ctx := realStreamRole(t, "hb")
	var bodies [][]byte
	for _, scenario := range readOnlyScenarios(t) {
		for _, raw := range scenario.Heartbeats {
			body := editBody(t, raw, func(m map[string]any) { m["routing"] = old.config.Scope.Route })
			bodies = append(bodies, body)
			appendRealStream(t, ctx, client, old, fmt.Sprintf("%d-0", len(bodies)+1), "body", body)
		}
	}
	first, err := store.ReadOnce(ctx, old, reader, 11)
	if err != nil || len(first.ReceiptIDs) != 11 {
		t.Fatal(first, err)
	}
	h, err := store.Open(ctx, old.config)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		page, err := store.ReadOnce(ctx, h, reader, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.ReceiptIDs) == 0 {
			break
		}
	}
	page, err := store.HeartbeatAudit(ctx, h, 0, 100)
	if err != nil || len(page) != len(bodies)+1 {
		t.Fatal("missing Redis/PG evidence", len(page), err)
	}
	if page[0].DecodeError == "" {
		t.Fatal("synthetic seed incorrectly decoded")
	}
	for i, body := range bodies {
		record := page[i+1]
		if !bytes.Equal(record.Receipt.Raw, body) || record.Decoded == nil || record.Decoded.ReadOnly == nil || record.DecodeError != "" || record.Decoded.AcceptingTradeCommands || record.Decoded.AcceptingCancelCommands {
			t.Fatal("read-only evidence changed or promoted", i)
		}
	}
	if _, err := store.ReadOnce(ctx, old, reader, 1); !errors.Is(err, ErrStreamOwner) {
		t.Fatal("old owner not fenced", err)
	}
	if groups, err := client.XInfoGroups(ctx, h.stream).Result(); err != nil || len(groups) != 0 {
		t.Fatal("receiver created OC groups", err)
	}
	assertRealCursor(t, ctx, store, h, "26-0", 26)
}
