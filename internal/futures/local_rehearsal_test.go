package futures

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
	"ti-relay-trader/internal/config"
)

// Run with scripts/test-futures-postgres.sh. No application configuration or
// shared Redis endpoint is used; fixtures never leave the private Unix socket.
func TestLocalCTPJointRehearsal(t *testing.T) {
	if os.Getenv("RELAY_FUTURES_LOCAL_REDIS_SERVER") == "" {
		t.Skip("requires an explicit local Redis binary and disposable PostgreSQL")
	}
	pg, err := pgx.ParseConfig(os.Getenv("RELAY_FUTURES_TEST_DATABASE_URL"))
	if err != nil || !strings.HasPrefix(filepath.Clean(pg.Host), "/tmp/relay-futures-pg.") || filepath.Base(pg.Host) != "socket" ||
		filepath.Dir(filepath.Dir(filepath.Clean(pg.Host))) != "/tmp" || pg.Database != "postgres" || pg.User != "relay_ctp_test" {
		t.Fatal("run through scripts/test-futures-postgres.sh")
	}
	r := newLocalStorageRedis(t)
	cfg := config.Default()
	cfg.Service.Environment = config.EnvironmentTest
	cfg.Redis.Env, cfg.Redis.URL = "test", "unix://"+r.socket
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "isolated-test.yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELAY_FUTURES_TEST_REDIS_CONFIG", path)
	t.Setenv("RELAY_FUTURES_TEST_REDIS_WRITES", "development-keys-only")

	// Keep the actual production-path assertions in their existing focused tests.
	// This suite supplies isolated infrastructure and a repeatable execution order.
	for _, test := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"query_publication_gate", TestQueryPublisherRedisAtomicGate},
		{"query_no_resend", TestQueryPublicationDurabilityAndNoResend},
		{"query_receive", TestReviewedQueryRedisReceive},
		{"query_binding_restart", TestReviewedQueryAtomicBindingAndRestart},
		{"query_pages_restart", TestStreamRedisQueryPagesHandoffAndRecovery},
		{"registered_dispatch_only", TestStreamRedisDispatchRegisteredQueryOnly},
		{"raw_bytes_restart", TestStreamRedisRawBytesPagingAndRestart},
		{"unread_history_loss", TestStreamRedisRejectsUnreadHistoryLoss},
		{"pg_failure_reread", TestStreamRedisPGFailureRereadsWithoutDuplicate},
		{"redis_reconnect", TestStreamRedisClientDisconnectAndReconnect},
		{"assessment_gate_budget", TestAssessmentRedisGateAndExactOnce},
		{"assessment_unknown_no_resend", TestAssessmentDurabilityBudgetAndUnknown},
		{"cancel_own_order_only", TestAssessmentCancelRequiresCapturedOwnOrder},
		{"fill_first_restart", TestEventStoreDurableFillFirstAndReplay},
		{"fill_v2_query_realtime", TestFillV2StoreQueryRealtimeAndRestart},
		{"fill_mixed_version_reject", TestFillV2StoreMixedVersionsSurviveRestart},
		{"event_conflict_audit", TestEventStoreConflictingSourceOrMessage},
		{"event_rollback", TestEventStoreProcessingRollbackRetainsInbox},
		{"query_application_restart", TestTradeQueryApplicationRecoveryAndAssociation},
		{"partial_empty_preserve", TestTradeQueryPartialDuplicateAndEmptyNeverClear},
		{"heartbeat_resume", TestHeartbeatCaptureRealRedisBoundedRestartAndRawEvidence},
		{"heartbeat_cli", TestHeartbeatCaptureCLIRealRedisAndDisposableDatabase},
		{"reply_cli", TestReplyReceiveCLIRealRedis},
	} {
		if !t.Run(test.name, test.run) {
			t.Fatal("offline rehearsal failed; not a broker acceptance result")
		}
	}
}
