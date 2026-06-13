# relay 数据模型与落盘设计

更新时间：`2026-06-13`

## 设计结论

relay 的最终账户交易数据、订单数据、成交数据、资金持仓快照和盘后对账结果需要落盘。当前优先将 PostgreSQL 作为最终账本候选。

基本原则：

1. Redis Stream 是前置通信和事件传输通道，不作为最终账本。
2. PostgreSQL 是订单、成交、事件、资产、持仓、资金流水和对账结果的审计存储。
3. 9092 API 返回的标准结构体要能追溯到 Redis Stream 消息和前置 C++ 结构体。
4. 未能标准化的前置字段先保存在 `raw_payload` 或 `adapter_context`，避免信息丢失。
5. PostgreSQL 访问方式参考 `http://doc.quantstage.com`，仓库中不保存账号、密码、Token 或生产连接串。

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
| `client_order_id` | `client_order_id` | `szUseStr` 或扩展字段 | 调用方订单号 |
| `gateway_order_id` | `gateway_order_id` | `szUseStr` 或扩展字段 | Relay/前置之间的北向订单主键 |
| `symbol` | `symbol` | `szSymbol` | 证券代码 |
| `exchange` | `exchange` | `szExchange` | 交易所 |
| `trade_side` | `trade_side` | `nTradeSideType` | `B` 买入，`S` 卖出 |
| `offset_type` | `offset_type` | `nOffsetType` | A 股通常为 `C` |
| `business_type` | `business_type` | `nBusinessType` | 股票 `S`，ETF `E` |
| `price` | `price` | `nOrderPrice` | 委托价格 |
| `qty` | `qty` | `nOrderVol` | 委托数量 |
| `sent_at` | `sent_at` | `nReqTimestamp` | 调用方发送时间 |

### 订单状态

| Relay 字段 | Redis event/reply | C++ 字段 | 说明 |
| --- | --- | --- | --- |
| `gateway_order_id` | `gateway_order_id` | `szUseStr` 或扩展字段 | 跨系统订单主键 |
| `adapter_order_id` | `order_id` | `nOrderId` | 前置/柜台侧订单编号 |
| `order_stream_id` | `order_stream_id` | `szOrderStreamId` | 柜台或交易所委托编号 |
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
| `fill_id` | `fill_id` | `szStreamId` | 成交编号，优先用于去重 |
| `gateway_order_id` | `gateway_order_id` | `szUseStr` 或扩展字段 | 归属订单 |
| `adapter_order_id` | `order_id` | `nOrderId` | 前置/柜台侧订单编号 |
| `order_stream_id` | `order_stream_id` | `szOrderStreamId` | 委托编号 |
| `price` | `price` | `nMatchPrice` | 成交价 |
| `qty` | `qty` | `nMatchVol` | 成交量 |
| `fee` | `fee` | `nFee` | 手续费 |
| `match_timestamp` | `match_timestamp` | `nMatchTimestamp` | 成交时间 |
| `trade_side` | `trade_side` | `nTradeSideType` | 买卖方向 |
| `shareholder_id` | `shareholder_id` | `szShareholderId` | 股东代码 |

## PostgreSQL 首批表

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
| `fills` | 成交流水，以 `account_id + fill_id` 或 fallback 组合键去重 |
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

## 关键约束

1. `orders.account_id + orders.gateway_order_id` 必须唯一。
2. `fills.account_id + fills.fill_id` 优先唯一；缺少稳定 `fill_id` 时使用 `order_stream_id + match_timestamp + qty + price` 去重。
3. `order_events` 不覆盖历史，只追加事件。
4. `raw_stream_messages` 保留 Redis stream key、stream id、direction、body 和解析状态。
5. 所有交易金额字段优先使用数据库 `numeric`，避免浮点误差进入最终账本。
6. 所有接口时间进入数据库时统一为带时区时间，原始时间戳保留在 raw 字段。

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
