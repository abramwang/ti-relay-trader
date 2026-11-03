# CTP F0 Offline Fixtures

Version: `ctp.f0.draft.2`. Synthetic data only. Not a frozen or deployed protocol.

Read [the draft.2 corrections and Meridian mapping rules](../../docs/CTP_F0_DRAFT2_REVIEW_20261103.md) and the [original field appendix](../../docs/CTP_F0_CONTRACT_DRAFT_20261103.md) before implementing a consumer. The original draft.1 files remain in commit `2e25e62`; do not validate these revised messages with the old hashes. The credential vector is unchanged.

- `messages.json`: ten standalone draft messages, not an executable trading sequence.
- `query_cases.json`: expected snapshot replacement decisions, not an OC scheduler implementation test.
- `credentials.json`: public AES-GCM known-answer vector. The key and fixed nonce must never be deployed.
- `meridian_mapping.json`: selected fields from Meridian's synthetic instrument fixture, not a production directory. Native symbol case is preserved; missing SHFE mapping cannot be synthesized.
- `../../tests/ctpcontract/contract_test.go` and `draft2_test.go`: offline runner; uses existing Relay envelope decoding and credential AAD/key helpers. It does not connect to Redis, PostgreSQL, Meridian or a broker. Both files need the root Go module and referenced internal packages; a single copied test file is not a standalone runner.

```bash
go test ./tests/ctpcontract -count=1 -v
```

The numeric account is fictional and has not been provisioned. Do not publish these messages to a Stream. Current Relay production models do not support their futures payloads. Authentication, byte identity and fixture consistency passing do not establish trading readiness, financial scope or crash recovery correctness.

Pending: OC cross-language verification, complete SDK field bounds and schema, dynamic constraints, cancel-rejection/batch-failure/crash-recovery fixtures and actual runtime implementation. Regenerate the credential vector only after reviewing an intentional contract change, using `-update-ctp-vectors`.
