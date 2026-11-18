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
  assert.equal(q.state,"failed");
  assert.equal(q.reviewed,false);
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
