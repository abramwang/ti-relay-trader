# 测试目录索引

当前测试覆盖 Go 核心模块、Python SDK、交易日任务、临时 PostgreSQL migration/repository、9092 页面/API 轻量冒烟和关键运维页面 Playwright 交互。

## 目录约定

- `tests/unit/`: Go 和 Python 的单元测试，后续用于配置、schema、状态机、账表计算等小粒度测试。
- `tests/integration/`: Redis Stream、9092 API、盘后对账和 Meridian 数据接入的集成测试。

## 当前状态

- 已创建测试目录骨架。
- 已增加 Go 配置加载、结构化日志、HTTP envelope 和 API 健康检查骨架的单元测试。
- 已增加统一交易 schema 的基础校验和状态机单元测试。
- 已增加 Redis Stream 命名、环境变量兼容、消息摘要解析和 Redis URL 脱敏测试。
- 已增加 PostgreSQL migration 静态检查，确认首版账本表和关键约束存在。
- 已增加 migration loader 单元测试，并验证 `relayctl migrate` 在无 DSN 时给出明确错误。
- 已增加 SDK 只读 live smoke：`tests/integration/sdk_live_smoke.py`，可对运行中的 9092 验证状态、账户、资金、持仓、订单、成交和 SSE 首事件；`--allow-degraded` 仅用于明确接受运行态告警的生产只读验收。
- 已增加 SDK 发布检查脚本：`scripts/check-python-sdk-release.py`，覆盖版本一致性、tar.gz 内容、sha256、SDK 单元测试和可选 live smoke。
- 已增加 9092 页面轻量冒烟测试：`tests/integration/page_smoke.py`，覆盖首页、文档、测试索引、API Console、交易终端、静态资源、基础 API 和 SDK 下载入口。
- 已增加绩效页 Playwright 只读测试：`tests/integration/performance_visual_smoke.py`，覆盖指定区间查询、ECharts canvas 有效像素、六项数据质量检查、无横向溢出、无控制台错误和无 HTTP 错误。
- 已增加任务复核页 Playwright 只读测试：`tests/integration/jobs_visual_smoke.py`，覆盖交易日选择、六账户复核结论、任务时间东八区格式、无横向溢出、无控制台错误和无 HTTP 错误。
- 运维页 Playwright 会动态读取 `/v1/accounts`，验证六个 gateway 和账户筛选器均使用 PostgreSQL 落库别名，不回退显示旧的“生产查询账户”配置名。
- 已增加交易终端 Playwright 交互测试：`tests/integration/trade_terminal_interaction_smoke.py`，覆盖生产环境/账户切换、只读下单护栏、日期查询、持仓和订单分页/排序、持仓联动代码、分钟 K 线 canvas 及订单详情。
- 已增加批量下单 Playwright 交互测试：`tests/integration/batch_order_interaction_smoke.py`，用浏览器路由模拟券商测试环境和拦截批量 POST，覆盖粘贴导入、编辑后校验失效、账户后四位确认、标准请求体及发布结果；真实生产页面仍验证零写请求。
- 已增加 API Console Playwright 交互测试：`tests/integration/api_console_interaction_smoke.py`，覆盖状态、账户、历史订单表单填写、请求预览、JSON/表格响应、命名集合保存/恢复、JSON 导入导出和响应断言，并在浏览器路由层中止所有 `/v1/*` 写请求。
- 已增加 API catalog 一致性检查：`scripts/check-api-catalog.py`，校验 67 个 catalog 条目、Go handler 注册模式、源码 schema 和在线 `/v1/schema`。
- 已增加生产只读发布验收入口：`scripts/check-readonly-release.py`，先强制校验 `environment=production`、`trading_enabled=0`，再组合单元测试、SDK、API 和双视口 Playwright；清单见 `docs/RELEASE_CHECKLIST.md`。
- 已增加 API/worker 跨进程事件桥集成测试：`tests/integration/event_bridge_smoke_test.go` 在显式提供生产式测试配置时直接发送 PostgreSQL 通知，验证 API SSE 能收到对应账本事件，全程不写 Redis 命令或交易账表。
- 已增加 API handler、交易 schema/状态机、订单编排/幂等、Redis command/envelope/账本同步、成交去重、自动刷新、Meridian 客户端、事件 hub 和 PostgreSQL repository 单元测试。
- 已增加 Python 交易日任务测试，覆盖非交易日跳过、账户级异常隔离和快照流程关键语义。
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
  --date-from 20260701 \
  --date-to 20260729

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

1. 将现有临时 PostgreSQL migration/repository 集成脚本接入外部 CI。
2. 增加盘后对账和绩效贡献的更多固定样例数据测试；人工复核报告已有 API 固定样例和生产只读 Playwright 验收。
3. [x] 增加 gateway 心跳、stream lag、非交易时段抑制和 DLQ 处置写保护测试；`operations_visual_smoke.py` 验证生产 6 gateway / 24 output stream 页面。
