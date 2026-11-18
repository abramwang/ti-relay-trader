(() => {
  "use strict";
  const el = (id) => document.getElementById(id);
  const picker = el("futuresAccount");
  if (!picker) return;
  let accounts = [], selected = "", action = "account.positions.query", page = 0, sort = 0, ascending = true, loading = false, loadError = "";
  const size = 20;
  const names = {"account.asset.query":"资金", "account.positions.query":"持仓", "order.list.query":"委托", "fill.list.query":"成交"};
  const states = {not_queried:"尚未查询", publication_unknown:"查询发送结果未知", not_published:"查询未发出", awaiting_reply:"等待回复", pending_review:"回复已收 · 待独立核验", collecting:"分页未完整", failed:"查询失败", invalid:"证据校验失败", ready:"历史查询已核验"};
  const connections = {read_only:"柜台在线 · 只读", assessment:"柜台测评中", execution:"柜台在线 · Relay 授权", stopped:"OC 已停止", stale:"心跳已过期", unavailable:"连接状态不可用", not_ready:"柜台未就绪", waiting:"等待交易窗口", expired:"授权窗口已结束"};
  const text = (v) => v === null || v === undefined || v === "" ? "--" : String(v);
  const date = (v) => !v || !Number.isFinite(Date.parse(v)) ? "--" : new Date(v).toLocaleString("zh-CN", {timeZone:"Asia/Shanghai", hour12:false});
  const escape = (v) => text(v).replace(/[&<>"']/g, (c) => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
  const number = (v) => v === null || v === undefined || v === "" ? "--" : Number.isFinite(Number(v)) ? Number(v).toLocaleString("zh-CN", {maximumFractionDigits:2}) : text(v);
  const cell = (raw, display = text(raw)) => ({raw, display});
  function tableData(q) {
    if (!q || !q.reviewed || q.state !== "ready") return {columns:[], rows:[]};
    if (action === "account.positions.query") return {
      columns:["合约", "交易所", "多空", "今昨仓", "投保", "投资单元", "持仓（手）", "今仓", "昨仓", "多头冻结", "空头冻结", "开仓成本", "持仓成本", "占用保证金", "采样时间（东八区）"],
      rows:(q.positions || []).map((r) => { const f=r.futures; return [cell(r.symbol),cell(r.exchange),cell(f.posi_direction, {"2":"多头","3":"空头"}[f.posi_direction] || f.posi_direction),cell(f.position_date,{"1":"今仓","2":"昨仓"}[f.position_date] || f.position_date),cell(f.hedge_flag),cell(f.invest_unit_id),...["position","today_position","yd_position","long_frozen","short_frozen","open_cost","position_cost","use_margin"].map(k=>cell(f[k],number(f[k]))),cell(r.captured_at,date(r.captured_at))]; })
    };
    if (action === "account.asset.query") return {
      columns:["柜台资金账户", "币种", "业务范围", "权益 Balance", "可用", "保证金", "冻结保证金", "冻结资金", "冻结手续费", "手续费", "持仓盈亏", "平仓盈亏", "入金", "出金", "范围确认", "采样时间（东八区）"],
      rows:(q.assets || []).map(r=>{const f=r.futures;return [cell(f.scope.account_id),cell(f.scope.currency_id),cell(f.scope.biz_type),...["balance","available","curr_margin","frozen_margin","frozen_cash","frozen_commission","commission","position_profit","close_profit","deposit","withdraw"].map(k=>cell(f[k],number(f[k]))),cell(f.scope_verified,f.scope_verified?"已确认":"未核验"),cell(r.captured_at,date(r.captured_at))];})
    };
    const fill=action === "fill.list.query";
    return {columns:[fill?"成交编号":"委托编号","柜台委托号","合约","交易所","买卖","开平","价格","数量",fill?"费用":"已成交",fill?"成交时间（东八区）":"委托时间（东八区）",fill?"费用完整":"状态"],rows:(fill?q.fills||[]:q.orders||[]).map(r=>[cell(fill?r.fill_id:r.gateway_order_id),cell(r.native_ids?.order_sys_id),cell(r.symbol),cell(r.exchange),cell(r.trade_side,{B:"买入",S:"卖出"}[r.trade_side]||r.trade_side),cell(r.offset_type,{O:"开仓",C:"平仓",T:"平今",Y:"平昨"}[r.offset_type]||r.offset_type),cell(Number(fill?r.price:r.limit_price),number(fill?r.price:r.limit_price)),cell(fill?r.qty:r.order_qty),cell(fill?r.fee:r.cum_filled_qty),cell(fill?r.matched_at:r.submitted_at,date(fill?r.matched_at:r.submitted_at)),cell(fill?r.fee_complete:r.gateway_status,fill?text(r.fee_complete):({working:"已报",cancelled:"已撤单",filled:"全部成交",partially_filled:"部分成交",rejected:"已拒绝"}[r.gateway_status]||r.gateway_status))])};
  }
  function orderObservation(a, q) {
    if(action!=="order.list.query" || !a || !Array.isArray(a.local_orders) || !a.local_orders.length)return q;
    const merged=new Map((q && q.reviewed && q.state==="ready"?q.orders||[]:[]).map(r=>[r.gateway_order_id,r]));
    for(const row of a.local_orders)merged.set(row.gateway_order_id,row);
    return {...q,reviewed:true,state:"ready",orders:[...merged.values()],local_event_view:true};
  }
  function render() {
    const a=accounts.find(a=>a.account_id===selected);
    window.RelayFuturesExecution?.update(accounts,selected,loadError);
    const q=a && a.queries.find(q=>q.action===action);
    const hb=a && a.heartbeat;
    el("futuresAccountTitle").textContent=a?`${a.alias || a.account_id} · ${a.account_id}`:"期货账户";
    el("futuresConnection").textContent=loadError?"页面读取失败":a?(connections[a.connection_state] || a.connection_state):"未配置期货账户";
    el("futuresHeartbeatTime").textContent=`心跳：${date(hb && hb.produced_at)}（东八区）`;
    el("futuresRouteIdentity").textContent=a?`${a.route.broker_id} / ${a.route.gateway_id}${a.connection_error?" · "+a.connection_error:""}`:"";
    const fresh=a?.execution && Date.parse(a.execution.valid_until)>Date.now() && !loadError;
    el("futuresExecutionPermission").textContent=`TEST 报单：${fresh && a.execution.submit_enabled?"已授权":"关闭"} · 撤单：${fresh && a.execution.cancel_enabled?"已授权":"关闭"}`;
    for (const b of el("futuresQueryTabs").querySelectorAll("button")) {
      const query=a && a.queries.find(q=>q.action===b.dataset.futuresQuery);
      b.setAttribute("aria-selected", String(b.dataset.futuresQuery===action));
      b.textContent=names[b.dataset.futuresQuery]+(b.dataset.futuresQuery==="order.list.query" && a?.local_orders?.length?` · ${a.local_orders.length}`:query && query.row_count!==null && query.row_count!==undefined?` · ${query.row_count}`:query && query.state==="failed"?" · 失败":"");
    }
    const status=el("futuresQueryStatus");
    status.dataset.failed=String(Boolean(loadError || a && a.ledger_error || q && ["failed","invalid"].includes(q.state)));
    status.textContent=loadError || a && a.ledger_error || (q?[states[q.state]||q.state,q.code!=="OK"?q.code:"",q.message,q.produced_at?`回报 ${date(q.produced_at)}`:"",q.trade_date?`交易日 ${q.trade_date}`:"",q.message_id && (!hb || hb.counter_session_id!==q.counter_session_id || !["read_only","assessment","execution"].includes(a.connection_state))?"历史会话采样":""].filter(Boolean).join(" · "):"尚无查询结果");
    if(action==="order.list.query" && a && a.local_orders?.length)status.textContent += ` · 本地委托回报 ${a.local_orders.length} 笔`;
    if(action==="order.list.query" && a && a.local_order_error)status.textContent += ` · ${a.local_order_error}`;
    const observation=orderObservation(a,q);
    const data=loadError || a && a.ledger_error?{columns:[],rows:[]}:tableData(observation);
    if(action==="order.list.query" && data.columns.length){
      data.columns.push("操作");
      data.rows.forEach((row,i)=>{row.cancelTarget=observation.orders[i];row.push(cell(""));});
    }
    data.rows.sort((a,b)=>{const x=a[sort]?.raw,y=b[sort]?.raw; const numeric=/^-?\d+(\.\d+)?$/;const delta=numeric.test(text(x)) && numeric.test(text(y))?Number(x)-Number(y):text(x).localeCompare(text(y),"zh-CN",{numeric:true});return ascending?delta:-delta;});
    const pages=Math.max(1,Math.ceil(data.rows.length/size));page=Math.min(page,pages-1);
    el("futuresQueryTable").querySelector("thead").innerHTML=`<tr>${data.columns.map((c,i)=>`<th><button type="button" data-sort="${i}" title="排序">${escape(c)}${i===sort?(ascending?" ↑":" ↓"):""}</button></th>`).join("")}</tr>`;
    const empty=q && q.reviewed && q.state==="ready"?"完整查询：0 条记录":"暂无可展示的有效数据";
    const visible=data.rows.slice(page*size,(page+1)*size);
    el("futuresQueryTable").querySelector("tbody").innerHTML=visible.map((row,i)=>`<tr>${row.map((c,j)=>row.cancelTarget && j===row.length-1?`<td><button type="button" data-futures-cancel="${i}" ${window.RelayFuturesExecution?.canCancel(row.cancelTarget)?"":"disabled"} title="撤销本地 TEST 委托">撤单</button></td>`:`<td title="${escape(c.raw)}">${escape(c.display)}</td>`).join("")}</tr>`).join("")||`<tr><td class="empty-state" colspan="${data.columns.length||1}">${empty}</td></tr>`;
    for(const button of el("futuresQueryTable").querySelectorAll("button[data-futures-cancel]"))button.addEventListener("click",()=>window.RelayFuturesExecution?.cancel(visible[Number(button.dataset.futuresCancel)].cancelTarget));
    el("futuresQueryCount").textContent=data.rows.length?`${data.rows.length} 条 · 每页 ${size} 条`:q && q.row_count===0?"0 条":"--";
    el("futuresQueryPage").textContent=`${page+1} / ${pages}`;
    el("futuresQueryPrev").disabled=page===0;el("futuresQueryNext").disabled=page+1>=pages;
    el("futuresQueryAudit").textContent=q?JSON.stringify({message_id:q.message_id,gateway_id:q.gateway_id,counter_session_id:q.counter_session_id,requested_at:q.requested_at,produced_at:q.produced_at,reviewed:q.reviewed,business_published:q.business_published},null,2):"--";
  }
  async function load() {
    if(loading)return;loading=true;el("futuresAccountRefresh").disabled=true;
    try {
      const res=await fetch("/v1/futures/terminal",{cache:"no-store",signal:AbortSignal.timeout(10000)});
      if(!res.ok || !res.headers.get("content-type")?.includes("application/json"))throw new Error(`读取失败 HTTP ${res.status}`);
      const body=await res.json();if(!body.ok || !Array.isArray(body.data?.accounts))throw new Error("期货查询视图不可用");
      accounts=body.data.accounts;loadError="";
      if(!accounts.some(a=>a.account_id===selected))selected=accounts[0]?.account_id||"";
      picker.innerHTML=accounts.map(a=>`<option value="${escape(a.account_id)}" title="${escape(a.alias)}">${escape(a.account_id)}</option>`).join("")||"<option>未配置期货账户</option>";
      picker.value=selected;picker.disabled=!accounts.length;
      el("futuresAccountEntry").hidden=!accounts.length;
      if(accounts.length)el("futuresAccountEntry").textContent=`${accounts[0].alias||"期货"} · ${accounts[0].account_id} · TEST`;
    }catch(err){loadError=err.message;}finally{loading=false;el("futuresAccountRefresh").disabled=false;render();}
  }
  el("futuresAccountEntry").addEventListener("click",()=>window.dispatchEvent(new CustomEvent("relay-futures-account-open")));
  picker.addEventListener("change",()=>{selected=picker.value;page=0;render();});
  el("futuresQueryTabs").addEventListener("click",e=>{const b=e.target.closest("button[data-futures-query]");if(!b)return;action=b.dataset.futuresQuery;page=sort=0;ascending=true;render();});
  el("futuresQueryTable").addEventListener("click",e=>{const b=e.target.closest("button[data-sort]");if(!b)return;const next=Number(b.dataset.sort);ascending=sort===next?!ascending:true;sort=next;render();});
  el("futuresQueryPrev").addEventListener("click",()=>{page=Math.max(0,page-1);render();});
  el("futuresQueryNext").addEventListener("click",()=>{page++;render();});
  el("futuresAccountRefresh").addEventListener("click",load);
  window.addEventListener("relay-futures-refresh",load);
  load();
  window.setInterval(()=>{if(!document.hidden && el("terminalShell").classList.contains("market-futures"))load();},5000);
})();
