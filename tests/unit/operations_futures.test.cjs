const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync(path.join(__dirname, "../../cmd/relay-docs/web/static/operations-status.js"), "utf8");
function context(snapshot) {
  const ctx = vm.createContext({ state: { snapshot, accountID: "" }, elements: { gatewayCount: {}, gatewayBody: {} } });
  for (const name of ["escapeHTML", "formatNumber", "formatTime", "renderOverview", "allGateways", "futuresStatusDetails", "futuresIssue", "filteredGateways", "renderGateways"]) {
    const match = new RegExp(`^  function ${name}\\(`, "m").exec(source);
    const rest = source.slice(match.index + match[0].length);
    const end = /^  (?:async )?function /m.exec(rest);
    vm.runInContext(source.slice(match.index, match.index + match[0].length + end.index), ctx);
  }
  ctx.statusBadge = (status) => status;
  return ctx;
}

test("futures shares account table and filter; missing PEL is not zero", () => {
  const ctx = context({gateways: [{account_id: "00030484"}], futures_accounts: [{account_id: "300052141", alias: "CTP", route: {broker_id: "ctp_4900", gateway_id: "test"}, connection_state: "unavailable", connection_error: "epoch_missing_or_changed"}]});
  assert.equal(ctx.allGateways().length, 2);
  ctx.state.accountID = "300052141";
  assert.equal(ctx.filteredGateways().length, 1);
  ctx.renderGateways();
  assert.match(ctx.elements.gatewayBody.innerHTML, /期货 · CTP/);
  assert.match(ctx.elements.gatewayBody.innerHTML, /路由绑定缺失/);
  assert.match(ctx.elements.gatewayBody.innerHTML, /终端报撤：未开放/);
  assert.equal((ctx.elements.gatewayBody.innerHTML.match(/class="mono-cell">--<\/td>/g) || []).length, 3);
});

test("stopped and stale advertised readiness is explicitly historical", () => {
  const ctx = context({});
  for (const state of ["stopped", "stale"]) {
    const account = {connection_state: state, heartbeat: {accepting_trade_commands: true, accepting_cancel_commands: true, broker_ready: true, runtime_mode: "test_broker_assessment"}};
    assert.match(ctx.futuresStatusDetails(account), /历史OC报单/);
    assert.doesNotMatch(ctx.futuresStatusDetails(account), /当前心跳/);
    assert.notEqual(ctx.futuresIssue(account), "--");
  }
});

test("query readiness never grants terminal trading; source text is escaped", () => {
  const ctx = context({});
  const html = ctx.futuresStatusDetails({connection_state: "read_only", heartbeat: {runtime_mode: "test_broker_read_only", broker_ready: true, query_collection_ready: true, counter_session_id: "<img>"}});
  assert.match(html, /查询 就绪/);
  assert.match(html, /终端报撤：未开放/);
  assert.match(html, /&lt;img&gt;/);
  assert.doesNotMatch(html, /<img>/);
});

test("resident execution heartbeat is current but missing history stays visible", () => {
  const ctx = context({});
  const account = {connection_state: "execution", heartbeat: {runtime_mode: "test_broker_assessment", authorization_owner: "relay", broker_ready: true, query_collection_ready: true, runtime_blocked_reason: "historical_state_missing_read_only", trade_date: "20261119", connection_generation: 1, counter_session_id: "current-session"}};
  const html = ctx.futuresStatusDetails(account);
  assert.match(html, /当前心跳 · Relay执行模式/);
  assert.doesNotMatch(html, /历史OC/);
  assert.match(html, /柜台登录 就绪 · 查询 就绪/);
  assert.match(html, /交易日 20261119 · 连接代次 1/);
  assert.match(html, /终端报撤：未开放/);
  assert.match(html, /historical_state_missing_read_only/);
  assert.match(ctx.futuresIssue(account), /历史状态缺失，仅允许只读/);
});

test("resident query-ready account counts as connected, not as trading permission", () => {
  const ctx = context({monitoring_active: true, summary: {gateways_online: 1}, gateways: [{account_id: "stock"}], futures_accounts: [{account_id: "future", connection_state: "execution", trading_enabled: false}]});
  ctx.monitoringLabels = {};
  for (const key of ["monitoringWindow", "monitoringReason", "gatewayOnline", "gatewayAttention", "streamLag", "streamAttention", "pendingDLQ", "updatedAt", "dlqWriteMode"]) ctx.elements[key] = {style: {}};
  ctx.renderOverview();
  assert.equal(ctx.elements.gatewayOnline.textContent, "2 / 2");
  assert.match(ctx.elements.gatewayAttention.textContent, /期货 0 个未就绪/);
  assert.equal(ctx.state.snapshot.futures_accounts[0].trading_enabled, false);
});
