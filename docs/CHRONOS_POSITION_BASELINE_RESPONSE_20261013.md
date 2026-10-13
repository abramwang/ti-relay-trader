# Chronos 历史收盘持仓基线 P0 回执

日期：2026-10-13，业务时间 `Asia/Shanghai`。对应需求：`/home/ti-chronos-strategy/docs/RELAY-POSITION-BASELINE-REQUIREMENTS-20261011.md`。

## 能力与边界

SDK `0.1.43` 新增完整历史 `close` 数量基线：

```text
GET /v1/accounts/{account_id}/positions/baseline
GET /v1/schema -> capabilities: positions.close_baseline.v1
```

仅读 PostgreSQL 已归档的 OC 回报、账户收盘捕获报告和历史快照，不调用 OC 或 Meridian，不发布 Redis 命令，不依赖当前柜台登录、账户启用状态或下单权限。停用账户的可信历史仍可读取。没有历史时不回退当前持仓。

这是指定历史时点的全账户证券数量证明，不是当前可卖量、资金可用性、成本/NAV 正确性或交易授权。`execution_ready` 始终为 `false`。当前时点的原子基线尚未实现。生产下单保持关闭。

## 请求与响应

| 参数 | 约束 |
| --- | --- |
| `account_id` | 路径参数，必须存在于当前服务配置 |
| `trade_date` | 必填，`YYYYMMDD` 或 `YYYY-MM-DD`，回显为后者 |
| `snapshot_type` | 仅支持 `close`，默认 `close` |
| `limit` | 默认 500，范围 1..500，越界明确 400 |
| `baseline_id` | 可选固定引用；后续页应传首页返回值 |
| `cursor` | 不透明的代际绑定游标，不得自行拼接 offset |

不允许证券过滤、日期范围、重复参数或未知参数，以免把子集误认成全账户。

标准 envelope 保留 `request_id/time`，`data` 为 `PositionBaselinePage`：`query` 回显环境、账户、券商、网关、日期、类型、请求基线 ID、游标和 limit；`baseline` 为所有页共享的身份与完整性证据；`positions` 按 `security_id` 字典序稳定排列；`count/offset/next_cursor/page_complete` 为分页证据。`page_complete` 仅表示到达末页，不是完整账户证明。

`baseline` 关键字段：

| 字段 | 语义 |
| --- | --- |
| `schema_version` | `position_baseline.v1` |
| `baseline_id` | `pb-` + SHA256，绑定作用域、数量摘要、来源运行、时间和完成证明，不依赖本次 GET 时间 |
| `environment/account_id/broker_id/gateway_id` | 固定范围，环境为 `production` 或 `test` |
| `trade_date/snapshot_type` | 历史业务日期和 `close` |
| `completeness` | `complete`、`partial`、`unavailable` |
| `missing_security_means_zero` | 只有 `complete` 才为 true |
| `execution_ready` | 永远 false |
| `total_rows/max_rows` | 基线行数、硬上限 100000；超限 422，不返回截断结果 |
| `content_sha256/digest_format` | 全部数量行摘要和 `position_baseline_rows.v1` |
| `source_run_id/capture_run_id` | close 发布运行和 broker_close 捕获运行 |
| `position_query_message_id/asset_query_message_id` | 各自独立成功的 OC 查询原命令 |
| `counter_session_id` | 源回报提供时保留，不从当前 heartbeat 推导 |
| `source_broker_account_id` | 可选柜台原生资金账户审计值；标准身份由 routing 确定，不猜账户后缀 |
| `collection_started_at/collection_completed_at` | 采集起点、资金/持仓最后成功末页的本地归档时间 |
| `business_as_of` | broker_close 捕获时点；生产收盘要求目标日 15:00 之后 |
| `generated_at` | close 发布完成时间，不是读取时间 |
| `proof/quality_flags` | 各项证明及未闭合原因 |

行字段为 `security_id/symbol/exchange/quantity/sellable_qty/source_updated_at`。数量使用 OC 真实证券数量，完整基线只保留正持仓；源时间使用 position page 的 `produced_at` 并转换为东八区。历史可卖量不保证下一交易日可卖量，不推断交收、企业行动或后续成交。

`complete && total_rows=0` 才能证明真实空账户；没有历史记录返回 `unavailable`，有历史但证据不全返回 `partial`。后两者即使返回空页也不能补零。

## 完整性来源

以下八项必须全部 true，且 `quality_flags=[]`：

1. `close_snapshot_published`：目标账户 close 资产和全部持仓写入计数匹配，来源 broker_close，发布完成。
2. `broker_snapshot_published`：独立捕获运行的资产及全部持仓写入完成。
3. `asset_query_completed`：账户资产查询独立成功且唯一末页，无矛盾终态。
4. `positions_query_completed`：账户全量持仓查询独立成功且唯一末页。
5. `archive_pages_closed`：持仓原始页数量与报告一致，范围/命令身份一致，无解析错误、重复消息/证券、缺页或非法末页。
6. `asset_archive_closed`：真实资产成功末页存在，且对应正确范围、原命令及采集时间窗口。
7. `snapshot_matches_full_query`：close 和 broker_close 的证券集合、数量、可卖量、运行及捕获时间均与完整 OC 查询一致。
8. `stale_rows_excluded`：完整 OC 集合与两个快照逐项核对，无额外旧行或遗漏证券。

其他账户失败不使本账户自动失败。仅资金写入、部分页成功、任务摘要正常、当前表清理或重复 GET 相同，都不足以证明完整。

`counter_session_provided` 是额外审计项。旧归档全部回报均无 session 时，字段省略并明确标为 false；若部分回报提供而其他缺失，或提供值互相冲突，则基线为 partial。不补造历史 session。

## 代际、分页与摘要

每次 GET 在只读 `REPEATABLE READ` 事务中读取来源。首页 ID 固定本轮代际，后续页固定该 ID，SDK 自动处理。日期/类型被重建或来源证据改变时，旧引用返回 `409 POSITION_BASELINE_CHANGED`；调用方丢弃本轮所有页，从首页重新读取，不自动重试旧页或混合新旧集合。

P0 不额外保留被覆盖的历史代际；固定引用在来源未变时可反复读取，变更后明确失败。这是需求允许的“代际变化失败重读”模式，不是永久保留所有旧代际。

摘要输入为按 `security_id` 排序的全部行数组，每行严格使用：

```text
[security_id, symbol, exchange, quantity, sellable_qty, source_updated_at]
```

对紧凑 UTF-8 JSON（无额外空格）计算 SHA256；数量为 JSON 整数，空集合为 `[]`，标准证券身份及源时间使用 ASCII 字段，不包含名称、价格或浮点估值。SDK 收齐后复核摘要、总数、逐页元数据、连续 offset、证券单调顺序和游标推进，失败不返回半成品。

持仓上限 100000，单批原始回报验证上限 250000；超限明确 `POSITION_BASELINE_TOO_LARGE`。盘后持仓采集也改为分页收齐，不再静默截取前 2000 行，采集失败/重复/超限不得发布成功快照。

## SDK 使用

```bash
python -m pip install "http://relay-trader.quantstage.com/sdk/relay-sdk-0.1.43.tar.gz"
```

```python
from relay_sdk import RelayClient

client = RelayClient(account_id="307000051387")
client.require_capabilities("positions.close_baseline.v1")
snapshot = client.get_position_baseline(
    trade_date="20261009", expected_environment="production", page_size=500,
)
print(snapshot.baseline.baseline_id, snapshot.baseline.total_rows)
for row in snapshot.positions:
    print(row.security_id, row.quantity, row.sellable_qty)
for page in snapshot.pages:
    print(page.request_id, page.time, page.offset, page.query, page.raw)
```

公开模型为 `PositionBaseline/PositionBaselineRow/PositionBaselinePage/PositionBaselineSnapshot`；全部来源字段在 `baseline.raw`，HTTP 页在 `snapshot.pages`。

`get_position_baseline_page(..., limit=500, require_complete=True)` 可用于逐页审计；只在查看质量原因时设置 `require_complete=False`。完整读取 `get_position_baseline(..., max_pages=1000, max_rows=100000)` 不允许放宽完整性。缺数/部分状态抛 `RelayPaginationError(code="POSITION_BASELINE_INCOMPLETE")`，HTTP 409 同样映射为保留原始回执的 `RelayPaginationError`。

SDK 完整读取成功后可计算历史参考 `delta_qty=target_qty-quantity`；仅此时允许把基线未列证券当零。正式当前交易仍需独立核对现金、可卖量、在途订单、租约和交易权限。

## 验收与后续

- 后端/SDK 覆盖完整/空、缺历史、部分查询、旧行、重复/缺页、计数与账户/环境/session 变化、代际变更失败。
- 边界覆盖单页、2001、10001 行和 100001 超限。独立 PostgreSQL TEMP 表验证 10001 行完整跨页/摘要及同日重建失败，永久账表未写入。
- 生产 GET-only 使用 2026-10-09 四个启用账户：13、1、7、1 行；真实空账户为已停用 `307000051389/2026-09-18`。
- 正式发布后的 HTTP 回执和包校验结果见下节。消费端独立验收待 Chronos 执行，不把 Relay 自测冒充消费端通过。
- 后续单独规划当前时点原子基线，不要求 OC 新增历史能力，也不开放生产柜台查询或下单。

## 正式发布核验

2026-10-13 20:12..20:13 Asia/Shanghai，从正式域名用 SDK 仅 GET，`page_size=2`；每户固定 ID 再完整读取一次，数量、内容摘要和全部基线证据一致：

| 账户 | close 日期 | 行数 / 页数 | baseline_id |
| --- | --- | --- | --- |
| 债享5号 `314000046830` | 2026-10-09 | 13 / 7 | `pb-ee26b769ca70c15676b5bd317d0aa7ba800337a0319ea58d549da0cdb4c88604` |
| 富盈13号 `314000045768` | 2026-10-09 | 1 / 1 | `pb-3bbc2234ad6c02ba0dd4d46a1115411fedb185ef7358d303960db63c8a77e23c` |
| 涌盈波动率 `307000051388` | 2026-10-09 | 7 / 4 | `pb-9592b944f81140d4d8f3f0d383c05aa5ce7386b66faf8e2f7c41ed963d19bcb8` |
| 智算汇利混合 `307000051387` | 2026-10-09 | 1 / 1 | `pb-d45e4856e51ee7f48fbaaf92dcaaa59ab37f924f3e3e15db014d3c19b80efea3` |
| 停用账户 `307000051389` | 2026-09-18 | 0 / 1 | `pb-7880fe04a76069ca0cc806db9a2755377fec0df58a0e9d39475dd3817d34910f` |

首轮 HTTP request ID：债享 `relay-1791893577327207093-10` .. `relay-1791893577605013029-22`，富盈 `relay-1791893578043671046-32`，涌盈 `relay-1791893578326259520-38` .. `relay-1791893578398081328-44`，智算 `relay-1791893578656208236-50`，空账户 `relay-1791893578907091838-56`。固定 ID 复读各户 request ID 分别为 `relay-1791893577663355155-24`、`relay-1791893578073959709-34`、`relay-1791893578421393256-46`、`relay-1791893578681629966-52`、`relay-1791893578934162959-58`。

债享 `2025-01-01` 缺历史：`relay-1791893577715793522-26` 返回 unavailable、零解释权限 false；SDK 完整读取明确拒绝。部分/旧行/代际重建使用隔离测试，不人为破坏生产来源来造场景。

Go 全量测试、API/ledger/orderflow race、SDK 67/67 单测及打包校验通过；正式域名下载包再次核验 SHA256，解包后单测 67/67 通过。PostgreSQL TEMP 表 10001 行实际跨页/摘要/重建拒读测试通过，耗时约 9 秒（完整大集合反复归档验证），不把此合成规模外推为生产规模或性能 SLA。API Console 新入口实际请求 HTTP 200、持仓数量表格六列，1600x900 桌面及 390x844 手机视口均无页面溢出及脚本/HTTP 错误。

安装包：`/sdk/relay-sdk-0.1.43.tar.gz`，SHA256：

```text
06981080cfa79aa27c9be2ba17980c81397151ec0c08fb80ad7aed16d94d8d9d
```

只更新 API 和下载/文档；独立 worker 进程未重启，生产环境和 0 个启用下单账户保持不变，未改真实交易账本、快照、资金流水或 NAV。可以转给 Chronos 做 P0 完整差额消费端验收；当前原子基线与正式交易准入仍未验收。
