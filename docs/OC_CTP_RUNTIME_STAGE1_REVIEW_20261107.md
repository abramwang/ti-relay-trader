# Relay 对 OC TEST 恢复与心跳入口的复核

日期：2026-11-07，Asia/Shanghai。接收方：OC；协调方：Relay。

## 结论

本轮材料及 Relay 接收兼容验证通过，可继续开发，不需要重新原则回签。规范 gateway 路由、原物理输出键、安全轨迹和跨日别名材料的上一轮缺口已补齐。此次收口限于传输恢复与未登录心跳入口，不代表真实 SDK、在线四查询、交易准入或完整 F0 验收。

交付包 SHA256：`aa15fc6f15fb732ae7bd6c046506748a1147fab6de68cfd82e71a3fbe97faf4d`。包仅含正文、JSON、日志及清单，已按原字节保存到 `testdata/runtime_review` 和其上级正文；8项内部SHA全部匹配，正文与用户附件完全一致，旧交付未改。

## 已复核证据

| 项目 | Relay 复核结果 |
| --- | --- |
| 版本边界 | Redis/主程序/sanitizer JSON均为`db6518608760cf3fc282d0ffb386786c61bc614d`且source_dirty=false；`22574ce`配置边界修正仅有交付声明及普通28项CTest日志，不把旧JSON算作该版本重测 |
| 规范路由 | 新输出实际物理键与body中的gateway一致，gateway与account故意不同；本地binding为`ctp.redis.test.transport.v2`、stream_routing=gateway_id。Relay不修改路由生成规则，不自动迁移旧日志 |
| 两组组件记录 | 共享Redis8.2.2与私有sanitizer Redis7.2.14各48个场景，包含20个崩溃、15个安全、2个分页/别名及11个运行时恢复场景；SDK均为模拟，不是48个柜台交易样本 |
| 安全轨迹 | 原输入、journal及关键阶段SDK/PEL/durable_ack已提供；失败后没有额外SDK调用或错误ACK。组删除后的pending为null并有原因，不按0处理 |
| 跨日别名 | 三次实际输入键/ID、原body、确认日5/6/6、同原意图字节及摘要已独立重算；SDK次数保持1，原输出关联不改为别名message |
| 查询分页 | 使用此次实际reply键及原body进入Relay临时PG，四条运输记录0,1,1,2只形成三页、一份三行持仓快照；owner恢复不重复发布，原文字节保持一致 |
| 主程序记录 | 三次进程返回码0/0/4，对应fresh/restart/unregistered_pending，会话不同；三种选取心跳能逐字节对应主程序记录。预检不写、审计半行拒启等未附完整逐系统调用轨迹的项，仅按OC报告及摘要记录，不升级为Relay独立执行验证 |
| ACK不确定 | ack_gap中pending=0仍包含OC_ACK_UNCERTAIN且recovery_clear=false，继续人工/明确证据核对，不自动解除 |

本轮未执行OC二进制或重跑其CTest；没有取得真实柜台身份或连接代次。二进制SHA仅作材料身份，不要求交换二进制或源码。

## Relay 兼容实现

原共享draft.2样例仍按既有严格规则处理。新增仅限TEST的`test_transport_recovery_only`心跳解析分支：

- 保留原body，`trade_date=null`和`connection_generation=null`不填假值；内部TradeDate空串仅表示该显式null，并有独立Recovery对象区分，不作为wire或API日期输出。
- 接受本轮payload状态及顶层镜像，要求镜像一致；未知模式/状态、遗漏镜像、冲突布尔、伪造交易日或连接代次、任一柜台/结算/快照/报撤就绪为true均拒绝。此分支不接受生产环境。
- `transport_recovery_clear`与诊断数量只作为恢复审计，不能推导柜台就绪、查询可发、lag=0或全账户已恢复。process_id保留原文，不当作原生身份。
- 心跳审计可解码这些有效未就绪消息，不再仅因null误报格式错误；`TestSessionGuard`显式锁存`heartbeat_transport_recovery_only`，后续ready不能复活同一个guard。

原文采集与业务消费门禁必须分开：broker_ready=false时仍可采集恢复输出/心跳审计，不能为了读取这些原文而放开业务guard。尚未将持久观察自动连接RunGuarded或9092服务。

## 验证

新增3项Go模型及2项临时PG测试，覆盖34条收到的心跳记录、镜像/权限/null反例、guard不能激活、原文恢复和实际物理查询键；新增6项Python材料/编码校验。相关28项Go/race通过，12.531秒、无跳过；103项Python全量、七包/cmd回归及vet通过。定向日志：`.runtime/ctp-runtime-stage1-review.log`。

测试命令见`tests/README.md`。PG为独立临时实例；已有消费回归仅使用随机TEST开发键，收到的OC原body未投递Redis业务流。本轮不重复上轮187项全量容量测试，不冒称新版本生产容量验收。

## 下一步分工

1. Relay继续持久会话观察、新消费窗口及审计/业务门禁分离；用OC真实运行样例对齐，不自造交易日、连接代次或新错误枚举。
2. 双方固定一组独立gateway/account/group、SourceID/epoch与短时窗口后，先做合成SDK输出经规范Redis到Relay PG的联合验证。下一轮证据需包含原输入、实际输出键/ID/body、会话及Relay持久结果，不能只给进程退出0。
3. 心跳间隔需与Relay MaxAge留裕量，首轮可选1秒、窗口15秒；OC配置允许30秒间隔，不代表它适合Relay当前默认30秒超时。无需OC改协议或延长生产进程。
4. OC继续已有查询调度/转换到运行入口的接线，以及external来源证明、待关联事件outbox/释放。真实登录、认证、结算及交易仍需各自授权，不用本轮恢复入口绕过。

没有新增Meridian需求。标的仍以Meridian定义为准，本轮不修改行情或证券映射。现有华鑫TEST服务、生产配置与下单权限不变；不自动切回生产。
