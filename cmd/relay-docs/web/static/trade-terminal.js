(() => {
  const state = {
    accounts: [],
    activeAccount: "",
    environment: "test",
    asset: null,
    positions: [],
    allPositions: [],
    allPositionsAccount: "",
    allPositionsLoadedDate: "",
    positionStatsDirty: true,
    positionStatsSeq: 0,
    metricFills: [],
    metricFillsAccount: "",
    metricFillsLoadedDate: "",
    metricFillsDirty: true,
    metricFillsSeq: 0,
    orders: [],
    fills: [],
    transfers: [],
    ordersPage: { cursor: "", previous: [], next: "", page: 1, pageSize: 50 },
    fillsPage: { cursor: "", previous: [], next: "", page: 1, pageSize: 50 },
    transfersPage: { cursor: "", previous: [], next: "", page: 1, pageSize: 50 },
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
    instrumentBySecurityID: new Map(),
    instrumentMisses: new Map(),
    activeSuggestion: -1,
    suggestionSeq: 0,
    quoteSeq: 0,
    priceEdited: false,
    activeView: "trade",
    performanceSummary: null,
    performanceSeries: [],
    performanceDaily: null,
    performanceContribution: null,
    performanceTradeQuality: null,
    performanceCostLedger: null,
    performanceTableView: "contributions",
    performanceEconomicNAV: null,
    performanceNAVReconciliation: null,
    performanceChart: null,
    performanceLoadSeq: 0,
    performanceReviewBusy: false,
    performanceReviewSettingsError: "",
    performanceLoaded: false,
    performanceError: "",
    performanceSettings: null,
    feeRules: [],
    cashLedgerEntries: [],
    navBaselines: [],
    reverseRepo: null,
    performanceSettingsLoaded: false,
    performanceSettingsError: "",
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
    systemStatus: null,
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
    chartAutoRefreshTimer: 0,
    chartAutoRefreshRunning: false,
    chartAutoRefreshErrorAt: 0,
    streamErrorLoggedAt: 0,
    toastTimer: 0,
    tableSorts: {
      positions: { key: "market_value", direction: "desc" },
      orders: { key: "created_at", direction: "desc" },
      fills: { key: "matched_at", direction: "desc" },
      transfers: { key: "matched_at", direction: "desc" }
    }
  };

  const chartAutoRefreshIntervalMs = 30000;

  const els = {
    shell: byID("terminalShell"),
    viewLinks: Array.from(document.querySelectorAll("[data-view-link]")),
    apiStatus: byID("apiStatus"),
    environmentBadge: byID("environmentBadge"),
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
    exportAssetButton: byID("exportAssetButton"),
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
    transferCount: byID("transferCount"),
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
    performanceTitle: byID("performanceTitle"),
    performanceRangeHint: byID("performanceRangeHint"),
    perfDateFrom: byID("perfDateFrom"),
    perfDateTo: byID("perfDateTo"),
    perfBenchmarkInput: byID("perfBenchmarkInput"),
    loadPerformanceButton: byID("loadPerformanceButton"),
    downloadPerformanceButton: byID("downloadPerformanceButton"),
    perfNetAsset: byID("perfNetAsset"),
    perfStartNetAsset: byID("perfStartNetAsset"),
    perfEconomicNav: byID("perfEconomicNav"),
    perfEconomicStatus: byID("perfEconomicStatus"),
    perfEconomicReturn: byID("perfEconomicReturn"),
    perfEconomicPnl: byID("perfEconomicPnl"),
    perfExternalFlow: byID("perfExternalFlow"),
    perfQualityFlags: byID("perfQualityFlags"),
    perfOpenNetAsset: byID("perfOpenNetAsset"),
    perfOpenSource: byID("perfOpenSource"),
    perfOvernightAdjustment: byID("perfOvernightAdjustment"),
    perfPreviousNetAsset: byID("perfPreviousNetAsset"),
    perfIntradayPnl: byID("perfIntradayPnl"),
    perfIntradayReturn: byID("perfIntradayReturn"),
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
    performanceChart: byID("performanceChart"),
    performanceChartRange: byID("performanceChartRange"),
    performanceQualityPanel: byID("performanceQualityPanel"),
    performanceQualityDate: byID("performanceQualityDate"),
    performanceQualityStatus: byID("performanceQualityStatus"),
    performanceQualityPassed: byID("performanceQualityPassed"),
    performanceQualityWarnings: byID("performanceQualityWarnings"),
    performanceQualityBlocked: byID("performanceQualityBlocked"),
    performanceQualityList: byID("performanceQualityList"),
    performanceStatus: byID("performanceStatus"),
    performanceSeriesBody: byID("performanceSeriesBody"),
    performanceContributionBody: byID("performanceContributionBody"),
    tradeQualityBody: byID("tradeQualityBody"),
    performanceTableViewButtons: Array.from(document.querySelectorAll("[data-performance-table-view]")),
    performanceTablePanels: Array.from(document.querySelectorAll("[data-performance-table-panel]")),
    contributionNetTotal: byID("contributionNetTotal"),
    contributionBPSTotal: byID("contributionBPSTotal"),
    contributionQualityCount: byID("contributionQualityCount"),
    performanceStrategySummary: byID("performanceStrategySummary"),
    tradeQualityOrders: byID("tradeQualityOrders"),
    tradeQualityExecutionRate: byID("tradeQualityExecutionRate"),
    tradeQualityFullRate: byID("tradeQualityFullRate"),
    tradeQualityQuantityRate: byID("tradeQualityQuantityRate"),
    tradeQualityCancelReject: byID("tradeQualityCancelReject"),
    tradeQualityOpen: byID("tradeQualityOpen"),
    tradeQualityAnomalies: byID("tradeQualityAnomalies"),
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
    navReconciliationPanel: byID("navReconciliationPanel"),
    navReconciliationStatus: byID("navReconciliationStatus"),
    navReconciliationHeadline: byID("navReconciliationHeadline"),
    navReconciliationDates: byID("navReconciliationDates"),
    navReconciliationBookNAV: byID("navReconciliationBookNAV"),
    navReconciliationObservedNAV: byID("navReconciliationObservedNAV"),
    navReconciliationResidual: byID("navReconciliationResidual"),
    navReconciliationResidualBar: byID("navReconciliationResidualBar"),
    navReconciliationThresholds: byID("navReconciliationThresholds"),
    navReconciliationCash: byID("navReconciliationCash"),
    navReconciliationPositions: byID("navReconciliationPositions"),
    navReconciliationReviewMeta: byID("navReconciliationReviewMeta"),
    navReconciliationWriteState: byID("navReconciliationWriteState"),
    navReviewOperator: byID("navReviewOperator"),
    navReviewNote: byID("navReviewNote"),
    navReviewForce: byID("navReviewForce"),
    blockNAVReconciliationButton: byID("blockNAVReconciliationButton"),
    confirmNAVReconciliationButton: byID("confirmNAVReconciliationButton"),
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
    barsBody: byID("barsBody"),
    performanceSettingsStatus: byID("performanceSettingsStatus"),
    repoTradeDateInput: byID("repoTradeDateInput"),
    loadPerformanceSettingsButton: byID("loadPerformanceSettingsButton"),
    previewRepoButton: byID("previewRepoButton"),
    persistRepoButton: byID("persistRepoButton"),
    settingsFormulaVersion: byID("settingsFormulaVersion"),
    settingsWriteState: byID("settingsWriteState"),
    settingsAutoTolerance: byID("settingsAutoTolerance"),
    settingsWarningTolerance: byID("settingsWarningTolerance"),
    repoPrincipal: byID("repoPrincipal"),
    repoNetInterest: byID("repoNetInterest"),
    feeRuleStatus: byID("feeRuleStatus"),
    feeRuleForm: byID("feeRuleForm"),
    feeRuleName: byID("feeRuleName"),
    feeRuleEffectiveFrom: byID("feeRuleEffectiveFrom"),
    feeRuleStatusInput: byID("feeRuleStatusInput"),
    feeRuleBusinessType: byID("feeRuleBusinessType"),
    feeRuleTradeSide: byID("feeRuleTradeSide"),
    feeRuleCommissionRate: byID("feeRuleCommissionRate"),
    feeRuleMinimumCommission: byID("feeRuleMinimumCommission"),
    feeRuleStampDutyRate: byID("feeRuleStampDutyRate"),
    feeRuleTransferFeeRate: byID("feeRuleTransferFeeRate"),
    feeRuleRepoFeeRate: byID("feeRuleRepoFeeRate"),
    feeRuleFrictionRate: byID("feeRuleFrictionRate"),
    feeRulesBody: byID("feeRulesBody"),
    cashLedgerStatus: byID("cashLedgerStatus"),
    cashLedgerForm: byID("cashLedgerForm"),
    cashTradeDateInput: byID("cashTradeDateInput"),
    cashLedgerTypeInput: byID("cashLedgerTypeInput"),
    cashFlowClassInput: byID("cashFlowClassInput"),
    cashAmountInput: byID("cashAmountInput"),
    cashBucketInput: byID("cashBucketInput"),
    cashDescriptionInput: byID("cashDescriptionInput"),
    cashLedgerBody: byID("cashLedgerBody"),
    navBaselineStatus: byID("navBaselineStatus"),
    navBaselineForm: byID("navBaselineForm"),
    navBaselineDateInput: byID("navBaselineDateInput"),
    navBaselineValueInput: byID("navBaselineValueInput"),
    navBaselineDescriptionInput: byID("navBaselineDescriptionInput"),
    navBaselinesBody: byID("navBaselinesBody"),
    repoStatus: byID("repoStatus"),
    reverseRepoBody: byID("reverseRepoBody")
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
      try {
        payload = JSON.parse(text);
      } catch (err) {
        const contentType = response.headers.get("content-type") || "unknown content-type";
        const snippet = text.slice(0, 120).replace(/\s+/g, " ").trim();
        const error = new Error((init.method || "GET") + " " + path + " 返回非 JSON：" + response.status + " " + contentType + (snippet ? "，响应片段：" + snippet : ""));
        error.cause = err;
        error.responseText = text;
        throw error;
      }
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

  function shiftCompactDate(value, days) {
    const digits = compactDate(value);
    if (!digits) {
      return "";
    }
    const date = new Date(Date.UTC(
      Number(digits.slice(0, 4)),
      Number(digits.slice(4, 6)) - 1,
      Number(digits.slice(6, 8))
    ));
    date.setUTCDate(date.getUTCDate() + Number(days || 0));
    return date.getUTCFullYear().toString().padStart(4, "0") +
      String(date.getUTCMonth() + 1).padStart(2, "0") +
      String(date.getUTCDate()).padStart(2, "0");
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
    [
      els.repoTradeDateInput,
      els.feeRuleEffectiveFrom,
      els.cashTradeDateInput,
      els.navBaselineDateInput
    ].forEach((input) => applyDefaultDateInput(input, nextDate, previousDefault));
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
    const instrument = instrumentForSecurityID(securityID);
    if (instrument && instrument.instrument_type) {
      return instrument.instrument_type;
    }
    if (securityID && state.marketSnapshot && state.marketSnapshot.security_id === securityID) {
      return state.marketSnapshot.instrument_type || "";
    }
    return "";
  }

  function instrumentForSecurityID(securityID) {
    const normalized = normalizeSecurityID(securityID);
    if (!normalized) {
      return null;
    }
    return state.instrumentBySecurityID.get(normalized) || null;
  }

  function registerInstrument(instrument) {
    if (!instrument) {
      return null;
    }
    const securityID = normalizeSecurityID(instrument.security_id || "");
    if (!securityID) {
      return null;
    }
    const parsed = splitSecurityID(securityID);
    const normalized = Object.assign({}, instrument, {
      security_id: securityID,
      symbol: instrument.symbol || parsed.symbol,
      exchange: normalizeExchangeCode(instrument.exchange || parsed.exchange, parsed.symbol),
      name: instrument.name || ""
    });
    state.instrumentBySecurityID.set(securityID, normalized);
    state.instrumentMisses.delete(securityID);
    return normalized;
  }

  function instrumentMissExpired(securityID) {
    const missedAt = state.instrumentMisses.get(normalizeSecurityID(securityID));
    return !missedAt || Date.now() - missedAt > 60000;
  }

  function rememberInstrumentMiss(securityID) {
    const normalized = normalizeSecurityID(securityID);
    if (normalized) {
      state.instrumentMisses.set(normalized, Date.now());
    }
  }

  function itemSecurityID(item) {
    if (!item) {
      return "";
    }
    if (item.security_id) {
      return normalizeSecurityID(item.security_id);
    }
    if (item.symbol) {
      const symbol = normalizeSymbol(item.symbol);
      if (symbol.includes(".")) {
        return normalizeSecurityID(symbol);
      }
      return symbol + "." + normalizeExchangeCode(item.exchange, symbol);
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

  function formatShortDateTime(value) {
    if (!value) {
      return "--";
    }
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }
    const compact = businessDateCompact(date);
    const time = date.toLocaleTimeString("zh-CN", { hour12: false });
    if (compact === currentBusinessDate()) {
      return time;
    }
    return compact.slice(4, 6) + "-" + compact.slice(6, 8) + " " + time;
  }

  function symbolText(item) {
    if (!item) {
      return "--";
    }
    const securityID = itemSecurityID(item);
    if (securityID) {
      return securityID;
    }
    return item.symbol + (item.exchange ? "." + item.exchange : "");
  }

  function securityNameText(item, fallback) {
    const name = String(item && item.name || "").trim();
    if (name) {
      return name;
    }
    const instrument = instrumentForSecurityID(itemSecurityID(item));
    if (instrument && instrument.name) {
      return instrument.name;
    }
    const fallbackName = String(fallback && fallback.name || "").trim();
    if (fallbackName) {
      return fallbackName;
    }
    const fallbackInstrument = instrumentForSecurityID(itemSecurityID(fallback));
    return fallbackInstrument && fallbackInstrument.name ? fallbackInstrument.name : "--";
  }

  function normalizeSymbol(value) {
    return String(value || "").trim().toUpperCase().replace(/[^0-9A-Z.]/g, "");
  }

  function splitSecurityID(securityID) {
    const normalized = normalizeSymbol(securityID);
    const parts = normalized.split(".");
    return {
      symbol: parts[0] || "",
      exchange: normalizeExchangeCode(parts[1], parts[0] || "")
    };
  }

  function normalizeExchangeCode(value, symbol = "") {
    const raw = normalizeSymbol(value).replace(/\..*$/, "");
    if (raw === "SH" || raw === "XSHG" || raw === "SHSE" || raw === "SSE") {
      return "SH";
    }
    if (raw === "SZ" || raw === "XSHE" || raw === "SZSE") {
      return "SZ";
    }
    if (raw === "BJ" || raw === "XBSE" || raw === "BSE") {
      return "BJ";
    }
    return inferExchange(symbol || raw);
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
      return normalizeSecurityID(raw);
    }
    return raw + "." + normalizeExchangeCode(els.exchangeInput.value, raw);
  }

  function normalizeSecurityID(value) {
    const raw = normalizeSymbol(value);
    if (!raw) {
      return "";
    }
    if (raw.includes(".")) {
      const parsed = splitSecurityID(raw);
      return parsed.symbol ? parsed.symbol + "." + parsed.exchange : "";
    }
    return raw + "." + inferExchange(raw);
  }

  function setSymbolFromSecurityID(securityID) {
    const parsed = splitSecurityID(securityID);
    els.symbolInput.value = parsed.symbol;
    els.exchangeInput.value = parsed.exchange;
  }

  function focusTradeSymbol(securityID, options = {}) {
    const normalized = normalizeSecurityID(securityID);
    if (!normalized) {
      return;
    }
    setSymbolFromSecurityID(normalized);
    state.priceEdited = false;
    hideSuggestions();
    navigateView("trade");
    if (options.side) {
      updateSide(options.side);
    }
    loadQuoteForInput({ securityID: normalized }).catch((err) => pushLog("warn", "行情刷新失败", err.message));
    loadTradeChartBars({ securityID: normalized, tradeDate: currentChartTradeDate(), silent: true })
      .catch((err) => pushLog("warn", "K线查询失败", err.message))
      .finally(() => scheduleChartAutoRefresh());
  }

  function sideCode(item) {
    if (typeof item === "string") {
      return item.toUpperCase();
    }
    return String(item && item.trade_side || "").toUpperCase();
  }

  function businessTypeCode(item, linkedOrder) {
    return String(
      item && (item.business_type || adapterText(item, "business_type")) ||
      linkedOrder && (linkedOrder.business_type || adapterText(linkedOrder, "business_type")) ||
      ""
    ).toUpperCase();
  }

  function sideText(item, linkedOrder) {
    const side = sideCode(item);
    const businessType = businessTypeCode(item, linkedOrder);
    if (side === "R") {
      return businessType === "E" ? "ETF赎回" : "赎回";
    }
    if (side === "P") {
      return businessType === "E" ? "ETF申购" : "申购";
    }
    if (side === "S") {
      return "卖出";
    }
    if (side === "B") {
      return "买入";
    }
    return side || "--";
  }

  function sideKind(item, linkedOrder) {
    const side = sideCode(item);
    const businessType = businessTypeCode(item, linkedOrder);
    if (side === "R") {
      return businessType === "E" ? "redeem" : "sell";
    }
    if (side === "P") {
      return businessType === "E" ? "create" : "buy";
    }
    if (side === "S") {
      return "sell";
    }
    return "buy";
  }

  function sideBadge(item, linkedOrder) {
    const kind = sideKind(item, linkedOrder);
    const label = sideText(item, linkedOrder);
    return '<span class="side-badge ' + escapeHTML(kind) + '">' + escapeHTML(label) + "</span>";
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
    window.clearTimeout(state.toastTimer);
    els.toast.textContent = message;
    els.toast.classList.toggle("error", type === "error");
    els.toast.classList.add("show");
    state.toastTimer = window.setTimeout(() => {
      els.toast.classList.remove("show");
      state.toastTimer = 0;
    }, type === "error" ? 6000 : 3200);
  }

  function viewFromLocation() {
    const hash = String(window.location.hash || "").replace("#", "");
    const pathView = String(window.location.pathname || "").replace(/^\/trade\/?/, "").replace(/^\/+|\/+$/g, "");
    const viewToken = hash || pathView;
    if (viewToken === "asset") {
      return "asset";
    }
    if (viewToken === "performance") {
      return "performance";
    }
    if (viewToken === "snapshots") {
      return "snapshots";
    }
    if (viewToken === "logs") {
      state.selectedTab = "logs";
      return "logs";
    }
    if (viewToken === "performance-settings") {
      return "performance-settings";
    }
    if (viewToken === "orders" || viewToken === "fills") {
      if (viewToken === "fills") {
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
    if (!["trade", "orders", "asset", "performance", "snapshots", "logs", "performance-settings"].includes(view)) {
      view = "trade";
    }
    if (view === "logs") {
      state.selectedTab = "logs";
    }
    state.activeView = view;
    els.shell.classList.toggle("view-trade", view === "trade");
    els.shell.classList.toggle("view-orders", view === "orders" || view === "logs");
    els.shell.classList.toggle("view-asset", view === "asset");
    els.shell.classList.toggle("view-performance", view === "performance" || view === "snapshots");
    els.shell.classList.toggle("view-snapshots", view === "snapshots");
    els.shell.classList.toggle("view-logs", view === "logs");
    els.shell.classList.toggle("view-performance-settings", view === "performance-settings");
    for (const link of els.viewLinks) {
      link.classList.toggle("active", link.dataset.viewLink === view);
    }
    renderMonitorSummary();
    renderBlotter();
    renderDetail();
    if (view === "trade" && state.initialized) {
      loadQuoteForInput().catch((err) => pushLog("warn", "行情刷新失败", err.message));
      ensureChartDefaults();
      const securityID = currentSecurityID();
      const tradeDate = currentChartTradeDate();
      if (!state.barsLoaded || !barsMatch(securityID, tradeDate)) {
        loadTradeChartBars({ silent: true })
          .catch((err) => pushLog("warn", "K线查询失败", err.message))
          .finally(() => scheduleChartAutoRefresh());
      } else {
        window.setTimeout(() => {
          refreshMinuteChartMarkers()
            .catch((err) => pushLog("warn", "图表买卖点刷新失败", err.message))
            .finally(() => {
              renderMinuteChart();
              resizeMinuteChart();
              scheduleChartAutoRefresh();
            });
        }, 0);
      }
    } else {
      stopChartAutoRefresh();
    }
    if (els.performanceTitle) {
      els.performanceTitle.textContent = view === "snapshots" ? "日终快照" : "绩效分析";
    }
    if (view === "snapshots") {
      setPerformanceTableView("series");
    }
    if ((view === "performance" || view === "snapshots") && state.initialized) {
      ensurePerformanceDefaults();
      if (state.activeAccount && !state.performanceLoaded) {
        loadPerformance().catch((err) => pushLog("warn", "绩效查询失败", err.message));
      } else {
        renderPerformance();
        window.requestAnimationFrame(resizePerformanceChart);
      }
    }
    if (view === "performance-settings" && state.initialized) {
      ensurePerformanceSettingsDefaults();
      if (state.activeAccount && !state.performanceSettingsLoaded) {
        loadPerformanceSettings().catch((err) => pushLog("warn", "绩效设置查询失败", err.message));
      }
    }
  }

  async function loadStatus() {
    try {
      const data = await request("/v1/status");
      state.systemStatus = data;
      const dependencies = data.dependencies || {};
      const apiOK = data.status === "ok";
      updateEnvironmentBadge(data.environment);
      setStatus(els.apiStatus, apiOK, "API: " + (apiOK ? "connected" : data.status || "degraded"));
      setStatus(els.redisStatus, dependencyOK(dependencies.redis), dependencyLabel("Redis", dependencies.redis));
      setStatus(els.dbStatus, dependencyOK(dependencies.database), dependencyLabel("DB", dependencies.database));
      els.footerApi.textContent = data.public_url || window.RELAY_PUBLIC_URL || "connected";
      updateStreamFooter();
      if (data.time) {
        syncClock(data.time);
      }
      if (state.performanceLoaded) {
        renderPerformanceQuality();
      }
    } catch (err) {
      setStatus(els.apiStatus, false, "API: error");
      updateEnvironmentBadge(state.environment);
      setStatus(els.redisStatus, false, "Redis: unknown");
      setStatus(els.dbStatus, false, "DB: unknown");
      pushLog("error", "状态接口失败", err.message);
    }
  }

  function updateEnvironmentBadge(environment) {
    const normalized = String(environment || "test").trim().toLowerCase();
    state.environment = normalized || "test";
    if (!els.environmentBadge) {
      return;
    }
    els.environmentBadge.classList.toggle("production", normalized === "production");
    els.environmentBadge.classList.toggle("test", normalized !== "production");
    els.environmentBadge.textContent = normalized === "production" ? "生产环境" : "测试环境";
    els.environmentBadge.title = normalized === "production" ? "当前服务连接生产环境" : "当前服务连接测试环境";
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
    stopChartAutoRefresh();
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
    state.metricFillsSeq += 1;
    state.allPositions = [];
    state.allPositionsAccount = "";
    state.allPositionsLoadedDate = "";
    state.positionStatsDirty = true;
    state.positionQuotes.clear();
    state.metricFills = [];
    state.metricFillsAccount = "";
    state.metricFillsLoadedDate = "";
    state.metricFillsDirty = true;
  }

  function markPositionStatsDirty() {
    state.positionStatsDirty = true;
  }

  function markMetricFillsDirty() {
    state.metricFillsDirty = true;
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

  async function ensureInstrumentsForItems(items) {
    const ids = [];
    const seen = new Set();
    for (const item of items || []) {
      const securityID = itemSecurityID(item);
      if (!securityID || seen.has(securityID)) {
        continue;
      }
      seen.add(securityID);
      if (!instrumentForSecurityID(securityID) && instrumentMissExpired(securityID)) {
        ids.push(securityID);
      }
    }
    if (ids.length === 0) {
      return;
    }
    const chunkSize = 100;
    for (let offset = 0; offset < ids.length; offset += chunkSize) {
      const chunk = ids.slice(offset, offset + chunkSize);
      const params = new URLSearchParams({
        security_ids: chunk.join(","),
        limit: String(Math.max(chunk.length, 100))
      });
      const data = await request("/v1/meridian/metadata/instruments?" + params.toString());
      if (data.error) {
        throw new Error(data.error.message || data.error.code || "Meridian metadata error");
      }
      const rows = Array.isArray(data.data) ? data.data : [];
      const found = new Set();
      for (const row of rows) {
        const instrument = registerInstrument(row);
        if (instrument) {
          found.add(instrument.security_id);
        }
      }
      for (const securityID of chunk) {
        if (!found.has(securityID)) {
          rememberInstrumentMiss(securityID);
        }
      }
    }
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
      registerInstrument(row);
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
    if (type === "fill.changed") {
      markMetricFillsDirty();
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
      loadTradeChartBars({ silent: true })
        .catch((err) => pushLog("warn", "K线查询失败", err.message))
        .finally(() => scheduleChartAutoRefresh());
    }, delay);
  }

  function stopChartAutoRefresh() {
    window.clearTimeout(state.chartAutoRefreshTimer);
    state.chartAutoRefreshTimer = 0;
  }

  function shouldAutoRefreshTradeChart() {
    if (!state.initialized || state.activeView !== "trade" || document.hidden) {
      return false;
    }
    const securityID = normalizeSecurityID(currentSecurityID());
    const tradeDate = currentChartTradeDate();
    return Boolean(securityID && tradeDate && isCurrentBusinessDate(tradeDate));
  }

  function scheduleChartAutoRefresh(delay = chartAutoRefreshIntervalMs) {
    stopChartAutoRefresh();
    if (!shouldAutoRefreshTradeChart()) {
      return;
    }
    state.chartAutoRefreshTimer = window.setTimeout(refreshTradeChartAutomatically, delay);
  }

  async function refreshTradeChartAutomatically() {
    if (!shouldAutoRefreshTradeChart()) {
      stopChartAutoRefresh();
      return;
    }
    if (state.chartAutoRefreshRunning) {
      scheduleChartAutoRefresh();
      return;
    }
    state.chartAutoRefreshRunning = true;
    try {
      await loadTradeChartBars({ silent: true, auto: true });
    } catch (err) {
      const now = Date.now();
      if (now - state.chartAutoRefreshErrorAt > 30000) {
        state.chartAutoRefreshErrorAt = now;
        pushLog("warn", "分钟K线自动刷新失败", err.message);
      }
    } finally {
      state.chartAutoRefreshRunning = false;
      scheduleChartAutoRefresh();
    }
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
    const [assetResult, positionsResult, ordersResult, fillsResult, transfersResult] = await Promise.allSettled([
      fetchAssetForSelectedDate(),
      fetchPositionsPage(),
      fetchOrdersPage(),
      fetchFillsPage(),
      fetchComponentTransfersPage()
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
    if (transfersResult.status === "fulfilled") {
      state.transfers = transfersResult.value.transfers || [];
      state.transfersPage.next = transfersResult.value.next_cursor || "";
    } else {
      pushLog("warn", "ETF 划转读取失败", transfersResult.reason.message);
    }

    try {
      await refreshPositionStatsSource();
    } catch (err) {
      state.positionStatsDirty = true;
      pushLog("warn", "全量持仓统计读取失败", err.message);
    }
    try {
      await refreshMetricFillsSource();
    } catch (err) {
      state.metricFillsDirty = true;
      pushLog("warn", "成交费用统计读取失败", err.message);
    }
    await enrichVisibleLedgerInstruments();

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
    resetPage(state.transfersPage);
    resetPage(state.positionsPage);
  }

  async function fetchAssetForSelectedDate() {
    const accountID = encodeURIComponent(state.activeAccount);
    const tradeDate = selectedAssetTradeDate();
    return fetchAssetForExport(accountID, tradeDate, true);
  }

  async function fetchAssetForExport(accountID, tradeDate, encoded = false) {
    const encodedAccountID = encoded ? accountID : encodeURIComponent(accountID);
    if (isCurrentBusinessDate(tradeDate)) {
      const data = await request("/v1/accounts/" + encodedAccountID + "/asset");
      return data.asset || null;
    }
    const data = await request("/v1/accounts/" + encodedAccountID + "/performance/daily?trade_date=" + encodeURIComponent(tradeDate));
    return assetFromPerformance(data.performance || {});
  }

  function assetFromPerformance(performance) {
    const positionMarketValue = finiteNumber(performance.position_market_value);
    const rawMarketValue = finiteNumber(performance.market_value);
    const marketValue = rawMarketValue !== null && rawMarketValue > 0
      ? rawMarketValue
      : (positionMarketValue !== null ? positionMarketValue : performance.market_value);
    const cashTotal = finiteNumber(performance.cash_total);
    const rawNetAsset = finiteNumber(performance.net_asset);
    const effectiveNetAsset = positionMarketValue !== null && positionMarketValue > 0 &&
      cashTotal !== null && (rawNetAsset === null || rawNetAsset <= cashTotal)
      ? cashTotal + positionMarketValue
      : performance.net_asset;
    const stockValue = finiteNumber(performance.stock_value);
    const fundValue = finiteNumber(performance.fund_value);
    return {
      account_id: performance.account_id,
      cash_available: performance.cash_available,
      cash_total: performance.cash_total,
      net_asset: effectiveNetAsset,
      market_value: marketValue,
      stock_value: stockValue !== null && stockValue > 0 ? stockValue : (fundValue ? 0 : marketValue),
      fund_value: fundValue !== null && fundValue > 0 ? fundValue : 0,
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

  async function fetchAllPositionsForExport(accountID, tradeDate) {
    const encodedAccountID = encodeURIComponent(accountID);
    const current = isCurrentBusinessDate(tradeDate);
    const path = current
      ? "/v1/accounts/" + encodedAccountID + "/positions"
      : "/v1/accounts/" + encodedAccountID + "/positions/history";
    const positions = [];
    const seenCursors = new Set();
    let cursor = "";
    for (let page = 0; page < 100; page += 1) {
      const params = new URLSearchParams({ limit: "2000" });
      if (!current) {
        params.set("trade_date", tradeDate);
      }
      if (cursor) {
        params.set("cursor", cursor);
      }
      const data = await request(path + "?" + params.toString());
      positions.push(...(Array.isArray(data.positions) ? data.positions : []));
      const nextCursor = String(data.next_cursor || "");
      if (!nextCursor) {
        return positions;
      }
      if (seenCursors.has(nextCursor)) {
        throw new Error("持仓分页游标重复，导出已停止");
      }
      seenCursors.add(nextCursor);
      cursor = nextCursor;
    }
    throw new Error("持仓记录超过前端导出上限，请缩小查询范围");
  }

  function exportPositionView(position, useLiveQuote) {
    if (useLiveQuote) {
      return livePositionView(position);
    }
    const qty = finiteNumber(position.quantity);
    const avgCost = finiteNumber(position.avg_cost);
    const price = finiteNumber(position.last_price);
    const marketValue = finiteNumber(position.market_value);
    const pnl = finiteNumber(position.unrealized_pnl);
    const dayPnl = finiteNumber(position.day_unrealized_pnl);
    const costAmount = qty !== null && avgCost !== null ? qty * avgCost : null;
    const dayBase = marketValue !== null && dayPnl !== null ? marketValue - dayPnl : null;
    return {
      quote: null,
      quoteItem: position,
      price,
      marketValue,
      pnl,
      pnlRatio: pnl !== null && costAmount ? pnl / costAmount * 100 : null,
      dayPnl,
      dayPnlRatio: dayPnl !== null && dayBase ? dayPnl / dayBase * 100 : null
    };
  }

  function sortedExportPositions(positions, useLiveQuote) {
    const sort = state.tableSorts.positions;
    if (!sort || !sort.key) {
      return positions.slice();
    }
    return positions.map((position, index) => ({
      position,
      index,
      view: exportPositionView(position, useLiveQuote)
    })).sort((left, right) => {
      const compared = compareSortValues(
        exportPositionSortValue(left.position, left.view, sort.key),
        exportPositionSortValue(right.position, right.view, sort.key),
        sort.direction
      );
      return compared || left.index - right.index;
    }).map((item) => item.position);
  }

  function exportPositionSortValue(position, view, key) {
    switch (key) {
    case "symbol":
      return symbolText(position);
    case "name":
      return securityNameText(position);
    case "quantity":
      return finiteNumber(position.quantity);
    case "sellable_qty":
      return finiteNumber(position.sellable_qty);
    case "avg_cost":
      return finiteNumber(position.avg_cost);
    case "last_price":
      return finiteNumber(view.price);
    case "market_value":
      return finiteNumber(view.marketValue);
    case "pnl":
      return finiteNumber(view.pnl);
    case "day_pnl":
      return finiteNumber(view.dayPnl);
    case "updated_at":
      return timeSortValue(position.updated_at);
    default:
      return "";
    }
  }

  function exportAssetMetrics(asset, accountID, tradeDate, positions, useLiveQuote) {
    let marketValue = 0;
    let positionProfit = 0;
    let dayPositionProfit = 0;
    let hasMarketValue = false;
    let hasPositionProfit = false;
    let hasDayPositionProfit = false;
    for (const position of positions) {
      const view = exportPositionView(position, useLiveQuote);
      const rowMarketValue = finiteNumber(view.marketValue);
      const rowPositionProfit = finiteNumber(view.pnl);
      const rowDayPositionProfit = finiteNumber(view.dayPnl);
      if (rowMarketValue !== null) {
        marketValue += rowMarketValue;
        hasMarketValue = true;
      }
      if (rowPositionProfit !== null) {
        positionProfit += rowPositionProfit;
        hasPositionProfit = true;
      }
      if (rowDayPositionProfit !== null) {
        dayPositionProfit += rowDayPositionProfit;
        hasDayPositionProfit = true;
      }
    }
    const assetMarketValue = finiteNumber(asset.market_value);
    const effectiveMarketValue = hasMarketValue ? marketValue : assetMarketValue;
    const cashTotal = finiteNumber(asset.cash_total);
    const commission = accountID === state.activeAccount && tradeDate === selectedAssetTradeDateSafe()
      ? metricCommission(asset)
      : finiteNumber(asset.commission);
    const closeProfit = accountID === state.activeAccount && tradeDate === selectedAssetTradeDateSafe()
      ? metricCloseProfit(asset)
      : finiteNumber(asset.close_profit);
    const effectiveDayPositionProfit = hasDayPositionProfit
      ? dayPositionProfit
      : finiteNumber(asset.day_unrealized_pnl);
    return {
      netAsset: cashTotal !== null && effectiveMarketValue !== null
        ? cashTotal + effectiveMarketValue
        : finiteNumber(asset.net_asset),
      cashAvailable: finiteNumber(asset.cash_available),
      cashTotal,
      marketValue: effectiveMarketValue,
      stockValue: finiteNumber(asset.stock_value),
      fundValue: finiteNumber(asset.fund_value),
      positionProfit: hasPositionProfit ? positionProfit : finiteNumber(asset.position_profit),
      closeProfit,
      commission,
      dayProfit: metricDayProfit(asset, effectiveDayPositionProfit, closeProfit, commission, hasDayPositionProfit),
      updatedAt: asset.updated_at || asset.captured_at || ""
    };
  }

  function csvCell(value) {
    if (value === null || value === undefined) {
      return "";
    }
    return '"' + String(value).replace(/"/g, '""') + '"';
  }

  function csvLine(values) {
    return values.map(csvCell).join(",");
  }

  function csvNumber(value) {
    const number = finiteNumber(value);
    return number === null ? "" : String(number);
  }

  function downloadCSVFile(filename, lines) {
    const blob = new Blob(["\ufeff" + lines.join("\r\n") + "\r\n"], {
      type: "text/csv;charset=utf-8"
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  async function exportAssetPositionsCSV() {
    if (!state.activeAccount) {
      showToast("请先选择账户", "error");
      return;
    }
    let tradeDate;
    try {
      tradeDate = selectedAssetTradeDate();
    } catch (err) {
      showToast(err.message, "error");
      return;
    }
    const accountID = state.activeAccount;
    const account = state.accounts.find((item) => item.account_id === accountID);
    const originalText = els.exportAssetButton.textContent;
    els.exportAssetButton.disabled = true;
    els.exportAssetButton.textContent = "导出中";
    try {
      const [assetResult, positions] = await Promise.all([
        fetchAssetForExport(accountID, tradeDate).then((asset) => ({ asset: asset || {}, error: "" }))
          .catch((err) => ({ asset: {}, error: err.message })),
        fetchAllPositionsForExport(accountID, tradeDate)
      ]);
      const useLiveQuote = isCurrentBusinessDate(tradeDate) && accountID === state.activeAccount;
      const sortedPositions = sortedExportPositions(positions, useLiveQuote);
      const metrics = exportAssetMetrics(assetResult.asset, accountID, tradeDate, positions, useLiveQuote);
      const exportedAt = new Date().toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false
      });
      const lines = [
        csvLine(["资金摘要"]),
        csvLine(["字段", "值"]),
        csvLine(["账户ID", accountID]),
        csvLine(["账户别名", account ? accountLabel(account) : accountID]),
        csvLine(["交易日", tradeDate]),
        csvLine(["导出时间(Asia/Shanghai)", exportedAt]),
        csvLine(["资金数据状态", assetResult.error ? "不可用: " + assetResult.error : "正常"]),
        csvLine(["总资产", csvNumber(metrics.netAsset)]),
        csvLine(["可用资金", csvNumber(metrics.cashAvailable)]),
        csvLine(["资金总额", csvNumber(metrics.cashTotal)]),
        csvLine(["证券市值", csvNumber(metrics.marketValue)]),
        csvLine(["股票市值", csvNumber(metrics.stockValue)]),
        csvLine(["基金市值", csvNumber(metrics.fundValue)]),
        csvLine(["持仓盈亏", csvNumber(metrics.positionProfit)]),
        csvLine(["平仓盈亏", csvNumber(metrics.closeProfit)]),
        csvLine(["手续费", csvNumber(metrics.commission)]),
        csvLine(["当日盈亏", csvNumber(metrics.dayProfit)]),
        csvLine(["资金更新时间", metrics.updatedAt]),
        "",
        csvLine(["持仓明细"]),
        csvLine([
          "账户ID", "交易日", "代码", "市场", "证券名称", "持仓数量", "可用数量",
          "日初数量", "当日数量", "成本价", "账本现价", "导出现价", "市值",
          "持仓盈亏", "盈亏比例(%)", "当日盈亏", "当日盈亏比例(%)", "已结算盈亏",
          "股东代码", "快照类型", "更新时间"
        ])
      ];
      for (const position of sortedPositions) {
        const view = exportPositionView(position, useLiveQuote);
        lines.push(csvLine([
          accountID,
          tradeDate,
          symbolText(position),
          position.exchange || "",
          securityNameText(position) === "--" ? "" : securityNameText(position),
          csvNumber(position.quantity),
          csvNumber(position.sellable_qty),
          csvNumber(position.initial_qty),
          csvNumber(position.today_qty),
          csvNumber(position.avg_cost),
          csvNumber(position.last_price),
          csvNumber(view.price),
          csvNumber(view.marketValue),
          csvNumber(view.pnl),
          csvNumber(view.pnlRatio),
          csvNumber(view.dayPnl),
          csvNumber(view.dayPnlRatio),
          csvNumber(position.settled_profit),
          position.shareholder_id || "",
          position.snapshot_type || (isCurrentBusinessDate(tradeDate) ? "current" : "close"),
          position.updated_at || ""
        ]));
      }
      const filename = [
        "relay-asset-positions",
        accountID.replace(/[^0-9A-Za-z_-]/g, ""),
        tradeDate
      ].join("-") + ".csv";
      downloadCSVFile(filename, lines);
      pushLog("info", "资金持仓已导出", accountID + " / " + displayDate(tradeDate) + " / " + formatInt(sortedPositions.length) + " 条");
      showToast("已导出 " + formatInt(sortedPositions.length) + " 条持仓");
    } catch (err) {
      pushLog("error", "资金持仓导出失败", err.message);
      showToast("导出失败：" + err.message, "error");
    } finally {
      els.exportAssetButton.disabled = false;
      els.exportAssetButton.textContent = originalText;
    }
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

  async function refreshMetricFillsSource(options = {}) {
    const tradeDate = selectedAssetTradeDateSafe();
    const accountID = state.activeAccount;
    const force = Boolean(options.force);
    if (!accountID || !tradeDate) {
      state.metricFills = [];
      state.metricFillsAccount = accountID || "";
      state.metricFillsLoadedDate = tradeDate || "";
      state.metricFillsDirty = false;
      return;
    }
    if (!force &&
      !state.metricFillsDirty &&
      state.metricFillsAccount === accountID &&
      state.metricFillsLoadedDate === tradeDate) {
      return;
    }

    const seq = ++state.metricFillsSeq;
    const allFills = [];
    let cursor = "";
    for (let page = 0; page < 20; page += 1) {
      const params = new URLSearchParams({
        account_id: accountID,
        limit: "500"
      });
      if (cursor) {
        params.set("cursor", cursor);
      }
      let path = "/v1/fills";
      if (!isCurrentBusinessDate(tradeDate)) {
        path = "/v1/history/fills";
        params.set("trade_date", tradeDate);
      }
      const data = await request(path + "?" + params.toString());
      allFills.push(...(data.fills || []));
      cursor = data.next_cursor || "";
      if (!cursor) {
        break;
      }
    }
    if (cursor) {
      pushLog("warn", "成交费用统计超过前端查询上限", "已读取 " + formatInt(allFills.length) + " 条");
    }
    if (seq !== state.metricFillsSeq) {
      return;
    }
    state.metricFills = allFills;
    state.metricFillsAccount = accountID;
    state.metricFillsLoadedDate = tradeDate;
    state.metricFillsDirty = false;
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

  async function fetchComponentTransfersPage() {
    const tradeDate = selectedOrdersTradeDate();
    const params = new URLSearchParams({
      account_id: state.activeAccount,
      limit: String(state.transfersPage.pageSize)
    });
    if (state.transfersPage.cursor) {
      params.set("cursor", state.transfersPage.cursor);
    }
    let path = "/v1/transfers";
    if (!isCurrentBusinessDate(tradeDate)) {
      path = "/v1/history/transfers";
      params.set("trade_date", tradeDate);
    }
    return request(path + "?" + params.toString());
  }

  async function enrichVisibleLedgerInstruments() {
    try {
      await ensureInstrumentsForItems([
        ...state.positions,
        ...state.orders,
        ...state.fills,
        ...state.transfers
      ]);
    } catch (err) {
      pushLog("warn", "证券名称补齐失败", err.message);
    }
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
      registerInstrument(state.marketSnapshot);
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
          const instrument = {
            ...item,
            symbol: parsed.symbol,
            exchange: parsed.exchange
          };
          registerInstrument(instrument);
          return instrument;
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
      const label = accountLabel(account);
      const suffix = accountSuffix(account);
      const tab = document.createElement("button");
      tab.type = "button";
      tab.className = account.account_id === state.activeAccount ? "active" : "";
      tab.title = label + " / " + suffix;
      const strong = document.createElement("strong");
      strong.textContent = label;
      const small = document.createElement("small");
      small.textContent = suffix;
      tab.appendChild(strong);
      tab.appendChild(small);
      tab.addEventListener("click", async () => {
        state.activeAccount = account.account_id;
        state.selectedOrderID = "";
        state.performanceLoaded = false;
        state.performanceContribution = null;
        resetLedgerPages();
        resetPositionStats();
        renderAccounts();
        connectEventStream();
        await refreshNow();
        if (state.activeView === "trade") {
          await loadTradeChartBars({ silent: true });
        }
        if (state.activeView === "performance" || state.activeView === "snapshots") {
          await loadPerformance();
        }
      });
      els.accountTabs.appendChild(tab);

      const option = document.createElement("option");
      option.value = account.account_id;
      option.textContent = label === account.account_id ? suffix : label + " - " + account.account_id;
      option.selected = account.account_id === state.activeAccount;
      els.orderAccount.appendChild(option);
    }
    const editButton = document.createElement("button");
    editButton.type = "button";
    editButton.className = "alias-edit";
    editButton.textContent = "别名";
    editButton.title = "编辑当前账户的服务端显示别名";
    editButton.disabled = !state.activeAccount;
    editButton.addEventListener("click", editActiveAccountAlias);
    els.accountTabs.appendChild(editButton);
    updateRisk();
  }

  function accountLabel(account) {
    const accountID = account && account.account_id ? String(account.account_id) : "";
    return String(account && account.alias || accountID || "未命名账户").trim();
  }

  function accountSuffix(account) {
    const parts = [];
    if (account && account.account_id) {
      parts.push(account.account_id);
    }
    if (account && account.trading_enabled === false) {
      parts.push("只读");
    }
    if (account && account.simulated) {
      parts.push("模拟");
    }
    return parts.join(" / ") || "--";
  }

  function accountTradingEnabled(accountID) {
    const account = state.accounts.find((item) => item.account_id === accountID);
    return Boolean(account && account.enabled !== false && account.trading_enabled === true);
  }

  function selectedOrderAccountCanTrade() {
    return accountTradingEnabled(els.orderAccount.value || state.activeAccount);
  }

  function activeAccountLabel() {
    const account = state.accounts.find((item) => item.account_id === state.activeAccount);
    return account ? accountLabel(account) : state.activeAccount;
  }

  async function editActiveAccountAlias() {
    const account = state.accounts.find((item) => item.account_id === state.activeAccount);
    if (!account) {
      showToast("请先选择账户", "error");
      return;
    }
    const current = accountLabel(account) === account.account_id ? "" : accountLabel(account);
    const next = window.prompt("账户别名：" + account.account_id + "\n留空则恢复配置默认别名", current);
    if (next === null) {
      return;
    }
    const alias = String(next).trim().slice(0, 24);
    try {
      const data = await request("/v1/accounts/" + encodeURIComponent(account.account_id) + "/alias", {
        method: "PATCH",
        body: { alias }
      });
      const updated = data.account || {};
      state.accounts = state.accounts.map((item) => {
        if (item.account_id !== account.account_id) {
          return item;
        }
        return Object.assign({}, item, { alias: updated.alias || "" });
      });
      renderAccounts();
      renderPerformance();
      showToast(alias ? "账户别名已保存" : "已恢复配置默认别名");
    } catch (err) {
      pushLog("error", "账户别名保存失败", err.message);
      showToast("账户别名保存失败：" + err.message, "error");
    }
  }

  function renderMetrics() {
    const asset = state.asset || {};
    const liveTotals = livePortfolioTotals();
    const marketValue = liveTotals ? liveTotals.marketValue : asset.market_value;
    const positionProfit = liveTotals ? liveTotals.positionProfit : asset.position_profit;
    const dayPositionProfit = liveTotals ? liveTotals.dayPositionProfit : asset.day_unrealized_pnl;
    const stockValue = liveTotals ? liveTotals.stockValue : asset.stock_value;
    const fundValue = liveTotals ? liveTotals.fundValue : asset.fund_value;
    const commission = metricCommission(asset);
    const closeProfit = metricCloseProfit(asset);
    const dayProfit = metricDayProfit(asset, dayPositionProfit, closeProfit, commission, Boolean(liveTotals));
    const netAsset = liveTotals ? liveTotals.netAsset : asset.net_asset;
    els.netAsset.textContent = formatNumber(netAsset);
    els.cashAvailable.textContent = formatNumber(asset.cash_available);
    els.marketValue.textContent = formatNumber(marketValue);
    els.dayProfit.textContent = formatSigned(dayProfit);
    els.dayProfit.className = Number(dayProfit) < 0 ? "down" : "up";
    els.cashTotal.textContent = formatNumber(asset.cash_total);
    els.stockValue.textContent = formatNumber(stockValue);
    els.fundValue.textContent = formatNumber(fundValue);
    els.positionProfit.textContent = formatSigned(positionProfit);
    els.positionProfit.className = Number(positionProfit) < 0 ? "down" : "up";
    els.closeProfit.textContent = formatSigned(closeProfit);
    els.closeProfit.className = Number(closeProfit) < 0 ? "down" : "up";
    els.commission.textContent = formatNumber(commission);
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

  function metricCommission(asset) {
    const assetCommission = finiteNumber(asset && asset.commission);
    if (assetCommission !== null) {
      return assetCommission;
    }
    if (state.metricFillsAccount !== state.activeAccount ||
      state.metricFillsLoadedDate !== selectedAssetTradeDateSafe() ||
      state.metricFillsDirty) {
      return null;
    }
    return state.metricFills.reduce((total, fill) => total + fillFee(fill), 0);
  }

  function fillFee(fill) {
    const direct = finiteNumber(fill && fill.fee);
    if (direct !== null) {
      return direct;
    }
    const context = fill && fill.adapter_context;
    const adapterFee = finiteNumber(context && (context.fee ?? context.nFee));
    return adapterFee !== null ? adapterFee : 0;
  }

  function metricCloseProfit(asset) {
    const assetCloseProfit = finiteNumber(asset && asset.close_profit);
    if (assetCloseProfit !== null) {
      return assetCloseProfit;
    }
    return estimatedCloseProfit();
  }

  function metricDayProfit(asset, dayPositionProfit, closeProfit, commission, preferComputed = false) {
    const assetDayProfit = finiteNumber(asset && asset.day_profit);
    if (!preferComputed && assetDayProfit !== null) {
      return assetDayProfit;
    }
    const parts = [dayPositionProfit, closeProfit, commission].map(finiteNumber);
    if (parts.every((value) => value === null)) {
      return assetDayProfit;
    }
    return (parts[0] || 0) + (parts[1] || 0) - (parts[2] || 0);
  }

  function estimatedCloseProfit() {
    const tradeDate = selectedAssetTradeDateSafe();
    if (state.metricFillsAccount !== state.activeAccount ||
      state.metricFillsLoadedDate !== tradeDate ||
      state.metricFillsDirty ||
      state.allPositionsAccount !== state.activeAccount ||
      state.allPositionsLoadedDate !== tradeDate ||
      state.positionStatsDirty) {
      return null;
    }
    const avgCostBySecurity = new Map();
    for (const position of state.allPositions) {
      const securityID = itemSecurityID(position);
      const avgCost = finiteNumber(position.avg_cost);
      if (securityID && avgCost !== null) {
        avgCostBySecurity.set(securityID, avgCost);
      }
    }
    let total = 0;
    let hasEstimate = false;
    for (const fill of state.metricFills) {
      if (String(fill.trade_side || "").toUpperCase() !== "S") {
        continue;
      }
      const securityID = itemSecurityID(fill);
      const avgCost = avgCostBySecurity.get(securityID);
      const price = finiteNumber(fill.price);
      const qty = finiteNumber(fill.qty);
      if (avgCost === undefined || price === null || qty === null) {
        continue;
      }
      total += (price - avgCost) * qty;
      hasEstimate = true;
    }
    return hasEstimate ? total : null;
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
    const ledgerDayPnl = finiteNumber(position.day_unrealized_pnl);
    let dayPnl = ledgerDayPnl;
    let dayCostAmount = null;
    if (ledgerMarketValue !== null && ledgerDayPnl !== null) {
      dayCostAmount = ledgerMarketValue - ledgerDayPnl;
    } else if (qty !== null && price !== null) {
      const todayQty = effectiveTodayPositionQty(position);
      const oldQty = Math.max(qty - todayQty, 0);
      const openPrice = finiteNumber(quote && quote.open);
      if (oldQty > 0 && openPrice !== null && openPrice > 0) {
        dayCostAmount = oldQty * openPrice;
      }
      if (todayQty > 0 && avgCost !== null && avgCost > 0) {
        dayCostAmount = (dayCostAmount || 0) + todayQty * avgCost;
      }
    }
    if (marketValue !== null && dayCostAmount !== null && dayCostAmount > 0) {
      dayPnl = marketValue - dayCostAmount;
    }
    let dayPnlRatio = null;
    if (dayPnl !== null && dayCostAmount !== null && dayCostAmount !== 0) {
      dayPnlRatio = dayPnl / dayCostAmount * 100;
    }
    return {
      quote,
      quoteItem: quote || position,
      price,
      marketValue,
      pnl,
      pnlRatio,
      dayPnl,
      dayPnlRatio
    };
  }

  function effectiveTodayPositionQty(position) {
    const qty = finiteNumber(position && position.quantity);
    if (qty === null || qty <= 0) {
      return 0;
    }
    const todayQty = finiteNumber(position.today_qty);
    if (todayQty !== null && todayQty > 0) {
      return Math.min(todayQty, qty);
    }
    const sellableQty = finiteNumber(position.sellable_qty);
    if (sellableQty !== null && sellableQty >= 0 && sellableQty < qty) {
      return qty - sellableQty;
    }
    return 0;
  }

  function sortedRows(rows, table) {
    const sort = state.tableSorts[table];
    if (!sort || !sort.key) {
      return rows.slice();
    }
    return rows.map((row, index) => ({ row, index }))
      .sort((left, right) => {
        const compared = compareSortValues(
          tableSortValue(left.row, table, sort.key),
          tableSortValue(right.row, table, sort.key),
          sort.direction
        );
        return compared || left.index - right.index;
      })
      .map((item) => item.row);
  }

  function compareSortValues(left, right, direction) {
    const dir = direction === "asc" ? 1 : -1;
    const leftEmpty = left === null || left === undefined || left === "";
    const rightEmpty = right === null || right === undefined || right === "";
    if (leftEmpty && rightEmpty) {
      return 0;
    }
    if (leftEmpty) {
      return 1;
    }
    if (rightEmpty) {
      return -1;
    }
    if (typeof left === "number" && typeof right === "number") {
      return (left - right) * dir;
    }
    return String(left).localeCompare(String(right), "zh-CN", {
      numeric: true,
      sensitivity: "base"
    }) * dir;
  }

  function tableSortValue(row, table, key) {
    if (table === "positions") {
      return positionSortValue(row, key);
    }
    if (table === "orders") {
      return orderSortValue(row, key);
    }
    if (table === "fills") {
      return fillSortValue(row, key);
    }
    if (table === "transfers") {
      return transferSortValue(row, key);
    }
    return "";
  }

  function positionSortValue(position, key) {
    const view = livePositionView(position);
    switch (key) {
    case "symbol":
      return symbolText(position);
    case "name":
      return securityNameText(position);
    case "quantity":
      return finiteNumber(position.quantity);
    case "sellable_qty":
      return finiteNumber(position.sellable_qty);
    case "avg_cost":
      return finiteNumber(position.avg_cost);
    case "last_price":
      return finiteNumber(view.price);
    case "market_value":
      return finiteNumber(view.marketValue);
    case "pnl":
      return finiteNumber(view.pnl);
    case "day_pnl":
      return finiteNumber(view.dayPnl);
    case "updated_at":
      return timeSortValue(position.updated_at);
    default:
      return "";
    }
  }

  function orderSortValue(order, key) {
    switch (key) {
    case "req_id":
      return order.client_order_id || order.gateway_order_id || "";
    case "symbol":
      return symbolText(order);
    case "name":
      return securityNameText(order);
    case "side":
      return String(order.trade_side || "");
    case "price":
      return finiteNumber(order.limit_price);
    case "quantity":
      return finiteNumber(order.order_qty);
    case "filled_qty":
      return finiteNumber(order.cum_filled_qty);
    case "counter":
      return String(order.order_id || order.order_stream_id || "");
    case "status":
      return String(order.status || "");
    case "created_at":
      return timeSortValue(order.created_at || order.inserted_at);
    case "updated_at":
      return timeSortValue(order.last_updated_at || order.terminal_at || order.accepted_at || order.created_at || order.inserted_at);
    default:
      return "";
    }
  }

  function fillSortValue(fill, key) {
    const order = orderForFill(fill);
    switch (key) {
    case "fill_id":
      return fill.fill_id || "";
    case "req_id":
      return order.client_order_id || fill.gateway_order_id || "";
    case "counter":
      return String(fill.order_id || order.order_id || fill.order_stream_id || order.order_stream_id || "");
    case "symbol":
      return symbolText(fill);
    case "name":
      return securityNameText(fill, order);
    case "side":
      return String(fill.trade_side || order.trade_side || "");
    case "price":
      return finiteNumber(fill.price);
    case "quantity":
      return finiteNumber(fill.qty);
    case "matched_at":
      return timeSortValue(fill.matched_at);
    default:
      return "";
    }
  }

  function transferSortValue(transfer, key) {
    const order = orderForFill(transfer);
    switch (key) {
    case "fill_id":
      return transfer.fill_id || "";
    case "req_id":
      return order.client_order_id || transfer.gateway_order_id || "";
    case "counter":
      return String(transfer.order_id || order.order_id || transfer.order_stream_id || order.order_stream_id || "");
    case "symbol":
      return symbolText(transfer);
    case "name":
      return securityNameText(transfer, order);
    case "side":
      return String(transfer.trade_side || order.trade_side || "");
    case "quantity":
      return finiteNumber(transfer.component_qty || transfer.qty);
    case "matched_at":
      return timeSortValue(transfer.matched_at);
    default:
      return "";
    }
  }

  function timeSortValue(value) {
    if (!value) {
      return null;
    }
    const timestamp = new Date(value).getTime();
    return Number.isFinite(timestamp) ? timestamp : null;
  }

  function defaultSortDirection(_table, key) {
    if (key === "symbol" || key === "name" || key === "req_id" || key === "fill_id" || key === "side" || key === "status") {
      return "asc";
    }
    return "desc";
  }

  function setTableSort(table, key) {
    if (!state.tableSorts[table]) {
      return;
    }
    const current = state.tableSorts[table];
    if (current.key === key) {
      current.direction = current.direction === "asc" ? "desc" : "asc";
    } else {
      state.tableSorts[table] = {
        key,
        direction: defaultSortDirection(table, key)
      };
    }
    if (table === "positions") {
      if (clientPositionPagingEnabled()) {
        state.positionsPage.page = 1;
      }
      renderPositions();
      return;
    }
    if (table === "orders" || table === "fills" || table === "transfers") {
      renderBlotter();
    }
  }

  function updateSortHeaders(table) {
    const sort = state.tableSorts[table];
    const headers = document.querySelectorAll('th.sortable[data-sort-table="' + table + '"]');
    for (const header of headers) {
      const active = sort && header.dataset.sortKey === sort.key;
      header.classList.toggle("sorted", active);
      header.classList.toggle("asc", active && sort.direction === "asc");
      header.classList.toggle("desc", active && sort.direction === "desc");
      header.setAttribute("aria-sort", active ? (sort.direction === "asc" ? "ascending" : "descending") : "none");
    }
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
    let dayPositionProfit = 0;
    let stockValue = 0;
    let fundValue = 0;
    let unclassifiedValue = 0;
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
      const rowDayPnl = finiteNumber(view.dayPnl);
      if (rowDayPnl !== null) {
        dayPositionProfit += rowDayPnl;
      }
      if (rowMarketValue !== null) {
        const instrumentType = String(instrumentTypeForItem(view.quoteItem) || "").toLowerCase();
        if (instrumentType === "etf") {
          fundValue += rowMarketValue;
        } else if (instrumentType) {
          stockValue += rowMarketValue;
        } else {
          unclassifiedValue += rowMarketValue;
        }
      }
    }
    const cashTotal = finiteNumber(state.asset && state.asset.cash_total);
    return {
      marketValue,
      positionProfit,
      dayPositionProfit,
      stockValue: unclassifiedValue > 0 ? null : stockValue,
      fundValue: unclassifiedValue > 0 ? null : fundValue,
      netAsset: cashTotal !== null ? cashTotal + marketValue : (state.asset && state.asset.net_asset)
    };
  }

  function renderPositions() {
    const rows = positionTableRows();
    if (rows.length === 0) {
      els.positionsBody.innerHTML = '<tr><td colspan="9"><div class="empty-state">暂无 ' + escapeHTML(displayDate(selectedAssetTradeDateSafe())) + ' 持仓数据</div></td></tr>';
      updateSortHeaders("positions");
      renderPositionsPager();
      return;
    }
    els.positionsBody.innerHTML = rows.map((position) => {
      const view = livePositionView(position);
      const pnl = view.pnl;
      const pnlClass = Number(pnl) < 0 ? "down" : "up";
      const pnlRatio = view.pnlRatio;
      const dayPnl = view.dayPnl;
      const dayPnlClass = Number(dayPnl) < 0 ? "down" : "up";
      const dayPnlRatio = view.dayPnlRatio;
      const avgCost = finiteNumber(position.avg_cost);
      const priceClass = view.price !== null && avgCost !== null && view.price < avgCost ? "down" : "up";
      const pnlRatioText = pnlRatio === null ? "--" : formatSigned(pnlRatio) + "%";
      const dayPnlRatioText = dayPnlRatio === null ? "--" : formatSigned(dayPnlRatio) + "%";
      const securityID = itemSecurityID(position);
      return `
        <tr class="position-row" data-position-security-id="${escapeHTML(securityID)}">
          <td>${escapeHTML(symbolText(position))}</td>
          <td class="security-name">${escapeHTML(securityNameText(position))}</td>
          <td class="num">${formatInt(position.quantity)}<br><span class="muted">${formatInt(position.sellable_qty)}</span></td>
          <td class="num">${formatPrice(position.avg_cost, view.quoteItem)}<br><span class="${priceClass}">${formatPrice(view.price, view.quoteItem)}</span></td>
          <td class="num">${formatNumber(view.marketValue)}</td>
          <td class="num ${pnlClass}">${formatSigned(pnl)}<br>${pnlRatioText}</td>
          <td class="num ${dayPnlClass}">${formatSigned(dayPnl)}<br>${dayPnlRatioText}</td>
          <td class="time-cell">${formatShortDateTime(position.updated_at)}</td>
          <td><button type="button" class="row-action" data-sell-security-id="${escapeHTML(securityID)}">卖出</button></td>
        </tr>`;
    }).join("");
    updateSortHeaders("positions");
    renderPositionsPager();
  }

  function positionTableRows() {
    const source = clientPositionPagingEnabled() ? state.allPositions : state.positions;
    const sorted = sortedRows(source, "positions");
    if (!clientPositionPagingEnabled()) {
      return sorted;
    }
    const totalPages = Math.max(1, Math.ceil(sorted.length / state.positionsPage.pageSize));
    state.positionsPage.page = Math.min(Math.max(1, state.positionsPage.page), totalPages);
    const start = (state.positionsPage.page - 1) * state.positionsPage.pageSize;
    return sorted.slice(start, start + state.positionsPage.pageSize);
  }

  function clientPositionPagingEnabled() {
    const tradeDate = selectedAssetTradeDateSafe();
    return isCurrentBusinessDate(tradeDate) &&
      !state.positionStatsDirty &&
      state.allPositionsAccount === state.activeAccount &&
      state.allPositionsLoadedDate === tradeDate &&
      state.allPositions.length >= state.positions.length;
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
    els.transferCount.textContent = formatInt(state.transfers.length);
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
    for (const transfer of state.transfers) {
      note(transfer.matched_at);
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
    if (state.selectedTab === "transfers") {
      renderComponentTransfersTable();
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
            <th class="sortable" data-sort-table="orders" data-sort-key="req_id">ReqID</th>
            <th class="sortable" data-sort-table="orders" data-sort-key="symbol">代码</th>
            <th class="sortable" data-sort-table="orders" data-sort-key="name">证券名称</th>
            <th class="sortable" data-sort-table="orders" data-sort-key="side">方向</th>
            <th class="num sortable" data-sort-table="orders" data-sort-key="price">委托价格</th>
            <th class="num sortable" data-sort-table="orders" data-sort-key="quantity">委托/成交</th>
            <th class="sortable" data-sort-table="orders" data-sort-key="counter">柜台/交易所</th>
            <th class="sortable" data-sort-table="orders" data-sort-key="status">状态</th>
            <th>错误/柜台信息</th>
            <th class="sortable" data-sort-table="orders" data-sort-key="created_at">委托时间</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          ${sortedRows(state.orders, "orders").map((order) => {
            const id = order.gateway_order_id || order.client_order_id || "";
            const changedAt = state.changedOrders.get(id) || 0;
            const className = [
              id === state.selectedOrderID ? "selected" : "",
              now - changedAt < 3600 ? "flash" : ""
            ].join(" ");
            const debugText = orderDebugText(order);
            const cancelAction = order.is_terminal
              ? '<span class="muted">已完成</span>'
              : accountTradingEnabled(state.activeAccount)
                ? '<button type="button" class="row-action" data-cancel-id="' + escapeHTML(id) + '">撤单</button>'
                : '<span class="muted">只读</span>';
            return `
              <tr class="${className}" data-order-id="${escapeHTML(id)}">
                <td><span class="row-title"><strong>${escapeHTML(order.client_order_id || id)}</strong><span>${escapeHTML(id)}</span></span></td>
                <td>${escapeHTML(symbolText(order))}</td>
                <td class="security-name">${escapeHTML(securityNameText(order))}</td>
                <td>${sideBadge(order)}</td>
                <td class="num">${formatPrice(order.limit_price, order)}</td>
                <td class="num">${formatInt(order.order_qty)} / ${formatInt(order.cum_filled_qty)}</td>
                <td><span class="row-title"><strong>${escapeHTML(order.order_id || "--")}</strong><span>${escapeHTML(order.order_stream_id || "--")}</span></span></td>
                <td><span class="status-badge ${escapeHTML(order.status)}">${statusText(order.status)}</span></td>
                <td class="debug-cell"><span class="row-title"><strong class="${debugText ? "down" : "muted"}">${escapeHTML(debugText || "--")}</strong><span>${escapeHTML(order.reject_code || adapterText(order, "relay_error_code") || "")}</span></span></td>
                <td>${formatTime(order.created_at || order.inserted_at)}</td>
                <td>${cancelAction}</td>
              </tr>`;
          }).join("")}
        </tbody>
      </table>`;
    updateSortHeaders("orders");
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
            <th class="sortable" data-sort-table="fills" data-sort-key="fill_id">成交编号</th>
            <th class="sortable" data-sort-table="fills" data-sort-key="req_id">ReqID</th>
            <th class="sortable" data-sort-table="fills" data-sort-key="counter">柜台/交易所</th>
            <th class="sortable" data-sort-table="fills" data-sort-key="symbol">代码</th>
            <th class="sortable" data-sort-table="fills" data-sort-key="name">证券名称</th>
            <th class="sortable" data-sort-table="fills" data-sort-key="side">方向</th>
            <th class="num sortable" data-sort-table="fills" data-sort-key="price">成交价格</th>
            <th class="num sortable" data-sort-table="fills" data-sort-key="quantity">成交数量</th>
            <th class="sortable" data-sort-table="fills" data-sort-key="matched_at">成交时间</th>
          </tr>
        </thead>
        <tbody>
          ${sortedRows(state.fills, "fills").map((fill) => {
            const order = orderForFill(fill);
            return `
              <tr>
                <td>${escapeHTML(fill.fill_id)}</td>
                <td><span class="row-title"><strong>${escapeHTML(order.client_order_id || "--")}</strong><span>${escapeHTML(fill.gateway_order_id)}</span></span></td>
                <td><span class="row-title"><strong>${escapeHTML(fill.order_id || order.order_id || "--")}</strong><span>${escapeHTML(fill.order_stream_id || order.order_stream_id || "--")}</span></span></td>
                <td>${escapeHTML(symbolText(fill))}</td>
                <td class="security-name">${escapeHTML(securityNameText(fill, order))}</td>
                <td>${sideBadge(fill, order)}</td>
                <td class="num">${formatPrice(fill.price, fill)}</td>
                <td class="num">${formatInt(fill.qty)}</td>
                <td>${formatTime(fill.matched_at)}</td>
              </tr>`;
          }).join("")}
        </tbody>
      </table>`;
    updateSortHeaders("fills");
  }

  function renderComponentTransfersTable() {
    if (state.transfers.length === 0) {
      els.blotterContent.innerHTML = '<div class="empty-state">暂无 ' + escapeHTML(displayDate(selectedOrdersTradeDateSafe())) + ' ETF 成分股划转</div>';
      return;
    }
    els.blotterContent.innerHTML = `
      <table>
        <thead>
          <tr>
            <th class="sortable" data-sort-table="transfers" data-sort-key="fill_id">划转编号</th>
            <th class="sortable" data-sort-table="transfers" data-sort-key="req_id">关联委托</th>
            <th class="sortable" data-sort-table="transfers" data-sort-key="counter">柜台/交易所</th>
            <th class="sortable" data-sort-table="transfers" data-sort-key="symbol">成分证券</th>
            <th class="sortable" data-sort-table="transfers" data-sort-key="name">证券名称</th>
            <th class="sortable" data-sort-table="transfers" data-sort-key="side">申赎方向</th>
            <th>划转类型</th>
            <th class="num sortable" data-sort-table="transfers" data-sort-key="quantity">划转数量</th>
            <th>成分金额</th>
            <th class="sortable" data-sort-table="transfers" data-sort-key="matched_at">划转时间</th>
          </tr>
        </thead>
        <tbody>
          ${sortedRows(state.transfers, "transfers").map((transfer) => {
            const order = orderForFill(transfer);
            const componentValue = transfer.component_value === null || transfer.component_value === undefined
              ? "--"
              : formatMoney(transfer.component_value);
            return `
              <tr>
                <td>${escapeHTML(transfer.fill_id || "--")}</td>
                <td><span class="row-title"><strong>${escapeHTML(order.client_order_id || "--")}</strong><span>${escapeHTML(transfer.gateway_order_id || "--")}</span></span></td>
                <td><span class="row-title"><strong>${escapeHTML(transfer.order_id || order.order_id || "--")}</strong><span>${escapeHTML(transfer.order_stream_id || order.order_stream_id || "--")}</span></span></td>
                <td>${escapeHTML(symbolText({ symbol: transfer.component_symbol || transfer.symbol, exchange: transfer.component_exchange || transfer.exchange }))}</td>
                <td class="security-name">${escapeHTML(transfer.component_name || securityNameText(transfer, order))}</td>
                <td>${sideBadge(transfer, order)}</td>
                <td>${escapeHTML(transfer.transfer_type || transfer.record_type || "--")}</td>
                <td class="num">${formatInt(transfer.component_qty || transfer.qty)}</td>
                <td class="num">${componentValue}</td>
                <td>${formatTime(transfer.matched_at)}</td>
              </tr>`;
          }).join("")}
        </tbody>
      </table>`;
    updateSortHeaders("transfers");
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
    if (clientPositionPagingEnabled()) {
      const total = state.allPositions.length;
      const start = total > 0 ? (state.positionsPage.page - 1) * state.positionsPage.pageSize + 1 : 0;
      const end = total > 0 ? Math.min(start + state.positionsPage.pageSize - 1, total) : 0;
      els.positionsPageInfo.textContent = [
        displayDate(selectedAssetTradeDateSafe()),
        "持仓",
        "第 " + state.positionsPage.page + " 页",
        total > 0 ? start + "-" + end + " / " + total : "0 条",
        end < total ? "还有下一页" : "已到末页"
      ].join(" · ");
      els.positionsPrevPage.disabled = state.positionsPage.page <= 1;
      els.positionsNextPage.disabled = end >= total;
      return;
    }
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
    if (state.selectedTab === "transfers") {
      renderPager({
        page: state.transfersPage,
        count: state.transfers.length,
        info: els.ordersPageInfo,
        prev: els.ordersPrevPage,
        next: els.ordersNextPage,
        label: "ETF 划转",
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
        <thead><tr><th>成交编号</th><th>方向</th><th>订单 ID</th><th class="num">价格</th><th class="num">数量</th></tr></thead>
        <tbody>${fills.map((fill) => `
          <tr>
            <td>${escapeHTML(fill.fill_id)}</td>
            <td>${sideBadge(fill, order)}</td>
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
      els.perfBenchmarkInput.value = "000001.SH";
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
    const loadSeq = ++state.performanceLoadSeq;
    const isCurrentLoad = () => loadSeq === state.performanceLoadSeq;
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
      let data = await request("/v1/accounts/" + accountID + "/performance/series?" + query.toString());
      if (!isCurrentLoad()) {
        return;
      }
      if ((!Array.isArray(data.series) || data.series.length === 0) && params.dateFrom === params.dateTo) {
        const fallbackQuery = new URLSearchParams({
          date_from: shiftCompactDate(params.dateTo, -45),
          date_to: params.dateTo
        });
        const fallbackData = await request("/v1/accounts/" + accountID + "/performance/series?" + fallbackQuery.toString());
        if (!isCurrentLoad()) {
          return;
        }
        const fallbackSeries = Array.isArray(fallbackData.series) ? fallbackData.series : [];
        const latestDate = compactDate(fallbackSeries[fallbackSeries.length - 1] && fallbackSeries[fallbackSeries.length - 1].trade_date);
        if (latestDate) {
          params.dateFrom = latestDate;
          params.dateTo = latestDate;
          els.perfDateFrom.value = latestDate;
          els.perfDateTo.value = latestDate;
          query.set("date_from", latestDate);
          query.set("date_to", latestDate);
          data = await request("/v1/accounts/" + accountID + "/performance/series?" + query.toString());
          if (!isCurrentLoad()) {
            return;
          }
          pushLog("info", "绩效日期已回退", displayDate(latestDate) + " 最近已结算交易日");
        }
      }
      state.performanceError = "";
      state.performanceContribution = null;
      state.performanceTradeQuality = null;
      state.performanceCostLedger = null;
      state.performanceEconomicNAV = null;
      state.performanceNAVReconciliation = null;
      state.performanceSummary = data.summary || null;
      state.performanceSeries = Array.isArray(data.series) ? data.series : [];
      state.performanceDaily = state.performanceSeries[state.performanceSeries.length - 1] || null;
      const dailyDate = compactDate(state.performanceDaily && state.performanceDaily.trade_date) || params.dateTo;
      const qualityQuery = new URLSearchParams({
        date_from: params.dateFrom,
        date_to: params.dateTo
      });
      const detailResults = await Promise.allSettled([
        request("/v1/performance/settings"),
        request("/v1/accounts/" + accountID + "/performance/daily?trade_date=" + encodeURIComponent(dailyDate)),
        request("/v1/accounts/" + accountID + "/performance/contributions?trade_date=" + encodeURIComponent(dailyDate)),
        request("/v1/accounts/" + accountID + "/performance/trade-quality?" + qualityQuery.toString()),
        request("/v1/accounts/" + accountID + "/performance/cost-ledger/preview?trade_date=" + encodeURIComponent(dailyDate)),
        request("/v1/accounts/" + accountID + "/performance/economic-nav/preview?trade_date=" + encodeURIComponent(dailyDate)),
        request("/v1/accounts/" + accountID + "/performance/nav-reconciliations?trade_date=" + encodeURIComponent(dailyDate))
      ]);
      if (!isCurrentLoad()) {
        return;
      }
      const [settingsResult, dailyResult, contributionResult, qualityResult, costResult, economicResult, reconciliationResult] = detailResults;

      if (settingsResult.status === "fulfilled") {
        state.performanceSettings = settingsResult.value;
        state.performanceReviewSettingsError = "";
      } else {
        state.performanceSettings = null;
        state.performanceReviewSettingsError = settingsResult.reason.message;
        pushLog("warn", "绩效写权限读取失败", settingsResult.reason.message);
      }

      if (dailyResult.status === "fulfilled") {
        state.performanceDaily = dailyResult.value.performance || state.performanceDaily;
      } else {
        pushLog("warn", "日终快照读取失败", displayDate(dailyDate) + " " + dailyResult.reason.message);
      }

      if (contributionResult.status === "fulfilled") {
        state.performanceContribution = contributionResult.value.contribution || null;
      } else {
        state.performanceContribution = null;
        pushLog("warn", "证券贡献读取失败", displayDate(dailyDate) + " " + contributionResult.reason.message);
      }

      if (qualityResult.status === "fulfilled") {
        state.performanceTradeQuality = qualityResult.value.trade_quality || null;
      } else {
        state.performanceTradeQuality = null;
        pushLog("warn", "交易质量读取失败", displayDate(params.dateFrom) + " 至 " + displayDate(params.dateTo) + " " + qualityResult.reason.message);
      }

      if (costResult.status === "fulfilled") {
        state.performanceCostLedger = costResult.value.cost_ledger || null;
      } else {
        state.performanceCostLedger = null;
        pushLog("warn", "持仓成本账读取失败", displayDate(dailyDate) + " " + costResult.reason.message);
      }

      if (economicResult.status === "fulfilled") {
        state.performanceEconomicNAV = economicResult.value.economic_nav || null;
      } else {
        state.performanceEconomicNAV = null;
        pushLog("warn", "经济净值预览失败", displayDate(dailyDate) + " " + economicResult.reason.message);
      }

      if (reconciliationResult.status === "fulfilled") {
        state.performanceNAVReconciliation = selectPerformanceNAVReconciliation(
          reconciliationResult.value.reconciliations,
          state.performanceEconomicNAV && state.performanceEconomicNAV.nav
        );
      } else {
        state.performanceNAVReconciliation = null;
        pushLog("warn", "经济净值对账读取失败", displayDate(dailyDate) + " " + reconciliationResult.reason.message);
      }
      state.performanceLoaded = true;
      renderPerformance();
      showToast("绩效数据已更新");
    } catch (err) {
      if (!isCurrentLoad()) {
        return;
      }
      state.performanceLoaded = false;
      state.performanceError = err.message;
      state.performanceSummary = null;
      state.performanceSeries = [];
      state.performanceDaily = null;
      state.performanceContribution = null;
      state.performanceTradeQuality = null;
      state.performanceCostLedger = null;
      state.performanceEconomicNAV = null;
      state.performanceNAVReconciliation = null;
      pushLog("error", "绩效查询失败", err.message);
      showToast("绩效查询失败：" + err.message, "error");
      renderPerformance();
    } finally {
      if (isCurrentLoad()) {
        els.loadPerformanceButton.disabled = false;
      }
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

  function selectPerformanceNAVReconciliation(items, nav) {
    const rows = Array.isArray(items) ? items : [];
    if (rows.length === 0) {
      return null;
    }
    const navPK = nav && nav.performance_nav_pk;
    if (navPK) {
      const matched = rows.find((item) => item && item.performance_nav_pk === navPK);
      if (matched) {
        return matched;
      }
    }
    const priority = { blocked: 0, review_required: 1, auto_completed: 2, confirmed: 3 };
    return rows
      .slice()
      .sort((a, b) => (priority[a && a.status] ?? 9) - (priority[b && b.status] ?? 9))[0] || null;
  }

  function navReconciliationStatusInfo(status) {
    switch (status) {
    case "auto_completed":
      return { label: "自动通过", headline: "对账差异在自动阈值内" };
    case "review_required":
      return { label: "待人工确认", headline: "对账差异需要人工复核" };
    case "blocked":
      return { label: "已阻断", headline: "经济净值已被阻断" };
    case "confirmed":
      return { label: "已确认", headline: "对账已确认，经济净值已定稿" };
    default:
      return { label: "暂无记录", headline: "当前交易日暂无 NAV 对账记录" };
    }
  }

  function renderNAVReconciliation(reconciliation, nav) {
    if (!els.navReconciliationPanel) {
      return;
    }
    const item = reconciliation && reconciliation.reconciliation_id ? reconciliation : {};
    const status = item.status || "missing";
    const statusInfo = navReconciliationStatusInfo(status);
    const autoThreshold = Number(item.auto_threshold);
    const warningThreshold = Number(item.warning_threshold);
    const residual = Number(item.residual);
    const thresholdForBar = Number.isFinite(warningThreshold) && warningThreshold > 0
      ? warningThreshold
      : (Number.isFinite(autoThreshold) && autoThreshold > 0 ? autoThreshold : 0);
    const residualRatio = thresholdForBar > 0 && Number.isFinite(residual)
      ? Math.min(Math.abs(residual) / thresholdForBar * 100, 100)
      : 0;
    const writeEnabled = Boolean(state.performanceSettings && state.performanceSettings.settings_write_enabled);
    const hasRecord = Boolean(item.reconciliation_id);
    const reviewedBy = item.reviewed_by || "";
    const reviewedAt = item.reviewed_at ? shortDateTime(item.reviewed_at) : "";
    const reviewedText = reviewedBy
      ? "复核 " + reviewedBy + (reviewedAt ? " · " + reviewedAt : "")
      : "尚未人工复核";

    els.navReconciliationPanel.dataset.status = status;
    els.navReconciliationStatus.textContent = statusInfo.label;
    els.navReconciliationHeadline.textContent = statusInfo.headline;
    els.navReconciliationDates.textContent = hasRecord
      ? [
        "归属 " + displayDate(item.trade_date),
        "观测 " + displayDate(item.observed_trade_date),
        item.reconciliation_id
      ].filter(Boolean).join(" · ")
      : "--";
    els.navReconciliationBookNAV.textContent = formatNumber(item.provisional_close_nav ?? nav.close_economic_nav);
    els.navReconciliationObservedNAV.textContent = formatNumber(item.observed_open_assets);
    els.navReconciliationResidual.textContent = formatSigned(item.residual);
    els.navReconciliationResidualBar.style.width = residualRatio.toFixed(2) + "%";
    els.navReconciliationThresholds.textContent = formatNumber(item.auto_threshold) + " / " + formatNumber(item.warning_threshold);
    els.navReconciliationCash.textContent = formatNumber(item.observed_visible_cash);
    els.navReconciliationPositions.textContent = formatNumber(item.observed_position_value);
    els.navReconciliationReviewMeta.textContent = reviewedText;

    els.navReconciliationWriteState.className = "";
    if (state.performanceReviewSettingsError) {
      els.navReconciliationWriteState.textContent = "权限读取失败";
      els.navReconciliationWriteState.classList.add("error");
    } else if (writeEnabled) {
      els.navReconciliationWriteState.textContent = "允许复核写入";
    } else {
      els.navReconciliationWriteState.textContent = "只读模式";
      els.navReconciliationWriteState.classList.add("readonly");
    }

    const disabledReason = state.performanceReviewSettingsError
      ? "绩效写权限读取失败"
      : (!writeEnabled ? "服务端未开启 performance.settings_write_enabled" : (!hasRecord ? "当前交易日暂无可复核记录" : ""));
    const commonDisabled = state.performanceReviewBusy || !writeEnabled || !hasRecord;
    els.navReviewOperator.disabled = commonDisabled;
    els.navReviewNote.disabled = commonDisabled;
    els.navReviewForce.disabled = commonDisabled || status === "confirmed";
    els.confirmNAVReconciliationButton.disabled = commonDisabled || status === "confirmed";
    els.blockNAVReconciliationButton.disabled = commonDisabled || status === "blocked";
    els.confirmNAVReconciliationButton.textContent = state.performanceReviewBusy ? "处理中..." : "确认并定稿";
    els.blockNAVReconciliationButton.textContent = state.performanceReviewBusy ? "处理中..." : "阻断";
    els.confirmNAVReconciliationButton.title = disabledReason;
    els.blockNAVReconciliationButton.title = disabledReason;
  }

  function setPerformanceTableView(view) {
    const supported = new Set(["contributions", "series", "trade-quality"]);
    state.performanceTableView = supported.has(view) ? view : "contributions";
    for (const button of els.performanceTableViewButtons) {
      const active = button.dataset.performanceTableView === state.performanceTableView;
      button.classList.toggle("active", active);
      button.setAttribute("aria-selected", active ? "true" : "false");
    }
    for (const panel of els.performanceTablePanels) {
      panel.classList.toggle("active", panel.dataset.performanceTablePanel === state.performanceTableView);
    }
  }

  function performanceStrategyLabel(value) {
    return {
      stock_cross_section: "股票截面",
      etf_cross_section: "ETF 截面",
      etf_redemption_t0: "ETF 申赎 T0",
      cash_management: "现金管理",
      etf_component_transfer: "ETF 成分划转",
      unattributed: "待归因"
    }[value] || value || "待归因";
  }

  function contributionStatusLabel(value) {
    return {
      calculated: "已计算",
      estimated: "估算",
      missing: "缺失",
      excluded: "已排除"
    }[value] || value || "--";
  }

  function renderPerformanceContributions() {
    if (!els.performanceContributionBody) {
      return;
    }
    const contribution = state.performanceContribution || {};
    const summary = contribution.summary || {};
    const rows = Array.isArray(contribution.contributions) ? contribution.contributions : [];
    const strategies = Array.isArray(contribution.strategies) ? contribution.strategies : [];
    els.contributionNetTotal.textContent = formatSigned(summary.net_contribution);
    els.contributionNetTotal.className = classForNumber(summary.net_contribution);
    els.contributionBPSTotal.textContent = formatSigned(summary.contribution_bps);
    els.contributionBPSTotal.className = classForNumber(summary.contribution_bps);
    els.contributionQualityCount.textContent = formatInt(summary.estimated_items) + " / " + formatInt(summary.missing_items);
    els.performanceStrategySummary.innerHTML = strategies.slice(0, 5).map((item) => {
      const label = performanceStrategyLabel(item.strategy_type);
      return `<span title="${escapeHTML(label + " " + formatSigned(item.net_contribution))}">${escapeHTML(label)} ${formatSigned(item.net_contribution)}</span>`;
    }).join("");

    if (rows.length === 0) {
      els.performanceContributionBody.innerHTML = '<tr><td colspan="10"><div class="empty-state">当前交易日暂无可归因证券，或归因输入尚未完成</div></td></tr>';
      return;
    }
    els.performanceContributionBody.innerHTML = rows.map((item) => {
      const flags = Array.isArray(item.quality_flags) ? item.quality_flags : [];
      const qualityText = flags.length ? flags.join(" / ") : "quality ok";
      const status = item.pnl_status || "missing";
      const valuation = item.estimated_exit_value ?? item.sell_amount;
      return `
        <tr>
          <td><span class="contribution-status ${escapeHTML(status)}">${escapeHTML(performanceStrategyLabel(item.strategy_type))}</span></td>
          <td class="contribution-security">
            <strong>${escapeHTML(item.security_id || item.symbol || "--")}</strong>
            <span>${escapeHTML(item.name || item.instrument_type || "--")}</span>
          </td>
          <td class="num">${formatInt(item.open_quantity)} / ${formatInt(item.close_quantity)}</td>
          <td class="num">${formatNumber(item.buy_amount)}</td>
          <td class="num">${formatNumber(valuation)}</td>
          <td class="num">${formatNumber(item.turnover)}</td>
          <td class="num">${formatNumber(item.effective_fee)}</td>
          <td class="num ${classForNumber(item.net_contribution)}">${formatSigned(item.net_contribution)}</td>
          <td class="num ${classForNumber(item.contribution_bps)}">${formatSigned(item.contribution_bps)}</td>
          <td class="contribution-quality" title="${escapeHTML(qualityText)}">
            <strong>${escapeHTML(contributionStatusLabel(status))} · ${escapeHTML(item.fee_source || "--")}</strong>
            <span>${escapeHTML(item.estimation_method || qualityText)}</span>
          </td>
        </tr>
      `;
    }).join("");
  }

  function tradeQualityFlagLabel(value) {
    return {
      rejected_order: "拒单",
      non_terminal_order: "未终态",
      invalid_order_quantity: "委托量无效",
      invalid_quantity: "废单数量",
      order_fill_quantity_mismatch: "成交量不一致",
      filled_quantity_exceeds_order: "成交量超委托",
      terminal_flag_conflict: "终态标记冲突",
      status_gateway_conflict: "状态冲突",
      filled_status_quantity_conflict: "成交终态冲突",
      fill_order_security_mismatch: "成交证券错配",
      fill_order_side_mismatch: "成交方向错配",
      fill_order_business_type_mismatch: "业务类型错配",
      terminal_time_missing: "终态时间缺失",
      terminal_before_created: "终态早于委托",
      terminal_trade_date_mismatch: "终态跨交易日",
      broker_error_message: "柜台错误",
      orphan_fill: "成交缺委托"
    }[value] || value || "异常";
  }

  function formatQualityRate(value) {
    const number = Number(value);
    return Number.isFinite(number) ? formatNumber(number * 100, 2) + "%" : "--";
  }

  function renderPerformanceTradeQuality() {
    if (!els.tradeQualityBody) {
      return;
    }
    const quality = state.performanceTradeQuality || {};
    const summary = quality.summary || {};
    const rows = Array.isArray(quality.anomalies) ? quality.anomalies : [];
    els.tradeQualityOrders.textContent = formatInt(summary.orders);
    els.tradeQualityExecutionRate.textContent = formatQualityRate(summary.executed_order_rate);
    els.tradeQualityFullRate.textContent = formatQualityRate(summary.full_fill_rate);
    els.tradeQualityQuantityRate.textContent = formatQualityRate(summary.quantity_fill_rate);
    els.tradeQualityCancelReject.textContent = formatInt(summary.cancelled_orders) + " / " + formatInt(summary.rejected_orders);
    els.tradeQualityOpen.textContent = formatInt(summary.non_terminal_orders);
    els.tradeQualityAnomalies.textContent = formatInt(summary.anomaly_items);

    if (rows.length === 0) {
      const message = summary.orders > 0 ? "当前区间未发现交易质量异常" : "当前区间暂无委托记录";
      els.tradeQualityBody.innerHTML = '<tr><td colspan="8"><div class="empty-state">' + escapeHTML(message) + "</div></td></tr>";
      return;
    }
    els.tradeQualityBody.innerHTML = rows.map((item) => {
      const flags = Array.isArray(item.flags) ? item.flags : [];
      const flagText = flags.map(tradeQualityFlagLabel);
      const reason = item.broker_message || item.reject_message || flagText.join(" / ");
      const status = item.status || "orphan";
      const statusLabel = item.status ? statusText(item.status) : "成交无委托";
      const security = item.security_id || "--";
      const quantityDelta = Number(item.fill_quantity_delta);
      return `
        <tr>
          <td>${escapeHTML(displayDate(item.trade_date))}</td>
          <td class="trade-quality-security">
            <strong>${escapeHTML(security)}</strong>
            <span>${escapeHTML(item.name || item.gateway_order_id || "--")}</span>
          </td>
          <td>${sideBadge(item)}<br><span class="muted">${escapeHTML(item.business_type || "--")}</span></td>
          <td class="num">${formatInt(item.order_quantity)} / ${formatInt(item.reported_filled_quantity)} / ${formatInt(item.ledger_filled_quantity)}</td>
          <td><span class="status-badge ${escapeHTML(status)}">${escapeHTML(statusLabel)}</span></td>
          <td class="num ${classForNumber(quantityDelta)}">${formatSigned(quantityDelta)}</td>
          <td class="trade-quality-reason" title="${escapeHTML(reason)}">
            <strong>${flags.slice(0, 2).map((flag) => '<i class="quality-flag">' + escapeHTML(tradeQualityFlagLabel(flag)) + "</i>").join("")}</strong>
            <span>${escapeHTML(reason || item.gateway_order_id || "--")}</span>
          </td>
          <td>${escapeHTML(shortDateTime(item.last_updated_at))}</td>
        </tr>
      `;
    }).join("");
  }

  function performanceQualityFlagLabel(value) {
    return {
      missing_open_asset: "缺少日初资产",
      open_asset_fallback: "日初资产使用回退值",
      overnight_adjustment_unclassified: "隔夜调整待分类",
      missing_nav_baseline: "缺少经济净值基线",
      missing_previous_economic_nav: "缺少上一日经济净值",
      reverse_repo_accrual_preview: "逆回购应计为预估",
      missing_repo_fee: "缺少逆回购费用",
      strategy_attribution_pending: "策略归因待完成",
      missing_iopv: "缺少 IOPV",
      minute_iopv_fallback: "IOPV 使用分钟回退",
      missing_meridian_etf_redemption_unit: "缺少 ETF 最小申赎单位",
      redemption_quantity_not_pcf_unit_multiple: "赎回量未通过 PCF 单位校验",
      ambiguous_t0_order_group: "T0 订单组存在歧义",
      incomplete_t0_order_group: "T0 订单组未闭合",
      missing_transfer_link: "缺少成分划转关联",
      research_position_valuation: "持仓使用 Meridian 行情重估",
      broker_position_cost_excluded: "柜台持仓成本已隔离",
      broker_unrealized_pnl_excluded: "柜台累计浮盈已隔离",
      performance_inception_baseline: "使用可信绩效起算点",
      nav_contribution_residual_exceeds_warning: "净值与贡献残差超限",
      net_performance_fee_incomplete: "净收益费用不完整",
      performance_nav_blocked: "正式绩效已阻断",
      excluded_from_official_performance_series: "未纳入正式绩效曲线",
      official_performance_nav_unavailable: "缺少正式 v2 净值",
      cash_only_snapshot_not_performance_nav: "现金快照不作为绩效净值",
      position_quantity_not_reconciled: "持仓数量未闭合",
      position_quantity_bridge_incomplete: "持仓数量桥不完整",
      external_flow_time_estimated_mid_session: "外部资金发生时间按盘中估算"
    }[value] || value || "未知质量标记";
  }

  function performanceQualityChecks() {
    const series = state.performanceSeries || [];
    const summary = state.performanceSummary || {};
    const daily = state.performanceDaily || series[series.length - 1] || {};
    const contribution = state.performanceContribution || {};
    const contributionSummary = contribution.summary || {};
    const tradeQuality = state.performanceTradeQuality || {};
    const tradeSummary = tradeQuality.summary || {};
    const economic = state.performanceEconomicNAV || {};
    const nav = economic.nav || {};
    const reconciliation = state.performanceNAVReconciliation || economic.reconciliation || {};
    const flags = Array.from(new Set(
      series.flatMap((item) => Array.isArray(item.quality_flags) ? item.quality_flags : [])
        .concat(Array.isArray(economic.quality_flags) ? economic.quality_flags : [])
        .concat(Array.isArray(nav.quality_flags) ? nav.quality_flags : [])
    ));
    const checks = [];

    let snapshotStatus = "passed";
    let snapshotDetail = formatInt(series.length) + " 个 close 样本，日初来源 " + (daily.open_snapshot_source || "--");
    if (series.length === 0) {
      snapshotStatus = "blocked";
      snapshotDetail = "所选区间没有可用的 close 资产快照";
    } else if (flags.length > 0) {
      snapshotStatus = flags.some((flag) => String(flag).startsWith("missing_")) ? "blocked" : "warning";
      snapshotDetail = flags.slice(0, 2).map(performanceQualityFlagLabel).join(" / ");
      if (flags.length > 2) {
        snapshotDetail += " +" + (flags.length - 2);
      }
    }
    checks.push({
      label: "资产快照与资金桥",
      detail: snapshotDetail,
      status: snapshotStatus
    });

    const benchmarkDays = Number(summary.benchmark_observation_days) || 0;
    let benchmarkStatus = "passed";
    let benchmarkDetail = (summary.benchmark_security_id || "未设置基准") + " · " + formatInt(benchmarkDays) + "/" + formatInt(series.length) + " 个交易日";
    if (!summary.benchmark_security_id || benchmarkDays === 0) {
      benchmarkStatus = "blocked";
      benchmarkDetail = "缺少 Meridian 基准 bars，无法计算超额与基准回撤";
    } else if (benchmarkDays < series.length) {
      benchmarkStatus = "warning";
      benchmarkDetail += "，存在行情缺口";
    }
    checks.push({
      label: "Meridian 基准行情",
      detail: benchmarkDetail,
      status: benchmarkStatus
    });

    const missingItems = Number(contributionSummary.missing_items) || 0;
    const estimatedItems = Number(contributionSummary.estimated_items) || 0;
    let contributionStatus = "passed";
    let contributionDetail = "证券贡献完整，未使用缺失或估算输入";
    if (!state.performanceContribution) {
      contributionStatus = "warning";
      contributionDetail = "证券贡献接口未返回结果";
    } else if (missingItems > 0) {
      contributionStatus = "blocked";
      contributionDetail = formatInt(missingItems) + " 个缺失项，" + formatInt(estimatedItems) + " 个估算项";
    } else if (estimatedItems > 0) {
      contributionStatus = "warning";
      contributionDetail = formatInt(estimatedItems) + " 个估算项，明细可在证券贡献表核对";
    }
    checks.push({
      label: "收益归因输入",
      detail: contributionDetail,
      status: contributionStatus
    });

    const anomalyItems = Number(tradeSummary.anomaly_items) || 0;
    const nonTerminalOrders = Number(tradeSummary.non_terminal_orders) || 0;
    let ledgerStatus = "passed";
    let ledgerDetail = formatInt(tradeSummary.orders) + " 笔委托，未发现账本一致性异常";
    if (!state.performanceTradeQuality) {
      ledgerStatus = "warning";
      ledgerDetail = "交易质量接口未返回结果";
    } else if (nonTerminalOrders > 0) {
      ledgerStatus = "blocked";
      ledgerDetail = formatInt(nonTerminalOrders) + " 笔未终态，" + formatInt(anomalyItems) + " 个异常项";
    } else if (anomalyItems > 0) {
      ledgerStatus = "warning";
      ledgerDetail = formatInt(anomalyItems) + " 个异常项，进入交易质量表核对";
    }
    checks.push({
      label: "订单与成交账本",
      detail: ledgerDetail,
      status: ledgerStatus
    });

    const costLedger = state.performanceCostLedger || {};
    const costSummary = costLedger.summary || {};
    let costStatus = "warning";
    let costDetail = "当前交易日暂无可信持仓成本结果";
    if (costLedger.status === "calculated") {
      costStatus = "passed";
      costDetail = formatInt(costSummary.securities) + " 个证券，数量桥全部闭合";
    } else if (costLedger.status === "estimated") {
      costDetail = formatInt(costSummary.securities) + " 个证券，" + formatInt(costSummary.missing_fee_items) + " 个费用待配置";
    } else if (costLedger.status === "blocked") {
      costStatus = "blocked";
      costDetail = formatInt(costSummary.quantity_breaks) + " 个数量差异，" + formatInt(costSummary.blocked_items) + " 个成本项阻断";
    }
    checks.push({
      label: "持仓成本连续性",
      detail: costDetail,
      status: costStatus
    });

    const reconciliationStatus = reconciliation.status || "";
    let navStatus = "warning";
    let navDetail = "当前交易日暂无 T+1 NAV 对账记录";
    if (reconciliationStatus === "auto_completed" || reconciliationStatus === "confirmed") {
      navStatus = "passed";
      navDetail = navReconciliationStatusInfo(reconciliationStatus).label + " · 残差 " + formatSigned(reconciliation.residual);
    } else if (reconciliationStatus === "blocked") {
      navStatus = "blocked";
      navDetail = "经济净值已阻断 · 残差 " + formatSigned(reconciliation.residual);
    } else if (reconciliationStatus === "review_required") {
      navDetail = "待人工确认 · 残差 " + formatSigned(reconciliation.residual);
    } else if (nav.status === "finalized") {
      navStatus = "passed";
      navDetail = "经济净值已定稿";
    } else if (nav.status) {
      navDetail = "经济净值 " + nav.status + "，等待 T+1 对账";
    }
    checks.push({
      label: "经济净值与 T+1 对账",
      detail: navDetail,
      status: navStatus
    });

    const targetDate = compactDateLoose(daily.trade_date);
    const jobRuns = state.systemStatus && state.systemStatus.job_runs || {};
    const expectedJobs = ["pre_open_init", "post_close_settlement"];
    const exactRuns = expectedJobs.map((name) => jobRuns[name]).filter((run) => run && compactDateLoose(run.target_trade_date) === targetDate);
    const failedRun = exactRuns.find((run) => run.status === "failed");
    const unfinishedRun = exactRuns.find((run) => !["succeeded", "skipped"].includes(run.status));
    let jobStatus = "passed";
    let jobDetail = "盘前初始化、盘后结算均已完成";
    if (!targetDate || exactRuns.length < expectedJobs.length) {
      jobStatus = "warning";
      const latestDates = expectedJobs.map((name) => compactDateLoose(jobRuns[name] && jobRuns[name].target_trade_date)).filter(Boolean);
      jobDetail = latestDates.length
        ? "所选日无完整任务记录，最近 " + displayDate(latestDates.sort().pop())
        : "尚未读取到日流程任务状态";
    } else if (failedRun) {
      jobStatus = "blocked";
      jobDetail = failedRun.job_name + " 执行失败：" + (failedRun.error_summary || "查看任务中心");
    } else if (unfinishedRun) {
      jobStatus = "warning";
      jobDetail = unfinishedRun.job_name + " 状态 " + unfinishedRun.status;
    }
    checks.push({
      label: "盘前初始化与盘后结算",
      detail: jobDetail,
      status: jobStatus
    });

    return checks;
  }

  function renderPerformanceQuality() {
    if (!els.performanceQualityPanel || !els.performanceQualityList) {
      return;
    }
    const series = state.performanceSeries || [];
    const daily = state.performanceDaily || series[series.length - 1] || {};
    if (!state.performanceLoaded) {
      els.performanceQualityPanel.dataset.status = "waiting";
      els.performanceQualityStatus.textContent = "待检查";
      els.performanceQualityDate.textContent = "等待结算数据";
      els.performanceQualityPassed.textContent = "--";
      els.performanceQualityWarnings.textContent = "--";
      els.performanceQualityBlocked.textContent = "--";
      els.performanceQualityList.innerHTML = '<div class="empty-state">查询绩效后显示快照、行情、归因、对账与任务检查</div>';
      return;
    }
    const checks = performanceQualityChecks();
    const passed = checks.filter((item) => item.status === "passed").length;
    const warnings = checks.filter((item) => item.status === "warning").length;
    const blocked = checks.filter((item) => item.status === "blocked").length;
    const overall = blocked > 0 ? "blocked" : (warnings > 0 ? "warning" : "passed");
    const labels = { passed: "全部通过", warning: "需要关注", blocked: "存在阻断" };
    const statusLabels = { passed: "通过", warning: "提示", blocked: "阻断" };
    const statusSymbols = { passed: "✓", warning: "!", blocked: "×" };
    els.performanceQualityPanel.dataset.status = overall;
    els.performanceQualityStatus.textContent = labels[overall];
    els.performanceQualityDate.textContent = daily.trade_date ? displayDate(daily.trade_date) + " · " + checks.length + " 项检查" : "所选区间";
    els.performanceQualityPassed.textContent = formatInt(passed);
    els.performanceQualityWarnings.textContent = formatInt(warnings);
    els.performanceQualityBlocked.textContent = formatInt(blocked);
    els.performanceQualityList.innerHTML = checks.map((item) => `
      <div class="performance-quality-item" data-status="${escapeHTML(item.status)}" title="${escapeHTML(item.detail)}">
        <i>${escapeHTML(statusSymbols[item.status])}</i>
        <div>
          <strong>${escapeHTML(item.label)}</strong>
          <span>${escapeHTML(item.detail)}</span>
        </div>
        <b>${escapeHTML(statusLabels[item.status])}</b>
      </div>
    `).join("");
  }

  function ensurePerformanceChart() {
    if (!els.performanceChart || !window.echarts) {
      return null;
    }
    if (!state.performanceChart) {
      state.performanceChart = window.echarts.init(els.performanceChart, null, { renderer: "canvas" });
    }
    return state.performanceChart;
  }

  function renderPerformanceChart() {
    if (!els.performanceChart || !els.performanceChartRange) {
      return;
    }
    if (state.activeView !== "performance" && state.activeView !== "snapshots") {
      return;
    }
    const chart = ensurePerformanceChart();
    if (!chart) {
      els.performanceChartRange.textContent = "ECharts 未加载";
      return;
    }
    const rows = (state.performanceSeries || []).slice().sort((a, b) => String(a.trade_date).localeCompare(String(b.trade_date)));
    if (rows.length === 0) {
      chart.clear();
      els.performanceChartRange.textContent = state.performanceError ? "绩效序列读取失败" : "暂无净值序列";
      return;
    }
    const labels = rows.map((item) => displayDate(item.trade_date));
    const accountNAV = rows.map((item) => {
      const value = numericOrNull(item.cumulative_return);
      return value === null ? null : 1 + value;
    });
    const benchmarkNAV = rows.map((item) => {
      const value = numericOrNull(item.benchmark_cumulative_return);
      return value === null ? null : 1 + value;
    });
    const excessReturns = rows.map((item) => {
      const value = numericOrNull(item.excess_cumulative_return);
      return value === null ? null : value * 100;
    });
    const accountDrawdowns = rows.map((item) => {
      const value = numericOrNull(item.drawdown);
      return value === null ? null : value * 100;
    });
    const benchmarkDrawdowns = rows.map((item) => {
      const value = numericOrNull(item.benchmark_drawdown);
      return value === null ? null : value * 100;
    });
    const summary = state.performanceSummary || {};
    els.performanceChartRange.textContent = formatInt(rows.length) + " 个交易日 · close 净值归一化 1.0000 · " + (summary.benchmark_security_id || "未设置基准");
    chart.setOption({
      animationDuration: 260,
      backgroundColor: "transparent",
      color: ["#4ea1ff", "#f0b90b", "#d1d4dc", "#f23645", "#787b86"],
      axisPointer: { link: [{ xAxisIndex: [0, 1] }] },
      tooltip: {
        trigger: "axis",
        backgroundColor: "rgba(17,21,30,0.96)",
        borderColor: "#363a45",
        borderWidth: 1,
        padding: [8, 10],
        textStyle: { color: "#d1d4dc", fontSize: 11 },
        formatter(params) {
          const index = Array.isArray(params) && params.length ? params[0].dataIndex : 0;
          const item = rows[index] || {};
          return [
            "<strong>" + escapeHTML(displayDate(item.trade_date)) + "</strong>",
            "账户净值　" + formatNumber(accountNAV[index], 4) + "　净资产 " + formatNumber(item.net_asset),
            "上证基准　" + formatNumber(benchmarkNAV[index], 4) + "　收盘 " + formatNumber(item.benchmark_close, 2),
            "超额收益　" + formatPercent(item.excess_cumulative_return),
            "账户回撤　" + formatPercent(item.drawdown) + "　基准 " + formatPercent(item.benchmark_drawdown)
          ].join("<br>");
        }
      },
      legend: { show: false },
      grid: [
        { left: 58, right: 58, top: 24, height: "53%" },
        { left: 58, right: 58, top: "72%", bottom: 24 }
      ],
      xAxis: [
        {
          type: "category",
          data: labels,
          boundaryGap: false,
          axisLabel: { show: false },
          axisLine: { lineStyle: { color: "#3a3f4a" } },
          axisTick: { show: false },
          splitLine: { show: false }
        },
        {
          type: "category",
          gridIndex: 1,
          data: labels,
          boundaryGap: false,
          axisLabel: { color: "#6f7480", fontSize: 10, hideOverlap: true },
          axisLine: { lineStyle: { color: "#3a3f4a" } },
          axisTick: { show: false },
          splitLine: { show: false }
        }
      ],
      yAxis: [
        {
          type: "value",
          scale: true,
          name: "净值",
          nameTextStyle: { color: "#6f7480", fontSize: 10 },
          axisLabel: { color: "#6f7480", fontSize: 10, formatter: (value) => Number(value).toFixed(2) },
          axisLine: { show: false },
          axisTick: { show: false },
          splitNumber: 4,
          splitLine: { lineStyle: { color: "#2b303b", opacity: 0.8 } }
        },
        {
          type: "value",
          scale: true,
          position: "right",
          name: "超额",
          nameTextStyle: { color: "#6f7480", fontSize: 10 },
          axisLabel: { color: "#6f7480", fontSize: 10, formatter: (value) => formatNumber(value, 1) + "%" },
          axisLine: { show: false },
          axisTick: { show: false },
          splitLine: { show: false }
        },
        {
          type: "value",
          gridIndex: 1,
          scale: true,
          max: 0,
          name: "回撤",
          nameTextStyle: { color: "#6f7480", fontSize: 10 },
          axisLabel: { color: "#6f7480", fontSize: 10, formatter: (value) => formatNumber(value, 0) + "%" },
          axisLine: { show: false },
          axisTick: { show: false },
          splitNumber: 2,
          splitLine: { lineStyle: { color: "#2b303b", opacity: 0.8 } }
        }
      ],
      dataZoom: [{ type: "inside", xAxisIndex: [0, 1], throttle: 60 }],
      series: [
        {
          name: "账户净值",
          type: "line",
          data: accountNAV,
          showSymbol: false,
          connectNulls: false,
          lineStyle: { width: 2, color: "#4ea1ff" },
          itemStyle: { color: "#4ea1ff" },
          emphasis: { focus: "series" },
          markLine: {
            silent: true,
            symbol: "none",
            label: { show: false },
            lineStyle: { color: "#3a3f4a", type: "dashed", width: 1 },
            data: [{ yAxis: 1 }]
          }
        },
        {
          name: "上证基准",
          type: "line",
          data: benchmarkNAV,
          showSymbol: false,
          connectNulls: false,
          lineStyle: { width: 1.5, color: "#f0b90b" },
          itemStyle: { color: "#f0b90b" },
          emphasis: { focus: "series" }
        },
        {
          name: "超额收益",
          type: "line",
          yAxisIndex: 1,
          data: excessReturns,
          showSymbol: false,
          connectNulls: false,
          lineStyle: { width: 1.5, color: "#d1d4dc", type: "dashed" },
          itemStyle: { color: "#d1d4dc" },
          emphasis: { focus: "series" }
        },
        {
          name: "账户回撤",
          type: "line",
          xAxisIndex: 1,
          yAxisIndex: 2,
          data: accountDrawdowns,
          showSymbol: false,
          connectNulls: false,
          lineStyle: { width: 1.5, color: "#f23645" },
          areaStyle: { color: "rgba(242,54,69,0.10)" },
          itemStyle: { color: "#f23645" },
          emphasis: { focus: "series" }
        },
        {
          name: "基准回撤",
          type: "line",
          xAxisIndex: 1,
          yAxisIndex: 2,
          data: benchmarkDrawdowns,
          showSymbol: false,
          connectNulls: false,
          lineStyle: { width: 1, color: "#787b86", type: "dashed" },
          itemStyle: { color: "#787b86" },
          emphasis: { focus: "series" }
        }
      ]
    }, true);
  }

  function resizePerformanceChart() {
    if (state.performanceChart) {
      state.performanceChart.resize();
    }
  }

  function resizeTerminalCharts() {
    resizeMinuteChart();
    resizePerformanceChart();
  }

  function renderPerformance() {
    if (!els.performanceSeriesBody) {
      return;
    }
    const summary = state.performanceSummary || {};
    const series = state.performanceSeries || [];
    const latest = series[series.length - 1] || state.performanceDaily || {};
    const daily = state.performanceDaily || latest || {};
    const economic = state.performanceEconomicNAV || {};
    const nav = economic.nav || {};
    const reconciliation = state.performanceNAVReconciliation || economic.reconciliation || {};
    const cashFlows = economic.cash_flows || {};
    const navFlags = Array.isArray(economic.quality_flags) ? economic.quality_flags : (Array.isArray(nav.quality_flags) ? nav.quality_flags : []);
    els.performanceRangeHint.textContent = [
      activeAccountLabel() || "未选择账户",
      summary.date_from && summary.date_to ? displayDate(summary.date_from) + " 至 " + displayDate(summary.date_to) : "close 快照序列",
      summary.benchmark_security_id ? "基准 " + summary.benchmark_security_id : "",
      daily.open_snapshot_source ? "日初 " + daily.open_snapshot_source : "",
      nav.status ? "经济净值 " + nav.status : "",
      reconciliation.status ? "对账 " + reconciliation.status : "",
      "Asia/Shanghai"
    ].filter(Boolean).join(" · ");
    els.perfNetAsset.textContent = formatNumber(summary.end_net_asset ?? latest.net_asset);
    els.perfStartNetAsset.textContent = "期初 " + formatNumber(summary.start_net_asset);
    els.perfEconomicNav.textContent = formatNumber(nav.close_economic_nav);
    els.perfEconomicStatus.textContent = [
      nav.status || "preview --",
      reconciliation.status ? "对账 " + reconciliation.status : "",
      economic.persisted ? "persisted" : (nav.close_economic_nav ? "preview" : "")
    ].filter(Boolean).join(" · ");
    els.perfEconomicReturn.textContent = formatPercent(nav.daily_return);
    els.perfEconomicReturn.className = classForNumber(nav.daily_return);
    els.perfEconomicPnl.textContent = "PnL " + formatSigned(nav.account_day_pnl);
    els.perfEconomicPnl.className = classForNumber(nav.account_day_pnl);
    els.perfExternalFlow.textContent = formatSigned(cashFlows.external_net_flow);
    els.perfExternalFlow.className = classForNumber(cashFlows.external_net_flow);
    els.perfQualityFlags.textContent = navFlags.length ? navFlags.slice(0, 2).join(" / ") + (navFlags.length > 2 ? " +" + (navFlags.length - 2) : "") : "质量 ok";
    els.perfOpenNetAsset.textContent = formatNumber(daily.open_net_asset);
    els.perfOpenSource.textContent = [
      daily.open_snapshot_source || "--",
      daily.open_captured_at ? shortDateTime(daily.open_captured_at) : ""
    ].filter(Boolean).join(" · ");
    els.perfOvernightAdjustment.textContent = formatSigned(daily.overnight_adjustment);
    els.perfOvernightAdjustment.className = classForNumber(daily.overnight_adjustment);
    els.perfPreviousNetAsset.textContent = "上日 " + formatNumber(daily.previous_net_asset);
    els.perfIntradayPnl.textContent = formatSigned(daily.intraday_pnl);
    els.perfIntradayPnl.className = classForNumber(daily.intraday_pnl);
    els.perfIntradayReturn.textContent = "收益 " + formatPercent(daily.intraday_return);
    els.perfTotalPnl.textContent = formatSigned(summary.total_pnl);
    els.perfTotalPnl.className = classForNumber(summary.total_pnl);
    els.perfRows.textContent = "样本 " + formatInt(summary.count);
    els.perfTotalReturn.textContent = formatPercent(summary.total_return);
    els.perfTotalReturn.className = classForNumber(summary.total_return);
    els.perfDailyReturn.textContent = "close " + formatPercent(daily.return_rate);
    els.perfMaxDrawdown.textContent = formatPercent(summary.max_drawdown);
    els.perfMaxDrawdown.className = classForNumber(summary.max_drawdown);
    els.perfDailyPnl.textContent = "close " + formatSigned(daily.daily_pnl);
    els.perfBenchmarkReturn.textContent = formatPercent(summary.benchmark_total_return);
    els.perfBenchmarkReturn.className = classForNumber(summary.benchmark_total_return);
    els.perfBenchmarkID.textContent = "基准 " + (summary.benchmark_security_id || "--");
    els.perfExcessReturn.textContent = formatPercent(summary.excess_total_return);
    els.perfExcessReturn.className = classForNumber(summary.excess_total_return);
    els.perfBenchmarkDays.textContent = "bars " + formatInt(summary.benchmark_observation_days);
    els.perfDailyDate.textContent = daily.trade_date ? displayDate(daily.trade_date) : "--";
    els.perfPositions.textContent = formatInt(daily.positions_count);
    els.perfPositionValue.textContent = formatNumber(daily.position_market_value);
    els.perfUnrealizedPnl.textContent = daily.unrealized_pnl_available ? formatSigned(daily.unrealized_pnl) : "--";
    els.perfUnrealizedPnl.className = daily.unrealized_pnl_available ? classForNumber(daily.unrealized_pnl) : "";
    els.perfFills.textContent = formatInt(daily.fills_count);
    els.perfTurnover.textContent = formatNumber(daily.turnover);
    els.perfFee.textContent = formatNumber(daily.fee_total);
    els.perfCapturedAt.textContent = "captured_at " + (daily.captured_at || "--");
    renderNAVReconciliation(reconciliation, nav);
    renderPerformanceContributions();
    renderPerformanceTradeQuality();
    renderPerformanceChart();
    renderPerformanceQuality();
    setPerformanceTableView(state.performanceTableView);
    els.performanceStatus.textContent = state.performanceError
      ? "查询失败：" + state.performanceError
      : (state.performanceLoaded ? "已加载 " + formatInt(series.length) + " 条" : "等待查询");
    if (series.length === 0) {
      els.performanceSeriesBody.innerHTML = '<tr><td colspan="15"><div class="empty-state">暂无 close 快照绩效序列</div></td></tr>';
      return;
    }
    els.performanceSeriesBody.innerHTML = series.map((item) => `
      <tr>
        <td>${escapeHTML(displayDate(item.trade_date))}</td>
        <td class="num">${formatNumber(item.net_asset)}</td>
        <td class="num">${formatNumber(item.open_net_asset)}</td>
        <td class="num ${classForNumber(item.overnight_adjustment)}">${formatSigned(item.overnight_adjustment)}</td>
        <td class="num ${classForNumber(item.intraday_pnl)}">${formatSigned(item.intraday_pnl)}</td>
        <td class="num ${classForNumber(item.intraday_return)}">${formatPercent(item.intraday_return)}</td>
        <td class="num ${classForNumber(item.asset_change)}">${formatSigned(item.asset_change)}</td>
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

  async function reviewNAVReconciliation(action) {
    const item = state.performanceNAVReconciliation || {};
    const writeEnabled = Boolean(state.performanceSettings && state.performanceSettings.settings_write_enabled);
    if (!writeEnabled) {
      throw new Error("服务端未开启绩效复核写权限");
    }
    if (!state.activeAccount || !item.reconciliation_id) {
      throw new Error("当前交易日暂无可复核的 NAV 对账记录");
    }
    const operator = els.navReviewOperator.value.trim();
    const note = els.navReviewNote.value.trim();
    const force = els.navReviewForce.checked;
    if (!operator) {
      throw new Error("请输入复核人");
    }
    if (action === "block" && !note) {
      throw new Error("阻断操作需要填写复核说明");
    }
    const residual = Math.abs(Number(item.residual));
    const warningThreshold = Number(item.warning_threshold);
    const forceRequired = action === "confirm" && (
      item.status === "blocked" ||
      (Number.isFinite(residual) && Number.isFinite(warningThreshold) && warningThreshold > 0 && residual > warningThreshold)
    );
    if (forceRequired && !force) {
      throw new Error("残差超过警告阈值或记录已阻断，请完成复核后勾选强制确认");
    }
    const tradeDate = compactDate(item.trade_date);
    if (!tradeDate) {
      throw new Error("对账记录缺少有效交易日");
    }
    const confirmationText = action === "block"
      ? "确认阻断 " + displayDate(tradeDate) + " 的经济净值？"
      : (force
        ? "确认强制定稿 " + displayDate(tradeDate) + " 的经济净值？"
        : "确认定稿 " + displayDate(tradeDate) + " 的经济净值？");
    if (!window.confirm(confirmationText)) {
      return;
    }

    state.performanceReviewBusy = true;
    renderNAVReconciliation(item, (state.performanceEconomicNAV && state.performanceEconomicNAV.nav) || {});
    try {
      const accountID = encodeURIComponent(state.activeAccount);
      const data = await request(
        "/v1/accounts/" + accountID + "/performance/nav-reconciliations/" + action,
        {
          method: "POST",
          body: {
            trade_date: tradeDate,
            reconciliation_id: item.reconciliation_id,
            operator,
            note,
            force
          }
        }
      );
      const result = data.nav_reconciliation_review || {};
      state.performanceNAVReconciliation = result.reconciliation || item;
      state.performanceEconomicNAV = Object.assign({}, state.performanceEconomicNAV || {}, {
        nav: result.nav || ((state.performanceEconomicNAV && state.performanceEconomicNAV.nav) || {}),
        reconciliation: result.reconciliation || item,
        persisted: true
      });
      els.navReviewNote.value = "";
      els.navReviewForce.checked = false;
      pushLog("info", action === "confirm" ? "NAV 对账已确认" : "NAV 对账已阻断", tradeDate + " · " + operator);
      showToast(action === "confirm" ? "NAV 对账已确认并定稿" : "NAV 对账已阻断");
    } catch (err) {
      pushLog("error", "NAV 对账复核失败", err.message);
      throw err;
    } finally {
      state.performanceReviewBusy = false;
      renderPerformance();
    }
  }

  function ensurePerformanceSettingsDefaults() {
    const day = defaultQueryDate();
    for (const input of [
      els.repoTradeDateInput,
      els.feeRuleEffectiveFrom,
      els.cashTradeDateInput,
      els.navBaselineDateInput
    ]) {
      if (input && !input.value) {
        input.value = day;
      }
    }
    if (els.feeRuleName && !els.feeRuleName.value) {
      els.feeRuleName.value = "账户默认费率";
    }
  }

  async function loadPerformanceSettings() {
    ensurePerformanceSettingsDefaults();
    if (!state.activeAccount) {
      renderPerformanceSettings();
      return;
    }
    const accountID = encodeURIComponent(state.activeAccount);
    const repoTradeDate = compactDate(els.repoTradeDateInput.value || defaultQueryDate());
    els.performanceSettingsStatus.textContent = "查询中...";
    els.loadPerformanceSettingsButton.disabled = true;
    try {
      const settings = await request("/v1/performance/settings");
      state.performanceSettings = settings || {};
      setPerformanceWriteInputs();
      const queries = await Promise.allSettled([
        request("/v1/performance/fee-rules?account_id=" + accountID + "&limit=200"),
        request("/v1/accounts/" + accountID + "/cash-ledger?limit=200"),
        request("/v1/accounts/" + accountID + "/performance/baselines"),
        request("/v1/accounts/" + accountID + "/performance/reverse-repo/accruals?trade_date=" + encodeURIComponent(repoTradeDate))
      ]);
      const errors = [];
      if (queries[0].status === "fulfilled") {
        state.feeRules = Array.isArray(queries[0].value.rules) ? queries[0].value.rules : [];
      } else {
        state.feeRules = [];
        errors.push("费率 " + queries[0].reason.message);
      }
      if (queries[1].status === "fulfilled") {
        state.cashLedgerEntries = Array.isArray(queries[1].value.entries) ? queries[1].value.entries : [];
      } else {
        state.cashLedgerEntries = [];
        errors.push("流水 " + queries[1].reason.message);
      }
      if (queries[2].status === "fulfilled") {
        state.navBaselines = Array.isArray(queries[2].value.baselines) ? queries[2].value.baselines : [];
      } else {
        state.navBaselines = [];
        errors.push("日初 " + queries[2].reason.message);
      }
      if (queries[3].status === "fulfilled") {
        state.reverseRepo = {
          trade_date: displayDate(repoTradeDate),
          accruals: Array.isArray(queries[3].value.accruals) ? queries[3].value.accruals : []
        };
      } else {
        state.reverseRepo = { trade_date: displayDate(repoTradeDate), accruals: [] };
        errors.push("逆回购 " + queries[3].reason.message);
      }
      state.performanceSettingsLoaded = true;
      state.performanceSettingsError = errors.join("；");
      renderPerformanceSettings();
      if (errors.length === 0) {
        showToast("绩效设置已更新");
      }
    } catch (err) {
      state.performanceSettingsLoaded = false;
      state.performanceSettingsError = err.message;
      pushLog("error", "绩效设置查询失败", err.message);
      showToast("绩效设置查询失败：" + err.message, "error");
      renderPerformanceSettings();
    } finally {
      els.loadPerformanceSettingsButton.disabled = false;
      setPerformanceWriteInputs();
    }
  }

  function setPerformanceWriteInputs() {
    const enabled = Boolean(state.performanceSettings && state.performanceSettings.settings_write_enabled);
    for (const input of document.querySelectorAll("[data-write-input]")) {
      input.disabled = !enabled;
    }
  }

  function renderPerformanceSettings() {
    if (!els.performanceSettingsStatus) {
      return;
    }
    const settings = state.performanceSettings || {};
    const writeEnabled = Boolean(settings.settings_write_enabled);
    const repo = state.reverseRepo || {};
    const accruals = Array.isArray(repo.accruals) ? repo.accruals : [];
    els.performanceSettingsStatus.textContent = state.performanceSettingsError
      ? "部分失败：" + state.performanceSettingsError
      : (state.performanceSettingsLoaded ? "已加载 " + activeAccountLabel() : "等待读取设置");
    els.settingsFormulaVersion.textContent = settings.formula_version || "--";
    els.settingsWriteState.textContent = writeEnabled ? "已开放" : "只读";
    els.settingsWriteState.className = writeEnabled ? "up" : "down";
    els.settingsAutoTolerance.textContent = formatNumber(settings.auto_tolerance_cny, 2) + " / " + formatNumber(settings.auto_tolerance_bp, 2) + "bp";
    els.settingsWarningTolerance.textContent = formatNumber(settings.warning_tolerance_cny, 2) + " / " + formatNumber(settings.warning_tolerance_bp, 2) + "bp";
    els.repoPrincipal.textContent = formatNumber(repo.principal || sumBy(accruals, "principal"));
    els.repoNetInterest.textContent = formatNumber(repo.net_interest || sumBy(accruals, "net_interest"));
    els.repoNetInterest.className = classForNumber(repo.net_interest || sumBy(accruals, "net_interest"));
    setPerformanceWriteInputs();
    renderFeeRules();
    renderCashLedgerEntries();
    renderNavBaselines();
    renderReverseRepoAccruals();
  }

  function renderFeeRules() {
    els.feeRuleStatus.textContent = formatInt(state.feeRules.length) + " 条";
    if (state.feeRules.length === 0) {
      els.feeRulesBody.innerHTML = '<tr><td colspan="5"><div class="empty-state">暂无费率规则</div></td></tr>';
      return;
    }
    els.feeRulesBody.innerHTML = state.feeRules.map((rule) => `
      <tr>
        <td><span class="row-title"><strong>${escapeHTML(rule.name || rule.rule_id || "--")}</strong><span>${escapeHTML(rule.rule_id || "--")}</span></span></td>
        <td><span class="status-badge ${escapeHTML(rule.status || "draft")}">${escapeHTML(rule.status || "--")}</span></td>
        <td>${escapeHTML(rule.business_type || "*")} / ${escapeHTML(rule.trade_side || "*")}</td>
        <td class="num">${formatRate(rule.commission_rate)}<br><span class="muted">${formatRate(rule.repo_fee_rate)}</span></td>
        <td>${escapeHTML(displayDate(rule.effective_from))}<br><span class="muted">${escapeHTML(rule.effective_to ? displayDate(rule.effective_to) : "open")}</span></td>
      </tr>
    `).join("");
  }

  function renderCashLedgerEntries() {
    els.cashLedgerStatus.textContent = formatInt(state.cashLedgerEntries.length) + " 条";
    if (state.cashLedgerEntries.length === 0) {
      els.cashLedgerBody.innerHTML = '<tr><td colspan="6"><div class="empty-state">暂无手工资金流水</div></td></tr>';
      return;
    }
    const writeEnabled = Boolean(state.performanceSettings && state.performanceSettings.settings_write_enabled);
    els.cashLedgerBody.innerHTML = state.cashLedgerEntries.map((entry) => {
      const canConfirm = writeEnabled && entry.status === "draft";
      const canVoid = writeEnabled && entry.status !== "voided";
      const actions = [
        canConfirm ? '<button type="button" class="row-action" data-cash-confirm="' + escapeHTML(entry.entry_id) + '">确认</button>' : "",
        canVoid ? '<button type="button" class="row-action" data-cash-void="' + escapeHTML(entry.entry_id) + '">作废</button>' : ""
      ].filter(Boolean).join(" ");
      return `
        <tr>
          <td><span class="row-title"><strong>${escapeHTML(entry.entry_id || "--")}</strong><span>${escapeHTML(entry.cash_bucket || "--")}</span></span></td>
          <td>${escapeHTML(displayDate(entry.trade_date))}</td>
          <td>${escapeHTML(entry.ledger_type || "--")}<br><span class="muted">${escapeHTML(entry.flow_class || "--")}</span></td>
          <td class="num ${classForNumber(entry.amount)}">${formatSigned(entry.amount)}</td>
          <td><span class="status-badge ${escapeHTML(entry.status || "draft")}">${escapeHTML(entry.status || "--")}</span><br>${actions}</td>
          <td>${escapeHTML(entry.description || "--")}</td>
        </tr>`;
    }).join("");
  }

  function renderNavBaselines() {
    els.navBaselineStatus.textContent = formatInt(state.navBaselines.length) + " 条";
    if (state.navBaselines.length === 0) {
      els.navBaselinesBody.innerHTML = '<tr><td colspan="5"><div class="empty-state">暂无日初经济净值</div></td></tr>';
      return;
    }
    els.navBaselinesBody.innerHTML = state.navBaselines.map((baseline) => `
      <tr>
        <td>${escapeHTML(displayDate(baseline.effective_date))}</td>
        <td class="num">${formatNumber(baseline.initial_economic_nav)}</td>
        <td><span class="status-badge ${escapeHTML(baseline.status || "confirmed")}">${escapeHTML(baseline.status || "--")}</span></td>
        <td>${escapeHTML(baseline.source || "--")}</td>
        <td>${escapeHTML(baseline.description || "--")}</td>
      </tr>
    `).join("");
  }

  function renderReverseRepoAccruals() {
    const repo = state.reverseRepo || {};
    const accruals = Array.isArray(repo.accruals) ? repo.accruals : [];
    els.repoStatus.textContent = [
      repo.trade_date ? displayDate(repo.trade_date) : displayDate(els.repoTradeDateInput.value),
      repo.actual_occupation_days ? "占款 " + repo.actual_occupation_days + " 天" : "",
      repo.persisted ? "已落库" : ""
    ].filter(Boolean).join(" · ") || "--";
    if (accruals.length === 0) {
      els.reverseRepoBody.innerHTML = '<tr><td colspan="8"><div class="empty-state">暂无逆回购估算</div></td></tr>';
      return;
    }
    els.reverseRepoBody.innerHTML = accruals.map((item) => `
      <tr>
        <td><span class="row-title"><strong>${escapeHTML(item.gateway_order_id || "--")}</strong><span>${escapeHTML(item.security_id || "204001.SH")}</span></span></td>
        <td class="num">${formatNumber(item.principal)}</td>
        <td class="num">${formatNumber(item.weighted_rate_pct, 4)}%</td>
        <td>${escapeHTML(item.first_settlement_date || "--")}<br><span class="muted">${escapeHTML(item.maturity_settlement_date || "--")} · ${formatInt(item.actual_occupation_days)}天</span></td>
        <td class="num">${formatNumber(item.gross_interest)}</td>
        <td class="num">${formatNumber(item.effective_fee)}<br><span class="muted">${escapeHTML(item.fee_source || "--")}</span></td>
        <td class="num ${classForNumber(item.net_interest)}">${formatSigned(item.net_interest)}</td>
        <td>${escapeHTML((item.quality_flags || []).join(", ") || "ok")}</td>
      </tr>
    `).join("");
  }

  async function createFeeRule(event) {
    event.preventDefault();
    if (!state.activeAccount) {
      return;
    }
    const body = {
      account_id: state.activeAccount,
      name: valueOf(els.feeRuleName),
      status: valueOf(els.feeRuleStatusInput) || "active",
      market: "*",
      instrument_type: "*",
      business_type: valueOf(els.feeRuleBusinessType) || "*",
      trade_side: valueOf(els.feeRuleTradeSide) || "*",
      commission_rate: numericValue(els.feeRuleCommissionRate),
      minimum_commission: numericValue(els.feeRuleMinimumCommission),
      stamp_duty_rate: numericValue(els.feeRuleStampDutyRate),
      transfer_fee_rate: numericValue(els.feeRuleTransferFeeRate),
      repo_fee_rate: numericValue(els.feeRuleRepoFeeRate),
      estimated_friction_rate: numericValue(els.feeRuleFrictionRate),
      effective_from: compactDate(valueOf(els.feeRuleEffectiveFrom)) || defaultQueryDate(),
      created_by: "web-terminal"
    };
    await request("/v1/performance/fee-rules", { method: "POST", body });
    showToast("费率规则已新增");
    await loadPerformanceSettings();
  }

  async function createCashLedgerEntry(event) {
    event.preventDefault();
    if (!state.activeAccount) {
      return;
    }
    const accountID = encodeURIComponent(state.activeAccount);
    const body = {
      trade_date: compactDate(valueOf(els.cashTradeDateInput)) || defaultQueryDate(),
      ledger_type: valueOf(els.cashLedgerTypeInput) || "adjustment",
      flow_class: valueOf(els.cashFlowClassInput) || "external",
      currency: "CNY",
      amount: numericValue(els.cashAmountInput),
      cash_bucket: valueOf(els.cashBucketInput) || "unknown",
      status: "draft",
      description: valueOf(els.cashDescriptionInput),
      source: "manual",
      created_by: "web-terminal"
    };
    await request("/v1/accounts/" + accountID + "/cash-ledger", { method: "POST", body });
    showToast("资金流水已新增");
    await loadPerformanceSettings();
  }

  async function createNavBaseline(event) {
    event.preventDefault();
    if (!state.activeAccount) {
      return;
    }
    const accountID = encodeURIComponent(state.activeAccount);
    const body = {
      effective_date: compactDate(valueOf(els.navBaselineDateInput)) || defaultQueryDate(),
      initial_economic_nav: numericValue(els.navBaselineValueInput),
      status: "confirmed",
      source: "manual",
      description: valueOf(els.navBaselineDescriptionInput),
      created_by: "web-terminal"
    };
    await request("/v1/accounts/" + accountID + "/performance/baselines", { method: "POST", body });
    showToast("日初经济净值已新增");
    await loadPerformanceSettings();
  }

  async function calculateReverseRepo(persist) {
    if (!state.activeAccount) {
      return;
    }
    const accountID = encodeURIComponent(state.activeAccount);
    const tradeDate = compactDate(valueOf(els.repoTradeDateInput)) || defaultQueryDate();
    const path = "/v1/accounts/" + accountID + "/performance/reverse-repo" + (persist ? "/rebuild" : "") + "?trade_date=" + encodeURIComponent(tradeDate);
    els.repoStatus.textContent = persist ? "落库中..." : "估算中...";
    const data = await request(path, { method: persist ? "POST" : "GET" });
    state.reverseRepo = data.reverse_repo || null;
    renderPerformanceSettings();
    showToast(persist ? "逆回购估算已落库" : "逆回购估算已更新");
  }

  async function transitionCashLedger(entryID, action) {
    if (!state.activeAccount || !entryID) {
      return;
    }
    const accountID = encodeURIComponent(state.activeAccount);
    await request("/v1/accounts/" + accountID + "/cash-ledger/" + encodeURIComponent(entryID) + "/" + action, {
      method: "POST",
      body: { operator: "web-terminal" }
    });
    showToast(action === "confirm" ? "流水已确认" : "流水已作废");
    await loadPerformanceSettings();
  }

  function valueOf(input) {
    return input ? String(input.value || "").trim() : "";
  }

  function numericValue(input) {
    const number = Number(valueOf(input));
    return Number.isFinite(number) ? number : 0;
  }

  function formatRate(value) {
    const number = Number(value);
    if (!Number.isFinite(number)) {
      return "--";
    }
    return formatPercent(number, 4);
  }

  function sumBy(rows, key) {
    return (rows || []).reduce((total, row) => total + (Number(row && row[key]) || 0), 0);
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
      silent: options.silent !== false,
      auto: Boolean(options.auto)
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
    } else if (els.minuteChartStatus && !options.auto) {
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
      itemStyle: { color: isUpBar(row) ? "rgba(242,54,69,0.48)" : "rgba(8,153,129,0.48)" }
    }));
    const markers = chartMarkers(labels, chartRows);
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
      color: ["#f23645", "#089981", "#787b86"],
      textStyle: { color: "#9ca0aa" },
      grid: [
        { left: 58, right: 18, top: 28, height: "58%" },
        { left: 58, right: 18, bottom: 36, height: "18%" }
      ],
      tooltip: {
        trigger: "axis",
        backgroundColor: "rgba(17,21,30,0.96)",
        borderColor: "#363a45",
        borderWidth: 1,
        textStyle: { color: "#d1d4dc", fontSize: 11 },
        axisPointer: {
          type: "cross",
          lineStyle: { color: "#596170", width: 1, opacity: 0.72 },
          crossStyle: { color: "#596170", width: 1, opacity: 0.72 },
          label: {
            backgroundColor: "#2a2e39",
            borderColor: "#4b5160",
            color: "#c5c8d0"
          }
        },
        formatter: (params) => minuteTooltip(params, chartRows)
      },
      legend: {
        top: 0,
        right: 8,
        itemWidth: 12,
        itemHeight: 8,
        textStyle: { color: "#787b86", fontSize: 11 },
        data: ["K线", "成交量", "买点", "卖点"]
      },
      axisPointer: {
        link: [{ xAxisIndex: [0, 1] }],
        lineStyle: { color: "#596170", opacity: 0.72 },
        label: {
          backgroundColor: "#2a2e39",
          borderColor: "#4b5160",
          color: "#c5c8d0"
        }
      },
      xAxis: [
        {
          type: "category",
          data: labels,
          boundaryGap: true,
          axisLabel: { color: "#6f7480", fontSize: 11 },
          axisTick: { lineStyle: { color: "#3a3f4a" } },
          axisLine: { lineStyle: { color: "#3a3f4a" } }
        },
        {
          type: "category",
          gridIndex: 1,
          data: labels,
          boundaryGap: true,
          axisLabel: { show: false },
          axisTick: { show: false },
          axisLine: { lineStyle: { color: "#3a3f4a" } }
        }
      ],
      yAxis: [
        {
          type: "value",
          scale: true,
          axisLabel: { color: "#6f7480", fontSize: 11, formatter: (value) => formatPrice(value, latest) },
          axisLine: { show: false },
          axisTick: { show: false },
          splitLine: { lineStyle: { color: "#2b303b", width: 1, opacity: 0.82 } }
        },
        {
          type: "value",
          gridIndex: 1,
          scale: true,
          axisLabel: { color: "#666b76", fontSize: 10, formatter: formatCompactVolume },
          axisLine: { show: false },
          axisTick: { show: false },
          splitNumber: 2,
          splitLine: { lineStyle: { color: "#282d37", width: 1, opacity: 0.72 } }
        }
      ],
      dataZoom: [
        { type: "inside", xAxisIndex: [0, 1], throttle: 60 },
        {
          type: "slider",
          xAxisIndex: [0, 1],
          height: 18,
          bottom: 8,
          borderColor: "#363a45",
          backgroundColor: "#181c26",
          fillerColor: "rgba(41,98,255,0.12)",
          handleStyle: { color: "#4b5160", borderColor: "#667085" },
          moveHandleStyle: { color: "#4b5160", opacity: 0.8 },
          dataBackground: {
            lineStyle: { color: "#414754", opacity: 0.65 },
            areaStyle: { color: "#242832", opacity: 0.5 }
          },
          selectedDataBackground: {
            lineStyle: { color: "#65718a", opacity: 0.75 },
            areaStyle: { color: "#33415f", opacity: 0.38 }
          },
          textStyle: { color: "#666b76" }
        }
      ],
      series: [
        {
          name: "K线",
          type: "candlestick",
          data: candles,
          itemStyle: {
            color: "#f23645",
            color0: "#089981",
            borderColor: "#f23645",
            borderColor0: "#089981"
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
          itemStyle: { color: "#f23645", borderColor: "#151923", borderWidth: 1 }
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
          itemStyle: { color: "#089981", borderColor: "#151923", borderWidth: 1 }
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

  function chartMarkers(labels, chartRows) {
    const fillOrderIDs = new Set();
    const buy = [];
    const sell = [];
    const rowsByLabel = new Map((chartRows || []).map((row) => [minuteLabel(row.datetime || row.trade_date), row]));
    const appendMarker = (item, label, price, meta) => {
      if (!label) {
        return;
      }
      const snapped = nearestChartLabel(label, labels);
      if (!snapped) {
        return;
      }
      const markerRow = rowsByLabel.get(snapped) || {};
      const markerPrice = markerPlotPrice(item, price, markerRow);
      if (!Number.isFinite(markerPrice) || markerPrice <= 0) {
        return;
      }
      const marker = {
        value: [snapped, markerPrice],
        meta: {
          ...meta,
          price: markerPrice,
          price_note: isCreateRedeemSide(item) ? "K线收盘" : ""
        }
      };
      const kind = sideKind(item);
      if (kind === "sell" || kind === "redeem") {
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
      appendMarker(fill, minuteLabel(fill.matched_at || fill.match_timestamp), Number(fill.price), {
        kind: sideText(fill) + "成交",
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
      appendMarker(order, minuteLabel(order.created_at || order.inserted_at || order.accepted_at || order.last_updated_at), Number(order.limit_price), {
        kind: sideText(order) + "委托",
        id,
        qty: order.order_qty,
        price: order.limit_price,
        status: statusText(order.status)
      });
    }
    return { buy, sell };
  }

  function markerPlotPrice(item, fallbackPrice, row) {
    if (isCreateRedeemSide(item)) {
      return numericOrNull(row && row.close);
    }
    return Number(fallbackPrice);
  }

  function isCreateRedeemSide(item) {
    const side = sideCode(item);
    return side === "P" || side === "R";
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
        "价 " + escapeHTML(formatPrice(meta.price, priceItem)) + (meta.price_note ? " (" + escapeHTML(meta.price_note) + ")" : ""),
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
    renderPerformanceSettings();
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
    const canTrade = selectedOrderAccountCanTrade();
    els.submitOrderButton.disabled = !canTrade;
    els.submitOrderButton.title = canTrade ? "" : "当前账户未开启交易权限";
    if (!canTrade) {
      els.riskAlert.textContent = "只读保护：当前账户未开启交易权限，终端不会发送下单或撤单请求。";
      return;
    }
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
    if (!accountTradingEnabled(accountID)) {
      updateRisk();
      showToast("当前账户为只读，未发送下单请求", "error");
      return;
    }
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
      updateRisk();
    }
  }

  async function cancelOrder(gatewayOrderID) {
    if (!gatewayOrderID) {
      return;
    }
    if (!accountTradingEnabled(state.activeAccount)) {
      showToast("当前账户为只读，未发送撤单请求", "error");
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
    let path = "/v1/accounts/" + encodeURIComponent(state.activeAccount) + "/" + kind + "/refresh";
    if (kind === "orders" || kind === "fills") {
      path += "?trade_date=" + encodeURIComponent(selectedOrdersTradeDateSafe());
    }
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
      resetPage(state.transfersPage);
      const [ordersResult, fillsResult, transfersResult] = await Promise.allSettled([
        fetchOrdersPage(),
        fetchFillsPage(),
        fetchComponentTransfersPage()
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
      if (transfersResult.status === "fulfilled") {
        state.transfersPage.next = transfersResult.value.next_cursor || "";
        state.transfers = transfersResult.value.transfers || [];
      } else {
        pushLog("warn", "ETF 划转读取失败", transfersResult.reason.message);
      }
      await enrichVisibleLedgerInstruments();
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
    const previousState = {
      cursor: page.cursor,
      previous: page.previous.slice(),
      next: page.next,
      page: page.page
    };
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
      page.cursor = previousState.cursor;
      page.previous = previousState.previous;
      page.next = previousState.next;
      page.page = previousState.page;
      if (page === state.positionsPage) {
        renderPositionsPager();
      } else {
        renderBlotterPager();
      }
      pushLog("error", "分页查询失败", err.message);
      showToast("分页查询失败：" + err.message, "error");
    }
  }

  function gotoPositionsPage(direction) {
    if (!clientPositionPagingEnabled()) {
      gotoPage(state.positionsPage, direction, loadPositionsOnly);
      return;
    }
    const totalPages = Math.max(1, Math.ceil(state.allPositions.length / state.positionsPage.pageSize));
    if (direction === "next") {
      state.positionsPage.page = Math.min(totalPages, state.positionsPage.page + 1);
    } else {
      state.positionsPage.page = Math.max(1, state.positionsPage.page - 1);
    }
    renderPositions();
  }

  async function loadOrdersOnly() {
    const data = await fetchOrdersPage();
    state.ordersPage.next = data.next_cursor || "";
    updateOrders(data.orders || []);
    await enrichVisibleLedgerInstruments();
    renderMonitorSummary();
    renderBlotter();
    renderDetail();
  }

  async function loadFillsOnly() {
    const data = await fetchFillsPage();
    state.fillsPage.next = data.next_cursor || "";
    state.fills = data.fills || [];
    await enrichVisibleLedgerInstruments();
    renderMonitorSummary();
    renderBlotter();
    renderDetail();
  }

  async function loadComponentTransfersOnly() {
    const data = await fetchComponentTransfersPage();
    state.transfersPage.next = data.next_cursor || "";
    state.transfers = data.transfers || [];
    await enrichVisibleLedgerInstruments();
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
    try {
      await refreshMetricFillsSource();
    } catch (err) {
      state.metricFillsDirty = true;
      pushLog("warn", "成交费用统计读取失败", err.message);
    }
    await enrichVisibleLedgerInstruments();
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
    window.addEventListener("resize", resizeTerminalCharts);
    document.addEventListener("visibilitychange", () => {
      if (document.hidden) {
        stopChartAutoRefresh();
      } else {
        scheduleChartAutoRefresh(1000);
      }
    });
    document.addEventListener("click", (event) => {
      const target = event.target && event.target.closest ? event.target : event.target && event.target.parentElement;
      const header = target && target.closest ? target.closest("th.sortable[data-sort-table][data-sort-key]") : null;
      if (!header) {
        return;
      }
      setTableSort(header.dataset.sortTable, header.dataset.sortKey);
    });
    els.orderAccount.addEventListener("change", async () => {
      state.activeAccount = els.orderAccount.value;
      state.performanceLoaded = false;
      state.performanceContribution = null;
      state.performanceSettingsLoaded = false;
      state.selectedOrderID = "";
      resetLedgerPages();
      resetPositionStats();
      connectEventStream();
      await refreshNow();
      if (state.activeView === "trade") {
        await loadTradeChartBars({ silent: true });
        scheduleChartAutoRefresh();
      }
      if (state.activeView === "performance" || state.activeView === "snapshots") {
        await loadPerformance();
      }
      if (state.activeView === "performance-settings") {
        await loadPerformanceSettings();
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
    els.exportAssetButton.addEventListener("click", () => exportAssetPositionsCSV());
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
    els.positionsPrevPage.addEventListener("click", () => gotoPositionsPage("prev"));
    els.positionsNextPage.addEventListener("click", () => gotoPositionsPage("next"));
    els.ordersPrevPage.addEventListener("click", () => {
      const page = state.selectedTab === "fills"
        ? state.fillsPage
        : state.selectedTab === "transfers" ? state.transfersPage : state.ordersPage;
      const loader = state.selectedTab === "fills"
        ? loadFillsOnly
        : state.selectedTab === "transfers" ? loadComponentTransfersOnly : loadOrdersOnly;
      gotoPage(page, "prev", loader);
    });
    els.ordersNextPage.addEventListener("click", () => {
      const page = state.selectedTab === "fills"
        ? state.fillsPage
        : state.selectedTab === "transfers" ? state.transfersPage : state.ordersPage;
      const loader = state.selectedTab === "fills"
        ? loadFillsOnly
        : state.selectedTab === "transfers" ? loadComponentTransfersOnly : loadOrdersOnly;
      gotoPage(page, "next", loader);
    });
    els.loadPerformanceButton.addEventListener("click", () => loadPerformance().catch((err) => showToast(err.message, "error")));
    els.downloadPerformanceButton.addEventListener("click", downloadPerformanceCSV);
    for (const button of els.performanceTableViewButtons) {
      button.addEventListener("click", () => setPerformanceTableView(button.dataset.performanceTableView));
    }
    els.loadBarsButton.addEventListener("click", () => loadBars().catch((err) => showToast(err.message, "error")));
    els.confirmNAVReconciliationButton.addEventListener("click", () => reviewNAVReconciliation("confirm").catch((err) => showToast(err.message, "error")));
    els.blockNAVReconciliationButton.addEventListener("click", () => reviewNAVReconciliation("block").catch((err) => showToast(err.message, "error")));
    els.loadPerformanceSettingsButton.addEventListener("click", () => loadPerformanceSettings().catch((err) => showToast(err.message, "error")));
    els.previewRepoButton.addEventListener("click", () => calculateReverseRepo(false).catch((err) => showToast(err.message, "error")));
    els.persistRepoButton.addEventListener("click", () => calculateReverseRepo(true).catch((err) => showToast(err.message, "error")));
    els.feeRuleForm.addEventListener("submit", (event) => createFeeRule(event).catch((err) => showToast(err.message, "error")));
    els.cashLedgerForm.addEventListener("submit", (event) => createCashLedgerEntry(event).catch((err) => showToast(err.message, "error")));
    els.navBaselineForm.addEventListener("submit", (event) => createNavBaseline(event).catch((err) => showToast(err.message, "error")));
    els.cashLedgerBody.addEventListener("click", (event) => {
      const confirmButton = event.target.closest("button[data-cash-confirm]");
      if (confirmButton) {
        transitionCashLedger(confirmButton.dataset.cashConfirm, "confirm").catch((err) => showToast(err.message, "error"));
        return;
      }
      const voidButton = event.target.closest("button[data-cash-void]");
      if (voidButton) {
        transitionCashLedger(voidButton.dataset.cashVoid, "void").catch((err) => showToast(err.message, "error"));
      }
    });
    els.reloadChartButton.addEventListener("click", () => {
      loadTradeChartBars({ silent: false })
        .catch((err) => showToast(err.message, "error"))
        .finally(() => scheduleChartAutoRefresh());
    });
    els.chartTradeDateInput.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        loadTradeChartBars({ silent: false })
          .catch((err) => showToast(err.message, "error"))
          .finally(() => scheduleChartAutoRefresh());
      }
    });
    els.chartTradeDateInput.addEventListener("blur", () => {
      const tradeDate = compactDate(els.chartTradeDateInput.value);
      if (tradeDate) {
        els.chartTradeDateInput.value = tradeDate;
      }
      scheduleChartAutoRefresh();
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
      const button = event.target.closest("button[data-sell-security-id]");
      if (button) {
        focusTradeSymbol(button.dataset.sellSecurityId || "", { side: "S" });
        return;
      }
      const row = event.target.closest("tr[data-position-security-id]");
      if (row) {
        focusTradeSymbol(row.dataset.positionSecurityId || "");
      }
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
      if (state.activeView === "trade") {
        await loadQuoteForInput();
      }
      ensurePerformanceDefaults();
      await loadAccountData();
      state.initialized = true;
      if (state.activeView === "performance" || state.activeView === "snapshots") {
        await loadPerformance();
      } else if (state.activeView === "performance-settings") {
        await loadPerformanceSettings();
      } else if (state.activeView === "trade") {
        await loadTradeChartBars({ silent: true });
        scheduleChartAutoRefresh();
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
