# Relay 致 OC：问题答复与异常恢复样例

日期：2026-11-04，Asia/Shanghai。接续 `ctp.f0.draft.2`，仍为离线候选，未冻结。已收到 OC `1aed721/f15a0f7` 共享样例复验回签，七份原始指纹匹配 Relay `8381d97`；最新关闭项、金额待补材料和完整源码包见 [回签确认](OC_CTP_DRAFT2_ACCEPTANCE_REVIEW_20261104.md)。没有执行 OC 测试或真实柜台操作。下文保留原始请求及样例解释。

同日最新更新：Meridian v2类型校验/请求隔离两项整改已由Relay独立离线复验通过，见 [整改复核](MERIDIAN_CTP_V2_RECTIFICATION_ACCEPTANCE_20261104.md)。SHFE真实样本及在线目录/行情仍未验收。其快照完成证明新增connection_generation由Relay处理适配，不扩展本轮CTP交易wire；OC无需对接Meridian、增加全目录扫描或因该文档更新重跑未变的JSON。

## 协调边界与问题答复

由 Relay 统一协调 Meridian。OC 不需要调用 Meridian 接口、跟踪其 SDK、编写其导入格式或直接询问其行情覆盖。OC 保留柜台原始身份和约束，问题反馈给 Relay 即可。

| 事项 | Relay 答复 / OC 待反馈 |
| --- | --- |
| 标的定义 | 标准代码唯一来源是 Meridian。HTTP/SDK 的 security_id 由 Relay 精确解析为原生 symbol/exchange；OC 保持 InstrumentID/ExchangeID 原码，不大写、不拼后缀。未知映射不丢柜台事实，相关新单不放行 |
| 真实合约证据 | 仅请说明现有原始 QueryResult/导出出口、结构及范围。若只有显式单合约查询，请如实说明，不能声称是全 SHFE 目录。Relay 取得 Meridian 接收约定后负责适配，当前不要求新增全市场采集或真实登录 |
| 已回签部分 | 已记录 OC 报告独立验证凭据向量、SDK 字节上限、原生身份算法及 O/C/CT/CY 头文件映射。不重复要求从头设计；draft.2 修正项仍需逐项复验，不能据离线 codec 宣告完整运行时通过 |
| 价格与金额 | 价格词法/档位检查和资金金额编码分开。金额按 SDK binary64 可往返十进制文本、classic locale、展开指数，不做分厘舍入；请补 serializer 的有效边界、哨兵/非有限、负值和 null 共同向量。不能声称恢复券商原始十进制精度 |
| 路由与凭据 | 外层 broker_id 最长32字节、key_id最长64字节及字符集、逻辑账户6至20位数字沿用 draft.2；通用128字节 token 上限不能覆盖这些更严约束。真实身份来自可信配置，不由 envelope 自选 |
| 快照结果 | snapshot_applied 仅为 OC 本地类型化应用；Relay 自行检查完整分页并原子落库。旧缓存可读、原始末页到达均不能代替本次应用成功 |

动态限价、夜盘和目录覆盖缺口由 Relay 向 Meridian 跟进。SHFE 仍未发布，不阻断 OC 不依赖在线数据的离线开发，也不因此开放任何期货交易权限。

Meridian v2 只接受真实 exchange_full 请求及对应 IsLast 证明作为生产候选。OC 已报告的显式单合约查询保持诊断用途，不要求为此增加全市场扫描；优先由 Meridian 已有采集器在获授权窗口采样。OC 只需向 Relay 说明现有原始 QueryResult 的字段/范围和可用样例，Relay 决定可用性与适配，不拼接单合约结果冒充完整请求。InstrumentField 静态量边界不等于账户权限证明。

## 本轮共享异常样例

新增 `testdata/ctp/fault_messages.json`，全部虚构，不能发送到 Redis：

| 用例 | 验收意图 |
| --- | --- |
| cancel_rejected | 拒撤只形成撤单尝试审计，不把原委托改成 rejected/cancelled，不制造成交 |
| cancel_unknown | 撤单结果未知保留 retry_safe=false 和需核对状态，不自动重撤或改变原订单 |
| submit_unknown | COMMAND_OUTCOME_UNKNOWN 不等于业务拒单，不生成假 rejected 终态或自动重报 |
| batch_partial | accepted 回执中的已知失败子单单独处理，不把已接收子单或整批改成失败，不等待查询式 final chunk |

这四个样例通过 Relay 现有 ProcessLedgerEntry 的共享回执处理测试；不表示当前 A 股账本已经支持期货订单、成交或原生字符串编号。OC 请逐项返回样例解析结果与实现版本；若现有输出语义不同，向 Relay 提出具体字段差异，不自行扩展业务枚举。

## 恢复期望矩阵

新增 `testdata/ctp/recovery_cases.json`，版本 `ctp.recovery.expectations.v1`。它是待共同核验的**持久事实参考模型**，不是 Redis/磁盘/SDK 崩溃注入测试，也不是强制 OC 使用同名数据库字段。

| 中断位置 | 重启时的保守期望 |
| --- | --- |
| 意图落盘前 | 未开始；不能凭无记录就 ACK |
| 意图已落盘、dispatch 标记前 | 仅在写前标记与同步持久化顺序已证实的前提下判定未发送；这不授权自动报单 |
| dispatch 标记之后、SDK 调用之前 | 只有持久标记仍不能证明未调用，按 outcome_unknown 处理 |
| SDK 调用之后、结果落盘之前 | outcome_unknown，不重报/重撤 |
| 结果已落盘、outbox 之前 | 由已保存的结果恢复同一输出，不再调用 SDK |
| outbox 已落盘、尚未发布 | 发布原输出，保持业务消息身份 |
| 发布实际成功、确认丢失 | 重发同一输出；接收端须去重，不能新造事件或再次调用 SDK |
| 发布确认已持久化、ACK 前 | 仅 ACK 原命令，不重复业务调用 |
| ACK 成功后重启 | 已完成，仍需保持消息/业务幂等记录 |

这里 result 是命令调用结果/回执事实，不是等待订单生命周期最终态；accepted 后订单事件仍持续更新。ACK 参考门禁要求结果与可恢复输出已持久化且发布获确认。若 OC 采用可证明等价的持久 outbox 策略，请反馈精确故障窗口与恢复证据，由 Relay 评审，不自行声称 F0 已冻结。

请 OC 将每个窗口映射到实际持久化边界，并在未来离线故障注入中提供 SDK 调用次数、消息身份、重复输出去重、原命令 pending/ACK 及重启前后证据。仅运行本参考矩阵不能证明 OC 的 fsync、Redis ACK 或真实进程恢复正确。

## 运行与剩余项

在 Relay 仓库根目录执行，需要 Go 模块和依赖缓存；不连接真实服务：

```bash
go test ./tests/ctpcontract ./internal/redisstream ./internal/orderflow ./internal/credentials -count=1
```

本轮四包通过。旧 `ctp-f0-draft2-source.tar.gz` 是原提交归档，不包含本轮新增文件；应使用包含本文件的提交和根 Go 模块，不能只复制单个测试文件运行。

最新剩余项：OC 已报告金额 serializer 测试通过，Relay 尚缺具体实现说明/向量，待材料复核而非要求重做；完整 HTTP/wire/SDK/DB schema、未关联成交持久恢复、同消息重放与不同消息矛盾终态、真正的 SDK/输出/ACK 崩溃注入，以及后续获授权在线验收仍待完成。F0 整体保持未完成；未部署、切环境、改账或重启 OC。
