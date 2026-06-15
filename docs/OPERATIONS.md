# relay 运行配置与任务管理

更新时间：`2026-06-15`

## 配置文件口径

真实数据库、Redis、账户路由等连接凭据可以放在部署机本地配置文件中，但不要提交到 Git。

推荐路径：

| 文件 | 是否提交 | 说明 |
| --- | --- | --- |
| `config/relay.example.yaml` | 是 | 配置模板，只放占位符 |
| `config/relay.test.example.yaml` | 是 | 券商测试环境模板，只放占位符 |
| `config/relay.prod.example.yaml` | 是 | 生产环境模板，只放占位符 |
| `config/relay.test.yaml` | 否 | 部署机测试配置，可包含测试凭据 |
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
4. account 到 broker/gateway/stream prefix 的多账户路由，以及账户别名默认值 `alias`。
5. 订单/成交事件驱动的资金持仓自动刷新限频参数。
6. 服务业务时区，统一为 `Asia/Shanghai`。
7. 日志级别和输出格式。
8. 后台任务开关和 cron 时间。

真实 PostgreSQL、Redis 等访问方式查阅 `http://doc.quantstage.com`。

当前实现：

1. Go 配置包位于 `internal/config`。
2. 支持 `docs`、`api`、`worker` 三种服务运行模式。
3. 支持 `service.environment=test|production` 显式标记运行环境，默认 `test`。
4. 支持从 `RELAY_CONFIG_PATH` 或 `-config` 指定的 YAML 文件读取配置。
5. 文档门户会用配置中的 `service.public_url` 和 `service.docs_addr` 覆盖默认值。
6. API 模式会使用 `service.api_addr`，并提供 `/healthz`、`/v1/status`、`/v1/accounts` 等基础接口；`/v1/status` 会返回 PostgreSQL、Redis、订单服务、行情代理、事件流和自动刷新状态摘要。
7. worker 模式会连接 PostgreSQL 和 Redis，持续消费 `reply/event/hb/dlq`，通过 `stream_checkpoints` 持久化每条 stream 的 `last_stream_id`。
8. 自动资金持仓刷新默认开启，订单/成交事件落账后会按账户合并并限频发送 `account.asset.query` 和 `account.positions.query`。
9. 默认业务时区为 `Asia/Shanghai`，配置加载会校验 `service.timezone` 是否为合法 IANA timezone。
10. 已校验服务模式、运行环境、业务时区、日志级别、日志格式、数据库连接池参数、自动刷新参数、重复账户路由、账户交易开关和 Redis Stream 前缀一致性。

## 测试/生产切换

测试环境和生产环境不要通过临时编辑同一个配置文件切换。推荐固定两份本地未跟踪配置：

```bash
cp config/relay.test.example.yaml config/relay.test.yaml
cp config/relay.prod.example.yaml config/relay.prod.yaml
chmod 600 config/relay.test.yaml config/relay.prod.yaml
```

9092 首页的“运行环境控制”会展示两类候选环境：

1. 测试环境：优先读取 `config/relay.test.yaml`，如果不存在则读取历史兼容的 `config/relay.local.yaml`。
2. 生产环境：读取 `config/relay.prod.yaml`。

首页只展示配置摘要和本机命令，不提供公网可点击切换按钮。真实切换必须登录部署机执行脚本：

切换测试环境：

```bash
scripts/switch-relay-env.sh test
```

切换生产环境：

```bash
scripts/switch-relay-env.sh production
```

生产 Redis 的 host、port、auth、db 只写入 `config/relay.prod.yaml` 或进程环境变量，不写入 README、docs、示例配置、脚本或 Git commit。当前收到的生产 Redis 凭据尚未写入仓库。

生产配置必须显式设置：

```yaml
service:
  environment: "production"

redis:
  env: "prod"

accounts:
  - enabled: true
    alias: "生产账户"
    trading_enabled: false
    simulated: false
```

上线顺序：

1. 先保持生产账户 `trading_enabled: false`，启动只读链路。
2. 执行 `relayctl redis-scan -config config/relay.prod.yaml`，发现生产 Redis 中实际存在的账户 stream 前缀。
3. 将确认后的账户写入本地未跟踪配置 `accounts[]`，继续保持 `trading_enabled: false`。
4. 执行 `relayctl redis-probe -config config/relay.prod.yaml`，确认已配置账户的只读 stream、心跳和命名空间。
5. 检查 `GET /v1/status`，确认 `environment=production`、Redis/PostgreSQL 为 `ok`，账户摘要符合预期。
6. 打开 `/trade`，确认顶部显示“生产环境”红色标识，并核对账户号、broker、gateway。
7. 手动评审 `accounts[].stream_prefix`，必须等于 `relay:<redis.env>:v1:<broker_id>:<gateway_id>`。
8. 完成只读验证后，再把需要交易的生产账户 `trading_enabled` 改为 `true` 并重启服务。切换脚本默认拒绝带生产下单权限的配置；确需开放时必须在服务器本机执行 `scripts/switch-relay-env.sh production --allow-production-trading` 并输入确认短语。
9. 首次生产写入只发小额/最小单位测试单，确认订单、成交、撤单、资金持仓刷新和账本落盘全链路正常。

配置加载会阻止以下明显危险配置：

1. 未知 `service.environment`。
2. `trading_enabled=true` 但账户 `enabled=false`。
3. 生产环境中 `trading_enabled=true` 且 `simulated=true`。
4. 账户 `stream_prefix` 与 `redis.env`、`broker_id`、`gateway_id` 不一致。

注意：券商测试环境的 Redis Stream namespace 可能仍使用 `relay:prod:*`，所以 `redis.env=prod` 不能单独表示生产。以 `service.environment`、配置文件路径、账户权限和页面环境标识共同判断当前运行态。

## SDK 与落库隔离

Python SDK 和策略程序不承载测试/生产环境选择。SDK 只连接 relay 的 `base_url`，后端实际使用测试 Redis 还是生产 Redis，由 relay 服务端配置文件、账户路由和 `trading_enabled` 决定。策略侧应读取 `/v1/status.environment` 和 `/v1/accounts` 做运行前自检，但不要在 SDK 内维护另一套环境切换逻辑。

`accounts[].alias` 是 UI 和人工识别的默认别名，不参与 Redis Stream 命名、账本唯一键、幂等键或权限判断。多账户环境中建议为每个账户设置短别名，例如“生产查询账户”“一号量化”“高频测试”等；交易终端会优先显示别名，同时保留账号用于核对。

交易终端顶部账户区域的“别名”按钮会调用 `PATCH /v1/accounts/{account_id}/alias`，把用户修改写入 PostgreSQL `accounts.account_name`。`GET /v1/accounts` 读取账户列表时会优先使用落库别名，若落库值为空则回退到配置文件里的 `accounts[].alias`。别名修改只允许写入当前服务配置中存在的账户，不会改变 broker/gateway/stream prefix、账户权限或下单开关。

当前账本 schema 中 `gateways` 和 `account_gateway_routes` 已有 `env` 维度，但 `accounts`、`orders`、`fills`、`asset_snapshots`、`positions` 等核心事实表仍主要按 `account_id` 唯一或索引。因此：

1. 短期内，如果测试账户和生产账户 ID 不同，可以通过 `account_id` 区分。
2. 长期生产化不建议只靠账户区分，推荐测试/生产使用独立 PostgreSQL DSN 或独立 schema。
3. 如果必须共用一个库，需要补 migration，把 `environment` 纳入核心账表唯一键、索引和查询条件。
4. 页面和 SDK 查询生产数据时必须显式选择账户，不能用无账户过滤的研究导出结果直接混用测试/生产。

## 时区口径

relay 的业务时间统一使用 `Asia/Shanghai`，即东八区 UTC+8。A 股交易日、盘前初始化、收盘后结算、对账批次、报表展示、页面时间和 cron 调度都按这个时区解释。

建议：

1. 配置文件显式设置 `service.timezone: "Asia/Shanghai"`。
2. cron、systemd timer 或其他调度器显式设置 `CRON_TZ=Asia/Shanghai` 或等价配置。
3. 文档和日志不要使用容易歧义的三字母时区缩写，需要写时区时使用 `Asia/Shanghai` 或 `+08:00`。
4. 数据库继续使用 `timestamptz` 保存绝对时刻，业务展示和报告按 `Asia/Shanghai` 转换。

交易日主流程见 [docs/TRADING_DAY_WORKFLOW.md](/home/ti-relay-trader/docs/TRADING_DAY_WORKFLOW.md:1)。

## 自动资金持仓刷新

9092 docs/api 常驻服务在启动轻量后台 Redis `reply/event` 同步循环时，会监听订单和成交落账结果。如果某个账户出现 `order.event` 或 `fill.event`，relay 会自动向该账户的 `cmd.query` 写入一轮资金和持仓查询命令。

默认配置：

```yaml
auto_refresh:
  enabled: true
  debounce_seconds: 2
  cooldown_seconds: 20
  timeout_seconds: 10
```

含义：

1. `debounce_seconds`：同一账户密集订单/成交事件先合并，等待该秒数后发查询。
2. `cooldown_seconds`：同一账户发出一轮资金+持仓查询后，冷却期内的新事件会继续合并到下一轮，避免高频查询柜台。
3. `timeout_seconds`：发布查询命令到 Redis 的单轮超时。
4. `enabled: false` 可关闭自动刷新，页面仍可通过手动刷新按钮发送查询。

## 运行模式

默认文档门户模式：

```bash
go run ./cmd/relay-docs -root .
```

指定配置文件：

```bash
RELAY_CONFIG_PATH=/home/ti-relay-trader/config/relay.prod.yaml go run ./cmd/relay-docs -root .
```

如需试运行 API 或 worker，将本地未提交配置里的 `service.mode` 改为 `api` 或 `worker`。API 模式提供 9092 HTTP 服务；worker 模式不监听 HTTP，负责 Redis output stream 持续同步、checkpoint 落盘和订单/成交事件驱动的资金持仓自动刷新。

worker 模式示例：

```bash
RELAY_CONFIG_PATH=/home/ti-relay-trader/config/relay.prod.yaml go run ./cmd/relay-docs
```

生产化建议将 API/docs 进程和 worker 进程拆开部署，避免 HTTP 重启影响 Redis 消费位点推进。

## 日志与响应

当前 Go 服务使用结构化日志：

1. 默认 `service.log_format=json`，可改为 `text`。
2. 默认 `service.log_level=info`，可设为 `debug`、`warn`、`error`。
3. HTTP 请求日志包含 `request_id`、method、path、status、bytes、duration_ms、remote_addr。
4. API 模式统一返回 JSON envelope，包含 `ok`、`data` 或 `error`、`request_id`、`time`。

## 健康检查口径

1. `GET /healthz` 只用于进程存活探测，不做数据库或 Redis ping。
2. `GET /v1/status` 用于服务状态和依赖健康检查，包含账户摘要、PostgreSQL、Redis、订单服务、Meridian 行情代理、SSE 事件流和自动刷新状态。
3. PostgreSQL 和 Redis 已配置时会执行短超时 ping；未配置时返回 `not_configured`，已配置但不可用时返回 `error`、`timeout` 或 `unavailable`。
4. 健康检查响应不返回 DSN、密码、Token、Redis URL 或底层错误原文，只返回通用摘要，避免把本地配置泄露到页面和日志里。

## 接口测试台

文档门户提供 Apifox 风格接口测试台：

```text
http://relay-trader.quantstage.com/api-console
```

当前测试台用于 API 联调和只读/写入接口验证。交易写接口已经接入测试账户链路；只有在启动配置包含 PostgreSQL、Redis、账户路由且账户 `enabled=true`、`trading_enabled=true` 时才可成功发布 Redis 命令。纯文档配置下，交易和账本接口会返回明确的服务不可用或空结果。

## PostgreSQL Migration

当前交易账本和位点 DDL 位于：

```text
migrations/postgres/000001_init_ledger.up.sql
migrations/postgres/000002_stream_checkpoints.up.sql
migrations/postgres/000003_job_runs.up.sql
migrations/postgres/000004_reconciliation_idempotency.up.sql
migrations/postgres/000005_fill_id_order_scope.up.sql
migrations/postgres/000006_research_performance_views.up.sql
```

真实 DSN 仍放在部署机本地配置或安全渠道。当前测试 PostgreSQL 已应用 `000001` 到 `000006`，包含账本、stream checkpoint、任务运行、对账幂等、成交订单作用域去重和研究导出 view。

当前环境已安装 `psql`，同时可使用内置 runner：

```bash
RELAY_DATABASE_URL=postgres://... go run ./cmd/relayctl migrate status
RELAY_DATABASE_URL=postgres://... go run ./cmd/relayctl migrate up
```

如果使用配置文件：

```bash
go run ./cmd/relayctl migrate up -config config/relay.local.yaml
```

## 前置测试环境

当前用户已启动前置程序测试环境，relay 已基于测试 Redis 跑通查询、下单、批量下单、撤单、reply/event 合并和 SSE 推送。继续联调时优先使用以下入口：

1. `relayctl redis-scan` 发现账户前缀，`relayctl redis-probe` 只读探测 `reply`、`event`、`hb`、`dlq` stream。
2. `/api-console` 或 SDK 发送资金、持仓、订单、成交刷新命令。
3. `/trade` 或 SDK 做小流量下单、批量下单、撤单。
4. `relayctl ledger-sync` 或 worker 回放/追赶指定 stream。
5. `/v1/status`、`/jobs` 和 `/trade` 检查依赖、任务和账本状态。

联调必须继续遵守凭据管理约定：Redis 密码、账号密码、柜台地址等只放本地未提交配置或安全渠道。

只读探测入口见 [docs/REDIS_STREAM_PROBE.md](/home/ti-relay-trader/docs/REDIS_STREAM_PROBE.md:1)。

## Cron 任务管理

后台批处理可以优先采用 cron 管理，适合以下任务：

1. 盘前初始化。
2. 收盘后结算。
3. 盘后对账。
4. 资产快照。
5. 持仓快照。
6. 账户盈亏统计。
7. 历史数据补拉。
8. 对账报告生成。

低延迟交易主链路、Redis Stream 实时消费和 9092 在线 API 不建议由 cron 触发，应使用常驻服务进程。

## Cron 示例

基础 Python jobs 已实现。正式启用前先在部署机手动执行一次，确认 `PYTHONPATH`、9092 地址、账户范围、日志目录和输出目录都正确：

```cron
SHELL=/bin/bash
CRON_TZ=Asia/Shanghai
TZ=Asia/Shanghai
RELAY_HOME=/home/ti-relay-trader
RELAY_CONFIG_PATH=/home/ti-relay-trader/config/relay.prod.yaml
PYTHONPATH=/home/ti-relay-trader/src:/home/ti-relay-trader/sdk/python
RELAY_BASE_URL=http://relay-trader.quantstage.com

# A 股交易日盘前初始化，08:55 Asia/Shanghai。太早券商柜台和交易所链路可能尚未完成初始化。
55 8 * * 1-5 root cd $RELAY_HOME && flock -n /tmp/relay-pre-open-init.lock python3 -m relay.jobs.pre_open_init --persist --trigger cron --output /var/log/relay/reports/pre_open_init.json >> /var/log/relay/pre_open_init.log 2>&1

# A 股生产环境交易日收盘后结算，15:30 Asia/Shanghai。
30 15 * * 1-5 root cd $RELAY_HOME && flock -n /tmp/relay-post-close-settlement.lock python3 -m relay.jobs.post_close_settlement --persist --trigger cron --output /var/log/relay/reports/post_close_settlement.json >> /var/log/relay/post_close_settlement.log 2>&1

# 研究侧绩效导出当前通过 9092 API / PostgreSQL view 查询，不需要单独 cron。
```

注意：

1. 使用 `flock -n` 防止同一任务重复运行。
2. cron 日志写入 `/var/log/relay/`，并配置 logrotate。
3. cron 环境变量少，必须显式设置 `RELAY_CONFIG_PATH`。
4. 当前 Python jobs 通过 `PYTHONPATH=$RELAY_HOME/src:$RELAY_HOME/sdk/python` 复用仓库内源码和 SDK。
5. cron 时区必须和 `service.timezone` 保持一致，当前固定为 `Asia/Shanghai`。
6. 首次部署前先手动执行任务命令，确认配置、权限和日志目录无误。

手动 dry-run 示例：

```bash
cd /home/ti-relay-trader
PYTHONPATH=src:sdk/python python3 -m relay.jobs.pre_open_init \
  --base-url http://relay-trader.quantstage.com \
  --dry-run \
  --output outputs/jobs/pre_open_init.dry-run.json

PYTHONPATH=src:sdk/python python3 -m relay.jobs.post_close_settlement \
  --base-url http://relay-trader.quantstage.com \
  --dry-run \
  --output outputs/jobs/post_close_settlement.dry-run.json
```

当前 `pre_open_init` 与 `post_close_settlement` 会输出 JSON 报告，包含交易日、依赖状态、账户范围、刷新命令回执、资金/持仓/订单/成交快照摘要和未终态订单列表。默认会调用 Meridian 交易日接口；如果目标日期不是交易日且未传 `--allow-non-trading-day`，任务会跳过账户刷新并以 `ok=true, skipped=true` 结束。

任务报告需要进入 9092 状态面板时，使用 `--persist`。该参数会调用 `POST /v1/jobs/runs` 写入 PostgreSQL `job_runs`，`/v1/status` 展示最近盘前/盘后任务摘要，`/jobs` 提供页面化任务监控。

## 待增强项

当前仍需补齐：

1. 正式部署脚本、systemd unit 或 `/etc/cron.d/relay-trader` 模板。
2. cron 安装后验收 `/v1/status` 和 `/jobs` 中的日流程最近运行状态。
3. worker 心跳状态合并、DLQ 告警和处置页面。
4. 更完整的人工复核报告导出。
