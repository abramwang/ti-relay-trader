# Relay 发布检查清单

更新时间：`2026-08-02`

## 适用范围

本清单用于 Relay 9092 API、文档门户、交易终端和后台 worker 的生产发布。所有业务时间、任务日期和验收报告统一使用 `Asia/Shanghai`。

当前生产账户默认只读。恢复真实下单权限属于独立变更，不能通过普通代码发布顺带开启。

## 发布前

- [ ] 工作树中的变更已评审并提交，README 恢复卡片和开发路线图已同步。
- [ ] `config/relay.prod.yaml`、数据库 DSN、Redis 凭据和 Webhook Token 仍位于未跟踪配置或运行环境中。
- [ ] `/v1/status.environment=production`，`accounts.trading_enabled=0`；目标账户在 `/v1/accounts` 中启用但保持只读。
- [ ] 生产和测试 PostgreSQL 使用独立数据库，`database.expected_name` 与 DSN 目标一致。
- [ ] migration 已在临时库验证；涉及 schema 或账表变更时，先生成 custom-format 备份、SHA256 和 manifest。
- [ ] OC、Redis、PostgreSQL、Meridian 的兼容版本和变更通知已复核。
- [ ] 当日盘前/盘后任务没有运行中实例；交易日内发布需避开策略交易窗口。

## 自动验收

生产只读全量验收命令：

```bash
python3 scripts/check-readonly-release.py \
  --base-url http://127.0.0.1:9092 \
  --account-id 314000046830 \
  --trade-date 20260731 \
  --performance-date-from 20260701 \
  --performance-date-to 20260731
```

脚本会先检查生产环境和零下单账户，再执行：

- API catalog、Go handler、源码 `/v1/schema` 和在线 `/v1/schema` 一致性检查。
- Go 单元测试、Python 单元测试、Python SDK 版本/归档/SHA256/单测检查。
- 9092 页面/API 冒烟和 SDK 账户、资金、持仓、订单、成交、SSE 只读检查。
- 交易终端、API Console、绩效、任务、运维页面的 `1600x1000` 与 `1280x800` Playwright 验收。
- 交易终端和 API Console 浏览器测试在 Playwright 路由层记录并中止任何 `/v1/*` 非 GET 请求。
- 批量下单专项测试在浏览器内模拟 `environment=test`；唯一的 `POST /v1/orders/batch` 由 Playwright 路由直接拦截并返回假回执，不会到达 9092、Redis 或 PostgreSQL。

结果写入 `/tmp/relay-readonly-release-<trade_date>.json`，截图写入 `/tmp/relay-*-release-*.png`。快速检查可加 `--single-viewport`，无浏览器环境时可加 `--skip-browser`；正式发布必须运行完整模式。

## 发布与观察

- [ ] 记录发布 commit、配置版本、migration 版本、操作人和东八区发布时间。
- [ ] 使用 `scripts/relay-docs-service.sh restart` 或 `scripts/relay-worker-service.sh restart` 独立发布目标进程；确认另一个 PID 不变且 watchdog 不重复拉起。
- [ ] 独立 worker 发布时先停止旧实例，确认 checkpoint 已落库，再启动新实例；API 与 worker 不同时滚动变更 Redis wire schema。
- [ ] 检查 `scripts/relay-runtime-service.sh status`、`/healthz`、worker `/readyz`、`/v1/status`、`/v1/operations/status` 和 `/jobs`；`worker/event_bridge` 必须为 `ok`。
- [ ] 确认六账户路由、24 条 output stream checkpoint/lag、DLQ 数量和 gateway 状态符合当前交易阶段。
- [ ] 确认盘前 `09:01`、盘后 `15:01` 和 OC `15:30` 的职责窗口没有被配置覆盖。
- [ ] 观察服务日志、worker 日志、cron 日志和告警投递；凭据、订单原始报文不得进入发布报告。
- [ ] 重新运行完整只读验收，并归档 JSON 报告与异常说明。

## 回滚

1. 立即保持或恢复 `trading_enabled=false`，停止新的交易写入。
2. 仅停止需要回滚的 API 或 worker，保留 PostgreSQL `raw_stream_messages`、stream checkpoint 和任务报告，不清空 Redis Stream/Pending。
3. 可先执行 `scripts/relay-runtime-service.sh rollback-api|rollback-worker` 切换该进程上一二进制；配置或 wire schema 有变化时仍需回滚已记录的 Git commit、配套配置和 OC 兼容版本。
4. 数据库只允许恢复到新库，完成 migration、约束、关键行数和 raw archive 重放验收后再切换 DSN；禁止直接覆盖生产库。
5. 启动只读服务，执行本页完整自动验收；确认订单、成交、ETF 划转和账户日期作用域无孤立/重复后，才结束回滚。

数据库备份与临时恢复细节见 `docs/DATABASE_BACKUP_RESTORE.md`，常驻进程和配置门禁见 `docs/OPERATIONS.md`。
