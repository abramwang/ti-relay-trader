(() => {
  "use strict";
  const el = (id) => document.getElementById(`research${id}`);
  if (!el("Form")) return;
  const basis = { core_cost: "普通底仓成本", redemption_cost_release_not_loss: "申赎成本转出", t0_residual_cost: "T0 余仓成本", component_marked_basis_not_acquisition_cost: "成分股估值基数" };
  const outcome = { equal: "完全一致", changed: "数值 / 口径变化", metadata_only: "仅元数据变化", research_only: "仅研究记录", stored_only: "仅正式记录" };
  const state = { account: "", active: false, seq: 0, controller: null, result: null, rows: [], page: 1, sort: "trade_date", direction: 1 };
  const number = (v) => v == null ? "--" : Number(v).toLocaleString("zh-CN", { maximumFractionDigits: 6 });
  const micros = (v) => Number.isSafeInteger(v) ? v : null;
  const node = (tag, text, cls) => { const n = document.createElement(tag); if (text != null) n.textContent = text; if (cls) n.className = cls; return n; };
  const option = (value, label) => { const n = node("option", label); n.value = value; return n; };
  const status = (text, error = false) => { el("Status").textContent = text; el("Status").dataset.state = error ? "error" : ""; };
  const dialog = node("dialog", null, "research-detail");
  document.body.append(dialog);
  function cancel() { state.seq++; state.controller?.abort(); state.controller = null; }
  function clear() {
    state.result = null; state.rows = []; state.page = 1;
    el("Summary").replaceChildren(); el("Evidence").replaceChildren();
    el("Date").replaceChildren(option("", "全部日期"));
    ["Outcome", "Basis", "Search"].forEach((key) => { el(key).value = ""; });
    dialog.close(); render();
  }
  async function get(path, signal) {
    const response = await fetch(path, { signal, cache: "no-store", headers: { Accept: "application/json" } });
    let body;
    try { body = await response.json(); } catch (_) { throw new Error(`响应不是有效 JSON（HTTP ${response.status}）`); }
    if (!response.ok || body.ok === false) throw new Error(body.error?.message || `HTTP ${response.status}`);
    return body.data ?? body;
  }
  function endpoint(suffix) { return `/v1/accounts/${encodeURIComponent(state.account)}/performance/${suffix}`; }
  async function versions() {
    cancel(); clear();
    el("Version").disabled = true; el("Load").disabled = true;
    el("Version").replaceChildren(option("", "正在读取版本"));
    if (!state.active || !state.account) { status("等待读取账户"); return; }
    const seq = state.seq, account = state.account;
    state.controller = new AbortController(); status("正在读取研究版本…");
    try {
      const data = await get(endpoint("research-cost-versions"), state.controller.signal);
      if (seq !== state.seq) return;
      if (data.account_id !== account || data.research_only !== true || !Array.isArray(data.versions)) throw new Error("版本账户校验失败");
      el("Version").replaceChildren(option("", data.versions.length ? "请选择研究版本" : "该账户暂无研究版本"));
      data.versions.forEach((v) => el("Version").append(option(v.version_id, `${v.version_id} · ${v.date_from} 至 ${v.date_to}`)));
      el("Version").disabled = data.versions.length === 0;
      status(data.versions.length ? "尚未选择版本" : "该账户暂无研究版本");
    } catch (error) {
      if (seq !== state.seq || error.name === "AbortError") return;
      el("Version").replaceChildren(option("", "版本读取失败"));
      status(`版本读取失败：${error.message}`, true);
    }
  }
  window.addEventListener("relay:research-context", ({ detail }) => {
    el("Account").textContent = detail.label || detail.accountID || "--";
    if (state.account === detail.accountID && state.active === detail.active) return;
    state.account = detail.accountID; state.active = detail.active;
    if (state.active) versions(); else { cancel(); clear(); }
  });
  const retry = node("button", "刷新版本"); retry.type = "button"; retry.id = "researchRefresh";
  el("Form").append(retry); retry.addEventListener("click", versions);
  el("Version").addEventListener("change", () => {
    cancel(); clear(); el("Load").disabled = !el("Version").value;
    el("Version").title = el("Version").selectedOptions[0]?.textContent || "";
    status(el("Version").value ? "待查询" : "尚未选择版本");
  });
  el("Form").addEventListener("submit", async (event) => {
    event.preventDefault(); const version = el("Version").value;
    if (!version || !state.active || !state.account) return;
    cancel(); clear(); const seq = state.seq, account = state.account;
    state.controller = new AbortController(); el("Load").disabled = true; status("正在对照研究成本与正式记录…");
    try {
      const data = await get(`${endpoint("research-cost-comparison")}?${new URLSearchParams({ version_id: version })}`, state.controller.signal);
      if (seq !== state.seq) return;
      const result = data.comparison;
      if (!result || result.account_id !== account || result.version_id !== version || result.research_only !== true || result.economic_nav_publishable !== false || !Array.isArray(result.days)) throw new Error("对照账户或版本校验失败");
      state.result = result;
      state.rows = result.days.flatMap((day) => day.rows.map((row) => ({ ...row, trade_date: day.trade_date })));
      result.days.forEach((day) => el("Date").append(option(day.trade_date, day.trade_date)));
      const counts = result.counts;
      const metrics = [["覆盖交易日", result.days.length], ["研究记录", state.rows.filter((r) => r.research).length], ["正式记录", state.rows.filter((r) => r.stored).length], ["数值 / 口径变化", counts.changed], ["仅研究记录", counts.research_only]];
      metrics.forEach(([label, value]) => { const item = node("div"); item.append(node("span", label), node("strong", number(value))); el("Summary").append(item); });
      el("Evidence").dataset.state = result.anchor_and_allocations_match ? "matched" : "stale";
      el("Evidence").append(node("div", result.anchor_and_allocations_match ? "期初成本与分配审计一致 · 未核验其他源事实的后续变化" : "期初成本或分配审计已变化 · 研究版本需复核"));
      const changes = node("ul");
      (result.evidence_changes || []).forEach((c) => changes.append(node("li", `${c.scope} · ${c.identity} · ${c.kind} · ${(c.changed_fields || []).join(", ")}`)));
      if (changes.childNodes.length) el("Evidence").append(changes);
      status(`${result.days[0]?.trade_date || "--"} 至 ${result.days.at(-1)?.trade_date || "--"} · SHA256 ${result.payload_sha256}`);
      render();
    } catch (error) {
      if (seq !== state.seq || error.name === "AbortError") return;
      clear(); status(`对照读取失败：${error.message}`, true);
    } finally { if (seq === state.seq) el("Load").disabled = !el("Version").value; }
  });
  function filtered() {
    const search = el("Search").value.trim().toLowerCase();
    const value = (r) => state.sort === "quantity" ? (r.research || r.stored)?.close_quantity : state.sort === "cost" ? r.research?.close_total_cost : r[state.sort];
    return state.rows.filter((r) => (!el("Date").value || r.trade_date === el("Date").value) && (!el("Outcome").value || r.outcome === el("Outcome").value) && (!el("Basis").value || r.basis_kind === el("Basis").value) && (!search || `${r.security_id} ${r.cost_bucket}`.toLowerCase().includes(search))).sort((a, b) => {
      const x = value(a), y = value(b);
      if (x == null || y == null) return x == null ? (y == null ? 0 : 1) : -1;
      return (typeof x === "number" ? x - y : String(x).localeCompare(String(y))) * state.direction;
    });
  }
  function render() {
    const rows = filtered(), size = Number(el("PageSize").value), pages = Math.ceil(rows.length / size);
    state.page = Math.max(1, Math.min(state.page, pages));
    el("Rows").replaceChildren();
    for (const row of rows.slice((state.page - 1) * size, state.page * size)) {
      const tr = node("tr");
      const delta = micros(row.research_minus_stored?.close_cost_micros);
      const values = [row.trade_date, row.security_id, basis[row.basis_kind] || row.basis_kind, outcome[row.outcome] || row.outcome, number((row.research || row.stored)?.close_quantity), number(row.research?.close_total_cost), number(row.stored?.close_total_cost), number(delta == null ? null : delta / 1e6)];
      values.forEach((v, i) => tr.append(node("td", v, i >= 4 ? "numeric" : i === 3 ? `research-outcome ${row.outcome}` : "")));
      const cell = node("td"), button = node("button", "详情"); button.type = "button";
      button.title = `${row.security_id} ${row.trade_date} ${row.cost_bucket}`;
      button.addEventListener("click", () => detail(row)); cell.append(button); tr.append(cell); el("Rows").append(tr);
    }
    if (!rows.length) { const tr = node("tr"), td = node("td", state.result ? "没有符合条件的记录" : "尚未加载研究对照", "research-empty"); td.colSpan = 9; tr.append(td); el("Rows").append(tr); }
    el("Count").textContent = `${rows.length.toLocaleString("zh-CN")} / ${state.rows.length.toLocaleString("zh-CN")} 条`;
    el("Page").textContent = `${pages ? state.page : 0} / ${pages}`;
    el("Prev").disabled = state.page <= 1; el("Next").disabled = state.page >= pages;
    el("Export").disabled = !rows.length; el("Export").title = `导出当前筛选的全部 ${rows.length} 条记录`;
    const movements = (state.result?.days || []).filter((d) => !el("Date").value || d.trade_date === el("Date").value).flatMap((d) => [...(d.basis_movements || []).map((m) => ({ ...m, group: "成本转移" })), ...(d.rounding_differences || []).map((m) => ({ ...m, group: "舍入差异" }))]).filter((m) => !el("Search").value.trim() || `${m.security_id} ${m.cost_bucket}`.toLowerCase().includes(el("Search").value.trim().toLowerCase()));
    el("MovementCount").textContent = `成本转移与舍入 · ${movements.length} 条（日期 / 证券筛选）`;
    el("Movements").replaceChildren(...movements.map((m) => node("p", `${m.trade_date} · ${m.security_id} · ${m.group} · ${m.kind} · 数量 ${number(m.quantity)} · 基数 ${number(micros(m.basis_micros) == null ? null : m.basis_micros / 1e6)} 元 · ${m.cost_bucket}`)));
  }
  function detail(row) {
    const title = node("h3", `${row.trade_date} · ${row.security_id} · ${row.cost_bucket}`); title.id = "researchDetailTitle";
    const close = node("button", "×"); close.type = "button"; close.title = "关闭"; close.setAttribute("aria-label", "关闭"); close.onclick = () => dialog.close();
    const header = node("header"); header.append(title, close);
    const table = node("table"), head = node("tr"); ["字段", "研究版本", "正式记录"].forEach((v) => head.append(node("th", v))); table.append(head);
    const fields = [...new Set([...Object.keys(row.research || {}), ...Object.keys(row.stored || {})])];
    fields.forEach((field) => {
      const tr = node("tr", null, (row.changed_fields || []).concat(row.metadata_fields || []).includes(field) ? "changed" : "");
      const display = (record) => record?.[field] == null ? "--" : typeof record[field] === "object" ? JSON.stringify(record[field]) : typeof record[field] === "number" ? number(record[field]) : String(record[field]);
      [field, display(row.research), display(row.stored)].forEach((v) => tr.append(node("td", v))); table.append(tr);
    });
    dialog.replaceChildren(header, node("p", `${basis[row.basis_kind] || row.basis_kind} · ${outcome[row.outcome]}${row.delta_unavailable_reason ? ` · 差额不可比较：${row.delta_unavailable_reason}` : ""}`), table);
    dialog.setAttribute("aria-labelledby", title.id); dialog.showModal();
  }
  ["Date", "Outcome", "Basis", "PageSize"].forEach((key) => el(key).addEventListener("change", () => { state.page = 1; render(); }));
  el("Search").addEventListener("input", () => { state.page = 1; render(); });
  el("Prev").addEventListener("click", () => { state.page--; render(); });
  el("Next").addEventListener("click", () => { state.page++; render(); });
  document.querySelectorAll("[data-research-sort]").forEach((button) => button.addEventListener("click", () => {
    state.direction = state.sort === button.dataset.researchSort ? -state.direction : 1; state.sort = button.dataset.researchSort; state.page = 1;
    document.querySelectorAll("[data-research-sort]").forEach((b) => b.parentElement.removeAttribute("aria-sort"));
    button.parentElement.setAttribute("aria-sort", state.direction === 1 ? "ascending" : "descending"); render();
  }));
  el("Export").addEventListener("click", () => {
    if (!state.result) return;
    const csvCell = (v) => { const text = v == null ? "" : String(v); return `"${(typeof v === "string" && /^[\s]*[=+\-@]/.test(text) ? `'${text}` : text).replaceAll('"', '""')}"`; };
    const lines = [["账户", "研究版本", "SHA256", "研究用途", "期初与分配审计一致", "交易日", "证券代码", "成本桶", "成本类别", "对照状态", "研究期末数量", "正式期末数量", "研究期末成本", "正式期末成本", "研究减正式成本微元", "差额不可比原因"]];
    filtered().forEach((r) => lines.push([state.account, state.result.version_id, state.result.payload_sha256, true, state.result.anchor_and_allocations_match, r.trade_date, r.security_id, r.cost_bucket, r.basis_kind, r.outcome, r.research?.close_quantity, r.stored?.close_quantity, r.research?.close_total_cost, r.stored?.close_total_cost, micros(r.research_minus_stored?.close_cost_micros), r.delta_unavailable_reason || (r.research_minus_stored && micros(r.research_minus_stored.close_cost_micros) == null ? "browser_integer_precision_limit" : "")]));
    const url = URL.createObjectURL(new Blob(["\ufeff", lines.map((line) => line.map(csvCell).join(",")).join("\r\n")], { type: "text/csv;charset=utf-8" }));
    const link = node("a"); link.href = url; link.download = `research-cost-${state.account}-${state.result.version_id.replace(/[^a-zA-Z0-9_-]/g, "_")}.csv`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
})();
