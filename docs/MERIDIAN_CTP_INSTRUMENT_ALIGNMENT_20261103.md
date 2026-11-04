# 给 Meridian：CTP 期货标的定义与 SHFE 覆盖确认

日期：2026-11-03，Asia/Shanghai。背景：OC 开始普通期货交易接入，首批测评为 SHFE；Relay 需要复用 Meridian 标准，不另建合约目录或行情字段。

2026-11-04 用户最新边界：行情开发不以Trader完整快照为前提，Meridian可基于权威交易所资料及现有行情源独立推进SHFE；柜台约束与交易准入由OC/Relay负责。见 [边界修正](MERIDIAN_SHFE_MARKET_BOUNDARY_UPDATE_20261104.md)。

2026-11-04 回执已收到：Meridian 已发布统一 `/v1/futures/instruments` 和 SDK 0.1.65，SHFE 目录及行情仍未发布；本清单保留原始需求，最新取证和下一步见 [目录回执复核](MERIDIAN_CTP_CATALOG_RECEIPT_20261104.md)。不要继续将“目录接口不存在”作为待办，也不要把接口发布当成 SHFE ready。

## 已确认

本轮在线读取 `GET /v1/contracts/futures/ctp-level1`，contract_id=`meridian.ctp_futures_level1.v1`、version=1；instrument schema=`futures_instrument.v1`，authoritative_products 为 IF/IH/IC/IM。

Relay 将沿用 security_id/source_instrument_id/exchange_id/product_id、volume_multiple/price_tick、来源与发布身份；不自行将 rb2612 转成某个标准 ID，不将目录观察日期解释为 Trader API 法定生命周期。现有 SC2610.INE/sc2610 离线 fixture 只能用于映射测试，不当生产覆盖。

## 请确认或补齐

1. 是否已有 SHFE 实际合约目录？请给现有查询入口或 SDK 方法、schema/发布版本、有效范围及标准 ID 与原始 InstrumentID/ExchangeID 的明确对应；若尚未支持，明确为待补，不必另设计 Relay 专用代码规范。
2. 完整目录与精确单合约查询如何取得？需要分页/代际、已到期及尚未生效状态、歧义/未知标的处理。字段仍沿用 futures_instrument.v1，来源和有效时刻不能只靠代码推断。
3. SHFE 的乘数/价位及真实生命周期来源和质量标记是否可用？与 OC 交易 API 静态合约值不一致时，双方应保留来源、采集时点和冲突，不能由 Relay 默认补值。
4. 现有 snapshots/bars/SSE 是否覆盖 SHFE、哪些品种和时段？请给可执行只读样例、字段与 stale/readiness 语义。特别确认动态涨跌停价的字段、源日/时效及缺值语义；静态 PriceTick 不能替代动态价格限制。
5. 夜盘自然日期与 TradingDay、交易时段/节假日和后续结算价发布口径是否已覆盖目标品种？若只支持 CFFEX 日盘，请明确分项状态，避免套用其自然日/调度假设。

## 边界

- 这是一份协作请求，尚不代表 Meridian 已接受开发或已发布上述能力；本轮未修改其代码、任务或会话。
- Relay/OC 继续离线解析、只读柜台采集和原生事实落盘；无标准映射的柜台记录保留原码、待补关联，不丢弃，也不造标准 ID。
- 新单必须具备可信实际合约映射与必要行情/约束；缺项时禁止发单。测试样例、连续系列和静态查询成功都不能绕过这一点。
- 不要求 Meridian 提供券商账户交易权限、资金、保证金余额或订单事实；这些仍由 OC 和 Relay 管理。期货会计结算另行验收，不扩张到模拟撮合。
