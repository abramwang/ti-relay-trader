# CTP TEST 独立查询入口

日期：2026-11-16，Asia/Shanghai。

## 11月17日现场准备

10:23更新：用户接受临时存储风险，新gateway=`ctp-dongwu-asset-retest-20261117`已只读核实空且确认给OC。仍同TEST Redis/独立PG，新的route接收绑定从0-0开始，不沿用旧owner/cursor；候选epoch及单笔执行口径见[新路由确认](OC_CTP_TEST_ROUTE_LOSS_20261117.md#1023用户授权的新路由功能复测)。尚无新会话、登记或发送，等待OC完成初始化并回实际窗口；不再将存储整改作为本次功能复测前置。

11月17日更新：真实四查询已执行，3类completed、资金标准转换失败；OC独立审计已复核，资金原始BizType=NUL，业务快照未发布。见[本轮结果、OC最小修正项和最新owner/cursor](OC_CTP_DONGWU_FOUR_QUERY_REVIEW_20261117.md)。以下09:20的0查询和owner记录是准备时状态，不作为最新恢复位点。

截至09:20，Relay查询发送、reply接收、心跳采集三个CLI已重新构建，定向测试通过。新建独立本机数据库`relay_ctp_dongwu_20261117_test`，仅应用期货33..41、44、45表结构，不迁移9092业务库或生产库。

- 数据目录`.runtime/ctp-dongwu-pg-20261117/data`，Unix socket目录`.runtime/ctp-dongwu-pg-20261117`，port=15434、user=relay_ctp_audit；无TCP监听，目录0700，数据库时区Asia/Shanghai。PG当前运行，未安装自启动服务。
- 本机原有PG只有客户端，17.11服务端包下载并解包到`.runtime/ctp-pg-toolchain/root/`，未安装系统服务；后续pg_ctl使用其`usr/lib/postgresql/17/bin/pg_ctl`，不得重新initdb已有数据目录。
- OC确认BrokerID=4900、UserID=300052141（认证/登录回包校验）、InvestorID=300052141（明确配置，范围仍需业务回包核实）。逻辑路由保持`test/ctp_4900/ctp-dongwu-assessment-20261117/300052141`。
- 实际会话`ctpproc-62e233a7af3025ca87e72ca929e2cd61`于09:13:34进入read_only，连接代次1、TradingDay=20261117；09:14:03.164最后心跳为stopping。OC另确认09:14:03.163进程已退出，不能依据旧ready心跳发查询。
- 32条真实心跳已捕获到独立PG，共39,620字节，PG原文SHA256不一致数0；末位点`1794878043172-0`，owner=`919de788e230865c3f651c638650a02614f86d59ac5000bdde08c293ce35003f`。采集器已退出，PG保留运行；记录仍为raw审计，不伪造业务delivered。
- 采集输出`outputs/recovery/ctp_dongwu_joint_20261117/heartbeat-capture-20261117T012000Z.jsonl`，时点Redis检查仍保存在`outputs/recovery/ctp_dongwu_joint_20261116/preflight-20261117*.json`。本机一次性采集脚本`.runtime/ctp-dongwu-capture-20261117.go`只用于首次收件，不能直接重跑替代显式owner/cursor续读。
- 登记命令数0、发送责任数0，未发送四查询/报撤/结算确认。9092仍华鑫TEST，未重启API或OC、未切换环境。

下一轮请OC在双方准备完成后启动最长300秒的真实只读窗口，沿用已有路由/组/epoch，不清pending、不重新初始化。回传新session、代次、实际TradingDay、run_id/epoch及开始/结束时刻；Relay重新核对新鲜只读心跳，再按资金、持仓、订单、成交串行登记发送。每笔请求的ticket/SDK request_id及原始末页证据须通过OC审计及时取得，不能由reply反推预期身份；审核不及时则先保存raw，超时不补造即时成功。下一轮尚未启动，四查询和报撤均未验收。

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

## 独立回复入口：已实现

新增`cmd/relay-ctp-replies`，独立于9092和A股worker，不发送命令、不创建OC组、不XACK、不确认结算。复用原始Stream收件、分页组装、查询绑定及交易快照入账；没有新增wire字段。

| 模式 | 明确开关 | 结果 |
| --- | --- | --- |
| 默认 | 无运行开关 | 只检查身份、目标及可选请求证据；不读取连接凭据、不连接 |
| 状态 | `--status` | 只读PG owner/cursor/数量，不接管、不连接Redis |
| 原文采集 | `--capture --persist-test-replies` | XREAD原始reply到独立PG，未绑定项保留pending；不要求OC当前仍ready |
| 请求收件 | `--receive-reviewed --persist-test-replies` | 核对独立请求证据，检查新鲜只读心跳，接分页、完成快照与订单/成交事实 |

两种写入模式互斥。连接仅来自`RELAY_CTP_REPLY_DATABASE_URL`、`RELAY_CTP_REPLY_REDIS_URL`，不读当前服务配置；库名必须以`_test`结尾且匹配实际连接库。需要既有期货收件/查询迁移及000044、000045，程序不自动迁移。000045只在临时数据库验证，当前业务TEST/生产库均未应用。

### 接管与来源

公用参数沿用查询入口的`--env/--broker/--gateway/--account/--native-broker/--user/--investor/--database/--deployment-id/--target-file`。目标JSON格式与上节一致。

- 首次收件：`--cursor 0-0`，不提供previous-owner。
- 续读：先停止旧进程，以`--status`取得真实位点，再明确提供`--previous-owner <owner> --cursor <cursor> --confirm-stopped`。CAS在事务内完成，缺旧owner、旧cursor或身份不符均拒绝；失败不改变现有owner。
- 原文接收绑定SourceID/run_id/epoch/命令组，存入`futures_test_reply_sources`；后续不能用新run_id/epoch配合旧位点续读。恢复/替换实例须另行核对和迁移审计，不删记录绕过。
- 原文采集前后检查实际Redis来源，观测到变化即停止，已采集原文保留供排查，不继续业务分发。跨Redis/PG不是原子快照，不承诺识别所有未观测到的替换。
- 原文模式遇空即退出，不是常驻订阅。默认每页32条、最多256轮，可通过`--page-size/--max-pages`在1..256内限制；`--duration`最多5分钟。页面预算结束不表示整个Stream已读完。
- OC尚未创建reply Stream时，预检失败且不创建收件owner或绑定查询，不会伪造空结果或消耗查询绑定。待首条真实reply出现后再显式启动；本入口不替OC创建Stream，也不是等待空路由的守护进程。

### 独立请求证据

请求收件另需`--review-file`（最多16KiB）及`--evidence-file`（最多256KiB）。后者是经操作者核对、可对应本次原命令的OC请求审计原文，不得包含凭据，不是某条reply复制件。原文、清单及各自SHA256与查询绑定同事务保存在`futures_test_query_reviews`，不能仅存路径或覆盖旧证据。

以下是**Relay内部审核清单**，不是要求OC实现的新协议；`query_context`沿用既有Go持久模型的大写字段名，target沿用上节。尖括号是待核实项，不是可执行配置：

```json
{
  "command_id": 123,
  "batch_id": "<本次独立批次ID>",
  "review_id": "<请求审计审核引用>",
  "evidence_sha256": "<独立原文的SHA256>",
  "target": {
    "source_id": "<配置指纹>",
    "server_run_id": "<实际run_id>",
    "epoch": "<实际epoch>",
    "group": "<实际查询组>",
    "counter_session_id": "<实际session>",
    "trade_date": "<实际TradingDay>",
    "connection_generation": 1
  },
  "query_context": {
    "Route": {"env":"test","broker_id":"ctp_4900","gateway_id":"<已核对gateway>","account_id":"<逻辑账户>"},
    "Action": "account.asset.query",
    "OriginMessageID": "<原登记message_id>",
    "Ticket": "<OC本次任务ticket>",
    "CounterSessionID": "<实际session>",
    "ConnectionGeneration": 1,
    "SDKRequestID": 123,
    "TradeDate": "<实际TradingDay>",
    "NativeBrokerID": "<真实BrokerID>",
    "NativeInvestorID": "<真实InvestorID>",
    "NativeUserID": "<真实UserID>"
  },
  "timeout_ms": 60000
}
```

示例中的数值也不能照抄。SDK未调用时request_id应为null，ticket确实不存在时为空字符串；不得伪造。当前入口要求session、连接代次和TradingDay已独立核实；缺少这些事实只能原文采集。程序校验摘要、路由、原命令、已确认发布目标和字段一致性，**不会把摘要一致当成OC签名或原文语义已自动鉴定**；目前仍需操作者根据OC原始请求审计形成审核清单，不从第一条reply学习“预期值”。

只接受`published`的原始command_id，`attempted`/未知发送不能凭回复自动升级。request/correlation/idempotency由原登记命令绑定，alias不能代替原始message_id。timeout为1..300000毫秒，从PG原发送尝试时刻计时，不在启动接收时重置。审核来不及时先保存raw；超时记录不能靠增加新批次或刷新ID冒充及时完成，后续离线复核路径另行处理。

### 结果与退出

资产、持仓、订单、成交共用原分页契约。保留`ready/failed/invalid/expired/interrupted`，成功空列表与失败严格区分；查询SDK ID不符、矛盾终态不发布有效快照。所有输入先落原始收件，未绑定/格式不合法记录保留pending，不为清零伪造delivered。

每轮再次检查只读心跳及来源，失效即停。完整扫描当前已捕获待处理记录后才报告`query_terminal`；不会忽略同一轮后面的矛盾页。`ready`报告包含历史snapshot，订单/成交进一步进入既有期货事实表；不向股票账本写入，也不授予报撤权限。

正常退出或错误退出都会退役本次查询owner；未完成批次变为interrupted，完整历史保留但不能当当前ready。进程被强杀时没有伪造清理成功：新进程不能直接续接其查询，需核对后显式Recover；raw位点可以按CAS续读。原始收件恢复与柜台请求恢复是两件事。

CLI独立进程真实Redis采集/PG持久/状态只读/续读不重复已验证。四查询、SDK身份错误、失败、缺页、epoch变化、矛盾回报及关闭柜台后的raw采集通过隔离Redis回归；资产用例还经过上一节实际受控发布，再注入合成回复入库。其余场景使用合成发布证据；这些均不是OC实际登录/SDK业务回调，不能标作真实四查询通过。

本轮验证：全量`go test ./...`、三个CLI定向race及四包vet通过；显式TEST Redis+临时PG定向race为38.456秒，含独立CLI跨进程与原心跳CLI回归，无跳过；临时PG期货race回归86.389秒，排除上轮已验证且本轮未改算法的万笔容量用例，未配置Redis的专属用例依规则跳过。日志位于`.runtime/ctp-query-receive-final-20261116.log`、`.runtime/ctp-query-receive-pg-regression-20261116.log`。临时库/开发键已清理，既有17条OC心跳证据、真实路由及9092未动。

## 下一步：真实窗口验收

1. 在已授权白天窗口核实OC真实登录/session/代次/TradingDay、run_id/epoch/组及双方native身份；不复用今晚虚构账户材料。
2. 依次发布资产、持仓、订单、成交查询，先保留原文，再用OC对应原命令的任务ticket/SDK request_id原始请求证据绑定；及时取得证据后执行受控收件，核对末页/转换/PG快照与OC ACK。
3. 发布未知、缺独立证据或超时只保留审计，不自动重发。真实柜台认证/四查询/报撤及F0整体仍待验收，不因本轮单边程序通过提前开放交易。

本轮没有新增OC或Meridian协议要求，双方无需再做原则回签；11-17 09:05仍是候选窗口，不是自动登录任务。
