# OC 给 Relay：容量可观测性增量

日期：2026-11-18，Asia/Shanghai。仅代码开发及离线验证，未部署，未连接业务 Redis 或柜台。交易授权仍唯一归属 Relay。

## 增量范围

在已有 `authorization_owner=relay` 执行入口的心跳中新增：

```text
adapter_context.admission_evidence.resource_usage
  journal.revision
  journal.warning / exhausted
  journal.metrics.file_bytes / commands / messages / deliveries / conflicts / callbacks / events
  query_commands_received
```

每个 journal metric 使用 `used / limit / remaining / warning / exhausted`。达到上限的80%时 warning=true；达到上限时 exhausted=true。预警不关准入、不重启、不扩大许可，不是另一套业务额度。query_commands_received 仅为非负整数统计，不再有查询次数门禁。

- journal 统计来自当前持久状态；文件字节数在成功落盘/加载时取得，包含封装与摘要。查询不重新序列化整份 journal，不写 revision；重启不清累计历史。
- commands 上限2000；messages/deliveries/conflicts/callbacks/events各10000；file_bytes上限64MiB。query_commands_received 统计本进程已领取查询，重启重新计数，但既有 journal 累计历史不清零。
- remaining仅是该维度的计数余量，不是已预留空间，不保证下一笔查询结果、成交回调或磁盘写入必然成功。未覆盖审计文件、identity文件、磁盘剩余空间或 Redis 内存容量。

## 行为与消费

| 情况 | 当前行为 |
| --- | --- |
| 仅 warning | 继续正常处理，Relay 可提示运维容量压力；不能把预警当用户撤销授权 |
| 查询累计数增长 | 继续接受查询，不再在执行入口使用 query_command_limit；串行流控和每笔查询超时保留 |
| journal 任一统计维度耗尽 | 不再领取新的 cmd.query/cmd.trade；三个 accepting_* 均 false，submit/cancel 原因为 `journal_capacity_exhausted`。query 在连接/恢复正常时使用同一原因；停止或未登录等更基础原因仍可优先显示 |
| 已有输出/回调/待核对责任 | 原 outbox、发布确认、ACK、回调处理继续按已有逻辑执行，不删除记录，不强制ACK。停止新领取不代表已有责任已完成 |
| 后续回调或持久提交实际失败 | 保留未决责任，默认常驻入口转为阻断诊断态；不能保证硬满额后无限续收或一定发得出心跳。显式限时测评入口保持旧失败退出行为 |

不新增顶层 state_text、业务错误枚举、命令动作或授权模式。总体状态仍沿原心跳规则；具体容量原因读取 operation_reasons 和 resource_usage，不仅看 state_text。旧输出样例不覆盖，旧心跳没有 resource_usage 时表示未报告，不填成容量0或无限余量。

Relay 请在现有严格解析器中接纳这一诊断对象及 `journal_capacity_exhausted` 操作原因：整数计数、五个metric字段、各维度硬上限和汇总一致性保留。OC已校验自身编码的计数、预警/满额标记及能力布尔一致性；这不是 Relay 独立复验结果。原查询/回报/重放语义不变，部署仍需两端兼容后安排。

## 测试边界

新增离线验证包含计数阈值、实际文件大小、读取不改变状态、相同意图重复投递计数及重启恢复；主循环覆盖超过旧查询上限后继续、8000条合成历史回调预警、10000条合成历史回调满额，核对SDK查询数、PEL、心跳和历史原值。均为测试入口的假SDK/IO和临时状态，不是实际柜台或容量压力验收，也未在业务Redis做OOM演练。

Relay 单一授权回归保留午休场景的旧 max_commands=2 输入；该字段不再形成执行入口的查询或报撤预算。

当前增量交付路径见 `OC_RELAY_CTP_RESIDENT_PROGRESS_20261118.md`；不覆盖上一轮 execution_authority_review_20261118 材料，不交换二进制，不清旧路由、状态或 once。
