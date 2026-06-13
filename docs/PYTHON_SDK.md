# relay Python SDK 设计

更新时间：`2026-06-13`

## 目标

relay 最终交易接口需要同时封装 Python SDK，供策略开发和研究联调使用。

SDK 的定位：

1. 面向策略开发者，提供比直接 HTTP 调用更稳定、更易用的接口。
2. 只调用 relay 9092 标准 API，不绕过 relay 账表、风控、状态机或审计链路。
3. 统一处理 `gateway_order_id`、`client_order_id`、`idempotency_key`、请求追踪和错误码。
4. 明确区分“命令已接受”和“订单最终完成”，不隐藏实盘交易异步语义。
5. 后续同时支持实盘账户和模拟账户，使用同一套方法。

## 包形态

建议包名：

```text
relay-sdk
```

Python import：

```python
from relay_sdk import RelayClient
```

建议目录：

```text
sdk/python/
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

relay SDK 建议命令：

```bash
python -m pip install "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.0.tar.gz"
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

### 交易

| SDK 方法 | HTTP API | 说明 |
| --- | --- | --- |
| `submit_order(...)` | `POST /v1/orders` | 单笔下单 |
| `submit_orders(...)` | `POST /v1/orders/batch` | 批量下单 |
| `cancel_order(...)` | `POST /v1/orders/{gateway_order_id}/cancel` | 撤单 |
| `wait_order_terminal(...)` | `GET /v1/orders` + event stream | 等待终态 |
| `stream_events(...)` | `GET /v1/events/stream` | 订阅订单和成交事件 |

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

## 测试与发布

SDK 后续需要：

1. 使用 mock 9092 API 做单元测试。
2. 使用文档门户或测试服务做集成测试。
3. 覆盖下单 accepted 但最终 rejected 的场景。
4. 覆盖撤单 accepted 但最终 filled 的竞态场景。
5. 覆盖 idempotency replay 和 conflict。
6. 提供内网 tar.gz 安装包，路径形如 `http://relay-trader.quantstage.com/sdk/relay-sdk-<version>.tar.gz`。
7. 后续可补充 wheel 包或内部 PyPI 发布方式。

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
