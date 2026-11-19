# relay 统一交易接口 Schema

更新时间：`2026-11-19`

## 当前状态

第一版 schema 已落在 Go 包 `internal/trading`，版本号为 `relay.trading.v1alpha1`。

`GET /v1/schema` 同时返回机器可读 `capabilities`。当前能力为：

- `ledger.cursor_pagination.v1`
- `events.cursor_resume.v1`
- `orders.batch_child_outcomes.v1`
- `orders.explicit_command_ids.v1`

SDK 应在启动阶段使用能力握手，不得只比较 Python 包版本。缺少必需能力时失败关闭。

该 schema 定义对象、枚举、基础校验和状态机语义。当前 API 模式已将资金、持仓、单笔下单、批量下单、撤单、订单查询、成交查询和前置刷新接入测试链路；下单/批量下单/撤单写 Redis `cmd.trade`，资金/持仓/订单/成交查询读取 PostgreSQL 本地账本，刷新接口写 Redis `cmd.query`。

## 参考来源

1. 前置服务 Redis Stream 对接手册：`docs/THIRD_PARTY_INTEGRATION_GUIDE.md`
2. C++ 交易结构体：`/home/Titans/resource/include/ti_trader_struct.h`
3. C++ 交易客户端接口：`/home/Titans/resource/include/ti_trader_client.h`
4. C++ 回调接口：`/home/Titans/resource/include/ti_trader_callback.h`

当前前置测试环境已启动，后续需要联调时可以直接基于 Redis Stream 做查询、下单、撤单和事件消费验证。凭据仍只放在部署机本地配置或安全渠道，不写入仓库。

## HTTP Envelope

### 期货持久命令列表（TEST已部署）

`GET /v1/futures/commands?account_id=300052141&limit=50&before_id=43`，能力标识`futures.command_history.v1`。只读取已配置TEST账户当前gateway的PostgreSQL命令，不依赖发送服务、交易权限、新鲜心跳、Redis或Meridian在线；不登录/查询OC，不创建登记、不发布或恢复命令。生产环境503；该能力标识表示实现存在，不表示账户已配置或可交易。

参数仅有必填`account_id`、可选`limit`（默认50，1..100）、`before_id`（上一页`next_before_id`，正int64）；未知/重复参数、非法游标400，未配置账户404，不完整/损坏记录或数据库不可用503而非空成功。错误不泄露私有连接信息；GET以外405。使用同一个只读数据库快照取得页面及发布状态，全部响应no-store，不改变owner/cursor、审计计数或ACK。

`data.route`始终返回本页的`env/broker_id/gateway_id/account_id`，空页也保留，供切账户/路由时校验。`data.items`按持久`command_id`倒序，一条登记只出现一次，不按重投别名重复列；包含HTTP及CLI登记的报单、批量报单意图、撤单，不包含只读查询和登记前失败的请求。每项包括`command_id/account_id/gateway_id/action/message_id/idempotency_key/registered_at`、原始`orders`或`cancel`以及`publication`。只列已登记的批量意图不代表批量交易服务已开放。金额/价格继续使用已有精确十进制类型。

`publication`沿用下一节的四种状态；`command_published=null`保持未知，`broker_state_known=false/retry_safe=false`，本GET的`replayed=false`，均不能推断柜台受理/拒绝/成交/撤成。`next_before_id`存在才有下一页，下一页严格小于此ID；新登记在前页之前插入不导致重复，刷新首页才会看到。空页`items=[]`，不声称柜台无订单。当前只查配置的gateway，不自动跨历史路由合并。

SDK源码`list_futures_commands(account_id=..., limit=50, before_id=None)`，一次GET、不自动翻页/轮询/重试下单；SDK wheel尚未发布。11月19日TEST API及终端“报撤命令”已部署，每页20条，显式分页/刷新，关闭页面或换浏览器后可从PG重新读取，不依赖本页sessionStorage。关联委托状态仅来自同账户/当前路由、原委托ID及交易日匹配的本地订单观察，不能把撤单命令published或原单filled解释为撤成；尚未将独立拒撤/全部命令回执作为列表结果列。列表不提供重发或撤单操作，不开放新权限。此前按key的两个GET和“本页报撤记录”保持原职责。

后续源码增量（尚未部署，需迁移000050及显式worker `project_replies=true`）：单笔报单/撤单列表可增加`response`，含`command_id/action/state/code/message/produced_at/response_validated`，state为accepted、failed或invalid。`native_audit_verified/order_state_changed/retry_safe`始终false；与publication及真实委托状态分开呈现，未收到/未关联则省略，不把失败自动转换为可重试。批量交易回执暂不自动投影。独立拒撤事件不归入这个命令回执字段。

同一增量使`GET /v1/futures/terminal`的四查询优先读持久结果：`response_persisted=true`；完整校验通过为`response_ready`及原类型行数组/row_count，缺页collecting、失败failed、矛盾invalid，失败和矛盾不回退旧成功候选。`response_validated`只是协议/关联校验，`reviewed=false/business_published=false`保留；不是独立原生审计、经济账本已发布或新鲜交易准入。GET不收件、不ACK、不发查询，旧人工审计批次仍沿用原路径。

### 期货TEST发送编排（源码新增，默认关闭）

收件健康增量（2026-11-19，源码待部署）：新报单/撤单要求独立TEST库中双路reply/event收件健康、来源与owner匹配；登记前和实际发布前均检查。无记录/缺表/任一路15秒未续期/显式停收时拒绝新发送，登记前返回503，已登记未发布保留原publication结果。重复原key及GET核对不依赖此健康状态，不自动重发。终端账户增加`capture={ready,state,valid_until}`，`execution.reason=relay_capture_unavailable`表示收件链路不可用；业务落库/柜台授权另行判断。需先应用迁移000048并启动双路收件，操作员CLI不属于本HTTP门禁，详见[运行边界](OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md#收件健康与http停发联动11月19日增量)。

`POST /v1/futures/orders`和`GET /v1/futures/order-command`已接现有持久命令登记/TEST发布器；本轮只完成源码及隔离验证，未部署、未发真实命令、未发布SDK wheel，不表示生产期货或终端发送已开放。两个入口与股票`/v1/orders`分离，不改变股票账户配置。生产环境始终503。

POST要求`Content-Type: application/json`，拒绝跨Origin、query参数、重复键、额外字段及超过4KiB正文：

```json
{"idempotency_key":"strategy-run-1-order-1","order":{"account_id":"300052141","security_id":"RB2701.SHFE","trade_date":"20261119","trade_side":"B","offset_type":"O","hedge_type":"speculation","qty":1,"price":"3000.000"}}
```

调用方只给意图及显式幂等键，不能传路由、session、权限、原始行情或command/order ID。price为正十进制字符串，委托原价不改；同一scope及key生成稳定的message/client/gateway身份，幂等域跨交易日保留。同键改价、改量、改方向/开平/日期/标的返回409；仅价格尾零不同视为同一数值并返回原始价格文本。路由变更不是幂等续接，仍须按既有责任核对流程处理，不能换gateway规避历史。

新请求由服务端从已配置Meridian取得唯一正式目录/tick及当前原始快照，取得绑定路由的新鲜心跳session/代次/TradingDay，再构造短期Relay本地许可，登记后使用既有原子TEST发布器。总时限8秒，不自动重试、不改价、不发送OC行情查询、不静默回退原生报价。现有发布边界仍为东吴TEST `ctp_4900 / 300052141`、SHFE买开投机1手；不放宽成生产规则。**东吴测评行情与实盘不同，不能据此直接用Meridian实盘价启动测试**；需要原生定价时仍显式使用既有execution CLI，不能冒称本HTTP服务已支持原生来源。

幂等重试先读取已登记事实，不再要求行情/新会话或重新授权。并发请求在同一数据库路由锁下重查，只有新登记的请求可以尝试发布；其余返回原记录。不另造幂等表，不修改原始命令、目录证据及行情正文。进程中断后即使只有registered，也不自动续发；attempted/超时/发布确认未知继续待核。SDK不生成替代key、不重试、不轮询。

HTTP202只表示已取得持久登记身份，返回原始order、idempotency_key、message_id、gateway_order_id、client_order_id及publication：

| publication.state | command_published | 解释 |
| --- | --- | --- |
| registered | null | 已登记，尚无可用的持久发布结果；可能有并发首请求仍在处理，不据此补发 |
| attempted | null | 发布尝试已登记，结果未确认；不得自动补发 |
| published | true | 已确认Redis命令entry；不等于OC受理、柜台接单或成交 |
| not_published | false | 原子门禁关闭，原请求不再尝试；也不授权调用方自动换key重报 |

`broker_state_known=false`、`retry_safe=false`，publication里的broker_accepted也不作为拒单事实；柜台终态仍查订单/成交回报。`GET /v1/futures/order-command?account_id=300052141&idempotency_key=strategy-run-1-order-1`只读上述原始身份/发布记录，不访问Meridian或OC、不恢复发送。404表示该已配置scope下未找到记录，不是全路由的柜台未执行证明。格式400、冲突409、服务/依赖不可用503；未登记时失败不暴露私有连接错误。所有业务响应no-store。登记后的发布异常仍返回202和状态，调用方必须检查publication，不能仅判断HTTP成功。

运行配置分两层：`futures_terminal.execution_enabled`默认false；期货专用manifest账户可选`execution`包含`submit_enabled`、`cancel_enabled`（缺省false）、带时区的`authorization_expires_at`、既有`command_group`。这些均由服务端运维配置，不从心跳取得授权、不由请求覆盖。必须已有独立、名称以`_test`结尾的数据库且`current_database()`与ExpectedName一致；不自动建库/迁移/初始化Redis。启用仍需独立收件及可用撤单路径，不因编排已完成直接开放终端。

全局发送关闭时POST503；保留有效manifest/数据库配置后GET仍可查询原责任。账户submit关闭或期限已过时，新意图拒绝，同键已登记意图仍可读取。配置当前随服务启动加载，不声称已有动态热撤权；线上终端保持禁发，新增按钮接线见下方终端小节。源码SDK方法为`submit_futures_order`和`get_futures_order_command`；撤单编排见下一节，原生事实HTTP分流、动态停止及持续消费服务部署仍待后续接入，OC无需修改wire。

### 期货TEST撤单编排（源码新增，默认关闭）

`POST /v1/futures/cancels`复用上述服务、独立TEST库及原撤单发布器；新增只读`GET /v1/futures/cancel-command`。未部署、未开放线上终端权限、未发布wheel，生产环境503；不改股票撤单或OC协议。

```json
{"idempotency_key":"strategy-run-1-cancel-1","account_id":"300052141","gateway_order_id":"原始报单返回的gateway_order_id","trade_date":"20261119"}
```

POST只接受这四项，正文最多4KiB、JSON及同Origin规则与报单相同。调用方不能提供receipt_id、cancel_id、session、柜台ID、报价或授权。报单与撤单共用scope级幂等键空间，必须使用不同的显式key；相同撤单key改目标或交易日409。

- Relay从持久原报单及event原文选择可撤证据，核对来源、scope、原命令关联、哈希、交易日/会话及原生SysID。按数值Stream ID选最新订单观察，不按数据库插入顺序或浏览器选中行猜测；发现终态、冲突/损坏/隔离记录或晚于最新订单的成交，拒绝发送。发布事务内再次核验，不能沿用已被更新的working回报。DB核对与柜台状态不构成跨系统原子快照，最终可撤性仍由柜台判断。
- 账户`cancel_enabled`和OC `accepting_cancel_commands`独立检查，不依赖`submit_enabled`、OC新报单能力或Meridian行情；全局`execution_enabled=false`仍关闭两个POST，两个GET只读不受此开关影响。不能把“停止新报单”配置成关闭所有执行而期望撤单仍可发送。
- 当前继承原TEST边界：仅本服务已确认发布的本地单、相同source/run/epoch/group/session/代次/交易日，订单为accepted/working且有剩余量；最新订单回报须在1分钟内。旧CLI单、外部单、跨会话/跨日、已发布但状态未知和长期无新回报的挂单均不在此增量范围；不能伪造新时间解除限制。
- 保留每单一次持久撤单发布尝试的保护；已有attempted/published/not_published不允许换key追加撤单。registered记录同键也不续发。明确拒撤后再次撤单尚待独立实现，不把该限制宣称为完整普通期货撤单能力。
- 新登记方最多发布一次，重复请求只读取原记录，不重取心跳/行情、不另造cancel_id、不重发。授权到期或账户撤单关闭后，同键仍能读取原责任（全局POST关闭则使用GET）。发送前新到终态导致只登记未发送时，也保留原记录，不在重试时恢复发送。

202返回原请求、稳定`message_id/cancel_id`及publication；状态表沿用上一节，`broker_state_known=false/retry_safe=false`，published不等于cancelled。GET参数为`account_id`和撤单的`idempotency_key`，只读原登记/发布状态，不能用它替代订单终态。SDK新增`cancel_futures_order`、`get_futures_cancel_command`，均只请求一次，不自动重试或轮询。

### 期货TEST终端报撤联动（源码新增，待部署）

终端复用上述两个POST及两个命令状态GET，股票表单不会接收期货委托。当前仍只支持Meridian来源、SHFE买开投机1手的TEST报单；撤单继承上一节边界。界面接线不等于启用，线上配置与服务未替换；东吴测评不同源报价不能直接用本页实盘价格测试，仍走显式原生CLI。

`GET /v1/futures/terminal`继续只读查询及本地订单回报，增加账户`execution`显示对象。只有TEST、发送总开关、已构建执行服务均满足时才返回该对象；生产不读取TEST账户。`submit_authorized/cancel_authorized`表示Relay配置及授权期限，`submit_enabled/cancel_enabled`还要求当前源绑定、心跳/恢复/结算和TEST日间时段。11月19日新版execution已删除全量原生订单快照的重复门禁。`valid_until`不晚于心跳产生后5秒、授权截止和时段结束；`trade_date`来自柜台，不从浏览器自然日推导。`price_source=meridian_futures_snapshot.v1`。这些仅用于按钮显示，不能作为可复用许可或替代发布前校验；资产scope仍未升级为已验证。

11月19日持仓显示修复：`account.positions.query`的已发布命令，可直接展示经关联和完整性校验的OC持仓回包，不再要求运维先补一份人工SDK审计文件。自动校验原登记scope、来源、session/代次/交易日、命令关联、原文摘要、SDK返回/快照应用标志及完整分页；SDK ticket/request_id只用于回包间一致性，不宣称是独立请求审计。完整结果返回`state=response_ready`、`response_validated=true`、`positions`和`row_count`；`reviewed=false`、`business_published=false`保持不变，不修改收件状态、游标或正式业务快照。独立人工核验的原`ready/reviewed=true`路径仍可读取。未完成、失败、冲突及未知发布不展示部分持仓或伪造0；只有完整空结果才显示0。采样时间和历史会话照实显示，不从成交增量拼造完整当前持仓。页面刷新仍为GET已保存快照，不隐式发柜台查询；资金、订单和成交查询的独立核验规则此次未扩大。

新版Relay-owned execution心跳使用`connection_state=execution`，不再因旧assessment截止字段为空误判未就绪；保持零时长常驻、容量及锁存字段，不把心跳存活当交易授权。顶层`mode`在任一动作当前可用时为`test_execution`，否则仍为`query_observation_only`，顶层及账户报撤布尔均以服务端结果为准。预检继续不发送且`can_submit=false`，已接执行服务时权限说明改取同一manifest，不再沿股票账户配置产生矛盾提示。

网页报撤均需明确点击并确认账户、合约和数量/原单。请求前生成随机显式key并写入本标签页sessionStorage，写入失败不发请求；服务端PG仍是命令责任的权威持久记录，浏览器不是账本。按TEST route+账户隔离本页记录，上限100条，不自动删除未知记录。刷新后保留原key；关闭标签页、清浏览器存储或换设备不承诺恢复本页列表，可通过独立“报撤命令”页签读取已持久登记的历史。不应据空列表推断无在途命令；新列表不替代服务端跨浏览器未决风险检查。

发布状态与原委托的真实回报分别展示，published不改成accepted/cancelled。未知/注册未发及尚未观察到原委托的报单阻止本页再报；“核对状态”仅GET原key，404/网络错误不自动重新POST、换key或认定未执行。异步返回只更新原账户/route记录。撤单按钮只对当前会话本地回报开放，不从历史查询页假造本地单。查询表仍沿5秒只读刷新，使用既有持久event观察，不是新增交易SSE或独立消费守护服务。

价格原文本保留；HTML步长基准改为0，避免默认股价9.67使整数tick的期货价无法提交。行情/接收时间按Asia/Shanghai转换，不把UTC原文截取后标成东八区；无时区值不猜时区。本轮浏览器验收全部API被拦截，未访问真实柜台或业务Redis。

### 期货只读报价准备（源码新增，待部署）

`GET /v1/futures/price-preview?security_id=RB2701.SHFE&trade_date=20261119`，能力标识`futures.price_preview.v1`。只接受一个真实CFFEX/SHFE合约标准代码及明确的YYYYMMDD交易日，不从自然日期推导夜盘TradingDay；不接受账户权限、行情正文、原生来源、tick、URL或`allow_stale`覆盖参数。

Relay从配置的Meridian依次读取唯一正式目录记录和当前快照，保留HTTP原始JSON字节及精确数值，复用现有报价核验函数。两次请求共用5秒超时，目录正文最多64KiB、快照最多32KiB；不重试、不跟随重定向、不使用陈旧缓存，不访问OC、Redis或交易数据库。目录publication/checksum、交易所coverage、真实期货类型和active状态均需有效；失败不继续查询快照。

成功返回标准envelope，`data`包含：

- `mode=price_preview_only`；`business_authorized=false`、`command_published=false`、`publication_supported=false`始终明确返回；只描述本接口，不改变账户权限。
- `catalog_body_sha256`：本次目录原文摘要。
- `preparation`：market、有效期、行情原文摘要及`business_authorized=false`；market包括目录映射/tick、Meridian报价来源及base64保留的行情JSON原文。目录与行情不是跨源原子快照，响应也不是可重复使用的下单许可。

400表示非法/重复/多余参数，405表示非GET，503表示依赖不可用或证据未通过，`error.detail.stage`为`catalog_request/catalog_validation/snapshot_request/snapshot_validation`；不透出上游私有错误正文、URL或凭据。所有结果`Cache-Control: no-store`。

SDK源码入口为`get_futures_price_preview(security_id=..., trade_date=...)`，仅一次GET。普通期货API/SDK报撤仍未开放；该能力不代表`/v1/orders`支持期货，也不改变既有股票接口。正式使用前核对部署版`/v1/schema`，本轮没有发布wheel或重启服务。

### 期货委托预检（源码新增，待部署）

`POST /v1/futures/orders/preview`，能力`futures.order_preview.v1`，当前仅服务已配置的TEST期货观察账户。POST只是传递委托意图，**不登记订单、不写交易流、不授权、不撤单**；生产环境不读取TEST观察库或心跳，返回503。本接口不替代既有TEST execution CLI，不是普通期货交易已开放。

```json
{"account_id":"300052141","security_id":"RB2701.SHFE","trade_date":"20261119","trade_side":"B","offset_type":"O","hedge_type":"speculation","qty":1,"price":"3000.000"}
```

字段全部必填：price必须是正十进制字符串，qty是正int32整数token；日期为明确TradingDay。方向B/S、开平O/C/CT/CY、投机speculation；参数合法不代表当前发送范围支持。拒绝重复键、额外字段、query参数、超4KiB正文及来源/权限覆盖。只读取所选账户当前心跳，不加载历史账本，再复用Meridian目录/快照准备；总时限8秒，其中行情最多5秒，无重试或OC查询命令。

成功返回`mode=order_preview_only`、原样`intent`及`quote`（前述报价准备结构）。以下指标分别说明，不互相代替：

- `price_valid`：正数、精确tick校验。原委托价格不变，不以动态涨跌停、买一减10tick或保证金作本地附加门禁，最终由柜台判断。
- `account_permission_enabled`：Relay服务端现有账户配置的enabled/trading_enabled，且broker/gateway/account精确匹配。不是OC授予权限，也不是本次命令许可。
- `broker_accepting_submit`：当前所选路由心跳仍新鲜（5秒）、同TradingDay且连接/结算/恢复/接单状态满足诊断条件。是瞬时观察，不证明正式发送前置完整或有效订单快照。
- `can_submit=false`、`command_published=false`始终保持；`blockers`始终含`publication_not_connected`。其他值为`relay_account_read_only/broker_not_ready/price_off_tick/outside_current_test_scope`；后一项仅说明现有TEST发布器仍限SHFE买开一手，不推广为生产策略规则。

400为请求格式问题，404为未配置观察账户，405为非POST；503为环境/读取能力不可用、账户歧义或行情失败，行情错误保留`detail.stage`。预检不检查完整委托状态/幂等责任，不改变交易权限，不自动向股票配置添加期货账户。所有结果no-store；浏览器参数变化、晚到结果或行情有效期已过必须丢弃，不持有可复用授权。

SDK源码`preview_futures_order(..., price="3000.000")`只发送一次预检，拒绝float价格以避免精度丢失。终端新增“核对委托”，分别显示Relay权限/OC接单/价位和未开放原因；依然禁止期货提交和撤单。源码及离线测试完成后仍需部署，未发布SDK安装包。

API 模式统一返回 JSON envelope：

```json
{
  "ok": true,
  "data": {},
  "request_id": "relay-...",
  "time": "2026-06-13T18:00:00+08:00"
}
```

错误响应：

```json
{
  "ok": false,
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "qty must be positive"
  },
  "request_id": "relay-...",
  "time": "2026-06-13T18:00:00+08:00"
}
```

时间字段约定：

1. 业务展示时间统一按 `Asia/Shanghai` 输出，格式为 RFC3339/RFC3339Nano，例如 `2026-06-14T11:00:00+08:00`。
2. PostgreSQL `timestamptz` 仍记录绝对时刻，API 响应在序列化阶段转换为东八区。
3. 订单、成交、资金、持仓、订单事件、成交事件和任务运行记录中的零值时间会省略，不返回 `0001-01-01T00:00:00Z`。

## 枚举

| 名称 | 值 | 说明 |
| --- | --- | --- |
| `exchange` | `SH`、`SZ`、`BJ` | 上海、深圳、北京交易所 |
| `trade_side` | `B`、`S`、`P`、`R` | 买入、卖出、申购、赎回；当前 relay 二级市场下单只开放 `B/S` |
| `business_type` | `S`、`E` | `S` 表示二级市场证券买卖，股票和 ETF 二级市场买卖均使用 `S`；`E` 预留给 ETF 申购/赎回专项，当前 `/v1/orders` 返回 `NOT_IMPLEMENTED` |
| `offset_type` | `O`、`C` | 开仓、平仓；A 股股票通常为 `C` |
| `reply_status` | `accepted`、`partial`、`completed`、`rejected`、`failed` | 前置命令级回包状态 |
| `gateway_status` | `accepted`、`working`、`filled`、`cancelled`、`rejected` | 前置/柜台订单状态 |
| `order_status` | `created`、`accepted`、`working`、`partially_filled`、`filled`、`cancelled`、`rejected` | relay 标准订单状态 |
| `event_type` | `order.event`、`fill.event` | 订单事件、成交事件 |

终态集合：

```text
filled
cancelled
rejected
```

## 标准对象

### Account

账户对象描述 relay 内部可路由账户：

```json
{
  "account_id": "00030484",
  "broker_id": "huaxin",
  "gateway_id": "00030484",
  "stream_prefix": "relay:prod:v1:huaxin:00030484",
  "status": "enabled",
  "enabled": true,
  "trading_enabled": false,
  "simulated": false
}
```

### Asset

资金对象映射 `TiRspAccountInfo` 和前置 `asset_page`：

```json
{
  "account_id": "00030484",
  "cash_available": 50000000.0,
  "cash_total": 50000000.0,
  "net_asset": 50000000.0,
  "market_value": 0.0,
  "stock_value": 0.0,
  "fund_value": 0.0,
  "reverse_repo_receivable": 0.0,
  "day_profit": 0.0,
  "position_profit": 0.0,
  "close_profit": 0.0
}
```

OC 的原始 `asset_page` 可能只包含柜台可见资金。Relay 默认读模型和派生 `open/close` 快照会补足普通持仓市值；标准 `broker_close -> close` 结算还会按当日 `204001.SH` 普通成交账本计算逆回购本金应收，并使用 `net_asset = cash_total + market_value + reverse_repo_receivable`。应收只统计成交时间不晚于资金快照捕获时间的成交：更晚的成交发生时，旧快照现金尚未扣除对应本金，不能重复叠加。逆回购预估利息不提前进入资产，待实际回款后再通过资金账确认；`broker_close` 继续保留 OC 原始口径。

OC于2026-10-19说明，华鑫当前原始映射为 `cash_available=UsefulMoney`、`cash_total=UsefulMoney+FrozenCash+FrozenCommission+UnDeliveredFrozenCash+UnDeliveredFrozenCommission`、`net_asset=cash_total`、`market_value=0`。这只是现有资金投影，不证明普通/极速柜台全范围；FetchLimit不再叠加，Deposit/Withdraw也不直接归为产品外部资金。OC已报告P0修正多行末行覆盖、保留OwnerUnit并对含糊多行返回失败，但真实账户部署/在线范围尚待验证；Relay不在缺证据时补齐或汇总丢失范围。详见 [P0交付记录](OC_CASH_P0_DELIVERY_REVIEW_20261019.md)。

生产默认 `enrich=true` 的资产响应在收盘后可返回 `account_day_pnl`、`account_day_pnl_source`、`account_day_pnl_trade_date`。仅当同账户同日current非blocked的v2净值与当前资金快照的现金、市值及总资产逐项匹配时，`day_profit`和可空元数据 `account_day_pnl` 使用已发布账户日收益；0值由后者显式表达。无匹配证据、盘中或测试环境不应用，`enrich=false`保持原始口径，不回写账本。前端和当前日CSV优先使用该账户/日期匹配值，不再由柜台当前均价的平仓估算覆盖；持仓浮盈仍是独立指标，已发布值也不等于券商最终清算。见 [387核查与修复](ASSET_DAY_PNL_ALIGNMENT_20261019.md)。

`GET /v1/accounts/{account_id}/asset?trade_date=YYYYMMDD`（也接受 `YYYY-MM-DD`）直接读取该日 `asset_snapshots(close)`，返回 `asset`、`trade_date`、`snapshot_type=close`，不使用绩效曲线投影替代资产，不重估当前持仓或重加逆回购。没有close返回404，不回退当前账户资产；未知账户404、非法日期400。历史恢复的 `captured_at` 可能晚于业务日，日收益关联使用显式查询交易日，不拿导入时刻当交易日。不传日期保持既有当前资产行为。

生产收盘资产的默认补全另返回 `account_day_pnl_status` 和可选 `account_day_pnl_reason`：`available` 表示同范围匹配的已发布账户日收益，`pending` 表示暂无可用净值/依赖不可用，`blocked` 表示该日净值已阻断，`snapshot_mismatch` 表示净值与现金/市值/总资产不匹配。后三种不返回 `account_day_pnl` 数值，前端显示待核算/待复核/待核对，CSV金额留空，不把缺失当0，也不回退柜台成本或卖出差额估算。原始 `day_profit` 仅保留兼容诊断，不能在不可用状态下当账户收益消费。`enrich=false` 不增加这些展示标记；指定日期时仍是已落盘的派生close，而不是原始OC现金页。盘中及TEST不套用此收盘门禁。见 [收盘资金持仓修正](ASSET_CLOSE_DISPLAY_20261023.md)。

### Position

持仓对象映射 `TiRspQryPosition`，保留 A 股 T+1 可卖数量：

```json
{
  "account_id": "00030484",
  "symbol": "600000",
  "name": "浦发银行",
  "exchange": "SH",
  "quantity": 100,
  "sellable_qty": 100,
  "initial_qty": 100,
  "today_qty": 0,
  "avg_cost": 9.54,
  "total_cost": 954.0,
  "avg_cost_source": "broker_total_position_cost",
  "cost_complete": true,
  "market_value": 954.0,
  "unrealized_pnl": 0.0,
  "day_unrealized_pnl": 0.0,
  "shareholder_id": "A00030484"
}
```

`sellable_qty` 当前按前置/柜台 `position_page` 原样落盘和返回，relay 不在账本层重新计算 A 股 T+1 可卖数量。券商测试环境或外部前置如果返回同日买入可卖/不可卖差异，页面和 SDK 会如实展示；需要统一 T+1 规则时应优先在前置持仓回报中统一字段语义。

当前持仓账本只展示 `quantity > 0` 的 `positions` 行。`position_page` 被视为账户全量快照批次：同一 `origin_message_id/correlation_id` 下的 `partial` 回包逐条 upsert，`completed` 回包到达后清理该账户本次查询开始前仍未更新的旧当前持仓，避免旧批次残留标的被误认为仍持有。历史持仓查询仍读取 `position_snapshots`，不受当前表清理影响。

持仓盈亏统一保留两列：`unrealized_pnl` 是按买入成本计算的总持仓浮盈；`day_unrealized_pnl` 是当日持仓浮动贡献，老仓按今日开盘价作日内基准，当日买入按当日买入成交成本作基准。当前持仓查询会在账本字段缺失或行情更新时结合 Meridian level1 snapshot 和当日成交账本重算，历史持仓查询只读取 `position_snapshots` 已落盘字段。

OC 2026-08-03 版本为持仓增加 `total_cost`、`avg_cost_source` 和 `cost_complete`。Relay 优先使用 `cost_complete=true` 的柜台总成本作为可信起算成本；`avg_cost_source=unavailable` 时不得回退信任 `avg_cost`。旧 OC 的 `market_value` 实际承载 `TotalPosCost`，不是盯市市值，因此 Relay 不再把该字段直接写入标准市值；当前市值统一使用 Meridian 行情重估，只有 OC 同时提供可靠 `last_price` 时才可在同步层计算 `quantity * last_price`。

### SubmitOrderRequest

下单请求映射前置 `order.submit` payload 和 `TiReqOrderInsert`：

```json
{
  "account_id": "00030484",
  "client_order_id": "cli-0001",
  "gateway_order_id": "gw-cli-0001",
  "trade_date": "20260724",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "business_type": "S",
  "offset_type": "C",
  "price": 9.54,
  "qty": 100,
  "idempotency_key": "idem-submit-0001",
  "strategy_type": "stock_cross_section",
  "strategy_id": "alpha-basket-v1",
  "basket_id": "basket-20260724-001",
  "parent_order_id": "",
  "t0_order_group_id": ""
}
```

基础校验：

1. `account_id`、`symbol`、`exchange`、`trade_side`、`business_type` 必填。
2. `price` 必须大于 0。
3. `qty` 必须大于 0。
4. `gateway_order_id` 可由调用方传入；未传时 relay 会生成 `gw-*`。无论来源如何，它都是撤单和事件匹配的北向主键。
5. `trade_date` 可选；未传时 relay 按 `Asia/Shanghai` 当前日期写入本地草稿。查询/回包链路会优先使用 OC payload 中的 `trade_date`，否则从订单时间推导。
6. `strategy_type/strategy_id/basket_id/parent_order_id/t0_order_group_id` 可选，用于后续绩效归因，不影响下单校验和 OC 交易语义。

订单编号和幂等规则：

1. `gateway_order_id` 在 `account_id + trade_date` 内唯一；`000012` 已将订单、订单事件和成交外键统一到该业务键。OC 查询页与实时推送必须对同一订单使用同一 ID。
2. `client_order_id` 未传时默认等于 `gateway_order_id`；非空时在 `account_id` 内唯一。
3. `idempotency_key` 未传时，单笔下单默认 `order:{account_id}:{gateway_order_id}`。
4. 重复提交同一 `gateway_order_id + idempotency_key + payload` 会返回已有订单，`replayed=true`，不再写 Redis。
5. 同一 `gateway_order_id` 换幂等键，或同一幂等键换 `gateway_order_id/payload`，返回 `409` 冲突。
6. 批量下单的整批 `idempotency_key` 对整批 payload 生效，子订单仍各自拥有 `gateway_order_id` 和子订单幂等键。

### BatchSubmitOrderRequest

批量下单请求：

```json
{
  "account_id": "00030484",
  "orders": [
    {
      "account_id": "00030484",
      "gateway_order_id": "gw-cli-batch-0001-1",
      "symbol": "600000",
      "exchange": "SH",
      "trade_side": "B",
      "business_type": "S",
      "offset_type": "C",
      "price": 9.54,
      "qty": 100
    }
  ],
  "idempotency_key": "idem-batch-0001"
}
```

基础校验：

1. `orders` 至少包含一笔订单。
2. 子订单 `account_id` 必须与批量请求 `account_id` 一致。
3. 同一批内非空 `gateway_order_id` 不允许重复。

### CancelOrderRequest

撤单请求映射前置 `order.cancel` payload 和 `TiReqOrderDelete`：

```json
{
  "account_id": "00030484",
  "gateway_order_id": "gw-cli-0001",
  "cancel_id": "cancel-0001",
  "idempotency_key": "idem-cancel-0001"
}
```

注意：撤单 reply `accepted` 只表示撤单请求已提交，是否撤成必须等待 `order.event.gateway_status=cancelled`。

### Order

订单对象映射前置 `order.event.payload` 和 `TiRtnOrderStatus`：

```json
{
  "account_id": "00030484",
  "gateway_order_id": "gw-cli-0001",
  "order_id": 1680001,
  "order_stream_id": "110018100000001",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "business_type": "S",
  "limit_price": 9.54,
  "order_qty": 100,
  "cum_filled_qty": 0,
  "leaves_qty": 100,
  "status": "working",
  "gateway_status": "working",
  "is_terminal": false
}
```

### Fill

成交对象映射前置 `fill.event.payload` 和 `TiRtnOrderMatch`：

```json
{
  "fill_id": "match-stream-id",
  "account_id": "00030484",
  "gateway_order_id": "gw-cli-0001",
  "order_id": 1680001,
  "order_stream_id": "110018100000001",
  "business_type": "S",
  "symbol": "600000",
  "exchange": "SH",
  "trade_side": "B",
  "price": 9.54,
  "qty": 100,
  "fee": 0.0,
  "trade_date": "2026-07-24",
  "match_timestamp": 1784860259957,
  "matched_at": "2026-07-24T10:30:59.957+08:00",
  "strategy_type": "stock_cross_section",
  "strategy_id": "alpha-basket-v1",
  "basket_id": "basket-20260724-001"
}
```

成交去重优先级：

1. `account_id + trade_date + gateway_order_id + fill_id`
2. `order_stream_id + order_id + symbol + exchange + match_timestamp + qty + price`

`fill_id` 对应的柜台成交流号或 `adapter_context.match_stream_id` 只要求在订单作用域内稳定，不要求在账户当日或全历史范围内唯一。策略端如果自行做成交回调去重，也应把 `gateway_order_id` 纳入 key。

逐笔成交的业务时间统一使用 `matched_at`。它是带时区的 RFC3339 时间，也是成交回调、成交展示、TCA 和策略持久化的唯一首选时间字段。`match_timestamp` 保留为 OC 原始毫秒时间戳和幂等证据；订单的 `accepted_at`、`last_updated_at`、`terminal_at` 只描述订单生命周期，不能替代成交时间。一笔订单存在多笔成交时，每笔成交必须保留自己的 `matched_at`。

华鑫 7x24 测试柜台可能同时出现真实报单/成交时刻和回放行情对应的订单状态时钟；这是测试柜台语义，不外推到生产正式柜台，也不改变上述消费规则。

如果前置只推送订单累计成交量，而没有同步推送完整 `fill.event` 或 `fill_page`，relay 会在新订单事件入账时补一条汇总成交，保证订单账本和成交账本的数量口径向前一致。该补齐记录的 `fill_id` 形如 `relay-summary:<gateway_order_id>`，并在 `adapter_context` 中标记 `relay_synthesized=true`、`relay_synthesis_source=order.event/order_page`、`relay_synthesis_reason=order_filled_without_complete_fill_ledger`。这类记录不是柜台逐笔成交，策略端如果需要严格逐笔成交，可以按该标记过滤。

### ComponentTransfer

ETF 申赎成分证券划转、现金替代和 0 价记录使用独立 `ComponentTransfer` 账本。Relay 接收实时 `transfer.event` 和查询 `fill_page.component_transfers[]`，写入 `etf_component_transfers`，不会写入 `fills`。`component_value=null` 表示 OC 未提供可估值金额，不解释为 0；柜台原始方向保留在 `broker_trade_side/broker_business_type`。

OC v1.2 生成的 `gateway_order_id` 是不透明稳定标识。Relay 不从 `basket_id` 或 OC 内部 token 重建、截断或改写 ID；新订单的实时事件、查询回包和 OC 重启后查询都必须保留 Relay 原始 ID。只有归档重放旧 `etfarb#...` 消息时保留一次兼容修复。

### ETFSettlementFinalization

该对象是 Relay 绩效内部的版本化清算输入，不属于 OC wire schema，也不要求 Meridian 增加交易字段。它只在真实券商资金和 Meridian PCF 均齐备后，把来源日 ETF T0 从 IOPV+15bp 暂估升级为终值：

```json
{
  "account_id": "501000114077",
  "source_trade_date": "2026-08-24",
  "security_id": "159915.SZ",
  "status": "confirmed",
  "settlement_complete": true,
  "redemption_quantity": 3000000,
  "redemption_unit": 1000000,
  "buy_gross_amount": 10475000.0,
  "component_sale_gross_amount": 10425205.0,
  "actual_cash_component": 7913.79,
  "actual_cash_substitution": 0.0,
  "actual_total_fee": 7722.05,
  "source_close_settlement_carry": 7874.70,
  "gross_contribution": -41881.21,
  "net_contribution": -49603.26,
  "pcf_trade_date": "2026-08-25",
  "pcf_schema_version": "etf_cash_component.v1",
  "source": "broker_statement_one_time_audit"
}
```

约束：赎回数量必须是申赎单位整数倍；实际买入和成分卖出必须与 OC 账本闭合；PCF 只校验单位和现金差额，不生成虚构成分成交；`actual_cash_component` 可为负，其余现金替代和费用不得为负；confirmed 记录必须有完整确认审计。该对象按版本追加，原始 OC、Meridian 和券商证据不覆盖。日常生产事实仍来自 OC 当日数据，历史券商文件不形成常规导入接口。

### 撤单动作结果

`order.cancel` 的 `reply.status=accepted` 只表示撤单请求已交给柜台接口。OC 可以继续发布 `event_type=order.cancel.event`，其中 `event_name` 支持 `order.cancel.accepted` 和 `order.cancel.rejected`；响应超时则写入 `CANCEL_RESPONSE_TIMEOUT` DLQ。Relay 按原命令 `origin_message_id` 将 reply/event 幂等写入同一条 `order_cancel_attempts`，后到的柜台事件会覆盖 reply 的暂态 `reconciliation_required`；accepted 只补充动作证据，rejected 另外发布 `order.cancel.rejected` SSE，两者都不直接修改原订单的 `status/gateway_status/reject_code/reject_message`。成功撤单仍只以普通 `order.event.gateway_status=cancelled` 为准。

确定拒撤尊重显式 `reconciliation_required=false`，缺失该字段时保守要求对账。
`timeout/outcome_unknown` 强制 `reconciliation_required=true/retry_safe=false`，
不由迟到 accepted 或确定动作结果清除；原始失败审计与后续订单终态分别保留。
相同结果重消费不再产生数据库变更通知，迟到 accepted 不覆盖失败，普通 reply 不覆盖已取得的柜台 event。
SSE 在当前有界重放窗口内按账户、attempt_id、原订单和结果阶段去重；
不承诺跨进程 exactly-once，重启/gap/游标过期必须全分页对账。
撤单尝试 `raw_payload` 保留完整 OC payload，包括受控 TEST 的 `fault_plan_id/original_body`，
审计字段不影响准入或业务状态。

`GET /v1/command-status/{origin_message_id}` 对撤单附加可选 `cancel_attempts` 审计关联。
`state=accepted/success=true` 与其中 `status=timeout` 可同时存在，前者是命令受理，后者是动作结果未知。
SDK 0.1.41 可从 `CommandStatus.raw["cancel_attempts"]` 读取，不需要升级 SDK 或改变命令格式。

`GET /v1/order-cancel-attempts` 提供上述证据的账户级持久化分页读取，支持日期、原订单
`gateway_order_id`、状态和 cursor 过滤。`ORDER_NOT_FOUND` 不推导原订单终态。可选
`counter_session_id` 用于证明订单所属 OC 进程会话；它是不透明值，不能按自然日生成。TEST 中
一次 OC 进程生命周期对应一个会话：进程内短线重连保持 ID，人工重启后生成新 ID。OC 不需要
识别华鑫 7x24 的四个时段，Relay 也不根据时钟推导会话切换。字段缺失时 Relay 保持失败关闭；
生产正常 A 股准入和状态机不使用 TEST 时间表。

`COMMAND_OUTCOME_UNKNOWN` 表示 OC 重启时交易命令结果不可安全推断，Relay 不把草稿订单改成拒绝，必须先查询对账。`QUERY_INTERRUPTED` 可使用新 `message_id` 重试查询；若恢复竞态中同一 `origin_message_id` 随后收到 action/result type 匹配的合法查询末页，`GET /v1/command-status/{origin_message_id}` 返回 `state=completed`、`success=true`、`recovered=true`，原始中断与 DLQ 仍保留审计。批量下单 reply 的 `failed_orders[]` 按 `index/gateway_order_id` 逐笔回写对应失败子单，不把整个 batch 合并成一个虚拟订单。

## API 路由规划

| 方法 | 路径 | 请求 | 响应 | 当前状态 |
| --- | --- | --- | --- | --- |
| `GET` | `/healthz` | - | `StatusView` | 已有骨架 |
| `GET` | `/v1/status` | - | `StatusView` | 已实现，包含依赖健康、账户摘要、交易阶段、Meridian 交易日状态和最近日流程任务状态 |
| `GET` | `/v1/schema` | - | `CatalogDocument` | 已有骨架 |
| `GET` | `/v1/accounts` | - | `[]Account` | 已实现，配置账户列表并合并 PostgreSQL 账户别名 |
| `GET` | `/v1/accounts/{account_id}/readiness` | `force` query | `AccountReadiness` | 已实现，账户级 OC 心跳、柜台会话和下单准入状态 |
| `GET` | `/v1/account-routes` | - | `[]AccountRoute` | 已实现，展示账户路由、查询/交易权限、环境和 Redis stream key |
| `PATCH` | `/v1/accounts/{account_id}/alias` | `{alias}` | `Account` | 已实现，写入 PostgreSQL `accounts.account_name` |
| `GET` | `/v1/accounts/{account_id}/asset` | - | `Asset` | 已实现，读取 PostgreSQL 最新快照 |
| `POST` | `/v1/accounts/{account_id}/asset/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `GET` | `/v1/accounts/{account_id}/positions` | `PositionQuery` | `[]Position` | 已实现，默认读取 PostgreSQL 当前持仓 |
| `GET` | `/v1/accounts/{account_id}/positions/history` | `PositionQuery` | `[]Position` | 已实现，读取 `position_snapshots` 历史快照，默认 `snapshot_type=close`，可传 `snapshot_type=open` |
| `GET` | `/v1/accounts/{account_id}/positions/baseline` | `PositionBaselineQuery` | `PositionBaselinePage` | 历史 close 完整账户数量基线，代际绑定分页、独立资金/持仓查询完成证明、总数与 SHA256；不发布 OC 命令 |
| `GET` | `/v1/accounts/{account_id}/performance/daily` | `trade_date` query | `DailyPerformance` | 已实现，读取日初 open、日终 close、持仓快照和成交汇总 |
| `GET` | `/v1/accounts/{account_id}/performance/contributions` | `trade_date` query | `ContributionResult` | 已实现，只读聚合证券/策略贡献、费用、贡献 bp 和质量标记；空日期取 Meridian 当前或最近交易日 |
| `GET` | `/v1/accounts/{account_id}/performance/default-range` | `reference_date` query | `PerformanceDefaultRange` | 已实现，按账户选择不晚于参考交易日的最近权威绩效日，并返回向前一个自然月的默认区间 |
| `GET` | `/v1/accounts/{account_id}/performance/series` | `date_from/date_to/benchmark_security_id` query | `PerformanceSeries` | 已实现，读取 close 资产快照生成净值序列，同时返回 open-to-close 日内绩效字段，并可用 Meridian bars 增加基准和超额收益 |
| `GET` | `/v1/accounts/{account_id}/performance/series.csv` | `date_from/date_to/benchmark_security_id` query | `text/csv` | 已实现，导出账户绩效、基准和超额收益 CSV |
| `GET` | `/v1/performance/settings` | - | `PerformanceSettings` | 已实现，返回经济净值公式版本、对账阈值和人工输入写入开关 |
| `GET` | `/v1/performance/fee-rules` | `account_id/status/effective_on/limit` query | `{rules,count,is_complete,query}` | 已实现，读取账户级、生效区间版本化费率规则；上限 500，真实额外一行判定完整性 |
| `POST` | `/v1/performance/fee-rules` | `FeeRule` | `FeeRule` | 已实现，新增费率规则；默认因 `performance.settings_write_enabled=false` 返回 `403` |
| `GET` | `/v1/accounts/{account_id}/cash-ledger` | `trade_date/date_from/date_to/flow_class/status/limit` query | `[]CashLedgerEntry` | 已实现，读取人工资金流水、外部入出金和柜台间划转 |
| `POST` | `/v1/accounts/{account_id}/cash-ledger` | `CashLedgerEntry` | `CashLedgerEntry` | 已实现，新增手工资金流水；默认写入关闭 |
| `POST` | `/v1/accounts/{account_id}/cash-ledger/{entry_id}/confirm` | `{operator}` | `CashLedgerEntry` | 已实现，在账户维度确认 draft 流水 |
| `POST` | `/v1/accounts/{account_id}/cash-ledger/{entry_id}/void` | `{operator}` | `CashLedgerEntry` | 已实现，在账户维度作废 draft/confirmed 流水 |
| `GET` | `/v1/accounts/{account_id}/performance/baselines` | - | `[]NavBaseline` | 已实现，读取手工维护的日初经济净值基线 |
| `POST` | `/v1/accounts/{account_id}/performance/baselines` | `NavBaseline` | `NavBaseline` | 已实现，新增日初经济净值基线；默认写入关闭 |
| `GET` | `/v1/accounts/{account_id}/performance/reverse-repo` | `trade_date` query | `ReverseRepoResult` | 已实现，从成交账本估算 `204001.SH` 逆回购本金、占款天数、毛息、费用和净息，不落库 |
| `POST` | `/v1/accounts/{account_id}/performance/reverse-repo/rebuild` | `trade_date` query | `ReverseRepoResult` | 已实现，重建并落库逆回购应计结果；默认写入关闭 |
| `GET` | `/v1/accounts/{account_id}/performance/reverse-repo/accruals` | `trade_date` query | `[]ReverseRepoAccrual` | 已实现，查询已落库逆回购应计结果 |
| `GET` | `/v1/accounts/{account_id}/performance/economic-nav/preview` | `trade_date/status` query | `EconomicNAVResult` | 已实现，按当前账本试算经济净值，不写库 |
| `POST` | `/v1/accounts/{account_id}/performance/economic-nav/rebuild` | `{trade_date,status}` | `EconomicNAVResult` | 已实现，重建并落库当前 economic NAV 版本和对账记录；默认写入关闭 |
| `GET` | `/v1/accounts/{account_id}/performance/economic-nav` | `trade_date` 或 `date_from/date_to` query | `[]PerformanceNAV` | 已实现，查询当前版本化经济净值结果 |
| `GET` | `/v1/accounts/{account_id}/performance/cycle-reviews` | 必填`date_from/date_to`，可选`version_id` | 独立版本元数据及`cycles` | 已实现，按申赎源日筛选，不改账户净值、不代表最终清算 |
| `GET` | `/v1/accounts/{account_id}/performance/nav-reconciliations` | `trade_date` 或 `date_from/date_to` query | `[]NAVReconciliation` | 已实现，查询经济净值对账结果 |
| `POST` | `/v1/accounts/{account_id}/positions/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/accounts/{account_id}/orders/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/accounts/{account_id}/fills/refresh` | - | `RefreshQueryResult` | 已实现，返回 `202 Accepted` |
| `GET` | `/v1/command-status/{origin_message_id}` | - | `CommandStatus` | 已实现，从归档 reply 判断查询或交易命令状态 |
| `GET` | `/v1/order-cancel-attempts` | `OrderCancelAttemptQuery` | `OrderCancelAttemptPage` | 已实现，持久化撤单尝试证据与 cursor 分页 |
| `POST` | `/v1/orders` | `SubmitOrderRequest` | `Order` | 已实现，返回 `202 Accepted` |
| `POST` | `/v1/orders/batch` | `BatchSubmitOrderRequest` | `BatchSubmitOrderResult` | 已实现，返回 `202 Accepted`，逐单保留调用方 ID 和 Relay 接受状态 |
| `POST` | `/v1/orders/{gateway_order_id}/cancel` | `CancelOrderRequest` | `Order` | 已实现，返回 `202 Accepted` |
| `GET` | `/v1/orders` | `OrderQuery` | `[]Order` | 已实现，默认按 `Asia/Shanghai` 当日读取 PostgreSQL 账本；`origin_message_id` 可选择同一批次的全部子单 |
| `GET` | `/v1/fills` | `FillQuery` | `[]Fill` | 已实现，默认按 `Asia/Shanghai` 当日读取 PostgreSQL 账本 |
| `GET` | `/v1/transfers` | `ComponentTransferQuery` | `[]ComponentTransfer` | 已实现，默认按 `Asia/Shanghai` 当日读取 ETF 成分股划转账本 |
| `GET` | `/v1/history/orders` | `OrderQuery` | `[]Order` | 已实现，显式历史订单查询 |
| `GET` | `/v1/history/fills` | `FillQuery` | `[]Fill` | 已实现，显式历史成交查询 |
| `GET` | `/v1/history/transfers` | `ComponentTransferQuery` | `[]ComponentTransfer` | 已实现，显式历史 ETF 成分股划转查询 |
| `GET` | `/v1/events/stream` | `account_id`，可选 `Last-Event-ID` header 或 `last_event_id` query | `SSE Event` | 已实现，支持订单、成交、资金和持仓变化、有限回放及显式 gap |
| `GET` | `/v1/meridian/metadata/instruments` | Meridian query | `metadata_instrument.v2` | 已实现，透明返回沪深证券主数据和权威 `price_tick/price_decimals`；北交所为未来能力 |
| `GET` | `/v1/meridian/metadata/status` | - | `metadata_status.v2` | 已实现，透明返回证券主数据和价位覆盖质量 |
| `GET` | `/v1/meridian/market/bars` | Meridian query | `market_bar.v1` | 已实现，同源薄代理，保留 Meridian 原始字段 |
| `GET` | `/v1/meridian/stream/market/bars` | Meridian query | Meridian SSE | 已实现，同源 SSE 薄代理，默认 `frequency=1m`、`data_scope=realtime` |
| `GET` | `/v1/meridian/market/etf-components` | Meridian query | Meridian payload | 已实现，ETF PCF 成分清单薄代理 |
| `GET` | `/v1/meridian/market/etf-cash-components` | Meridian query | `etf_cash_component.v1` | 已实现，ETF PCF 现金清单及最小申赎单位薄代理 |
| `GET` | `/v1/meridian/market/etf-pcf-status` | - | Meridian payload | 已实现，PCF 同步状态薄代理 |
| `GET` | `/v1/meridian/metadata/adjust-factors` | Meridian query | Meridian payload | 已实现，同源薄代理，保留 Meridian 原始字段 |
| `GET` | `/v1/jobs/runs` | `job_name` query | `[]JobRun` | 已实现，查询最近任务运行 |
| `POST` | `/v1/jobs/runs` | `JobRunRequest` | `JobRun` | 已实现，日流程任务报告落盘 |
| `POST` | `/v1/settlements/snapshots` | `SettlementSnapshotRequest` | `SettlementSnapshotResult` | 已实现，盘前 open 资产快照、收盘 close 资产/持仓快照和 reconciliation run 落盘 |

### 费率预算范围

`GET /v1/performance/fee-rules` 自 `2026-10-11` 返回四个启用生产账户各 8 条 active 规则，明确匹配 `SH/SZ × stock/etf × B/S`、`business_type=S`。这是用户确认共享的历史样本预算，不是券商结算合同；`raw_payload.authority=sample_verified_budget`、`not_broker_settlement_rule=true` 保留样本覆盖、确认日、发布摘要、订单粒度及未知边界。股票最低佣金 0 仅为预算假设，不能解释成正式免最低收费。实际费用始终优先，ETF 申赎/逆回购/期货不套用，历史日无新规则也不回退未来参数。SDK 0.1.42 `get_fee_rules` 保留这些证据，详见 [费率发布响应](CHRONOS_FEE_RULE_PUBLICATION_RESPONSE_20261011.md)。生产 POST 和交易权限保持关闭。

## Redis Stream 映射

HTTP API 不直接暴露前置 Redis envelope，但后端会映射到以下 action：

| HTTP API | Redis action | Stream |
| --- | --- | --- |
| `POST /v1/orders` | `order.submit` | `cmd.trade` |
| `POST /v1/orders/batch` | `order.batch.submit` | `cmd.trade` |
| `POST /v1/orders/{gateway_order_id}/cancel` | `order.cancel` | `cmd.trade` |
| `POST /v1/accounts/{account_id}/asset/refresh` | `account.asset.query` | `cmd.query` |
| `POST /v1/accounts/{account_id}/positions/refresh` | `account.positions.query` | `cmd.query` |
| `POST /v1/accounts/{account_id}/orders/refresh` | `order.list.query` | `cmd.query` |
| `POST /v1/accounts/{account_id}/fills/refresh` | `fill.list.query` | `cmd.query` |

`POST /v1/orders` 的 `202 Accepted` 仅表示 relay 已接受请求、写入订单草稿并向 Redis `cmd.trade` 写入 `order.submit`，不表示交易所接单或成交。最终状态以 `order.event` 和 `fill.event` 回流为准。

若同一 `account_id + gateway_order_id` 和同一 `idempotency_key` 的请求与原始下单核心字段一致，relay 不会再次写 Redis，而是返回已有订单并标记 `replayed=true`，HTTP 状态为 `200 OK`。核心字段包含 `trade_date` 和策略归因字段；同一订单号重放但策略标签不同会返回 `409 IDEMPOTENCY_CONFLICT`。若同一 `gateway_order_id` 使用不同幂等键，返回 `409 CONFLICT`；若同一 `idempotency_key` 指向不同订单或不同 payload，返回 `409 IDEMPOTENCY_CONFLICT`。该语义由应用预检和 PostgreSQL insert-only 原子占位共同保证，并发请求不能同时发布同一笔订单命令。

涨跌停等柜台规则当前以异步回报为准。relay 同步层只做 schema、账户路由、重复订单和已知 unsupported 交易类型校验；超涨跌停价格可能先返回 `202 Accepted`，随后通过订单账本/SSE 进入 `rejected`。策略端必须订阅订单状态或轮询账本判断最终结果。若需要同步涨跌停预校验，应以后续接入 Meridian 涨跌停/交易规则数据后单独实现。

拒绝/失败的下单 reply 会被归档到 `raw_stream_messages`，同时回写对应草稿订单为 `rejected`。同步层会从 reply 顶层 `code/message`、payload 的 `reject_code/reject_message/error_* /message` 和 `adapter_context.error_text/broker_status_text` 等字段抽取柜台错误，写入订单的 `reject_code`、`reject_message`，并在 `adapter_context.relay_error_code`、`adapter_context.relay_error_message` 保留归一化后的排错信息。`BROKER_NOT_READY` 和 `COMMAND_OUTCOME_UNKNOWN` 是例外：前者表示券商柜台尚未 ready，后者表示 OC 重启时命令结果不可安全推断；Relay 只归档并提示重试或先查询对账，不把订单账本改成 `rejected`。撤单 reply/event/DLQ 无论失败、超时或结果未知都只写 `order_cancel_attempts`。`/trade` 订单监控表展示订单摘要，raw archive 保留完整上下文。

ETF 二级市场买卖按普通证券二级市场订单提交，使用 `business_type=S`、`trade_side=B/S`，价格精度按 Meridian `instrument_type=etf` 保留 3 位。ETF 申购/赎回不是普通买卖参数，涉及最小申赎单位、申赎清单等数据，当前 relay `/v1/orders` 未实现，`business_type=E` 会返回 `NOT_IMPLEMENTED`。

`POST /v1/orders/{gateway_order_id}/cancel` 会先读取 PostgreSQL 订单账本，只有非终态且 `leaves_qty > 0` 的订单才会写入 Redis `order.cancel`。撤单 `202 Accepted` 只表示撤单请求已提交到前置，是否撤成仍以 `order.event.gateway_status=cancelled` 为准。

`POST /v1/orders/batch` 会为每笔子订单写入本地草稿，再向 Redis `cmd.trade` 写入一条 `order.batch.submit` command。批量请求的 `202 Accepted` 不表示交易所接单或成交，最终仍以回流事件为准。

当前 `GET /v1/accounts/{account_id}/asset`、`GET /v1/accounts/{account_id}/positions`、`GET /v1/orders`、`GET /v1/fills` 和 `GET /v1/transfers` 是本地账本查询，不主动查询柜台。对应的 `POST .../refresh` 接口会向前置发送 `account.asset.query`、`account.positions.query`、`order.list.query` 或 `fill.list.query`，由 9092 同步循环把 `asset_page/position_page/order_page/fill_page` 合并回 PostgreSQL；同一 `fill_page` 中的普通成交和 `component_transfers[]` 会分别入账。

资金和持仓读取默认执行展示层补全；内部券商快照流程使用 `GET .../asset?enrich=false` 与 `GET .../positions?enrich=false` 直接读取 PostgreSQL 中的柜台原始字段，不请求证券名称、行情、成交成本或 Meridian。该参数只关闭读时补全，不改变账本内容；省略时保持原有终端和 SDK 行为。

资产读取通过 `/v1/schema` 能力 `assets.readiness_errors.v1` 声明可分类的失败语义：账户路由不存在返回 `404 NOT_FOUND`；账户已配置但当前环境尚未形成任何资产快照返回 `503 ASSET_NOT_READY`，调用方可在确认 OC ready 后执行 `POST .../asset/refresh` 并有限重试；PostgreSQL 查询或 schema 故障返回 `500 INTERNAL`，不得按资产尚未就绪自动重试。测试与生产使用独立数据库，资产读取不会跨环境回退。

`GET /v1/accounts/{account_id}/readiness?force=true` 通过能力 `accounts.order_entry_readiness.v1` 暴露 `order_entry_ready`、`broker_session_state`、`order_entry_block_reason`、`observed_at` 和 `next_transition_at`，同时保留 OC 的 `broker_ready/order_snapshot_ready/accepting_trade_commands`。当前华鑫 7x24 时段只配置在测试账户：每日 `01:15-01:25`、`01:30-03:30`、`03:40-05:40`、`07:15-07:25`、`07:30-09:30`、`09:40-11:40`、`13:15-13:25`、`13:30-15:30`、`15:40-17:40`、`19:15-19:25`、`19:30-21:30`、`21:40-23:40` 为可报单窗口，起点包含、终点不包含；休市和结算阶段返回 `OUTSIDE_TEST_COUNTER_WINDOW`。该测试配置忽略 Meridian 交易日历，生产环境禁止配置并继续使用正常 A 股交易时段逻辑。准入为 false 时 SDK `verify_ready()` 失败关闭；同步 `accepted` 仍只表示命令已接收，最终订单结果继续以账本和 SSE 为准。

柜台动态报单能力只由新鲜 OC heartbeat 表达。`accepting_trade_commands=false` 时，Relay 返回 `order_entry_ready=false` 和通用原因 `OC_TRADE_COMMANDS_PAUSED`；Relay 不扫描历史拒单推断柜台状态，也不通过固定冷却时间或探测订单推断恢复。华鑫 7x24 TEST 柜台的精确全局状态拒绝由 OC 在当前进程内锁存，恢复需重启 OC、生成新 `counter_session_id` 并发布新的可报单心跳。生产环境仍使用正常 A 股交易时段和生产 OC 心跳规则。

回执中的 `message_id` 是命令状态关联键。`GET /v1/command-status/{origin_message_id}` 对查询命令要求只有一个终态，成功终态必须同时满足 `status=completed`、与 action 匹配的 `result_type` 和 `chunk.is_last=true`；对 `order.submit/order.batch.submit/order.cancel`，一个 `status=accepted` 且 `result_type=order_action_receipt` 的 reply 表示命令接收已完成，返回 `state=accepted, terminal=true, success=true`，但不代表订单已成交或撤单成功。`failed/rejected` 返回 `state=failed`，缺少 final、结果类型不匹配或多个终态返回 `pending/invalid`。交易命令使用同一路由读取 `BROKER_NOT_READY`、`COMMAND_OUTCOME_UNKNOWN` 等归档回报，最终订单状态仍以订单账本为准。盘前初始化和盘后结算同时检查本地账本新鲜度与查询终态，不能用新鲜时间戳掩盖 OC 查询失败。

批量下单的同步 HTTP 成功只表示 Relay 已逐单校验、建立草稿并发布一个批量命令。调用方以 `message_id` 查询命令回报，并用 `GET /v1/history/orders?account_id=...&origin_message_id=...` 取得完整子单账本。首次下单写入的 `origin_message_id/request_id/idempotency_key` 是订单命令身份，后续查询回报和状态事件只更新业务状态，不覆盖这三个字段。子单仍为 `created` 时不得推断柜台接受；订单进入 `accepted/working/partially_filled/filled/cancelled` 才视为 accepted，`rejected` 保留逐单错误，`BROKER_NOT_READY` 和 `COMMAND_OUTCOME_UNKNOWN` 保持显式非确定状态。

`GET /v1/orders` 和 `GET /v1/fills` 不传 `trade_date/date_from/date_to/history` 时，默认按 `Asia/Shanghai` 当日过滤。历史订单和成交应使用 `/v1/history/orders`、`/v1/history/fills`，或在原查询接口显式传 `history=true`、`trade_date=YYYYMMDD`、`date_from=YYYYMMDD`、`date_to=YYYYMMDD`。订单查询优先使用 `orders.trade_date` 过滤，缺失时按东八区订单时间兜底；成交查询优先使用 `fills.trade_date`，缺失时按成交时间兜底。订单和成交查询都支持 `strategy_type`、`strategy_id`、`basket_id`、`parent_order_id`、`t0_order_group_id` 过滤。历史持仓使用 `/v1/accounts/{account_id}/positions/history`，数据来源为 `position_snapshots`；默认读取 `snapshot_type=close` 的日终持仓，可传 `snapshot_type=open` 读取盘前初始化固化的日初持仓。

成交入账时会按同一 `account_id + trade_date + gateway_order_id` 从订单账本继承缺失的 `business_type`、`strategy_type`、`strategy_id`、`basket_id`、`parent_order_id` 和 `t0_order_group_id`。OC 不需要重复维护这些由 Relay 下单请求持久化的归属字段；原始成交报文保持不变，继承只作用于结构化成交账本。

订单、成交、ETF 划转、当前持仓和历史持仓查询均支持 `limit` + `cursor` 翻页。第一版 cursor 采用 offset 语义，响应中如果存在 `next_cursor`，客户端可在下一次查询带上该值继续向后读取；如果 `next_cursor` 为空，表示当前条件已到末页。`/trade` 页面默认使用每页 50 条，通过 `next_cursor` 做服务端分页。

完整历史账户数量基线使用独立 `/v1/accounts/{account_id}/positions/baseline` 契约和 `positions.close_baseline.v1` capability。显式指定 `trade_date`，仅支持 `close`；返回 `complete/partial/unavailable`，只有完整基线允许 `missing_security_means_zero=true`。逐页固定内容寻址 `baseline_id`、总数和摘要，重建后旧 ID/游标返回 409，不混合新旧行；GET 仅读归档，不刷新 OC 或回退当前持仓。单页 1..500、全基线上限 100000，超限明确失败。`page_complete` 仍不是全账户完成证明，历史可卖量也不等于当前交易授权。全部元数据、八项完成证明、摘要和 SDK 0.1.43 见 [P0 回执](CHRONOS_POSITION_BASELINE_RESPONSE_20261013.md)。

`GET /v1/events/stream` 的账本事件使用 API Hub 生成的进程 epoch 单调游标，游标是 opaque string，客户端不得解析或自行构造。初次连接先收到无 SSE ID 的 `relay.connected`，其 data 包含 `resume_status=fresh`、`current_cursor`、`replay_capacity=2048` 和 `reconciliation_required=false`。恢复连接携带 `Last-Event-ID` 后，同一 API 进程且游标仍在窗口内时，服务端先返回 `resume_status=resumed`，再按原顺序发送游标之后且符合账户过滤的事件。

API 进程重启、游标无效或超前、回放窗口过期、订阅者缓冲区溢出以及 PostgreSQL 事件桥断线重连都会产生 `relay.gap`，并设置 `reconciliation_required=true`。恢复连接即使能够回放也会要求客户端先读取当前完整订单、成交、资金和持仓，因为 SSE 只承担实时唤醒，不是权威账本。15 秒 heartbeat 不推进游标；SDK 默认 30 秒 idle timeout。回放只覆盖当前 API 进程最近 2,048 个事件，PostgreSQL `NOTIFY` 本身不被描述为持久事件存储。

OC 无法确定单笔委托身份或发现普通成交与订单证券、交易所、方向、业务类型不一致时，会写入 `dlq` 且 `action=adapter.data_quality`。Relay 9092 进程消费并归档 DLQ，独立统计 `dead_letters/data_quality_dead_letters`，不会把问题记录落入普通订单或成交账本。

`GET /v1/accounts/{account_id}/performance/daily?trade_date=YYYYMMDD` 返回账户日终权益和 PnL 输入汇总。该接口以指定交易日 `asset_snapshots(snapshot_type=close)` 为主记录，读取上一条 close 净资产保留兼容字段 `daily_pnl`、`return_rate` 和 `asset_change`，同时读取当日 `asset_snapshots(snapshot_type=open)` 生成 `open_net_asset`、`overnight_adjustment=open_net_asset-previous_close_net_asset`、`intraday_pnl=close_net_asset-open_net_asset`、`intraday_return=intraday_pnl/open_net_asset`、`open_snapshot_source` 和 `quality_flags`。如果缺少 open 快照，会用上一 close 兜底并标记 `missing_open_asset/open_asset_fallback`。接口还汇总同日 `position_snapshots(snapshot_type=close)` 的持仓市值、总持仓浮盈、当日持仓浮动盈亏以及 `fills` 的买入金额、卖出金额、成交额和费用。研究侧派生口径为 `realized_pnl=settled_profit`、`gross_pnl=realized_pnl+day_unrealized_pnl`、`net_pnl=gross_pnl-fee_total`。接口只读取本地账本，不主动查询柜台；如果目标日尚未写入 close 资产快照，会返回 `404 NOT_FOUND`。

close 结算返回 `day_end_expired_orders/day_end_expired_order_ids`。该集合只包含 15:00 后仍由柜台查询为 `business_type=S + working/queued` 且有剩余数量的 A 股当日委托；订单账本原始状态不覆盖，质量统计按等效 cancelled 处理。其它非终态、特殊 ETF 业务或非 queued 状态仍保留 `non_terminal_order` 告警。

`GET /v1/accounts/{account_id}/performance/contributions?trade_date=YYYYMMDD` 返回 `ContributionResult`。普通股票和 ETF 截面使用 `close_value + sell_amount - buy_amount - open_value - effective_fee`；期初价格取 Meridian 当日 `pre_close`，期末价格取 Meridian 当日 `close`，缺失时才回退券商 open/close 持仓快照并标记质量缺口。费用优先使用 `order_fee_records` 中 `fee_complete=true && association_complete=true` 的 OC 订单实际费用，同一订单无论有多少成交只扣一次；其次才使用可信成交费用或账户生效费率，缺少规则时标记 `missing_fee_rule`。

`GET /v1/accounts/{account_id}/performance/cost-ledger/preview?trade_date=YYYYMMDD` 只读试算 `performance_position_cost.v3.2`，`POST .../cost-ledger/rebuild` 在开启绩效写保护后保存结果。非起算日以上一 close 成本状态承接总成本，以当日券商 open 持仓作为物理数量，并查询 Meridian `adjust-factors`：数量比匹配 `ex_factor` 时保存 `corporate_action_type=quantity_adjustment`，总成本不变、单位成本随数量调整；因子存在但数量不变时保存 `price_adjustment`；无法闭合时保存 `mismatch` 并阻断。账户起算配置显式使用 `cost_source=meridian_pre_close_mark_to_market` 时，只在已确认起算日以券商盘前持仓数量乘 Meridian 当日未复权 `pre_close` 建立 CORE 初始成本；缺少任一证券前收盘价即阻断，绝不回退使用柜台平均成本。普通持仓使用 `cost_bucket=CORE`；完整 ETF T0 买入/赎回组使用 `ETF_T0:{group_id}`，买入成本与底仓隔离且赎回后日内归零。只有与 OC 实际 `transfer.event` 按篮子、证券、时间和数量闭合的成分卖出才会在移动成本前剥离；PCF 不用于合成跨市场划转。赎回名义价格不进入成本账，IOPV 退出估值与 15bp 摩擦仍由 contributions 返回。

`POST /v1/accounts/{account_id}/fees/refresh` 发布 OC `fee.list.query`，只支持 OC 当前柜台交易日；`GET /v1/accounts/{account_id}/fees` 查询已落库的订单费用，支持 `trade_date/date_from/date_to/gateway_order_id/fee_complete/limit/cursor`。`fee_page.account_total_fee` 仅用于账户级核对，不参与逐订单累计；费用记录由 `account_id + fee_record_id` 幂等更新，只有完整且关联成功的订单费用可进入绩效。历史费用不由该刷新接口回补。

`fee_complete/association_complete` 与绩效 `fee_coverage_complete` 描述柜台费用快照的消费/覆盖条件，不是券商最终清算证明。`fee_as_of` 不等同于 `settled_at`；`actual_fee` 表示观测到的费用而非承诺最终交割金额。`adapter_context.component_fee_total` 是该条记录六个分项之和，不是ETF所有成分委托的费用汇总，不能与total_fee重复相加。最终清算与NAV人工确认仍需相应的独立证据，不能只因上述布尔值为true就自动推进终值状态。

费用缺失不等于订单不可用。订单和成交核心字段及关联完整时，Relay 仍使用它计算数量桥、移动成本、成交额和毛收益；费用完整性按当日有成交的唯一 `gateway_order_id` 单独统计。覆盖不完整时，贡献摘要返回 `fee_required_orders/fee_covered_orders/fee_coverage_complete/fee_coverage_source`，并将净绩效标记为 provisional、等待券商交割单，不把订单标记为数据质量失败。

ETF 申赎 T0 单独返回 `strategy_type=etf_redemption_t0`。同一赎回订单的多条成交先按 `gateway_order_id` 聚合；历史买单只有在赎回前的目标委托量精确闭合赎回量时才组成推断 T0 订单组，其实际成交额作为买入成本。估算退出价值使用每条赎回成交时刻之前最近的 Meridian historical Level1 `iopv * qty`，再按服务端 `performance.etf_t0_friction_rate` 扣减综合摩擦成本。无法闭合买单、缺 IOPV 或缺价格时不返回伪精确盈亏，而是标记 `missing/estimated`。赎回成分卖出必须命中实际 transfer 数量链路后才归为 `etf_component_transfer` 并从 T0 估算收益中排除；响应通过 `linked_component_sales`、`etf_settlement_estimate` 和对应质量标记展示已实现成分现金与估算在途清算资产。逆回购本金不进入普通卖出额，只有按实际占款天数计算的净息进入 `cash_management`。

`GET /v1/accounts/{account_id}/performance/trade-quality` 是只读交易质量接口，支持单日 `trade_date` 或区间 `date_from/date_to`。接口完整扫描本地订单、成交和订单费用账本，不触发 OC 查询；成交先按 `account_id + trade_date + gateway_order_id + fill_id` 去重，并在存在真实成交时排除同订单的 `relay-summary:*` 汇总成交。`trade_quality.v7` 的费用按 `trade_date + gateway_order_id` 精确关联：完整订单费用存在时覆盖该订单成交行费用且只计一次，否则回退成交费用，跨日复用订单号不会串账；15:00 后仍为普通二级市场 `working/queued` 的当日有效委托计入 `day_end_expired_orders` 和等效撤单，不计为未终态异常。普通二级市场订单以 fill 账本作为执行数量证据；`P/R + business_type=E` 的 ETF 申购赎回以订单终态 `cum_filled_qty` 和独立 transfer 账本表达，历史 OC 写入普通 fill 表的申赎本体、现金差额和旧 summary 记录也会从普通成交统计中排除，不再误报数量、证券或业务类型错配。申赎订单仍纳入有成交订单率与数量成交率。区间容量独立支持最多 60,000 条订单和 60,000 条成交，不再受证券贡献 20,000 行上限影响。`summary` 返回有实际成交订单率、完全成交率、按委托数量计算的成交率、撤单率、拒单率、拒单有原因/缺原因数量、未终态订单、日终自动失效订单、异常订单、孤立成交、成交额和费用。业务拒单是有效交易结果：终态为 `rejected` 且有 `reject_message` 或 OC 柜台原因时只计入拒单统计，不计入数据质量异常；有完整原因、零成交且 `invalid_qty` 不超过委托量的整笔无效申报同样属于拒单结果。缺少拒绝原因时才返回 `rejected_order_missing_reason`。柜台原因只从标准拒单字段及明确的 adapter 错误字段提取，`relay_*` 迁移、幂等清理等内部审计子树中的 `reason/message` 不得冒充柜台原因。`cancelled/rejected` 的正 `leaves_qty` 只作为历史终态残量质量指标，绝不恢复为可执行数量。`anomalies` 继续返回未终态、订单/成交数量不一致、成交证券/方向/业务类型错配、终态时间缺失或跨日、终态冲突、非拒单柜台错误残留和成交缺委托等可追溯明细。OC 委托时间与 Redis 事件时间存在精度差时，`terminal_before_created` 使用 5 秒容差。`gateway_order_id` 只按账户内交易日唯一处理，区间统计不会把跨交易日复用的同 ID 错误关联。

`GET /v1/accounts/{account_id}/performance/series?date_from=YYYYMMDD&date_to=YYYYMMDD&benchmark_security_id=000001.SH` 返回账户 close 净值绩效序列，并在每个交易日返回同样的日初资产、隔夜调整、日内盈亏和日内收益率字段。服务读取区间内 `asset_snapshots(snapshot_type=close)` 形成长期净值主线，按上一条 close 净资产计算兼容的单日收益，并在响应层计算 `cumulative_return`、`drawdown`、`summary.total_return` 和 `summary.max_drawdown`。绩效页面和 API Console 默认用上证指数 `000001.SH` 作为基准；如果传入其他 `benchmark_security_id`，relay 会按绩效序列中的交易日逐日读取 Meridian `bars` 的 14:55-15:00 窗口最后一条 1m close，生成 `benchmark_return`、`benchmark_cumulative_return`、`benchmark_drawdown`、`excess_return` 和 `excess_cumulative_return`，并在 `summary` 中返回基准区间收益、基准最大回撤和超额收益。该接口不主动查询柜台。

`GET /v1/accounts/{account_id}/performance/default-range?reference_date=YYYYMMDD` 为绩效页面解析默认窗口。服务从不晚于参考日的账户 current NAV 中倒序选择锚点：`performance_economic_nav.v2.x` 必须使用 `meridian_1d_pre_close_and_close`、不带 Level1 fallback 标记，并且对应交易日的 `performance_canonical` 已成功完成；人工 `finalized` NAV 和 `broker_statement_nav.v1` 也可作为权威锚点。返回的 `date_to` 是该锚点，`date_from` 为向前一个自然月并按月末夹紧后的日期。当前交易日只有 15:01 临时净值、权威日线尚未复算或账户当日 NAV blocked 时，接口返回更早的最近权威日及 `fallback_applied=true`；完全没有权威绩效时返回 `available=false`，不会触发当日只读 preview 或制造页面阻断。

`GET /v1/accounts/{account_id}/performance/series.csv?date_from=YYYYMMDD&date_to=YYYYMMDD&benchmark_security_id=000001.SH` 复用同一绩效序列口径，返回 CSV 文件，便于研究侧脚本、表格工具或验收脚本直接下载。CSV 当前包含账户、交易日、净资产、上一 close 净资产、日初资产、隔夜调整、日内盈亏、日内收益率、close-to-close 收益、累计收益、回撤、基准标的、基准 close、基准收益、基准回撤、超额收益、已实现 PnL、总持仓浮盈、当日持仓浮盈、总/净 PnL、成交额、费用和快照时间等列。

`GET /v1/accounts/{account_id}/performance/cycle-reviews?date_from=YYYYMMDD&date_to=YYYYMMDD`读取独立核对版本，也接受ISO日期。日期按赎回源日筛选而非退款记账日；可选`version_id`精确读取旧版，不存在时404，不回落最新版。未发布账户返回`available=false, cycles=[]`，存储/证据校验失败返回503，不影响账户净值接口。

每个`cycles[]`保留`profit`、已知/推定现金来源笔数，新增可选`component_review`。其中`same_day_gaps[]`逐赎回委托/证券记录划转量、当日关联卖出量、未关联量和账户开收盘数量；经已有成本链独立核对的后续卖出另含`later_sale_date/quantity/cents`。金额字段单位为分，数量不跨证券求和；后续卖出已包含在`profit.trading.later_component_sale_cents`，不得再次相加。空差额数组只证明已观察划转与当日关联卖出数量匹配，不证明清算完整；旧版本没有此字段时代表未发布明细，不等于零差额。原质量标记保留，账户日盈亏、净值和最终性不由这些明细改写。

逐父单匹配已封存券商现金证据的差额可另含`cash_explanation`：`transfer_fill_id`、`broker_flow_id`、`amount_cents`、`posted_at`（东八区）、`evidence_sha256`及`already_included=true`。它仅解释已经进入原周期现金的记录，不是新的现金腿、收入或应收；不得再次叠加。该流水必须属于同一账户/源日/周期且只能解释一项差额；不能与后续实物卖出解释共存。旧v1/v2无此字段不等于现金不存在，不得按证券代码推广成通用分类，也不改变原始划转报文或最终性。

核对版本v4新增可选`daily_review`（旧版本返回null）：`opening_date`为整份证据的开端，`days[]`按实际`trade_date`筛选，区别于周期的赎回源日；`daily_coverage_from/observed_through`为完整核对范围。日期筛选在完整版本校验之后进行，不以筛选起点将期初应收应付归零。

每个逐日记录包含`account_basis`（券商基准盈亏、原发布暂定盈亏、期初/期末未核验资产分项及原质量标记）、`cash_timing`（已知/推定源日两套收付/应收/应付/调整）及`inferred_exposed_flow_ids`。`inferred_cash_legs`仅返回所选日期引用的推定现金事实，包含原流水、金额、推定源日、实际记账日、关联依据及证据引用。金额全部为分。校验恒等式为`published_pnl = broker_basis_pnl + known_adjustment + inferred_adjustment`，其中调整已包含在原发布盈亏中，不是可再次记账的收益。

暴露按应收/应付毛额及期初/期末判断，不按净额是否为0判断；释放日仍有期初暴露。“无已观察时序及分项暴露”仅适用于本批已观察事实，不改变NAV原质量标记，不表示私有清算全部完成或每日策略归因已通过。接口仅读PG不可变版本，不增加CSV、OC或行情在线依赖。

经济净值输入层由 `000009_performance_accounting` 提供：

- `performance_fee_rules` 记录账户级、生效区间、版本化费用规则。柜台成交实际费用优先；实际费用缺失时使用生效费率估算；ETF 申赎 T0 的 15bp 摩擦成本通过 `estimated_friction_rate` 单独维护。

费率只读列表显式返回 `is_complete`，以额外读取一行判断截断，因此恰好 500 行可以证明完整，而 501 行不会冒充完整。未传 `limit` 或为 0 时默认 200，正值上限 500；超过上限、非法日期或非法状态返回 400，不再静默换成较小上限。`query` 回显规范化的 `account_id/status/effective_on/limit`，外层保留 `request_id/time`。日期有效期首尾均包含，列表不替调用方选择适用规则；实际账户必须明确匹配，不借用其他账户参数。普通贡献估算按市场/证券类型/业务/方向明确匹配数选择最具体规则，同分多条一律 `fee_rule_conflict`，不按 version 或返回顺序任意覆盖。SDK `0.1.42 get_fee_rules()` 默认拒绝不完整或旧响应中完整性未知的列表；完整空列表仍表示无已发布规则，不能当作零费用。当前数值字段和研究公式的语义、样本证据及尚待确认的收费/舍入口径见 `docs/CHRONOS_FEE_RULE_PUBLICATION_RESPONSE_20261011.md`。
- `cash_ledger` 扩展为可人工维护的资金流水，支持 `external` 外部入出金、`internal_transfer` 极速/普通柜台内部划转、`settlement_income` 清算收入、`fee` 和 `adjustment`。外部净流入修正收益率，内部划转成对记录但不计入账户净收益。
- `performance_nav_baselines` 记录手工确认的日初经济净值基线，解决逆回购回款、占款释放和柜台间划转导致的前一日日终资产与当日日初资产差异。
- `performance_nav_versions` 与 `performance_nav_reconciliations` 保存滚动经济净值和 T+1 对账结果。`economic-nav/preview` 可无写权限试算，`economic-nav/rebuild` 会把同账户同交易日旧 current 版本退役并插入新 current 版本；`economic-nav/reconcile` 读取下一交易日 open 资产/持仓快照，计算 T+1 观测资产残差并可受写开关保护落库；`nav-reconciliations/confirm` 和 `/block` 可人工确认或阻断对账，并将 current economic NAV 就地推进为 `finalized/blocked`；策略归因尚未拆分完成的账户日收益写入 `pnl_components.unattributed`。
- `reverse_repo_accruals` 保存 `204001.SH` 逆回购应计结果。估算口径为 `principal=qty*100`，`gross_interest=principal*(成交年化利率/100)*实际占款天数/365`，费用优先取 OC 订单实际费用，其次取可信成交费用和账户费率规则，缺失时标记 `missing_repo_fee`。实际占款天数由 Meridian 交易日接口向后取两个交易日计算，跨周末会自然得到 3 天。

`GET /v1/accounts/{account_id}/performance/contributions?trade_date=YYYYMMDD` 使用普通证券现金流恒等式 `close_value + sell_amount - buy_amount - open_value - effective_fee`。ETF 申赎 T0 优先使用赎回成交时刻之前最近的 Meridian Level1 IOPV；Level1 缺失时降级到赎回前最后一个完整 1 分钟 bar 的 IOPV，返回 `price_source=meridian_1m_iopv_fallback` 和 `minute_iopv_fallback`，不会使用当前赎回分钟或日收盘价冒充时点 IOPV。估值扣除配置项 `performance.etf_t0_friction_rate`；多个赎回组最多 8 路并发查询。缺少当日 open 持仓时，只在前一交易日 close 快照存在且 `open + buy - sell = close` 数量桥闭合时估算，否则返回 `pnl_status=missing`，不会把缺失持仓当作 0。接口只读，不触发 OC 查询。

`GET /v1/accounts/{account_id}/performance/economic-nav/preview?trade_date=YYYYMMDD` 当前使用 `performance_economic_nav.v2.7`。常规路径沿用以下公式：

```text
actual_execution_fee = unique(t0_buy_order_fee + redemption_parent_order_fee + linked_component_sell_order_fee)
etf_settlement_estimate = iopv_exit_value - linked_component_sales - estimated_friction + actual_execution_fee
open_etf_settlement_asset = sum(confirmed_fund_refund.released_estimate)
open_economic_nav = open_visible_cash + meridian_open_position_value + open_etf_settlement_asset
etf_settlement_variance = confirmed_fund_refund.receipt_amount - confirmed_fund_refund.released_estimate
base_close_nav = close_visible_cash + meridian_close_position_value + etf_settlement_estimate
principal_receivable = reverse_repo_principal - principal_already_in_visible_cash
close_economic_nav = base_close_nav + principal_receivable
account_day_pnl = close_economic_nav - open_economic_nav - external_net_flow - settlement_adjustment
daily_return = account_day_pnl / (open_economic_nav + sum(weight_i * external_flow_i))
```

当存在显式确认的 `asset_snapshots(snapshot_type=reconcile)` 券商资产基数时，v2.6 使用 `open_economic_nav = reported_open_total_asset + open_outstanding_etf_settlement_asset` 和 `close_economic_nav = reported_close_total_asset + close_outstanding_etf_settlement_asset + principal_receivable`。启用记录必须同时通过来源、`recurring_import=false`、文件 SHA-256、券商资产连续恒等式以及 ETF 待结算增量校验；否则继续 blocked。该分支不覆盖 OC 原始 `open/broker_close/close` 快照，也不把历史券商文件建设为日常导入。

v2.7 对开户首日增加例外但受严格门禁：当 confirmed clean-start 起算日的 `reported_open_total_asset=0`、`reported_deposit>0`、`reported_withdrawal=0` 且 `inception_funding_as_open_capital=true` 时，使用 `reported_deposit + open_outstanding_etf_settlement_asset` 作为首日 `open_economic_nav`。这笔盘前起算资本不写成普通盘中 `external_flow`，因此不会被误计为收益或重复调整。

其中 `open_economic_nav` 常规优先使用 `asset_snapshots(snapshot_type=open)`，缺失时使用上一 close 或手工 `performance_nav_baselines` 并打质量标记；`external_flow` 只读取已确认手工资金流水，用 Modified Dietz 盘中权重修正收益率分母；`settlement_adjustment` 不计入策略收益；`internal_transfer` 要求净额接近 0，否则标记 `internal_transfer_unbalanced`。ETF 待结算估值要求 T0 买入/赎回和实际 transfer/成分卖出数量闭合；OC 实际订单费用不完整时仍使用 IOPV + 15bp 生成 provisional 估值，只加回已经确认的费用并返回 `etf_settlement_execution_fee_pending`，不得 finalized。它不是公募最终清算事实。公募实际返款使用 confirmed `cash_ledger` 结算记录，`flow_class=settlement_adjustment`、`ledger_type=settlement`，并在 `raw_payload` 明确 `settlement_kind=etf_redemption_fund_refund`、`source_trade_date`、`estimated_receivable` 和 `confirmation_source`。分期返款通过 `settlement_group_id/prior_receipt_entry_id/receipt_stage` 形成审计链；快照后到账独立记入 `post_close_settlement_cash`。字段不完整时阻断，不把现金残差自动猜成返款，也不把返款记为外部入金。逆回购优先使用已落库 `reverse_repo_accruals`，没有时按成交账本只读试算；系统比较含/不含本金两条候选 NAV 对正式证券贡献的残差，输出 `principal_treatment=embedded/separate/ambiguous`、`principal_cash_overlap`、`principal_receivable`、`resolution_residual` 和 `alternate_residual`，歧义时阻断。超过告警阈值的剩余差额标记 `strategy_attribution_pending`；阈值内差额标记 `attribution_residual_within_tolerance`。

`GET /v1/accounts/{account_id}/performance/series` 同时识别日常 `performance_economic_nav.v2.x` 和经过完整券商账表门禁的 `broker_statement_nav.v1`。后者可为没有历史 OC close 快照的日期生成账户级曲线行，返回 `performance_status=finalized`、`open_snapshot_source=broker_statement`，并从 `pnl_components.broker_statement` 补充券商现金和市值；缺少旧 OC 计算依据的证券级浮盈或贡献字段保持不可用，不以账户总盈亏倒推。

`GET /v1/accounts/{account_id}/performance/economic-nav/reconcile?trade_date=YYYYMMDD&observed_trade_date=YYYYMMDD` 只读预览 T+1 对账；`POST /v1/accounts/{account_id}/performance/economic-nav/reconcile` 持久化对账结果，仍由 `performance.settings_write_enabled` 控制。`observed_trade_date` 为空时会通过 Meridian 交易日接口向后取下一交易日。第一版公式为 `observed_open_assets = asset_snapshots(open).cash_total + sum(position_snapshots(open).market_value)`，再扣减 `provisional_close_economic_nav`、盘前已确认 `external_flow` 和盘前已确认 `income_expense` 后得到 `residual`；状态按配置阈值写为 `auto_completed/review_required/blocked`。

`POST /v1/accounts/{account_id}/performance/nav-reconciliations/confirm` 请求体使用 `{trade_date, operator, note?, force?, reconciliation_id?}`，确认同一 current NAV 对应的 T+1 对账记录，写入 `reviewed_by/reviewed_at/details.review`，并把 current `performance_nav_versions.status` 就地改为 `finalized`。如果对账已 blocked 或 residual 超过 warning threshold，必须显式 `force=true`。`POST /v1/accounts/{account_id}/performance/nav-reconciliations/block` 使用同类请求体，把对账和 current NAV 标记为 `blocked`。两类接口都是绩效写入口，默认生产配置关闭。

人工输入类写接口默认关闭，由配置 `performance.settings_write_enabled` 控制。生产 9092 当前应保持 `false`，仅在服务器侧明确切换配置后才能通过 `/trade#performance-settings` 或 API Console 新增费率、资金流水、日初净值和持久化逆回购估算。

研究侧 PostgreSQL 导出 view 已通过 `000006_research_performance_views` 提供：

- `research_account_daily_performance_v1`：账户日绩效、持仓汇总、成交汇总和第一版 PnL 字段。
- `research_order_fill_export_v1`：订单与成交关联明细，包含本地/柜台/交易所订单 ID、委托状态、拒单信息和成交价量。

`GET /v1/meridian/market/bars` 是 Meridian `GET /v1/market/bars` 的同源薄代理，用于 P8 账表计算、绩效序列和交易终端分钟线的行情输入。relay 不重新定义 bars 字段，也不做字段映射；响应保持 Meridian `market_bar.v1` 的 `data/meta/error` 结构。典型参数包括 `security_id`、`security_ids`、`trade_date`、`start_date`、`end_date`、`frequency`、`adjustment`、`start_time`、`end_time` 和 `limit`，具体字段约束以 Meridian 为准。例如分钟线查询可使用 `security_id=600000.SH&trade_date=20260615&frequency=1m&adjustment=none&start_time=09:30:00&end_time=15:00:00&limit=300`；批量日线使用 `security_ids=600000.SH,000001.SZ&start_date=20260615&end_date=20260615&frequency=1d&adjustment=none`。仅当没有 `start_date/end_date` 且 `trade_date` 为空或等于东八区当天时，relay 才会调用 Meridian 交易日接口取得 `previous_or_current_trading_date`；范围查询原样透传，不补入互斥的 `trade_date`。当前交易日 15:00 前默认使用 `data_scope=realtime`，15:00 后使用 `auto` 读取 Meridian 当日归档，非交易日自动读取最近交易日 historical bars。为降低读压和 benchmark 重复查询，bars 代理对标准化后同 key 请求做 2 秒短缓存、singleflight 合并和 60 秒 stale fallback；该缓存只作用于 relay 到 Meridian 的代理层，不改变响应字段结构。

`GET /v1/meridian/metadata/adjust-factors` 是 Meridian `GET /v1/metadata/adjust-factors` 的同源薄代理，用于股票/ETF 截面绩效中的除权除息、分红和 ETF 份额折算校验。relay 只透传 `security_id/security_ids/trade_date/start_date/end_date/limit` 等 Meridian 参数并保留上游 `data/meta/error` 结构，不在本项目内另建复权因子标准。

ETF PCF 三个接口同样是透明代理，不转换字符串数值，也不在 Relay 中复制 PCF schema。单日查询使用 `trade_date`，范围查询使用 `start_date/end_date`，互斥约束以 Meridian 为准。绩效贡献仅从 `etf_cash_component.v1.unit_subscribe_redeem` 读取最小申赎单位，校验赎回量是否为整数倍；缺失、上游失败或数量不匹配时输出质量标记，不猜测单位，也不使用 PCF 估算基金管理人最终清算。

`GET /v1/meridian/stream/market/bars` 直接转发 Meridian SSE 事件，不改变 event/data 内容。默认参数只用于交易时实时分钟线订阅；Relay 当前交易终端仍使用带缓存的 HTTP bars 做初始加载和定时刷新，避免在本轮适配中改变页面刷新语义。

`POST /v1/jobs/runs` 用于 Python 日流程任务将 JSON 报告写入 `job_runs`，`/v1/status` 只展示最近盘前/盘后任务摘要，不返回完整 `report_json`。

`POST /v1/settlements/snapshots` 用于盘前初始化和收盘后结算任务内部调用。请求体包含 `trade_date`、`account_ids`、`run_id`、`snapshot_type`、`source`，以及可选的 `input_snapshot_type`、`captured_at`、`snapshot_only`、`dry_run`；其中 `snapshot_type` 支持 `intraday/open/broker_close/close/reconcile`。`broker_close` 表示 OC 最终查询完成后的原始券商资金/持仓，只固化数据，不做 Meridian 行情补全、不读取订单成交、不写 reconciliation；正式 `close` 使用 `input_snapshot_type=broker_close` 从这份不可变输入生成，因此可以在 Meridian 恢复后按原交易日补跑且不再连接 OC。`open` 使用 OC 盘前现金与持仓数量，并按 Meridian 上一交易日未复权 `1d close` 估值；`close` 按当日 Level1 last 或历史未复权 `1d close` 估值。日初派生资产按 `net_asset = cash_total + sum(position.market_value)` 汇总；标准日终派生资产按 `net_asset = cash_total + sum(position.market_value) + reverse_repo_receivable` 汇总，其中逆回购应收只含已成交本金，不含预估利息。现金总额缺失时才以 `cash_available` 兜底；任一正持仓缺失估值时拒绝写入，不能降级为现金-only 总资产，该严格门禁同样适用于 `reconcile` 等历史输入。仅有券商资产基数而没有逐证券持仓的 `reconcile` 不能作为持仓重估源。派生快照的 `raw_payload` 同时保留 `broker_asset/broker_position`、估值后字段、`valuation.source/trade_date` 和逆回购来源。`captured_at` 必须是带时区的 RFC3339 时间且日期与 `trade_date` 一致，仅用于已经确认源账本时间的故障恢复；响应也会返回最终使用的 `captured_at`。`snapshot_only=true` 必须同时提供 `captured_at`，只固化源资金/持仓，不按当前行情重估、不读取当前订单成交、不写 reconciliation。`pre_open_init` 使用 `snapshot_type=open` 写入日初快照；`post_close_capture` 使用 `snapshot_type=broker_close` 写入券商收盘输入；`post_close_settlement` 使用 `snapshot_type=close,input_snapshot_type=broker_close` 写入正式 close 和 `reconciliation_runs`。多账户请求最多并行处理 3 个账户并保持响应账户顺序。该接口本身不向前置发送查询命令。

## 后续工作

1. 增加常驻 worker 心跳状态和 DLQ 告警。
2. 如需同步涨跌停预校验，等待 Meridian 提供交易规则或涨跌停口径后再接入，不在 relay 内自建行情规则。
