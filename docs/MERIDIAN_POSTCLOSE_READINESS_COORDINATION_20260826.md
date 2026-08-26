# Relay / Meridian 盘后行情就绪协调说明

更新时间：`2026-08-26`，业务时区：`Asia/Shanghai`。

## 结论

本轮问题不是 Meridian Python SDK 升级导致。Relay 核心绩效链路使用 Go HTTP 客户端，没有加载 `meridian-data-sdk`；Meridian `0.1.26` 主要收紧 canonical 日频特征和水位语义，不要求 Relay 更新 SDK。

直接原因是双方契约演进后的控制流不兼容：Meridian 对尚未发布的交易日日线从旧的空 `200` 收紧为 `503 archive_incomplete`，Relay 却在 HTTP 错误分支直接结束当前批次，未执行已经存在的当日 Level1 降级。Relay 已修改为保留 `meridian_daily_bars_unavailable` 证据并继续尝试当日 Level1；历史日期仍严格要求日线分区，不使用当前快照补历史。

## 2026-08-26 现场证据

| 东八区时间 | Meridian 状态 | Relay 应采取的动作 |
| --- | --- | --- |
| `15:01` | 当日日线尚未发布 | `post_close_capture` 独立查询并固化 OC 最终资金持仓，不依赖 Meridian |
| `15:35-15:54` | Level1 归档与盘后质量任务运行 | 等待机器状态，不按固定分钟猜测完成 |
| `15:54:08` | `meridian-level1-archive=success` | 当日 Level1 可作为临时收盘估值输入 |
| `15:54:20` | `meridian-intraday-quality=success` | 允许发布带 `meridian_level1_close_fallback` 的 provisional 绩效 |
| `15:56` | 当日 `1d` 返回 `503 archive_incomplete` | 这是正确的未就绪响应，不应改为空 `200` |
| `16:30-16:45` | `meridian-postclose-reference-sync` 生产日线并执行水位门禁 | 水位到达当日后重算 canonical 绩效 |

现场批量请求 8 个持仓证券的 `data_scope=realtime&market_level=level1` 返回 8/8，`trade_date=20260826`，快照时间均在 `15:00:00-15:00:10+08:00`，`last/pre_close` 完整。相同日期的 `frequency=1d` 在父任务运行前返回：

```json
{
  "error": {
    "code": "archive_incomplete",
    "message": "日线请求范围存在未归档交易日分区"
  },
  "meta": {
    "schema_version": "market_bar.v1",
    "data_scope": "historical"
  }
}
```

## 双方职责

Relay：

1. `post_close_capture` 永远不等待行情，先保存不可变 `broker_close` 资金持仓。
2. 当前交易日 `1d` 为 `archive_incomplete` 时，使用批量 Level1 realtime 的 `pre_close/last` 补齐估值，保留日线未就绪和 Level1 来源标记。
3. Level1 必须满足目标 `trade_date`、每个请求证券唯一、价格大于零；缺任一正持仓价格仍阻断该账户。
4. 历史日期只读取 `security_ids + start_date/end_date + frequency=1d + adjustment=none`，禁止 Level1 当前快照降级。
5. Meridian canonical 日线水位到达目标日后，对当天 provisional 结果重新计算；日线与 Level1 收盘存在超容差差异时保留新版本并告警，不静默覆盖审计证据。

Meridian：

1. 保持 `503 archive_incomplete` fail-closed 语义，不为下游兼容返回伪造日线或空 `200`。
2. 稳定提供 `/v1/realtime/status` 的 `realtime_status.v1`，其中 Level1 归档和盘后质量步骤必须包含 `status/evidence_trade_date/finished_at`。
3. 稳定提供 `/v1/quality/postclose-reference` 的 `postclose_reference_watermark.v1`，以 `target_trade_date/status` 及六组日线的 `published_watermark` 作为 canonical 就绪门禁。
4. 若未来更改归档时间、错误码、状态 schema、`data_scope=realtime` 最新快照语义或批量 selector 行为，提前提供 compatibility notice 和回归样例。
5. 建议增加一个面向下游的精简就绪对象，避免每个项目解析 23 步任务计划：

```json
{
  "trade_date": 20260826,
  "close_snapshot": {
    "status": "ready",
    "source": "level1_archive",
    "finished_at": "2026-08-26T15:54:20+08:00"
  },
  "canonical_daily": {
    "status": "pending",
    "published_watermark": 20260825,
    "next_expected_at": "2026-08-26T16:30:00+08:00"
  }
}
```

该精简对象可以是现有状态响应中的稳定子对象，不强制增加新 URL。

## Relay 读取门禁

临时收盘估值就绪条件：

1. `/v1/realtime/status.meta.schema_version == realtime_status.v1`。
2. `meridian-level1-archive` 与 `meridian-intraday-quality` 的 `status == success`。
3. 两个步骤的 `evidence_trade_date == target_trade_date`。
4. 目标证券批量快照 100% 返回，行内 `trade_date` 匹配，`pre_close/last > 0`。

权威日线就绪条件：

1. `/v1/quality/postclose-reference.meta.schema_version == postclose_reference_watermark.v1`。
2. `data.target_trade_date == target_trade_date` 且 `data.status == ready`。
3. `daily_bar_stock_none`、`daily_bar_etf_none`、`daily_bar_index_none` 均为 `ready`，且 `published_watermark >= target_trade_date`。
4. 实际日线查询返回 `200`、`archive_complete=true`，所有持仓与基准证券覆盖完整。

## 联合验收矩阵

| 场景 | Meridian | Relay 预期 |
| --- | --- | --- |
| 当日日线未发布、Level1 已就绪 | bars `503`，snapshots `200` | provisional 可计算，记录双质量标记 |
| 当日日线和 Level1 均未就绪 | bars `503`，snapshots 不完整 | 账户 blocked，不发布 NAV |
| 当日日线水位到达 | bars `200` 且完整 | 使用 `meridian_1d_pre_close_and_close` 重算 canonical 输入并保留旧版本 |
| 历史日线缺分区 | bars `503` | blocked，绝不读取当前 Level1 |
| 非交易日 | 交易日接口明确非交易日 | 日任务跳过，行情回退最近交易日仅用于页面查询 |
| schema 或错误码变化 | compatibility notice | 双方 fixture 回归通过后部署 |

## 后续动作

1. [x] Relay 部署 503 降级修复，并使用当日 Level1 恢复 `2026-08-26` provisional 绩效。
2. [x] Relay 实现 `performance_canonical`：16:00-17:59 每 10 分钟读取权威水位，复用同一交易日账本生成新 NAV 版本，保存版本差异并保证同日幂等。
3. [x] 完成 `2026-08-26` 首次真实水位到达验收：16:30 父任务运行时 Relay 保持等待，16:32:59 三类水位到达后生成 3 个活跃账户的权威日线 NAV v2；3 户 NAV/PnL/收益率差异均为 0，质量结果为 3 ready、1 not_applicable、0 attention/blocked。
4. [ ] 将本文发给 Meridian，确认现有两个状态接口字段是否承诺稳定；如不能承诺，按精简对象补契约。
5. [x] Relay 单测固定 `503 -> Level1 provisional -> daily ready` 的关键门禁和等待/完成/幂等状态；Meridian 侧仍需保留对应契约 fixture。
