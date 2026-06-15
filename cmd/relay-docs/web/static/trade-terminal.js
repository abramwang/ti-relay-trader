(() => {
  const state = {
    accounts: [],
    activeAccount: "",
    asset: null,
    positions: [],
    allPositions: [],
    allPositionsAccount: "",
    allPositionsLoadedDate: "",
    positionStatsDirty: true,
    positionStatsSeq: 0,
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
    barsSecurityID: "",
    barsTradeDate: "",
    barsLoaded: false,
    barsError: "",
    defaultTradeDate: "",
    defaultTradeDateSource: "",
    lastDefaultTradeDate: "",
    chartOrders: [],
    chartFills: [],
    minuteChart: null,
    initialized: false,
    eventSource: null,
    eventSourceAccount: "",
    streamConnected: false,
    positionQuotes: new Map(),
    positionQuoteStreams: [],
    positionQuoteStreamKey: "",
    positionQuoteLive: false,
    positionQuoteStreamErrorAt: 0,
    streamRefreshTimer: 0,
    chartMarkerRefreshTimer: 0,
    chartLoadTimer: 0,
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
    perfBenchmarkInput: byID("perfBenchmarkInput"),
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
    perfBenchmarkReturn: byID("perfBenchmarkReturn"),
    perfBenchmarkID: byID("perfBenchmarkID"),
    perfExcessReturn: byID("perfExcessReturn"),
    perfBenchmarkDays: byID("perfBenchmarkDays"),
    performanceStatus: byID("performanceStatus"),
    performanceSeriesBody: byID("performanceSeriesBody"),
    minuteChart: byID("minuteChart"),
    minuteChartStatus: byID("minuteChartStatus"),
    chartTradeDateInput: byID("chartTradeDateInput"),
    reloadChartButton: byID("reloadChartButton"),
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
    if (value === null || value === undefined || value === "") {
      return "--";
    }
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

  function currentBusinessDate() {
    return businessDateCompact();
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
    return terminalDefaultDate();
  }

  function isCurrentBusinessDate(value) {
    const date = compactDate(value);
    return date !== "" && date === currentBusinessDate();
  }

  function terminalDefaultDate() {
    return compactDate(state.defaultTradeDate) ||
      compactDate(state.marketSnapshot && state.marketSnapshot.trade_date) ||
      compactDate(els.tradeDate.textContent) ||
      currentBusinessDate();
  }

  function shouldReplaceDefaultDateInput(input, previousDefault) {
    if (!input) {
      return false;
    }
    const value = compactDate(input.value);
    if (!value) {
      return true;
    }
    return value === previousDefault || value === currentBusinessDate();
  }

  function applyDefaultDateInput(input, nextDate, previousDefault) {
    if (!shouldReplaceDefaultDateInput(input, previousDefault)) {
      return false;
    }
    if (input.value !== nextDate) {
      input.value = nextDate;
      return true;
    }
    return false;
  }

  function setTerminalDefaultDate(value, source, options = {}) {
    const nextDate = compactDate(value);
    if (!nextDate) {
      return { changed: false, ledgerChanged: false, chartChanged: false, performanceChanged: false };
    }
    const previousDefault = compactDate(state.defaultTradeDate) ||
      compactDate(state.lastDefaultTradeDate) ||
      currentBusinessDate();
    const changed = compactDate(state.defaultTradeDate) !== nextDate;
    state.lastDefaultTradeDate = previousDefault;
    state.defaultTradeDate = nextDate;
    state.defaultTradeDateSource = source || state.defaultTradeDateSource || "terminal";
    const result = {
      changed,
      ledgerChanged: false,
      chartChanged: false,
      performanceChanged: false
    };
    if (!options.applyToInputs) {
      return result;
    }
    result.ledgerChanged = [
      applyDefaultDateInput(els.ordersTradeDate, nextDate, previousDefault),
      applyDefaultDateInput(els.assetTradeDate, nextDate, previousDefault)
    ].some(Boolean);
    result.chartChanged = [
      applyDefaultDateInput(els.chartTradeDateInput, nextDate, previousDefault),
      applyDefaultDateInput(els.barTradeDateInput, nextDate, previousDefault)
    ].some(Boolean);
    result.performanceChanged = [
      applyDefaultDateInput(els.perfDateFrom, nextDate, previousDefault),
      applyDefaultDateInput(els.perfDateTo, nextDate, previousDefault)
    ].some(Boolean);
    return result;
  }

  function maybeAdoptMarketDefaultDate(value, source, requestedDate = "") {
    const nextDate = compactDate(value);
    if (!nextDate) {
      return { changed: false, ledgerChanged: false, chartChanged: false, performanceChanged: false };
    }
    const requested = compactDate(requestedDate);
    const current = currentBusinessDate();
    const previousDefault = compactDate(state.defaultTradeDate) || current;
    if (requested && requested !== current && requested !== previousDefault) {
      return { changed: false, ledgerChanged: false, chartChanged: false, performanceChanged: false };
    }
    return setTerminalDefaultDate(nextDate, source, { applyToInputs: true });
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
    if (value === null || value === undefined || value === "") {
      return "--";
    }
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
    if (view === "trade" && state.initialized) {
      ensureChartDefaults();
      const securityID = currentSecurityID();
      const tradeDate = currentChartTradeDate();
      if (!state.barsLoaded || !barsMatch(securityID, tradeDate)) {
        loadTradeChartBars({ silent: true }).catch((err) => pushLog("warn", "K线查询失败", err.message));
      } else {
        window.setTimeout(() => {
          refreshMinuteChartMarkers()
            .catch((err) => pushLog("warn", "图表买卖点刷新失败", err.message))
            .finally(() => {
              renderMinuteChart();
              resizeMinuteChart();
            });
        }, 0);
      }
    }
    if (view === "performance" && state.initialized) {
      ensurePerformanceDefaults();
      if (state.activeAccount && !state.performanceLoaded) {
        loadPerformance().catch((err) => pushLog("warn", "绩效查询失败", err.message));
      }
      if (!state.barsLoaded) {
        loadBars().catch((err) => pushLog("warn", "Bars 查询失败", err.message));
      } else {
        window.setTimeout(() => {
          refreshMinuteChartMarkers()
            .catch((err) => pushLog("warn", "图表买卖点刷新失败", err.message))
            .finally(() => {
              renderBars();
              resizeMinuteChart();
            });
        }, 0);
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
    window.clearTimeout(state.chartMarkerRefreshTimer);
    updateStreamFooter();
  }

  function closeTerminalStreams() {
    closeEventStream();
    closePositionQuoteStreams();
  }

  function closePositionQuoteStreams() {
    for (const source of state.positionQuoteStreams) {
      source.close();
    }
    state.positionQuoteStreams = [];
    state.positionQuoteStreamKey = "";
    state.positionQuoteLive = false;
  }

  function resetPositionStats() {
    closePositionQuoteStreams();
    state.positionStatsSeq += 1;
    state.allPositions = [];
    state.allPositionsAccount = "";
    state.allPositionsLoadedDate = "";
    state.positionStatsDirty = true;
    state.positionQuotes.clear();
  }

  function markPositionStatsDirty() {
    state.positionStatsDirty = true;
  }

  function uniquePositionSecurityIDs(positions) {
    const ids = [];
    const seen = new Set();
    for (const position of positions || []) {
      const securityID = itemSecurityID(position);
      if (!securityID || seen.has(securityID)) {
        continue;
      }
      seen.add(securityID);
      ids.push(securityID);
    }
    return ids;
  }

  function refreshPositionQuoteStreams() {
    const tradeDate = selectedAssetTradeDateSafe();
    if (!window.EventSource || !state.activeAccount || !isCurrentBusinessDate(tradeDate)) {
      closePositionQuoteStreams();
      return;
    }
    if (state.allPositionsAccount !== state.activeAccount || state.allPositionsLoadedDate !== tradeDate) {
      closePositionQuoteStreams();
      return;
    }
    const securityIDs = uniquePositionSecurityIDs(state.allPositions);
    if (securityIDs.length === 0) {
      closePositionQuoteStreams();
      return;
    }
    const streamKey = state.activeAccount + "|" + tradeDate + "|" + securityIDs.join(",");
    if (state.positionQuoteStreamKey === streamKey && state.positionQuoteStreams.length > 0) {
      return;
    }
    closePositionQuoteStreams();
    state.positionQuoteStreamKey = streamKey;

    const chunkSize = 200;
    for (let offset = 0; offset < securityIDs.length; offset += chunkSize) {
      const chunk = securityIDs.slice(offset, offset + chunkSize);
      const params = new URLSearchParams({
        security_ids: chunk.join(","),
        trade_date: tradeDate,
        market_level: "level1",
        include_existing: "true",
        watch_interval_ms: "1000"
      });
      const source = new EventSource("/v1/meridian/stream/market/snapshots?" + params.toString());
      source.addEventListener("open", () => {
        state.positionQuoteLive = true;
      });
      source.addEventListener("market_snapshots", handlePositionQuoteEvent);
      source.onerror = () => {
        state.positionQuoteLive = false;
        const now = Date.now();
        if (now - state.positionQuoteStreamErrorAt > 10000) {
          state.positionQuoteStreamErrorAt = now;
          pushLog("warn", "持仓行情流重连中", "Meridian level1 SSE");
        }
      };
      state.positionQuoteStreams.push(source);
    }
  }

  function handlePositionQuoteEvent(event) {
    let payload;
    try {
      payload = JSON.parse(event.data || "{}");
    } catch (err) {
      pushLog("warn", "持仓行情解析失败", err.message);
      return;
    }
    const rows = Array.isArray(payload.data) ? payload.data : [];
    let changed = false;
    for (const row of rows) {
      const securityID = itemSecurityID(row);
      if (!securityID) {
        continue;
      }
      state.positionQuotes.set(securityID, row);
      changed = true;
    }
    if (changed) {
      renderMetrics();
      renderPositions();
    }
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
    if (type === "asset.changed" || type === "positions.changed" || type === "fill.changed") {
      markPositionStatsDirty();
    }
    scheduleStreamRefresh();
    scheduleChartMarkerRefresh(type);
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

  function scheduleChartMarkerRefresh(type) {
    if (type !== "order.changed" && type !== "fill.changed") {
      return;
    }
    if (state.activeView !== "trade" || !state.barsLoaded || !state.activeAccount) {
      return;
    }
    window.clearTimeout(state.chartMarkerRefreshTimer);
    state.chartMarkerRefreshTimer = window.setTimeout(async () => {
      try {
        await refreshMinuteChartMarkers();
      } catch (err) {
        pushLog("warn", "图表买卖点刷新失败", err.message);
      }
    }, 350);
  }

  function scheduleTradeChartLoad(delay = 360) {
    window.clearTimeout(state.chartLoadTimer);
    if (!state.initialized || state.activeView !== "trade") {
      return;
    }
    state.chartLoadTimer = window.setTimeout(() => {
      loadTradeChartBars({ silent: true }).catch((err) => pushLog("warn", "K线查询失败", err.message));
    }, delay);
  }

  async function refreshMinuteChartMarkers() {
    const securityID = normalizeSecurityID(state.barsSecurityID || currentSecurityID());
    const tradeDate = effectiveBarsTradeDate(state.barsTradeDate || currentChartTradeDate());
    if (!securityID || !tradeDate) {
      return;
    }
    await loadChartMarkers(securityID, tradeDate);
    renderMinuteChart();
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

    try {
      await refreshPositionStatsSource();
    } catch (err) {
      state.positionStatsDirty = true;
      pushLog("warn", "全量持仓统计读取失败", err.message);
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

  async function refreshPositionStatsSource(options = {}) {
    const tradeDate = selectedAssetTradeDateSafe();
    const accountID = state.activeAccount;
    const force = Boolean(options.force);
    if (!accountID || !isCurrentBusinessDate(tradeDate)) {
      closePositionQuoteStreams();
      state.allPositions = [];
      state.allPositionsAccount = accountID || "";
      state.allPositionsLoadedDate = tradeDate;
      state.positionStatsDirty = false;
      return;
    }
    if (!force &&
      !state.positionStatsDirty &&
      state.allPositionsAccount === accountID &&
      state.allPositionsLoadedDate === tradeDate) {
      refreshPositionQuoteStreams();
      return;
    }

    const seq = ++state.positionStatsSeq;
    const allPositions = [];
    let cursor = "";
    for (let page = 0; page < 20; page += 1) {
      const params = new URLSearchParams({ limit: "2000" });
      if (cursor) {
        params.set("cursor", cursor);
      }
      const data = await request("/v1/accounts/" + encodeURIComponent(accountID) + "/positions?" + params.toString());
      allPositions.push(...(data.positions || []));
      cursor = data.next_cursor || "";
      if (!cursor) {
        break;
      }
    }
    if (cursor) {
      pushLog("warn", "全量持仓统计超过前端查询上限", "已读取 " + formatInt(allPositions.length) + " 条");
    }
    if (seq !== state.positionStatsSeq) {
      return;
    }
    state.allPositions = allPositions;
    state.allPositionsAccount = accountID;
    state.allPositionsLoadedDate = tradeDate;
    state.positionStatsDirty = false;
    refreshPositionQuoteStreams();
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
      const adopted = maybeAdoptMarketDefaultDate(state.marketSnapshot && state.marketSnapshot.trade_date, "snapshot");
      renderQuote();
      renderDepthBook();
      applyQuotePrice();
      if (adopted.ledgerChanged && state.initialized && state.activeAccount) {
        resetLedgerPages();
        loadAccountData().catch((err) => pushLog("warn", "交易日默认值刷新失败", err.message));
      }
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
        resetPositionStats();
        renderAccounts();
        connectEventStream();
        await refreshNow();
        if (state.activeView === "trade") {
          await loadTradeChartBars({ silent: true });
        }
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
    const liveTotals = livePortfolioTotals();
    const marketValue = liveTotals ? liveTotals.marketValue : asset.market_value;
    const positionProfit = liveTotals ? liveTotals.positionProfit : asset.position_profit;
    const netAsset = liveTotals ? liveTotals.netAsset : asset.net_asset;
    els.netAsset.textContent = formatNumber(netAsset);
    els.cashAvailable.textContent = formatNumber(asset.cash_available);
    els.marketValue.textContent = formatNumber(marketValue);
    els.dayProfit.textContent = formatSigned(asset.day_profit);
    els.dayProfit.className = Number(asset.day_profit) < 0 ? "down" : "up";
    els.cashTotal.textContent = formatNumber(asset.cash_total);
    els.stockValue.textContent = formatNumber(asset.stock_value);
    els.fundValue.textContent = formatNumber(asset.fund_value);
    els.positionProfit.textContent = formatSigned(positionProfit);
    els.positionProfit.className = Number(positionProfit) < 0 ? "down" : "up";
    els.closeProfit.textContent = formatSigned(asset.close_profit);
    els.closeProfit.className = Number(asset.close_profit) < 0 ? "down" : "up";
    els.commission.textContent = formatNumber(asset.commission);
    els.availableCash.textContent = formatNumber(asset.cash_available);
    const price = Number(els.priceInput.value);
    const maxBuy = price > 0 ? Math.floor(Number(asset.cash_available || 0) / price / 100) * 100 : 0;
    els.maxBuy.textContent = maxBuy > 0 ? formatInt(maxBuy) : "--";
  }

  function formatSigned(value) {
    if (value === null || value === undefined || value === "") {
      return "--";
    }
    const number = Number(value);
    if (!Number.isFinite(number)) {
      return "--";
    }
    const prefix = number > 0 ? "+" : "";
    return prefix + formatNumber(number);
  }

  function quoteForPosition(position) {
    const securityID = itemSecurityID(position);
    if (!securityID) {
      return null;
    }
    return state.positionQuotes.get(securityID) || null;
  }

  function finiteNumber(value) {
    if (value === null || value === undefined || value === "") {
      return null;
    }
    const number = Number(value);
    return Number.isFinite(number) ? number : null;
  }

  function livePositionView(position) {
    const quote = quoteForPosition(position);
    const qty = finiteNumber(position.quantity);
    const avgCost = finiteNumber(position.avg_cost);
    const quotedLast = finiteNumber(quote && quote.last);
    const ledgerLast = finiteNumber(position.last_price);
    const price = quotedLast !== null && quotedLast > 0 ? quotedLast : ledgerLast;
    const ledgerMarketValue = finiteNumber(position.market_value);
    const marketValue = qty !== null && price !== null ? qty * price : ledgerMarketValue;
    const costAmount = qty !== null && avgCost !== null ? qty * avgCost : null;
    const ledgerPnl = finiteNumber(position.unrealized_pnl);
    let pnl = ledgerPnl;
    if (marketValue !== null && costAmount !== null) {
      pnl = marketValue - costAmount;
    }
    let pnlRatio = null;
    if (pnl !== null && costAmount !== null && costAmount !== 0) {
      pnlRatio = pnl / costAmount * 100;
    }
    return {
      quote,
      quoteItem: quote || position,
      price,
      marketValue,
      pnl,
      pnlRatio
    };
  }

  function livePortfolioTotals() {
    const tradeDate = selectedAssetTradeDateSafe();
    if (!isCurrentBusinessDate(tradeDate) ||
      state.positionStatsDirty ||
      state.allPositionsAccount !== state.activeAccount ||
      state.allPositionsLoadedDate !== tradeDate) {
      return null;
    }
    let marketValue = 0;
    let positionProfit = 0;
    for (const position of state.allPositions) {
      const view = livePositionView(position);
      const rowMarketValue = finiteNumber(view.marketValue);
      const rowPnl = finiteNumber(view.pnl);
      if (rowMarketValue !== null) {
        marketValue += rowMarketValue;
      }
      if (rowPnl !== null) {
        positionProfit += rowPnl;
      }
    }
    const cashTotal = finiteNumber(state.asset && state.asset.cash_total);
    return {
      marketValue,
      positionProfit,
      netAsset: cashTotal !== null ? cashTotal + marketValue : (state.asset && state.asset.net_asset)
    };
  }

  function renderPositions() {
    if (state.positions.length === 0) {
      els.positionsBody.innerHTML = '<tr><td colspan="6"><div class="empty-state">暂无 ' + escapeHTML(displayDate(selectedAssetTradeDateSafe())) + ' 持仓数据</div></td></tr>';
      renderPositionsPager();
      return;
    }
    els.positionsBody.innerHTML = state.positions.map((position) => {
      const view = livePositionView(position);
      const pnl = view.pnl;
      const pnlClass = Number(pnl) < 0 ? "down" : "up";
      const pnlRatio = view.pnlRatio;
      const avgCost = finiteNumber(position.avg_cost);
      const priceClass = view.price !== null && avgCost !== null && view.price < avgCost ? "down" : "up";
      const pnlRatioText = pnlRatio === null ? "--" : formatSigned(pnlRatio) + "%";
      return `
        <tr>
          <td><span class="row-title"><strong>${escapeHTML(symbolText(position))}</strong><span>${escapeHTML(position.name || "")}</span></span></td>
          <td class="num">${formatInt(position.quantity)}<br><span class="muted">${formatInt(position.sellable_qty)}</span></td>
          <td class="num">${formatPrice(position.avg_cost, view.quoteItem)}<br><span class="${priceClass}">${formatPrice(view.price, view.quoteItem)}</span></td>
          <td class="num">${formatNumber(view.marketValue)}</td>
          <td class="num ${pnlClass}">${formatSigned(pnl)}<br>${pnlRatioText}</td>
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
            <th>错误/柜台信息</th>
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
            const debugText = orderDebugText(order);
            return `
              <tr class="${className}" data-order-id="${escapeHTML(id)}">
                <td><span class="row-title"><strong>${escapeHTML(order.client_order_id || id)}</strong><span>${escapeHTML(id)}</span></span></td>
                <td>${escapeHTML(symbolText(order))}</td>
                <td class="${order.trade_side === "S" ? "down" : "up"}">${sideText(order.trade_side)}</td>
                <td class="num">${formatPrice(order.limit_price, order)}</td>
                <td class="num">${formatInt(order.order_qty)} / ${formatInt(order.cum_filled_qty)}</td>
                <td><span class="row-title"><strong>${escapeHTML(order.order_id || "--")}</strong><span>${escapeHTML(order.order_stream_id || "--")}</span></span></td>
                <td><span class="status-badge ${escapeHTML(order.status)}">${statusText(order.status)}</span></td>
                <td class="debug-cell"><span class="row-title"><strong class="${debugText ? "down" : "muted"}">${escapeHTML(debugText || "--")}</strong><span>${escapeHTML(order.reject_code || adapterText(order, "relay_error_code") || "")}</span></span></td>
                <td>${formatTime(order.created_at || order.inserted_at)}</td>
                <td>${order.is_terminal ? '<span class="muted">已完成</span>' : '<button type="button" class="row-action" data-cancel-id="' + escapeHTML(id) + '">撤单</button>'}</td>
              </tr>`;
          }).join("")}
        </tbody>
      </table>`;
  }

  function orderDebugText(order) {
    return firstText(
      order.reject_message,
      adapterText(order, "relay_error_message"),
      adapterText(order, "error_message"),
      adapterText(order, "error_msg"),
      adapterText(order, "err_msg"),
      adapterText(order, "error_text"),
      adapterText(order, "status_msg"),
      adapterText(order, "status_message"),
      adapterText(order, "broker_status_text")
    );
  }

  function adapterText(item, key) {
    const context = item && item.adapter_context;
    if (!context || !Object.prototype.hasOwnProperty.call(context, key)) {
      return "";
    }
    const value = context[key];
    if (value === null || value === undefined) {
      return "";
    }
    return String(value).trim();
  }

  function firstText(...values) {
    for (const value of values) {
      const text = String(value || "").trim();
      if (text) {
        return text;
      }
    }
    return "";
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
    const debugText = orderDebugText(order);
    const events = [
      ["下单指令生成", order.created_at || order.inserted_at],
      ["柜台受理", order.accepted_at],
      ["状态刷新 " + statusText(order.status), order.last_updated_at],
      debugText ? ["柜台/前置信息：" + debugText, order.last_updated_at || order.terminal_at] : null,
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
    const terminalDate = compactDate(state.defaultTradeDate);
    if (terminalDate) {
      return terminalDate;
    }
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
    return currentBusinessDate();
  }

  function ensurePerformanceDefaults() {
    const day = defaultQueryDate();
    if (!els.perfDateFrom.value) {
      els.perfDateFrom.value = day;
    }
    if (!els.perfDateTo.value) {
      els.perfDateTo.value = day;
    }
    if (els.perfBenchmarkInput && !els.perfBenchmarkInput.value) {
      els.perfBenchmarkInput.value = "000300.SH";
    }
    if (!els.barTradeDateInput.value) {
      els.barTradeDateInput.value = els.perfDateTo.value || day;
    }
    if (!els.barSecurityInput.value) {
      els.barSecurityInput.value = currentSecurityID() || "600000.SH";
    }
    ensureChartDefaults();
  }

  function ensureChartDefaults() {
    if (!els.chartTradeDateInput) {
      return;
    }
    if (!els.chartTradeDateInput.value) {
      els.chartTradeDateInput.value = defaultQueryDate();
    }
  }

  function currentChartTradeDate() {
    ensureChartDefaults();
    return compactDate(els.chartTradeDateInput && els.chartTradeDateInput.value) || defaultQueryDate();
  }

  function syncBarsInputs(securityID, tradeDate) {
    if (els.chartTradeDateInput && tradeDate) {
      els.chartTradeDateInput.value = tradeDate;
    }
    if (els.barSecurityInput && securityID) {
      els.barSecurityInput.value = securityID;
    }
    if (els.barTradeDateInput && tradeDate) {
      els.barTradeDateInput.value = tradeDate;
    }
  }

  function barsMatch(securityID, tradeDate) {
    const loadedSecurityID = normalizeSecurityID(state.barsSecurityID || "");
    const requestedSecurityID = normalizeSecurityID(securityID || "");
    const loadedDate = compactDateLoose(state.barsTradeDate);
    const requestedDate = compactDateLoose(tradeDate);
    return Boolean(loadedSecurityID && requestedSecurityID && loadedSecurityID === requestedSecurityID && loadedDate && requestedDate && loadedDate === requestedDate);
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
    const benchmarkSecurityID = normalizeSecurityID(els.perfBenchmarkInput && els.perfBenchmarkInput.value);
    return { dateFrom, dateTo, benchmarkSecurityID };
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
    if (params.benchmarkSecurityID) {
      query.set("benchmark_security_id", params.benchmarkSecurityID);
    }
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
    if (params.benchmarkSecurityID) {
      query.set("benchmark_security_id", params.benchmarkSecurityID);
    }
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
      summary.benchmark_security_id ? "基准 " + summary.benchmark_security_id : "",
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
    els.perfBenchmarkReturn.textContent = formatPercent(summary.benchmark_total_return);
    els.perfBenchmarkReturn.className = classForNumber(summary.benchmark_total_return);
    els.perfBenchmarkID.textContent = "基准 " + (summary.benchmark_security_id || "--");
    els.perfExcessReturn.textContent = formatPercent(summary.excess_total_return);
    els.perfExcessReturn.className = classForNumber(summary.excess_total_return);
    els.perfBenchmarkDays.textContent = "bars " + formatInt(summary.benchmark_observation_days);
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
      els.performanceSeriesBody.innerHTML = '<tr><td colspan="11"><div class="empty-state">暂无 close 快照绩效序列</div></td></tr>';
      return;
    }
    els.performanceSeriesBody.innerHTML = series.map((item) => `
      <tr>
        <td>${escapeHTML(displayDate(item.trade_date))}</td>
        <td class="num">${formatNumber(item.net_asset)}</td>
        <td class="num ${classForNumber(item.daily_pnl)}">${formatSigned(item.daily_pnl)}</td>
        <td class="num ${classForNumber(item.return_rate)}">${formatPercent(item.return_rate)}</td>
        <td class="num ${classForNumber(item.cumulative_return)}">${formatPercent(item.cumulative_return)}</td>
        <td class="num ${classForNumber(item.benchmark_cumulative_return)}">${formatPercent(item.benchmark_cumulative_return)}</td>
        <td class="num ${classForNumber(item.excess_cumulative_return)}">${formatPercent(item.excess_cumulative_return)}</td>
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
    return loadBarsFor({
      securityID,
      tradeDate,
      frequency: els.barFrequencyInput.value || "1m",
      adjustment: els.barAdjustmentInput.value || "none",
      startTime: String(els.barStartTimeInput.value || "").trim(),
      endTime: String(els.barEndTimeInput.value || "").trim(),
      source: "performance",
      silent: false
    });
  }

  async function loadTradeChartBars(options = {}) {
    ensureChartDefaults();
    const securityID = normalizeSecurityID(options.securityID || currentSecurityID());
    const tradeDate = compactDate(options.tradeDate || currentChartTradeDate());
    if (!securityID || !tradeDate) {
      throw new Error("请输入 K 线标的和交易日");
    }
    return loadBarsFor({
      securityID,
      tradeDate,
      frequency: "1m",
      adjustment: "none",
      startTime: "09:30:00",
      endTime: "15:00:00",
      source: "trade",
      silent: options.silent !== false
    });
  }

  async function loadBarsFor(options) {
    const securityID = normalizeSecurityID(options.securityID || currentSecurityID());
    const tradeDate = compactDate(options.tradeDate || defaultQueryDate());
    if (!securityID || !tradeDate) {
      throw new Error("请输入 bars 标的和交易日");
    }
    const query = new URLSearchParams({
      security_id: securityID,
      trade_date: tradeDate,
      frequency: options.frequency || "1m",
      adjustment: options.adjustment || "none",
      limit: "300"
    });
    if (options.startTime) {
      query.set("start_time", options.startTime);
    }
    if (options.endTime) {
      query.set("end_time", options.endTime);
    }
    if (options.source === "performance") {
      els.barsStatus.textContent = "查询中...";
      els.loadBarsButton.disabled = true;
    } else if (els.minuteChartStatus) {
      els.minuteChartStatus.textContent = securityID + " · K线查询中...";
    }
    try {
      const data = await request("/v1/meridian/market/bars?" + query.toString());
      if (data.error) {
        throw new Error(data.error.message || data.error.code || "Meridian bars error");
      }
      state.barsError = "";
      state.barsRows = Array.isArray(data.data) ? data.data : [];
      state.barsMeta = data.meta || null;
      state.barsLoaded = true;
      const effectiveTradeDate = effectiveBarsTradeDate(tradeDate);
      state.barsSecurityID = securityID;
      state.barsTradeDate = effectiveTradeDate;
      const adopted = maybeAdoptMarketDefaultDate(effectiveTradeDate, "bars", tradeDate);
      syncBarsInputs(securityID, effectiveTradeDate);
      if (adopted.ledgerChanged && state.initialized && state.activeAccount) {
        resetLedgerPages();
        resetPositionStats();
        loadAccountData().catch((err) => pushLog("warn", "交易日默认值刷新失败", err.message));
      }
      try {
        await loadChartMarkers(securityID, effectiveTradeDate);
      } catch (markerErr) {
        state.chartOrders = [];
        state.chartFills = [];
        pushLog("warn", "图表买卖点读取失败", markerErr.message);
      }
      renderBars();
      if (!options.silent) {
        showToast("Bars 数据已更新");
      }
    } catch (err) {
      state.barsLoaded = false;
      state.barsError = err.message;
      state.barsSecurityID = securityID;
      state.barsTradeDate = tradeDate;
      pushLog("warn", "Bars 查询失败", securityID + " " + err.message);
      if (!options.silent) {
        showToast("Bars 查询失败：" + err.message, "error");
      }
      renderBars();
    } finally {
      if (options.source === "performance") {
        els.loadBarsButton.disabled = false;
      }
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
      renderMinuteChart();
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
    renderMinuteChart();
  }

  function effectiveBarsTradeDate(fallback) {
    const rows = state.barsRows || [];
    const meta = state.barsMeta || {};
    const rowDate = rows.length > 0 ? compactDateLoose(rows[0].trade_date || rows[0].datetime) : "";
    return rowDate || compactDateLoose(meta.trade_date) || compactDateLoose(fallback);
  }

  async function loadChartMarkers(securityID, tradeDate) {
    if (!state.activeAccount || !securityID || !tradeDate) {
      state.chartOrders = [];
      state.chartFills = [];
      return;
    }
    const parsed = splitSecurityID(securityID);
    const [orders, fills] = await Promise.all([
      fetchChartLedger("/v1/history/orders", "orders", parsed, tradeDate),
      fetchChartLedger("/v1/history/fills", "fills", parsed, tradeDate)
    ]);
    state.chartOrders = orders;
    state.chartFills = fills;
  }

  async function fetchChartLedger(path, key, parsed, tradeDate) {
    const rows = [];
    let cursor = "";
    for (let page = 0; page < 6; page += 1) {
      const params = new URLSearchParams({
        account_id: state.activeAccount,
        trade_date: tradeDate,
        symbol: parsed.symbol,
        exchange: parsed.exchange,
        limit: "500"
      });
      if (cursor) {
        params.set("cursor", cursor);
      }
      const data = await request(path + "?" + params.toString());
      rows.push(...(Array.isArray(data[key]) ? data[key] : []));
      cursor = data.next_cursor || "";
      if (!cursor) {
        break;
      }
    }
    return rows;
  }

  function renderMinuteChart() {
    if (!els.minuteChart || !els.minuteChartStatus) {
      return;
    }
    if (state.activeView !== "trade") {
      return;
    }
    const rows = state.barsRows || [];
    const chart = ensureMinuteChart();
    if (!chart) {
      return;
    }
    if (rows.length === 0) {
      chart.clear();
      els.minuteChartStatus.textContent = state.barsError ? "分钟线读取失败" : "暂无分钟线";
      return;
    }
    const chartRows = rows.slice().sort(compareBarRows);
    const labels = chartRows.map((row) => minuteLabel(row.datetime || row.trade_date));
    const candles = chartRows.map(candleValues);
    const volumes = chartRows.map((row) => ({
      value: numericOrNull(row.volume) || 0,
      itemStyle: { color: isUpBar(row) ? "rgba(200,16,46,0.52)" : "rgba(0,138,99,0.52)" }
    }));
    const markers = chartMarkers(labels);
    const latest = chartRows[chartRows.length - 1] || {};
    const tradeDate = effectiveBarsTradeDate(state.barsTradeDate || currentChartTradeDate());
    const securityID = normalizeSecurityID(state.barsSecurityID || currentSecurityID());
    els.minuteChartStatus.textContent = [
      securityID,
      displayDate(tradeDate),
      rows.length + " 条",
      "买 " + markers.buy.length,
      "卖 " + markers.sell.length
    ].join(" · ");
    chart.setOption({
      animation: false,
      color: ["#c8102e", "#008a63", "#667085"],
      grid: [
        { left: 58, right: 18, top: 28, height: "58%" },
        { left: 58, right: 18, bottom: 36, height: "18%" }
      ],
      tooltip: {
        trigger: "axis",
        axisPointer: { type: "cross" },
        formatter: (params) => minuteTooltip(params, chartRows)
      },
      legend: {
        top: 0,
        right: 8,
        itemWidth: 12,
        itemHeight: 8,
        textStyle: { color: "#475467", fontSize: 11 },
        data: ["K线", "成交量", "买点", "卖点"]
      },
      axisPointer: {
        link: [{ xAxisIndex: [0, 1] }]
      },
      xAxis: [
        {
          type: "category",
          data: labels,
          boundaryGap: true,
          axisLabel: { color: "#667085", fontSize: 11 },
          axisLine: { lineStyle: { color: "#cfd7e3" } }
        },
        {
          type: "category",
          gridIndex: 1,
          data: labels,
          boundaryGap: true,
          axisLabel: { show: false },
          axisTick: { show: false },
          axisLine: { lineStyle: { color: "#cfd7e3" } }
        }
      ],
      yAxis: [
        {
          type: "value",
          scale: true,
          axisLabel: { color: "#667085", fontSize: 11, formatter: (value) => formatPrice(value, latest) },
          splitLine: { lineStyle: { color: "#e4e9f0" } }
        },
        {
          type: "value",
          gridIndex: 1,
          scale: true,
          axisLabel: { color: "#667085", fontSize: 10, formatter: formatCompactVolume },
          splitNumber: 2,
          splitLine: { lineStyle: { color: "#edf1f6" } }
        }
      ],
      dataZoom: [
        { type: "inside", xAxisIndex: [0, 1], throttle: 60 },
        { type: "slider", xAxisIndex: [0, 1], height: 18, bottom: 8, borderColor: "#cfd7e3" }
      ],
      series: [
        {
          name: "K线",
          type: "candlestick",
          data: candles,
          itemStyle: {
            color: "#c8102e",
            color0: "#008a63",
            borderColor: "#a10d24",
            borderColor0: "#006b4d"
          }
        },
        {
          name: "成交量",
          type: "bar",
          xAxisIndex: 1,
          yAxisIndex: 1,
          data: volumes,
          barMaxWidth: 8
        },
        {
          name: "买点",
          type: "scatter",
          xAxisIndex: 0,
          yAxisIndex: 0,
          data: markers.buy,
          symbol: "triangle",
          symbolSize: 12,
          itemStyle: { color: "#c8102e", borderColor: "#fff", borderWidth: 1 }
        },
        {
          name: "卖点",
          type: "scatter",
          xAxisIndex: 0,
          yAxisIndex: 0,
          data: markers.sell,
          symbol: "triangle",
          symbolRotate: 180,
          symbolSize: 12,
          itemStyle: { color: "#008a63", borderColor: "#fff", borderWidth: 1 }
        }
      ]
    }, true);
  }

  function ensureMinuteChart() {
    if (!window.echarts) {
      els.minuteChartStatus.textContent = "ECharts 未加载";
      return null;
    }
    if (!state.minuteChart) {
      state.minuteChart = window.echarts.init(els.minuteChart, null, { renderer: "canvas" });
    }
    return state.minuteChart;
  }

  function resizeMinuteChart() {
    if (state.minuteChart) {
      state.minuteChart.resize();
    }
  }

  function chartMarkers(labels) {
    const fillOrderIDs = new Set();
    const buy = [];
    const sell = [];
    const appendMarker = (side, label, price, meta) => {
      if (!Number.isFinite(price) || price <= 0 || !label) {
        return;
      }
      const snapped = nearestChartLabel(label, labels);
      if (!snapped) {
        return;
      }
      const marker = { value: [snapped, price], meta };
      if (side === "S") {
        sell.push(marker);
      } else {
        buy.push(marker);
      }
    };

    for (const fill of state.chartFills || []) {
      const id = fill.gateway_order_id || "";
      if (id) {
        fillOrderIDs.add(id);
      }
      appendMarker(fill.trade_side, minuteLabel(fill.matched_at || fill.match_timestamp), Number(fill.price), {
        kind: "成交",
        id: fill.fill_id || id,
        qty: fill.qty,
        price: fill.price,
        status: "filled"
      });
    }
    for (const order of state.chartOrders || []) {
      const id = order.gateway_order_id || order.client_order_id || "";
      if (id && fillOrderIDs.has(id)) {
        continue;
      }
      appendMarker(order.trade_side, minuteLabel(order.created_at || order.inserted_at || order.accepted_at || order.last_updated_at), Number(order.limit_price), {
        kind: "委托",
        id,
        qty: order.order_qty,
        price: order.limit_price,
        status: statusText(order.status)
      });
    }
    return { buy, sell };
  }

  function minuteTooltip(params, rows) {
    const items = Array.isArray(params) ? params : [params];
    const label = items[0] && items[0].axisValueLabel ? items[0].axisValueLabel : "";
    const rowIndex = rows.findIndex((row) => minuteLabel(row.datetime || row.trade_date) === label);
    const row = rowIndex >= 0 ? rows[rowIndex] : {};
    const priceItem = row && row.instrument_type ? row : (rows[0] || {});
    const lines = [
      escapeHTML(label),
      "开 " + escapeHTML(formatPrice(row.open, row)) + " / 收 " + escapeHTML(formatPrice(row.close, row)),
      "高 " + escapeHTML(formatPrice(row.high, row)) + " / 低 " + escapeHTML(formatPrice(row.low, row)),
      "量 " + escapeHTML(formatInt(row.volume))
    ];
    for (const item of items) {
      const meta = item && item.data && item.data.meta;
      if (!meta) {
        continue;
      }
      lines.push([
        escapeHTML(meta.kind || item.seriesName),
        escapeHTML(meta.id || ""),
        escapeHTML(meta.status || ""),
        "价 " + escapeHTML(formatPrice(meta.price, priceItem)),
        "量 " + escapeHTML(formatInt(meta.qty))
      ].filter(Boolean).join(" · "));
    }
    return lines.join("<br>");
  }

  function numericOrNull(value) {
    const number = Number(value);
    return Number.isFinite(number) ? number : null;
  }

  function numericOrFallback(value, fallback) {
    const number = Number(value);
    return Number.isFinite(number) ? number : fallback;
  }

  function candleValues(row) {
    const close = numericOrNull(row.close);
    return [
      numericOrFallback(row.open, close),
      close,
      numericOrFallback(row.low, close),
      numericOrFallback(row.high, close)
    ];
  }

  function isUpBar(row) {
    const open = Number(row.open);
    const close = Number(row.close);
    if (!Number.isFinite(open) || !Number.isFinite(close)) {
      return true;
    }
    return close >= open;
  }

  function formatCompactVolume(value) {
    const number = Number(value);
    if (!Number.isFinite(number)) {
      return "--";
    }
    if (Math.abs(number) >= 100000000) {
      return (number / 100000000).toFixed(1) + "亿";
    }
    if (Math.abs(number) >= 10000) {
      return (number / 10000).toFixed(1) + "万";
    }
    return String(Math.round(number));
  }

  function compareBarRows(left, right) {
    return barRowTimeValue(left) - barRowTimeValue(right);
  }

  function barRowTimeValue(row) {
    const raw = row && (row.datetime || row.trade_date);
    const parsed = raw ? Date.parse(raw) : NaN;
    if (Number.isFinite(parsed)) {
      return parsed;
    }
    const minutes = minutesOfDay(minuteLabel(raw));
    return Number.isFinite(minutes) ? minutes : 0;
  }

  function minuteLabel(value) {
    if (!value) {
      return "";
    }
    if (typeof value === "number") {
      const timestamp = value > 1000000000000 ? value : value * 1000;
      return new Date(timestamp).toLocaleTimeString("zh-CN", {
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
        timeZone: "Asia/Shanghai"
      });
    }
    const text = String(value);
    const match = text.match(/(\d{2}):(\d{2})(?::\d{2})?/);
    if (match) {
      return match[1] + ":" + match[2];
    }
    return "";
  }

  function nearestChartLabel(label, labels) {
    if (labels.includes(label)) {
      return label;
    }
    const target = minutesOfDay(label);
    if (!Number.isFinite(target)) {
      return "";
    }
    let best = "";
    let bestDistance = Infinity;
    for (const candidate of labels) {
      const value = minutesOfDay(candidate);
      const distance = Math.abs(value - target);
      if (distance < bestDistance) {
        best = candidate;
        bestDistance = distance;
      }
    }
    return bestDistance <= 8 ? best : "";
  }

  function minutesOfDay(label) {
    const match = String(label || "").match(/^(\d{2}):(\d{2})$/);
    if (!match) {
      return NaN;
    }
    return Number(match[1]) * 60 + Number(match[2]);
  }

  function compactDateLoose(value) {
    const compact = compactDate(value);
    if (compact) {
      return compact;
    }
    const text = String(value || "");
    const isoMatch = text.match(/(\d{4})[-/]?(\d{2})[-/]?(\d{2})/);
    if (isoMatch) {
      return isoMatch[1] + isoMatch[2] + isoMatch[3];
    }
    return "";
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
    scheduleTradeChartLoad(120);
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
      resetPositionStats();
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
    try {
      await refreshPositionStatsSource();
    } catch (err) {
      state.positionStatsDirty = true;
      pushLog("warn", "全量持仓统计读取失败", err.message);
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
    window.addEventListener("resize", resizeMinuteChart);
    els.orderAccount.addEventListener("change", async () => {
      state.activeAccount = els.orderAccount.value;
      state.performanceLoaded = false;
      state.selectedOrderID = "";
      resetLedgerPages();
      resetPositionStats();
      connectEventStream();
      await refreshNow();
      if (state.activeView === "trade") {
        await loadTradeChartBars({ silent: true });
      }
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
        quoteTimer = window.setTimeout(() => {
          loadQuoteForInput().catch((err) => pushLog("warn", "行情刷新失败", err.message));
          scheduleTradeChartLoad(120);
        }, 320);
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
      scheduleTradeChartLoad(120);
    });
    els.orderForm.addEventListener("submit", submitOrder);
    els.resetOrderButton.addEventListener("click", () => {
      els.symbolInput.value = "600000";
      els.exchangeInput.value = "SH";
      els.qtyInput.value = "100";
      state.priceEdited = false;
      updateSide("B");
      loadQuoteForInput().catch((err) => pushLog("warn", "行情刷新失败", err.message));
      scheduleTradeChartLoad(120);
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
    els.reloadChartButton.addEventListener("click", () => loadTradeChartBars({ silent: false }).catch((err) => showToast(err.message, "error")));
    els.chartTradeDateInput.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        loadTradeChartBars({ silent: false }).catch((err) => showToast(err.message, "error"));
      }
    });
    els.chartTradeDateInput.addEventListener("blur", () => {
      const tradeDate = compactDate(els.chartTradeDateInput.value);
      if (tradeDate) {
        els.chartTradeDateInput.value = tradeDate;
      }
    });
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
      scheduleTradeChartLoad(120);
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
      await loadQuoteForInput();
      ensurePerformanceDefaults();
      await loadAccountData();
      state.initialized = true;
      if (state.activeView === "performance") {
        await loadPerformance();
        await loadBars();
      } else if (state.activeView === "trade") {
        await loadTradeChartBars({ silent: true });
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
    window.addEventListener("beforeunload", closeTerminalStreams);
  }

  boot();
})();
