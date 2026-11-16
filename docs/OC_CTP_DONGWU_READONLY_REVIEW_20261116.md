# Relay 对 OC 东吴 TEST 真实只读增量的复核

日期：2026-11-16，Asia/Shanghai。接收方：OC。

## 结论与直接分工

接收`2290dc9`原始审计及`89fb095a309e1a14ff69fa0b6d96fdad48686a39`真实只读入口的实现交付。A从“待实现”更新为“已有实现、待部署与在线验收”；B完整报撤单和实时order/fill运行入口仍未完成。无需重复原则回签，也不要求源码或二进制。

- OC继续B：SHFE单笔报撤、原生身份及O/C/CT/CY、事件发布、准入和恢复。已有A按当前契约准备实际配置及授权窗口，不再重做。
- Relay已补新只读心跳接收兼容；下一步接独立TEST业务接收、可信请求登记/命令发送和有界查询运行入口。现有inspect/capture/audit及内部收件组件不是完整业务命令CLI，不手工XADD绕过请求登记。
- 本轮无新增Meridian需求。合约定义/行情仍由Relay协调Meridian，不能把交易API的查询就绪作为行情建设门槛。

本次没有登录柜台、确认结算、发布查询/报撤单、创建业务组/epoch、迁移运行数据库或重启服务。当前9092继续华鑫TEST，并非CTP已部署。

## 材料与独立复验

用户交付原目录`reference/readonly_review/`原样保留；同字节归档至`testdata/readonly_review/`用于自动回归。清单的上级交付说明在原目录缺失，其SHA与用户附件一致，已按原相对路径归档到`testdata/OC_RELAY_CTP_READONLY_RUNTIME_DELIVERY_20261116.md`。合计7项SHA匹配；交付说明SHA256：

`6066a3dcdbcdecca395a16d6387f424c33001f03bae1c71e82ceabbfe7d439e9`

JSON明确`synthetic_only=true`、`real_sdk_calls=false`、`redis_connected=false`。Relay独立复验：

| 材料 | 结果与边界 |
| --- | --- |
| 8个场景、25条heartbeat | 新模式解析、空交易日/连接代次、状态镜像一致性通过；原文不改 |
| 7条原始reply body | 既有四查询解码/批次完成或失败语义通过，无新增wire需求 |
| four_queries | 资金非空，持仓/订单/成交完整空；不是非空持仓维度或交易身份验收 |
| duplicate | 报告2个输入、1次SDK调用、1条reply；仅离线实现证据 |
| submit_failure / partial_shutdown | failed仍可能进程退出0；进程正常退出不代表业务成功 |
| publication_failure / trade_on_query_stream | 未交付输出、pending保留；不把未输出当成功空结果 |
| CTest / sanitizer日志 | OC报告32项CTest、2项非PIE sanitizer通过；Relay没有重编译OC，原PIE问题不宣布解决 |

样例路由`test/ctp_4900/offline-readonly/990000000017`与计划东吴路由不同，不能直接发布样例到Redis或拿离线身份登记在线请求。在线请求预期上下文必须独立核验，不能从收到的reply反推“预期值”。

## Relay 已完成适配

`internal/futures`接受显式TEST profile `test_broker_read_only`，与旧`test_transport_recovery_only`及既有draft.2分开校验：

1. 保留`connection_generation=null`和`trade_date=null`的未登录事实；登录就绪时必须有有效交易日和正整数代次。未知profile、非TEST使用、缺字段及镜像冲突拒绝。
2. `query_collection_ready`、`transport_recovery_clear`分别检查；`UP/read_only`不代表可报撤单。结算确认、订单交易快照及报撤准入四个字段在该profile必须为false。
3. 只读采集仍绑定独立确认的source、run/epoch、路由、session、交易日、连接代次，并检查心跳新鲜性。能力关闭、代次/模式变化、停止或过期使当前窗口终止，后续心跳不能自行复活旧窗口。
4. inspector和审计显示新模式事实，但`business_authorized=false`不变；原始body无损保留。旧恢复模式仍不能通过业务采集门禁。
5. 事务化心跳观察同时保存模式状态，避免跨观察批次丢失保护。只读采集门禁不是交易/查询命令发布授权。

### Relay 本轮执行结果

- `go test ./...`通过；没有配置数据库的测试按原规则跳过，不能据此声称在线验证。
- 定向`go test -race`与`go vet`通过，范围为futures及两个独立CLI；CLI包在定向名称下没有匹配用例，其全量普通测试由上一项覆盖。
- 临时PostgreSQL 17.11执行`TestOCReadOnly*|TestHeartbeatWindow*|TestOCRuntimeHeartbeat*`，race通过，5.867秒。新增原文审计落库及窗口能力撤销两项PG用例实际执行通过；需真实Redis配置的旧消费者用例跳过，没有把它们计为通过。
- 工具包SHA256与本机APT元数据匹配，仅解压到`/tmp/relay-pg-test-tools/root`，未安装系统服务。测试库使用独立Unix socket、退出销毁，不读取运行配置或连接业务数据库。

复现数据库定向测试：

```bash
RELAY_TEST_PG_BIN=/tmp/relay-pg-test-tools/root/usr/lib/postgresql/17/bin \
  scripts/test-futures-postgres.sh -run 'TestOCReadOnly|TestHeartbeatWindow|TestOCRuntimeHeartbeat'
```

临时工具若被清理，应指定已有的PostgreSQL服务端工具路径，不改用业务数据库。

## 入场与未完成项

沿用已接受的`test/ctp_4900/ctp-dongwu-assessment-20261117/300052141`、独立group/consumer和计划测试库；不是已预留或已登记。正式启动前核对：

- 实际源码/SDK/加载库与Trader创建参数`false`；UserID和InvestorID单独明确，不按账户同名推定验证完成。
- Redis来源、run_id、epoch、物理键及既有pending；不自动重建已有组、不清pending或旧身份。未取得epoch不能猜填。
- OC默认配置检查不联网；只有明确窗口才使用`--run-test-readonly`。维持`settlement_confirm=never`和`trading_enabled=false`，查询若被柜台限制则保留原始拒绝，不自动确认结算。
- 先保留真实登录及新鲜只读心跳，再按串行/有界预算执行四查询；逐请求核对SDK身份、末页、转换结果、reply原文、Relay审计及PG落库。pending=0不替代持久ACK责任核验。
- 实际非空订单/成交若缺持久身份，按失败保留证据，不造身份或成功空结果；实时事件另验。只读入口验收成功不代替券商要求的至少一报一撤。

四查询SDK输出、真实Redis传输到Relay数据库的端到端验证及B报撤单仍待；明日按实际具备的能力逐层验收，不作完成承诺。
