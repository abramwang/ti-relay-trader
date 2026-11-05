# Relay 对 OC r1 回签的确认与补充材料

日期：2026-11-05，Asia/Shanghai。本文件回应OC `bbf8f14` 及C1-C6回签，是统一送审稿r1的增量答复，不是生产发布或F0冻结。

## 1. 回签结论

已核对OC收到的r1与Relay文件SHA256完全一致：`a406c0452c378ac1a28a92d32a30efaf3bf0eaa74e8c421f4bc65537146c8a75`。r1原文不覆盖；OC反馈存档为 [回签原文](OC_CTP_R1_SIGNOFF_20261105.md)。接受“总体方向一致、可继续离线开发，但运行时与持久链路未交付”的状态。

| 项目 | Relay本次结论 |
| --- | --- |
| C1 | 接受标准映射由Relay持久绑定、OC按原生意图指纹约束的边界。新增四类只读请求字段校验；实际持久指纹/完整命令调度/批量容量尚待实现与确认 |
| C2 | 接受逐ticket不可变应用结果要求；补入队前失败、投影失败、旧完整结果重投及新查询拒收旧半页样例，查询schema修订到材料版2 |
| C3 | 接受OC未关联事实持久暂存方案；保持gateway必填，不新增私有事件。仍是待实现，不能写成已恢复可用 |
| C4 | 接受五态需结合原生状态/数量，不强制转换未知或触发类状态；真实更正/撤后余量仍待样本，旧股票式转换不得接回CTP主循环 |
| C5 | Relay独立核对附录59项预期全部一致，接受当前金额编码作为共同候选；DBL_MAX仅为保守拒绝规则，不宣称厂商字段级哨兵已证实 |
| C6 | 保留真实持久/outbox/XACK/重启窗口未实现状态；参考恢复模型通过不等于实际落盘或在线验收 |

OC的空资金快照修正及12项CTest通过仅记录为OC报告；Relay没有运行OC构建、sanitizer、CentOS或真实柜台，也没有验证其代码运行时。原PIE问题不关闭。

## 2. 标准映射与指纹

确认两侧分工：Relay持久绑定Meridian security_id、目录发布证据与精确原码映射；OC只校验当前wire中已有的原码、可信路由、原生身份和交易意图。不要求OC反查Meridian，不宣称现有命令包含security_id或目录版本。

同一Relay意图重投仍用已保存映射，不能因目录变化悄悄换约；OC指纹及实体七段SHA身份分开。金额编码与订单价规范化也分开：新单价先检查原词法/64字节/9位/未缩放uint64，再去小数尾零。Relay新增反例验证 `18446744073709551615.0` 必须先拒绝，不能删零后放行；`1.00`与`1`规范价格一致。具体意图字节编码及传输字段排除仍需共同向量，不在本次伪造一个已经实现的hash。

## 3. 查询请求与失败形状

确认四类北向只读请求payload仅含account_id/product_type/schema_version：account.asset.query、account.positions.query、order.list.query、fill.list.query。无历史、日期、symbol、exchange、cursor过滤。Relay HTTP历史查询继续从自己的数据库读取，不把历史需求转发为OC当前日查询。未知TradingDay不妨碍返回明确的“尚未就绪”失败，但不得伪造日期。

首期失败统一沿用适用的既有失败路径；样例采用status=failed/code=QUERY_FAILED，不新增业务错误枚举。`failure_stage`是审计阶段候选，不是柜台错误码。

### 未取得调度ticket

仅在未入队时用 `failure_stage=before_enqueue`，ticket为null，sdk_request_id/sdk_return_code/callback_error_id全部null，snapshot_applied=false。不生成假ticket、不用命令ID冒充调度ticket。

connection_generation若已有真实连接代次则保留正整数；尚未建立任何连接则null，不能用0。counter_session_id仍必须是当前OC进程的真实会话身份，不依赖柜台登录完成，也不补造柜台SessionID。未取得TradingDay则payload省略trade_date；不能输出null/空串/全零。已确认的日期可以保留。

```json
{
  "ticket": null,
  "sdk_request_id": null,
  "sdk_return_code": null,
  "callback_error_id": null,
  "connection_generation": null,
  "counter_session_id": "ctpproc-fixture-1",
  "snapshot_applied": false,
  "failure_stage": "before_enqueue"
}
```

成功页仍要求ticket非空、connection_generation和SDK请求号为正整数。已获得ticket的排队到期继续保留真实ticket/代次，不能套用before_enqueue。所有失败都不输出items/chunk，不把失败当空账户或空持仓。

### 已完成原始查询但标准投影失败

snapshot_applied应指**本ticket可输出的完整标准类型化候选**，不只代表raw store有副本。未知订单状态、关联不完整、数值错误、重复业务键冲突均阻断标准成功输出。保持真实SDK完成证据和原始行，不把原始completed改成柜台失败；北向是failed且snapshot_applied=false。

样例 `unsupported_order_state` 保留sdk_request_id/同步返回/回调错误原值，并在adapter_context.standard_validation审计区示例保留raw_completed=true及原始OrderStatus=a。该审计对象不是新标准业务结果类型，不能凭客户端填true当柜台证明。

同类多个ticket一次提取，必须逐结果绑定不可变候选和应用决定；不能把最后的querySnapshot(kind)视图套给前面的请求。完整候选验证之后才分页，不边收原始行边发成功partial。

## 4. 原文重投和会话隔离

确认两种场景必须分开：

- **已持久保存的完整旧输出**：以原命令/原请求/原会话和代次为关联依据，允许重启后原body、message_id、produced_at、query_evidence不变地交付。Redis entry ID可变。不要求换成当前会话。
- **旧请求只有部分页或未完成结果**：不能因为重启而补一个成功末页，不能拼入新ticket/新命令。新查询只接受其自身完整上下文。

Relay新增恢复参考校验：调用方必须提供已验证的原请求与完整持久输出证据，逐条原文一致、分页完整且无冲突；仅接受为原查询的历史结果，不自动恢复当前readiness，不自动覆盖新会话当前快照。已经提交的相同消息须幂等识别；迟到历史结果与当前发布时点的选择另由持久层控制。

`query_edge_cases.json`含两页完整旧输出、原上下文、新上下文及预期；测试含完整乱序重投/重复投递正例、缺首/末页及篡改时间/会话反例。参考测试的proof布尔是外部已核实证据的假设，不来自wire，也不证明fsync发生；实际恢复仍需C6实现和故障注入。不要把新的收集等待窗口解释为延长旧SDK查询期限。

## 5. 只读原生业务与容量

持仓可以无损表达已在头文件确认的原生字符，不因只允许投机新单而改写既有持仓。本次schema候选明确PosiDirection=1/2/3、PositionDate=1/2、HedgeFlag=1/2/3/5/6/7；原值保留，未知值阻断标准投影并留证。这不是授权非投机新单，不解释Net桶为股票净持仓。

订单/成交当前标准结构仍只有F/B/S/O/C/CT/CY/speculation和已覆盖五态。不可表达的原生事实不能默默截行或改枚举后输出全量成功；使用第3节失败路径。SDK八字符源时间不会因此增加不存在的小数精度，Relay只是具备保留精度的接收能力。

类型确认：ticket为字符串（仅before_enqueue允许null）；generation为正JSON整数（仅before_enqueue可null）；不得布尔/浮点/数字字符串。逻辑ID字符规则和原生字节上限沿用r1，额外ID长度/整数实现范围、批量最大子项数和实际分页容量还未双向冻结。以下不能混为一谈：

| 现有本地边界 | 状态 |
| --- | --- |
| OC命令通用解析64KiB、调度4任务/单在途/1秒间隔、原始单请求10000行/8MiB | OC报告的实现限制，不宣称协议默认 |
| Relay参考批量100子项、分页256页/100000行/8MiB | 本地参考模型限制，不宣称柜台能力或共同默认 |
| money最大343 ASCII字节 | 本次已独立对向量，Relay接受编码候选并纳入查询schema材料版2 |

请OC先完成固定ticket候选/失败输出及独立指纹编码，不必等容量最终冻结才开发无歧义部分。分页/批量实现到位后以真实编码字节和最小双方容量定边界，另给恰好上限/超限样例；不得把本地上限直接标为线上已验收能力。

## 6. 金额逐向量核对

从回签附录原文转录28个输入，按required/optional共56项，加3项舍入策略共59项，与Relay独立Python参考结果逐字一致；所有有效结果重新解析为binary64后位模式一致。另以固定随机种子检查2048个有限binary64样本往返。

接受：17位有效数字后展开指数、去小数尾零、保留-0、最多343 ASCII字节、必需缺失错误/可选缺失null、非有限与正负精确DBL_MAX明确拒绝（即使可选）、有效值仅允许FE_TONEAREST。无效不得补零/null或截断。DBL_MAX规则属于保守codec策略，不是已验证的每个厂商字段语义。

这里验证的是协议预期；三项非默认舍入测试只验证声明的拒绝规则，没有改变Python/C线程舍入模式，也没有运行OC编码器。OC实际fenv检查、调用线程不变及19995样本是其报告结果，不能写成Relay已复验。

本地转录文件 `oc_money_review_vectors.json` **不是** OC两个JSON附件，不能冒用对方公布的附件SHA；源附录完整存档，错误名称仍为内部codec诊断。查询schema的343字符限制只是必要结构校验，不能代替编码器检查位模式、哨兵和会计来源。

## 7. 本次提供的纯协议材料

目录为 `/home/ti-relay-trader/testdata/ctp/`。OC只需读取以下文档/JSON，不需要源码、测试程序、压缩包或二进制：

| 文件 | 内容/状态 | SHA256 |
| --- | --- | --- |
| query-page.schema.json | 材料身份urn:relay:ctp:query-page:draft:2；原wire仍ctp.f0.draft.2 | 988891918ed1abc6889832d1aa446a515d77e52ca8d5e9d359f566f63beee47c |
| query_pages.json | 原两份订单/成交完整查询页，未修改 | df78b499815383acdd784d58912f14a043ad3aa04416d6e250a2bebc4f025c30 |
| query_edge_cases.json | 入队前两种失败、原始完成但投影失败、完整旧输出/新上下文 | b4d5406fd3bd68c557db3ae9f57dfaae90517f66802a3eec954261191eb353f3 |
| oc_money_review_vectors.json | Relay从本次附录转录的28输入及3舍入用例 | de274e9e0c62df63a24cd455386a538459eba7f6323a3ec6024420e7a013ec1d |

schema材料版1的差异只有：入队前ticket/generation可null且严格阶段约束、成功仍要求真实身份；资金文本343长度界限；持仓原生枚举收紧。原六份共享JSON未改，原r1送审文档不覆盖；不要求重新执行已确认的旧样例。

## 8. 下一步与验收边界

Relay本轮83项Python测试和4个Go包通过。未登录/连接柜台、未部署/切环境/改权限/改数据库；生产下单权限不变。F0仍开放，F1/F2没有因离线通过而上线。

建议按OC回签的顺序继续：逐ticket不可变应用决定与资金/持仓标准转换；完整命令校验和明确指纹向量；原始事件与持久身份/待关联；durable结果/outbox/ACK及真实故障窗口。Relay配合确认本次入队前失败与原文恢复样例、资金向量，并继续隔离账本设计；OC只需反馈具体不同项和实现证据，不重复索要整体原则回签。
