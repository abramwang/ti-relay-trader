(() => {
  "use strict";

  const state = {
    snapshot: null,
    accountRoutes: [],
    accountID: "",
    streamRole: "",
    dlqStatus: "",
    dlqPage: 1,
    dlqPageSize: 20,
    dlqTotal: 0,
    deadLetters: [],
    selectedDeadLetter: null,
    refreshTimer: null,
    toastTimer: null,
    credentialToken: sessionStorage.getItem("relayCredentialAdminToken") || "",
    credentialUnlocked: false,
  };

  const elements = {
    accountFilter: document.getElementById("operationsAccountFilter"),
    refresh: document.getElementById("refreshOperations"),
    monitoringWindow: document.getElementById("monitoringWindow"),
    monitoringReason: document.getElementById("monitoringReason"),
    gatewayOnline: document.getElementById("gatewayOnline"),
    gatewayAttention: document.getElementById("gatewayAttention"),
    streamLag: document.getElementById("streamLag"),
    streamAttention: document.getElementById("streamAttention"),
    pendingDLQ: document.getElementById("pendingDLQ"),
    updatedAt: document.getElementById("operationsUpdatedAt"),
    gatewayCount: document.getElementById("gatewayCount"),
    gatewayBody: document.getElementById("gatewayBody"),
    streamBody: document.getElementById("streamBody"),
    dlqStatusFilter: document.getElementById("dlqStatusFilter"),
    dlqWriteMode: document.getElementById("dlqWriteMode"),
    refreshDLQ: document.getElementById("refreshDLQ"),
    dlqBody: document.getElementById("dlqBody"),
    dlqPageText: document.getElementById("dlqPageText"),
    dlqPrevious: document.getElementById("dlqPrevious"),
    dlqNext: document.getElementById("dlqNext"),
    dlqDetail: document.getElementById("dlqDetail"),
    toast: document.getElementById("operationsToast"),
    credentialPanel: document.getElementById("credentialAdminPanel"),
    credentialAdminState: document.getElementById("credentialAdminState"),
    credentialEnvironment: document.getElementById("credentialEnvironment"),
    credentialManagedAccount: document.getElementById("credentialManagedAccount"),
    credentialVersion: document.getElementById("credentialVersion"),
    credentialKeyID: document.getElementById("credentialKeyID"),
    credentialVerify: document.getElementById("credentialVerify"),
    credentialIssuedAt: document.getElementById("credentialIssuedAt"),
    credentialSource: document.getElementById("credentialSource"),
    credentialEnvelopeSHA: document.getElementById("credentialEnvelopeSHA"),
    credentialForm: document.getElementById("credentialForm"),
    credentialAdminToken: document.getElementById("credentialAdminToken"),
    credentialUnlock: document.getElementById("credentialUnlock"),
    credentialAccount: document.getElementById("credentialAccount"),
    credentialOperator: document.getElementById("credentialOperator"),
    credentialLoginUser: document.getElementById("credentialLoginUser"),
    credentialPassword: document.getElementById("credentialPassword"),
    credentialDynamicPassword: document.getElementById("credentialDynamicPassword"),
    credentialConfirmAccount: document.getElementById("credentialConfirmAccount"),
    credentialRotate: document.getElementById("credentialRotate"),
    credentialDisable: document.getElementById("credentialDisable"),
  };

  const statusLabels = {
    online: "在线",
    off_hours: "非监控时段",
    broker_not_ready: "柜台未就绪",
    credential_not_ready: "凭据未就绪",
    credential_identity_mismatch: "凭据账户错配",
    credential_metadata_invalid: "凭据元数据无效",
    reconnecting: "重连中",
    stale: "心跳超时",
    degraded: "状态异常",
    missing: "无心跳",
    ok: "正常",
    warning: "Lag 警告",
    critical: "Lag 严重",
    error: "消费错误",
    idle: "暂无数据",
    unknown: "未知",
    pending: "待处理",
    acknowledged: "已确认",
    ignored: "已忽略",
    replayed: "已重放",
  };

  const monitoringLabels = {
    trading_session: "交易时段",
    off_hours: "非监控时段",
    non_trading_day: "非交易日",
    calendar_unavailable: "交易日历不可用",
  };

  async function getJSON(url, options = {}) {
    const { headers = {}, ...fetchOptions } = options;
    const response = await fetch(url, {
      cache: "no-store",
      ...fetchOptions,
      headers: { Accept: "application/json", ...headers },
    });
    const text = await response.text();
    let payload;
    try {
      payload = text ? JSON.parse(text) : {};
    } catch (_error) {
      throw new Error(`服务返回了非 JSON 内容（HTTP ${response.status}）`);
    }
    if (!response.ok || payload.ok === false) {
      throw new Error(payload.error?.message || `请求失败（HTTP ${response.status}）`);
    }
    return payload.data ?? payload;
  }

  function escapeHTML(value) {
    return String(value ?? "")
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#039;");
  }

  function formatNumber(value) {
    const numeric = Number(value);
    return Number.isFinite(numeric) ? numeric.toLocaleString("zh-CN") : "--";
  }

  function formatTime(value, withDate = false) {
    if (!value) return "--";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "--";
    return new Intl.DateTimeFormat("zh-CN", {
      timeZone: "Asia/Shanghai",
      month: withDate ? "2-digit" : undefined,
      day: withDate ? "2-digit" : undefined,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).format(date);
  }

  function statusBadge(status) {
    const normalized = status || "unknown";
    return `<span class="status-badge status-${escapeHTML(normalized)}">${escapeHTML(statusLabels[normalized] || normalized)}</span>`;
  }

  function showToast(message, isError = false) {
    clearTimeout(state.toastTimer);
    elements.toast.textContent = message;
    elements.toast.classList.toggle("error", isError);
    elements.toast.classList.add("visible");
    state.toastTimer = setTimeout(() => elements.toast.classList.remove("visible"), 3800);
  }

  function renderAccountOptions() {
    const current = elements.accountFilter.value;
    const gateways = state.snapshot?.gateways || [];
    const options = gateways.map((gateway) => {
      const label = gateway.alias ? `${gateway.alias} (${gateway.account_id})` : gateway.account_id;
      return `<option value="${escapeHTML(gateway.account_id)}">${escapeHTML(label)}</option>`;
    });
    elements.accountFilter.innerHTML = `<option value="">全部账户</option>${options.join("")}`;
    elements.accountFilter.value = gateways.some((gateway) => gateway.account_id === current) ? current : "";
    state.accountID = elements.accountFilter.value;
  }

  function renderCredentialAccountOptions() {
    const credentialCurrent = elements.credentialAccount.value;
    const routes = state.accountRoutes.filter((route) => route.broker_id === "huaxin");
    elements.credentialAccount.innerHTML = routes.map((route) => {
      const label = route.alias ? `${route.alias} (${route.account_id})` : route.account_id;
      return `<option value="${escapeHTML(route.account_id)}">${escapeHTML(label)}</option>`;
    }).join("");
    if (routes.some((route) => route.account_id === credentialCurrent)) {
      elements.credentialAccount.value = credentialCurrent;
    }
  }

  function renderOverview() {
    const snapshot = state.snapshot;
    const summary = snapshot?.summary || {};
    const active = Boolean(snapshot?.monitoring_active);
    elements.monitoringWindow.textContent = active ? "监控中" : (monitoringLabels[snapshot?.monitoring_reason] || "--");
    elements.monitoringWindow.style.color = active ? "#36c9ad" : "";
    elements.monitoringReason.textContent = monitoringLabels[snapshot?.monitoring_reason] || snapshot?.monitoring_reason || "--";
    elements.gatewayOnline.textContent = `${formatNumber(summary.gateways_online)} / ${formatNumber((snapshot?.gateways || []).length)}`;
    elements.gatewayAttention.textContent = active ? `${formatNumber(summary.gateways_attention)} 个需关注` : `${formatNumber(summary.gateways_off_hours)} 个休市`;
    elements.streamLag.textContent = formatNumber(summary.total_lag);
    elements.streamAttention.textContent = `${formatNumber(summary.streams_attention)} 条流需关注`;
    elements.pendingDLQ.textContent = formatNumber(summary.pending_dead_letters);
    elements.updatedAt.textContent = `刷新 ${formatTime(snapshot?.generated_at)}`;
    elements.dlqWriteMode.textContent = snapshot?.actions_write_enabled
      ? "审核动作已启用，所有操作写入审计记录"
      : "当前环境只读，审核动作未启用";
  }

  function filteredGateways() {
    return (state.snapshot?.gateways || []).filter((gateway) => !state.accountID || gateway.account_id === state.accountID);
  }

  function renderGateways() {
    const gateways = filteredGateways();
    elements.gatewayCount.textContent = `${gateways.length} 个账户`;
    if (!gateways.length) {
      elements.gatewayBody.innerHTML = `<tr><td colspan="8">没有匹配的 Gateway</td></tr>`;
      return;
    }
    elements.gatewayBody.innerHTML = gateways.map((gateway) => {
      const account = gateway.alias
        ? `<strong>${escapeHTML(gateway.alias)}</strong><br><span class="mono">${escapeHTML(gateway.account_id)}</span>`
        : `<strong class="mono">${escapeHTML(gateway.account_id)}</strong>`;
      const issue = gateway.last_issue_code
        ? `${escapeHTML(gateway.last_issue_code)}<br><span>${escapeHTML(gateway.last_issue_message || formatTime(gateway.last_issue_at, true))}</span>`
        : "--";
      return `
        <tr>
          <td>${account}</td>
          <td class="mono-cell">${escapeHTML(gateway.broker_id)} / ${escapeHTML(gateway.gateway_id)}</td>
          <td>
            ${statusBadge(gateway.status)}
            ${gateway.state_text ? `<br><span>${escapeHTML(gateway.state_text)}</span>` : ""}
            ${gateway.accepting_trade_commands !== undefined
              ? `<br><span>OC交易命令 ${gateway.accepting_trade_commands ? "接收" : "暂停"} · 撤单 ${gateway.accepting_cancel_commands ? "接收" : "暂停"}</span>`
              : ""}
            ${gateway.broker_session_state
              ? `<br><span>下单准入 ${gateway.order_entry_ready ? "就绪" : "关闭"} · ${escapeHTML(gateway.broker_session_state)}${gateway.order_entry_block_reason ? ` · ${escapeHTML(gateway.order_entry_block_reason)}` : ""}</span>`
              : ""}
            ${gateway.next_transition_at
              ? `<br><span>下次切换 ${formatTime(gateway.next_transition_at, true)}</span>`
              : ""}
            ${gateway.credential_status
              ? `<br><span>凭据 ${escapeHTML(gateway.credential_status)} · v${formatNumber(gateway.credential_version)} · ${escapeHTML(gateway.credential_key_id || "--")}${gateway.managed_account_id ? ` · ${escapeHTML(gateway.managed_account_id)}` : ""}</span>`
              : ""}
          </td>
          <td>${formatTime(gateway.last_heartbeat_at, true)}</td>
          <td class="mono-cell">${gateway.last_heartbeat_at ? `${formatNumber(gateway.heartbeat_age_seconds)}s` : "--"}</td>
          <td class="mono-cell">${formatNumber(gateway.pending_trade_count)}</td>
          <td class="mono-cell">${formatNumber(gateway.pending_query_count)}</td>
          <td>${issue}</td>
        </tr>`;
    }).join("");
  }

  function filteredStreams() {
    return (state.snapshot?.streams || []).filter((stream) => {
      if (state.accountID && stream.account_id !== state.accountID) return false;
      if (state.streamRole && stream.role !== state.streamRole) return false;
      return true;
    });
  }

  function renderStreams() {
    const streams = filteredStreams();
    if (!streams.length) {
      elements.streamBody.innerHTML = `<tr><td colspan="9">没有匹配的 Stream</td></tr>`;
      return;
    }
    elements.streamBody.innerHTML = streams.map((stream) => `
      <tr>
        <td class="mono-cell">${escapeHTML(stream.account_id)}</td>
        <td>${escapeHTML(stream.role)}</td>
        <td>${statusBadge(stream.status)}</td>
        <td class="mono-cell">${formatNumber(stream.length)}</td>
        <td class="mono-cell">${stream.lag >= 0 ? `${formatNumber(stream.lag)}${stream.lag_capped ? "+" : ""}` : "--"}</td>
        <td class="mono-cell" title="${escapeHTML(stream.stream_key)}">${escapeHTML(stream.latest_stream_id || "--")}</td>
        <td class="mono-cell">${escapeHTML(stream.checkpoint_id || "--")}</td>
        <td>${formatTime(stream.last_consumed_at, true)}</td>
        <td title="${escapeHTML(stream.last_error || "")}">${escapeHTML(stream.last_error || "--")}</td>
      </tr>`).join("");
  }

  async function loadSnapshot(force = false) {
    const data = await getJSON(`/v1/operations/status${force ? "?force=true" : ""}`);
    state.snapshot = data;
    renderAccountOptions();
    renderOverview();
    renderGateways();
    renderStreams();
  }

  async function loadAccountRoutes() {
    const data = await getJSON("/v1/account-routes");
    state.accountRoutes = data.routes || [];
    renderCredentialAccountOptions();
  }

  function credentialAdminEnabled() {
    return elements.credentialPanel?.dataset.enabled === "true";
  }

  function credentialHeaders() {
    return { Authorization: `Bearer ${state.credentialToken}` };
  }

  function clearCredentialPlaintext() {
    elements.credentialLoginUser.value = "";
    elements.credentialPassword.value = "";
    elements.credentialDynamicPassword.value = "";
  }

  function setCredentialUnlocked(unlocked) {
    state.credentialUnlocked = unlocked;
    elements.credentialRotate.disabled = !unlocked;
    elements.credentialDisable.disabled = !unlocked;
    elements.credentialAdminState.textContent = unlocked ? "管理权限已验证" :
      (credentialAdminEnabled() ? "管理员令牌未验证" : "当前环境未启用");
  }

  async function loadCredentialStatus() {
    const accountID = elements.credentialAccount.value;
    if (!accountID || !state.credentialToken) return;
    const data = await getJSON(`/v1/admin/credentials?account_id=${encodeURIComponent(accountID)}`, {
      headers: credentialHeaders(),
    });
    const status = data.status || {};
    setCredentialUnlocked(true);
    elements.credentialEnvironment.textContent = status.environment || elements.credentialPanel.dataset.environment || "--";
    elements.credentialManagedAccount.textContent = status.account_id || accountID;
    elements.credentialVersion.textContent = status.configured ? `v${formatNumber(status.credential_version)}` : "未配置";
    elements.credentialKeyID.textContent = status.key_id || "--";
    elements.credentialVerify.textContent = status.decryption_verified ? "认证解密通过" : "--";
    elements.credentialIssuedAt.textContent = formatTime(status.issued_at, true);
    elements.credentialSource.textContent = status.credential_source || "--";
    elements.credentialEnvelopeSHA.textContent = status.envelope_sha256 || "--";
    elements.credentialEnvelopeSHA.title = status.envelope_sha256 || "";
  }

  async function unlockCredentialAdmin() {
    state.credentialToken = elements.credentialAdminToken.value.trim();
    if (!state.credentialToken) throw new Error("请输入管理员令牌");
    await loadCredentialStatus();
    sessionStorage.setItem("relayCredentialAdminToken", state.credentialToken);
  }

  async function rotateCredential(event) {
    event.preventDefault();
    const accountID = elements.credentialAccount.value;
    if (!accountID || !state.credentialUnlocked) return;
    if (elements.credentialPanel.dataset.environment === "production" && elements.credentialConfirmAccount.value.trim() !== accountID) {
      throw new Error("生产环境需要准确输入账户编号确认");
    }
    if (!window.confirm(`确认写入 ${accountID} 的新凭据版本？OC 需要单账户重启后生效。`)) return;
    try {
      const data = await getJSON("/v1/admin/credentials/rotate", {
        method: "POST",
        headers: { ...credentialHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({
          account_id: accountID,
          operator: elements.credentialOperator.value.trim(),
          confirm_account: elements.credentialConfirmAccount.value.trim(),
          broker_login_user: elements.credentialLoginUser.value,
          broker_password: elements.credentialPassword.value,
          dynamic_password: elements.credentialDynamicPassword.value,
        }),
      });
      showToast(`凭据 v${data.rotation?.credential_version || "--"} 已写入，等待 OC 重启`);
      await loadCredentialStatus();
    } finally {
      clearCredentialPlaintext();
    }
  }

  async function disableCredential() {
    const accountID = elements.credentialAccount.value;
    if (!accountID || !state.credentialUnlocked) return;
    if (elements.credentialConfirmAccount.value.trim() !== accountID) throw new Error("请输入完整账户编号确认停用");
    if (!window.confirm(`确认停用 ${accountID} 的当前凭据？对应 OC 仍需停止或重启。`)) return;
    const data = await getJSON("/v1/admin/credentials/disable", {
      method: "POST",
      headers: { ...credentialHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify({
        account_id: accountID,
        operator: elements.credentialOperator.value.trim(),
        confirm_account: elements.credentialConfirmAccount.value.trim(),
      }),
    });
    showToast(data.disable?.disabled ? "当前凭据已停用，需停止或重启 OC" : "凭据停用完成");
    await loadCredentialStatus();
  }

  function dlqQuery() {
    const params = new URLSearchParams({
      page: String(state.dlqPage),
      page_size: String(state.dlqPageSize),
    });
    if (state.accountID) params.set("account_id", state.accountID);
    if (state.dlqStatus) params.set("status", state.dlqStatus);
    return params.toString();
  }

  async function loadDeadLetters() {
    const data = await getJSON(`/v1/operations/dlq?${dlqQuery()}`);
    state.deadLetters = data.items || [];
    state.dlqTotal = Number(data.total_count || 0);
    if (state.selectedDeadLetter) {
      state.selectedDeadLetter = state.deadLetters.find((item) =>
        item.stream_key === state.selectedDeadLetter.stream_key &&
        item.stream_id === state.selectedDeadLetter.stream_id
      ) || null;
    }
    renderDeadLetters();
    if (state.selectedDeadLetter) {
      await renderDeadLetterDetail(state.selectedDeadLetter);
    } else {
      elements.dlqDetail.innerHTML = `<div class="empty-detail">选择一条死信查看原文与审核记录</div>`;
    }
  }

  function renderDeadLetters() {
    if (!state.deadLetters.length) {
      elements.dlqBody.innerHTML = `<tr><td colspan="7">当前筛选下没有死信</td></tr>`;
    } else {
      elements.dlqBody.innerHTML = state.deadLetters.map((item, index) => {
        const selected = state.selectedDeadLetter?.stream_key === item.stream_key &&
          state.selectedDeadLetter?.stream_id === item.stream_id;
        return `
          <tr class="dlq-body-row${selected ? " selected" : ""}" data-dlq-index="${index}">
            <td>${formatTime(item.received_at, true)}</td>
            <td class="mono-cell">${escapeHTML(item.account_id || "--")}</td>
            <td>${statusBadge(item.review_status)}</td>
            <td>${escapeHTML(item.code || "--")}</td>
            <td class="mono-cell">${escapeHTML(item.action || "--")}</td>
            <td title="${escapeHTML(item.message || "")}">${escapeHTML(item.message || "--")}</td>
            <td class="mono-cell">${escapeHTML(item.stream_id)}</td>
          </tr>`;
      }).join("");
    }
    const totalPages = Math.max(1, Math.ceil(state.dlqTotal / state.dlqPageSize));
    elements.dlqPageText.textContent = `第 ${state.dlqPage} / ${totalPages} 页，共 ${formatNumber(state.dlqTotal)} 条`;
    elements.dlqPrevious.disabled = state.dlqPage <= 1;
    elements.dlqNext.disabled = state.dlqPage >= totalPages;
  }

  async function renderDeadLetterDetail(item) {
    const params = new URLSearchParams({ stream_key: item.stream_key, stream_id: item.stream_id });
    let reviews = [];
    try {
      const data = await getJSON(`/v1/operations/dlq/reviews?${params.toString()}`);
      reviews = data.reviews || [];
    } catch (error) {
      showToast(error.message, true);
    }
    const writable = Boolean(state.snapshot?.actions_write_enabled);
    const history = reviews.length
      ? reviews.map((review) => `<div><strong>${escapeHTML(statusLabels[review.status] || review.status)}</strong> · ${escapeHTML(review.operator)} · ${formatTime(review.created_at, true)}${review.note ? `<br>${escapeHTML(review.note)}` : ""}</div>`).join("")
      : `<div>暂无审核记录</div>`;
    elements.dlqDetail.innerHTML = `
      <div class="detail-heading">
        <div>
          <h3>${escapeHTML(item.code || "Dead Letter")}</h3>
          <code>${escapeHTML(item.stream_id)}</code>
        </div>
        ${statusBadge(item.review_status)}
      </div>
      <div class="detail-meta">
        <div><span>账户</span><strong>${escapeHTML(item.account_id || "--")}</strong></div>
        <div><span>Action</span><strong>${escapeHTML(item.action || "--")}</strong></div>
        <div><span>Origin Message</span><strong>${escapeHTML(item.origin_message_id || "--")}</strong></div>
        <div><span>Request ID</span><strong>${escapeHTML(item.request_id || "--")}</strong></div>
      </div>
      <strong>原始报文</strong>
      <pre class="dlq-json">${escapeHTML(JSON.stringify(item.body || {}, null, 2))}</pre>
      <strong>审核记录</strong>
      <div class="review-history">${history}</div>
      <div class="detail-review">
        <input id="dlqOperator" maxlength="120" placeholder="操作人" ${writable ? "" : "disabled"}>
        <textarea id="dlqNote" maxlength="2000" placeholder="处置说明" ${writable ? "" : "disabled"}></textarea>
        <div class="detail-actions">
          <button type="button" class="primary" data-review-status="acknowledged" ${writable ? "" : "disabled"}>确认</button>
          <button type="button" data-review-status="ignored" ${writable ? "" : "disabled"}>忽略</button>
          <button type="button" data-review-status="replayed" ${writable ? "" : "disabled"}>标记已重放</button>
        </div>
      </div>`;
  }

  async function submitReview(status) {
    const item = state.selectedDeadLetter;
    if (!item) return;
    const operator = document.getElementById("dlqOperator")?.value.trim() || "";
    const note = document.getElementById("dlqNote")?.value.trim() || "";
    if (!operator) {
      showToast("请填写操作人", true);
      return;
    }
    await getJSON("/v1/operations/dlq/review", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        stream_key: item.stream_key,
        stream_id: item.stream_id,
        status,
        operator,
        note,
      }),
    });
    showToast(`死信已${statusLabels[status] || status}`);
    await Promise.all([loadSnapshot(true), loadDeadLetters()]);
  }

  async function refreshAll(force = false) {
    try {
      await Promise.all([loadSnapshot(force), loadAccountRoutes()]);
      await loadDeadLetters();
    } catch (error) {
      showToast(error.message, true);
    }
  }

  elements.refresh.addEventListener("click", () => refreshAll(true));
  elements.credentialUnlock.addEventListener("click", () => unlockCredentialAdmin().catch((error) => {
    setCredentialUnlocked(false);
    sessionStorage.removeItem("relayCredentialAdminToken");
    showToast(error.message, true);
  }));
  elements.credentialForm.addEventListener("submit", (event) => rotateCredential(event).catch((error) => showToast(error.message, true)));
  elements.credentialDisable.addEventListener("click", () => disableCredential().catch((error) => showToast(error.message, true)));
  elements.credentialAccount.addEventListener("change", () => {
    elements.credentialConfirmAccount.value = "";
    loadCredentialStatus().catch((error) => showToast(error.message, true));
  });
  elements.refreshDLQ.addEventListener("click", () => loadDeadLetters().catch((error) => showToast(error.message, true)));
  elements.accountFilter.addEventListener("change", () => {
    state.accountID = elements.accountFilter.value;
    state.dlqPage = 1;
    renderGateways();
    renderStreams();
    loadDeadLetters().catch((error) => showToast(error.message, true));
  });
  elements.dlqStatusFilter.addEventListener("change", () => {
    state.dlqStatus = elements.dlqStatusFilter.value;
    state.dlqPage = 1;
    loadDeadLetters().catch((error) => showToast(error.message, true));
  });
  document.querySelectorAll("[data-stream-role]").forEach((button) => {
    button.addEventListener("click", () => {
      document.querySelectorAll("[data-stream-role]").forEach((candidate) => candidate.classList.remove("active"));
      button.classList.add("active");
      state.streamRole = button.dataset.streamRole || "";
      renderStreams();
    });
  });
  elements.dlqBody.addEventListener("click", (event) => {
    const row = event.target.closest("[data-dlq-index]");
    if (!row) return;
    state.selectedDeadLetter = state.deadLetters[Number(row.dataset.dlqIndex)] || null;
    renderDeadLetters();
    if (state.selectedDeadLetter) {
      renderDeadLetterDetail(state.selectedDeadLetter);
    }
  });
  elements.dlqDetail.addEventListener("click", (event) => {
    const button = event.target.closest("[data-review-status]");
    if (!button) return;
    submitReview(button.dataset.reviewStatus).catch((error) => showToast(error.message, true));
  });
  elements.dlqPrevious.addEventListener("click", () => {
    if (state.dlqPage <= 1) return;
    state.dlqPage -= 1;
    loadDeadLetters().catch((error) => showToast(error.message, true));
  });
  elements.dlqNext.addEventListener("click", () => {
    state.dlqPage += 1;
    loadDeadLetters().catch((error) => showToast(error.message, true));
  });

  elements.credentialAdminToken.value = state.credentialToken;
  setCredentialUnlocked(false);
  refreshAll(true).then(() => {
    if (credentialAdminEnabled() && state.credentialToken) {
      loadCredentialStatus().catch(() => {
        sessionStorage.removeItem("relayCredentialAdminToken");
        state.credentialToken = "";
        elements.credentialAdminToken.value = "";
        setCredentialUnlocked(false);
      });
    }
  });
  state.refreshTimer = setInterval(() => {
    if (document.visibilityState === "visible") refreshAll(false);
  }, 10000);
})();
