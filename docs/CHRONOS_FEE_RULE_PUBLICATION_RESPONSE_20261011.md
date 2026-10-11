# Chronos 费率发布需求响应

日期：2026-10-11，Asia/Shanghai。对应需求：`/home/ti-chronos-strategy/docs/RELAY-FEE-RULE-PUBLICATION-REQUEST-20261010.md`。

## 当前结论

已完成只读 API 完整性、SDK 公开入口、普通费率冲突门禁和历史样本核对。**尚未完成费率 active 发布，不能通知 Chronos 全部验收通过。**

生产 `performance_fee_rules` 确实为 0 行。以往核验的是 OC 订单实际费用完整性和账本闭合，不等于已经发布未来预算费率合同。历史实际费用始终优先，不改成新估算。本轮不改费率表、交易账本、资金、快照、NAV 或任务；不查询 OC、不试单、不切环境。生产下单和 Web 绩效设置写权限继续关闭。

## 只读入口

```http
GET /v1/performance/fee-rules?account_id=307000051387&status=active&effective_on=2026-10-12&limit=500
```

外层保留 `request_id/time`，内层返回 `rules/count/is_complete/query`。有效期两端包含，日期可传紧凑格式，回显规范化日期。未传 limit 或为 0 时默认 200，正值最大 500；超限、非法状态或日期返回 400。

数据库额外读一行证明完整性：恰好 500 条 complete，有第 501 条时截断且 `is_complete=false`。本轮不加游标；截断时缩小账户/日期范围，不能声称获取全部。完整空列表是未发布规则，不是零费率。

SDK `0.1.42`：

```python
from relay_sdk import RelayClient

client = RelayClient(account_id="307000051387")
page = client.get_fee_rules(effective_on="2026-10-12")
for rule in page.rules:
    print(rule.rule_id, rule.version, rule.raw_payload)
audit = page.raw
```

返回 `FeeRulePage` 和 `FeeRule`，保留查询回显、count、请求 ID、时间及原始证据。缺数值是 `None`，真实零保持 0。SDK 不写入、不选择或推导费率。完整性未知/截断默认抛 `FEE_RULE_LIST_INCOMPLETE`；count 不符抛 `FEE_RULE_RESPONSE_INVALID`。`require_complete=False` 仅允许部分审计，不能据此做完整预算。支持显式账户覆盖默认账户，无跨账户回退。

## 字段和粒度

以下是 Relay 现有研究估算语义，不是未经确认的券商收费承诺：

| 字段 | 单位/处理 |
| --- | --- |
| `commission_rate` | 成交金额比例；0.00005 为万分之 0.5 |
| `minimum_commission` | CNY；旧公式仅在 commission_rate > 0 时应用 |
| `stamp_duty_rate` | 成交金额比例；普通卖出加税，买入不加 |
| `transfer_fee_rate` | 成交金额比例，双向相加，范围由规则限定 |
| `handling_fee_rate/other_rate` | 成交金额比例；佣金已内含的规费不能再次累加 |
| `fixed_fee` | CNY；当前估算每次直接相加 |
| `repo_fee_rate` | 独立逆回购参数，不用于普通股票/ETF |
| `estimated_friction_rate` | T0 模型参数，不作为二级市场收费 |

OC 实际费用按 `record_scope=order` 每单只计一次。**现有费率回退仍逐 fill 估算，minimum/fixed_fee 尚未按订单聚合。** 激活前需要统一订单粒度；本轮不声称一单多成交最低佣金已修复。零佣金率加非零最低佣金的旧公式不收最低佣金，已加回归测试，但这不证明券商零佣金条款。

普通股票/ETF 二级市场均为 `business_type=S`，还需 Meridian 证券类型区分；`P/R+E`、逆回购和期货不得套用。账户和有效日须明确匹配。普通贡献按市场/证券类型/业务/方向明确匹配数选最具体规则；同一最高分多条一律 `fee_rule_conflict`，不按 version 或返回顺序自动取一条。发布新版本须关闭旧版重叠有效期。实际订单费用优先，不受预算冲突影响。

## 历史核对

只读筛选 2026-09-22..10-09 的 OC 完整且关联完整、turnover>0 的普通费用，排除 20 开头逆回购。以下 stock/fund candidate 临时按代码前缀分组，仅用于限定审计，不作为规则分类；正式发布采用 Meridian 主数据。

| 账户 | 类型候选 | 记录数 | 交易日数 | 佣金候选最大误差 CNY |
| --- | --- | --- | --- | --- |
| `307000051387` | ETF | 1,649 | 8 | 0 |
| `307000051387` | 股票 | 174 | 1 | 0.000000400 |
| `307000051388` | ETF | 1,588 | 6 | 0 |
| `314000045768` | ETF | 1,653 | 8 | 0 |
| `314000046830` | 股票 | 2,660 | 6 | 0.000000496 |

总计 7,724 条。ETF 佣金样本符合 `max(turnover*0.00005,0.10)`；股票 BrokerageFee 符合 `turnover*0.0001354`。10 月 8/9 日股票卖出印花税为 0.0005、买入为 0；SH 过户费约 0.00001、SZ 为 0。ETF 其它独立分项、股票独立 handling/regulatory 分项为 0，不能由此断言 BrokerageFee 不含规费。股票最小样本金额 336 元，不足以证明任意小额/零股最低收费；两个仅做 ETF 的账户缺股票样本，不能私自借用参数。

脱敏算例，候选预算不等于正式结算：

| 情形 | 金额 CNY | 候选费用 CNY | 依据/边界 |
| --- | --- | --- | --- |
| ETF 小额卖出 | 75.20 | 0.10 | OC 实际样本 |
| ETF 买入，一单四笔成交 | 3,771,000 | 188.55 | OC 实际订单级总费用 |
| ETF 两单各 1,000 | 合计 2,000 | 合计 0.20 | 合成；每单预算最低收费，不按证券合并 |
| SH 股票买入 | 10,000 | 1.454 | 合成；BrokerageFee 1.354 + 过户 0.10 |
| SH 股票卖出 | 10,000 | 6.454 | 上项加印花税 5.00 |
| SZ 股票卖出 | 10,000 | 6.354 | 合成；不另加 SH 过户费 |
| 零佣金率、最低佣金 5 | 1,000 | 未确认 | 旧公式为 0，不发布成券商规则 |

正式收费粒度、撤单重报/部分成交的最低收费、规费内含、分项舍入顺序及分币舍入仍需合同或 OC 原始字段说明。订单费用可有分币以下精度；库金额 6 位、费率 10 位，`roundMoney` 的 6 位四舍五入是存储/研究约定，不是华鑫结算舍入证明。

## 剩余步骤

1. 确认候选预算参数、四账户共享关系和 active 起始日，或提供已有权威费率说明；新规则不外推历史。
2. 统一按订单估算最低佣金/固定费，记录舍入和撤改限制，预算不能混同实际结算。
3. 本机受控发布版本化规则，raw_payload 保存范围、核验时间、依据摘要、确认人及未知边界；无需打开 Web 写权限。
4. 四账户/计划日唯一匹配和缺规则/冲突失败关闭只读验收通过后，再通知 Chronos 关闭需求。

## 验证

全量 Go、API/performance race、隔离 PostgreSQL 500/501 行及日期/账户过滤通过。临时表事务回滚，不修改真实规则表或序列。SDK 61/61 测试及归档/SHA-256 发布检查通过。生产四户列表仍为空且完整，表示尚未激活，不是查询失败。

2026-10-11 08:42 已只重启 API 发布：pid=487508；独立 worker pid=2508335、启动时间 2026-10-07 18:12 未变。四户 SDK GET 均验证 `effective_on=2026-10-12`、实际账户回显和完整空列表；线上 limit=501 返回 HTTP 400。状态为 production/ok、4 个启用查询账户、0 个交易账户。

线上 `relay-sdk-0.1.42.tar.gz` 与校验文件一致，SHA-256：`b4433a24712198457dfea96c5897b1a3a2428ed7336814006769169f233e8d92`。
