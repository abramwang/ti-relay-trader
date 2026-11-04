# CTP F0 Offline Fixtures

Version: `ctp.f0.draft.2`. Synthetic data only. Not a frozen or deployed protocol.

Read [the draft.2 corrections and Meridian mapping rules](../../docs/CTP_F0_DRAFT2_REVIEW_20261103.md) and the [original field appendix](../../docs/CTP_F0_CONTRACT_DRAFT_20261103.md) before implementing a consumer. The original draft.1 files remain in commit `2e25e62`; do not validate these revised messages with the old hashes. The credential vector is unchanged.

- `messages.json`: ten standalone draft messages, not an executable trading sequence.
- `query_cases.json`: expected snapshot replacement decisions, not an OC scheduler implementation test.
- `credentials.json`: public AES-GCM known-answer vector. The key and fixed nonce must never be deployed.
- `meridian_mapping.json`: selected fields from Meridian's synthetic instrument fixture, not a production directory. Native symbol case is preserved; missing SHFE mapping cannot be synthesized.
- `fault_messages.json`: four cancel-rejection, unknown-outcome and partial-batch replies/events. Shared reply projection tests preserve raw evidence and do not enable futures ingestion.
- `recovery_cases.json`: nine reference expectations over durable facts, not process-crash, persistence or SDK invocation tests. An unknown dispatch outcome never authorizes automatic submit/cancel replay. A proven-unsent case assumes a durable write-ahead marker before every SDK call.
- `../../tests/ctpcontract/`: offline runner; uses existing Relay envelope decoding and credential AAD/key helpers. It does not connect to Redis, PostgreSQL, Meridian or a broker. All files need the root Go module and referenced internal packages; a single copied test file is not a standalone runner.
- `../../internal/redisstream/ctp_fault_contract_test.go`: exercises the existing shared ledger reply handler with an in-memory writer, not a CTP runtime or database.

```bash
go test ./tests/ctpcontract -count=1 -v
go test ./internal/redisstream ./internal/orderflow ./internal/credentials -count=1
```

The numeric account is fictional and has not been provisioned. Do not publish these messages to a Stream. Current Relay production models do not support their futures payloads. Authentication, byte identity and fixture consistency passing do not establish trading readiness, financial scope or crash recovery correctness.

OC reported independent credential-vector verification and SDK byte bounds for draft.1; draft.2 and the new fault cases still require its confirmation. Pending: full schema, amount serializer, dynamic constraints, unmatched fills, duplicate/conflicting terminal events, real crash injection and runtime implementation. See [the OC follow-up](../../docs/OC_CTP_NEXT_ACTIONS_20261104.md) for evidence boundaries and recovery assumptions. Relay coordinates Meridian separately; OC does not need a Meridian integration. Regenerate the credential vector only after reviewing an intentional contract change, using `-update-ctp-vectors`.
