(function () {
  const els = {
    serviceStatus: document.getElementById("jobsServiceStatus"),
    tradingPhase: document.getElementById("jobsTradingPhase"),
    dependencies: document.getElementById("jobsDependencies"),
    refreshTime: document.getElementById("jobsRefreshTime"),
    cards: document.getElementById("jobCards"),
    count: document.getElementById("jobCount"),
    body: document.getElementById("jobRunsBody"),
    reportTitle: document.getElementById("jobReportTitle"),
    report: document.getElementById("jobReport"),
    refresh: document.getElementById("refreshJobs"),
    filter: document.getElementById("jobNameFilter"),
  };

  const knownJobs = [
    { name: "pre_open_init", title: "盘前初始化" },
    { name: "post_close_settlement", title: "盘后结算" },
  ];

  function escapeHTML(value) {
    return String(value == null ? "" : value).replace(/[&<>"']/g, (char) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#39;",
    }[char]));
  }

  function unwrap(payload) {
    if (payload && payload.ok === true && Object.prototype.hasOwnProperty.call(payload, "data")) {
      return payload.data;
    }
    return payload;
  }

  async function getJSON(path) {
    const response = await fetch(path, { headers: { Accept: "application/json" } });
    const text = await response.text();
    let payload = null;
    try {
      payload = text ? JSON.parse(text) : null;
    } catch (error) {
      throw new Error(`JSON 解析失败: ${error.message}`);
    }
    if (!response.ok || (payload && payload.ok === false)) {
      const message = payload && payload.error ? payload.error.message : response.statusText;
      throw new Error(`${response.status} ${message}`);
    }
    return unwrap(payload);
  }

  function statusLabel(status, skipped) {
    if (skipped) return "skipped";
    return status || "unknown";
  }

  function statusClass(status, skipped) {
    const normalized = statusLabel(status, skipped);
    if (normalized === "succeeded" || normalized === "completed" || normalized === "ok") return "succeeded";
    if (normalized === "running") return "running";
    if (normalized === "failed" || normalized === "error") return "failed";
    if (normalized === "skipped") return "skipped";
    return "";
  }

  function formatTime(value) {
    if (!value) return "--";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return String(value);
    return date.toLocaleString("zh-CN", {
      hour12: false,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  }

  function formatDuration(ms) {
    const value = Number(ms || 0);
    if (!value) return "--";
    if (value < 1000) return `${value} ms`;
    const seconds = value / 1000;
    if (seconds < 60) return `${seconds.toFixed(2)} s`;
    const minutes = Math.floor(seconds / 60);
    return `${minutes}m ${(seconds % 60).toFixed(0)}s`;
  }

  function dependencySummary(dependencies) {
    if (!dependencies || typeof dependencies !== "object") return "--";
    const values = Object.values(dependencies);
    if (!values.length) return "--";
    const bad = values.filter((item) => item && item.status && item.status !== "ok");
    return bad.length ? `${bad.length}/${values.length} 异常` : `${values.length}/${values.length} ok`;
  }

  function jobTitle(name) {
    const found = knownJobs.find((item) => item.name === name);
    return found ? found.title : name;
  }

  function reportSummary(run) {
    const report = run && run.report;
    if (!report || typeof report !== "object") return "--";
    const parts = [];
    if (report.ok !== undefined) parts.push(report.ok ? "ok" : "not ok");
    if (report.skipped) parts.push("skipped");
    if (Array.isArray(report.accounts)) parts.push(`accounts ${report.accounts.length}`);
    if (Array.isArray(report.errors) && report.errors.length) parts.push(`errors ${report.errors.length}`);
    const snapshotReport = report.settlement_snapshot || report.open_snapshot;
    const snapshot = snapshotReport && snapshotReport.result;
    if (snapshot) {
      if (snapshot.asset_snapshots !== undefined) parts.push(`asset ${snapshot.asset_snapshots}`);
      if (snapshot.position_snapshots !== undefined) parts.push(`positions ${snapshot.position_snapshots}`);
      if (snapshot.reconciliation_breaks !== undefined) parts.push(`breaks ${snapshot.reconciliation_breaks}`);
    }
    return parts.join(" · ") || "--";
  }

  function renderOverview(status) {
    els.serviceStatus.textContent = status.status || "--";
    const tradingDay = status.trading_day || {};
    els.tradingPhase.textContent = [tradingDay.date, tradingDay.phase].filter(Boolean).join(" / ") || "--";
    els.dependencies.textContent = dependencySummary(status.dependencies);
    els.refreshTime.textContent = formatTime(new Date().toISOString());
  }

  function renderCards(runs) {
    const byName = new Map(runs.map((run) => [run.job_name, run]));
    const names = [...knownJobs.map((item) => item.name), ...runs.map((run) => run.job_name)]
      .filter((name, index, array) => name && array.indexOf(name) === index);
    els.cards.innerHTML = names.map((name) => {
      const run = byName.get(name);
      if (!run) {
        return `
          <article class="job-card">
            <div class="job-card-top">
              <h2>${escapeHTML(jobTitle(name))}</h2>
              <span class="status-badge">未运行</span>
            </div>
            <dl>
              <div><dt>任务名</dt><dd>${escapeHTML(name)}</dd></div>
              <div><dt>交易日</dt><dd>--</dd></div>
              <div><dt>开始</dt><dd>--</dd></div>
              <div><dt>完成</dt><dd>--</dd></div>
            </dl>
          </article>`;
      }
      const status = statusLabel(run.status, run.skipped);
      return `
        <article class="job-card">
          <div class="job-card-top">
            <h2>${escapeHTML(jobTitle(name))}</h2>
            <span class="status-badge ${statusClass(run.status, run.skipped)}">${escapeHTML(status)}</span>
          </div>
          <dl>
            <div><dt>任务名</dt><dd>${escapeHTML(run.job_name)}</dd></div>
            <div><dt>交易日</dt><dd>${escapeHTML(run.target_trade_date || "--")}</dd></div>
            <div><dt>开始</dt><dd>${escapeHTML(formatTime(run.started_at))}</dd></div>
            <div><dt>完成</dt><dd>${escapeHTML(formatTime(run.finished_at))}</dd></div>
            <div><dt>耗时</dt><dd>${escapeHTML(formatDuration(run.duration_ms))}</dd></div>
            <div><dt>报告摘要</dt><dd>${escapeHTML(reportSummary(run))}</dd></div>
          </dl>
        </article>`;
    }).join("");
  }

  function renderTable(runs) {
    els.count.textContent = `${runs.length} 条`;
    if (!runs.length) {
      els.body.innerHTML = '<tr><td colspan="8">暂无任务运行记录</td></tr>';
      return;
    }
    els.body.innerHTML = runs.map((run, index) => {
      const status = statusLabel(run.status, run.skipped);
      const error = run.error_summary || (Array.isArray(run.report && run.report.errors) ? run.report.errors.join("; ") : "");
      return `
        <tr data-index="${index}">
          <td>${escapeHTML(jobTitle(run.job_name))}<br><code>${escapeHTML(run.run_id || "")}</code></td>
          <td><span class="status-badge ${statusClass(run.status, run.skipped)}">${escapeHTML(status)}</span></td>
          <td>${escapeHTML(run.target_trade_date || "--")}</td>
          <td>${escapeHTML(run.trigger || "--")}</td>
          <td>${escapeHTML(formatTime(run.started_at))}</td>
          <td>${escapeHTML(formatTime(run.finished_at))}</td>
          <td>${escapeHTML(formatDuration(run.duration_ms))}</td>
          <td>${escapeHTML(error || "--")}</td>
        </tr>`;
    }).join("");
    els.body.querySelectorAll("tr[data-index]").forEach((row) => {
      row.addEventListener("click", () => {
        const run = runs[Number(row.getAttribute("data-index"))];
        renderReport(run);
      });
    });
    renderReport(runs[0]);
  }

  function renderReport(run) {
    if (!run) {
      els.reportTitle.textContent = "选择一条记录查看 report_json";
      els.report.textContent = "暂无报告";
      return;
    }
    els.reportTitle.textContent = `${jobTitle(run.job_name)} / ${run.run_id}`;
    els.report.textContent = JSON.stringify(run.report || run, null, 2);
  }

  function renderError(error) {
    els.cards.innerHTML = `<article class="job-card"><div class="job-card-top"><h2>加载失败</h2><span class="status-badge failed">failed</span></div><p>${escapeHTML(error.message)}</p></article>`;
    els.body.innerHTML = `<tr><td colspan="8">${escapeHTML(error.message)}</td></tr>`;
  }

  async function loadJobs() {
    els.refresh.disabled = true;
    try {
      const filter = els.filter.value.trim();
      const suffix = filter ? `?job_name=${encodeURIComponent(filter)}` : "";
      const [status, jobs] = await Promise.all([
        getJSON("/v1/status"),
        getJSON(`/v1/jobs/runs${suffix}`),
      ]);
      const runs = Array.isArray(jobs.runs) ? jobs.runs : [];
      renderOverview(status);
      renderCards(runs);
      renderTable(runs);
    } catch (error) {
      renderError(error);
    } finally {
      els.refresh.disabled = false;
    }
  }

  els.refresh.addEventListener("click", loadJobs);
  els.filter.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      loadJobs();
    }
  });
  loadJobs();
  window.setInterval(loadJobs, 30000);
}());
