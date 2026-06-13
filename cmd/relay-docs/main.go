package main

import (
	"context"
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

	"ti-relay-trader/internal/api"
	relayconfig "ti-relay-trader/internal/config"
	"ti-relay-trader/internal/httpx"
	"ti-relay-trader/internal/logging"
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
	Content    template.HTML
	Docs       []docPage
	Doc        *docPage
	ProjectDir string
}

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
			Slug:        "redis-stream-probe",
			Title:       "Redis Stream 探测",
			Path:        "docs/REDIS_STREAM_PROBE.md",
			Description: "前置测试环境 Redis Stream 只读探测命令、输出和联调顺序。",
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

	mux := http.NewServeMux()
	server := &portalServer{root: absRoot, logger: logger}
	mux.HandleFunc("/", server.handleHome)
	mux.HandleFunc("/healthz", server.handleHealthz)
	mux.HandleFunc("/docs", server.handleDocsIndex)
	mux.HandleFunc("/docs/", server.handleDoc)
	mux.HandleFunc("/api-console", server.handleAPIConsole)
	mux.HandleFunc("/tests", server.handleTests)
	mux.HandleFunc("/tree", server.handleTree)
	mux.HandleFunc("/raw/", server.handleRaw)

	logger.Info("relay_service_listening",
		"mode", cfg.Service.Mode,
		"addr", listenAddr,
		"public_url", cfg.Service.PublicURL,
		"project_root", absRoot,
	)
	return http.ListenAndServe(listenAddr, httpx.RequestLogger(logger)(mux))
}

func runAPIServer(cfg relayconfig.Config, flagAddr string, addrWasSet bool, logger *slog.Logger) error {
	listenAddr := cfg.Service.APIAddr
	if addrWasSet {
		listenAddr = flagAddr
	}

	logger.Info("relay_service_listening",
		"mode", cfg.Service.Mode,
		"addr", listenAddr,
		"public_url", cfg.Service.PublicURL,
	)
	return http.ListenAndServe(listenAddr, api.New(cfg, logger))
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
    <a href="/docs/roadmap">开发路线图</a>
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
  <a class="card" href="/docs/trading-api-schema"><strong>交易接口 Schema</strong><span>标准对象与状态机</span></a>
  <a class="card" href="/api-console"><strong>接口测试台</strong><span>Apifox 风格联调页面</span></a>
  <a class="card" href="/docs/python-sdk"><strong>Python SDK</strong><span>策略开发客户端</span></a>
  <a class="card" href="/docs/operations"><strong>运行配置</strong><span>凭据与 cron 任务</span></a>
  <a class="card" href="/docs/redis-stream-probe"><strong>Redis Stream 探测</strong><span>只读联调入口</span></a>
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

	content := `<section class="console-shell">
  <aside class="console-sidebar">
    <div class="console-sidebar-head">
      <strong>relay API</strong>
      <span>v1alpha1</span>
    </div>
    <div id="endpointList" class="endpoint-list"></div>
  </aside>
  <section class="console-workbench">
    <div class="request-line">
      <select id="methodSelect" aria-label="HTTP method"></select>
      <input id="baseUrlInput" aria-label="Base URL" value="` + html.EscapeString(publicURL) + `" />
      <input id="pathInput" aria-label="Path" />
      <button id="sendButton" type="button">Send</button>
    </div>
    <div class="console-tabs" role="tablist">
      <button class="tab active" type="button" data-tab="params">Params</button>
      <button class="tab" type="button" data-tab="headers">Headers</button>
      <button class="tab" type="button" data-tab="body">Body</button>
    </div>
    <div class="tab-panel active" id="tab-params">
      <textarea id="queryInput" spellcheck="false"></textarea>
    </div>
    <div class="tab-panel" id="tab-headers">
      <textarea id="headersInput" spellcheck="false"></textarea>
    </div>
    <div class="tab-panel" id="tab-body">
      <textarea id="bodyInput" spellcheck="false"></textarea>
    </div>
  </section>
  <aside class="console-response">
    <div class="response-head">
      <strong>Response</strong>
      <span id="responseMeta">idle</span>
    </div>
    <pre id="responseOutput">{}</pre>
  </aside>
</section>
<script>
const endpoints = [
  { group: "基础", method: "GET", path: "/healthz", status: "ready", body: "" },
  { group: "基础", method: "GET", path: "/v1/status", status: "api-mode", body: "" },
  { group: "基础", method: "GET", path: "/v1/schema", status: "api-mode", body: "" },
  { group: "账户", method: "GET", path: "/v1/accounts", status: "api-mode", body: "" },
  { group: "账户", method: "GET", path: "/v1/accounts/{account_id}/asset", status: "planned", body: "" },
  { group: "账户", method: "GET", path: "/v1/accounts/{account_id}/positions", status: "planned", body: "" },
  { group: "交易", method: "POST", path: "/v1/orders", status: "planned", body: JSON.stringify({account_id:"00030484", gateway_order_id:"gw-demo-0001", symbol:"600000", exchange:"SH", trade_side:"B", business_type:"S", offset_type:"C", price:9.54, qty:100}, null, 2) },
  { group: "交易", method: "POST", path: "/v1/orders/batch", status: "planned", body: JSON.stringify({account_id:"00030484", orders:[]}, null, 2) },
  { group: "交易", method: "POST", path: "/v1/orders/{gateway_order_id}/cancel", status: "planned", body: JSON.stringify({account_id:"00030484", gateway_order_id:"gw-demo-0001"}, null, 2) },
  { group: "查询", method: "GET", path: "/v1/orders", status: "planned", body: "" },
  { group: "查询", method: "GET", path: "/v1/fills", status: "planned", body: "" },
  { group: "事件", method: "GET", path: "/v1/events/stream", status: "planned", body: "" }
];

const methodSelect = document.getElementById("methodSelect");
const baseUrlInput = document.getElementById("baseUrlInput");
const pathInput = document.getElementById("pathInput");
const queryInput = document.getElementById("queryInput");
const headersInput = document.getElementById("headersInput");
const bodyInput = document.getElementById("bodyInput");
const sendButton = document.getElementById("sendButton");
const responseMeta = document.getElementById("responseMeta");
const responseOutput = document.getElementById("responseOutput");
let selectedEndpoint = endpoints[0];

for (const method of ["GET", "POST", "PUT", "PATCH", "DELETE"]) {
  const option = document.createElement("option");
  option.value = method;
  option.textContent = method;
  methodSelect.appendChild(option);
}

function renderEndpointList() {
  const root = document.getElementById("endpointList");
  root.innerHTML = "";
  const groups = [...new Set(endpoints.map((endpoint) => endpoint.group))];
  for (const group of groups) {
    const title = document.createElement("div");
    title.className = "endpoint-group";
    title.textContent = group;
    root.appendChild(title);
    for (const endpoint of endpoints.filter((item) => item.group === group)) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "endpoint-item" + (endpoint === selectedEndpoint ? " active" : "");
      const method = document.createElement("span");
      method.className = "method " + endpoint.method.toLowerCase();
      method.textContent = endpoint.method;
      const path = document.createElement("span");
      path.className = "endpoint-path";
      path.textContent = endpoint.path;
      const status = document.createElement("span");
      status.className = "endpoint-status";
      status.textContent = endpoint.status;
      button.append(method, path, status);
      button.addEventListener("click", () => selectEndpoint(endpoint));
      root.appendChild(button);
    }
  }
}

function selectEndpoint(endpoint) {
  selectedEndpoint = endpoint;
  methodSelect.value = endpoint.method;
  pathInput.value = endpoint.path;
  bodyInput.value = endpoint.body || "";
  queryInput.value = "";
  headersInput.value = endpoint.method === "GET" ? "" : "Content-Type: application/json";
  sendButton.disabled = endpoint.status === "planned";
  renderEndpointList();
}

function readHeaders() {
  const headers = {};
  for (const line of headersInput.value.split("\n")) {
    const idx = line.indexOf(":");
    if (idx > 0) {
      headers[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
    }
  }
  return headers;
}

function buildURL() {
  const base = baseUrlInput.value.trim().replace(/\/+$/, "");
  const path = pathInput.value.trim().startsWith("/") ? pathInput.value.trim() : "/" + pathInput.value.trim();
  const url = new URL(base + path);
  for (const line of queryInput.value.split("\n")) {
    const idx = line.indexOf("=");
    if (idx > 0) {
      url.searchParams.set(line.slice(0, idx).trim(), line.slice(idx + 1).trim());
    }
  }
  return url.toString();
}

async function sendRequest() {
  const started = performance.now();
  responseMeta.textContent = "sending";
  responseOutput.textContent = "";
  const method = methodSelect.value;
  const init = { method, headers: readHeaders() };
  if (!["GET", "HEAD"].includes(method) && bodyInput.value.trim() !== "") {
    init.body = bodyInput.value;
  }
  try {
    const response = await fetch(buildURL(), init);
    const text = await response.text();
    const elapsed = Math.round(performance.now() - started);
    responseMeta.textContent = response.status + " " + response.statusText + " / " + elapsed + "ms";
    try {
      responseOutput.textContent = JSON.stringify(JSON.parse(text), null, 2);
    } catch {
      responseOutput.textContent = text;
    }
  } catch (err) {
    responseMeta.textContent = "error";
    responseOutput.textContent = String(err);
  }
}

document.querySelectorAll(".tab").forEach((tab) => {
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((item) => item.classList.remove("active"));
    document.querySelectorAll(".tab-panel").forEach((item) => item.classList.remove("active"));
    tab.classList.add("active");
    document.getElementById("tab-" + tab.dataset.tab).classList.add("active");
  });
});
sendButton.addEventListener("click", sendRequest);
selectEndpoint(endpoints[0]);
</script>`

	s.render(w, pageData{
		Title:      "接口测试台",
		Active:     "console",
		Summary:    "Apifox-style API console",
		Content:    template.HTML(content),
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
		"time":       time.Now().UTC().Format(time.RFC3339Nano),
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
    .console-shell {
      display: grid;
      grid-template-columns: 260px minmax(420px, 1fr) 380px;
      min-height: 620px;
      border: 1px solid var(--line);
      border-radius: 8px;
      overflow: hidden;
      background: #fff;
    }
    .console-sidebar, .console-response {
      background: #f8fafc;
    }
    .console-sidebar {
      border-right: 1px solid var(--line);
      overflow: auto;
    }
    .console-sidebar-head, .response-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 10px;
      min-height: 46px;
      padding: 12px 14px;
      border-bottom: 1px solid var(--line);
      color: var(--text);
    }
    .console-sidebar-head span, .response-head span {
      color: var(--muted);
      font-size: 12px;
      font-weight: 650;
    }
    .endpoint-list {
      padding: 10px;
    }
    .endpoint-group {
      margin: 12px 6px 6px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 800;
    }
    .endpoint-item {
      display: grid;
      grid-template-columns: 48px minmax(0, 1fr);
      gap: 4px 8px;
      width: 100%;
      min-height: 48px;
      margin-bottom: 4px;
      padding: 8px;
      border: 1px solid transparent;
      border-radius: 6px;
      background: transparent;
      color: var(--text);
      text-align: left;
      cursor: pointer;
    }
    .endpoint-item:hover, .endpoint-item.active {
      border-color: var(--accent);
      background: var(--accent-soft);
    }
    .method {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-width: 42px;
      height: 22px;
      border-radius: 5px;
      color: #fff;
      font-size: 11px;
      font-weight: 800;
    }
    .method.get { background: #0f766e; }
    .method.post { background: #2563eb; }
    .method.put, .method.patch { background: #a16207; }
    .method.delete { background: #b42318; }
    .endpoint-path {
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 12px;
      line-height: 22px;
    }
    .endpoint-status {
      grid-column: 2;
      color: var(--muted);
      font-size: 11px;
      font-weight: 700;
    }
    .console-workbench {
      min-width: 0;
      padding: 14px;
    }
    .request-line {
      display: grid;
      grid-template-columns: 86px minmax(160px, 260px) minmax(220px, 1fr) 86px;
      gap: 8px;
      margin-bottom: 12px;
    }
    .request-line input, .request-line select, .request-line button, .console-tabs button {
      min-height: 36px;
      border: 1px solid var(--line);
      border-radius: 6px;
      background: #fff;
      color: var(--text);
      font: inherit;
    }
    .request-line input, .request-line select {
      min-width: 0;
      padding: 0 10px;
    }
    .request-line button {
      border-color: var(--accent);
      background: var(--accent);
      color: #fff;
      font-weight: 800;
      cursor: pointer;
    }
    .request-line button:disabled {
      border-color: var(--line);
      background: #e4e7ec;
      color: var(--muted);
      cursor: not-allowed;
    }
    .console-tabs {
      display: flex;
      gap: 6px;
      margin-bottom: 8px;
      border-bottom: 1px solid var(--line);
    }
    .console-tabs button {
      min-width: 86px;
      border-bottom: 0;
      border-bottom-right-radius: 0;
      border-bottom-left-radius: 0;
      font-weight: 700;
      cursor: pointer;
    }
    .console-tabs button.active {
      border-color: var(--accent);
      color: var(--accent);
      background: var(--accent-soft);
    }
    .tab-panel { display: none; }
    .tab-panel.active { display: block; }
    .tab-panel textarea {
      width: 100%;
      height: 470px;
      resize: vertical;
      padding: 12px;
      border: 1px solid var(--line);
      border-radius: 6px;
      background: #fbfcfe;
      color: var(--text);
      font: 13px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }
    .console-response {
      min-width: 0;
      border-left: 1px solid var(--line);
      overflow: hidden;
    }
    .console-response pre {
      height: calc(100% - 46px);
      min-height: 574px;
      margin: 0;
      border-radius: 0;
      white-space: pre-wrap;
      word-break: break-word;
    }
    @media (max-width: 860px) {
      header { height: auto; align-items: flex-start; padding: 14px 18px; flex-direction: column; }
      nav { justify-content: flex-start; }
      main { width: min(100vw - 24px, 1180px); margin-top: 18px; }
      .grid, .doc-list { grid-template-columns: 1fr; }
      .hero, .panel, article { padding: 20px; }
      .console-shell { grid-template-columns: 1fr; }
      .console-sidebar, .console-response { border: 0; }
      .request-line { grid-template-columns: 1fr; }
      .tab-panel textarea, .console-response pre { min-height: 320px; height: 320px; }
    }
  </style>
</head>
<body>
  <header>
    <a class="brand" href="/">relay <span>9092 docs</span></a>
    <nav>
      <a class="{{if eq .Active "home"}}active{{end}}" href="/">首页</a>
      <a class="{{if eq .Active "docs"}}active{{end}}" href="/docs">文档</a>
      <a class="{{if eq .Active "console"}}active{{end}}" href="/api-console">接口</a>
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
  <footer>relay documentation portal. This mode is read-only and does not execute trading commands.</footer>
</body>
</html>`))
