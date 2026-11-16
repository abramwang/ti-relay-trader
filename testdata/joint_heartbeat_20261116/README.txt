OC / Relay live TEST heartbeat collection, 2026-11-16 Asia/Shanghai

Status: OC ran and exited normally (exit 0). Relay PG/cursor verification pending.
Implementation: c8f4830185ac3b3f6f7b1ca8964c56afe127de72
Mode: test_transport_recovery_only. No broker API object or broker login.
This contains actual process heartbeats over shared TEST Redis, not replayed
offline fixtures. Account/native identities are intentionally fictional.

Routing: test / ctp_joint_test / ctp-transport-20261116 / 990000000116
Prefix: relay:test:v1:ctp_joint_test:ctp-transport-20261116
Heartbeat stream: relay:test:v1:ctp_joint_test:ctp-transport-20261116:hb
SourceID: shared-test-redis
Redis run_id: 94f801dc68e3699cc681feb6dbb48f19e46aef9e
Epoch: joint-98ae9473c67f709b2e735aeb31a55da6
Epoch key: relay:test:v1:ctp_joint_test:ctp-transport-20261116:cmd.trade:transport-epoch
Command group: oc-ctp-transport-20261116
Command consumer: oc-ctp-transport-20261116-owner1
Counter session: ctpproc-cd57a6a1810d671fbffaa838936f100e
Connection generation: null
TradingDay: null

T0 (first produced_at): 2026-11-16T21:28:56.037+08:00
T0 + 15 seconds: 2026-11-16T21:29:11.037+08:00
15 heartbeat records fall within that inclusive produced_at window:
  first: 1794835736035-0
  last:  1794835750255-0
Two additional heartbeats are retained, including the final stopping heartbeat.
Total: 17; full range 1794835736035-0 through 1794835752035-0.
Final produced_at: 2026-11-16T21:29:12.036+08:00.
The runtime was configured for 16 seconds to cover the full 15 seconds after its
first heartbeat. No trimming/deletion/expiry was applied. Do not discard the
additional records or mistake the stopped process for a live-ready session.

Both command groups ended pending=0 and lag=0. No new commands were submitted.
Only the previously empty fictional namespace was initialized; no real Dongwu
account route, Huaxin route, production Redis, output consumer group or PG was
modified. The existing recovery code scanned the empty command PELs only.
Redis run_id and epoch remained unchanged at completion.

heartbeat_evidence.json records original body strings, physical keys/entry IDs,
UTF-8 byte lengths and body SHA256. All 17 body strings were compared exactly
against the local runtime audit. process.stdout.txt and process.stderr.txt are
unmodified process outputs; the latter is empty. Credentials/config are excluded.
runtime.audit.jsonl is the original local process audit.

Relay may read the retained hb stream via XREAD and verify each original entry
against this evidence, then check its own durable owner/cursor and PG results.
OC has not verified Relay capture, persistence, resume or real-time latency.
No four-query, authentication, settlement, trade or F0 acceptance is claimed.
