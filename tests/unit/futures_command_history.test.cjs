const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const source = fs.readFileSync("cmd/relay-docs/web/static/futures-command-history.js", "utf8");

class Element {
  constructor() { this.childNodes=[];this.events={};this.dataset={};this.textContent=""; }
  append(...nodes) { this.childNodes.push(...nodes); }
  replaceChildren() { this.childNodes=[]; }
  addEventListener(k,f) { this.events[k]=f; }
  querySelector() { return this.body ||= new Element(); }
}
const account = (id="300052141",gateway="test") => ({account_id:id,route:{env:"test",broker_id:"ctp_4900",gateway_id:gateway,account_id:id},local_orders:[],connection_state:"stopped"});
function item(id,a=account(),state="published") {
  return {command_id:id,account_id:a.account_id,gateway_id:a.route.gateway_id,action:"order.submit",message_id:"message-"+id,idempotency_key:"key-"+id,
    registered_at:"2026-11-19T01:05:00Z",broker_state_known:false,retry_safe:false,
    orders:[{account_id:a.account_id,gateway_order_id:"order-"+id,trade_date:"20261119",symbol:"IF2612",exchange:"CFFEX",trade_side:"B",offset_type:"O",order_qty:1,limit_price:"3900.000000001"}],
    publication:{command_id:id,state,command_published:{published:true,not_published:false,registered:null,attempted:null}[state],broker_accepted:false}};
}
const response = (a,items=[],next) => ({ok:true,status:200,headers:{get:()=>"application/json"},json:async()=>({ok:true,data:{route:a.route,items,...(next?{next_before_id:next}:{})}})});
function fixture(fetcher=async()=>response(account())) {
  const nodes=new Map(),calls=[];
  const el=id=>{if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id);};
  const ctx={document:{getElementById:el,createElement:()=>new Element()},window:{setTimeout,clearTimeout},URLSearchParams,AbortController,
    fetch:async(url,options)=>{calls.push([url,options]);return fetcher(url,options);}};
  vm.runInNewContext(source,ctx);
  return {el,calls,ctx,engine:ctx.window.RelayFuturesCommandHistory,rows:()=>el("futuresCommandTable").querySelector().childNodes};
}
function cellText(f,col) {return f.rows().map(r=>r.childNodes[col].textContent);}

test("fetches only visible TEST history, without execution or fresh heartbeat",async()=>{
  const a=account(), f=fixture(async()=>response(a,[item(4,a,"attempted")]));
  await f.engine.update(a,false); assert.equal(f.calls.length,0);
  await f.engine.update(a,true);
  assert.equal(f.calls.length,1);assert.equal(f.calls[0][1].method,undefined);
  assert.match(f.calls[0][0],/^\/v1\/futures\/commands\?account_id=300052141&limit=20$/);
  assert.deepEqual(cellText(f,6),["发布结果未知"]);
  assert.deepEqual(cellText(f,7),["尚无回执"]);
  assert.deepEqual(cellText(f,8),["未读取到委托回报"]);
  assert.deepEqual(cellText(f,4),["3900.000000001"]);
  assert.match(cellText(f,0)[0],/09:05:00/);
  const row = f.rows()[0];
  await f.engine.update(structuredClone(a),true); assert.equal(f.calls.length,1);
  assert.equal(f.rows()[0],row,"unchanged polling must preserve open details and focus");
  await f.engine.update({...a,route:{...a.route,env:"production"}},true);
  assert.equal(f.calls.length,1);assert.equal(f.el("futuresCommandRefresh").disabled,true);
});
test("keyset next/previous and refresh never auto-fetch all pages",async()=>{
  const a=account(), f=fixture(async url=>{
    const older=new URLSearchParams(url.split("?")[1]).has("before_id");
    return response(a,older?[item(2)]:Array.from({length:20},(_,i)=>item(30-i)),older?undefined:11);
  });
  await f.engine.update(a,true); assert.equal(f.rows().length,20);
  await f.el("futuresCommandNext").events.click();
  assert.match(f.calls[1][0],/before_id=11/);assert.equal(f.rows()[0].dataset.commandId,"2");
  assert.equal(f.el("futuresCommandNext").disabled,true);
  await f.el("futuresCommandPrev").events.click();
  assert.doesNotMatch(f.calls[2][0],/before_id/);
  await f.engine.refresh();assert.equal(f.calls.length,4);
});
test("late response cannot overwrite another account or gateway",async()=>{
  for(const change of [account("300052142"),account("300052141","new")]) {
    let finish;
    const f=fixture(async url=>url.includes("300052141") && !finish?new Promise(resolve=>{finish=resolve;}):response(change,[item(8,change)]));
    const first=f.engine.update(account(),true);
    await f.engine.update(change,true);
    finish(response(account(),[item(9)]));await first;
    assert.equal(f.rows()[0].dataset.commandId,"8");assert.match(f.el("futuresCommandStatus").textContent,new RegExp(change.route.gateway_id));
  }
});
test("leaving a tab during paging cancels rendering and refetches on return",async()=>{
  const a=account();let finish, requests=0;
  const f=fixture(async()=>++requests===2?new Promise(resolve=>{finish=resolve;}):response(a,Array.from({length:20},(_,i)=>item(30-i)),11));
  await f.engine.update(a,true);
  const pending=f.el("futuresCommandNext").events.click();
  await f.engine.update(a,false);
  finish(response(a,[item(2)]));await pending;
  assert.equal(f.el("futuresCommandView").hidden,true);
  // The refreshed second page must still respect its cursor.
  f.ctx.fetch=async()=>{requests++;return response(a,[item(2)]);};
  await f.engine.update(a,true);assert.equal(requests,3);assert.equal(f.rows()[0].dataset.commandId,"2");
});
test("404, non-JSON and corrupt pages are errors, not an empty success or a POST",async()=>{
  for(const mode of ["404","html","scope","state","cursor","duplicate","account","number_price","timestamp","missing_orders"]) {
    const a=account(), x=item(3), data=response(a,[x]);
    if(mode==="404"){data.ok=false;data.status=404;}
    if(mode==="html")data.headers.get=()=>"text/html";
    if(mode==="scope")data.json=async()=>({ok:true,data:{route:account("300052142").route,items:[]}});
    if(mode==="state")x.publication.command_published=false;
    if(mode==="cursor")data.json=async()=>({ok:true,data:{route:a.route,items:[x],next_before_id:4}});
    if(mode==="duplicate")data.json=async()=>({ok:true,data:{route:a.route,items:[x,x]}});
    if(mode==="account")x.account_id="300052142";
    if(mode==="number_price")x.orders[0].limit_price=3900;
    if(mode==="timestamp")x.registered_at="2026-11-19T09:00:00";
    if(mode==="missing_orders")delete x.orders;
    const f=fixture(async()=>data);await f.engine.update(a,true);
    assert.equal(f.el("futuresCommandStatus").dataset.failed,"true",mode);
    assert.equal(f.rows()[0].childNodes[0].textContent,"读取失败，不代表没有委托",mode);
    assert.equal(f.el("futuresCommandNext").disabled,true);
    assert.equal(f.calls.length,1);assert.equal(f.calls[0][1].method,undefined);
  }
});
test("cancel publication is separate from original order status and rejects no identity",async()=>{
  const a=account(), x=item(3);
  x.action="order.cancel";delete x.orders;
  x.cancel={gateway_order_id:"order-2",trade_date:"20261119",cancel_id:"cancel-3"};
  a.local_orders=[{account_id:a.account_id,gateway_order_id:"order-2",trade_date:"20261119",gateway_status:"filled"}];
  const f=fixture(async()=>response(a,[x]));await f.engine.update(a,true);
  assert.deepEqual(cellText(f,6),["已发布至 OC 通道"]);assert.deepEqual(cellText(f,8),["全部成交"]);
  delete x.cancel.cancel_id;await f.engine.refresh();
  assert.equal(f.el("futuresCommandStatus").dataset.failed,"true");
});
test("batch intentions retain child sequence, exact prices and untrusted text",async()=>{
  const a=account(), x=item(5);x.action="order.batch.submit";
  x.orders.push({...x.orders[0],gateway_order_id:"other",symbol:"<script>bad</script>",trade_side:"S",offset_type:"C",limit_price:"3901.10",order_qty:2});
  const f=fixture(async()=>response(a,[x]));await f.engine.update(a,true);
  assert.deepEqual(cellText(f,4),["3900.000000001\n3901.10"]);
  assert.deepEqual(cellText(f,5),["1\n2"]);assert.match(cellText(f,2)[0],/<script>/);
  assert.equal(f.rows()[0].childNodes[2].childNodes.length,0);
});
test("empty response must have scope and reads no browser storage",async()=>{
  const a=account(), f=fixture(async()=>response(a));await f.engine.update(a,true);
  assert.match(f.rows()[0].childNodes[0].textContent,/当前路由暂无已登记/);
  assert.equal(f.el("futuresCommandStatus").dataset.failed,"false");
  assert.equal(f.calls.length,1);assert.doesNotMatch(source,/sessionStorage|localStorage|method\s*:\s*["']POST/);
});

test("persisted receipts never stand in for an order outcome",async()=>{
  const a=account();
  for(const [state,label] of [["accepted","命令已接收"],["failed","命令失败"],["invalid","回执冲突或无效"]]) {
    const x=item(3);
    x.response={command_id:3,action:x.action,state,response_validated:state!=="invalid",native_audit_verified:false,order_state_changed:false,retry_safe:false};
    const f=fixture(async()=>response(a,[x]));await f.engine.update(a,true);
    assert.deepEqual(cellText(f,7),[label]);
    assert.deepEqual(cellText(f,8),["未读取到委托回报"]);
    x.response.command_id=4;await f.engine.refresh();
    assert.equal(f.el("futuresCommandStatus").dataset.failed,"true");
    x.response.command_id=3;x.publication.state="attempted";x.publication.command_published=null;
    await f.engine.refresh();assert.equal(f.el("futuresCommandStatus").dataset.failed,"true");
  }
});
