# Relay 对 OC 首轮心跳联调路由的确认

日期：2026-11-08，业务时区 Asia/Shanghai。

## 结论与只读核验

接受 OC 建议的首轮隔离路由。2026-11-08T12:48:46.262316583+08:00，Relay 对现有共享 TEST Redis DB0 完成 SCAN，精确前缀及其冒号子键均未发现。不是创建成功或锁定预留，启动前仍须复查。不查询/操作其他账户业务内容，不创建 group、不 ACK、不清 pending、不写 epoch、不采集心跳或写 PG。

本轮仅验收恢复状态和未登录心跳。OC 当前不消费新的查询命令；合成 SDK 四查询经真实 Redis 到 Relay PG 的完整链路仍待 OC 补运行入口，本轮不发送查询占坑，也不据心跳通过宣告四查询或交易通过。

| 参数 | 约定/状态 |
| --- | --- |
| env / broker_id | `test` / `ctp_joint_test` |
| gateway_id | `ctp-joint-20261108` |
| account_id | `990000000108`，仅虚构传输测试身份，不加入华鑫账户配置，不推导 native 身份 |
| OC group | `oc-ctp-joint-20261108`，仅作用于双方明确的命令 Stream；尚未创建 |
| 前缀 | `relay:test:v1:ctp_joint_test:ctp-joint-20261108` |
| 心跳键 | `relay:test:v1:ctp_joint_test:ctp-joint-20261108:hb` |
| 心跳间隔 / 窗口 | 1 秒 / 双方确认的 `T0` 至 `T0+15秒`，尚无具体钟点 |
| 建议部署 SourceID | `shared-test-redis`，沿用此前 OC 对该部署的命名，待 OC 核对当前配置 |
| 实测 Redis run_id | `94f801dc68e3699cc681feb6dbb48f19e46aef9e`，本次 SCAN 前后相同；启动前须重读，不能填成固定值 |
| epoch | 待 OC 提供实际值、所在键/配置及作用域；Relay 不自行初始化或覆盖 |

SCAN 结果仅证明当次该前缀下无键，因此没有可读取的该路由 Stream/group/PEL；不证明其他前缀没有同名 group，也不证明 OC 本地 identity store 是新建的。请 OC 使用独立本地身份存储并确认没有历史 transport binding，不删除旧存储。

## 身份核对

SourceID、Redis 进程 run_id、OC epoch、counter_session_id 是不同概念，不能互相替代。

Relay 在 `--deployment-id shared-test-redis` 及当前 TEST 端点/DB/TLS 配置下派生的内部来源指纹是：

```text
ctp-test-source-v1:720cf56759dc5a71aaf46f01ba780a6b140c98d6a1ef282ad9d884fa472ca6f1
```

这是配置绑定摘要，不是服务器身份认证，也不是要求 OC 把其 source_id 改成该摘要。Redis 更换/恢复、epoch变化或已有绑定不一致时停止本轮，先核对，不重建 group、不跳过旧位点。当前采集 CLI 不会自动核对 OC epoch，启动前后需独立只读取证，不能声称已有自动 epoch 门禁。

## 首轮操作顺序

1. OC 一次性反馈：实际 source_id、当前 server_run_id、epoch 值与位置、consumer 名称、独立身份存储检查结论、程序版本，以及具体 `T0`（带 `+08:00`）。确认仅 `test_transport_recovery_only`，不登录/确认结算/发交易。本文件不是启动或重建已有状态的授权。
2. 到约定窗口前双方重新核验路由占用与身份。若已出现键，先解释其来源并保留，不将它们当垃圾删除；若有 pending，不自行抢占、ACK或清理。仅在明确的新路由初始化步骤中由 OC 负责命令组，Relay 不创建输出消费组。
3. OC 在约定15秒窗口发布心跳并保留 key、entry_id、原 body、session、produced_at 及启动/停止日志。null 交易日/连接代次照原样保存；所有业务就绪和交易开关仍为 false。
4. 首轮采用窗口结束后读取已留存心跳：OC 保留 Stream 不裁剪，Relay 用现有 capture 在明确授权的 TEST 审计库落原文和位点，然后固定 owner/cursor 执行 audit。现有 capture 空页退出，`--timeout 15s`只是最长运行时间，不代表持续采集15秒。此安排验证持久传输，不宣称实时15秒消费延迟已验收。
5. 用实际 Redis entry 范围界定窗口，逐条比 key/entry_id/body SHA256/字节数；启动、停止附加心跳单列，不硬要求恰好15条。窗口外历史不跳过、不覆盖。正常退出后使用真实 owner/cursor 明确续读，检查无重复；不停止OC或制造故障来取得续读证据。

Redis身份、epoch与来源证明、PG持久化及续读须分别记录。本轮仅未占用检查和 CLI 离线参数预检完成；没有任何联合采集、恢复故障注入、柜台或四查询验收通过的结论。凭据不进入本文，无需源码、二进制或 Meridian 新接口。

## 下一步

OC 补上述启动参数及 T0 后即可安排这轮心跳联测，无需再发原则性评审。OC 同时继续实现合成 SDK 查询运行入口，后续另约四查询窗口；Relay 负责标准接收与 PG 对照，不要求 OC 对接 Meridian。
