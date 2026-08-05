# 绩效净值人工金标

更新时间：`2026-08-01`

## 定位

人工金标用于验收 Relay 绩效公式和定位数据质量问题，不是 OC 资金、持仓、订单、成交或快照的替代来源，也不直接参与经济净值计算。

系统必须保持两条独立链路：

1. 被验收结果由 OC 原始账本、Meridian 行情、已确认资金流水和 Relay 公式计算。
2. 人工金标由用户确认的独立来源导入，只在计算完成后比较日初资产、日末资产和当日盈利。

不得为了贴合人工结果按日期选择公式、覆盖原始快照，或用金标填补证券级成交和持仓数量差异。

## 数据模型

`000021_performance_nav_gold` 新增 `performance_nav_gold_versions`：

| 字段 | 口径 |
| --- | --- |
| `account_id + trade_date + source + version` | 版本唯一键 |
| `is_current` | 同账户、交易日、来源只能有一个 current 版本 |
| `carried_open_asset` | 人工表中由上一条记录承接的日初资产 |
| `observed_open_asset` | `close_asset - daily_pnl`，数据库生成列 |
| `overnight_adjustment` | `observed_open_asset - carried_open_asset`，数据库生成列 |
| `close_asset` | 人工确认的日末经济资产 |
| `daily_pnl` | 人工确认的当日策略盈利 |
| `asset_scope` | 资产范围；债享5号当前为 `excluding_fund_occupancy` |
| `source/source_ref` | 来源类型和原始文件或文档引用 |
| `content_hash` | 规范化业务字段 SHA-256；相同 current 内容重复导入不新增版本 |
| `confirmed_by/confirmed_at` | 确认人和东八区审计时间 |
| `raw_payload` | 原始列值、文件和行号，不修改输入证据 |

记录修正时会退役旧 `is_current=true` 版本并插入新版本；历史版本只读保留。`status=confirmed` 必须同时提供确认人和确认时间。

## 导入

CSV 必须包含：

```text
trade_date
open_asset_excluding_fund_occupancy
close_asset_excluding_fund_occupancy
daily_pnl
```

默认仅预检，不访问数据库：

```bash
go run ./cmd/relayctl performance-gold-import \
  -account 314000046830 \
  -input testdata/performance/314000046830_manual_nav_202607.csv \
  -confirmed-by user
```

确认后显式增加 `-persist`。所有 CSV 行在同一个 PostgreSQL 事务中写入，任一行失败则整批回滚：

```bash
go run ./cmd/relayctl performance-gold-import \
  -config config/relay.prod.yaml \
  -account 314000046830 \
  -input testdata/performance/314000046830_manual_nav_202607.csv \
  -confirmed-by user \
  -persist
```

## 对比

`performance-gold-compare` 只读取 current confirmed 金标，并对每个交易日执行当前配置的经济净值公式预览；生产默认已升级为 `performance_economic_nav.v2.2`：

```bash
env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
  -u http_proxy -u https_proxy -u all_proxy \
  go run ./cmd/relayctl performance-gold-compare \
  -config config/relay.prod.yaml \
  -account 314000046830 \
  -date-from 20260701 \
  -date-to 20260731
```

逐日 `quality_gate_passed` 需要同时满足：

1. Relay 预览状态不是 `blocked`。
2. 日末资产绝对差异不超过配置的比较容差，默认 `0.01` 元。
3. 当日盈利绝对差异不超过同一容差。

逐日通过只表示该日公式与金标一致，不等于区间曲线可以发布。批量重建还必须满足连续性门禁：不能跳过有真实收益、外部资金流或 blocked 的中间交易日，否则累计净值会漏计该日收益。

## 当前验收

债享5号 `314000046830` 的 17 条 2026 年 7 月金标已经生产落库，来源为 `manual_user_confirmed`，全部为 current version 1。重复导入相同文件后仍为 17 个 version 1，内容哈希幂等成立。

数据库驱动对比结果：

- `2026-07-22..31` 范围有 6 条金标，5 条可计算、0 blocked，7 月 31 日因缺 OC close 快照 unavailable。
- 7 月 22、23、28、29、30 日的日末资产和当日盈利全部在 0.01 元门禁内。
- 7 月 22 日至 30 日的七个交易日已从证券空仓干净起点连续重建；7 月 30 日累计净值为 `0.972127585393`。
- 7 月 31 日不使用交割单交易流水补造资产/持仓快照，继续保持 unavailable。

7 月 23 日原阻断已通过券商交割单实际费用解决：155 个股票卖出成本项承接上一交易日 close，卖出费用为 `1,005.77 CNY`，数量差异、费用缺口和公司行为差异均为 0。修复没有从金标残差倒推费用；来源是与稳定订单逐笔唯一闭合的券商费用字段。

最终对比报告保存在部署机临时文件 `/tmp/debt5-gold-compare-20260804-final.json`，不提交包含运行时数据库结果的临时文件。完整审计见 `docs/DEBT5_HISTORICAL_LEDGER_RECONCILIATION_20260804.md`。
