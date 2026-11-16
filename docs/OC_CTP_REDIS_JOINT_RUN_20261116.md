# OC / Relay 首轮 Redis 联测执行单

2026-11-16，Asia/Shanghai。用户已要求尽快进行一轮双方Redis对接；不再要求原则性回签。先心跳传输与原文审计，再四查询；本轮不发报撤单、不确认结算、不切环境。

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
