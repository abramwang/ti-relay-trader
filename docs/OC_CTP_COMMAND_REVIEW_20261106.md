# Relay 对 OC 完整命令、意图编码与有限分页交付的复核

日期：2026-11-06，Asia/Shanghai。依据：[OC交付原文](OC_CTP_COMMAND_DELIVERY_20261105.md)。

## 结论与推进

无新增原则性分歧，OC可直接继续订单/成交标准转换及持久身份/未关联成交，随后推进持久命令、结果、outbox/ACK和故障恢复。命令校验、意图编码、资金/持仓完整有限分页组件记为**OC报告已实现**，不再笼统列作未实现；实际Stream调度、授权执行和持久恢复仍未完成。F0不冻结，无任何在线操作授权。

Relay已独立复验收到的12份意图向量，通过；尚缺分页实际输出JSON，不能将文档里的15条回包、边界字节数或20项CTest报告写成Relay已验收。只补材料，不重开金额、凭据AAD、实体身份或Meridian标准定义。

## 已取得的证据

| 材料 | 本地证据与状态 |
| --- | --- |
| 交付说明 | 用户附件原字节存档；`cmp`一致 |
| `testdata/fixtures/ctp_intent_vectors.v1.json` | SHA256为`cd2e9f38e2d4216a17cfa0802bcff3093256dbd5920ee39fe5e9f5cddf0d69b4`，与交付说明一致 |
| `query_reply_examples.v1.json` | 尚未收到；期望SHA256为`b60073574d767acad27866d7727ea1f28c7ed77902984757d129c7a5ec7a640b` |
| CTest/sanitizer日志 | 未独立运行OC代码，仅保留OC报告；不要求源码/二进制，不以离线通过代替在线证据 |

`tests/ctpcontract/test_oc_command_delivery.py`从每组**原始命令及显式scope**重新推导字段，不把OC给出的预期fields拿来当编码输入：

- 12组字段序列与顺序、36份意图/幂等查找/消息查找完整字节、48个SHA256全部一致。
- 价格同值及传输字段变化不改变意图，原body指纹仍不同。
- 相同body与查找键，可信UserID改变时意图必须改变；不能仅比raw hash放行。
- 子项换序改变意图；UTF-8按字节长度、不按字符数，不把冒号/反斜杠再JSON转义后编码。
- 四类query无当前TradingDay/会话后缀；同键换action查找空间不变，意图不同，应冲突而非新建请求。

以上为纯JSON合成样例复验，不是实际柜台/Redis请求、生产账户权限或持久幂等验收。

## 容量与 Relay 差异

| 项目 | OC本地候选 | Relay处理 |
| --- | --- | --- |
| 完整命令体 | 65,536 UTF-8字节，含空白/转义 | 未上线的单笔/批量报单解码由8MiB收紧至同值；入站原始审计容量不变 |
| 逻辑ID | 128 UTF-8字节 | 报单envelope四ID及gateway/client从512收紧至128；不收紧历史事件的原文留存 |
| 批量 | 最多100子项且仍受64KiB约束 | 保持100；已补100/101、含转义长ID导致100条也超字节预算的测试 |
| JSON深度 | OC命令32 | Relay共享入站扫描仍64；报单固定形状拒绝额外嵌套，合法报单远低于32，不为此修改全部审计解码 |
| 回包 | 每页200行/256KiB，总256页/10,000行/8MiB | 现有查询组装页数/总字节可容纳；较宽的本地行数保护不要求OC提高产量，实际bytes待样例核验 |
| 事实持久原型 | OC查询最多10,000行 | Relay当前每作用域日仅1024条独立行证据，且包含事件及重复查询；**尚不具备10,000行账本生产承载能力**，应用超限失败关闭。Relay负责增量存储与容量验收，不要求OC裁掉完整账户事实 |

新增Go测试验证64KiB恰好/多1字节、128/129字节ASCII及多字节ID、批量双预算，以及合成`failure_stage=output_limits`仍是failed而非成功空集合。查询测试的`raw_completed`置于既有`adapter_context.standard_validation`审计扩展，是Relay自构边界用例；实际OC字段位置仍以待收JSON复核，不放宽`query_evidence`未知字段检查来猜兼容。

## 意图登记的落实边界

OC字节候选可作为下一轮实现基线，实体订单/成交七段哈希不改。必须区分：

1. 查找空间只绑定四项路由加message_id或idempotency_key，不按action、日期、凭据身份另开空间。
2. 意图包含原生BrokerID/InvestorID/**UserID**和业务字段；标准security_id及目录版本由Relay在持久登记中另存可信映射，OC不负责对接Meridian。
3. 同message不同raw留证拒绝；新message同幂等键/同意图只恢复原输出，不能重发SDK或把旧origin/request关联改写成新命令。
4. 查询需要新采样必须新建命令/幂等身份；先查旧意图再进行新TradingDay/撤单目标绑定，不能午夜重新归日。

现有Relay `EventStore.RegisterCommand`只是作用域日内、原字节严格一致的报单身份登记；尚不包含UserID和上述跨日查找空间，也不支持同意图新message恢复旧输出。`QueryStore`按action隔离批次也不能替代全局命令查找。**不得把这些原型当成OC意图v1已完整实现**。后续新增统一可信命令登记及映射，不直接覆盖旧身份/原文或用回包反向登记本地命令；再接查询ticket及事件统一校验、发送outbox和消费位点/ACK。

## 待补及分工

- OC：仅补`query_reply_examples.v1.json`原文件，保留文档指纹；继续已计划的订单/成交标准化、持久身份与待关联，不等待Relay重复原则确认。
- Relay：拿到分页JSON后按每个独立case复验原body、schema、页序、容量和失败原文；不把不同case复用的fixture ID混入同一Stream，不重序列化再校验ID。再落实统一命令登记与容量扩展。
- Meridian：本轮没有新增要求，继续行情目录/映射职责，不等待Trader目录作为行情卡点。

本轮88项Go测试（38项真实临时PG）以race通过，88项Python协议测试及Go相关包/vet通过。未连接真实OC/Redis，未迁移生产库、部署、切环境或改交易权限；四份用户暂存资金CSV不纳入提交。
