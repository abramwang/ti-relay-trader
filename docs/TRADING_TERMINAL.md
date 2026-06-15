# relay 交易终端

更新时间：`2026-06-14`

## 目标

`/trade` 是面向手动联调和交易运维的成熟交易软件风格测试终端，用来补充 `/api-console` 的接口级测试能力。

它重点覆盖订单具备持续推新、状态刷新和撤单回报的使用场景：

1. 多账户切换。
2. 资金、持仓、订单、成交统一观察。
3. 手动单笔下单。
4. 从委托表直接撤单。
5. 订单状态变化高亮。
6. 委托详情、状态轨迹、原始 JSON 和成交执行记录查看。

## 当前入口

```text
http://relay-trader.quantstage.com/trade
```

该页面在文档门户模式下直接同源调用 `/v1/*` API。当前 9092 使用本地未跟踪配置启动时，可以访问测试 PostgreSQL、测试 Redis 和测试账户。

## 实现结构

交易终端不复用文档门户的普通文章外壳，而是全屏工作台模板：

| 文件 | 说明 |
| --- | --- |
| `cmd/relay-docs/web/templates/trade_terminal.html` | 全屏交易终端页面结构 |
| `cmd/relay-docs/web/static/trade-terminal.css` | 交易终端布局、配色、表格、状态和细节面板样式 |
| `cmd/relay-docs/web/static/trade-terminal.js` | 页面状态、API 调用、轮询刷新、下单、撤单和渲染逻辑 |
| `cmd/relay-docs/web/static/echarts.min.js` | 本地 ECharts 运行时，用于交易测试分钟 K 线和买卖点标注 |

Go 侧只负责 `embed` 打包、`/trade` 路由和 `/assets/` 静态资源暴露。

## 当前 API 接入

| 能力 | 接口 |
| --- | --- |
| 服务状态 | `GET /v1/status` |
| 账户列表 | `GET /v1/accounts` |
| 账户别名更新 | `PATCH /v1/accounts/{account_id}/alias` |
| 资金读取 | `GET /v1/accounts/{account_id}/asset` |
| 持仓读取 | `GET /v1/accounts/{account_id}/positions` |
| 订单读取 | `GET /v1/orders?account_id=...` |
| 成交读取 | `GET /v1/fills?account_id=...` |
| 手动下单 | `POST /v1/orders` |
| 撤单 | `POST /v1/orders/{gateway_order_id}/cancel` |
| 资金刷新指令 | `POST /v1/accounts/{account_id}/asset/refresh` |
| 持仓刷新指令 | `POST /v1/accounts/{account_id}/positions/refresh` |
| 委托刷新指令 | `POST /v1/accounts/{account_id}/orders/refresh` |
| 成交刷新指令 | `POST /v1/accounts/{account_id}/fills/refresh` |
| 实时事件流 | `GET /v1/events/stream?account_id=...` |
| Meridian 证券主数据代理 | `GET /v1/meridian/metadata/instruments` |
| Meridian 快照代理 | `GET /v1/meridian/market/snapshots` |
| Meridian bars 代理 | `GET /v1/meridian/market/bars` |
| 历史订单读取 | `GET /v1/history/orders?account_id=...&trade_date=...` |
| 历史成交读取 | `GET /v1/history/fills?account_id=...&trade_date=...` |

行情和证券主数据相关字段约束全部以 Meridian 为准。relay 只做同源薄代理和交易页输入转换，不重新定义行情数据字段；响应保持 Meridian `data/meta/error` 结构，页面直接使用 `security_id`、`name`、`instrument_type`、`market_level`、`trade_date`、`last`、`pre_close`、`bids`、`asks` 等字段。

价格精度同样只依据 Meridian `instrument_type`：股票 `stock` 显示和输入保留 2 位小数，ETF `etf` 显示和输入保留 3 位小数。该规则覆盖行情头、涨跌额、五档盘口、下单价格框、持仓成本/现价、委托价格和成交价格；账本记录缺少 `instrument_type` 时，页面会先尝试用当前快照或已缓存证券主数据匹配，仍无法识别时默认按股票 2 位显示。

## 刷新策略

当前页面优先通过 `GET /v1/events/stream?account_id=...` 建立 SSE 实时通道。9092 服务端会启动轻量后台 Redis `reply/event` 同步循环，把前置订单状态、成交、资金和持仓回报持续写入 PostgreSQL；同步循环在落账后广播 `order.changed`、`fill.changed`、`asset.changed` 和 `positions.changed` 事件，页面收到事件后合并触发账本查询刷新。

页面仍保留 3 秒轮询作为兜底：如果浏览器不支持 EventSource、SSE 断线重连或服务端临时不可用，订单、成交、资金和持仓仍会通过轮询刷新。

订单监控区额外提供“刷新委托”和“刷新成交”按钮，分别向前置发送 `order.list.query` 和 `fill.list.query`。这两个按钮只负责触发柜台查询，查询结果仍通过 `order_page/fill_page` reply 进入账本，再由本地账本查询和 SSE/轮询刷新到页面。

订单或成交事件落账后，服务端会按账户自动调度一轮资金和持仓刷新查询，写入前置 `cmd.query`。默认策略是 2 秒 debounce 合并、20 秒 cooldown 冷却、10 秒发布超时，配置项为 `auto_refresh.debounce_seconds`、`auto_refresh.cooldown_seconds` 和 `auto_refresh.timeout_seconds`。这样 `/trade` 的持仓可以跟随成交更新，同时不会因为订单状态连续推送而高频查询柜台。

订单状态签名包含：

- `status`
- `gateway_status`
- `cum_filled_qty`
- `leaves_qty`
- `last_updated_at`
- `reject_message`

签名变化时，委托行会短暂高亮，并在推送日志 tab 中记录状态变化。

## 页面结构

1. 顶部状态栏：环境、API/Redis/DB 状态、账户 tab、服务器时间和 RT 身份块。
2. 左侧导航：交易测试、订单监控、资金持仓、绩效分析、接口 Console、系统状态。
3. 左侧主面板：Meridian 行情、五档盘口和手动下单表单。
4. 中央主面板：Meridian 1m 分钟 K 线、成交量和买卖订单/成交点位标注。
5. 右侧主面板：压缩版资金摘要和持仓表。
6. 底部面板：当日委托、当日成交、撤单记录、推送日志、原始报文。
7. 右侧详情栏：委托详情、状态轨迹、原始 JSON 和成交执行记录。
8. 右下角弹出框：下单、撤单、初始化失败等操作反馈。
9. 底部状态栏：API、Redis 延迟口径、交易阶段和 `Asia/Shanghai` 服务时间。

左侧导航当前在 `/trade` 内切换四类工作区：

1. `交易测试`：保留行情、下单、持仓摘要、委托/成交底部面板和右侧委托详情。
2. `订单监控`：用底部订单模块扩展成完整页面，展示委托数、活动委托、成交数和最近回报；成交回报不再是独立页面，而是该页面里的 `当日成交` tab。
3. `资金持仓`：用当前持仓模块扩展成完整页面，展示总资产、可用资金、市值、当日盈亏，以及资金总额、股票市值、基金市值、持仓盈亏、平仓盈亏、手续费和持仓表。
4. `绩效分析`：承接收盘后 close 快照、日终权益/PnL、净值序列、CSV 导出和 Meridian bars 基准数据检查；不再单独提供“盘后对账”页面入口。后续绩效页会改为净值曲线、收益贡献和交易归因，不再把交易辅助分钟 K 线作为主图。

`交易测试` 视图中的右侧持仓区域采用压缩版资金摘要、工具栏和表格行高，中间区域使用 ECharts `candlestick` 绘制 Meridian `bars` 1m 分钟 K 线并叠加成交量，给手动下单提供点位参考；`资金持仓` 独立工作区仍保留完整资金拆分和分页持仓表。

`交易测试` 的分钟 K 线使用同源 `/v1/meridian/market/bars`，默认请求 1m、`09:30:00` 到 `15:00:00`、最多 300 条。若页面输入的是当天或空日期，后端会先调用 Meridian 交易日接口取得 `previous_or_current_trading_date`：交易日当天使用 `data_scope=realtime`，非交易日读取最近交易日 historical bars，并把实际交易日回填到页面输入框。bars 代理对同 key 请求做短 TTL 缓存、并发合并和 stale fallback，因此交易终端图表、API Console 和绩效 benchmark 共享同一层抗压保护。

分钟 K 线买卖点来自 relay 本地账本，不新增行情字段定义：

1. 成交优先，使用 `fills.price` 和 `fills.matched_at/match_timestamp`。
2. 同一订单已有成交时，不重复绘制该订单的委托点。
3. 未成交订单使用 `orders.limit_price` 和 `created_at/inserted_at/accepted_at/last_updated_at`。
4. 买点用红色上三角，卖点用绿色下三角；tooltip 展示成交/委托、ID、状态、价格和数量。
5. 打开交易测试页、切换证券代码、手动刷新 K 线或收到 `order.changed/fill.changed` SSE 事件时，会刷新当前图表标的的标注。

## 订单 ID 口径

前置程序在同一订单状态变化时，会推送完整订单快照。relay 账本处理这类 `order.event` 时按整单 upsert，而不是只更新状态字段。

当日委托表展示三类当日唯一 ID：

| 页面字段 | relay 字段 | 说明 |
| --- | --- | --- |
| `ReqID` | `client_order_id` | 本地客户端维护的请求/委托 ID |
| `柜台 ID` | `order_id` | 前置系统或券商柜台返回的订单 ID |
| `交易所 ID` | `order_stream_id` | 交易所委托流号 |

`gateway_order_id` 仍作为 relay 和前置之间的北向关联键，用于撤单、事件归属和内部排查；页面在 `ReqID` 下方以小字展示。

成交记录与订单具有关联关系。成交表会通过 `gateway_order_id` 回查订单，并展示：

| 页面字段 | 成交字段 | 关联说明 |
| --- | --- | --- |
| `成交编号` | `fill_id` | 成交自身编号 |
| `ReqID` | `orders.client_order_id` | 通过 `fills.gateway_order_id` 关联订单后展示 |
| `柜台 ID` | `fills.order_id` 或 `orders.order_id` | 成交回报里的柜台订单 ID，缺失时使用订单表字段 |
| `交易所 ID` | `fills.order_stream_id` 或 `orders.order_stream_id` | 成交回报里的交易所委托流号，缺失时使用订单表字段 |

## 当前边界

1. 行情/盘口当前通过 Meridian `/v1/market/snapshots` 获取；如果当日不是交易日，relay 会先调用 Meridian `/v1/metadata/trading-day` 取得最近交易日再读取 historical 快照。若当日是交易日，relay 会显式带上 `trade_date=东八区当天`，避免 Meridian 实时缓存尚未换日时回放旧交易日快照。交易测试页分钟 K 线通过 Meridian `/v1/market/bars` 获取，当 `trade_date` 为空或等于东八区当天时，交易日当天默认使用 `data_scope=realtime`，非交易日才回退最近交易日 historical。
2. 实时推送当前使用 9092 内部事件 hub 和 SSE；正式生产化后应由持久化位点 worker 继续驱动同一个事件出口。
3. 撤单记录 tab 当前占位，等待撤单查询或事件分类落盘后展示。
4. Redis/DB 状态来自 `/v1/status` 依赖健康检查；页面顶部当前展示摘要状态，后续可扩展为更细的 lag、DLQ 和 pending query/trade 监控。
5. 代码补全当前使用 Meridian `/v1/metadata/instruments`，按 `exchange/instrument_type/status/limit/cursor` 取证券主数据并在前端过滤输入前缀。持仓、委托和成交表格中的“证券名称”列使用同一个 Meridian metadata 薄代理，并按当前可见表格代码通过 `security_ids` 批量补齐 `name/instrument_type`；这些字段只作为页面展示和价格精度辅助，不写入 relay 自定义证券主数据。若需要更多名称、拼音、行业等补全能力，应在 Meridian 增加/完善接口，而不是在 relay 内自建标准。
6. 页面只面向测试账户。实盘 Redis 和生产账户接入前，需要再加环境隔离、二次确认和权限控制。

## 后续工作

1. 将 SSE 事件源从 9092 轻量同步循环迁移到正式 worker，并持久化 Redis Stream 位点。
2. 在 Meridian 提供更完整证券主数据搜索能力后，增强代码输入的名称/拼音补全。
3. 支持批量下单测试视图。
4. 支持请求模板保存。
5. 增加订单详情里的前置原始 reply/event 链路查看。
6. 增加页面级 Playwright 冒烟测试，覆盖三类工作区切换。
