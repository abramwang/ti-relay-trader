# relay 数据模型与落盘设计

更新时间：`2026-06-14`

## 设计结论

relay 的最终账户交易数据、订单数据、成交数据、资金持仓快照和盘后对账结果需要落盘。当前优先将 PostgreSQL 作为最终账本候选。

基本原则：

1. Redis Stream 是前置通信和事件传输通道，不作为最终账本。
2. PostgreSQL 是订单、成交、事件、资产、持仓、资金流水和对账结果的审计存储。
3. 9092 API 返回的标准结构体要能追溯到 Redis Stream 消息和前置 C++ 结构体。
4. 未能标准化的前置字段先保存在 `raw_payload` 或 `adapter_context`，避免信息丢失。
5. 业务时间统一使用 `Asia/Shanghai`；交易日、盘前初始化、盘后结算、报表和页面展示都按东八区解释。
6. PostgreSQL 访问方式参考 `http://doc.quantstage.com`，仓库中不保存账号、密码、Token 或生产连接串。

## 设计参考源

| 来源 | 用途 |
| --- | --- |
| `docs/THIRD_PARTY_INTEGRATION_GUIDE.md` | Relay 与前置服务之间的 Redis Stream envelope、action、reply、event、heartbeat、DLQ 协议 |
| `/home/Titans/resource/include/ti_trader_struct.h` | 下单、撤单、订单状态、成交、持仓、账户资产等 C++ 结构体参考 |
| `/home/Titans/resource/include/ti_common_struct.h` | 交易所、证券代码、账户、订单号、文本字段长度参考 |
| `/home/Titans/resource/include/ti_trader_client.h` | 前置交易能力边界参考 |
| `/home/Titans/resource/include/ti_trader_callback.h` | 前置查询和事件回调类型参考 |
| `http://doc.quantstage.com` | PostgreSQL、MySQL、Redis 等内网资源访问方式 |

如果当前结构体字段不足，先在 relay 文档中记录缺口，再和前置程序一起扩展 Redis payload 或 `adapter_context`，保持向后兼容。

## C++ 交易结构体对应关系

| Relay 标准对象 | Redis 协议 | C++ 参考结构体 |
| --- | --- | --- |
| `OrderSubmitRequest` | `action=order.submit` | `TiReqOrderInsert` |
| `BatchOrderSubmitRequest` | `action=order.batch.submit` | `std::vector<TiReqOrderInsert>` / `orderInsertBatch` |
| `OrderCancelRequest` | `action=order.cancel` | `TiReqOrderDelete` |
| `Order` | `order.event.payload` / `order_page.items[]` | `TiRtnOrderStatus` / `TiRspQryOrder` |
| `Fill` | `fill.event.payload` / `fill_page.items[]` | `TiRtnOrderMatch` / `TiRspQryMatch` |
| `Position` | `position_page.items[]` | `TiRspQryPosition` |
| `AccountAsset` | `asset_page.account` | `TiRspAccountInfo` |

## 标准字段映射

### 订单请求

| Relay 字段 | Redis payload | C++ 字段 | 说明 |
| --- | --- | --- | --- |
| `account_id` | `account_id` | `szAccount` | 资金账户 |
| `client_order_id` | `client_order_id` / `req_id` | `szUseStr` 或扩展字段 | 本地客户端维护的当日唯一请求/委托 ID |
| `gateway_order_id` | `gateway_order_id` | `szUseStr` 或扩展字段 | Relay/前置之间的北向关联键，用于撤单和事件归属 |
| `symbol` | `symbol` | `szSymbol` | 证券代码 |
| `exchange` | `exchange` | `szExchange` | 交易所 |
| `trade_side` | `trade_side` | `nTradeSideType` | `B` 买入，`S` 卖出 |
| `offset_type` | `offset_type` | `nOffsetType` | A 股通常为 `C` |
| `business_type` | `business_type` | `nBusinessType` | `S` 表示二级市场证券买卖，股票和 ETF 二级市场买卖均使用 `S`；`E` 预留 ETF 申购/赎回专项，当前 relay API 未实现 |
| `price` | `price` | `nOrderPrice` | 委托价格 |
| `qty` | `qty` | `nOrderVol` | 委托数量 |
| `sent_at` | `sent_at` | `nReqTimestamp` | 调用方发送时间 |

### 订单状态

| Relay 字段 | Redis event/reply | C++ 字段 | 说明 |
| --- | --- | --- | --- |
| `gateway_order_id` | `gateway_order_id` | `szUseStr` 或扩展字段 | 跨系统订单主键 |
| `adapter_order_id` | `order_id` | `nOrderId` | 前置系统或券商柜台侧当日唯一订单编号 |
| `order_stream_id` | `order_stream_id` | `szOrderStreamId` | 交易所当日唯一委托流号 |
| `submitted_qty` | `order_qty` / `submit_vol` | `nSubmitVol` | 提交申报数量 |
| `filled_qty` | `cum_filled_qty` | `nDealtVol` | 累计成交数量 |
| `cancelled_qty` | `withdrawn_vol` | `nTotalWithDrawnVol` | 累计撤单数量 |
| `invalid_qty` | `invalid_vol` | `nInValid` | 废单数量 |
| `gateway_status` | `gateway_status` | `nStatus` | 前置标准状态 |
| `accepted_at` | `accepted_at` | `nInsertTimestamp` | 接受时间 |
| `updated_at` | `updated_at` | `nLastUpdateTimestamp` | 最后更新时间 |
| `fee` | `fee` | `nFee` | 手续费 |
| `shareholder_id` | `shareholder_id` | `szShareholderId` | 股东代码 |

### 成交

| Relay 字段 | Redis event/query | C++ 字段 | 说明 |
| --- | --- | --- | --- |
| `fill_id` | `fill_id` | `szStreamId` | 成交编号；部分柜台/模拟柜台可能在不同订单间复用，需结合订单作用域去重 |
| `gateway_order_id` | `gateway_order_id` | `szUseStr` 或扩展字段 | 归属订单 |
| `adapter_order_id` | `order_id` | `nOrderId` | 前置/柜台侧订单编号 |
| `order_stream_id` | `order_stream_id` | `szOrderStreamId` | 委托编号 |
| `price` | `price` | `nMatchPrice` | 成交价 |
| `qty` | `qty` | `nMatchVol` | 成交量 |
| `fee` | `fee` | `nFee` | 手续费 |
| `match_timestamp` | `match_timestamp` | `nMatchTimestamp` | 成交时间 |
| `trade_side` | `trade_side` | `nTradeSideType` | 买卖方向 |
| `shareholder_id` | `shareholder_id` | `szShareholderId` | 股东代码 |

成交回报同样携带订单关联字段。页面和对账逻辑应优先通过 `gateway_order_id` 关联订单主表，再用 `order_id`、`order_stream_id` 做柜台和交易所口径交叉校验。`fill_id` 或 `adapter_context.match_stream_id` 不能假设为账户级全局唯一，relay 按 `account_id + gateway_order_id + fill_id` 处理成交幂等。

## PostgreSQL 首批表

首版 DDL 已落在：

```text
migrations/postgres/000001_init_ledger.up.sql
migrations/postgres/000001_init_ledger.down.sql
```

执行说明见 [docs/MIGRATIONS.md](/home/ti-relay-trader/docs/MIGRATIONS.md:1)。

当前已在 `internal/ledger` 增加首批 Go repository，作为 Redis Stream 消费、API 写入和后续对账任务进入 PostgreSQL 的统一边界。已覆盖：

1. `accounts` 的账户 upsert。
2. `orders` 的订单 upsert。
3. `order_events` 的事件追加和重复事件幂等处理。
4. `fills` 的成交写入和重复成交幂等处理。
5. `raw_stream_messages` 的原始 Redis 消息归档与重放审计。

### 配置与路由

| 表 | 说明 |
| --- | --- |
| `accounts` | 账户配置、账户状态、是否可交易、是否模拟账户 |
| `gateways` | broker、gateway、env、stream prefix、心跳状态 |
| `account_gateway_routes` | `account_id` 到 `broker_id + gateway_id + stream_prefix` 的路由 |

### 交易账本

| 表 | 说明 |
| --- | --- |
| `orders` | 标准订单主表，以 `account_id + gateway_order_id` 做唯一约束 |
| `order_events` | 订单状态事件流水，保存每次状态变化 |
| `fills` | 成交流水，以 `account_id + gateway_order_id + fill_id` 或 fallback 组合键去重 |
| `raw_stream_messages` | Redis 原始输入输出消息归档，用于审计和重放 |

### 账户账表

| 表 | 说明 |
| --- | --- |
| `asset_snapshots` | 账户资产快照，来自柜台查询和盘后结算 |
| `positions` | 当前持仓 |
| `position_snapshots` | 日终持仓快照 |
| `cash_ledger` | 资金流水，记录冻结、解冻、成交扣款、费用、结算 |

### 盘后对账

| 表 | 说明 |
| --- | --- |
| `reconciliation_runs` | 对账批次 |
| `reconciliation_breaks` | 对账差异 |
| `reconciliation_inputs` | 对账输入快照和来源信息 |

当前第一版 `post_close_settlement` 会通过 9092 `POST /v1/settlements/snapshots` 写入：

- `reconciliation_inputs`：按账户记录 relay 标准账本摘要、PnL 输入摘要、Redis raw stream 窗口摘要和柜台查询摘要。
- `reconciliation_breaks`：按账户记录未终态订单、订单成交数量不一致、资产/持仓快照缺失和账户刷新失败。
- `GET /v1/reconciliations/breaks`：按 `run_id/account_id/status` 查询待复核差异。

## 关键约束

1. `orders.account_id + orders.gateway_order_id` 必须唯一。
2. `fills.account_id + fills.gateway_order_id + fills.fill_id` 优先唯一；缺少稳定 `fill_id` 时使用 `order_stream_id + match_timestamp + qty + price` 去重。
3. `order_events` 不覆盖历史，只追加事件。
4. `raw_stream_messages` 保留 Redis stream key、stream id、direction、body 和解析状态。
5. 所有交易金额字段优先使用数据库 `numeric`，避免浮点误差进入最终账本。
6. 所有接口时间进入数据库时统一为带时区时间，原始时间戳保留在 raw 字段。
7. `reconciliation_inputs` 和 `reconciliation_breaks` 通过唯一索引保证同一 `run_id` 重复执行时可幂等覆盖。
7. 业务展示、API 输出和报表按 `Asia/Shanghai` 转换；数据库 `timestamptz` 仍保存绝对时刻。
8. `trade_date` 必须按 `Asia/Shanghai` 下的 A 股交易日确定，交易日判断和最近交易日回退以 Meridian 交易日接口为准。

## 交易日与时间字段

时间字段建议分层处理：

| 字段类型 | 存储建议 | 业务解释 |
| --- | --- | --- |
| 事件发生时间 | `timestamptz` | 前置、柜台或 relay 记录的绝对时刻，展示时转为 `Asia/Shanghai` |
| `trade_date` | `date` | A 股业务交易日，按 `Asia/Shanghai` 和 Meridian 交易日接口确定 |
| 原始时间戳 | `raw_payload` / `adapter_context` | 保留上游原样字段，不做破坏性转换 |
| 任务运行时间 | `timestamptz` | 盘前初始化、收盘后结算、对账和 PnL 任务的开始/结束时间 |

盘前初始化和收盘后结算需要在任务结果中记录：

1. `target_trade_date`。
2. `timezone`，固定为 `Asia/Shanghai`。
3. `started_at`、`finished_at`。
4. 触发方式：cron、manual、retry 或 backfill。
5. 账户范围和任务结果摘要。

## 配置与密钥

PostgreSQL 连接信息来源：

1. 本地开发和部署环境优先从 `RELAY_CONFIG_PATH` 指向的配置文件读取。
2. 配置文件中可以包含 PostgreSQL、Redis、账户路由等真实凭据，但该文件必须留在部署机本地。
3. 连接方式和授权信息查阅 `http://doc.quantstage.com`。
4. 仓库只保存配置模板，不保存真实密码、Token、生产账号或生产连接串。

建议后续增加：

- `config/relay.example.yaml`
- `config/relay.local.yaml`
- `config/relay.prod.yaml`
- `RELAY_DATABASE_URL`
- `RELAY_REDIS_URL`
- `RELAY_CONFIG_PATH`

## 与前置程序协作规则

如果 relay 发现现有 Redis payload 或 C++ 结构体无法支持标准字段，按以下流程推进：

1. 在本文件记录字段缺口、来源、影响范围和建议字段名。
2. 优先通过 Redis payload 增加可选字段或 `adapter_context` 扩展。
3. 保持旧字段兼容，不破坏已有验收脚本。
4. 前置程序完成扩展后，relay 增加 schema 和落盘字段。
5. 增加回放和对账测试，确认历史事件仍可解析。

## 已发现字段缺口

### `order.event.payload`

本轮真实 Redis 联调已确认，当前 `order.event` 可以归档到 `raw_stream_messages`，但部分历史事件缺少订单主表重建所需字段：

| 缺口字段 | 影响 | 建议 |
| --- | --- | --- |
| `trade_side` | 无法满足 `orders.trade_side` 枚举约束 | 前置在 `order.event.payload` 中补充买卖方向 |
| `business_type` | 无法满足 `orders.business_type` 枚举约束 | 前置在 `order.event.payload` 中补充证券业务类型 |

短期处理：

1. 对无订单草稿的历史缺字段事件，`relayctl ledger-sync` 只归档 raw，并在报告中记录无法重建主表的原因。
2. 9092 下单 API 写入 Redis 命令前会先写订单草稿；事件回流后可基于本地草稿更新订单主表状态并追加事件。
3. 测试 Redis 已验证一笔 API 下单可从草稿更新到 `filled/filled`，并落盘订单事件和成交。

长期处理：

1. 前置事件 payload 补齐 `trade_side` 和 `business_type`。
2. relay 增加回放测试，确认历史事件 raw 不丢失，新事件可直接重建 `orders/order_events`。
