(() => {
  const state = {
    accounts: [],
    activeAccount: "",
    asset: null,
    positions: [],
    orders: [],
    fills: [],
    ordersPage: { cursor: "", previous: [], next: "", page: 1, pageSize: 50 },
    fillsPage: { cursor: "", previous: [], next: "", page: 1, pageSize: 50 },
    positionsPage: { cursor: "", previous: [], next: "", page: 1, pageSize: 50 },
    selectedOrderID: "",
    selectedTab: "orders",
    side: "B",
    logs: [],
    lastPayload: {},
    orderSignatures: new Map(),
    changedOrders: new Map(),
    marketSnapshot: null,
    symbolSuggestions: [],
    instrumentCache: new Map(),
    activeSuggestion: -1,
    suggestionSeq: 0,
    quoteSeq: 0,
    priceEdited: false,
    activeView: "trade",
    performanceSummary: null,
    performanceSeries: [],
    performanceDaily: null,
    performanceLoaded: false,
    performanceError: "",
    barsRows: [],
    barsMeta: null,
    barsLoaded: false,
    barsError: "",
    initialized: false,
    eventSource: null,
    eventSourceAccount: "",
    streamConnected: false,
    streamRefreshTimer: 0,
    streamErrorLoggedAt: 0
  };

  const els = {
    shell: byID("terminalShell"),
    viewLinks: Array.from(document.querySelectorAll("[data-view-link]")),
    apiStatus: byID("apiStatus"),
    redisStatus: byID("redisStatus"),
    dbStatus: byID("dbStatus"),
    accountTabs: byID("accountTabs"),
    tradeDate: byID("tradeDate"),
    serverClock: byID("serverClock"),
    footerClock: byID("footerClock"),
    footerApi: byID("footerApi"),
    footerRedis: byID("footerRedis"),
    orderAccount: byID("orderAccount"),
    orderForm: byID("orderForm"),
    symbolInput: byID("symbolInput"),
    symbolSuggest: byID("symbolSuggest"),
    exchangeInput: byID("exchangeInput"),
    priceInput: byID("priceInput"),
    qtyInput: byID("qtyInput"),
    maxBuy: byID("maxBuy"),
    availableCash: byID("availableCash"),
    riskAlert: byID("riskAlert"),
    submitOrderButton: byID("submitOrderButton"),
    resetOrderButton: byID("resetOrderButton"),
    refreshAssetButton: byID("refreshAssetButton"),
    refreshPositionsButton: byID("refreshPositionsButton"),
    refreshOrdersButton: byID("refreshOrdersButton"),
    refreshFillsButton: byID("refreshFillsButton"),
    queryAssetButton: byID("queryAssetButton"),
    queryOrdersButton: byID("queryOrdersButton"),
    assetTradeDate: byID("assetTradeDate"),
    ordersTradeDate: byID("ordersTradeDate"),
    netAsset: byID("netAsset"),
    cashAvailable: byID("cashAvailable"),
    marketValue: byID("marketValue"),
    dayProfit: byID("dayProfit"),
    cashTotal: byID("cashTotal"),
    stockValue: byID("stockValue"),
    fundValue: byID("fundValue"),
    positionProfit: byID("positionProfit"),
    closeProfit: byID("closeProfit"),
    commission: byID("commission"),
    positionsBody: byID("positionsBody"),
    positionsPageInfo: byID("positionsPageInfo"),
    positionsPrevPage: byID("positionsPrevPage"),
    positionsNextPage: byID("positionsNextPage"),
    orderCount: byID("orderCount"),
    activeOrderCount: byID("activeOrderCount"),
    fillCount: byID("fillCount"),
    lastEventTime: byID("lastEventTime"),
    blotterTabs: byID("blotterTabs"),
    blotterContent: byID("blotterContent"),
    ordersPageInfo: byID("ordersPageInfo"),
    ordersPrevPage: byID("ordersPrevPage"),
    ordersNextPage: byID("ordersNextPage"),
    detailSub: byID("detailSub"),
    timeline: byID("timeline"),
    rawJson: byID("rawJson"),
    executionList: byID("executionList"),
    closeDetailButton: byID("closeDetailButton"),
    toast: byID("terminalToast"),
    depthBook: byID("depthBook"),
    quoteSymbol: byID("quoteSymbol"),
    quoteName: byID("quoteName"),
    quoteSource: byID("quoteSource"),
    quotePrice: byID("quotePrice"),
    quoteLast: byID("quoteLast"),
    quoteChange: byID("quoteChange"),
    performanceRangeHint: byID("performanceRangeHint"),
    perfDateFrom: byID("perfDateFrom"),
    perfDateTo: byID("perfDateTo"),
    loadPerformanceButton: byID("loadPerformanceButton"),
    downloadPerformanceButton: byID("downloadPerformanceButton"),
    perfNetAsset: byID("perfNetAsset"),
    perfStartNetAsset: byID("perfStartNetAsset"),
    perfTotalPnl: byID("perfTotalPnl"),
    perfRows: byID("perfRows"),
    perfTotalReturn: byID("perfTotalReturn"),
    perfDailyReturn: byID("perfDailyReturn"),
    perfMaxDrawdown: byID("perfMaxDrawdown"),
    perfDailyPnl: byID("perfDailyPnl"),
    performanceStatus: byID("performanceStatus"),
    performanceSeriesBody: byID("performanceSeriesBody"),
    perfDailyDate: byID("perfDailyDate"),
    perfPositions: byID("perfPositions"),
    perfPositionValue: byID("perfPositionValue"),
    perfUnrealizedPnl: byID("perfUnrealizedPnl"),
    perfFills: byID("perfFills"),
    perfTurnover: byID("perfTurnover"),
    perfFee: byID("perfFee"),
    perfCapturedAt: byID("perfCapturedAt"),
    barSecurityInput: byID("barSecurityInput"),
    barTradeDateInput: byID("barTradeDateInput"),
    barFrequencyInput: byID("barFrequencyInput"),
    barAdjustmentInput: byID("barAdjustmentInput"),
    barStartTimeInput: byID("barStartTimeInput"),
    barEndTimeInput: byID("barEndTimeInput"),
    loadBarsButton: byID("loadBarsButton"),
    barsStatus: byID("barsStatus"),
    barClose: byID("barClose"),
    barVolume: byID("barVolume"),
    barCount: byID("barCount"),
    barTime: byID("barTime"),
    barsBody: byID("barsBody")
  };

  function byID(id) {
    return document.getElementById(id);
  }

  function apiURL(path) {
    return path;
  }

  async function request(path, options = {}) {
    const init = {
      method: options.method || "GET",
      headers: {
        "X-Request-ID": "relay-trade-" + Date.now()
      }
    };
    if (options.body) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(options.body);
    }
    if (options.signal) {
      init.signal = options.signal;
    }
    const response = await fetch(apiURL(path), init);
    const text = await response.text();
    let payload = {};
    if (text) {
      payload = JSON.parse(text);
    }
    state.lastPayload = payload;
    if (!response.ok || payload.ok === false) {
      const message = payload.error && payload.error.message ? payload.error.message : "HTTP " + response.status;
      const error = new Error(message);
      error.payload = payload;
      throw error;
    }
    return Object.prototype.hasOwnProperty.call(payload, "data") ? payload.data : payload;
  }

  function formatNumber(value, digits = 2) {
    const number = Number(value);
    if (!Number.isFinite(number)) {
      return "--";
    }
    return number.toLocaleString("en-US", {
      minimumFractionDigits: digits,
      maximumFractionDigits: digits
    });
  }

  function formatPercent(value, digits = 2) {
    const number = Number(value);
    if (!Number.isFinite(number)) {
      return "--";
    }
    const prefix = number > 0 ? "+" : "";
    return prefix + (number * 100).toLocaleString("en-US", {
      minimumFractionDigits: digits,
      maximumFractionDigits: digits
    }) + "%";
  }

  function compactDate(value) {
    const text = String(value || "").trim();
    if (!text) {
      return "";
    }
    const digits = text.replace(/[^0-9]/g, "");
    if (digits.length === 8) {
      return digits;
    }
    return "";
  }

  function businessDateCompact(date = new Date()) {
    const parts = new Intl.DateTimeFormat("zh-CN", {
      timeZone: "Asia/Shanghai",
      year: "numeric",
      month: "2-digit",
      day: "2-digit"
    }).formatToParts(date);
    const byType = {};
    for (const part of parts) {
      byType[part.type] = part.value;
    }
    return String(byType.year || "").padStart(4, "0") +
      String(byType.month || "").padStart(2, "0") +
      String(byType.day || "").padStart(2, "0");
  }

  function displayDate(value) {
    const digits = compactDate(value);
    if (!digits) {
      return String(value || "--");
    }
    return digits.slice(0, 4) + "-" + digits.slice(4, 6) + "-" + digits.slice(6, 8);
  }

  function classForNumber(value) {
    const number = Number(value);
    if (!Number.isFinite(number) || number === 0) {
      return "";
    }
    return number < 0 ? "down" : "up";
  }

  function defaultLedgerDate() {
    return compactDate(els.tradeDate.textContent) || businessDateCompact();
  }

  function isCurrentBusinessDate(value) {
    const date = compactDate(value);
    return date !== "" && date === businessDateCompact();
  }

  function priceDigitsForInstrument(instrumentType) {
    return String(instrumentType || "").toLowerCase() === "etf" ? 3 : 2;
  }

  function priceDigitsForItem(item) {
    return priceDigitsForInstrument(instrumentTypeForItem(item));
  }

  function instrumentTypeForItem(item) {
    if (item && item.instrument_type) {
      return item.instrument_type;
    }
    const securityID = itemSecurityID(item);
    if (securityID && state.marketSnapshot && state.marketSnapshot.security_id === securityID) {
      return state.marketSnapshot.instrument_type || "";
    }
    if (securityID) {
      for (const instruments of state.instrumentCache.values()) {
        const match = instruments.find((instrument) => instrument.security_id === securityID);
        if (match) {
          return match.instrument_type || "";
        }
      }
    }
    return "";
  }

  function itemSecurityID(item) {
    if (!item) {
      return "";
    }
    if (item.security_id) {
      return String(item.security_id).toUpperCase();
    }
    if (item.symbol) {
      const symbol = normalizeSymbol(item.symbol);
      if (symbol.includes(".")) {
        return symbol;
      }
      return symbol + "." + String(item.exchange || inferExchange(symbol)).toUpperCase();
    }
    return "";
  }

  function formatPrice(value, item) {
    return formatNumber(value, priceDigitsForItem(item));
  }

  function formatSignedPrice(value, item) {
    const number = Number(value);
    if (!Number.isFinite(number)) {
      return "--";
    }
    const prefix = number > 0 ? "+" : "";
    return prefix + formatNumber(number, priceDigitsForItem(item));
  }

  function applyPriceInputPrecision(item) {
    const digits = priceDigitsForItem(item || state.marketSnapshot);
    els.priceInput.step = digits === 3 ? "0.001" : "0.01";
  }

  function formatInt(value) {
    const number = Number(value);
    if (!Number.isFinite(number)) {
      return "--";
    }
    return Math.trunc(number).toLocaleString("en-US");
  }

  function formatTime(value) {
    if (!value) {
      return "--";
    }
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    return date.toLocaleTimeString("zh-CN", { hour12: false });
  }

  function symbolText(item) {
    if (!item) {
      return "--";
    }
    return item.symbol + (item.exchange ? "." + item.exchange : "");
  }

  function normalizeSymbol(value) {
    return String(value || "").trim().toUpperCase().replace(/[^0-9A-Z.]/g, "");
  }

  function splitSecurityID(securityID) {
    const normalized = normalizeSymbol(securityID);
    const parts = normalized.split(".");
    return {
      symbol: parts[0] || "",
      exchange: parts[1] || inferExchange(parts[0] || "")
    };
  }

  function inferExchange(symbol) {
    const code = normalizeSymbol(symbol).replace(/\..*$/, "");
    if (/^(6|5|9)/.test(code)) {
      return "SH";
    }
    if (/^(0|1|2|3)/.test(code)) {
      return "SZ";
    }
    if (/^(4|8)/.test(code)) {
      return "BJ";
    }
    return els.exchangeInput.value || "SH";
  }

  function currentSecurityID() {
    const raw = normalizeSymbol(els.symbolInput.value);
    if (!raw) {
      return "";
    }
    if (raw.includes(".")) {
      return raw;
    }
    return raw + "." + (els.exchangeInput.value || inferExchange(raw));
  }

  function normalizeSecurityID(value) {
    const raw = normalizeSymbol(value);
    if (!raw) {
      return "";
    }
    if (raw.includes(".")) {
      return raw;
    }
    return raw + "." + inferExchange(raw);
  }

  function setSymbolFromSecurityID(securityID) {
    const parsed = splitSecurityID(securityID);
    els.symbolInput.value = parsed.symbol;
    els.exchangeInput.value = parsed.exchange;
  }

  function sideText(side) {
    return side === "S" ? "卖出" : "买入";
  }

  function statusText(status) {
    return {
      created: "已提交",
      accepted: "已受理",
      working: "已报待成",
      partially_filled: "部分成交",
      filled: "全部成交",
      cancelled: "已撤",
      rejected: "废单"
    }[status] || status || "--";
  }

  function escapeHTML(value) {
    return String(value ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function setStatus(el, ok, label) {
    el.classList.toggle("danger", !ok);
    const text = el.childNodes[1];
    if (text) {
      text.nodeValue = label;
    } else {
      el.append(label);
    }
  }

  function dependencyOK(dep) {
    return dep && dep.status === "ok";
  }

  function dependencyLabel(name, dep) {
    return name + ": " + (dep && dep.status ? dep.status : "unknown");
  }

  function pushLog(level, message, detail) {
    state.logs.unshift({
      at: new Date(),
      level,
      message,
      detail: detail || ""
    });
    state.logs = state.logs.slice(0, 80);
    if (state.selectedTab === "logs") {
      renderBlotter();
    }
  }

  function showToast(message, type = "info") {
    els.toast.textContent = message;
    els.toast.classList.toggle("error", type === "error");
  }

  function viewFromLocation() {
    const hash = String(window.location.hash || "").replace("#", "");
    if (hash === "asset") {
      return "asset";
    }
    if (hash === "performance") {
      return "performance";
    }
    if (hash === "orders" || hash === "fills") {
      if (hash === "fills") {
        state.selectedTab = "fills";
      }
      return "orders";
    }
    return "trade";
  }

  function navigateView(view) {
    const url = view === "trade" ? "/trade" : "/trade#" + view;
    window.history.pushState({ view }, "", url);
    setActiveView(view);
  }

  function setActiveView(view) {
    if (!["trade", "orders", "asset", "performance"].includes(view)) {
      view = "trade";
    }
    state.activeView = view;
    els.shell.classList.toggle("view-trade", view === "trade");
    els.shell.classList.toggle("view-orders", view === "orders");
    els.shell.classList.toggle("view-asset", view === "asset");
    els.shell.classList.toggle("view-performance", view === "performance");
    for (const link of els.viewLinks) {
      link.classList.toggle("active", link.dataset.viewLink === view);
    }
    renderMonitorSummary();
    renderBlotter();
    renderDetail();
    if (view === "performance" && state.initialized) {
      ensurePerformanceDefaults();
      if (state.activeAccount && !state.performanceLoaded) {
        loadPerformance().catch((err) => pushLog("warn", "绩效查询失败", err.message));
      }
      if (!state.barsLoaded) {
        loadBars().catch((err) => pushLog("warn", "Bars 查询失败", err.message));
      }
    }
  }

  async function loadStatus() {
    try {
      const data = await request("/v1/status");
      const dependencies = data.dependencies || {};
      const apiOK = data.status === "ok";
      setStatus(els.apiStatus, apiOK, "API: " + (apiOK ? "connected" : data.status || "degraded"));
      setStatus(els.redisStatus, dependencyOK(dependencies.redis), dependencyLabel("Redis", dependencies.redis));
      setStatus(els.dbStatus, dependencyOK(dependencies.database), dependencyLabel("DB", dependencies.database));
      els.footerApi.textContent = data.public_url || window.RELAY_PUBLIC_URL || "connected";
      updateStreamFooter();
      if (data.time) {
        syncClock(data.time);
      }
    } catch (err) {
      setStatus(els.apiStatus, false, "API: error");
      setStatus(els.redisStatus, false, "Redis: unknown");
      setStatus(els.dbStatus, false, "DB: unknown");
      pushLog("error", "状态接口失败", err.message);
    }
  }

  async function loadAccounts() {
    const data = await request("/v1/accounts");
    state.accounts = data.accounts || [];
    if (!state.activeAccount && state.accounts.length > 0) {
      state.activeAccount = state.accounts[0].account_id;
    }
    renderAccounts();
  }

  function connectEventStream() {
    if (!window.EventSource || !state.activeAccount) {
      updateStreamFooter();
      return;
    }
    if (state.eventSource && state.eventSourceAccount === state.activeAccount) {
      updateStreamFooter();
      return;
    }
    closeEventStream();
    const accountID = state.activeAccount;
    const source = new EventSource("/v1/events/stream?account_id=" + encodeURIComponent(accountID));
    state.eventSource = source;
    state.eventSourceAccount = accountID;
    state.streamConnected = false;
    updateStreamFooter();

    source.addEventListener("open", () => {
      state.streamConnected = true;
      updateStreamFooter();
    });
    source.addEventListener("relay.connected", (event) => {
      state.streamConnected = true;
      updateStreamFooter();
      const payload = parseStreamPayload(event);
      pushLog("info", "实时通道已连接", payload && payload.account_ids ? payload.account_ids.join(",") : accountID);
    });
    source.addEventListener("relay.heartbeat", () => {
      state.streamConnected = true;
      updateStreamFooter();
    });
    for (const type of ["order.changed", "fill.changed", "asset.changed", "positions.changed"]) {
      source.addEventListener(type, (event) => handleLedgerStreamEvent(type, event));
    }
    source.onerror = () => {
      state.streamConnected = false;
      updateStreamFooter();
      const now = Date.now();
      if (now - state.streamErrorLoggedAt > 10000) {
        state.streamErrorLoggedAt = now;
        pushLog("warn", "实时通道重连中", "保留 3 秒轮询兜底");
      }
    };
  }

  function closeEventStream() {
    if (state.eventSource) {
      state.eventSource.close();
      state.eventSource = null;
    }
    state.eventSourceAccount = "";
    state.streamConnected = false;
    updateStreamFooter();
  }

  function updateStreamFooter() {
    if (!els.footerRedis) {
      return;
    }
    if (state.streamConnected) {
      els.footerRedis.textContent = "sse live";
    } else if (state.eventSource) {
      els.footerRedis.textContent = "sse reconnecting / poll 3s";
    } else {
      els.footerRedis.textContent = "poll 3s";
    }
  }

  function parseStreamPayload(event) {
    try {
      return JSON.parse(event.data || "{}");
    } catch (err) {
      pushLog("warn", "实时事件解析失败", err.message);
      return null;
    }
  }

  function handleLedgerStreamEvent(type, event) {
    const payload = parseStreamPayload(event);
    if (!payload || !streamEventMatchesActiveAccount(payload)) {
      return;
    }
    state.streamConnected = true;
    state.lastPayload = payload;
    updateStreamFooter();
    pushLog("info", "实时事件", type + (payload.last_stream_id ? " " + payload.last_stream_id : ""));
    scheduleStreamRefresh();
  }

  function streamEventMatchesActiveAccount(payload) {
    const accountIDs = Array.isArray(payload.account_ids) ? payload.account_ids : [];
    return accountIDs.length === 0 || accountIDs.includes(state.activeAccount);
  }

  function scheduleStreamRefresh() {
    window.clearTimeout(state.streamRefreshTimer);
    state.streamRefreshTimer = window.setTimeout(() => {
      loadAccountData().catch((err) => pushLog("error", "实时刷新失败", err.message));
    }, 150);
  }

  async function loadAccountData() {
    if (!state.activeAccount) {
      return;
    }
    ensureLedgerQueryDefaults();
    const [assetResult, positionsResult, ordersResult, fillsResult] = await Promise.allSettled([
      fetchAssetForSelectedDate(),
      fetchPositionsPage(),
      fetchOrdersPage(),
      fetchFillsPage()
    ]);

    if (assetResult.status === "fulfilled") {
      state.asset = assetResult.value;
    } else {
      state.asset = null;
      pushLog("warn", "资金读取失败", assetResult.reason.message);
    }
    if (positionsResult.status === "fulfilled") {
      state.positions = positionsResult.value.positions || [];
      state.positionsPage.next = positionsResult.value.next_cursor || "";
    } else {
      pushLog("warn", "持仓读取失败", positionsResult.reason.message);
    }
    if (ordersResult.status === "fulfilled") {
      state.ordersPage.next = ordersResult.value.next_cursor || "";
      updateOrders(ordersResult.value.orders || []);
    } else {
      pushLog("warn", "订单读取失败", ordersResult.reason.message);
    }
    if (fillsResult.status === "fulfilled") {
      state.fills = fillsResult.value.fills || [];
      state.fillsPage.next = fillsResult.value.next_cursor || "";
    } else {
      pushLog("warn", "成交读取失败", fillsResult.reason.message);
    }

    renderAll();
  }

  function ensureLedgerQueryDefaults() {
    const day = defaultLedgerDate();
    if (!els.ordersTradeDate.value) {
      els.ordersTradeDate.value = day;
    }
    if (!els.assetTradeDate.value) {
      els.assetTradeDate.value = day;
    }
  }

  function selectedOrdersTradeDate() {
    ensureLedgerQueryDefaults();
    const day = compactDate(els.ordersTradeDate.value);
    if (!day) {
      throw new Error("订单交易日需为 YYYYMMDD 或 YYYY-MM-DD");
    }
    return day;
  }

  function selectedAssetTradeDate() {
    ensureLedgerQueryDefaults();
    const day = compactDate(els.assetTradeDate.value);
    if (!day) {
      throw new Error("持仓交易日需为 YYYYMMDD 或 YYYY-MM-DD");
    }
    return day;
  }

  function resetPage(page) {
    page.cursor = "";
    page.previous = [];
    page.next = "";
    page.page = 1;
  }

  function resetLedgerPages() {
    resetPage(state.ordersPage);
    resetPage(state.fillsPage);
    resetPage(state.positionsPage);
  }

  async function fetchAssetForSelectedDate() {
    const accountID = encodeURIComponent(state.activeAccount);
    const tradeDate = selectedAssetTradeDate();
    if (isCurrentBusinessDate(tradeDate)) {
      const data = await request("/v1/accounts/" + accountID + "/asset");
      return data.asset || null;
    }
    const data = await request("/v1/accounts/" + accountID + "/performance/daily?trade_date=" + encodeURIComponent(tradeDate));
    return assetFromPerformance(data.performance || {});
  }

  function assetFromPerformance(performance) {
    return {
      account_id: performance.account_id,
      cash_available: performance.cash_available,
      cash_total: performance.cash_total,
      net_asset: performance.net_asset,
      market_value: performance.market_value || performance.position_market_value,
      stock_value: performance.stock_value,
      fund_value: performance.fund_value,
      day_profit: performance.daily_pnl,
      position_profit: performance.position_profit || performance.unrealized_pnl,
      close_profit: performance.close_profit || performance.settled_profit,
      commission: performance.fee_total,
      captured_at: performance.captured_at
    };
  }

  async function fetchPositionsPage() {
    const accountID = encodeURIComponent(state.activeAccount);
    const tradeDate = selectedAssetTradeDate();
    const params = new URLSearchParams({
      limit: String(state.positionsPage.pageSize)
    });
    if (state.positionsPage.cursor) {
      params.set("cursor", state.positionsPage.cursor);
    }
    let path = "/v1/accounts/" + accountID + "/positions";
    if (!isCurrentBusinessDate(tradeDate)) {
      path = "/v1/accounts/" + accountID + "/positions/history";
      params.set("trade_date", tradeDate);
    }
    return request(path + "?" + params.toString());
  }

  async function fetchOrdersPage() {
    const tradeDate = selectedOrdersTradeDate();
    const params = new URLSearchParams({
      account_id: state.activeAccount,
      limit: String(state.ordersPage.pageSize)
    });
    if (state.ordersPage.cursor) {
      params.set("cursor", state.ordersPage.cursor);
    }
    let path = "/v1/orders";
    if (!isCurrentBusinessDate(tradeDate)) {
      path = "/v1/history/orders";
      params.set("trade_date", tradeDate);
    }
    return request(path + "?" + params.toString());
  }

  async function fetchFillsPage() {
    const tradeDate = selectedOrdersTradeDate();
    const params = new URLSearchParams({
      account_id: state.activeAccount,
      limit: String(state.fillsPage.pageSize)
    });
    if (state.fillsPage.cursor) {
      params.set("cursor", state.fillsPage.cursor);
    }
    let path = "/v1/fills";
    if (!isCurrentBusinessDate(tradeDate)) {
      path = "/v1/history/fills";
      params.set("trade_date", tradeDate);
    }
    return request(path + "?" + params.toString());
  }

  async function loadQuoteForInput(options = {}) {
    const securityID = options.securityID || currentSecurityID();
    if (!securityID) {
      return;
    }
    const seq = ++state.quoteSeq;
    const params = new URLSearchParams({
      security_id: securityID,
      market_level: "level1",
      data_scope: "realtime",
      limit: "1"
    });
    try {
      const data = await request("/v1/meridian/market/snapshots?" + params.toString(), {
        signal: options.signal
      });
      if (seq !== state.quoteSeq) {
        return;
      }
      if (data.error) {
        throw new Error(data.error.message || data.error.code || "Meridian quote error");
      }
      const items = Array.isArray(data.data) ? data.data : [];
      state.marketSnapshot = items[0] || null;
      renderQuote();
      renderDepthBook();
      applyQuotePrice();
    } catch (err) {
      if (err.name === "AbortError") {
        return;
      }
      pushLog("warn", "行情刷新失败", securityID + " " + err.message);
    }
  }

  async function loadSymbolSuggestions() {
    const query = normalizeSymbol(els.symbolInput.value).replace(/\..*$/, "");
    const seq = ++state.suggestionSeq;
    if (query.length < 3) {
      state.symbolSuggestions = localSymbolSuggestions(query);
      state.activeSuggestion = state.symbolSuggestions.length > 0 ? 0 : -1;
      renderSymbolSuggestions();
      return;
    }
    const exchange = inferExchange(query);
    try {
      const instruments = await loadInstruments(exchange);
      if (seq !== state.suggestionSeq) {
        return;
      }
      state.symbolSuggestions = mergeSuggestions(
        instruments.filter((item) => item.symbol.startsWith(query)).map(instrumentSuggestion),
        localSymbolSuggestions(query)
      );
      state.activeSuggestion = state.symbolSuggestions.length > 0 ? 0 : -1;
      renderSymbolSuggestions();
    } catch (err) {
      if (err.name !== "AbortError") {
        pushLog("warn", "代码补全失败", err.message);
      }
      if (seq === state.suggestionSeq) {
        state.symbolSuggestions = localSymbolSuggestions(query);
        state.activeSuggestion = state.symbolSuggestions.length > 0 ? 0 : -1;
        renderSymbolSuggestions();
      }
    }
  }

  async function loadInstruments(exchange) {
    const cacheKey = exchange || "SH";
    if (state.instrumentCache.has(cacheKey)) {
      return state.instrumentCache.get(cacheKey);
    }
    const pages = [];
    for (const instrumentType of ["stock", "etf"]) {
      let cursor = "";
      for (let page = 0; page < 3; page += 1) {
        const params = new URLSearchParams({
          exchange: cacheKey,
          instrument_type: instrumentType,
          status: "active",
          limit: "1000"
        });
        if (cursor) {
          params.set("cursor", cursor);
        }
        const data = await request("/v1/meridian/metadata/instruments?" + params.toString());
        if (data.error) {
          throw new Error(data.error.message || data.error.code || "Meridian metadata error");
        }
        const items = Array.isArray(data.data) ? data.data : [];
        pages.push(...items.map((item) => {
          const parsed = splitSecurityID(item.security_id || "");
          return {
            ...item,
            symbol: parsed.symbol,
            exchange: parsed.exchange
          };
        }));
        cursor = data.meta && data.meta.next_cursor ? String(data.meta.next_cursor) : "";
        if (!cursor) {
          break;
        }
      }
    }
    state.instrumentCache.set(cacheKey, pages);
    return pages;
  }

  function instrumentSuggestion(instrument) {
    const securityID = String(instrument.security_id || "");
    const parsed = splitSecurityID(securityID);
    return {
      security_id: securityID,
      symbol: parsed.symbol,
      exchange: parsed.exchange,
      name: instrument.name || "",
      instrument_type: instrument.instrument_type || "",
      status: instrument.status || "",
      trade_date: "",
      last: ""
    };
  }

  function localSymbolSuggestions(query) {
    const rows = []
      .concat(state.positions || [])
      .concat(state.orders || [])
      .concat(state.fills || []);
    return mergeSuggestions(rows.map((item) => {
      const symbol = normalizeSymbol(item.symbol);
      const exchange = String(item.exchange || inferExchange(symbol)).toUpperCase();
      return {
        security_id: symbol && exchange ? symbol + "." + exchange : "",
        symbol,
        exchange,
        name: item.name || "",
        instrument_type: item.instrument_type || "",
        status: "",
        trade_date: "",
        last: item.last_price || item.limit_price || item.price || ""
      };
    })).filter((item) => !query || item.symbol.startsWith(query) || item.security_id.startsWith(query));
  }

  function mergeSuggestions(...groups) {
    const merged = [];
    const seen = new Set();
    for (const group of groups) {
      for (const item of group || []) {
        if (!item || !item.security_id || seen.has(item.security_id)) {
          continue;
        }
        seen.add(item.security_id);
        merged.push(item);
      }
    }
    return merged.slice(0, 10);
  }

  function updateOrders(nextOrders) {
    const now = Date.now();
    for (const order of nextOrders) {
      const id = order.gateway_order_id || order.client_order_id || "";
      const signature = [
        order.status,
        order.gateway_status,
        order.cum_filled_qty,
        order.leaves_qty,
        order.last_updated_at,
        order.reject_message
      ].join("|");
      const previous = state.orderSignatures.get(id);
      if (previous && previous !== signature) {
        state.changedOrders.set(id, now);
        pushLog("info", "订单状态更新", id + " -> " + statusText(order.status));
      }
      state.orderSignatures.set(id, signature);
    }
    state.orders = nextOrders;
    if (!state.selectedOrderID && state.orders.length > 0) {
      state.selectedOrderID = state.orders[0].gateway_order_id;
    }
  }

  function renderAccounts() {
    els.accountTabs.innerHTML = "";
    els.orderAccount.innerHTML = "";
    if (state.accounts.length === 0) {
      els.accountTabs.innerHTML = '<button type="button" class="active">无账户</button>';
      return;
    }
    for (const account of state.accounts) {
      const tab = document.createElement("button");
      tab.type = "button";
      tab.className = account.account_id === state.activeAccount ? "active" : "";
      tab.textContent = account.account_id + (account.simulated ? " (模拟)" : "");
      tab.addEventListener("click", async () => {
        state.activeAccount = account.account_id;
        state.selectedOrderID = "";
        state.performanceLoaded = false;
        resetLedgerPages();
        renderAccounts();
        connectEventStream();
        await refreshNow();
        if (state.activeView === "performance") {
          await loadPerformance();
        }
      });
      els.accountTabs.appendChild(tab);

      const option = document.createElement("option");
      option.value = account.account_id;
      option.textContent = account.account_id;
      option.selected = account.account_id === state.activeAccount;
      els.orderAccount.appendChild(option);
    }
  }

  function renderMetrics() {
    const asset = state.asset || {};
    els.netAsset.textContent = formatNumber(asset.net_asset);
    els.cashAvailable.textContent = formatNumber(asset.cash_available);
    els.marketValue.textContent = formatNumber(asset.market_value);
    els.dayProfit.textContent = formatSigned(asset.day_profit);
    els.dayProfit.className = Number(asset.day_profit) < 0 ? "down" : "up";
    els.cashTotal.textContent = formatNumber(asset.cash_total);
    els.stockValue.textContent = formatNumber(asset.stock_value);
    els.fundValue.textContent = formatNumber(asset.fund_value);
    els.positionProfit.textContent = formatSigned(asset.position_profit);
    els.positionProfit.className = Number(asset.position_profit) < 0 ? "down" : "up";
    els.closeProfit.textContent = formatSigned(asset.close_profit);
    els.closeProfit.className = Number(asset.close_profit) < 0 ? "down" : "up";
    els.commission.textContent = formatNumber(asset.commission);
    els.availableCash.textContent = formatNumber(asset.cash_available);
    const price = Number(els.priceInput.value);
    const maxBuy = price > 0 ? Math.floor(Number(asset.cash_available || 0) / price / 100) * 100 : 0;
    els.maxBuy.textContent = maxBuy > 0 ? formatInt(maxBuy) : "--";
  }

  function formatSigned(value) {
    const number = Number(value);
    if (!Number.isFinite(number)) {
      return "--";
    }
    const prefix = number > 0 ? "+" : "";
    return prefix + formatNumber(number);
  }

  function renderPositions() {
    if (state.positions.length === 0) {
      els.positionsBody.innerHTML = '<tr><td colspan="6"><div class="empty-state">暂无 ' + escapeHTML(displayDate(selectedAssetTradeDateSafe())) + ' 持仓数据</div></td></tr>';
      renderPositionsPager();
      return;
    }
    els.positionsBody.innerHTML = state.positions.map((position) => {
      const pnl = Number(position.unrealized_pnl || 0);
      const pnlClass = pnl < 0 ? "down" : "up";
      const pnlRatio = position.market_value ? pnl / Number(position.market_value) * 100 : 0;
      return `
        <tr>
          <td><span class="row-title"><strong>${escapeHTML(symbolText(position))}</strong><span>${escapeHTML(position.name || "")}</span></span></td>
          <td class="num">${formatInt(position.quantity)}<br><span class="muted">${formatInt(position.sellable_qty)}</span></td>
          <td class="num">${formatPrice(position.avg_cost, position)}<br><span class="${Number(position.last_price) < Number(position.avg_cost) ? "down" : "up"}">${formatPrice(position.last_price, position)}</span></td>
          <td class="num">${formatNumber(position.market_value)}</td>
          <td class="num ${pnlClass}">${formatSigned(pnl)}<br>${formatSigned(pnlRatio)}%</td>
          <td><button type="button" class="row-action" data-sell-symbol="${escapeHTML(position.symbol)}" data-sell-exchange="${escapeHTML(position.exchange)}">卖出</button></td>
        </tr>`;
    }).join("");
    renderPositionsPager();
  }

  function renderMonitorSummary() {
    const terminalStatuses = new Set(["filled", "cancelled", "rejected"]);
    const activeOrders = state.orders.filter((order) => {
      const status = String(order.status || "").toLowerCase();
      return !order.is_terminal && !terminalStatuses.has(status);
    });
    els.orderCount.textContent = formatInt(state.orders.length);
    els.activeOrderCount.textContent = formatInt(activeOrders.length);
    els.fillCount.textContent = formatInt(state.fills.length);
    const latest = latestOrderOrFillTime();
    els.lastEventTime.textContent = latest ? formatTime(latest) : "--";
  }

  function latestOrderOrFillTime() {
    let latest = null;
    const note = (value) => {
      if (!value) {
        return;
      }
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) {
        return;
      }
      if (!latest || date > latest) {
        latest = date;
      }
    };
    for (const order of state.orders) {
      note(order.last_updated_at || order.terminal_at || order.accepted_at || order.created_at || order.inserted_at);
    }
    for (const fill of state.fills) {
      note(fill.matched_at);
    }
    return latest;
  }

  function renderBlotter() {
    for (const button of els.blotterTabs.querySelectorAll("button")) {
      button.classList.toggle("active", button.dataset.tab === state.selectedTab);
    }
    if (state.selectedTab === "orders") {
      renderOrdersTable();
      renderBlotterPager();
      return;
    }
    if (state.selectedTab === "fills") {
      renderFillsTable();
      renderBlotterPager();
      return;
    }
    if (state.selectedTab === "logs") {
      renderLogs();
      renderBlotterPager();
      return;
    }
    if (state.selectedTab === "raw") {
      els.blotterContent.innerHTML = '<pre class="raw-block">' + escapeHTML(JSON.stringify(state.lastPayload || {}, null, 2)) + "</pre>";
      renderBlotterPager();
      return;
    }
    els.blotterContent.innerHTML = '<div class="empty-state">撤单记录将在撤单查询接口完成后展示</div>';
    renderBlotterPager();
  }

  function renderOrdersTable() {
    if (state.orders.length === 0) {
      els.blotterContent.innerHTML = '<div class="empty-state">暂无 ' + escapeHTML(displayDate(selectedOrdersTradeDateSafe())) + ' 委托</div>';
      return;
    }
    const now = Date.now();
    els.blotterContent.innerHTML = `
      <table>
        <thead>
          <tr>
            <th>ReqID</th>
            <th>代码</th>
            <th>方向</th>
            <th class="num">委托价格</th>
            <th class="num">委托/成交</th>
            <th>柜台/交易所</th>
            <th>状态</th>
            <th>委托时间</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          ${state.orders.map((order) => {
            const id = order.gateway_order_id || order.client_order_id || "";
            const changedAt = state.changedOrders.get(id) || 0;
            const className = [
              id === state.selectedOrderID ? "selected" : "",
              now - changedAt < 3600 ? "flash" : ""
            ].join(" ");
            return `
              <tr class="${className}" data-order-id="${escapeHTML(id)}">
                <td><span class="row-title"><strong>${escapeHTML(order.client_order_id || id)}</strong><span>${escapeHTML(id)}</span></span></td>
                <td>${escapeHTML(symbolText(order))}</td>
                <td class="${order.trade_side === "S" ? "down" : "up"}">${sideText(order.trade_side)}</td>
                <td class="num">${formatPrice(order.limit_price, order)}</td>
                <td class="num">${formatInt(order.order_qty)} / ${formatInt(order.cum_filled_qty)}</td>
                <td><span class="row-title"><strong>${escapeHTML(order.order_id || "--")}</strong><span>${escapeHTML(order.order_stream_id || "--")}</span></span></td>
                <td><span class="status-badge ${escapeHTML(order.status)}">${statusText(order.status)}</span></td>
                <td>${formatTime(order.created_at || order.inserted_at)}</td>
                <td>${order.is_terminal ? '<span class="muted">已完成</span>' : '<button type="button" class="row-action" data-cancel-id="' + escapeHTML(id) + '">撤单</button>'}</td>
              </tr>`;
          }).join("")}
        </tbody>
      </table>`;
  }

  function renderFillsTable() {
    if (state.fills.length === 0) {
      els.blotterContent.innerHTML = '<div class="empty-state">暂无 ' + escapeHTML(displayDate(selectedOrdersTradeDateSafe())) + ' 成交</div>';
      return;
    }
    els.blotterContent.innerHTML = `
      <table>
        <thead>
          <tr>
            <th>成交编号</th>
            <th>ReqID</th>
            <th>柜台/交易所</th>
            <th>代码</th>
            <th>方向</th>
            <th class="num">成交价格</th>
            <th class="num">成交数量</th>
            <th>成交时间</th>
          </tr>
        </thead>
        <tbody>
          ${state.fills.map((fill) => {
            const order = orderForFill(fill);
            return `
              <tr>
                <td>${escapeHTML(fill.fill_id)}</td>
                <td><span class="row-title"><strong>${escapeHTML(order.client_order_id || "--")}</strong><span>${escapeHTML(fill.gateway_order_id)}</span></span></td>
                <td><span class="row-title"><strong>${escapeHTML(fill.order_id || order.order_id || "--")}</strong><span>${escapeHTML(fill.order_stream_id || order.order_stream_id || "--")}</span></span></td>
                <td>${escapeHTML(symbolText(fill))}</td>
                <td class="${fill.trade_side === "S" ? "down" : "up"}">${sideText(fill.trade_side)}</td>
                <td class="num">${formatPrice(fill.price, fill)}</td>
                <td class="num">${formatInt(fill.qty)}</td>
                <td>${formatTime(fill.matched_at)}</td>
              </tr>`;
          }).join("")}
        </tbody>
      </table>`;
  }

  function selectedOrdersTradeDateSafe() {
    try {
      return selectedOrdersTradeDate();
    } catch {
      return defaultLedgerDate();
    }
  }

  function selectedAssetTradeDateSafe() {
    try {
      return selectedAssetTradeDate();
    } catch {
      return defaultLedgerDate();
    }
  }

  function renderPositionsPager() {
    renderPager({
      page: state.positionsPage,
      count: state.positions.length,
      info: els.positionsPageInfo,
      prev: els.positionsPrevPage,
      next: els.positionsNextPage,
      label: "持仓",
      tradeDate: selectedAssetTradeDateSafe()
    });
  }

  function renderBlotterPager() {
    if (state.selectedTab === "orders") {
      renderPager({
        page: state.ordersPage,
        count: state.orders.length,
        info: els.ordersPageInfo,
        prev: els.ordersPrevPage,
        next: els.ordersNextPage,
        label: "委托",
        tradeDate: selectedOrdersTradeDateSafe()
      });
      return;
    }
    if (state.selectedTab === "fills") {
      renderPager({
        page: state.fillsPage,
        count: state.fills.length,
        info: els.ordersPageInfo,
        prev: els.ordersPrevPage,
        next: els.ordersNextPage,
        label: "成交",
        tradeDate: selectedOrdersTradeDateSafe()
      });
      return;
    }
    els.ordersPageInfo.textContent = state.selectedTab === "logs" ? "推送日志无分页" : "当前视图无分页";
    els.ordersPrevPage.disabled = true;
    els.ordersNextPage.disabled = true;
  }

  function renderPager(options) {
    const page = options.page;
    const start = options.count > 0 ? (page.page - 1) * page.pageSize + 1 : 0;
    const end = options.count > 0 ? start + options.count - 1 : 0;
    options.info.textContent = [
      displayDate(options.tradeDate),
      options.label,
      "第 " + page.page + " 页",
      options.count > 0 ? start + "-" + end : "0 条",
      page.next ? "还有下一页" : "已到末页"
    ].join(" · ");
    options.prev.disabled = page.previous.length === 0;
    options.next.disabled = !page.next;
  }

  function orderForFill(fill) {
    return state.orders.find((order) => order.gateway_order_id === fill.gateway_order_id) || {};
  }

  function renderLogs() {
    if (state.logs.length === 0) {
      els.blotterContent.innerHTML = '<div class="empty-state">暂无推送日志</div>';
      return;
    }
    els.blotterContent.innerHTML = '<ul class="log-list">' + state.logs.map((log) => `
      <li>[${formatTime(log.at)}] ${escapeHTML(log.level.toUpperCase())} ${escapeHTML(log.message)} ${escapeHTML(log.detail)}</li>
    `).join("") + "</ul>";
  }

  function renderDetail() {
    const order = state.orders.find((item) => item.gateway_order_id === state.selectedOrderID);
    if (!order) {
      els.detailSub.textContent = "请选择订单";
      els.timeline.innerHTML = '<div class="empty-state">暂无状态轨迹</div>';
      els.rawJson.textContent = "{}";
      els.executionList.textContent = "暂无成交执行记录...";
      return;
    }
    els.detailSub.textContent = "ReqID: " + (order.client_order_id || "--") + " · OID: " + order.gateway_order_id;
    const events = [
      ["下单指令生成", order.created_at || order.inserted_at],
      ["柜台受理", order.accepted_at],
      ["状态刷新 " + statusText(order.status), order.last_updated_at],
      order.terminal_at ? ["终态确认", order.terminal_at] : null
    ].filter(Boolean);
    els.timeline.innerHTML = events.map((item) => `
      <div class="timeline-item">
        <strong>${escapeHTML(item[0])}</strong>
        <span>${formatTime(item[1])}</span>
      </div>
    `).join("");
    els.rawJson.textContent = JSON.stringify(order, null, 2);
    const fills = state.fills.filter((fill) => fill.gateway_order_id === order.gateway_order_id);
    if (fills.length === 0) {
      els.executionList.textContent = "暂无成交执行记录...";
      return;
    }
    els.executionList.innerHTML = `
      <table>
        <thead><tr><th>成交编号</th><th>订单 ID</th><th class="num">价格</th><th class="num">数量</th></tr></thead>
        <tbody>${fills.map((fill) => `
          <tr>
            <td>${escapeHTML(fill.fill_id)}</td>
            <td><span class="row-title"><strong>${escapeHTML(fill.order_id || order.order_id || "--")}</strong><span>${escapeHTML(fill.order_stream_id || order.order_stream_id || "--")}</span></span></td>
            <td class="num">${formatPrice(fill.price, fill)}</td>
            <td class="num">${formatInt(fill.qty)}</td>
          </tr>
        `).join("")}</tbody>
      </table>`;
  }

  function defaultQueryDate() {
    const snapshotDate = state.marketSnapshot && state.marketSnapshot.trade_date;
    const snapshotDigits = compactDate(snapshotDate);
    if (snapshotDigits) {
      return snapshotDigits;
    }
    const latestPoint = state.performanceSeries[state.performanceSeries.length - 1];
    const latestDigits = compactDate(latestPoint && latestPoint.trade_date);
    if (latestDigits) {
      return latestDigits;
    }
    const screenDate = compactDate(els.tradeDate.textContent);
    if (screenDate) {
      return screenDate;
    }
    return new Date().toISOString().slice(0, 10).replace(/-/g, "");
  }

  function ensurePerformanceDefaults() {
    const day = defaultQueryDate();
    if (!els.perfDateFrom.value) {
      els.perfDateFrom.value = day;
    }
    if (!els.perfDateTo.value) {
      els.perfDateTo.value = day;
    }
    if (!els.barTradeDateInput.value) {
      els.barTradeDateInput.value = els.perfDateTo.value || day;
    }
    if (!els.barSecurityInput.value) {
      els.barSecurityInput.value = currentSecurityID() || "600000.SH";
    }
  }

  function performanceParams() {
    ensurePerformanceDefaults();
    const dateFrom = compactDate(els.perfDateFrom.value);
    const dateTo = compactDate(els.perfDateTo.value || els.perfDateFrom.value);
    if (!dateFrom || !dateTo) {
      throw new Error("请输入 YYYYMMDD 或 YYYY-MM-DD 日期");
    }
    if (dateFrom > dateTo) {
      throw new Error("起始日不能晚于结束日");
    }
    return { dateFrom, dateTo };
  }

  async function loadPerformance() {
    if (!state.activeAccount) {
      renderPerformance();
      return;
    }
    const params = performanceParams();
    const accountID = encodeURIComponent(state.activeAccount);
    const query = new URLSearchParams({
      date_from: params.dateFrom,
      date_to: params.dateTo
    });
    els.performanceStatus.textContent = "查询中...";
    els.loadPerformanceButton.disabled = true;
    try {
      const data = await request("/v1/accounts/" + accountID + "/performance/series?" + query.toString());
      state.performanceError = "";
      state.performanceSummary = data.summary || null;
      state.performanceSeries = Array.isArray(data.series) ? data.series : [];
      state.performanceDaily = state.performanceSeries[state.performanceSeries.length - 1] || null;
      const dailyDate = compactDate(state.performanceDaily && state.performanceDaily.trade_date) || params.dateTo;
      try {
        const dailyData = await request("/v1/accounts/" + accountID + "/performance/daily?trade_date=" + encodeURIComponent(dailyDate));
        state.performanceDaily = dailyData.performance || state.performanceDaily;
      } catch (err) {
        pushLog("warn", "日终快照读取失败", displayDate(dailyDate) + " " + err.message);
      }
      state.performanceLoaded = true;
      renderPerformance();
      showToast("绩效数据已更新");
    } catch (err) {
      state.performanceLoaded = false;
      state.performanceError = err.message;
      pushLog("error", "绩效查询失败", err.message);
      showToast("绩效查询失败：" + err.message, "error");
      renderPerformance();
    } finally {
      els.loadPerformanceButton.disabled = false;
    }
  }

  function downloadPerformanceCSV() {
    if (!state.activeAccount) {
      return;
    }
    let params;
    try {
      params = performanceParams();
    } catch (err) {
      showToast(err.message, "error");
      return;
    }
    const accountID = encodeURIComponent(state.activeAccount);
    const query = new URLSearchParams({
      date_from: params.dateFrom,
      date_to: params.dateTo
    });
    window.open("/v1/accounts/" + accountID + "/performance/series.csv?" + query.toString(), "_blank", "noopener");
  }

  function renderPerformance() {
    if (!els.performanceSeriesBody) {
      return;
    }
    const summary = state.performanceSummary || {};
    const series = state.performanceSeries || [];
    const latest = series[series.length - 1] || state.performanceDaily || {};
    const daily = state.performanceDaily || latest || {};
    els.performanceRangeHint.textContent = [
      state.activeAccount || "未选择账户",
      summary.date_from && summary.date_to ? displayDate(summary.date_from) + " 至 " + displayDate(summary.date_to) : "close 快照序列",
      "Asia/Shanghai"
    ].filter(Boolean).join(" · ");
    els.perfNetAsset.textContent = formatNumber(summary.end_net_asset ?? latest.net_asset);
    els.perfStartNetAsset.textContent = "期初 " + formatNumber(summary.start_net_asset);
    els.perfTotalPnl.textContent = formatSigned(summary.total_pnl);
    els.perfTotalPnl.className = classForNumber(summary.total_pnl);
    els.perfRows.textContent = "样本 " + formatInt(summary.count);
    els.perfTotalReturn.textContent = formatPercent(summary.total_return);
    els.perfTotalReturn.className = classForNumber(summary.total_return);
    els.perfDailyReturn.textContent = "当日 " + formatPercent(daily.return_rate);
    els.perfMaxDrawdown.textContent = formatPercent(summary.max_drawdown);
    els.perfMaxDrawdown.className = classForNumber(summary.max_drawdown);
    els.perfDailyPnl.textContent = "当日 " + formatSigned(daily.daily_pnl);
    els.perfDailyDate.textContent = daily.trade_date ? displayDate(daily.trade_date) : "--";
    els.perfPositions.textContent = formatInt(daily.positions_count);
    els.perfPositionValue.textContent = formatNumber(daily.position_market_value);
    els.perfUnrealizedPnl.textContent = formatSigned(daily.unrealized_pnl);
    els.perfUnrealizedPnl.className = classForNumber(daily.unrealized_pnl);
    els.perfFills.textContent = formatInt(daily.fills_count);
    els.perfTurnover.textContent = formatNumber(daily.turnover);
    els.perfFee.textContent = formatNumber(daily.fee_total);
    els.perfCapturedAt.textContent = "captured_at " + (daily.captured_at || "--");
    els.performanceStatus.textContent = state.performanceError
      ? "查询失败：" + state.performanceError
      : (state.performanceLoaded ? "已加载 " + formatInt(series.length) + " 条" : "等待查询");
    if (series.length === 0) {
      els.performanceSeriesBody.innerHTML = '<tr><td colspan="9"><div class="empty-state">暂无 close 快照绩效序列</div></td></tr>';
      return;
    }
    els.performanceSeriesBody.innerHTML = series.map((item) => `
      <tr>
        <td>${escapeHTML(displayDate(item.trade_date))}</td>
        <td class="num">${formatNumber(item.net_asset)}</td>
        <td class="num ${classForNumber(item.daily_pnl)}">${formatSigned(item.daily_pnl)}</td>
        <td class="num ${classForNumber(item.return_rate)}">${formatPercent(item.return_rate)}</td>
        <td class="num ${classForNumber(item.cumulative_return)}">${formatPercent(item.cumulative_return)}</td>
        <td class="num ${classForNumber(item.drawdown)}">${formatPercent(item.drawdown)}</td>
        <td class="num">${formatNumber(item.turnover)}</td>
        <td class="num">${formatNumber(item.fee_total)}</td>
        <td>${escapeHTML(shortDateTime(item.captured_at))}</td>
      </tr>
    `).join("");
  }

  async function loadBars() {
    ensurePerformanceDefaults();
    const securityID = normalizeSecurityID(els.barSecurityInput.value || currentSecurityID());
    const tradeDate = compactDate(els.barTradeDateInput.value || els.perfDateTo.value);
    if (!securityID || !tradeDate) {
      throw new Error("请输入 bars 标的和交易日");
    }
    const query = new URLSearchParams({
      security_id: securityID,
      trade_date: tradeDate,
      frequency: els.barFrequencyInput.value || "1m",
      adjustment: els.barAdjustmentInput.value || "none",
      limit: "20"
    });
    const startTime = String(els.barStartTimeInput.value || "").trim();
    const endTime = String(els.barEndTimeInput.value || "").trim();
    if (startTime) {
      query.set("start_time", startTime);
    }
    if (endTime) {
      query.set("end_time", endTime);
    }
    els.barsStatus.textContent = "查询中...";
    els.loadBarsButton.disabled = true;
    try {
      const data = await request("/v1/meridian/market/bars?" + query.toString());
      if (data.error) {
        throw new Error(data.error.message || data.error.code || "Meridian bars error");
      }
      state.barsError = "";
      state.barsRows = Array.isArray(data.data) ? data.data : [];
      state.barsMeta = data.meta || null;
      state.barsLoaded = true;
      els.barSecurityInput.value = securityID;
      els.barTradeDateInput.value = tradeDate;
      renderBars();
      showToast("Bars 数据已更新");
    } catch (err) {
      state.barsLoaded = false;
      state.barsError = err.message;
      pushLog("warn", "Bars 查询失败", securityID + " " + err.message);
      showToast("Bars 查询失败：" + err.message, "error");
      renderBars();
    } finally {
      els.loadBarsButton.disabled = false;
    }
  }

  function renderBars() {
    if (!els.barsBody) {
      return;
    }
    const rows = state.barsRows || [];
    const meta = state.barsMeta || {};
    const latest = rows[rows.length - 1] || {};
    els.barsStatus.textContent = state.barsError ? "查询失败：" + state.barsError : (meta.schema_version || latest.schema_version || "market_bar.v1");
    els.barClose.textContent = formatPrice(latest.close, latest);
    els.barVolume.textContent = formatInt(latest.volume);
    els.barCount.textContent = formatInt(meta.count ?? rows.length);
    els.barTime.textContent = latest.datetime ? shortDateTime(latest.datetime) : "--";
    if (rows.length === 0) {
      els.barsBody.innerHTML = '<tr><td colspan="6"><div class="empty-state">暂无 Meridian bars 数据</div></td></tr>';
      return;
    }
    els.barsBody.innerHTML = rows.map((row) => `
      <tr>
        <td>${escapeHTML(shortDateTime(row.datetime || row.trade_date))}</td>
        <td class="num">${formatPrice(row.open, row)}</td>
        <td class="num">${formatPrice(row.high, row)}</td>
        <td class="num">${formatPrice(row.low, row)}</td>
        <td class="num">${formatPrice(row.close, row)}</td>
        <td class="num">${formatInt(row.volume)}</td>
      </tr>
    `).join("");
  }

  function shortDateTime(value) {
    if (!value) {
      return "--";
    }
    const text = String(value);
    if (/^\d{8}$/.test(text)) {
      return displayDate(text);
    }
    return text.replace("T", " ").replace(/\.\d+/, "").replace(/([+-]\d{2}:\d{2}|Z)$/, "");
  }

  function renderQuote() {
    const snapshot = state.marketSnapshot || {};
    const securityID = snapshot.security_id || currentSecurityID() || "--";
    const last = Number(snapshot.last);
    const preClose = Number(snapshot.pre_close);
    const change = Number.isFinite(last) && Number.isFinite(preClose) ? last - preClose : NaN;
    const pct = Number.isFinite(change) && preClose !== 0 ? change / preClose * 100 : NaN;
    els.quoteSymbol.textContent = securityID;
    els.quoteName.textContent = snapshot.instrument_type || "--";
    els.quoteSource.textContent = [
      snapshot.market_level,
      snapshot.data_scope,
      snapshot.trade_date,
      snapshot.source_dataset || snapshot.source
    ].filter(Boolean).join(" · ") || "Meridian";
    applyPriceInputPrecision(snapshot);
    els.quoteLast.textContent = formatPrice(snapshot.last, snapshot);
    els.quoteChange.textContent = Number.isFinite(change) && Number.isFinite(pct)
      ? formatSignedPrice(change, snapshot) + " / " + formatSigned(pct) + "%"
      : "-- / --";
    els.quotePrice.classList.toggle("down", change < 0);
    els.quotePrice.classList.toggle("flat", !Number.isFinite(change) || change === 0);
  }

  function renderDepthBook() {
    const snapshot = state.marketSnapshot || {};
    const asks = Array.isArray(snapshot.asks) ? snapshot.asks.slice(0, 5).reverse() : [];
    const bids = Array.isArray(snapshot.bids) ? snapshot.bids.slice(0, 5) : [];
    if (asks.length === 0 && bids.length === 0) {
      els.depthBook.innerHTML = '<div class="empty-state">等待 Meridian 快照...</div>';
      return;
    }
    els.depthBook.innerHTML = asks.map((row, idx) => depthRow(row, "sell", idx === asks.length - 1 ? "best-ask" : "")).join("") +
      bids.map((row, idx) => depthRow(row, "buy", idx === 0 ? "best-bid" : "")).join("");
  }

  function depthRow(row, side, extra) {
    const label = (side === "sell" ? "卖 " : "买 ") + (row.level || "");
    return `<div class="depth-row ${side} ${extra}"><span>${escapeHTML(label)}</span><strong>${formatPrice(row.price, state.marketSnapshot)}</strong><span class="qty">${formatInt(row.volume)}</span></div>`;
  }

  function renderSymbolSuggestions() {
    if (state.symbolSuggestions.length === 0) {
      els.symbolSuggest.classList.remove("open");
      els.symbolInput.setAttribute("aria-expanded", "false");
      els.symbolSuggest.innerHTML = "";
      return;
    }
    els.symbolInput.setAttribute("aria-expanded", "true");
    els.symbolSuggest.classList.add("open");
    els.symbolSuggest.innerHTML = state.symbolSuggestions.map((item, index) => `
      <button type="button" class="symbol-option ${index === state.activeSuggestion ? "active" : ""}" role="option" data-index="${index}">
        <strong>${escapeHTML(item.security_id)}</strong>
        <span>${escapeHTML(item.name || item.instrument_type || "")}</span>
        <em>${escapeHTML(item.status || item.trade_date || "")}</em>
      </button>
    `).join("");
  }

  function selectSuggestion(index) {
    const item = state.symbolSuggestions[index];
    if (!item) {
      return;
    }
    setSymbolFromSecurityID(item.security_id);
    hideSuggestions();
    state.priceEdited = false;
    applyPriceInputPrecision(item);
    loadQuoteForInput({ securityID: item.security_id }).catch((err) => pushLog("warn", "行情刷新失败", err.message));
  }

  function hideSuggestions() {
    state.symbolSuggestions = [];
    state.activeSuggestion = -1;
    renderSymbolSuggestions();
  }

  function renderAll() {
    renderAccounts();
    renderQuote();
    renderDepthBook();
    renderMetrics();
    renderPositions();
    renderMonitorSummary();
    renderBlotter();
    renderDetail();
    renderPerformance();
    renderBars();
    updateRisk();
  }

  function syncClock(serverTime) {
    const date = serverTime ? new Date(serverTime) : new Date();
    const day = displayDate(businessDateCompact(date));
    const time = date.toLocaleTimeString("zh-CN", { hour12: false, timeZone: "Asia/Shanghai" });
    els.tradeDate.textContent = day;
    els.serverClock.textContent = time;
    els.footerClock.textContent = time;
  }

  function updateClock() {
    syncClock();
  }

  function updateSide(side) {
    state.side = side;
    for (const button of document.querySelectorAll(".side-switch button")) {
      button.classList.toggle("active", button.dataset.side === side);
    }
    els.submitOrderButton.textContent = side === "S" ? "卖出下单" : "买入下单";
    els.submitOrderButton.classList.toggle("sell", side === "S");
    els.submitOrderButton.classList.toggle("buy", side !== "S");
    applyQuotePrice();
    updateRisk();
  }

  function applyQuotePrice() {
    if (state.priceEdited) {
      return;
    }
    const price = quoteOrderPrice();
    if (Number.isFinite(price) && price > 0) {
      els.priceInput.value = price.toFixed(priceDigitsForItem(state.marketSnapshot));
    }
  }

  function quoteOrderPrice() {
    const snapshot = state.marketSnapshot || {};
    const bids = Array.isArray(snapshot.bids) ? snapshot.bids : [];
    const asks = Array.isArray(snapshot.asks) ? snapshot.asks : [];
    const best = state.side === "S" ? bids[0] : asks[0];
    const bestPrice = Number(best && best.price);
    if (Number.isFinite(bestPrice) && bestPrice > 0) {
      return bestPrice;
    }
    const last = Number(snapshot.last);
    return Number.isFinite(last) ? last : NaN;
  }

  function updateRisk() {
    const qty = Number(els.qtyInput.value);
    const price = Number(els.priceInput.value);
    const cash = Number((state.asset && state.asset.cash_available) || 0);
    const amount = qty * price;
    if (!Number.isFinite(qty) || qty <= 0 || qty % 100 !== 0) {
      els.riskAlert.textContent = "风控提示：A 股测试下单数量应为 100 股整数倍。";
      return;
    }
    if (state.side === "B" && amount > cash) {
      els.riskAlert.textContent = "风控警报：委托金额超过当前可用资金。";
      return;
    }
    els.riskAlert.textContent = "风控提示：测试账户指令将写入测试 Redis。";
  }

  async function submitOrder(event) {
    event.preventDefault();
    const accountID = els.orderAccount.value || state.activeAccount;
    const body = {
      account_id: accountID,
      client_order_id: "manual-" + Date.now(),
      symbol: els.symbolInput.value.trim(),
      exchange: els.exchangeInput.value,
      trade_side: state.side,
      business_type: "S",
      offset_type: "O",
      price: Number(els.priceInput.value),
      qty: Number.parseInt(els.qtyInput.value, 10)
    };
    try {
      els.submitOrderButton.disabled = true;
      const data = await request("/v1/orders", { method: "POST", body });
      pushLog("info", "下单已提交", data.order && data.order.gateway_order_id);
      showToast("下单已提交 " + formatTime(new Date()));
      if (data.order && data.order.gateway_order_id) {
        state.selectedOrderID = data.order.gateway_order_id;
      }
      await refreshNow();
    } catch (err) {
      pushLog("error", "下单失败", err.message);
      showToast("下单失败：" + err.message, "error");
    } finally {
      els.submitOrderButton.disabled = false;
    }
  }

  async function cancelOrder(gatewayOrderID) {
    if (!gatewayOrderID) {
      return;
    }
    try {
      const accountID = state.activeAccount;
      const data = await request("/v1/orders/" + encodeURIComponent(gatewayOrderID) + "/cancel", {
        method: "POST",
        body: {
          account_id: accountID,
          gateway_order_id: gatewayOrderID,
          cancel_id: "cancel-" + Date.now()
        }
      });
      pushLog("info", "撤单已提交", data.cancel_id || gatewayOrderID);
      showToast("撤单已提交 " + formatTime(new Date()));
      await refreshNow();
    } catch (err) {
      pushLog("error", "撤单失败", err.message);
      showToast("撤单失败：" + err.message, "error");
    }
  }

  async function refreshAccountResource(kind) {
    if (!state.activeAccount) {
      return;
    }
    const path = "/v1/accounts/" + encodeURIComponent(state.activeAccount) + "/" + kind + "/refresh";
    const labels = {
      asset: "资金",
      positions: "持仓",
      orders: "委托",
      fills: "成交"
    };
    const label = labels[kind] || kind;
    try {
      const data = await request(path, { method: "POST" });
      pushLog("info", label + "刷新指令已发送", data.stream_id || "");
      showToast(label + "刷新指令已发送");
    } catch (err) {
      pushLog("error", "刷新指令失败", err.message);
      showToast("刷新失败：" + err.message, "error");
    }
  }

  async function refreshNow() {
    await loadStatus();
    await loadAccountData();
  }

  async function queryOrdersForDate() {
    try {
      selectedOrdersTradeDate();
      resetPage(state.ordersPage);
      resetPage(state.fillsPage);
      const [ordersResult, fillsResult] = await Promise.allSettled([
        fetchOrdersPage(),
        fetchFillsPage()
      ]);
      if (ordersResult.status === "fulfilled") {
        state.ordersPage.next = ordersResult.value.next_cursor || "";
        updateOrders(ordersResult.value.orders || []);
      } else {
        throw ordersResult.reason;
      }
      if (fillsResult.status === "fulfilled") {
        state.fillsPage.next = fillsResult.value.next_cursor || "";
        state.fills = fillsResult.value.fills || [];
      } else {
        pushLog("warn", "成交读取失败", fillsResult.reason.message);
      }
      renderMonitorSummary();
      renderBlotter();
      renderDetail();
      showToast("订单监控已更新");
    } catch (err) {
      showToast(err.message, "error");
    }
  }

  async function queryPositionsForDate() {
    try {
      selectedAssetTradeDate();
      resetPage(state.positionsPage);
      await loadPositionsOnly();
      showToast("资金持仓已更新");
    } catch (err) {
      showToast(err.message, "error");
    }
  }

  async function gotoPage(page, direction, loader) {
    if (direction === "next") {
      if (!page.next) {
        return;
      }
      page.previous.push(page.cursor || "");
      page.cursor = page.next;
      page.next = "";
      page.page += 1;
    } else {
      if (page.previous.length === 0) {
        return;
      }
      page.cursor = page.previous.pop() || "";
      page.next = "";
      page.page = Math.max(1, page.page - 1);
    }
    try {
      await loader();
    } catch (err) {
      pushLog("error", "分页查询失败", err.message);
      showToast("分页查询失败：" + err.message, "error");
    }
  }

  async function loadOrdersOnly() {
    const data = await fetchOrdersPage();
    state.ordersPage.next = data.next_cursor || "";
    updateOrders(data.orders || []);
    renderMonitorSummary();
    renderBlotter();
    renderDetail();
  }

  async function loadFillsOnly() {
    const data = await fetchFillsPage();
    state.fillsPage.next = data.next_cursor || "";
    state.fills = data.fills || [];
    renderMonitorSummary();
    renderBlotter();
    renderDetail();
  }

  async function loadPositionsOnly() {
    const [assetResult, positionsResult] = await Promise.allSettled([
      fetchAssetForSelectedDate(),
      fetchPositionsPage()
    ]);
    if (assetResult.status === "fulfilled") {
      state.asset = assetResult.value;
    } else {
      state.asset = null;
      pushLog("warn", "资金读取失败", assetResult.reason.message);
    }
    if (positionsResult.status === "fulfilled") {
      state.positions = positionsResult.value.positions || [];
      state.positionsPage.next = positionsResult.value.next_cursor || "";
    } else {
      throw positionsResult.reason;
    }
    renderMetrics();
    renderPositions();
    updateRisk();
  }

  function bindEvents() {
    let symbolTimer = 0;
    let quoteTimer = 0;
    for (const link of els.viewLinks) {
      link.addEventListener("click", (event) => {
        event.preventDefault();
        navigateView(link.dataset.viewLink || "trade");
      });
    }
    window.addEventListener("hashchange", () => setActiveView(viewFromLocation()));
    window.addEventListener("popstate", () => setActiveView(viewFromLocation()));
    els.orderAccount.addEventListener("change", async () => {
      state.activeAccount = els.orderAccount.value;
      state.performanceLoaded = false;
      state.selectedOrderID = "";
      resetLedgerPages();
      connectEventStream();
      await refreshNow();
      if (state.activeView === "performance") {
        await loadPerformance();
      }
    });
    for (const button of document.querySelectorAll(".side-switch button")) {
      button.addEventListener("click", () => updateSide(button.dataset.side));
    }
    els.priceInput.addEventListener("input", () => {
      state.priceEdited = true;
      updateRisk();
    });
    els.qtyInput.addEventListener("input", updateRisk);
    els.symbolInput.addEventListener("input", () => {
      const normalized = normalizeSymbol(els.symbolInput.value);
      els.symbolInput.value = normalized.replace(/\..*$/, "");
      if (els.symbolInput.value.length >= 1) {
        els.exchangeInput.value = inferExchange(els.symbolInput.value);
      }
      window.clearTimeout(symbolTimer);
      window.clearTimeout(quoteTimer);
      symbolTimer = window.setTimeout(loadSymbolSuggestions, 220);
      if (els.symbolInput.value.length === 6) {
        state.priceEdited = false;
        quoteTimer = window.setTimeout(() => loadQuoteForInput().catch((err) => pushLog("warn", "行情刷新失败", err.message)), 320);
      }
    });
    els.symbolInput.addEventListener("keydown", (event) => {
      if (!els.symbolSuggest.classList.contains("open")) {
        return;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        state.activeSuggestion = Math.min(state.activeSuggestion + 1, state.symbolSuggestions.length - 1);
        renderSymbolSuggestions();
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        state.activeSuggestion = Math.max(state.activeSuggestion - 1, 0);
        renderSymbolSuggestions();
      } else if (event.key === "Enter") {
        if (state.activeSuggestion >= 0) {
          event.preventDefault();
          selectSuggestion(state.activeSuggestion);
        }
      } else if (event.key === "Escape") {
        hideSuggestions();
      }
    });
    els.symbolInput.addEventListener("blur", () => {
      window.setTimeout(hideSuggestions, 120);
    });
    els.symbolSuggest.addEventListener("mousedown", (event) => {
      event.preventDefault();
      const button = event.target.closest("button[data-index]");
      if (button) {
        selectSuggestion(Number(button.dataset.index));
      }
    });
    els.exchangeInput.addEventListener("change", () => {
      state.priceEdited = false;
      loadQuoteForInput().catch((err) => pushLog("warn", "行情刷新失败", err.message));
    });
    els.orderForm.addEventListener("submit", submitOrder);
    els.resetOrderButton.addEventListener("click", () => {
      els.symbolInput.value = "600000";
      els.exchangeInput.value = "SH";
      els.qtyInput.value = "100";
      state.priceEdited = false;
      updateSide("B");
      loadQuoteForInput().catch((err) => pushLog("warn", "行情刷新失败", err.message));
    });
    els.refreshAssetButton.addEventListener("click", () => refreshAccountResource("asset"));
    els.refreshPositionsButton.addEventListener("click", () => refreshAccountResource("positions"));
    els.refreshOrdersButton.addEventListener("click", () => refreshAccountResource("orders"));
    els.refreshFillsButton.addEventListener("click", () => refreshAccountResource("fills"));
    els.queryAssetButton.addEventListener("click", queryPositionsForDate);
    els.queryOrdersButton.addEventListener("click", queryOrdersForDate);
    els.assetTradeDate.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        queryPositionsForDate();
      }
    });
    els.ordersTradeDate.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        queryOrdersForDate();
      }
    });
    els.positionsPrevPage.addEventListener("click", () => gotoPage(state.positionsPage, "prev", loadPositionsOnly));
    els.positionsNextPage.addEventListener("click", () => gotoPage(state.positionsPage, "next", loadPositionsOnly));
    els.ordersPrevPage.addEventListener("click", () => {
      const page = state.selectedTab === "fills" ? state.fillsPage : state.ordersPage;
      const loader = state.selectedTab === "fills" ? loadFillsOnly : loadOrdersOnly;
      gotoPage(page, "prev", loader);
    });
    els.ordersNextPage.addEventListener("click", () => {
      const page = state.selectedTab === "fills" ? state.fillsPage : state.ordersPage;
      const loader = state.selectedTab === "fills" ? loadFillsOnly : loadOrdersOnly;
      gotoPage(page, "next", loader);
    });
    els.loadPerformanceButton.addEventListener("click", () => loadPerformance().catch((err) => showToast(err.message, "error")));
    els.downloadPerformanceButton.addEventListener("click", downloadPerformanceCSV);
    els.loadBarsButton.addEventListener("click", () => loadBars().catch((err) => showToast(err.message, "error")));
    els.barSecurityInput.addEventListener("blur", () => {
      const securityID = normalizeSecurityID(els.barSecurityInput.value);
      if (securityID) {
        els.barSecurityInput.value = securityID;
      }
    });
    els.blotterTabs.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-tab]");
      if (!button) {
        return;
      }
      state.selectedTab = button.dataset.tab;
      renderBlotter();
    });
    els.blotterContent.addEventListener("click", (event) => {
      const row = event.target.closest("tr[data-order-id]");
      if (row) {
        state.selectedOrderID = row.dataset.orderId;
        renderBlotter();
        renderDetail();
      }
      const cancelButton = event.target.closest("button[data-cancel-id]");
      if (cancelButton) {
        event.stopPropagation();
        cancelOrder(cancelButton.dataset.cancelId);
      }
    });
    els.positionsBody.addEventListener("click", (event) => {
      const button = event.target.closest("button[data-sell-symbol]");
      if (!button) {
        return;
      }
      els.symbolInput.value = button.dataset.sellSymbol || "";
      els.exchangeInput.value = button.dataset.sellExchange || "SH";
      state.priceEdited = false;
      navigateView("trade");
      updateSide("S");
      loadQuoteForInput().catch((err) => pushLog("warn", "行情刷新失败", err.message));
    });
    els.closeDetailButton.addEventListener("click", () => {
      state.selectedOrderID = "";
      renderDetail();
      renderBlotter();
    });
  }

  async function boot() {
    setActiveView(viewFromLocation());
    renderQuote();
    renderDepthBook();
    bindEvents();
    updateClock();
    setInterval(updateClock, 1000);
    try {
      await loadStatus();
      await loadAccounts();
      connectEventStream();
      await loadAccountData();
      await loadQuoteForInput();
      ensurePerformanceDefaults();
      state.initialized = true;
      if (state.activeView === "performance") {
        await loadPerformance();
        await loadBars();
      }
      pushLog("info", "交易终端初始化完成");
    } catch (err) {
      state.initialized = true;
      pushLog("error", "初始化失败", err.message);
      showToast("初始化失败：" + err.message, "error");
      renderAll();
    }
    window.setInterval(() => {
      refreshNow().catch((err) => pushLog("error", "轮询刷新失败", err.message));
    }, 3000);
    window.setInterval(() => {
      loadQuoteForInput().catch((err) => pushLog("warn", "行情轮询失败", err.message));
    }, 5000);
    window.addEventListener("beforeunload", closeEventStream);
  }

  boot();
})();
