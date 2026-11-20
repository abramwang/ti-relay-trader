const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const { webcrypto } = require("node:crypto");
const source = fs.readFileSync("cmd/relay-docs/web/static/futures-execution.js", "utf8");

class Element {
  constructor() { this.childNodes=[];this.events={};this.textContent=""; }
  append(...nodes) { this.childNodes.push(...nodes); }
  replaceChildren() { this.childNodes=[]; }
  addEventListener(k,f) { this.events[k]=f; }
}
function fixture(overrides={}) {
  const panel=new Element(), calls=[], storage=new Map();
  const window={crypto:webcrypto,confirm:()=>true,dispatchEvent(){},setInterval(){}};
  const ctx={document:{getElementById:()=>panel,createElement:()=>new Element()},window,
    sessionStorage:{getItem:k=>storage.get(k),setItem:(k,v)=>storage.set(k,v)},
    CustomEvent:class {},URLSearchParams,AbortSignal,Uint8Array,
    fetch:async(url,opts)=>{calls.push([url,opts]);return response(url,opts);},...overrides};
  vm.runInNewContext(source,ctx);
  const a={account_id:"300052141",alias:"TEST",route:{env:"test",broker_id:"ctp_4900",gateway_id:"test"},
    heartbeat:{counter_session_id:"new"},local_orders:[],execution:{submit_enabled:true,cancel_enabled:true,
      price_source:"meridian_futures_snapshot.v1",trade_date:"20261119",valid_until:new Date(Date.now()+60000).toISOString()}};
  const intent={account_id:a.account_id,security_id:"RB2701.SHFE",trade_date:"20261119",trade_side:"B",offset_type:"O",hedge_type:"speculation",qty:1,price:"3000.000"};
  const engine=ctx.window.RelayFuturesExecution;
  engine.update([a],a.account_id,false);
  return {engine,a,intent,calls,panel,storage,ctx};
}
function response(url,opts) {
  const body=JSON.parse(opts.body);
  return {ok:true,status:202,headers:{get:()=>"application/json"},json:async()=>({ok:true,data:{...body,
    message_id:"original",gateway_order_id:"relay-web-order-original",cancel_id:"cancel-original",
    broker_state_known:false,retry_safe:false,publication:{command_id:1,state:"published",command_published:true}}})};
}
function record(p) { return JSON.parse([...p.storage.values()][0])[0]; }
function row(p) { return {account_id:p.a.account_id,trade_date:"20261119",counter_session_id:"new",gateway_order_id:"relay-web-order-original",symbol:"rb2701",exchange:"SHFE",gateway_status:"working",leaves_qty:1}; }

test("configured markets select their own permission, quantity and close model",async()=>{
  for (const [exchange,symbol] of [["DCE","L2612F"],["CZCE","TA701"],["INE","SC2701"],["GFEX","SI2701"]]) {
    const p=fixture();
    p.a.execution.markets=[{exchange,max_order_qty:3,close_model:"combined",submit_enabled:true,cancel_enabled:true,valid_until:p.a.execution.valid_until}];
    Object.assign(p.intent,{security_id:symbol+"."+exchange,trade_side:"S",offset_type:"C",qty:3});
    assert.equal(p.engine.sync(p.intent).enabled,true);
    assert.equal(p.engine.sync({...p.intent,offset_type:"CT"}).enabled,false);
    assert.equal(p.engine.sync({...p.intent,qty:4}).enabled,false);
    assert.equal(p.engine.sync({...p.intent,security_id:"RB2701.SHFE"}).enabled,false);
    await p.engine.submit(p.intent);
    assert.equal(p.calls.length,1);assert.equal(record(p).body.order.security_id,symbol+"."+exchange);
  }
});
test("market cancel ignores new-order cap but cannot borrow another market readiness",async()=>{
  const p=fixture();
  const policy={exchange:"DCE",max_order_qty:1,close_model:"none",submit_enabled:false,cancel_enabled:true,valid_until:p.a.execution.valid_until};
  p.a.execution.markets=[policy];
  p.a.local_orders=[{...row(p),exchange:"DCE",symbol:"m2701",leaves_qty:10}];
  assert.equal(p.engine.canCancel(p.a.local_orders[0]),true);
  policy.valid_until=new Date(0).toISOString();
  assert.equal(p.engine.canCancel(p.a.local_orders[0]),false);
  policy.valid_until=p.a.execution.valid_until;
  assert.equal(p.engine.canCancel({...p.a.local_orders[0],exchange:"SHFE"}),false);
  await p.engine.cancel(p.a.local_orders[0]);assert.equal(p.calls.length,1);
});
test("submit preserves intention and blocks another order until broker observation",async()=>{
  const p=fixture(); assert.equal(p.engine.sync(p.intent).enabled,true);
  await p.engine.submit(p.intent);await p.engine.submit(p.intent);
  assert.equal(p.calls.length,1);assert.equal(p.calls[0][0],"/v1/futures/orders");
  const r=record(p);assert.equal(r.body.order.price,"3000.000");assert.equal(r.state,"published");
  assert.equal(p.engine.sync(p.intent).enabled,false);
  p.a.local_orders=[row(p)];p.engine.update([p.a],p.a.account_id,false);
  assert.equal(p.engine.sync(p.intent).enabled,true);
});
test("double click during in-flight POST creates one request",async()=>{
  let finish,count=0;
  const p=fixture({fetch:(u,o)=>{count++;return new Promise(resolve=>{finish=()=>resolve(response(u,o));});}});
  const first=p.engine.submit(p.intent);await p.engine.submit(p.intent);
  assert.equal(count,1);finish();await first;
});
test("unknown publication persists across reload without POST replay",async()=>{
  let writes=0;
  const p=fixture({fetch:async()=>{writes++;throw new Error("timeout");}});
  await p.engine.submit(p.intent);assert.equal(record(p).state,"unknown");
  const q=fixture({sessionStorage:p.ctx.sessionStorage,fetch:async()=>{writes++;throw new Error();}});
  await q.engine.submit(q.intent);assert.equal(writes,1);
  assert.equal(q.engine.sync(q.intent).enabled,false);
});
test("explicit status check only GETs original key; 404 cannot unlock resend",async()=>{
  const p=fixture();await p.engine.submit(p.intent);
  const key=record(p).body.idempotency_key;
  p.ctx.fetch=async(url,opts)=>{p.calls.push([url,opts]);return {ok:false,status:404,headers:{get:()=>"application/json"},json:async()=>({ok:false,error:{message:"not found"}})};};
  await p.panel.childNodes[0].childNodes[1].events.click();
  assert.equal(p.calls.length,2);assert.match(p.calls[1][0],new RegExp("idempotency_key="+key));
  assert.equal(p.calls[1][1].method,undefined);assert.equal(p.engine.sync(p.intent).enabled,false);
});
test("cancel is independent of submit and no market request or second cancel is made",async()=>{
  const p=fixture();p.a.execution.submit_enabled=false;p.a.local_orders=[row(p)];
  assert.equal(p.engine.canCancel(p.a.local_orders[0]),true);
  await p.engine.cancel(p.a.local_orders[0]);await p.engine.cancel(p.a.local_orders[0]);
  assert.equal(p.calls.length,1);assert.equal(p.calls[0][0],"/v1/futures/cancels");
  assert.deepEqual(Object.keys(record(p).body).sort(),["account_id","gateway_order_id","idempotency_key","trade_date"]);
});
test("production, stale, revoked and unsupported intentions never POST",async()=>{
  for(const kind of ["prod","stale","revoked","read_error","day","side","offset","qty","exchange","account"]){
    const p=fixture();
    if(kind==="prod")p.a.route.env="prod";
    if(kind==="stale")p.a.execution.valid_until=new Date(0).toISOString();
    if(kind==="revoked")p.a.execution.submit_enabled=false;
    if(kind==="read_error")p.engine.update([p.a],p.a.account_id,true);
    if(kind==="day")p.intent.trade_date="20261118";
    if(kind==="side")p.intent.trade_side="S";
    if(kind==="offset")p.intent.offset_type="C";
    if(kind==="qty")p.intent.qty=2;
    if(kind==="exchange")p.intent.security_id="IF2701.CFFEX";
    if(kind==="account")p.intent.account_id="other";
    await p.engine.submit(p.intent);assert.equal(p.calls.length,0,kind);
  }
});
test("confirmation cancellation and revocation during confirmation never POST",async()=>{
  const p=fixture();p.ctx.window.confirm=()=>false;await p.engine.submit(p.intent);assert.equal(p.calls.length,0);
  p.ctx.window.confirm=()=>{p.a.execution.submit_enabled=false;return true;};await p.engine.submit(p.intent);assert.equal(p.calls.length,0);
});
test("storage failure before POST blocks new trading",async()=>{
  let writes=0;
  const p=fixture({sessionStorage:{getItem:()=>null,setItem(){throw new Error();}},fetch:async()=>{writes++;}});
  await p.engine.submit(p.intent);assert.equal(writes,0);assert.equal(p.engine.sync(p.intent).enabled,false);
});
test("old scope requests remain stored but are never checked on a new route",async()=>{
  const p=fixture();await p.engine.submit(p.intent);
  const button=p.panel.childNodes[0].childNodes[1];p.a.route.gateway_id="changed";p.engine.update([p.a],p.a.account_id,false);
  await button.events.click();assert.equal(p.calls.length,1);assert.equal(JSON.parse([...p.storage.values()][0]).length,1);
});
test("contradictory publication response is unknown and cannot unlock another POST",async()=>{
  const p=fixture({fetch:async(u,o)=>{
    const reply=response(u,o), body=await reply.json();body.data.publication.command_published=false;
    reply.json=async()=>body;return reply;
  }});
  await p.engine.submit(p.intent);assert.equal(record(p).state,"unknown");assert.equal(p.engine.sync(p.intent).enabled,false);
});
