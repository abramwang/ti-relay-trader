# 东吴 TEST Redis 路由丢失排查与恢复边界

日期：2026-11-17，Asia/Shanghai。状态：暂停资金复测，未实施恢复。

## 给OC的确认

Relay本轮没有执行该路由的DEL/UNLINK、TTL设置、裁剪、FLUSH、消费组重建或epoch重建。实际操作为4条已登记的只读命令发布、XREAD原文采集及PG持久化，之后是本地审计/离线回归。查询发布EVAL只在准入检查通过后XADD；采集器不XACK、不删除源消息。开发回归的删除仅针对随机`relay:test:v1:ctp:relay-dev-*`，不使用真实东吴前缀；本日新增BizType回归不连接Redis。

本说明是对本轮Relay操作及相应入口的核对，不能代替共享Redis的全客户端操作审计，也不能凭没有慢日志就排除其他删除操作。

09:48:51全量SCAN确认以下真实前缀0键，run_id仍为`94f801dc68e3699cc681feb6dbb48f19e46aef9e`：

`relay:test:v1:ctp_4900:ctp-dongwu-assessment-20261117`

**接受OC的保护处理：不把它当作首次空路由初始化，不登录、不发查询、不重建组/epoch。**

## 已确认事实与原因置信度

| 项目 | 只读实测 |
| --- | --- |
| Redis | 共享TEST DB0，8.2.2，master；run_id与早上相同 |
| 内存配置 | maxmemory=536870912（512 MiB），maxmemory-policy=allkeys-lru |
| 内存轨迹 | 累计峰值535.53 MiB；09:50:03当前281.76 MiB，不能用当前低于上限排除此前压力 |
| 计数 | evicted_keys=11847、expired_keys=307，均为累计而非本次路由专属计数 |
| 短窗口复核 | 09:50:03至09:51:41计数未增加；不能声称在该窗口观察到目标键被淘汰 |
| 影响范围 | 东吴路由、11月16日虚构账户心跳路由均0键；华鑫TEST只剩cmd.query/cmd.trade/hb，09:06补发的凭据版本及current也已不在 |
| 其他负载 | 存在行情Stream及Prism因子缓存；09:50:55两条行情Stream的MEMORY USAGE约106.1/80.1 MiB，多个因子hash各约2.2 MiB。这是占用事实，不是某个项目删除目标键的证据 |
| 命令统计/慢日志 | 当前无flushdb/flushall统计项；del/expire是累计值。最近128条慢日志无本次目标路由删除证据，但阈值10ms，快速删除不会必然入日志 |
| 持久化 | AOF开启、写状态ok；不等于源键仍存在，也不保证AOF重写后仍有历史删除证据 |

**内存淘汰是目前最有力的风险线索，但具体路由的删除来源尚未锁定。** 尚无事故前evicted_keys基线、逐键淘汰事件或覆盖事故时段的服务端日志，不能仅凭当前累计11847将此事件定性为已证实淘汰。相同run_id只证明所连接进程身份未变，不证明其数据没有被删、过期或淘汰。

Redis官方说明：allkeys-lru可淘汰任意键，不要求设置TTL；noeviction在内存不足时拒绝相应写入而不自动淘汰现有键。交易命令、恢复epoch和凭据不应与可丢弃缓存共用allkeys淘汰策略。[Redis Key Eviction](https://redis.io/docs/latest/reference/eviction/)

需Redis管理员进一步查看09:26至09:47的容器/服务端日志、内存和evicted_keys监控、已有命令审计及未被覆盖的持久化证据；仅只读分析，不向运行实例加载历史AOF/RDB。没有历史审计时应保留原因未最终确认，不能凭空归责。

## 保留证据

- Relay独立PG仍有4条published命令、4条reply/8,695字节及327条hb/404,767字节，全部原文SHA256复算通过。原始失败、请求身份、源epoch和位点不变。
- OC已提供本地审计，含4笔原始结果和stopped/unresolved_commands=0；Relay此前观测两组PEL/lag=0。这支持已知4笔只读命令的核对，不证明丢失后Redis还能继续沿原位点恢复。
- 诊断文件：`outputs/recovery/ctp_dongwu_joint_20261117/route-loss-20261117T015003Z.json`、`...T015019Z.json`、`...T015055Z.json`、`...T015141Z.json`。完整证据含受限客户端地址及键名，不公开分发原件。
- 前三份诊断的负PTTL经Duration.Milliseconds转换显示0，不应解释为已设置0ms TTL；最后一份修正哨兵表示，华鑫残存3键均为-1（无TTL）。原报告未重写，此问题不影响键不存在、内存或统计证据。
- 09:48:51全量路由检查在`outputs/recovery/ctp_dongwu_joint_20261116/preflight-20261117T014851*.json`；本机只读诊断脚本`.runtime/ctp-route-loss-diagnostics-20261117.go`不含Redis写命令。

## 恢复方案（待授权执行）

1. 先保护存储。优先为Relay/OC提供独立交易TEST Redis实例，使用noeviction、足够容量、持久化与备份，并监控OOM/内存/缺键。仅换逻辑DB不能隔离实例级内存淘汰。若临时修改现共享实例，须用户/管理员协调其他项目的写入影响并持久化配置；Relay本轮没有CONFIG SET/REWRITE或修改其部署。
2. 固定事故边界。保留旧OC journal/identity/audit与Relay PG、原epoch和位点；不删除旧目录、不把4条命令/回包XADD回旧队列，不冒充恢复旧group/ACK。
3. 双方核对已知4笔查询及OC未决责任。若仍有不明在途/未决，不通过新名字规避。完成核对后建议显式批准新的隔离TEST路由、epoch和OC独立状态目录，旧链只读归档；逻辑/原生账户关系保持明确，不猜改账户。
4. 新路由由OC按首次初始化流程核实占用并创建，Relay创建新的source/route接收绑定；不得把旧cursor当新路由起点、伪造旧owner或改写原source。具体新路由须双方确认，本轮没有自行生成或启用。
5. 存储保护和新会话就绪后，确认运行96c55f7，仅新发一笔资金查询，核对请求BizType31及真实响应；依旧不开放报撤或结算确认。

若必须沿原路由恢复，需要额外设计双方显式运输代次迁移和已知责任消歧，不是重建一个同名epoch就算恢复；当前CLI故意拒绝来源/epoch变化配旧位点，不能临时绕过。

华鑫TEST也受同实例风险影响：在存储措施落实前不反复补凭据，不将旧进程已加载凭据等同于Redis状态持久可靠。生产配置、Redis及下单权限本轮未改；资金定向修正96c55f7与此次存储事故分开处理。
