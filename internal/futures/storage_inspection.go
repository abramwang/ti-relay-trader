package futures

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Inspection is advisory. It neither acquires a receiver nor changes execution
// readiness, and never interprets retained history as unfinished trading risk.
type StorageMetric struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Value      *int64 `json:"value"`
	Limit      *int64 `json:"limit,omitempty"`
	LowerBound bool   `json:"lower_bound,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type StorageInspection struct {
	Mode                string          `json:"mode"`
	Route               QueryRoute      `json:"route"`
	ConfiguredTradeDate string          `json:"configured_trade_date"`
	StartedAt           time.Time       `json:"started_at"`
	CompletedAt         time.Time       `json:"completed_at"`
	AtomicSnapshot      bool            `json:"atomic_snapshot"`
	BusinessAuthorized  bool            `json:"business_authorized"`
	Metrics             []StorageMetric `json:"metrics"`
}

func storageValue(name string, value int64) StorageMetric {
	if value < 0 {
		return storageUnknown(name, "invalid_counter")
	}
	return StorageMetric{Name: name, State: "ok", Value: &value}
}

func storageUnknown(name, reason string) StorageMetric {
	return StorageMetric{Name: name, State: "unknown", Detail: reason}
}

func storageCapacity(name string, used, limit int64) StorageMetric {
	m := storageValue(name, used)
	if used < 0 || limit <= 0 {
		m.State, m.Detail = "unknown", "capacity_limit_unavailable"
		return m
	}
	m.Limit = &limit
	// ceil(80%/90%) without multiplying large counters or rounding to float.
	if used >= limit-limit/10 {
		m.State = "critical"
	} else if used >= limit-limit/5 {
		m.State = "warning"
	}
	return m
}

func ValidateStoragePaths(paths []string) error {
	if len(paths) > 8 {
		return ErrStreamIdentity
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || len(p) > 4096 || strings.ContainsAny(p, "\x00\r\n") || seen[p] {
			return ErrStreamIdentity
		}
		seen[p] = true
	}
	return nil
}

// Only explicit local mounts are sampled. Nothing infers where a remote
// database/Redis volume lives from the worker's own filesystem.
func InspectTestStorage(ctx context.Context, db *sql.DB, source *TestStreamSource, c CaptureServiceConfig, paths []string) (StorageInspection, error) {
	if c.Validate() != nil || ValidateStoragePaths(paths) != nil || db == nil || source == nil ||
		source.scope != c.Scope || source.sourceID != c.Target.SourceID {
		return StorageInspection{}, ErrStreamIdentity
	}
	r := StorageInspection{Mode: "read_only_storage_inspection", Route: c.Scope.Route,
		ConfiguredTradeDate: c.Target.TradeDate, StartedAt: time.Now().UTC()}
	redisCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	info, err := source.reader.client.Info(redisCtx, "server", "memory", "persistence", "stats").Result()
	cancel()
	if err != nil {
		r.Metrics = append(r.Metrics, storageUnknown("redis", "info_unavailable"))
	} else {
		r.Metrics = append(r.Metrics, redisStorageMetrics(info, c.Target.ServerRunID)...)
	}
	pgCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	metrics, err := inspectPostgresStorage(pgCtx, db, c)
	cancel()
	if err != nil {
		reason := "inspection_unavailable_or_schema_incomplete"
		if errors.Is(err, ErrStreamIdentity) {
			reason = "scope_or_database_mismatch"
		}
		r.Metrics = append(r.Metrics, storageUnknown("postgres", reason))
	} else {
		r.Metrics = append(r.Metrics, metrics...)
	}
	r.Metrics = append(r.Metrics,
		storageUnknown("postgres.volume_available_bytes", "remote_volume_not_inferred"),
		storageUnknown("redis.volume_available_bytes", "remote_volume_not_inferred"))
	for _, path := range paths {
		if ctx.Err() != nil {
			r.Metrics = append(r.Metrics, storageUnknown("local_volume:"+path, "inspection_cancelled"))
			continue
		}
		var stat syscall.Statfs_t
		if syscall.Statfs(path, &stat) != nil {
			r.Metrics = append(r.Metrics, storageUnknown("local_volume:"+path, "statfs_unavailable"))
			continue
		}
		r.Metrics = append(r.Metrics, localStorageMetrics(path, stat)...)
	}
	r.CompletedAt = time.Now().UTC()
	return r, nil
}

func redisStorageMetrics(info, expectedRun string) []StorageMetric {
	fields := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), ":")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		if _, duplicate := fields[key]; duplicate {
			return []StorageMetric{storageUnknown("redis", "duplicate_info_field")}
		}
		fields[key] = value
	}
	if expectedRun == "" || fields["run_id"] != expectedRun {
		return []StorageMetric{storageUnknown("redis", "server_run_id_mismatch")}
	}
	number := func(key string) int64 {
		v, err := strconv.ParseInt(fields[key], 10, 64)
		if err != nil || v < 0 {
			return -1
		}
		return v
	}
	used, limit := number("used_memory"), number("maxmemory")
	memory := storageCapacity("redis.used_memory_bytes", used, limit)
	memory.Detail = "server_wide_advisory_not_exact_eviction_accounting"
	if limit == 0 {
		memory.Detail = "maxmemory_unlimited_physical_headroom_unknown"
	}
	policy := StorageMetric{Name: "redis.eviction_policy", State: "unknown", Detail: "policy_unavailable"}
	if fields["maxmemory_policy"] == "noeviction" {
		policy.State, policy.Detail = "ok", "noeviction"
	} else if oneOf(fields["maxmemory_policy"], "allkeys-lru", "allkeys-lfu", "allkeys-random", "volatile-lru", "volatile-lfu", "volatile-random", "volatile-ttl") {
		policy.State, policy.Detail = "critical", "eviction_enabled"
	}
	aof := StorageMetric{Name: "redis.aof", State: "unknown", Detail: "persistence_info_unavailable"}
	if fields["aof_enabled"] == "0" {
		aof.State, aof.Detail = "warning", "aof_disabled"
	} else if fields["aof_enabled"] == "1" {
		if fields["aof_last_write_status"] == "ok" {
			aof.State, aof.Detail = "ok", "last_write_ok_not_durability_certification"
		} else if fields["aof_last_write_status"] == "err" {
			aof.State, aof.Detail = "critical", "last_write_error"
		}
	}
	evicted := storageValue("redis.evicted_keys_total", number("evicted_keys"))
	evicted.Detail = "cumulative_since_statistics_reset_not_current_eviction_rate"
	if evicted.Value != nil && *evicted.Value > 0 {
		evicted.State = "warning"
	}
	return []StorageMetric{memory, policy, aof, evicted,
		storageValue("redis.used_memory_rss_bytes", number("used_memory_rss")),
		storageValue("redis.expired_keys_total", number("expired_keys"))}
}

func localStorageMetrics(path string, s syscall.Statfs_t) []StorageMetric {
	prefix := "local_volume:" + path
	if s.Bsize <= 0 || s.Blocks == 0 || s.Bavail > s.Blocks || s.Blocks > uint64(math.MaxInt64/s.Bsize) {
		return []StorageMetric{storageUnknown(prefix, "invalid_filesystem_statistics")}
	}
	total, available := int64(s.Blocks)*s.Bsize, int64(s.Bavail)*s.Bsize
	m := storageCapacity(prefix+".available_bytes", total-available, total)
	m.Value = &available
	m.Detail = "explicit_local_mount_available_to_unprivileged_writer_warning_at_20pct_critical_at_10pct"
	inodes := storageUnknown(prefix+".available_inodes", "inode_statistics_unavailable")
	if s.Files > 0 && s.Files <= math.MaxInt64 && s.Ffree <= s.Files {
		free := int64(s.Ffree)
		inodes = storageCapacity(prefix+".available_inodes", int64(s.Files)-free, int64(s.Files))
		inodes.Value = &free
		inodes.Detail = "low_available_count_not_used_count"
	}
	return []StorageMetric{m, inodes}
}

func inspectPostgresStorage(ctx context.Context, db *sql.DB, c CaptureServiceConfig) ([]StorageMetric, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout='1500ms'`); err != nil {
		return nil, err
	}
	var database string
	var size int64
	if err := tx.QueryRowContext(ctx, `SELECT current_database(),pg_database_size(current_database())`).Scan(&database, &size); err != nil {
		return nil, err
	}
	if database != c.Database {
		return nil, ErrStreamIdentity
	}
	metrics := []StorageMetric{storageValue("postgres.database_bytes", size)}
	for _, table := range []string{"futures_commands", "futures_command_work", "futures_stream_inbox", "futures_trade_observations", "futures_order_states", "futures_execution_facts", "futures_trade_aliases"} {
		if err := tx.QueryRowContext(ctx, `SELECT pg_total_relation_size(to_regclass($1))`, table).Scan(&size); err != nil {
			return nil, err
		}
		metrics = append(metrics, storageValue("postgres."+table+".total_bytes", size))
	}
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(work_bytes),0) FROM (
		SELECT work_bytes FROM futures_command_work WHERE route_key=$1 AND pending ORDER BY command_id LIMIT 4097) w`,
		string(commandRouteBytes(c.Scope.Route))).Scan(&count, &size); err != nil {
		return nil, err
	}
	for _, m := range []StorageMetric{storageCapacity("commands.pending_count", count, maxPendingCommands), storageCapacity("commands.pending_bytes", size, maxPendingCommandBytes)} {
		m.LowerBound = count > maxPendingCommands
		metrics = append(metrics, m)
	}
	for _, role := range []string{"reply", "event"} {
		config := StreamConfig{Scope: c.Scope, Role: role, SourceID: c.Target.SourceID}
		key, _ := config.stream()
		raw, _ := json.Marshal(config)
		var same bool
		var usage StreamStatus
		err := tx.QueryRowContext(ctx, `SELECT config_json=$2::jsonb,audit_count,audit_bytes,pending_count,pending_bytes,
			quarantined_count,quarantined_bytes,delivered_count,delivered_bytes FROM futures_stream_receivers WHERE stream_key=$1`, key, raw).
			Scan(&same, &usage.Records, &usage.Bytes, &usage.Pending, &usage.PendingBytes, &usage.Quarantined, &usage.QuarantinedBytes, &usage.Delivered, &usage.DeliveredBytes)
		if errors.Is(err, sql.ErrNoRows) {
			metrics = append(metrics, StorageMetric{Name: role + ".backlog", State: "not_observed", Detail: "receiver_absent_not_healthy_empty"})
			continue
		}
		if err != nil {
			return nil, err
		}
		if !same {
			return nil, ErrStreamIdentity
		}
		if err := usage.validateUsage(); err != nil {
			return nil, err
		}
		metrics = append(metrics, storageCapacity(role+".backlog_count", int64(usage.Pending)+int64(usage.Quarantined), maxStreamBacklogCount),
			storageCapacity(role+".backlog_bytes", usage.PendingBytes+usage.QuarantinedBytes, maxStreamBacklogBytes),
			storageValue(role+".retained_count", usage.Records), storageValue(role+".retained_bytes", usage.Bytes))
	}
	scope := TradeScope{Route: c.Scope.Route, NativeBrokerID: c.Scope.NativeBrokerID, InvestorID: c.Scope.InvestorID, TradeDate: c.Target.TradeDate}
	raw, _ := json.Marshal(scope)
	var same bool
	err = tx.QueryRowContext(ctx, `SELECT context_json=$2::jsonb,audit_count,audit_bytes FROM futures_trade_scopes WHERE scope_key=$1`, tradeScopeKey(scope), raw).Scan(&same, &count, &size)
	if errors.Is(err, sql.ErrNoRows) {
		metrics = append(metrics, StorageMetric{Name: "day_ledger", State: "not_observed", Detail: "configured_trade_date_absent_not_empty_complete"})
	} else if err != nil {
		return nil, err
	} else if !same {
		return nil, ErrStreamIdentity
	} else {
		metrics = append(metrics, storageCapacity("day_ledger.audit_count", count, maxTradeAuditCount), storageCapacity("day_ledger.audit_bytes", size, maxTradeAuditBytes))
	}
	return metrics, tx.Commit()
}
