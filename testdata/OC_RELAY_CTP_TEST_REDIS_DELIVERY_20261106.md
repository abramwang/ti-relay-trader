# OC 给 Relay：TEST Redis 传输与恢复实测交付

日期：2026-11-06，业务时区 Asia/Shanghai。

## 1. 结论与版本

用户已明确允许 OC、Relay 在原 TEST Redis 开发调试。本轮已实际使用
`192.168.3.100:6379/db0`，不再只是文件模拟 Redis。没有修改 `relay.stream.v1`、业务枚举、
订单身份、交易 accepted 语义或华鑫运行行为。

| 提交 | 内容 | 提交前验证 |
| --- | --- | --- |
| `c591071` | 命令日志持久绑定可信传输来源，拒绝换源及无绑定历史的静默接管 | 完整构建、26 项 CTest |
| `b315e76` | TEST-only hiredis 传输、原文发布、严格 RESP2 解码、原子 ACK 门禁 | 完整构建、27 项 CTest；私有及共享 TEST Redis 各 34 场景 |
| `0759873` | 有界消费、真实 XREAD、查询分页断点重投、跨日同意图重投 | 完整构建、27 项 CTest、私有 Redis 37 场景 |

最终共享 TEST 复验基线为 **`07598735ea3f8bdc3a26e754a9f042b5f0b0f688`**，
采集开始时间 `2026-11-06T21:40:12.410132+08:00`。Redis `8.2.2`，
本机 hiredis `0.14`，Ubuntu 构建。最终源码无已跟踪文件的未提交修改；测试二进制指纹随 JSON 保存。

最终结果：27 项 CTest 通过；共享 TEST Redis **20 个崩溃窗口 + 15 个安全场景 + 2 个查询/重投场景**通过。
另在私有 Unix socket Redis `7.2.14` 复验同一组 37 场景，并运行非 PIE ASan/UBSan 配置。
后者不是原 PIE 异常已消除的证明，也不代表 CentOS7 已验证。

**本轮 SDK 调用全部是有持久计数的模拟函数。测试程序不链接 CTP Trader/Md 动态库，没有登录、
认证、结算确认、报单、撤单、托管凭据读取、服务重启、环境切换或生产部署。**

## 2. 隔离及协作约定

双方共用 Redis 服务，不共用各自测试执行中的命令流、消费组或恢复位点。

- OC 每轮创建 `relay:test:v1:ctp_oc_it_<随机12位十六进制>:<虚构数字账户>:...`。
- 最终共享复验使用路由 `ctp_oc_it_850dcd8cf5db`，37 个虚构账户 `990000000001` 至 `990000000037`。
- 输入流仅为该轮的 `cmd.trade/cmd.query`，组为 `oc-recovery-it`，消费者为 `oc-recovery-it-owner`。
- 输出侧实际使用 `XREAD` 验证 `reply`，没有给输出流建组或 XACK。OC 只 ACK 自己的命令输入。
- 每个场景只删除事先登记的六个自有键，最后逐键检查无遗留；没有 SCAN 批量删除、FLUSH、服务端重启或配置更改。
- 未操作华鑫 `00030484`、正式 CTP 路由 `ctp_4900` 或 Relay 已有业务流。Relay 不应订阅本轮虚构账户并当作柜台事实。

后续真正 OC/Relay 端到端联调时，再共同指定一组隔离的 CTP 路由、账户、组及来源配置。
本轮两端独立测试不等于 Relay 已实际消费本轮 OC 输出或完成 PostgreSQL/SSE 验收。

## 3. 已实现的传输边界

`RedisCommandTransport` 目前只接受可信配置中的 `env=test`，生产环境在连接前拒绝。
密码只从本地 TEST 配置读取，临时连接文件权限为 0600；不写入命令参数、持久身份、日志或交付包。

首次消费前，将端点/DB、人工配置的 SourceID、预期 Redis run_id、隔离 epoch、路由、账户、
group/consumer 绑定到本地日志。绑定后的日志不能静默改源；已有未绑定命令历史不能直接补绑定后接管。
原日志格式的绑定是附加字段，旧无绑定空日志可以初始化，旧有命令日志须先核对，不能删除日志来绕过门禁。

读取、发布和 ACK 使用固定 Lua 脚本，把 run_id/epoch 校验与对应 Redis 操作放在同一次执行内。
脚本不可由 wire 指定。需要 TEST ACL 允许相应键的 `EVAL/INFO/GET/XREADGROUP/XPENDING/XADD/XRANGE/XACK`；
测试 provisioner 另需建组、写 epoch、构造故障及删除自有键的权限，不要求把这些权限开放给生产消费者。
目前使用单机 Redis、RESP2、3 秒连接/响应超时，不声称已支持 Cluster、TLS 或 ACL 用户名登录。

具体行为：

- 非阻塞 `COUNT 1` 消费，保留 body 原字节；重复 body、未知字段、非法 RESP 结构、超 64KiB body、错误作用域拒绝。
- 不自动创建/重置消费组、不使用 NOACK、不自动 XCLAIM、不自动重连/重试。错误输入保留 pending，等待显式处置。
- 每个输入流本组 pending 达 256 后停止领取新消息，但仍允许读自身 pending；本地日志条数耗尽也先停止新消费。
- 从 pending 恢复的消息，即使本地未找到 intent，也不会取得新 SDK 执行许可。未解析消息、缺失本地历史不能被当作安全重报依据。
- 结果、原始 body、outbox、发布 Stream ID 延续既有 fsync/rename/fsync(日志目录)顺序；发布确认丢失时重投相同 message_id/body。
- ACK 前原子检查 epoch/run_id、输入原文、pending 所属消费者，以及全部已确认输出的 Stream ID 和原文。
  输入/输出被删除、所有者改变、命令冲突或发布未确认都不 ACK。
- Redis 已 ACK、进程尚未保存本地 ACK 确认时，PEL 不存在不会被解释为确认成功。
  返回本机 `OC_ACK_UNCERTAIN`，保持本地未确认，要求核对；不重报业务、不制造交易失败或撤单终态。

以上 `OC_*`、`redis_*` 是本机异常/测试证据，不新增 Relay 业务错误枚举。
错误输入的持久隔离队列、运维解除及 ACK 争议核对入口尚未接入主程序，不能声称已有完整 quarantine 服务。

Redis 语义参考：[XREADGROUP](https://redis.io/docs/latest/commands/xreadgroup/)、
[XACK](https://redis.io/docs/latest/commands/xack/)、[XINFO STREAM](https://redis.io/docs/latest/commands/xinfo-stream/)。

## 4. 九窗及附加场景

下表对 `order.submit` 与 `order.cancel` 各实测一次，是真实子进程在指定位置 `_exit`，
再打开原本地日志和真实 Redis PEL 恢复。SDK 列为模拟函数调用次数，恢复过程不增加次数。

| 中断位置 | SDK 次数 | 保守恢复 | 恢复后输入 pending | 输出条数 |
| --- | ---: | --- | ---: | ---: |
| intent 持久化前 | 0 | not_started；pending 重新登记也不授予发送许可 | 1 | 0 |
| intent 已持久化 | 0 | proven_not_submitted，不自动发送 | 1 | 0 |
| dispatch 已持久化、SDK 前 | 0 | outcome_unknown | 1 | 0 |
| SDK 后、结果前 | 1 | outcome_unknown | 1 | 0 |
| 结果后、outbox 前 | 1 | restore_output | 0 | 1 |
| outbox 后、发布前 | 1 | republish_same_output | 0 | 1 |
| XADD 成功、发布确认持久化前 | 1 | 原 message_id/body 重投 | 0 | 2，业务输出身份唯一 |
| 发布确认后、ACK 前 | 1 | 只 ACK 原命令 | 0 | 1 |
| 本地 ACK 确认后 | 1 | already_complete，不再 ACK | 0 | 1 |

附加 ACK 缝隙对报单/撤单各一例：Redis XACK 成功、本地确认前退出。
恢复后真实 pending=0、本地 durable_ack=false、`OC_ACK_UNCERTAIN`。这是**保守阻断通过**，不是该窗口已自动闭环。

15 个安全场景涵盖重复 RESP 字段、坏 JSON、错账户、pending 原文被删、消费组消失、
读/发/ACK 时 epoch 改变、输出/输入删除、pending 被其他测试消费者领取、run_id 不匹配、
日志 SourceID 改变、同消息矛盾意图及 256 条 pending 背压。

两个额外场景：

1. 真实查询编码器生成三页 position_page，第二页发布确认前退出。
   恢复后 XREAD 得到四条原 body，页序为 `0,1,1,2`，唯一 message_id 共三条，SDK 模拟调用一次，cmd.query pending=0。
2. 同 message 重投和新 message/同意图跨日重投，共三个输入 Stream ID、一次 SDK 模拟调用、一个原输出，全部对应输入分别 ACK。

报撤回执是隔离传输夹具，仅验证恢复/关联/原字节，不替代完整交易回执 schema 或柜台业务语义验收。
查询页来自正式编码器，但其 SPI 行仍是合成输入。保留旧的已交付订单/成交标准化材料，不据这些样例升级在线能力。

## 5. 仍未覆盖及下一步

**CTP 主程序仍不开放在线运行**，只支持已有配置预检。新传输组件没有启用账户、心跳、
登录/结算流程或实际 SDK 调度。真实四查询、运行时可信身份恢复、external 来源证明、待关联实时事件释放、
启动时 pending 扫描及账户级恢复准入、未知交易结果核对，仍需后续运行时接线和获授权在线验收。

SourceID 是可信配置的逻辑标识，不是 Redis 自带认证。run_id/epoch 能检测本轮演练的变化，
不能证明所有同进程回档、恢复旧数据或重建同名组的历史。运维重建/接管须更换受控 epoch 并重新核对，
不能复制旧 marker 来“恢复就绪”。本轮没有主动重启共享 Redis、回滚其数据或测试 ACL 变更。
错误 run_id 测试是配置不匹配拒绝，不冒称已做服务端真实重启测试。

37 场景不是硬断电、磁盘写满、真实 SDK 回调丢失、网络中途断包、Redis 持久化/副本故障或跨机器接管全覆盖。
本机 SDK 仍使用模拟函数；hiredis 系统库与厂商库未纳入 sanitizer 插桩。RESP 解码的 body 上限在 hiredis
接收分配后检查，不能宣传为对任意恶意 RESP 的内存硬上限。日志有界但没有自动压缩/清理，满额时停止领取。

Relay 可以继续原计划的可信消费者、分页账本和恢复测试，不需要更改协议或等待 SHFE 在线行情。
下一轮 OC 将继续可信运行时接线；正式启动 TEST OC、登录/认证、结算确认与报撤单仍按既有边界分别确认。
本轮不宣告 F0 冻结，不改变华鑫链路，不联系 Meridian。

## 6. 材料与复现

材料目录：[redis_review](redis_review/README.md)。包括最终共享/私有 Redis 原 JSON、sanitizer 运行证据、
CTest 原日志及 SHA256SUMS；归档不包含源码、二进制、连接配置、真实账号或密码。

以下命令会实际写入已授权 TEST Redis 的新随机隔离前缀，不应当作普通离线 CTest：

```bash
cd /home/Titian_Cpp
/usr/bin/python3 oceanus/src/oc_trader_commander_ctp/tests/run_redis_recovery.py \
  --binary oceanus/src/oc_trader_commander_ctp/build/ctp_redis_recovery_test \
  --shared-test --profiles oceanus/released/build_profiles.env \
  --output /tmp/oc-ctp-redis-new-run.json
```

依赖已有 `python3-redis` 与已构建的测试程序。输出文件必须不存在，避免覆盖证据。
只读取 profiles 中三个明确的 TEST Redis 连接变量，拒绝非 `192.168.3.100:6379` 的 TCP 配置。
连接信息不在命令行展开；测试结束逐个核对自有键已清理。
