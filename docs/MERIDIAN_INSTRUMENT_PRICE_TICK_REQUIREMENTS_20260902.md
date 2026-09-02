# Relay / Chronos 对 Meridian 证券价位元数据的需求

- 提交方：Relay Trader、Chronos Strategy
- 接收方：Meridian Data
- 日期：`2026-09-02`
- 优先级：`P0`，阻塞 Relay Python SDK 的统一价格契约验收
- 需求边界：证券主数据与价格规则元数据，不涉及交易订单、风控、费用或行情撮合

## 1. 背景

Chronos 通过 Relay Python SDK 构建策略执行链路。Relay 和 Meridian 当前继续使用 JSON number / Python `float` 传输成交价、委托价和行情价格；Chronos 会在进程内部用 `Decimal(str(value))` 将价格归一为整数微元，并只在调用 Relay SDK 时转换回 `float`。

这套方式不要求 Meridian 改变现有价格字段的数据类型，但策略侧必须从 Meridian 获得每个证券的权威最小报价单位，才能判断归一后的价格是否可报。Relay 不应根据证券代码前缀或自行维护的品种枚举猜测最小价位。

## 2. 当前观测

截至 `2026-09-02`，生产接口 [证券主数据查询](http://meridian-data.quantstage.com/api-tests/metadata/instruments) 使用 `metadata_instrument.v1`：

1. `GET /v1/metadata/instruments` 的记录包含 `security_id`、`instrument_type`、`exchange`、`round_lot` 等字段，但没有最小价位或价格小数位字段。
2. 页面和接口参数只接受 `instrument_type=stock|etf|index`。
3. 请求 `instrument_type=convertible_bond` 返回 HTTP 400，当前无法通过 Meridian 标准主数据发现沪深可转债。
4. `600000.SH`、`510300.SH` 的实际响应均没有 `price_tick`、`tick_size` 或 `price_decimals`。

因此 Relay 目前只能在界面层沿用股票 2 位、ETF 3 位的既有约定，无法为 Chronos 提供覆盖 A 股、ETF、可转债且来源统一的可报价格校验契约。

## 3. P0 需求

### 3.1 扩展证券类型覆盖

在 `GET /v1/metadata/instruments` 中增加沪深可转债主数据，并支持：

```text
instrument_type=convertible_bond
```

可转债记录继续使用 Meridian 标准 `security_id`，来源代码放在 `aliases`；至少保留 `exchange`、`name`、`status`、`listed_date`、`round_lot` 和交易时段等现有通用字段。活动和已退市证券应沿用已有 `status` 查询口径，不能只覆盖当日活跃样本。

若 Meridian 已有更合适且稳定的官方类型名称，可以采用该名称，但需要在文档中明确，并保证 API、SDK、schema 和质量报告使用同一个枚举值。

### 3.2 增加权威最小价位字段

为至少 `stock`、`etf`、`convertible_bond` 三类可交易证券增加以下字段：

| 字段 | 类型 | 必填性 | 语义 |
| --- | --- | --- | --- |
| `price_tick` | JSON number | 活跃可交易证券必填 | 交易所允许的最小报价增量，必须有限且大于 0 |
| `price_decimals` | integer | 活跃可交易证券必填 | 规范展示和 JSON 往返允许的最大小数位；有效性仍以 `price_tick` 整倍数为准 |
| `price_tick_source` | string | 必填 | 权威来源或上游数据集名称，不接受 Relay 代码前缀推断 |
| `price_tick_as_of_date` | `YYYYMMDD` integer | 必填 | 该规则最后确认适用的业务日期，按 `Asia/Shanghai` 解释 |

响应示意如下，数值需要由 Meridian 使用其权威来源核验：

```json
{
  "security_id": "510300.SH",
  "instrument_type": "etf",
  "exchange": "SH",
  "round_lot": 100,
  "price_tick": 0.001,
  "price_decimals": 3,
  "price_tick_source": "<authoritative-source>",
  "price_tick_as_of_date": 20260902,
  "schema_version": "<meridian-version>"
}
```

不要求 Meridian 返回 `price_micros`。下游统一使用十进制文本语义读取 JSON number，例如 Python 使用 `Decimal(str(price_tick))`，避免二进制浮点参与补偿和取整。

### 3.3 规则日期与失败关闭

最低要求是稳定提供当前有效规则和 `price_tick_as_of_date`。如果 Meridian 能提供历史规则，建议在现有 endpoint 增加 `trade_date` 或有效期字段 `effective_from/effective_to`，用于旧策略运行清单重放。

规则缺失、来源不明、存在多个冲突值或值非正时，不应按证券代码猜测默认值。建议保留记录但将价位字段置空，并在元数据质量状态中明确列为未就绪；Relay/Chronos 会对此失败关闭。

### 3.4 批量查询与质量状态

现有 `security_ids`、`limit`、`cursor` 能力应覆盖新增字段和可转债，保证策略启动时可以一次批量读取交易 universe 的价格规则。

建议在 `/v1/metadata/status` 增加价位规则质量摘要，至少包括：

- 目标业务日期和生成时间。
- 按 `stock/etf/convertible_bond` 分组的活动证券总数、已覆盖数和缺失数。
- `price_tick <= 0`、小数位不一致、重复或冲突规则数量。
- 整体 `ready/degraded` 状态；任一活动可交易证券缺失权威规则时不得报告全量 ready。

## 4. Schema 与兼容性

本需求是字段增加和证券类型扩展。请由 Meridian 按现有 schema 版本策略决定是否升级 `metadata_instrument.v1`；无论采用兼容追加还是新版本，都需要满足：

1. `meta.schema_version` 与每条记录的 `schema_version` 能识别包含价位规则的新契约。
2. API 文档、接口工作台、Python SDK 和生产响应使用同一字段名及类型。
3. 旧客户端忽略新增字段时仍能读取原有 stock/ETF/index 记录。
4. 扩展 `instrument_type` 后，不让旧查询的分页结果产生重复或遗漏。
5. Meridian Python SDK 只需原样暴露新增字段和可转债筛选，不需要实现 Chronos 的微元模型。

## 5. 验收样例

Meridian 完成后，Relay 将按以下口径验收：

1. 分别查询沪市主板、深市主板、创业板、北交所股票、沪深 ETF、沪深可转债，活动证券均返回完整价位字段。
2. 使用 `security_ids` 混合批量查询股票、ETF 和可转债，结果无缺失、重复或类型漂移。
3. `price_tick` 均为有限正数，`price_decimals` 为非负整数，且二者可共同执行整倍数校验。
4. 对每类证券各选择至少 3 个样本，使用交易所允许价、相邻一档价和半档非法价验证规则。
5. 将 `11.19999999` 一类 JSON/float 传输噪声通过 `Decimal(str(value))` 归一后，能稳定回到同一整数微元，并按 Meridian `price_tick` 判断是否合法。
6. 分页遍历活动证券全集，页间无重复、无遗漏，末页游标行为与现有契约一致。
7. 规则缺失或质量未就绪的定向 fixture 返回明确 degraded/缺口信息，下游不会获得伪造默认值。
8. Meridian API、SDK 文档、安装包版本和质量页同步更新。

## 6. Relay 后续动作

Meridian 契约上线后，Relay 将：

1. 继续通过现有 Meridian HTTP 薄代理原样返回字段，不复制证券主数据表，也不新增代码前缀规则。
2. 在 Relay API/SDK 文档中声明价格最多小数位和最小价位的唯一来源为 Meridian instruments。
3. 为 A 股、ETF、可转债增加 JSON 往返、整数微元和非法半档价格测试。
4. 在 Chronos 接入验收中对缺失或 degraded 价位元数据失败关闭。

## 7. 不在本需求范围

- 不要求 Meridian 接入 Relay 订单、成交、SSE 或幂等协议。
- 不要求 Meridian 返回券商费用、账户可用量或交易风控额度。
- 不要求增加 Level2 数据。
- 不要求 Meridian 使用整数微元替换现有行情 JSON number。
- 不要求 Meridian 承担订单价格校验；Meridian 只提供权威规则，校验由 Chronos 和 Relay 各自执行。

## 8. 请 Meridian 反馈

请反馈以下信息，Relay 据此安排 SDK 版本：

1. 采用的可转债 `instrument_type` 枚举值。
2. `price_tick` 的权威来源、覆盖范围和每日更新时间。
3. 是否同时提供 `price_decimals`、来源字段和规则日期。
4. schema 版本与兼容策略。
5. API、质量状态、Python SDK 和文档的预计上线版本。
