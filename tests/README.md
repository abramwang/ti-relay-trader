# 测试目录索引

当前测试覆盖 Go 核心模块、Python SDK、交易日任务、临时 PostgreSQL migration/repository、9092 页面/API 轻量冒烟和关键运维页面 Playwright 交互。

交易终端资产回归覆盖盘前 Meridian Level1 尚无最新价的两条路径：快照已有 `pre_close` 时直接估值，快照整体未出现时只对缺失证券回退上一交易日未复权日线收盘，防止总资产退化为纯现金。

## 目录约定

- `tests/unit/`: Go 和 Python 的单元测试，后续用于配置、schema、状态机、账表计算等小粒度测试。
- `tests/integration/`: Redis Stream、9092 API、盘后对账和 Meridian 数据接入的集成测试。

## 当前状态

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

## 后续计划

2026-10-27绩效修复：`go test ./internal/performance ./cmd/relayctl ./internal/api ./internal/ledger` 覆盖PCF整倍委托、底仓排除、显式分组、真实歧义、连续内存预览与源资金时间门禁。`TestBrokerCashTimestampReadOnly` 使用单独的 `RELAY_CASH_TIME_READONLY_DATABASE_URL` 显式启用，只读事务验证CTE及两条2026-10生产历史样本；不得使用会建表写入的其他集成测试开关替代。详见 `docs/PERFORMANCE_REPAIR_PREVIEW_20261027.md`。

1. 将现有临时 PostgreSQL migration/repository 集成脚本接入外部 CI。
2. 增加盘后对账和绩效贡献的更多固定样例数据测试；人工复核报告已有 API 固定样例和生产只读 Playwright 验收。
3. [x] 增加 gateway 心跳、stream lag、非交易时段抑制和 DLQ 处置写保护测试；`operations_visual_smoke.py` 验证生产 6 gateway / 24 output stream 页面。
