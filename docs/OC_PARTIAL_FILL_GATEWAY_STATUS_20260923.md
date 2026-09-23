# OC 部分成交 gateway_status 契约偏差

日期：`2026-09-23`

环境：生产，只读排查；Relay 下单权限关闭

## 现象

OC 在部分成交阶段发送了：

```json
{
  "gateway_status": "partially_filled",
  "atlas_status": "partially_filled",
  "is_terminal": false,
  "cum_filled_qty": 400,
  "leaves_qty": 200,
  "adapter_status": "dealt"
}
```

`relay.stream.v1` 当前只允许 `gateway_status=accepted|working|filled|cancelled|rejected`。部分成交属于仍在工作的柜台状态，标准表达应为：

```json
{
  "gateway_status": "working",
  "atlas_status": "partially_filled",
  "is_terminal": false,
  "cum_filled_qty": 400,
  "leaves_qty": 200
}
```

华鑫原始 `dealt`、状态码和成交数量继续保留在 `adapter_context`，不需要丢弃。

## 生产证据

`2026-09-23` 已观察到三户共 `975` 条该类事件：

| Relay 账户 | 条数 | 东八区时间范围 |
| --- | ---: | --- |
| `307000051387` | 481 | `09:25:00..09:46:24` |
| `314000045768` | 299 | `09:25:00..09:34:50` |
| `314000046830` | 195 | `09:30:01..10:03:45` |

代表订单 `external-huaxin-31400004683001-12002A620001744` 的生命周期为：

1. `accepted/new, 0/600`
2. `working/routed, 0/600`
3. `partially_filled/partially_filled, 400/600`
4. `filled/filled, 600/600`

第三步因 `orders_gateway_status_check` 被拒绝写入，原始 Stream 报文仍完整归档；第四步和两条合计 `600` 股的真实成交均已正确落库，订单最终为 `filled, cum_filled_qty=600, leaves_qty=0, is_terminal=true`。因此本例最终账本没有缺失，但盘中部分成交状态不能及时进入标准订单账本。若部分成交后长时间未终态，页面和策略端会暂时停留在旧的 working 数量。

当前该事件恰好是 event Stream 最近批次中的最后一个账本错误，因此 `/v1/status` 显示 `stream_runtime=attention`、整体 `degraded`；Stream `lag=0`、pending DLQ 为 0，Redis、数据库、worker、事件桥和四户 OC 心跳均正常。

## OC 修正要求

1. 部分成交时固定输出 `gateway_status=working`。
2. 业务细分状态继续输出 `atlas_status=partially_filled`。
3. 保持 `is_terminal=false`、`0 < cum_filled_qty < order_qty`、`leaves_qty=order_qty-cum_filled_qty-invalid_qty-withdrawn_qty`。
4. 全部成交后再输出 `gateway_status=filled`、`atlas_status=filled`、`is_terminal=true`、`leaves_qty=0`。
5. 不修改订单三类 ID、交易日、时间、`adapter_context` 或成交事件规则。

Relay 不新增 `gateway_status=partially_filled` 枚举，也不静默改写原始报文。若 OC 希望扩展 wire schema，需要先更新双方协议、数据库约束、SDK 枚举和兼容通知。

## 联合验收

1. 选择可产生拆分成交的订单，确认中间事件为 `working/partially_filled`。
2. Relay 标准订单实时更新 `cum_filled_qty/leaves_qty`，且不产生 constraint error。
3. 最终订单数量与唯一真实成交求和一致。
4. 四户 event Stream 均为 `lag=0/status=ok`，没有新增 DLQ 或 ledger error。

