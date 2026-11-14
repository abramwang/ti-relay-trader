# 测试目录索引

当前测试覆盖 Go 核心模块、Python SDK、交易日任务、临时 PostgreSQL migration/repository、9092 页面/API 轻量冒烟和关键运维页面 Playwright 交互。

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
- `cmd/relayctl/performance_fuying13_carveout_test.go` 覆盖正式发布器的固定输入指纹、显式确认、65点逐分桥、净值连乘和幂等防覆盖；`internal/api/performance_carveout_test.go` 覆盖历史段查询、跨口径409、不误用另一范围昨日资产及Meridian可选基准503不阻断已发布净值。历史现金段页面可用 `performance_visual_smoke.py --expect-quality-items 5` 验证，其余账户默认七项检查不变。
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

2026-11-14正式成本分项预检：`go test ./internal/performance -run TestResearchCostPublication`覆盖逐日分桶/语义定位、稳定输出、未知规则、部分正式目标、源修订及锚点变化，拒绝缺行/跨账户/错版本/SHA/发布或写入标记。`performance-research-source-check --publication-review`没有apply/persist入口，使用原有只读repeatable-read事务；上述隔离PG脚本验证真实22日冻结链的预检不改变金融表指纹，报告不能冒充源观察基准。独立证据绑定与原子发布尚未实现，不把数量通过当净值通过。

2026-11-14源账表预检：`go test ./internal/performance -run TestResearchCostVersionSource`覆盖首次观察/稳定复读/新增删除/同条数修订、锚点变化、跨环境/账户/版本/数据库/未来时间、缺日/重复日/无效SHA/超预算拒绝。上述隔离PG脚本额外运行源表读取、并发写入后的repeatable-read稳定性、下轮识别1微元修订和删除、只读/隔离级别门禁、私有报告读取及禁止覆盖。实际生产命令和证据见[源账表预检](../docs/FUYING13_BROKER_ASSET_SCOPE_20261028.md#研究成本源账表观察与发布缺项)，不是正式净值验收。

2026-10-27绩效修复：`go test ./internal/performance ./cmd/relayctl ./internal/api ./internal/ledger` 覆盖PCF整倍委托、底仓排除、显式分组、真实歧义、连续内存预览与源资金时间门禁。`TestBrokerCashTimestampReadOnly` 使用单独的 `RELAY_CASH_TIME_READONLY_DATABASE_URL` 显式启用，只读事务验证CTE及两条2026-10生产历史样本；不得使用会建表写入的其他集成测试开关替代。详见 `docs/PERFORMANCE_REPAIR_PREVIEW_20261027.md`。

1. 将现有临时 PostgreSQL migration/repository 集成脚本接入外部 CI。
2. 增加盘后对账和绩效贡献的更多固定样例数据测试；人工复核报告已有 API 固定样例和生产只读 Playwright 验收。
3. [x] 增加 gateway 心跳、stream lag、非交易时段抑制和 DLQ 处置写保护测试；`operations_visual_smoke.py` 验证生产 6 gateway / 24 output stream 页面。
