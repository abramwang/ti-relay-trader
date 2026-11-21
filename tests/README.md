# 测试目录索引

当前测试覆盖 Go 核心模块、Python SDK、交易日任务、临时 PostgreSQL migration/repository、9092 页面/API 轻量冒烟和关键运维页面 Playwright 交互。

## 期货日账本按实体存取

000063和`trade_entities_integration_test.go`验证千组订单/成交只加载目标一组、日汇总不含事实副本、完整读取保持原结果、跨组乱序/费用/别名冲突与原文重放等价。覆盖旧0/1版本激活、原文保留、事务失败回滚、索引损坏阻断、并发完整读取一致性、旧读取器失败及禁止丢弃活跃版本2的降级。新别名表纳入现有只读空间巡检。

复现沿用下节隔离PG/Redis脚本，`-run '^TestTrade|^TestFillV2|^TestLocalProjection|^TestStreamDispatch|^TestCFFEX|^TestCaptureService|^TestStorageInspection' -timeout 12m`。公开报告仍是完整`TradeReport`，只有内部持久格式改变；日容量保护不变。同一大委托组、冲突列表和完整报告读取仍随各自数据量增长，不把合成回归作为线上延迟或稳定性验收。所有故障注入只作用于临时数据库，没有迁移或修改业务数据。

11-21验证：隔离PG/Redis及race广域回归477.298秒通过，包含万笔订单加万笔成交、持续收件、开平仓/撤单、查询应用和日账本；实体存取/旧格式激活/并发读取/存储巡检补验20.625秒通过。`go test ./...`、`go vet ./...`及worker/capture/replies/trade构建通过。全库Go测试未注入业务数据库，PG用例单独通过隔离脚本运行；这些耗时不是前后性能提升比例。

## 期货日账本增量检查点

`trade_checkpoint*_test.go`在000062上验证检查点重开与原文全量重建等价：60组固定种子乱序，每次恢复后追加一条，逐步对比报告与私有索引，覆盖费用补充、成交先到、稀疏委托、别名冲突、超量、混合v1/v2和非法原文。PG逐次核验重复/source冲突，50组独立订单成交时单笔只写关联两行、重投写零事实行；旧日首次激活、末尾事务故障回滚、原文/事实修改删除TRUNCATE、检查点缺失/损坏/计数差及降级拒绝均隔离测试。

```bash
go test ./...
go vet ./...
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-futures-postgres.sh \
  -run 'TestTrade|TestFillV2|TestLocalProjection|TestStreamDispatch|TestClose|TestCFFEX|TestCaptureService' -timeout 10m
```

检查点/整日报告仍整体保存，不是常数内存基准，日上限不变。无真实柜台/生产库连接、业务迁移或部署；临时PG故障注入不等于物理断电或整日生产验收。旧审计损坏测试在隔离事务内回滚，另有真实提交的损坏用例确认不能继续写入，不通过清空历史完成测试。

11-21本轮验证：万笔订单加万笔成交、批量末尾故障回滚及检查点首轮PG/race回归295.058秒通过；随后完整性检查补充后的持续收件/平仓/分发/存储巡检/检查点/旧账本回归98.186秒通过。`go test ./...`、`go vet ./...`及worker/capture/replies构建通过；全库Go命令未注入数据库，PG用例由上述隔离脚本另跑。这些是合成样本回归耗时，不是线上吞吐或前后性能提升比例。

## 期货只读存储巡检

`storage_inspection_test.go`与worker CLI测试覆盖80%/90%阈值、整数溢出、零上限、INFO缺失/重复、run_id不符、历史淘汰不当作当前速率、AOF失败、显式挂载点/inode不可用、错库/来源及缺schema不补零。临时PG和本地Redis验证实际CLI报告，并比较巡检前后数据库原文/计数/owner/cursor及Redis所有键内容，确保不抢占、不写业务数据、不创建receiver。已交接历史仍计保留量，不再计结果等待积压。

```bash
go test ./cmd/relay-ctp-worker ./internal/futures -run 'TestWorker|TestStorageInspection'
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-futures-postgres.sh -run 'TestStorageInspection' -timeout 4m
```

未提供隔离环境时集成用例跳过，不算已验证。日账本预警采用显式合成计数，不冒充50,000笔实际柜台压力；该报告也不是原文完整性核验。没有实际共享Redis/业务PG查询、物理磁盘耗尽、线上告警或持续负载验收；无新迁移，实际使用需要已有000059至000061 schema。

最终全量Go、vet、worker/capture构建通过；新巡检与既有CaptureService/积压续读隔离race回归通过（29.057秒）。统一`test-ctp-offline.sh`的本机Redis分组已纳入巡检，本轮没有重复执行该脚本的全部长测。

## 期货命令历史与结果积压

`command_backlog_integration_test.go`及既有命令/发布/回执测试在临时PG使用000061：实际登记4096笔后新意图拒绝、别名和冲突仍留档，完整结果释放后并发仅一笔占用余量，晚到矛盾恢复积压；独立64MiB字节上限和超过旧64MiB拒绝审计历史继续登记。原命令、审计、别名和风险责任不删，不以回执accepted冒充订单终态。

覆盖升级后保守回填、原文和结果摘要重验、完整失败/空/部分/未知发布的区别、独立查询结果、有界扫描、缺失/损坏拒绝、回执交接或未发布结果写入失败的原子回滚、重建读者不重发、超旧上限拒绝降级。所有数据为合成输入，不接业务Redis/OC。

```bash
go test ./...
go vet ./...
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-futures-postgres.sh \
  -run 'Test(Command|Reply|QueryPublication|Assessment|BoundQuery|QueryStore|Capture|Resident|CFFEX|CancelService|OrderService)' -timeout 10m
```

迁移和正常写入不得与旧版登记程序混跑；须先停相关writer再升级。实际磁盘预警/耗尽及长期归档未在本轮实现，不将局部SQL故障注入等同整盘故障验收。

本轮全量Go、vet及worker/capture构建通过。4096笔并发边界、64MiB在途和超64MiB历史审计压力用例两轮通过；扩大PG/本地Redis回归发现旧平仓夹具漏建000046交易发布表，已补齐，未放宽运行校验。新增测试初次写错结果表名也已修正。最终命令登记、历史重验、发布、分页回执、平仓夹具及实际本地Redis投影专项`-race`全部通过（24.995秒）；扩大回归其余用例通过，共享Redis用例仍跳过。不把这些结果当线上稳定性验收。

## 期货收件历史与积压

`stream_usage*_test.go`在临时PG迁移至000059，区分历史总量与pending/quarantined积压。覆盖50,000条合成交接历史后继续收件、64MiB积压满时整页拒绝但可继续真实查询交接、隔离不释放、旧原文重投/重建读者、增量计数与实际正文一致、迁移不修补损坏及超旧上限降级拒绝。既有Stream/Heartbeat、回执/查询投影及并发事务回归一并运行。不存在自动清理、伪造delivered或向OC发消息的路径。

11-21新增`resident_backpressure_test.go`及健康回归，测试schema至000060。以真实64MiB合成原文填满额度，验证worker继续处理已收回执/本地事件、原位点恢复、未释放前仅一次XREAD、隔离不释放、暂停期间源变化/已读与未读消息丢失/计数错误仍停、投影事务失败不部分交接、取消后显式续接不重算。逐通道健康、发布前关闭、另一通道不覆盖、停止后晚到观察不能重开及活跃状态降级拒绝均覆盖。

```bash
go test ./...
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-futures-postgres.sh \
  -run 'Test(Stream|Heartbeat|Reply|Capture|Resident|CommandStore|CFFEX|CancelService|OrderService|QueryStoreIdentityIntegrityAndMigrationDown)' -timeout 7m
```

脚本自建仅Unix socket的PG及指定的本地Redis测试实例，不读取应用配置或业务DSN。需要`RELAY_FUTURES_TEST_REDIS_CONFIG`的旧共享Redis用例保持跳过，不计线上验收。000059/000060须先停止相关writer再迁移；没有业务库迁移、OC重启或环境切换。磁盘预警、长期归档及积压无法自动关联时的业务处置不属于已完成范围。

扩大回归曾发现DELETE同步减少计数会掩盖原文丢失，触发器已限定为INSERT/状态交接，不对异常删除或正文改写重算计数；原`lost_stream_input`反例保留，新增缺失/篡改不能释放额度和不能降级隐藏的回归。常规Status是事务计数视图，不冒称逐条原文审计；发送前业务核验仍检测实际缺失。

修正后上述临时PG/本地Redis专项race回归通过（139.555秒），全量Go及`go vet ./...`通过；旧共享Redis用例依前述条件跳过。未做线上迁移、真实磁盘耗尽或整日稳定性验收。

11-21续读增量完成后，上述扩大回归`-race`通过（190.000秒），全量`go test ./...`、`go vet ./...`及CTP worker/capture构建通过；共享Redis用例仍跳过，所有容量/故障输入仅临时PG和本地Redis合成数据。

## 证券发送前登记

`registration_integration_test.go`在隔离PG迁移至000058，验证并发登记/唯一发送尝试、报单/批量/撤单成功与丢响应、重建Service重试不重发、原意图冲突、关闭开关防旧路径绕行、缺schema拒发、归档失败事务回滚、原文字节及固定基线历史、不可变/修订/降级保护。`command_registration_test.go`覆盖TEST/数据库/发布器启用限制，HTTP回归检查未知发布返回409及不要重发提示。

`command_once_test.go`以本地故障TCP服务读完XADD后关闭响应，检查配置过自动重试的原客户端不会使新路径重复XADD；另以独立Unix socket Redis核对实际原文及Stream ID。没有访问共享Redis或真实柜台，也未改变生产配置。

```bash
go test ./internal/orderflow ./internal/api ./internal/redisstream
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_BASELINE_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-position-baseline-postgres.sh
```

新发送路径默认关闭，仅TEST可启用。发布状态并非柜台执行/终态，旧发送者切换、原始回执/事件闭合及真实链路仍需后续验收。

## CFFEX服务开平仓接线

`cffex_execution_test.go`使用合成Meridian目录/快照、真实隔离PG和假发布器，验证TEST服务默认关闭、IF/IH/IC/IM显式品种/手数策略、B/S及O/C原价不改、CT/CY拒绝、CFFEX连续日盘与SHFE分离、最新持久持仓自动选择、失败/未知不回退、并发超平拒绝及发布未知不释放/重发。`terminal_execution_test.go`验证策略配置和复制隔离，`terminal_permissions_test.go`验证分市场显示时段，HTTP测试拒绝客户端覆盖策略/持仓ID并区分缺基线503。多手撤单覆盖部分/重复/冲突/晚到成交、全成拒绝、报单权限关闭后独立撤单。

```bash
go test ./internal/futures ./internal/api -count=1
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-futures-postgres.sh \
  -run 'Test(CFFEX|CancelService|OrderService|RelayExecution|TradePublish|QueryPublication|Assessment|MeridianPrice|NativePrice|DayMultipleOrdersAndOwnCancelPostgres)' -timeout 4m
```

没有使用业务Redis/PG或真实柜台；假发布器不代替真实时段/Redis原子门禁验收，时段另以固定时间的心跳门禁单测覆盖。合成价格/数量不是账户授权或实际合约参数。首轮持仓样例误加payload会话字段被严格解析拒绝，改正样例后复跑；接收契约未放宽。迁移000051未部署，生产发送、真实CFFEX柜台和保证金风控仍未验收。

最终全量Go、vet及交易CLI/worker构建通过；上述隔离race回归79.17秒通过，本地Unix socket Redis原生事实测试亦通过。`TestAssessmentRedisGateAndExactOnce`的12个共享TEST子项因未提供业务Redis配置而跳过，未接共享Redis；不将其记为线上通过。扩大回归曾发现收件停止在新增事务检查处更早被拒绝，旧断言期待`not_published`；按实际未生成发布尝试修正为`registered`并检查publication表为空，同键重试仍不发单，复跑通过。

## CFFEX平仓数量与长期挂单撤单

`close_capacity*_test.go`验证完整持仓基准、当前剩余昨仓、多空冻结和投资单元隔离、未确认意图占量、部分成交/撤成余量、重复/逆序及重新读取持久事实、冲突与整数边界。`close_reservation_integration_test.go`在独立schema显式迁移000051，使用真实CommandStore/收件/回复投影/TradeStore验证同事务命令及数量预留：并发超量、同单重试与别名、跨gateway竞争、部分成交撤成、仅撤单登记不释放、写盘回滚、未管理/损坏事实和降级防丢。

`close_baseline_integration_test.go`补同会话已终态平仓的两轮快照吸收、原预留重读、更新快照拒用旧基线、登记竞争、事务回滚；工作态/未发布/发布未知、重叠采样、持仓/冻结变化及跨会话不猜测承接。共用发布事务用合成transport验证预留复核、迟到矛盾、已有执行事实、输入积压、结果未知及重启不重发。没有启用公开CFFEX发送策略，不接Redis/真实柜台，生产/CT/CY仍拒绝。`cancel_execution_test.go`新增旧工作态仍可撤、收件停止/陈旧/owner变化仍拒绝；未来时间、终态回退、重复撤单、成交竞态及发布前复核继续覆盖。

```bash
go test ./internal/futures ./internal/api -count=1
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-futures-postgres.sh \
  -run 'Test(CFFEXClose|CommandStore|TradeStore|ReplyProjection|CancelService|OrderService|CaptureHealth|.*ServiceCapture|DayMultipleOrdersAndOwnCancelPostgres)' -timeout 3m
```

只使用临时PG及Unix socket Redis和合成输入，不使用业务DSN、共享Redis或真实柜台。脚本默认`/usr/lib/postgresql/17/bin`在仅安装客户端的环境不可用，需要上面的本地工具路径；不为测试安装/启动系统数据库服务。

## CFFEX双包接收回归

11-19新增`cffex_delivery_test.go`、`cffex_delivery_integration_test.go`及`test_cffex_delivery.py`。原样材料在`testdata/oc_ctp_cffex_{facts,execution}_review_20261119/`，13个清单文件全部校验；215条执行原输出、21条选取原文、32处成交v2及独立事实回复核对通过。Python独立重算九段身份，Go核对原命令关联及失败/空/部分语义；IF2701、101手和3500.21均为合成参数，不是实际合约规格或可发送订单。

```bash
.runtime/ctp-contract-venv/bin/python -m unittest discover -s tests/ctpcontract -v
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-futures-postgres.sh \
  -run 'TestCFFEX|TestFillV2|TestCommandStore|TestSubmit|TestConstraintsRequestReview|TestReplyResult|TestExecution|TestTradePublish|TestOrderExecution' -timeout 3m
```

隔离PG四组正序/逆序、重建读者及原文重投验证部分成交后撤单/成交先到，各8委托8成交不重复入账；只启动临时库及本地Unix socket Redis，不连接共享Redis/OC或业务库。全量Go、vet、112项Python及上述定向race通过；实际发送护栏保持原边界。首次回归暴露旧测试仍把CFFEX视为非法交易所、旧命令表降级漏撤000050外键，已分别改用DCE反例及逆序撤依赖，不放宽业务降级保护。未验证真实CFFEXSDK、在线常驻、物理断电或生产。

## CTP持续收件服务隔离回归

11-19新增`capture_service_test.go`及`cmd/relay-ctp-worker`离线CLI测试，后续`reply_result_test.go/reply_projection_test.go`覆盖持续回执与完整查询。下列入口自建一次性PostgreSQL和仅Unix socket的Redis，不读取应用DSN或连接共享Redis/OC；迁移000049/000050只在临时库执行。实际worker子进程测试先SIGKILL再重启/SIGTERM，核对原文不重复、持久owner/cursor续读及零命令发布。

```bash
go test ./cmd/relay-ctp-worker
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_LOCAL_REDIS_SERVER=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server \
LD_LIBRARY_PATH=/home/ti-relay-trader/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu \
  scripts/test-futures-postgres.sh \
  -run 'Test(ReplyResult|ReplyProjection|CaptureService|ResidentPair|ResidentLocalProjection|ResidentCapture|CaptureHealth|CommandHistory|Terminal|QueryStoreIdentityIntegrityAndMigrationDown)'
```

默认race。最终定向回归通过，无跳过；覆盖显式旧位点接管、竞争读者/来源变化拒绝、owner事务故障回滚、输出失败后恢复、当前会话本地投影和取消时持锁健康写入。首轮断言修正及随后发现的真实停机竞态均保留开发记录，不将中间失败记为通过。全量Go/vet及worker离线配置校验另行通过；没有业务库迁移、线上收件器部署、柜台或整日稳定性验收。

回执增量覆盖四查询及OC九组事实原文、六组失败回执、分页重排/跨重启补齐/重复、迟到矛盾永久失效、来源/请求/会话错配、发布未知延后、原文损坏、交接失败事务回滚和降级防丢。accepted/failed不生成订单/成交状态。浏览器`futures_command_history_smoke.py`全拦截API，检查四查询持久结果、独立回执列、错误/分页、桌面及手机无页面溢出；不得将合成样例发往账户Stream。首轮测试夹具的空资金断言、发布未知finished_at及损坏顺序已修正，保留失败事实，不称首跑全绿。

11-19固定TEST账户只读浏览器复验可使用下面入口。`--require-persisted`验证持仓及命令回执已落库、回执不改变关联委托终态；所有非只读API请求被拦截。后半段分页/失败场景仅浏览器拦截数据，不向Redis发布。使用新的输出目录保留前轮证据；当前样本结果18持仓行、7命令/6当前会话回执，桌面/手机无页面溢出，写请求及页面异常均0。系统Python未安装Playwright，使用项目`.venv`，无需另装依赖。

```bash
.venv/bin/python tests/integration/futures_account_smoke.py --require-persisted \
  --output outputs/recovery/ctp_capture_activation_20261119/browser
```

本轮在线启用及34条原文核对记录见[启用结果](../docs/OC_RELAY_RESPONSIBILITY_SIMPLIFICATION_20261118.md#11月19日test持续收件启用)，与上面的隔离迁移/故障注入测试分开。没有新柜台查询或报撤，不将既有回包处理写成实时新回报验收。

扩展旧账本降级回归时同步补齐000050/49/48的逆序撤销，避免新表残留导致旧四表数量断言失败；不删除真实数据或修改业务降级保护。共享Redis专用测试不在本轮授权范围，使用`-skip '^TestReplyReceiveCLIRealRedis$'`明确排除，其他数据库/本地Redis测试照常执行。

## 历史验证记录

11-07证据导出：`heartbeat_evidence_integration_test.go`新增5项PG，CLI子进程集成补audit默认隐藏正文/显式原文/旧锚点拒绝。覆盖只读分页与锚点一致、空body/坏UTF-8无损还原、跨Stream游标、元数据/尾部/SHA/顺序损坏、8MiB页预算、空收件与stage1分类。用下方临时PG脚本及Redis双开关执行`-run 'TestHeartbeatEvidence|TestHeartbeatCapture|TestHeartbeatInspection' -timeout 3m`，17项Go/race通过、19.226秒、无跳过；3项CLI/race、103项Python、七包/cmd/vet通过。日志`.runtime/ctp-evidence-regression.log`；临时PG及随机开发键清理完成，无OC业务流读写或运行库迁移。本轮未重复前轮万笔容量回归。

11-07持久采集入口：新增1项模型、6项PG（3项含真实TEST随机开发hb键，1项构建执行`relay-ctp-capture`子进程），以及`cmd/relay-ctp-capture`的3项离线护栏。覆盖无native的hb限定、禁止业务guard、只读status不写/不连Redis、初建/续读CAS及8路竞争、旧位点/旧owner拒绝、提交失败回滚、完整原文及页预算、输出失败后按真实checkpoint恢复。CLI子进程使用本机临时PG下独立`*_test`数据库，结束删除；不会创建/迁移应用数据库。最终`-skip '^TestTradeCapacityTenThousandOrdersAndFills$' -timeout 3m`的211项Go/race通过，81.305秒、无运行时skip；万笔订单/成交恢复单项在首轮日志中通过（299.39秒），产品代码相同，仅CLI测试夹具修正。合计212项（125项PG）分批覆盖通过，另103项Python、3项CLI/race、七包/cmd及vet通过。临时PG销毁、随机开发键清理无报错。

日志`.runtime/ctp-capture-final-regression.log`为修正后的非万笔套件；`.runtime/ctp-capture-cli-targeted.log`为7项采集定向复验。首轮`.runtime/ctp-capture-regression.log`中的CLI测试曾因夹具修改`pgx.Config.Database`后仍使用保留原DSN的`ConnString()`而被目标库护栏拒绝；已改用结构化URL生成临时目标DSN，最终套件真实CLI通过。这个夹具失败不算产品验收通过，也不能把首轮整文件写成全量通过。复现沿用下方Redis双开关及临时PG脚本；CLI离线用`go test -race ./cmd/relay-ctp-capture -count=1 -v`。用户CSV和收到的OC材料未修改。

11-07只读诊断入口：`heartbeat_inspect_test.go`新增5项模型、`cmd/relay-ctp-inspect`新增5项CLI测试（含真实TEST随机开发hb键；无开关则该项skip）。39项相关Go/race通过，22.216秒，无跳过：临时PG脚本配合`-run 'TestHeartbeat|TestTestSession|TestTestConsumer|TestOCRuntime' -timeout 3m`。CLI使用同一Redis双开关执行`go test -race ./cmd/relay-ctp-inspect -count=1 -v`，5项全过、1.052秒。验证默认不读取凭据或联网、prod/huaxin拒绝、分页/重读不变更Stream/group、旧源时间/null不造值、坏报文保留和错误脱敏。日志`.runtime/ctp-inspection-{regression,cli}.log`；103项Python通过，运行环境为`.runtime/ctp-contract-venv/bin/python`，不是系统Python或浏览器`.venv`。七包/cmd/vet通过；本轮未重跑下方200项容量全量。CLI用法见`docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md`，命令自身无DB、无迁移或生产操作。

11-07持久心跳窗口增量：`heartbeat_window_integration_test.go`新增8项PG测试（2项用真实TEST开发键，含多个子场景）。覆盖显式历史边界、原文/观察双事务、提交失败不续期、顺序断开不跳过、接管重建、源时间/重投、坏SHA/上下文/隔离、stage1拒绝业务准入、慢PG期间过期取消、128窗口预算与非破坏性降级；真实消费覆盖bootstrap积压、运行失效/无心跳、查询退休及完整快照保留。000041只在临时PG执行，不迁移运行库。定向参数`-run 'TestHeartbeatWindow|TestTestSession|TestTestConsumer|TestTradeStoreMalformed|TestQueryStoreIdentity' -timeout 3m`沿用下面Redis双开关；日志`.runtime/ctp-heartbeat-window-targeted.log`。全量日志`.runtime/ctp-heartbeat-window-regression.log`；全量通过后，最终退出顺序和+08:00审计调整另以24项定向race复验，18.388秒、无跳过。

11-07 OC runtime-stage1增量：`oc_runtime_delivery*_test.go`新增3项模型及2项PG测试，核验34条原心跳、TEST/null/镜像/全false门禁、guard不可激活、原文持久恢复及实际reply物理键的0,1,1,2分页去重。`test_oc_runtime_delivery.py`新增6项材料/编码检查（8项SHA、两组48场景、主程序/选取心跳对应、安全轨迹、别名意图重算）。沿用下方Redis双开关及临时PG入口，`-run 'TestHeartbeat|TestTestSession|TestTestConsumer|TestOCRuntime|TestOCRedisReceived' -timeout 3m`：28项Go/race通过，12.531秒、无跳过；103项Python全量及七包/cmd/vet通过。日志`.runtime/ctp-runtime-stage1-review.log`。未运行OC程序，原交付body仅离线/临时PG使用；下方187项容量全量为前轮结果，本轮未重跑。

11-07心跳持久审计增量：`heartbeat_audit_integration_test.go`新增5项PG测试，其中2项使用真实TEST Redis随机开发hb键；`TestTestSourceFingerprintAndSafety`增加hb来源绑定检查。覆盖全序列/隔离/坏原文/跨账户、分页、owner接管、checksum损坏、错角色禁止移交、客户端续读、PG提交失败全回滚和未读断开消息丢失。审计不恢复guard、不标delivered、不接OC业务流。定向`-run 'TestHeartbeat|TestTestSource|TestStream' -timeout 3m`以race通过，14.456秒，无跳过；运行需要下方Redis双开关和临时PG入口。

11-07心跳失效保护增量：`heartbeat_test.go/test_session_integration_test.go`新增9项模型及2项临时PG/真实TEST Redis开发键测试。涵盖原共享hb、字段严格校验、重放不续期/有效续期、时钟/来源/会话/日期反例、容量、并发、过期唤醒、缺心跳零读取、停止退休保留完整历史和guard绑定。定向入口沿用下面的Redis双开关，`-run '^TestHeartbeat|^TestTestSession|^TestTestConsumer' -timeout 3m`；心跳为本地合成输入，Redis只访问自己的event/reply键，没有OC心跳订阅或业务命令。全量回归日志`.runtime/ctp-heartbeat-guard-regression.log`。

11-07 OC Redis材料复验：`test_oc_redis_delivery.py`新增5项检查，核对清单8项SHA、三套37场景记录、原body指纹/重投及CTest日志；`oc_redis_delivery*_test.go`新增2项Go测试，三套原查询页0,1,1,2正逆序组装及临时PG保留4条审计/1份3行快照/恢复不重复发布。97项Python全量、`TestOCReceived|TestOCRedisReceived`共5项Go/race（7.748秒、无跳过）和vet通过。原物理输出键未提供，PG使用显式规范来源前提；不是OC程序或真实Redis联合消费验收。无需Redis环境变量，详见`docs/OC_CTP_REDIS_DELIVERY_REVIEW_20261106.md`；本轮未重跑下方169项全量容量测试。

CTP TEST消费循环增量：`test_consumer_test.go/test_consumer_integration_test.go`新增2项模型及7项临时PG测试，其中4项使用真实TEST Redis开发键。覆盖固定来源/凭据轮换/端点漂移、完整循环、取消/连接关闭/owner接管停止、无输入超时和退避、窗口与上下文门禁、完成历史保留及退休事务失败回滚。沿用下方隔离Redis环境变量，以`-run 'TestTest|TestQueryRetire' -timeout 3m`定向执行；不发OC命令，不证明真实心跳、来源认证或交易许可。完整回归日志为`.runtime/ctp-test-consumer-regression.log`。

交易终端资产回归覆盖盘前 Meridian Level1 尚无最新价的两条路径：快照已有 `pre_close` 时直接估值，快照整体未出现时只对缺失证券回退上一交易日未复权日线收盘，防止总资产退化为纯现金。

## 目录约定

- `tests/unit/`: Go 和 Python 的单元测试，后续用于配置、schema、状态机、账表计算等小粒度测试。
- `tests/integration/`: Redis Stream、9092 API、盘后对账和 Meridian 数据接入的集成测试。

## 当前状态

- CTP受限分发/联合质量：`stream_dispatch_integration_test.go`新增8项临时PG测试，`stream_redis_integration_test.go`新增1项真实TEST Redis查询分发测试，定向race通过。验证显式白名单、前部未知不饿死后部、移交失败重试游标、原请求计时、旧owner拒绝、四类最新快照及交易事实应用、待关联/隔离/旧查询冲突、来源/会话/代次/日/校验和反例。质量检查使用只读repeatable-read事务；不等于OC认证、柜台ready或允许发单。复现时沿用下方环境变量，`-run 'TestStreamDispatch|TestStreamQuality|TestStreamRedisDispatch'`。

- CTP真实Redis增量：`stream_redis_integration_test.go`新增8项TEST Redis + 临时PG测试，`stream_store_test.go`新增2项原始结构/RESP2模型测试。重复body实测复现并修复，覆盖分页/原字节/位点接管、客户端关闭后重连、裁剪/删除/回退/类型错误、读后变化、PG提交失败和两页查询原子交接/旧半页恢复拒绝。只操作随机CTP开发键，TTL一小时并精确清理；不发OC命令、不改业务流/ACL或共享Redis配置。当前实测Redis8.2.2，不冒称最小权限ACL、服务重启或生产吞吐验收。详见`docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md`。
- CTP读取位点与持久交接原阶段：`stream_store_test.go/stream_store_integration_test.go`新增3项模型+9项临时PG/race，包含真实PG断连、16路CAS、接管隔离、输入/交接最后一步失败、原文/坏UTF-8、query/event下游绑定、旧半页不可恢复、隔离及降级保护。该阶段go-redis读取为客户端替身，不验证XACK或OC命令执行。

经授权的TEST Redis复现入口（配置仅提供Redis连接，不使用其中数据库DSN；没有两个显式开关时不会写Redis）：

```bash
RELAY_TEST_PG_BIN=/home/ti-relay-trader/.runtime/futures-pg/server/usr/lib/postgresql/17/bin \
RELAY_FUTURES_TEST_REDIS_CONFIG=/home/ti-relay-trader/config/relay.local.yaml \
RELAY_FUTURES_TEST_REDIS_WRITES=development-keys-only \
  scripts/test-futures-postgres.sh -run TestStream -timeout 3m
```

测试要求配置的service.environment和redis.env均为test；对随机开发键进行XADD/XREAD/XINFO/裁剪/删除等操作，不能用于生产配置。不在测试日志打印凭据。无Redis配置时真实Redis用例skip，不得把skip报告为在线通过。完整回归去掉`-run TestStream`并延长timeout，含10,000订单/成交场景。

11-07最新全量结果：200项Go（119项临时PG）/race、103项Python通过，无跳过；10,000订单/成交恢复包含在398.731秒全量回归中。七个相关Go包、三个cmd入口和vet通过；TEST Redis8.2.2仅访问开发event/reply/hb键及服务元数据，无真实柜台/OC命令、OC心跳订阅或账户业务Stream操作。日志`.runtime/ctp-heartbeat-window-regression.log`，最终细节另按上方24项定向复验；开发键清理无报错，临时PG销毁完成。前轮187项日志`.runtime/ctp-heartbeat-audit-regression.log`及更早日志均保留。

- OC 11-06订单/成交交付：`oc_delivery_test.go/oc_delivery_integration_test.go`新增2项模型+1项PG测试（24个子场景），用包内27条实际原body验证严格解码、正逆序/重投、原生ID/夜盘源时间/未知费用、真实PG原字节持久/恢复、身份缺失后补登记及重复应用；定向race通过。`test_oc_trade_delivery.py`新增4项指纹/schema/失败语义/容量记录检查，Python全量92项通过。样例均合成，场景按独立命令空间隔离；OC C++执行、文件崩溃/SDK/Redis/ACK未验证，容量摘要不是原始分页输出。见`docs/OC_CTP_TRADE_DELIVERY_REVIEW_20261106.md`。
- 期货完整查询容量：`trade_capacity_test.go/trade_capacity_integration_test.go`新增3项模型和4项临时PG测试，累计126项Go（67项PG）/race通过。10,000成交先到再10,000订单、恢复不重复入账、513行跨SQL块最后标记失败全回滚、批量来源错误/冲突不发布、迁移降级不丢证据；100x40步与原全扫描算法逐步对照，另验单订单10,000成交线性关联。迁移33..39只在临时库；88项Python通过。脚本支持追加Go测试参数，如`-run TestTradeCapacityTenThousandOrdersAndFills -race=false`，默认仍race全包。没有真实OC/Redis、ACK、生产迁移或吞吐SLA结论。
- 期货命令与查询/事件接线：`query_command_test.go/query_command_integration_test.go`新增1项模型+12项临时PG测试，验证四类查询原命令/Stream绑定、关联ID缺失/错值、别名不能改origin或重采样、ticket/会话/原生身份不变、8路重复登记、链接失败回滚、完整/半页重启、查询事实身份等待及经济冲突、事件原关联/原生绑定、旧原型不自动升级和绑定损坏。累计119项Go（63项PG）/race、88项Python通过；测试schema统一应用33..38并验证down，未接真实OC/Redis/ACK，未部署生产。
- 期货统一命令登记：`command_test.go/command_store_integration_test.go`新增5项模型及13项临时PG测试，包括12份OC字段/字节/48指纹、跨日重试、七动作同域冲突、原生用户漂移、价格尾零/批量顺序、映射不变、16路同意图与8路冲突并发、别名原关联、原子审计回滚、限额/损坏及up/down。累计106项Go（51项PG）/race、88项Python通过；迁移000037仅临时库执行，未接EventStore/QueryStore、outbox或ACK。
- OC 11-05交付复核：`test_oc_command_delivery.py`独立从原命令/scope推导12组字段、36份意图/查找字节及48个指纹，5项新增Python测试；报单64KiB/128字节ID/100子项双预算与output_limits失败语义新增4项Go验证。累计88项Go含38项PG/race、88项Python通过。原JSON见`testdata/fixtures/ctp_intent_vectors.v1.json`；分页输出JSON尚缺，其文档中的真实body边界未计入Relay验收。
- 期货报单登记/事件持久收件：新增4项Go模型及10项真实PG集成，验证单/批命令字段、子单身份和原命令绑定、完整原文持久恢复、身份未知等待、错误/冲突阻断、同号换entry仍冲突、批量登记及事件处理失败整体回滚、16路重投、来源/投资者/环境隔离、游标扫描和容量/校验和。累计84项Go含38项数据库测试以race通过，迁移000036仅临时库执行，83项Python不变；没有真实Redis/OC调用、ACK或生产迁移。
- 期货完整查询到事实应用：新增6项查询/事件模型测试及7项真实PG集成，覆盖订单/成交乱序收页、原生授权、空结果、完整事件/源时间、重启补应用、迟到冲突阻断、标记失败整体回滚、16路并发幂等及容量超限。累计70项Go含28项真实数据库测试以race通过，迁移000035仅临时schema执行；83项Python协议测试不变。运行入口仍为`scripts/test-futures-postgres.sh`，事件当前只解码，未接Redis/ACK/生产。
- 期货订单/成交隔离账本：新增10项Go模型/状态测试和9项真实PG测试，覆盖七段身份、成交先到不造单、终态24种到达排列、跨日/环境、待关联重建恢复、16路并发、费用版本/别名/超量冲突及事务回滚。仍用`scripts/test-futures-postgres.sh`，当前累计57项Go测试含21项数据库集成，以race验证；不连接OC、不改生产账本。迁移000034只在临时schema验证，尚无在线Redis消费或ACK。
- 期货查询隔离存储：`scripts/test-futures-postgres.sh`自行启动并销毁本机临时PostgreSQL（Unix socket、WAL/fsync默认开启），不读应用配置/现有DSN。设置`RELAY_TEST_PG_BIN`为含initdb/pg_ctl的服务端二进制目录；12项数据库集成+26项Go测试以race运行，验证原子发布、并发重投、连接终止回滚、旧进程隔离、历史恢复及迁移up/down。直接运行时只认`RELAY_FUTURES_TEST_DATABASE_URL`且须指向一次性测试库；无此变量集成测试会skip，不能声称验证落库。详见`docs/CTP_LEDGER_LINKAGE_DESIGN_20261105.md`。
- 已创建测试目录骨架。
- 已增加 Go 配置加载、结构化日志、HTTP envelope 和 API 健康检查骨架的单元测试。
- 已增加统一交易 schema 的基础校验和状态机单元测试。
- 已增加 Redis Stream 命名、环境变量兼容、消息摘要解析和 Redis URL 脱敏测试。
- 已增加 PostgreSQL migration 静态检查，确认首版账本表和关键约束存在。
- 已增加 migration loader 单元测试，并验证 `relayctl migrate` 在无 DSN 时给出明确错误。
- 已增加 SDK 只读 live smoke：`tests/integration/sdk_live_smoke.py`，可对运行中的 9092 验证状态、账户、资金、持仓、订单、成交和 SSE 首事件；`--allow-degraded` 仅用于明确接受运行态告警的生产只读验收。
- 已增加 SDK 发布检查脚本：`scripts/check-python-sdk-release.py`，覆盖版本一致性、tar.gz 内容、sha256、SDK 单元测试和可选 live smoke。
- 已增加完整历史 close 数量基线测试和 `sdk_position_baseline_live_smoke.py`：完整/空/缺历史/部分查询、旧行、10001 行、总数/摘要/分页和重建失败关闭；`RELAY_POSITION_BASELINE_LIVE_DATABASE_URL` 只读验证历史样本，`RELAY_POSITION_BASELINE_ISOLATED_DATABASE_URL` 仅在连接本地 TEMP 表验证大集合和重建，不写永久账表。两个变量仅用于指定的 `TestPositionBaseline*` 测试，不用于其他写库集成测试。
- 已增加 9092 页面轻量冒烟测试：`tests/integration/page_smoke.py`，覆盖首页、文档、测试索引、API Console、交易终端、静态资源、基础 API 和 SDK 下载入口。
- 已增加绩效页 Playwright 只读测试：`tests/integration/performance_visual_smoke.py`，覆盖账户切换、查询日期稳定、当日四项主指标、ECharts canvas 有效像素、七项数据质量检查、无横向溢出、无控制台错误和无 HTTP 错误。
- `internal/api/performance_cash_events_test.go` 覆盖历史/券商/原生NAV资金明细投影、东八区时间、同日反向资金、日期精度和日净额回退；`performance_cash_markers_smoke.py` 将真实序列逐日对照图表资金标记，并仅在浏览器模拟等额进出、策略资本和未知时刻，验证悬浮和缩放，禁止所有非GET业务请求。
- `performance_capital_reference_test.go` 覆盖双口径收益、固定市值仓位、盈亏对称、零值/缺失/阻断/非正资本、账户/范围/资产衔接门禁及单日API/CSV；`performance_reference_test.go` 约束首日锚点为同账户严格前日、当前版本、单行有界查询，不影响正式NAV。
- 参考指数测试覆盖所选区间基期、收益连乘、参考回撤、重算幂等及缺失/阻断/非有限/不大于-100%收益后不续算；`performance_cash_markers_smoke.py --check-reference-overlay` 验证默认关闭、勾选/取消、真实曲线像素、API数值一致、缩放保持及不重新查询，单日参考字段与区间指数分别验证。
- 出金前参考分母另覆盖7月2日出金、2025年9月3日小余额后大额入金、同日双向资金按原权重加回、盈亏对称、缺权重/不完整证据拒算、明确日末约定和零盈亏开户锚点。完整375日期参考指数与正式曲线可同时绘制，原日初资本诊断不再直接作为该曲线分母。
- `tests/integration/asset_day_pnl_smoke.py`：以已发布NAV验证资金持仓页账户当日盈亏及CSV来源，覆盖桌面/手机视口；禁发非GET API请求。需指定`--account-id`、`--trade-date`、`--output-dir`，只适用于有匹配收盘资产与非阻断NAV的日期。
- `tests/integration/asset_close_smoke.py`：GET-only核对当前/历史收盘资金持仓的总资产、逆回购分项、可用或待核算的账户日收益及CSV；同样需账户、日期、输出目录。`--repo-only-fixture`仅在浏览器注入零证券市值、现金加逆回购及错误诊断盈亏；`--browser-date`仅改变浏览器日期以验证非交易日默认仍读最近交易日close，均不改真实账本。
- 已增加任务复核页 Playwright 只读测试：`tests/integration/jobs_visual_smoke.py`，覆盖交易日选择、六账户复核结论、任务时间东八区格式、无横向溢出、无控制台错误和无 HTTP 错误。
- 运维页 Playwright 会动态读取 `/v1/accounts`，验证六个 gateway 和账户筛选器均使用 PostgreSQL 落库别名，不回退显示旧的“生产查询账户”配置名。
- 已增加交易终端 Playwright 交互测试：`tests/integration/trade_terminal_interaction_smoke.py`，覆盖生产环境/账户切换、只读下单护栏、日期查询、持仓和订单分页/排序、持仓联动代码、分钟 K 线 canvas 及订单详情。
- 已增加批量下单 Playwright 交互测试：`tests/integration/batch_order_interaction_smoke.py`，用浏览器路由模拟券商测试环境和拦截批量 POST，覆盖粘贴导入、编辑后校验失效、账户后四位确认、标准请求体及发布结果；真实生产页面仍验证零写请求。
- 已增加 API Console Playwright 交互测试：`tests/integration/api_console_interaction_smoke.py`，覆盖状态、账户、历史订单及完整 close 基线表单、请求预览、JSON/表格响应、命名集合保存/恢复、JSON 导入导出和响应断言，并在浏览器路由层中止所有 `/v1/*` 写请求。基线要求传入已有完整 close 的交易日。
- 已增加 API catalog 一致性检查：`scripts/check-api-catalog.py`，校验 67 个 catalog 条目、Go handler 注册模式、源码 schema 和在线 `/v1/schema`。
- 已增加生产只读发布验收入口：`scripts/check-readonly-release.py`，先强制校验 `environment=production`、`trading_enabled=0`，再组合单元测试、SDK、API 和双视口 Playwright；清单见 `docs/RELEASE_CHECKLIST.md`。
- 已增加 API/worker 跨进程事件桥集成测试：`tests/integration/event_bridge_smoke_test.go` 在显式提供生产式测试配置时直接发送 PostgreSQL 通知，验证 API SSE 能收到对应账本事件，全程不写 Redis 命令或交易账表。
- 已增加 API handler、交易 schema/状态机、订单编排/幂等、Redis command/envelope/账本同步、成交去重、自动刷新、Meridian 客户端、事件 hub 和 PostgreSQL repository 单元测试。
- 已增加 Python 交易日任务测试，覆盖非交易日跳过、账户级异常隔离和快照流程关键语义。
- 已增加 Python DataFrame 版本策略测试，固定 `pandas==2.3.3` constraints，并防止纯标准库 `relay-sdk` 无意引入 pandas/NumPy/PyArrow/Polars。
- `test_fuying13_history_audit.py` 覆盖一次性券商历史审计的非零锚点、另一策略债券退出、成交/交割/现金双桥、原始身份、逆回购单位、未知委托终态及失败证据包；脚本无网络和数据库写入口，不进入每日任务。
- `test_fuying13_history_rebuild.py` 覆盖原生ETF分类、股份余额、同日内部转托、除权日生效、实际逆回购本金/利息、日线范围与完整性、分红权益时点、缺页不当空仓，以及明确源日的实际ETF现金权责试算；脚本只有Meridian GET，不连接柜台或写库。
- `test_fuying13_carveout.py` 覆盖债券剔除子组合的真实收付、策略资本/银证分离、两种日度计权、观察锚点、交接连续性、非正分母和未知业务门禁；全部结果仅为有条件的收付预览，不发布产品净值。
- `test_fuying13_etf_origins.py` 的10项测试覆盖实际ETF流水源日与历史委托原标识精确关联、编号复用/不匹配/未来日期失败关闭、显式冲突、全历史已发布/缺失区段、响应截断和账户隔离；审计只读、不发布NAV、不把全部原始行匹配视为唯一会计门槛。
- `test_fuying13_continuous_nav.py` 的9项测试覆盖ETF实付金额多重集合、源日/记账日分流、成分过户费不二次调整、无父单现金差额、补款与佣金隔离、真实银行计权、未拆分资产保留及非正分母失败；Go CLI测试覆盖375点资产/资金/净值链、非有限值及指纹门禁，API/绩效服务验证统一历史公式、默认区间与未来NAV承接。全区间375点API/CSV及只读桌面Playwright已验收。
- `cmd/relayctl/performance_fuying13_carveout_test.go` 覆盖正式发布器的固定输入指纹、显式确认、65点逐分桥、净值连乘和幂等防覆盖；`internal/api/performance_carveout_test.go` 覆盖历史段查询、跨口径409、不误用另一范围昨日资产及Meridian可选基准503不阻断已发布净值。历史现金段页面可用 `performance_visual_smoke.py --expect-quality-items 6` 验证，其余账户默认八项检查，均含独立区间发布状态。
- `node --test tests/unit/performance_quality.test.cjs` 验证净值未发布/试算/快照缺失/归因残差分别显示，历史专用分支不能跳过区间缺口。浏览器脚本仅允许GET；`--local-assets`可在不部署的情况下验证工作区JS/CSS。
- 已保留可选 PostgreSQL repository 集成测试，设置 `RELAY_LEDGER_TEST_DATABASE_URL` 后运行真实 migration/写库验证。
- 9092 文档门户可通过 `/tests` 查看本索引和测试目录树。

## 运行示例

```bash
python3 tests/integration/page_smoke.py --base-url http://127.0.0.1:9092

python3 -m venv .venv
.venv/bin/pip install playwright
.venv/bin/playwright install chromium
.venv/bin/python tests/integration/performance_visual_smoke.py \
  --base-url http://127.0.0.1:9092 \
  --account-id 314000046830 \
  --date-from 20260701 \
  --date-to 20260729

# GET-only: published metrics appear before delayed quality, replaced requests cancel.
.venv/bin/python tests/integration/performance_loading_smoke.py \
  --account-id 314000046830 --date-from 20260101 --date-to 20261019 \
  --replacement-from 20260920

.venv/bin/python tests/integration/jobs_visual_smoke.py \
  --base-url http://127.0.0.1:9092 \
  --trade-date 2026-07-31

.venv/bin/python tests/integration/batch_order_interaction_smoke.py \
  --base-url http://127.0.0.1:9092 \
  --account-id 314000046830

python3 scripts/check-readonly-release.py \
  --base-url http://127.0.0.1:9092 \
  --account-id 314000046830 \
  --trade-date 20260731 \
  --performance-date-from 20260701 \
  --performance-date-to 20260731
```

脚本默认禁用系统 HTTP 代理，避免本机 `127.0.0.1:9092` 被代理环境误转发；也可以通过 `--base-url http://relay-trader.quantstage.com` 对最终服务口径做同样检查。

## 研究成本只读页面

2026-11-19当前证券基线：`TestCurrentAccountBaseline*`与`bash scripts/test-position-baseline-postgres.sh`覆盖ledger固定存储及accountbaseline原文组装。包括规范化/容量限制、501跨页时新版本发布、并发防重/代次、回滚/篡改；四查询原文→只读组装→固定发布、跨日/未发布/未知状态订单保留、相同意图观察合并/冲突、缺页/末页/字段/原文及错误作用域、十进制精度与未知冻结。脚本只启动Unix socket临时PG，在独立schema运行迁移及合成源表，拒绝非隔离数据源；旧close继续回归。归档终态一致不等于来源完整，候选始终partial/unavailable、覆盖/缺失为零标志false。无生产迁移或柜台查询。

11-20命令观察：`TestCurrentAccountBaselineCommand*`及`IsolatedCommands`覆盖已归档却无订单行、批量部分有账本、撤单未闭合、跨日/旧gateway、索引与原文冲突/重复JSON键、重复命令/子单、精确金额与原文保留。临时PG执行迁移57、501命令固定分页、后来命令使旧修订失效、历史终态不加入未决orders、旧三资源读取commands为unavailable、空commands版本禁止破坏性降级。全部是合成数据，无Redis业务发布；命令观察不等于完整责任登记或交易授权。

`TestCurrentAccountBaselineRevision*`使用真实000001至000053迁移，覆盖12张表的24个观察触发器、同事务提交/回滚、8路并发修订、账户隔离与归属变更、未归属收件/整表截断的全局失效、普通心跳排除；发布等待实际写事务后拒绝旧候选、幂等重试、原501行分页不变、改回原值不复活、scope变化、停用追踪及重装epoch。首轮重装用例暴露连接缓存函数OID问题，修正目录核验后整套PG race通过。`CheckValidity`为只读、只返回保守invalidated/unverifiable，尚无完整源覆盖、TTL或HTTP上线验收。

2026-11-20 `TestCurrentAccountBaselineHealth*`在上述真实schema增加000054：检查五个健康观察触发器、54上/下迁移、正常心跳/位点前进不误失效、会话变化再恢复仍失效、源写入/修订一起回滚、错误恢复与历史错误分离、游标数值比较/回退/截断、原文与作用域/缺字段/镜像冲突、停用追踪、固定501行页不变。`TestCurrentAccountBaselineCheckpointIsNotCoverage`验证处理位点落后有独立诊断，追上也不升级来源完整；来源组装集成测试验证新诊断固化。仅隔离PG与合成输入，无Redis/柜台/生产访问；缺少源连续区间和采样边界时仍不发布current能力。

同日`TestCurrentAccountBaselineReceipt*`增加000055和可选证券持久收件：原文BYTEA/SHA、同事务账本/checkpoint/cursor、部分处理savepoint回滚、SQL提交失败、同attempt八路并发/响应丢失后重复读取、owner接管/旧writer拒写、read失败保留并显式重读、同ID内容冲突、容量及健康观察、55上下迁移。实际应用复用现有ledger writer；缺少资金关键字段拒绝，显式零余额允许。旧`raw_stream_messages.body_text`仍去掉首尾空白，测试分别核对旧兼容行为与新receipt的逐字节保留，不混称原文证据。

本地Redis回归需显式提供二进制；测试自行启动仅Unix socket的临时Redis，使用自己创建的合成流验证INFO/XINFO/XREAD/XRANGE、续接/空读和同run_id删流重建，结束停止该进程并清理临时目录。没有业务Redis配置，不接共享TEST/生产。示例（路径以本机实际安装为准）：

```bash
RELAY_TEST_PG_BIN="$PWD/.runtime/futures-pg/server/usr/lib/postgresql/17/bin" \
RELAY_BASELINE_LOCAL_REDIS_SERVER="$PWD/.runtime/redis-isolated-tools/extracted/usr/bin/redis-server" \
LD_LIBRARY_PATH="$PWD/.runtime/redis-isolated-tools/extracted/usr/lib/x86_64-linux-gnu" \
  bash scripts/test-position-baseline-postgres.sh
```

不设置Redis二进制时仅该本地Redis用例跳过，不能记为Redis验收通过；普通`go test ./...`缺少隔离PG环境时跳过数据库集成。首次接线回归暴露缺字段被旧解析器补零，已在新可选路径拦截；补齐本地Redis依赖路径并区分旧归档trim行为后重跑。新接收器仍无运行中worker启用、源epoch/无遗漏证明或HTTP/SDK发布，隔离成功不代表在线柜台采样覆盖。

最终全量`go test ./...`、ledger/accountbaseline/redisstream的vet及上述三包隔离race通过；新增收件PG和独立Redis用例均实际执行。既有`TestPositionBaselineExistingHistoricalReadOnly`因未提供历史库连接跳过，不记为线上验收。最终日志`/tmp/relay-baseline-receipts-final-20261120.log`、`/tmp/relay-baseline-all-go-final-20261120.log`；临时PG/Redis进程已停止。没有重放业务命令、重算账表、业务库迁移或权限变更。

11-20后续单写者/worker增量：`TestCurrentAccountBaselineWriter*`、`BootstrapBoundary`、`ReceiptManifest`、`LegacyEntrypointsGuarded`、`ReceiptWorkerLocalRedis`、`ReceiptLoopFailureStops`、`LegacyToReceiptHandoff`纳入同一隔离脚本。覆盖三个旧入口共用数据库锁、占用拒绝登记/接管、部分锁释放、终止持锁PG连接后禁止借池写账、精确旧checkpoint/归档交接及原错误保留；显式四流manifest、TEST/外部worker限制、私有文件/未知字段/重复键/错来源拒绝；实际四流两次启动不重复账表、COMMIT后通知、失败留证不报ready、不修改消费组。旧路径先实际应用再交接时，不从0重扫，不把旧trim归档补造成新receipt原文。

该增量仍只改源码，新worker未部署；当时显式登记工具尚未完成，后续实现见下一段，失败处置、来源覆盖及生产启用仍未完成。首跑三个边界用例因测试归档方向误用`inbound`而非既有schema的`in`失败，修正测试输入后重跑，没有放宽数据库约束。后续补跑另复现既有源读取SQL按文本主键排序的问题：随机插入顺序使分页ID跨过9/10时末页提前。已按源表数字主键排序，并固定9/10/11分页、同时间心跳9/10回归；新增bootstrap证据超限时整笔登记回滚。最终验证日志：`/tmp/relay-baseline-handoff-verified-20261120.log`（临时PG/Redis race）、`/tmp/relay-writer-all-go-20261120.log`（全量Go）、`/tmp/relay-writer-vet-20261120.log`（相关包vet）。缺历史库连接的在线历史用例仍按设计跳过。

11-20操作员入口：`TestCurrentAccountBaselineHandoff*`纳入同一隔离PG脚本及000056迁移，覆盖只读计划、四流原子登记/接管、旧状态及锚点变化、最后一步SQL失败整批回滚、原计划幂等取回、不可变操作记录、旧owner和在途写入隔离、blocked/fault保留。`HandoffLocalRedis`实际运行计划→登记→worker→接管→worker，核对两条原文只入账一次、消费组未改变、删除锚点和错run_id拒绝。CLI的`TestLedgerReceipt*`另覆盖TEST数据库/完整集合、显式提交参数、私有文件/符号链接/重复JSON/未知字段/大小和禁止覆盖；私有目录用例首跑发现TempDir权限不保证0700，已明确设置测试目录权限，没有放宽业务文件检查。

本轮全量Go、相关四包vet及临时PG/Redis race最终通过，日志分别为`/tmp/relay-handoff-operator-all-go-20261120.log`、`/tmp/relay-handoff-operator-vet-20261120.log`、`/tmp/relay-handoff-operator-final-pg-20261120.log`。历史库只读用例仍因未提供连接跳过，不能写成生产历史验收；迁移56仅执行在临时schema，未操作业务Redis或服务。操作方式与失败取回边界见当前基线回复第15节，不能用重复生成计划代替核对未确认提交。

11-20逐页receipt关联：`TestCurrentAccountBaselineReceiptLinkedAssembly/FailureEvidence/ProtectionAndCorruption/ScopeAndMissing/LegacyBoundary`使用000001至56真实schema、既有收件/解析/账本处理器及合成Redis读接口，验证四查询与501行固定分页、原文字节/旧trim双摘要、失败后成功保留两观察、同条目不同内容、仅失败收件、旧归档实质变化、错账户/流/位点、保护停用及摘要损坏拒绝、交接前数据不补造收件证明。`ReceiptProjectionPolicy`覆盖各状态的组装策略和残缺关联拒绝。上述新增用例无真实柜台/共享Redis访问；既有本地Redis收件回归继续运行。所有覆盖字段仍不升级，当前完整基线未发布。

本轮最终全量Go、相关四包vet及隔离PG/本地Redis race通过，日志分别为`/tmp/relay-receipt-links-final-all-go-20261120.log`、`/tmp/relay-receipt-links-vet-20261120.log`、`/tmp/relay-receipt-links-final-pg-20261120.log`。历史库只读测试仍因未提供连接跳过；测试结束由脚本停止临时PG/Redis，无业务库或配置变更。

2026-11-14历史查询证据：`go test ./cmd/relayctl -run 'TestResearch(QueryEvidence|SnapshotTiming)'`覆盖报告摘要截短但真实计数完整、空items末页、原命令/账户/环境/日期/时间窗错误、重复ID/双末页/缺末页/缺页/缺items/解析失败，以及只有实际盘前refresh计划不含费用时才标not_scheduled。快照时间诊断不覆盖原baseline，不把任意时间差当合法现金观察。生产只读23日/207查询实际结果见[核验记录](../docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#历史查询证据与基线时间差)；本轮未修改共享基线判定逻辑。

2026-11-14后续基线修复：`TestPositionBaselineSeparateCashAndBatchClocks`、`TestPositionBaselineCashClockSkewUsesReceiveWindow`、`TestPositionBaselineRecoveryPublication`覆盖双时间证据、数据库六位精度、OC/Relay时钟微差、本机接收窗口、错误账户/代次/金额/数量、缺回包、失败/试算/不完整修复拒绝，且不能覆盖已有无效常规发布。`bash scripts/test-position-baseline-postgres.sh`启动仅Unix socket的临时PG，用TEMP表验证10001行分页、reconciliation_runs关联、失败发布和旧游标拒绝；无生产凭据、迁移或金融写入。本机服务器程序解包于`.runtime/futures-pg/server/usr/lib/postgresql/17/bin`，运行前用`RELAY_TEST_PG_BIN`显式指定该路径。在线GET验收沿用`sdk_position_baseline_live_smoke.py`，新审计字段保留在SDK的`baseline.raw`中。

2026-11-14订单/成交覆盖：`go test ./cmd/relayctl -run 'TestResearch(Book|Request|TradePage)'`覆盖双向多重集合、501/2001/10001行、不吞重复、微元差、日期/账户隔离、命令sent_at及筛选检查、普通成交与划转分离、坏整页的逐条诊断仍保留失败。`DecodeTradeQueryRecords`复用原入库解析，无writer/Redis/写库或合成成交副作用。`RELAY_TEST_PG_BIN=... bash scripts/test-book-coverage-postgres.sh`在专用Unix socket空PG运行前32个迁移，验证501行跨页、跨账户/日过滤、repeatable-read、存储来源提取及只读事务拒绝写入；不读生产配置，也不在生产执行迁移。私有核验报告不进入git。

2026-11-14独立事件/逆回购时点：`TestResearchEventEvidence`覆盖完整订单/成交、原始路由/日期/身份/时间、逐字段差异、重复message_id、早期生命周期与CLI参数门禁；`TestDecodeTradeEventRecordReadOnly`拒绝部分订单及错误类型，不产生合成记录。`TestResearchRepoCashTiming`覆盖现金前/同刻/之后及批次完成前后、缺失/重复快照或回包、金额/账户差异、重复和跨日成交，不以批次结束代替现金观察。隔离PG脚本已纳入上述测试并扩展真实event SQL账户/日过滤；线上复核仅显式CLI读取并写私有报告，不启动OC查询或变更成本/NAV。

2026-11-14特殊申赎解释：`TestResearchSpecialParent`核对独立父单身份及Go类型化枚举，不回写坏回报。`python3 -m unittest discover -s tests/unit -p 'test_fuying13_special_records.py'`验证现金拆分多重集合、零数量现金腿、错误身份/日期/金额/时间、缺项/重复/借用、资金桥和原始报告SHA，越出成交/交割导出范围明确标记未覆盖。全部富盈Python回归153项通过；新脚本仅一次性离线证据审计，不修改解析器、账表或NAV门禁。

2026-11-14次日开盘承接：`TestResearchOpening*`及`TestDecodePositionQueryRecordsReadOnly`覆盖完整空集合/缺失、原始时间/末页/字段、跨账户与身份、集合竞价边界、重复持仓、T+1可用量变化、CORE/T0分桶汇总、数量冲突及公司行为/NaN/溢出拒绝。隔离PG脚本纳入Opening并验证缺收盘快照不被认作可信空仓。只读CLI通过`--opening-coverage`显式启用，报告默认仍不批准正式成本或NAV，不修改API的close-only基线契约。

2026-11-14封存输入离线重放：`go test ./cmd/relayctl -run TestResearchReplay`覆盖请求不匹配后不可恢复、输入耗尽、未支持错误失败、`sql.ErrNoRows`身份、并发快照按参数匹配且不跨查询边界、证券集合顺序/日期/重复数量、JSON大整数和微元精度、新算跨日成本不一致拒绝、金融写入/实时回退禁止、manifest和字节篡改拒绝。`RELAY_RESEARCH_INPUT_REPLAY=1`额外启用私有1234条输入的完整22日引擎验证，可用`-count=3`检查并发重复运行；它不调用DB或Meridian。详见[离线验收](../docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#封存输入离线重放验收)。

2026-11-14实际输入采集：`go test ./cmd/relayctl -run TestFuyingResearchCapture`覆盖请求/返回值修改不污染记录、错误保留、序号/字节/SHA、0600与禁止覆盖、并发记录、容量失败及金融写入拒绝。设置`RELAY_RESEARCH_COST_FROZEN_REPLAY=1`后同一测试集额外核对本机冻结22日/972行、拒绝不完整链及识别0.000001成本差异。实际生产只读复算证据与限制见[采集记录](../docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#实际输入采集与成本复算)；这不是从封存输入离线运行完整引擎的验收，也不证明原柜台历史查询完整。

2026-11-14新增：

```bash
go test ./internal/api -run TestResearchCostRoutes
RELAY_TEST_PG_BIN="$PWD/.runtime/futures-pg/server/usr/lib/postgresql/17/bin" \
  RELAY_RESEARCH_COST_FROZEN_REPLAY=1 bash scripts/test-research-cost-postgres.sh
.venv/bin/python tests/integration/research_cost_visual_smoke.py \
  --base-url http://relay-trader.quantstage.com \
  --output outputs/recovery/research_cost_web_check
```

PG脚本仅用临时Unix socket数据库，完整回放需要本地冻结证据。页面测试显式读取768的`fuying13-cost-research-20261114-v1`，检查22日/972行、筛选/排序/分页/CSV、桌面/手机、空账户版本目录及跨账户取消。浏览器拦截所有非GET/HEAD请求；审计变化、微元差和错误响应使用合成GET替身，不改变生产账表。报告区分实际数据读取和合成边界测试，无正式NAV发布验收含义。

## 后续计划

2026-11-15独立CORE恢复：`go test -race ./internal/performance ./cmd/relayctl`覆盖从完整空仓起点逐日重放普通成交、按订单只计一次费用、超卖/歧义顺序/跨账户/缺费用拒绝，以及原始订单成交查询覆盖门禁。`RELAY_TEST_PG_BIN=... bash scripts/test-research-cost-postgres.sh`包含`TestFuyingResearchCorePublishPostgres`：只读拒写、回滚、独立回读、已有/部分目标拒绝覆盖及并发写入串行化。PG为临时Unix socket实例；默认跳过需要私有冻结历史文件的旧用例，不依赖用户更新CSV。

受控入口`performance-core-cost-publish`默认只读；显式`--apply`要求已审核计划SHA、两小时内完整备份及SHA。仅本次768的11-10..12，重新核验PG原始证据与正常引擎结果后单事务插入成本，禁止覆盖已有日期，不写资金/NAV。提交前报告不冒充成功，成功后另写`.commit.json`并独立回读；失败不得通过删除已有记录强行重跑。详见[恢复记录](../docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#普通持仓成本的独立恢复)。

2026-11-14成分现金解释：`python3 -m unittest discover -s tests/unit -p 'test_fuying13_component_cash.py' -v`覆盖原父单/现金委托身份、日期/方向/金额/时区、物理持仓或普通成交冲突、重复流水、封存序号/SHA/重复页变化/缺末页、查询投影指纹、完整持仓证据和旧导出范围；保留原查询失败及禁止发布标记。全富盈Python159项通过，两次实际离线报告除生成时刻外一致，不调用数据库、OC或Meridian。[执行与证据](../docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#四日成分划转与替代现金核验)。

2026-11-14正式成本分项预检：`go test ./internal/performance -run TestResearchCostPublication`覆盖逐日分桶/语义定位、稳定输出、未知规则、部分正式目标、源修订及锚点变化，拒绝缺行/跨账户/错版本/SHA/发布或写入标记。`performance-research-source-check --publication-review`没有apply/persist入口，使用原有只读repeatable-read事务；上述隔离PG脚本验证真实22日冻结链的预检不改变金融表指纹，报告不能冒充源观察基准。独立证据绑定与原子发布尚未实现，不把数量通过当净值通过。

2026-11-14源账表预检：`go test ./internal/performance -run TestResearchCostVersionSource`覆盖首次观察/稳定复读/新增删除/同条数修订、锚点变化、跨环境/账户/版本/数据库/未来时间、缺日/重复日/无效SHA/超预算拒绝。上述隔离PG脚本额外运行源表读取、并发写入后的repeatable-read稳定性、下轮识别1微元修订和删除、只读/隔离级别门禁、私有报告读取及禁止覆盖。实际生产命令和证据见[源账表预检](../docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#研究成本源账表观察与发布缺项)，不是正式净值验收。

2026-10-27绩效修复：`go test ./internal/performance ./cmd/relayctl ./internal/api ./internal/ledger` 覆盖PCF整倍委托、底仓排除、显式分组、真实歧义、连续内存预览与源资金时间门禁。`TestBrokerCashTimestampReadOnly` 使用单独的 `RELAY_CASH_TIME_READONLY_DATABASE_URL` 显式启用，只读事务验证CTE及两条2026-10生产历史样本；不得使用会建表写入的其他集成测试开关替代。详见 `docs/PERFORMANCE_REPAIR_PREVIEW_20261027.md`。

1. 将现有临时 PostgreSQL migration/repository 集成脚本接入外部 CI。
2. 增加盘后对账和绩效贡献的更多固定样例数据测试；人工复核报告已有 API 固定样例和生产只读 Playwright 验收。
3. [x] 增加 gateway 心跳、stream lag、非交易时段抑制和 DLQ 处置写保护测试；`operations_visual_smoke.py` 验证生产 6 gateway / 24 output stream 页面。
