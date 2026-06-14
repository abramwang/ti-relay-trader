# relay 接口测试台

更新时间：`2026-06-14`

## 目标

relay 后续提供正式交易 API 后，9092 页面需要同步提供接口测试台，方便交易软件、策略开发、前置服务联调和运维排查。

页面形态参考 Apifox 的工作台模式：

1. 左侧接口集合。
2. 中间请求编辑区。
3. 右侧响应查看区。
4. 支持 base URL、method、path、query 和 body 参数表单填写。
5. 支持直接发送请求并查看 HTTP 状态、耗时、响应 JSON 和表格视图。

## 当前实现

当前已在文档门户增加入口：

```text
http://relay-trader.quantstage.com/api-console
```

现阶段测试台已经可以从 9092 文档门户同源发送 `/v1/*` 请求。文档门户启动时会复用 API handler：基础发现接口可直接返回；如果启动配置包含 PostgreSQL、测试 Redis 和账户路由，也可以在同一页面发送测试账户资金、持仓、订单、成交刷新、下单、批量下单、撤单和账本查询请求。

页面已参考 Meridian API 测试页改成表单模式：左侧选择接口，中间按 `path/query/body` 自动生成参数表单，右侧展示 HTTP 状态、耗时、响应 JSON；当响应里包含 `accounts`、`asset`、`positions`、`orders`、`fills` 等账表对象时，会额外显示表格。`GET /v1/events/stream` 这类 SSE 接口会使用 EventSource 连接，并展示最近收到的事件。

## 实现结构

接口测试台不再直接内联在 Go handler 中，当前拆分为：

| 文件 | 说明 |
| --- | --- |
| `cmd/relay-docs/web/templates/api_console.html` | 页面结构模板 |
| `cmd/relay-docs/web/static/api-console.css` | 接口测试台样式 |
| `cmd/relay-docs/web/static/api-console.js` | 表单渲染、请求发送、JSON/表格响应渲染 |
| `cmd/relay-docs/web/static/api-console.catalog.json` | 接口分组、参数字段、默认值和状态 |

Go 侧通过 `embed` 打包这些资源，并通过 `/assets/` 暴露静态文件。后续新增接口时，优先更新 catalog；只有后端路由、权限或响应结构变化时才修改 Go 代码。

当前可直接测试：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 文档门户或 API 模式健康检查 |
| `GET` | `/v1/status` | 服务状态、依赖健康和账户摘要 |
| `GET` | `/v1/schema` | schema 发现 |
| `GET` | `/v1/accounts` | 配置态账户列表 |

需要本地配置支持的接口：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/accounts/{account_id}/asset` | 从 PostgreSQL 最新资金快照读取 |
| `POST` | `/v1/accounts/{account_id}/asset/refresh` | 向前置发送 `account.asset.query`，后续由同步任务合并 reply |
| `GET` | `/v1/accounts/{account_id}/positions` | 从 PostgreSQL 当前持仓表读取 |
| `POST` | `/v1/accounts/{account_id}/positions/refresh` | 向前置发送 `account.positions.query`，后续由同步任务合并 reply |
| `POST` | `/v1/accounts/{account_id}/orders/refresh` | 向前置发送 `order.list.query`，后续由同步任务合并非空 `order_page` |
| `POST` | `/v1/accounts/{account_id}/fills/refresh` | 向前置发送 `fill.list.query`，后续由同步任务合并非空 `fill_page` |
| `POST` | `/v1/orders` | 单笔下单，返回 `202 Accepted`，表示订单草稿已落盘且命令已写入 Redis |
| `POST` | `/v1/orders/batch` | 批量下单，写入多笔订单草稿并发布 Redis `order.batch.submit` |
| `POST` | `/v1/orders/{gateway_order_id}/cancel` | 撤单，先校验本地订单非终态，再写入 Redis `order.cancel` |
| `GET` | `/v1/orders` | 从 PostgreSQL 账本查询当日订单，默认按东八区当日过滤 |
| `GET` | `/v1/fills` | 从 PostgreSQL 账本查询当日成交，默认按东八区当日过滤 |
| `GET` | `/v1/history/orders` | 历史订单查询，支持 `trade_date/date_from/date_to` |
| `GET` | `/v1/history/fills` | 历史成交查询，支持 `trade_date/date_from/date_to` |
| `GET` | `/v1/accounts/{account_id}/positions/history` | 历史持仓快照查询 |
| `GET` | `/v1/accounts/{account_id}/performance/daily` | 日终权益和 PnL 输入汇总，依赖 close 资产快照 |
| `GET` | `/v1/accounts/{account_id}/performance/series` | 账户 close 净值绩效序列、累计收益和回撤 |
| `GET` | `/v1/accounts/{account_id}/performance/series.csv` | 账户绩效序列 CSV 导出 |
| `GET` | `/v1/meridian/market/bars` | Meridian bars 薄代理，保留 `market_bar.v1` 原始字段 |
| `GET` | `/v1/jobs/runs` | 查看最近盘前/盘后任务运行记录 |
| `GET` | `/v1/events/stream` | SSE 实时事件流，支持按 `account_id` 过滤订单、成交、资金和持仓变化 |

## 测试行情参考

当前前置测试环境行情按上一交易日回放或静态数据理解。测试下单前应先从 Meridian 拉参考价，不要硬编码旧价格。

当前确认可用样例：

```text
GET http://meridian-data.quantstage.com/v1/market/bars?security_id=600000.SH&trade_date=20260612&frequency=1m&adjustment=none&start_time=14:59:00&end_time=15:00:00&limit=5
```

该接口返回 `600000.SH` 在 `2026-06-12 15:00:00+08:00` 的 1 分钟线 `close=9.67`。接口测试台当前下单样例使用 `symbol=600000`、`exchange=SH`、`price=9.67`。

## 安全边界

1. 页面本身不保存凭据。
2. 页面不会自动发送任何请求。
3. `planned` 接口默认禁用发送按钮。
4. 交易写接口仅在启动配置包含 PostgreSQL、测试 Redis 和账户路由时可用，且账户配置必须 `enabled=true`、`trading_enabled=true`。
5. 实盘账户会使用另一套 Redis 连接方式，测试 Redis 与实盘 Redis 不混用。
6. 资金/持仓/订单/成交查询默认只读本地 PostgreSQL 账表；刷新接口会发送测试前置 `cmd.query`，需要 9092 轻量同步循环、`ledger-sync` 或后续 worker 合并 reply 后才能在查询接口看到最新数据。
7. 若 9092 以纯文档配置启动，测试台仍可发送 `/v1/status`、`/v1/schema` 和 `/v1/accounts`，交易和账本接口会返回明确的不可用或空结果。

## 后续工作

1. 增加环境切换：文档门户、API 模式、测试前置环境。
2. 将 endpoint 状态和参数字段由静态 JSON catalog 改成自动读取 `/v1/schema`。
3. 增加请求样例保存和导出。
4. 增加响应断言，用于冒烟测试。
