# OC / Relay 首轮 Redis 联测执行单

2026-11-16，Asia/Shanghai。用户已要求尽快进行一轮双方Redis对接；不再要求原则性回签。先心跳传输与原文审计，再四查询；本轮不发报撤单、不确认结算、不切环境。

OC已回签首轮验收记录。Relay复核其引用的送审版SHA256为`48c65d335222854b7704c4df0511ff9915308a83c62cac0dcbc8aceb2b6d645c`，与本文件在`f18a2a2`版本原字节一致；以下新增记录后文件SHA自然变化，不代表原证据变化。接受双方各自验证范围，不重做心跳、不要求再回签。后续Relay已开始[独立查询入口](CTP_TEST_QUERY_RUNNER_20261116.md)：目前校验/持久登记完成，出站发送及真实四查询仍待。

## 首轮结果：实际心跳收件、PG及续读通过

OC实现`c8f4830185ac3b3f6f7b1ca8964c56afe127de72`实际运行未登录传输模式，21:28:56.037至21:29:12.036产生17条心跳，正常退出。Relay在进程结束后读取保留消息，21:34完成独立审计库验证，并完成PostgreSQL停止/重启后的原文与位点复核。

**本轮双端Redis心跳传输与Relay持久收件通过，无需OC为此修改协议。** 不代表实时延迟、柜台认证/登录、四查询、报撤或F0整体验收通过；只读业务运行入口仍为下一步。

| 核对项 | Relay实际结果 |
| --- | --- |
| OC材料 | `reference/joint_heartbeat_20261116/`，7项SHA256全部匹配；原样归档`testdata/joint_heartbeat_20261116/` |
| 真实来源 | 与本执行单隔离路由一致；run_id与此前相同，epoch实际为`joint-98ae9473c67f709b2e735aeb31a55da6`，前后未变 |
| 原文链路 | OC `runtime.audit.jsonl`、OC保留证据、Redis经实际capture入口写入PG，17条entry/body/UTF-8长度/SHA256逐项一致 |
| 全量范围 | `1794835736035-0`至`1794835752035-0`，共17条、18,745字节，无丢弃窗口外两条 |
| 15秒窗口 | T0=21:28:56.037，截止21:29:11.037，共15条，窗口末条`1794835750255-0`；后续普通心跳及stopping均保留 |
| 接收器进程续读 | 第一次独立CLI只收5条，到`1794835740097-0`后退出；第二个CLI明确旧owner/cursor续收剩余12条 |
| 再次启动 | 第三个CLI从末位点启动，新增0条，总数/字节/末位点不变；owner依正常接管更新 |
| 陈旧owner | 使用第一轮owner/cursor被拒绝，最终位点和owner未被覆盖 |
| PostgreSQL重启 | 停止、重新启动本机独立PG后，17条原文与SHA、最终owner/cursor、18,745字节全部保持一致 |
| 业务安全 | 所有心跳均transport-only；交易日/连接代次null，broker/结算/订单快照/报撤ready均false，末条stopping；所有审计输出business_authorized=false |
| OC命令组 | cmd.trade和cmd.query均pending=0、lag=0；Relay未新建hb消费组、未XACK或发送命令 |

最终Relay owner：`cffe69bb411fbd11b993988bc6c569f61540449322cd427229e5ef833701b1dc`；cursor：`1794835752035-0`。后续只在明确旧采集器停止且来源/epoch未变化时按此续读，不自动接管。

### 实际持久化位置和边界

- 独立本机PG17.11，数据库`relay_ctp_transport_20261116_test`，仅Unix socket，无TCP监听。数据目录`.runtime/ctp-transport-pg-20261116/data`，仅在这个新库执行33..41表结构；未连接或迁移任何现有业务库。
- 已停止本轮PG进程，数据目录保留；没有创建后台服务或自动启动任务，也未重启9092/OC。不是已经部署的常驻CTP运行库。
- 完整证据目录：`outputs/recovery/ctp_transport_joint_20261116/live-verification/`。含三次capture输出、陈旧owner拒绝、status、三页带原文的audit、`verification.json`、`pg-after-restart.json`和`pg-restart-verification.json`。
- `relay-audit.dump`已保存，SHA256：`f816bc3c3d76824a952456ed429128535653ac064edc1156e738974384fcb6af`。本机执行脚本`.runtime/ctp-live-verify-20261116.go`保留；它是一次性新库核验，不可直接重复初始化已有库。
- PG的17条原始收件仍为`pending`、delivered=0、quarantined=0。这是尚未转交业务处理的审计责任状态，**不是OC命令PEL积压，不应为清零伪改delivered**。本轮未登记命令、查询批次或交易事件。
- 新增`TestOCJointLiveHeartbeatEvidence`冻结本次材料的7项SHA、17条解析/原文、窗口和OC审计一致性，仅离线读取历史证据，不重新发布到Redis。

下一步直接接真实只读命令与四查询，沿用11-17 09:05候选窗口并核实实际柜台可用性；不重做此次心跳联测、不自动登录或确认结算。以下21:27及更早小节为执行前记录，未启动/未建库等历史描述以本节更新为准。

## 21:27 确认：今晚隔离心跳，明早真实只读

OC反馈私有配置及SDK预检已通过、尚未启动；东吴真实路由仍为空，尚无柜台会话/连接代次。Relay接受OC提议的今晚隔离路由，并在21:27:25完成全量SCAN：0键、无hb/命令组/epoch，检查前后run_id仍为`94f801dc68e3699cc681feb6dbb48f19e46aef9e`。原始报告在`outputs/recovery/ctp_transport_joint_20261116/`。这不是路由预留，OC启动前再核对。

可直接执行的首轮安排：

| 项目 | 确认值 |
| --- | --- |
| env / broker / gateway / account | `test / ctp_joint_test / ctp-transport-20261116 / 990000000116`，虚构账户 |
| Redis / DB | 现有共享TEST Redis，DB 0；与东吴配置同一来源，凭据不进文档 |
| Stream前缀 | `relay:test:v1:ctp_joint_test:ctp-transport-20261116` |
| OC入口 | 已实现的不登录恢复/传输入口，不使用真实登录入口，不发送离线样例冒充运行心跳 |
| T0 / 时长 | OC实际产生首条心跳的北京时间为T0，连续运行至少15秒、心跳间隔1秒；不必再往返约固定钟点 |
| 初始化 | OC按既有已审定规则首次初始化独立组/epoch，实际值回传；如发现旧数据则停查，不覆盖/清pending |
| 结束处理 | 保留Stream及本机证据；回传T0/T1、实际route/SourceID/run_id/epoch/group/consumer、进程session及首尾entry_id、心跳条数和原始body/SHA256 |
| Relay核对 | 读取OC保留的真实hb，按实际entry/body逐项比对并保存审计；不是要求两个进程同时启动 |

未登录心跳不补假交易日/代次，不声称四查询或报撤ready。若OC无法提供不登录入口，今晚不转用真实登录入口，保留到白天。

Relay已通过该隔离路由的capture零连接参数预检；计划审计库`relay_ctp_transport_20261116_test`尚未创建，没有启动后台采集器。CLI在首次空读退出，其timeout不是15秒持续采集配置；因此首轮明确采用OC运行结束后读取保留消息，再做持久审计，不假称已经驻留等候。

真实东吴只读联调候选：**2026-11-17 09:05（Asia/Shanghai）**，以实际柜台可用及双方运行条件为准。没有安排自动启动/登录/结算确认；实际开始前核对真实路由来源、测评模式及新鲜会话。Relay独立可信查询命令入口仍需补齐；今晚心跳通过不解除这一项，也不代替明早柜台验收。

## 现场检查结果

Relay在21:22、21:24两次读取显式TEST配置，完成目标前缀全量SCAN及前后Redis run_id核对：

| 项目 | 实际结果 |
| --- | --- |
| Redis | TEST可连接，DB 0 |
| 路由 | `test / ctp_4900 / ctp-dongwu-assessment-20261117 / 300052141` |
| 前缀 | `relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117` |
| 目标键 | 0；cmd.trade、cmd.query、reply、event、dlq、hb均不存在 |
| 消费组/心跳/epoch | 目标Stream不存在，尚无可核验运行证据；未初始化 |
| Redis run_id | `94f801dc68e3699cc681feb6dbb48f19e46aef9e`，两次检查及各自前后相同 |
| 部署标签 | `shared-test-redis` |
| Relay来源指纹 | `ctp-test-source-v1:720cf56759dc5a71aaf46f01ba780a6b140c98d6a1ef282ad9d884fa472ca6f1` |

这是时点检查，不是路由预留，也不能证明OC没有运行在别的Redis/路由。指纹绑定配置，不替代服务器或OC认证。原始报告保存在`outputs/recovery/ctp_dongwu_joint_20261116/preflight-*.json`，复查脚本为本机`.runtime/ctp-dongwu-preflight-20261116.go`。没有读取或修改其他账户数据。

## OC 请直接反馈运行信息

请通过用户转交实际启动信息，Relay没有直接控制远端OC进程的通道：

1. 是否已在上述TEST Redis DB0/规范路由启动，实际实现版本和runtime_mode。若路由不同，先给准确路由，双方不要各自换名。
2. SourceID、Redis run_id、实际epoch键和值、group/consumer，以及本次session；若已登录，再给真实TradingDay/连接代次和独立配置的原生身份。
3. 15秒心跳窗口的实际北京时间T0、首尾entry_id及原始body/摘要。未登录阶段可先验证恢复心跳，但不能标四查询通过。

当前为夜间，用户明确东吴仿真仅白天实际交易时间测试，**今晚不为Redis联测尝试真实柜台登录**。若OC有不登录的恢复/传输入口，可先做心跳窗口；若只有`--run-test-readonly`真实登录入口，则约定11-17白天窗口。合成SDK经真实Redis的联测需要OC明确支持的隔离入口/虚构身份，不能把归档样例直接发送到东吴账户路由。

首次初始化新路由的组/epoch由OC按已实现规则负责，先复查确实为空；发现既有pending/身份时不清除、不重建。Relay不代替OC生成epoch，不通过手工XADD给真实账户投递命令。

## Relay 本轮已实测

真实TEST Redis 8.2.2 + 独立临时PostgreSQL 17.11，随机`relay:test:v1:ctp:relay-dev-*`开发键，非东吴账户路由：

- 新只读profile的25条合成心跳改写到随机开发路由后，逐字节Redis收件、PG保存、分页重启续读、旧接收器隔离均通过。归档OC原始样例保持不变。
- 二进制原文、查询分页完整/中断恢复、仅已登记查询可分发通过。
- 启动前断连、中途断连、心跳过期及缺失停止通过。
- 定向race整轮7.568秒，无跳过；开发键正常清理，临时库销毁。`go test ./...`通过；全量命令中未配置数据库的测试仍按原规则跳过。

日志：`.runtime/ctp-redis-rehearsal-20261116.log`。新增回归：`TestOCReadOnlyRedisHeartbeatCaptureAndRestart`。这是Relay单边预演，不是OC在线SDK输出，不冒充两端联调完成。

## 本轮完成标准与后续

首轮心跳：OC实际写入hb，Relay读取/保存原文，并与OC首尾entry_id和body SHA256逐项一致；实际来源/路由/session状态完整，不依赖fixture造值。默认诊断/审计不授权业务。

四查询：在实际白天登录就绪后，Relay先完成独立可信命令登记/发送运行入口，分别核验资产、持仓、订单、成交的原始请求、分页完成/失败、reply入库及ACK责任。**Relay目前独立业务命令运行入口仍待接通，不能说只差OC启动就能完成四查询。** 不要求重写协议或交换程序包。

当前9092仍华鑫TEST；生产配置、权限、数据库和OC进程均未更改。
