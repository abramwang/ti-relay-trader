# Relay / Chronos 对 Meridian 证券价位元数据的需求与验收

- 提交方：Relay Trader、Chronos Strategy
- 接收方：Meridian Data
- 日期：`2026-09-02`
- 状态：已完成并通过生产只读验收
- Meridian 契约：SDK `0.1.28`、`metadata_instrument.v2`、`metadata_status.v2`

## 1. 背景

Chronos 通过 Relay Python SDK 构建策略执行链路。价格仍以 JSON number / Python `float` 通过接口传输，策略进程可使用 `Decimal(str(value))` 归一为整数微元；判断价格是否可报所需的最小价位必须来自 Meridian，Relay 不根据证券代码前缀或本地枚举猜测规则。

Meridian 上线前，`metadata_instrument.v1` 缺少可转债覆盖和 `price_tick/price_decimals`，因此本需求曾作为 Chronos 价格契约的 P0 阻塞。该缺口现已关闭。

## 2. 已上线契约

`GET /v1/metadata/instruments` 当前支持：

- `instrument_type=stock|etf|index|convertible_bond`。
- `security_id/security_ids/instrument_type/exchange/status/limit/cursor` 查询和分页。
- 活动股票、ETF、可转债的 `price_tick`、`price_decimals`、`price_tick_source`、`price_tick_as_of_date`。
- 记录及响应 `meta.schema_version=metadata_instrument.v2`。

价位字段语义：

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `price_tick` | JSON number | 权威最小报价增量，活动可交易证券必须为有限正数 |
| `price_decimals` | integer | 规范展示小数位；有效性仍以 `price_tick` 整倍数为准 |
| `price_tick_source` | string | 权威来源，当前为 `rqdatac.Instrument.tick_size` |
| `price_tick_as_of_date` | `YYYYMMDD` integer | 按 `Asia/Shanghai` 解释的规则确认日期 |

当前品种规则由 Meridian 提供，Relay 只消费：

| 类型 | `price_tick` | `price_decimals` |
| --- | ---: | ---: |
| `stock` | `0.01` | `2` |
| `etf` | `0.001` | `3` |
| `convertible_bond` | `0.001` | `3` |
| `index` | `0.01` | `2` |

P0 契约提供当前确认规则和业务日期，不提供历史有效期区间。需要重放旧策略运行清单时，调用方应保存当时读取的规则快照。

## 3. 质量状态

`GET /v1/metadata/status` 已升级为 `metadata_status.v2`，其中 `price_tick_quality` 使用 `active_total/covered_count/missing_count/invalid_tick_count/decimal_mismatch_count/duplicate_count/conflict_count` 以及 `groups` 提供质量统计。规则缺失或冲突时下游必须失败关闭，不得生成默认价位。

## 4. 生产验收结果

`2026-09-02` 对 Meridian 生产接口完成只读验收：

1. 混合查询 `600000.SH`、`510300.SH`、`110075.SH`，分别返回 `stock`、`etf`、`convertible_bond` 以及完整价位来源和规则日期。
2. 股票返回 `0.01 / 2`，ETF 和可转债返回 `0.001 / 3`，响应 schema 为 `metadata_instrument.v2`。
3. `metadata_status.v2.price_tick_quality.status=ready`。
4. 沪深活动证券覆盖 `7187/7187`：股票 `5215/5215`、ETF `1653/1653`、可转债 `319/319`。
5. 缺失、非法价位、小数位不一致、重复和冲突计数均为 `0`。
6. Meridian SDK `0.1.28` 已提供 `get_instruments()`、`get_instruments_df()` 和 `get_metadata_status()`。

Relay 随后增加 `/v1/meridian/metadata/status` 透明代理，并在 `relay-sdk 0.1.29` 增加 `get_meridian_instruments()`、`get_meridian_metadata_status()`。Relay 服务端继续使用 Go HTTP 薄客户端，不引入 Meridian Python SDK 运行时依赖。

## 5. 当前边界

- 当前生产范围仅为上海、深圳证券交易所。
- 实际交易账户未开通北交所权限，本轮 API Console、交易终端和验收样例不承诺北交所支持。
- 北交所作为未来升级路径；开通前需由 Meridian 提供独立权威目录和价位质量覆盖，再补 Relay/Chronos 契约测试。
- 不要求 Meridian 接入订单、成交、账户额度、费用、风控或 Level2 数据。
- 不要求 Meridian 使用整数微元替换现有行情 JSON number，也不要求 Meridian 承担订单价格校验。

## 6. 下游使用约束

1. Relay 透明保留 Meridian `data/meta/error` 和价位字段，不复制证券主数据表。
2. 交易终端使用 `price_decimals` 展示价格、使用 `price_tick` 设置价格输入步长；主数据刷新时保留这些字段。
3. Python 调用方使用 `Decimal(str(price_tick))`，不要直接从二进制浮点构造 `Decimal`。
4. 缺失价位或质量状态非 `ready` 时，Chronos 的价格合法性检查应失败关闭。
5. Relay 不把当前品种规则固化成独立业务标准；规则变化由 Meridian 契约和质量状态驱动。
