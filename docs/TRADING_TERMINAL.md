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

Go 侧只负责 `embed` 打包、`/trade` 路由和 `/assets/` 静态资源暴露。

## 当前 API 接入

| 能力 | 接口 |
| --- | --- |
| 服务状态 | `GET /v1/status` |
| 账户列表 | `GET /v1/accounts` |
| 资金读取 | `GET /v1/accounts/{account_id}/asset` |
| 持仓读取 | `GET /v1/accounts/{account_id}/positions` |
| 订单读取 | `GET /v1/orders?account_id=...` |
| 成交读取 | `GET /v1/fills?account_id=...` |
| 手动下单 | `POST /v1/orders` |
| 撤单 | `POST /v1/orders/{gateway_order_id}/cancel` |
| 资金刷新指令 | `POST /v1/accounts/{account_id}/asset/refresh` |
| 持仓刷新指令 | `POST /v1/accounts/{account_id}/positions/refresh` |

## 刷新策略

当前第一版采用 3 秒轮询现有账本查询接口，避免在实时推送接口尚未完成时引入额外复杂度。

订单状态签名包含：

- `status`
- `gateway_status`
- `cum_filled_qty`
- `leaves_qty`
- `last_updated_at`
- `reject_message`

签名变化时，委托行会短暂高亮，并在推送日志 tab 中记录状态变化。

## 页面结构

1. 顶部状态栏：环境、API/Redis/DB 状态、账户 tab、服务器时间和刷新/暂停按钮。
2. 左侧导航：交易测试、订单监控、成交回报、资金持仓、盘后对账、接口 Console、系统状态。
3. 左侧主面板：示例盘口和手动下单表单。
4. 中央主面板：资金摘要和持仓表。
5. 底部面板：当日委托、当日成交、撤单记录、推送日志、原始报文。
6. 右侧详情栏：委托详情、状态轨迹、原始 JSON 和成交执行记录。
7. 底部状态栏：API、Redis 延迟口径、交易阶段和本地时间。

## 当前边界

1. 行情/盘口当前是 UI 占位数据，后续接 Meridian 或前置行情后替换。
2. 实时推送当前用轮询模拟，后续升级为 `GET /v1/events/stream` SSE 或 WebSocket。
3. 撤单记录 tab 当前占位，等待撤单查询或事件分类落盘后展示。
4. Redis/DB 状态当前通过 `/v1/status` 和页面调用结果间接展示，后续应接入真实依赖健康检查。
5. 页面只面向测试账户。实盘 Redis 和生产账户接入前，需要再加环境隔离、二次确认和权限控制。

## 后续工作

1. 增加 `GET /v1/events/stream`，从 worker/Redis 消费位点推送订单、成交、资金、持仓事件。
2. 将盘口占位数据接入 Meridian 昨日行情或前置行情源。
3. 支持批量下单测试视图。
4. 支持请求模板保存。
5. 增加订单详情里的前置原始 reply/event 链路查看。
6. 增加页面级 Playwright 冒烟测试。
