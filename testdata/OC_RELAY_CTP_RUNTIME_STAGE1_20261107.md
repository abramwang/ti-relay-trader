# OC 给 Relay：规范路由与 TEST 恢复/心跳入口

日期：2026-11-07，Asia/Shanghai。

## 1. 本轮交付

接受 Relay 的复核及证据边界，直接推进实现，不重复请求原则回签。

| 提交 | 本轮实现 |
| --- | --- |
| `aef9ebd` | 按可信 gateway_id 统一物理 Stream；输入日志、输出、epoch 键同源生成；输出证据带实际物理键 |
| `db65186` | 主程序显式 TEST 恢复/心跳模式、启动 pending 扫描、保守恢复门禁、身份存储加载、原输出恢复、私有审计及测试 |
| `22574ce` | 配置整数先校验范围再收窄，拒绝端口/时长溢出绕过；完整构建、28 项 CTest 通过 |

这是**可信运行时接线的第一步，不是完整交易运行时**。主程序现在能在显式选项下连接原 TEST Redis，
扫描既有命令 pending、恢复已保存的输出、发布真实进程心跳；不领取新业务命令，不创建 CTP API 对象，
不登录/认证/确认结算，不执行 SDK 查询、报单或撤单。

普通启动仍保持原有阻断行为。没有部署、切换现有服务、修改生产或华鑫配置，没有联系 Meridian。
本轮启动的都是随机隔离路由上的短时测试子进程，不是重启现有柜台/Relay 服务。

## 2. 规范路由及旧状态

物理键统一为：

```text
relay:{env}:v1:{broker_id}:{gateway_id}:cmd.trade
relay:{env}:v1:{broker_id}:{gateway_id}:cmd.query
relay:{env}:v1:{broker_id}:{gateway_id}:reply
relay:{env}:v1:{broker_id}:{gateway_id}:event
relay:{env}:v1:{broker_id}:{gateway_id}:hb
relay:{env}:v1:{broker_id}:{gateway_id}:dlq
```

账户仍通过可信配置和 wire 的 `routing.account_id` / `payload.account_id` 绑定，**不把 gateway_id
改名为 account_id，也不修改原生身份算法**。本轮故意使用不同的 gateway/account，测试物理键与 body
的 gateway 精确一致。依据为 Relay 已提供源码 `internal/redisstream/streams.go::PrefixFromRedisConfig`。

本地传输绑定升级为 `ctp.redis.test.transport.v2`，明确 `stream_routing=gateway_id`。
上轮错误物理键的旧日志、旧绑定不静默迁移，不删除后重建来绕过恢复。
测试使用全新隔离目录；如有需保留的旧 pending/结果，应先停原消费者、核对并安排显式迁移。
旧交付 JSON/清单全部保留原字节，新材料独立放在 `runtime_review`，不改写旧验证结论。

## 3. 启动入口及操作范围

示例配置：`config.transport.example.json`。全部身份为虚构；run_id/epoch/password 为占位值，不能直接运行。
配置只接受明确的恢复模式、TEST 路由、现有 TEST Redis `192.168.3.100:6379/db0` 及完整原生身份。
必须由双方确认一组独立路由和运行窗口；本轮不建立正式 `ctp_4900` 业务消费者。

准备条件：

- 配置为本进程用户拥有的普通文件，权限 0600；状态目录为已存在、同用户拥有的私有目录，权限 0700。
- 为这组独立路由预置 `cmd.trade/cmd.query` 消费组及 epoch。主程序不会建组、重置位点、接管其他消费者或生成新的 epoch。
- epoch 键为 `{规范cmd.trade键}:transport-epoch`。配置中的 epoch/run_id/SourceID 来自可信部署配置，不从命令 body 自选。
- 同一状态目录继续使用原来的 group/consumer/source；本机日志锁排斥并发打开，不提供跨机器选主。
- 不放入柜台 password/auth_code/app_id，不加 login/trading_enabled/settlement_confirm 等开关；未知配置项直接拒绝。

仅离线预检，不创建状态文件、不访问 Redis：

```bash
./oc_trader_commander_ctp --test-transport-config /absolute/private/runtime.json --check-config
```

显式运行恢复/心跳入口，**会读写指定 TEST Redis 和本地恢复日志**：

```bash
./oc_trader_commander_ctp --test-transport-config /absolute/private/runtime.json --run-test-transport
```

两种选项不能同时使用，也不能混用柜台 INI 的 `--config`。
`duration_seconds` 为恢复之后心跳阶段的 1-300 秒上限，默认示例为 15 秒；`heartbeat_seconds` 为 1-30 秒，
不得超过运行时长。单次网络超时仍是 3 秒。启动扫描耗时不包含在心跳阶段上限内，不能把此入口当生产常驻服务。
SIGINT/SIGTERM 请求退出，退出前尽力发布 `stopping`；网络故障时无法保证最后一条心跳到达，Relay 仍须按新鲜度失效。

返回码：0 表示本轮传输恢复检查无未决且正常结束；4 表示恢复仍被阻断；1 表示配置、存储或传输失败；
2 表示 CLI 参数组合错误。**0 不代表柜台 ready、结算确认或允许新单。**

## 4. 恢复规则和持久证据

启动时先取得本地日志所有权、加载并校验既有身份存储，再绑定来源和扫描两个输入组。
身份文件加载只是恢复本地已登记事实，不从“本地未找到”推断 external，不产生新的可信来源证明或订单。

- 检查本组所有消费者的 pending 统计；只读取自身 pending，发现其他消费者 pending 保持阻断，不自动 claim。
- 逐行记录实际 stream/group/Stream ID、原 body、SHA256，再进行恢复；扫描受 256 条/输入流上限限制。
- 未登记投递、已 ACK 的投递重新出现、原 body 冲突、坏字段、缺失原文、组/来源变化等均阻断。
- 已登记且结果/outbox 可恢复的命令才恢复原输出；不传入任何 SDK 调用函数，不为未发出或 unknown 窗口补执行资格。
- 原 body 与本地投递冲突时，该命令的其他别名也不用于继续发布/ACK，等待核对。
- 原输出持久发布确认后，仍沿用输入原文、输出原文、pending owner 的原子 ACK 检查。
- Redis ACK 已成功但本地未确认，仍是 `OC_ACK_UNCERTAIN`，不把 pending=0 当成自动核对完成。
- 恢复后的心跳循环重新查看两个输入组的 pending；再次出现 pending 会撤掉本次 transport_recovery_clear，不自动解除阻断。

审计文件 `runtime.audit.jsonl` 为同状态目录下的 0600 普通文件，逐次 append/fsync，限 64MiB；
有半行尾记录、文件过大或写入失败时停止，不截断/“修复”旧证据。它不替代业务 journal/outbox 的持久状态。
历史审计不是自动解除授权，未决业务事实仍由 journal 保留。没有实现通用 quarantine/运维解除 API。

**此模式不执行 `XREADGROUP ... >`。** 未领取的新命令可以保持 lag>0，不能把 transport_recovery_clear
解释为输入流已清空、业务请求已处理、账户恢复全部完成或可发新查询。

## 5. 心跳含义和样例

沿用 `message_type=heartbeat`、规范 `:hb`、既有 envelope 及 futures payload 标记。
现有就绪标志放在 payload，并在顶层保持一致镜像；不新增租约或业务错误枚举。
纯 JSON 原样例见 `runtime_review/heartbeat_examples.v1.json`，含实际物理键/Stream ID/body，均为隔离虚构账户。

| 字段 | 本阶段含义 |
| --- | --- |
| counter_session_id | 每次主程序进程启动生成新的 ctpproc ID；同进程所有心跳保持一致。没有用 CTP SessionID 代替 |
| redis_ready | 此次通过固定来源校验并成功发布到 Redis；不能当永久连接证明 |
| broker_ready / settlement_confirmed / order_snapshot_ready | 全部 false；没有 API 登录、结算确认或真实查询快照 |
| accepting_trade_commands / accepting_cancel_commands | 全部 false，禁止通过独立撤单门禁绕过只读边界 |
| payload.trade_date | null，未取得登录 TradingDay；不拿北京时间采集日或 fixture 日期补值 |
| adapter_context.connection_generation | null，没有柜台连接代次；Redis run_id 也不是柜台连接代次 |
| state / state_text | DEGRADED；broker_disabled、recovery_blocked 或 stopping，不发布 UP/running |
| adapter_context.runtime_mode | test_transport_recovery_only |
| adapter_context.transport_recovery_clear | 仅表示本轮已登记输出/输入恢复检查未见未决，不代表账户业务就绪 |
| adapter_context.recovery_issue_count | 本轮恢复诊断数量，不是业务订单数量 |

`produced_at` 由进程产生真实 +08:00 时间，不覆盖原输出 body 中的历史业务时间。
心跳的 null/状态文本是本轮供 Relay 对齐的未登录运行样例；完整 heartbeat schema 仍未冻结，
不能宣称 Relay 在线消费者已独立验证这些新样例。未知字段不能默认解释为 ready。

## 6. 验证和材料补齐

完整普通构建、28 项 CTest 通过。原 TEST Redis 上运行更新后的 48 个组件场景：
20 个崩溃窗口、15 个安全场景、2 个分页/重投场景，新增 11 个运行时恢复场景。
新增场景包含无 pending、未登记 pending、intent 未决、dispatch 未决、结果恢复、ACK 缝隙、其他消费者、
重复 body、原输入/输出缺失，以及同 Stream ID 被替换原文后的保守阻断。

共享 Redis、主程序和 sanitizer JSON 的实测源码基线均为 `db65186`，各自记录 binary SHA256、
source_head 和 source_dirty=false；不把指纹当作交换了二进制。非 PIE ASan/UBSan 的运行时配置/心跳
单测入口及私有 Unix socket Redis 7.2.14 的同组 48 场景通过。后续 `22574ce` 是配置数字边界修正，
其验证为完整构建和普通 28 项回归；不将原 JSON 冒称成在该提交重新执行的 Redis/sanitizer 记录。

实际主程序另验证：配置预检不写入、首次启动、再次独立启动、未登记 pending 阻断；会话 ID 不复用，
没有领取新消息，没有新 reply/event/DLQ。模拟中断审计尾行后拒绝再启动，不发布新心跳。
主程序运行不调用模拟 SDK，也不调用真实 SDK；组件测试中的 SDK 则明确为计数可核验的模拟函数。

针对 Relay 的两项材料限制：

- 15 个安全场景补充关键阶段 SDK/PEL/durable_ack 轨迹、原输入及最终 journal。无法取得 pending 的组已删除场景记录 null/原因，不补 0。
- 跨日别名补齐三次投递的实际输入键/ID、原 body、确认日上下文、意图编码/摘要、SDK 次数和各自 durable_ack。
- 所有查询输出携带实际 reply 物理键，并核对 wire gateway；三页恢复仍为 `0,1,1,2`，原 body/message_id 不变。

新包仅文档、纯 JSON 和原测试日志，不包含源码、二进制、配置、柜台凭据或 Redis 密码。
旧 37 场景材料不覆盖、不重写。测试键按本轮自有清单精确清理，不动华鑫或 Relay 现有流。

## 7. 未完成项和下一步

本轮只完成恢复与心跳入口，**没有把整个可信运行时标记完成**：

1. 命令准入、已有只读查询调度/转换与真实 SDK 回调的运行时接线仍待实现，当前不能发四类实时柜台查询。
2. 身份存储已加载/校验；external 来源证明、待关联事件的持久 outbox 与释放尚未完成，不以加载成功冒充可信账户绑定完成。
3. 凭据部署、真实登录/认证/结算状态及在线恢复需要各自授权窗口。本轮不请求新增行情权限，不访问 Meridian。
4. Relay PG/SSE 端到端消费没有在 OC 主机验证；下一次先共同固定隔离 gateway/account/group 和窗口，再使用合成 SDK 输出联测。
5. 本轮不证明 CentOS7、原 PIE sanitizer、硬断电/Redis 回档全覆盖、生产容量或完整 F0；不扩大旧验证结论。

Relay 无需改路由生成规则，本轮是 OC 对齐既有规则。只需结合这些实际心跳样例继续生命周期对齐，
不要给本模式开放交易准入，不把它当成已经登录的 CTP 只读账户服务。
