# CTP F0 Offline Fixtures

Version: `ctp.f0.draft.2`. Synthetic data only. Not a frozen or deployed protocol.

Read [the draft.2 corrections and Meridian mapping rules](../../docs/CTP_F0_DRAFT2_REVIEW_20261103.md) and the [original field appendix](../../docs/CTP_F0_CONTRACT_DRAFT_20261103.md) before implementing a consumer. The original draft.1 files remain in commit `2e25e62`; do not validate these revised messages with the old hashes. The credential vector is unchanged.

- `messages.json`: ten standalone draft messages, not an executable trading sequence.
- `query_cases.json`: expected snapshot replacement decisions, not an OC scheduler implementation test.
- `credentials.json`: public AES-GCM known-answer vector. The key and fixed nonce must never be deployed.
- `meridian_mapping.json`: selected fields from Meridian's synthetic instrument fixture, not a production directory. Native symbol case is preserved; missing SHFE mapping cannot be synthesized.
- `fault_messages.json`: four cancel-rejection, unknown-outcome and partial-batch replies/events. Shared reply projection tests preserve raw evidence and do not enable futures ingestion.
- `recovery_cases.json`: nine reference expectations over durable facts, not process-crash, persistence or SDK invocation tests. An unknown dispatch outcome never authorizes automatic submit/cancel replay. A proven-unsent case assumes a durable write-ahead marker before every SDK call.
- `query-page.schema.json`: offline candidate schema for four query reply types, with standard JSON Schema 2020-12 structure. Strict integer-token, date, trusted-context and cross-field checks are also required; it is not a full protocol or runtime schema.
- `query_pages.json`: two additional standalone order/fill query pages; existing asset/position/failure examples and the original six JSON files are unchanged. See [the query field appendix](../../docs/CTP_QUERY_PAGE_CONTRACT_20261104.md).
- `../../tests/ctpcontract/`: offline runner; uses existing Relay envelope decoding and credential AAD/key helpers. It does not connect to Redis, PostgreSQL, Meridian or a broker. All files need the root Go module and referenced internal packages; a single copied test file is not a standalone runner.
- `../../internal/redisstream/ctp_fault_contract_test.go`: exercises the existing shared ledger reply handler with an in-memory writer, not a CTP runtime or database.

```bash
go test ./tests/ctpcontract -count=1 -v
go test ./internal/redisstream ./internal/orderflow ./internal/credentials -count=1
```

The numeric account is fictional and has not been provisioned. Do not publish these messages to a Stream. Current Relay production models do not support their futures payloads. Authentication, byte identity and fixture consistency passing do not establish trading readiness, financial scope or crash recovery correctness.

OC reported selected draft.2 constraints, four fault cases and nine recovery expectations passing in `1aed721/f15a0f7`. All seven received file hashes match Relay `8381d97`; the six JSON files remain byte-identical, while this README has later documentation updates. See [the acceptance review](../../docs/OC_CTP_DRAFT2_ACCEPTANCE_REVIEW_20261104.md) for the full source package and verification boundaries. Pending: full schema, review of OC's implemented amount serializer/vectors, dynamic constraints, unmatched fills, duplicate/conflicting terminal events, real crash injection and runtime implementation. Relay coordinates Meridian separately; OC does not need a Meridian integration. Regenerate the credential vector only after reviewing an intentional contract change, using `-update-ctp-vectors`.

The recovery runner now takes an explicit write-ahead-proof flag. The unchanged nine vectors assume it is true; without that proof, intent without dispatch is `outcome_unknown`, not `proven_not_submitted`. Both settings test all 64 fact combinations, rejecting 53 inconsistent states. Missing/null/non-boolean facts are rejected rather than defaulted to false. This remains a reference model, not a durable recovery implementation or permission to replay SDK writes.

The optional local query-schema runner uses `tests/ctpcontract/requirements.txt` in an isolated Python environment. It is not a service or SDK dependency, and OC does not need to run Relay code. Exchange the schema, field appendix, message samples and expected evidence only. No source or binary package is required.

The internal `query_sequence.py` reference model checks bounded out-of-order assembly, exact-body replay, conflicting terminals, cross-page duplicates, deadlines and caller-owned request context. Run both Python suites with `-m unittest discover -s tests/ctpcontract -p 'test_query*.py'` in the isolated environment. Its `ready` result is not a database commit or ACK authorization; persistence, restart and unmatched-fill handling are pending. See [sequence review](../../docs/CTP_QUERY_SEQUENCE_REVIEW_20261104.md). Per user instruction, hold the new query documents internally until Relay consolidates a final OC review document; no interim handoff is requested.

Optional cross-project review: `../../tests/ctpcontract/review_meridian_query.py` runs Meridian's validator against synthetic in-memory mutations using its Python environment. It is not part of the Go runner, never collects or publishes data, and returns nonzero for unsafe acceptance. The 2026-11-04 rectification now passes all nine cases; see [the latest verification](../../docs/MERIDIAN_CTP_V2_RECTIFICATION_ACCEPTANCE_20261104.md). SHFE live readiness remains unverified. The Meridian completion-generation field is not a change to these six shared JSON files or the CTP trading wire.
