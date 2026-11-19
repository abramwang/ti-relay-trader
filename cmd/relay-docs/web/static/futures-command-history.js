(() => {
  "use strict";
  const el = id => document.getElementById(id);
  const view = el("futuresCommandView");
  if (!view) return;
  const size = 20;
  const states = {registered:"已登记 · 发布未确认", attempted:"发布结果未知", published:"已发布至 OC 通道", not_published:"明确未发布"};
  const actions = {"order.submit":"报单", "order.batch.submit":"批量报单", "order.cancel":"撤单"};
  const receiptStates = {accepted:"命令已接收",failed:"命令失败",invalid:"回执冲突或无效"};
  const orderStates = {accepted:"已受理", working:"已报", partially_filled:"部分成交", filled:"全部成交", cancelled:"已撤单", rejected:"已拒绝"};
  const scopeKey = r => JSON.stringify([r?.env,r?.broker_id,r?.gateway_id,r?.account_id]);
  const orderStatesKey = a => JSON.stringify((a?.local_orders || []).map(o=>[o.account_id,o.gateway_order_id,o.trade_date,o.gateway_status]));
  const nonempty = v => typeof v === "string" && v.length > 0;
  const positiveID = v => Number.isSafeInteger(v) && v > 0;
  const date = v => new Date(v).toLocaleString("zh-CN", {timeZone:"Asia/Shanghai",hour12:false});
  let account, scope = "", active = false, generation = 0, request, loading = false;
  let items = [], cursors = [null], page = 0, next = null, loaded = false, error = "", readAt = "";

  function validate(data, expected, before) {
    if (!data || scopeKey(data.route) !== scopeKey(expected.route) || !Array.isArray(data.items) || data.items.length > size) throw new Error("命令记录账户或路由不匹配");
    let previous = before || Infinity;
    for (const item of data.items) {
      const p = item.publication;
      if (!positiveID(item.command_id) || item.command_id >= previous || item.account_id !== expected.account_id || item.gateway_id !== expected.route.gateway_id || !nonempty(item.message_id) || !nonempty(item.idempotency_key) || !Object.hasOwn(actions,item.action) || !Number.isFinite(Date.parse(item.registered_at)) || !/(Z|[+-]\d\d:\d\d)$/.test(item.registered_at) || item.broker_state_known !== false || item.retry_safe !== false || !p || p.command_id !== item.command_id || !Object.hasOwn(states,p.state) || p.command_published !== ({registered:null,attempted:null,published:true,not_published:false}[p.state]) || p.broker_accepted !== false) throw new Error("命令记录或发布状态校验失败");
      previous = item.command_id;
      const receipt = item.response;
      if (receipt && (p.state !== "published" || receipt.command_id !== item.command_id || receipt.action !== item.action || !Object.hasOwn(receiptStates,receipt.state) || receipt.response_validated !== (receipt.state !== "invalid") || receipt.native_audit_verified !== false || receipt.order_state_changed !== false || receipt.retry_safe !== false)) throw new Error("命令回执关联校验失败");
      if (item.action === "order.cancel") {
        if (!item.cancel || !nonempty(item.cancel.gateway_order_id) || !nonempty(item.cancel.cancel_id) || !/^\d{8}$/.test(item.cancel.trade_date) || item.orders?.length) throw new Error("撤单意图不完整");
      } else {
        if (!Array.isArray(item.orders) || !item.orders.length || item.orders.length > 100 || item.action === "order.submit" && item.orders.length !== 1 || item.cancel) throw new Error("报单意图不完整");
        for (const o of item.orders) {
          if (o.account_id !== expected.account_id || !nonempty(o.gateway_order_id) || !nonempty(o.symbol) || !nonempty(o.exchange) || !/^\d{8}$/.test(o.trade_date) || !["B","S"].includes(o.trade_side) || !["O","C","T","Y","CT","CY"].includes(o.offset_type) || !positiveID(o.order_qty) || typeof o.limit_price !== "string" || !/^\d+(?:\.\d+)?$/.test(o.limit_price)) throw new Error("报单意图校验失败");
        }
      }
    }
    if (data.next_before_id !== undefined && (!positiveID(data.next_before_id) || data.items.length !== size || data.next_before_id !== data.items.at(-1).command_id)) throw new Error("命令分页游标无效");
    return data;
  }

  function invalidate() {
    generation++;
    request?.abort();
    request = undefined;
    loading = false;
  }
  function reset() {
    items = []; cursors = [null]; page = 0; next = null; loaded = false; error = ""; readAt = "";
  }
  async function load() {
    if (!active || !account || loading || account.route?.env !== "test") return;
    invalidate();
    const version = generation, expected = account, before = cursors[page];
    const controller = new AbortController(); request = controller;
    const timeout = window.setTimeout(() => controller.abort(),10000);
    loading = true; loaded = false; error = ""; items = []; next = null; render();
    const query = new URLSearchParams({account_id:expected.account_id,limit:String(size)});
    if (before !== null) query.set("before_id",String(before));
    try {
      const response = await fetch("/v1/futures/commands?"+query,{cache:"no-store",signal:controller.signal});
      if (!response.headers.get("content-type")?.includes("application/json")) throw new Error(`命令记录不可用 · HTTP ${response.status}`);
      const body = await response.json();
      if (!response.ok || body.ok !== true) throw new Error(`命令记录读取失败 · HTTP ${response.status}`);
      const data = validate(body.data,expected,before);
      if (version !== generation) return;
      items = data.items; next = data.next_before_id || null; loaded = true; readAt = date(new Date().toISOString());
    } catch (err) {
      if (version !== generation) return;
      error = err.name === "AbortError" ? "命令记录读取超时" : err.message;
      items = []; next = null; loaded = true;
    } finally {
      window.clearTimeout(timeout);
      if (version === generation) { loading = false; request = undefined; render(); }
    }
  }
  function associated(item) {
    const targets = item.action === "order.cancel" ? [item.cancel] : item.orders;
    return targets.map(target => {
      const o = (account?.local_orders || []).find(row => row.account_id === item.account_id && row.gateway_order_id === target.gateway_order_id && row.trade_date === target.trade_date);
      return o ? orderStates[o.gateway_status] || o.gateway_status : "未读取到委托回报";
    }).join("\n");
  }
  function cell(row, value, className) {
    const td = document.createElement("td");
    td.textContent = value; td.title = value;
    if (className) td.className = className;
    row.append(td);
    return td;
  }
  function render() {
    view.hidden = !active;
    const body = el("futuresCommandTable").querySelector("tbody"); body.replaceChildren();
    el("futuresCommandStatus").dataset.failed = String(Boolean(error));
    el("futuresCommandStatus").textContent = error || (!account ? "未配置 TEST 期货账户" : loading ? "正在读取持久命令记录" : loaded ? `持久记录 · ${account.route.gateway_id} · 读取于 ${readAt}` : "尚未读取命令记录");
    el("futuresCommandCount").textContent = loaded && !error && !loading ? `本页 ${items.length} 条 · 每页 ${size} 条` : "--";
    el("futuresCommandPage").textContent = `第 ${page+1} 页`;
    el("futuresCommandPrev").disabled = loading || page === 0;
    el("futuresCommandNext").disabled = loading || !next || Boolean(error);
    el("futuresCommandRefresh").disabled = loading || !account;
    for (const item of items) {
      const row = document.createElement("tr");
      row.dataset.commandId = String(item.command_id);
      const orders = item.orders || [];
      cell(row,date(item.registered_at));
      cell(row,actions[item.action]);
      cell(row,item.cancel?.gateway_order_id || orders.map(o=>`${o.symbol}.${o.exchange}`).join("\n"),"command-target");
      cell(row,orders.map(o=>`${o.trade_side === "B" ? "买入" : "卖出"} / ${{O:"开仓",C:"平仓",T:"平今",Y:"平昨",CT:"平今",CY:"平昨"}[o.offset_type]}`).join("\n") || "--");
      cell(row,orders.map(o=>o.limit_price).join("\n") || "--");
      cell(row,orders.map(o=>String(o.order_qty)).join("\n") || "--");
      cell(row,states[item.publication.state],"command-publication").dataset.state = item.publication.state;
      cell(row,item.response ? receiptStates[item.response.state] : "尚无回执");
      cell(row,associated(item));
      const td = cell(row,"");
      const details = document.createElement("details"), summary = document.createElement("summary"), pre = document.createElement("pre");
      summary.textContent = `#${item.command_id}`;
      pre.textContent = JSON.stringify({message_id:item.message_id,idempotency_key:item.idempotency_key,stream_id:item.publication.stream_id || null,reason:item.publication.reason || null,response:item.response || null,orders:item.orders,cancel:item.cancel},null,2);
      details.append(summary,pre); td.append(details); body.append(row);
    }
    if (!items.length) {
      const row = document.createElement("tr");
      cell(row,error ? "读取失败，不代表没有委托" : loading ? "正在读取" : !account || !loaded ? "尚无可用命令列表" : page ? "此页无更早的已登记命令" : "当前路由暂无已登记报撤命令","empty-state").colSpan = 10;
      body.append(row);
    }
  }
  function refresh() {
    if (!active || !account) return;
    invalidate(); reset(); return load();
  }
  el("futuresCommandRefresh").addEventListener("click",refresh);
  el("futuresCommandPrev").addEventListener("click",()=>{if(!loading && page>0){page--;return load();}});
  el("futuresCommandNext").addEventListener("click",()=>{if(!loading && next && !error){cursors[page+1]=next;page++;return load();}});
  window.RelayFuturesCommandHistory = {
    refresh,
    update(a,visible) {
      const valid = a?.route?.env === "test" && /^\d{6,20}$/.test(a.account_id) && a.account_id === a.route.account_id && nonempty(a.route.broker_id) && nonempty(a.route.gateway_id);
      const key = valid ? scopeKey(a.route) : "";
      const changed = key !== scope || active !== Boolean(visible) || orderStatesKey(account) !== orderStatesKey(valid ? a : undefined);
      if (key !== scope) { invalidate(); reset(); scope = key; }
      if (!visible && active) invalidate();
      account = valid ? a : undefined; active = Boolean(visible);
      if (changed) render();
      if (active && account && !loaded && !loading) return load();
    },
  };
  render();
})();
