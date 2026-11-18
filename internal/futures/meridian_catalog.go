package futures

import "encoding/json"

// ResolveMeridianPriceCatalog accepts an exact selector response, not a
// caller-invented mapping or a partially read catalog page.
func ResolveMeridianPriceCatalog(raw []byte, securityID string) (CatalogBinding, Price, error) {
	bad := func() (CatalogBinding, Price, error) {
		return CatalogBinding{}, "", priceAdmissionError("invalid_meridian_catalog")
	}
	if len(raw) == 0 || len(raw) > 65536 {
		return bad()
	}
	root, err := parseReply(raw)
	if err != nil {
		return bad()
	}
	var rows []rawObject
	var meta rawObject
	if json.Unmarshal(root["data"], &rows) != nil || len(rows) != 1 || json.Unmarshal(root["meta"], &meta) != nil {
		return bad()
	}
	row := rows[0]
	if textField(meta, "schema_version") != "futures_instrument.v1" || textField(meta, "catalog_schema_version") != "futures_instrument_catalog.v1" || textField(meta, "data_scope") != "current_view" || !literal(meta["count"], "1") || !literal(meta["total_count"], "1") || textField(meta, "next_cursor") != "" {
		return bad()
	}
	if cursor, ok := meta["next_cursor"]; ok && !literal(cursor, "null") && !literal(cursor, `""`) {
		return bad()
	}
	if textField(row, "schema_version") != "futures_instrument.v1" || textField(row, "security_id") != securityID || textField(row, "product_class") != "futures" || !oneOf(textField(row, "trading_status"), "active", "active_observed") {
		return bad()
	}
	b := CatalogBinding{SecurityID: securityID, SourceInstrumentID: textField(row, "source_instrument_id"), ExchangeID: textField(row, "exchange_id"), PublicationSnapshotID: textField(meta, "publication_snapshot_id"), LogicalTableChecksum: textField(meta, "logical_table_checksum")}
	if !oneOf(b.ExchangeID, "SHFE", "CFFEX") || !native(b.SourceInstrumentID, 30, false) || !validID(b.SecurityID) || len(b.SecurityID) > 128 || !validID(b.PublicationSnapshotID) || len(b.PublicationSnapshotID) > 128 || !validID(b.LogicalTableChecksum) || len(b.LogicalTableChecksum) > 128 {
		return bad()
	}
	var coverage map[string]struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(meta["exchange_coverage"], &coverage) != nil || coverage[b.ExchangeID].Status != "production_ready" {
		return bad()
	}
	var multiplier int64
	if json.Unmarshal(row["volume_multiple"], &multiplier) != nil || multiplier <= 0 {
		return bad()
	}
	tick, err := meridianDecimal(row["price_tick"])
	if err != nil {
		return bad()
	}
	return b, tick, nil
}
