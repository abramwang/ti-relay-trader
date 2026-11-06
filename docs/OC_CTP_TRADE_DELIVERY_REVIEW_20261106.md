# Relay 对 OC 订单/成交与持久身份交付的复核

日期：2026-11-06，Asia/Shanghai。OC报告实现基线：`ee58039`。

## 结论

**收到的协议输出兼容，OC可以直接推进下一阶段，不需要再等待原则回签。**

原分页材料缺失已解除；新订单/成交输出与Relay既有字段、原生身份、业务时间及失败语义一致。本轮只新增原始材料归档和验收测试，无需修改生产代码、wire或Meridian定义。订单/成交标准化、身份文件持久化、待关联及完整查询接线更新为“OC报告已实现，收到的输出由Relay独立复验通过”，不是OC运行时、文件崩溃窗口或在线恢复已由Relay独立验证。

F0仍未冻结；无登录、结算确认、业务Redis访问、报撤单、环境切换、部署或生产迁移授权。

## 材料核验

包名：`testdata/oc-ctp-trade-review-20261106.tar.gz`。
包SHA256：`92d621c53792d6d379228d9a22d0151ec9eafa625b9565cefa662c42d08533b8`，与旁置文件一致。
原字节解包归档在 [`testdata/oc-trade-review-20261106`](../testdata/oc-trade-review-20261106)，内含四份材料及`SHA256SUMS`；四项校验全部通过。

| 材料 | 独立核验结果 |
| --- | --- |
| `command_review/query_reply_examples.v1.json` | SHA256 `b60073574d767acad27866d7727ea1f28c7ed77902984757d129c7a5ec7a640b`；12场景/15条原body；保留旧版本订单/成交未实现的失败样例，不解释为新版本能力回退 |
| `trade_review/order_fill_query_examples.v1.json` | SHA256 `2357304ca1be22e8132c02c6b6815f572596c09c1b17afc37e98adb54faa4fa4`；12场景/12条原body，5成功、7失败 |
| 查询schema | Relay本地SHA256 `988891918ed1abc6889832d1aa446a515d77e52ca8d5e9d359f566f63beee47c`，与OC声明一致；27条body全部通过严格schema及业务字段检查 |
| CTest/ASan日志 | 说明中提及的两个`.txt`不在此次tar内，仍仅记录为OC报告通过；以后材料交付可补原日志，不阻塞后续开发，不要求源码/二进制 |

六项容量记录只有测量摘要，并未附对应全部原body。Relay核对了摘要一致性和已交成功body的真实字节预算，但**未独立重跑OC的1254/1253字节、3760/3759字节及256/257页编码边界**。Relay自身10,000行容量测试也不能替代这项OC编码器证据。

## Relay 已执行

1. 27条原body直接进入Go `QueryBatch`，不重序列化或补字段；正序、逆序收页及原字节重投通过。失败保留failed，不生成空快照；完整资金多scope不自行相加。
2. 24个独立临时PG场景经统一命令登记、查询绑定和批量收页落库，逐字节核对原文；新接收器恢复后状态保留，成功候选可读，失败无候选。5个交易成功样例应用及重复应用通过，原始事实不重复入账。
3. 本地交易查询在缺报单登记时拒绝落事实；补充明确的合成报单前提后才成功。外部样例使用其合成的可信OC来源前提，不宣称已验证真实external attestation；单独成交落库仍pending，不制造订单。
4. 独立验证本地/外部订单与成交关联、前导零、原生订单RequestID与查询SDK request_id分离、`working -> cancelled`样例中的4手已成/6手余量；源委托/成交时间保留11月4日21时，TradingDay保持11月5日，不替换为11月6日采集/输出时间。
5. 未知费用保持`null/false`。待关联、重复业务键、未知状态、同成交冲突、未知外部身份及缺TradeID等7个失败场景保持`QUERY_FAILED`；原始回包完整与标准转换失败可同时存在，不虚构SDK失败。

新增2项Go模型测试、1项PG测试（含24子场景），以及4项Python材料/schema测试。全量129项Go（68项临时PG）以race通过，耗时356.0秒，含10,000订单+10,000成交恢复场景；Python全量92项、七个相关Go包回归和vet通过。入口：

```bash
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
  scripts/test-futures-postgres.sh -run TestOCReceived
.runtime/ctp-contract-venv/bin/python -m unittest discover -s tests/ctpcontract -p 'test_*.py'
```

测试前提必须保留：旧12场景复用同message/幂等键，但动作不同，不能串成一个可投递命令序列；本地/外部样例也是分开的身份存储前提。新交易样例未附原查询及报单命令，PG测试为其显式建立合成前提（报单10手）；这不是验证OC真实dispatch记录或篡改旧1手意图向量。所有收到的JSON原字节不变，禁止投递实际Stream。

## OC 下一步

继续原计划，优先交付可验证的执行与恢复闭环，不重做本轮已对齐的协议：

1. 运行时可信身份恢复、外部来源证明及待关联事件释放；关联恢复后保持原gateway/fill身份和命令关联，未知身份不绕过校验。未完成/SDK失败的部分回调审计接线也需补齐。
2. 命令意图、dispatch标记、SDK调用事实、结果和outbox的持久顺序。已有身份登记不等于发送许可；无充分写前证据的窗口继续unknown，不自动重发报单或撤单。
3. 持久原输出及发布确认/ACK，复用既有九个恢复窗口。逐窗给出SDK调用次数、原输出message/body是否一致、原命令pending及恢复动作；本地身份文件fork测试不替代SDK/输出/ACK链路测试。没有修改交易accepted语义，不增加查询式交易final chunk。

后续交付只需协议差异（若有）、纯JSON和实际测试证据，可将缺失日志/容量原body随下轮附上；无需再交换源码或二进制，也无需等待SHFE线上行情才做离线恢复开发。在线测试另行安排授权窗口。

## Relay 与 Meridian

- Relay继续可信消费者生命周期、恢复位点和ACK、原输出恢复，并控制高频账本重放与全局资源预算；当前调用方合成请求上下文不是在线认证。未连接实际OC的任何新入口。
- Meridian本轮无新增需求。OC原生`InstrumentID/ExchangeID`保持原大小写；标准标的和目录版本继续由Relay使用Meridian契约绑定，不从`rb2612`拼后缀，不要求OC对接Meridian。
- 期货财务/逐日盯市/绩效仍单独评审；未知成交费用、资金scope及未结算事实不能借本轮交易样例升级为正式资产或最终收益。
