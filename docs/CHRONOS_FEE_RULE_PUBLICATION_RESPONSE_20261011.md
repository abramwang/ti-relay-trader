# Chronos 费率发布需求响应

日期：2026-10-11，Asia/Shanghai。对应需求：`/home/ti-chronos-strategy/docs/RELAY-FEE-RULE-PUBLICATION-REQUEST-20261010.md`。

## 当前结论

已完成只读 API 完整性、SDK 公开入口、普通费率冲突门禁、订单粒度预算及四账户 active 发布。**可以通知 Chronos 按本文件的“样本核验预算”边界验收；尚未收到 Chronos 本轮消费端验收报告，不能代替其宣布验收通过。**

2026-10-11 用户明确认可四个生产账户共享样本核验的预算参数，保留未核验边界。生产 `performance_fee_rules` 从 0 行新增 32 条 active，生效日为 **2026-10-11**，不倒填历史。历史实际费用始终优先，不改成新估算。本轮仅发布费率，不修改交易账本、资金、快照、NAV 或任务；不查询 OC、不试单、不切环境。生产下单和 Web 绩效设置写权限继续关闭。

发布 ID 为 `huaxin-budget-20261011-v1`。四户 `314000046830/314000045768/307000051387/307000051388` 各 8 条，明确覆盖 `SH/SZ × stock/etf × B/S`，全部 `business_type=S`。每户数据库 version 为 1..8（每账户递增的行版本，不是整套规则版本）；`raw_payload.profile_version=1` 表示共同参数集版本。停用账户、其它券商和 ETF 申赎/逆回购/期货不覆盖。

| 类型 | 佣金率 | 最低佣金 CNY | 卖出印花税 | SH 过户费 | SZ 过户费 |
| --- | --- | --- | --- | --- | --- |
| ETF 二级市场 | 0.00005 | 0.10 | 0 | 0 | 0 |
| 股票 | 0.0001354 | 0（预算假设，未核验） | 0.0005 | 0.00001 | 0 |

买入印花税、另计 handling/other/fixed 预算均为 0；这是样本模型，不代表收费合同免收或不含规费。股票 336 元以下、零股、撤改重报和券商分项/分币舍入尚未核验，Chronos 不应把这些边界当作已知实际费用。`raw_payload` 明确携带 `authority=sample_verified_budget`、`not_broker_settlement_rule=true`、账户自身样本数量及用户授权共享关系；无自身样本不冒充本账户独立核验。

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

以下是用户认可的研究预算语义，不是券商收费承诺：

| 字段 | 单位/处理 |
| --- | --- |
| `commission_rate` | 成交金额比例；0.00005 为万分之 0.5 |
| `minimum_commission` | CNY；旧公式仅在 commission_rate > 0 时应用 |
| `stamp_duty_rate` | 成交金额比例；普通卖出加税，买入不加 |
| `transfer_fee_rate` | 成交金额比例，双向相加，范围由规则限定 |
| `handling_fee_rate/other_rate` | 成交金额比例；佣金已内含的规费不能再次累加 |
| `fixed_fee` | CNY；有正成交额的订单只加一次；未成交撤单不计 |
| `repo_fee_rate` | 独立逆回购参数，不用于普通股票/ETF |
| `estimated_friction_rate` | T0 模型参数，不作为二级市场收费 |

OC 实际费用按 `record_scope=order` 每单只计一次。费率回退现在在普通贡献和移动成本账共用订单级累计：`account_id + trade_date + gateway_order_id` 同一订单累加成交额，最低佣金和固定费不逐 fill 重扣；每次只分配“当前累计总预算减前次累计预算”。同证券不同订单、不同账户/日分别计算，不跨日合并。累计总额统一 `roundMoney` 到 6 位，实际费用不按该估算舍入改写。缺订单身份、同单规则变化或实际成交费用与预算混用均带缺口质量标记，不假定完整净费用。零佣金率加非零最低佣金的旧公式不收最低佣金，已加回归测试，但未发布该组合为券商规则。

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

总计 7,724 条。ETF 佣金样本符合 `max(turnover*0.00005,0.10)`；股票 BrokerageFee 符合 `turnover*0.0001354`。10 月 8/9 日股票卖出印花税为 0.0005、买入为 0；SH 过户费约 0.00001、SZ 为 0。ETF 其它独立分项、股票独立 handling/regulatory 分项为 0，不能由此断言 BrokerageFee 不含规费。股票最小样本金额 336 元，不足以证明任意小额/零股最低收费；缺本账户样本的证券类型仅依据用户本轮明确授权共享预算，不变更其自身样本覆盖事实。

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

## 发布与验收

受控发布文件：`testdata/performance/huaxin_fee_budget_20261011.json`，SHA-256 为 `5c929f07c483097d4678a8cc60b3128d324a450c63278c79a3602fda2250f427`，每条规则保留该摘要。发布后不得原地修改文件再冒用同一 publication ID；改参数应另起版本，结束旧有效区间后发布。

```bash
go run ./cmd/relayctl performance-fee-rules-publish -config .runtime/active-config.yaml -expected-env production -input testdata/performance/huaxin_fee_budget_20261011.json
# 首行只读预览；仅获授权才增加 -persist，使用表锁和单一事务发布。
python3 scripts/verify-fee-budget-publication.py --base-url http://relay-trader.quantstage.com
```

本次已执行授权发布。工具检查环境、启用华鑫账户、完整明确范围、有限非负数值、确认日及有效区间重叠；重复内容返回 replayed，不新增版本。写前费率表备份为 `outputs/backups/fee_rules_before_20261011.dump`，SHA-256 为 `db5a4ddb55ee533c2d95798fc7dd93ef40a13d8df650a1539da0a4aafcccccdd`。

只读 SDK 已验证四户各 8 条、唯一范围、正确参数/原始证据和完整性；总查询 32 条；`2026-10-10`、停用及未知账户为空；limit=7 截断默认失败关闭。日期必须取计划日，历史日期不能自动回退本版本或复制其它账户参数。Chronos 可用 `relay-sdk==0.1.42` 的公开 `get_fee_rules` 替换窄 GET 适配，保留匹配 ID/version 和原始证据，再提交消费端验收。无需生产试单。

## 验证

全量 Go、API/performance/relayctl race、隔离 PostgreSQL 500/501 行及日期/账户过滤通过；新增隔离发布测试覆盖预览零写入、32 行发布、幂等重放及冲突拦截，临时表/独立临时序列在事务回滚后清除。费用回归覆盖 ETF 买卖小额最低收费、一单多成交与固定费、多单同证券、日期隔离、股票税费、实际/真实零费用优先和混合证据保护。SDK 61/61 测试及归档/SHA-256 发布检查通过。

2026-10-11 08:42 为第一阶段只读 API 发布，当时完整空列表符合未发布状态；本轮规则发布后已重启 API 加载订单粒度预算修正，pid=525413。域名入口四户只读验收通过，request_id 分别为 `relay-1791680300569290715-26`、`relay-1791680300667463913-32`、`relay-1791680300743784518-38`、`relay-1791680300825757549-44`（账户顺序同发布文件）。独立 worker pid=2508335 未重启，环境保持 production/ok、4 个启用查询账户、0 个交易账户，Web 写权限不开放。

线上 `relay-sdk-0.1.42.tar.gz` 与校验文件一致，SHA-256：`b4433a24712198457dfea96c5897b1a3a2428ed7336814006769169f233e8d92`。
