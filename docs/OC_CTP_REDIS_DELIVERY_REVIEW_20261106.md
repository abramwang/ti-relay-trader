# Relay 对 OC TEST Redis 传输与恢复交付的复核

日期：2026-11-06，更新至2026-11-07，Asia/Shanghai。对应 OC 基线 `07598735ea3f8bdc3a26e754a9f042b5f0b0f688`。

## 结论

**文档描述与 Relay 现有契约无新增冲突，接受为阶段交付，可以直接推进可信运行时接线，不再等待原则回签。** 无需修改 `relay.stream.v1`、业务错误枚举、accepted 语义或 Meridian 标的定义。

状态须分开记录：11-07已取得原始材料，Relay独立完成指纹、三组37场景记录一致性及原分页输出的解码/临时PG复验；材料缺口解除。没有重新执行OC C++/sanitizer、没有订阅该轮OC Redis输出，也没有完成真实SDK/柜台验收。安全场景和跨日别名的证据粒度限制如下，不能将材料一致性写成全部运行行为已独立重现。CTP主程序仍未开放在线运行，F0不标整体完成。

## 收到的材料

- [OC 原文归档](OC_CTP_REDIS_DELIVERY_20261106.md)，用户附件 SHA256：`3f132e85f2d36d17682e55bf5ae43a4a778f87a150e5ed6252b62b2c510ae19a`。
- 已收到版本、20 个崩溃窗口、15 个安全场景、2 个查询/重投场景及限制说明。
- 11-07用户提供 `testdata/redis_review`，7份原文件指纹全部匹配。清单第8项为上级交付正文，原目录缺此路径；Relay用已归档且同SHA的正文原字节补至`testdata/OC_RELAY_CTP_TEST_REDIS_DELIVERY_20261106.md`，没有改清单或收到的文件，现在`sha256sum -c SHA256SUMS`为8/8通过。
- 已核对27项普通CTest、2项非PIE sanitizer入口原日志及三组最终37场景JSON；日志均报告成功。不是Relay重新编译/运行OC测试，也不证明PIE异常消除或CentOS7支持。

## 11-07 材料独立复验

| 文件/范围 | 核验结果 |
| --- | --- |
| shared final，SHA `62353b228392d3184a47783e0d571ec2afba4d37d17c50120c4328578d4986ff` | Redis8.2.2；20崩溃+15安全+2查询重投，与说明一致 |
| private final，SHA `c00119c6d802aba4f603b4c79c8cfca958dcf07bccf3c93b1e9939d5471c0acc` | Redis7.2.14；同组37场景 |
| sanitizer private，SHA `8bfd9b59cdcf5ffb86bb7e7248ab23c81971e0aa3c638316183b6fd996b24aed` | 同一源码基线、37场景；模拟SDK，未把文件中的二进制指纹当可执行程序 |
| 旧shared文件 | 20崩溃+14安全=34场景，缺最终版本元数据，不替代final验收 |

三份final均为`0759873`、tracked source clean、synthetic_only=true，采集时间含+08:00、清理标志为true。逐项核对20个崩溃窗口的动作/窗口唯一性、SDK前后调用次数、恢复步骤、pending、durable_ack、输出条数；所有输出body重算SHA，原输出前缀及重投原字节一致，Stream ID合法且递增。两种动作的`after_ack`均为pending=0、durable_ack=false、uncertain_requires_reconciliation，继续作为未自动闭环，而非订单失败。

15个安全场景的名称、错误码和pass与说明一致，**这些记录没有完整SDK/PEL前后轨迹**，只能独立核对收到的记录，不额外声称证实每次没有SDK调用或错误后的PEL变化。跨日别名场景有原命令、3次投递/1次SDK/1个输出/pending=0，但没有三个输入各自的原文、业务日和Stream ID，不能独立重算各次意图或逐条ACK；后续可随运行时证据补齐，不要求重做现有组件。

三组查询各4条原body按`0,1,1,2`排列，重复页的body/message_id相同、Stream ID不同，唯一页为3。Python逐页schema校验通过；Go正逆序收页均只得到3行完整持仓。临时PG使用收到的原查询命令、固定合成会话前提和未改写的body/entry ID，逐条收页保留4条审计，只发布1份3行快照；新owner恢复后历史仍完整，不增加快照。没有把`rb0/rb1/rb2`虚构品种加入Meridian目录。

**路由边界**：材料中输入键是`relay:test:v1:<broker_id>:<虚构账户>:cmd.*`，输出body的gateway_id是`fixture-gw`，且输出数组未提供reply物理键。Relay标准物理键按gateway_id生成，本轮PG测试显式使用规范reply键作为离线前提，不能证明实测物理路由与wire一致。下一轮两端联合测试须使用已约定gateway_id的规范Stream并附输入/输出实际键；不为这组隔离夹具修改生产路由或直接订阅虚构账户。这是联调入场对齐项，不是已发生的线上故障。

新增5项Python材料检查、2项Go测试（其中1项临时PG，三套材料分别运行）；97项Python全量通过，`TestOCReceived|TestOCRedisReceived`共5项Go/race定向回归通过（7.748秒、无跳过），vet通过。只新增测试和归档/文档，无业务代码修改。本轮未重跑10,000行全量Go容量回归，不把前轮169项全量结果改写为本轮171项全量已通过。

复现：

```bash
.runtime/ctp-contract-venv/bin/python -m unittest discover -s tests/ctpcontract -p 'test_*.py'
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin scripts/test-futures-postgres.sh -run 'TestOCReceived|TestOCRedisReceived' -timeout 3m
```

不配置Redis或使用生产DSN；只读材料、临时Unix socket PG，无OC程序执行、Redis发布、登录、环境切换或服务重启。

## 接受的边界

| 事项 | Relay 处理及结论 |
| --- | --- |
| 命令输入与输出责任 | OC 管自己的 cmd.trade/cmd.query 组、pending 与 XACK；Relay 继续 XREAD 输出并持久化，不给 reply/event 建组或执行 XACK |
| 发布确认丢失 | 同 message_id、同 body 可以出现在多个 Stream ID；每条传输证据保留，业务页/事实幂等，不按 Stream ID 直接重复记账 |
| 三页重投 `0,1,1,2` | 11-07三组原body均通过解码和临时PG复验，重复页保留接收审计而不重复快照；原输出物理路由仍按上节边界处理 |
| 跨日同意图重投 | 保留首次命令与输出关联，不重建新订单或改写原 origin；登记重放不是再次调用 SDK 的授权。需要重新采样的查询应使用新命令/新幂等键 |
| intent/dispatch 未决窗口 | pending 恢复、未找到本地 intent 或 SDK 调用次数为零，都不自动取得发送资格；未知结果不自动重报报单/撤单 |
| ACK 已成功而本地未确认 | `OC_ACK_UNCERTAIN` 是本机恢复阻断，不是 Relay 业务错误、订单拒绝或撤单终态。PEL=0 不能替代本地 durable_ack；该窗口尚需运维核对，不写成自动恢复闭环 |
| SourceID、run_id、epoch | 接受 OC 的固定来源及原子校验边界；不把逻辑 SourceID 当服务器认证，不把本轮有限反例当所有回档/重建证明。Relay 不复制或修改 OC 的 epoch，也不将两端自己的来源标识强行设为同一个值 |
| SDK 与主程序 | 真实 Redis 加模拟 SDK 证明传输/恢复组件，不证明登录、结算确认、真实回调或生产可用。保持华鑫与生产隔离 |

OC 的 `OC_*`/`redis_*` 本机异常不新增到 Relay 业务 schema。错误输入保留 pending、满额停止消费符合保守边界；quarantine 服务、争议解除和账户恢复准入仍属待接线能力，不能根据本轮说明假定已有操作入口。

## 11-06 初轮复测记录

以下既有测试在临时 PostgreSQL、race 模式下通过，真实 Redis 仅使用 Relay 自己的随机 TEST 开发键，未使用 OC 的 `ctp_oc_it_850dcd8cf5db` 路由或虚构账户：

1. `TestCommandStoreCrossDayReplayAndAliasRestart`：跨日重投/新 message 同意图保持首次身份和原命令。
2. `TestQueryStoreAtomicCompleteAndReplay`：完整查询原子发布，同页跨 Stream ID 重投保留审计但不重复快照。
3. `TestStreamRedisQueryPagesHandoffAndRecovery`：真实 Redis 完整页交接与接管恢复；查询 owner 已退休时不复活旧半页。

三项测试全部通过，无跳过，耗时 2.047 秒；Redis 8.2.2，临时 PG 已由脚本清理，开发键精确删除无报错。这是 Relay 既有合成 fixture 回归，不是对 OC 37 场景或其三页原输出的独立复验。本轮只更新文档，无业务代码、迁移、部署、环境/权限变更或柜台命令。

## 下一步分工

**OC 直接继续可信运行时接线**：启动恢复扫描与账户级准入、可信身份恢复、external 来源和待关联事件释放、查询调度与真实心跳。先提供不默认登录/确认结算/放开交易的 TEST 运行入口及支持范围；新心跳输出请附现有字段含义和纯 JSON 样例，由 Relay 对齐消费生命周期，不另造租约协议。

**Relay 继续心跳/会话生命周期与独立只读入口**：把来源、会话/连接代次、TradingDay、快照和未决恢复责任共同纳入状态判断。当前最长五分钟TEST消费原型不能替代真实心跳新鲜度或会话撤销。收到的原分页材料离线复验已完成，下一步安排同一组规范隔离路由上的两端端到端测试；先模拟SDK输出经真实Stream进入Relay PG，之后才在另行确认的CTP账户和授权窗口做四查询在线验收。

联合测试前明确两端隔离路由、账户、来源配置和运行窗口；不订阅本轮已清理的虚构账户，不复用华鑫账户，不自动创建 OC 消费组或接管其 pending。登录、认证、结算确认、重启及报撤单分别确认边界。当前无需向 Meridian 提新需求，行情标的继续沿用 Meridian，OC 不直接对接 Meridian。
