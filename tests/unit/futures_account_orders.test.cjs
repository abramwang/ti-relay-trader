const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const test = require("node:test");
const source = fs.readFileSync("cmd/relay-docs/web/static/futures-account.js", "utf8");
const start = source.indexOf("  function orderObservation(");
const end = source.indexOf("  function render()", start);
function context(action) {
  const c = vm.createContext({action, Map});
  vm.runInContext(source.slice(start,end),c);
  return c;
}
test("local events display despite failed full query, without rewriting its evidence",()=>{
  const c=context("order.list.query");
  const q={state:"failed",reviewed:false,message:"order_association_pending"};
  const a={local_orders:[{gateway_order_id:"local",gateway_status:"cancelled"}]};
  const result=c.orderObservation(a,q);
  assert.equal(result.orders[0].gateway_status,"cancelled");
  assert.equal(result.local_event_view,true);
  assert.equal(result.reviewed,false);
  assert.equal(result.state,"failed");
  assert.equal(q.state,"failed");
  assert.equal(q.reviewed,false);
});

test("persisted response orders merge with local observations without a manual review claim",()=>{
  const q={state:"response_ready",response_validated:true,response_persisted:true,reviewed:false,orders:[{gateway_order_id:"external"}]};
  const result=context("order.list.query").orderObservation({local_orders:[{gateway_order_id:"local"}]},q);
  assert.equal(result.orders.length,2);
  assert.equal(result.reviewed,false);
  assert.equal(result.state,"response_ready");
});

test("all complete persisted query responses display, but partial or invalid ones do not",()=>{
  const c=vm.createContext({action:"account.asset.query"});
  vm.runInContext(source.slice(source.indexOf("  const text ="),source.indexOf("  function orderObservation(")),c);
  for(const action of ["account.asset.query","account.positions.query","order.list.query","fill.list.query"]) {
    c.action=action;
    c.q={state:"response_ready",response_validated:true,response_persisted:true,reviewed:false};
    assert.equal(vm.runInContext("displayable(q)",c),true,action);
    for(const state of ["failed","collecting","invalid","awaiting_reply"]) {
      c.q.state=state;assert.equal(vm.runInContext("displayable(q)",c),false);
    }
  }
});
test("same order event replaces queried state without duplicate rows",()=>{
  const c=context("order.list.query");
  const q={state:"ready",reviewed:true,orders:[{gateway_order_id:"local",gateway_status:"working"}]};
  const result=c.orderObservation({local_orders:[{gateway_order_id:"local",gateway_status:"cancelled"}]},q);
  assert.equal(result.orders.length,1);
  assert.equal(result.orders[0].gateway_status,"cancelled");
  assert.equal(q.orders[0].gateway_status,"working");
});
test("order observations do not unlock failed assets or positions",()=>{
  const q={state:"failed",reviewed:false};
  for(const action of ["account.asset.query","account.positions.query","fill.list.query"]){
    assert.equal(context(action).orderObservation({local_orders:[{}]},q),q);
  }
});

test("complete correlated position response displays without manual audit",()=>{
  const c=vm.createContext({action:"account.positions.query"});
  vm.runInContext(source.slice(source.indexOf("  const text ="),source.indexOf("  function orderObservation(")),c);
  const q={state:"response_ready",response_validated:true,reviewed:false,positions:[{symbol:"rb2701",exchange:"SHFE",futures:{position:1,posi_direction:"2"},captured_at:"2026-11-19T10:30:00+08:00"}]};
  assert.equal(c.tableData(q).rows.length,1);
  assert.equal(q.reviewed,false);
  for(const state of ["failed","collecting","pending_review","invalid"]){
    assert.equal(c.tableData({...q,state}).rows.length,0);
  }
  assert.equal(c.tableData({...q,response_validated:false}).rows.length,0);
  c.action="account.asset.query";
  assert.equal(c.tableData(q).rows.length,0);
});
