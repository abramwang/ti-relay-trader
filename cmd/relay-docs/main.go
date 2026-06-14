package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"ti-relay-trader/internal/api"
	relayconfig "ti-relay-trader/internal/config"
	"ti-relay-trader/internal/events"
	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/ledger"
	"ti-relay-trader/internal/logging"
	"ti-relay-trader/internal/orderflow"
	"ti-relay-trader/internal/redisstream"
	"ti-relay-trader/internal/timeutil"
	"ti-relay-trader/internal/worker"
)

type docPage struct {
	Slug        string
	Title       string
	Path        string
	Description string
}

type pageData struct {
	Title      string
	Active     string
	Summary    string
	Generated  string
	Head       template.HTML
	Content    template.HTML
	Scripts    template.HTML
	Docs       []docPage
	Doc        *docPage
	ProjectDir string
}

//go:embed web/templates/*.html web/static/*
var portalAssets embed.FS

var apiConsoleTemplate = template.Must(template.ParseFS(portalAssets, "web/templates/api_console.html"))
var tradeTerminalTemplate = template.Must(template.ParseFS(portalAssets, "web/templates/trade_terminal.html"))
var jobStatusTemplate = template.Must(template.ParseFS(portalAssets, "web/templates/job_status.html"))

var (
	addr    = flag.String("addr", "0.0.0.0:9092", "HTTP listen address")
	rootDir = flag.String("root", ".", "project root directory")
	cfgPath = flag.String("config", os.Getenv(relayconfig.EnvPath), "optional relay config file path")

	publicURL = "http://relay-trader.quantstage.com"

	docs = []docPage{
		{
			Slug:        "readme",
			Title:       "README",
			Path:        "README.md",
			Description: "项目恢复卡片、职责范围、端口约定、待办事项和工作日志。",
		},
		{
			Slug:        "architecture",
			Title:       "架构草案",
			Path:        "docs/ARCHITECTURE.md",
			Description: "Go + Python 分工、服务边界、多账户模型、持久化和实施顺序。",
		},
		{
			Slug:        "roadmap",
			Title:       "开发路线图",
			Path:        "docs/ROADMAP.md",
			Description: "整体开发步骤、阶段状态、当前优先级和里程碑任务。",
		},
		{
			Slug:        "data-model",
			Title:       "数据模型与落盘",
			Path:        "docs/DATA_MODEL.md",
			Description: "PostgreSQL 落盘口径、C++ 结构体参考、标准字段映射和账表建议。",
		},
		{
			Slug:        "migrations",
			Title:       "PostgreSQL Migration",
			Path:        "docs/MIGRATIONS.md",
			Description: "首批交易账本 migration、表清单、关键约束和执行方式。",
		},
		{
			Slug:        "trading-api-schema",
			Title:       "交易接口 Schema",
			Path:        "docs/TRADING_API_SCHEMA.md",
			Description: "统一 A 股交易接口对象、枚举、校验、状态机和 Redis 映射。",
		},
		{
			Slug:        "api-test-console",
			Title:       "接口测试台",
			Path:        "docs/API_TEST_CONSOLE.md",
			Description: "Apifox 风格 API 联调页面、当前能力、安全边界和后续计划。",
		},
		{
			Slug:        "trading-terminal",
			Title:       "交易终端",
			Path:        "docs/TRADING_TERMINAL.md",
			Description: "成熟交易软件风格手动测试台、页面结构、接口接入和实时刷新计划。",
		},
		{
			Slug:        "python-sdk",
			Title:       "Python SDK",
			Path:        "docs/PYTHON_SDK.md",
			Description: "面向策略开发的 9092 API Python 客户端封装设计。",
		},
		{
			Slug:        "operations",
			Title:       "运行配置与任务管理",
			Path:        "docs/OPERATIONS.md",
			Description: "本地配置文件、凭据管理、cron 后台任务和部署运行约定。",
		},
		{
			Slug:        "trading-day-workflow",
			Title:       "交易日流程",
			Path:        "docs/TRADING_DAY_WORKFLOW.md",
			Description: "东八区时间口径、盘前初始化和收盘后结算流程。",
		},
		{
			Slug:        "redis-stream-probe",
			Title:       "Redis Stream 探测",
			Path:        "docs/REDIS_STREAM_PROBE.md",
			Description: "前置测试环境 Redis Stream 只读探测命令、输出和联调顺序。",
		},
		{
			Slug:        "redis-ledger-sync",
			Title:       "Redis 账本同步",
			Path:        "docs/REDIS_LEDGER_SYNC.md",
			Description: "Redis reply/event 到 PostgreSQL 账本的批处理同步、幂等策略和字段缺口。",
		},
		{
			Slug:        "third-party-integration",
			Title:       "前置服务对接手册",
			Path:        "docs/THIRD_PARTY_INTEGRATION_GUIDE.md",
			Description: "Redis Stream 协议、命令、回包、事件、心跳、DLQ 和验收流程。",
		},
		{
			Slug:        "tests",
			Title:       "测试目录索引",
			Path:        "tests/README.md",
			Description: "测试目录约定、当前状态和后续补充计划。",
		},
	}

	headingPattern = regexp.MustCompile(`^(#{1,4})\s+(.+)$`)
	numberPattern  = regexp.MustCompile(`^\d+\.\s+(.+)$`)
)

func main() {
	flag.Parse()
	addrWasSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			addrWasSet = true
		}
	})

	absRoot, err := filepath.Abs(*rootDir)
	if err != nil {
		exitError("resolve project root: %v", err)
	}

	cfg, err := loadPortalConfig(*cfgPath)
	if err != nil {
		exitError("load config: %v", err)
	}
	if cfg.Service.PublicURL != "" {
		publicURL = cfg.Service.PublicURL
	}

	logger, err := logging.New(os.Stdout, cfg.Service.LogLevel, cfg.Service.LogFormat)
	if err != nil {
		exitError("create logger: %v", err)
	}

	switch cfg.Service.Mode {
	case relayconfig.ModeDocs:
		err = runDocsPortal(absRoot, *cfg, *addr, addrWasSet, logger)
	case relayconfig.ModeAPI:
		err = runAPIServer(*cfg, *addr, addrWasSet, logger)
	case relayconfig.ModeWorker:
		err = runWorkerMode(*cfg, logger)
	default:
		err = fmt.Errorf("unsupported service mode %q", cfg.Service.Mode)
	}
	if err != nil {
		logger.Error("relay_service_stopped", "error", err)
		os.Exit(1)
	}
}

func runDocsPortal(absRoot string, cfg relayconfig.Config, flagAddr string, addrWasSet bool, logger *slog.Logger) error {
	listenAddr := cfg.Service.DocsAddr
	if addrWasSet {
		listenAddr = flagAddr
	}

	eventHub := events.NewHub()
	apiDeps, ledgerWriter, apiCleanup, err := buildAPIDependencies(cfg, logger)
	apiDeps.Events = eventHub
	if err != nil {
		logger.Warn("relay_docs_api_dependencies_unavailable", "error", err)
		apiDeps = api.Dependencies{Events: eventHub}
		ledgerWriter = nil
		apiCleanup = func() {}
	}
	defer apiCleanup()
	stopLedgerSync := startLedgerSyncLoop(context.Background(), cfg, ledgerWriter, apiDeps.Orders, eventHub, logger)
	defer stopLedgerSync()

	mux := http.NewServeMux()
	server := &portalServer{root: absRoot, logger: logger}
	mux.HandleFunc("/", server.handleHome)
	mux.HandleFunc("/healthz", server.handleHealthz)
	mux.Handle("/v1/", api.NewWithDependencies(cfg, logger, apiDeps))
	mux.HandleFunc("/docs", server.handleDocsIndex)
	mux.HandleFunc("/docs/", server.handleDoc)
	mux.HandleFunc("/api-console", server.handleAPIConsole)
	mux.HandleFunc("/trade", server.handleTradeTerminal)
	mux.HandleFunc("/jobs", server.handleJobStatus)
	staticFS, err := fs.Sub(portalAssets, "web/static")
	if err != nil {
		return err
	}
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(staticFS))))
	mux.Handle("/sdk/", http.StripPrefix("/sdk/", http.FileServer(http.Dir(filepath.Join(absRoot, "public", "sdk")))))
	mux.HandleFunc("/tests", server.handleTests)
	mux.HandleFunc("/tree", server.handleTree)
	mux.HandleFunc("/raw/", server.handleRaw)

	logger.Info("relay_service_listening",
		"mode", cfg.Service.Mode,
		"addr", listenAddr,
		"public_url", cfg.Service.PublicURL,
		"project_root", absRoot,
		"api_console_enabled", true,
		"order_service_enabled", apiDeps.Orders != nil,
	)
	return http.ListenAndServe(listenAddr, httpx.RequestLogger(logger)(mux))
}

func runAPIServer(cfg relayconfig.Config, flagAddr string, addrWasSet bool, logger *slog.Logger) error {
	cfg = redisstream.ApplyProbeEnv(cfg)
	listenAddr := cfg.Service.APIAddr
	if addrWasSet {
		listenAddr = flagAddr
	}

	eventHub := events.NewHub()
	deps, ledgerWriter, cleanup, err := buildAPIDependencies(cfg, logger)
	deps.Events = eventHub
	if err != nil {
		return err
	}
	defer cleanup()
	stopLedgerSync := startLedgerSyncLoop(context.Background(), cfg, ledgerWriter, deps.Orders, eventHub, logger)
	defer stopLedgerSync()

	logger.Info("relay_service_listening",
		"mode", cfg.Service.Mode,
		"addr", listenAddr,
		"public_url", cfg.Service.PublicURL,
		"order_service_enabled", deps.Orders != nil,
	)
	return http.ListenAndServe(listenAddr, api.NewWithDependencies(cfg, logger, deps))
}

func buildAPIDependencies(cfg relayconfig.Config, logger *slog.Logger) (api.Dependencies, redisstream.LedgerWriter, func(), error) {
	cleanup := func() {}
	if strings.TrimSpace(cfg.Database.DSN) == "" {
		logger.Warn("relay_api_order_service_unavailable", "reason", "database.dsn is required")
		return api.Dependencies{}, nil, cleanup, nil
	}

	db, err := sql.Open("pgx", cfg.Database.DSN)
	if err != nil {
		return api.Dependencies{}, nil, cleanup, err
	}
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	cleanup = func() {
		_ = db.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		cleanup()
		return api.Dependencies{}, nil, func() {}, err
	}

	var publisher orderflow.CommandPublisher
	var redisPublisher *redisstream.RedisCommandPublisher
	if strings.TrimSpace(cfg.Redis.URL) == "" {
		logger.Warn("relay_api_trade_commands_unavailable", "reason", "redis.url is required")
	} else {
		redisPublisher, err = redisstream.OpenRedisCommandPublisher(cfg.Redis)
		if err != nil {
			cleanup()
			return api.Dependencies{}, nil, func() {}, err
		}
		publisher = redisPublisher
		previousCleanup := cleanup
		cleanup = func() {
			_ = redisPublisher.Close()
			previousCleanup()
		}
	}

	repo := ledger.NewRepository(db)
	orders, err := orderflow.New(orderflow.Options{
		Config:    cfg,
		Ledger:    repo,
		Publisher: publisher,
	})
	if err != nil {
		cleanup()
		return api.Dependencies{}, nil, func() {}, err
	}

	deps := api.Dependencies{
		Orders:       orders,
		Jobs:         repo,
		Settlements:  repo,
		DatabasePing: db.PingContext,
	}
	if redisPublisher != nil {
		deps.RedisPing = redisPublisher.Ping
	}
	return deps, repo, cleanup, nil
}

func startLedgerSyncLoop(ctx context.Context, cfg relayconfig.Config, writer redisstream.LedgerWriter, refresher orderflow.AccountRefresher, eventHub *events.Hub, logger *slog.Logger) func() {
	if writer == nil || strings.TrimSpace(cfg.Redis.URL) == "" {
		logger.Warn("relay_ledger_sync_loop_disabled", "reason", "redis url or ledger writer missing")
		return func() {}
	}

	syncCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var autoRefresh *orderflow.AutoRefreshScheduler
	if cfg.AutoRefreshEnabled() && refresher != nil {
		autoRefresh = orderflow.NewAutoRefreshScheduler(orderflow.AutoRefreshSchedulerOptions{
			Refresher: refresher,
			Logger:    logger.With("component", "auto-refresh"),
			Debounce:  time.Duration(cfg.AutoRefresh.DebounceSeconds) * time.Second,
			Cooldown:  time.Duration(cfg.AutoRefresh.CooldownSeconds) * time.Second,
			Timeout:   time.Duration(cfg.AutoRefresh.TimeoutSeconds) * time.Second,
		})
		logger.Info("relay_auto_refresh_enabled",
			"debounce", fmt.Sprintf("%ds", cfg.AutoRefresh.DebounceSeconds),
			"cooldown", fmt.Sprintf("%ds", cfg.AutoRefresh.CooldownSeconds),
			"timeout", fmt.Sprintf("%ds", cfg.AutoRefresh.TimeoutSeconds),
		)
	} else {
		logger.Info("relay_auto_refresh_disabled", "enabled", cfg.AutoRefreshEnabled(), "refresher_available", refresher != nil)
	}
	go func() {
		defer close(done)
		var checkpoints redisstream.LedgerCheckpointStore
		if checkpointStore, ok := writer.(redisstream.LedgerCheckpointStore); ok {
			checkpoints = checkpointStore
			logger.Info("relay_ledger_sync_checkpoints_enabled")
		} else {
			logger.Warn("relay_ledger_sync_checkpoints_unavailable")
		}
		err := redisstream.RunLedgerSyncLoop(syncCtx, cfg, writer, redisstream.LedgerSyncLoopOptions{
			StartID:     "0",
			Count:       200,
			Block:       time.Second,
			Roles:       []string{redisstream.SuffixReply, redisstream.SuffixEvent},
			Checkpoints: checkpoints,
			OnTradeChange: func(_ context.Context, change redisstream.LedgerTradeChange) {
				if autoRefresh == nil {
					return
				}
				reason := fmt.Sprintf("ledger:%s order_events=%d fills=%d", change.LastStreamID, change.OrderEvents, change.Fills)
				autoRefresh.RequestAccounts(change.AccountIDs, reason)
			},
			OnLedgerChange: func(_ context.Context, change redisstream.LedgerChange) {
				publishLedgerEvents(eventHub, change)
			},
		}, logger.With("component", "ledger-sync-loop"))
		if err != nil && syncCtx.Err() == nil {
			logger.Error("relay_ledger_sync_loop_stopped", "error", err)
		}
	}()

	return func() {
		cancel()
		if autoRefresh != nil {
			autoRefresh.Stop()
		}
		<-done
	}
}

func publishLedgerEvents(eventHub *events.Hub, change redisstream.LedgerChange) {
	if eventHub == nil {
		return
	}
	base := events.Event{
		AccountIDs:   change.AccountIDs,
		Source:       "redis-ledger-sync",
		Stream:       change.Stream,
		LastStreamID: change.LastStreamID,
		Data: map[string]any{
			"role":           change.Role,
			"orders":         change.Orders,
			"order_events":   change.OrderEvents,
			"fills":          change.Fills,
			"assets":         change.Assets,
			"positions":      change.Positions,
			"last_stream_id": change.LastStreamID,
		},
	}
	if change.Orders > 0 || change.OrderEvents > 0 {
		event := base
		event.Type = events.TypeOrderChanged
		eventHub.Publish(event)
	}
	if change.Fills > 0 {
		event := base
		event.Type = events.TypeFillChanged
		eventHub.Publish(event)
	}
	if change.Assets > 0 {
		event := base
		event.Type = events.TypeAssetChanged
		eventHub.Publish(event)
	}
	if change.Positions > 0 {
		event := base
		event.Type = events.TypePositionsChanged
		eventHub.Publish(event)
	}
}

func runWorkerMode(cfg relayconfig.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return worker.Run(ctx, cfg, logger)
}

func loadPortalConfig(path string) (*relayconfig.Config, error) {
	if strings.TrimSpace(path) == "" {
		cfg := relayconfig.Default()
		return &cfg, nil
	}
	return relayconfig.Load(path)
}

func exitError(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "relay: "+format+"\n", args...)
	os.Exit(1)
}

type portalServer struct {
	root   string
	logger *slog.Logger
}

func (s *portalServer) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	content := `
<section class="hero">
  <p class="eyebrow">relay documentation portal</p>
  <h1>TI Relay Trader</h1>
  <p>9092 当前运行文档门户模式，用于查看项目框架、设计文档、接入手册和测试目录。该服务不连接实盘柜台，不处理交易命令。</p>
  <p>最终服务口径：<a href="` + publicURL + `">` + publicURL + `</a></p>
  <div class="actions">
    <a href="/docs">查看文档</a>
    <a href="/api-console">接口测试台</a>
    <a href="/trade">交易终端</a>
    <a href="/jobs">任务状态</a>
    <a href="/docs/roadmap">开发路线图</a>
    <a href="/sdk/relay-sdk-0.1.9.tar.gz">SDK 下载</a>
    <a href="/tree">项目结构</a>
    <a href="/tests">测试目录</a>
    <a href="/healthz">健康检查</a>
  </div>
</section>
<section class="grid">
  <a class="card" href="/docs/readme"><strong>README</strong><span>恢复卡片、状态、待办</span></a>
  <a class="card" href="/docs/architecture"><strong>架构草案</strong><span>Go + Python 服务边界</span></a>
  <a class="card" href="/docs/roadmap"><strong>开发路线图</strong><span>阶段规划与进度跟踪</span></a>
  <a class="card" href="/docs/data-model"><strong>数据模型</strong><span>落盘与字段映射</span></a>
  <a class="card" href="/docs/migrations"><strong>PostgreSQL Migration</strong><span>交易账本首版 DDL</span></a>
  <a class="card" href="/docs/trading-api-schema"><strong>交易接口 Schema</strong><span>标准对象与状态机</span></a>
  <a class="card" href="/api-console"><strong>接口测试台</strong><span>Apifox 风格联调页面</span></a>
  <a class="card" href="/trade"><strong>交易终端</strong><span>成熟交易软件风格手动测试台</span></a>
  <a class="card" href="/jobs"><strong>任务状态</strong><span>盘前初始化与盘后结算监控</span></a>
  <a class="card" href="/docs/trading-terminal"><strong>交易终端文档</strong><span>手动测试台实现说明</span></a>
  <a class="card" href="/docs/python-sdk"><strong>Python SDK</strong><span>策略开发客户端</span></a>
  <a class="card" href="/sdk/relay-sdk-0.1.9.tar.gz"><strong>SDK 安装包</strong><span>relay-sdk 0.1.9 tar.gz</span></a>
  <a class="card" href="/docs/operations"><strong>运行配置</strong><span>凭据与 cron 任务</span></a>
  <a class="card" href="/docs/trading-day-workflow"><strong>交易日流程</strong><span>盘前初始化与盘后结算</span></a>
  <a class="card" href="/docs/redis-stream-probe"><strong>Redis Stream 探测</strong><span>只读联调入口</span></a>
  <a class="card" href="/docs/redis-ledger-sync"><strong>Redis 账本同步</strong><span>reply/event 落盘入口</span></a>
  <a class="card" href="/docs/third-party-integration"><strong>前置对接</strong><span>Redis Stream 协议手册</span></a>
  <a class="card" href="/tests"><strong>测试目录</strong><span>测试索引与目录树</span></a>
</section>`

	s.render(w, pageData{
		Title:      "relay 文档门户",
		Active:     "home",
		Summary:    "9092 文档门户模式",
		Content:    template.HTML(content),
		ProjectDir: s.root,
	})
}

func (s *portalServer) handleAPIConsole(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api-console" {
		http.NotFound(w, r)
		return
	}

	var body bytes.Buffer
	if err := apiConsoleTemplate.Execute(&body, map[string]string{
		"PublicURL": publicURL,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, pageData{
		Title:      "接口测试台",
		Active:     "console",
		Summary:    "Form-based API console",
		Head:       template.HTML(`<link rel="stylesheet" href="/assets/api-console.css">`),
		Content:    template.HTML(body.String()),
		Scripts:    template.HTML(`<script defer src="/assets/api-console.js"></script>`),
		ProjectDir: s.root,
	})
}

func (s *portalServer) handleTradeTerminal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/trade" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tradeTerminalTemplate.Execute(w, map[string]string{
		"PublicURL": publicURL,
	}); err != nil {
		s.logger.Error("render_trade_terminal_failed", "error", err)
	}
}

func (s *portalServer) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/jobs" {
		http.NotFound(w, r)
		return
	}

	var body bytes.Buffer
	if err := jobStatusTemplate.Execute(&body, map[string]string{
		"PublicURL": publicURL,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, pageData{
		Title:      "任务状态",
		Active:     "jobs",
		Summary:    "Daily jobs and background process monitor",
		Head:       template.HTML(`<link rel="stylesheet" href="/assets/job-status.css">`),
		Content:    template.HTML(body.String()),
		Scripts:    template.HTML(`<script defer src="/assets/job-status.js"></script>`),
		ProjectDir: s.root,
	})
}

func (s *portalServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"service":    "relay-docs",
		"mode":       "documentation-portal",
		"status":     "ok",
		"public_url": publicURL,
		"time":       timeutil.Now().Format(time.RFC3339Nano),
	})
}

func (s *portalServer) handleDocsIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/docs" {
		http.NotFound(w, r)
		return
	}

	var b strings.Builder
	b.WriteString(`<section class="panel"><h1>文档</h1><p>这些内容直接读取仓库文件，便于线程恢复和项目协作。</p><div class="doc-list">`)
	for _, doc := range docs {
		fmt.Fprintf(&b, `<a class="doc-item" href="/docs/%s"><strong>%s</strong><span>%s</span><code>%s</code></a>`,
			html.EscapeString(doc.Slug),
			html.EscapeString(doc.Title),
			html.EscapeString(doc.Description),
			html.EscapeString(doc.Path),
		)
	}
	b.WriteString(`</div></section>`)

	s.render(w, pageData{
		Title:      "文档",
		Active:     "docs",
		Content:    template.HTML(b.String()),
		Docs:       docs,
		ProjectDir: s.root,
	})
}

func (s *portalServer) handleDoc(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/docs/")
	doc, ok := findDoc(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}

	body, err := s.readProjectFile(doc.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var content strings.Builder
	fmt.Fprintf(&content, `<div class="doc-tools"><a href="/raw/%s">Raw</a></div>`, html.EscapeString(doc.Path))
	content.WriteString(renderMarkdown(string(body)))

	s.render(w, pageData{
		Title:      doc.Title,
		Active:     "docs",
		Summary:    doc.Path,
		Content:    template.HTML(content.String()),
		Docs:       docs,
		Doc:        &doc,
		ProjectDir: s.root,
	})
}

func (s *portalServer) handleTests(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tests" {
		http.NotFound(w, r)
		return
	}

	body, err := s.readProjectFile("tests/README.md")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tree, err := buildTree(filepath.Join(s.root, "tests"), s.root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	content := renderMarkdown(string(body)) +
		`<section class="panel"><h2>测试目录树</h2><pre class="tree">` +
		html.EscapeString(tree) +
		`</pre></section>`

	s.render(w, pageData{
		Title:      "测试目录",
		Active:     "tests",
		Content:    template.HTML(content),
		ProjectDir: s.root,
	})
}

func (s *portalServer) handleTree(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/tree" {
		http.NotFound(w, r)
		return
	}

	tree, err := buildTree(s.root, s.root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	content := `<section class="panel"><h1>项目结构</h1><p>已隐藏 .git 目录和常见构建产物。</p><pre class="tree">` +
		html.EscapeString(tree) +
		`</pre></section>`

	s.render(w, pageData{
		Title:      "项目结构",
		Active:     "tree",
		Content:    template.HTML(content),
		ProjectDir: s.root,
	})
}

func (s *portalServer) handleRaw(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/raw/")
	body, err := s.readProjectFile(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(body)
}

func (s *portalServer) readProjectFile(rel string) ([]byte, error) {
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return nil, errors.New("invalid project path")
	}
	path := filepath.Join(s.root, clean)
	if !strings.HasPrefix(path, s.root+string(os.PathSeparator)) && path != s.root {
		return nil, errors.New("path escapes project root")
	}
	return os.ReadFile(path)
}

func (s *portalServer) render(w http.ResponseWriter, data pageData) {
	data.Generated = time.Now().Format("2006-01-02 15:04:05")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, data); err != nil {
		s.logger.Error("render_page_failed", "error", err)
	}
}

func findDoc(slug string) (docPage, bool) {
	for _, doc := range docs {
		if doc.Slug == slug {
			return doc, true
		}
	}
	return docPage{}, false
}

func buildTree(root, projectRoot string) (string, error) {
	type entry struct {
		path string
		info fs.FileInfo
	}
	var entries []entry
	err := filepath.WalkDir(root, func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := dirEntry.Name()
		if dirEntry.IsDir() && shouldSkipDir(name) {
			return filepath.SkipDir
		}
		if !dirEntry.IsDir() && shouldSkipFile(name) {
			return nil
		}
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		entries = append(entries, entry{path: path, info: info})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})

	var b strings.Builder
	for _, entry := range entries {
		rel, err := filepath.Rel(projectRoot, entry.path)
		if err != nil {
			return "", err
		}
		if rel == "." {
			rel = filepath.Base(projectRoot)
		}
		depth := strings.Count(rel, string(os.PathSeparator))
		if rel == filepath.Base(projectRoot) {
			depth = 0
		}
		indent := strings.Repeat("  ", depth)
		label := filepath.Base(rel)
		if rel == filepath.Base(projectRoot) {
			label = rel
		}
		if entry.info.IsDir() {
			fmt.Fprintf(&b, "%s%s/\n", indent, label)
			continue
		}
		fmt.Fprintf(&b, "%s%s\n", indent, label)
	}
	return b.String(), nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", ".venv", "venv", "__pycache__", "dist", "build", ".pytest_cache":
		return true
	default:
		return false
	}
}

func shouldSkipFile(name string) bool {
	return strings.HasSuffix(name, ".pyc") ||
		strings.HasSuffix(name, ".log") ||
		strings.HasPrefix(name, ".DS_Store")
}

func renderMarkdown(md string) string {
	lines := strings.Split(md, "\n")
	var b strings.Builder
	inCode := false
	inUL := false
	inOL := false
	inTable := false

	closeLists := func() {
		if inUL {
			b.WriteString("</ul>")
			inUL = false
		}
		if inOL {
			b.WriteString("</ol>")
			inOL = false
		}
	}
	closeTable := func() {
		if inTable {
			b.WriteString("</tbody></table>")
			inTable = false
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			closeLists()
			closeTable()
			if inCode {
				b.WriteString("</code></pre>")
				inCode = false
			} else {
				b.WriteString("<pre><code>")
				inCode = true
			}
			continue
		}
		if inCode {
			b.WriteString(html.EscapeString(line))
			b.WriteByte('\n')
			continue
		}
		if trimmed == "" {
			closeLists()
			closeTable()
			continue
		}
		if isMarkdownTableRow(trimmed) {
			closeLists()
			if isMarkdownTableSeparator(trimmed) {
				continue
			}
			if !inTable {
				b.WriteString("<table><tbody>")
				inTable = true
			}
			b.WriteString("<tr>")
			for _, cell := range splitTableRow(trimmed) {
				fmt.Fprintf(&b, "<td>%s</td>", inlineMarkdown(cell))
			}
			b.WriteString("</tr>")
			continue
		}
		closeTable()
		if match := headingPattern.FindStringSubmatch(trimmed); match != nil {
			closeLists()
			level := len(match[1])
			fmt.Fprintf(&b, "<h%d>%s</h%d>", level, inlineMarkdown(match[2]), level)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if inOL {
				b.WriteString("</ol>")
				inOL = false
			}
			if !inUL {
				b.WriteString("<ul>")
				inUL = true
			}
			fmt.Fprintf(&b, "<li>%s</li>", inlineMarkdown(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			continue
		}
		if match := numberPattern.FindStringSubmatch(trimmed); match != nil {
			if inUL {
				b.WriteString("</ul>")
				inUL = false
			}
			if !inOL {
				b.WriteString("<ol>")
				inOL = true
			}
			fmt.Fprintf(&b, "<li>%s</li>", inlineMarkdown(match[1]))
			continue
		}
		closeLists()
		fmt.Fprintf(&b, "<p>%s</p>", inlineMarkdown(trimmed))
	}
	closeLists()
	closeTable()
	if inCode {
		b.WriteString("</code></pre>")
	}
	return b.String()
}

func isMarkdownTableRow(line string) bool {
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|")
}

func isMarkdownTableSeparator(line string) bool {
	for _, char := range strings.Trim(line, "| ") {
		if char != '-' && char != ':' && char != '|' && char != ' ' {
			return false
		}
	}
	return true
}

func splitTableRow(line string) []string {
	trimmed := strings.Trim(line, "|")
	parts := strings.Split(trimmed, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func inlineMarkdown(text string) string {
	escaped := html.EscapeString(text)
	escaped = strings.ReplaceAll(escaped, "`", "")
	return escaped
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} - relay</title>
  <style>
    :root {
      --bg: #f5f7fa;
      --panel: #ffffff;
      --text: #18202a;
      --muted: #667085;
      --line: #d8dee8;
      --accent: #0f766e;
      --accent-soft: #e7f6f3;
      --code: #f0f3f7;
      --shadow: 0 12px 32px rgba(16, 24, 40, 0.08);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--text);
      font: 15px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    header {
      position: sticky;
      top: 0;
      z-index: 10;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 18px;
      height: 58px;
      padding: 0 28px;
      border-bottom: 1px solid var(--line);
      background: rgba(255,255,255,0.96);
      backdrop-filter: blur(8px);
    }
    .brand {
      display: flex;
      align-items: baseline;
      gap: 10px;
      color: var(--text);
      text-decoration: none;
      font-weight: 800;
    }
    .brand span {
      color: var(--muted);
      font-size: 12px;
      font-weight: 600;
    }
    nav {
      display: flex;
      gap: 6px;
      flex-wrap: wrap;
      justify-content: flex-end;
    }
    nav a, .actions a, .doc-tools a {
      display: inline-flex;
      align-items: center;
      min-height: 34px;
      padding: 7px 11px;
      border: 1px solid var(--line);
      border-radius: 6px;
      color: var(--text);
      background: #fff;
      text-decoration: none;
      font-weight: 650;
      font-size: 13px;
    }
    nav a:hover, nav a.active, .actions a:hover, .doc-tools a:hover {
      border-color: var(--accent);
      color: var(--accent);
      background: var(--accent-soft);
    }
    main {
      width: min(1180px, calc(100vw - 36px));
      margin: 28px auto 56px;
    }
    .meta {
      display: flex;
      flex-wrap: wrap;
      gap: 10px 18px;
      margin-bottom: 18px;
      color: var(--muted);
      font-size: 13px;
    }
    .hero, .panel {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      box-shadow: var(--shadow);
    }
    .hero {
      padding: 30px;
    }
    .eyebrow {
      margin: 0 0 8px;
      color: var(--accent);
      font-size: 12px;
      font-weight: 800;
      text-transform: uppercase;
    }
    h1, h2, h3, h4 { line-height: 1.25; }
    h1 { margin: 0 0 12px; font-size: 30px; }
    h2 { margin-top: 28px; font-size: 22px; }
    h3 { margin-top: 22px; font-size: 18px; }
    p { margin: 10px 0; }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: 9px;
      margin-top: 22px;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 14px;
      margin-top: 16px;
    }
    .card, .doc-item {
      display: flex;
      flex-direction: column;
      gap: 8px;
      min-height: 118px;
      padding: 18px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      color: var(--text);
      text-decoration: none;
    }
    .card:hover, .doc-item:hover {
      border-color: var(--accent);
    }
    .card span, .doc-item span {
      color: var(--muted);
      font-size: 13px;
    }
    .doc-list {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 14px;
      margin-top: 18px;
    }
    .panel, article {
      padding: 26px;
    }
    article {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      box-shadow: var(--shadow);
      overflow-x: auto;
    }
    .doc-tools {
      display: flex;
      justify-content: flex-end;
      margin-bottom: 16px;
    }
    code {
      padding: 2px 5px;
      border-radius: 5px;
      background: var(--code);
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 0.92em;
    }
    pre {
      padding: 16px;
      border-radius: 8px;
      overflow-x: auto;
      background: #101828;
      color: #eef2f6;
      line-height: 1.45;
    }
    pre code {
      padding: 0;
      background: transparent;
      color: inherit;
    }
    .tree {
      min-height: 220px;
      white-space: pre;
    }
    table {
      width: 100%;
      margin: 16px 0;
      border-collapse: collapse;
      font-size: 14px;
    }
    td, th {
      border: 1px solid var(--line);
      padding: 8px 10px;
      vertical-align: top;
    }
    td:first-child {
      font-weight: 650;
      white-space: nowrap;
    }
    footer {
      width: min(1180px, calc(100vw - 36px));
      margin: 0 auto 24px;
      color: var(--muted);
      font-size: 12px;
    }
    @media (max-width: 860px) {
      header { height: auto; align-items: flex-start; padding: 14px 18px; flex-direction: column; }
      nav { justify-content: flex-start; }
      main { width: min(100vw - 24px, 1180px); margin-top: 18px; }
      .grid, .doc-list { grid-template-columns: 1fr; }
      .hero, .panel, article { padding: 20px; }
    }
  </style>
  {{.Head}}
</head>
<body>
  <header>
    <a class="brand" href="/">relay <span>9092 docs</span></a>
    <nav>
      <a class="{{if eq .Active "home"}}active{{end}}" href="/">首页</a>
      <a class="{{if eq .Active "docs"}}active{{end}}" href="/docs">文档</a>
      <a class="{{if eq .Active "console"}}active{{end}}" href="/api-console">接口</a>
      <a href="/trade">交易终端</a>
      <a class="{{if eq .Active "jobs"}}active{{end}}" href="/jobs">任务</a>
      <a href="/docs/python-sdk">SDK</a>
      <a class="{{if eq .Active "tree"}}active{{end}}" href="/tree">项目结构</a>
      <a class="{{if eq .Active "tests"}}active{{end}}" href="/tests">测试</a>
      <a href="/healthz">健康</a>
    </nav>
  </header>
  <main>
    <div class="meta">
      <span>项目目录: {{.ProjectDir}}</span>
      <span>生成时间: {{.Generated}}</span>
      {{if .Summary}}<span>{{.Summary}}</span>{{end}}
    </div>
    <article>{{.Content}}</article>
  </main>
  <footer>relay documentation portal. Basic API discovery is available here; trading and ledger routes follow the loaded local config.</footer>
  {{.Scripts}}
</body>
</html>`))
