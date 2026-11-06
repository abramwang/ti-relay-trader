-- Fail rather than discard evidence when a journal exceeds the former limit.
ALTER TABLE futures_query_trade_applications DROP CONSTRAINT futures_query_trade_applications_row_count_check;
ALTER TABLE futures_query_trade_applications ADD CHECK(row_count BETWEEN 0 AND 1024);
ALTER TABLE futures_trade_scopes DROP CONSTRAINT futures_trade_scopes_audit_count_check;
ALTER TABLE futures_trade_scopes DROP CONSTRAINT futures_trade_scopes_audit_bytes_check;
ALTER TABLE futures_trade_scopes ADD CHECK(audit_count BETWEEN 0 AND 1024);
ALTER TABLE futures_trade_scopes ADD CHECK(audit_bytes BETWEEN 0 AND 33554432);
ALTER TABLE futures_event_scopes DROP CONSTRAINT futures_event_scopes_event_count_check;
ALTER TABLE futures_event_scopes DROP CONSTRAINT futures_event_scopes_event_bytes_check;
ALTER TABLE futures_event_scopes ADD CHECK(event_count BETWEEN 0 AND 1024);
ALTER TABLE futures_event_scopes ADD CHECK(event_bytes BETWEEN 0 AND 33554432);
