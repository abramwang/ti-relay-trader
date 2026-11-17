# 期货终端行情界面

日期：2026-11-16，Asia/Shanghai。入口：`/trade`。

11-17增量：[CTP账户只读视图](CTP_TERMINAL_READONLY_20261117.md)已发布，TEST可见东吴300052141及历史查询观察、心跳和资金失败。下文“账户未接入”为11-16阶段记录；当前仍未接通期货报撤或实时业务快照。

## 发布范围

本轮完成中金所 / 上期所行情观察和期货委托表单布局，不代表期货交易链路上线。
当前仍为华鑫 TEST API；没有注册 CTP 账户、发送柜台查询/报撤单、启停 OC、变更权限或数据库迁移。

- 左上“行情市场”选择股票/ETF、中金所或上期所；代码、品种前缀或中文合约名称自动补全。
- 合约目录读取 Meridian `/v1/futures/instruments`，筛选 `active,active_observed`，跟随 `next_cursor`，不再拿快照充当目录。
- `security_id/exchange_id/instrument_name/price_tick/volume_multiple/source_instrument_id` 保持 Meridian 定义；不推造主力、乘数或账户交易限制。
- 行情经既有 Relay 只读代理读取，快照 SSE 持续更新。批量 SSE 取当前合约最新接收时间，迟到 HTTP/旧合约结果不能覆盖新观察。
- 价格精度由目录最小价位推导显示小数位；不可用的盘口档不显示为虚构深度。期货涨跌基准为 `pre_settlement`。
- 合约面板显示交易日、实际行情/接收时间、昨结算、昨收、涨跌停、乘数、价位、成交量和持仓量。时间以 Meridian 显式东八区时间为准。
- `allow_stale=true` 仅用于人工行情观察；陈旧快照/断线明确标示，不参与任何交易准入。

## 分钟线与隔离

K线按 OHLC 绘制，日期轴保留自然日和时刻，按完整时间排序。使用 `TradingDay` 归属夜盘，不套用 A 股09:30-15:00过滤，且不修改股票账本/绩效默认日期。

来源选择“自动 / 实时 / 归档”：自动按当前合约快照交易日及采集进程状态选择，用户可显式指定。夜盘尚未开盘但采集已运行时仍请求实时，不因为暂时陈旧转查尚不存在的未来归档。每30秒刷新当前行情交易日，历史手选日期不被推送覆盖。

跟随分钟分页，防重复游标；未知成交量保留 null。请求失败清空旧图，显示 Meridian 原始错误代码/说明，不能以旧合约曲线冒充成功。`archive_incomplete` 等质量门禁不绕过。

期货表单显示手数及开仓/平仓，上期所另有平今/平昨；这些只是未启用的委托编辑项，不作为交易能力证明。账户选择显示“交易路由未接通”，提交在按钮和处理函数两层关闭。期货模式隐藏证券资产与证券订单，单独显示未连接的期货账本，不复用证券可用资金推算保证金/最大手数。

## 验证

```sh
node --test tests/unit/futures_terminal.test.cjs tests/unit/performance_quality.test.cjs
go test ./...
/tmp/relay-browser-venv/bin/python tests/integration/futures_terminal_smoke.py
/tmp/relay-browser-venv/bin/python tests/integration/futures_terminal_smoke.py --live
```

13项期货JS单测及已有11项绩效回归；Go全包通过。浏览器1600x1000 / 390x844验证市场切换、目录分页、600根跨夜合成分钟线、SSE更新/断线、价位/手数、归档失败清图、提交保护及canvas非空，所有写请求拦截且未发生。

合成数据只在浏览器路由拦截中存在，不写服务器/Redis/PG。证据：`outputs/recovery/futures_terminal_20261116/`。
真实只读检查确认 `AU2612.SHFE` 目录价位0.01、乘数1000；`IF2612.CFFEX` 价位0.2、乘数300。约20:50源切换至夜盘启动阶段，快照含20261117交易日，盘口/分钟线尚不就绪，明确显示陈旧/断线或 `source_unavailable`。这是观察时点状态，不据此否认 Meridian 的实时能力，也不声称当晚已完成在线连续行情验收。

## 后续

继续按 [OC东吴准备回签后的推进](OC_CTP_DONGWU_READINESS_RESPONSE_20261116.md) 接通独立TEST会话/四查询，再接真实资金、保证金、多空今昨持仓、订单/成交与报撤单。只有后端路由、readiness和权限均已验收后才能解除对应表单保护；不因行情可用而放开交易。
