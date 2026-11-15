const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const source = fs.readFileSync(path.join(__dirname, "../../cmd/relay-docs/web/static/trade-terminal.js"), "utf8");
function functionSource(name) {
  const start = source.indexOf(`  function ${name}(`);
  assert.notEqual(start, -1);
  const end = source.indexOf("\n  function ", start + 1);
  assert.notEqual(end, -1);
  return source.slice(start, end);
}
function checks(state) {
  const context = {
    state,
    numericOrNull: (v) => v === undefined || v === null || !Number.isFinite(Number(v)) ? null : Number(v),
    compactDate: (v) => String(v || "").replaceAll("-", ""),
    compactDateLoose: (v) => String(v || "").replaceAll("-", ""),
    displayDate: (v) => `${v.slice(0, 4)}-${v.slice(4, 6)}-${v.slice(6, 8)}`,
    formatInt: (v) => String(Number(v) || 0),
    formatSigned: (v) => Number(v).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 }),
    performanceAccountNotApplicable: () => false,
    performanceValuationSourceLabel: (v) => v || "--",
    navReconciliationStatusInfo: () => ({ label: "confirmed" }),
  };
  vm.createContext(context);
  for (const name of ["performanceQualityFlagLabel", "performancePublicationCheck", "performanceQualityChecks"]) {
    vm.runInContext(functionSource(name), context);
  }
  return vm.runInContext("performanceQualityChecks()", context);
}
function base() {
  return {
    performanceSeries: [{ trade_date: "2026-10-19", performance_status: "provisional", quality_flags: [] }],
    performanceDaily: { trade_date: "2026-10-19", quality_flags: [] },
    performanceEconomicNAV: {
      persisted: true,
      nav: { status: "provisional", open_economic_nav: 100, close_economic_nav: 101, quality_flags: [] },
    },
  };
}
const missing = (date) => ({ trade_date: date, performance_status: "blocked", quality_flags: ["official_performance_nav_unavailable"] });
const card = (rows, label) => rows.find((row) => row.label === label);

test("authorized provisional reconstruction keeps unknown facts visible", () => {
  const state = base();
  state.performanceDaily.formula_version = "broker_reconstructed_provisional_nav.v1";
  state.performanceEconomicNAV.nav.pnl_components = { continuity_verified: true, authorization: { approved: true, scope: "provisional_only" }, reconstruction: { known_receivable: 825442.19, known_payable: 869.85, unclassified_broker_component: 7423.35 } };
  const rows = checks(state);
  assert.equal(rows[0].status, "passed");
  assert.equal(card(rows, "资产快照与资金桥").status, "warning");
  assert.match(card(rows, "资产快照与资金桥").detail, /尚未核实/);
  assert.match(card(rows, "ETF待结权益与实际收付").detail, /825,442.19/);
  state.performanceEconomicNAV.nav.pnl_components.authorization.approved = false;
  assert.equal(card(checks(state), "资产快照与资金桥").status, "blocked");
});

test("missing publication counts unique dates without inventing missing snapshots", () => {
  const state = base();
  state.performanceSeries.push(missing("2026-10-20"), missing("2026-10-21"), missing("20261020"));
  const rows = checks(state);
  assert.equal(rows[0].status, "blocked");
  assert.match(rows[0].detail, /2 个交易日尚未发布.*2026-10-20 至 2026-10-21/);
  assert.match(rows[0].detail, /不等同于/);
  assert.equal(card(rows, "当日资产与资金桥").status, "passed");
});

for (const formula of ["broker_reconstructed_economic_nav.v1", "broker_cash_repo_carveout.v1"]) {
  test(`historical branch still reports range publication gaps: ${formula}`, () => {
    const state = base();
    state.performanceDaily.formula_version = formula;
    state.performanceEconomicNAV.nav.pnl_components = { account_ledger_verified: true, ledger_matching: { export_cutoff: "2026-10-19" } };
    state.performanceSeries.push(missing("2026-10-20"));
    assert.equal(checks(state)[0].status, "blocked");
    assert.match(checks(state)[0].detail, /1 个交易日尚未发布/);
  });
}

test("real residual uses unattributed.pnl, not reconciliation.residual", () => {
  const state = base();
  state.performanceEconomicNAV = { persisted: false, asset_basis: { applied: false }, reconciliation: { residual: 0 },
    nav: { status: "blocked", open_economic_nav: 100, close_economic_nav: 101,
      quality_flags: ["nav_contribution_residual_exceeds_warning"], pnl_components: { unattributed: { pnl: 1270276.025 } } } };
  const rows = checks(state);
  assert.match(rows[0].detail, /阻断试算，尚未发布/);
  const bridge = card(rows, "当日资产与资金桥");
  assert.equal(bridge.status, "blocked");
  assert.match(bridge.detail, /净值与贡献残差超限.*1,270,276/);
  assert.match(bridge.detail, /券商经济资产基准未应用/);
  state.performanceEconomicNAV.reconciliation.status = "blocked";
  assert.match(card(checks(state), "经济净值与 T+1 对账")?.detail || "", /不代表净值归因已闭合/);
});

test("actual snapshot failure remains distinct from unpublished NAV", () => {
  const state = base();
  state.performanceDaily.quality_flags = ["open_asset_observation_unavailable"];
  assert.match(card(checks(state), "当日资产与资金桥").detail, /日初资产观测不可用/);
});

test("no current detail is not reported as a healthy cash bridge", () => {
  const state = base();
  state.performanceEconomicNAV = null;
  const rows = checks(state);
  assert.equal(rows[0].status, "warning");
  assert.equal(card(rows, "当日资产与资金桥").status, "warning");
});

test("range blocked status without missing-publication evidence is not relabelled", () => {
  const state = base();
  state.performanceSeries.push({ trade_date: "2026-10-20", performance_status: "blocked" });
  assert.match(checks(state)[0].detail, /净值阻断/);
  assert.doesNotMatch(checks(state)[0].detail, /尚未发布/);
});

test("published provisional and read-only provisional remain distinct", () => {
  const state = base();
  assert.match(checks(state)[0].detail, /暂定，非最终清算/);
  state.performanceEconomicNAV.persisted = false;
  assert.equal(checks(state)[0].status, "warning");
  assert.match(checks(state)[0].detail, /只读试算/);
});

test("unknown blocked calculation preserves the unknown cause", () => {
  const state = base();
  state.performanceEconomicNAV.nav.status = "blocked";
  assert.match(card(checks(state), "当日资产与资金桥").detail, /接口未提供具体原因/);
});

test("a historical finalized day is not relabelled as a broken bridge by another date", () => {
  const state = base();
  state.performanceDaily.formula_version = "broker_cash_repo_carveout.v1";
  state.performanceEconomicNAV.nav.status = "finalized";
  state.performanceSeries.push(missing("2026-10-20"));
  const rows = checks(state);
  assert.equal(rows[0].status, "blocked");
  assert.equal(card(rows, "历史资产与资本桥").status, "passed");
});
