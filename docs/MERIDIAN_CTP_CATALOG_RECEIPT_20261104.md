# Meridian 期货目录回执复核与 Relay 只读接入

日期：2026-11-04，Asia/Shanghai。来源：Meridian `docs/Relay-CTP合约目录与SHFE覆盖回执.md`，并直接只读核验其在线 `/v1/futures/instruments`。

## 1. 结论

统一合约目录查询入口已发布，SDK 据 Meridian 回执为 0.1.65；SHFE 仍明确未就绪，不能因新增目录接口就解锁期货下单。Meridian 主数据标准继续是唯一跨系统标的标准，Relay 不自建代码规范。

| 能力 | 当前证据与处理 |
| --- | --- |
| CFFEX 真实合约目录 | 在线 meta 显示 production_ready、541 行；IF/IH/IC/IM 历史及当前实际合约，不是 541 个当前可交易合约 |
| 标准与原生映射 | 精确查询 IF2612+CFFEX 返回 IF2612.CFFEX；沿用 security_id/source_instrument_id/exchange_id/product_id |
| SHFE 目录 | 查询 rb2612+SHFE 返回 HTTP 503/source_unavailable；不自行生成 RB2612.SHFE |
| SHFE 快照/分钟/SSE/夜盘/结算 | Meridian 回执明确 not_published/source_unavailable，尚不能作为交易行情输入 |
| 动态价格限制 | CFFEX 快照已提供 upper_limit_price/lower_limit_price；沿用快照时效和 null 语义，不用 price_tick 代替 |
| 当前合约生命周期 | complete_daily_observed_trade_bounds 是观察边界，不能当 Trader API 的法定上市/到期范围 |

在线本轮目录 publication_snapshot_id 为 `30be21670608f0f24fab2bbd77e11e572a7823754001215cd70fa3eb9aaad351`；logical_table_checksum 为 `d077d3e7197f23c2ae7a473ab19e6a9a240694732d0254bab1172ca5524398ee`。这些是本轮取证版本，不在代码中硬编码为未来允许列表。

## 2. 只读验证

- `source_instrument_id=IF2612&exchange_id=CFFEX`：HTTP 200，标准 ID 与原码、乘数、价位、原始生命周期质量及来源一并返回。
- `exchange_id=CFFEX&limit=1`：首条 IC1505.CFFEX；携带返回 cursor 读取下一页为 IC1506.CFFEX，两页发布身份相同，total_count=541。
- 用上述 cursor 把 limit 改为 2：HTTP 409/cursor_expired，证明游标绑定页大小/查询条件，不能静默重启或拼接新发布目录。
- `source_instrument_id=rb2612&exchange_id=SHFE`：HTTP 503/source_unavailable，保留原因“有效只读 Trader API 合约查询源尚未取得”。

例如 IF2612.CFFEX 此次行的 expire_date 为 20261103、trading_status=active_observed；这不是“已经过期”的矛盾，而是明确标记的最近观察日。必须结合 lifecycle_quality 解释；后续交易准入仍依赖 OC 的真实生命周期和其他条件。

本轮没有登录 Trader、订阅行情、操作资金或发送交易指令。Meridian 报告的“用户不活跃/旧前置未连接”仅记录为其探测结论，Relay 没有再次尝试这些登录。

## 3. Relay 本轮实现

新增只读代理 `GET /v1/meridian/futures/instruments`，沿用既有 Meridian 代理结构，不改变交易 API：

- 参数直接传递，包括标准 ID、原始 ID、交易所、品种、状态、limit/cursor；不自动大写、不改代码、不添加默认交易日。
- 成功数据及 meta 保存在 Relay 响应 data 中，保留 publication_snapshot_id、logical_table_checksum、next_cursor、exchange_coverage 和 lifecycle_quality。
- 上游 404/409/503 保留 HTTP 状态，不改成空成功；现有 Relay 通用错误封装不变，上游完整响应在 `error.detail`，因此精确上游错误码位于 `error.detail.error.code`。不是新增交易错误枚举。
- 不主动重试、重启分页或在 SHFE 失败时回退 CFFEX/代码推断；POST 拒绝，不访问柜台。
- API 目录同步登记；Python SDK 源码新增 `get_meridian_futures_instruments(...)`，返回单页原始映射与 meta，不包装为股票 Instrument，不自动吞掉游标失效。

SDK 源码示例（此次未构建/发布新 wheel）：

```python
page = client.get_meridian_futures_instruments(
    source_instrument_id="IF2612", exchange_id="CFFEX", limit=200,
)
instrument = page["data"][0]
publication = page["meta"]["publication_snapshot_id"]
```

Relay 此处使用自己的 HTTP 代理客户端，不依赖 Meridian Python SDK，因此无需为本次只读目录接入安装或升级外部 pandas/meridian-sdk。现有行情/SSE/交易模型不变。代码和 SDK 方法尚未部署/发布，不能把上游验证说成 Relay 9092 已提供新端点。

## 4. 给 OC 的下一步

F0 codec、凭据和持久恢复的离线开发可以继续；SHFE 新单仍不能放开，也不能把测评时的 rb2612 合成 fixture 当权威合约目录。

解除第一层阻塞有两条由 Meridian 明确允许的路径：提供经授权、能执行 ReqQryInstrument 的只读 Trader 配置，或提供与其采集器同结构、脱敏且带采集时点的真实查询快照。建议优先评估 OC 后续获授权窗口已有的真实查询输出能否按 Meridian 接收格式交付，避免另开并发登录影响交易会话。此建议不是现在登录、导出真实数据或读取托管凭据的授权。

快照须能核验请求作用域、真实完成证据、原始合约字段及采集时点；不能以合成 JSON、仅哈希、仅代码列表或缺末页的部分结果代替。接收格式由 Meridian 既有采集器约定，Relay 不再设计第二套合约标准。

目录转为 production_ready 只是第一层。还需逐合约核对静态值、SHFE 日盘及完整夜盘时间/动态限价/stale 验收、账户权限和 OC/Relay 交易恢复门禁。历史 bars 及正式结算按 Meridian 后续完整分区验收，不因目录发布提前标绿。

未知标准映射不阻止保存柜台原始订单/成交/持仓及其原码；标准关联保持未解析，相关新单失败关闭。本文不为现有 A 股链路新增限制，不改变当前环境和权限。

## 5. 验证与剩余工作

自动测试覆盖目录参数/大小写/游标保留、成功 meta、404 未知代码、409 歧义与游标失效、503 SHFE 未发布、禁止 POST，以及 Python 参数列表/原生错误详情和无自动重试。

本轮验证结果：`go test ./internal/market ./internal/api ./internal/trading ./tests/ctpcontract -count=1` 四包通过；Python SDK 全部 68 项单测通过。新增 Go 错误回归同时校正了测试对现有错误包装的假设（Relay 是 error.detail，而非 error.details），SDK mock 与真实包装一致。未执行完整项目测试、OC 回归、真实交易或 Relay 新版本在线发布。

下一步：OC/用户协调真实只读源或快照给 Meridian；Relay 按 F0 继续金额、拒撤、未关联成交和 ACK 恢复共同样例。目录代理不是新单准入解析器，未来必须显式读取 exchange_coverage、同一发布目录映射与实时约束后接入准入；当前期货交易仍未实现/开放。
