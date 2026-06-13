package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
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
			Slug:        "operations",
			Title:       "运行配置与任务管理",
			Path:        "docs/OPERATIONS.md",
			Description: "本地配置文件、凭据管理、cron 后台任务和部署运行约定。",
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

	absRoot, err := filepath.Abs(*rootDir)
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}

	mux := http.NewServeMux()
	server := &portalServer{root: absRoot}
	mux.HandleFunc("/", server.handleHome)
	mux.HandleFunc("/healthz", server.handleHealthz)
	mux.HandleFunc("/docs", server.handleDocsIndex)
	mux.HandleFunc("/docs/", server.handleDoc)
	mux.HandleFunc("/tests", server.handleTests)
	mux.HandleFunc("/tree", server.handleTree)
	mux.HandleFunc("/raw/", server.handleRaw)

	log.Printf("relay documentation portal listening on http://%s", *addr)
	log.Printf("project root: %s", absRoot)
	if err := http.ListenAndServe(*addr, logRequest(mux)); err != nil {
		log.Fatal(err)
	}
}

type portalServer struct {
	root string
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
  <a class="card" href="/docs/operations"><strong>运行配置</strong><span>凭据与 cron 任务</span></a>
  <a class="card" href="/docs/third-party-integration"><strong>前置对接</strong><span>Redis Stream 协议手册</span></a>
  <a class="card" href="/tests"><strong>测试目录</strong><span>测试索引与目录树</span></a>
</section>`

	if roadmap, err := s.readProjectFile("docs/ROADMAP.md"); err == nil {
		content += `<section class="panel roadmap">` + renderMarkdown(string(roadmap)) + `</section>`
	} else {
		content += `<section class="panel"><h2>开发路线图</h2><p>docs/ROADMAP.md 暂不可读。</p></section>`
	}

	s.render(w, pageData{
		Title:      "relay 文档门户",
		Active:     "home",
		Summary:    "9092 文档门户模式",
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
		log.Printf("render page: %v", err)
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

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
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
</head>
<body>
  <header>
    <a class="brand" href="/">relay <span>9092 docs</span></a>
    <nav>
      <a class="{{if eq .Active "home"}}active{{end}}" href="/">首页</a>
      <a class="{{if eq .Active "docs"}}active{{end}}" href="/docs">文档</a>
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
