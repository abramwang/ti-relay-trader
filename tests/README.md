# 测试目录索引

当前测试覆盖 Go 核心模块、Python SDK、交易日任务、9092 页面/API 轻量冒烟和绩效页 Playwright 交互；临时 PostgreSQL CI 仍待补齐。

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
- 已增加 SDK 只读 live smoke：`tests/integration/sdk_live_smoke.py`，可对运行中的 9092 验证状态、账户、资金、持仓、订单、成交和 SSE 首事件。
- 已增加 SDK 发布检查脚本：`scripts/check-python-sdk-release.py`，覆盖版本一致性、tar.gz 内容、sha256、SDK 单元测试和可选 live smoke。
- 已增加 9092 页面轻量冒烟测试：`tests/integration/page_smoke.py`，覆盖首页、文档、测试索引、API Console、交易终端、静态资源、基础 API 和 SDK 下载入口。
- 已增加绩效页 Playwright 只读测试：`tests/integration/performance_visual_smoke.py`，覆盖指定区间查询、ECharts canvas 有效像素、六项数据质量检查、无横向溢出、无控制台错误和无 HTTP 错误。
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
```

脚本默认禁用系统 HTTP 代理，避免本机 `127.0.0.1:9092` 被代理环境误转发；也可以通过 `--base-url http://relay-trader.quantstage.com` 对最终服务口径做同样检查。

## 后续计划

1. 扩展 Playwright 到账户切换、接口异常和写权限护栏场景。
2. 增加 API Console 响应断言和可保存的冒烟测试集合。
3. 在 CI 中使用临时 PostgreSQL 执行 migration 和 repository 集成测试。
4. 增加盘后对账、绩效贡献和人工复核报告的固定样例数据测试。
5. [x] 增加 gateway 心跳、stream lag、非交易时段抑制和 DLQ 处置写保护测试；`operations_visual_smoke.py` 验证生产 6 gateway / 24 output stream 页面。
