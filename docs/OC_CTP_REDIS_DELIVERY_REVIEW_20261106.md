# Relay 对 OC TEST Redis 传输与恢复交付的复核

日期：2026-11-06，Asia/Shanghai。对应 OC 基线 `07598735ea3f8bdc3a26e754a9f042b5f0b0f688`。

## 结论

**文档描述与 Relay 现有契约无新增冲突，接受为阶段交付，可以直接推进可信运行时接线，不再等待原则回签。** 无需修改 `relay.stream.v1`、业务错误枚举、accepted 语义或 Meridian 标的定义。

状态须分开记录：OC 报告完成真实 Redis 传输及 37 场景恢复测试；Relay 本轮核对交付说明并复测自身相关能力，尚未独立核验 OC 原始实测材料，更没有消费该轮 OC 输出或完成真实 SDK/柜台验收。CTP 主程序仍未开放在线运行，F0 不标整体完成。

## 收到的材料

- [OC 原文归档](OC_CTP_REDIS_DELIVERY_20261106.md)，用户附件 SHA256：`3f132e85f2d36d17682e55bf5ae43a4a778f87a150e5ed6252b62b2c510ae19a`。
- 已收到版本、20 个崩溃窗口、15 个安全场景、2 个查询/重投场景及限制说明。
- 本机本项目尚未找到文中 `redis_review` 目录；共享/私有 Redis 原 JSON、CTest/sanitizer 日志及 SHA256SUMS 待补路径或纯材料包。无需源码、二进制或重新跑一遍同样测试；材料复核不阻塞双方继续实现。
- 27 项 CTest、两套 Redis 的 37 场景及 sanitizer 结果均标记为“OC 报告通过”。后续取得材料后逐项核对，不把摘要当原始输出，也不要求 Relay运行 OC 二进制。

## 接受的边界

| 事项 | Relay 处理及结论 |
| --- | --- |
| 命令输入与输出责任 | OC 管自己的 cmd.trade/cmd.query 组、pending 与 XACK；Relay 继续 XREAD 输出并持久化，不给 reply/event 建组或执行 XACK |
| 发布确认丢失 | 同 message_id、同 body 可以出现在多个 Stream ID；每条传输证据保留，业务页/事实幂等，不按 Stream ID 直接重复记账 |
| 三页重投 `0,1,1,2` | 与既有收页模型一致；未齐不发布，重复页同字节接受，异体冲突阻断。具体 OC 四条原 body 仍待材料复验，不宣称本轮已验证原输出 |
| 跨日同意图重投 | 保留首次命令与输出关联，不重建新订单或改写原 origin；登记重放不是再次调用 SDK 的授权。需要重新采样的查询应使用新命令/新幂等键 |
| intent/dispatch 未决窗口 | pending 恢复、未找到本地 intent 或 SDK 调用次数为零，都不自动取得发送资格；未知结果不自动重报报单/撤单 |
| ACK 已成功而本地未确认 | `OC_ACK_UNCERTAIN` 是本机恢复阻断，不是 Relay 业务错误、订单拒绝或撤单终态。PEL=0 不能替代本地 durable_ack；该窗口尚需运维核对，不写成自动恢复闭环 |
| SourceID、run_id、epoch | 接受 OC 的固定来源及原子校验边界；不把逻辑 SourceID 当服务器认证，不把本轮有限反例当所有回档/重建证明。Relay 不复制或修改 OC 的 epoch，也不将两端自己的来源标识强行设为同一个值 |
| SDK 与主程序 | 真实 Redis 加模拟 SDK 证明传输/恢复组件，不证明登录、结算确认、真实回调或生产可用。保持华鑫与生产隔离 |

OC 的 `OC_*`/`redis_*` 本机异常不新增到 Relay 业务 schema。错误输入保留 pending、满额停止消费符合保守边界；quarantine 服务、争议解除和账户恢复准入仍属待接线能力，不能根据本轮说明假定已有操作入口。

## Relay 本轮复测

以下既有测试在临时 PostgreSQL、race 模式下通过，真实 Redis 仅使用 Relay 自己的随机 TEST 开发键，未使用 OC 的 `ctp_oc_it_850dcd8cf5db` 路由或虚构账户：

1. `TestCommandStoreCrossDayReplayAndAliasRestart`：跨日重投/新 message 同意图保持首次身份和原命令。
2. `TestQueryStoreAtomicCompleteAndReplay`：完整查询原子发布，同页跨 Stream ID 重投保留审计但不重复快照。
3. `TestStreamRedisQueryPagesHandoffAndRecovery`：真实 Redis 完整页交接与接管恢复；查询 owner 已退休时不复活旧半页。

三项测试全部通过，无跳过，耗时 2.047 秒；Redis 8.2.2，临时 PG 已由脚本清理，开发键精确删除无报错。这是 Relay 既有合成 fixture 回归，不是对 OC 37 场景或其三页原输出的独立复验。本轮只更新文档，无业务代码、迁移、部署、环境/权限变更或柜台命令。

## 下一步分工

**OC 直接继续可信运行时接线**：启动恢复扫描与账户级准入、可信身份恢复、external 来源和待关联事件释放、查询调度与真实心跳。先提供不默认登录/确认结算/放开交易的 TEST 运行入口及支持范围；新心跳输出请附现有字段含义和纯 JSON 样例，由 Relay 对齐消费生命周期，不另造租约协议。

**Relay 继续心跳/会话生命周期与独立只读入口**：把来源、会话/连接代次、TradingDay、快照和未决恢复责任共同纳入状态判断。当前最长五分钟 TEST 消费原型不能替代真实心跳新鲜度或会话撤销。收到 OC 原材料后先离线复验，再安排同一组隔离路由上的两端端到端测试；先模拟 SDK 输出进 Relay PG，之后才在另行确认的 CTP 账户和授权窗口做四查询在线验收。

联合测试前明确两端隔离路由、账户、来源配置和运行窗口；不订阅本轮已清理的虚构账户，不复用华鑫账户，不自动创建 OC 消费组或接管其 pending。登录、认证、结算确认、重启及报撤单分别确认边界。当前无需向 Meridian 提新需求，行情标的继续沿用 Meridian，OC 不直接对接 Meridian。
