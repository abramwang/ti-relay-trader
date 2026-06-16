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
    { name: "pre_open_init", title: "盘前初始化", expectedTime: "09:01", purpose: "刷新账户并写入日初资产" },
    { name: "post_close_settlement", title: "盘后结算", expectedTime: "15:30", purpose: "固化日终快照和对账输入" },
  ];
  const expectedRunGraceMinutes = 5;

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

  function knownJob(name) {
    return knownJobs.find((item) => item.name === name) || { name, title: name, expectedTime: "", purpose: "" };
  }

  function jobSchedule(status, name) {
    const configured = status && status.jobs && status.jobs[name] ? status.jobs[name] : {};
    const known = knownJob(name);
    const enabled = configured.enabled !== undefined ? configured.enabled : true;
    return {
      name,
      enabled,
      schedule: configured.schedule || "",
      expectedTime: configured.expected_time || known.expectedTime || "",
      timezone: configured.timezone || (status && status.timezone) || "Asia/Shanghai",
      purpose: known.purpose || "",
    };
  }

  function expectedLabel(schedule) {
    if (!schedule.enabled) return "未启用";
    const time = schedule.expectedTime || "";
    if (time) return `交易日 ${time} ${schedule.timezone || ""}`.trim();
    return schedule.schedule ? `${schedule.schedule} ${schedule.timezone || ""}`.trim() : "--";
  }

  function normalizeDate(value) {
    const digits = String(value || "").replace(/\D/g, "");
    if (digits.length !== 8) return String(value || "");
    return `${digits.slice(0, 4)}-${digits.slice(4, 6)}-${digits.slice(6, 8)}`;
  }

  function minutesOfDay(value) {
    const match = String(value || "").match(/^(\d{1,2}):(\d{2})$/);
    if (!match) return null;
    const hour = Number(match[1]);
    const minute = Number(match[2]);
    if (!Number.isFinite(hour) || !Number.isFinite(minute)) return null;
    return hour * 60 + minute;
  }

  function currentMinutes(timezone) {
    try {
      const parts = new Intl.DateTimeFormat("en-GB", {
        timeZone: timezone || "Asia/Shanghai",
        hour: "2-digit",
        minute: "2-digit",
        hourCycle: "h23",
      }).formatToParts(new Date());
      const hour = Number(parts.find((part) => part.type === "hour")?.value);
      const minute = Number(parts.find((part) => part.type === "minute")?.value);
      if (Number.isFinite(hour) && Number.isFinite(minute)) return hour * 60 + minute;
    } catch (_error) {
      return null;
    }
    return null;
  }

  function runMatchesTradeDate(run, tradeDate) {
    if (!run || !tradeDate) return false;
    return normalizeDate(run.target_trade_date) === normalizeDate(tradeDate);
  }

  function dailyState(run, schedule, status) {
    if (!schedule.enabled) return { label: "未启用", className: "skipped" };
    const tradeDate = status && status.trading_day && status.trading_day.date;
    if (runMatchesTradeDate(run, tradeDate)) {
      const label = statusLabel(run.status, run.skipped);
      if (run.skipped) return { label: "今日跳过", className: "skipped" };
      if (label === "succeeded" || label === "completed" || label === "ok") return { label: "今日完成", className: "succeeded" };
      if (label === "running") return { label: "运行中", className: "running" };
      if (label === "failed" || label === "error") return { label: "今日失败", className: "failed" };
      return { label, className: statusClass(run.status, run.skipped) };
    }
    const expected = minutesOfDay(schedule.expectedTime);
    const now = currentMinutes(schedule.timezone);
    if (expected != null && now != null && now < expected) {
      return { label: "等待运行", className: "running" };
    }
    if (expected != null && now != null && now < expected + expectedRunGraceMinutes) {
      return { label: "等待回写", className: "running" };
    }
    return { label: "今日未完成", className: "failed" };
  }

  function snapshotResult(run) {
    const report = run && run.report;
    if (!report || typeof report !== "object") return null;
    const wrapper = report.settlement_snapshot || report.open_snapshot;
    return wrapper && wrapper.result ? wrapper.result : null;
  }

  function sumAccountSnapshot(run, field) {
    const accounts = run && run.report && Array.isArray(run.report.accounts) ? run.report.accounts : [];
    return accounts.reduce((total, account) => total + Number(account.snapshot && account.snapshot[field] || 0), 0);
  }

  function finalResult(run) {
    if (!run) return "--";
    const report = run.report || {};
    if (report.skipped) return report.skip_reason || "skipped";
    const snapshot = snapshotResult(run);
    const parts = [];
    if (snapshot) {
      if (Array.isArray(snapshot.accounts)) parts.push(`账户 ${snapshot.accounts.length}`);
      if (snapshot.asset_snapshots !== undefined) parts.push(`资产快照 ${snapshot.asset_snapshots}`);
      if (snapshot.position_snapshots !== undefined) parts.push(`持仓快照 ${snapshot.position_snapshots}`);
      if (snapshot.orders_count !== undefined) parts.push(`订单 ${snapshot.orders_count}`);
      if (snapshot.fills_count !== undefined) parts.push(`成交 ${snapshot.fills_count}`);
      if (snapshot.non_terminal_orders !== undefined) parts.push(`未终态 ${snapshot.non_terminal_orders}`);
      if (snapshot.reconciliation_breaks !== undefined) parts.push(`差异 ${snapshot.reconciliation_breaks}`);
      return parts.join(" · ") || snapshot.status || "--";
    }
    const accounts = Array.isArray(report.accounts) ? report.accounts.length : 0;
    if (accounts) parts.push(`账户 ${accounts}`);
    const orders = sumAccountSnapshot(run, "orders_count");
    const fills = sumAccountSnapshot(run, "fills_count");
    const positions = sumAccountSnapshot(run, "positions_count");
    const nonTerminal = sumAccountSnapshot(run, "non_terminal_orders");
    if (orders) parts.push(`订单 ${orders}`);
    if (fills) parts.push(`成交 ${fills}`);
    if (positions) parts.push(`持仓 ${positions}`);
    if (nonTerminal) parts.push(`未终态 ${nonTerminal}`);
    return parts.join(" · ") || "--";
  }

  function renderOverview(status) {
    els.serviceStatus.textContent = status.status || "--";
    const tradingDay = status.trading_day || {};
    els.tradingPhase.textContent = [tradingDay.date, tradingDay.phase].filter(Boolean).join(" / ") || "--";
    els.dependencies.textContent = dependencySummary(status.dependencies);
    els.refreshTime.textContent = formatTime(new Date().toISOString());
  }

  function renderCards(statusView, runs) {
    const byName = new Map(runs.map((run) => [run.job_name, run]));
    const names = [...knownJobs.map((item) => item.name), ...runs.map((run) => run.job_name)]
      .filter((name, index, array) => name && array.indexOf(name) === index);
    els.cards.innerHTML = names.map((name) => {
      const run = byName.get(name);
      const schedule = jobSchedule(statusView, name);
      const state = dailyState(run, schedule, statusView);
      if (!run) {
        return `
          <article class="job-card">
            <div class="job-card-top">
              <h2>${escapeHTML(jobTitle(name))}</h2>
              <span class="status-badge ${escapeHTML(state.className)}">${escapeHTML(state.label)}</span>
            </div>
            <p class="job-purpose">${escapeHTML(schedule.purpose || "--")}</p>
            <dl>
              <div><dt>预期运行</dt><dd>${escapeHTML(expectedLabel(schedule))}</dd></div>
              <div><dt>cron</dt><dd>${escapeHTML(schedule.schedule || "--")}</dd></div>
              <div><dt>目标交易日</dt><dd>${escapeHTML(statusView.trading_day && statusView.trading_day.date || "--")}</dd></div>
              <div><dt>最近运行</dt><dd>--</dd></div>
            </dl>
          </article>`;
      }
      const status = statusLabel(run.status, run.skipped);
      return `
        <article class="job-card">
          <div class="job-card-top">
            <h2>${escapeHTML(jobTitle(name))}</h2>
            <span class="status-badge ${escapeHTML(state.className || statusClass(run.status, run.skipped))}">${escapeHTML(state.label || status)}</span>
          </div>
          <p class="job-purpose">${escapeHTML(schedule.purpose || "--")}</p>
          <dl>
            <div><dt>预期运行</dt><dd>${escapeHTML(expectedLabel(schedule))}</dd></div>
            <div><dt>交易日</dt><dd>${escapeHTML(run.target_trade_date || "--")}</dd></div>
            <div><dt>开始</dt><dd>${escapeHTML(formatTime(run.started_at))}</dd></div>
            <div><dt>完成</dt><dd>${escapeHTML(formatTime(run.finished_at))}</dd></div>
            <div><dt>耗时</dt><dd>${escapeHTML(formatDuration(run.duration_ms))}</dd></div>
            <div><dt>运行状态</dt><dd>${escapeHTML(status)}</dd></div>
            <div class="wide"><dt>最终结果</dt><dd>${escapeHTML(finalResult(run))}</dd></div>
          </dl>
        </article>`;
    }).join("");
  }

  function renderTable(statusView, runs) {
    els.count.textContent = `${runs.length} 条`;
    if (!runs.length) {
      els.body.innerHTML = '<tr><td colspan="10">暂无任务运行记录</td></tr>';
      return;
    }
    els.body.innerHTML = runs.map((run, index) => {
      const status = statusLabel(run.status, run.skipped);
      const error = run.error_summary || (Array.isArray(run.report && run.report.errors) ? run.report.errors.join("; ") : "");
      const schedule = jobSchedule(statusView, run.job_name);
      return `
        <tr data-index="${index}">
          <td>${escapeHTML(jobTitle(run.job_name))}<br><code>${escapeHTML(run.run_id || "")}</code></td>
          <td>${escapeHTML(expectedLabel(schedule))}</td>
          <td><span class="status-badge ${statusClass(run.status, run.skipped)}">${escapeHTML(status)}</span></td>
          <td>${escapeHTML(run.target_trade_date || "--")}</td>
          <td>${escapeHTML(run.trigger || "--")}</td>
          <td>${escapeHTML(formatTime(run.started_at))}</td>
          <td>${escapeHTML(formatTime(run.finished_at))}</td>
          <td>${escapeHTML(formatDuration(run.duration_ms))}</td>
          <td>${escapeHTML(finalResult(run))}</td>
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
    els.body.innerHTML = `<tr><td colspan="10">${escapeHTML(error.message)}</td></tr>`;
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
      renderCards(status, runs);
      renderTable(status, runs);
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
