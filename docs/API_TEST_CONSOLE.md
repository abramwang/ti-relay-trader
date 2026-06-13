# relay 接口测试台

更新时间：`2026-06-13`

## 目标

relay 后续提供正式交易 API 后，9092 页面需要同步提供接口测试台，方便交易软件、策略开发、前置服务联调和运维排查。

页面形态参考 Apifox 的工作台模式：

1. 左侧接口集合。
2. 中间请求编辑区。
3. 右侧响应查看区。
4. 支持 base URL、method、path、query、headers、body 编辑。
5. 支持直接发送请求并查看 HTTP 状态、耗时和响应 JSON。

## 当前实现

当前已在文档门户增加入口：

```text
http://relay-trader.quantstage.com/api-console
```

现阶段测试台已经可以从 9092 文档门户同源发送 `/v1/*` 请求。文档门户启动时会复用 API handler：基础发现接口可直接返回；如果启动配置包含 PostgreSQL、测试 Redis 和账户路由，也可以在同一页面发送测试账户资金、持仓、下单、批量下单、撤单和账本查询请求。

当前可直接测试：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 文档门户或 API 模式健康检查 |
| `GET` | `/v1/status` | 服务状态 |
| `GET` | `/v1/schema` | schema 发现 |
| `GET` | `/v1/accounts` | 配置态账户列表 |

需要本地配置支持的接口：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/accounts/{account_id}/asset` | 从 PostgreSQL 最新资金快照读取 |
| `GET` | `/v1/accounts/{account_id}/positions` | 从 PostgreSQL 当前持仓表读取 |
| `POST` | `/v1/orders` | 单笔下单，返回 `202 Accepted`，表示订单草稿已落盘且命令已写入 Redis |
| `POST` | `/v1/orders/batch` | 批量下单，写入多笔订单草稿并发布 Redis `order.batch.submit` |
| `POST` | `/v1/orders/{gateway_order_id}/cancel` | 撤单，先校验本地订单非终态，再写入 Redis `order.cancel` |
| `GET` | `/v1/orders` | 从 PostgreSQL 账本查询订单 |
| `GET` | `/v1/fills` | 从 PostgreSQL 账本查询成交 |

事件流接口已经在页面中占位，但标记为 `planned`，当前页面不会发送这些未实现请求。

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
6. 资金/持仓查询当前只读本地 PostgreSQL 账表，不主动刷新前置柜台数据。
7. 若 9092 以纯文档配置启动，测试台仍可发送 `/v1/status`、`/v1/schema` 和 `/v1/accounts`，交易和账本接口会返回明确的不可用或空结果。

## 后续工作

1. 增加环境切换：文档门户、API 模式、测试前置环境。
2. 将 endpoint 状态由手写清单改成自动读取 `/v1/schema`。
3. 增加请求样例保存和导出。
4. 增加响应断言，用于冒烟测试。
5. 增加前置查询刷新模板。
