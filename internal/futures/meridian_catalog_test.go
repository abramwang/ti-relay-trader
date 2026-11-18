package futures

import (
	"os"
	"testing"
)

func TestMeridianPriceCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/fixtures/meridian_futures_catalog.synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	b, tick, err := ResolveMeridianPriceCatalog(raw, "RB2701.SHFE")
	if err != nil || b.SourceInstrumentID != "rb2701" || tick != "1" || b.PublicationSnapshotID != "synthetic-catalog-version" {
		t.Fatal(b, tick, err)
	}
	for name, change := range map[string]func(map[string]any, map[string]any, map[string]any){
		"wrong_symbol": func(root, row, meta map[string]any) { row["security_id"] = "AU2612.SHFE" },
		"options":      func(root, row, meta map[string]any) { row["product_class"] = "options" },
		"expired":      func(root, row, meta map[string]any) { row["trading_status"] = "expired_observed" },
		"ambiguous":    func(root, row, meta map[string]any) { root["data"] = []any{row, row}; meta["count"] = 2 },
		"partial":      func(root, row, meta map[string]any) { meta["total_count"] = 2 },
		"cursor":       func(root, row, meta map[string]any) { meta["next_cursor"] = "next" },
		"bad_cursor":   func(root, row, meta map[string]any) { meta["next_cursor"] = true },
		"shadow": func(root, row, meta map[string]any) {
			meta["exchange_coverage"] = map[string]any{"SHFE": map[string]any{"status": "ready_for_promotion"}}
		},
		"no_coverage":    func(root, row, meta map[string]any) { delete(meta, "exchange_coverage") },
		"no_version":     func(root, row, meta map[string]any) { delete(meta, "publication_snapshot_id") },
		"missing_tick":   func(root, row, meta map[string]any) { row["price_tick"] = nil },
		"bad_multiplier": func(root, row, meta map[string]any) { row["volume_multiple"] = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := editBody(t, raw, func(root map[string]any) {
				change(root, root["data"].([]any)[0].(map[string]any), root["meta"].(map[string]any))
			})
			if _, _, err := ResolveMeridianPriceCatalog(changed, "RB2701.SHFE"); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}
