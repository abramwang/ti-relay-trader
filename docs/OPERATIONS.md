# relay 运行配置与任务管理

更新时间：`2026-06-13`

## 配置文件口径

真实数据库、Redis、账户路由等连接凭据可以放在部署机本地配置文件中，但不要提交到 Git。

推荐路径：

| 文件 | 是否提交 | 说明 |
| --- | --- | --- |
| `config/relay.example.yaml` | 是 | 配置模板，只放占位符 |
| `config/relay.local.yaml` | 否 | 本地开发配置，可包含本地凭据 |
| `config/relay.prod.yaml` | 否 | 部署机生产配置，可包含真实凭据 |

仓库通过 `.gitignore` 忽略 `config/*.yaml` 和 `config/*.yml`，只允许提交 `*.example.yaml` 或 `*.example.yml`。

建议部署时使用：

```bash
export RELAY_CONFIG_PATH=/home/ti-relay-trader/config/relay.prod.yaml
```

配置文件权限建议：

```bash
chmod 600 /home/ti-relay-trader/config/relay.prod.yaml
```

## 配置内容

配置文件建议覆盖：

1. 9092 服务地址和最终服务口径。
2. PostgreSQL 连接 DSN、连接池参数。
3. Redis URL、env、broker、gateway。
4. account 到 broker/gateway/stream prefix 的多账户路由。
5. 日志级别和输出格式。
6. 后台任务开关和 cron 时间。

真实 PostgreSQL、Redis 等访问方式查阅 `http://doc.quantstage.com`。

当前实现：

1. Go 配置包位于 `internal/config`。
2. 支持 `docs`、`api`、`worker` 三种服务运行模式。
3. 支持从 `RELAY_CONFIG_PATH` 或 `-config` 指定的 YAML 文件读取配置。
4. 文档门户会用配置中的 `service.public_url` 和 `service.docs_addr` 覆盖默认值。
5. API 模式会使用 `service.api_addr`，并提供 `/healthz`、`/v1/status`、`/v1/accounts` 骨架接口。
6. worker 模式当前只记录配置态账户和任务数量，后续承接 Redis 消费与后台常驻任务。
7. 已校验服务模式、日志级别、日志格式、数据库连接池参数和重复账户路由。

## 运行模式

默认文档门户模式：

```bash
go run ./cmd/relay-docs -root .
```

指定配置文件：

```bash
RELAY_CONFIG_PATH=/home/ti-relay-trader/config/relay.prod.yaml go run ./cmd/relay-docs -root .
```

如需试运行 API 或 worker，将本地未提交配置里的 `service.mode` 改为 `api` 或 `worker`。当前 API 模式只暴露工程骨架，不连接 Redis、数据库或实盘柜台。

## 日志与响应

当前 Go 服务使用结构化日志：

1. 默认 `service.log_format=json`，可改为 `text`。
2. 默认 `service.log_level=info`，可设为 `debug`、`warn`、`error`。
3. HTTP 请求日志包含 `request_id`、method、path、status、bytes、duration_ms、remote_addr。
4. API 模式统一返回 JSON envelope，包含 `ok`、`data` 或 `error`、`request_id`、`time`。

## Cron 任务管理

后台批处理可以优先采用 cron 管理，适合以下任务：

1. 盘后对账。
2. 资产快照。
3. 持仓快照。
4. 账户盈亏统计。
5. 历史数据补拉。
6. 对账报告生成。

低延迟交易主链路、Redis Stream 实时消费和 9092 在线 API 不建议由 cron 触发，应使用常驻服务进程。

## Cron 示例

以下示例用于规划，等 Python jobs 实现后再启用：

```cron
SHELL=/bin/bash
RELAY_HOME=/home/ti-relay-trader
RELAY_CONFIG_PATH=/home/ti-relay-trader/config/relay.prod.yaml

# A 股交易日盘后资产快照，15:45 本地时间。
45 15 * * 1-5 root cd $RELAY_HOME && flock -n /tmp/relay-asset-snapshot.lock python3 -m relay.jobs.asset_snapshot >> /var/log/relay/asset_snapshot.log 2>&1

# 盘后对账，16:30 本地时间。
30 16 * * 1-5 root cd $RELAY_HOME && flock -n /tmp/relay-reconcile.lock python3 -m relay.jobs.reconcile >> /var/log/relay/reconcile.log 2>&1

# 账户盈亏统计，17:10 本地时间。
10 17 * * 1-5 root cd $RELAY_HOME && flock -n /tmp/relay-pnl.lock python3 -m relay.jobs.pnl >> /var/log/relay/pnl.log 2>&1
```

注意：

1. 使用 `flock -n` 防止同一任务重复运行。
2. cron 日志写入 `/var/log/relay/`，并配置 logrotate。
3. cron 环境变量少，必须显式设置 `RELAY_CONFIG_PATH`。
4. 首次部署前先手动执行任务命令，确认配置、权限和日志目录无误。

## 后续实现

后续需要补齐：

1. 敏感字段脱敏日志。
2. Python jobs 入口。
3. cron 安装脚本或 `/etc/cron.d/relay-trader` 模板。
4. 任务运行状态、最近成功时间和失败原因落盘。
