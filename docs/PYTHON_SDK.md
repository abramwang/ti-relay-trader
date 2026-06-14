# relay Python SDK 设计

更新时间：`2026-06-14`

## 目标

relay 最终交易接口需要同时封装 Python SDK，供策略开发和研究联调使用。

SDK 的定位：

1. 面向策略开发者，提供比直接 HTTP 调用更稳定、更易用的接口。
2. 只调用 relay 9092 标准 API，不绕过 relay 账表、风控、状态机或审计链路。
3. 统一处理 `gateway_order_id`、`client_order_id`、`idempotency_key`、请求追踪和错误码。
4. 明确区分“命令已接受”和“订单最终完成”，不隐藏实盘交易异步语义。
5. 后续同时支持实盘账户和模拟账户，使用同一套方法。

## 当前状态

首版源码包已落在 `sdk/python/relay_sdk`，版本号 `0.1.1`。当前实现不依赖第三方 Python 包，使用标准库 HTTP 客户端，便于策略机在内网环境直接 editable 安装或通过 tar.gz 包安装。

已实现能力：

1. `RelayClient`：统一 base URL、默认账户、超时、Bearer API key 和代理开关。
2. 账户、资金、持仓、订单、成交查询。
3. 资金、持仓、订单、成交前置刷新指令。
4. 单笔下单、批量下单、撤单。
5. `wait_order_terminal()` 轮询等待订单终态。
6. `stream_events()` SSE 事件迭代器。
7. `on_order_status()`、`on_fill()` 后台回调订阅，以及 `watch_order_status()`、`watch_fills()` 阻塞式回调循环。
8. dataclass 模型和 `raw` 原始响应保留。
9. relay envelope 错误到 SDK 异常的映射。
10. mock 9092 API 单元测试。
11. `scripts/build-python-sdk.py` 打包脚本。
12. 9092 `/sdk/relay-sdk-0.1.1.tar.gz` 和 `.sha256` 下载入口。

尚未完成：

1. 面向真实 9092 测试服务的 SDK 集成测试。
2. 更完整的事件流断线重连和心跳处理。
3. SDK 版本发布检查清单和历史版本索引。

## 包形态

包名：

```text
relay-sdk
```

Python import：

```python
from relay_sdk import RelayClient
```

当前目录：

```text
sdk/python/
├── pyproject.toml
├── README.md
└── relay_sdk/
    ├── __init__.py
    ├── client.py
    ├── models.py
    ├── errors.py
    └── streaming.py
```

## 内网安装方式

参考 Meridian SDK 的发布方式，relay SDK 后续也通过内网 HTTP 地址提供安装包，策略机可直接 pip 安装。

Meridian 当前参考命令：

```bash
python -m pip install "http://meridian-data.quantstage.com/sdk/meridian-data-sdk-0.1.7.tar.gz"
```

relay SDK 当前命令：

```bash
python -m pip install "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.1.tar.gz"
```

校验文件：

```bash
curl -O http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.1.tar.gz
curl -O http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.1.tar.gz.sha256
sha256sum -c relay-sdk-0.1.1.tar.gz.sha256
```

本机工作区 editable 安装：

```bash
cd /home/ti-relay-trader
python -m pip install -e sdk/python
```

如果策略运行环境设置了 `HTTP_PROXY` 或 `HTTPS_PROXY`，需要确保 `NO_PROXY` 包含内网域名：

```bash
export NO_PROXY=relay-trader.quantstage.com,meridian-data.quantstage.com,$NO_PROXY
```

SDK 默认不读取系统代理环境变量，避免内网请求被外部代理劫持；如确实需要代理，应显式传入 `trust_env=True`。

## 最小使用示例

```python
from relay_sdk import RelayClient

client = RelayClient(
    base_url="http://relay-trader.quantstage.com",
    account_id="00030484",
    trust_env=False,
)

order = client.submit_order(
    symbol="600000",
    exchange="SH",
    side="B",
    price=9.54,
    qty=100,
    client_order_id="strategy-a-0001",
    idempotency_key="strategy-a-0001-submit",
)

print(order.gateway_order_id, order.status)

terminal = client.wait_order_terminal(
    gateway_order_id=order.gateway_order_id,
    timeout=30,
)

print(terminal.status, terminal.filled_qty)
```

## 订单和成交回调

策略程序可以用后台回调订阅订单状态和成交回报，避免自己解析 SSE 原始事件：

```python
from relay_sdk import RelayClient

client = RelayClient(
    base_url="http://relay-trader.quantstage.com",
    account_id="00030484",
)

def on_order_status(order, event):
    print("order", order.gateway_order_id, order.status, order.filled_qty)

def on_fill(fill, event):
    print("fill", fill.gateway_order_id, fill.fill_id, fill.qty, fill.price)

order_sub = client.on_order_status(on_order_status)
fill_sub = client.on_fill(on_fill)

# 策略退出前停止后台订阅。
order_sub.stop()
fill_sub.stop()
```

`on_order_status()` 和 `on_fill()` 会在后台 daemon thread 中运行，并返回 `CallbackSubscription`，可调用 `stop()`、`close()`、`join()`，也可读取 `error` 查看后台异常。若策略希望自己控制主循环，可直接使用阻塞式 `watch_order_status()` 和 `watch_fills()`。

当前后端 SSE 事件只说明订单或成交账本发生变化，不直接携带完整订单/成交对象。SDK 收到 `order.changed` 后会自动调用 `list_orders()` 拉取账本并按订单状态去重触发回调；收到 `fill.changed` 后会调用 `list_fills()` 并按成交唯一键去重触发回调。

## 建议接口

### 客户端

```python
client = RelayClient(
    base_url="http://relay-trader.quantstage.com",
    account_id="<account_id>",
    timeout=10,
    api_key=None,
    trust_env=False,
)
```

### 账户和查询

| SDK 方法 | HTTP API | 说明 |
| --- | --- | --- |
| `list_accounts()` | `GET /v1/accounts` | 查询可用账户 |
| `get_asset(account_id=None)` | `GET /v1/accounts/{account_id}/asset` | 查询资金资产 |
| `get_positions(account_id=None)` | `GET /v1/accounts/{account_id}/positions` | 查询持仓 |
| `list_orders(...)` | `GET /v1/orders` | 查询订单 |
| `list_fills(...)` | `GET /v1/fills` | 查询成交 |
| `refresh_asset(account_id=None)` | `POST /v1/accounts/{account_id}/asset/refresh` | 触发资金前置查询 |
| `refresh_positions(account_id=None)` | `POST /v1/accounts/{account_id}/positions/refresh` | 触发持仓前置查询 |
| `refresh_orders(account_id=None)` | `POST /v1/accounts/{account_id}/orders/refresh` | 触发订单前置查询 |
| `refresh_fills(account_id=None)` | `POST /v1/accounts/{account_id}/fills/refresh` | 触发成交前置查询 |

### 交易

| SDK 方法 | HTTP API | 说明 |
| --- | --- | --- |
| `submit_order(...)` | `POST /v1/orders` | 单笔下单 |
| `submit_orders(...)` | `POST /v1/orders/batch` | 批量下单 |
| `cancel_order(...)` | `POST /v1/orders/{gateway_order_id}/cancel` | 撤单 |
| `wait_order_terminal(...)` | `GET /v1/orders` + event stream | 等待终态 |
| `stream_events(...)` | `GET /v1/events/stream` | 订阅订单和成交事件 |
| `on_order_status(...)` | `GET /v1/events/stream` + `GET /v1/orders` | 后台订单状态回调 |
| `on_fill(...)` | `GET /v1/events/stream` + `GET /v1/fills` | 后台成交回调 |
| `watch_order_status(...)` | `GET /v1/events/stream` + `GET /v1/orders` | 阻塞式订单状态回调 |
| `watch_fills(...)` | `GET /v1/events/stream` + `GET /v1/fills` | 阻塞式成交回调 |

## 模型建议

首批 SDK 模型应和 9092 API schema 一一对应：

| SDK 模型 | 说明 |
| --- | --- |
| `Account` | 账户配置和状态 |
| `Asset` | 资金资产 |
| `Position` | 持仓和可卖数量 |
| `OrderRequest` | 下单请求 |
| `OrderReceipt` | 下单命令回执 |
| `Order` | 订单状态 |
| `Fill` | 成交 |
| `OrderEvent` | 订单事件 |
| `FillEvent` | 成交事件 |
| `RelayError` | 标准错误 |

## 关键语义

SDK 必须保留 relay 的实盘语义：

1. `submit_order()` 返回成功只表示 relay 或前置服务接受命令，不表示交易所已接单或成交。
2. 订单最终状态必须通过 `OrderEvent` 或 `wait_order_terminal()` 判断。
3. 撤单返回成功只表示撤单命令已提交，撤单最终结果仍以订单事件为准。
4. 成交事实以 `FillEvent` 和 `Fill` 为准。
5. SDK 自动生成 `gateway_order_id` 和 `idempotency_key` 时必须可复现、可追踪。
6. SDK 日志不能打印账号密码、Token、数据库连接串或 Redis 连接串。

## 配置来源

SDK 支持三种配置方式：

1. 显式参数，例如 `RelayClient(base_url=..., account_id=...)`。
2. 环境变量，例如 `RELAY_BASE_URL`、`RELAY_ACCOUNT_ID`、`RELAY_API_KEY`。
3. 本地配置文件，例如 `~/.relay/client.yaml`。

策略代码中不建议硬编码真实凭据。

## 错误处理

SDK 将 HTTP 错误和 relay 标准错误统一封装为异常：

| 异常 | 说明 |
| --- | --- |
| `RelayConnectionError` | 网络连接失败 |
| `RelayTimeoutError` | 请求或等待事件超时 |
| `RelayRejectedError` | relay 或前置服务拒绝命令 |
| `RelayIdempotencyError` | 幂等键冲突 |
| `RelayOrderStateError` | 订单状态不满足操作条件 |

异常中保留：

- `code`
- `message`
- `request_id`
- `correlation_id`
- `gateway_order_id`
- `raw_response`

## 当前测试

本地 mock API 单元测试：

```bash
cd /home/ti-relay-trader
PYTHONPATH=sdk/python python3 -m unittest discover -s sdk/python/tests -v
```

当前覆盖：

1. 账户、资金、持仓、订单和成交查询模型转换。
2. 下单时自动生成可追踪 `gateway_order_id` 和 `idempotency_key`。
3. 资金、持仓、订单和成交刷新。
4. 撤单回执。
5. `wait_order_terminal()` 轮询终态。
6. `IDEMPOTENCY_CONFLICT` 到 `RelayIdempotencyError` 的异常映射。
7. SSE `order.changed` 事件解析。
8. `on_order_status()` 收到事件后查询订单并按状态去重触发回调。
9. `watch_fills()` 收到事件后查询成交并按成交唯一键去重触发回调。

打包验证：

```bash
cd /home/ti-relay-trader
python3 scripts/build-python-sdk.py
sha256sum -c public/sdk/relay-sdk-0.1.1.tar.gz.sha256
python3 -m pip install --no-deps --target /tmp/relay-sdk-install-test public/sdk/relay-sdk-0.1.1.tar.gz
```

## 测试与发布

SDK 后续需要：

1. 使用文档门户或测试服务做集成测试。
2. 增加事件流断线重连、heartbeat 和超时测试。
3. 覆盖下单 accepted 但最终 rejected 的场景。
4. 覆盖撤单 accepted 但最终 filled 的竞态场景。
5. 覆盖 idempotency replay 和 conflict。
6. 后续可补充 wheel 包或内部 PyPI 发布方式。

每次 SDK 版本更新必须同步更新：

- `sdk/python/pyproject.toml`
- `sdk/python/relay_sdk/__init__.py`
- `docs/PYTHON_SDK.md` 的版本记录和安装命令
- 9092 文档门户首页或 `/docs/python-sdk`
- `/v1/version` 中的 SDK 版本
- `public/sdk/relay-sdk-<version>.tar.gz` 安装包

## 与 9092 API 的关系

Python SDK 是 9092 标准 API 的客户端封装，不是单独的交易后门。

所有策略交易请求仍然必须经过：

```text
策略 Python SDK
    -> relay 9092 API
    -> relay 多账户路由和账表
    -> Redis Stream
    -> 托管机房前置服务
    -> A 股柜台
```
