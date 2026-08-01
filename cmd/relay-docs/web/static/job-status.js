(function () {
  const els = {
    serviceStatus: document.getElementById("jobsServiceStatus"),
    tradingPhase: document.getElementById("jobsTradingPhase"),
    dependencies: document.getElementById("jobsDependencies"),
    refreshTime: document.getElementById("jobsRefreshTime"),
    planHint: document.getElementById("jobPlanHint"),
    cards: document.getElementById("jobCards"),
    count: document.getElementById("jobCount"),
    body: document.getElementById("jobRunsBody"),
    reportTitle: document.getElementById("jobReportTitle"),
    report: document.getElementById("jobReport"),
    refresh: document.getElementById("refreshJobs"),
    filter: document.getElementById("jobNameFilter"),
    tradeDate: document.getElementById("reviewTradeDate"),
    exportReview: document.getElementById("exportReview"),
    reviewStatus: document.getElementById("reviewStatus"),
    reviewSummary: document.getElementById("reviewSummary"),
    reviewBody: document.getElementById("reviewAccountsBody"),
  };

  let currentReview = null;

  const knownJobs = [
    { name: "pre_open_init", title: "盘前初始化", expectedTime: "09:01", purpose: "刷新账户并写入日初资产" },
    { name: "post_close_settlement", title: "盘后结算", expectedTime: "15:01", purpose: "固化日终快照和对账输入" },
    { name: "performance_daily", title: "每日绩效计算", expectedTime: "17:45", purpose: "逐账户计算成本账和经济净值质量" },
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
      timeZone: "Asia/Shanghai",
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

  function formatNumber(value, digits) {
    const number = Number(value);
    if (!Number.isFinite(number)) return "--";
    return number.toLocaleString("zh-CN", {
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    });
  }

  function reviewStatusLabel(status) {
    return {
      passed: "通过",
      attention: "待复核",
      blocked: "已阻断",
      pending: "等待结算",
      non_trading: "非交易日",
    }[status] || status || "--";
  }

  function reviewStatusClass(status) {
    if (status === "passed") return "succeeded";
    if (status === "attention" || status === "pending") return "running";
    if (status === "blocked") return "failed";
    if (status === "non_trading") return "skipped";
    return "";
  }

  function dependencySummary(dependencies) {
    if (!dependencies || typeof dependencies !== "object") return "--";
    const entries = Object.entries(dependencies).filter(([name]) => name !== "auto_refresh");
    if (!entries.length) return "--";
    const bad = entries.filter(([, item]) => item && item.status && item.status !== "ok");
    const autoRefresh = dependencies.auto_refresh && dependencies.auto_refresh.status;
    const suffix = autoRefresh === "disabled" ? " · 自动刷新关闭" : "";
    return bad.length ? `${bad.length}/${entries.length} 异常${suffix}` : `${entries.length}/${entries.length} ok${suffix}`;
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

  function isNonTradingDay(status) {
    return status && status.trading_day && status.trading_day.is_trading_day === false;
  }

  function tradingDayLabel(status) {
    const tradingDay = status && status.trading_day || {};
    const parts = [tradingDay.date, tradingDay.phase].filter(Boolean);
    if (tradingDay.is_trading_day === true) {
      parts.push("交易日");
    } else if (tradingDay.is_trading_day === false) {
      parts.push("非交易日");
    }
    const previous = normalizeDate(tradingDay.previous_or_current_trading_date);
    const current = normalizeDate(tradingDay.date);
    if (previous && previous !== current) {
      parts.push(`最近交易日 ${previous}`);
    }
    return parts.join(" / ") || "--";
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
      if (run.skipped) return { label: "已跳过", className: "skipped" };
      if (label === "succeeded" || label === "completed" || label === "ok") return { label: "已完成", className: "succeeded" };
      if (label === "running") return { label: "运行中", className: "running" };
      if (label === "failed" || label === "error") return { label: "失败", className: "failed" };
      return { label, className: statusClass(run.status, run.skipped) };
    }
    if (isNonTradingDay(status)) {
      return { label: "非交易日跳过", className: "skipped" };
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

  function currentTradeDate(status) {
    return status && status.trading_day && normalizeDate(status.trading_day.date) || "";
  }

  function sameTradeDate(run, status) {
    return runMatchesTradeDate(run, currentTradeDate(status));
  }

  function compactRunSummary(run) {
    if (!run) return "--";
    const status = statusLabel(run.status, run.skipped);
    const date = run.target_trade_date || "--";
    const start = formatTime(run.started_at);
    return `${date} / ${status} / ${start}`;
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

  function accountErrorCountFromList(accounts) {
    if (!Array.isArray(accounts)) return 0;
    return accounts.reduce((total, account) => total + (Array.isArray(account.errors) && account.errors.length ? 1 : 0), 0);
  }

  function accountErrorCount(run) {
    if (!run || !run.report) return 0;
    const report = run.report;
    const snapshot = snapshotResult(run);
    const snapshotCount = snapshot && Number(snapshot.account_error_count || 0);
    if (snapshotCount) return snapshotCount;
    const reportCount = Number(report.account_error_count || 0);
    if (reportCount) return reportCount;
    return accountErrorCountFromList(report.accounts) || accountErrorCountFromList(snapshot && snapshot.accounts);
  }

  function accountErrorSummary(run) {
    const count = accountErrorCount(run);
    if (!count) return "";
    const reportErrors = run && run.report && Array.isArray(run.report.account_errors) ? run.report.account_errors : [];
    const first = reportErrors.find((item) => item && item.account_id && Array.isArray(item.errors) && item.errors.length);
    if (first) return `账户异常 ${count}: ${first.account_id} ${first.errors[0]}`;
    const snapshot = snapshotResult(run);
    const accounts = snapshot && Array.isArray(snapshot.accounts) ? snapshot.accounts : [];
    const item = accounts.find((account) => account && account.account_id && Array.isArray(account.errors) && account.errors.length);
    if (item) return `账户异常 ${count}: ${item.account_id} ${item.errors[0]}`;
    return `账户异常 ${count}`;
  }

  function alertDelivery(run) {
    const value = run && run.report && run.report.alert_delivery;
    return value && typeof value === "object" ? value : null;
  }

  function alertDeliveryView(run) {
    const delivery = alertDelivery(run);
    if (!delivery) return { label: "--", className: "" };
    const status = delivery.status || "unknown";
    if (status === "delivered") return { label: `已通知${delivery.attempts > 1 ? ` (${delivery.attempts}次)` : ""}`, className: "succeeded" };
    if (status === "not_required") {
      return { label: delivery.configured ? "无需通知 · 通道可用" : "无需通知 · 通道未启用", className: "skipped" };
    }
    if (status === "suppressed") return { label: "演练已抑制", className: "skipped" };
    if (status === "disabled") return { label: "需通知 · 通道未启用", className: "failed" };
    if (status === "misconfigured") return { label: "告警配置不完整", className: "failed" };
    if (status === "failed") return { label: "通知失败", className: "failed" };
    return { label: status, className: "" };
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
      const accountErrors = accountErrorCount(run);
      if (accountErrors) parts.push(`账户异常 ${accountErrors}`);
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
    const accountErrors = accountErrorCount(run);
    if (accountErrors) parts.push(`账户异常 ${accountErrors}`);
    return parts.join(" · ") || "--";
  }

  function renderOverview(status) {
    els.serviceStatus.textContent = status.status || "--";
    els.tradingPhase.textContent = tradingDayLabel(status);
    els.dependencies.textContent = dependencySummary(status.dependencies);
    els.refreshTime.textContent = formatTime(new Date().toISOString());
    if (els.planHint) {
      const tradingDay = status && status.trading_day || {};
      const current = currentTradeDate(status) || "--";
      const previous = normalizeDate(tradingDay.previous_or_current_trading_date);
      if (isNonTradingDay(status)) {
        els.planHint.textContent = `当前日期 ${current} 非交易日 · 交易日任务跳过 · 最近交易日 ${previous || "--"}`;
      } else {
        els.planHint.textContent = `当前交易日 ${current} · ${status.timezone || "Asia/Shanghai"}`;
      }
    }
  }

  function renderCards(statusView, runs) {
    const byName = new Map(runs.map((run) => [run.job_name, run]));
    const names = [...knownJobs.map((item) => item.name), ...runs.map((run) => run.job_name)]
      .filter((name, index, array) => name && array.indexOf(name) === index);
    els.cards.innerHTML = names.map((name) => {
      const latestRun = byName.get(name);
      const todayRun = sameTradeDate(latestRun, statusView) ? latestRun : null;
      const schedule = jobSchedule(statusView, name);
      const state = dailyState(todayRun, schedule, statusView);
      const status = todayRun ? statusLabel(todayRun.status, todayRun.skipped) : "";
      return `
        <article class="job-card">
          <div class="job-card-top">
            <h2>${escapeHTML(jobTitle(name))}</h2>
            <span class="status-badge ${escapeHTML(state.className || statusClass(todayRun && todayRun.status, todayRun && todayRun.skipped))}">${escapeHTML(state.label || status)}</span>
          </div>
          <p class="job-purpose">${escapeHTML(schedule.purpose || "--")}</p>
          <dl>
            <div><dt>计划时间</dt><dd>${escapeHTML(expectedLabel(schedule))}</dd></div>
            <div><dt>cron</dt><dd>${escapeHTML(schedule.schedule || "--")}</dd></div>
            <div><dt>所选日期</dt><dd>${escapeHTML(currentTradeDate(statusView) || "--")}</dd></div>
            <div><dt>本次运行</dt><dd>${escapeHTML(todayRun ? compactRunSummary(todayRun) : "--")}</dd></div>
            <div class="wide"><dt>上次运行记录</dt><dd>${escapeHTML(compactRunSummary(latestRun))}</dd></div>
            <div class="wide"><dt>运行结果</dt><dd>${escapeHTML(todayRun ? finalResult(todayRun) : "--")}</dd></div>
            <div class="wide"><dt>告警通知</dt><dd>${escapeHTML(todayRun ? alertDeliveryView(todayRun).label : "--")}</dd></div>
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
      const alert = alertDeliveryView(run);
      const error = run.error_summary || accountErrorSummary(run) || (Array.isArray(run.report && run.report.errors) ? run.report.errors.join("; ") : "");
      return `
        <tr data-index="${index}">
          <td>${escapeHTML(jobTitle(run.job_name))}<br><code>${escapeHTML(run.run_id || "")}</code></td>
          <td><span class="status-badge ${statusClass(run.status, run.skipped)}">${escapeHTML(status)}</span></td>
          <td>${escapeHTML(run.target_trade_date || "--")}</td>
          <td>${escapeHTML(run.trigger || "--")}</td>
          <td>${escapeHTML(formatTime(run.started_at))}</td>
          <td>${escapeHTML(formatTime(run.finished_at))}</td>
          <td>${escapeHTML(formatDuration(run.duration_ms))}</td>
          <td class="job-result-cell">${escapeHTML(finalResult(run))}</td>
          <td><span class="status-badge ${escapeHTML(alert.className)}">${escapeHTML(alert.label)}</span></td>
          <td class="job-error-cell">${escapeHTML(error || "--")}</td>
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

  function renderReview(report) {
    currentReview = report || null;
    const summary = report && report.summary || {};
    els.reviewStatus.innerHTML = report
      ? `<span class="status-badge ${escapeHTML(reviewStatusClass(report.status))}">${escapeHTML(reviewStatusLabel(report.status))}</span> · ${escapeHTML(report.trade_date || "--")}`
      : "--";
    const metrics = [
      ["账户", summary.reviewed_accounts, summary.configured_accounts],
      ["通过", summary.passed_accounts],
      ["待复核", summary.attention_accounts],
      ["阻断", summary.blocked_accounts],
      ["未完成", summary.pending_accounts],
      ["开放差异", summary.open_breaks],
    ];
    els.reviewSummary.innerHTML = metrics.map(([label, value, total]) => `
      <div><span>${escapeHTML(label)}</span><strong>${escapeHTML(value == null ? "0" : value)}${total == null ? "" : ` / ${escapeHTML(total)}`}</strong></div>
    `).join("");
    const accounts = report && Array.isArray(report.accounts) ? report.accounts : [];
    if (!accounts.length) {
      els.reviewBody.innerHTML = '<tr><td colspan="10">暂无账户复核记录</td></tr>';
      return;
    }
    els.reviewBody.innerHTML = accounts.map((account, index) => {
      const openAsset = account.open && account.open.asset || {};
      const closeAsset = account.close && account.close.asset || {};
      const close = account.close || {};
      const issues = Array.isArray(account.issues) ? account.issues : [];
      const openBreaks = (Array.isArray(account.breaks) ? account.breaks : []).filter((item) => item.status === "open").length;
      const issueText = issues.slice(0, 2).map((item) => item.message || item.code).filter(Boolean).join("; ");
      return `
        <tr data-review-index="${index}">
          <td><strong>${escapeHTML(account.alias || account.account_id)}</strong><br><code>${escapeHTML(account.account_id)}</code></td>
          <td><span class="status-badge ${escapeHTML(reviewStatusClass(account.status))}">${escapeHTML(reviewStatusLabel(account.status))}</span></td>
          <td>${escapeHTML(formatNumber(openAsset.net_asset, 2))}</td>
          <td>${escapeHTML(formatNumber(closeAsset.net_asset, 2))}</td>
          <td>${escapeHTML(formatNumber(close.positions_count, 0))}</td>
          <td>${escapeHTML(formatNumber(close.orders_count, 0))}</td>
          <td>${escapeHTML(formatNumber(close.fills_count, 0))}</td>
          <td>${escapeHTML(formatNumber(close.non_terminal_orders, 0))}</td>
          <td>${escapeHTML(formatNumber(openBreaks, 0))}</td>
          <td title="${escapeHTML(issueText)}">${escapeHTML(issueText || "--")}</td>
        </tr>`;
    }).join("");
    els.reviewBody.querySelectorAll("tr[data-review-index]").forEach((row) => {
      row.addEventListener("click", () => {
        const account = accounts[Number(row.getAttribute("data-review-index"))];
        els.reportTitle.textContent = `账户复核 / ${account.account_id}`;
        els.report.textContent = JSON.stringify(account, null, 2);
      });
    });
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
    els.reviewBody.innerHTML = `<tr><td colspan="10">${escapeHTML(error.message)}</td></tr>`;
  }

  async function loadJobs() {
    els.refresh.disabled = true;
    try {
      const status = await getJSON("/v1/status");
      const tradingDay = status && status.trading_day || {};
      if (!els.tradeDate.value) {
        const fallback = tradingDay.is_trading_day === false
          ? normalizeDate(tradingDay.previous_or_current_trading_date)
          : normalizeDate(tradingDay.date);
        els.tradeDate.value = fallback || "";
      }
      const filter = els.filter.value.trim();
      const params = new URLSearchParams();
      if (filter) params.set("job_name", filter);
      if (els.tradeDate.value) params.set("trade_date", els.tradeDate.value);
      params.set("limit", "20");
      const reviewParams = new URLSearchParams();
      if (els.tradeDate.value) reviewParams.set("trade_date", els.tradeDate.value);
      const [jobs, review] = await Promise.all([
        getJSON(`/v1/jobs/runs?${params.toString()}`),
        getJSON(`/v1/reconciliations/review-report?${reviewParams.toString()}`),
      ]);
      const runs = Array.isArray(jobs.runs) ? jobs.runs : [];
      const scopedStatus = {
        ...status,
        trading_day: { ...(status.trading_day || {}), date: els.tradeDate.value || (status.trading_day || {}).date, is_trading_day: true },
      };
      renderOverview(status);
      renderCards(scopedStatus, runs);
      renderTable(status, runs);
      renderReview(review);
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
  els.tradeDate.addEventListener("change", loadJobs);
  els.exportReview.addEventListener("click", () => {
    if (!currentReview) return;
    const blob = new Blob([`${JSON.stringify(currentReview, null, 2)}\n`], { type: "application/json;charset=utf-8" });
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = `relay-daily-review-${currentReview.trade_date || "report"}.json`;
    link.click();
    URL.revokeObjectURL(link.href);
  });
  loadJobs();
  window.setInterval(loadJobs, 30000);
}());
