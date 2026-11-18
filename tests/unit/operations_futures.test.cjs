const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync(path.join(__dirname, "../../cmd/relay-docs/web/static/operations-status.js"), "utf8");
function context(snapshot) {
  const ctx = vm.createContext({ state: { snapshot, accountID: "" }, elements: { gatewayCount: {}, gatewayBody: {} } });
  for (const name of ["escapeHTML", "formatNumber", "formatTime", "allGateways", "futuresStatusDetails", "futuresIssue", "filteredGateways", "renderGateways"]) {
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
