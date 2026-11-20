(() => {
  "use strict";
  const storageKey = "relay.futures.test.commands.v1";
  const panel = document.getElementById("futuresExecutionRecords");
  if (!panel) return;
  let accounts = [], selected = "", unavailable = true, storageError = "", records = [], busy = false;
  const states = {sending:"发送中", unknown:"结果未知 · 待核对", registered:"已登记 · 尚未确认发送", attempted:"发送结果未知", published:"命令已发出", not_published:"未发出", rejected:"请求被拒绝"};
  const orderStates = {accepted:"柜台已受理",working:"柜台已报",partially_filled:"部分成交",filled:"全部成交",cancelled:"已撤单",rejected:"柜台拒单"};
  const routeKey = a => JSON.stringify(a.route);
  const account = () => accounts.find(a => a.account_id === selected);
  function save() {
    if (storageError) return false;
    try { sessionStorage.setItem(storageKey, JSON.stringify(records)); return true; }
    catch (_) { storageError = "请求记录保存失败 · 禁止新报撤"; return false; }
  }
  try {
    records = JSON.parse(sessionStorage.getItem(storageKey) || "[]");
    if (!Array.isArray(records) || records.length > 100 || records.some(r => !r || !["submit","cancel"].includes(r.kind) || !r.body || typeof r.body.idempotency_key !== "string" || typeof r.route !== "string" || typeof r.account !== "string" || (r.kind === "submit" && (!r.body.order || r.body.order.account_id !== r.account)) || (r.kind === "cancel" && r.body.account_id !== r.account))) throw new Error();
    records.forEach(r=>{if(r.state==="sending")r.state="unknown";});
  } catch (_) { records = []; storageError = "请求记录无法读取 · 禁止新报撤"; }
  function activeRecords(a) { return a ? records.filter(r => r.account === a.account_id && r.route === routeKey(a)) : []; }
  function observed(r, a) {
    const id = r.kind === "cancel" ? r.body.gateway_order_id : r.result?.gateway_order_id;
    const day = r.kind === "cancel" ? r.body.trade_date : r.body.order.trade_date;
    return (a?.local_orders || []).find(o => o.gateway_order_id === id && o.trade_date === day);
  }
  function marketPermission(a, exchange) {
    const p = a?.execution;
    if (Array.isArray(p?.markets)) return p.markets.find(m => m.exchange === exchange);
    if (exchange === "CFFEX") return p?.cffex;
    return exchange === "SHFE" ? p : null;
  }
  function permission(a, kind, exchange) {
    if (storageError || unavailable || !window.crypto?.getRandomValues || !a || a.route?.env !== "test" || a.connection_error || a.ledger_error || a.local_order_error) return false;
    const p = marketPermission(a, exchange);
    return Boolean(p && Date.parse(p.valid_until) > Date.now() && p[kind + "_enabled"] === true && (kind !== "submit" || a.execution.price_source === "meridian_futures_snapshot.v1"));
  }
  function allowedIntent(a, intent, exchange) {
    const p = marketPermission(a, exchange);
    if (!p || !["B","S"].includes(intent.trade_side) || intent.hedge_type !== "speculation" || !Number.isSafeInteger(intent.qty) || intent.qty <= 0) return false;
    if (Array.isArray(a.execution.markets)) return intent.qty <= p.max_order_qty && (intent.offset_type === "O" || intent.offset_type === "C" && p.close_model === "combined");
    if (exchange === "CFFEX") return intent.qty <= p.max_order_qty && ["O","C"].includes(intent.offset_type) && p.products?.includes(intent.security_id.match(/^([A-Z]+)\d{4}\.CFFEX$/)?.[1]);
    return intent.trade_side === "B" && intent.offset_type === "O" && intent.qty === 1;
  }
  function unresolved(a) {
    return activeRecords(a).some(r => r.kind === "submit" && !["rejected","not_published"].includes(r.state) && !observed(r,a));
  }
  function sync(intent) {
    const a = account();
    const exchange = intent?.security_id?.split(".")[1];
    const valid = intent && intent.account_id === selected && /^\d{8}$/.test(intent.trade_date) && intent.trade_date === a?.execution?.trade_date && /^[A-Z][A-Z0-9]{0,79}\.[A-Z][A-Z0-9]{0,7}$/.test(intent.security_id) && allowedIntent(a,intent,exchange) && /^\d+(?:\.\d+)?$/.test(intent.price) && /[1-9]/.test(intent.price);
    const enabled = !busy && records.length < 100 && permission(a,"submit",exchange) && valid && !unresolved(a);
    const reason = storageError || (unresolved(a) ? "上一笔报单待核对" : !permission(a,"submit",exchange) ? "TEST 报单权限关闭或状态已过期" : !valid ? "超出账户市场配置或开平能力" : busy ? "请求处理中" : records.length >= 100 ? "请求记录已满 · 待核对" : "TEST · Meridian 定价");
    return {enabled,reason};
  }
  function canCancel(row) {
    const a=account();
    return Boolean(!busy && records.length < 100 && permission(a,"cancel",row?.exchange) && row && row.account_id === selected && row.trade_date === a.execution.trade_date && /^relay-web-order-/.test(row.gateway_order_id) && row.counter_session_id === a.heartbeat?.counter_session_id && ["accepted","working"].includes(row.gateway_status) && row.leaves_qty > 0 && (a.local_orders || []).some(o => o.gateway_order_id === row.gateway_order_id && o.trade_date === row.trade_date) && !activeRecords(a).some(r => r.kind === "cancel" && r.body.gateway_order_id === row.gateway_order_id && r.body.trade_date === row.trade_date));
  }
  function notify() { window.dispatchEvent(new CustomEvent("relay-futures-execution-change")); }
  async function api(path, options = {}) {
    const response = await fetch(path, {cache:"no-store",signal:AbortSignal.timeout(10000),...options});
    if (!response.headers.get("content-type")?.includes("application/json")) throw new Error("响应不是 JSON · 结果待核对");
    const body = await response.json();
    if (!response.ok || !body.ok) {
      const error = new Error(body.error?.message || `HTTP ${response.status}`);
      error.definitive = response.status === 400;
      throw error;
    }
    return body.data;
  }
  function accept(r, data) {
    if (!data || data.idempotency_key !== r.body.idempotency_key || !data.message_id || data.broker_state_known !== false || data.retry_safe !== false || !Number.isSafeInteger(data.publication?.command_id) || data.publication.command_id <= 0 || !["registered","attempted","published","not_published"].includes(data.publication.state)) throw new Error("命令响应不匹配 · 待核对");
    const published = {registered:null,attempted:null,published:true,not_published:false}[data.publication.state];
    if (data.publication.command_published !== published || data.publication.broker_accepted === true) throw new Error("发布状态冲突 · 待核对");
    if (r.result && (r.result.message_id !== data.message_id || r.result.gateway_order_id !== data.gateway_order_id || r.result.cancel_id !== data.cancel_id || r.result.publication.command_id !== data.publication.command_id || (["published","not_published"].includes(r.result.publication.state) && r.result.publication.state !== data.publication.state))) throw new Error("原命令身份或发布状态变化 · 待核对");
    const original = r.kind === "submit" ? r.body.order : r.body;
    const returned = r.kind === "submit" ? data.order : data;
    if (!returned || !Object.keys(original).every(k => returned[k] === original[k])) throw new Error("命令意图不匹配 · 待核对");
    if (r.kind === "submit" && !data.gateway_order_id || r.kind === "cancel" && !data.cancel_id) throw new Error("命令身份缺失 · 待核对");
    r.result = data; r.state = data.publication.state; r.error = "";
  }
  async function send(kind, body, a) {
    if (busy || !window.crypto?.getRandomValues) return;
    const bytes = new Uint8Array(16); window.crypto.getRandomValues(bytes);
    body.idempotency_key = "web-" + Array.from(bytes,b=>b.toString(16).padStart(2,"0")).join("");
    const r = {kind, body, account:a.account_id, route:routeKey(a), state:"sending", at:new Date().toISOString()};
    records.push(r);
    if (!save()) { render(); notify(); return; }
    busy=true; render(); notify();
    try { accept(r, await api(kind === "submit" ? "/v1/futures/orders" : "/v1/futures/cancels", {method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)})); }
    catch (err) { r.state=err.definitive ? "rejected" : "unknown"; r.error=err.message; }
    finally { busy=false; save(); render(); notify(); window.dispatchEvent(new CustomEvent("relay-futures-refresh")); }
  }
  async function submit(intent) {
    if (!sync(intent).enabled) return;
    const a=account();
    if (!window.confirm(`TEST ${a.alias || a.account_id} (${a.account_id})\n${intent.security_id} ${intent.trade_side === "B" ? "买入" : "卖出"}${intent.offset_type === "O" ? "开仓" : "平仓"} · 投机\n${intent.price} × ${intent.qty} 手\n确认发送？`)) return;
    if (!sync(intent).enabled || a !== account()) return;
    await send("submit", {order:{...intent}}, a);
  }
  async function cancel(row) {
    if (!canCancel(row)) return;
    const a=account();
    if (!window.confirm(`TEST ${a.alias || a.account_id} (${a.account_id})\n撤销 ${row.symbol} · ${row.gateway_order_id}\n剩余 ${row.leaves_qty} 手，确认撤单？`)) return;
    if (!canCancel(row) || a !== account()) return;
    await send("cancel", {account_id:a.account_id,gateway_order_id:row.gateway_order_id,trade_date:row.trade_date}, a);
  }
  async function check(r) {
    if (busy || !activeRecords(account()).includes(r)) return;
    busy=true; render(); notify();
    try { accept(r, await api(`/v1/futures/${r.kind === "submit" ? "order" : "cancel"}-command?` + new URLSearchParams({account_id:r.account,idempotency_key:r.body.idempotency_key}))); }
    catch (err) { r.error=err.message + " · 不重发"; }
    finally { busy=false; save(); render(); notify(); }
  }
  function render() {
    const a=account();
    panel.replaceChildren();
    if (storageError) { const p=document.createElement("p");p.textContent=storageError;panel.append(p); }
    for (const r of activeRecords(a).slice().reverse()) {
      const line=document.createElement("div"); line.className="futures-command-record";
      const label=document.createElement("span");
      const o=observed(r,a);
      const actual = o ? (r.kind === "cancel" && o.gateway_status !== "cancelled" ? "原单：" : "") + (orderStates[o.gateway_status] || o.gateway_status) : "暂无柜台回报";
      const intent=r.kind === "submit" ? r.body.order : r.body;
      label.textContent=`${r.kind === "submit" ? "报单" : "撤单"} · ${intent.security_id || intent.gateway_order_id} · ${states[r.state] || "待核对"} · ${actual}${r.error ? " · "+r.error : ""}`;
      const key=document.createElement("small"); key.textContent=r.body.idempotency_key;
      const button=document.createElement("button");button.type="button";button.textContent="核对状态";button.disabled=busy;button.addEventListener("click",()=>check(r));
      line.append(label,button,key);panel.append(line);
    }
    if (!panel.childNodes.length) { const p=document.createElement("p");p.textContent="暂无本页报撤记录";panel.append(p); }
  }
  window.RelayFuturesExecution = {
    sync,submit,cancel,canCancel,
    offsets(exchange) {
      const a=account(), p=marketPermission(a,exchange);
      if (!Array.isArray(a?.execution?.markets)) return null;
      return p ? (p.close_model === "combined" ? ["O","C"] : ["O"]) : [];
    },
    update(accountsValue,selectedValue,failed) { accounts=accountsValue;selected=selectedValue;unavailable=Boolean(failed);render();notify(); },
  };
  window.setInterval(notify,1000);
  render();
})();
