SELECT advance_account_baseline_revision('','ledger_stream_receipts','UNINSTALL');
DROP TABLE ledger_stream_read_entries;
DROP TABLE ledger_stream_read_batches;
DROP TABLE ledger_stream_receivers;
DROP FUNCTION observe_account_baseline_receiver();
