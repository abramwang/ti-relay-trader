const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync(path.join(__dirname, "../../cmd/relay-docs/web/static/trade-terminal.js"), "utf8");
function context(names, overrides = {}) {
  const ctx = vm.createContext({ URLSearchParams, Map, Set, ...overrides });
  for (const name of names) {
    const match = new RegExp(`^  (?:async )?function ${name}\\(`, "m").exec(source);
    assert.ok(match, name);
    const start = match.index;
    const rest = source.slice(start + match[0].length);
    const end = /^  (?:async )?function /m.exec(rest);
    vm.runInContext(source.slice(start, start + match[0].length + end.index), ctx);
  }
  return ctx;
}

function previewContext(request) {
  const picker = {value:"300052141"};
  const els = {
    futuresOrderPreviewButton:{disabled:false}, futuresOrderPreviewResult:{textContent:""},
    futuresOffset:{value:"O"}, qtyInput:{value:"1"}, priceInput:{value:"3000.000000001"},
  };
  const state = {side:"B", marketSnapshot:{security_id:"RB2701.SHFE",trading_day:20261119}};
  let expiry;
  const ctx = context(["futuresPreviewIntent", "syncFuturesOrderPreview", "previewFuturesOrder"], {
    state, els, byID:()=>picker, currentSecurityID:()=>"RB2701.SHFE", futuresMarketSelected:()=>true,
    isFuturesSecurityID:()=>true, request,
    window:{clearTimeout:()=>{}, setTimeout:fn=>{expiry=fn;return 1;}},
  });
  return {ctx,els,state,picker,expire:()=>expiry()};
}
function previewResponse(intent) {
  return {mode:"order_preview_only",intent,can_submit:false,command_published:false,
    account_permission_enabled:false,broker_accepting_submit:true,price_valid:true,
    blockers:["publication_not_connected","relay_account_read_only"],
    quote:{preparation:{valid_until:new Date(Date.now()+4000).toISOString(),market:{price_tick:"0.000000001"}}}};
}
test("order preview preserves decimal price, never submits, and expires visibly", async () => {
  const calls=[];
  const p=previewContext(async (path, options)=>{calls.push([path,options]);return previewResponse(options.body);});
  await p.ctx.previewFuturesOrder();
  assert.equal(calls.length,1);
  assert.equal(p.state.priceEdited,true);
  assert.equal(calls[0][0],"/v1/futures/orders/preview");
  assert.equal(calls[0][1].body.price,"3000.000000001");
  assert.equal(calls[0][1].body.trade_date,"20261119");
  assert.match(p.els.futuresOrderPreviewResult.textContent,/Relay权限：未开放/);
  assert.match(p.els.futuresOrderPreviewResult.textContent,/未发送订单/);
  p.expire();
  assert.match(p.els.futuresOrderPreviewResult.textContent,/已过期/);
});
test("editing an in-flight preview discards its late response", async () => {
  let finish, count=0;
  const p=previewContext((path, options)=>{count++;return new Promise(resolve=>{finish=()=>resolve(previewResponse(options.body));});});
  const pending=p.ctx.previewFuturesOrder();
  await p.ctx.previewFuturesOrder();
  assert.equal(count,1);
  p.picker.value="other-account";
  p.ctx.syncFuturesOrderPreview();
  finish(); await pending;
  assert.equal(p.els.futuresOrderPreviewResult.textContent,"委托已变更 · 待核对");
});
test("missing TradingDay never falls back to wall date and failures do not retry", async () => {
  let count=0;
  const p=previewContext(async ()=>{count++;throw new Error("market unavailable");});
  p.state.marketSnapshot=null;
  await p.ctx.previewFuturesOrder();
  assert.equal(count,0);
  p.state.marketSnapshot={security_id:"RB2701.SHFE",trading_day:20261119};
  await p.ctx.previewFuturesOrder();
  assert.equal(count,1);
  assert.match(p.els.futuresOrderPreviewResult.textContent,/核对失败.*未发送订单/);
});
test("unexpected permission or expired preview cannot look successful", async () => {
  for(const invalid of ["permit","expired"]){
    const p=previewContext(async (_,options)=>{
      const response=previewResponse(options.body);
      if(invalid==="permit")response.can_submit=true;
      else response.quote.preparation.valid_until=new Date(Date.now()-1000).toISOString();
      return response;
    });
    await p.ctx.previewFuturesOrder();
    assert.match(p.els.futuresOrderPreviewResult.textContent,/核对失败/);
  }
});
test("SHFE and CFFEX security identities do not rewrite source instrument ids", () => {
  const ctx = context(["isFuturesSecurityID"]);
  for (const id of ["AU2612.SHFE", "rb2701.shfe", "IF2612.CFFEX", "T2612.CFFEX"]) assert.ok(ctx.isFuturesSecurityID(id));
  for (const id of ["600000.SH", "510300.SH", "AU.SHFE", "SC2612.INE", "IF2612.SH"]) assert.equal(ctx.isFuturesSecurityID(id), false);
});
test("price precision follows Meridian tick, including one-letter products", () => {
  const ctx = context(["isFuturesSecurityID", "priceDigitsForItem", "priceTickForItem"], {
    itemSecurityID: (item) => item.security_id,
    priceMetadataCandidates: (item) => [item],
  });
  for (const [tick, digits] of [[0.02, 2], [0.2, 1], [5, 0], [0.005, 3]]) {
    assert.equal(ctx.priceDigitsForItem({ security_id: "AU2612.SHFE", price_tick: tick }), digits);
  }
  assert.equal(ctx.priceDigitsForItem({ security_id: "510300.SH", price_decimals: 3 }), 3);
  assert.equal(ctx.priceDigitsForItem({ security_id: "AU2612.SHFE" }), 8);
});
test("SSE batches select newest observation and cannot regress after HTTP races", () => {
  const ctx = context(["latestFuturesSnapshot"]);
  const old = {security_id: "AU2612.SHFE", received_at: "2026-11-16T21:01:00+08:00", last: 800};
  const newest = {...old, received_at: "2026-11-16T21:03:00+08:00", last: 801};
  const other = {...newest, security_id: "IF2612.CFFEX"};
  assert.equal(ctx.latestFuturesSnapshot([old, newest, other], old.security_id), newest);
  assert.equal(ctx.latestFuturesSnapshot([old], old.security_id, newest), newest);
  assert.equal(ctx.latestFuturesSnapshot([], old.security_id, newest), newest);
  assert.equal(ctx.latestFuturesSnapshot([other], old.security_id), null);
});
test("catalog uses authoritative instruments and follows pagination", async () => {
  const calls = [];
  const ctx = context(["loadFuturesInstruments", "isFuturesSecurityID"], {
    state: { instrumentCache: new Map() }, currentBusinessDate: () => "20261116",
    splitSecurityID: (id) => { const [symbol, exchange] = id.split("."); return { symbol, exchange }; },
    futuresDisplayName: (item) => item.instrument_name, registerInstrument: () => {},
    request: async (url) => {
      calls.push(url);
      return { data: [{ security_id: calls.length === 1 ? "AU2612.SHFE" : "IF2612.CFFEX", instrument_name: "官方名称", price_tick: 0.2 }], meta: { next_cursor: calls.length === 1 ? "p2" : "" } };
    },
  });
  const rows = await ctx.loadFuturesInstruments();
  assert.equal(rows.length, 2);
  assert.equal(rows[0].name, "官方名称");
  assert.ok(calls.every((url) => url.startsWith("/v1/meridian/futures/instruments?")));
  assert.match(calls[0], /trading_statuses=active%2Cactive_observed/);
  assert.match(calls[1], /cursor=p2/);
  await ctx.loadFuturesInstruments();
  assert.equal(calls.length, 2);
});
test("catalog duplicate cursor fails rather than silently truncating", async () => {
  const ctx = context(["loadFuturesInstruments"], {
    state: { instrumentCache: new Map() }, currentBusinessDate: () => "20261116",
    request: async () => ({ data: [], meta: { next_cursor: "same" } }),
  });
  await assert.rejects(ctx.loadFuturesInstruments("SHFE"), /游标重复/);
  assert.equal(ctx.state.instrumentCache.size, 0);
});
test("night session date updates chart only, respects manual dates", () => {
  const state = { futuresChartManualDate: false, defaultTradeDate: "20261116" };
  const els = { chartTradeDateInput: { value: "20261116" }, ordersTradeDate: { value: "20261116" } };
  const ctx = context(["adoptFuturesChartDate"], {
    state, els, compactDate: String, futuresMarketSelected: () => true, scheduleTradeChartLoad: () => {},
  });
  ctx.adoptFuturesChartDate(20261117);
  assert.equal(els.chartTradeDateInput.value, "20261117");
  assert.equal(els.ordersTradeDate.value, "20261116");
  assert.equal(state.defaultTradeDate, "20261116");
  state.futuresChartManualDate = true;
  ctx.adoptFuturesChartDate(20261118);
  assert.equal(els.chartTradeDateInput.value, "20261117");
});
test("night chart labels keep natural date and ordering", () => {
  const ctx = context(["isFuturesSecurityID", "barChartLabel", "shortDateTime", "compareBarRows", "barRowTimeValue"], { state: {} });
  const night = { security_id: "AU2612.SHFE", datetime: "2026-11-16T21:01:00+08:00" };
  const day = { security_id: "AU2612.SHFE", datetime: "2026-11-17T09:01:00+08:00" };
  assert.equal(ctx.barChartLabel(night), "11-16 21:01");
  assert.equal(ctx.barChartLabel(day), "11-17 09:01");
  assert.ok(ctx.compareBarRows(night, day) < 0);
});
test("manual form submission cannot send a futures order", async () => {
  let writes = 0;
  const ctx = context(["submitOrder"], {
    els: { orderAccount: { value: "00030484" } }, state: {},
    futuresMarketSelected: () => true, updateRisk: () => {}, showToast: () => {},
    request: async () => { writes++; },
  });
  await ctx.submitOrder({ preventDefault() {} });
  assert.equal(writes, 0);
});
test("upstream market failure remains actionable", () => {
  const ctx = context(["marketErrorMessage"]);
  assert.equal(ctx.marketErrorMessage({ message: "proxy failed", payload: { error: { detail: { error: { code: "archive_incomplete", message: "归档未通过门禁" } } } } }), "archive_incomplete · 归档未通过门禁");
});
test("futures chart never queries stock account markers", async () => {
  const state = { activeAccount: "00030484", chartOrders: [1], chartFills: [1] };
  const ctx = context(["loadChartMarkers", "isFuturesSecurityID"], { state });
  await ctx.loadChartMarkers("AU2612.SHFE", "20261117");
  assert.equal(state.chartOrders.length, 0);
  assert.equal(state.chartFills.length, 0);
});

function barsContext(overrides = {}) {
  return context(["loadBarsFor", "isFuturesSecurityID", "marketErrorMessage"], {
    state: { barsSeq: 0, marketSnapshot: {trading_day: "20261117"}, marketMeta: {process_running: true, stale_data: true} },
    els: { futuresBarsScope: {value: "auto"}, minuteChartStatus: {} },
    normalizeSecurityID: String, compactDate: String,
    currentSecurityID: () => "AU2612.SHFE", defaultQueryDate: () => "20261116",
    isCurrentBusinessDate: (value) => value === "20261116",
    effectiveBarsTradeDate: (value) => value, syncBarsInputs: () => {},
    renderBars: () => {}, pushLog: () => {}, showToast: () => {},
    ...overrides,
  });
}
test("night bar query uses TradingDay, not wall date or equity hours, and follows pages", async () => {
  const calls = [];
  const ctx = barsContext({request: async (url) => {
    calls.push(url);
    return {data: [{security_id: "AU2612.SHFE"}], meta: {next_cursor: calls.length === 1 ? "p2" : ""}};
  }});
  await ctx.loadBarsFor({source: "trade", tradeDate: "20261117", startTime: "09:30:00", endTime: "15:00:00"});
  assert.equal(ctx.state.barsRows.length, 2);
  assert.match(calls[0], /data_scope=realtime/);
  assert.match(calls[0], /trade_date=20261117/);
  assert.doesNotMatch(calls[0], /start_time|end_time/);
  assert.match(calls[1], /cursor=p2/);
});
test("explicit archive choice is retained and errors clear old candles", async () => {
  const calls = [];
  const ctx = barsContext({
    els: {futuresBarsScope: {value: "historical"}, minuteChartStatus: {}},
    request: async (url) => { calls.push(url); throw new Error("archive_incomplete"); },
  });
  ctx.state.barsRows = [{close: 123}];
  await ctx.loadBarsFor({source: "trade", tradeDate: "20261117"});
  assert.match(calls[0], /data_scope=historical/);
  assert.equal(ctx.state.barsRows.length, 0);
  assert.equal(ctx.state.barsError, "archive_incomplete");
});
test("old contract HTTP result cannot overwrite new contract chart", async () => {
  let resolve;
  const ctx = barsContext({request: () => new Promise((r) => {resolve = r;})});
  const pending = ctx.loadBarsFor({source: "trade", tradeDate: "20261117"});
  ctx.state.barsSeq++;
  ctx.state.barsRows = [{security_id: "IF2612.CFFEX", close: 4000}];
  resolve({data: [{security_id: "AU2612.SHFE", close: 800}], meta: {}});
  await pending;
  assert.equal(ctx.state.barsRows[0].security_id, "IF2612.CFFEX");
});
