# CTP TEST 独立查询入口

日期：2026-11-16，Asia/Shanghai。

## 当前已完成：校验、持久登记与受控发送

`cmd/relay-ctp-query`复用已有`DecodeCommand`、`CommandStore`与000037结构；本轮增加000044发送审计表，不修改wire、不连接行情或柜台SDK。

- 仅允许TEST CTP的资产、持仓、订单、成交四类查询；生产、华鑫、报单、批量报单、撤单及未知动作均拒绝。
- 逻辑路由与native BrokerID/UserID/InvestorID分别显式配置，不从账户号猜后缀或从收到的reply反推身份。
- 读取已经固定的命令JSON，保留原字节、message/request/correlation/idempotency身份；默认零连接，输出目标cmd.query键和原文SHA256。命令文件最大64KiB，解析按既有严格契约。
- `--register --persist-test-intent`成对启用后，只向指定独立TEST数据库登记。DSN来自`RELAY_CTP_QUERY_DATABASE_URL`，不读取运行配置；显式库名须以`_test`结尾，并与DSN和实际连接库名一致。没有自动建库/迁移。
- 重复命令复用原command_id；同message或幂等键改变意图时保留冲突审计，CLI以非零退出。不覆盖原命令；报告同时列出当前输入及原始message/body摘要，避免混淆别名。
- 默认检查与仅登记时`command_published=false`。显式发送结果按下表区分；`broker_accepted=false`、`business_authorized=false`不变，表示本入口不能证明柜台接受，也不授予交易权限。

发送必须同时指定`--register --persist-test-intent --publish-query --allow-test-query-write`，以及`--target-file`、`--deployment-id`。`--run/--publish`仍不受支持。不能拿登记或Redis发布结果当查询完成证明，不得用手工XADD绕过持久登记。

## 发送责任与准入

新增`futures_test_query_publications`，每个原始command_id最多一条发送责任。原始命令行锁串行化并发尝试，先提交`attempted`再调用Redis。表保存复核目标、发送前心跳entry/raw、采样/过期时间及最终Stream ID；心跳原文在JSON中以base64保存，原字节不改。

| 持久状态 | command_published | 含义及再次运行 |
| --- | --- | --- |
| attempted | null | 已登记尝试，可能尚未发送，也可能已发但回包/PG更新失败；返回未知，不自动重发 |
| published | true | Redis确认追加，保留entry_id；再次运行返回原结果，不重复追加 |
| not_published | false | Redis原子检查发现准入变化，未追加；保留本次失败，再次运行不重发 |

未能读取既有发送责任时同样不能声称`false`或安全重试。预检失败且没有创建尝试记录时不会发送；所有失败先核对审计。重新创建message_id不能作为消除未知结果的办法。确定未发布后若确需新查询，应重新审查目标并登记新的独立意图，当前CLI不修改原尝试。`observed_stream_id`仅表示进程曾看到Redis结果，不能代替PG成功记录。

发送前检查：

1. 仅显式TEST CTP来源；来源配置指纹、Redis实际run_id、既有epoch和唯一命令组均匹配，不创建组、不改epoch、不清pending。
2. 最新心跳严格符合`test_broker_read_only`，`query_collection_ready/transport_recovery_clear=true`，session、连接代次、TradingDay与复核目标一致；transport-only不能放行。
3. 心跳年龄小于30秒，未来偏移不超过2秒。Redis原子EVAL重新核对run_id、epoch、组、最新心跳ID/原字节及服务器时间后才XADD原登记命令。
4. 客户端禁用命令重试，直接EVAL，不做脚本缓存回退。网络超时/回包丢失/写后PG失败均不自动重发。

最新心跳即使正常推进，也会使原子检查拒绝旧证明，这是保守拒绝，不把它误报为OC故障。Redis与PG没有跨系统事务，本实现选择保留未知责任而不是声称exactly-once。恢复后的人工核验/结果补证入口尚待接通，不能删表清状态重发。

## 操作边界

以虚构身份说明默认校验形式，非东吴真实身份或可直接执行的业务命令：

```bash
go run ./cmd/relay-ctp-query \
  --env test --broker ctp --gateway relay-dev-query \
  --account 990000000117 --native-broker 4900 \
  --user fake-user --investor investor01 \
  --database relay_query_test --command-file /path/to/fixed-query.json
```

实际命令必须由操作者准备符合既有共享契约格式的原始JSON；不要复制测试夹具到真实账户路由，也不要每次重试刷新sent_at/message_id来绕过冲突。指定已有独立TEST库并明确授权后，才追加登记两开关；输出失败时先查登记审计，不能假定事务未提交。

发送另从`RELAY_CTP_QUERY_REDIS_URL`读取显式TEST Redis，不读取9092运行配置。`--target-file`最多4KiB，拒绝重复/未知/缺失字段。以下仅展示字段，全部占位值必须换成独立核实的真实事实，不能直接运行：

```json
{
  "source_id": "ctp-test-source-v1:<配置指纹>",
  "server_run_id": "<Redis INFO server 的40位run_id>",
  "epoch": "<该路由现有transport-epoch>",
  "group": "<该路由现有cmd.query消费组>",
  "counter_session_id": "<本次OC真实只读进程会话>",
  "trade_date": "<OC实际TradingDay YYYYMMDD>",
  "connection_generation": 1
}
```

代次1也只是结构示例，须以实际连接为准。`source_id`是既有`TestStreamSource.String()`的配置指纹（部署标识、地址、DB、TLS），不是OC说明里的`shared-test-redis`标签；应与同配置采集器的`--status`输出一致。配置指纹不能证明物理实例，另以run_id/epoch核验。原虚构账户的session/epoch不得复用于真实东吴路由。

独立库需事先人工准备000037及000044等所需迁移，CLI不会自动建库或迁移。本轮000044只在一次性测试库执行，当前业务TEST库和生产库均未应用。Redis需允许连接握手及`INFO/GET/XINFO/XREVRANGE/TIME/EVAL/XADD`，写键仅指定路由的`cmd.query`；缺权限即失败，不降级直接XADD。凭据仅私有环境变量，不进入文档、命令参数或报告。

## 验证

上一阶段5项CLI/race测试包括真实独立本机PG的登记、跨连接重开重放、改变动作冲突和原文不覆盖。本轮增加严格目标/危险开关、真实PG发送责任、8并发单次发送、回包丢失/PG写失败不重发和迁移回滚测试；TEST Redis随机`relay-dev-*`隔离路由验证实际EVAL、原文/entry一致、重放不重复及epoch/心跳/时间/代次/关闭/重复body拒绝。

Redis测试使用合成心跳与查询，只证明Relay发送机制，不是OC真实四查询验收。开发键已清理，一次性本机PG退出并删除；原心跳审计库17条/18,745字节/末位点`1794835752035-0`未改。没有向真实东吴/华鑫路由发查询或交易，没有登录柜台、重启9092或切环境。发送方法实际连Redis与PG的组合测试已运行，CLI发送全过程尚未做真实账户验收。

验证结果：`go test ./...`、CLI定向race及`go vet`通过。临时PG完整期货race回归通过（385.601秒，包含订单/成交各10,000笔与重放/回滚）；此轮未配置Redis，Redis专属用例按原规则跳过。另一次显式配置TEST Redis的发送/登记/心跳证据定向race回归通过（8.326秒）。本机日志分别为`.runtime/ctp-query-publication-pg-full-20261116.log`与`.runtime/ctp-query-publication-final-20261116.log`，不将本机日志当OC在线业务证据。

## 下一步：结果关联与四查询验收

1. 接通独立reply收件与OC原始请求证据，建立ticket/SDK request_id关联，再接既有分页完成证明和快照持久化；不得从首条reply自行取值当“独立预期”。
2. 发布未知时按原始消息、Stream和OC审计核查；不能因本地缺成功结果自动重发或生成新身份掩盖原责任。
3. 在已授权白天窗口按资产、持仓、订单、成交逐项验收，严格区分完整空、失败、未完成及成功。不因本CLI已能发送就标真实四查询链路完成。

本轮没有新增OC或Meridian协议要求，双方无需再做原则回签；11-17 09:05仍是候选窗口，不是自动登录任务。
